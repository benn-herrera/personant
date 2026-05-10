package ping

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"personant/internal/model"
)

// TestRunWithClientStreamsToStdout: a multi-chunk scripted mock should land
// chunk content on stdout in order; the summary line lands on stderr.
func TestRunWithClientStreamsToStdout(t *testing.T) {
	mock := model.NewScriptedMock([]model.Response{
		{
			Content:      "Hello from the mock provider.",
			FinishReason: "stop",
			Usage:        model.Usage{PromptTokens: 4, CompletionTokens: 6, TotalTokens: 10},
		},
	}, nil)
	mock.SetMockChunks(5)

	var stdout, stderr bytes.Buffer
	err := runWithClient(mock, Options{
		Provider: "test",
		Prompt:   "say hi",
		Stdout:   &stdout,
		Stderr:   &stderr,
	}, "test-model", 5*time.Second)
	if err != nil {
		t.Fatalf("runWithClient: %v", err)
	}
	if !strings.Contains(stdout.String(), "Hello from the mock provider.") {
		t.Errorf("body missing from stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "ping ok: test test-model") {
		t.Errorf("summary missing from stderr: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "tokens=4/6/10") {
		t.Errorf("token counts missing from summary: %q", stderr.String())
	}
}

// TestRunWithClientNoTopicTagFilter: the topic-tag-shaped first line must
// pass through unchanged (ping is a free-form connectivity probe — no
// filter applies).
func TestRunWithClientNoTopicTagFilter(t *testing.T) {
	mock := model.NewScriptedMock([]model.Response{
		{
			Content:      "*topic: thr_1 [a, b, c, d]*\nbody after",
			FinishReason: "stop",
			Usage:        model.Usage{TotalTokens: 1},
		},
	}, nil)
	mock.SetMockChunks(3)

	var stdout, stderr bytes.Buffer
	if err := runWithClient(mock, Options{
		Provider: "test",
		Prompt:   "p",
		Stdout:   &stdout,
		Stderr:   &stderr,
	}, "test-model", 5*time.Second); err != nil {
		t.Fatalf("runWithClient: %v", err)
	}
	// ping does NOT use the streamfilter — the topic-tag-shaped line
	// should appear verbatim on stdout.
	if !strings.Contains(stdout.String(), "*topic: thr_1") {
		t.Errorf("ping should NOT strip topic tag; got stdout %q", stdout.String())
	}
}

// TestRunWithClientUsesDefaultRequest: every Consult/ConsultStream call
// from ping flows through DefaultRequest. The mock's recorded request
// should carry the documented sampling defaults.
func TestRunWithClientUsesDefaultRequest(t *testing.T) {
	mock := model.NewScriptedMock([]model.Response{{Content: "ok", FinishReason: "stop"}}, nil)
	var stdout, stderr bytes.Buffer
	if err := runWithClient(mock, Options{
		Provider: "test",
		Prompt:   "x",
		Stdout:   &stdout,
		Stderr:   &stderr,
	}, "test-model", 5*time.Second); err != nil {
		t.Fatalf("runWithClient: %v", err)
	}
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call; got %d", len(calls))
	}
	req := calls[0].Request
	if req.MaxTokens != 16384 {
		t.Errorf("MaxTokens: got %d, want 16384", req.MaxTokens)
	}
	if req.Temperature != 0 {
		t.Errorf("Temperature: got %v, want 0", req.Temperature)
	}
	if v, ok := req.ChatTemplateKwargs["thinking"]; !ok || v != true {
		t.Errorf("ChatTemplateKwargs.thinking: got %v (present=%v), want true", v, ok)
	}
	if v, ok := req.ChatTemplateKwargs["enable_thinking"]; !ok || v != true {
		t.Errorf("ChatTemplateKwargs.enable_thinking: got %v (present=%v), want true", v, ok)
	}
}
