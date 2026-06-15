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

	// recallSurfaced marks thread IDs that arrived in context via recall
	// (§2.7.3 origin-provenance attribution source) — as opposed to direct
	// engagement or switch. Both recall paths mark here through their shared
	// chokepoint promoteToLayerB: the §3.4 recall-accept (applyRecallResolution,
	// turn-close) and the §5.5 mid-turn fetch (fetchThreadForReprompt). It is
	// the "got here via recall" marker the
	// turn-close derived_from population scores newly-emitted symbols against:
	// a symbol coinciding with a recall-surfaced thread's symbol set acquires
	// that thread's id as an origin. Eligibility is NOT membership alone — it
	// is recallSurfaced ∩ Layer-B residency (state.ActiveThreads); see
	// surfacedSymbolSets / residentRecallOrigins. A recall-promoted thread that
	// has since been evicted from Layer B (displaced past BTopK) is no longer
	// in context and is no longer an eligible origin — provenance stays honest,
	// not "ever-recalled". residentRecallOrigins opportunistically prunes ids
	// that have left ActiveThreads, bounding the map over a long session.
	//
	// Why a State field rather than threaded call/return (#2 option B):
	// the §3.4 recall-accept stack runs at turn close (surfaceRecallCandidates
	// → applyRecallResolution → promoteToLayerB), which is structurally AFTER
	// the merge sites (engageOwner update + new-thread creation) within the
	// same Run. The accepted-recall set therefore cannot be threaded forward
	// to those callers in the same turn — it is produced too late. It is the
	// recall accepted in turn N (promoting parents into Layer B, which the
	// model then sees in turn N+1) that the turn N+1 merge attributes
	// against. (The §5.5 mid-turn fetch promotes BEFORE the merge, so it
	// attributes same-turn — but it too marks through promoteToLayerB into
	// this same field, so the field serves both paths uniformly.) So the set
	// must survive the turn boundary; a per-Run return value cannot. It is
	// deliberately NOT cleared at turn start: residency, not turn boundary,
	// ends eligibility. Per-symbol union remains monotonic
	// (an attributed DerivedFrom origin is never removed, §2.7.3); only
	// eligibility — which origins can be NEWLY added — is residency-gated.
	// nil is the well-formed empty case (no recall ever accepted).
	recallSurfaced map[string]struct{}

	// embeddingDebt accrues per thread the count of turn-excerpts that have
	// scrolled out of the *assembly* window (ThreadTurnWindow) but are not
	// yet in the §3.4 fine-tier embedding index. It is the trigger for the
	// debt-cap flush (§6.2): when a thread's debt reaches embeddingDebtCap,
	// recordExcerptScrollOut enqueues a flush of the engaged-thread index
	// and resets the counter to 0. Session-scoped, like the other LRU /
	// coalesce runtime state — a fresh session starts at zero debt and the
	// next dormancy flush (or the cache reconcile on Prepare) covers any
	// gap. nil is the well-formed empty case (no excerpt has scrolled out,
	// or no indexer-capable recaller is installed so debt is never tracked).
	embeddingDebt map[string]int

	// flushCalls / flushChunks are the OBSERVED §6.5 fine-tier flush cost of
	// THIS session: the number of times the debt-cap or dormancy trigger fired
	// a flush, and the total turn-excerpt count those flushes carried into the
	// fine tier. They count the flush DECISION (the §6.2 policy), not the embed
	// dispatch — so they accrue even on the symbolic-only path where
	// EnqueueFlush no-ops (no embedder), which is exactly the cost an
	// embedding-enabled run WOULD pay for this workload. The harness reads them
	// via the FlushCalls/FlushChunks accessors and folds each session's count
	// into a run-total before the session is discarded (a restart rebuilds a
	// fresh State, so a single field cannot hold the run total — see the §6.5
	// flush-cost gauge in the sim harness). Session-scoped, reset per LoadSession
	// like embeddingDebt.
	flushCalls  int
	flushChunks int

	// structuralCreates / structuralCloses count the §3.11 structural
	// changes that occurred during the in-flight turn: thread creations
	// (createNewThread) and §3.5 closure/retire writes (applyClosureResolution).
	// They are the turn-close commit-cadence trigger — a turn with
	// (creates+closes) > 0 yields EXACTLY ONE Checkpoint at Run close, never
	// one per mutation, so a close+create switch or a vacation closure-storm
	// (#82, many closes in one turn) coalesces to a single commit (§3.11).
	// Both are reset to 0 at the top of every Run; the counts also build the
	// commit reason string (e.g. "+1 thread, -2 retired"). Archival commits
	// via its own §3.8 batch and is deliberately NOT counted here.
	structuralCreates int
	structuralCloses  int
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

// commitOnStructuralChange gates the §3.11 turn-close commit cadence: when
// true (the default), a turn that produced ≥1 structural change (thread
// create / §3.5 close-retire) takes exactly one substrate Checkpoint at turn
// close. Per-turn content writes never commit on their own — the
// session-close commit (chat.go) is the safety net that flushes them.
// Default-on; a §9-calibration toggle (not a config.toml setting — that file
// is model choices only), flipped only to A/B the cadence in the sim.
const commitOnStructuralChange = true

