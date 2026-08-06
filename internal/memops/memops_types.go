package memops

import "io"

// This file holds the operation-supporting types referenced by the
// MemoryOps interface — filters, options, working-set inputs, bootstrap
// and verification result shapes. The domain data-model types
// (SpineRecord, ThreadMeta, Thread, ProjectMeta, Provider, the
// symbol/state enums, the sentinel errors, ParseModelRef/ValidateConfig)
// are real definitions in memops_model.go: the port owns its domain
// types outright. internal/store and the adapters depend on memops for
// them — the dependency arrow runs substrate → port, the correct
// direction for ports-and-adapters.

// ---------- RetentionClass ----------

// RetentionClass classifies a context-modification delta for the
// transient-data lifecycle (sister memory entry
// project_personant_transient_data_lifecycle.md). The field on Delta
// captures the §3.0.1 source-based *provisional* class:
//
//   - RetentionTask: tool.result / user.shell-capture style content,
//     default-transient. Bytes are discardable after the citation
//     window closes; only symbols cross-referenced by a subsequent
//     decision delta get promoted into the persistent symbol index.
//
//   - RetentionDecision: user.prompt / model.response style content,
//     default-persistent. Threads and persistent symbols are anchored
//     to decision deltas.
//
// The file adapter in step A.2 ignores the field — every delta is
// treated as persistent. The field exists on the port so the §3.0
// chain integration and a future transient-data-aware adapter can use
// it without an interface change.
type RetentionClass string

const (
	// RetentionTask is the provisional-transient class
	// (tool.result, user.shell-capture).
	RetentionTask RetentionClass = "task"
	// RetentionDecision is the provisional-persistent class
	// (user.prompt, model.response).
	RetentionDecision RetentionClass = "decision"
)

// ---------- Delta ----------

// Delta is one context-modification event (spec §3.0.1). The port
// receives Deltas from the application-layer §3.0 chain; the adapter
// records them on the substrate side.
//
// Canonical Delta type used by both the port and internal/turn.
type Delta struct {
	// Source is the dotted §3.0.1 event name
	// ("user.prompt", "model.response", "tool.result", ...).
	Source string

	// Content is the raw delta text.
	Content string

	// Meta carries source-specific metadata (e.g. {"thr": "thr_3"}
	// on a thread.fetched delta). nil-safe.
	Meta map[string]string

	// Retention is the provisional retention class derived from
	// Source. Callers set this; the adapter may use it (transient-
	// data lifecycle work) or ignore it (current file adapter).
	// Zero value (empty string) is interpreted as RetentionDecision
	// — the safe default — so callers that have not yet migrated do
	// not silently drop content.
	Retention RetentionClass
}

// ---------- Thread write payload ----------

// ThreadWrite bundles the three pieces of state needed to materialize a
// thread on the substrate: the spine index entry, the metadata block
// (which mirrors the spine fields), and the new turn's excerpt.
//
// The three are kept distinct on the port surface because they map to
// different substrate concerns in the file adapter (spine.jsonl vs
// thread.md metadata vs one turns/ excerpt file), and callers in
// internal/turn build them up side-by-side during turn close. Bundling
// them into one struct at the call site is what eliminates the
// historical three-parameter signature; the adapter's job is to
// materialize the bundle atomically.
//
// Invariant: Spine.ID == Meta.ID. The adapter does not enforce
// this; callers are responsible for keeping the two in sync at
// construction.
type ThreadWrite struct {
	// Spine is the canonical index entry for the thread. Always written
	// to spine.jsonl by the adapter.
	Spine SpineRecord

	// Meta is the thread metadata block. Always written to the
	// thread's metadata slot by the adapter (thread.md frontmatter in
	// the file adapter).
	Meta ThreadMeta

	// TurnExcerpt is the terse operational excerpt for the turn this
	// write records. The adapter appends it as a new turn-excerpt file
	// numbered by Meta.TurnCount (the recency-windowed turns/
	// directory; see store.AppendThreadTurn). An empty TurnExcerpt is a
	// meta-only update — it appends no turn file. The closure path uses
	// the empty form to rewrite metadata without recording a turn.
	TurnExcerpt string
}

// ---------- Filters and options ----------

