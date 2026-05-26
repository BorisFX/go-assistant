package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/olegmatyakubov/go-assistant/internal/domain/valueobject"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
)

type ChatMessageRequest struct {
	Message string `json:"message"`
}

func HandleChat(chatService input.ChatService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		if req.Message == "" {
			writeError(w, http.StatusBadRequest, "message is required")
			return
		}

		sessionKey := valueobject.NewSessionKey("dashboard", "web")

		resp, err := chatService.ProcessMessage(r.Context(), input.ChatRequest{
			SessionKey: sessionKey,
			Content:    req.Message,
		})

		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, resp)
	}
}
