package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"personant/internal/clock"
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

// ToolReceipt is the record of ONE executed tool call, handed to the
// optional State.OnToolReceipt presentation hook the moment the call
// returns (user ruling 2026-08-05). Sibling of RecallNotice and
// ClosureNotice: the runtime supplies the facts, the front end owns the
// wording.
//
// WHY it exists. Tool activity had no durable trace on screen. The §4.3.2
// phase label (`running <tool>`) lives in the ephemeral slot and is erased
// by the next thing that writes, and it is suppressed outright for a call
// that finishes inside the indicator's reveal window — so a sub-second
// call left NOTHING behind. That made "did you actually search?" a
// question only the model could answer, and the model's post-hoc account
// of its own tool use is exactly the thing that must never be the
// evidence. One committed line per call makes it a matter of eyes.
//
// A REFUSAL is the load-bearing case. `web.wikipedia` without a configured
// contact refuses at invocation; that is an ordinary model-recoverable
// result, invisible everywhere except the event log. Err + Outcome put it
// on screen.
type ToolReceipt struct {
	// Name is the tool as called ("?" when the model named none).
	Name string

	// ArgsGist is a compact rendering of the DECODED arguments —
	// `q="general relativity"`, `url="https://…"` — bounded to
	// argsGistMaxRunes. Empty for a zero-argument call, or when the
	// arguments did not decode (a malformed blob is the handler's error to
	// report, not the receipt's).
	ArgsGist string

	// Outcome is the short result. On success: `ok, 4.1kB` — the size of
	// what the handler produced, measured BEFORE the §6.5 delta cap, so it
	// reports the tool's output rather than the budget's slice of it. On
	// failure (Err): the error's first line, bounded to
	// receiptErrorMaxRunes, with no "error:" prefix — labelling the
	// failure is the front end's job, which is what Err is for.
	Outcome string

	// Err reports that the call did not succeed. A DISPLAY discriminator,
	// not a control signal: nothing in the pipeline observes it, and the
	// model has already been handed a result it can recover from either
	// way.
	Err bool

	// Elapsed is the measured wall-clock cost of the call (clock.Profiling
	// — real time, never the simulated Timeline).
	Elapsed time.Duration
}

// emitToolReceipt reports one executed call to the optional
// State.OnToolReceipt hook. Same discipline as emitPhase and
// emitReasoning: the nil check lives here and nowhere else.
func emitToolReceipt(state *State, r ToolReceipt) {
	if state.OnToolReceipt != nil {
		state.OnToolReceipt(r)
	}
}

// Receipt bounds. The gist has to fit beside the tool name, the outcome
// and the elapsed time on one terminal line, so it is the tight one; an
// error gets more room because a refusal the user cannot read is the
// failure this whole mechanism exists to fix.
const (
	argsGistMaxRunes     = 60
	receiptErrorMaxRunes = 80

	// minGistPairRunes is the smallest budget worth starting another
	// `k="v"` pair on. Below it the remainder is elided rather than
	// rendered as a key with nothing legible after it.
	minGistPairRunes = 6
)

// argsGist renders a call's decoded arguments for the receipt.
//
// It decodes into a plain map rather than any tool's argument struct:
// internal/turn knows no tool's schema and must not learn one, and the
// wire blob is DOUBLE-ENCODED, which tools.DecodeArgs already unwraps for
// every handler. A blob that does not decode yields the empty gist — the
// receipt reports what the call was, and a malformed one is a failure the
// Outcome already carries.
func argsGist(raw json.RawMessage) string {
	var args map[string]any
	if err := tools.DecodeArgs(raw, &args); err != nil || len(args) == 0 {
		return ""
	}
	var b strings.Builder
	budget := argsGistMaxRunes
	for _, k := range gistKeys(args) {
		if budget < minGistPairRunes {
			b.WriteString(" …")
			break
		}
		if b.Len() > 0 {
			b.WriteString(" ")
			budget--
		}
		pair := k + "=" + gistValue(args[k], budget-utf8.RuneCountInString(k)-1)
		b.WriteString(pair)
		budget -= utf8.RuneCountInString(pair)
	}
	return b.String()
}