// ThreadFilter narrows the result set of ListThreads. The zero value
// returns the full spine.
//
// ALT: an explicit "any state" sentinel could be cleaner than
// nil-States meaning "no state filter"; revisit on first caller
// migration.
type ThreadFilter struct {
	// Project, when non-empty, restricts to threads whose
	// SpineRecord.Project equals this ID.
	Project string

	// States, when non-empty, restricts to threads whose
	// SpineRecord.State is in this set. nil/empty disables state
	// filtering.
	States []ThreadState

	// SinceTurn, when > 0, restricts to threads whose TurnCount is
	// >= SinceTurn. Useful for finding recently active threads
	// without loading every record.
	SinceTurn int
}

// RecallOptions governs ProposeRecall behavior. Promoted from
// recall.Options; field semantics are identical so callers porting
// over change only the type name.
type RecallOptions struct {
	// Threshold is the minimum Jaccard score for a candidate to be
	// returned. 0 → adapter default (spec §2.6.1
	// recall.symbolic-threshold = 0.4).
	Threshold float64

	// Project restricts candidates to threads whose
	// SpineRecord.Project equals this ID. Empty disables project
	// filtering.
	Project string

	// Exclude is the set of thread IDs to omit from results
	// (typically the threads already engaged in the current turn).
	// nil/empty → no exclusion.
	Exclude map[string]struct{}

	// Limit caps the number of returned candidates. 0 → adapter
	// default (10). Negative → unbounded.
	Limit int

	// SupersededWeight scales a matched superseded symbol's
	// contribution to the Jaccard numerator. 0 → adapter default (1.0,
	// = today's no-down-weight behavior; spec §3.4
	// recall.superseded-weight).
	SupersededWeight float64
}

// RecallCandidate is one match returned by ProposeRecall. Promoted
// from recall.Candidate; field semantics are identical.
type RecallCandidate struct {
	// ThreadID is the matched thread.
	ThreadID string
	// Score is the Jaccard score of the query against the thread's
	// symbol set.
	Score float64
	// MatchedSymbols is the sorted query ∩ thread-set used for
	// surface-UI lines and structured log records.
	MatchedSymbols []string
}

// ---------- Working set ----------

// WorksetInput carries the per-turn inputs for ComposeWorkingSet.
// Promoted from workset.State minus the PersonantPaths field (paths
// are the adapter's secret).
//
// ActiveThreads / DormantThreads are ordered: index 0 is the most
// recently engaged thread. The turn package owns the LRU update path;
// the adapter trusts the ordering it receives.
type WorksetInput struct {
	// ActiveProject is the project meta for the current session.
	ActiveProject ProjectMeta

	// ActiveThreads is Layer B membership (full thread bodies),
	// most-recently-engaged first. Bounded by Budget.BTopK at the
	// caller.
	ActiveThreads []string

	// DormantThreads is Layer C membership (spine display lines),
	// most-recently-engaged first. Bounded by the caller; the
	// adapter honors the supplied order.
	DormantThreads []string

	// Budget is the byte budget for layer composition. Zero-valued
	// → adapter default.
	Budget Budget
}

// WorksetLayers is the rendered per-layer content for one turn,
// ready to be assembled into a system prompt by internal/prompt.
// Mirrors the §3.1 layer surface (E / A1 / A2 / B / C) on the port
// side; the application layer copies it field-by-field into
// prompt.SystemPromptElements at the prompt-assembly seam.
//
// The two types stay distinct on purpose: WorksetLayers is the
// substrate's output (whatever the composer produced); SystemPromptElements
// is the prompt template's input contract. They happen to have identical
// fields today; future prompt-template inputs that aren't layer content
// (e.g. directive overrides) belong in SystemPromptElements and not here.
type WorksetLayers struct {
	// LayerE is directive files + project conventions (spec §3.1).
	LayerE string
	// LayerA1 is the current project's spine entries in display
	// form (spec §2.2.2).
	LayerA1 string
	// LayerA2 is other projects' compressed digests.
	LayerA2 string
	// LayerB is actively engaged threads' full content.
	LayerB string
	// LayerC is recently engaged dormant threads' summaries.
	LayerC string
}

