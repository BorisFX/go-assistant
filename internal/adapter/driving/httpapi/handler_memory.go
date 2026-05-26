package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func HandleListMemories(repo output.MemoryRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit <= 0 {
			limit = 50
		}

		memories, err := repo.List(r.Context(), limit, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, memories)
	}
}

type CreateMemoryRequest struct {
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

func HandleCreateMemory(svc *memory.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateMemoryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		if req.Content == "" {
			writeError(w, http.StatusBadRequest, "content is required")
			return
		}

		if err := svc.StoreFact(r.Context(), req.Content, "dashboard", req.Tags); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]string{"status": "created"})
	}
}

func HandleDeleteMemory(repo output.MemoryRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ID")
			return
		}

		if err := repo.Delete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, map[string]string{"status": "deleted"})
	}
}
