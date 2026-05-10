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
	"time"

	"personant/internal/store"
)

const testAPIKey = "sk-TEST-MUST-NOT-LEAK-1234567890abcdef"

func newTestProvider(baseURL string) store.Provider {
	return store.Provider{
		Name:         "test",
		BaseURL:      baseURL,
		APIKey:       testAPIKey,
		DefaultModel: "test-model",
	}
}

// TestHTTPClientHappyPath: verify the request body shape, the auth header,
// the User-Agent header, and the response decode round-trip.
func TestHTTPClientHappyPath(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotUA string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"index": 0,
				"finish_reason": "stop",
				"message": {"role": "assistant", "content": "hello world"}
			}],
			"usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}
		}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	resp, err := c.Consult(context.Background(), Request{
		Model: "test-model",
		Messages: []Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path: got %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer "+testAPIKey {
		t.Errorf("auth header missing or wrong: got %q", gotAuth)
	}
	if gotUA != userAgent {
		t.Errorf("user-agent: got %q, want %q", gotUA, userAgent)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model in body: got %v, want test-model", gotBody["model"])
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Errorf("messages count: got %d, want 2", len(msgs))
	}

	if resp.Content != "hello world" {
		t.Errorf("content: got %q, want %q", resp.Content, "hello world")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish_reason: got %q, want stop", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("total_tokens: got %d, want 7", resp.Usage.TotalTokens)
	}
}

// TestHTTPClientBaseURLTrailingSlash: a base URL ending in "/" must produce
// "<base>chat/completions", not "<base>/chat/completions".
func TestHTTPClientBaseURLTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	p := newTestProvider(srv.URL + "/")
	c := NewHTTPClient(p)
	if _, err := c.Consult(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}); err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path with trailing-slash base: got %q, want /chat/completions", gotPath)
	}
}

// TestHTTPClientNoAPIKeyOmitsAuthHeader: an empty APIKey must not produce a
// "Bearer " header (some local servers reject empty bearer tokens).
func TestHTTPClientNoAPIKeyOmitsAuthHeader(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	p := store.Provider{Name: "nokey", BaseURL: srv.URL, DefaultModel: "m"}
	c := NewHTTPClient(p)
	if _, err := c.Consult(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}); err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if sawAuth {
		t.Errorf("Authorization header sent despite empty APIKey")
	}
}

// TestHTTPClientHTTPErrorWraps: a 5xx response is surfaced as a wrapped
// error, and the API key never appears in the error string even when the
// server echoes the Authorization header back.
func TestHTTPClientHTTPErrorWraps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo what we got, including the Authorization header (this is the
		// scenario the scrubber exists to defend against).
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error: auth was " + r.Header.Get("Authorization") + " but who cares"))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	_, err := c.Consult(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error from 500, got nil")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error missing status code: %v", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("API key leaked into 5xx error: %v", err)
	}
}

