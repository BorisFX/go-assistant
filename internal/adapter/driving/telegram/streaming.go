package telegram

import (
	"errors"
	"log/slog"
	"net/http"
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

// maxSendAttempts bounds delivery retries. Telegram's keep-alive connections are
// dropped every few hours; without a retry the generated answer is lost and the
// user is left staring at an unfinished draft.
const maxSendAttempts = 3

// messageSender is the part of tgbotapi.BotAPI the stream depends on, named so
// retry behaviour is testable without a live Telegram connection.
type messageSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}

type DraftStream struct {
	chatID     int64
	messageID  int
	mode       StreamMode
	bot        messageSender
	buffer     strings.Builder
	mu         sync.Mutex
	ticker     *time.Ticker
	lastSent   time.Time
	minDelay   time.Duration
	retryDelay time.Duration
}

func NewDraftStream(bot messageSender, chatID int64, mode StreamMode) *DraftStream {
	return &DraftStream{
		chatID:     chatID,
		mode:       mode,
		bot:        bot,
		minDelay:   2 * time.Second,
		retryDelay: time.Second,
	}
}

// isRetryable separates failures worth another attempt from ones that will fail
// identically forever. A malformed request (4xx) is our bug; a dropped
// connection, a rate limit or a Telegram-side 5xx is not.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *tgbotapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= 500
	}

	// Never reached the API: reset, timeout, DNS, EOF. Worth retrying.
	return true
}

// sendWithRetry delivers c, backing off between attempts. It honours Telegram's
// own retry_after hint when the API supplies one.
func (d *DraftStream) sendWithRetry(c tgbotapi.Chattable, op string) error {
	delay := d.retryDelay
	if delay <= 0 {
		delay = time.Second
	}

	var err error
	for attempt := 1; attempt <= maxSendAttempts; attempt++ {
		if _, err = d.bot.Send(c); err == nil {
			if attempt > 1 {
				slog.Info("telegram send recovered", "op", op, "attempt", attempt)
			}
			return nil
		}

		if !isRetryable(err) || attempt == maxSendAttempts {
			break
		}

		wait := delay
		var apiErr *tgbotapi.Error
		if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
			wait = time.Duration(apiErr.RetryAfter) * time.Second
		}

		slog.Warn("telegram send failed, retrying", "op", op, "attempt", attempt, "wait", wait, "err", err)
		time.Sleep(wait)
		delay *= 2
	}

	return err
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
		if err := d.sendWithRetry(msg, "finalize send"); err != nil {
			slog.Error("finalize send failed", "err", err)
			return err
		}
		return nil
	}

	edit := tgbotapi.NewEditMessageText(d.chatID, d.messageID, text)
	if err := d.sendWithRetry(edit, "finalize edit"); err != nil {
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
			// A surviving error here means Telegram rejected the markup itself
			// (transport failures are already retried), so drop the formatting.
			if err := d.sendWithRetry(msg, "chunk"); err != nil {
				msg.ParseMode = ""
				if err2 := d.sendWithRetry(msg, "chunk plain"); err2 != nil {
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
