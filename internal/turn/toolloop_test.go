package turn

// §6.1 tool-round tests (wave 2). No real tools exist yet, so every tool
// here is a fake registered into a test registry — the harness aimed at
// the unit rather than at a live provider. Mock client throughout: wave 3
// owns live validation.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
	"personant/internal/tools"
)

// fakeTool registers name into r, returning body and recording every call
// into the returned slice pointer.
func fakeTool(t *testing.T, r *tools.Registry, name, body string) *[]string {
	t.Helper()
	var mu sync.Mutex
	calls := &[]string{}
	err := r.Register(tools.Tool{
		Spec: model.ToolSpec{Name: name, Description: "fake " + name},
		Handler: func(_ context.Context, args json.RawMessage) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			*calls = append(*calls, string(args))
			return []byte(body), nil
		},
	})
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return calls
}

// toolCallResponse builds a tool-call-only assistant response — zero
// visible content, N calls. This is the exact live shape that rendered
// nothing before wave 2.
func toolCallResponse(calls ...model.ToolCall) model.Response {
	return model.Response{FinishReason: "tool_calls", ToolCalls: calls}
}

func call(id, name, args string) model.ToolCall {
	return model.ToolCall{ID: id, Function: name, Args: json.RawMessage(args)}
}

// toolStateWith builds a turn State wired to a per-consult mock and a
// registry, on a fresh test home.
func toolStateWith(t *testing.T, reg *tools.Registry, responses ...model.Response) (*State, *model.MockClient, store.PersonantPaths) {
	t.Helper()
	paths, meta := newTestHome(t)
	mock := model.NewScriptedMockPerConsult(responses)
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	state.Tools = reg
	pinClock(t, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	return state, mock, paths
}

// TestToolRoundExecutesAndFeedsResultBack — the base case. Round 1 is
// tool-calls-only, the tool runs, its result comes back as a tool message
// keyed to the call, and round 2 produces the answer the user sees.
func TestToolRoundExecutesAndFeedsResultBack(t *testing.T) {
	reg := tools.NewRegistry()
	calls := fakeTool(t, reg, "web.search", "RESULT: three hits for topology")

	state, mock, paths := toolStateWith(t, reg,
		toolCallResponse(call("c1", "web.search", `"{\"q\":\"topology\"}"`)),
		model.Response{Content: "*topic: *new-topic* [topology, knots]*\nHere is what I found."},
	)

	var out bytes.Buffer
	body, err := Run(context.Background(), state, "search for topology", &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("handler call count = %d; want 1", len(*calls))
	}
	if got := (*calls)[0]; got != `"{\"q\":\"topology\"}"` {
		t.Errorf("handler saw args %q; want them passed through verbatim", got)
	}
	if !strings.Contains(body, "Here is what I found") {
		t.Errorf("body = %q; want round 2's answer", body)
	}
	if !strings.Contains(out.String(), "Here is what I found") {
		t.Errorf("out = %q; the user must see round 2's answer", out.String())
	}

	// Second request carries the tool conversation in protocol order.
	reqs := mock.Calls()
	if len(reqs) != 2 {
		t.Fatalf("model round-trips = %d; want 2", len(reqs))
	}
	msgs := reqs[1].Request.Messages
	assistant, toolMsg := msgs[len(msgs)-2], msgs[len(msgs)-1]
	if assistant.Role != "assistant" || len(assistant.ToolCalls) != 1 {
		t.Fatalf("penultimate message = %+v; want the assistant message carrying ToolCalls", assistant)
	}
	if assistant.ToolCalls[0].ID != "c1" {
		t.Errorf("echoed call id = %q; want c1", assistant.ToolCalls[0].ID)
	}
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "c1" {
		t.Fatalf("last message = %+v; want role=tool tool_call_id=c1", toolMsg)
	}
	if !strings.Contains(toolMsg.Content, "three hits for topology") {
		t.Errorf("tool message content = %q; want the handler's output", toolMsg.Content)
	}

	// §6.4: the result reached memory through the §3.0 chain, so it is a
	// tool.result delta and the tool events are logged.
	logBody := readDayLog(t, paths)
	for _, want := range []string{
		"tool.call name=web.search",
		"tool.result name=web.search",
		"context.modified source=tool.result",
	} {
		if !strings.Contains(logBody, want) {
			t.Errorf("log missing %q:\n%s", want, logBody)
		}
	}
}

// TestToolRoundParallelCalls — several calls in ONE round each get their
// own tool message, keyed to their own id, in emission order.
func TestToolRoundParallelCalls(t *testing.T) {
	reg := tools.NewRegistry()
	fetch := fakeTool(t, reg, "web.fetch", "FETCHED")
	search := fakeTool(t, reg, "web.search", "SEARCHED")

	state, mock, _ := toolStateWith(t, reg,
		toolCallResponse(
			call("c1", "web.search", `"{}"`),
			call("c2", "web.fetch", `"{}"`),
			call("c3", "web.search", `"{}"`),
		),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nDone."},
	)

	if _, err := Run(context.Background(), state, "go", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*fetch) != 1 || len(*search) != 2 {
		t.Fatalf("handler calls: fetch=%d search=%d; want 1 and 2", len(*fetch), len(*search))
	}

	msgs := mock.Calls()[1].Request.Messages
	tail := msgs[len(msgs)-4:]
	if tail[0].Role != "assistant" || len(tail[0].ToolCalls) != 3 {
		t.Fatalf("want one assistant message carrying all 3 calls, got %+v", tail[0])
	}
	wantIDs := []string{"c1", "c2", "c3"}
	wantBody := []string{"SEARCHED", "FETCHED", "SEARCHED"}
	for i, m := range tail[1:] {
		if m.Role != "tool" {
			t.Fatalf("message %d role = %q; want tool", i, m.Role)
		}
		if m.ToolCallID != wantIDs[i] {
			t.Errorf("tool message %d id = %q; want %q", i, m.ToolCallID, wantIDs[i])
		}
		if m.Content != wantBody[i] {
			t.Errorf("tool message %d content = %q; want %q", i, m.Content, wantBody[i])
		}
	}
}

// TestToolRoundCapExhaustedSurfacesHonestly — a model that asks for tools
// forever gets cut off after maxToolRoundsPerTurn, and BOTH the user and
// the event log are told. Silence here would look like a hung or broken
// system.
func TestToolRoundCapExhaustedSurfacesHonestly(t *testing.T) {
	reg := tools.NewRegistry()
	calls := fakeTool(t, reg, "web.search", "more")

	// One more tool-call response than the cap allows.
	responses := make([]model.Response, 0, maxToolRoundsPerTurn+1)
	for i := 0; i <= maxToolRoundsPerTurn; i++ {
		responses = append(responses, toolCallResponse(call(fmt.Sprintf("c%d", i), "web.search", `"{}"`)))
	}
	state, mock, paths := toolStateWith(t, reg, responses...)

	var out bytes.Buffer
	if _, err := Run(context.Background(), state, "loop forever", &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(*calls) != maxToolRoundsPerTurn {
		t.Errorf("handler ran %d times; want the cap %d", len(*calls), maxToolRoundsPerTurn)
	}
	if got := len(mock.Calls()); got != maxToolRoundsPerTurn+1 {
		t.Errorf("model round-trips = %d; want %d", got, maxToolRoundsPerTurn+1)
	}
	if !strings.Contains(out.String(), "tool-round limit") {
		t.Errorf("user saw %q; want an honest cap notice", out.String())
	}
	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "tool.truncated reason=round-cap") {
		t.Errorf("log missing tool.truncated:\n%s", logBody)
	}
}

// TestToolRoundUnknownToolRecovers — the provider emits calls for tools we
// do not have (observed live, unprompted). The model gets an error result
// it can act on, the failure is logged, and the turn completes.
func TestToolRoundUnknownToolRecovers(t *testing.T) {
	reg := tools.NewRegistry()
	fakeTool(t, reg, "web.fetch", "FETCHED")

	state, mock, paths := toolStateWith(t, reg,
		toolCallResponse(call("c1", "bash.run", `"{\"cmd\":\"rm -rf /\"}"`)),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nI cannot run shell commands."},
	)

	var out bytes.Buffer
	body, err := Run(context.Background(), state, "delete everything", &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(body, "I cannot run shell commands") {
		t.Errorf("body = %q; the turn must survive an unknown tool", body)
	}

	msgs := mock.Calls()[1].Request.Messages
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "c1" {
		t.Fatalf("unknown tool produced no answering tool message: %+v", toolMsg)
	}
	if !strings.Contains(toolMsg.Content, "bash.run") || !strings.Contains(toolMsg.Content, "web.fetch") {
		t.Errorf("tool message = %q; want the bad name and the real inventory", toolMsg.Content)
	}
	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "tool.error name=bash.run") {
		t.Errorf("log missing tool.error for the unknown tool:\n%s", logBody)
	}
}

// TestToolRoundHandlerErrorAndTimeoutRecover — both failure shapes reach
// the model as results, never as a dropped turn.
func TestToolRoundHandlerErrorAndTimeoutRecover(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"handler error", errors.New("connection refused"), "connection refused"},
		{"handler timeout", context.DeadlineExceeded, "timed out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := tools.NewRegistry()
			if err := reg.Register(tools.Tool{
				Spec:    model.ToolSpec{Name: "web.fetch"},
				Handler: func(context.Context, json.RawMessage) ([]byte, error) { return nil, c.err },
			}); err != nil {
				t.Fatalf("register: %v", err)
			}
			state, mock, paths := toolStateWith(t, reg,
				toolCallResponse(call("c1", "web.fetch", `"{}"`)),
				model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nThe fetch failed; here is what I know."},
			)

			body, err := Run(context.Background(), state, "fetch it", io.Discard)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(body, "here is what I know") {
				t.Errorf("body = %q; the turn must survive a failing tool", body)
			}
			msgs := mock.Calls()[1].Request.Messages
			toolMsg := msgs[len(msgs)-1]
			if toolMsg.Role != "tool" || !strings.Contains(toolMsg.Content, c.wantMsg) {
				t.Errorf("tool message = %+v; want content containing %q", toolMsg, c.wantMsg)
			}
			if !strings.Contains(readDayLog(t, paths), "tool.error name=web.fetch") {
				t.Error("log missing tool.error")
			}
		})
	}
}

