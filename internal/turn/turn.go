// Package turn implements the spec §3.0 turn loop and the
// onContextDelta hook chain that every content-emitting code path must
// pass through (§3.0.5). One Run = one user turn end-to-end.
package turn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/recall"
	"personant/internal/workset"
)

// State is the per-session mutable runtime state passed to Run. Most
// fields are stable across a session; the coalesce buffer is reset at
// the start of every Run.
type State struct {
	Ops           memops.MemoryOps
	ActiveProject memops.ProjectMeta
	Provider      memops.Provider
	Client        model.Client

	// Recaller is the §3.4 recall stack (recall.Recaller). NewState
	// installs a default symbolic-only Service; callers that have an
	// embedding provider replace it with an embedding-enabled Service
	// post-construction, then call Recaller.Prepare. The turn loop and
	// the recall UI surface depend only on the interface — recall
	// internals are insulated behind it.
	Recaller recall.Recaller

	// RecallResolver resolves the §3.4 recall offer surfaced at turn
	// close into accept/decline decisions. nil → recall stays log-only
	// (no offer surfaced). The chat REPL installs an interactive
	// resolver; the scenario harness installs a scripted one.
	RecallResolver RecallResolver

	// Curator drafts the §3.5 closure summary + anchors for a thread
	// that has decayed into idleness. NewState leaves it nil (closure
	// disabled); the chat REPL installs a model-backed curator and the
	// scenario harness installs a deterministic stub.
	Curator curator.Curator

	// ClosureResolver resolves the §3.5 closure offer surfaced at turn
	// close into a retire / wip / defer outcome. nil → closure stays
	// detection-disabled (no offer surfaced, no decay scan). The chat
	// REPL installs an interactive resolver; the scenario harness
	// installs a scripted one. Both Curator and ClosureResolver must be
	// non-nil for closure detection to run.
	ClosureResolver ClosureResolver

	// Model overrides the provider's DefaultModel when non-empty.
	Model string

	// Temperature / MaxTokens — when 0, the provider's default is used.
	Temperature float64
	MaxTokens   int

	// History accumulates assistant/user messages across the session so the
	// model sees prior turns. The system prompt is recomputed every turn
	// from working-set state (the spine may have changed); only the
	// user/assistant exchange is replayed from History.
	History []model.Message

	// ActiveThreads is Layer B membership: thread IDs the working set
	// renders as full thread bodies (per workset.Compose). Index 0 is
	// the most-recently-engaged thread. The list is bounded by
	// Budget.BTopK; overflow demotes to the head of DormantThreads.
	ActiveThreads []string

	// DormantThreads is Layer C membership: thread IDs the working set
	// renders as spine display lines. Index 0 is the most-recently
	// demoted (or independently engaged) thread. The list is capped by
	// dormantThreadsCap; the on-disk byte budget is honored at render
	// time by workset.Compose.
	DormantThreads []string

	// Budget is the byte budget composed into the working set. Defaults
	// to workset.DefaultBudget() at NewState; future directive plumbing
	// (Phase 3+) will recompute this per-turn.
	Budget workset.Budget

	// turn-scoped state
	coalesce *coalesceBuffer

	// TurnNumber is the monotonically incrementing turn count for this
	// State. Incremented at the top of every Run before any chain steps
	// fire, so a staged delta's StagedAt always matches the turn during
	// which it was emitted. Used by the §3.0 transient-data lifecycle
	// (B.4 window-close GC) to evict staging entries older than the
	// retention window.
	TurnNumber int

	// staging is the cross-turn buffer for task-class symbols awaiting
	// cross-reference promotion (§3.0 transient-data lifecycle). Phase
	// B.1 declares the buffer; B.2 routes task-class symbols into it
	// instead of coalesce; B.3 cross-references and promotes; B.4 GC's
	// at window close.
	staging *stagingBuffer

	// closureDeferUntil maps a thread ID to the turn index until which a
	// §3.5 closure re-prompt is suppressed. Populated when the user
	// defers a closure offer; not persisted (session-scoped).
	closureDeferUntil map[string]int
}

// dormantThreadsCap is the v0.1 maximum count for State.DormantThreads.
// The byte budget on Layer C is enforced at workset.Compose render
// time; this count cap is a coarser upstream bound to keep the slice
// from growing unboundedly across a long session. Once the directive
// layer can plumb actual byte counts to the LRU update path (Phase
// 3+), the count cap goes away.
const dormantThreadsCap = 20

// NewState constructs a State for a chat session. The coalesce buffer
// is initialized empty; Client must be non-nil (the chat REPL passes
// either an HTTPClient or a MockClient, never nil).
func NewState(ops memops.MemoryOps, project memops.ProjectMeta, provider memops.Provider, client model.Client) *State {
	return &State{
		Ops:               ops,
		ActiveProject:     project,
		Provider:          provider,
		Client:            client,
		Budget:            workset.DefaultBudget(),
		coalesce:          newCoalesceBuffer(),
		staging:           newStagingBuffer(),
		closureDeferUntil: make(map[string]int),
		// Default to symbolic-only recall; callers with an embedding
		// provider replace this with an embedding-enabled Service.
		Recaller: recall.NewService(ops, nil),
	}
}

