package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

func APIKeyAuth(apiKey string) func(http.Handler) http.Handler {
	expected := []byte(apiKey)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(expected) == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "dashboard api_key is not configured"})
				return
			}

			key := r.Header.Get("X-API-Key")
			if subtle.ConstantTimeCompare([]byte(key), expected) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
