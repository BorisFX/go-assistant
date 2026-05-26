package chat

import (
	"context"
	"fmt"
	"log/slog"

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
	systemPrompt string
}

func NewService(
	pipeline *Pipeline,
	messageRepo output.MessageRepository,
	activityRepo output.ActivityRepository,
	memorySvc *memory.Service,
	systemPrompt string,
) *Service {
	return &Service{
		systemPrompt: systemPrompt,
		pipeline:     pipeline,
		messageRepo:  messageRepo,
		activityRepo: activityRepo,
		memorySvc:    memorySvc,
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