// maxRePromptsPerTurn caps the §5.5 system-injected mid-turn re-prompt at
// 1 per turn. Total LLM stream attempts within a turn ≤ 1 +
// maxRePromptsPerTurn = 2. The constant exists for symmetry with future
// directive plumbing (§2.6.1) that may expose it as a parameter; the
// six-month simulation (§11.1) will measure incidence and steady-state
// latency cost.
const maxRePromptsPerTurn = 1

// Run drives one complete user turn end-to-end (spec §3.0). It is a
// thin wrapper around RunWithDeltas with no pre-prompt deltas; see
// RunWithDeltas for the step-by-step contract.
func Run(ctx context.Context, state *State, userInput string, out io.Writer) (string, error) {
	return RunWithDeltas(ctx, state, nil, userInput, out)
}

// RunWithDeltas drives one complete user turn (spec §3.0) with an
// optional slot for pre-prompt deltas. Step ordering:
//
//  1. Bump TurnNumber + window-close GC on staging (B.4 lifecycle).
//  2. Fire each preEvent through the §3.0 chain. preEvents are emitted
//     AFTER staging GC but BEFORE the user.prompt delta, sharing the
//     same TurnNumber. Use for tool.result, user.shell-capture,
//     thread.fetched, and other non-user.prompt context-modification
//     events that need to stage symbols for the same-turn user.prompt
//     to cite.
//  3. Fire user.prompt context-modify event.
//  4. Compose working-set + build system prompt.
//  5. Stream the model response via state.Client.ConsultStream, writing
//     chunks through a topic-tag stream filter to out as they arrive.
//     If the model's topic tag references a thread not in Layer B, the
//     in-flight stream is aborted, the missing thread is fetched (per
//     §5.5), and the request is re-issued with augmented context. The
//     re-prompt is capped at maxRePromptsPerTurn per turn.
//  6. Fire model.response context-modify event (parses topic tag,
//     accumulates symbols, defers engagement) after the stream completes.
//     Per §3.0.5 the model.response delta fires once with the full body,
//     not per chunk.
//  7. Close-out the turn: fire engagement updates with the coalesced
//     symbol set (§3.0.4).
//  8. Return the response body with the topic tag stripped (matches what
//     the user saw on out).
//
// Errors at any step are wrapped and returned. The coalesce buffer is
// reset at the start of each call so per-turn accumulation is fresh.
//
// out receives the streamed response body with the §5.1 topic tag
// suppressed; pass io.Discard to keep the streaming behavior without
// presenting tokens (e.g. tests that only assert on side-effects).
func RunWithDeltas(ctx context.Context, state *State, preEvents []Delta, userInput string, out io.Writer) (string, error) {
	if state == nil {
		return "", errors.New("turn: nil state")
	}
	if state.Client == nil {
		return "", errors.New("turn: nil model client")
	}
	if out == nil {
		out = io.Discard
	}
	if state.coalesce == nil {
		state.coalesce = newCoalesceBuffer()
	}
	if state.staging == nil {
		state.staging = newStagingBuffer()
	}
	state.coalesce.reset()
	// Bump the turn counter BEFORE any chain step fires so the user.prompt
	// delta and the model.response delta both observe the same
	// TurnNumber. The transient-data lifecycle B.4 window-close GC keys
	// off StagedAt-vs-TurnNumber, so the convention "TurnNumber reflects
	// the in-flight turn" must hold for the whole of Run.
	state.TurnNumber++

	// B.4 window-close GC: evict staging entries whose citation window
	// closed at the start of this turn. Must run BEFORE the user.prompt
	// delta so a same-turn citation cannot accidentally observe (and
	// promote) an entry that was just supposed to expire.
	if n := pruneStaging(state); n > 0 {
		_ = state.Ops.Log(ctx, "staging", "evicted",
			fmt.Sprintf("count=%d turn=%d", n, state.TurnNumber))
	}

	// Pre-prompt deltas (tool.result, user.shell-capture, thread.fetched,
	// etc.). Emitted AFTER staging GC and BEFORE the user.prompt delta so
	// they share the same TurnNumber as the user.prompt that follows and
	// can stage task-class symbols for that prompt to cite.
	for _, pre := range preEvents {
		if err := onContextDelta(ctx, state, pre); err != nil {
			return "", err
		}
	}

	// Step 1: user.prompt delta.
	if err := onContextDelta(ctx, state, Delta{Source: "user.prompt", Content: userInput}); err != nil {
		return "", err
	}

	// Step 2: compose working-set and assemble the system prompt.
	if state.Budget.Total == 0 {
		state.Budget = workset.DefaultBudget()
	}
	buildSystemPrompt := func() (string, error) {
		ws, err := state.Ops.ComposeWorkingSet(ctx, memops.WorksetInput{
			ActiveProject:  state.ActiveProject,
			ActiveThreads:  state.ActiveThreads,
			DormantThreads: state.DormantThreads,
			Budget:         memopsBudgetFromWorkset(state.Budget),
		})
		if err != nil {
			return "", err
		}
		return prompt.BuildSystemPrompt(prompt.SystemPromptElements{
			LayerE:  ws.LayerE,
			LayerA1: ws.LayerA1,
			LayerA2: ws.LayerA2,
			LayerB:  ws.LayerB,
			LayerC:  ws.LayerC,
		}), nil
	}

	systemPrompt, err := buildSystemPrompt()
	if err != nil {
		return "", fmt.Errorf("turn: compose working set: %w", err)
	}

	// Step 3: LLM round-trip (streaming) with §5.5 mid-turn re-prompt.
	chosenModel := state.Model
	if chosenModel == "" {
		chosenModel = state.Provider.DefaultModel
	}

	// The stream filter strips the §5.1 topic tag from the user-visible
	// body. It is created once and only receives writes on the final
	// (non-aborted) attempt; aborted attempts never touch out.
	filter := prompt.NewStreamFilter(out)

	var full model.Response
	for attempt := 0; ; attempt++ {
		messages := make([]model.Message, 0, 2+len(state.History))
		messages = append(messages, model.Message{Role: "system", Content: systemPrompt})
		messages = append(messages, state.History...)
		messages = append(messages, model.Message{Role: "user", Content: userInput})

		req := model.DefaultRequest(chosenModel, messages)
		// State-level overrides — when set, they win over the defaults.
		if state.Temperature != 0 {
			req.Temperature = state.Temperature
		}
		if state.MaxTokens != 0 {
			req.MaxTokens = state.MaxTokens
		}

		sr, err := state.Client.ConsultStream(ctx, req)
		if err != nil {
			return "", fmt.Errorf("turn: model consult: %w", err)
		}

		pre, err := readPreamble(sr)
		if err != nil {
			_ = sr.Close()
			return "", fmt.Errorf("turn: read preamble: %w", err)
		}

		// §5.5 mid-turn fetch: if the preamble is a topic tag referencing
		// a thr_<n> not in Layer B, abort the stream, fetch the thread,
		// and re-prompt. Capped at maxRePromptsPerTurn per turn.
		if attempt < maxRePromptsPerTurn && pre.tag != nil {
			missing := missingFromActiveB(pre.tag.Threads, state.ActiveThreads)
			fetched := 0
			for _, thrID := range missing {
				if fetchThreadForReprompt(ctx, state, thrID) {
					fetched++
				}
			}
			if fetched > 0 {
				_ = sr.Close()
				_ = state.Ops.Log(ctx, "topic", "re-prompt",
					"fetched="+itoa(fetched)+" attempt="+itoa(attempt+1))
				systemPrompt, err = buildSystemPrompt()
				if err != nil {
					return "", fmt.Errorf("turn: recompose working set: %w", err)
				}
				continue
			}
		}

		// No re-prompt — drain the stream through the filter, starting
		// with the buffered preamble.
		if _, werr := filter.Write(pre.head); werr != nil {
			_ = sr.Close()
			return "", fmt.Errorf("turn: filter write head: %w", werr)
		}
		if len(pre.tail) > 0 {
			if _, werr := filter.Write(pre.tail); werr != nil {
				_ = sr.Close()
				return "", fmt.Errorf("turn: filter write tail: %w", werr)
			}
		}
		var streamErr error
		if !pre.ended {
			streamErr = streamThroughFilter(sr, filter)
		}
		// Always flush the filter (even on error) so any buffered non-tag
		// content reaches the user before we surface the failure.
		flushErr := filter.Close()
		closeErr := sr.Close()

		if streamErr != nil {
			return "", fmt.Errorf("turn: stream: %w", streamErr)
		}
		if flushErr != nil {
			return "", fmt.Errorf("turn: flush stream filter: %w", flushErr)
		}
		if closeErr != nil {
			// Close-after-EOF errors are usually benign (e.g. the body was
			// already drained); surface them but don't lose the response.
			_ = state.Ops.Log(ctx, "model", "stream-close-warn", closeErr.Error())
		}
		full = sr.Final()
		break
	}

	// Step 4: model.response delta with the full accumulated body —
	// fired once, not per chunk (spec §3.0.5).
	if err := onContextDelta(ctx, state, Delta{Source: "model.response", Content: full.Content}); err != nil {
		return "", err
	}

	// Step 5: turn close — fire deferred engagement updates with the
	// coalesced symbol set.
	if err := closeTurnAndUpdateEngagement(ctx, state, userInput, full.Content); err != nil {
		return "", fmt.Errorf("turn: close: %w", err)
	}

	// Step 5b: §3.5 decay-triggered closure scan. Runs on EVERY turn —
	// including turns that engaged no thread — because a thread decays
	// regardless of this turn's activity, so the scan must NOT sit
	// behind closeTurnAndUpdateEngagement's no-engagement early return.
	// Opportunistic, like recall: a failure is logged and swallowed.
	if err := surfaceClosureCandidates(ctx, state); err != nil {
		_ = state.Ops.Log(ctx, "retire", "error", sanitizeDetail(err.Error()))
	}

	// Step 6: derive the topic-tag-stripped body for the return value.
	// (out has already received the same content in chunks.)
	body := full.Content
	if pr, perr := prompt.Parse(full.Content); perr == nil {
		body = pr.Body
	}
	body = strings.TrimRight(body, "\n")

	// Append to History so the next turn sees the exchange.
	state.History = append(state.History,
		model.Message{Role: "user", Content: userInput},
		model.Message{Role: "assistant", Content: full.Content},
	)

	return body, nil
}

