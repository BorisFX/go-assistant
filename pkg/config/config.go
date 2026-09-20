package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode         string       `yaml:"mode"` // "server" or "local"
	Telegram     Telegram     `yaml:"telegram"`
	LLM          LLM          `yaml:"llm"`
	Database     Database     `yaml:"database"`
	Search       Search       `yaml:"search"`
	Trading      Trading      `yaml:"trading"`
	Budget       Budget       `yaml:"budget"`
	Dashboard    Dashboard    `yaml:"dashboard"`
	Code         Code         `yaml:"code"`
	Memory       MemoryConfig `yaml:"memory"`
	Chat         ChatConfig   `yaml:"chat"`
	SystemPrompt string       `yaml:"system_prompt_file"`
	MailRu       MailRu       `yaml:"mailru"`
	LegalReview  LegalReview  `yaml:"legal_review"`
	Obsidian     Obsidian     `yaml:"obsidian"`
	TravelSearch TravelSearch `yaml:"travel_search"`
	Google       Google       `yaml:"google"`
	// Timezone is an IANA name (e.g. "Asia/Phnom_Penh"). Used to anchor
	// clock-time cron schedules like "daily at 09:00" to the owner's local time.
	Timezone string `yaml:"timezone"`
}

type MailRu struct {
	Email    string `yaml:"email"`
	Password string `yaml:"password"`
	BasePath string `yaml:"base_path"`
}

// Google configures access to Google Workspace through a service account.
// Off by default (empty CredentialsFile), so existing configs need no migration.
type Google struct {
	CredentialsFile string       `yaml:"credentials_file"`
	Impersonate     string       `yaml:"impersonate"`
	Drive           GoogleDrive  `yaml:"drive"`
	Sheets          GoogleSheets `yaml:"sheets"`
	Gmail           GoogleGmail  `yaml:"gmail"`
}

type GoogleDrive struct {
	RootFolderID string `yaml:"root_folder_id"`
}

type GoogleSheets struct {
	RegistryID    string `yaml:"registry_id"`
	RegistrySheet string `yaml:"registry_sheet"`
}

type GoogleGmail struct {
	Enabled        bool          `yaml:"enabled"`
	IngestQuery    string        `yaml:"ingest_query"`
	ProcessedLabel string        `yaml:"processed_label"`
	PollInterval   time.Duration `yaml:"poll_interval"`
	// MaxMessages caps one courier pass. A mailbox left unprocessed for a week
	// would otherwise pull hundreds of attachments in a single burst.
	MaxMessages int `yaml:"max_messages"`
}

// Enabled reports whether any Google integration should be wired up.
func (g Google) Enabled() bool { return g.CredentialsFile != "" }

// LegalReview configures the legal-document-review pipeline. Off by default
// (zero value Enabled=false), so existing configs need no migration.
type LegalReview struct {
	Enabled bool `yaml:"enabled"`
	// CADPython and CADScript enable the structural DWG/DXF reader. Empty means
	// drawings keep going to the vision model, as before.
	CADPython string `yaml:"cad_python"`
	CADScript string `yaml:"cad_script"`
	// OfficeScript enables reading .doc/.docx/.xls/.xlsx через тот же python.
	OfficeScript  string `yaml:"office_script"`
	NormativyPath string `yaml:"normativy_path"`
	// NormsDir is the folder with full normative texts (laws, СП) that the
	// corpus indexes. Empty means no corpus: the coordinator then refuses to
	// cite any norm rather than citing from model memory.
	NormsDir                  string `yaml:"norms_dir"`
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
	Chat      LLMModel   `yaml:"chat"`
	Embedding LLMModel   `yaml:"embedding"`
	Vision    LLMModel   `yaml:"vision"`
	Image     ImageModel `yaml:"image"`
}

