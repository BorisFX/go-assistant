package telegram

import (
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type StreamMode string

const (
	StreamOff      StreamMode = "off"
	StreamPartial  StreamMode = "partial"
	StreamProgress StreamMode = "progress"
	StreamTool     StreamMode = "tool"
)

type DraftStream struct {
	chatID    int64
	messageID int
	mode      StreamMode
	bot       *tgbotapi.BotAPI
	buffer    strings.Builder
	mu        sync.Mutex
	ticker    *time.Ticker
	lastSent  time.Time
	minDelay  time.Duration
}

func NewDraftStream(bot *tgbotapi.BotAPI, chatID int64, mode StreamMode) *DraftStream {
	return &DraftStream{
		chatID:   chatID,
		mode:     mode,
		bot:      bot,
		minDelay: 2 * time.Second,
	}
}

func (d *DraftStream) SendDraft(text string) error {
	if d.mode == StreamOff {
		return nil
	}

	msg := tgbotapi.NewMessage(d.chatID, formatForTelegram(text))

	sent, err := d.bot.Send(msg)
	if err != nil {
		return err
	}

	d.mu.Lock()
	d.messageID = sent.MessageID
	d.lastSent = time.Now()
	d.mu.Unlock()

	return nil
}

func (d *DraftStream) UpdateDraft(text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == StreamOff || d.messageID == 0 {
		return nil
	}

	// Throttle edits to avoid Telegram rate limit, but always allow if text changed significantly
	if time.Since(d.lastSent) < d.minDelay {
		// Still allow if this is a status change (different from buffer)
		if d.buffer.String() == text {
			return nil
		}
	}
	d.buffer.Reset()
	d.buffer.WriteString(text)

	edit := tgbotapi.NewEditMessageText(d.chatID, d.messageID, formatForTelegram(text))
	d.bot.Send(edit)

	d.lastSent = time.Now()
	return nil
}

func (d *DraftStream) Finalize(text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Telegram rejects empty message text; send a visible placeholder instead of failing silently.
	if strings.TrimSpace(text) == "" {
		slog.Warn("finalize: empty response, sending placeholder")
		text = "(empty response)"
	}

	text = formatForTelegram(text)

	if d.messageID == 0 {
		msg := tgbotapi.NewMessage(d.chatID, text)
		if _, err := d.bot.Send(msg); err != nil {
			slog.Error("finalize send failed", "err", err)
			return err
		}
		return nil
	}

	edit := tgbotapi.NewEditMessageText(d.chatID, d.messageID, text)
	if _, err := d.bot.Send(edit); err != nil {
		slog.Error("finalize edit failed", "err", err)
		return err
	}

	return nil
}

func (d *DraftStream) SendChunked(text string) error {
	const maxLen = 4000

	// Count runes, not bytes — Telegram's limit is in characters, and byte-length
	// over-counts multi-byte text, triggering needless (and buggy) splits.
	if utf8.RuneCountInString(text) <= maxLen {
		return d.Finalize(text)
	}

	chunks := splitText(text, maxLen)
	for i, chunk := range chunks {
		if i == 0 && d.messageID != 0 {
			d.Finalize(chunk)
		} else {
			msg := tgbotapi.NewMessage(d.chatID, chunk)
			msg.ParseMode = "Markdown"
			if _, err := d.bot.Send(msg); err != nil {
				msg.ParseMode = ""
				if _, err2 := d.bot.Send(msg); err2 != nil {
					slog.Error("sendChunked send failed", "chunk", i, "markdown_err", err, "plain_err", err2)
				}
			}
		}
	}
	return nil
}

// splitText splits on rune boundaries so multi-byte characters (e.g. Cyrillic)
// are never cut in half — a cut mid-rune produces invalid UTF-8 that Telegram rejects.
func splitText(text string, maxLen int) []string {
	var chunks []string
	runes := []rune(text)
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		window := string(runes[:maxLen])
		cut := maxLen
		if idx := strings.LastIndex(window, "\n"); idx > 0 {
			cut = utf8.RuneCountInString(window[:idx])
		}
		if cut <= 0 {
			cut = maxLen
		}

		chunks = append(chunks, string(runes[:cut]))
		runes = runes[cut:]
	}
	return chunks
}
