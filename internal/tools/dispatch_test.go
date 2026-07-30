package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"personant/internal/model"
)

func TestDispatchSuccess(t *testing.T) {
	r := NewRegistry()
	var gotArgs string
	if err := r.Register(Tool{
		Spec: model.ToolSpec{Name: "web.fetch"},
		Handler: func(_ context.Context, args json.RawMessage) ([]byte, error) {
			gotArgs = string(args)
			return []byte("page body"), nil
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res := r.Dispatch(context.Background(), model.ToolCall{
		ID: "call_1", Function: "web.fetch", Args: json.RawMessage(`"{\"url\":\"x\"}"`),
	})
	if res.Err != nil {
		t.Fatalf("Dispatch err = %v", res.Err)
	}
	if res.Content != "page body" {
		t.Errorf("Content = %q; want %q", res.Content, "page body")
	}
	if res.CallID != "call_1" || res.Name != "web.fetch" {
		t.Errorf("identity = %q/%q", res.CallID, res.Name)
	}
	if gotArgs != `"{\"url\":\"x\"}"` {
		t.Errorf("handler saw args %q; want the raw JSON value verbatim", gotArgs)
	}
}

// TestDispatchEmptyOutput — success with zero bytes must still produce
// readable content: an empty tool message is ambiguous to the model and
// rejected outright by some providers.
func TestDispatchEmptyOutput(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Tool{
		Spec:    model.ToolSpec{Name: "web.search"},
		Handler: func(context.Context, json.RawMessage) ([]byte, error) { return nil, nil },
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := r.Dispatch(context.Background(), model.ToolCall{ID: "c", Function: "web.search"})
	if res.Err != nil {
		t.Fatalf("Dispatch err = %v", res.Err)
	}
	if strings.TrimSpace(res.Content) == "" {
		t.Error("empty handler output produced an empty tool message")
	}
}

// TestDispatchUnknownTool — the model WILL call tools we do not have. The
// result must be a message it can act on (naming the tool and the real
// inventory), with a matchable cause for the log line, and never a crash
// or a dropped message.
func TestDispatchUnknownTool(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(okTool("web.fetch", "x")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	res := r.Dispatch(context.Background(), model.ToolCall{ID: "c9", Function: "bash.run"})
	if !errors.Is(res.Err, ErrUnknownTool) {
		t.Fatalf("Err = %v; want errors.Is ErrUnknownTool", res.Err)
	}
	if res.Aborted {
		t.Error("unknown tool must not report Aborted — the turn continues")
	}
	if res.CallID != "c9" {
		t.Errorf("CallID = %q; the tool message must answer the call that failed", res.CallID)
	}
	for _, want := range []string{"bash.run", "web.fetch"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("Content %q missing %q", res.Content, want)
		}
	}

	// With nothing registered at all the message says so rather than
	// printing an empty inventory.
	empty := NewRegistry().Dispatch(context.Background(), model.ToolCall{Function: "bash.run"})
	if !strings.Contains(empty.Content, "no tools are available") {
		t.Errorf("empty-registry content = %q", empty.Content)
	}
}

// TestDispatchNamelessCall — a malformed call with no function name is
// still answered, so the provider's N-calls→N-replies invariant holds.
func TestDispatchNamelessCall(t *testing.T) {
	res := NewRegistry().Dispatch(context.Background(), model.ToolCall{ID: "c0"})
	if res.Err == nil {
		t.Fatal("nameless call: want an error cause")
	}
	if strings.TrimSpace(res.Content) == "" {
		t.Error("nameless call produced no tool message")
	}
}

func TestDispatchHandlerError(t *testing.T) {
	r := NewRegistry()
	boom := errors.New("connection refused\nwhile dialing")
	if err := r.Register(Tool{
		Spec:    model.ToolSpec{Name: "web.fetch"},
		Handler: func(context.Context, json.RawMessage) ([]byte, error) { return nil, boom },
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := r.Dispatch(context.Background(), model.ToolCall{ID: "c", Function: "web.fetch"})
	if !errors.Is(res.Err, boom) {
		t.Fatalf("Err = %v; want the handler's error wrapped", res.Err)
	}
	if res.Aborted {
		t.Error("a handler error must not report Aborted")
	}
	if !strings.Contains(res.Content, "connection refused while dialing") {
		t.Errorf("Content = %q; want the cause collapsed onto one line", res.Content)
	}
}

// TestDispatchGivesHandlerADeadline — a wedged handler must cost a
// BOUNDED wait, which requires the deadline to actually reach it. Asserted
// on the handler's own view of its context rather than by waiting
// DefaultTimeout out.
func TestDispatchGivesHandlerADeadline(t *testing.T) {
	r := NewRegistry()
	var deadline time.Time
	var hasDeadline bool
	if err := r.Register(Tool{
		Spec: model.ToolSpec{Name: "web.fetch"},
		Handler: func(ctx context.Context, _ json.RawMessage) ([]byte, error) {
			deadline, hasDeadline = ctx.Deadline()
			return []byte("ok"), nil
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if res := r.Dispatch(context.Background(), model.ToolCall{Function: "web.fetch"}); res.Err != nil {
		t.Fatalf("Dispatch: %v", res.Err)
	}
	if !hasDeadline {
		t.Fatal("handler ran with no deadline — a wedged tool would hang the turn")
	}
	if d := time.Until(deadline); d > DefaultTimeout+time.Second || d <= 0 {
		t.Errorf("handler deadline is %v out; want ~%v", d, DefaultTimeout)
	}
}

// TestDispatchTimeoutShape exercises the timeout branch without waiting
// DefaultTimeout: a handler that reports context.DeadlineExceeded while
// the caller's context is alive is, by definition, a tool that ran out of
// its own budget.
func TestDispatchTimeoutShape(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Tool{
		Spec: model.ToolSpec{Name: "web.fetch"},
		Handler: func(context.Context, json.RawMessage) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	res := r.Dispatch(context.Background(), model.ToolCall{ID: "c", Function: "web.fetch"})
	if res.Aborted {
		t.Fatal("a tool timeout must not report Aborted — the turn continues")
	}
	if !strings.Contains(res.Content, "timed out") {
		t.Errorf("Content = %q; want a timeout message", res.Content)
	}
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Errorf("Err = %v; want DeadlineExceeded wrapped", res.Err)
	}
}

// TestDispatchCancellation — an Esc abort (or session shutdown) reaching a
// running tool is the CALLER's business, not the model's: Aborted is set,
// Content is empty, and the caller abandons the turn instead of feeding
// the model a result nobody will read.
func TestDispatchCancellation(t *testing.T) {
	r := NewRegistry()
	started := make(chan struct{})
	if err := r.Register(Tool{
		Spec: model.ToolSpec{Name: "web.fetch"},
		Handler: func(ctx context.Context, _ json.RawMessage) ([]byte, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	defer cancel()

	done := make(chan Result, 1)
	go func() { done <- r.Dispatch(ctx, model.ToolCall{ID: "c", Function: "web.fetch"}) }()

	select {
	case res := <-done:
		if !res.Aborted {
			t.Fatalf("Aborted = false; want true (Err=%v Content=%q)", res.Err, res.Content)
		}
		if res.Content != "" {
			t.Errorf("Content = %q; a cancelled turn gets no model-facing text", res.Content)
		}
		if !errors.Is(res.Err, context.Canceled) {
			t.Errorf("Err = %v; want context.Canceled", res.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch did not return after the context was cancelled")
	}
}