// TestToolRoundCancellationLeavesNoResidue — Esc mid-tool cancels the
// running tool AND leaves the session exactly as it found it. This is the
// §4.3.3 retraction invariant (TestPreCanonicalAbort_NoSessionResidue)
// applied to the tool path: tool rounds are pre-canonical, so an abort in
// one must roll back and release the recovery scope like any other.
func TestToolRoundCancellationLeavesNoResidue(t *testing.T) {
	reg := tools.NewRegistry()
	entered := make(chan struct{})
	if err := reg.Register(tools.Tool{
		Spec: model.ToolSpec{Name: "web.fetch"},
		Handler: func(ctx context.Context, _ json.RawMessage) ([]byte, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	state, _, paths := toolStateWith(t, reg,
		toolCallResponse(call("c1", "web.fetch", `"{}"`)),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nnever reached"},
	)
	// Give the session state something an escaped turn could corrupt.
	state.ActiveThreads = []string{"thr_1"}
	beforeTurn := state.TurnNumber
	beforeActive := append([]string(nil), state.ActiveThreads...)
	beforeHistory := len(state.History)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-entered
		cancel()
	}()
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, state, "fetch it", io.Discard)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil after the turn was cancelled mid-tool")
		}
		if errors.Is(err, ErrMarkerRetained) {
			t.Fatalf("cancellation must abort PRE-canonically, not retain the marker: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation — the tool was not cancelled")
	}

	// Session rollback (§4.3.3): nothing from the retracted turn survives.
	if state.TurnNumber != beforeTurn {
		t.Errorf("TurnNumber = %d; want the pre-turn %d", state.TurnNumber, beforeTurn)
	}
	if len(state.History) != beforeHistory {
		t.Errorf("History grew to %d; want %d", len(state.History), beforeHistory)
	}
	if strings.Join(state.ActiveThreads, ",") != strings.Join(beforeActive, ",") {
		t.Errorf("ActiveThreads = %v; want %v", state.ActiveThreads, beforeActive)
	}
	// The #94 recovery scope was released — the journal is empty, so the
	// next launch opens quiet.
	if data, err := os.ReadFile(paths.TurnJournal); err == nil && len(bytes.TrimSpace(data)) != 0 {
		t.Errorf("turn journal not truncated after a pre-canonical abort:\n%s", data)
	}
}

