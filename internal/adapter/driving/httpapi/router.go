package httpapi

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type RouterDeps struct {
	APIKey       string
	Mode         string
	ChatService  input.ChatService
	MessageRepo  output.MessageRepository
	ActivityRepo output.ActivityRepository
	ToolRegistry output.ToolRegistry
	DashboardFS  fs.FS
	MemoryRepo   output.MemoryRepository
	MemorySvc    *memory.Service
}

func NewRouter(deps RouterDeps) http.Handler {
	r := chi.NewRouter()

	r.Use(chiMiddleware.Logger)
	r.Use(chiMiddleware.Recoverer)
	r.Use(chiMiddleware.RealIP)

	r.Get("/api/health", HandleHealth(HealthDeps{
		Mode:      deps.Mode,
		ToolCount: len(deps.ToolRegistry.ListTools()),
	}))

	r.Group(func(r chi.Router) {
		r.Use(APIKeyAuth(deps.APIKey))

		r.Get("/api/conversations", HandleListConversations(deps.MessageRepo))
		r.Get("/api/conversations/{id}/messages", HandleListMessages(deps.MessageRepo))
		r.Post("/api/chat", HandleChat(deps.ChatService))
		r.Get("/api/activity", HandleListActivity(deps.ActivityRepo))
		r.Get("/api/activity/stats", HandleActivityStats(deps.ActivityRepo))

		// Memory
		if deps.MemoryRepo != nil {
			r.Get("/api/memory", HandleListMemories(deps.MemoryRepo))
			r.Post("/api/memory", HandleCreateMemory(deps.MemorySvc))
			r.Delete("/api/memory/{id}", HandleDeleteMemory(deps.MemoryRepo))
		}
	})

	if deps.DashboardFS != nil {
		r.NotFound(SPAHandler(deps.DashboardFS))
	}

	return r
}
