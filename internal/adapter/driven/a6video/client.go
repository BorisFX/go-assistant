// Package a6video renders clips through the a6api video task API (/v1/videos).
//
// Generation is asynchronous on the gateway: submit returns a task id, the task
// is polled until it completes, then the mp4 is fetched from /videos/{id}/content.
// The gateway honours exactly one picture, passed as input_reference (a data
// URI); it becomes the opening frame. metadata.content references are accepted,
// billed and silently dropped, and arrays or repeated parts are rejected —
// verified 2026-10-14 — so callers blend several references into one frame.
package a6video

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

const defaultBaseURL = "https://a6.a6api.com/v1"

// Gateway suppliers only list 720p for the cheap Seedance tiers; a 480p request
// is rejected with video_spec_no_supplier.
const defaultSize = "1280x720"

// Seedance accepts 4..15 seconds per clip; billing is per second.
const (
	minSeconds     = 4
	maxSeconds     = 15
	defaultSeconds = 5
)

const (
	submitAttempts = 3
	pollInterval   = 10 * time.Second
)

var ErrTaskFailed = errors.New("video task failed")

// ErrSupplierBusy means no supplier took the task and the prepaid charge was
// refunded. The text goes to the user as is.
var ErrSupplierBusy = errors.New("поставщик видео сейчас не принял задачу, деньги не списаны — попробуй ещё раз через пару минут")

// Gateway error codes meaning "nobody took the task, nothing charged, try
// again later" — the gateway itself advises a delayed resend for them.
var busyCodes = map[string]bool{
	"video_submit_upstream_acceptance_unknown": true,
	"upstream_unavailable":                     true,
	"video_candidate_incompatible":             true,
}

type Client struct {
	apiKey       string
	model        string
	baseURL      string
	size         string
	outDir       string
	pollInterval time.Duration
	busyDelay    time.Duration
	httpClient   *http.Client
}

func New(apiKey, model, baseURL, outDir string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:       apiKey,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		size:         defaultSize,
		outDir:       outDir,
		pollInterval: pollInterval,
		busyDelay:    30 * time.Second,
		// Per-request bound; the whole generation is bounded by the caller's ctx.
		httpClient: &http.Client{Timeout: 3 * time.Minute},
	}
}

// WithSize overrides the requested output size (default 1280x720).
func (c *Client) WithSize(size string) *Client {
	if size != "" {
		c.size = size
	}
	return c
}

// WithPollInterval is for tests: it shortens polling and the busy-supplier
// backoff (10 s and 30 s in production).
func (c *Client) WithPollInterval(d time.Duration) *Client {
	c.pollInterval = d
	c.busyDelay = d
	return c
}

func (c *Client) Model() string { return c.model }

// ClampSeconds normalises a requested duration to what the model accepts.
func ClampSeconds(s int) int {
	switch {
	case s <= 0:
		return defaultSeconds
	case s < minSeconds:
		return minSeconds
	case s > maxSeconds:
		return maxSeconds
	}
	return s
}

func (c *Client) GenerateVideo(ctx context.Context, req output.VideoRequest) (string, error) {
	body, err := c.buildBody(req)
	if err != nil {
		return "", err
	}

	id, err := c.submit(ctx, body)
	if err != nil {
		return "", err
	}
	slog.Info("video task submitted", "id", id, "model", c.model,
		"start_image", req.StartImage != "", "seconds", ClampSeconds(req.Seconds))

	if err := c.wait(ctx, id); err != nil {
		return "", err
	}
	return c.fetch(ctx, id)
}

func (c *Client) buildBody(req output.VideoRequest) ([]byte, error) {
	metadata := map[string]any{
		"resolution":     "720p",
		"generate_audio": req.Audio,
		"watermark":      false,
	}
	body := map[string]any{
		"model":    c.model,
		"prompt":   req.Prompt,
		"seconds":  fmt.Sprint(ClampSeconds(req.Seconds)),
		"size":     c.size,
		"metadata": metadata,
	}
	if req.StartImage != "" {
		uri, err := dataURI(req.StartImage, "image/jpeg")
		if err != nil {
			return nil, err
		}
		body["input_reference"] = uri
		// The frame dictates the shape; a ratio would fight it.
	} else if req.Ratio != "" {
		metadata["ratio"] = req.Ratio
	}
	return json.Marshal(body)
}

