package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"personant/internal/store"
)

// userAgent is the User-Agent header value sent with every request.
const userAgent = "personant/0.1"

// HTTPClient is a thin OpenAI-compatible chat-completions client.
//
// Construction is intentionally cheap: the underlying http.Client is
// reused across calls so connection pooling works. Provider holds the
// resolved configuration (BaseURL, APIKey, DefaultModel) — APIKey is
// secret-bearing and is never logged or returned in error strings.
type HTTPClient struct {
	provider store.Provider
	http     *http.Client
}

// NewHTTPClient constructs an HTTPClient for the given provider. A
// nil-equivalent zero Provider yields a client whose first call will
// produce a clear error.
func NewHTTPClient(p store.Provider) Client {
	return &HTTPClient{
		provider: p,
		http: &http.Client{
			// Per-request timeouts are honored via ctx; this is a hard
			// upper bound that keeps a runaway handshake from hanging
			// indefinitely if the caller forgets a deadline.
			Timeout: 5 * time.Minute,
		},
	}
}

// Consult performs one chat-completions round-trip.
//
// On any non-2xx response, the body is included in the returned error,
// scrubbed of any Authorization header value the server might have
// echoed back. The APIKey itself is never written into an error string
// constructed by this client.
func (c *HTTPClient) Consult(ctx context.Context, req Request) (Response, error) {
	if c.provider.BaseURL == "" {
		return Response{}, fmt.Errorf("model: provider BaseURL is empty")
	}

	body, err := encodeRequest(req)
	if err != nil {
		return Response{}, fmt.Errorf("model: encode request: %w", err)
	}

	url := joinChatCompletionsURL(c.provider.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("model: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)
	if c.provider.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.provider.APIKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// http.Client.Do already wraps ctx errors usefully; make sure the
		// caller can errors.Is(err, context.Canceled) without our wrapper
		// hiding it.
		return Response{}, fmt.Errorf("model: http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("model: read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		return Response{}, fmt.Errorf("model: http %d: %s", resp.StatusCode, scrubAuthorization(string(respBody), c.provider.APIKey))
	}

	out, err := decodeResponse(respBody)
	if err != nil {
		return Response{}, fmt.Errorf("model: decode response: %w", err)
	}
	return out, nil
}

// joinChatCompletionsURL appends "chat/completions" to base, handling the
// trailing-slash variation gracefully.
func joinChatCompletionsURL(base string) string {
	if strings.HasSuffix(base, "/") {
		return base + "chat/completions"
	}
	return base + "/chat/completions"
}

// scrubAuthorization removes any occurrence of the bearer-token header
// value (or the bare key) from s. Empty key → no scrubbing.
func scrubAuthorization(s, apiKey string) string {
	const placeholder = "<redacted>"
	if apiKey != "" {
		s = strings.ReplaceAll(s, "Bearer "+apiKey, "Bearer "+placeholder)
		s = strings.ReplaceAll(s, apiKey, placeholder)
	}
	return s
}

// --- wire format (OpenAI chat-completions) ---

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolFunction `json:"function"`
}

type wireToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type wireToolSpec struct {
	Type     string             `json:"type"`
	Function wireToolSpecParams `json:"function"`
}

type wireToolSpecParams struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireRequest struct {
	Model       string         `json:"model"`
	Messages    []wireMessage  `json:"messages"`
	Tools       []wireToolSpec `json:"tools,omitempty"`
	ToolChoice  string         `json:"tool_choice,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	MaxTokens   *int           `json:"max_tokens,omitempty"`
	Stop        []string       `json:"stop,omitempty"`
}

type wireChoice struct {
	Index        int         `json:"index"`
	Message      wireMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type wireResponse struct {
	Choices []wireChoice `json:"choices"`
	Usage   wireUsage    `json:"usage"`
}

func encodeRequest(req Request) ([]byte, error) {
	wr := wireRequest{
		Model:    req.Model,
		Messages: make([]wireMessage, 0, len(req.Messages)),
		Stop:     req.StopSequences,
	}
	for _, m := range req.Messages {
		wm := wireMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: wireToolFunction{
					Name:      tc.Function,
					Arguments: tc.Args,
				},
			})
		}
		wr.Messages = append(wr.Messages, wm)
	}
	if len(req.Tools) > 0 {
		wr.ToolChoice = "auto"
		for _, t := range req.Tools {
			wr.Tools = append(wr.Tools, wireToolSpec{
				Type: "function",
				Function: wireToolSpecParams{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
	}
	if req.Temperature != 0 {
		t := req.Temperature
		wr.Temperature = &t
	}
	if req.MaxTokens != 0 {
		mt := req.MaxTokens
		wr.MaxTokens = &mt
	}
	return json.Marshal(wr)
}

func decodeResponse(body []byte) (Response, error) {
	var w wireResponse
	if err := json.Unmarshal(body, &w); err != nil {
		return Response{}, err
	}
	if len(w.Choices) == 0 {
		return Response{}, fmt.Errorf("response contains no choices")
	}
	choice := w.Choices[0]
	out := Response{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:     w.Usage.PromptTokens,
			CompletionTokens: w.Usage.CompletionTokens,
			TotalTokens:      w.Usage.TotalTokens,
		},
	}
	for _, tc := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:       tc.ID,
			Function: tc.Function.Name,
			Args:     tc.Function.Arguments,
		})
	}
	return out, nil
}
