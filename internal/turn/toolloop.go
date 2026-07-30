package turn

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/tools"
)

// §6.1 tool execution inside the turn.
//
// Shape of a tool round, in the OpenAI-compatible convention the wire
// encoder already speaks (model.encodeRequest): the assistant message that
// carried ToolCalls is echoed back verbatim, then ONE tool message per
// call, each carrying the matching ToolCallID. A provider that saw N tool
// calls requires N tool replies; that is why runToolRound produces a
// result for every call including the ones that failed.
//
// # Crash stability (#94): tool calls and results are NOT journaled
//
// The §4.5.8 turn journal holds the (prompt, response) byte pair — the
// content that must survive a crash with ≤1 turn of loss. Tool calls and
// their results are deliberately kept out of it, for two reasons:
//
//  1. They are DERIVED, not authored. A crash mid-tool-round loses the
//     whole turn, and replay re-issues the prompt, which re-elicits the
//     calls and re-executes the tools. Nothing the user typed is lost, so
//     journaling would buy no content back.
//  2. Adding a third journal record kind would change the record
//     vocabulary (memops.TurnContentPrompt / TurnContentResponse) that the
//     recovery state machine reads, and the journal/marker/commit ordering
//     is load-bearing. Tool rounds run entirely pre-canonical: they add no
//     canonical write, so they need no recovery point of their own.
//
// The consequence is explicit and stated so it can be checked:
// **tool execution is AT-LEAST-ONCE.** A crash between a tool running and
// the turn committing means that tool runs again on replay.
//
// That is harmless for every tool in scope — the §6.1.1 read/think tools
// are read-only, so re-running one costs a repeated fetch and nothing
// else. It would NOT be harmless for a mutating tool, and §6.1.2's
// `propose_*` tools are additionally ack-gated, which is a separate unbuilt
// mechanism.
//
// **This decision must be revisited before any mutating tool lands.**
// The trip-wire is mechanical, not a comment: tools.Registry.Register
// REFUSES a Tool declaring Mutates, citing this block
// (tools.ErrMutatingToolUnsupported). An agent adding a mutating tool
// cannot get it registered without reading this first.

// maxToolRoundsPerTurn bounds how many times one turn will execute tool
// calls and re-issue the request. Round N+1's response is streamed like
// any other; only the (N+1)th request for MORE tools is refused.
//
// It is deliberately a SEPARATE counter from the §5.5 / D6 re-prompt caps
// (≤1 per cause, ≤3 per turn). Those bound recovery from a PROTOCOL
// FAILURE — the model did something wrong and the runtime spent an
// intervention on it, so repeating the intervention is known-useless. A
// tool round is not a failure: it is the model working, and each round
// carries new information (the previous round's results), so the same
// "never repeat a failed intervention" logic does not apply. Folding the
// two would let a single tool-using turn exhaust the budget that exists to
// recover a malformed topic tag.
//
// 4 is a calibration starting point, not a load-bearing constant: enough
// for search → fetch → fetch → answer, small enough that a model looping
// on a broken call costs a bounded number of round-trips. Exhausting it is
// surfaced to BOTH the user (an on-screen notice) and the event log
// (tool.truncated), never silently swallowed.
const maxToolRoundsPerTurn = 4

// toolPhasePrefix opens every §6.1 tool-execution phase label. It is the
// membership test for the phase FAMILY (IsToolPhase), which the front end's
// abort allow-list needs: the label carries the tool name, so it cannot be
// matched as a constant.
const toolPhasePrefix = "running "

// PhaseTooling is the tool-execution phase with no name available. Tool
// execution is its own wait — without a label the user sits on "waiting on
// the model" for a multi-round turn in which the model is not the thing
// being waited on.
const PhaseTooling Phase = toolPhasePrefix + "tools"

// PhaseRunningTool labels tool execution with the tool being run, from the
// identity-only model.Chunk.ToolCalls data (ID + Function, never Args).
// This is exactly what that field exists for.
//
// The label stays SHORT: §4.3.2 requires the indicator line not to wrap,
// because a wrapped line turns the in-place redraw into a scroll. One name
// is rendered; more are summarized.
func PhaseRunningTool(names ...string) Phase {
	switch len(names) {
	case 0:
		return PhaseTooling
	case 1:
		return Phase(toolPhasePrefix + names[0])
	default:
		return Phase(fmt.Sprintf("%s%s +%d more", toolPhasePrefix, names[0], len(names)-1))
	}
}

// IsToolPhase reports whether p is a §6.1 tool-execution label. Tool
// execution is PRE-CANONICAL — it happens between model streams, before
// any canonical write — so the front end's §4.3.3 abort allow-list admits
// the whole family: an Esc during a slow fetch must cancel it.
func IsToolPhase(p Phase) bool { return strings.HasPrefix(string(p), toolPhasePrefix) }

