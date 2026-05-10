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
