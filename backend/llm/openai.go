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

// OpenAIBaseURL is the chat-completions endpoint. The wire shape is the
// canonical OpenAI Chat Completions format — DeepSeek is a clone of it,
// so most of the request/response code mirrors deepseek.go.
const OpenAIBaseURL = "https://api.openai.com/v1/chat/completions"

// OpenAI is a Client backed by the OpenAI HTTP API. Same posture as the
// DeepSeek client: 30s timeout, no retries, JSON mode on for structured
// responses (issue 02 sign-off).
type OpenAI struct {
	APIKey   string
	BaseURL  string
	HTTP     *http.Client
	JSONMode bool
}

// NewOpenAI returns a client with the defaults agreed in issue 02.
func NewOpenAI(apiKey string) *OpenAI {
	return &OpenAI{
		APIKey:   apiKey,
		BaseURL:  OpenAIBaseURL,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		JSONMode: true,
	}
}

// ErrOpenAIMissingKey is returned when Complete runs without an API key.
var ErrOpenAIMissingKey = errors.New("openai: missing API key")

func (o *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	if o.APIKey == "" {
		return Response{}, ErrOpenAIMissingKey
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
	if o.JSONMode {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("marshal: %w", err)
	}

	url := o.BaseURL
	if url == "" {
		url = OpenAIBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Response{}, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)

	client := o.HTTP
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
			return Response{}, fmt.Errorf("openai %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return Response{}, fmt.Errorf("openai %d: %s", resp.StatusCode, string(raw))
	}

	var parsed deepseekResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("decode: %w (body=%s)", err, string(raw))
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: empty choices (body=%s)", string(raw))
	}
	return Response{Content: parsed.Choices[0].Message.Content}, nil
}
