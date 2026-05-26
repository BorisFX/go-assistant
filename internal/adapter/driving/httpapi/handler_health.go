package httpapi

import (
	"encoding/json"
	"net/http"
	"time"
)

type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Mode      string `json:"mode"`
	ToolCount int    `json:"tool_count"`
}

type HealthDeps struct {
	Mode      string
	ToolCount int
}

func HandleHealth(deps HealthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Mode:      deps.Mode,
			ToolCount: deps.ToolCount,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