// TurnInfo carries cheaply-available per-turn measurements a caller may
// observe without re-deriving them from the substrate or the event log.
// It is populated by RunWithInfo and intentionally minimal: only fields
// available for free from the in-flight turn belong here.
//
// PromptTokens is the provider's reported prompt-token count for the
// fully-assembled request (system prompt + history + userInput + any
// pre-prompt/tool-result deltas) — model.Response.Usage.PromptTokens from
// the turn's chat round-trip. It is the only honest measurement of the
// assembled-request size; the byte budget and composed-memory-only counts
// are true-by-construction and cannot stand in for it. On the mock path it
// is whatever Usage the mock client reports (often a canned value or 0):
// no special-casing — the number flows through honestly.
type TurnInfo struct {
	PromptTokens int
}

// Run drives one complete user turn end-to-end (spec §3.0). It is a thin
// wrapper around RunWithInfo with no pre-prompt deltas, dropping the
// TurnInfo; see RunWithDeltas for the step-by-step contract.
func Run(ctx context.Context, state *State, userInput string, out io.Writer) (string, error) {
	body, _, err := RunWithInfo(ctx, state, nil, userInput, out)
	return body, err
}

// RunWithDeltas drives one complete user turn (spec §3.0) with an
// optional slot for pre-prompt deltas. It is a thin wrapper around
// RunWithInfo that drops the TurnInfo, preserving the existing
// (body, err) signature for callers that do not need the per-turn
// measurements. See RunWithInfo for the step-by-step contract.
func RunWithDeltas(ctx context.Context, state *State, preEvents []Delta, userInput string, out io.Writer) (string, error) {
	body, _, err := RunWithInfo(ctx, state, preEvents, userInput, out)
	return body, err
}

// RunWithInfo drives one complete user turn (spec §3.0) with an
// optional slot for pre-prompt deltas, returning the response body, a
// TurnInfo with cheaply-available per-turn measurements (notably
// usage.prompt_tokens for the fully-assembled request), and any error.
// Step ordering:
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
func RunWithInfo(ctx context.Context, state *State, preEvents []Delta, userInput string, out io.Writer) (string, TurnInfo, error) {
	if state == nil {
		return "", TurnInfo{}, errors.New("turn: nil state")
	}
	if state.Client == nil {
		return "", TurnInfo{}, errors.New("turn: nil model client")
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
	// §3.11 structural-change counters are per-turn — clear them so the
	// turn-close cadence check sees only this turn's creates/closes.
	state.structuralCreates = 0
	state.structuralCloses = 0
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
			return "", TurnInfo{}, err
		}
	}

	// Step 1: user.prompt delta.
	if err := onContextDelta(ctx, state, Delta{Source: memops.SourceUserPrompt, Content: userInput}); err != nil {
		return "", TurnInfo{}, err
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
		return "", TurnInfo{}, fmt.Errorf("turn: compose working set: %w", err)
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
			return "", TurnInfo{}, fmt.Errorf("turn: model consult: %w", err)
		}

		pre, err := readPreamble(sr)
		if err != nil {
			_ = sr.Close()
			return "", TurnInfo{}, fmt.Errorf("turn: read preamble: %w", err)
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
					return "", TurnInfo{}, fmt.Errorf("turn: recompose working set: %w", err)
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
			return "", TurnInfo{}, fmt.Errorf("turn: filter write head: %w", werr)
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
			return "", TurnInfo{}, fmt.Errorf("turn: stream: %w", streamErr)
		}
		if flushErr != nil {
			return "", TurnInfo{}, fmt.Errorf("turn: flush stream filter: %w", flushErr)
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
		return "", TurnInfo{}, err
	}

	// Step 5: turn close — fire deferred engagement updates with the
	// coalesced symbol set.
	if err := closeTurnAndUpdateEngagement(ctx, state, userInput, full.Content); err != nil {
		return "", TurnInfo{}, fmt.Errorf("turn: close: %w", err)
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

	// Step 5e: §3.11 commit-on-structural-change. The cadence check runs ONCE
	// here at turn close, not at each mutation site, so a turn with ≥1
	// structural change (thread create / §3.5 close-retire) yields EXACTLY one
	// substrate Checkpoint — a close+create switch or a closure-storm (#82)
	// coalesces to a single commit. The turn's accumulated content writes ride
	// along in the same commit. This MUST run after 5b/5c/5d: closure and
	// archival are the structural mutators, and the content they touched must
	// be staged by the time Checkpoint calls Add("."). A Checkpoint failure is
	// non-fatal (log and continue), consistent with the other close-time
	// substrate calls; the session-close commit is the backstop. Archival
	// commits via its own §3.8 batch, so it is excluded from the trigger.
	if commitOnStructuralChange && state.structuralCreates+state.structuralCloses > 0 {
		reason := fmt.Sprintf("structural: +%d thread, -%d retired",
			state.structuralCreates, state.structuralCloses)
		if err := state.Ops.Checkpoint(ctx, reason); err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategorySession, "checkpoint-error", memops.SanitizeDetail(err.Error()))
		}
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

	return body, TurnInfo{PromptTokens: full.Usage.PromptTokens}, nil
}
