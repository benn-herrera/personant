package model

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestEncodeRequestStreamOptions pins the conditional: a STREAMING request
// carries stream_options.include_usage=true (without it an
// OpenAI-compatible provider sends no usage payload at all on a streamed
// response, which zeroes every token count and silently disables anything
// gated on one), and a BLOCKING request must NOT carry the field — its
// mere presence is a protocol error with some providers.
func TestEncodeRequestStreamOptions(t *testing.T) {
	req := DefaultRequest("m", []Message{{Role: "user", Content: "hi"}})

	for _, tc := range []struct {
		name   string
		stream bool
		want   bool
	}{
		{name: "streaming", stream: true, want: true},
		{name: "blocking", stream: false, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := encodeRequest(req, tc.stream)
			if err != nil {
				t.Fatalf("encodeRequest: %v", err)
			}
			var decoded struct {
				Stream        bool `json:"stream"`
				StreamOptions *struct {
					IncludeUsage bool `json:"include_usage"`
				} `json:"stream_options"`
			}
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if decoded.Stream != tc.stream {
				t.Errorf("stream: got %v want %v", decoded.Stream, tc.stream)
			}
			if !tc.want {
				if decoded.StreamOptions != nil {
					t.Errorf("stream_options must be absent on a blocking request: %s", body)
				}
				if strings.Contains(string(body), "stream_options") {
					t.Errorf("stream_options key present in blocking body: %s", body)
				}
				return
			}
			if decoded.StreamOptions == nil {
				t.Fatalf("stream_options missing from streaming request: %s", body)
			}
			if !decoded.StreamOptions.IncludeUsage {
				t.Errorf("stream_options.include_usage: got false want true: %s", body)
			}
		})
	}
}

// TestStreamUsageOnlyFinalChunk: with include_usage the provider ends the
// stream with a CHOICES-LESS chunk carrying only `usage`. It must decode
// (not error, not mis-index), land in Final().Usage, and disturb nothing
// else — content accumulation, finish reason, and the tool-call merge all
// come from the chunks before it.
func TestStreamUsageOnlyFinalChunk(t *testing.T) {
	const sse = `data: {"choices":[{"delta":{"content":"Hel"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"content":"lo"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":41,"completion_tokens":9,"total_tokens":50}}

data: [DONE]

`
	chunks, final, err := runStream(t, sse)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
	if final.Usage.PromptTokens != 41 || final.Usage.CompletionTokens != 9 || final.Usage.TotalTokens != 50 {
		t.Errorf("Final.Usage = %+v; want prompt=41 completion=9 total=50", final.Usage)
	}
	if final.Content != "Hello" {
		t.Errorf("Final.Content = %q; want %q", final.Content, "Hello")
	}
	if final.FinishReason != "tool_calls" {
		t.Errorf("Final.FinishReason = %q; want tool_calls", final.FinishReason)
	}
	got := projectCalls(final.ToolCalls)
	want := []wantCall{{ID: "call_1", Function: "search", Args: `"{\"q\":\"go\"}"`}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("Final.ToolCalls = %+v; want %+v", got, want)
	}
	// The usage-only chunk surfaces as a real (content-less) chunk rather
	// than being swallowed: a caller counting chunks sees it, and it adds
	// nothing to the body.
	last := chunks[len(chunks)-1]
	if last.Content != "" || last.Reasoning != "" || len(last.ToolCalls) != 0 {
		t.Errorf("usage-only chunk carried payload: %+v", last)
	}
	if last.Usage.TotalTokens != 50 {
		t.Errorf("usage-only chunk Usage = %+v; want total=50", last.Usage)
	}
}

// TestStreamUsageReasoningTokens: completion_tokens_details.reasoning_tokens
// is the only measurement of what thinking mode costs; capture it from the
// usage block on both the streaming and the blocking path.
func TestStreamUsageReasoningTokens(t *testing.T) {
	const sse = `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":900,"total_tokens":912,"completion_tokens_details":{"reasoning_tokens":880}}}

data: [DONE]

`
	_, final, err := runStream(t, sse)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
	if final.Usage.ReasoningTokens != 880 {
		t.Errorf("Final.Usage.ReasoningTokens = %d; want 880", final.Usage.ReasoningTokens)
	}
	if final.Usage.CompletionTokens != 900 {
		t.Errorf("Final.Usage.CompletionTokens = %d; want 900", final.Usage.CompletionTokens)
	}

	// Blocking path shares the same wire projection.
	resp, err := decodeResponse([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
		"usage":{"prompt_tokens":12,"completion_tokens":900,"total_tokens":912,"completion_tokens_details":{"reasoning_tokens":880}}}`))
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	if resp.Usage.ReasoningTokens != 880 {
		t.Errorf("blocking Usage.ReasoningTokens = %d; want 880", resp.Usage.ReasoningTokens)
	}
	// An absent details block is 0, not an error.
	resp, err = decodeResponse([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":3}}`))
	if err != nil {
		t.Fatalf("decodeResponse (no details): %v", err)
	}
	if resp.Usage.ReasoningTokens != 0 {
		t.Errorf("absent completion_tokens_details should yield 0, got %d", resp.Usage.ReasoningTokens)
	}
}

