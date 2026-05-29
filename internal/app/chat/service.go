package chat

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Service struct {
	pipeline     *Pipeline
	messageRepo  output.MessageRepository
	activityRepo output.ActivityRepository
	memorySvc    *memory.Service
	extractor    *memory.Extractor
	systemPrompt string

	mu        sync.Mutex
	msgCounts map[uuid.UUID]int
}

func NewService(
	pipeline *Pipeline,
	messageRepo output.MessageRepository,
	activityRepo output.ActivityRepository,
	memorySvc *memory.Service,
	extractor *memory.Extractor,
	systemPrompt string,
) *Service {
	return &Service{
		systemPrompt: systemPrompt,
		pipeline:     pipeline,
		messageRepo:  messageRepo,
		activityRepo: activityRepo,
		memorySvc:    memorySvc,
		extractor:    extractor,
		msgCounts:    make(map[uuid.UUID]int),
	}
}

func (s *Service) ProcessMessage(ctx context.Context, req input.ChatRequest) (*input.ChatResponse, error) {
	sessionID := req.SessionKey.String()

	conv, err := s.messageRepo.GetOrCreateConversation(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	userMsg := entity.NewMessage(conv.ID, entity.RoleUser, req.Content)
	if err := s.messageRepo.SaveMessage(ctx, userMsg); err != nil {
		slog.Error("failed to save user message", "error", err)
	}

	history, err := s.messageRepo.ListMessages(ctx, conv.ID, 30)
	if err != nil {
		slog.Error("failed to load history", "error", err)
		history = nil
	}

	llmMessages := make([]output.LLMMessage, 0, len(history)+1)
	llmMessages = append(llmMessages, output.LLMMessage{
		Role:    entity.RoleSystem,
		Content: s.systemPrompt,
	})

	for _, msg := range history {
		llmMessages = append(llmMessages, output.LLMMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Add memory context
	if s.memorySvc != nil {
		memCtx, err := s.memorySvc.BuildContext(ctx, req.Content)
		if err != nil {
			slog.Warn("failed to build memory context", "error", err)
		} else if memCtx != "" {
			// Insert memory context as second system message
			memMsg := output.LLMMessage{
				Role:    entity.RoleSystem,
				Content: "Memory context:\n" + memCtx,
			}
			// Insert after the first system message
			llmMessages = append(llmMessages[:1], append([]output.LLMMessage{memMsg}, llmMessages[1:]...)...)
		}
	}

	// If images present, attach to last user message and use vision model
	if len(req.Images) > 0 {
		last := &llmMessages[len(llmMessages)-1]
		last.Images = req.Images
	}

	resp, err := s.pipeline.Process(ctx, llmMessages, req.OnProgress)
	if err != nil {
		return nil, fmt.Errorf("pipeline: %w", err)
	}

	assistantMsg := entity.NewMessage(conv.ID, entity.RoleAssistant, resp.Content)
	if err := s.messageRepo.SaveMessage(ctx, assistantMsg); err != nil {
		slog.Error("failed to save assistant message", "error", err)
	}

	if s.extractor != nil {
		s.mu.Lock()
		s.msgCounts[conv.ID] += 2 // user + assistant
		trigger := s.msgCounts[conv.ID] >= s.extractor.Interval()
		if trigger {
			s.msgCounts[conv.ID] = 0
		}
		s.mu.Unlock()

		if trigger {
			go func(id uuid.UUID) {
				bg, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if err := s.extractor.Extract(bg, id); err != nil {
					slog.Warn("realtime fact extraction failed", "error", err)
				}
			}(conv.ID)
		}
	}

	if s.activityRepo != nil {
		activity := entity.NewActivity(entity.ActivityLLMCall, resp.Model, sessionID, conv.ID)
		activity.InputTokens = resp.InputTokens
		activity.OutputTokens = resp.OutputTokens
		if err := s.activityRepo.Save(ctx, activity); err != nil {
			slog.Error("failed to save activity", "error", err)
		}
	}

	return &input.ChatResponse{
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	}, nil
}