// Budget is the token-denominated whole-request context budget (spec
// §3.1, parameters from §2.6.1). It carries one authoritative token
// ceiling plus the derived per-layer *byte* allocations that drive
// truncation.
//
// Token vs byte split (#127):
//   - TokenCeiling is the I1 gate — the fully-assembled request
//     (system prompt + history + current user input + injected fetches)
//     must not exceed it in tokens. It is checked post-flight against
//     the provider's usage.prompt_tokens, the only modality-agnostic
//     authoritative count.
//   - The per-layer byte fields (LayerE/A1/A2/B/C) and LiveTurnReserve
//     are the truncation *driver* (I4): they are derived from the token
//     ceiling via bytesPerTokenConservative so byte-truncation
//     under-fills rather than over-fills the token window. Bytes are a
//     deliberately conservative proxy reconciled empirically later
//     (§6.5 / #118 / #98).
//
// Byte total = LayerE + LayerA1 + LayerA2 + LayerB + LayerC +
// LiveTurnReserve. LiveTurnReserve is the I2 reservation for the live
// turn (current user input + current-turn tool.result deltas + a
// bounded recent-history tail). It is NOT consumed by workset.Compose
// (which sees only the memory layers); the live-turn sub-policy in
// package turn owns it. It replaces the dead CurrentTurn accounting
// field.
type Budget struct {
	// TokenCeiling is the authoritative whole-request token gate (I1).
	// ~DefaultTokenCeiling by default. The per-layer byte fields are
	// derived from this; assertions check assembled tokens against it.
	TokenCeiling int
	// ResponseReserveTokens mirrors model.Request.MaxTokens (I3): the
	// response headroom reserved below the model window so that
	// TokenCeiling + ResponseReserveTokens ≤ window. The Budget and the
	// request must agree (see turn.go).
	ResponseReserveTokens int

	// Total is the overall context byte budget (TokenCeiling × the
	// conservative bytes/token ratio). It is the byte envelope the
	// per-layer + live-turn shares partition.
	Total int
	// LayerE is directives + conventions (§2.6.1: E=12% of the memory
	// byte budget).
	LayerE int
	// LayerA1 is the current project's spine display (fixed portion of
	// the 18% high tier).
	LayerA1 int
	// LayerA2 is cross-project digests (residue of the 18% high tier).
	LayerA2 int
	// LayerB is active thread bodies (§2.6.1: B=55%).
	LayerB int
	// LayerC is dormant thread summaries (§2.6.1: C=15%).
	LayerC int
	// LiveTurnReserve is the byte reservation for the live turn — current
	// user input + current-turn tool.result deltas + a bounded
	// recent-history tail (§2.6.1: live_turn=15% of Total). Owned by the
	// live-turn sub-policy in package turn (I2); never consumed by
	// workset.Compose.
	LiveTurnReserve int
	// BTopK caps the number of threads in Layer B
	// (§2.6.1: layer.b-top-k; default 3).
	BTopK int
	// PerProjectDigestBytes caps a single project's A2 line
	// (§2.6.1: cross-project.digest-per-project-bytes; default 150).
	PerProjectDigestBytes int
}

// DefaultTokenCeiling is the v0.1 whole-request token ceiling (#127): the
// I1 gate value. ~200K bakes in headroom below the 256K gemma-4 window
// for response reserve (DefaultResponseReserveTokens) and attention
// attenuation. Exposed to the directive layer as `context.token-budget`
// once directive plumbing lands; until then NewState reads it from this
// const (or a constructor override).
const DefaultTokenCeiling = 200000

// DefaultResponseReserveTokens mirrors model.DefaultRequest's MaxTokens
// (16384). I3: TokenCeiling + DefaultResponseReserveTokens ≤ the 256K
// window. The Budget value and the request's MaxTokens must agree; the
// turn loop asserts this (A6). It lives here so the partition arithmetic
// is testable without importing package model (no dependency inversion).
const DefaultResponseReserveTokens = 16384

// bytesPerTokenConservative is the single chars-per-token ratio used to
// derive the byte truncation budget from the token ceiling (I4, #127
// DECISIONS). Deliberately conservative (code/JSON tokenizes denser than
// prose) so byte-truncation under-fills the token window rather than
// overshooting it; the post-flight token assertion is the real gate.
//
// TEXT-ONLY. Image/multimodal content tokenizes by patch count, not byte
// size — bytes/2.5 is invalid for images. The runtime is text-only today
// (#128 tracks multimodal token estimation); the byte→token pre-flight
// estimate is valid only for text. The post-flight usage.prompt_tokens
// assertion remains correct regardless of modality.
const bytesPerTokenConservative = 2.5