func dataURI(path, fallbackMime string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read reference: %w", err)
	}
	mime := http.DetectContentType(data)
	// DetectContentType knows jpeg/png/gif/webp/mp4; anything vaguer gets the caller's default.
	if !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "video/") {
		mime = fallbackMime
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

type taskResponse struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Error    *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (c *Client) submit(ctx context.Context, body []byte) (string, error) {
	var lastErr error
	for attempt := range submitAttempts {
		var task taskResponse
		status, err := c.doJSON(ctx, http.MethodPost, "/videos", body, &task)
		if err == nil && task.ID != "" {
			return task.ID, nil
		}
		if err == nil {
			err = fmt.Errorf("status %d: no task id in response", status)
		}
		lastErr = err
		delay := time.Duration(attempt+1) * 2 * time.Second
		busy := task.Error != nil && busyCodes[task.Error.Code]
		switch {
		case busy:
			// The charge was refunded; the gateway asks not to resend at once,
			// so give the supplier pool time to recover.
			lastErr = fmt.Errorf("%w (%s)", ErrSupplierBusy, task.Error.Code)
			delay = time.Duration(attempt+1) * c.busyDelay
		case status != 0 && status != http.StatusTooManyRequests && task.Error != nil:
			// Any other structured error is the request itself being refused.
			return "", fmt.Errorf("submit video: %w", lastErr)
		}
		if attempt == submitAttempts-1 {
			break
		}
		slog.Warn("video submit failed, retrying", "attempt", attempt+1, "busy", busy, "error", err)
		c.httpClient.CloseIdleConnections()
		if err := sleep(ctx, delay); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("submit video: %w", lastErr)
}

func (c *Client) wait(ctx context.Context, id string) error {
	for {
		var task taskResponse
		_, err := c.doJSON(ctx, http.MethodGet, "/videos/"+id, nil, &task)
		switch {
		case err != nil:
			// A flaky gateway poll is not a failed render; keep polling until ctx ends.
			slog.Warn("video poll failed", "id", id, "error", err)
			c.httpClient.CloseIdleConnections()
		case task.Status == "completed":
			return nil
		case task.Status == "failed" || task.Status == "cancelled":
			msg := "no reason given"
			if task.Error != nil && task.Error.Message != "" {
				msg = task.Error.Message
			}
			return fmt.Errorf("%w: %s", ErrTaskFailed, msg)
		}
		if err := sleep(ctx, c.pollInterval); err != nil {
			return fmt.Errorf("video %s not ready: %w", id, err)
		}
	}
}

func (c *Client) fetch(ctx context.Context, id string) (string, error) {
	var lastErr error
	for attempt := range submitAttempts {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/videos/"+id+"/content", nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		resp, err := c.httpClient.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case readErr != nil:
				err = readErr
			case resp.StatusCode != http.StatusOK:
				err = fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(data)))
			default:
				return c.save(data)
			}
		}
		lastErr = err
		c.httpClient.CloseIdleConnections()
		if err := sleep(ctx, time.Duration(attempt+1)*2*time.Second); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("download video %s: %w", id, lastErr)
}

// doJSON sends a request and decodes a JSON reply. The status is returned even
// on error so callers can tell a rejection from a gateway hiccup.
func (c *Client) doJSON(ctx context.Context, method, path string, body []byte, out *taskResponse) (int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.StatusCode, fmt.Errorf("status %d, unparseable response: %s", resp.StatusCode, truncate(string(raw)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := truncate(string(raw))
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return resp.StatusCode, fmt.Errorf("status %d: %s", resp.StatusCode, msg)
	}
	return resp.StatusCode, nil
}

func (c *Client) save(data []byte) (string, error) {
	if err := os.MkdirAll(c.outDir, 0755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	path := filepath.Join(c.outDir, fmt.Sprintf("video_%d.mp4", time.Now().UnixNano()))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("save video: %w", err)
	}
	return path, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func truncate(s string) string {
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
