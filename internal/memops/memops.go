// Package memops defines the conceptual memory-operations port that the
// personant application layer uses to talk to its substrate.
//
// # Port-and-adapter boundary
//
// The application layer (internal/turn, internal/recall, internal/workset,
// internal/chat) depends on MemoryOps, not on any concrete substrate
// package. One adapter ships today — internal/memops/fileadapter, which
// wraps internal/store and its derived-index helpers; future adapters can
// swap in without rewriting callers or tests.
//
// MemoryOps is the port between the two halves. Application code composes
// the conceptual operations defined here; the adapter translates them
// into substrate-specific ops (JSONL, markdown, frontmatter, atomic
// temp+rename, go-git — all the file-substrate details the application
// must not see).
//
// # Port abstraction policy
//
// The port is substrate-agnostic by design with one file-based
// implementation today. ARCHITECTURE.md's "Port abstraction policy"
// section is the source of truth — read it for the four design rules
// governing what does and doesn't belong at this layer
// (substrate-agnostic naming by default, no ceremony for hypothetical
// substrates, mutable API surface, explicit case-by-case concessions).
//
// In short: method names and parameter types on MemoryOps describe
// abstract operations (load a thread's metadata, list spine records,
// engage a thread). Names tied to a specific storage format — paths,
// file extensions, on-disk layout — stay on the adapter side.
// Concessions to substrate-specific terminology at the port surface are
// explicit exceptions, not implicit drift.
//
// # Design rationale and the larger queued workstream
//
// The design rationale and the connection to the transient-data
// lifecycle and recall-fidelity workstreams live in two memory entries:
//
//   - project_personant_memory_ops_abstraction_queued.md
//   - project_personant_transient_data_lifecycle.md
//
// In particular: every delta-emit operation carries a RetentionClass so
// the interface is forward-compatible with the transient-data lifecycle
// work. The current adapter ignores the field; future adapters and the
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
//   - Normalize / DominantSource (pure functions that do not depend on
//     substrate state — they live in package memops alongside the domain
//     model, not on the MemoryOps port).
//   - PersonantPaths in any signature (paths are the adapter's secret;
//     application code does not see them).
//
// # Data-model types
//
// SpineRecord, ThreadMeta, Thread, ProjectMeta, Provider, the
// thread/symbol enums, the sentinel errors — these are real definitions
// in memops_model.go. The port owns its domain types outright;
// internal/store and the adapters depend on memops for them, so the
// dependency arrow runs substrate → port.
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

	// CreateThread atomically creates a new thread: the adapter creates
	// the thread directory, writes thread.md from w.Meta, appends
	// w.TurnExcerpt as turn w.Meta.TurnCount, and appends the
	// spine record. Folds in the createNewThread path currently
	// open-coded in internal/turn/turn.go.
	//
	// `w.Spine.ID` must already be allocated via NextThreadID; the
	// adapter returns ErrDuplicateThreadID (sentinel via errors.Is) if
	// it collides.
	//
	// Atomicity is the adapter's responsibility: the adapter handles
	// write ordering (file then spine, or spine then file — its
	// choice), retry semantics if one write fails, and any
	// substrate-internal transaction wrapping (a future SQLite adapter
	// would wrap both in BEGIN/COMMIT; the file adapter handles partial
	// failure via reconciliation on next engagement). Application code
	// passes a fully-built ThreadWrite and trusts the adapter.
	CreateThread(ctx context.Context, w ThreadWrite) error

	// EngageThread atomically updates an existing thread: the adapter
	// rewrites thread.md from w.Meta, appends w.TurnExcerpt as
	// turn w.Meta.TurnCount (FIFO-windowed in the turns/
	// directory), and updates the spine record. An empty w.TurnExcerpt
	// is a meta-only update (the closure path). Folds in the
	// updateExistingThread path currently open-coded in
	// internal/turn/turn.go.
	//
	// Adapter owns the missing-thread-dir recovery. When `w.Spine.ID`
	// exists in the spine but the thread directory is missing (drift
	// state), the adapter materializes the directory from w.Meta
	// rather than failing. The application never sees this recovery path
	// — it doesn't synthesize meta from spine, doesn't deal with
	// ErrThreadFileNotFound on engagement.
	//
	// Returns ErrThreadNotFound (sentinel) only when the spine itself
	// has no record matching `w.Spine.ID`.
	EngageThread(ctx context.Context, w ThreadWrite) error

	// LoadThread returns the full thread (metadata + recency-windowed
	// body) for the given ID. Folds in store.LoadThread.
	// ErrThreadFileNotFound is returned (via errors.Is) when no on-disk
	// thread exists for the ID; callers distinguish "fresh thread about
	// to be created" from a parse failure.
	//
	// LoadThread assembles the body from every retained turn-excerpt
	// file. For callers that only need metadata, LoadThreadMeta
	// is the cheaper choice — it never touches the turns/ directory.
	LoadThread(ctx context.Context, threadID string) (Thread, error)

	// LoadThreadMeta returns only the thread's metadata block — it reads
	// the small bounded thread metadata slot and skips the turn-excerpt
	// directory entirely. This is the workhorse for the engagement-update
	// path, which rewrites metadata and appends one turn excerpt without
	// ever loading the prior body. ErrThreadFileNotFound semantics match
	// LoadThread.
	LoadThreadMeta(ctx context.Context, threadID string) (ThreadMeta, error)

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

	// RecordRecallFire increments the RecallFires counter for threadID —
	// a recall match that the user accepted into the working set (spec
	// §2.2). Updates the spine record and the thread's metadata block
	// together so the two stay in sync. Returns ErrThreadNotFound if no
	// spine record exists for threadID.
	RecordRecallFire(ctx context.Context, threadID string) error

	// ArchiveThread removes a retired thread from the active spine. v0.1
	// implements this as a deletion STUB — the thread's spine record and
	// file are deleted outright. Real §3.8 git-based archival with a
	// recovery path is v0.2; this port method is the stable seam across
	// both. Returns ErrThreadNotFound if no spine record exists.
	ArchiveThread(ctx context.Context, threadID string) error

	// ---------- Project operations ----------

	// CreateProject persists a new ProjectMeta. The caller is
	// responsible for allocating meta.ID — typically via NextProjectID
	// on the port. Folds in store.SaveProjectMeta in the not-yet-existed
	// case.
	//
	// ALT: a unified Upsert may be cleaner; v0.1 splits Create vs.
	// Save to keep the distinction between first-write and
	// drift-update visible at call sites. Revisit when project
	// lifecycle commands land.
	CreateProject(ctx context.Context, meta ProjectMeta) error

	// NextProjectID returns the next available prj_<n> id, scanning the
	// known project set so a new id never collides with an existing
	// project. Folds in store.NextProjectID (which today takes a
	// pre-read meta slice; the port pushes the read into the adapter).
	NextProjectID(ctx context.Context) (string, error)

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
	// over the spine and per-thread metadata symbol sets. Folds in
	// recall.Propose (which is itself a thin wrapper around
	// recall.ProposeFromIndex once spine + thread metadata are loaded).
	//
	// `query` is the set of query symbols, already normalized by the
	// caller. Empty query → empty result, no error.
	ProposeRecall(ctx context.Context, query []string, opts RecallOptions) ([]RecallCandidate, error)

	// RegenerateDerivedState regenerates every derived artifact from
	// canonical sources. For the file adapter this rebuilds
	// symbols.jsonl plus per-project digest.json. For a SQL-backed
	// adapter it might refresh materialized views; for a service-backed
	// adapter it might POST to a /derived/rebuild endpoint. The
	// application doesn't distinguish — derived state is substrate-
	// internal and the adapter decides how to materialize it.
	// Folds in index.Rebuild.
	//
	// ALT: callers may want a fine-grained "rebuild only this
	// project's digest" variant; v0.1 has no such caller, so the
	// full-rebuild form is the only entry point on the port.
	RegenerateDerivedState(ctx context.Context, opts IndexBuildOptions) error

	// CheckDerivedState computes what RegenerateDerivedState would
	// write and compares it to what is currently materialized, reporting
	// any drift. Never mutates. Folds in index.Check.
	CheckDerivedState(ctx context.Context, opts IndexBuildOptions) (CheckResult, error)

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
	ComposeWorkingSet(ctx context.Context, in WorksetInput) (WorksetLayers, error)

	// ---------- Session working set ----------

	// SaveWorkingSet persists the session working-set membership — the
	// Layer B (activeThreads) and Layer C (dormantThreads) ordered ID
	// lists — so a clean shutdown→relaunch cycle resumes the working set
	// instead of cold-starting it empty. Both lists are most-recently-
	// engaged-first; the adapter persists them verbatim, order included.
	//
	// Only these two lists are persisted. Session-volatile state
	// (turn counter, coalesce/staging buffers, closure-defer grace) is
	// deliberately not persisted — it correctly resets on restart. The
	// active project is persisted separately via SetLastActiveProject.
	//
	// The artifact is volatile session state, rewritten every turn; the
	// adapter keeps it out of any substrate version control so per-turn
	// LRU churn does not dirty the substrate's git tree.
	SaveWorkingSet(ctx context.Context, activeThreads, dormantThreads []string) error

	// LoadWorkingSet returns the persisted session working-set membership
	// — the Layer B (activeThreads) and Layer C (dormantThreads) ordered
	// ID lists, in most-recently-engaged-first order.
	//
	// A missing artifact returns (nil, nil, nil): the fresh-launch state,
	// not an error — mirroring how GetLastActiveProject treats an absent
	// last-active marker. The error return is reserved for a present but
	// unreadable or malformed artifact.
	LoadWorkingSet(ctx context.Context) (activeThreads, dormantThreads []string, err error)

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

	// ---------- §3.9 tracked-file content ----------

	// RecordFileWrite records a content write for a tracked file in the
	// per-thread §3.9 file store: it loads the thread's tracked-file
	// sidecar, appends `content` as the path's new version, and saves it.
	// Folds in store.LoadThreadFiles + ThreadFiles.RecordWrite +
	// store.SaveThreadFiles.
	//
	// The write is idempotent on unchanged content: a re-write whose
	// `content` equals the path's current version grows neither the
	// version chain nor un-commits a committed file. The durable home of
	// file content is this store, not the delta event log — the event
	// log records only the event line.
	RecordFileWrite(ctx context.Context, threadID, path, content string) error

	// RecordFileCommit records a git-commit pointer for a tracked file in
	// the per-thread §3.9 file store: it loads the sidecar, sets the
	// commit hash + timestamp + turn number on the path's entry, and saves
	// it. The adapter stamps the commit timestamp itself (clock.Timeline())
	// — the port carries no timestamp; `committedTurn` is the turn at which
	// the commit happened and feeds the turn-count side of the §3.9
	// git-minimization retention window. Folds in store.LoadThreadFiles +
	// ThreadFiles.RecordCommit + store.SaveThreadFiles.
	//
	// Returns an error when `path` is not tracked (a file the store has
	// never seen a write for cannot be committed); callers treat that as
	// non-fatal.
	RecordFileCommit(ctx context.Context, threadID, path, hash string, committedTurn int) error

	// AgeFileChains applies the §3.9 git-minimization policy to threadID's
	// tracked-file store: a committed file's reverse-delta chain — which is
	// pure duplication of what the project git repo holds at the commit
	// hash — is dropped once a retention window has elapsed, leaving only
	// the hash as a recovery pointer. Folds in store.LoadThreadFiles +
	// ThreadFiles.AgeOut + store.SaveThreadFiles.
	//
	// A thread with no tracked-file sidecar is a no-op: (nil, 0, nil) with
	// no sidecar written. Returns the sorted aged paths and total bytes
	// freed (demand-sizing forensic data, also written to the event log).
	AgeFileChains(ctx context.Context, threadID string, currentTurn int) (agedPaths []string, bytesFreed int, err error)

	// ---------- Bootstrap and verification ----------

	// Init scaffolds the substrate for first-run use. Idempotent: a
	// second call is safe when the substrate is already initialized.
	// Folds in store.Init.
	//
	// Substrate-internal setup is the adapter's secret. The file
	// adapter creates the directory layout, runs `git init` on its
	// home tree, and writes seed files. A SQLite adapter would create
	// schema; a network adapter might register a tenant. The port
	// surface doesn't expose any of that — callers just ask for the
	// substrate to be ready.
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
	// empty map, no faults, and no error — `personant init` writes a
	// template-only file and a fresh home may not have written one
	// yet at all.
	//
	// A provider that parsed but whose apiKeyFile is unreadable is
	// omitted from the map and reported as a ProviderFault; the rest
	// of the pool still loads. The error return is reserved for
	// file-level failures (providers.toml unreadable or malformed).
	LoadProviders(ctx context.Context) (map[string]Provider, []ProviderFault, error)

	// LoadConfig reads config.toml — the chat/embedding choices that
	// draw from the providers.toml pool. Folds in store.LoadConfig. A
	// nonexistent file yields a zero Config and no error.
	LoadConfig(ctx context.Context) (Config, error)
}
