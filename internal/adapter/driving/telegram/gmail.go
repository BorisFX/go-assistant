package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	gworkspace "github.com/olegmatyakubov/go-assistant/internal/adapter/driven/google"
)

// gmailSender is the outgoing half of the mailbox. Every method here runs only
// after an explicit human action: the assistant itself can prepare a draft, but
// a letter to a counterparty cannot be recalled, so pressing send stays manual.
type gmailSender interface {
	SendDraft(ctx context.Context, draftID string) (gworkspace.Message, error)
	DeleteDraft(ctx context.Context, draftID string) error
	ListDrafts(ctx context.Context, limit int) ([]gworkspace.DraftInfo, error)
}

// EnableGmail turns on draft confirmation for this instance. Call once after
// NewBot, only when google.gmail.enabled is set.
func (b *Bot) EnableGmail(client gmailSender) { b.gmail = client }

const (
	draftSendAction = "send"
	draftDropAction = "drop"
	batchSendAction = "sendall"
	batchDropAction = "dropall"
	draftPrefix     = "gmail:"
)

// parseDraftAction reads callback data of the form "gmail:send:<draft id>".
// Callback data is a 64-byte field controlled by whatever Telegram delivers, so
// anything unexpected is rejected rather than interpreted.
func parseDraftAction(data string) (action, draftID string, ok bool) {
	rest, found := strings.CutPrefix(data, draftPrefix)
	if !found {
		return "", "", false
	}
	action, draftID, found = strings.Cut(rest, ":")
	if !found || draftID == "" {
		return "", "", false
	}
	switch action {
	case draftSendAction, draftDropAction, batchSendAction, batchDropAction:
	default:
		return "", "", false
	}
	return action, draftID, true
}

// draftCard renders the confirmation. The body is not included: the letter was
// composed in the chat a moment ago, and repeating it buries the buttons.
func draftCard(info gworkspace.DraftInfo) (string, tgbotapi.InlineKeyboardMarkup) {
	var b strings.Builder
	b.WriteString("✉️ Черновик готов, НЕ отправлен\n")
	if info.To != "" {
		fmt.Fprintf(&b, "Кому: %s\n", info.To)
	}
	if info.Subject != "" {
		fmt.Fprintf(&b, "Тема: %s\n", info.Subject)
	}
	if info.Snippet != "" {
		fmt.Fprintf(&b, "\n%s\n", info.Snippet)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Отправить", draftPrefix+draftSendAction+":"+info.ID),
		tgbotapi.NewInlineKeyboardButtonData("Удалить", draftPrefix+draftDropAction+":"+info.ID),
	))
	return b.String(), keyboard
}

// SendDraftCard shows a prepared letter to the owner with send and discard
// buttons. Wired to the gmail tool, so the confirmation appears by itself.
func (b *Bot) SendDraftCard(info gworkspace.DraftInfo) {
	text, keyboard := draftCard(info)
	msg := tgbotapi.NewMessage(b.ownerID, text)
	msg.ReplyMarkup = keyboard
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("failed to send draft card", "draft_id", info.ID, "error", err)
	}
}

