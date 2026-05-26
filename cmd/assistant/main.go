package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/claudecode"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/cryptoai"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/openrouter"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/postgres"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/searxng"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driving/httpapi"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driving/telegram"
	"github.com/olegmatyakubov/go-assistant/internal/app/chat"
	cronpkg "github.com/olegmatyakubov/go-assistant/internal/app/cron"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/tooling"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
	"github.com/olegmatyakubov/go-assistant/pkg/config"
)

//go:embed all:dashboard_dist
var dashboardEmbedFS embed.FS

const defaultSystemPrompt = `You are a personal AI assistant. Be concise and helpful.

RULES:
- Never lie, exaggerate, or fabricate. If you don't know — say so.
- Never flatter or praise. Be brutally objective.
- Short, direct answers. Max 2-3 paragraphs unless asked for more.
- When doing multi-step tasks, report progress after each step.
- Default language: Russian. Switch to English for code or if asked.

TOOLS: search_web (internet search), bash (server commands), trading_status (CryptoAI), run_code (Claude Code CLI).`

func main() {
	configPath := flag.String("config", "configs/config.yaml", "path to config file")
	migrateOnly := flag.Bool("migrate", false, "run database migrations and exit")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("starting assistant", "mode", cfg.Mode)

	// Database
	db, err := postgres.Connect(cfg.Database.DSN())
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate("migrations"); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	if *migrateOnly {
		slog.Info("migrations completed")
		return
	}

	// Repositories
	messageRepo := postgres.NewMessageRepo(db)
	activityRepo := postgres.NewActivityRepo(db)

	// Adapters
	llmClient := openrouter.New(cfg.LLM.Chat.APIKey, cfg.LLM.Chat.Model, cfg.LLM.Chat.Fallback)
	searchClient := searxng.New(cfg.Search.SearXNGURL)
	codeExecutor := claudecode.New(cfg.Code.DefaultDir, cfg.Code.Binary)

	var tradingClient *cryptoai.Client
	if cfg.Trading.CryptoAIURL != "" {
		tradingClient = cryptoai.New(cfg.Trading.CryptoAIURL, cfg.Trading.CryptoAIKey)
	}

	// Tool registry
	registry := tooling.NewRegistry()
	registry.Register(builtin.NewSearchWeb(searchClient))
	registry.Register(builtin.NewRunCode(codeExecutor, cfg.Code.DefaultDir))
	registry.Register(builtin.NewBash())
	if tradingClient != nil {
		registry.Register(builtin.NewTradingStatus(tradingClient))
	}
	if cfg.MailRu.Email != "" {
		registry.Register(builtin.NewMailRuCloud(cfg.MailRu.Email, cfg.MailRu.Password, cfg.MailRu.BasePath))
	}

	// Memory system
	memoryRepo := postgres.NewMemoryRepo(db)
	embeddingClient := openrouter.NewEmbeddingClient(
		cfg.LLM.Embedding.APIKey,
		cfg.LLM.Embedding.Model,
		"",
	)
	memorySvc := memory.NewService(memoryRepo, embeddingClient)

	// System prompt
	systemPrompt := defaultSystemPrompt
	if cfg.SystemPrompt != "" {
		data, err := os.ReadFile(cfg.SystemPrompt)
		if err != nil {
			slog.Warn("failed to read system prompt file, using default", "file", cfg.SystemPrompt, "error", err)
		} else {
			systemPrompt = string(data)
			slog.Info("loaded system prompt", "file", cfg.SystemPrompt)
		}
	}

	// Chat pipeline
	classifier := chat.NewRuleClassifier()
	toolLoop := chat.NewToolLoop(registry, 10)
	pipeline := chat.NewPipeline(classifier, llmClient, registry, toolLoop)
	chatService := chat.NewService(pipeline, messageRepo, activityRepo, memorySvc, systemPrompt)

	// Cron scheduler (SendFunc will be set after bot is created)
	cronRepo := postgres.NewCronRepo(db)
	var cronSendFunc cronpkg.SendFunc
	cronScheduler := cronpkg.NewScheduler(cronRepo, chatService, func(text string) {
		if cronSendFunc != nil {
			cronSendFunc(text)
		}
	})

	// Telegram bot
	bot, err := telegram.NewBot(
		telegram.BotConfig{
			Token:           cfg.Telegram.Token,
			OwnerID:         cfg.Telegram.OwnerID,
			AllowedUsers:    cfg.Telegram.AllowedUsers,
			FilesDir:        filepath.Join(filepath.Dir(*configPath), "files"),
			StreamMode:      telegram.StreamMode(cfg.Telegram.StreamMode),
			WatchdogTimeout: cfg.Telegram.WatchdogTimeout,
			DebounceDelay:   cfg.Telegram.DebounceDelay,
		},
		chatService,
		tradingClient,
		codeExecutor,
		openrouter.NewSTTClient(cfg.LLM.Chat.APIKey),
		memorySvc,
		cronScheduler,
	)
	if err != nil {
		slog.Error("failed to create telegram bot", "error", err)
		os.Exit(1)
	}

	// Wire cron send function to bot
	cronSendFunc = bot.SendToOwner

	// Dashboard FS
	var dashboardFS fs.FS
	if sub, err := fs.Sub(dashboardEmbedFS, "dashboard_dist"); err == nil {
		dashboardFS = sub
	}

	// HTTP API
	router := httpapi.NewRouter(httpapi.RouterDeps{
		APIKey:       cfg.Dashboard.APIKey,
		Mode:         cfg.Mode,
		ChatService:  chatService,
		MessageRepo:  messageRepo,
		ActivityRepo: activityRepo,
		ToolRegistry: registry,
		DashboardFS:  dashboardFS,
		MemoryRepo:   memoryRepo,
		MemorySvc:    memorySvc,
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Dashboard.Port),
		Handler: router,
	}

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("shutdown signal received")
		httpServer.Close()
		cancel()
	}()

	// Daily summarizer
	summarizer := memory.NewSummarizer(memorySvc, messageRepo, llmClient, cfg.Memory.SummarizeInterval)
	go summarizer.Run(ctx)

	// Cron scheduler
	go cronScheduler.Run(ctx)

	// Start HTTP server
	go func() {
		slog.Info("dashboard started", "port", cfg.Dashboard.Port)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
		}
	}()

	// Start Telegram bot (blocking)
	slog.Info("assistant ready",
		"mode", cfg.Mode,
		"tools", len(registry.ListTools()),
	)

	if err := bot.Start(ctx); err != nil {
		slog.Error("bot error", "error", err)
		os.Exit(1)
	}
}
