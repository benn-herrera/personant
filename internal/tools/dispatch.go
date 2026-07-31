package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"personant/internal/model"
)

// Result is the outcome of one dispatched tool call.
//
// The contract Dispatch guarantees, and the reason it returns no error:
// EVERY call produces a Result whose Content is safe to hand back to the
// model as a tool message. A tool the runtime does not have, a handler
// that failed, a handler that hung — each becomes an error result the
// model can read and recover from, never a dropped turn and never a
// dropped message. An OpenAI-compatible provider that received an
// assistant message with N tool calls REQUIRES N tool messages back;
// silently omitting one wedges the conversation.
//
// Err is non-nil exactly when the call did not succeed. It carries the
// real cause for the caller's log line — the model sees Content, the event
// log sees Err.
//
// Aborted is the one outcome that is the CALLER's business rather than the
// model's: the caller's context ended (§4.3.3 Esc, session shutdown, the
// REPL's per-turn deadline) while the tool was running. Content is empty,
// Err is ctx.Err(), and the caller must abandon the turn rather than
// continue the conversation. It is a separate field and not an errors.Is
// test on Err because a tool TIMEOUT also wraps context.DeadlineExceeded
// while being an ordinary, model-recoverable result.
type Result struct {
	CallID  string
	Name    string
	Content string
	Err     error
	Aborted bool
}

// emptyResultContent stands in for a handler that returned zero bytes. A
// tool message with empty content is at best ambiguous to the model and at
// worst rejected by the provider, so success-with-nothing says so.
const emptyResultContent = "(the tool returned no output)"

// Dispatch runs one model-requested tool call and returns the result to
// feed back as a tool message. It never returns an error; see Result.
//
// The handler runs under a DefaultTimeout deadline derived from ctx, so a
// wedged tool costs a bounded wait and a cancellation of ctx reaches the
// handler immediately.
func (r *Registry) Dispatch(ctx context.Context, call model.ToolCall) Result {
	res := Result{CallID: call.ID, Name: call.Function}

	if call.Function == "" {
		res.Err = fmt.Errorf("%w: %q", ErrUnknownTool, "")
		res.Content = "ERROR: the tool call named no tool. Re-issue the call with a tool name, or answer without it."
		return res
	}
	tool, ok := r.Lookup(call.Function)
	if !ok {
		res.Err = fmt.Errorf("%w: %q", ErrUnknownTool, call.Function)
		res.Content = unknownToolMessage(call.Function, r.names())
		return res
	}

	hctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	out, err := tool.Handler(hctx, call.Args)

	switch {
	case ctx.Err() != nil:
		// The CALLER's context ended — the turn is going away. Not a tool
		// fault, and nothing the model can act on.
		res.Err = ctx.Err()
		res.Aborted = true
	case err == nil:
		res.Content = string(out)
		if strings.TrimSpace(res.Content) == "" {
			res.Content = emptyResultContent
		}
	case errors.Is(err, context.DeadlineExceeded), hctx.Err() != nil:
		res.Err = fmt.Errorf("tool %q timed out after %s: %w", call.Function, DefaultTimeout, err)
		res.Content = timeoutMessage(call.Function, DefaultTimeout)
	default:
		res.Err = fmt.Errorf("tool %q: %w", call.Function, err)
		res.Content = failureMessage(call.Function, err)
	}
	return res
}

// names returns the registered tool names, sorted — the inventory quoted
// back to a model that called something else.
func (r *Registry) names() []string {
	specs := r.Specs()
	if len(specs) == 0 {
		return nil
	}
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

// The model-facing error shapes. Each states what went wrong AND what to
// do next, because a tool result that only says "error" invites an
// immediate identical retry — which is how a bounded round budget gets
// spent on nothing.

func unknownToolMessage(name string, available []string) string {
	inventory := "no tools are available in this session"
	if len(available) > 0 {
		inventory = "available tools: " + strings.Join(available, ", ")
	}
	return fmt.Sprintf("ERROR: there is no tool named %q (%s). Do not call it again; answer using what you already have.",
		name, inventory)
}

func timeoutMessage(name string, d time.Duration) string {
	return fmt.Sprintf("ERROR: tool %q timed out after %s. Retry once with narrower arguments, or answer without it.", name, d)
}

func failureMessage(name string, err error) string {
	return fmt.Sprintf("ERROR: tool %q failed: %s. Retry with corrected arguments if the error names a fixable problem, otherwise answer without it.",
		name, OneLine(err.Error()))
}

// OneLine collapses whitespace runs so a multi-line string stays a
// single readable line in a tool message or a metadata block. Exported
// because every tool that renders model-facing text needs it and three
// private copies of `strings.Join(strings.Fields(s), " ")` is three
// chances to drift.
func OneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