// streamThroughFilter pumps every chunk's Content through filter until
// the StreamReader signals EOF. Errors from filter.Write are surfaced
// immediately — a sink that fails to accept bytes is not something the
// runtime can recover from per-chunk.
func streamThroughFilter(sr model.StreamReader, filter io.Writer) error {
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if chunk.Content == "" {
			continue
		}
		if _, werr := io.WriteString(filter, chunk.Content); werr != nil {
			return werr
		}
	}
}

// preambleResult bundles the bytes accumulated from a streaming response
// up through the first newline (head), bytes that arrived in the same
// chunk after that newline (tail), and a parsed topic tag if head was
// recognized as one (per §5.1.2). ended==true signals the stream EOF'd
// before any newline was seen — head holds whatever bytes did arrive,
// tail is empty.
//
// readPreamble + classifyPreamble are split so the unit tests can
// exercise the classification in isolation from the StreamReader pump.
type preambleResult struct {
	head  []byte           // first line up to and including its trailing \n
	tail  []byte           // bytes after that \n in the chunk that contained it
	tag   *prompt.TopicTag // non-nil iff head parsed as a §5.1.2 topic tag
	ended bool             // true when EOF arrived before any \n
}

// readPreamble loops over chunks from sr until either the first newline
// arrives or the stream ends. Returned head/tail point into freshly
// allocated buffers — the caller owns them across subsequent sr.Next()
// calls. Errors other than io.EOF are surfaced verbatim.
func readPreamble(sr model.StreamReader) (preambleResult, error) {
	var buf []byte
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			return classifyPreamble(buf, nil, true), nil
		}
		if err != nil {
			return preambleResult{}, err
		}
		if chunk.Content == "" {
			continue
		}
		buf = append(buf, chunk.Content...)
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			head := append([]byte(nil), buf[:i+1]...)
			tail := append([]byte(nil), buf[i+1:]...)
			return classifyPreamble(head, tail, false), nil
		}
	}
}

