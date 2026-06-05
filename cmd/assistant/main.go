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
	"time"

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

TOOLS: search_web (internet search), bash (server commands), trading_status (CryptoAI), run_code (Claude Code CLI), manage_cron (schedule recurring tasks for yourself), read_pdf (extract a PDF's real text), inspect_signature (read who really signed a .sig file).

DOCUMENT CHECKS (mandatory, override any other workflow):
- Never judge a document's content by its file name. Read it first: PDFs with read_pdf, electronic signatures (.sig) with inspect_signature.
- Before saying anything about signatures (who signed, wrong signer, "re-sign all"), you MUST call inspect_signature on EVERY .sig the claim concerns. Do not use pdftotext/bash for signatures. A second signature by a government body or the document's author org is that party's own normal signature, not a violation.
- Do not claim a section, legend, field or value is missing/present until you have read the actual text with read_pdf.
- Per claim, emit "Проверено (файл X, инструмент Y): ..." when you actually read it, or "не проверено" otherwise (then no verdict is allowed). "Нарушение не выявлено" / "не подтверждается" is a valid result — never invent a discrepancy to fill a template. Mark guesses as "Предположение (не проверено): ...".
- If read_pdf returns truncated=true and you have not found the value you need, page on with read_pdf using offset = previous offset + returned before concluding "не проверено". Only declare it missing after reading to the end (truncated=false).`

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
	registry.Register(builtin.NewReadPDF())
	registry.Register(builtin.NewInspectSignature())
	if tradingClient != nil {
		registry.Register(builtin.NewTradingStatus(tradingClient))
	}
	filesDir := filepath.Join(filepath.Dir(*configPath), "files")
	var mailRuCloud *builtin.MailRuCloud
	if cfg.MailRu.Email != "" {
		mailRuCloud = builtin.NewMailRuCloud(cfg.MailRu.Email, cfg.MailRu.Password, cfg.MailRu.BasePath, filesDir)
		registry.Register(mailRuCloud)
	}

	// Memory system
	memoryRepo := postgres.NewMemoryRepo(db)
	embeddingClient := openrouter.NewEmbeddingClient(
		cfg.LLM.Embedding.APIKey,
		cfg.LLM.Embedding.Model,
		"",
	)
	memorySvc := memory.NewService(memoryRepo, embeddingClient, memory.ServiceConfig{
		SimilarityThreshold: cfg.Memory.SimilarityThreshold,
		DedupThreshold:      cfg.Memory.DedupThreshold,
		TopK:                cfg.Memory.WorkingMemoryResults,
		FactLimit:           10,
		SummaryDays:         7,
	})
	factExtractor := memory.NewExtractor(memorySvc, messageRepo, llmClient, cfg.Memory.ExtractionModel, cfg.Memory.FactExtractionInterval)

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
	toolLoop := chat.NewToolLoop(registry, 16)
	pipeline := chat.NewPipeline(classifier, llmClient, registry, toolLoop, cfg.LLM.Vision.Model)
	chatService := chat.NewService(pipeline, messageRepo, activityRepo, memorySvc, factExtractor, systemPrompt)

	// Timezone for clock-time cron schedules ("daily at 09:00").
	cronLoc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		slog.Warn("invalid timezone, falling back to UTC", "timezone", cfg.Timezone, "error", err)
		cronLoc = time.UTC
	}

	// Cron scheduler (SendFunc will be set after bot is created)
	cronRepo := postgres.NewCronRepo(db)
	var cronSendFunc cronpkg.SendFunc
	cronScheduler := cronpkg.NewScheduler(cronRepo, chatService, func(text string) {
		if cronSendFunc != nil {
			cronSendFunc(text)
		}
	}, cronLoc)

	// Let the assistant manage its own scheduled tasks.
	registry.Register(builtin.NewManageCron(cronScheduler, cronLoc))

	// Telegram bot
	bot, err := telegram.NewBot(
		telegram.BotConfig{
			Token:           cfg.Telegram.Token,
			OwnerID:         cfg.Telegram.OwnerID,
			AllowedUsers:    cfg.Telegram.AllowedUsers,
			FilesDir:        filesDir,
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
	summarizer := memory.NewSummarizer(memorySvc, messageRepo, llmClient, cfg.Memory.SummarizeInterval, cfg.Memory.RetentionDays)
	go summarizer.Run(ctx)

	// Cron scheduler
	go cronScheduler.Run(ctx)

	// Warm the Mail.ru cloud index so the first search hits a ready cache
	// instead of blocking on a multi-minute WebDAV tree walk.
	if mailRuCloud != nil {
		go mailRuCloud.WarmIndex(ctx)
		go mailRuCloud.RefreshLoop(ctx)
	}

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
