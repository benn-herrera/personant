package model

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runStream serves the given SSE body from a test server, drains the
// stream, and returns every chunk observed, the merged Final(), and the
// TERMINAL error from Next (io.EOF on a clean stream, a real error when
// the merge could not complete).
func runStream(t *testing.T, sse string) ([]Chunk, Response, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	sr, err := c.ConsultStream(context.Background(), DefaultRequest("m", []Message{{Role: "user", Content: "go"}}))
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	defer sr.Close()

	var chunks []Chunk
	var termErr error
	for {
		chunk, err := sr.Next()
		if err != nil {
			termErr = err
			break
		}
		chunks = append(chunks, chunk)
	}
	return chunks, sr.Final(), termErr
}

// wantCall is the comparable projection of a merged ToolCall. Args is
// compared as a string because it is a raw JSON value.
type wantCall struct {
	ID       string
	Function string
	Args     string
}

func projectCalls(calls []ToolCall) []wantCall {
	out := make([]wantCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, wantCall{ID: c.ID, Function: c.Function, Args: string(c.Args)})
	}
	return out
}

// TestStreamToolCallMerge is the index-keyed merge table. Each case is a
// provider fragmentation shape observed in, or plausible from, an
// OpenAI-compatible endpoint behind litellm; the expectation is the fully
// merged Final().ToolCalls.
//
// Args is the raw JSON value of the wire `arguments` field, so a complete
// merged call reads `"{\"q\":\"go\"}"` — the JSON-encoded STRING form,
// matching what the non-streaming decodeResponse path yields.
func TestStreamToolCallMerge(t *testing.T) {
	cases := []struct {
		name        string
		sse         string
		wantCalls   []wantCall
		wantContent string
		wantFinish  string
		// wantErrSub, when non-empty, is a substring the terminal Next
		// error must contain (the stream did NOT end at a clean io.EOF).
		wantErrSub string
	}{
		{
			name: "canonical openai fragmentation",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"web.search","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"total_tokens":9}}

data: [DONE]

`,
			wantCalls:  []wantCall{{ID: "call_1", Function: "web.search", Args: `"{\"q\":\"go\"}"`}},
			wantFinish: "tool_calls",
		},
		{
			name: "index absent entirely infers index 0",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"web.fetch","arguments":"{\"url\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"\"http://x\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`,
			wantCalls:  []wantCall{{ID: "call_1", Function: "web.fetch", Args: `"{\"url\":\"http://x\"}"`}},
			wantFinish: "tool_calls",
		},
		{
			name: "name split across chunks",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"web."}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"search"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{{ID: "call_1", Function: "web.search", Args: `"{}"`}},
		},
		{
			name: "name arrives later than id",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function"}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"web.search","arguments":"{\"q\":\"a\"}"}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{{ID: "call_9", Function: "web.search", Args: `"{\"q\":\"a\"}"`}},
		},
		{
			name: "name repeated whole in every fragment",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"web.search","arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"web.search","arguments":"\"go\"}"}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{{ID: "call_1", Function: "web.search", Args: `"{\"q\":\"go\"}"`}},
		},
		{
			name: "arguments split mid-string",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":"{\"loc"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\":\"San Fran"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"cisco\"}"}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{{ID: "c", Function: "f", Args: `"{\"location\":\"San Francisco\"}"`}},
		},
		{
			// The é escape is torn across two fragments; only the
			// concatenation is a legal JSON escape sequence.
			name: "arguments split mid-escape",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":"{\"q\":\"caf\\u0"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"0e9\"}"}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{{ID: "c", Function: "f", Args: `"{\"q\":\"caf\\u00e9\"}"`}},
		},
		{
			name: "two parallel calls interleaved by index",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"web.search","arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"web.fetch","arguments":"{\"url\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"http://x\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`,
			wantCalls: []wantCall{
				{ID: "call_a", Function: "web.search", Args: `"{\"q\":\"go\"}"`},
				{ID: "call_b", Function: "web.fetch", Args: `"{\"url\":\"http://x\"}"`},
			},
			wantFinish: "tool_calls",
		},
		{
			name: "two parallel calls in one delta array",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"f","arguments":"{\"x\":1}"}},{"index":1,"id":"b","function":{"name":"g","arguments":"{\"y\":2}"}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{
				{ID: "a", Function: "f", Args: `"{\"x\":1}"`},
				{ID: "b", Function: "g", Args: `"{\"y\":2}"`},
			},
		},
		{
			// Some backends omit `index` and simply emit calls back to
			// back; a new non-empty id is the only boundary marker.
			name: "sequential unindexed calls separated by new id",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"id":"a","function":{"name":"f","arguments":"{\"x\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"1}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"id":"b","function":{"name":"g","arguments":"{\"y\":2}"}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{
				{ID: "a", Function: "f", Args: `"{\"x\":1}"`},
				{ID: "b", Function: "g", Args: `"{\"y\":2}"`},
			},
		},
		{
			name: "content interleaved with tool-call fragments",
			sse: `data: {"choices":[{"delta":{"content":"Let me "}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"web.search","arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"content":"look "}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"go\"}"}}]}}]}

data: {"choices":[{"delta":{"content":"that up."}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`,
			wantCalls:   []wantCall{{ID: "c", Function: "web.search", Args: `"{\"q\":\"go\"}"`}},
			wantContent: "Let me look that up.",
			wantFinish:  "tool_calls",
		},
		{
			name: "zero-arg tool with absent arguments",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"clock.now"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`,
			wantCalls:  []wantCall{{ID: "c", Function: "clock.now", Args: `"{}"`}},
			wantFinish: "tool_calls",
		},
		{
			name: "zero-arg tool with empty-string arguments fragments",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"clock.now","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":""}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{{ID: "c", Function: "clock.now", Args: `"{}"`}},
		},
		{
			// litellm normalization variance: arguments arrive as a whole
			// JSON object rather than a fragmented string.
			name: "arguments as a whole json object",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":{"q":"go"}}}]}}]}

data: [DONE]

`,
			wantCalls: []wantCall{{ID: "c", Function: "f", Args: `"{\"q\":\"go\"}"`}},
		},
		{
			// A tool call whose merged arguments are still an unterminated
			// object: dropped, never emitted truncated.
			name: "torn stream ends mid-fragment",
			sse: `data: {"choices":[{"delta":{"content":"one sec"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"web.search","arguments":"{\"q\":\"go"}}]}}]}

`,
			wantCalls:   nil,
			wantContent: "one sec",
			wantErrSub:  "not valid JSON after merge",
		},
		{
			// A complete sibling call survives the drop of a torn one.
			name: "one complete call survives a torn sibling",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"f","arguments":"{\"x\":1}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"g","arguments":"{\"y\":"}}]}}]}