// classifyPreamble inspects head and reports a parsed topic tag if it
// matches §5.1.2 with a non-empty thread list. tail and ended are
// passed through unchanged. head may or may not include a trailing
// newline (it does when readPreamble found one; it doesn't when the
// stream ended early); prompt.Parse is multi-line anchored so we feed
// it head as-is plus a synthetic newline only if absent.
func classifyPreamble(head, tail []byte, ended bool) preambleResult {
	res := preambleResult{head: head, tail: tail, ended: ended}
	if len(head) == 0 {
		return res
	}
	candidate := head
	if candidate[len(candidate)-1] != '\n' {
		c := make([]byte, len(candidate)+1)
		copy(c, candidate)
		c[len(c)-1] = '\n'
		candidate = c
	}
	pr, err := prompt.Parse(string(candidate))
	if err != nil {
		return res
	}
	if len(pr.Tag.Threads) == 0 {
		return res
	}
	tag := pr.Tag
	res.tag = &tag
	return res
}

// missingFromActiveB returns thread ids from threads that are not
// present in active. The literal "*new-topic*" sentinel never needs
// fetching and is filtered out unconditionally.
func missingFromActiveB(threads, active []string) []string {
	if len(threads) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(active))
	for _, id := range active {
		have[id] = struct{}{}
	}
	var out []string
	for _, t := range threads {
		if t == "*new-topic*" {
			continue
		}
		if _, ok := have[t]; ok {
			continue
		}
		out = append(out, t)
	}
	return out
}

// fetchThreadForReprompt loads thread thrID from disk, fires a
// thread.fetched context delta, and promotes the thread into
// state.ActiveThreads (de-duped, capped by Budget.BTopK; overflow
// demotes the tail to the head of state.DormantThreads).
//
// On a load error (missing file or otherwise unloadable), logs
// thread.fetch-miss and returns false — the caller skips that thread
// and proceeds with whichever others succeeded.
//
// fetchThreadForReprompt deliberately does not add thrID to
// coalesce.threads: engagement is owed by the second response's tag
// (which the model emits against the augmented context), not by the
// fetch action itself.
func fetchThreadForReprompt(ctx context.Context, state *State, thrID string) bool {
	thr, err := state.Ops.LoadThread(ctx, thrID)
	if err != nil {
		_ = state.Ops.Log(ctx, "thread", "fetch-miss",
			"thr="+thrID+" err="+sanitizeDetail(err.Error()))
		return false
	}
	if err := onContextDelta(ctx, state, Delta{
		Source:  "thread.fetched",
		Content: thr.Body,
		Meta:    map[string]string{"thr": thrID},
	}); err != nil {
		_ = state.Ops.Log(ctx, "thread", "fetch-miss",
			"thr="+thrID+" err="+sanitizeDetail(err.Error()))
		return false
	}

	promoteToLayerB(state, thrID)
	return true
}

// promoteToLayerB promotes thrID into state.ActiveThreads at the front
// (de-duped, capped by Budget.BTopK), demoting the displaced tail to the
// head of state.DormantThreads and capping that slice at
// dormantThreadsCap. Used by the §5.5 mid-turn fetch and by the §3.4
// recall-accept path (Part B).
func promoteToLayerB(state *State, thrID string) {
	bTopK := state.Budget.BTopK
	if bTopK <= 0 {
		bTopK = workset.DefaultBTopK
	}
	state.ActiveThreads = removeString(state.ActiveThreads, thrID)
	state.DormantThreads = removeString(state.DormantThreads, thrID)
	state.ActiveThreads = append([]string{thrID}, state.ActiveThreads...)
	for len(state.ActiveThreads) > bTopK {
		demoted := state.ActiveThreads[len(state.ActiveThreads)-1]
		state.ActiveThreads = state.ActiveThreads[:len(state.ActiveThreads)-1]
		state.DormantThreads = append([]string{demoted}, state.DormantThreads...)
	}
	if len(state.DormantThreads) > dormantThreadsCap {
		state.DormantThreads = state.DormantThreads[:dormantThreadsCap]
	}
}

