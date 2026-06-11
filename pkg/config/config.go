package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode      string    `yaml:"mode"` // "server" or "local"
	Telegram  Telegram  `yaml:"telegram"`
	LLM       LLM       `yaml:"llm"`
	Database  Database  `yaml:"database"`
	Search    Search    `yaml:"search"`
	Trading   Trading   `yaml:"trading"`
	Budget    Budget    `yaml:"budget"`
	Dashboard    Dashboard    `yaml:"dashboard"`
	Code         Code         `yaml:"code"`
	Memory       MemoryConfig `yaml:"memory"`
	SystemPrompt string       `yaml:"system_prompt_file"`
	MailRu       MailRu       `yaml:"mailru"`
	LegalReview  LegalReview  `yaml:"legal_review"`
	Obsidian     Obsidian     `yaml:"obsidian"`
	// Timezone is an IANA name (e.g. "Asia/Phnom_Penh"). Used to anchor
	// clock-time cron schedules like "daily at 09:00" to the owner's local time.
	Timezone string `yaml:"timezone"`
}

type MailRu struct {
	Email    string `yaml:"email"`
	Password string `yaml:"password"`
	BasePath string `yaml:"base_path"`
}

// LegalReview configures the legal-document-review pipeline. Off by default
// (zero value Enabled=false), so existing configs need no migration.
type LegalReview struct {
	Enabled                   bool   `yaml:"enabled"`
	NormativyPath             string `yaml:"normativy_path"`
	MaxFiles                  int    `yaml:"max_files"`
	Concurrency               int    `yaml:"concurrency"`
	DigestModel               string `yaml:"digest_model"`
	DigestMaxChars            int    `yaml:"digest_max_chars"`
	CoordinatorModel          string `yaml:"coordinator_model"`
	ReduceModel               string `yaml:"reduce_model"`
	CoordinatorMaxInputTokens int    `yaml:"coordinator_max_input_tokens"`
}

type Telegram struct {
	Token            string        `yaml:"token"`
	OwnerID          int64         `yaml:"owner_id"`
	AllowedUsers     []int64       `yaml:"allowed_users"`
	StreamMode       string        `yaml:"stream_mode"`
	WatchdogTimeout  time.Duration `yaml:"polling_watchdog_timeout"`
	DebounceDelay    time.Duration `yaml:"debounce_delay"`
	MediaGroupWindow time.Duration `yaml:"media_group_window"`
}

type LLM struct {
	Chat      LLMModel `yaml:"chat"`
	Embedding LLMModel `yaml:"embedding"`
	Vision    LLMModel `yaml:"vision"`
}

type LLMModel struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Fallback string `yaml:"fallback"`
	APIKey   string `yaml:"api_key"`
}

type Database struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Name     string `yaml:"name"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

func (d Database) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		d.User, d.Password, d.Host, d.Port, d.Name)
}

type Search struct {
	SearXNGURL string `yaml:"searxng_url"`
}

type Trading struct {
	CryptoAIURL  string        `yaml:"cryptoai_url"`
	CryptoAIKey  string        `yaml:"cryptoai_key"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

type Budget struct {
	MonthlyLimit   float64 `yaml:"monthly_limit"`
	AlertThreshold float64 `yaml:"alert_threshold"`
}

type Dashboard struct {
	Port   int    `yaml:"port"`
	APIKey string `yaml:"api_key"`
	Domain string `yaml:"domain"`
}

type Code struct {
	Binary     string `yaml:"binary"`
	DefaultDir string `yaml:"default_dir"`
}

type Obsidian struct {
	VaultDir string `yaml:"vault_dir"`
}

