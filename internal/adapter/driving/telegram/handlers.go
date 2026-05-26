package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/olegmatyakubov/go-assistant/internal/domain/valueobject"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

func (b *Bot) handleUpdate(update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	if !b.authorize(update) {
		slog.Warn("unauthorized access attempt", "user_id", update.Message.From.ID)
		return
	}

	if !b.debouncer.Allow(update.Message.Chat.ID) {
		return
	}

	if update.Message.IsCommand() {
		b.handleCommand(update.Message)
		return
	}

	// Voice message
	if update.Message.Voice != nil {
		b.handleVoiceMessage(update.Message)
		return
	}

	// Document
	if update.Message.Document != nil {
		b.handleDocumentMessage(update.Message)
		return
	}

	// Photo (take largest)
	if update.Message.Photo != nil && len(update.Message.Photo) > 0 {
		b.handlePhotoMessage(update.Message)
		return
	}

	// Text
	if update.Message.Text != "" {
		b.handleTextMessage(update.Message)
		return
	}
}

// downloadFile downloads a Telegram file to local disk
func (b *Bot) downloadFile(fileID, filename string) (string, error) {
	downloadDir := b.filesDir
	os.MkdirAll(downloadDir, 0755)

	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}

	fileURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", b.api.Token, file.FilePath)

	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	ext := filepath.Ext(file.FilePath)
	if filename == "" {
		filename = fmt.Sprintf("%d%s", time.Now().UnixMilli(), ext)
	}

	localPath := filepath.Join(downloadDir, filename)
	out, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return localPath, nil
}

// sendFile sends a file from local disk to Telegram chat
func (b *Bot) sendFile(chatID int64, filePath, caption string) {
	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
	if caption != "" {
		doc.Caption = caption
	}
	if _, err := b.api.Send(doc); err != nil {
		slog.Error("failed to send file", "error", err, "path", filePath)
	}
}

func (b *Bot) handleVoiceMessage(msg *tgbotapi.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	chatID := msg.Chat.ID
	stream := NewDraftStream(b.api, chatID, b.streamMode)
	stream.SendDraft("Transcribing voice...")

	// Download voice file
	localPath, err := b.downloadFile(msg.Voice.FileID, "")
	if err != nil {
		stream.Finalize("Failed to download voice: " + err.Error())
		return
	}

	// Transcribe via OpenRouter Whisper API
	if b.sttClient == nil {
		stream.Finalize("Speech-to-text not configured")
		return
	}

	transcribeResult, err := b.sttClient.Transcribe(ctx, localPath)
	if err != nil {
		stream.Finalize("Transcription failed: " + err.Error())
		return
	}

	if transcribeResult == "" {
		stream.Finalize("Could not transcribe audio")
		return
	}

	stream.UpdateDraft("Processing: " + truncate(transcribeResult, 50) + "...")

	// Process transcribed text through pipeline
	sessionKey := valueobject.NewSessionKey("telegram", fmt.Sprintf("dm:%d", msg.From.ID))
	resp, err := b.chatService.ProcessMessage(ctx, input.ChatRequest{
		SessionKey: sessionKey,
		Content:    transcribeResult,
		OnProgress: func(status string) { stream.UpdateDraft(status) },
	})

	if err != nil {
		stream.Finalize("Error: " + err.Error())
		return
	}

	stream.SendChunked(resp.Content)
}

