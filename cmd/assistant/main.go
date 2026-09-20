package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/a6image"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/claudecode"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/cryptoai"
	// Aliased: the package name would otherwise clash with the Google SDK's own
	// google packages pulled in alongside drive/v3.
	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/openrouter"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/postgres"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/search"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/searxng"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/tavily"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driving/httpapi"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driving/telegram"
	"github.com/olegmatyakubov/go-assistant/internal/app/chat"
	cronpkg "github.com/olegmatyakubov/go-assistant/internal/app/cron"
	"github.com/olegmatyakubov/go-assistant/internal/app/extraction"
	"github.com/olegmatyakubov/go-assistant/internal/app/legalreview"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/app/projects"
	"github.com/olegmatyakubov/go-assistant/internal/app/subagent"
	"github.com/olegmatyakubov/go-assistant/internal/observability"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
	"github.com/olegmatyakubov/go-assistant/pkg/config"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/sheets/v4"
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
	// Chat runs on cfg.LLM.Chat's provider — cfg.LLM.Chat.BaseURL may point it at a
	// non-OpenRouter gateway (e.g. a6api). Vision, voice (STT) and background memory
	// extraction stay on OpenRouter via orClient, since those models (gemini,
	// whisper) live there. orKey falls back to the chat key for single-provider
	// setups (e.g. the Yuri instance) that don't split providers.
	llmClient := openrouter.New(cfg.LLM.Chat.APIKey, cfg.LLM.Chat.Model, cfg.LLM.Chat.Fallback, cfg.LLM.Chat.BaseURL)

	visionModel := cfg.LLM.Vision.Model
	if visionModel == "" {
		visionModel = "google/gemini-2.5-flash"
	}
	orKey := cfg.LLM.Vision.APIKey
	if orKey == "" {
		orKey = cfg.LLM.Chat.APIKey
	}
	orClient := openrouter.New(orKey, visionModel, "", cfg.LLM.Vision.BaseURL)

	// Wrap LLM clients with circuit breakers for graceful degradation.
	var (
		chatLLM   output.LLMProvider = observability.NewCircuitBreaker(llmClient, "chat")
		visionLLM output.LLMProvider = observability.NewCircuitBreaker(orClient, "vision")
	)
	_ = visionLLM

	// Web search: Tavily first (LLM-native, dated & freshness-filtered results),
	// falling back to SearXNG/DuckDuckGo when Tavily is unconfigured or fails.
	searchChain := search.NewChain()
	if cfg.Search.Tavily.APIKey != "" {
		searchChain.Add("tavily", tavily.New(cfg.Search.Tavily.APIKey, cfg.Search.Tavily.Endpoint))
		slog.Info("tavily web search enabled")
	}
	searchChain.Add("searxng", searxng.New(cfg.Search.SearXNGURL))
	var searchClient output.SearchProvider = searchChain
	codeExecutor := claudecode.New(cfg.Code.DefaultDir, cfg.Code.Binary)

	var tradingClient *cryptoai.Client
	if cfg.Trading.CryptoAIURL != "" {
		tradingClient = cryptoai.New(cfg.Trading.CryptoAIURL, cfg.Trading.CryptoAIKey)
	}

	// Tool registry
	registry := tooling.NewRegistry()
	registry.Register(builtin.NewSearchWeb(searchClient))
	registry.Register(builtin.NewRunCode(codeExecutor, cfg.Code.DefaultDir))
	if cfg.Obsidian.VaultDir != "" {
		registry.Register(builtin.NewObsidian(cfg.Obsidian.VaultDir, codeExecutor))
		slog.Info("obsidian tool enabled", "vault_dir", cfg.Obsidian.VaultDir)
	}
	registry.Register(builtin.NewBash())
	registry.Register(builtin.NewReadPDF())
	registry.Register(builtin.NewInspectSignature())
	if cfg.TravelSearch.RapidAPIKey != "" {
		rapidClient := builtin.NewRapidAPIClient(cfg.TravelSearch.RapidAPIKey, cfg.TravelSearch.RapidAPIHost)
		registry.Register(builtin.NewFlightSearch(rapidClient, cfg.TravelSearch.Currency, cfg.TravelSearch.ResultsLimit))
		registry.Register(builtin.NewHotelSearch(rapidClient, cfg.TravelSearch.Currency, cfg.TravelSearch.ResultsLimit))
		slog.Info("travel search tools enabled", "host", cfg.TravelSearch.RapidAPIHost)
	}
	if tradingClient != nil {
		registry.Register(builtin.NewTradingStatus(tradingClient))
	}
	filesDir := filepath.Join(filepath.Dir(*configPath), "files")

	// Image drawing: no llm.image block means no tool, so instances that never
	// asked to draw (the Yuri bot) keep exactly the tool set they had.
	if cfg.LLM.Image.Model != "" {
		imageKey := cfg.LLM.Image.APIKey
		if imageKey == "" {
			imageKey = cfg.LLM.Chat.APIKey
		}
		imageClient := a6image.New(
			imageKey,
			cfg.LLM.Image.Model,
			cfg.LLM.Image.Fallback,
			cfg.LLM.Image.BaseURL,
			filesDir,
		).WithSize(cfg.LLM.Image.Size)
		registry.Register(builtin.NewGenerateImage(imageClient))
		slog.Info("image generation enabled", "model", cfg.LLM.Image.Model, "fallback", cfg.LLM.Image.Fallback)
	}

	var (
		mailRuCloud *builtin.MailRuCloud
		driveFiles  *builtin.DriveFiles
		mailCourier *projects.Courier
		gmailClient *gworkspace.Gmail
		gmailTool   *builtin.Gmail
		rfqTool     *builtin.Contractors
	)
	if cfg.MailRu.Email != "" {
		mailRuCloud = builtin.NewMailRuCloud(cfg.MailRu.Email, cfg.MailRu.Password, cfg.MailRu.BasePath, filesDir)
		registry.Register(mailRuCloud)
	}
	if cfg.Google.Enabled() {
		// Fail loudly: a configured but broken Google setup is a deployment
		// error, not something to discover later inside a tool call.
		creds, err := gworkspace.LoadCredentials(cfg.Google.CredentialsFile, cfg.Google.Impersonate)
		if err != nil {
			slog.Error("failed to load google credentials", "error", err)
			os.Exit(1)
		}
		driveOpts, err := creds.ClientOptions(context.Background(), "", drive.DriveScope)
		if err != nil {
			slog.Error("failed to build google drive auth", "error", err)
			os.Exit(1)
		}
		driveClient, err := gworkspace.NewDrive(context.Background(), cfg.Google.Drive.RootFolderID, driveOpts...)
		if err != nil {
			slog.Error("failed to create google drive client", "error", err)
			os.Exit(1)
		}
		driveFiles = builtin.NewDriveFiles(driveClient, filesDir)
		if mailRuCloud != nil {
			// Lets an object's folder be carried from the Mail.ru archive into
			// the project's Drive folder without the bytes passing the model.
			driveFiles.SetCloudSource(mailRuCloud)
		}
		registry.Register(driveFiles)
		slog.Info("google drive tool enabled",
			"service_account", creds.Email(),
			"root_folder_id", cfg.Google.Drive.RootFolderID)

		sheetsOpts, err := creds.ClientOptions(context.Background(), "", sheets.SpreadsheetsScope)
		if err != nil {
			slog.Error("failed to build google sheets auth", "error", err)
			os.Exit(1)
		}
		sheetsClient, err := gworkspace.NewSheets(context.Background(), cfg.Google.Sheets.RegistryID, sheetsOpts...)
		if err != nil {
			slog.Error("failed to create google sheets client", "error", err)
			os.Exit(1)
		}
		projectSvc := projects.NewService(sheetsClient, driveClient)
		registry.Register(builtin.NewProjects(projectSvc))
		slog.Info("projects tool enabled", "registry_id", cfg.Google.Sheets.RegistryID)

		if cfg.Google.Gmail.Enabled {
			// Gmail refuses to act as a service account: every call runs on
			// behalf of the impersonated mailbox via domain-wide delegation.
			gmailOpts, err := creds.ClientOptions(context.Background(), cfg.Google.Impersonate,
				gmail.GmailModifyScope)
			if err != nil {
				slog.Error("failed to build gmail auth", "error", err)
				os.Exit(1)
			}
			gmailClient, err = gworkspace.NewGmail(context.Background(), cfg.Google.Impersonate, gmailOpts...)
			if err != nil {
				slog.Error("failed to create gmail client", "error", err)
				os.Exit(1)
			}
			gmailTool = builtin.NewGmail(gmailClient, filesDir)
			registry.Register(gmailTool)
			// Tender mailings live next to the mailbox: a group of contractors
			// from the registry, one separate draft each.
			rfqTool = builtin.NewContractors(projectSvc, gmailClient, filesDir)
			registry.Register(rfqTool)
			mailCourier = projects.NewCourier(gmailClient, driveClient, projectSvc, projects.CourierConfig{
				Query:       cfg.Google.Gmail.IngestQuery,
				Label:       cfg.Google.Gmail.ProcessedLabel,
				MaxMessages: cfg.Google.Gmail.MaxMessages,
				Interval:    cfg.Google.Gmail.PollInterval,
			}, nil)
			slog.Info("gmail enabled",
				"mailbox", cfg.Google.Impersonate,
				"query", cfg.Google.Gmail.IngestQuery,
				"poll_interval", cfg.Google.Gmail.PollInterval)
		}
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
	factExtractor := memory.NewExtractor(memorySvc, messageRepo, chatLLM, cfg.Memory.ExtractionModel, cfg.Memory.FactExtractionInterval)

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
	toolLoop := chat.NewToolLoop(registry, cfg.Chat.MaxToolTurns, cfg.Chat.MaxToolResultChars).
		WithActivityRepo(activityRepo)
	pipeline := chat.NewPipeline(classifier, chatLLM, visionLLM, registry, toolLoop, visionModel, chat.ChatConfig{
		MaxTokens:          cfg.Chat.MaxTokens,
		MaxToolResultChars: cfg.Chat.MaxToolResultChars,
		ChatTemperature:    cfg.Chat.ChatTemperature,
		ToolTemperature:    cfg.Chat.ToolTemperature,
	})
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
		openrouter.NewSTTClient(orKey),
		memorySvc,
		cronScheduler,
	)
	if err != nil {
		slog.Error("failed to create telegram bot", "error", err)
		os.Exit(1)
	}

	// Wire cron send function to bot
	cronSendFunc = bot.SendToOwner

	// Outgoing mail: the tool only prepares a draft, the confirmation card and
	// the send button live in Telegram.
	// A migration outlives the chat request that started it, so its report goes
	// to Telegram on its own.
	if driveFiles != nil {
		driveFiles.SetNotify(bot.SendToOwner)
	}
	if gmailClient != nil {
		bot.EnableGmail(gmailClient)
		gmailTool.SetOnDraft(bot.SendDraftCard)
		rfqTool.SetOnRfq(func(r builtin.Rfq) {
			bot.SendRfqCard(telegram.RfqCard{
				Group:       r.Group,
				Subject:     r.Subject,
				Drafts:      r.Drafts,
				Skipped:     r.Skipped,
				Attachments: r.Attachments,
				Warning:     r.Warning,
			})
		})
	}

	// Legal-document-review pipeline (Yuri instance only; off by default).
	if cfg.LegalReview.Enabled {
		// Documents live in two places: the Mail.ru archive and, for everything
		// the mail courier delivers, Drive. Either one is enough to start.
		var collectors []telegram.FolderCollector
		if driveFiles != nil {
			collectors = append(collectors, driveFiles)
		}
		if mailRuCloud != nil {
			collectors = append(collectors, mailRuCloud)
		}
		if len(collectors) == 0 {
			slog.Error("legal_review enabled but neither google drive nor mail.ru cloud is configured")
			os.Exit(1)
		}
		normativy, err := os.ReadFile(cfg.LegalReview.NormativyPath)
		if err != nil {
			slog.Error("legal_review enabled but normativy file is unreadable",
				"path", cfg.LegalReview.NormativyPath, "error", err)
			os.Exit(1)
		}
		extractOpts := []extraction.Option{
			extraction.WithLocal(extraction.NewLocalExtractor()),
			extraction.WithVisionModel(cfg.LLM.Vision.Model),
		}
		// CAD reader: optional, and deliberately loud when configured but broken —
		// silently falling back to vision on drawings is how a misread dimension
		// ends up in a legal conclusion.
		if cfg.LegalReview.CADPython != "" {
			cad, err := extraction.NewDWGExtractor(cfg.LegalReview.CADPython, cfg.LegalReview.CADScript)
			if err != nil {
				slog.Error("dwg reader configured but unusable", "error", err)
				os.Exit(1)
			}
			extractOpts = append(extractOpts, extraction.WithCADExtractor(cad))
			slog.Info("dwg reader enabled", "python", cfg.LegalReview.CADPython, "script", cfg.LegalReview.CADScript)
		}
		if cfg.LegalReview.OfficeScript != "" {
			office, err := extraction.NewOfficeExtractor(cfg.LegalReview.CADPython, cfg.LegalReview.OfficeScript)
			if err != nil {
				slog.Error("office reader configured but unusable", "error", err)
				os.Exit(1)
			}
			extractOpts = append(extractOpts, extraction.WithOfficeExtractor(office))
			slog.Info("office reader enabled", "script", cfg.LegalReview.OfficeScript)
		}
		extractRouter := extraction.NewRouter(llmClient, extractOpts...)
		runner := subagent.NewRunner(chatLLM, registry)
		worker := legalreview.NewDigestWorker(runner, cfg.LegalReview.DigestModel, cfg.LegalReview.DigestMaxChars)
		coord := legalreview.NewCoordinator(runner,
			cfg.LegalReview.CoordinatorModel, cfg.LegalReview.ReduceModel,
			string(normativy), cfg.LegalReview.CoordinatorMaxInputTokens)
		orch := legalreview.NewOrchestrator(extractRouter, worker, coord, cfg.LegalReview.Concurrency)
		bot.EnableLegalReview(orch, cfg.LegalReview.MaxFiles, collectors...)
		slog.Info("legal-review pipeline enabled",
			"max_files", cfg.LegalReview.MaxFiles, "storages", len(collectors))
	}

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

	// Mail courier: attachments land in Drive, the digest goes to Telegram, and
	// the review itself waits for an explicit "разбери папку".
	if mailCourier != nil {
		mailCourier.SetNotify(bot.SendToOwner)
		go mailCourier.Run(ctx)
	}

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
