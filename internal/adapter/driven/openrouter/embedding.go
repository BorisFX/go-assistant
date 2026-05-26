package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type EmbeddingClient struct {
	apiKey     string
	model      string
	url        string
	httpClient *http.Client
}

func NewEmbeddingClient(apiKey, model, url string) *EmbeddingClient {
	if url == "" {
		url = "https://openrouter.ai/api/v1/embeddings"
	}
	return &EmbeddingClient{
		apiKey:     apiKey,
		model:      model,
		url:        url,
		httpClient: &http.Client{},
	}
}

type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type EmbeddingResponse struct {
	Data []EmbeddingData `json:"data"`
}

type EmbeddingData struct {
	Embedding []float32 `json:"embedding"`
}

func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	reqBody := EmbeddingRequest{
		Model: c.model,
		Input: text,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding error %d: %s", resp.StatusCode, string(body))
	}

	var embResp EmbeddingResponse
	if err := json.Unmarshal(body, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(embResp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings in response")
	}

	return embResp.Data[0].Embedding, nil
}