func (b *Bot) handleDocumentMessage(msg *tgbotapi.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	chatID := msg.Chat.ID
	stream := NewDraftStream(b.api, chatID, b.streamMode)

	// Telegram Bot API limit: 20MB for file downloads
	if msg.Document.FileSize > 20*1024*1024 {
		stream.Finalize(fmt.Sprintf(
			"Файл слишком большой (%dМБ). Telegram ограничивает ботов до 20МБ.\n\n"+
				"Загрузите файл в Mail.ru Cloud в папку объекта, потом напишите:\n"+
				"\"скачай и проанализируй [название файла] из [папка объекта]\"",
			msg.Document.FileSize/(1024*1024)))
		return
	}
	stream.SendDraft("Receiving file...")

	localPath, err := b.downloadFile(msg.Document.FileID, msg.Document.FileName)
	if err != nil {
		stream.Finalize("Failed to download: " + err.Error())
		return
	}

	caption := msg.Caption
	if caption == "" {
		caption = fmt.Sprintf("Analyze the file I sent: %s", msg.Document.FileName)
	}

	var images []output.ImageContent
	content := ""

	if isImageFile(msg.Document.FileName, msg.Document.MimeType) {
		// Image document — send to vision model
		imgData, err := os.ReadFile(localPath)
		if err == nil {
			mimeType := msg.Document.MimeType
			if mimeType == "" {
				mimeType = "image/jpeg"
			}
			images = append(images, output.ImageContent{
				Base64:   base64Encode(imgData),
				MimeType: mimeType,
			})
		}
	} else if isTextFile(msg.Document.FileName, msg.Document.MimeType) {
		data, err := os.ReadFile(localPath)
		if err == nil && len(data) < 10000 {
			content = fmt.Sprintf("[File: %s]\n```\n%s\n```", msg.Document.FileName, string(data))
		}
	} else {
		content = fmt.Sprintf("[File saved: %s (%s, %d bytes)]", msg.Document.FileName, msg.Document.MimeType, msg.Document.FileSize)
	}

	sessionKey := valueobject.NewSessionKey("telegram", fmt.Sprintf("dm:%d", msg.From.ID))
	resp, err := b.chatService.ProcessMessage(ctx, input.ChatRequest{
		SessionKey: sessionKey,
		Content:    caption + "\n" + content,
		Images:     images,
		OnProgress: func(status string) { stream.UpdateDraft(status) },
	})

	if err != nil {
		stream.Finalize("Error: " + err.Error())
		return
	}

	stream.SendChunked(resp.Content)
}

func (b *Bot) handlePhotoMessage(msg *tgbotapi.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	chatID := msg.Chat.ID
	stream := NewDraftStream(b.api, chatID, b.streamMode)
	stream.SendDraft("Analyzing image...")

	// Get largest photo
	photo := msg.Photo[len(msg.Photo)-1]

	localPath, err := b.downloadFile(photo.FileID, fmt.Sprintf("photo_%d.jpg", time.Now().UnixMilli()))
	if err != nil {
		stream.Finalize("Failed to download photo: " + err.Error())
		return
	}

	// Read file as base64
	imgData, err := os.ReadFile(localPath)
	if err != nil {
		stream.Finalize("Failed to read photo: " + err.Error())
		return
	}

	base64Img := base64Encode(imgData)
	mimeType := "image/jpeg"
	if strings.HasSuffix(localPath, ".png") {
		mimeType = "image/png"
	}

	caption := msg.Caption
	if caption == "" {
		caption = "Analyze this image in detail. What do you see?"
	}

	sessionKey := valueobject.NewSessionKey("telegram", fmt.Sprintf("dm:%d", msg.From.ID))
	resp, err := b.chatService.ProcessMessage(ctx, input.ChatRequest{
		SessionKey: sessionKey,
		Content:    caption,
		Images: []output.ImageContent{
			{Base64: base64Img, MimeType: mimeType},
		},
		OnProgress: func(status string) { stream.UpdateDraft(status) },
	})

	if err != nil {
		stream.Finalize("Error: " + err.Error())
		return
	}

	stream.SendChunked(resp.Content)
}


func isTextFile(filename, mimeType string) bool {
	textMimes := []string{"text/", "application/json", "application/xml", "application/yaml", "application/toml", "application/javascript"}
	for _, t := range textMimes {
		if strings.HasPrefix(mimeType, t) {
			return true
		}
	}
	textExts := []string{".txt", ".md", ".json", ".yaml", ".yml", ".toml", ".xml", ".csv", ".go", ".py", ".js", ".ts", ".sh", ".sql", ".html", ".css", ".cs", ".java", ".rs", ".log", ".env", ".cfg", ".ini", ".conf"}
	ext := strings.ToLower(filepath.Ext(filename))
	for _, e := range textExts {
		if ext == e {
			return true
		}
	}
	return false
}

