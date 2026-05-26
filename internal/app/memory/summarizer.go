package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type Summarizer struct {
	memorySvc   *Service
	messageRepo output.MessageRepository
	llm         output.LLMProvider
	interval    time.Duration
}

func NewSummarizer(
	memorySvc *Service,
	messageRepo output.MessageRepository,
	llm output.LLMProvider,
	interval time.Duration,
) *Summarizer {
	return &Summarizer{
		memorySvc:   memorySvc,
		messageRepo: messageRepo,
		llm:         llm,
		interval:    interval,
	}
}

func (s *Summarizer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			if now.Hour() == 23 && now.Minute() >= 55 {
				if err := s.Summarize(ctx); err != nil {
					slog.Error("daily summarization failed", "error", err)
				}
			}
		}
	}
}

func (s *Summarizer) Summarize(ctx context.Context) error {
	slog.Info("starting daily summarization")

	convs, err := s.messageRepo.ListConversations(ctx, 50, 0)
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}

	today := time.Now().Truncate(24 * time.Hour)
	var todayMessages []string

	for _, conv := range convs {
		if conv.UpdatedAt.Before(today) {
			continue
		}

		messages, err := s.messageRepo.ListMessages(ctx, conv.ID, 100)
		if err != nil {
			slog.Warn("failed to list messages", "conv_id", conv.ID, "error", err)
			continue
		}

		for _, msg := range messages {
			if msg.CreatedAt.Before(today) {
				continue
			}
			if msg.Role == entity.RoleUser || msg.Role == entity.RoleAssistant {
				todayMessages = append(todayMessages, fmt.Sprintf("[%s] %s", msg.Role, msg.Content))
			}
		}
	}

	if len(todayMessages) == 0 {
		slog.Info("no messages to summarize today")
		return nil
	}

	prompt := fmt.Sprintf(`Summarize today's conversations in 2-3 sentences. Then extract key facts about the user as a bullet list.

Conversations:
%s

Respond in this format:
SUMMARY: <2-3 sentence summary>
FACTS:
- <fact 1>
- <fact 2>`, strings.Join(todayMessages, "\n"))

	resp, err := s.llm.Chat(ctx, output.LLMRequest{
		Messages: []output.LLMMessage{
			{Role: entity.RoleUser, Content: prompt},
		},
		MaxTokens:   500,
		Temperature: 0.3,
	})
	if err != nil {
		return fmt.Errorf("llm summarize: %w", err)
	}

	parts := strings.SplitN(resp.Content, "FACTS:", 2)

	summaryText := strings.TrimPrefix(parts[0], "SUMMARY:")
	summaryText = strings.TrimSpace(summaryText)

	if summaryText != "" {
		if err := s.memorySvc.StoreSummary(ctx, summaryText, "summarizer", []string{"daily"}); err != nil {
			slog.Error("failed to store summary", "error", err)
		}
	}

	if len(parts) > 1 {
		factsText := strings.TrimSpace(parts[1])
		for _, line := range strings.Split(factsText, "\n") {
			fact := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			if fact != "" {
				if err := s.memorySvc.StoreFact(ctx, fact, "summarizer", []string{"extracted"}); err != nil {
					slog.Error("failed to store fact", "error", err)
				}
			}
		}
	}

	slog.Info("daily summarization complete", "messages_processed", len(todayMessages))
	return nil
}
