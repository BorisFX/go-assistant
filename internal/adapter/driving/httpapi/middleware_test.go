package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driving/httpapi"
)

func TestAPIKeyAuth_ValidKey(t *testing.T) {
	handler := httpapi.APIKeyAuth("test-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAPIKeyAuth_InvalidKey(t *testing.T) {
	handler := httpapi.APIKeyAuth("test-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAPIKeyAuth_MissingKey(t *testing.T) {
	handler := httpapi.APIKeyAuth("test-key")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}
