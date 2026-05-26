package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// DeepSeekBaseURL is the chat-completions endpoint. DeepSeek's API is
// OpenAI-compatible, so the wire shape mirrors OpenAI's.
const DeepSeekBaseURL = "https://api.deepseek.com/chat/completions"

// DeepSeek is a Client backed by the DeepSeek HTTP API.
//
// Defaults set by NewDeepSeek: 30s request timeout, no retries on transient
// failures (callers retry at the handler layer if they care). JSONMode = true
// hints the model to return strict JSON for the quiz path — see the
// system_agents prompt for quizmaster, which expects this.
type DeepSeek struct {
	APIKey   string
	BaseURL  string
	HTTP     *http.Client
	JSONMode bool
}

// NewDeepSeek returns a client with the chosen defaults from issue 02:
// 30s timeout, no retries, JSON mode on. The caller can override fields
// on the returned struct before use.
func NewDeepSeek(apiKey string) *DeepSeek {
	return &DeepSeek{
		APIKey:   apiKey,
		BaseURL:  DeepSeekBaseURL,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		JSONMode: true,
	}
}

type deepseekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type deepseekRequest struct {
	Model          string          `json:"model"`
	Messages       []deepseekMessage `json:"messages"`
	Temperature    float64         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type deepseekResponse struct {
	Choices []struct {
		Message deepseekMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ErrMissingAPIKey is returned when Complete runs without an API key set.
var ErrMissingAPIKey = errors.New("deepseek: missing API key")

func (d *DeepSeek) Complete(ctx context.Context, req Request) (Response, error) {
	if d.APIKey == "" {
		return Response{}, ErrMissingAPIKey
	}

	msgs := make([]deepseekMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, deepseekMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, deepseekMessage{Role: m.Role, Content: m.Content})
	}

	body := deepseekRequest{
		Model:       req.Model,
		Messages:    msgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if d.JSONMode {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("marshal: %w", err)
	}

	url := d.BaseURL
	if url == "" {
		url = DeepSeekBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Response{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+d.APIKey)

	client := d.HTTP
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
		var parsed deepseekResponse
		_ = json.Unmarshal(raw, &parsed)
		if parsed.Error != nil {
			return Response{}, fmt.Errorf("deepseek %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return Response{}, fmt.Errorf("deepseek %d: %s", resp.StatusCode, string(raw))
	}

	var parsed deepseekResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("decode: %w (body=%s)", err, string(raw))
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("deepseek: empty choices (body=%s)", string(raw))
	}
	content := parsed.Choices[0].Message.Content
	if content == "" {
		log.Printf("deepseek: empty content in response body=%s", string(raw))
	}
	return Response{Content: content}, nil
}
