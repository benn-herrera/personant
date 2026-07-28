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
//
// Two consult flavors are provided:
//   - Consult: blocking, returns the full Response. Right for programmatic
//     flows that don't surface tokens to a user.
//   - ConsultStream: returns a StreamReader; callers iterate chunks as
//     they arrive. Right for interactive flows (chat REPL, ping).
//
// Both methods consume the same Request and produce equivalent final
// content; ConsultStream additionally exposes per-chunk deltas.
type Client interface {
	Consult(ctx context.Context, req Request) (Response, error)
	ConsultStream(ctx context.Context, req Request) (StreamReader, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// Embedder produces embedding vectors for text, against an
// OpenAI-compatible /embeddings endpoint. It is separate from Client
// because embedding is a distinct capability — a provider may serve
// chat, embeddings, or both — and because the runtime uses it for an
// autonomic role (§3.4 layer-2 recall index), never as an
// LLM-callable tool.
//
// Embed returns one vector per input text, in input order. Implementations
// may batch internally; callers pass the full slice.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
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
// Sampling parameters are always wired on the wire — a zero value for
// Temperature or MaxTokens is a real value, not a "use server default"
// sentinel. Use DefaultRequest to construct a Request with personant's
// v0.1 defaults pre-filled; only override what you explicitly need.
type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolSpec

	// Temperature is the sampling temperature, always wired on the wire.
	// 0 means deterministic (greedy decode); it is NOT a "use server
	// default" sentinel.
	Temperature float64

	// MaxTokens is the response token cap, always wired on the wire. 0
	// is a real value (no tokens) — not a "use server default" sentinel.
	// Production callers should construct Requests via DefaultRequest.
	MaxTokens int

	// StopSequences is sent as `stop` in the wire payload; nil/empty →
	// field omitted.
	StopSequences []string

	// ChatTemplateKwargs is the de-facto OpenAI-API extension for passing
	// chat-template-level kwargs through to the underlying tokenizer.
	// Most commonly used for thinking-mode toggles on local-served
	// models (Qwen3, gpt-oss-style, etc.). Encoded as
	// `chat_template_kwargs` in the JSON wire format. Empty/nil → field
	// omitted entirely.
	ChatTemplateKwargs map[string]any
}

// DefaultRequest returns a Request prefilled with personant's v0.1
// defaults. The caller fills in Model and Messages; everything else is
// preset:
//
//   - Temperature: 0 (deterministic)
//   - MaxTokens:   16384 (16K; revisited per spec calibration work)
//   - ChatTemplateKwargs: {"thinking": true, "enable_thinking": true}
//     — both keys are sent so models recognizing either get thinking
//     on (the actual key varies by model; sending both is harmless
//     to those that recognize neither).
func DefaultRequest(model string, messages []Message) Request {
	return Request{
		Model:       model,
		Messages:    messages,
		Temperature: 0,
		MaxTokens:   16384,
		ChatTemplateKwargs: map[string]any{
			"thinking":        true,
			"enable_thinking": true,
		},
	}
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
//
// Args is the raw JSON value of the wire `arguments` field, NOT the
// decoded argument object. OpenAI-compatible providers send arguments as
// a JSON-encoded string, so Args typically reads `"{\"q\":\"go\"}"` —
// unmarshal it into a string first, then unmarshal that string into the
// argument struct. Both the blocking and the streaming path produce this
// same representation, and encodeRequest replays it verbatim when the
// call is echoed back to the provider in an assistant message.
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

// Chunk is one delta in a streamed chat-completion response. Most chunks
// carry a non-empty Content; the final chunk(s) typically carry empty
// Content but a non-empty FinishReason and Usage.
//
// ToolCalls is an IDENTITY-ONLY progress signal, not executable calls:
// one entry per tool call this chunk carried a fragment for, holding the
// ID and Function name known so far. Args is deliberately always nil —
// a chunk holds a slice of the argument string, which is not valid JSON
// on its own, so there is nothing safe to put there. It exists so a UI
// can say "calling web.search…" the moment the name arrives.
//
// The complete, merged calls come from StreamReader.Final().ToolCalls,
// which accumulates fragments by index across the whole stream.
//
// Reasoning carries thinking-mode output, kept strictly separate from
// Content because they are different kinds of output: Content is the
// model's committed answer, Reasoning is scratch. Reasoning must never be
// concatenated into a response body — it must not be journaled as the
// response, feed symbol extraction, or be replayed in history (providers
// advise against replaying it, and it would blow the §6.5 budget). It is
// available so a front end can display it, dimmed and optionally; it is
// per-chunk only and is NOT accumulated into Final() (see the note there).
// A reasoning-only chunk carries an empty Content — a stream can consist
// almost entirely of them.
type Chunk struct {
	Content      string
	Reasoning    string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
}

// StreamReader iterates the chunks of a streamed response.
//
// Usage:
//
//	sr, err := client.ConsultStream(ctx, req)
//	if err != nil { ... }
//	defer sr.Close()
//	for {
//	    chunk, err := sr.Next()
//	    if errors.Is(err, io.EOF) { break }
//	    if err != nil { ... }
//	    // chunk.Content has the next delta
//	}
//	final := sr.Final()  // accumulated Response after iteration completes
//
// Calling Close before EOF aborts the stream cleanly. Close is
// idempotent; double-close is safe.
type StreamReader interface {
	Next() (Chunk, error)
	Final() Response
	Close() error
}

// Usage is the token-accounting block returned by the provider.
//
// ReasoningTokens is the thinking-mode share of CompletionTokens
// (`completion_tokens_details.reasoning_tokens` on the wire), 0 when the
// provider does not report it. It is the only measurement of what thinking
// mode costs — on a reasoning-heavy turn it is most of CompletionTokens.
//
// On a STREAMED response every figure here is zero unless the request
// carried `stream_options.include_usage` (encodeRequest sends it on the
// streaming path). An all-zero Usage therefore means "not reported", not
// "no tokens", and anything gated on a token count must treat it as an
// unevaluable input rather than a passing one.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ReasoningTokens  int
}

// IsZero reports whether the provider supplied no usage figures at all.
func (u Usage) IsZero() bool { return u == Usage{} }

// ErrMockExhausted is returned by a scripted MockClient when its response
// queue has been drained. Tests check for this with errors.Is.
var ErrMockExhausted = errors.New("model: mock client exhausted")

// ErrConsultUnsupported is returned by a Client whose transport genuinely
// cannot serve a blocking Consult (a hypothetical stream-only backend).
// It is the ONLY error that authorizes a caller to fall back from Consult
// to ConsultStream — every other Consult error (transient network, HTTP
// status, decode) is a real failure and must propagate, not trigger a
// second (cost-doubling) round-trip. No current client returns this; it
// exists so a future stream-only client can opt into the fallback
// explicitly rather than the fallback firing on any error. Match with
// errors.Is.
var ErrConsultUnsupported = errors.New("model: blocking Consult not supported by this client; use ConsultStream")