// handleDraftCallback runs a button press. Authorisation is checked here too:
// a callback carries its own sender and never went through handleUpdate's
// message checks.
func (b *Bot) handleDraftCallback(cq *tgbotapi.CallbackQuery) {
	if cq.From == nil || !b.authorizeUser(cq.From.ID) {
		slog.Warn("unauthorized callback", "user_id", callbackUserID(cq))
		return
	}
	action, draftID, ok := parseDraftAction(cq.Data)
	if !ok {
		b.answerCallback(cq.ID, "Непонятная кнопка")
		return
	}
	if b.gmail == nil {
		b.answerCallback(cq.ID, "Почта не настроена")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	switch action {
	case batchSendAction, batchDropAction:
		b.handleBatch(ctx, cq, action, draftID)
	case draftSendAction:
		sent, err := b.gmail.SendDraft(ctx, draftID)
		if err != nil {
			slog.Error("failed to send draft", "draft_id", draftID, "error", err)
			b.answerCallback(cq.ID, "Не отправлено")
			b.replaceCard(cq, "❌ Не отправлено: "+err.Error())
			return
		}
		slog.Info("draft sent", "draft_id", draftID, "message_id", sent.ID)
		b.answerCallback(cq.ID, "Отправлено")
		b.replaceCard(cq, "✅ Отправлено: "+cardSummary(cq))
	case draftDropAction:
		if err := b.gmail.DeleteDraft(ctx, draftID); err != nil {
			slog.Error("failed to delete draft", "draft_id", draftID, "error", err)
			b.answerCallback(cq.ID, "Не удалось удалить")
			return
		}
		b.answerCallback(cq.ID, "Удалено")
		b.replaceCard(cq, "🗑 Черновик удалён: "+cardSummary(cq))
	}
}

// replaceCard rewrites the card in place, which both reports the outcome and
// removes the buttons — a second press must not send the letter twice.
func (b *Bot) replaceCard(cq *tgbotapi.CallbackQuery, text string) {
	if cq.Message == nil {
		return
	}
	edit := tgbotapi.NewEditMessageText(cq.Message.Chat.ID, cq.Message.MessageID, text)
	if _, err := b.api.Send(edit); err != nil {
		slog.Warn("failed to update draft card", "error", err)
	}
}

func (b *Bot) answerCallback(id, text string) {
	if _, err := b.api.Request(tgbotapi.NewCallback(id, text)); err != nil {
		slog.Warn("failed to answer callback", "error", err)
	}
}

// cardSummary reuses the subject line already shown, so the result message
// still says which letter it was about.
func cardSummary(cq *tgbotapi.CallbackQuery) string {
	if cq.Message == nil {
		return ""
	}
	for _, line := range strings.Split(cq.Message.Text, "\n") {
		if subject, ok := strings.CutPrefix(line, "Тема: "); ok {
			return subject
		}
	}
	return ""
}

func callbackUserID(cq *tgbotapi.CallbackQuery) int64 {
	if cq.From == nil {
		return 0
	}
	return cq.From.ID
}

// handleDraftsCommand lists pending drafts with their buttons. It is the way
// back to a confirmation whose card scrolled out of the chat.
func (b *Bot) handleDraftsCommand(ctx context.Context, chatID int64) {
	if b.gmail == nil {
		b.sendText(chatID, "Почта не настроена для этого инстанса.")
		return
	}
	drafts, err := b.gmail.ListDrafts(ctx, 10)
	if err != nil {
		b.sendText(chatID, "Не удалось получить черновики: "+err.Error())
		return
	}
	if len(drafts) == 0 {
		b.sendText(chatID, "Черновиков нет.")
		return
	}
	for _, d := range drafts {
		text, keyboard := draftCard(d)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ReplyMarkup = keyboard
		if _, err := b.api.Send(msg); err != nil {
			slog.Warn("failed to list draft", "draft_id", d.ID, "error", err)
		}
	}
}

// RfqCard is a prepared tender mailing: one draft per contractor, waiting for a
// single confirmation. The batch lives in memory only — if the bot restarts, the
// drafts stay in Gmail and /drafts brings them back one by one.
type RfqCard struct {
	Group       string
	Subject     string
	Drafts      []gworkspace.DraftInfo
	Skipped     []string
	Attachments []string
	Warning     string
}

// SendRfqCard shows the mailing with one button for the whole batch. Sending 12
// letters one confirmation at a time is how someone taps "send" on autopilot.
func (b *Bot) SendRfqCard(card RfqCard) {
	if len(card.Drafts) == 0 {
		return
	}
	items := make([]batchItem, 0, len(card.Drafts))
	for _, d := range card.Drafts {
		items = append(items, batchItem{ID: d.ID, To: d.To})
	}

	b.batchMu.Lock()
	b.batchSeq++
	batchID := strconv.Itoa(b.batchSeq)
	if b.batches == nil {
		b.batches = map[string][]batchItem{}
	}
	b.batches[batchID] = items
	b.batchMu.Unlock()

	var text strings.Builder
	fmt.Fprintf(&text, "📨 Рассылка по группе «%s»\nТема: %s\nПисем: %d — каждому отдельно, без общей копии\n",
		card.Group, card.Subject, len(card.Drafts))
	for _, d := range card.Drafts {
		fmt.Fprintf(&text, "• %s\n", d.To)
	}
	// Что именно приложено — видно здесь, до нажатия кнопки: текст письма может
	// обещать ТЗ, которого в письме нет.
	if len(card.Attachments) > 0 {
		fmt.Fprintf(&text, "Вложения: %s\n", strings.Join(card.Attachments, ", "))
	} else {
		text.WriteString("Вложений нет\n")
	}
	if card.Warning != "" {
		fmt.Fprintf(&text, "⚠ %s\n", card.Warning)
	}
	if len(card.Skipped) > 0 {
		fmt.Fprintf(&text, "\nБез почты, письмо не уйдёт: %s\n", strings.Join(card.Skipped, ", "))
	}
	text.WriteString("\nНичего не отправлено.")

	msg := tgbotapi.NewMessage(b.ownerID, text.String())
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("Отправить все (%d)", len(items)), draftPrefix+batchSendAction+":"+batchID),
		tgbotapi.NewInlineKeyboardButtonData("Удалить все", draftPrefix+batchDropAction+":"+batchID),
	))
	if _, err := b.api.Send(msg); err != nil {
		slog.Error("failed to send rfq card", "group", card.Group, "error", err)
	}
}