// Default percentage allocations (§2.6.1 layer.budget.percentages, #127
// re-denomination). LiveTurnReserve takes 15% of the byte Total off the
// top; the remaining 85% (the memory byte budget) is partitioned
// 12/18/15/55 over non-volatile→E / high→A1+A2 / medium→C / low→B.
// LayerA2 is the residue of the 18% high tier (A1 fixed, A2 = high − A1),
// matching SPEC §3.1's "A2 is variable / residue".
const (
	// defaultPctLiveTurn is carved from the byte Total before the memory
	// partition (§2.6.1: live_turn=15%).
	defaultPctLiveTurn = 15

	// The memory partition percentages are taken over the MEMORY byte
	// budget (Total − live-turn reserve), not over Total.
	defaultPctLayerE = 12
	defaultPctHighA  = 18 // A1 + A2 combined high tier
	defaultPctLayerC = 15
	defaultPctLayerB = 55
	// defaultPctLayerA1 is the fixed portion of the high tier; A2 is the
	// residue (defaultPctHighA − defaultPctLayerA1). 10/8 is a
	// calibration starting point (§9.4).
	defaultPctLayerA1 = 10
	defaultPctLayerA2 = defaultPctHighA - defaultPctLayerA1

	// DefaultBTopK matches §2.6.1's layer.b-top-k.
	DefaultBTopK = 3

	// DefaultPerProjectDigestBytes matches §2.6.1's
	// cross-project.digest-per-project-bytes.
	DefaultPerProjectDigestBytes = 150
)

// DefaultBudget returns a Budget computed from DefaultTokenCeiling. The
// token ceiling is the authoritative number; the byte Total is derived
// (ceiling × bytesPerTokenConservative), then the live-turn reserve is
// carved off and the memory remainder partitioned at the §2.6.1
// percentages. Rounding is integer truncation; shares sum to ≤ Total
// (any remainder is discarded, never padded, so the budget never
// overshoots).
func DefaultBudget() Budget {
	return BudgetForCeiling(DefaultTokenCeiling)
}

// BudgetForCeiling computes a Budget for an explicit token ceiling. It is
// the single source of truth for the partition (I7): NewState and
// LoadSession route any directive/override ceiling through here so no
// call site re-derives a layer's byte share by hand. A non-positive
// ceiling falls back to DefaultTokenCeiling.
func BudgetForCeiling(tokenCeiling int) Budget {
	if tokenCeiling <= 0 {
		tokenCeiling = DefaultTokenCeiling
	}
	total := int(float64(tokenCeiling) * bytesPerTokenConservative)
	liveTurn := total * defaultPctLiveTurn / 100
	memory := total - liveTurn
	return Budget{
		TokenCeiling:          tokenCeiling,
		ResponseReserveTokens: DefaultResponseReserveTokens,
		Total:                 total,
		LayerE:                memory * defaultPctLayerE / 100,
		LayerA1:               memory * defaultPctLayerA1 / 100,
		LayerA2:               memory * defaultPctLayerA2 / 100,
		LayerB:                memory * defaultPctLayerB / 100,
		LayerC:                memory * defaultPctLayerC / 100,
		LiveTurnReserve:       liveTurn,
		BTopK:                 DefaultBTopK,
		PerProjectDigestBytes: DefaultPerProjectDigestBytes,
	}
}

// ---------- Symbol index ----------

// IndexBuildOptions configures RebuildSymbolIndex / CheckSymbolIndex.
// Promoted from index.Options.
type IndexBuildOptions struct {
	// Quiet suppresses progress logging regardless of Logger.
	Quiet bool
	// Logger receives one progress line per significant step. nil →
	// silent.
	Logger func(format string, args ...any)
	// Warner receives recoverable issues (missing meta.json on a
	// referenced project, etc.). Defaults to Logger when nil.
	Warner func(format string, args ...any)
}

// CheckResult summarizes what CheckSymbolIndex found.
type CheckResult struct {
	// Drifts lists derived-file discrepancies. Empty ⇔ all derived
	// files match what RebuildSymbolIndex would produce.
	Drifts []Drift
}

// OK reports whether the check found no drift.
func (r CheckResult) OK() bool { return len(r.Drifts) == 0 }

