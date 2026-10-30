// Package rest adapts HTTP/JSON to the catalog service.
package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"example.com/productcatalog/internal/domain"
)

const maxBodyBytes = 1 << 20

type Service interface {
	Get(context.Context, string) (domain.Product, error)
	List(context.Context, string, string, int) (domain.Page, error)
	Create(context.Context, domain.CreateProduct) (domain.Product, error)
	Update(context.Context, string, domain.UpdateProduct) (domain.Product, error)
}

type Handler struct {
	service Service
	logger  *slog.Logger
}

func NewHandler(service Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/products/{id}", h.get)
	mux.HandleFunc("GET /v1/categories/{id}/products", h.list)
	mux.HandleFunc("POST /v1/products", h.create)
	mux.HandleFunc("PATCH /v1/products/{id}", h.update)
	return mux
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err, h.logger)
		return
	}
	writeJSON(w, http.StatusOK, p, h.logger)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeError(w, domain.NewError(domain.Invalid, "invalid limit"), h.logger)
			return
		}
		limit = parsed
	}
	page, err := h.service.List(r.Context(), r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeError(w, err, h.logger)
		return
	}
	writeJSON(w, http.StatusOK, page, h.logger)
}

func decode(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return domain.NewError(domain.Invalid, "invalid JSON: "+err.Error())
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return domain.NewError(domain.Invalid, "expected a single JSON object")
	}
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateProduct
	if err := decode(w, r, &req); err != nil {
		writeError(w, err, h.logger)
		return
	}
	p, err := h.service.Create(r.Context(), req)
	if err != nil {
		writeError(w, err, h.logger)
		return
	}
	w.Header().Set("Location", "/v1/products/"+p.ID)
	writeJSON(w, http.StatusCreated, p, h.logger)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var req domain.UpdateProduct
	if err := decode(w, r, &req); err != nil {
		writeError(w, err, h.logger)
		return
	}
	p, err := h.service.Update(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeError(w, err, h.logger)
		return
	}
	writeJSON(w, http.StatusOK, p, h.logger)
}

func writeJSON(w http.ResponseWriter, code int, value any, logger *slog.Logger) {
	data, err := json.Marshal(value)
	if err != nil {
		logger.Error("encode response", "error", err)
		code, data = http.StatusInternalServerError, []byte(`{"error":"internal server error"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if _, err := w.Write(append(data, '\n')); err != nil {
		logger.Error("write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, err error, logger *slog.Logger) {
	code, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, message = http.StatusGatewayTimeout, "deadline exceeded"
	case errors.Is(err, context.Canceled):
		code, message = http.StatusRequestTimeout, "request canceled"
	default:
		switch domain.KindOf(err) {
		case domain.Invalid:
			code, message = http.StatusBadRequest, err.Error()
		case domain.NotFound:
			code, message = http.StatusNotFound, err.Error()
		case domain.Conflict:
			code, message = http.StatusConflict, err.Error()
		case domain.Overloaded:
			code, message = http.StatusServiceUnavailable, err.Error()
		}
	}
	if code == http.StatusInternalServerError {
		logger.Error("request failed", "error", err)
	}
	writeJSON(w, code, map[string]string{"error": message}, logger)
}
