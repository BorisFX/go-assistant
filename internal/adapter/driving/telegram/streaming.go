package telegram

import (
	"strings"
	"sync"
	"time"

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

	msg := tgbotapi.NewMessage(d.chatID, text)
	msg.ParseMode = "Markdown"

	sent, err := d.bot.Send(msg)
	if err != nil {
		msg.ParseMode = ""
		sent, err = d.bot.Send(msg)
		if err != nil {
			return err
		}
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

	edit := tgbotapi.NewEditMessageText(d.chatID, d.messageID, text)
	edit.ParseMode = "Markdown"

	if _, err := d.bot.Send(edit); err != nil {
		edit.ParseMode = ""
		d.bot.Send(edit)
	}

	d.lastSent = time.Now()
	return nil
}

func (d *DraftStream) Finalize(text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.messageID == 0 {
		msg := tgbotapi.NewMessage(d.chatID, text)
		msg.ParseMode = "Markdown"
		if _, err := d.bot.Send(msg); err != nil {
			msg.ParseMode = ""
			d.bot.Send(msg)
		}
		return nil
	}

	edit := tgbotapi.NewEditMessageText(d.chatID, d.messageID, text)
	edit.ParseMode = "Markdown"

	if _, err := d.bot.Send(edit); err != nil {
		edit.ParseMode = ""
		d.bot.Send(edit)
	}

	return nil
}

func (d *DraftStream) SendChunked(text string) error {
	const maxLen = 4000

	if len(text) <= maxLen {
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
				d.bot.Send(msg)
			}
		}
	}
	return nil
}

func splitText(text string, maxLen int) []string {
	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		idx := strings.LastIndex(text[:maxLen], "\n")
		if idx <= 0 {
			idx = maxLen
		}

		chunks = append(chunks, text[:idx])
		text = text[idx:]
	}
	return chunks
}
