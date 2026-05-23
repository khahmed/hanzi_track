package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AnthropicBaseURL is the Messages API endpoint. Unlike the OpenAI-style
// APIs, Anthropic puts the system prompt in a top-level field rather than
// the messages array, and max_tokens is required (not optional).
const AnthropicBaseURL = "https://api.anthropic.com/v1/messages"

// AnthropicVersion is the API version header value. Pinned to the stable
// long-term release per Anthropic's recommendation.
const AnthropicVersion = "2023-06-01"

// anthropicDefaultMaxTokens is the fallback when Request.MaxTokens is 0
// (every other provider treats 0 as "use the server default"; Anthropic
// rejects the call as malformed).
const anthropicDefaultMaxTokens = 1024

// Anthropic is a Client backed by the Anthropic Messages API. Same posture
// as DeepSeek/OpenAI: 30s timeout, no retries. There is no JSON mode flag;
// JSON discipline comes from the system-prompt contract the handlers already
// emit.
type Anthropic struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// NewAnthropic returns a client with the agreed defaults.
func NewAnthropic(apiKey string) *Anthropic {
	return &Anthropic{
		APIKey:  apiKey,
		BaseURL: AnthropicBaseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Type    string                  `json:"type"`
	Error   *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ErrAnthropicMissingKey is returned when Complete runs without an API key.
var ErrAnthropicMissingKey = errors.New("anthropic: missing API key")

func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	if a.APIKey == "" {
		return Response{}, ErrAnthropicMissingKey
	}

	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = anthropicDefaultMaxTokens
	}

	body := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		System:      req.System,
		Messages:    msgs,
		Temperature: req.Temperature,
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("marshal: %w", err)
	}

	url := a.BaseURL
	if url == "" {
		url = AnthropicBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Response{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", AnthropicVersion)

	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var parsed anthropicResponse
		_ = json.Unmarshal(raw, &parsed)
		if parsed.Error != nil {
			return Response{}, fmt.Errorf("anthropic %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return Response{}, fmt.Errorf("anthropic %d: %s", resp.StatusCode, string(raw))
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("decode: %w (body=%s)", err, string(raw))
	}

	// content is an array of typed blocks; we concatenate text blocks and
	// ignore the rest. tool_use blocks won't appear because we're not
	// passing tools.
	var out bytes.Buffer
	for _, b := range parsed.Content {
		if b.Type == "text" {
			out.WriteString(b.Text)
		}
	}
	if out.Len() == 0 {
		return Response{}, fmt.Errorf("anthropic: empty content (body=%s)", string(raw))
	}
	return Response{Content: out.String()}, nil
}
