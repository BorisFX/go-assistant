package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/claudecode"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/cryptoai"
	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/openrouter"
	"github.com/olegmatyakubov/go-assistant/internal/app/cron"
	"github.com/olegmatyakubov/go-assistant/internal/app/memory"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
)

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
	ownerID      int64
	allowedUsers []int64
	filesDir     string
	streamMode   StreamMode
	cancel       context.CancelFunc
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

func (b *Bot) authorize(update tgbotapi.Update) bool {
	if update.Message == nil {
		return false
	}
	userID := update.Message.From.ID
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

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := b.api.GetUpdatesChan(u)

	slog.Info("telegram bot started", "owner_id", b.ownerID)

	for {
		select {
		case <-ctx.Done():
			slog.Info("telegram bot stopping")
			b.api.StopReceivingUpdates()
			return nil
		case update := <-updates:
			b.watchdog.Touch()
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