`,
			wantCalls:  []wantCall{{ID: "a", Function: "f", Args: `"{\"x\":1}"`}},
			wantErrSub: "index 1 (g)",
		},
		{
			// Concatenation completes but is not JSON at all — a provider
			// defect. Same treatment as a tear.
			name: "malformed after concatenation",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":"{\"q\": go}"}}]}}]}

data: [DONE]

`,
			wantCalls:  nil,
			wantErrSub: "not valid JSON after merge",
		},
		{
			// Arguments arrived but no function name ever did: unexecutable.
			name: "fragments with no function name",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"arguments":"{\"x\":1}"}}]}}]}

data: [DONE]

`,
			wantCalls:  nil,
			wantErrSub: "no function name",
		},
		{
			// No [DONE] terminator, but every call is complete: a clean
			// EOF, not an error.
			name: "clean eof without DONE terminator",
			sse: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":"{\"x\":1}"}}]}}]}

`,
			wantCalls: []wantCall{{ID: "c", Function: "f", Args: `"{\"x\":1}"`}},
		},
		{
			name: "content-only stream merges no calls",
			sse: `data: {"choices":[{"delta":{"content":"hello"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`,
			wantCalls:   nil,
			wantContent: "hello",
			wantFinish:  "stop",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, final, termErr := runStream(t, tc.sse)

			if tc.wantErrSub == "" {
				if !errors.Is(termErr, io.EOF) {
					t.Fatalf("terminal error: got %v, want io.EOF", termErr)
				}
			} else {
				if termErr == nil || errors.Is(termErr, io.EOF) {
					t.Fatalf("terminal error: got %v, want an error containing %q", termErr, tc.wantErrSub)
				}
				if !strings.Contains(termErr.Error(), tc.wantErrSub) {
					t.Errorf("terminal error %q does not contain %q", termErr, tc.wantErrSub)
				}
			}

			got := projectCalls(final.ToolCalls)
			if len(got) != len(tc.wantCalls) {
				t.Fatalf("ToolCalls: got %+v, want %+v", got, tc.wantCalls)
			}
			for i := range got {
				if got[i] != tc.wantCalls[i] {
					t.Errorf("ToolCalls[%d]: got %+v, want %+v", i, got[i], tc.wantCalls[i])
				}
			}
			// Every emitted call's Args must be parseable, and must
			// decode to the argument JSON — the property the execution
			// loop depends on.
			for _, c := range final.ToolCalls {
				var s string
				if err := json.Unmarshal(c.Args, &s); err != nil {
					t.Errorf("Args %s is not a JSON string: %v", c.Args, err)
					continue
				}
				if !json.Valid([]byte(s)) {
					t.Errorf("decoded Args %q is not valid JSON", s)
				}
			}
			if final.Content != tc.wantContent {
				t.Errorf("Content: got %q, want %q", final.Content, tc.wantContent)
			}
			if final.FinishReason != tc.wantFinish {
				t.Errorf("FinishReason: got %q, want %q", final.FinishReason, tc.wantFinish)
			}
		})
	}
}