// ImageModel configures the image-drawing endpoint (OpenAI-compatible
// /images/generations and /images/edits). An empty Model disables the
// generate_image tool entirely, which is how instances that shouldn't draw
// (e.g. the Yuri bot) stay unchanged.
type ImageModel struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	// Fallback is tried when the primary model fails — image models refuse
	// edits of photos of real people often enough to need a second opinion.
	Fallback string `yaml:"fallback"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Size     string `yaml:"size"`
}

type LLMModel struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Fallback string `yaml:"fallback"`
	// BaseURL overrides the OpenAI-compatible chat-completions endpoint for this
	// model. Empty = OpenRouter default. Lets the chat model run on a different
	// gateway (e.g. a6api) while vision/voice/embeddings stay on OpenRouter.
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
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
	// Tavily is the primary web-search provider (https://tavily.com) — LLM-native,
	// returns dated, ranked results with a freshness filter. When APIKey is empty
	// the bot falls back to SearXNG/DuckDuckGo. Endpoint is optional (defaults to
	// the public API).
	Tavily TavilyConfig `yaml:"tavily"`
}

type TavilyConfig struct {
	APIKey   string `yaml:"api_key"`
	Endpoint string `yaml:"endpoint"`
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

// TravelSearch configures the flight_search and hotel_search tools. Off by
// default (empty RapidAPIKey), so existing configs need no migration. Both
// tools share one RapidAPI key (booking-com15).
type TravelSearch struct {
	RapidAPIKey  string `yaml:"rapidapi_key"`
	RapidAPIHost string `yaml:"rapidapi_host"`
	Currency     string `yaml:"currency"`
	ResultsLimit int    `yaml:"results_limit"`
}

type ChatConfig struct {
	MaxTokens          int     `yaml:"max_tokens"`
	MaxToolTurns       int     `yaml:"max_tool_turns"`
	MaxToolResultChars int     `yaml:"max_tool_result_chars"`
	ChatTemperature    float64 `yaml:"chat_temperature"`
	ToolTemperature    float64 `yaml:"tool_temperature"`
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
	if c.Google.Enabled() {
		if c.Google.Drive.RootFolderID == "" {
			return fmt.Errorf("config: google.drive.root_folder_id is required when google.credentials_file is set")
		}
		if c.Google.Sheets.RegistryID == "" {
			return fmt.Errorf("config: google.sheets.registry_id is required when google.credentials_file is set")
		}
		if c.Google.Gmail.Enabled && c.Google.Impersonate == "" {
			return fmt.Errorf("config: google.impersonate is required when google.gmail.enabled is true")
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
	// TravelSearch defaults apply only when the feature is enabled (key set).
	if c.TravelSearch.RapidAPIKey != "" {
		if c.TravelSearch.RapidAPIHost == "" {
			c.TravelSearch.RapidAPIHost = "booking-com15.p.rapidapi.com"
		}
		if c.TravelSearch.Currency == "" {
			c.TravelSearch.Currency = "USD"
		}
		if c.TravelSearch.ResultsLimit == 0 {
			c.TravelSearch.ResultsLimit = 6
		}
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

	// Google defaults apply only when the integration is enabled.
	if c.Google.Enabled() {
		if c.Google.Sheets.RegistrySheet == "" {
			c.Google.Sheets.RegistrySheet = "Проекты"
		}
		if c.Google.Gmail.Enabled {
			if c.Google.Gmail.ProcessedLabel == "" {
				c.Google.Gmail.ProcessedLabel = "Обработано"
			}
			// Built after ProcessedLabel so the query excludes the label in use.
			if c.Google.Gmail.IngestQuery == "" {
				c.Google.Gmail.IngestQuery = "in:inbox -label:" + c.Google.Gmail.ProcessedLabel
			}
			if c.Google.Gmail.MaxMessages == 0 {
				c.Google.Gmail.MaxMessages = 20
			}
			if c.Google.Gmail.PollInterval == 0 {
				c.Google.Gmail.PollInterval = 15 * time.Minute
			}
		}
	}

	// Chat defaults
	if c.Chat.MaxTokens == 0 {
		c.Chat.MaxTokens = 4096
	}
	if c.Chat.MaxToolTurns == 0 {
		c.Chat.MaxToolTurns = 25
	}
	if c.Chat.MaxToolResultChars == 0 {
		c.Chat.MaxToolResultChars = 24000
	}
	if c.Chat.ChatTemperature == 0 {
		c.Chat.ChatTemperature = 0.7
	}
	if c.Chat.ToolTemperature == 0 {
		c.Chat.ToolTemperature = 0.2
	}
}
