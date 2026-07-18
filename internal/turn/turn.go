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
	"time"

	"personant/internal/clock"
	"personant/internal/crashpoint"
	"personant/internal/curator"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/recall/measure"
)

// Crashpoints for the R4 W-TURN matrix (#94 R3): the turn pipeline's own
// kill points. postJournalPreCanonical is the window between the fsynced
// response journal append and the first canonical write of turn close;
// betweenCanonicalRenames fires between successive canonical thread
// writes inside closeTurnAndUpdateEngagement (a multi-hit point — R4
// targets the kth crossing via crashpoint.ArmOnHit). Registered at
// import time for the coverage gate.
var (
	cpPostJournalPreCanonical = crashpoint.Register("turn.postJournalPreCanonical")
	cpBetweenCanonicalRenames = crashpoint.Register("turn.betweenCanonicalRenames")
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
	// sessions. The turn-pair count caps PERSISTENCE; #127 additionally
	// bounds the REPLAYED tail per request — boundHistoryTail recency-bounds
	// the slice sent to the model to its live-turn share (pair-aligned), so
	// a long session's replayed history stays within the whole-request
	// budget independent of the count cap.
	History []model.Message

	// ActiveThreads is Layer B membership: thread IDs the working set
	// renders as full thread bodies (per workset.Compose). Index 0 is
	// the most-recently-engaged thread. The list is bounded by
	// Budget.BTopK; overflow demotes to the head of DormantThreads.
	ActiveThreads []string

	// DormantThreads is Layer C membership: thread IDs the working set
	// renders as spine display lines. Index 0 is the most-recently
	// demoted (or independently engaged) thread. The slice is bounded
	// solely by Layer C's byte budget at render time (workset.Compose);
	// the v0.1 count cap was dropped in #127 in favor of byte truncation
	// (design §4 / Q2), which removes the m3 silent-drop behavior.
	DormantThreads []string

	// Budget is the token-denominated whole-request budget (#127).
	// memops.DefaultBudget() at NewState unless WithTokenCeiling overrides
	// the ceiling; future directive plumbing (Phase 3+) will read
	// context.token-budget from the directive layer into the ceiling.
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

	// tagReprompted records that this turn's D6 missing-tag re-prompt was
	// spent: the first stream lacked a leading topic tag while §3.9 file
	// edits were buffered, so the request was re-issued with
	// prompt.TopicTagReminder appended. Read by the close-time
	// owner-default (closeTurnAndUpdateEngagement) for the
	// thread.tag-defaulted line's reprompted= field, and by the re-issue
	// loop as the per-cause cap. Reset at the top of every Run alongside
	// turnOwner.
	tagReprompted bool

	// emptyReprompted records that this turn's D6 empty-response re-prompt
	// was spent: a stream ended with zero visible content (the
	// reasoning-burn signature), so the request was re-issued with
	// prompt.EmptyResponseReminder appended. Read by the close-time
	// owner-default for the thread.tag-defaulted line's reprompted= field
	// (cause=empty-response arm), and by the re-issue loop as the
	// per-cause cap. Reset at the top of every Run alongside tagReprompted.
	emptyReprompted bool

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
	// debt-cap flush (§6.2): when a thread's debt reaches EmbeddingDebtCap,
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

	// structuralCreates / structuralRetires / structuralWIPs count the §3.11
	// structural changes that occurred during the in-flight turn: thread
	// creations (createNewThread), §3.5 retire writes, and §3.5 WIP
	// demotions (both in applyClosureResolution). They are the turn-close
	// commit-cadence trigger — a turn with (creates+retires+wips) > 0 yields
	// EXACTLY ONE Checkpoint at Run close, never one per mutation, so a
	// close+create switch or a vacation closure-storm (#82, many closes in
	// one turn) coalesces to a single commit (§3.11). All three reset to 0
	// at the top of every Run; the counts also build the commit reason
	// string (e.g. "+1 thread, -2 retired, ~1 wip"). Retirements and WIP
	// demotions are counted SEPARATELY: a WIP demotion keeps the thread open
	// (state=wip), so folding it into a "retired" tally would misreport the
	// commit's forensic reason. Archival commits via its own §3.8 batch and
	// is deliberately NOT counted here.
	structuralCreates int
	structuralRetires int
	structuralWIPs    int
}