// TestStreamChunkToolCallsAreIdentityOnly pins the deliberate Chunk
// contract: a per-chunk tool-call entry carries the identity known so far
// and NEVER carries Args. A chunk holds a slice of the argument string,
// which is not valid JSON on its own — there is nothing safe to surface.
func TestStreamChunkToolCallsAreIdentityOnly(t *testing.T) {
	const sse = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"web.","arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"search","arguments":"\"go\"}"}}]}}]}

data: [DONE]

`
	chunks, final, termErr := runStream(t, sse)
	if !errors.Is(termErr, io.EOF) {
		t.Fatalf("terminal error: %v", termErr)
	}
	var withTools []Chunk
	for _, c := range chunks {
		if len(c.ToolCalls) > 0 {
			withTools = append(withTools, c)
		}
	}
	if len(withTools) != 2 {
		t.Fatalf("chunks carrying tool calls: got %d, want 2", len(withTools))
	}
	for i, c := range withTools {
		if len(c.ToolCalls) != 1 {
			t.Fatalf("chunk %d: got %d tool calls, want 1", i, len(c.ToolCalls))
		}
		if c.ToolCalls[0].Args != nil {
			t.Errorf("chunk %d: Args must be nil, got %s", i, c.ToolCalls[0].Args)
		}
		if c.ToolCalls[0].ID != "call_1" {
			t.Errorf("chunk %d: ID: got %q, want call_1", i, c.ToolCalls[0].ID)
		}
	}
	// The name accumulates as fragments arrive.
	if got := withTools[0].ToolCalls[0].Function; got != "web." {
		t.Errorf("first chunk Function: got %q, want %q", got, "web.")
	}
	if got := withTools[1].ToolCalls[0].Function; got != "web.search" {
		t.Errorf("second chunk Function: got %q, want %q", got, "web.search")
	}
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].Function != "web.search" {
		t.Errorf("Final().ToolCalls: got %+v", final.ToolCalls)
	}
}

// TestStreamFinalMidStreamIsASnapshot: Final() called while a tool call is
// still arriving must return a snapshot, not a verdict — a call whose
// arguments are still incomplete is withheld, and a LATER Final (after the
// remaining fragments land) must see the finished call rather than the
// stale snapshot. Repeated Final calls never duplicate.
func TestStreamFinalMidStreamIsASnapshot(t *testing.T) {
	const sse = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":"{\"x\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}

data: [DONE]

`
	sr := newHTTPStreamReader(context.Background(), io.NopCloser(strings.NewReader(sse)))
	defer sr.Close()

	if _, err := sr.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	// Mid-call: arguments are `{"x":` so far — not valid JSON, withheld.
	if calls := sr.Final().ToolCalls; len(calls) != 0 {
		t.Errorf("mid-call Final: got %+v, want no calls", calls)
	}
	if _, err := sr.Next(); err != nil {
		t.Fatalf("second Next: %v", err)
	}
	// The remaining fragment landed; the snapshot must move.
	got := projectCalls(sr.Final().ToolCalls)
	want := []wantCall{{ID: "c", Function: "f", Args: `"{\"x\":1}"`}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("post-fragment Final: got %+v, want %+v", got, want)
	}
	if _, err := sr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next: got %v, want io.EOF", err)
	}
	if got := projectCalls(sr.Final().ToolCalls); len(got) != 1 || got[0] != want[0] {
		t.Errorf("Final after EOF: got %+v, want %+v", got, want)
	}
}