// historyCapPerThread is the v0.1 default for `history.cap-per-thread`
// (spec §2.6.1). Directive-file lookup arrives in Phase 3+; until then
// the cap is a constant.
const historyCapPerThread = 40

// closeTurnAndUpdateEngagement fires after the model.response delta.
// Per §3.0.4: engagement updates fire once per affected thread with
// the turn's coalesced symbol set as input.
//
// For each thr_<n> in state.coalesce.threads:
//   - if it exists in spine: load thread file, append turn excerpt,
//     merge history_symbols, save thread file, update spine record.
//   - if it is the literal "*new-topic*": create a new thread file
//     and append a new spine record.
//
// File order is: thread file save first, spine update second. A failure
// after the thread file write but before the spine write would leave
// the on-disk file ahead of the spine; the next engagement reload would
// reconcile (it reads the existing file). The opposite order would
// strand a spine entry pointing at a non-existent file. v0.1 accepts
// the former trade-off and logs both errors with context.
//
// New-record creation requires the topic tag's anchors. v0.1 takes
// them from state.coalesce.symbols (the union accumulated across the
// turn). Anchor count must be 4–8 (§2.2 hard range); when out of
// range, a warning is logged and the first 4 (padded with "anchor-N"
// placeholders if there are fewer) are used. v0.1 deliberately does
// not fail the turn over malformed tag output — that judgment is
// recorded in the spec changelog.
//
// §5.5 mid-turn fetch (Phase 2.e-B) handles the common case where the
// model's topic tag references a thr_<n> not in Layer B at
// prompt-construction time: Run aborts the in-flight stream, loads the
// thread, and re-issues the request with augmented context. The
// close-time LRU update here still picks up any threads that the
// mid-turn fetch couldn't service (e.g. a missing thread file →
// thread.fetch-miss) or didn't trigger (re-prompt cap reached, or the
// model's second-stream tag introduces a new thr_<n> that we no longer
// re-prompt for). Those threads enter ActiveThreads at turn close so
// the next turn's prompt includes them.
func closeTurnAndUpdateEngagement(ctx context.Context, state *State, userInput, responseBody string) error {
	if len(state.coalesce.threads) == 0 {
		return nil
	}

	now := clock.Timeline().Format(time.RFC3339)
	turnSymbols := state.coalesce.coalescedList()
	turnAnchors := turnAnchorList(responseBody, state.coalesce.symbolList())

	// Engaged thread IDs in this turn — includes resolved IDs for the
	// *new-topic* sentinel. Used to update the Layer B/C LRU below.
	engaged := make([]string, 0, len(state.coalesce.threads))

	for _, threadID := range state.coalesce.threadList() {
		if threadID == "*new-topic*" {
			newID, err := createNewThread(ctx, state, userInput, responseBody, now, turnSymbols, turnAnchors)
			if err != nil {
				return err
			}
			engaged = append(engaged, newID)
			continue
		}
		if err := updateExistingThread(ctx, state, threadID, userInput, responseBody, now, turnSymbols, turnAnchors); err != nil {
			return err
		}
		engaged = append(engaged, threadID)
	}

	updateLayerLRU(state, engaged)

	engagedSet := make(map[string]struct{}, len(engaged))
	for _, id := range engaged {
		engagedSet[id] = struct{}{}
	}
	if err := surfaceRecallCandidates(ctx, state, userInput, engagedSet); err != nil {
		_ = state.Ops.Log(ctx, "recall", "error", sanitizeDetail(err.Error()))
		// Non-fatal: opportunistic recall failure does not abort the turn.
	}
	return nil
}

// updateLayerLRU applies the §3.1 Layer B/C eviction policy after a
// turn's engagements are committed.
//
// For each engaged thread (in coalesce-order — map iteration is
// non-deterministic, but every thread in the slice was definitely
// touched this turn so any order is correct):
//
//   - If the thread is already in ActiveThreads, move it to the
//     front (most-recent).
//   - Otherwise, prepend it. If ActiveThreads then exceeds Budget.BTopK,
//     pop the tail and prepend it to DormantThreads.
//   - If a thread newly entering ActiveThreads is currently in
//     DormantThreads, remove it from there before inserting at front.
//
// Threads not engaged this turn are left in place; decay is by
// overflow at the next engagement (per §3.1's "no per-thread decay
// counter; eviction is bounded by budget pressure"). DormantThreads
// is capped by count to keep the slice bounded across a long session.
func updateLayerLRU(state *State, engaged []string) {
	bTopK := state.Budget.BTopK
	if bTopK <= 0 {
		bTopK = workset.DefaultBTopK
	}
	for _, id := range engaged {
		// An engaged thread has started a fresh idle clock; any
		// §3.5 defer-suppression grace from a prior idle episode is
		// now meaningless, so prune it to avoid wrongly suppressing a
		// future legitimate re-prompt.
		delete(state.closureDeferUntil, id)
		// Drop from current positions in either layer.
		state.ActiveThreads = removeString(state.ActiveThreads, id)
		state.DormantThreads = removeString(state.DormantThreads, id)
		// Insert at the front of ActiveThreads.
		state.ActiveThreads = append([]string{id}, state.ActiveThreads...)
		// Overflow: tail of ActiveThreads demotes to head of
		// DormantThreads.
		for len(state.ActiveThreads) > bTopK {
			demoted := state.ActiveThreads[len(state.ActiveThreads)-1]
			state.ActiveThreads = state.ActiveThreads[:len(state.ActiveThreads)-1]
			state.DormantThreads = append([]string{demoted}, state.DormantThreads...)
		}
	}
	// Cap DormantThreads by count.
	if len(state.DormantThreads) > dormantThreadsCap {
		state.DormantThreads = state.DormantThreads[:dormantThreadsCap]
	}
}