func isImageFile(filename, mimeType string) bool {
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	imgExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff"}
	ext := strings.ToLower(filepath.Ext(filename))
	for _, e := range imgExts {
		if ext == e {
			return true
		}
	}
	return false
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (b *Bot) handleTextMessage(msg *tgbotapi.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	chatID := msg.Chat.ID

	stream := NewDraftStream(b.api, chatID, b.streamMode)
	stream.SendDraft("Thinking...")

	sessionKey := valueobject.NewSessionKey("telegram", fmt.Sprintf("dm:%d", msg.From.ID))

	resp, err := b.chatService.ProcessMessage(ctx, input.ChatRequest{
		SessionKey: sessionKey,
		Content:    msg.Text,
		OnProgress: func(status string) {
			stream.UpdateDraft(status)
		},
	})

	if err != nil {
		slog.Error("pipeline error", "error", err)
		stream.Finalize("Error: " + err.Error())
		return
	}

	stream.SendChunked(resp.Content)
}

func (b *Bot) handleCommand(msg *tgbotapi.Message) {
	ctx := context.Background()

	switch msg.Command() {
	case "start", "help":
		b.sendHelp(msg.Chat.ID)
	case "status":
		b.handleStatusCommand(ctx, msg.Chat.ID)
	case "code":
		b.handleCodeCommand(ctx, msg)
	case "memory":
		b.handleMemoryCommand(ctx, msg.Chat.ID)
	case "cron":
		b.handleCronCommand(ctx, msg)
	default:
		reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Unknown command: /%s", msg.Command()))
		b.api.Send(reply)
	}
}

func (b *Bot) sendHelp(chatID int64) {
	text := `*Go Assistant*

/status — trading bot status
/code <prompt> — run Claude Code
/memory — what I remember
/cron — manage scheduled tasks
/help — this message

*Cron usage:*
/cron list — show all tasks
/cron add every 1h | check trading bot status
/cron add every 30m | check BTC price
/cron add daily | find new .NET remote jobs $4k+
/cron del 2 — delete task #2

Or just send me any message!`

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

func (b *Bot) handleMemoryCommand(ctx context.Context, chatID int64) {
	if b.memorySvc == nil {
		msg := tgbotapi.NewMessage(chatID, "Memory system not configured")
		b.api.Send(msg)
		return
	}

	memories, err := b.memorySvc.ListAll(ctx, 20, 0)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Failed to load memories: "+err.Error())
		b.api.Send(msg)
		return
	}

	if len(memories) == 0 {
		msg := tgbotapi.NewMessage(chatID, "No memories stored yet.")
		b.api.Send(msg)
		return
	}

	var text strings.Builder
	text.WriteString("*Memory*\n\n")

	for _, m := range memories {
		icon := "📝"
		if m.Type == "fact" {
			icon = "📌"
		} else if m.Type == "event" {
			icon = "📊"
		}
		text.WriteString(fmt.Sprintf("%s [%s] %s\n", icon, m.Type, m.Content))
	}

	reply := tgbotapi.NewMessage(chatID, text.String())
	reply.ParseMode = "Markdown"
	b.api.Send(reply)
}

func (b *Bot) handleCronCommand(ctx context.Context, msg *tgbotapi.Message) {
	if b.cronScheduler == nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Cron scheduler not configured")
		b.api.Send(reply)
		return
	}

	args := strings.TrimSpace(msg.CommandArguments())

	// /cron or /cron list
	if args == "" || args == "list" {
		jobs, err := b.cronScheduler.List(ctx)
		if err != nil {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Error: "+err.Error())
			b.api.Send(reply)
			return
		}
		if len(jobs) == 0 {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "No scheduled tasks.\n\nAdd one:\n/cron add every 1h | check bot status")
			b.api.Send(reply)
			return
		}

		var text strings.Builder
		text.WriteString("*Scheduled Tasks*\n\n")
		for i, j := range jobs {
			status := "on"
			if !j.Enabled {
				status = "off"
			}
			lastRun := "never"
			if j.LastRunAt != nil {
				lastRun = j.LastRunAt.Format("15:04")
			}
			fmt.Fprintf(&text, "%d. [%s] *%s*\n   %s\n   Schedule: %s | Last: %s\n\n",
				i+1, status, j.Name, j.Prompt, j.Schedule, lastRun)
		}
		text.WriteString("Delete: /cron del <number>")

		reply := tgbotapi.NewMessage(msg.Chat.ID, text.String())
		reply.ParseMode = "Markdown"
		b.api.Send(reply)
		return
	}

	// /cron add every 1h | check bot status
	if strings.HasPrefix(args, "add ") {
		parts := strings.SplitN(args[4:], "|", 2)
		if len(parts) != 2 {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Format: /cron add <schedule> | <prompt>\nExample: /cron add every 1h | check trading bot status")
			b.api.Send(reply)
			return
		}

		schedule := strings.TrimSpace(parts[0])
		prompt := strings.TrimSpace(parts[1])

		// Use first few words of prompt as name
		name := prompt
		if len(name) > 40 {
			name = name[:40] + "..."
		}

		job, err := b.cronScheduler.Add(ctx, name, prompt, schedule)
		if err != nil {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Error: "+err.Error())
			b.api.Send(reply)
			return
		}

		text := fmt.Sprintf("Cron added: *%s*\nSchedule: %s\nNext run: %s",
			job.Name, job.Schedule, job.NextRunAt.Format("15:04:05"))
		reply := tgbotapi.NewMessage(msg.Chat.ID, text)
		reply.ParseMode = "Markdown"
		b.api.Send(reply)
		return
	}

	// /cron del 2
	if strings.HasPrefix(args, "del ") || strings.HasPrefix(args, "delete ") {
		numStr := strings.TrimPrefix(args, "del ")
		numStr = strings.TrimPrefix(numStr, "delete ")
		numStr = strings.TrimSpace(numStr)

		num, err := strconv.Atoi(numStr)
		if err != nil {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Usage: /cron del <number>\nUse /cron list to see numbers.")
			b.api.Send(reply)
			return
		}

		if err := b.cronScheduler.Delete(ctx, num); err != nil {
			reply := tgbotapi.NewMessage(msg.Chat.ID, "Error: "+err.Error())
			b.api.Send(reply)
			return
		}

		reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("Cron #%d deleted.", num))
		b.api.Send(reply)
		return
	}

	reply := tgbotapi.NewMessage(msg.Chat.ID, "Unknown cron command. Use: list, add, del")
	b.api.Send(reply)
}