// TestStreamFinalAfterEarlyClose: aborting mid-tool-call (the §5.5 /
// D6 re-issue path in internal/turn) must not surface a truncated call.
func TestStreamFinalAfterEarlyClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"f","arguments":"{\"x\":"}}]}}]}

`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	sr, err := c.ConsultStream(context.Background(), DefaultRequest("m", []Message{{Role: "user", Content: "x"}}))
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	if _, err := sr.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if calls := sr.Final().ToolCalls; len(calls) != 0 {
		t.Errorf("Final().ToolCalls after early close: got %+v, want none", calls)
	}
}

// TestConsultStreamTransmitsTools pins that the STREAMING path puts
// Request.Tools on the wire (the non-streaming path is covered by
// TestHTTPClientToolsSerialization). Wave 2's execution loop depends on
// this plumbing existing on both flavors.
func TestConsultStreamTransmitsTools(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	req := DefaultRequest("m", []Message{{Role: "user", Content: "search for go"}})
	req.Tools = []ToolSpec{{
		Name:        "web.search",
		Description: "search the web",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}}
	sr, err := c.ConsultStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	_, _ = sr.Next()
	_ = sr.Close()

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if got["stream"] != true {
		t.Errorf("stream: got %v, want true", got["stream"])
	}
	if got["tool_choice"] != "auto" {
		t.Errorf("tool_choice: got %v, want auto", got["tool_choice"])
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1 (body: %s)", len(tools), body)
	}
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "web.search" {
		t.Errorf("tool name: got %v, want web.search", fn["name"])
	}
}

// TestArgFragment covers the fragment-extraction edge cases directly: the
// wire field is canonically a JSON string but litellm-normalized backends
// vary, and whitespace inside a string fragment is significant.
func TestArgFragment(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"absent", "", ""},
		{"json null", "null", ""},
		{"empty string", `""`, ""},
		{"string fragment", `"{\"q\":"`, `{"q":`},
		{"significant whitespace", `" "`, " "},
		{"escaped quote inside fragment", `"\"go\""`, `"go"`},
		{"whole object", `{"q":"go"}`, `{"q":"go"}`},
		{"whole array", `[1,2]`, `[1,2]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := argFragment(json.RawMessage(tc.raw)); got != tc.want {
				t.Errorf("argFragment(%s) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestNoToolsFieldWhenInventoryIsEmpty — the wire counterpart of the
// wave-2 registry rule: an empty tool inventory must leave `tools` (and
// `tool_choice`) OFF the request entirely. Advertising an empty tool list
// invites calls nothing can service — the live probe already showed this
// provider emitting tool calls unprompted.
func TestNoToolsFieldWhenInventoryIsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []ToolSpec
	}{
		{"nil", nil},
		{"empty slice", []ToolSpec{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				req := DefaultRequest("m", []Message{{Role: "user", Content: "hi"}})
				req.Tools = tc.tools
				body, err := encodeRequest(req, stream)
				if err != nil {
					t.Fatalf("encodeRequest(stream=%v): %v", stream, err)
				}
				var got map[string]any
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if _, present := got["tools"]; present {
					t.Errorf("stream=%v: `tools` present on an empty inventory: %s", stream, body)
				}
				if _, present := got["tool_choice"]; present {
					t.Errorf("stream=%v: `tool_choice` present on an empty inventory: %s", stream, body)
				}
			}
		})
	}
}