// batchItem is one letter of a mailing: the draft and who it goes to.
type batchItem struct {
	ID string
	To string
}

const (
	// sendPause разводит отправки во времени — Gmail не любит пачку подряд.
	sendPause = 400 * time.Millisecond
	// retryPause даёт API прийти в себя перед единственным повтором.
	retryPause = 3 * time.Second
)

func (b *Bot) runBatchItem(ctx context.Context, action, draftID string) error {
	if action == batchSendAction {
		_, err := b.gmail.SendDraft(ctx, draftID)
		return err
	}
	return b.gmail.DeleteDraft(ctx, draftID)
}

func orUnknown(to string) string {
	if to == "" {
		return "адрес неизвестен"
	}
	return to
}

// takeBatch hands out a batch exactly once: a second tap on the same card must
// not resend the mailing, and the card is edited away right after.
func (b *Bot) takeBatch(batchID string) ([]batchItem, bool) {
	b.batchMu.Lock()
	defer b.batchMu.Unlock()
	ids, ok := b.batches[batchID]
	delete(b.batches, batchID)
	return ids, ok
}

// handleBatch runs one press over the whole mailing. A failure on one letter
// does not stop the rest: the report says how many went out.
func (b *Bot) handleBatch(ctx context.Context, cq *tgbotapi.CallbackQuery, action, batchID string) {
	items, ok := b.takeBatch(batchID)
	if !ok {
		b.answerCallback(cq.ID, "Рассылка уже обработана")
		b.replaceCard(cq, "Рассылка уже обработана (или бот перезапускался — смотрите /drafts)")
		return
	}

	var done int
	var failedTo []string
	for i, item := range items {
		if i > 0 && action == batchSendAction {
			// Gmail отбивает пачку отправок «Precondition check failed», если
			// бить в него без пауз: на девятнадцати письмах последнее не ушло.
			time.Sleep(sendPause)
		}
		err := b.runBatchItem(ctx, action, item.ID)
		if err != nil {
			slog.Warn("batch action failed, повторяю", "action", action, "draft_id", item.ID, "error", err)
			time.Sleep(retryPause)
			err = b.runBatchItem(ctx, action, item.ID)
		}
		if err != nil {
			slog.Error("batch action failed", "action", action, "draft_id", item.ID, "error", err)
			failedTo = append(failedTo, orUnknown(item.To))
			continue
		}
		done++
	}

	verb := "Отправлено"
	if action == batchDropAction {
		verb = "Удалено"
	}
	result := fmt.Sprintf("%s %d из %d", verb, done, len(items))
	if len(failedTo) > 0 {
		// Кому именно не ушло — иначе не понять, кого добивать вручную.
		result += fmt.Sprintf("\nНе получилось (%d): %s\nЭти письма остались черновиками, их видно в /drafts",
			len(failedTo), strings.Join(failedTo, ", "))
	}
	slog.Info("rfq batch handled", "action", action, "done", done, "failed", len(failedTo))
	b.answerCallback(cq.ID, result)
	b.replaceCard(cq, "📨 "+result)
}