// TestStreamReasoningNeverEntersContent is the HARD BOUNDARY test:
// reasoning is model scratch and must never reach the response body (which
// is what gets journaled, symbol-extracted, and replayed as history). With
// reasoning and content deltas interleaved, Content must contain ONLY the
// content bytes, and the reasoning must be reachable per-chunk instead.
//
// Both wire spellings are covered: litellm normalizes most vendors to
// `reasoning_content`, but `reasoning` appears in the wild.
func TestStreamReasoningNeverEntersContent(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning"} {
		t.Run(field, func(t *testing.T) {
			sse := `data: {"choices":[{"delta":{"` + field + `":"Let me think. "}}]}

data: {"choices":[{"delta":{"content":"The answer"}}]}

data: {"choices":[{"delta":{"` + field + `":"Still thinking."}}]}

data: {"choices":[{"delta":{"content":" is 4."},"finish_reason":"stop"}]}

data: [DONE]

`
			chunks, final, err := runStream(t, sse)
			if !errors.Is(err, io.EOF) {
				t.Fatalf("stream did not end cleanly: %v", err)
			}
			if final.Content != "The answer is 4." {
				t.Errorf("Final.Content = %q; want only the content bytes %q",
					final.Content, "The answer is 4.")
			}
			for _, frag := range []string{"think", "Still"} {
				if strings.Contains(final.Content, frag) {
					t.Fatalf("reasoning leaked into Final.Content: %q", final.Content)
				}
			}
			if final.FinishReason != "stop" {
				t.Errorf("Final.FinishReason = %q; want stop", final.FinishReason)
			}
			var reasoning, content strings.Builder
			for _, c := range chunks {
				reasoning.WriteString(c.Reasoning)
				content.WriteString(c.Content)
				if c.Reasoning != "" && c.Content != "" {
					t.Errorf("chunk carried both reasoning and content: %+v", c)
				}
			}
			if reasoning.String() != "Let me think. Still thinking." {
				t.Errorf("per-chunk reasoning = %q; want the two reasoning deltas in order",
					reasoning.String())
			}
			if content.String() != final.Content {
				t.Errorf("per-chunk content %q != Final.Content %q", content.String(), final.Content)
			}
		})
	}
}

// TestStreamReasoningOnly: a stream with no content deltas at all is a real
// shape (a tool-call turn spends its whole generation on reasoning). The
// body must come back empty rather than filled with scratch, and the usage
// and finish reason still land.
func TestStreamReasoningOnly(t *testing.T) {
	const sse = `data: {"choices":[{"delta":{"reasoning_content":"thinking hard"}}]}

data: {"choices":[{"delta":{"reasoning_content":" about it"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":31,"total_tokens":38,"completion_tokens_details":{"reasoning_tokens":31}}}

data: [DONE]

`
	chunks, final, err := runStream(t, sse)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
	if final.Content != "" {
		t.Errorf("Final.Content = %q; want empty on a reasoning-only stream", final.Content)
	}
	if final.FinishReason != "stop" {
		t.Errorf("Final.FinishReason = %q; want stop", final.FinishReason)
	}
	if final.Usage.ReasoningTokens != 31 {
		t.Errorf("Final.Usage.ReasoningTokens = %d; want 31", final.Usage.ReasoningTokens)
	}
	var reasoning strings.Builder
	for _, c := range chunks {
		reasoning.WriteString(c.Reasoning)
	}
	if reasoning.String() != "thinking hard about it" {
		t.Errorf("per-chunk reasoning = %q", reasoning.String())
	}
}

// TestStreamReasoningInterleavedWithToolCalls: reasoning deltas arriving
// between tool-call fragments must not disturb the index-keyed merge — the
// argument concatenation is contiguous across them.
func TestStreamReasoningInterleavedWithToolCalls(t *testing.T) {
	const sse = `data: {"choices":[{"delta":{"reasoning_content":"I should search."}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"search","arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"reasoning_content":"Which query?"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	_, final, err := runStream(t, sse)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
	if final.Content != "" {
		t.Errorf("Final.Content = %q; want empty", final.Content)
	}
	got := projectCalls(final.ToolCalls)
	want := wantCall{ID: "call_9", Function: "search", Args: `"{\"q\":\"go\"}"`}
	if len(got) != 1 || got[0] != want {
		t.Errorf("Final.ToolCalls = %+v; want %+v", got, want)
	}
}
