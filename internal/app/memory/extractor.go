package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

// Extractor pulls durable user facts from recent messages in the background.
type Extractor struct {
	svc      *Service
	msgRepo  output.MessageRepository
	llm      output.LLMProvider
	model    string
	interval int
}

func NewExtractor(svc *Service, msgRepo output.MessageRepository, llm output.LLMProvider, model string, interval int) *Extractor {
	if interval <= 0 {
		interval = 6
	}
	return &Extractor{svc: svc, msgRepo: msgRepo, llm: llm, model: model, interval: interval}
}

// Interval is the number of messages between extraction runs.
func (e *Extractor) Interval() int { return e.interval }

func (e *Extractor) Extract(ctx context.Context, convID uuid.UUID) error {
	messages, err := e.msgRepo.ListMessages(ctx, convID, e.interval)
	if err != nil {
		return fmt.Errorf("list messages: %w", err)
	}

	// ListMessages returns newest-first; iterate in reverse for chronological order.
	var transcript []string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == entity.RoleUser || msg.Role == entity.RoleAssistant {
			transcript = append(transcript, fmt.Sprintf("[%s] %s", msg.Role, msg.Content))
		}
	}
	if len(transcript) == 0 {
		return nil
	}

	prompt := fmt.Sprintf(`From the conversation below, extract durable facts about the user (preferences, identity, projects, decisions). Ignore one-off chatter. If there is nothing worth remembering, reply with the single word NONE.

Conversation:
%s

Reply with a bullet list of facts, or NONE.`, strings.Join(transcript, "\n"))

	resp, err := e.llm.Chat(ctx, output.LLMRequest{
		Messages:    []output.LLMMessage{{Role: entity.RoleUser, Content: prompt}},
		MaxTokens:   300,
		Temperature: 0.2,
		Model:       e.model,
	})
	if err != nil {
		return fmt.Errorf("llm extract: %w", err)
	}

	facts := parseFacts(resp.Content)
	for _, f := range facts {
		if err := e.svc.StoreFact(ctx, f, "realtime", []string{"extracted", "realtime"}); err != nil {
			slog.Warn("failed to store extracted fact", "error", err)
		}
	}
	slog.Info("realtime extraction complete", "conv_id", convID, "facts", len(facts))
	return nil
}

// parseFacts turns a bullet list into trimmed fact strings. "NONE" yields nothing.
func parseFacts(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || strings.EqualFold(text, "NONE") {
		return nil
	}
	var facts []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(strings.ToUpper(line), "FACTS:") {
			continue
		}
		line = strings.TrimLeft(line, "-*•")
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "NONE") {
			continue
		}
		facts = append(facts, line)
	}
	return facts
}
