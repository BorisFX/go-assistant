// Package azurespeech implements speech-to-text through Azure AI Speech.
package azurespeech

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// Client sends Telegram's OGG/Opus audio to Azure as 16 kHz mono WAV.
type Client struct {
	key        string
	region     string
	language   string
	httpClient *http.Client
}

func New(key, region, language string) *Client {
	if language == "" {
		language = "ru-RU"
	}
	return &Client{
		key:        key,
		region:     region,
		language:   language,
		httpClient: &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) endpoint() string {
	return fmt.Sprintf("https://%s.stt.speech.microsoft.com/speech/recognition/conversation/cognitiveservices/v1?language=%s&format=simple",
		c.region, c.language)
}

func (c *Client) Transcribe(ctx context.Context, filePath string) (string, error) {
	if c.key == "" || c.region == "" {
		return "", fmt.Errorf("Azure Speech is not configured")
	}

	wav, err := os.CreateTemp("", "assistant-voice-*.wav")
	if err != nil {
		return "", fmt.Errorf("create wav: %w", err)
	}
	wavPath := wav.Name()
	wav.Close()
	defer os.Remove(wavPath)

	// Telegram voice notes are OGG/Opus; Azure accepts WAV reliably. 30 s cap
	// on ffmpeg input is unnecessary — short voice messages take well under it.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", filePath,
		"-ac", "1", "-ar", "16000", "-f", "wav", wavPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("convert audio: %w: %s", err, output)
	}

	audio, err := os.ReadFile(wavPath)
	if err != nil {
		return "", fmt.Errorf("read wav: %w", err)
	}

	// This REST endpoint expects the WAV bytes directly, not multipart/form-data.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(audio))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", c.key)
	req.Header.Set("Content-Type", "audio/wav; codecs=audio/pcm; samplerate=16000")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Azure Speech request: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read Azure Speech response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Azure Speech error %d: %s", resp.StatusCode, responseBody)
	}

	text, err := displayText(responseBody)
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("Azure Speech returned no transcription: %s", responseBody)
	}
	return text, nil
}
