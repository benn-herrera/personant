// Package model is the LLM client surface.
//
// Two implementations satisfy the same Client interface: HTTPClient (this
// package), targeting any OpenAI-compatible chat-completions endpoint, and
// MockClient (mockllm.go), used by the test fabric. Both consume the
// same Request/Response shape, so scenario tests and production turn
// handling exercise the same code paths.
package model

import (
	"context"
	"encoding/json"
	"errors"
)

// Client is the abstraction the runtime depends on for an LLM round-trip.
// Defined here so test code can swap in MockClient without depending on
// the HTTP transport.
type Client interface {
	Consult(ctx context.Context, req Request) (Response, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// ModelInfo describes a single model exposed by a provider, mirroring the
// shape OpenAI-compatible /models endpoints return.
type ModelInfo struct {
	ID      string // provider-specific model identifier
	Created int64  // unix seconds; 0 if provider didn't supply
	OwnedBy string // free-form ownership string; "" if absent
}

// Request is one chat-completions invocation.
//
// Zero values for Temperature, MaxTokens, and StopSequences mean "use the
// provider's default" — they are omitted from the wire payload entirely
// rather than serialized as 0/empty, so a provider that distinguishes
// "unset" from "0" gets the right behavior.
type Request struct {
	Model         string
	Messages      []Message
	Tools         []ToolSpec
	Temperature   float64
	MaxTokens     int
	StopSequences []string
}

// Message is one turn in the chat history.
//
// Role is one of: "system", "user", "assistant", "tool". When Role is
// "assistant" and the model returned tool calls, ToolCalls is populated.
// When Role is "tool", Content carries the tool result and ToolCallID
// identifies which prior assistant tool call this responds to.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

// ToolSpec describes a tool the model may call. Parameters is an
// OpenAI-style JSON-Schema fragment passed through verbatim; the runtime
// does not validate or transform it.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is one tool invocation requested by the model.
type ToolCall struct {
	ID       string
	Function string
	Args     json.RawMessage
}

// Response is one chat-completions result.
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
}

// Usage is the token-accounting block returned by the provider.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ErrMockExhausted is returned by a scripted MockClient when its response
// queue has been drained. Tests check for this with errors.Is.
var ErrMockExhausted = errors.New("model: mock client exhausted")
