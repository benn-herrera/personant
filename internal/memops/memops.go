// Package memops defines the conceptual memory-operations port that the
// personant application layer uses to talk to its substrate.
//
// # Port-and-adapter boundary
//
// Today the application layer (internal/turn, internal/recall,
// internal/workset, internal/chat) imports internal/store directly. That
// entangles application logic with concrete file-format, path layout, and
// I/O-strategy choices (JSONL spine, YAML frontmatter, atomic temp+rename,
// daily-rotated log files). It also entangles every test that exercises
// those packages with the same concrete substrate.
//
// MemoryOps is the port between the two halves. The application layer
// composes the conceptual operations defined here; one or more adapter
// implementations (the first being internal/memops/fileadapter, planned
// for step A.2) translate them into the concrete substrate ops currently
// living in internal/store, internal/eventlog, internal/index, etc.
//
// # Design rationale and the larger queued workstream
//
// This is Phase A.1 of a two-stage abstraction: A.1 defines the port,
// A.2 lifts the existing store/eventlog/index/verify call sites into a
// concrete adapter and migrates callers. The design rationale and the
// connection to the transient-data lifecycle and recall-fidelity
// workstreams live in two memory entries:
//
//   - project_personant_memory_ops_abstraction_queued.md
//   - project_personant_transient_data_lifecycle.md
//
// In particular: every delta-emit operation carries a RetentionClass so
// the interface is forward-compatible with the transient-data lifecycle
// work. The current adapter ignores the field, but new adapters and the
// §3.0 chain integration will use it without a signature change.
//
// # What is on the port and what is not
//
// Catalogued: thread CRUD, project CRUD, recall queries, symbol-index
// rebuild/check, working-set composition, context-modification event
// emission, bootstrap and verification.
//
// Deliberately not on the port:
//
//   - The §3.0 hook chain itself (application-layer control flow that
//     composes primitives from the port).
//   - store.Normalize / store.DominantSource (pure functions that do
//     not depend on substrate state).
//   - PersonantPaths in any signature (paths are the adapter's secret;
//     application code does not see them).
//   - File-format details — no method mentions JSONL, YAML, or
//     temp+rename.
//
// # Data-model types
//
// SpineRecord, ThreadFrontmatter, Thread, ProjectMeta, Provider, the
// thread/symbol enums — these continue to live in internal/store and are
// referenced through type aliases in memops_types.go. A future refactor
// could move them into memops; that is larger churn than this step
// justifies.
//
// # Context.Context
//
// Every method takes context.Context first. The current file-backed
// adapter cannot honor cancellation for most ops, but the contract
// requires it so future adapters (network-backed, SQLite-transactional,
// in-memory simulation) can. Callers must pass a real context — passing
// context.TODO() is acceptable during migration but should not survive
// into production code paths.
package memops

import (
	"context"
)