// removeString returns ss with the first occurrence of v removed. ss
// is unmodified; the result aliases its tail when v is found at the
// head, otherwise allocates a fresh slice. Order-preserving.
func removeString(ss []string, v string) []string {
	for i, s := range ss {
		if s == v {
			out := make([]string, 0, len(ss)-1)
			out = append(out, ss[:i]...)
			out = append(out, ss[i+1:]...)
			return out
		}
	}
	return ss
}

// turnAnchorList returns the anchor list to print in the per-turn
// excerpt header. Prefers the model's own topic-tag anchors so the
// excerpt header matches the wire format exactly; falls back to the
// per-turn coalesced set when the model omitted a tag.
func turnAnchorList(responseBody string, fallback []string) []string {
	if pr, err := prompt.Parse(responseBody); err == nil {
		return append([]string(nil), pr.Tag.Anchors...)
	}
	return append([]string(nil), fallback...)
}

func updateExistingThread(ctx context.Context, state *State, threadID, userInput, responseBody, now string, turnSymbols []coalescedSymbol, turnAnchors []string) error {
	rec, found, err := state.Ops.FindThread(ctx, threadID)
	if err != nil {
		return err
	}
	if !found {
		// The model named a thread that does not exist. v0.1 logs and
		// continues; future phases may surface this as a recall miss or a
		// hallucination signal.
		return state.Ops.Log(ctx, "thread", "engaged-miss",
			"thr="+threadID+" reason=not-in-spine")
	}
	if rec.Project != "" && rec.Project != state.ActiveProject.ID {
		// Cross-project engagement is reserved for Phase 5; v0.1 warns and
		// declines to mutate a record that belongs to another project.
		return state.Ops.Log(ctx, "thread", "engaged-cross-project",
			"thr="+threadID+" project="+rec.Project+" active="+state.ActiveProject.ID)
	}

	// Load the thread file. The adapter owns the missing-file fallback:
	// EngageThread materializes a fresh file when the spine record exists
	// but the on-disk file is absent, so we don't special-case
	// ErrThreadFileNotFound here.
	thr, err := state.Ops.LoadThread(ctx, threadID)
	if err != nil && !errors.Is(err, memops.ErrThreadFileNotFound) {
		return fmt.Errorf("load thread %s: %w", threadID, err)
	}
	if errors.Is(err, memops.ErrThreadFileNotFound) {
		thr = memops.Thread{Frontmatter: frontmatterFromSpine(rec)}
	}

	// Bookkeeping update on the in-memory record. The new turn_count is
	// the value used for the per-turn excerpt header and as
	// first_seen_turn for any newly introduced history symbols.
	newTurnCount := rec.TurnCount + 1
	thr.Frontmatter.LastEngaged = now
	thr.Frontmatter.LastEngagedTurn = state.TurnNumber
	thr.Frontmatter.TurnCount = newTurnCount
	// Mirror the canonical spine fields so the frontmatter stays in
	// sync. The body of work reads the frontmatter; the spine is the
	// outer index.
	thr.Frontmatter.ID = rec.ID
	thr.Frontmatter.Project = rec.Project
	thr.Frontmatter.Anchors = append([]string(nil), rec.Anchors...)
	thr.Frontmatter.Summary = rec.Summary
	thr.Frontmatter.State = rec.State
	if thr.Frontmatter.Created == "" {
		thr.Frontmatter.Created = rec.Created
	}
	if thr.Frontmatter.StateChanged == "" {
		thr.Frontmatter.StateChanged = rec.StateChanged
	}
	thr.Frontmatter.RecallFires = rec.RecallFires

	thr.Frontmatter.HistorySymbols = mergeHistorySymbols(thr.Frontmatter.HistorySymbols, turnSymbols, newTurnCount)
	thr.Body = appendTurnExcerpt(thr.Body, newTurnCount, now, turnAnchors, userInput, responseBody)

	rec.LastEngaged = now
	rec.LastEngagedTurn = state.TurnNumber
	rec.TurnCount = newTurnCount
	if err := state.Ops.EngageThread(ctx, memops.ThreadWrite{
		Spine:       rec,
		Frontmatter: thr.Frontmatter,
		Body:        thr.Body,
	}); err != nil {
		return fmt.Errorf("engage thread %s: %w", threadID, err)
	}
	return state.Ops.Log(ctx, "thread", "engaged",
		threadID+" turn_count="+itoa(rec.TurnCount))
}