func (b *Bot) handleCodeCommand(ctx context.Context, msg *tgbotapi.Message) {
	args := msg.CommandArguments()
	if args == "" {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Usage: /code <prompt>\nExample: /code fix the login bug in auth.go")
		b.api.Send(reply)
		return
	}

	if b.codeExecutor == nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Code executor not configured")
		b.api.Send(reply)
		return
	}

	stream := NewDraftStream(b.api, msg.Chat.ID, b.streamMode)
	stream.SendDraft("Starting Claude Code...")

	result, err := b.codeExecutor.Execute(ctx, args, "", func(progress string) {
		stream.UpdateDraft(progress)
	})

	if err != nil {
		stream.Finalize("Error: " + err.Error())
		return
	}

	output := result.Output
	if result.Error != "" {
		output += "\n\nError: " + result.Error
	}

	stream.SendChunked(output)
}

func (b *Bot) handleStatusCommand(ctx context.Context, chatID int64) {
	if b.tradingClient == nil {
		msg := tgbotapi.NewMessage(chatID, "Trading monitor not configured")
		b.api.Send(msg)
		return
	}

	status, err := b.tradingClient.GetStatus(ctx)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Failed to get status: "+err.Error())
		b.api.Send(msg)
		return
	}

	text := fmt.Sprintf(`*Trading Bot Status*
Balance: $%.2f USDT
Open Positions: %d
Total P&L: $%.2f
Today P&L: $%.2f
Active Symbols: %d
Bot Running: %v`,
		status.Balance, status.OpenPositions,
		status.TotalPnL, status.TodayPnL,
		status.ActiveSymbols, status.BotRunning,
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}
