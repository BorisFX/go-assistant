// Package a6image draws images through an OpenAI-compatible images API (a6api).
package a6image

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultBaseURL = "https://a6.a6api.com/v1"

// Image models take their time — a single generation runs tens of seconds, and
// an edit of a detailed photo longer. Stay under the 5-minute chat deadline.
const requestTimeout = 3 * time.Minute

// Attempts per model before moving on to the fallback.
const maxAttempts = 3

type Client struct {
	apiKey     string
	model      string
	fallback   string
	baseURL    string
	size       string
	outDir     string
	httpClient *http.Client
}

// New builds an image client. fallback is tried when the primary model fails —
// image models refuse edits of real people often enough that a second, more
// permissive model is what keeps the feature usable. Pass "" to disable it.
func New(apiKey, model, fallback, baseURL, outDir string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey:     apiKey,
		model:      model,
		fallback:   fallback,
		baseURL:    baseURL,
		size:       "1024x1024",
		outDir:     outDir,
		httpClient: &http.Client{Timeout: requestTimeout},
	}
}

// WithSize overrides the requested image size (default 1024x1024).
func (c *Client) WithSize(size string) *Client {
	if size != "" {
		c.size = size
	}
	return c
}

func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	return c.withModels(ctx, "generate", func(ctx context.Context, model string) (*http.Request, error) {
		body, err := json.Marshal(map[string]any{
			"model":  model,
			"prompt": prompt,
			"n":      1,
			"size":   c.size,
		})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/images/generations", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
}

func (c *Client) Edit(ctx context.Context, imagePath, prompt string) (string, error) {
	source, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("read source image: %w", err)
	}

	return c.withModels(ctx, "edit", func(ctx context.Context, model string) (*http.Request, error) {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		if err := form.WriteField("model", model); err != nil {
			return nil, err
		}
		if err := form.WriteField("prompt", prompt); err != nil {
			return nil, err
		}
		part, err := form.CreateFormFile("image", filepath.Base(imagePath))
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(source); err != nil {
			return nil, err
		}
		if err := form.Close(); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/images/edits", bytes.NewReader(body.Bytes()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", form.FormDataContentType())
		return req, nil
	})
}

// withModels runs build+send for the primary model, then the fallback.
func (c *Client) withModels(ctx context.Context, op string, build func(context.Context, string) (*http.Request, error)) (string, error) {
	models := []string{c.model}
	if c.fallback != "" && c.fallback != c.model {
		models = append(models, c.fallback)
	}

	var lastErr error
	for _, model := range models {
		for attempt := 0; attempt < maxAttempts; attempt++ {
			req, err := build(ctx, model)
			if err != nil {
				return "", fmt.Errorf("build %s request: %w", op, err)
			}
			req.Header.Set("Authorization", "Bearer "+c.apiKey)

			path, err := c.send(req)
			if err == nil {
				slog.Info("image produced", "op", op, "model", model, "path", path)
				return path, nil
			}
			lastErr = err

			// A dead gateway origin is not the model refusing — retry it rather
			// than burning the fallback, and redial so the retry can land on a
			// different origin than the pinned keep-alive connection.
			if !isTransient(err) || attempt == maxAttempts-1 {
				slog.Warn("image request failed", "op", op, "model", model, "error", err)
				break
			}
			slog.Warn("image request failed, retrying", "op", op, "model", model, "attempt", attempt+1, "error", err)
			c.httpClient.CloseIdleConnections()
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}

	return "", lastErr
}

// isTransient reports whether the failure is the gateway's, not the model's.
func isTransient(err error) bool {
	msg := err.Error()
	for _, marker := range []string{"status 5", "status 429", "request:"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

type imageResponse struct {
	Data []struct {
		URL     string `json:"url"`
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) send(req *http.Request) (string, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var parsed imageResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("status %d, unparseable response: %s", resp.StatusCode, truncate(string(raw)))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, parsed.Error.Message)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(raw)))
	}
	if len(parsed.Data) == 0 {
		return "", fmt.Errorf("no image in response: %s", truncate(string(raw)))
	}

	// The same model answers sometimes with a hosted URL, sometimes inline base64.
	item := parsed.Data[0]
	switch {
	case item.B64JSON != "":
		data, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			return "", fmt.Errorf("decode base64 image: %w", err)
		}
		return c.save(data)
	case item.URL != "":
		data, err := c.download(req.Context(), item.URL)
		if err != nil {
			return "", err
		}
		return c.save(data)
	default:
		return "", fmt.Errorf("image entry has neither url nor b64_json")
	}
}

func (c *Client) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download image: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) save(data []byte) (string, error) {
	if err := os.MkdirAll(c.outDir, 0755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	path := filepath.Join(c.outDir, fmt.Sprintf("image_%d.png", time.Now().UnixNano()))
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("save image: %w", err)
	}
	return path, nil
}

func truncate(s string) string {
	const max = 300
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