func createNewThread(ctx context.Context, state *State, userInput, responseBody, now string, turnSymbols []coalescedSymbol, turnAnchors []string) (string, error) {
	// NextThreadID returns the next available thr_<n> id, scanning the
	// full spine so a new id never collides with a thread in a sibling
	// project.
	newID, err := state.Ops.NextThreadID(ctx)
	if err != nil {
		return "", err
	}

	anchors := state.coalesce.symbolList()
	if len(anchors) < 4 || len(anchors) > 8 {
		_ = state.Ops.Log(ctx, "thread", "anchor-cardinality",
			"new-thread anchors="+itoa(len(anchors))+" using-first-4-with-padding")
		anchors = padOrTruncateAnchors(anchors, 4)
	}

	summary := summarizeForNewThread(responseBody)

	rec := memops.SpineRecord{
		ID:              newID,
		Project:         state.ActiveProject.ID,
		Anchors:         anchors,
		Summary:         summary,
		State:           memops.ThreadWIP,
		Created:         now,
		LastEngaged:     now,
		StateChanged:    now,
		TurnCount:       1,
		LastEngagedTurn: state.TurnNumber,
	}

	frontmatter := ThreadFrontmatter{
		ID:              newID,
		Project:         rec.Project,
		Anchors:         append([]string(nil), anchors...),
		Summary:         summary,
		State:           memops.ThreadWIP,
		Created:         now,
		LastEngaged:     now,
		StateChanged:    now,
		TurnCount:       1,
		RecallFires:     0,
		LastEngagedTurn: state.TurnNumber,
		HistorySymbols:  mergeHistorySymbols(nil, turnSymbols, 1),
	}
	body := newThreadBody(anchors, 1, now, turnAnchors, userInput, responseBody)

	if err := state.Ops.CreateThread(ctx, memops.ThreadWrite{
		Spine:       rec,
		Frontmatter: frontmatter,
		Body:        body,
	}); err != nil {
		return "", fmt.Errorf("create thread %s: %w", newID, err)
	}
	if err := state.Ops.Log(ctx, "thread", "created",
		newID+" anchors="+itoa(len(anchors))+" project="+state.ActiveProject.ID); err != nil {
		return "", err
	}
	return newID, nil
}

// ThreadFrontmatter is a local alias to avoid a long-form type literal in
// the createNewThread frontmatter construction. Kept at package scope so
// the literal in the function body reads naturally.
type ThreadFrontmatter = memops.ThreadFrontmatter

// memopsBudgetFromWorkset projects the local workset.Budget value onto the
// memops.Budget shape carried across the port. Field-for-field identical;
// the conversion is a no-op except for the named type.
func memopsBudgetFromWorkset(b workset.Budget) memops.Budget {
	return memops.Budget{
		Total:                 b.Total,
		LayerE:                b.LayerE,
		LayerA1:               b.LayerA1,
		LayerA2:               b.LayerA2,
		LayerB:                b.LayerB,
		LayerC:                b.LayerC,
		CurrentTurn:           b.CurrentTurn,
		BTopK:                 b.BTopK,
		PerProjectDigestBytes: b.PerProjectDigestBytes,
	}
}

// frontmatterFromSpine builds a minimal-but-valid ThreadFrontmatter from
// a SpineRecord. Used when a thread's on-disk file is missing while its
// spine entry persists — the engagement update synthesizes a fresh file
// rather than failing the turn.
func frontmatterFromSpine(rec memops.SpineRecord) ThreadFrontmatter {
	return ThreadFrontmatter{
		ID:              rec.ID,
		Project:         rec.Project,
		Anchors:         append([]string(nil), rec.Anchors...),
		Summary:         rec.Summary,
		State:           rec.State,
		Created:         rec.Created,
		LastEngaged:     rec.LastEngaged,
		StateChanged:    rec.StateChanged,
		TurnCount:       rec.TurnCount,
		RecallFires:     rec.RecallFires,
		LastEngagedTurn: rec.LastEngagedTurn,
	}
}

// mergeHistorySymbols folds the per-turn coalesced symbol set into an
// existing history_symbols list and enforces the cap (spec §2.3 +
// §2.6.1, default 40).
//
// For each per-turn symbol:
//   - if its normalized form already appears: increment count, upgrade
//     source per §2.7.3 dominance.
//   - else: append a new entry with first_seen_turn = currentTurn.
//
// Eviction policy when the merged list exceeds the cap: lowest count
// first; ties broken by lowest first_seen_turn (oldest among lowest).
// Spec §2.3 OPEN flags the precise weight formula as deferred to v0.1.1;
// v0.1 uses count alone. Order of return is stable: existing entries
// retain insertion order, new entries append in the input order.
func mergeHistorySymbols(existing []memops.HistorySymbol, turnSymbols []coalescedSymbol, currentTurn int) []memops.HistorySymbol {
	// Index existing by normalized for O(1) lookup.
	idx := make(map[string]int, len(existing))
	out := make([]memops.HistorySymbol, len(existing))
	copy(out, existing)
	for i, h := range out {
		idx[h.Normalized] = i
	}

	// Sort turnSymbols by normalized for deterministic append order.
	turnSorted := make([]coalescedSymbol, len(turnSymbols))
	copy(turnSorted, turnSymbols)
	sort.Slice(turnSorted, func(i, j int) bool {
		return turnSorted[i].Normalized < turnSorted[j].Normalized
	})

	for _, sym := range turnSorted {
		if i, ok := idx[sym.Normalized]; ok {
			out[i].Count++
			out[i].Source = upgradeSource(out[i].Source, sym.Source)
			continue
		}
		raw := sym.Raw
		if raw == "" {
			raw = sym.Normalized
		}
		out = append(out, memops.HistorySymbol{
			Raw:           raw,
			Normalized:    sym.Normalized,
			FirstSeenTurn: currentTurn,
			Count:         1,
			Source:        sym.Source,
		})
		idx[sym.Normalized] = len(out) - 1
	}

	if len(out) <= historyCapPerThread {
		return out
	}
	return evictLowestWeight(out, historyCapPerThread)
}

