package memops

import (
	"personant/internal/store"
)

// This file holds the supporting types referenced by the MemoryOps
// interface. The data-model types (SpineRecord, ThreadFrontmatter,
// Thread, ProjectMeta, Provider, the symbol/state enums) are aliased
// from internal/store: the port is about *operations*, the data model
// is shared between application and substrate. A future refactor could
// move the model types into memops outright; today that is larger churn
// than this step justifies.
//
// Sentinel errors are aliased so callers can use errors.Is against the
// memops package without needing to import store for the sentinel.

// ---------- Data-model aliases ----------

type (
	// SpineRecord is one line of the canonical spine (spec §2.2).
	SpineRecord = store.SpineRecord
	// ThreadFrontmatter is the YAML frontmatter of a thread file
	// (spec §2.3).
	ThreadFrontmatter = store.ThreadFrontmatter
	// Thread is the in-memory shape of a thread file: frontmatter +
	// markdown body (spec §2.3).
	Thread = store.Thread
	// HistorySymbol is one entry in a thread's frontmatter
	// history_symbols list (spec §2.3).
	HistorySymbol = store.HistorySymbol
	// ProjectMeta is the per-project metadata record
	// (projects/prj_<n>/meta.json, spec §2.5.1).
	ProjectMeta = store.ProjectMeta
	// ProjectDigest is the derived per-project digest content
	// (projects/prj_<n>/digest.json, spec §2.5.2).
	ProjectDigest = store.ProjectDigest
	// Provider is one LLM provider's connectivity record from
	// providers.toml (spec §8.2.1).
	Provider = store.Provider
	// ThreadState is the lifecycle state of a thread (spec §2.2.1).
	ThreadState = store.ThreadState
	// SymbolSource is the provenance of a symbol emission
	// (spec §2.7.3).
	SymbolSource = store.SymbolSource
	// SymbolCategory is the classification tier of a symbol
	// (spec §2.7.1).
	SymbolCategory = store.SymbolCategory
)

// ---------- Sentinel error aliases ----------
//
// Aliased so callers can errors.Is against memops.<Err> without taking
// a direct internal/store dependency. The adapter returns the same
// underlying error values.

var (
	// ErrThreadNotFound is returned when an operation targets a
	// thread ID that has no spine record.
	ErrThreadNotFound = store.ErrThreadNotFound
	// ErrDuplicateThreadID is returned by CreateThread when its
	// rec.ID already exists in the spine.
	ErrDuplicateThreadID = store.ErrDuplicateThreadID
	// ErrThreadFileNotFound is returned by LoadThread when the
	// canonical thread file is absent. Callers use it to
	// distinguish "fresh thread about to be created" from a parse
	// failure.
	ErrThreadFileNotFound = store.ErrThreadFileNotFound
	// ErrProjectNotFound is returned by LoadProject when a
	// non-default project's meta.json is absent.
	ErrProjectNotFound = store.ErrProjectNotFound
)

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
// Mirrors internal/turn.Delta but carries the Retention field for
// transient-data lifecycle awareness. (turn.Delta will become an alias
// for memops.Delta when callers migrate in step A.2 / the transient-
// data work; defining the canonical form here keeps the port
// self-contained.)
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

// WorksetOutput is the rendered per-layer content for one turn,
// ready to be assembled into a system prompt by internal/prompt.
// Promoted from prompt.SystemPromptParams; the application layer
// passes the output straight through to prompt.BuildSystemPrompt.
//
// Defined here (rather than re-exporting prompt.SystemPromptParams)
// so the prompt package's import graph remains independent of the
// adapter side.
type WorksetOutput struct {
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

// Budget is the per-layer byte allocation for the working set
// (spec §3.1, parameters from §2.6.1). Promoted from workset.Budget;
// field semantics are identical.
type Budget struct {
	// Total is the overall context byte budget.
	Total int
	// LayerE is directives + conventions (§2.6.1: E=8% of Total).
	LayerE int
	// LayerA1 is the current project's spine display
	// (§2.6.1: A1=8%).
	LayerA1 int
	// LayerA2 is cross-project digests
	// (§2.6.1: variable; bounded per-project).
	LayerA2 int
	// LayerB is active thread bodies (§2.6.1: B=50%).
	LayerB int
	// LayerC is dormant thread summaries (§2.6.1: C=15%).
	LayerC int
	// CurrentTurn is the user-input + model-response budget
	// (§2.6.1: current_turn=15%).
	CurrentTurn int
	// BTopK caps the number of threads in Layer B
	// (§2.6.1: layer.b-top-k; default 3).
	BTopK int
	// PerProjectDigestBytes caps a single project's A2 line
	// (§2.6.1: cross-project.digest-per-project-bytes; default 150).
	PerProjectDigestBytes int
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

// CheckResult summarizes what CheckSymbolIndex found. Promoted from
// index.CheckResult.
type CheckResult struct {
	// Drifts lists derived-file discrepancies. Empty ⇔ all derived
	// files match what RebuildSymbolIndex would produce.
	Drifts []Drift
}

// OK reports whether the check found no drift.
func (r CheckResult) OK() bool { return len(r.Drifts) == 0 }

// Drift is one entry in a CheckResult. Promoted from index.Drift.
type Drift struct {
	// Path is the absolute path to the derived file.
	Path string
	// Status is one of "stale", "missing", "extra".
	Status string
	// Detail is an optional human-readable explanation.
	Detail string
}

// ---------- Bootstrap ----------

// BootstrapHints feeds ResolveActiveProject. Promoted from
// store.BootstrapOptions.
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
// Promoted from store.BootstrapResult. Exactly one of:
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
// produced a result. Promoted from store.BootstrapStep.
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

// ---------- Init and Verify ----------

// InitOptions configures Init. Promoted from store.InitOptions.
type InitOptions struct {
	// Quiet suppresses logger calls regardless of Logger.
	Quiet bool
	// Logger receives one log line per scaffold step. nil → silent.
	Logger func(format string, args ...any)
}

// VerifyReport aggregates the result of a Verify run. Promoted from
// verify.Report. Errors-or-drift drive non-zero exit codes; warnings
// alone do not.
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

// VerifyFinding is one schema/constraint violation. Promoted from
// verify.Finding.
type VerifyFinding struct {
	// Path locates the file or record (e.g. "spine.jsonl[42]" or
	// "projects/prj_3/meta.json").
	Path string
	// Field names the offending field, if any.
	Field string
	// Message is a human-readable explanation.
	Message string
}