// TestToolResultRespectsSixFiveCap — a verbose tool result is bounded by
// the §6.5 task-class byte cap on BOTH paths: what memory records and what
// goes back on the wire. Bounding only one would leave the other unbounded.
func TestToolResultRespectsSixFiveCap(t *testing.T) {
	reg := tools.NewRegistry()
	huge := strings.Repeat("x", 4<<20)
	if err := reg.Register(tools.Tool{
		Spec:    model.ToolSpec{Name: "web.fetch"},
		Handler: func(context.Context, json.RawMessage) ([]byte, error) { return []byte(huge), nil },
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	state, mock, _ := toolStateWith(t, reg,
		toolCallResponse(call("c1", "web.fetch", `"{}"`)),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nSummarized."},
	)

	if _, err := Run(context.Background(), state, "fetch the big page", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	_, _, share := liveTurnShares(state.Budget)
	if share <= 0 {
		t.Fatal("task-result share is zero — the cap under test is not configured")
	}
	msgs := mock.Calls()[1].Request.Messages
	toolMsg := msgs[len(msgs)-1]
	if len(toolMsg.Content) > share {
		t.Errorf("tool message is %d bytes; the §6.5 cap is %d", len(toolMsg.Content), share)
	}
	if len(toolMsg.Content) >= len(huge) {
		t.Fatal("tool message was not truncated at all")
	}
	if !strings.Contains(toolMsg.Content, "output truncated") {
		t.Errorf("truncation is silent; want the honest marker: %q", toolMsg.Content[max(0, len(toolMsg.Content)-120):])
	}
}

// TestEmptyRegistrySendsNoToolsField — the default state offers nothing,
// so no `tools` field reaches the provider and the model is never invited
// to call what cannot be serviced.
func TestEmptyRegistrySendsNoToolsField(t *testing.T) {
	for _, tc := range []struct {
		name string
		reg  *tools.Registry
	}{
		{"nil registry", nil},
		{"empty registry", tools.NewRegistry()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, mock, _ := toolStateWith(t, tc.reg,
				model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nHi."})
			if _, err := Run(context.Background(), state, "hello", io.Discard); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := mock.Calls()[0].Request.Tools; len(got) != 0 {
				t.Errorf("Request.Tools = %v; want none", got)
			}
		})
	}
}

// TestRegisteredToolsReachTheRequestInOrder — a populated registry puts
// every spec on the request, name-sorted, on every round of the turn.
func TestRegisteredToolsReachTheRequestInOrder(t *testing.T) {
	reg := tools.NewRegistry()
	fakeTool(t, reg, "web.search", "s")
	fakeTool(t, reg, "fs.read", "r")
	fakeTool(t, reg, "web.fetch", "f")

	state, mock, _ := toolStateWith(t, reg,
		toolCallResponse(call("c1", "fs.read", `"{}"`)),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nDone."},
	)
	if _, err := Run(context.Background(), state, "read it", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"fs.read", "web.fetch", "web.search"}
	for i, c := range mock.Calls() {
		if len(c.Request.Tools) != len(want) {
			t.Fatalf("round %d: %d tools; want %d", i, len(c.Request.Tools), len(want))
		}
		for j, spec := range c.Request.Tools {
			if spec.Name != want[j] {
				t.Errorf("round %d tool %d = %q; want %q", i, j, spec.Name, want[j])
			}
		}
	}
}

// TestToolOnlyTurnIsNeverSilent — the wave-2 regression guard. A turn
// whose model output is nothing but tool calls used to render zero bytes:
// the user pressed enter and got their prompt back. Every failure mode
// must put SOMETHING on screen.
func TestToolOnlyTurnIsNeverSilent(t *testing.T) {
	cases := []struct {
		name      string
		responses []model.Response
		want      string
	}{
		{
			name: "tool call then a still-empty answer",
			responses: []model.Response{
				toolCallResponse(call("c1", "web.fetch", `"{}"`)),
				{Content: "", FinishReason: "stop"},
				{Content: "", FinishReason: "stop"},
			},
			want: "no reply text",
		},
		{
			name: "unknown tool then an empty answer",
			responses: []model.Response{
				toolCallResponse(call("c1", "bash.run", `"{}"`)),
				{Content: "", FinishReason: "stop"},
				{Content: "", FinishReason: "stop"},
			},
			want: "no reply text",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := tools.NewRegistry()
			fakeTool(t, reg, "web.fetch", "FETCHED")
			state, _, _ := toolStateWith(t, reg, c.responses...)

			var out bytes.Buffer
			if _, err := Run(context.Background(), state, "go", &out); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(out.String(), c.want) {
				t.Errorf("user saw %q; want a notice containing %q", out.String(), c.want)
			}
		})
	}
}

// TestEmptyResponseIsNotConfusedWithAToolRound — the identity-only chunk
// view is what separates the two byte-identical shapes. A tool-call-only
// response must NOT spend the D6 empty-response re-prompt.
func TestEmptyResponseIsNotConfusedWithAToolRound(t *testing.T) {
	reg := tools.NewRegistry()
	fakeTool(t, reg, "web.fetch", "FETCHED")
	state, mock, paths := toolStateWith(t, reg,
		toolCallResponse(call("c1", "web.fetch", `"{}"`)),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nAnswered."},
	)
	if _, err := Run(context.Background(), state, "go", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Calls()); got != 2 {
		t.Fatalf("model round-trips = %d; want 2 (tool round + answer, no re-prompt)", got)
	}
	logBody := readDayLog(t, paths)
	if strings.Contains(logBody, "cause=empty-response") {
		t.Errorf("a tool round burned the empty-response re-prompt:\n%s", logBody)
	}
}

// TestToolRoundDoesNotBurnTheMissingTagReprompt — a binding turn (§3.9
// edits buffered) whose first round is tool-calls-only is tag-less because
// the model has not started replying, not because it forgot the protocol.
// Firing the D6 tag reminder there would spend the per-cause budget AND
// throw the calls away.
func TestToolRoundDoesNotBurnTheMissingTagReprompt(t *testing.T) {
	reg := tools.NewRegistry()
	calls := fakeTool(t, reg, "web.fetch", "FETCHED")
	state, mock, paths := toolStateWith(t, reg,
		toolCallResponse(call("c1", "web.fetch", `"{}"`)),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nEdited."},
	)

	if _, _, err := RunWithInfo(context.Background(), state,
		[]Delta{fsWriteDelta("src/a.go")}, "edit it", io.Discard); err != nil {
		t.Fatalf("RunWithInfo: %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("handler ran %d times; want 1", len(*calls))
	}
	if got := len(mock.Calls()); got != 2 {
		t.Errorf("model round-trips = %d; want 2 (tool round + answer)", got)
	}
	if logBody := readDayLog(t, paths); strings.Contains(logBody, "cause=missing-tag") {
		t.Errorf("a tool round burned the missing-tag re-prompt:\n%s", logBody)
	}
}

// TestToolPhaseNamesTheTool — the progress indicator must say what is
// running rather than leaving "waiting on the model" up for a multi-round
// turn, and the label must stay in the pre-canonical abort family so Esc
// still cancels a slow tool.
func TestToolPhaseNamesTheTool(t *testing.T) {
	reg := tools.NewRegistry()
	fakeTool(t, reg, "web.search", "hits")
	state, _, _ := toolStateWith(t, reg,
		toolCallResponse(call("c1", "web.search", `"{}"`)),
		model.Response{Content: "*topic: *new-topic* [alpha, beta]*\nDone."},
	)
	var phases []Phase
	state.OnPhase = func(p Phase) { phases = append(phases, p) }

	if _, err := Run(context.Background(), state, "go", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, p := range phases {
		if p == PhaseRunningTool("web.search") {
			found = true
		}
	}
	if !found {
		t.Fatalf("phases = %v; want one naming web.search", phases)
	}
	if !IsToolPhase(PhaseRunningTool("web.search")) || !IsToolPhase(PhaseTooling) {
		t.Error("tool phases must be recognized by IsToolPhase (the front end's abort allow-list)")
	}
	if IsToolPhase(PhaseWaiting) || IsToolPhase(PhaseClosing) {
		t.Error("IsToolPhase must not claim the non-tool phases")
	}
	// Multi-tool rounds summarize instead of wrapping the indicator line.
	if got := string(PhaseRunningTool("web.search", "web.fetch", "fs.read")); got != "running web.search +2 more" {
		t.Errorf("multi-tool label = %q", got)
	}
}