// Drift is one entry in a CheckResult.
type Drift struct {
	// Path is the absolute path to the derived file.
	Path string
	// Status is one of "stale", "missing", "extra".
	Status string
	// Detail is an optional human-readable explanation.
	Detail string
}

// ---------- Bootstrap ----------

// BootstrapHints feeds ResolveActiveProject.
type BootstrapHints struct {
	// ExplicitProject, if non-empty, short-circuits the waterfall.
	// The resolver tries to interpret it first as a prj_<n> id,
	// then as a ProjectMeta.Name.
	ExplicitProject string
	// CWD is the directory used as the basis for the heuristic
	// waterfall (git-remote match, path match). Callers typically
	// pass os.Getwd(); the field is explicit so tests can pin a
	// fixture path.
	CWD string
}

// BootstrapResult conveys the outcome of the resolve waterfall.
// Exactly one of:
//   - Resolved is non-nil (waterfall succeeded; last-active and meta
//     drift have been persisted).
//   - Step == StepNeedsConfirmation and Candidate is non-nil (caller
//     asks the user about resuming the last-active project).
//   - Step == StepNeedsFallback (caller offers create/switch/none).
type BootstrapResult struct {
	// Step identifies which branch of the waterfall produced the
	// result.
	Step BootstrapStep
	// Resolved is non-nil when the waterfall completed and the
	// active project is determined.
	Resolved *ProjectMeta
	// Candidate is non-nil when Step == StepNeedsConfirmation; the
	// caller prompts the user to resume this project.
	Candidate *ProjectMeta
}

// BootstrapStep identifies which branch of the bootstrap waterfall
// produced a result.
type BootstrapStep int

const (
	// StepUnset is the zero value; should not appear in successful
	// returns.
	StepUnset BootstrapStep = iota
	// StepExplicit indicates --project (or the equivalent hint)
	// matched a known project.
	StepExplicit
	// StepRemoteMatch indicates the CWD's git remote matched a
	// known project.
	StepRemoteMatch
	// StepPathMatch indicates the CWD itself matched a project's
	// current/historical root paths.
	StepPathMatch
	// StepNeedsConfirmation indicates last-active resolved but the
	// caller should confirm before resuming.
	StepNeedsConfirmation
	// StepNeedsFallback indicates the caller must offer
	// create/switch/no-project.
	StepNeedsFallback
)

// String returns a human-readable form of the step (for log lines, not
// user-facing UI).
func (s BootstrapStep) String() string {
	switch s {
	case StepExplicit:
		return "explicit"
	case StepRemoteMatch:
		return "remote-match"
	case StepPathMatch:
		return "path-match"
	case StepNeedsConfirmation:
		return "needs-confirmation"
	case StepNeedsFallback:
		return "needs-fallback"
	default:
		return "unset"
	}
}

// ---------- Init and Verify ----------

// InitOptions configures Init. Promoted from store.InitOptions.
type InitOptions struct {
	// Quiet suppresses logger and Out writes regardless of either.
	Quiet bool
	// Logger receives one log line per scaffold step. nil → silent.
	Logger func(format string, args ...any)
	// Out receives the small subset of init's report that the user must
	// see even with logging turned down — today, the [user] identity
	// step, whose absence disables the Wikimedia-backed tools. nil → no
	// stdout echo, which is what every embedded caller wants.
	Out io.Writer
}

// VerifyReport aggregates the result of a Verify run.
// Errors-or-drift drive non-zero exit codes; warnings alone do not.
type VerifyReport struct {
	// Errors are schema/constraint violations.
	Errors []VerifyFinding
	// Warnings are recoverable issues (missing meta.json, malformed
	// directives) — informational, do not affect exit code.
	Warnings []VerifyFinding
	// Drift carries pass-through descriptions from CheckSymbolIndex;
	// treated as errors for the purpose of the exit code.
	Drift []string
}

// HasErrors reports whether the report contains any error-grade
// issue (schema violation or index drift).
func (r VerifyReport) HasErrors() bool {
	return len(r.Errors) > 0 || len(r.Drift) > 0
}

// VerifyFinding is one schema/constraint violation.
type VerifyFinding struct {
	// Path locates the file or record (e.g. "spine.jsonl[42]" or
	// "projects/prj_3/meta.json").
	Path string
	// Field names the offending field, if any.
	Field string
	// Message is a human-readable explanation.
	Message string
}