// TestHTTPClientContextCancellation: a cancelled context must propagate
// through Do() and be reachable via errors.Is(err, context.Canceled).
func TestHTTPClientContextCancellation(t *testing.T) {
	// Server holds the response until either the request context fires
	// or the test signals the stop channel. The stop channel is closed
	// before srv.Close so the handler returns and srv.Close's
	// WaitGroup.Wait doesn't block on an outstanding connection.
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer func() {
		close(stop)
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	c := NewHTTPClient(newTestProvider(srv.URL))
	_, err := c.Consult(ctx, Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error from cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestHTTPClientToolsSerialization: when Tools are present, the request
// must include a tools array with the OpenAI shape and tool_choice = auto.
func TestHTTPClientToolsSerialization(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	_, err := c.Consult(context.Background(), Request{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "search please"}},
		Tools: []ToolSpec{{
			Name:        "search",
			Description: "do a search",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if gotBody["tool_choice"] != "auto" {
		t.Errorf("tool_choice: got %v, want auto", gotBody["tool_choice"])
	}
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools count: got %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type: got %v, want function", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "search" {
		t.Errorf("tool name: got %v, want search", fn["name"])
	}
}

// TestHTTPClientErrorScrubsAPIKey: smoke-test the scrubber against a
// pathological payload that intentionally embeds the key in the body.
func TestHTTPClientErrorScrubsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// Worst case: server reflects the key into the error body.
		_, _ = w.Write([]byte("bad request: token=" + testAPIKey))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	_, err := c.Consult(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("API key leaked into error string: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Errorf("expected <redacted> sentinel in scrubbed error, got: %v", err)
	}
}

// TestHTTPClientEmptyBaseURLErrors: an empty BaseURL must produce a clear
// error rather than an HTTP attempt against a malformed URL.
func TestHTTPClientEmptyBaseURLErrors(t *testing.T) {
	c := NewHTTPClient(store.Provider{Name: "x"})
	_, err := c.Consult(context.Background(), Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
	if err == nil {
		t.Fatal("expected error on empty BaseURL, got nil")
	}
	if !strings.Contains(err.Error(), "BaseURL") {
		t.Errorf("error doesn't mention BaseURL: %v", err)
	}
}

// TestHTTPClientWiresSamplingDefaults: verify that DefaultRequest's values
// (Temperature: 0, MaxTokens: 16384, chat_template_kwargs with thinking
// keys) round-trip onto the wire — `temperature: 0` must be PRESENT in
// the body, not omitted as it would be with `omitempty`.
func TestHTTPClientWiresSamplingDefaults(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	req := DefaultRequest("test-model", []Message{{Role: "user", Content: "hi"}})
	if _, err := c.Consult(context.Background(), req); err != nil {
		t.Fatalf("Consult: %v", err)
	}

	// Decode raw to assert presence (a map[string]any decode would lose the
	// "0 vs absent" distinction we care about; check the raw bytes too).
	var asMap map[string]any
	if err := json.Unmarshal(rawBody, &asMap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := asMap["temperature"]; !ok {
		t.Errorf("temperature missing from body: %s", rawBody)
	} else if v != 0.0 {
		t.Errorf("temperature: got %v, want 0", v)
	}
	if v, ok := asMap["max_tokens"]; !ok {
		t.Errorf("max_tokens missing from body: %s", rawBody)
	} else if v != float64(16384) {
		t.Errorf("max_tokens: got %v, want 16384", v)
	}
	// Raw-bytes check that omitempty hasn't dropped temperature: 0.
	if !strings.Contains(string(rawBody), `"temperature":0`) {
		t.Errorf("expected literal `\"temperature\":0` in body: %s", rawBody)
	}
	// chat_template_kwargs presence + thinking keys.
	ctk, ok := asMap["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("chat_template_kwargs missing or wrong type: %v", asMap["chat_template_kwargs"])
	}
	if ctk["thinking"] != true {
		t.Errorf("chat_template_kwargs.thinking: got %v, want true", ctk["thinking"])
	}
	if ctk["enable_thinking"] != true {
		t.Errorf("chat_template_kwargs.enable_thinking: got %v, want true", ctk["enable_thinking"])
	}
	// stream is false (or absent) for blocking Consult.
	if v, ok := asMap["stream"]; ok && v != false {
		t.Errorf("stream: got %v, want absent or false", v)
	}
}

// TestHTTPClientChatTemplateKwargsOmittedWhenEmpty: an empty/nil map must
// not emit a `chat_template_kwargs: null` or `: {}` field on the wire —
// some providers reject the field's mere presence.
func TestHTTPClientChatTemplateKwargsOmittedWhenEmpty(t *testing.T) {
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	// Build directly (not via DefaultRequest) so ChatTemplateKwargs stays nil.
	req := Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}}
	if _, err := c.Consult(context.Background(), req); err != nil {
		t.Fatalf("Consult: %v", err)
	}
	if strings.Contains(string(rawBody), "chat_template_kwargs") {
		t.Errorf("chat_template_kwargs should be omitted when empty; got body: %s", rawBody)
	}
}

// TestHTTPClientStreamHappyPath: SSE round-trip. Verify chunks come out in
// order, Final() concat matches the underlying body, finish_reason and
// usage populate, the wire body has stream: true, and the Accept header
// is text/event-stream.
func TestHTTPClientStreamHappyPath(t *testing.T) {
	var gotAccept string
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		// Three content deltas, then a final chunk with finish_reason
		// + usage, then [DONE].
		const sse = `data: {"choices":[{"delta":{"content":"Hello"}}]}

data: {"choices":[{"delta":{"content":" "}}]}

data: {"choices":[{"delta":{"content":"world"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}

data: [DONE]

`
		_, _ = w.Write([]byte(sse))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	req := DefaultRequest("test-model", []Message{{Role: "user", Content: "hi"}})
	sr, err := c.ConsultStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	defer sr.Close()

	var got strings.Builder
	chunks := 0
	var lastChunk Chunk
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got.WriteString(chunk.Content)
		chunks++
		lastChunk = chunk
	}
	if chunks < 3 {
		t.Errorf("expected at least 3 chunks; got %d", chunks)
	}
	if got.String() != "Hello world" {
		t.Errorf("concat content: got %q, want %q", got.String(), "Hello world")
	}
	final := sr.Final()
	if final.Content != "Hello world" {
		t.Errorf("Final.Content: got %q, want %q", final.Content, "Hello world")
	}
	if final.FinishReason != "stop" {
		t.Errorf("Final.FinishReason: got %q, want stop (lastChunk=%+v)", final.FinishReason, lastChunk)
	}
	if final.Usage.TotalTokens != 6 {
		t.Errorf("Final.Usage.TotalTokens: got %d, want 6", final.Usage.TotalTokens)
	}

	if gotAccept != "text/event-stream" {
		t.Errorf("Accept header: got %q, want text/event-stream", gotAccept)
	}
	if !strings.Contains(string(rawBody), `"stream":true`) {
		t.Errorf("expected `\"stream\":true` in body: %s", rawBody)
	}
}

// TestHTTPClientStreamErrorWraps: a 5xx during stream initiation is
// surfaced as a wrapped error, with the API key scrubbed.
func TestHTTPClientStreamErrorWraps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("bad: token=" + testAPIKey))
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	_, err := c.ConsultStream(context.Background(), DefaultRequest("m", []Message{{Role: "user", Content: "x"}}))
	if err == nil {
		t.Fatal("expected error from 500, got nil")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error missing status code: %v", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("API key leaked into 5xx error: %v", err)
	}
}

// TestHTTPClientStreamCloseBeforeEOF: closing the reader before exhausting
// it should release the body without leaking. Subsequent Next() returns
// io.EOF.
func TestHTTPClientStreamCloseBeforeEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"a"}}]}