// toolRoundOutcome is what one executed tool round hands back to the turn
// loop: the messages to append to the next request, and the deltas to fire
// through the §3.0 chain. Both are built from the SAME bounded content, so
// what the model reads and what memory records cannot diverge.
type toolRoundOutcome struct {
	messages []model.Message
	deltas   []Delta
}

// runToolRound executes every call in one round, in the order the model
// emitted them, and shapes the results.
//
// Execution is SEQUENTIAL and deterministic. Concurrency would shorten a
// multi-fetch round, but it would also make the result order (and hence
// the symbol-extraction order and the request bytes) depend on scheduling,
// and it would put the registry and the §3.0 chain under concurrent
// access. Neither cost is worth paying before a tool exists whose latency
// is the complaint.
//
// A cancelled turn context stops the round immediately and returns an
// error: the turn is going away, and feeding the model a result it will
// never read is the wrong move. Every other failure — unknown tool,
// handler error, handler timeout — becomes a tool message the model can
// recover from, which is why they are not errors here.
func runToolRound(ctx context.Context, state *State, round int, calls []model.ToolCall) (toolRoundOutcome, error) {
	var out toolRoundOutcome
	// The assistant message carrying the calls comes first: the provider
	// rejects tool messages that answer no visible call.
	out.messages = append(out.messages, model.Message{
		Role:      "assistant",
		Content:   "",
		ToolCalls: calls,
	})

	results := make([]tools.Result, 0, len(calls))
	for _, call := range calls {
		_ = state.Ops.Log(ctx, memops.LogCategoryTool, "call",
			fmt.Sprintf("name=%s id=%s round=%d", call.Function, call.ID, round))

		res := state.Tools.Dispatch(ctx, call)
		if res.Aborted {
			return toolRoundOutcome{}, fmt.Errorf("turn: tool %q: %w", call.Function, res.Err)
		}
		if res.Err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryTool, "error",
				fmt.Sprintf("name=%s id=%s round=%d detail=%s",
					call.Function, call.ID, round, memops.SanitizeDetail(res.Err.Error())))
		} else {
			_ = state.Ops.Log(ctx, memops.LogCategoryTool, "result",
				fmt.Sprintf("name=%s id=%s round=%d bytes=%d", call.Function, call.ID, round, len(res.Content)))
		}
		results = append(results, res)
	}

	// §6.5 byte cap. Applied to the DELTAS, by the same
	// boundTaskResultDeltas that already bounds pre-prompt tool.result and
	// §4.4 shell-capture content — one policy, one implementation. The
	// bounded content is then what builds the tool messages too: bounding
	// only the memory copy would leave the wire copy unbounded, which is
	// the budget the cap exists to protect.
	deltas := make([]Delta, 0, len(results))
	for _, res := range results {
		deltas = append(deltas, Delta{
			Source:  memops.SourceToolResult,
			Content: res.Content,
			Meta:    map[string]string{"tool": res.Name, "call_id": res.CallID},
		})
	}
	deltas = boundTaskResultDeltas(deltas, state.Budget)

	for i, d := range deltas {
		out.messages = append(out.messages, model.Message{
			Role:       "tool",
			Content:    d.Content,
			ToolCallID: results[i].CallID,
		})
	}
	out.deltas = deltas
	return out, nil
}

// joinRoundBodies concatenates the visible text of a turn's rounds,
// dropping the empty segments a tool-call-only round contributes. The
// blank-line separator keeps two assistant segments from running together
// into one paragraph in the thread excerpt and the replayed history.
// A single-round turn — every turn that uses no tools — returns its one
// segment VERBATIM, so the overwhelmingly common path is byte-for-byte
// what it was before tool rounds existed (this string is journaled, fed to
// §3.3 extraction, and replayed in History; a stray trim is not free).
func joinRoundBodies(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		kept = append(kept, strings.TrimRight(p, "\n"))
	}
	return strings.Join(kept, "\n\n")
}

// toolCallNames lists the tool names in a round, for the phase label.
func toolCallNames(calls []model.ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		name := c.Function
		if name == "" {
			name = "?"
		}
		out = append(out, name)
	}
	return out
}

// logToolRoundsExhausted records the §6.4 tool.truncated event for a turn
// that hit maxToolRoundsPerTurn with the model still asking for more.
func logToolRoundsExhausted(ctx context.Context, state *State, pending int) {
	_ = state.Ops.Log(ctx, memops.LogCategoryTool, "truncated",
		"reason=round-cap rounds="+strconv.Itoa(maxToolRoundsPerTurn)+
			" pending="+strconv.Itoa(pending)+
			" turn="+strconv.Itoa(state.TurnNumber))
}