// sessionHistoryCapTurns bounds State.History to N user/assistant turn
// pairs (i.e. up to 2*N messages). Picked to match the v0.1
// working-set discipline of dormantThreadsCap=20: a coarse count cap
// that keeps a long session from growing linearly without committing
// to a token-aware budget. At realistic ~1-3 KB/message this caps
// replayed context at ~40-120 KB, well under common 128k-200k
// context-window limits. See the State.History godoc and MAD
// architecture-review burn-down item B1 for the rationale.
const sessionHistoryCapTurns = 20

// StateOption customizes a State at construction. It is the #127
// constructor-override seam: the budget's token ceiling (and, later,
// other directive parameters) flow in here rather than being hardcoded.
// NewState and LoadSession both apply options after building the default
// State, so an option overrides the default.
type StateOption func(*State)

// WithTokenCeiling overrides the whole-request token ceiling (#127),
// recomputing Budget from it via memops.BudgetForCeiling — the single
// source of truth for the partition (I7). A non-positive ceiling is
// ignored (keeps the default). This is path (b) of design §3.4: the
// ceiling is settable at the constructor today; the directive-file
// parser that feeds context.token-budget here lands in a later task.
func WithTokenCeiling(tokenCeiling int) StateOption {
	return func(s *State) {
		if tokenCeiling > 0 {
			s.Budget = memops.BudgetForCeiling(tokenCeiling)
		}
	}
}