data: {"choices":[{"delta":{"content":"b"}}]}

`))
		// Hold the connection so the reader sees only what's been flushed.
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	sr, err := c.ConsultStream(context.Background(), DefaultRequest("m", []Message{{Role: "user", Content: "x"}}))
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	chunk, err := sr.Next()
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if chunk.Content != "a" {
		t.Errorf("first chunk content: got %q, want a", chunk.Content)
	}
	if err := sr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Idempotent.
	if err := sr.Close(); err != nil {
		t.Errorf("Close (second): %v", err)
	}
	// After Close, Next returns io.EOF.
	if _, err := sr.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next after Close: got %v, want io.EOF", err)
	}
}

// TestHTTPClientStreamCtxCancellation: a cancelled context mid-stream
// should surface via Next().
func TestHTTPClientStreamCtxCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := NewHTTPClient(newTestProvider(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	sr, err := c.ConsultStream(ctx, DefaultRequest("m", []Message{{Role: "user", Content: "x"}}))
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	defer sr.Close()
	// Drain the first chunk.
	if _, err := sr.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	cancel()
	// Subsequent Next must surface the ctx error (either via the ctx
	// check or via the underlying connection failing).
	_, err = sr.Next()
	if err == nil {
		t.Fatal("expected error after cancel, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
