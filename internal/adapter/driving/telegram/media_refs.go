package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/olegmatyakubov/go-assistant/internal/domain/valueobject"
	"github.com/olegmatyakubov/go-assistant/internal/port/input"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

// Telegram delivers an album as separate messages a few milliseconds apart;
// wait this long after the last one before treating the album as complete.
const albumSettle = 1500 * time.Millisecond

// Seedance accepts reference clips of 2..15 seconds.
const (
	minRefClip = 2
	maxRefClip = 15
)

// album gathers the messages of one Telegram media group.
type album struct {
	chatID  int64
	userID  int64
	caption string
	// pending counts items still downloading; the album is not flushed until
	// they land, or a slow download would split it into two answers.
	pending int
	timer   *time.Timer
}

// mediaRefs is the reference-collecting mode: pictures and GIFs are stashed
// instead of being answered one by one, so several of them can feed a single
// generation. Off (nil) on instances without video generation.
type mediaRefs struct {
	pool   *refPool
	mu     sync.Mutex
	albums map[string]*album
}

// EnableMediaRefs switches photos, albums and GIFs to reference collection.
// Call once after NewBot, only on instances that generate videos.
func (b *Bot) EnableMediaRefs() {
	pool := newRefPool(time.Now).persistTo(filepath.Join(b.filesDir, "ref_pool.json"))
	b.media = &mediaRefs{pool: pool, albums: make(map[string]*album)}
}

// handleMediaMessage stashes a photo, GIF, video or round video note. It reports false
// when the message carries none of them, or the mode is off.
func (b *Bot) handleMediaMessage(msg *tgbotapi.Message) bool {
	if b.media == nil || !hasReferenceMedia(msg) {
		return false
	}
	inAlbum := msg.MediaGroupID != ""
	if inAlbum {
		b.albumItemStarted(msg)
	}

	path, kind, err := b.downloadReference(msg)
	if err != nil {
		slog.Error("failed to save reference", "error", err)
		b.sendText(msg.Chat.ID, "Не смог сохранить вложение: "+err.Error())
	} else {
		b.stashReference(msg.From.ID, path, kind)
	}

	if inAlbum {
		b.albumItemDone(msg.MediaGroupID)
		return true
	}
	if err == nil {
		b.afterReferences(msg.Chat.ID, msg.From.ID, msg.Caption)
	}
	return true
}

func hasReferenceMedia(msg *tgbotapi.Message) bool {
	return len(msg.Photo) > 0 || msg.Animation != nil || msg.Video != nil || msg.VideoNote != nil
}

// downloadReference saves the photo, GIF or video of a message and names its kind.
func (b *Bot) downloadReference(msg *tgbotapi.Message) (path, kind string, err error) {
	stamp := time.Now().UnixNano()
	var (
		fileID   string
		duration int
	)
	switch {
	case len(msg.Photo) > 0:
		photo := msg.Photo[len(msg.Photo)-1]
		path, err = b.downloadFile(photo.FileID, fmt.Sprintf("ref_%d.jpg", stamp))
		return path, "photo", err
	case msg.Animation != nil:
		// Telegram re-encodes GIFs to silent mp4 on upload.
		fileID, duration, kind = msg.Animation.FileID, msg.Animation.Duration, "GIF"
	case msg.VideoNote != nil:
		fileID, duration, kind = msg.VideoNote.FileID, msg.VideoNote.Duration, "round video note of the user"
	default:
		fileID, duration, kind = msg.Video.FileID, msg.Video.Duration, "video"
	}
	path, err = b.downloadFile(fileID, fmt.Sprintf("ref_%d.mp4", stamp))
	if err != nil {
		return "", kind, err
	}
	return fitReferenceClip(path, duration), kind, nil
}

// stashReference adds a download to the pool. A clip contributes a still
// frame: the video gateway takes pictures only, never clips.
func (b *Bot) stashReference(userID int64, path, kind string) {
	if kind == "photo" {
		b.media.pool.add(userID, path, false)
		return
	}
	b.media.pool.addItem(userID, refItem{path: path, video: true, kind: kind})
	if frame := stillFrame(path); frame != "" {
		b.media.pool.addItem(userID, refItem{path: frame, kind: "photo", frameOf: path})
	}
}

func (b *Bot) albumItemStarted(msg *tgbotapi.Message) {
	b.media.mu.Lock()
	defer b.media.mu.Unlock()

	group := msg.MediaGroupID
	a, ok := b.media.albums[group]
	if !ok {
		a = &album{chatID: msg.Chat.ID, userID: msg.From.ID}
		b.media.albums[group] = a
		a.timer = time.AfterFunc(albumSettle, func() { b.flushAlbum(group) })
	}
	a.pending++
	// Telegram puts the album caption on one item, usually the first.
	if msg.Caption != "" {
		a.caption = msg.Caption
	}
}

func (b *Bot) albumItemDone(group string) {
	b.media.mu.Lock()
	defer b.media.mu.Unlock()
	if a, ok := b.media.albums[group]; ok {
		a.pending--
		a.timer.Reset(albumSettle)
	}
}

func (b *Bot) flushAlbum(group string) {
	b.media.mu.Lock()
	a, ok := b.media.albums[group]
	if ok && a.pending > 0 {
		a.timer.Reset(albumSettle)
		b.media.mu.Unlock()
		return
	}
	delete(b.media.albums, group)
	b.media.mu.Unlock()
	if ok {
		b.afterReferences(a.chatID, a.userID, a.caption)
	}
}