// NewState constructs a State for a chat session. The coalesce buffer
// is initialized empty; Client must be non-nil (the chat REPL passes
// either an HTTPClient or a MockClient, never nil). Options (e.g.
// WithTokenCeiling) override defaults after construction.
func NewState(ops memops.MemoryOps, project memops.ProjectMeta, provider memops.Provider, client model.Client, opts ...StateOption) *State {
	s := &State{
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
	for _, opt := range opts {
		opt(s)
	}
	return s
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
func LoadSession(ctx context.Context, ops memops.MemoryOps, project memops.ProjectMeta, provider memops.Provider, client model.Client, opts ...StateOption) (*State, error) {
	state := NewState(ops, project, provider, client, opts...)
	active, dormant, err := ops.LoadWorkingSet(ctx)
	if err != nil {
		return nil, fmt.Errorf("turn: load session working set: %w", err)
	}
	state.ActiveThreads = active
	state.DormantThreads = dormant
	return state, nil
}

// Mid-turn re-prompt bound (§5.5 + D6). A turn allows AT MOST ONE
// system-injected re-prompt PER CAUSE, and there are exactly three causes:
//
//   - missing-thread (§5.5): the tag references a thr_<n> not in Layer B —
//     the intervention is context augmentation (fetch + recompose).
//   - missing-tag (D6): the response lacks a leading tag on a turn whose
//     §3.9 buffered file edits require owner binding — the intervention is
//     a protocol reminder (prompt.TopicTagReminder appended).
//   - empty-response (D6 extension, probe 2026-07-15): the response ended
//     with zero visible content, on ANY turn — the intervention is a
//     protocol reminder (prompt.EmptyResponseReminder appended). A
//     persistent-empty response SUBSUMES the missing-tag cause: it is
//     tag-less only vacuously, and a tag reminder to a model that twice
//     produced nothing is the same failed intervention class, so the
//     missing-tag re-prompt is suppressed for it (see the loop).
//
// Combined bound: ≤ 3 re-prompts, ≤ 4 model streams per turn. One per
// cause because the causes are independent failure modes with independent
// interventions; several firing in one turn requires as many distinct
// model failures, so the worst case is bounded AND rare. Never two for the
// same cause: a repeat would re-run an intervention that just demonstrably
// failed — no new information, only latency — so the second miss falls
// through to the cause's deterministic close-time fallback (LRU pickup for
// missing-thread; owner-default for missing-tag; accept-the-empty for
// empty-response — owner-default with an empty excerpt on a binding turn,
// empty body to the caller otherwise). The per-cause caps are the
// fetchReprompted / state.tagReprompted / state.emptyReprompted flags in
// RunWithInfo; a bool per cause IS the cap, so there is no tunable
// constant.

// newTurnID mints the per-turn transaction id (#94, SPEC §4.5.8) carried
// by the journal records (whose first record is the in-flight-turn
// signal) and the commit trailer. Recovery
// compares ids only for equality (journal turn vs HEADTURN), so the format
// is free — but ids must not repeat across sessions: a fresh session's
// turn colliding with a prior session's committed trailer would make a
// cell-6 crash (nothing landed) read as cell 5 (committed), truncating a
// journal that still held unrecovered content. The session-scoped turn
// number alone repeats after every relaunch, so a real-clock instant is
// folded in — clock.Profiling, NOT clock.Timeline. This is a legitimate
// real-time read under the clock-discipline rule: the suffix is a
// uniqueness token, not a simulated-world timestamp, and Timeline is
// exactly the clock a sim pins — a pinned Timeline plus RestartSession's
// TurnNumber reset minted IDENTICAL ids across sessions, defeating the
// HEADTURN==T equality predicate (cell-5 misclassification truncating an
// unpreserved journal). The t<N>- prefix stays for human readability.
func newTurnID(turnNumber int) string {
	return "t" + strconv.Itoa(turnNumber) + "-" + strconv.FormatInt(clock.Profiling().UnixNano(), 10)
}

// releaseAbortedTurn releases the turn's crash-recovery scope after a
// PRE-CANONICAL abort (journal failure, model-consult failure, stream
// failure): nothing canonical was written, so ReleaseTurn truncates the
// journal (the scope release) WITHOUT a commit. The event log is
// dirty on every turn, so a commit-shaped release was never empty — a
// provider-outage retry loop emitted one real (logs-only) commit per
// error; the markerless log dirt instead absorbs into the next
// successful turn's commit, exactly as recovery's cell-3/logs carve-out
// treats it. Without any release, the in-flight journal would survive
// the abort and the NEXT turn's JournalTurn would refuse the
// conflicting scope, wedging the session on a transient model error.
//
// Post-canonical failures never come here — they leave the scope open
// (journal non-empty) so the turn fails loudly into the ≤1-loss
// recovery path (Reconcile).
//
// context.WithoutCancel: the abort may itself be a context cancellation
// (SIGINT mid-stream); the release — and the forensic log line on a
// release failure — is cleanup that must still run. A release failure
// is logged, never propagated — the abort's original error is the one
// the caller needs, and an unreleased scope heals at the next Reconcile.
func releaseAbortedTurn(ctx context.Context, state *State, turnID string) {
	rctx := context.WithoutCancel(ctx)
	if err := state.Ops.ReleaseTurn(rctx, turnID); err != nil {
		_ = state.Ops.Log(rctx, memops.LogCategorySystem, "turn-abort-release-error",
			"turn="+turnID+" err="+memops.SanitizeDetail(err.Error()))
	}
}

// ErrMarkerRetained tags a turn failure that deliberately left the
// turn's in-flight scope OPEN — the non-empty journal, the op=turn
// signal (a CommitTurn failure: canonical writes
// exist that no recovery point covers). Retrying in-session cannot
// succeed — every subsequent JournalTurn refuses the conflicting scope
// — so callers (the chat REPL) key on this to advise a restart, and to
// warn that prompts typed in the wedged window are not captured.
var ErrMarkerRetained = errors.New("turn recovery marker retained")

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
//
// TurnID is the per-turn transaction id (#94) stamped into the commit
// trailer; CommitDuration is the measured wall-clock cost of the turn's
// CommitTurn call — the per-turn-commit latency the crash-stability
// design's measurement plan requires (the harness records it into the
// turn_commit_ms histogram). JournalDuration is the summed wall-clock
// cost of the turn's two fsynced JournalTurn appends (prompt +
// response) — the other, previously un-instrumented half of the
// measured ~160ms/turn crash-stability overhead (the harness records it
// into the turn_journal_ms histogram beside turn_commit_ms).
// GCDuration is the wall-clock cost of the post-commit loose-object
// pressure gc (MemoryOps.MaybeGC, #94 R3-addendum item 4) — measured
// OUTSIDE the CommitDuration window so a repack never contaminates
// turn_commit_ms (the harness records it into turn_gc_ms). It is zero on
// the common turn where the throttled check does not fire.
type TurnInfo struct {
	PromptTokens    int
	TurnID          string
	CommitDuration  time.Duration
	JournalDuration time.Duration
	GCDuration      time.Duration
	// Barrier is the pre-turn day-barrier poll's result (#94 R3b §6.3).
	// Zero on the common same-day turn; when the poll sealed a day the
	// caller (the sim harness) records the duration gauges from it —
	// the seal happens INSIDE the turn call, so this is the only place
	// the measurements surface.
	Barrier memops.DayBarrierResult
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
//  1. Bump TurnNumber, journal the prompt (fsync — opens the #94 turn
//     recovery scope: the non-empty journal IS the in-flight-turn
//     signal), then window-close GC on staging (B.4 lifecycle).
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
//     §5.5), and the request is re-issued with augmented context. If the
//     response lacks a tag while §3.9 file edits are buffered, the stream
//     is aborted and re-issued with prompt.TopicTagReminder appended (D6).
//     If the response has zero visible content — on any turn — it is
//     re-issued with prompt.EmptyResponseReminder appended (D6 extension).
//     Re-prompts are capped at 1 per cause, ≤ 3 per turn (see the bound
//     comment above NewState).
//  6. Fire model.response context-modify event (parses topic tag,
//     accumulates symbols, defers engagement) after the stream completes.
//     Per §3.0.5 the model.response delta fires once with the full body,
//     not per chunk.
//  7. Journal the response (fsync, before any canonical write), then
//     close-out the turn: fire engagement updates with the coalesced
//     symbol set (§3.0.4), run the §3.5 closure scan, and land the
//     per-turn CommitTurn recovery point (#94). Archival (§3.8) and the
//     working-set save run strictly AFTER CommitTurn, outside the turn's
//     scope window.
//  8. Return the response body with the topic tag stripped (matches what
//     the user saw on out).
//
// Errors at any step are wrapped and returned. The coalesce buffer is
// reset at the start of each call so per-turn accumulation is fresh.
//
// out receives the streamed response body with the §5.1 topic tag
// suppressed; pass io.Discard to keep the streaming behavior without
// presenting tokens (e.g. tests that only assert on side-effects).
func RunWithInfo(ctx context.Context, state *State, preEvents []Delta, userInput string, out io.Writer) (_ string, _ TurnInfo, err error) {
	if state == nil {
		return "", TurnInfo{}, errors.New("turn: nil state")
	}
	if state.Client == nil {
		return "", TurnInfo{}, errors.New("turn: nil model client")
	}
	if out == nil {
		out = io.Discard
	}
	// Ensure the budget is materialized before any live-turn bounding so
	// the reject check below and the post-flight ceiling assertion read a
	// real ceiling, not the zero value.
	if state.Budget.Total == 0 {
		state.Budget = memops.DefaultBudget()
	}
	// #127 live-turn reject (design §3.4 / Q3): a single user-authored
	// userInput that exceeds its live-turn reserve is rejected with a
	// user-facing message BEFORE any chain step fires — rejecting after
	// staging GC / delta emission would leave half-applied side effects.
	// Non-user-authored live-turn components (tool.result deltas, the
	// history tail) are bounded by truncation/recency below, not rejected.
	if err := checkUserInput(userInput, state.Budget); err != nil {
		return "", TurnInfo{}, err
	}
	// #94 R3b §2.1: the pre-turn new-day check. The day barrier is a
	// between-turns batch op, so it must fire BEFORE this turn's recovery
	// scope opens (the JournalTurn below). The common same-day poll is a
	// cached in-memory compare; when a barrier DID run, any threads its
	// archival drain removed are evicted from the working-set LRU here
	// (the P2-1 phantom guard — an archived id lingering in
	// ActiveThreads/DormantThreads would be persisted by SaveWorkingSet
	// pointing at a thread that no longer exists). A barrier error fails
	// the turn: the substrate's own marker makes the next open detect and
	// complete the interrupted barrier.
	bres, berr := state.Ops.MaybeDayBarrier(ctx)
	if berr != nil {
		return "", TurnInfo{}, fmt.Errorf("turn: day barrier: %w", berr)
	}
	for _, id := range bres.ArchivedThreads {
		state.ActiveThreads = removeString(state.ActiveThreads, id)
		state.DormantThreads = removeString(state.DormantThreads, id)
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
	// The D6 tag / empty-response re-prompt flags are per-turn — clear them
	// so a prior turn's re-prompt neither caps this turn's nor mislabels
	// its thread.tag-defaulted line.
	state.tagReprompted = false
	state.emptyReprompted = false
	// §3.11 structural-change counters are per-turn — clear them so the
	// turn-close cadence check sees only this turn's creates/retires/wips.
	state.structuralCreates = 0
	state.structuralRetires = 0
	state.structuralWIPs = 0
	// Bump the turn counter BEFORE any chain step fires so the user.prompt
	// delta and the model.response delta both observe the same
	// TurnNumber. The transient-data lifecycle B.4 window-close GC keys
	// off StagedAt-vs-TurnNumber, so the convention "TurnNumber reflects
	// the in-flight turn" must hold for the whole of Run.
	state.TurnNumber++

	// Crash-stability (#94 R3): open the turn's recovery scope by
	// journaling the prompt — durably (fsync) and BEFORE the model call,
	// so a crash during the consult loses no typed content (SOLUTION
	// principle 3). The first JournalTurn append IS the in-flight-turn
	// signal (R3-addendum fold), and it runs before every canonical write
	// below, which is what licenses recovery's marker-gated reset. A
	// journal failure aborts the turn before anything canonical happens —
	// clean error to the caller, scope released by the deferred handler.
	turnID := newTurnID(state.TurnNumber)
	journalStart := clock.Profiling()
	jerr := state.Ops.JournalTurn(ctx, turnID, memops.TurnContentPrompt, []byte(userInput))
	journalDur := clock.Since(journalStart)
	if jerr != nil {
		err = fmt.Errorf("turn: journal prompt: %w", jerr)
		// The deferred release below is not yet armed — release directly.
		releaseAbortedTurn(ctx, state, turnID)
		return "", TurnInfo{}, err
	}
	// Pre-canonical abort handler: any error return between here and the
	// first canonical write releases the turn scope (see
	// releaseAbortedTurn). Once canonicalStarted flips, failures leave the
	// scope open (journal non-empty) so the turn fails loudly into the
	// ≤1-loss recovery path — including a CommitTurn failure (never
	// half-release).
	canonicalStarted := false
	defer func() {
		if err != nil && !canonicalStarted {
			releaseAbortedTurn(ctx, state, turnID)
		}
	}()

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
	//
	// #127: bound current-turn tool.result content (truncate-with-marker)
	// before it enters the chain so a single verbose tool result (design
	// m1) cannot balloon the request via staging/memory. Non-user-authored,
	// so truncated rather than rejected (design §3.4).
	preEvents = boundToolResultDeltas(preEvents, state.Budget)
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
	// (Budget is materialized at the top of Run, before the reject check.)
	// Once a D6 re-prompt has fired (state.tagReprompted /
	// state.emptyReprompted), every recomposition — including a later §5.5
	// fetch recompose in the same turn — re-appends that cause's reminder,
	// so the fetch path cannot silently drop it. Append order is fixed
	// (tag, then empty) for determinism when both causes fired.
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
		sp := prompt.BuildSystemPrompt(prompt.SystemPromptElements{
			LayerE:  ws.LayerE,
			LayerA1: ws.LayerA1,
			LayerA2: ws.LayerA2,
			LayerB:  ws.LayerB,
			LayerC:  ws.LayerC,
		})
		if state.tagReprompted {
			sp += "\n\n" + prompt.TopicTagReminder
		}
		if state.emptyReprompted {
			sp += "\n\n" + prompt.EmptyResponseReminder
		}
		return sp, nil
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

	// Per-cause re-prompt caps (see the mid-turn re-prompt bound comment
	// above NewState). fetchReprompted is loop-local; the tag cause lives
	// on State because the close-time owner-default reads it.
	fetchReprompted := false

	var full model.Response
	for attempt := 0; ; attempt++ {
		// #127: recency-bound the replayed history tail to its live-turn
		// share (pair-aligned, I5) so the live turn stays within
		// LiveTurnReserve. This implements the byte/token bound the
		// State.History godoc flagged as deferred. The full History is
		// kept in state (the FIFO cap still governs persistence); only the
		// replayed slice for THIS request is bounded.
		historyTail := boundHistoryTail(state.History, state.Budget)
		messages := make([]model.Message, 0, 2+len(historyTail))
		messages = append(messages, model.Message{Role: "system", Content: systemPrompt})
		messages = append(messages, historyTail...)
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

		// D6 empty-response re-prompt (cause: empty-response): the stream
		// ended with ZERO visible content — nothing to tag, bind, or show.
		// A protocol failure on ANY turn (binding or conversational), so
		// this check precedes the tag-oriented branches below. Abort
		// (already-ended) stream handling and re-issue with the reminder
		// appended. Once per turn for this cause; a second empty response
		// falls through — drained as-is, then at close: a binding turn hits
		// the owner-default (empty excerpt), a conversational turn surfaces
		// the empty body to the caller; either way the system.empty-response
		// forensic line below records it.
		emptyResponse := preambleIsEmpty(pre)
		if emptyResponse && !state.emptyReprompted {
			state.emptyReprompted = true
			_ = sr.Close()
			_ = state.Ops.Log(ctx, memops.LogCategoryTopic, "re-prompt",
				"cause=empty-response attempt="+strconv.Itoa(attempt+1))
			systemPrompt, err = buildSystemPrompt()
			if err != nil {
				return "", TurnInfo{}, fmt.Errorf("turn: recompose working set: %w", err)
			}
			continue
		}

		// §5.5 mid-turn fetch (cause: missing-thread): if the preamble is a
		// topic tag referencing a thr_<n> not in Layer B, abort the stream,
		// fetch the thread, and re-prompt. Once per turn for this cause.
		if !fetchReprompted && pre.tag != nil {
			missing := missingFromActiveB(pre.tag.Threads, state.ActiveThreads)
			fetched := 0
			for _, thrID := range missing {
				if fetchThreadForReprompt(ctx, state, thrID) {
					fetched++
				}
			}
			if fetched > 0 {
				fetchReprompted = true
				_ = sr.Close()
				_ = state.Ops.Log(ctx, memops.LogCategoryTopic, "re-prompt",
					"cause=missing-thread fetched="+strconv.Itoa(fetched)+" attempt="+strconv.Itoa(attempt+1))
				systemPrompt, err = buildSystemPrompt()
				if err != nil {
					return "", TurnInfo{}, fmt.Errorf("turn: recompose working set: %w", err)
				}
				continue
			}
		}

		// D6 tag re-prompt (cause: missing-tag): the response conclusively
		// lacks a leading topic tag AND this turn's deltas require owner
		// binding (§3.9 buffered file edits — complete by now, since fs.*
		// deltas arrive as pre-prompt events; nothing after the stream adds
		// to the buffer). Abort the stream and re-issue with the reminder
		// appended (buildSystemPrompt reads state.tagReprompted). Once per
		// turn for this cause; a still-tag-less second response falls
		// through to the close-time owner-default. A tag-less turn WITHOUT
		// buffered edits is deliberately not re-prompted — it proceeds
		// tag-less (see closeTurnAndUpdateEngagement's conversational
		// branch). Mutually exclusive with the fetch branch above within
		// one attempt (that one requires pre.tag != nil). Gated on
		// !emptyResponse: a persistent-empty response is tag-less only
		// vacuously — the empty-response cause above owns it, and spending
		// a tag reminder on a model that twice produced no content would
		// re-run the just-failed intervention class (see the bound comment
		// above NewState); it falls through to the owner-default directly.
		if !emptyResponse && !state.tagReprompted && len(state.fileEdits) > 0 && preambleLacksTag(pre) {
			state.tagReprompted = true
			_ = sr.Close()
			_ = state.Ops.Log(ctx, memops.LogCategoryTopic, "re-prompt",
				"cause=missing-tag attempt="+strconv.Itoa(attempt+1))
			systemPrompt, err = buildSystemPrompt()
			if err != nil {
				return "", TurnInfo{}, fmt.Errorf("turn: recompose working set: %w", err)
			}
			continue
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

	// Crash-stability (#94 R3): journal the response — durably, BEFORE any
	// canonical write — so the journal holds the full (prompt, response)
	// byte pair for the in-flight turn. From here to CommitTurn a crash
	// resolves to cell 4/6 with the content recoverable from the journal.
	// A journal failure aborts pre-canonical (deferred release).
	journalStart = clock.Profiling()
	jerr = state.Ops.JournalTurn(ctx, turnID, memops.TurnContentResponse, []byte(full.Content))
	journalDur += clock.Since(journalStart)
	if jerr != nil {
		return "", TurnInfo{}, fmt.Errorf("turn: journal response: %w", jerr)
	}
	crashpoint.At(cpPostJournalPreCanonical)

	// #127 post-flight integrity gate (I1, design §3.3): the provider's
	// usage.prompt_tokens is the only modality-agnostic authoritative
	// count of the fully-assembled request. A count above Budget.TokenCeiling
	// means the byte-driven pre-flight bounding under-estimated tokens (or
	// something bypassed it) — a zero-tolerance integrity finding routed
	// through the structured logger, never swallowed. The ceiling is read
	// from the budget, not hardcoded. On the mock path PromptTokens is a
	// canned small value, so this never fires there (the assertion is
	// meaningful only on the real-model rung); that is correct — the
	// mock-independent guarantee is the byte pre-flight above.
	if state.Budget.TokenCeiling > 0 && full.Usage.PromptTokens > state.Budget.TokenCeiling {
		_ = state.Ops.Log(ctx, memops.LogCategorySystem, "context-ceiling-breach",
			fmt.Sprintf("prompt_tokens=%d ceiling=%d turn=%d",
				full.Usage.PromptTokens, state.Budget.TokenCeiling, state.TurnNumber))
	}

	// D6 persistent-empty forensic line: the FINAL drained response carries
	// zero visible content — either the empty-response re-prompt also came
	// back empty, or a pathological over-bound all-whitespace shape slipped
	// the preamble trigger (see preambleIsEmpty's bound note). The REPL
	// prints nothing for an empty body (chat.runOneTurn only re-aligns the
	// prompt line), so without this line a blank turn would be forensically
	// invisible.
	if strings.TrimSpace(full.Content) == "" {
		reprompted := "no"
		if state.emptyReprompted {
			reprompted = "yes"
		}
		_ = state.Ops.Log(ctx, memops.LogCategorySystem, "empty-response",
			"reprompted="+reprompted+" turn="+strconv.Itoa(state.TurnNumber))
	}

	// Step 4: model.response delta with the full accumulated body —
	// fired once, not per chunk (spec §3.0.5).
	if err := onContextDelta(ctx, state, Delta{Source: memops.SourceModelResponse, Content: full.Content}); err != nil {
		return "", TurnInfo{}, err
	}

	// Step 5: turn close — fire deferred engagement updates with the
	// coalesced symbol set. This is the first canonical write of the turn:
	// from here on a failure leaves the scope open (fails into recovery)
	// rather than releasing it.
	canonicalStarted = true
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

	// Step 5c: CommitTurn — the per-turn durability recovery point (#94,
	// SOLUTION principle 1). Every turn commits: the turn's canonical
	// writes (content + any §3.5 structural changes from 5b) land in ONE
	// commit carrying the Personant-Turn trailer, and the journal
	// truncates (the scope release). This supersedes the former §3.11
	// commit-on-structural-change Checkpoint (structural commits are
	// absorbed — the reason string still reports the counts forensically);
	// the session-close Checkpoint in chat.go survives only as a
	// belt-and-braces backstop, normally a no-op. It MUST run after 5b:
	// closure is a structural mutator and its writes must be on disk when
	// CommitTurn stages Add(".").
	//
	// A CommitTurn failure is FATAL to the turn and leaves the scope open
	// (never half-release): canonical writes exist that no recovery point
	// covers, so the turn fails loudly into the ≤1-loss recovery path —
	// the next Reconcile rolls back to the last committed turn and
	// preserves this turn's journaled content.
	reason := ""
	if n := state.structuralCreates + state.structuralRetires + state.structuralWIPs; n > 0 {
		reason = fmt.Sprintf("structural: +%d thread, -%d retired, ~%d wip",
			state.structuralCreates, state.structuralRetires, state.structuralWIPs)
	}
	commitStart := clock.Profiling()
	if cerr := state.Ops.CommitTurn(ctx, turnID, reason); cerr != nil {
		// Both %w verbs wrap: callers match the cause with errors.Is/As as
		// before, and additionally match ErrMarkerRetained to recognize the
		// restart-to-recover wedge (see the sentinel's doc).
		return "", TurnInfo{}, fmt.Errorf("turn %s: commit: %w [%w]", turnID, cerr, ErrMarkerRetained)
	}
	commitDur := clock.Since(commitStart)

	// Loose-object pressure gc (#94 R3-addendum item 4) runs HERE, after the
	// commit-timing capture, so its repack cost never contaminates
	// turn_commit_ms. CommitTurn already released the turn's recovery scope
	// (it truncated the journal), so this is a between-turns op in its own
	// op=sleep scope; best-effort and synchronous, it never fails the turn.
	gcStart := clock.Profiling()
	state.Ops.MaybeGC(ctx)
	gcDur := clock.Since(gcStart)

	// Step 5d (RETIRED under #94 R3b): the §3.8 cardinality-pressure
	// archival drain no longer fires at turn close — archival is
	// BARRIER-ONLY (the day barrier's B1, INV-5: primary is
	// barrier-exclusive). The pressure policy itself survives as
	// memops.SelectArchivalCandidates, applied by the adapter at B1; the
	// pre-turn MaybeDayBarrier call at the top of this function is where
	// a drain's working-set evictions reach this State.

	// Step 5e: persist the updated Layer B/C working-set membership so a
	// clean shutdown→relaunch resumes the working set instead of
	// cold-starting it empty. This MUST run after step 5b: closure
	// retirement evicts threads from ActiveThreads / DormantThreads, so
	// saving any earlier would persist a stale set (a thread the same
	// turn went on to evict). It also runs on no-engagement turns —
	// closeTurnAndUpdateEngagement returns early then, but closure can
	// still have evicted something. The artifact is operational
	// (gitignored), so writing it after CommitTurn is outside any marker
	// scope's concern. A save failure is non-fatal: log and continue,
	// consistent with the other close-time substrate calls
	// (AgeFileChains, recall).
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
	if histCap := 2 * sessionHistoryCapTurns; len(state.History) > histCap {
		drop := len(state.History) - histCap
		// Drop is always even because both the append above and any
		// prior trim leave History even-length, and histCap is even.
		state.History = append(state.History[:0:0], state.History[drop:]...)
	}

	return body, TurnInfo{
		PromptTokens:    full.Usage.PromptTokens,
		TurnID:          turnID,
		CommitDuration:  commitDur,
		JournalDuration: journalDur,
		GCDuration:      gcDur,
		Barrier:         bres,
	}, nil
}
