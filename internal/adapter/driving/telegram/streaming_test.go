package telegram

import (
	"errors"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// stubSender replays a scripted sequence of results, one per Send call.
type stubSender struct {
	results []error
	calls   int
}

func (s *stubSender) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	i := s.calls
	s.calls++
	if i < len(s.results) && s.results[i] != nil {
		return tgbotapi.Message{}, s.results[i]
	}
	return tgbotapi.Message{MessageID: 100 + i}, nil
}

// connReset mirrors what the Telegram client returns when the keep-alive
// connection is dropped mid-request: a transport error, not an API error.
func connReset() error {
	return &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/botXXX/editMessageText",
		Err: &net.OpError{Op: "read", Err: syscall.ECONNRESET},
	}
}

func newTestStream(bot messageSender) *DraftStream {
	d := NewDraftStream(bot, 42, StreamPartial)
	d.messageID = 7 // pretend a draft placeholder already exists
	d.retryDelay = time.Millisecond
	return d
}

func TestFinalizeRetriesAfterConnectionReset(t *testing.T) {
	bot := &stubSender{results: []error{connReset()}}
	d := newTestStream(bot)

	if err := d.Finalize("готовый ответ"); err != nil {
		t.Fatalf("expected the retry to deliver the answer, got error: %v", err)
	}
	if bot.calls != 2 {
		t.Errorf("expected 2 send attempts (1 failed + 1 retry), got %d", bot.calls)
	}
}

func TestFinalizeRetriesUntilExhausted(t *testing.T) {
	bot := &stubSender{results: []error{connReset(), connReset(), connReset()}}
	d := newTestStream(bot)

	if err := d.Finalize("готовый ответ"); err == nil {
		t.Fatal("expected an error once every attempt failed")
	}
	if bot.calls != maxSendAttempts {
		t.Errorf("expected %d attempts, got %d", maxSendAttempts, bot.calls)
	}
}

func TestFinalizeRetriesOnRateLimit(t *testing.T) {
	bot := &stubSender{results: []error{&tgbotapi.Error{Code: 429, Message: "Too Many Requests"}}}
	d := newTestStream(bot)

	if err := d.Finalize("готовый ответ"); err != nil {
		t.Fatalf("429 should be retried, got error: %v", err)
	}
	if bot.calls != 2 {
		t.Errorf("expected 2 attempts, got %d", bot.calls)
	}
}

// A malformed request fails identically on every attempt — retrying only delays
// the inevitable and holds the stream lock for nothing.
func TestFinalizeDoesNotRetryBadRequest(t *testing.T) {
	bot := &stubSender{results: []error{&tgbotapi.Error{Code: 400, Message: "Bad Request: can't parse entities"}}}
	d := newTestStream(bot)

	if err := d.Finalize("готовый ответ"); err == nil {
		t.Fatal("expected the 400 to be returned")
	}
	if bot.calls != 1 {
		t.Errorf("expected a single attempt for a 400, got %d", bot.calls)
	}
}

func TestFinalizeSucceedsFirstTry(t *testing.T) {
	bot := &stubSender{}
	d := newTestStream(bot)

	if err := d.Finalize("готовый ответ"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bot.calls != 1 {
		t.Errorf("expected exactly 1 attempt, got %d", bot.calls)
	}
}

// The chunks after the first are plain sends; a reset there loses part of a long
// answer just as surely as a failed finalize loses a short one.
func TestSendChunkedRetriesLaterChunks(t *testing.T) {
	long := make([]rune, 9000)
	for i := range long {
		long[i] = 'я'
	}

	bot := &stubSender{results: []error{nil, connReset()}}
	d := newTestStream(bot)

	if err := d.SendChunked(string(long)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3 chunks at 4000 runes each; the second attempt of chunk 2 makes 4 calls.
	if bot.calls != 4 {
		t.Errorf("expected 4 send attempts (3 chunks + 1 retry), got %d", bot.calls)
	}
}

func TestIsRetryableClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"connection reset", connReset(), true},
		{"unexpected EOF", &url.Error{Op: "Post", Err: errors.New("unexpected EOF")}, true},
		{"broken pipe", &url.Error{Op: "Post", Err: &net.OpError{Op: "write", Err: syscall.EPIPE}}, true},
		{"rate limited", &tgbotapi.Error{Code: 429}, true},
		{"telegram down", &tgbotapi.Error{Code: 502}, true},
		{"bad request", &tgbotapi.Error{Code: 400}, false},
		{"forbidden", &tgbotapi.Error{Code: 403}, false},
		{"no error", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryable(tc.err); got != tc.want {
				t.Errorf("isRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
