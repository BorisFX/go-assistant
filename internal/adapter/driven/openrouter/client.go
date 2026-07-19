package openrouter

import (
	"bufio"
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

	"golang.org/x/time/rate"

	"github.com/olegmatyakubov/go-assistant/internal/domain/entity"
	"github.com/olegmatyakubov/go-assistant/internal/observability"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

const defaultBaseURL = "https://openrouter.ai/api/v1/chat/completions"

// providerFromURL extracts a short provider label from the endpoint URL
// so metrics can distinguish between OpenRouter, a6api, etc.
func providerFromURL(u string) string {
	switch {
	case strings.Contains(u, "openrouter.ai"):
		return "openrouter"
	case strings.Contains(u, "a6api"):
		return "a6api"
	case strings.Contains(u, "openai.com"):
		return "openai"
	default:
		return "custom"
	}
}

func mustLLMMetrics() *observability.LLMMetrics {
	m, err := observability.NewLLMMetrics()
	if err != nil {
		return nil
	}
	return m
}

type Client struct {
	apiKey     string
	model      string
	fallback   string
	baseURL    string
	provider   string
	httpClient *http.Client
	limiter    *rate.Limiter
	metrics    *observability.LLMMetrics
}

// New builds a chat client. baseURL overrides the OpenAI-compatible
// chat-completions endpoint; pass "" to use OpenRouter's default.
func New(apiKey, model, fallback, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	provider := providerFromURL(baseURL)
	// Force HTTP/1.1 to avoid GOAWAY issues with OpenRouter
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{},
		TLSNextProto:    make(map[string]func(string, *tls.Conn) http.RoundTripper),
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
		MaxIdleConnsPerHost: 20,
		MaxConnsPerHost:     50,
	}

	return &Client{
		apiKey:   apiKey,
		model:    model,
		fallback: fallback,
		baseURL:  baseURL,
		provider: provider,
		// 20 requests/sec burst of 5 — matches OpenRouter's rate limits.
		// Prevents runaway tool loops from hitting 429s.
		limiter:  rate.NewLimiter(rate.Limit(20), 5),
		metrics:  mustLLMMetrics(),
		httpClient: &http.Client{
			Transport: transport,
			// Reasoning models (e.g. the legal-review coordinator on Sonnet with an
			// 80k output budget) legitimately generate for several minutes; a 120s
			// ceiling cut them off mid-body. Per-call deadlines come from context.
			Timeout: 600 * time.Second,
		},
	}
}

type RequestBody struct {
	Model       string       `json:"model"`
	Messages    []APIMessage `json:"messages"`
	Tools       []APITool    `json:"tools,omitempty"`
	Plugins     []Plugin     `json:"plugins,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature float64          `json:"temperature,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Reasoning   *ReasoningConfig `json:"reasoning,omitempty"`
}

// ReasoningConfig caps the hidden reasoning budget. Reasoning models (Gemini 2.5+,
// Sonnet) can otherwise spend the entire max_tokens budget on reasoning and return
// zero visible content. OpenRouter silently ignores this for non-reasoning models.
type ReasoningConfig struct {
	MaxTokens int `json:"max_tokens,omitempty"`
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

	// Reserve visible-answer budget: cap reasoning at ~1/3 of max_tokens (≤2048),
	// so a reasoning model can never consume the whole budget on hidden thinking
	// and return an empty completion. Leaves the majority for the actual answer.
	if req.MaxTokens > 0 {
		if reasoningCap := min(req.MaxTokens/3, 2048); reasoningCap > 0 {
			body.Reasoning = &ReasoningConfig{MaxTokens: reasoningCap}
		}
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
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(jsonBody))
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

	start := time.Now()
	respBody, err := c.doRequest(ctx, jsonBody)
	if err != nil {
		if c.metrics != nil {
			c.metrics.RecordRequest(ctx, c.provider, model, time.Since(start), 0, 0, 0, err)
		}
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

	if c.metrics != nil {
		c.metrics.RecordRequest(ctx, c.provider, apiResp.Model, time.Since(start),
			apiResp.Usage.PromptTokens,
			apiResp.Usage.CompletionTokens,
			apiResp.Usage.PromptTokensDetails.CachedTokens,
			nil)
	}

	return result, nil
}

func (c *Client) ChatStream(ctx context.Context, req output.LLMRequest, onChunk func(chunk string)) (*output.LLMResponse, error) {
	if onChunk == nil {
		return c.Chat(ctx, req)
	}

	model := c.model
	if req.Model != "" {
		model = req.Model
	}
	body := BuildRequestBody(model, req)
	body.Stream = true

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openrouter stream error %d: %s", resp.StatusCode, string(body))
	}

	result, err := c.parseSSE(resp.Body, onChunk)
	if err != nil {
		return nil, err
	}

	if c.metrics != nil {
		c.metrics.RecordRequest(ctx, c.provider, model, time.Since(start),
			result.InputTokens, result.OutputTokens, 0, nil)
	}
	return result, nil
}

// streamDelta is a partial SSE chunk from an OpenAI-compatible streaming response.
type streamDelta struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

func (c *Client) parseSSE(body io.Reader, onChunk func(string)) (*output.LLMResponse, error) {
	var (
		contentBuf strings.Builder
		// toolCallArgs accumulates streamed argument fragments per tool call index
		toolCallArgs = map[int]entity.ToolCall{}
		inputTokens  int
		outputTokens int
		respModel    string
	)

	scanner := bufio.NewScanner(body)
	// Increase buffer for large SSE lines (tool call args can be long)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var delta streamDelta
		if err := json.Unmarshal([]byte(payload), &delta); err != nil {
			continue
		}

		if delta.Usage != nil {
			inputTokens = delta.Usage.PromptTokens
			outputTokens = delta.Usage.CompletionTokens
		}
		if delta.Model != "" {
			respModel = delta.Model
		}

		if len(delta.Choices) == 0 {
			continue
		}
		d := delta.Choices[0].Delta

		if d.Content != "" {
			contentBuf.WriteString(d.Content)
			onChunk(d.Content)
		}

		for _, tc := range d.ToolCalls {
			existing := toolCallArgs[tc.Index]
			if tc.ID != "" {
				existing.ID = tc.ID
			}
			if tc.Function.Name != "" {
				existing.Name = tc.Function.Name
			}
			existing.Args += tc.Function.Arguments
			toolCallArgs[tc.Index] = existing
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SSE stream: %w", err)
	}

	result := &output.LLMResponse{
		Content:      contentBuf.String(),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Model:        respModel,
	}
	for i := 0; i < len(toolCallArgs); i++ {
		if tc, ok := toolCallArgs[i]; ok {
			result.ToolCalls = append(result.ToolCalls, tc)
		}
	}
	return result, nil
}
