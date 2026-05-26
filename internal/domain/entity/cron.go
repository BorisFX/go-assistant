package entity

import (
	"time"

	"github.com/google/uuid"
)

type CronJob struct {
	ID              uuid.UUID
	Name            string
	Prompt          string
	Schedule        string // human-readable: "every 1h", "every day 9:00"
	IntervalSeconds int
	NextRunAt       time.Time
	LastRunAt       *time.Time
	Enabled         bool
	CreatedAt       time.Time
}

func NewCronJob(name, prompt, schedule string, intervalSeconds int) *CronJob {
	return &CronJob{
		ID:              uuid.New(),
		Name:            name,
		Prompt:          prompt,
		Schedule:        schedule,
		IntervalSeconds: intervalSeconds,
		NextRunAt:       time.Now().Add(time.Duration(intervalSeconds) * time.Second),
		Enabled:         true,
		CreatedAt:       time.Now(),
	}
}
