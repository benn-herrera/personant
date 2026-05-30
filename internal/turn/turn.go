// Package turn implements the spec §3.0 turn loop and the
// onContextDelta hook chain that every content-emitting code path must
// pass through (§3.0.5). One Run = one user turn end-to-end.
package turn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/recall/measure"
)

// State is the per-session mutable runtime state passed to Run. Most
// fields are stable across a session; the coalesce buffer is reset at
// the start of every Run.
type State struct {
	Ops           memops.MemoryOps
	ActiveProject memops.ProjectMeta
	Provider      memops.Provider
	Client        model.Client

	// Recaller is the §3.4 recall stack (measure.Recaller). NewState
	// installs a default symbolic-only Service; callers that have an
	// embedding provider replace it with an embedding-enabled Service
	// post-construction, then call Recaller.Prepare. The turn loop and
	// the recall UI surface depend only on the interface — recall
	// internals are insulated behind it.
	Recaller measure.Recaller

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

	// History accumulates user/assistant messages across the session so
	// the model sees prior turns. The system prompt is recomputed every
	// turn from working-set state (the spine may have changed); only the
	// user/assistant exchange is replayed from History.
	//
	// Bounded by sessionHistoryCapTurns turn pairs (FIFO): when a turn
	// close appends a pair that pushes the slice past the cap, the oldest
	// turn pair is evicted from the front. Eviction is always pair-
	// aligned (user+assistant together) so History always alternates
	// user/assistant cleanly. Per MAD architecture review burn-down item
	// B1 (= T1-2): before this cap, History grew unbounded → linear input
	// growth per turn → production context-window exhaustion in long
	// sessions. Token-byte budget enforcement is intentionally NOT added
	// here; turn-pair count is the v0.1 cap. A byte/token budget can
	// layer on later if measurement justifies it.
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
	// to memops.DefaultBudget() at NewState; future directive plumbing
	// (Phase 3+) will recompute this per-turn.
	Budget memops.Budget

	// turn-scoped state
	coalesce *coalesceBuffer

	// turnOwner is the in-flight single-owner stamp for this turn (spec
	// §3.2): the one thread that received this turn's excerpt. Empty
	// before ownership is assigned. claimTurnOwner sets it exactly once;
	// a second claim returns ErrTurnAlreadyOwned — a structural guard
	// against a future bug double-writing one turn's content to two
	// threads. Reset at the top of every Run alongside coalesce.
	turnOwner string

	// fileEdits buffers §3.9 file-edit events (fs.read / fs.write /
	// fs.commit) observed across the deltas of one turn. File-edit deltas
	// arrive before the turn's engaged thread is known (engagement is
	// deferred to turn close per §3.0.4), so they are buffered here and
	// applied to the primary engaged thread's tracked-file store at close.
	// Reset at the top of every Run alongside coalesce.
	fileEdits []fileEdit

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

// sessionHistoryCapTurns bounds State.History to N user/assistant turn
// pairs (i.e. up to 2*N messages). Picked to match the v0.1
// working-set discipline of dormantThreadsCap=20: a coarse count cap
// that keeps a long session from growing linearly without committing
// to a token-aware budget. At realistic ~1-3 KB/message this caps
// replayed context at ~40-120 KB, well under common 128k-200k
// context-window limits. See the State.History godoc and MAD
// architecture-review burn-down item B1 for the rationale.
const sessionHistoryCapTurns = 20

// NewState constructs a State for a chat session. The coalesce buffer
// is initialized empty; Client must be non-nil (the chat REPL passes
// either an HTTPClient or a MockClient, never nil).
func NewState(ops memops.MemoryOps, project memops.ProjectMeta, provider memops.Provider, client model.Client) *State {
	return &State{
		Ops:               ops,
		ActiveProject:     project,
		Provider:          provider,
		Client:            client,
		Budget:            memops.DefaultBudget(),
		coalesce:          newCoalesceBuffer(),
		staging:           newStagingBuffer(),
		closureDeferUntil: make(map[string]int),
		// Default to symbolic-only recall; callers with an embedding
		// provider replace this with an embedding-enabled Service.
		Recaller: measure.NewService(ops, nil),
	}
}

// LoadSession constructs a State for a chat session and reloads the
// persisted Layer B/C working-set membership from the substrate, so a
// clean shutdown→relaunch cycle resumes the working set instead of
// cold-starting it empty.
//
// It is the launch-path counterpart to NewState: NewState is kept a pure
// constructor (no I/O), and LoadSession layers the one launch-time read
// on top. A missing working-set artifact (fresh install, or a home that
// never completed a turn) leaves ActiveThreads/DormantThreads nil — the
// same state NewState produces — and is not an error.
//
// Only the working-set membership is reloaded. TurnNumber, the
// coalesce/staging buffers, and closureDeferUntil are session-volatile
// and correctly start fresh.
func LoadSession(ctx context.Context, ops memops.MemoryOps, project memops.ProjectMeta, provider memops.Provider, client model.Client) (*State, error) {
	state := NewState(ops, project, provider, client)
	active, dormant, err := ops.LoadWorkingSet(ctx)
	if err != nil {
		return nil, fmt.Errorf("turn: load session working set: %w", err)
	}
	state.ActiveThreads = active
	state.DormantThreads = dormant
	return state, nil
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
	// §3.9 file-edit buffer is per-turn — clear it alongside coalesce so a
	// prior turn's buffered edits cannot leak into this turn's close.
	state.fileEdits = state.fileEdits[:0]
	// The single-owner stamp is per-turn — clear it so the prior turn's
	// owner cannot trip this turn's claim guard.
	state.turnOwner = ""
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
		_ = state.Ops.Log(ctx, memops.LogCategoryStaging, "evicted",
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
	if err := onContextDelta(ctx, state, Delta{Source: memops.SourceUserPrompt, Content: userInput}); err != nil {
		return "", err
	}

	// Step 2: compose working-set and assemble the system prompt.
	if state.Budget.Total == 0 {
		state.Budget = memops.DefaultBudget()
	}
	buildSystemPrompt := func() (string, error) {
		ws, err := state.Ops.ComposeWorkingSet(ctx, memops.WorksetInput{
			ActiveProject:  state.ActiveProject,
			ActiveThreads:  state.ActiveThreads,
			DormantThreads: state.DormantThreads,
			Budget:         state.Budget,
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
				_ = state.Ops.Log(ctx, memops.LogCategoryTopic, "re-prompt",
					"fetched="+strconv.Itoa(fetched)+" attempt="+strconv.Itoa(attempt+1))
				systemPrompt, err = buildSystemPrompt()
				if err != nil {
					return "", fmt.Errorf("turn: recompose working set: %w", err)
				}
				continue
			}
		}

		// No re-prompt — drain the stream through the filter, starting
		// with the buffered preamble. The filter independently locates and
		// suppresses the tag line within the same bounded preamble region
		// while forwarding the surrounding text.
		if _, werr := filter.Write(pre.head); werr != nil {
			_ = sr.Close()
			return "", fmt.Errorf("turn: filter write head: %w", werr)
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
			_ = state.Ops.Log(ctx, memops.LogCategoryModel, "stream-close-warn", closeErr.Error())
		}
		full = sr.Final()
		break
	}

	// Step 4: model.response delta with the full accumulated body —
	// fired once, not per chunk (spec §3.0.5).
	if err := onContextDelta(ctx, state, Delta{Source: memops.SourceModelResponse, Content: full.Content}); err != nil {
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
		_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "error", memops.SanitizeDetail(err.Error()))
	}

	// Step 5c: §3.8 cardinality-pressure archival scan. Like closure, it
	// runs on EVERY turn (a spine grows past the watermark regardless of
	// this turn's activity) and is opportunistic — a failure is logged and
	// swallowed, never aborting the turn.
	if err := surfaceArchivalCandidates(ctx, state); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryArchive, "error", memops.SanitizeDetail(err.Error()))
	}

	// Step 5d: persist the updated Layer B/C working-set membership so a
	// clean shutdown→relaunch resumes the working set instead of
	// cold-starting it empty. This MUST run after steps 5b/5c: closure
	// retirement and archival both evict threads from ActiveThreads /
	// DormantThreads, so saving any earlier would persist a stale set
	// (a thread the same turn went on to evict). It also runs on
	// no-engagement turns — closeTurnAndUpdateEngagement returns early
	// then, but closure/archival can still have evicted something. A save
	// failure is non-fatal: log and continue, consistent with the other
	// close-time substrate calls (AgeFileChains, recall).
	if err := state.Ops.SaveWorkingSet(ctx, state.ActiveThreads, state.DormantThreads); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategorySession, "working-set-save-error", memops.SanitizeDetail(err.Error()))
	}

	// Step 6: derive the topic-tag-stripped body for the return value.
	// (out has already received the same content in chunks.)
	body := full.Content
	if pr, perr := prompt.Parse(full.Content); perr == nil {
		body = pr.Body
	}
	body = strings.TrimRight(body, "\n")

	// Append to History so the next turn sees the exchange, then enforce
	// the sessionHistoryCapTurns FIFO bound (MAD B1). History is always
	// appended as a user+assistant pair, so post-append the slice has
	// even length; eviction is pair-aligned (2 messages at a time from
	// the front) and the LLM-protocol invariant of alternating
	// user/assistant roles is preserved.
	state.History = append(state.History,
		model.Message{Role: "user", Content: userInput},
		model.Message{Role: "assistant", Content: full.Content},
	)
	if cap := 2 * sessionHistoryCapTurns; len(state.History) > cap {
		drop := len(state.History) - cap
		// Drop is always even because both the append above and any
		// prior trim leave History even-length, and cap is even.
		state.History = append(state.History[:0:0], state.History[drop:]...)
	}

	return body, nil
}
