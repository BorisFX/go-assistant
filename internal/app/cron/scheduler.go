package cron

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/domain/valueobject"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

type SendFunc func(text string)

type Scheduler struct {
	repo        output.CronRepository
	chatService input.ChatService
	send        SendFunc
}

func NewScheduler(repo output.CronRepository, chatService input.ChatService, send SendFunc) *Scheduler {
	return &Scheduler{
		repo:        repo,
		chatService: chatService,
		send:        send,
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	jobs, err := s.repo.GetDue(ctx)
	if err != nil {
		slog.Error("cron: failed to get due jobs", "error", err)
		return
	}

	for _, job := range jobs {
		slog.Info("cron: executing job", "name", job.Name, "prompt", job.Prompt)

		s.send(fmt.Sprintf("Cron [%s]: executing...", job.Name))

		sessionKey := valueobject.NewSessionKey("cron", job.ID.String())
		resp, err := s.chatService.ProcessMessage(ctx, input.ChatRequest{
			SessionKey: sessionKey,
			Content:    job.Prompt,
		})

		if err != nil {
			s.send(fmt.Sprintf("Cron [%s] error: %v", job.Name, err))
		} else {
			s.send(fmt.Sprintf("Cron [%s]:\n%s", job.Name, resp.Content))
		}

		nextRun := time.Now().Add(time.Duration(job.IntervalSeconds) * time.Second)
		if err := s.repo.MarkRun(ctx, job.ID, nextRun); err != nil {
			slog.Error("cron: failed to mark run", "error", err)
		}
	}
}

func (s *Scheduler) Add(ctx context.Context, name, prompt, schedule string) (*entity.CronJob, error) {
	intervalSec, err := ParseSchedule(schedule)
	if err != nil {
		return nil, err
	}

	job := entity.NewCronJob(name, prompt, schedule, intervalSec)
	if err := s.repo.Save(ctx, job); err != nil {
		return nil, fmt.Errorf("save cron: %w", err)
	}

	return job, nil
}

func (s *Scheduler) Delete(ctx context.Context, index int) error {
	jobs, err := s.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}

	if index < 1 || index > len(jobs) {
		return fmt.Errorf("invalid index %d, have %d jobs", index, len(jobs))
	}

	return s.repo.Delete(ctx, jobs[index-1].ID)
}

func (s *Scheduler) List(ctx context.Context) ([]*entity.CronJob, error) {
	return s.repo.List(ctx)
}

// ParseSchedule parses human-readable schedule into seconds.
// Supports: "every 5m", "every 1h", "every 30m", "every 2h", "every day", "every 12h"
func ParseSchedule(schedule string) (int, error) {
	s := strings.TrimSpace(strings.ToLower(schedule))

	if s == "every day" || s == "daily" {
		return 86400, nil
	}
	if s == "every hour" || s == "hourly" {
		return 3600, nil
	}

	// "every Nm" or "every Nh"
	re := regexp.MustCompile(`^every\s+(\d+)\s*(m|min|h|hr|s|sec)$`)
	matches := re.FindStringSubmatch(s)
	if matches != nil {
		n, _ := strconv.Atoi(matches[1])
		switch matches[2] {
		case "s", "sec":
			return n, nil
		case "m", "min":
			return n * 60, nil
		case "h", "hr":
			return n * 3600, nil
		}
	}

	// "Nm" or "Nh" shorthand
	reShort := regexp.MustCompile(`^(\d+)\s*(m|h)$`)
	shortMatches := reShort.FindStringSubmatch(s)
	if shortMatches != nil {
		n, _ := strconv.Atoi(shortMatches[1])
		switch shortMatches[2] {
		case "m":
			return n * 60, nil
		case "h":
			return n * 3600, nil
		}
	}

	return 0, fmt.Errorf("can't parse schedule %q. Use: every 5m, every 1h, every day, 30m, 2h", schedule)
}
