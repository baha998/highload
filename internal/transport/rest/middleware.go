package rest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"example.com/productcatalog/internal/domain"
	"example.com/productcatalog/internal/idempotency"
)

type response struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newResponse() *response            { return &response{header: make(http.Header)} }
func (r *response) Header() http.Header { return r.header }
func (r *response) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *response) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p)
}

func (r *response) replay(w http.ResponseWriter, logger *slog.Logger) {
	for key, values := range r.header {
		w.Header()[key] = append([]string(nil), values...)
	}
	code := r.status
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
	if _, err := w.Write(r.body.Bytes()); err != nil {
		logger.Error("replay response", "error", err)
	}
}

// Timeout isolates the worker's buffer so it cannot write to a completed HTTP response.
// Context cancellation is cooperative; it does not kill the worker goroutine.
func Timeout(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 80*time.Millisecond)
		defer cancel()
		done := make(chan *response, 1)
		go func() {
			result := newResponse()
			next.ServeHTTP(result, r.WithContext(ctx))
			done <- result
		}()
		select {
		case <-ctx.Done():
			writeError(w, ctx.Err(), logger)
		case result := <-done:
			if err := ctx.Err(); err != nil {
				writeError(w, err, logger)
				return
			}
			result.replay(w, logger)
		}
	})
}

func Idempotency(next http.Handler, ttl time.Duration, capacity int, logger *slog.Logger) http.Handler {
	cache := idempotency.New[*response](ttl, capacity)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/products" {
			next.ServeHTTP(w, r)
			return
		}
		keys := r.Header.Values("X-Idempotency-Key")
		if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 128 {
			writeError(w, domain.NewError(domain.Invalid, "one X-Idempotency-Key of 1..128 bytes is required"), logger)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			writeError(w, domain.NewError(domain.Invalid, "request body exceeds limit or cannot be read"), logger)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		result, err := cache.Do(r.Context(), keys[0], sha256.Sum256(body), func() (*response, error) {
			captured := newResponse()
			next.ServeHTTP(captured, r)
			if captured.status >= 400 {
				return captured, fmt.Errorf("HTTP request failed: %d", captured.status)
			}
			return captured, nil
		})
		if result != nil {
			result.replay(w, logger)
			return
		}
		if err != nil {
			writeError(w, err, logger)
		}
	})
}

func NewServerHandler(service Service, logger *slog.Logger) http.Handler {
	return Timeout(Idempotency(NewHandler(service, logger).Routes(), 10*time.Minute, 10000, logger), logger)
}