// upgradeSource is the persistent-history analogue of the per-turn
// coalesce path — same §2.7.3 precedence rule
// (curator > user > model > deterministic), applied on cumulative
// history when merging an incoming observation into an existing
// HistorySymbol entry. Delegates to memops.DominantSource so the rule
// has exactly one definition site.
func upgradeSource(existing, incoming memops.SymbolSource) memops.SymbolSource {
	return memops.DominantSource(existing, incoming)
}

// evictLowestWeight returns out with the lowest-cumulative-weight entries
// removed until len == cap. Weight = count alone in v0.1; ties broken by
// lowest first_seen_turn (evict oldest among lowest-count). Stable
// ordering of the survivors is preserved.
func evictLowestWeight(out []memops.HistorySymbol, cap int) []memops.HistorySymbol {
	if len(out) <= cap {
		return out
	}
	type indexed struct {
		idx int
		ref *memops.HistorySymbol
	}
	scored := make([]indexed, len(out))
	for i := range out {
		scored[i] = indexed{idx: i, ref: &out[i]}
	}
	// Sort: highest count first; ties broken by highest first_seen_turn
	// (newest survives when counts tie).
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i].ref, scored[j].ref
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.FirstSeenTurn > b.FirstSeenTurn
	})
	keepIdx := make(map[int]struct{}, cap)
	for i := range cap {
		keepIdx[scored[i].idx] = struct{}{}
	}
	survivors := make([]memops.HistorySymbol, 0, cap)
	for i := range out {
		if _, ok := keepIdx[i]; ok {
			survivors = append(survivors, out[i])
		}
	}
	return survivors
}

// appendTurnExcerpt appends a single per-turn excerpt block to body and
// returns the new body. The block format (spec §2.3 body content
// guidelines):
//
//	## Turn <N> · <RFC3339> · [a, b, c]
//
//	**user:** <userInput>
//
//	**agent:** <responseBody with topic tag stripped>
//
// Exactly one trailing newline is preserved on the returned body so
// repeated saves produce byte-identical files when nothing has changed.
func appendTurnExcerpt(body string, turnN int, when string, anchors []string, userInput, responseBody string) string {
	excerpt := renderTurnExcerpt(turnN, when, anchors, userInput, responseBody)
	if body == "" {
		return excerpt
	}
	body = strings.TrimRight(body, "\n") + "\n"
	return body + "\n" + excerpt
}

// newThreadBody renders the initial body for a freshly-created thread:
// a top-level title taken from the first anchor (or fallback) followed
// by the first turn excerpt.
func newThreadBody(anchors []string, turnN int, when string, headerAnchors []string, userInput, responseBody string) string {
	title := "new thread"
	if len(anchors) > 0 && anchors[0] != "" {
		title = anchors[0]
	}
	excerpt := renderTurnExcerpt(turnN, when, headerAnchors, userInput, responseBody)
	return "# " + title + "\n\n" + excerpt
}

func renderTurnExcerpt(turnN int, when string, anchors []string, userInput, responseBody string) string {
	stripped := responseBody
	if pr, err := prompt.Parse(responseBody); err == nil {
		stripped = pr.Body
	}
	stripped = strings.TrimRight(stripped, "\n")
	user := strings.TrimRight(userInput, "\n")

	var b strings.Builder
	b.Grow(len(userInput) + len(responseBody) + 64)
	b.WriteString("## Turn ")
	b.WriteString(itoa(turnN))
	b.WriteString(" · ")
	b.WriteString(when)
	b.WriteString(" · [")
	b.WriteString(strings.Join(anchors, ", "))
	b.WriteString("]\n\n")
	b.WriteString("**user:** ")
	b.WriteString(user)
	b.WriteString("\n\n")
	b.WriteString("**agent:** ")
	b.WriteString(stripped)
	b.WriteString("\n")
	return b.String()
}

// padOrTruncateAnchors enforces the §2.2 hard range [4, 8]. If the
// input has fewer than min entries, append "anchor-<n>" placeholders.
// If it has more than 8, take the first 8.
func padOrTruncateAnchors(in []string, min int) []string {
	out := append([]string(nil), in...)
	if len(out) > 8 {
		out = out[:8]
	}
	for i := len(out); i < min; i++ {
		out = append(out, fmt.Sprintf("anchor-%d", i+1))
	}
	return out
}

// summarizeForNewThread takes the response body's stripped form (tag
// already removed by the caller-provided string, or the raw body) and
// returns a short summary. v0.1 keeps it crude: first 120 chars,
// collapsed whitespace, ellipsized if truncated. Phase 4 retirement
// will replace this with a curator-drafted summary.
func summarizeForNewThread(body string) string {
	const maxLen = 120
	stripped := body
	if pr, err := prompt.Parse(body); err == nil {
		stripped = pr.Body
	}
	stripped = strings.TrimSpace(stripped)
	stripped = strings.Join(strings.Fields(stripped), " ")
	if stripped == "" {
		return "(new topic)"
	}
	if len(stripped) <= maxLen {
		return stripped
	}
	return stripped[:maxLen-3] + "..."
}
