package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// MockServer serves a MockClient's responses over the OpenAI
// chat-completions wire as SSE, for driving an external agent (opencode)
// against the scripted sim. Not safe to share between unrelated runs:
// Enqueue and TakeRequests are global to the server.
type MockServer struct {
	m *MockClient

	mu       sync.Mutex
	queue    []Response
	requests []ServedRequest
}

// ServedRequest records one chat-completions request the server answered.
type ServedRequest struct {
	Arrived        time.Time
	Duration       time.Duration
	Messages       int
	SystemMessages int
	SystemHash     string
	Usage          Usage
}

// NewMockServer returns a server that answers from m once its own queue
// is empty.
func NewMockServer(m *MockClient) *MockServer {
	return &MockServer{m: m}
}

// Enqueue appends responses served, one per request, before falling back
// to m's current response.
func (s *MockServer) Enqueue(rs ...Response) {
	s.mu.Lock()
	s.queue = append(s.queue, rs...)
	s.mu.Unlock()
}

// TakeRequests drains the records accumulated since the last call.
func (s *MockServer) TakeRequests() []ServedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.requests
	s.requests = nil
	return out
}

// SystemPromptHash is the SHA-256 hex of text's exact bytes.
func SystemPromptHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

const mockServerModel = "mock-model"

func (s *MockServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/models"):
		s.serveModels(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/chat/completions"):
		s.serveChat(w, r)
	default:
		http.Error(w, `{"error":{"message":"not found"}}`, http.StatusNotFound)
	}
}

func (s *MockServer) serveModels(w http.ResponseWriter, r *http.Request) {
	infos, err := s.m.ListModels(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	list := wireModelList{Object: "list", Data: []wireModel{}}
	for _, mi := range infos {
		list.Data = append(list.Data, wireModel{ID: mi.ID, Object: "model", Created: mi.Created, OwnedBy: mi.OwnedBy})
	}
	if len(list.Data) == 0 {
		list.Data = append(list.Data, wireModel{ID: mockServerModel, Object: "model", OwnedBy: "mock"})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// mockServerRequest decodes only what the server reads. Content stays raw
// because opencode sends either a string or an array of text parts.
type mockServerRequest struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// contentText returns a message's text: the string as received, or the
// array form's part texts joined with sep.
func contentText(raw json.RawMessage, sep string) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	texts := make([]string, len(parts))
	for i, p := range parts {
		texts[i] = p.Text
	}
	return strings.Join(texts, sep)
}

func (s *MockServer) serveChat(w http.ResponseWriter, r *http.Request) {
	arrived := time.Now()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req mockServerRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rec := ServedRequest{Arrived: arrived, Messages: len(req.Messages)}
	for _, msg := range req.Messages {
		if msg.Role != "system" {
			continue
		}
		if rec.SystemMessages == 0 {
			// opencode joins array-form system parts with "\n" in its
			// string form; joining the same way keeps the hash comparable.
			rec.SystemHash = SystemPromptHash(contentText(msg.Content, "\n"))
		}
		rec.SystemMessages++
	}

	resp, err := s.next(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rec.Usage = resp.Usage
	if rec.Usage.PromptTokens == 0 {
		rec.Usage = estimatedUsage(len(raw), resp)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	frames, err := sseFrames(resp, rec.Usage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	for _, frame := range frames {
		if _, err := w.Write(frame); err != nil {
			return
		}
		flusher.Flush()
	}

	rec.Duration = time.Since(arrived)
	s.mu.Lock()
	s.requests = append(s.requests, rec)
	s.mu.Unlock()
}

// next pops the queue, else consults the backing mock.
func (s *MockServer) next(ctx context.Context) (Response, error) {
	s.mu.Lock()
	if len(s.queue) > 0 {
		resp := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		return resp, nil
	}
	s.mu.Unlock()
	return s.m.Consult(ctx, Request{})
}

// estimatedUsage stands in for a provider's count when the scripted
// response carries none: about four bytes per token.
func estimatedUsage(reqBytes int, resp Response) Usage {
	completionBytes := len(resp.Content)
	for _, tc := range resp.ToolCalls {
		completionBytes += len(tc.Function) + len(argFragment(tc.Args))
	}
	u := Usage{PromptTokens: max(1, reqBytes/4), CompletionTokens: max(1, completionBytes/4)}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	return u
}

// sseFrames renders resp as chat.completion.chunk SSE frames: content in
// 64-rune deltas, each tool call's arguments split across two deltas, a
// finish frame, then usage on a choices-less frame, then [DONE].
func sseFrames(resp Response, u Usage) ([][]byte, error) {
	var frames [][]byte
	var err error
	add := func(choices []wireStreamChoice, usage *wireUsage) {
		if err != nil {
			return
		}
		var body []byte
		body, err = json.Marshal(struct {
			ID      string             `json:"id"`
			Object  string             `json:"object"`
			Created int64              `json:"created"`
			Model   string             `json:"model"`
			Choices []wireStreamChoice `json:"choices"`
			Usage   *wireUsage         `json:"usage,omitempty"`
		}{"chatcmpl-mock", "chat.completion.chunk", 1700000000, mockServerModel, choices, usage})
		frames = append(frames, append(append([]byte("data: "), body...), "\n\n"...))
	}
	delta := func(d wireStreamDelta) {
		add([]wireStreamChoice{{Delta: d}}, nil)
	}

	delta(wireStreamDelta{Role: "assistant"})
	content := []rune(resp.Content)
	for i := 0; i < len(content); i += 64 {
		delta(wireStreamDelta{Content: string(content[i:min(i+64, len(content))])})
	}
	for i, tc := range resp.ToolCalls {
		idx := i
		delta(wireStreamDelta{ToolCalls: []wireStreamToolCall{{
			Index: &idx, ID: tc.ID, Type: "function",
			Function: wireStreamToolFunction{Name: tc.Function, Arguments: json.RawMessage(`""`)},
		}}})
		args := []rune(argFragment(tc.Args))
		half := len(args) / 2
		for _, piece := range []string{string(args[:half]), string(args[half:])} {
			enc, _ := json.Marshal(piece) // a string always marshals
			delta(wireStreamDelta{ToolCalls: []wireStreamToolCall{{
				Index: &idx, Function: wireStreamToolFunction{Arguments: enc},
			}}})
		}
	}
	finish := resp.FinishReason
	if finish == "" {
		finish = "stop"
		if len(resp.ToolCalls) > 0 {
			finish = "tool_calls"
		}
	}
	add([]wireStreamChoice{{FinishReason: finish}}, nil)
	add([]wireStreamChoice{}, &wireUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	})
	return append(frames, []byte("data: [DONE]\n\n")), err
}
