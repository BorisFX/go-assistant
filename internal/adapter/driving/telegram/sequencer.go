package telegram

import (
	"log/slog"
	"runtime/debug"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type ChatSequencer struct {
	mu       sync.Mutex
	channels map[int64]chan tgbotapi.Update
	handler  func(tgbotapi.Update)
}

func NewChatSequencer(handler func(tgbotapi.Update)) *ChatSequencer {
	return &ChatSequencer{
		channels: make(map[int64]chan tgbotapi.Update),
		handler:  handler,
	}
}

func (s *ChatSequencer) Dispatch(update tgbotapi.Update) {
	chatID := getChatID(update)
	if chatID == 0 {
		return
	}

	s.mu.Lock()
	ch, exists := s.channels[chatID]
	if !exists {
		ch = make(chan tgbotapi.Update, 100)
		s.channels[chatID] = ch
		go s.worker(chatID, ch)
	}
	s.mu.Unlock()

	ch <- update
}

func (s *ChatSequencer) worker(chatID int64, ch chan tgbotapi.Update) {
	for update := range ch {
		s.handle(chatID, update)
	}
}

// handle isolates each update so a panic kills only this message, not the whole
// bot process (an unrecovered goroutine panic would crash every chat).
func (s *ChatSequencer) handle(chatID int64, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in chat handler",
				"chat_id", chatID,
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()
	s.handler(update)
}

func getChatID(update tgbotapi.Update) int64 {
	if update.Message != nil {
		return update.Message.Chat.ID
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
		return update.CallbackQuery.Message.Chat.ID
	}
	return 0
}
