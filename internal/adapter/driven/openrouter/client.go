package openrouter

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

const baseURL = "https://openrouter.ai/api/v1/chat/completions"

type Client struct {
	apiKey     string
	model      string
	fallback   string
	httpClient *http.Client
}

func New(apiKey, model, fallback string) *Client {
	// Force HTTP/1.1 to avoid GOAWAY issues with OpenRouter
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{},
		TLSNextProto:    make(map[string]func(string, *tls.Conn) http.RoundTripper),
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
		MaxIdleConnsPerHost: 5,
	}

	return &Client{
		apiKey:   apiKey,
		model:    model,
		fallback: fallback,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   120 * time.Second,
		},
	}
}

type RequestBody struct {
	Model       string       `json:"model"`
	Messages    []APIMessage `json:"messages"`
	Tools       []APITool    `json:"tools,omitempty"`
	Plugins     []Plugin     `json:"plugins,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature float64      `json:"temperature,omitempty"`
	Stream      bool         `json:"stream,omitempty"`
}

// Plugin configures an OpenRouter request-time plugin. The only one used here is
// "file-parser", which runs a server-side engine over an attached PDF.
type Plugin struct {
	ID  string         `json:"id"`
	PDF *PDFEngineSpec `json:"pdf,omitempty"`
}

// PDFEngineSpec selects the file-parser engine: "mistral-ocr" (scans, paid),
// "cloudflare-ai" (free general parser), or "native" (native-file models).
// Note: "pdf-text" is deprecated and silently redirects to "cloudflare-ai".
type PDFEngineSpec struct {
	Engine string `json:"engine"`
}

type APIMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"` // string or []ContentPart
	ToolCalls  []APIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type ContentPart struct {
	Type         string        `json:"type"` // "text", "image_url" or "file"
	Text         string        `json:"text,omitempty"`
	ImageURL     *ImageURL     `json:"image_url,omitempty"`
	File         *FilePart     `json:"file,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// FilePart carries a base64 data-URL PDF for the file-parser plugin.
type FilePart struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"` // "data:application/pdf;base64,..."
}

type CacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type ImageURL struct {
	URL string `json:"url"` // "data:image/jpeg;base64,..."
}

type APITool struct {
	Type         string        `json:"type"`
	Function     APIFunction   `json:"function"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type APIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type APIToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function APIFunctionCall `json:"function"`
}

type APIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ResponseBody struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
	Model   string   `json:"model"`
}

type Choice struct {
	Message      APIMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

// extractContent normalizes the OpenRouter content field, which may arrive as a
// plain string, as null, or as an array of content parts depending on the
// upstream provider. A bare string assertion silently drops the latter two,
// surfacing as an empty reply to the user.
func extractContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					sb.WriteString(text)
				}
			}
		}
		return sb.String()
	default:
		return ""
	}
}

type Usage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func BuildRequestBody(model string, req output.LLMRequest) RequestBody {
	body := RequestBody{
		Model:       model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	// Inject tool names as a lightweight system message (~300 tokens vs full schemas)
	if len(req.ToolNames) > 0 {
		var toolList strings.Builder
		toolList.WriteString("Available tools (ask for full schema when needed):\n")
		for _, t := range req.ToolNames {
			fmt.Fprintf(&toolList, "- %s: %s\n", t.Name, t.Description)
		}
		body.Messages = append(body.Messages, APIMessage{
			Role:    "system",
			Content: toolList.String(),
		})
	}

	for _, msg := range req.Messages {
		apiMsg := APIMessage{
			Role: string(msg.Role),
		}

		// Build content: if images present, use multimodal content parts
		if len(msg.Images) > 0 {
			parts := []ContentPart{}
			if msg.Content != "" {
				parts = append(parts, ContentPart{Type: "text", Text: msg.Content})
			}
			for _, img := range msg.Images {
				dataURL := fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Base64)
				parts = append(parts, ContentPart{
					Type:     "image_url",
					ImageURL: &ImageURL{URL: dataURL},
				})
			}
			apiMsg.Content = parts
		} else {
			apiMsg.Content = msg.Content
		}

		if msg.ToolCallID != "" {
			apiMsg.ToolCallID = msg.ToolCallID
		}

		for _, tc := range msg.ToolCalls {
			apiMsg.ToolCalls = append(apiMsg.ToolCalls, APIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: APIFunctionCall{
					Name:      tc.Name,
					Arguments: tc.Args,
				},
			})
		}

		body.Messages = append(body.Messages, apiMsg)
	}

	for _, tool := range req.Tools {
		body.Tools = append(body.Tools, APITool{
			Type: "function",
			Function: APIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Schema,
			},
		})
	}

	applyCaching(model, &body)
	return body
}

// applyCaching marks the stable request prefix with Anthropic ephemeral cache
// breakpoints so the tool loop re-reads it at ~0.1x instead of full price.
// No-op for non-Anthropic models (OpenRouter ignores/rejects cache_control there).
func applyCaching(model string, body *RequestBody) {
	if !strings.HasPrefix(model, "anthropic/") {
		return
	}
	if n := len(body.Tools); n > 0 {
		body.Tools[n-1].CacheControl = &CacheControl{Type: "ephemeral"}
	}
	for i := range body.Messages {
		if body.Messages[i].Role == "system" {
			cacheMessage(&body.Messages[i])
			break
		}
	}
	if n := len(body.Messages); n >= 2 {
		cacheMessage(&body.Messages[n-2])
	}
}

// cacheMessage converts a message's content to the array form (if needed) and
// attaches a cache breakpoint to its final content part (text or image).
func cacheMessage(m *APIMessage) {
	switch c := m.Content.(type) {
	case string:
		if c == "" {
			return
		}
		m.Content = []ContentPart{{Type: "text", Text: c, CacheControl: &CacheControl{Type: "ephemeral"}}}
	case []ContentPart:
		if len(c) == 0 {
			return
		}
		c[len(c)-1].CacheControl = &CacheControl{Type: "ephemeral"}
		m.Content = c
	}
}

func (c *Client) doRequest(ctx context.Context, jsonBody []byte) ([]byte, error) {
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if attempt < maxRetries-1 && (strings.Contains(err.Error(), "GOAWAY") || strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "EOF")) {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return nil, fmt.Errorf("do request: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			if attempt < maxRetries-1 {
				time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
				continue
			}
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("openrouter error %d: %s", resp.StatusCode, string(body))
		}

		return body, nil
	}

	return nil, fmt.Errorf("max retries exceeded")
}

func (c *Client) Chat(ctx context.Context, req output.LLMRequest) (*output.LLMResponse, error) {
	model := c.model
	if req.Model != "" {
		model = req.Model
	}
	body := BuildRequestBody(model, req)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	respBody, err := c.doRequest(ctx, jsonBody)
	if err != nil {
		return nil, err
	}

	var apiResp ResponseBody
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]
	result := &output.LLMResponse{
		Content:      extractContent(choice.Message.Content),
		InputTokens:  apiResp.Usage.PromptTokens,
		OutputTokens: apiResp.Usage.CompletionTokens,
		Model:        apiResp.Model,
	}

	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, entity.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: tc.Function.Arguments,
		})
	}

	if apiResp.Usage.PromptTokensDetails.CachedTokens > 0 {
		slog.Info("llm cache hit",
			"model", apiResp.Model,
			"cached_tokens", apiResp.Usage.PromptTokensDetails.CachedTokens,
			"prompt_tokens", apiResp.Usage.PromptTokens)
	}

	if result.Content == "" && len(result.ToolCalls) == 0 {
		slog.Warn("llm returned empty completion",
			"model", apiResp.Model,
			"finish_reason", choice.FinishReason,
			"completion_tokens", apiResp.Usage.CompletionTokens)
	}

	return result, nil
}

func (c *Client) ChatStream(ctx context.Context, req output.LLMRequest, onChunk func(chunk string)) (*output.LLMResponse, error) {
	return c.Chat(ctx, req)
}