// gistKeys orders a call's arguments: TEXTUAL arguments first, then the
// rest, each group alphabetical.
//
// The primary argument of a read tool is its text — the query, the URL —
// and the scalars beside it (`limit`) are options. Alphabetical order
// alone would put `limit=5` in front of the query it bounds and then spend
// the budget truncating the part the user actually wants to see. Map
// iteration order is not an option at all: a receipt that reshuffles
// between two identical calls is not a record of anything.
func gistKeys(args map[string]any) []string {
	text := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))
	for k, v := range args {
		if _, isText := v.(string); isText {
			text = append(text, k)
		} else {
			rest = append(rest, k)
		}
	}
	slices.Sort(text)
	slices.Sort(rest)
	return append(text, rest...)
}

// gistValue renders one argument value within max runes. Strings are
// quoted (a query with spaces has to read as one value); everything else
// — numbers, bools, a nested object — is rendered bare.
func gistValue(v any, max int) string {
	s, isText := v.(string)
	if !isText {
		return clipRunes(fmt.Sprint(v), max)
	}
	return `"` + clipRunes(s, max-2) + `"`
}

// receiptOutcome summarizes a dispatched call for its receipt.
//
// Success reports SIZE rather than a per-tool item count: the count is
// available only by parsing the rendered shortlist, and a receipt that
// re-reads another package's output format is a checksum of that format.
// Size is exact, uniform across every tool, and answers the question the
// receipt is for — the tool ran and produced this much.
func receiptOutcome(res tools.Result) string {
	if res.Err == nil {
		return "ok, " + formatBytes(len(res.Content))
	}
	first, _, _ := strings.Cut(res.Err.Error(), "\n")
	// Dispatch prefixes a handler error with the tool name, which the
	// receipt has already said. Dropping it buys back the budget for the
	// part that is news. A wording change there costs the receipt the
	// tidiness and nothing else.
	first = strings.TrimPrefix(strings.TrimSpace(first), fmt.Sprintf("tool %q: ", res.Name))
	return clipRunes(first, receiptErrorMaxRunes)
}

// formatBytes renders a byte count for a human reading one line.
func formatBytes(n int) string {
	switch {
	case n < 1<<10:
		return strconv.Itoa(n) + "B"
	case n < 1<<20:
		return fmt.Sprintf("%.1fkB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	}
}

// clipRunes bounds s to max RUNES, marking a cut with an ellipsis.
//
// Deliberately not truncateRunes (livebudget.go), which bounds by BYTES
// for a byte budget and marks nothing: this one is display width for a
// single line, where a count of characters is the unit and a silent cut
// would read as the real argument.
func clipRunes(s string, max int) string {
	if max <= 1 {
		return "…"
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

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

	// Turn identity on the tool context. A handler that budgets itself
	// per turn (the §6.1.1 web.search local query cap) has no other way
	// to see a turn boundary: the registry is built once at startup and a
	// handler is a plain function. An int on the context keeps
	// internal/tools turn-unaware, and a monotonic counter compared
	// against a stored one cannot be forgotten the way a reset callback
	// can.
	ctx = tools.ContextWithTurn(ctx, state.TurnNumber)

	results := make([]tools.Result, 0, len(calls))
	for _, call := range calls {
		_ = state.Ops.Log(ctx, memops.LogCategoryTool, "call",
			fmt.Sprintf("name=%s id=%s round=%d", call.Function, call.ID, round))

		started := clock.Profiling()
		res := state.Tools.Dispatch(ctx, call)
		elapsed := clock.Since(started)
		if res.Aborted {
			// No receipt: the turn is going away, and a line committed to
			// scrollback about a call the user just abandoned is noise
			// under the retraction notice.
			return toolRoundOutcome{}, fmt.Errorf("turn: tool %q: %w", call.Function, res.Err)
		}
		// The durable trace (user ruling 2026-08-05), fired the moment the
		// result is in hand — before the model's continuation streams, so
		// receipts interleave with the answer in the order things happened.
		emitToolReceipt(state, ToolReceipt{
			Name:     toolCallName(call),
			ArgsGist: argsGist(call.Args),
			Outcome:  receiptOutcome(res),
			Err:      res.Err != nil,
			Elapsed:  elapsed,
		})
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

// toolCallName is the display name of one call — the model's name for it,
// or "?" when it named none (Dispatch turns that into an error result).
// Shared by the phase label and the receipt so the two cannot disagree
// about what an unnamed call is called.
func toolCallName(c model.ToolCall) string {
	if c.Function == "" {
		return "?"
	}
	return c.Function
}

// toolCallNames lists the tool names in a round, for the phase label.
func toolCallNames(calls []model.ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, toolCallName(c))
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
