package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/claudecode"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/cryptoai"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/openrouter"
	"github.com/olegmatyakubov/go-assistant/internal/app/cron"
	"github.com/olegmatyakubov/go-assistant/internal/app/legalreview"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
)

// FolderCollector gathers a folder's documents from a storage into local files.
// Mail.ru Cloud holds the archive, Drive holds everything the mail courier
// delivers, and "разбери папку" must work over either.
type FolderCollector interface {
	CollectFolder(ctx context.Context, sub string, exts []string, maxFiles int) ([]string, error)
}

// legalReviewDeps holds the optional legal-document-review pipeline. A nil
// *legalReviewDeps on the Bot means the feature is off (Oleg's instance).
type legalReviewDeps struct {
	orch       *legalreview.Orchestrator
	collectors []FolderCollector
	maxFiles   int
	// Optional: where the finished PDF goes besides the chat, and where the
	// run is recorded. Either may be nil.
	sink   ReportSink
	store  legalreview.RunStore
	models struct{ Digest, Coordinator string }
	// normativyHash pins every report to the rule set it was checked against.
	normativyHash string
}

// ReportSink stores a finished report next to the reviewed documents. Drive
// implements it; the Mail.ru archive does not, and a nil sink is fine.
type ReportSink interface {
	UploadReport(ctx context.Context, folder, name string, data []byte) (string, error)
}

// ReviewOptions carries the optional legal-review dependencies so the bot's
// constructor signature stays put as the feature grows.
type ReviewOptions struct {
	Sink             ReportSink
	Store            legalreview.RunStore
	DigestModel      string
	CoordinatorModel string
	NormativyHash    string
}

type Bot struct {
	api           *tgbotapi.BotAPI
	chatService   input.ChatService
	tradingClient *cryptoai.Client
	codeExecutor  *claudecode.Executor
	sttClient     *openrouter.STTClient
	memorySvc     *memory.Service
	cronScheduler *cron.Scheduler
	sequencer     *ChatSequencer
	debouncer     *Debouncer
	watchdog      *PollingWatchdog
	lastImages    *imageMemory
	ownerID       int64
	allowedUsers  []int64
	filesDir      string
	streamMode    StreamMode
	cancel        context.CancelFunc
	legalReview   *legalReviewDeps
	gmail         gmailSender
	batches       map[string][]batchItem
	batchMu       sync.Mutex
	batchSeq      int
}

type BotConfig struct {
	Token           string
	OwnerID         int64
	AllowedUsers    []int64
	FilesDir        string
	StreamMode      StreamMode
	WatchdogTimeout time.Duration
	DebounceDelay   time.Duration
}

func NewBot(
	cfg BotConfig,
	chatService input.ChatService,
	tradingClient *cryptoai.Client,
	codeExecutor *claudecode.Executor,
	sttClient *openrouter.STTClient,
	memorySvc *memory.Service,
	cronScheduler *cron.Scheduler,
) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("create bot api: %w", err)
	}

	slog.Info("telegram bot authorized", "username", api.Self.UserName)

	b := &Bot{
		api:           api,
		lastImages:    newImageMemory(time.Now),
		chatService:   chatService,
		tradingClient: tradingClient,
		codeExecutor:  codeExecutor,
		sttClient:     sttClient,
		memorySvc:     memorySvc,
		cronScheduler: cronScheduler,
		ownerID:       cfg.OwnerID,
		allowedUsers:  cfg.AllowedUsers,
		filesDir:      cfg.FilesDir,
		streamMode:    cfg.StreamMode,
	}

	b.debouncer = NewDebouncer(cfg.DebounceDelay)
	b.sequencer = NewChatSequencer(b.handleUpdate)
	b.watchdog = NewPollingWatchdog(cfg.WatchdogTimeout, func() {
		slog.Warn("watchdog: restarting polling")
	})

	return b, nil
}

// EnableLegalReview turns on the legal-document-review pipeline for this bot
// instance. Call once after NewBot, only when cfg.LegalReview.Enabled.
func (b *Bot) EnableLegalReview(orch *legalreview.Orchestrator, maxFiles int, collectors ...FolderCollector) {
	b.legalReview = &legalReviewDeps{orch: orch, collectors: collectors, maxFiles: maxFiles}
}

// SetReviewOptions attaches the report sink, run store and provenance to an
// already-enabled legal review. No-op when the feature is off.
func (b *Bot) SetReviewOptions(opts ReviewOptions) {
	if b.legalReview == nil {
		return
	}
	b.legalReview.sink = opts.Sink
	b.legalReview.store = opts.Store
	b.legalReview.models.Digest = opts.DigestModel
	b.legalReview.models.Coordinator = opts.CoordinatorModel
	b.legalReview.normativyHash = opts.NormativyHash
}

func (b *Bot) authorize(update tgbotapi.Update) bool {
	if update.Message == nil {
		return false
	}
	return b.authorizeUser(update.Message.From.ID)
}

// authorizeUser is the check itself, shared by messages and button presses: a
// callback carries its own sender and skips every message-level guard.
func (b *Bot) authorizeUser(userID int64) bool {
	if userID == b.ownerID {
		return true
	}
	for _, id := range b.allowedUsers {
		if userID == id {
			return true
		}
	}
	return false
}

func (b *Bot) Start(ctx context.Context) error {
	ctx, b.cancel = context.WithCancel(ctx)

	go b.watchdog.Run(ctx)

	slog.Info("telegram bot started", "owner_id", b.ownerID)

	offset := 0
	for {
		select {
		case <-ctx.Done():
			slog.Info("telegram bot stopping")
			return nil
		default:
		}

		u := tgbotapi.NewUpdate(offset)
		u.Timeout = 30

		updates, err := b.api.GetUpdates(u)
		if err != nil {
			slog.Warn("getUpdates failed", "err", err)
			time.Sleep(time.Second)
			continue
		}

		// A successful poll (even an empty one) proves polling is alive.
		b.watchdog.Touch()

		for _, update := range updates {
			offset = update.UpdateID + 1
			b.sequencer.Dispatch(update)
		}
	}
}

func (b *Bot) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *Bot) SendToOwner(text string) {
	msg := tgbotapi.NewMessage(b.ownerID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.api.Send(msg); err != nil {
		msg.ParseMode = ""
		b.api.Send(msg)
	}
}
