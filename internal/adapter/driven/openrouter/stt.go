package openrouter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sttURL = "https://openrouter.ai/api/v1/audio/transcriptions"

type STTClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewSTTClient(apiKey string) *STTClient {
	return &STTClient{
		apiKey: apiKey,
		model:  "openai/whisper-large-v3",
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

type sttInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type sttRequest struct {
	Model      string        `json:"model"`
	InputAudio sttInputAudio `json:"input_audio"`
	Language   string        `json:"language,omitempty"`
}

type sttResponse struct {
	Text string `json:"text"`
}

func (c *STTClient) Transcribe(ctx context.Context, filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	// Detect format from extension
	ext := strings.ToLower(filepath.Ext(filePath))
	format := "ogg"
	switch ext {
	case ".mp3":
		format = "mp3"
	case ".wav":
		format = "wav"
	case ".m4a":
		format = "m4a"
	case ".flac":
		format = "flac"
	case ".oga", ".ogg":
		format = "ogg"
	case ".webm":
		format = "webm"
	}

	reqBody := sttRequest{
		Model: c.model,
		InputAudio: sttInputAudio{
			Data:   encoded,
			Format: format,
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", sttURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("STT error %d: %s", resp.StatusCode, string(body))
	}

	var result sttResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	return result.Text, nil
}