// MemoryOps is the port that the application layer uses to talk to its
// substrate. One adapter — internal/memops/fileadapter, planned for
// step A.2 — wraps the current internal/store package and its
// derived-index helpers. Future adapters can swap in without rewriting
// callers.
//
// Methods are grouped below by concern (threads, projects, symbol
// index, working set, event log, bootstrap, configuration). Each method
// names the concrete operation it folds in from the current codebase so
// the A.2 implementation is a one-to-one mapping.
type MemoryOps interface {

	// ---------- Thread operations ----------

	// CreateThread atomically creates a new thread: it appends a new
	// record to the spine and writes the canonical thread file. Folds
	// in the createNewThread path currently open-coded in
	// internal/turn/turn.go (store.SaveThread + store.AppendSpineRecord).
	//
	// `rec.ID` must already be allocated via NextThreadID; the adapter
	// returns ErrDuplicateThreadID (sentinel via errors.Is) if it
	// collides. `body` is the initial markdown body for the thread
	// file; the frontmatter is derived from `rec` plus any
	// per-thread history that the caller assembles before calling.
	//
	// On any partial failure the adapter is expected to log
	// substrate-side state and return a wrapped error; reconciliation
	// on the next engagement (or via Verify) is the recovery path.
	CreateThread(ctx context.Context, rec SpineRecord, fm ThreadFrontmatter, body string) error

	// EngageThread atomically updates an existing thread: it overwrites
	// the spine record and rewrites the thread file with the supplied
	// frontmatter and body. Folds in the updateExistingThread path
	// currently open-coded in internal/turn/turn.go
	// (store.SaveThread + store.UpdateSpineRecord).
	//
	// Returns ErrThreadNotFound (sentinel) when the spine has no
	// record matching `rec.ID`.
	EngageThread(ctx context.Context, rec SpineRecord, fm ThreadFrontmatter, body string) error

	// LoadThread returns the full thread (frontmatter + body) for the
	// given ID. Folds in store.LoadThread. ErrThreadFileNotFound is
	// returned (via errors.Is) when no on-disk file exists for the ID;
	// callers distinguish "fresh thread about to be created" from a
	// parse failure.
	LoadThread(ctx context.Context, threadID string) (Thread, error)

	// FindThread looks up the spine record for threadID. The second
	// return is false when no record exists. Folds in
	// store.FindSpineRecord. An I/O error on the underlying spine
	// read is the only failure mode.
	FindThread(ctx context.Context, threadID string) (SpineRecord, bool, error)

	// ListThreads returns spine records matching the filter, sorted by
	// thread ID ascending. An empty (zero-value) filter returns the
	// full spine. Folds in store.ReadSpine and
	// store.SpineRecordsByProject.
	//
	// ALT: consider exposing a streaming variant for very large spines
	// — revisit on first adapter implementation if six-month-sim spine
	// length pushes past a few thousand records.
	ListThreads(ctx context.Context, filter ThreadFilter) ([]SpineRecord, error)

	// NextThreadID returns the next available thr_<n> id, scanning the
	// full spine so the new id never collides with a thread in a
	// sibling project. Folds in store.NextThreadID (which today takes
	// pre-read records; the port pushes the read into the adapter).
	NextThreadID(ctx context.Context) (string, error)

	// ---------- Project operations ----------

	// CreateProject persists a new ProjectMeta. The caller is
	// responsible for allocating meta.ID (typically via store-side
	// NextProjectID once that helper is also lifted onto the port in
	// a future step; v0.1 callers go through ResolveActiveProject and
	// hand-rolled init paths). Folds in store.SaveProjectMeta in the
	// not-yet-existed case.
	//
	// ALT: a unified Upsert may be cleaner; v0.1 splits Create vs.
	// Save to keep the distinction between first-write and
	// drift-update visible at call sites. Revisit when project
	// lifecycle commands land.
	CreateProject(ctx context.Context, meta ProjectMeta) error

	// LoadProject returns the meta for the given project ID. Folds in
	// store.LoadProjectMeta, including the synthetic prj_default
	// fallback. ErrProjectNotFound is returned (via errors.Is) for
	// any non-default project whose meta.json is absent.
	LoadProject(ctx context.Context, projectID string) (ProjectMeta, error)

	// SaveProject overwrites the meta for an existing project. Folds
	// in store.SaveProjectMeta on the drift-update path.
	SaveProject(ctx context.Context, meta ProjectMeta) error

	// ListProjects returns every known project, sorted by ID. Folds
	// in store.ListProjects.
	ListProjects(ctx context.Context) ([]ProjectMeta, error)

	// FindProjectByRemote returns the project whose remote_urls or
	// historical_remote_urls contains the given (normalized) URL.
	// The adapter normalizes both sides; callers can pass a raw URL.
	// Folds in store.FindProjectByRemote.
	FindProjectByRemote(ctx context.Context, remoteURL string) (ProjectMeta, bool, error)

	// FindProjectByPath returns the project whose CurrentRootPath or
	// HistoricalRootPaths contains absPath (after filepath.Clean).
	// Folds in store.FindProjectByPath.
	FindProjectByPath(ctx context.Context, absPath string) (ProjectMeta, bool, error)

	// SetLastActiveProject records projectID as the most-recently used
	// project for next-launch bootstrap. Folds in store.WriteLastActive.
	SetLastActiveProject(ctx context.Context, projectID string) error

	// GetLastActiveProject returns the recorded last-active project
	// ID, or empty string when none has been recorded (fresh install).
	// A malformed marker yields ("", err). Folds in
	// store.ReadLastActive.
	GetLastActiveProject(ctx context.Context) (string, error)

	// ---------- Symbol index operations ----------

	// ProposeRecall runs the §3.4 layer-1 symbolic Jaccard pre-filter
	// over the spine and per-thread frontmatter symbol sets. Folds in
	// recall.Propose (which is itself a thin wrapper around
	// recall.ProposeFromIndex once spine + frontmatter are loaded).
	//
	// `query` is the set of query symbols, already normalized by the
	// caller. Empty query → empty result, no error.
	ProposeRecall(ctx context.Context, query []string, opts RecallOptions) ([]RecallCandidate, error)

	// RebuildSymbolIndex regenerates every derived index file from
	// the canonical spine and per-thread frontmatter (symbols.jsonl
	// plus per-project digest.json). Folds in index.Rebuild.
	//
	// ALT: callers may want a fine-grained "rebuild only this
	// project's digest" variant; v0.1 has no such caller, so the
	// full-rebuild form is the only entry point on the port.
	RebuildSymbolIndex(ctx context.Context, opts IndexBuildOptions) error

	// CheckSymbolIndex computes what RebuildSymbolIndex would write
	// and compares it to what is currently on disk, reporting any
	// drift. Never mutates. Folds in index.Check.
	CheckSymbolIndex(ctx context.Context, opts IndexBuildOptions) (CheckResult, error)

	// ---------- Working set composition ----------

	// ComposeWorkingSet builds the layer-by-layer working-set
	// content the prompt assembler needs for one turn. Folds in
	// workset.Compose. The caller supplies the per-session inputs
	// (active project, Layer B/C membership, byte budget); the
	// adapter consults its substrate for everything else (spine,
	// directives, cross-project digests, thread bodies).
	//
	// Layer-level failures are not returned as errors; the layer
	// renders empty and the failure is logged via the adapter's
	// configured logger. The only hard error is a missing
	// ActiveProject.ID — without it the spine cannot be projected
	// and the prompt is malformed.
	ComposeWorkingSet(ctx context.Context, in WorksetInput) (WorksetOutput, error)

	// ---------- Context-modification event logging ----------

	// EmitDelta records one context-modification event on the
	// substrate side: increments any per-source counters the adapter
	// maintains and writes the §2.8 event-log line. The §3.0 chain
	// itself stays in internal/turn (application-layer control
	// flow); this method is the substrate side of that chain's
	// step 5 ("logging") plus the future transient-data symbol
	// staging hook.
	//
	// The Retention field on Delta is forward-compatibility for the
	// transient-data lifecycle work. The current file adapter ignores
	// it — every delta is treated as persistent. A future adapter
	// (or a future file-adapter pass) will use it to drive the
	// staging-buffer / cross-reference / log-compaction mechanics.
	EmitDelta(ctx context.Context, delta Delta) error

	// Log writes a general-purpose event-log line. Folds in
	// eventlog.Log. `category` and `action` are the §2.8 vocabulary;
	// `details` is free-form (newlines and tabs are sanitized by the
	// adapter).
	Log(ctx context.Context, category, action, details string) error

	// ---------- Bootstrap and verification ----------

	// Init scaffolds the substrate for first-run use. Idempotent: a
	// second call is safe when the substrate is already initialized.
	// Folds in store.Init.
	Init(ctx context.Context, opts InitOptions) error

	// Verify performs a read-only structural validation pass over the
	// canonical state and returns a report. Errors-or-drift in the
	// report drive non-zero exit codes; the returned error itself
	// signals an I/O or programming failure that prevented validation
	// from completing. Folds in verify.Verify.
	Verify(ctx context.Context) (VerifyReport, error)

	// ResolveActiveProject runs the spec §4.5.7 bootstrap waterfall
	// and returns its outcome. Folds in store.ResolveActiveProject.
	// On a resolved branch the adapter persists last-active and any
	// project-meta drift updates; on the NeedsConfirmation /
	// NeedsFallback branches the adapter writes nothing — the caller
	// confirms via subsequent SaveProject / SetLastActiveProject
	// calls.
	ResolveActiveProject(ctx context.Context, hints BootstrapHints) (BootstrapResult, error)

	// ---------- Configuration ----------

	// LoadProviders reads the LLM provider configuration table from
	// the substrate (today, providers.toml). Folds in
	// store.LoadProviders. A nonexistent or empty config yields an
	// empty map and no error — `personant init` writes a
	// template-only file and a fresh home may not have written one
	// yet at all.
	LoadProviders(ctx context.Context) (map[string]Provider, error)
}
