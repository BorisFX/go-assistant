package telegram

import (
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

func TestParseDraftAction(t *testing.T) {
	cases := []struct {
		data   string
		action string
		id     string
		ok     bool
	}{
		{"gmail:send:r-123", "send", "r-123", true},
		{"gmail:drop:r-123", "drop", "r-123", true},
		{"gmail:send:", "", "", false},
		{"gmail:delete:r-123", "", "", false}, // unknown verb, not a send
		{"gmail:send", "", "", false},
		{"cron:send:r-123", "", "", false},
		{"", "", "", false},
	}

	for _, tc := range cases {
		action, id, ok := parseDraftAction(tc.data)
		if ok != tc.ok || action != tc.action || id != tc.id {
			t.Errorf("parseDraftAction(%q) = %q %q %v; want %q %q %v",
				tc.data, action, id, ok, tc.action, tc.id, tc.ok)
		}
	}
}

// The card must say the letter is still unsent and carry both buttons: the only
// way outward is a deliberate press.
func TestDraftCard(t *testing.T) {
	text, keyboard := draftCard(gworkspace.DraftInfo{
		ID: "r-7", To: "p@example.com", Subject: "Запрос КП", Snippet: "Добрый день",
	})

	if !strings.Contains(text, "НЕ отправлен") {
		t.Errorf("card text: %q", text)
	}
	if !strings.Contains(text, "p@example.com") || !strings.Contains(text, "Запрос КП") {
		t.Errorf("card omits the essentials: %q", text)
	}

	row := keyboard.InlineKeyboard[0]
	if len(row) != 2 {
		t.Fatalf("expected send and discard buttons, got %d", len(row))
	}
	for _, want := range []string{"gmail:send:r-7", "gmail:drop:r-7"} {
		found := false
		for _, btn := range row {
			if btn.CallbackData != nil && *btn.CallbackData == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no button carrying %q", want)
		}
	}
}

// Telegram caps callback data at 64 bytes; a card whose buttons exceed it is
// rejected at send time, and the user would be left unable to confirm.
func TestDraftCardCallbackDataFitsTelegramLimit(t *testing.T) {
	_, keyboard := draftCard(gworkspace.DraftInfo{ID: strings.Repeat("r", 40), Subject: "Тема"})

	for _, btn := range keyboard.InlineKeyboard[0] {
		if btn.CallbackData == nil {
			t.Fatal("button without callback data")
		}
		if len(*btn.CallbackData) > 64 {
			t.Errorf("callback data is %d bytes: %q", len(*btn.CallbackData), *btn.CallbackData)
		}
	}
}

func TestAuthorizeUser(t *testing.T) {
	b := &Bot{ownerID: 42, allowedUsers: []int64{7}}

	for id, want := range map[int64]bool{42: true, 7: true, 99: false, 0: false} {
		if got := b.authorizeUser(id); got != want {
			t.Errorf("authorizeUser(%d) = %v, want %v", id, got, want)
		}
	}
}

func TestCardSummary(t *testing.T) {
	cq := &tgbotapi.CallbackQuery{Message: &tgbotapi.Message{
		Text: "✉️ Черновик готов, НЕ отправлен\nКому: p@example.com\nТема: Запрос КП\n",
	}}
	if got := cardSummary(cq); got != "Запрос КП" {
		t.Errorf("cardSummary = %q", got)
	}
	if got := cardSummary(&tgbotapi.CallbackQuery{}); got != "" {
		t.Errorf("cardSummary without a message = %q", got)
	}
}

func TestParseDraftActionAcceptsBatchVerbs(t *testing.T) {
	for _, data := range []string{"gmail:sendall:7", "gmail:dropall:7"} {
		action, id, ok := parseDraftAction(data)
		if !ok || id != "7" || (action != "sendall" && action != "dropall") {
			t.Errorf("parseDraftAction(%q) = %q %q %v", data, action, id, ok)
		}
	}
}

// A mailing must go out once. The second tap on the same card finds nothing.
func TestTakeBatchIsSingleUse(t *testing.T) {
	b := &Bot{batches: map[string][]batchItem{"1": {{ID: "r1"}, {ID: "r2"}}}}

	ids, ok := b.takeBatch("1")
	if !ok || len(ids) != 2 {
		t.Fatalf("first take: %v %v", ids, ok)
	}
	if _, ok := b.takeBatch("1"); ok {
		t.Error("the same batch was handed out twice")
	}
	if _, ok := b.takeBatch("нет такой"); ok {
		t.Error("unknown batch must not resolve")
	}
}
