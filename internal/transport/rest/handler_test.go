package rest_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"example.com/productcatalog/internal/domain"
	"example.com/productcatalog/internal/repository"
	"example.com/productcatalog/internal/service"
	"example.com/productcatalog/internal/transport/rest"
)

func setup() http.Handler {
	return rest.NewServerHandler(service.New(repository.NewMemory()), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func request(handler http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if key != "" {
		r.Header.Set("X-Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestCreateIdempotencyConcurrent(t *testing.T) {
	handler := setup()
	body := `{"sku":"new-sku","title":"New product","price":100,"category_id":"cat-01","brand_id":"brand-01","in_stock":1}`
	const count = 24
	responses := make(chan *httptest.ResponseRecorder, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); responses <- request(handler, "POST", "/v1/products", "same-key", body) }()
	}
	wg.Wait()
	close(responses)
	var first string
	for response := range responses {
		if response.Code != http.StatusCreated {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
		if first == "" {
			first = response.Body.String()
		}
		if response.Body.String() != first {
			t.Fatal("duplicate request produced another response")
		}
		var product domain.Product
		if err := json.Unmarshal(response.Body.Bytes(), &product); err != nil {
			t.Fatal(err)
		}
		if response.Header().Get("Location") != "/v1/products/"+product.ID {
			t.Fatal("Location was not replayed")
		}
	}
	conflict := request(handler, "POST", "/v1/products", "same-key", `{"sku":"different"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", conflict.Code)
	}
	// A unique SKU constraint alone cannot pass the test: each replay must return 201 and the same UUID.
	page := request(handler, "GET", "/v1/categories/cat-01/products?limit=100", "", "")
	var result domain.Page
	if err := json.Unmarshal(page.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Products) != 21 {
		t.Fatalf("want exactly one new product, got %d", len(result.Products)-20)
	}
}

func TestRESTValidationAndVersion(t *testing.T) {
	handler := setup()
	tests := []struct {
		name, method, path, key, body string
		code                          int
	}{
		{"get", "GET", "/v1/products/product-01", "", "", 200},
		{"missing", "GET", "/v1/products/missing", "", "", 404},
		{"missing key", "POST", "/v1/products", "", `{}`, 400},
		{"unknown field", "PATCH", "/v1/products/product-01", "", `{"version":1,"unknown":1}`, 400},
		{"trailing JSON", "PATCH", "/v1/products/product-01", "", `{"version":1,"price":1}{}`, 400},
		{"bad limit", "GET", "/v1/categories/cat-01/products?limit=-1", "", "", 400},
		{"zero update", "PATCH", "/v1/products/product-01", "", `{"version":1,"price":0,"in_stock":0}`, 200},
		{"stale update", "PATCH", "/v1/products/product-01", "", `{"version":1,"price":123}`, 409},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := request(handler, tc.method, tc.path, tc.key, tc.body)
			if w.Code != tc.code {
				t.Fatalf("want %d, got %d: %s", tc.code, w.Code, w.Body.String())
			}
		})
	}
	w := request(handler, "GET", "/v1/products/product-01", "", "")
	var p domain.Product
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Price != 0 || p.InStock != 0 || p.Version != 2 {
		t.Fatalf("partial update failed: %+v", p)
	}
}

func TestTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	finished := make(chan struct{})
	handler := rest.Timeout(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprint(w, "late response"); err != nil {
			t.Errorf("write buffer: %v", err)
		}
	}), logger)
	w := request(handler, "GET", "/", "", "")
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("want 504, got %d", w.Code)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker did not observe cancellation")
	}
}