type MemoryConfig struct {
	ShortTermLimit       int           `yaml:"short_term_limit"`
	WorkingMemoryResults int           `yaml:"working_memory_results"`
	MaxContextTokens     int           `yaml:"max_context_tokens"`
	RetentionDays        int           `yaml:"retention_days"`
	SummarizeInterval    time.Duration `yaml:"summarize_interval"`

	FactExtractionInterval int     `yaml:"fact_extraction_interval"`
	ExtractionModel        string  `yaml:"extraction_model"`
	SimilarityThreshold    float64 `yaml:"similarity_threshold"`
	DedupThreshold         float64 `yaml:"dedup_threshold"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validate checks cross-field invariants that cannot be defaulted.
func (c *Config) validate() error {
	if c.LegalReview.Enabled {
		if c.LegalReview.DigestModel == "" {
			return fmt.Errorf("config: legal_review.digest_model is required when legal_review.enabled is true")
		}
		if c.LegalReview.CoordinatorModel == "" {
			return fmt.Errorf("config: legal_review.coordinator_model is required when legal_review.enabled is true")
		}
		if c.LegalReview.ReduceModel == "" {
			return fmt.Errorf("config: legal_review.reduce_model is required when legal_review.enabled is true")
		}
	}
	return nil
}

func (c *Config) setDefaults() {
	if c.Mode == "" {
		c.Mode = "server"
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if c.LLM.Vision.Model == "" {
		c.LLM.Vision.Model = "google/gemini-2.5-flash"
	}
	if c.Telegram.StreamMode == "" {
		c.Telegram.StreamMode = "partial"
	}
	if c.Telegram.WatchdogTimeout == 0 {
		c.Telegram.WatchdogTimeout = 120 * time.Second
	}
	if c.Telegram.DebounceDelay == 0 {
		c.Telegram.DebounceDelay = 500 * time.Millisecond
	}
	if c.Telegram.MediaGroupWindow == 0 {
		c.Telegram.MediaGroupWindow = 500 * time.Millisecond
	}
	if c.Database.Port == 0 {
		c.Database.Port = 5432
	}
	if c.Trading.PollInterval == 0 {
		c.Trading.PollInterval = 5 * time.Minute
	}
	if c.Budget.MonthlyLimit == 0 {
		c.Budget.MonthlyLimit = 5.0
	}
	if c.Budget.AlertThreshold == 0 {
		c.Budget.AlertThreshold = 0.8
	}
	if c.Dashboard.Port == 0 {
		c.Dashboard.Port = 8080
	}
	if c.Code.Binary == "" {
		c.Code.Binary = "claude"
	}
	if c.Memory.ShortTermLimit == 0 {
		c.Memory.ShortTermLimit = 30
	}
	if c.Memory.WorkingMemoryResults == 0 {
		c.Memory.WorkingMemoryResults = 5
	}
	if c.Memory.MaxContextTokens == 0 {
		c.Memory.MaxContextTokens = 3000
	}
	if c.Memory.RetentionDays == 0 {
		c.Memory.RetentionDays = 90
	}
	if c.Memory.SummarizeInterval == 0 {
		c.Memory.SummarizeInterval = 5 * time.Minute
	}
	if c.Memory.FactExtractionInterval == 0 {
		c.Memory.FactExtractionInterval = 6
	}
	if c.Memory.ExtractionModel == "" {
		c.Memory.ExtractionModel = "deepseek/deepseek-v4-flash"
	}
	if c.Memory.SimilarityThreshold == 0 {
		c.Memory.SimilarityThreshold = 0.45
	}
	if c.Memory.DedupThreshold == 0 {
		c.Memory.DedupThreshold = 0.15
	}
	// LegalReview numeric defaults apply only when the feature is enabled.
	if c.LegalReview.Enabled {
		if c.LegalReview.MaxFiles == 0 {
			c.LegalReview.MaxFiles = 60
		}
		if c.LegalReview.Concurrency == 0 {
			c.LegalReview.Concurrency = 4
		}
		if c.LegalReview.DigestMaxChars == 0 {
			c.LegalReview.DigestMaxChars = 48000
		}
		if c.LegalReview.CoordinatorMaxInputTokens == 0 {
			c.LegalReview.CoordinatorMaxInputTokens = 80000
		}
	}
}