// afterReferences runs once per photo or album: with a caption the caption is
// the task, without one the bot only confirms and waits for instructions
// (typed or spoken), so three photos don't trigger three separate answers.
func (b *Bot) afterReferences(chatID, userID int64, caption string) {
	if strings.TrimSpace(caption) == "" {
		images, videos := b.media.pool.count(userID)
		b.sendText(chatID, referencesAck(images, videos))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	stream := NewDraftStream(b.api, chatID, b.streamMode)
	stream.SendDraft("Thinking...")
	b.processWithMedia(ctx, stream, chatID, userID, caption)
}

func referencesAck(images, videos int) string {
	var parts []string
	if images > 0 {
		parts = append(parts, fmt.Sprintf("картинок: %d", images))
	}
	if videos > 0 {
		parts = append(parts, fmt.Sprintf("гифок/видео: %d", videos))
	}
	return "Принял. Референсы сейчас — " + strings.Join(parts, ", ") +
		". Докидывай ещё или скажи, что сделать (видео, правка, разбор)."
}

// processWithMedia sends one request with the not-yet-seen pictures attached
// and every live reference available to the tools.
func (b *Bot) processWithMedia(ctx context.Context, stream *DraftStream, chatID, userID int64, content string) {
	var images []output.ImageContent
	current := ""
	for _, path := range b.media.pool.takeUnshown(userID) {
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("reference vanished", "path", path, "error", err)
			continue
		}
		images = append(images, output.ImageContent{Base64: base64Encode(data), MimeType: "image/jpeg"})
		current = path
	}

	if inv := b.media.pool.inventory(userID); inv != "" {
		content += "\n\n[Pictures available to generate_image (images param) and generate_video, cite them exactly like this: " + inv + "]"
	}

	ctx, sink := b.withMedia(ctx, chatID, userID, current)
	sessionKey := valueobject.NewSessionKey("telegram", fmt.Sprintf("dm:%d", userID))
	resp, err := b.chatService.ProcessMessage(ctx, input.ChatRequest{
		SessionKey: sessionKey,
		Content:    content,
		Images:     images,
		OnProgress: func(status string) { stream.UpdateDraft(status) },
	})
	if err != nil {
		slog.Error("pipeline error", "error", err)
		stream.Finalize("Error: " + err.Error())
		return
	}
	stream.SendChunked(resp.Content)
	b.sendGeneratedImages(chatID, userID, sink)
}

// withMedia extends withImages with the reference pool and a way to post a
// finished video later. Without the mode it is plain withImages.
func (b *Bot) withMedia(ctx context.Context, chatID, userID int64, current string) (context.Context, *imgctx.Sink) {
	ctx, sink := b.withImages(ctx, userID, current)
	if b.media == nil {
		return ctx, sink
	}
	refs := b.media.pool.refs(userID)
	refs.Used = func() { b.media.pool.markUsed(userID) }
	ctx = imgctx.WithRefs(ctx, refs)
	ctx = imgctx.WithVideoDelivery(ctx, func(path string, err error) { b.deliverVideo(chatID, path, err) })
	return ctx, sink
}

func (b *Bot) deliverVideo(chatID int64, path string, err error) {
	if err != nil {
		b.sendText(chatID, "Видео не получилось: "+err.Error())
		return
	}
	video := tgbotapi.NewVideo(chatID, tgbotapi.FilePath(path))
	video.SupportsStreaming = true
	if _, err := b.api.Send(video); err != nil {
		slog.Error("failed to send video", "error", err, "path", path)
		b.sendFile(chatID, path, "")
		return
	}
	slog.Info("video delivered", "chat_id", chatID, "path", path)
}

// fitReferenceClip re-encodes a clip into what the gateway digests: at most
// 720 px, silent, 2..15 s (short GIFs are looped, long videos cut). Raw phone
// clips are megabytes of base64 and the submit times out on them. Without
// ffmpeg the clip goes as is and the gateway decides.
func fitReferenceClip(path string, duration int) string {
	var args []string
	target := min(max(duration, minRefClip), maxRefClip)
	if duration < minRefClip {
		target = 3
		args = append(args, "-stream_loop", "-1")
	}
	out := strings.TrimSuffix(path, ".mp4") + "_fit.mp4"
	args = append(args, "-y", "-i", path, "-t", fmt.Sprint(target), "-an",
		"-vf", "scale='min(720,iw)':'min(720,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2",
		"-c:v", "libx264", "-crf", "28", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-movflags", "+faststart", out)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if raw, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		slog.Warn("could not fit reference clip", "path", path, "error", err, "output", truncate(string(raw), 300))
		return path
	}
	return out
}

// stillFrame cuts one frame from the first second of a clip, where the subject
// is usually facing the camera. Empty on failure: the clip alone still works.
func stillFrame(clip string) string {
	out := strings.TrimSuffix(clip, ".mp4") + "_still.jpg"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "0.8", "-i", clip,
		"-frames:v", "1", "-q:v", "3", out).CombinedOutput()
	if err != nil {
		slog.Warn("could not cut still frame", "clip", clip, "error", err, "output", truncate(string(raw), 300))
		return ""
	}
	return out
}
