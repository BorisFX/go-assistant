package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

const dayLayout = "2006-01-02"

type Summarizer struct {
	memorySvc     *Service
	messageRepo   output.MessageRepository
	llm           output.LLMProvider
	interval      time.Duration
	retentionDays int
}

func NewSummarizer(
	memorySvc *Service,
	messageRepo output.MessageRepository,
	llm output.LLMProvider,
	interval time.Duration,
	retentionDays int,
) *Summarizer {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	return &Summarizer{
		memorySvc:     memorySvc,
		messageRepo:   messageRepo,
		llm:           llm,
		interval:      interval,
		retentionDays: retentionDays,
	}
}

func (s *Summarizer) Run(ctx context.Context) {
	if err := s.CatchUp(ctx); err != nil {
		slog.Error("initial summarization catch-up failed", "error", err)
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.CatchUp(ctx); err != nil {
				slog.Error("summarization catch-up failed", "error", err)
			}
		}
	}
}

// CatchUp summarizes every past day that has messages but no summary yet.
func (s *Summarizer) CatchUp(ctx context.Context) error {
	withMessages, err := s.daysWithMessages(ctx)
	if err != nil {
		return fmt.Errorf("days with messages: %w", err)
	}
	withSummary, err := s.daysWithSummary(ctx)
	if err != nil {
		return fmt.Errorf("days with summary: %w", err)
	}

	today := time.Now().Truncate(24 * time.Hour)
	for _, day := range missingDays(withMessages, withSummary, today) {
		parsed, perr := time.Parse(dayLayout, day)
		if perr != nil {
			continue
		}
		if serr := s.summarizeDay(ctx, parsed); serr != nil {
			slog.Error("failed to summarize day", "day", day, "error", serr)
		}
	}
	return nil
}

func (s *Summarizer) daysWithMessages(ctx context.Context) (map[string]bool, error) {
	convs, err := s.messageRepo.ListConversations(ctx, 100, 0)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().AddDate(0, 0, -s.retentionDays)
	days := map[string]bool{}
	for _, conv := range convs {
		messages, merr := s.messageRepo.ListMessages(ctx, conv.ID, 1000)
		if merr != nil {
			slog.Warn("failed to list messages", "conv_id", conv.ID, "error", merr)
			continue
		}
		for _, msg := range messages {
			if msg.CreatedAt.Before(cutoff) {
				continue
			}
			if msg.Role == entity.RoleUser || msg.Role == entity.RoleAssistant {
				days[msg.CreatedAt.Truncate(24*time.Hour).Format(dayLayout)] = true
			}
		}
	}
	return days, nil
}

func (s *Summarizer) daysWithSummary(ctx context.Context) (map[string]bool, error) {
	summaries, err := s.memorySvc.repo.GetRecentSummaries(ctx, s.retentionDays)
	if err != nil {
		return nil, err
	}
	days := map[string]bool{}
	for _, sum := range summaries {
		for _, tag := range sum.Tags {
			if strings.HasPrefix(tag, "day:") {
				days[strings.TrimPrefix(tag, "day:")] = true
			}
		}
	}
	return days, nil
}

// missingDays returns days (sorted) that have messages, no summary, and are before today.
func missingDays(withMessages, withSummary map[string]bool, today time.Time) []string {
	todayStr := today.Truncate(24 * time.Hour).Format(dayLayout)
	var out []string
	for day := range withMessages {
		if day == todayStr {
			continue
		}
		if day > todayStr {
			continue
		}
		if withSummary[day] {
			continue
		}
		out = append(out, day)
	}
	sort.Strings(out)
	return out
}

func (s *Summarizer) summarizeDay(ctx context.Context, day time.Time) error {
	dayStart := day.Truncate(24 * time.Hour)
	dayEnd := dayStart.AddDate(0, 0, 1)
	dayTag := dayStart.Format(dayLayout)
	slog.Info("summarizing day", "day", dayTag)

	convs, err := s.messageRepo.ListConversations(ctx, 100, 0)
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}

	var dayMessages []string
	for _, conv := range convs {
		messages, merr := s.messageRepo.ListMessages(ctx, conv.ID, 1000)
		if merr != nil {
			slog.Warn("failed to list messages", "conv_id", conv.ID, "error", merr)
			continue
		}
		for _, msg := range messages {
			if msg.CreatedAt.Before(dayStart) || !msg.CreatedAt.Before(dayEnd) {
				continue
			}
			if msg.Role == entity.RoleUser || msg.Role == entity.RoleAssistant {
				dayMessages = append(dayMessages, fmt.Sprintf("[%s] %s", msg.Role, msg.Content))
			}
		}
	}
	if len(dayMessages) == 0 {
		slog.Info("no messages to summarize", "day", dayTag)
		return nil
	}

	prompt := fmt.Sprintf(`Summarize the day's conversations in 2-3 sentences. Then extract key facts about the user as a bullet list.

Conversations:
%s

Respond in this format:
SUMMARY: <2-3 sentence summary>
FACTS:
- <fact 1>
- <fact 2>`, strings.Join(dayMessages, "\n"))

	resp, err := s.llm.Chat(ctx, output.LLMRequest{
		Messages:    []output.LLMMessage{{Role: entity.RoleUser, Content: prompt}},
		MaxTokens:   500,
		Temperature: 0.3,
	})
	if err != nil {
		return fmt.Errorf("llm summarize: %w", err)
	}

	parts := strings.SplitN(resp.Content, "FACTS:", 2)
	summaryText := strings.TrimSpace(strings.TrimPrefix(parts[0], "SUMMARY:"))
	if summaryText != "" {
		if err := s.memorySvc.StoreSummary(ctx, summaryText, "summarizer", []string{"daily", "day:" + dayTag}); err != nil {
			slog.Error("failed to store summary", "error", err)
		}
	}

	if len(parts) > 1 {
		for _, fact := range parseFacts(parts[1]) {
			if err := s.memorySvc.StoreFact(ctx, fact, "summarizer", []string{"extracted"}); err != nil {
				slog.Error("failed to store fact", "error", err)
			}
		}
	}

	slog.Info("day summarization complete", "day", dayTag, "messages_processed", len(dayMessages))
	return nil
}
