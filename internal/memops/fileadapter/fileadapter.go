// Package fileadapter implements memops.MemoryOps against personant's
// canonical file substrate (JSONL + markdown + YAML frontmatter + TOML
// providers + git on ~/.personant/). Delegates to internal/store,
// internal/index, internal/recall/scoring, internal/workset,
// internal/verify, and internal/eventlog.
//
// This is the v0.1 adapter. Future substrate adapters (SQLite,
// network-backed, in-memory simulation) implement memops.MemoryOps from
// scratch against their own storage; nothing here is intended to be
// reusable by them.
//
// # Caller migration is out of scope here (Phase A.2)
//
// This package is added alongside the existing application packages
// (internal/turn, internal/recall/measure, internal/workset,
// internal/chat) that still call internal/store directly. Caller
// migration happens in A.3+.
package fileadapter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"personant/internal/clock"
	"personant/internal/eventlog"
	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/recall/scoring"
	"personant/internal/recovery"
	"personant/internal/store"
	"personant/internal/verify"
)

// FileAdapter is the file-backed implementation of memops.MemoryOps.
//
// Construct via NewFileAdapter. The zero value is not useful — paths
// must be supplied.
type FileAdapter struct {
	paths store.PersonantPaths

	// Logger is a supplementary sink for non-fatal warnings (workset
	// layer-render failures, log-compaction misses, etc.). nil-safe.
	//
	// The adapter is responsible for emitting its substrate's canonical
	// events to the event log regardless of whether Logger is set; this
	// hook is for callers that want a live in-process tee (e.g. a CLI
	// that prints warnings to stderr while a session is running).
	Logger func(format string, args ...any)

	// fmCache is the write-through parsed-frontmatter cache that backs the
	// ProposeRecall hot path. Every thread.md frontmatter write site in
	// this adapter keeps it coherent (write-through on create/engage/
	// recall-fire, invalidate on archive). See fmcache.go for the
	// sole-mutator correctness invariant.
	fmCache *frontmatterCache

	// turnScope accumulates the repo-relative paths of every TRACKED
	// canonical file this adapter has written since the last successful
	// CommitTurn — the turn's known write set (#94 R3-addendum item 3).
	// CommitTurn stages exactly this set (autogit.AddPaths, no whole-tree
	// walk); the write methods record into it at the call site of each
	// write, so the set is derived from what actually happened, never
	// guessed. Entries persist across an aborted turn's ReleaseTurn (no
	// commit ran, so the dirt is still pending) and are dropped only when
	// a commit stages them — CommitTurn per-set, Checkpoint/ArchiveThreads
	// wholesale (their Add(".") sweeps absorb everything). Guarded by
	// scopeMu: the adapter itself is effectively single-writer, but Log
	// can be reached from cleanup paths on other goroutines.
	scopeMu   sync.Mutex
	turnScope map[string]struct{}

	// commitsSinceGCCheck throttles the count-triggered gc's loose-object
	// readdir to every gcCheckEveryCommits per-turn commits (see MaybeGC
	// in fileadapter_recovery.go). Single-writer like the turn path; no
	// lock needed.
	commitsSinceGCCheck int
}

// touchTurnScope records repo-relative tracked paths written by the
// in-flight turn. Call AFTER a successful write — a recorded-but-never-
// materialized path would force AddPaths through its slow fallback.
func (a *FileAdapter) touchTurnScope(rels ...string) {
	a.scopeMu.Lock()
	defer a.scopeMu.Unlock()
	if a.turnScope == nil {
		a.turnScope = make(map[string]struct{}, 8)
	}
	for _, rel := range rels {
		a.turnScope[rel] = struct{}{}
	}
}

// snapshotTurnScope returns the current write set, sorted.
func (a *FileAdapter) snapshotTurnScope() []string {
	a.scopeMu.Lock()
	defer a.scopeMu.Unlock()
	rels := make([]string, 0, len(a.turnScope))
	for rel := range a.turnScope {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	return rels
}

// dropFromTurnScope removes staged entries after a commit covered them.
func (a *FileAdapter) dropFromTurnScope(rels []string) {
	a.scopeMu.Lock()
	defer a.scopeMu.Unlock()
	for _, rel := range rels {
		delete(a.turnScope, rel)
	}
}

// resetTurnScope empties the write set — for callers whose Add(".")
// full sweep just absorbed every pending path (Checkpoint, archival).
func (a *FileAdapter) resetTurnScope() {
	a.scopeMu.Lock()
	defer a.scopeMu.Unlock()
	a.turnScope = nil
}

// NewFileAdapter constructs an adapter rooted at paths.Home. Caller
// supplies the resolved paths; the adapter does not call
// store.ResolvePaths or store.PathsForHome itself so that test code can
// pin a t.TempDir() and CLI code can honor --home flags without the
// adapter being involved in the resolution.
func NewFileAdapter(paths store.PersonantPaths) *FileAdapter {
	return &FileAdapter{paths: paths, fmCache: newFrontmatterCache()}
}

// Compile-time assertion that FileAdapter satisfies the port.
var _ memops.MemoryOps = (*FileAdapter)(nil)

// ---------- Thread operations ----------

// CreateThread atomically materializes a new thread: writes thread.md
// from the frontmatter, appends the first turn excerpt, then appends the
// spine record. Pre-checks for a duplicate ID so a duplicate does not
// produce a stray thread directory. AppendSpineRecord also enforces
// uniqueness as belt-and-braces; the pre-check is the visible guard.
func (a *FileAdapter) CreateThread(ctx context.Context, w memops.ThreadWrite) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, found, err := store.FindSpineRecord(a.paths, w.Spine.ID); err != nil {
		return fmt.Errorf("fileadapter: check duplicate: %w", err)
	} else if found {
		return fmt.Errorf("fileadapter: create thread %s: %w", w.Spine.ID, memops.ErrDuplicateThreadID)
	}
	if err := a.writeThread(w); err != nil {
		return fmt.Errorf("fileadapter: create thread: %w", err)
	}
	a.fmCache.Put(w.Meta) // write-through: thread.md just written
	if err := store.AppendSpineRecord(a.paths, w.Spine); err != nil {
		return fmt.Errorf("fileadapter: append spine: %w", err)
	}
	a.touchTurnScope(store.SpineRel())
	return nil
}

// EngageThread rewrites thread.md and appends one turn excerpt, then
// updates the spine record. Owns the missing-thread-dir recovery: if the
// spine record exists but the thread directory is absent (drift state),
// store.SaveThreadFrontmatter recreates the directory — no special case
// needed.
func (a *FileAdapter) EngageThread(ctx context.Context, w memops.ThreadWrite) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, found, err := store.FindSpineRecord(a.paths, w.Spine.ID); err != nil {
		return fmt.Errorf("fileadapter: find spine: %w", err)
	} else if !found {
		return fmt.Errorf("fileadapter: engage thread %s: %w", w.Spine.ID, memops.ErrThreadNotFound)
	}
	if err := a.writeThread(w); err != nil {
		return fmt.Errorf("fileadapter: engage thread: %w", err)
	}
	a.fmCache.Put(w.Meta) // write-through: thread.md just rewritten
	if err := store.UpdateSpineRecord(a.paths, w.Spine); err != nil {
		return fmt.Errorf("fileadapter: update spine: %w", err)
	}
	a.touchTurnScope(store.SpineRel())
	return nil
}

// writeThread persists a ThreadWrite to the substrate: it rewrites the
// bounded thread.md frontmatter file, then appends w.TurnExcerpt as turn
// w.Meta.TurnCount (a no-op when the excerpt is empty — the closure
// path's meta-only update). The turn-excerpt directory is FIFO-windowed
// by store.AppendThreadTurn. Shared by CreateThread and EngageThread;
// the only difference between the two is the spine op. Each successful
// write is recorded into the turn's scoped write set.
func (a *FileAdapter) writeThread(w memops.ThreadWrite) error {
	if err := store.SaveThreadFrontmatter(a.paths, w.Meta.ID, w.Meta); err != nil {
		return fmt.Errorf("save thread.md: %w", err)
	}
	a.touchTurnScope(store.ThreadMetaRel(w.Meta.ID))
	if err := store.AppendThreadTurn(a.paths, w.Meta.ID, w.Meta.TurnCount, w.TurnExcerpt); err != nil {
		return fmt.Errorf("append turn excerpt: %w", err)
	}
	if w.TurnExcerpt != "" {
		a.touchTurnScope(store.ThreadTurnRel(w.Meta.ID, w.Meta.TurnCount))
	}
	return nil
}

// LoadThread reads a thread's frontmatter and assembles its body from
// the recency-windowed turn-excerpt files.
func (a *FileAdapter) LoadThread(ctx context.Context, threadID string) (memops.Thread, error) {
	if err := ctx.Err(); err != nil {
		return memops.Thread{}, err
	}
	thr, err := store.LoadThread(a.paths, threadID)
	if err != nil {
		return memops.Thread{}, fmt.Errorf("fileadapter: load thread: %w", err)
	}
	return thr, nil
}

// LoadThreadMeta reads only the thread's metadata block, skipping the
// turn-excerpt directory.
func (a *FileAdapter) LoadThreadMeta(ctx context.Context, threadID string) (memops.ThreadMeta, error) {
	if err := ctx.Err(); err != nil {
		return memops.ThreadMeta{}, err
	}
	fm, err := store.LoadThreadFrontmatter(a.paths, threadID)
	if err != nil {
		return memops.ThreadMeta{}, fmt.Errorf("fileadapter: load thread meta: %w", err)
	}
	return fm, nil
}

// LoadThreadExcerpts returns the thread's retained turn-excerpts as
// discrete per-turn units (one per turns/<n>.md file), turn-number
// ordered — the fine-tier chunk unit for intra-thread recall.
func (a *FileAdapter) LoadThreadExcerpts(ctx context.Context, threadID string) ([]memops.ThreadExcerpt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ex, err := store.ReadThreadExcerpts(a.paths, threadID)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: load thread excerpts: %w", err)
	}
	return ex, nil
}

// LoadDebtWindowExcerpts returns the bounded embedding-debt window (the
// scrolled-out-but-recent tail, at most maxN excerpts) — the recall-
// completeness floor (§3.4). Delegates to store.ReadDebtWindowExcerpts, which
// reads only the maxN files below the assembly window.
func (a *FileAdapter) LoadDebtWindowExcerpts(ctx context.Context, threadID string, maxN int) ([]memops.ThreadExcerpt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ex, err := store.ReadDebtWindowExcerpts(a.paths, threadID, maxN)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: load debt-window excerpts: %w", err)
	}
	return ex, nil
}

// FindThread looks up the spine record for threadID.
func (a *FileAdapter) FindThread(ctx context.Context, threadID string) (memops.SpineRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return memops.SpineRecord{}, false, err
	}
	rec, found, err := store.FindSpineRecord(a.paths, threadID)
	if err != nil {
		return memops.SpineRecord{}, false, fmt.Errorf("fileadapter: find thread: %w", err)
	}
	return rec, found, nil
}

// ListThreads returns spine records matching the filter, sorted by ID.
// ReadSpine returns records in file order; spine.jsonl is appended in
// thread-creation order which is monotonic in n for thr_<n>, so the
// filtered result is already ID-ascending. We do not re-sort because
// resorting would mask drift on a corrupted spine — let verify catch it.
func (a *FileAdapter) ListThreads(ctx context.Context, filter memops.ThreadFilter) ([]memops.SpineRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := store.ReadSpine(a.paths.Spine)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: read spine: %w", err)
	}
	if filter.Project == "" && len(filter.States) == 0 && filter.SinceTurn <= 0 {
		return records, nil
	}
	stateSet := make(map[memops.ThreadState]struct{}, len(filter.States))
	for _, s := range filter.States {
		stateSet[s] = struct{}{}
	}
	out := make([]memops.SpineRecord, 0, len(records))
	for i := range records {
		r := records[i]
		if filter.Project != "" && r.Project != filter.Project {
			continue
		}
		if len(stateSet) > 0 {
			if _, ok := stateSet[r.State]; !ok {
				continue
			}
		}
		if filter.SinceTurn > 0 && r.TurnCount < filter.SinceTurn {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// NextThreadID returns the next available thr_<n> id, scanning the full
// spine so the new id never collides with a thread in a sibling project.
func (a *FileAdapter) NextThreadID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	records, err := store.ReadSpine(a.paths.Spine)
	if err != nil {
		return "", fmt.Errorf("fileadapter: read spine: %w", err)
	}
	return store.NextThreadID(records), nil
}

// RecordRecallFire increments the RecallFires counter for threadID. The
// spine record is canonical; the thread file's frontmatter is updated to
// the same value so the two stay in sync (a scenario invariant). If the
// spine record exists but the thread file is missing (drift), the spine
// write still stands and the frontmatter re-syncs on the next
// engagement — that case is not an error.
func (a *FileAdapter) RecordRecallFire(ctx context.Context, threadID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rec, found, err := store.FindSpineRecord(a.paths, threadID)
	if err != nil {
		return fmt.Errorf("fileadapter: record recall fire %s: %w", threadID, err)
	}
	if !found {
		return fmt.Errorf("fileadapter: record recall fire %s: %w", threadID, memops.ErrThreadNotFound)
	}
	newCount := rec.RecallFires + 1
	rec.RecallFires = newCount
	if err := store.UpdateSpineRecord(a.paths, rec); err != nil {
		return fmt.Errorf("fileadapter: update spine: %w", err)
	}
	a.touchTurnScope(store.SpineRel())
	fm, err := store.LoadThreadFrontmatter(a.paths, threadID)
	if err != nil {
		if errors.Is(err, memops.ErrThreadFileNotFound) {
			// Drift: spine is canonical; frontmatter re-syncs on next
			// engagement. Not a failure.
			return nil
		}
		return fmt.Errorf("fileadapter: load thread frontmatter: %w", err)
	}
	fm.RecallFires = newCount
	if err := store.SaveThreadFrontmatter(a.paths, threadID, fm); err != nil {
		return fmt.Errorf("fileadapter: save thread.md: %w", err)
	}
	a.fmCache.Put(fm) // write-through: thread.md just rewritten
	a.touchTurnScope(store.ThreadMetaRel(threadID))
	return nil
}

// ArchiveThread archives one retired thread — a thin wrapper over the batch
// ArchiveThreads (design §3.1). It preserves the single-thread seam and the
// ErrThreadNotFound contract: a thread with no spine record is reported by
// the batch as a skip, which this wrapper maps back to ErrThreadNotFound.
//
// The drain (turn/archival.go) still calls this per-thread in I2, so each
// call commits its own batch of one. The drain's rewire to call
// ArchiveThreads ONCE is I3.
func (a *FileAdapter) ArchiveThread(ctx context.Context, threadID string) error {
	res, err := a.ArchiveThreads(ctx, []string{threadID})
	if err != nil {
		return err
	}
	for _, o := range res.Outcomes {
		if o.ThrID == threadID && o.Skipped {
			return fmt.Errorf("fileadapter: archive thread %s: %w", threadID, memops.ErrThreadNotFound)
		}
	}
	return nil
}

// ---------- Project operations ----------

// CreateProject persists a new ProjectMeta. Today this delegates to
// SaveProjectMeta (no first-write vs. drift distinction at the substrate
// layer); the split exists on the port so caller intent is visible.
func (a *FileAdapter) CreateProject(ctx context.Context, meta memops.ProjectMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.SaveProjectMeta(a.paths, meta); err != nil {
		return fmt.Errorf("fileadapter: create project: %w", err)
	}
	a.touchTurnScope(store.ProjectMetaRel(meta.ID))
	return nil
}

// NextProjectID returns the next available prj_<n> id, scanning the
// full set of known projects so the new id never collides with an
// existing one.
func (a *FileAdapter) NextProjectID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	metas, err := store.ListProjects(a.paths)
	if err != nil {
		return "", fmt.Errorf("fileadapter: list projects: %w", err)
	}
	return store.NextProjectID(metas), nil
}

// LoadProject returns the meta for the given project ID.
func (a *FileAdapter) LoadProject(ctx context.Context, projectID string) (memops.ProjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return memops.ProjectMeta{}, err
	}
	meta, err := store.LoadProjectMeta(a.paths, projectID)
	if err != nil {
		return memops.ProjectMeta{}, fmt.Errorf("fileadapter: load project: %w", err)
	}
	return meta, nil
}

// SaveProject overwrites the meta for an existing project.
func (a *FileAdapter) SaveProject(ctx context.Context, meta memops.ProjectMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.SaveProjectMeta(a.paths, meta); err != nil {
		return fmt.Errorf("fileadapter: save project: %w", err)
	}
	a.touchTurnScope(store.ProjectMetaRel(meta.ID))
	return nil
}

// ListProjects returns every known project, sorted by ID.
func (a *FileAdapter) ListProjects(ctx context.Context) ([]memops.ProjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metas, err := store.ListProjects(a.paths)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: list projects: %w", err)
	}
	return metas, nil
}

// FindProjectByRemote returns the project whose remote URLs contain the
// given URL (after normalization on both sides — store.FindProjectByRemote
// does it internally, so callers may pass a raw URL).
func (a *FileAdapter) FindProjectByRemote(ctx context.Context, remoteURL string) (memops.ProjectMeta, bool, error) {
	if err := ctx.Err(); err != nil {
		return memops.ProjectMeta{}, false, err
	}
	meta, found, err := store.FindProjectByRemote(a.paths, remoteURL)
	if err != nil {
		return memops.ProjectMeta{}, false, fmt.Errorf("fileadapter: find project by remote: %w", err)
	}
	return meta, found, nil
}

// FindProjectByPath returns the project whose root paths contain absPath.
func (a *FileAdapter) FindProjectByPath(ctx context.Context, absPath string) (memops.ProjectMeta, bool, error) {
	if err := ctx.Err(); err != nil {
		return memops.ProjectMeta{}, false, err
	}
	meta, found, err := store.FindProjectByPath(a.paths, absPath)
	if err != nil {
		return memops.ProjectMeta{}, false, fmt.Errorf("fileadapter: find project by path: %w", err)
	}
	return meta, found, nil
}

// SetLastActiveProject records projectID for next-launch bootstrap.
func (a *FileAdapter) SetLastActiveProject(ctx context.Context, projectID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.WriteLastActive(a.paths, projectID); err != nil {
		return fmt.Errorf("fileadapter: set last active: %w", err)
	}
	return nil
}

// GetLastActiveProject returns the recorded last-active project ID.
func (a *FileAdapter) GetLastActiveProject(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, err := store.ReadLastActive(a.paths)
	if err != nil {
		return "", fmt.Errorf("fileadapter: get last active: %w", err)
	}
	return id, nil
}

// ---------- Session working set ----------

// SaveWorkingSet persists Layer B/C membership to <Home>/working-set.json.
// Format and atomic-write live in store.SaveWorkingSet; this is the
// substrate-routing pass-through.
func (a *FileAdapter) SaveWorkingSet(ctx context.Context, activeThreads, dormantThreads []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.SaveWorkingSet(a.paths, activeThreads, dormantThreads); err != nil {
		return fmt.Errorf("fileadapter: %w", err)
	}
	return nil
}

// LoadWorkingSet reads <Home>/working-set.json. A nonexistent file yields
// (nil, nil, nil) — the fresh-launch state. Format and parsing live in
// store.LoadWorkingSet.
func (a *FileAdapter) LoadWorkingSet(ctx context.Context) ([]string, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	active, dormant, err := store.LoadWorkingSet(a.paths)
	if err != nil {
		return nil, nil, fmt.Errorf("fileadapter: %w", err)
	}
	return active, dormant, nil
}

// RecallCacheDir returns <Home>/.recall-cache — the gitignored operational
// directory the §3.4 recall stack's persisted derived-vector cache lives in.
// The cache is operational state, never canonical (I5), so — like
// working-set.json — it is not a port concept and gets no MemoryOps method;
// the recall Service discovers this directory by type-asserting the adapter
// against a narrow optional interface (measure.recallCacheLocator). Paths
// stay the adapter's secret: callers never see PersonantPaths, only this one
// resolved directory string when they need it.
func (a *FileAdapter) RecallCacheDir() string {
	return a.paths.RecallCache
}

// ---------- Symbol index / recall ----------

// ProposeRecall runs the §3.4 layer-1 symbolic Jaccard pre-filter. The
// adapter loads spine + per-thread frontmatter from its substrate, then
// hands off to scoring.ProposeFromIndex (a pure function over the memops
// domain model). The scoring package itself never touches the substrate
// — that is this adapter's responsibility.
func (a *FileAdapter) ProposeRecall(ctx context.Context, query []string, opts memops.RecallOptions) ([]memops.RecallCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	spine, err := store.ReadSpine(a.paths.Spine)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: propose recall: read spine: %w", err)
	}
	threads, err := a.fmCache.LoadAll(a.paths, a.Logger)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: propose recall: load thread frontmatter: %w", err)
	}
	cands := scoring.ProposeFromIndex(spine, threads, query, scoring.Options{
		Threshold:        opts.Threshold,
		Project:          opts.Project,
		Exclude:          opts.Exclude,
		Limit:            opts.Limit,
		SupersededWeight: opts.SupersededWeight,
	})
	out := make([]memops.RecallCandidate, len(cands))
	for i, c := range cands {
		out[i] = memops.RecallCandidate{
			ThreadID:       c.ThreadID,
			Score:          c.Score,
			MatchedSymbols: c.MatchedSymbols,
		}
	}
	return out, nil
}

// InvalidateThread drops the cached parsed frontmatter for threadID,
// forcing the next ProposeRecall to re-read it from disk.
//
// This is the documented escape hatch for the cache-coherence invariant:
// FileAdapter is the sole thread.md frontmatter mutator in production and
// sim, so all in-adapter write sites keep the cache coherent automatically.
// Any out-of-band writer that bypasses the adapter (store.SeedThread in
// tests, future submind git-merge per #94) MUST call this — or
// InvalidateAll — after writing, or ProposeRecall may return a stale parse.
// No production caller invokes this today; it exists for those bypass paths.
func (a *FileAdapter) InvalidateThread(id string) {
	a.fmCache.Invalidate(id)
}

// InvalidateAll clears the entire parsed-frontmatter cache, forcing a full
// re-parse on the next ProposeRecall. See InvalidateThread for the
// coherence invariant; use this when an out-of-band writer touched an
// unknown set of threads (e.g. a bulk git-merge).
func (a *FileAdapter) InvalidateAll() {
	a.fmCache.Reset()
}

// RegenerateDerivedState rebuilds every derived artifact from canonical
// sources. For the file adapter this is symbols.jsonl + per-project
// digest.json, plus the derived-watermark stamp — regeneration is THE
// watermark write site (recovery.RebuildDerived), so the two can never
// drift apart. See RebuildDerived for why a mid-transaction (dirty-
// tree) stamp is safe: the watermark is only ever trusted against a
// clean worktree.
func (a *FileAdapter) RegenerateDerivedState(ctx context.Context, opts memops.IndexBuildOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := recovery.RebuildDerived(ctx, a.paths, indexOptionsFromMemops(opts)); err != nil {
		return fmt.Errorf("fileadapter: regenerate derived state: %w", err)
	}
	return nil
}

// CheckDerivedState compares what RegenerateDerivedState would write
// against what is currently materialized.
func (a *FileAdapter) CheckDerivedState(ctx context.Context, opts memops.IndexBuildOptions) (memops.CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return memops.CheckResult{}, err
	}
	result, err := index.Check(a.paths, indexOptionsFromMemops(opts))
	if err != nil {
		return memops.CheckResult{}, fmt.Errorf("fileadapter: check derived state: %w", err)
	}
	return result, nil
}

func indexOptionsFromMemops(o memops.IndexBuildOptions) index.Options {
	return index.Options{
		Quiet:  o.Quiet,
		Logger: o.Logger,
		Warner: o.Warner,
	}
}

// ---------- Working set ----------
//
// ComposeWorkingSet and its substrate-fetch helpers live in
// workset_compose.go so the fetch surface is visible as a unit.

// ---------- Event log ----------

// EmitDelta records one context-modification event on the substrate
// side. The Retention field on Delta is forward-compatibility for the
// transient-data lifecycle work; the current file adapter ignores it.
func (a *FileAdapter) EmitDelta(ctx context.Context, delta memops.Delta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := eventlog.LogContextModified(a.paths, delta.Source, len(delta.Content)); err != nil {
		return fmt.Errorf("fileadapter: emit delta: %w", err)
	}
	a.touchTurnScope(eventlog.DayLogRel(clock.Timeline()))
	return nil
}

// Log writes a general-purpose event-log line.
func (a *FileAdapter) Log(ctx context.Context, category, action, details string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := eventlog.Log(a.paths, category, action, details); err != nil {
		return fmt.Errorf("fileadapter: log: %w", err)
	}
	// The event log is tracked and continuously appended; recording the
	// day file here is what lets the scoped CommitTurn absorb log dirt
	// without a tree walk. Lines written by OTHER writers (recovery's
	// own events at open) land in the same day file and ride along.
	a.touchTurnScope(eventlog.DayLogRel(clock.Timeline()))
	return nil
}

// ---------- §3.9 tracked-file content ----------

// RecordFileWrite loads the thread's §3.9 tracked-file sidecar, appends
// content as the path's new version, and saves the sidecar. A missing
// sidecar is the fresh state — store.LoadThreadFiles returns an empty
// store, so the first write seeds the path's chain. The write is
// idempotent on unchanged content (see store.ThreadFiles.RecordWrite).
func (a *FileAdapter) RecordFileWrite(ctx context.Context, threadID, path, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tf, err := store.LoadThreadFiles(a.paths, threadID)
	if err != nil {
		return fmt.Errorf("fileadapter: record file write %s: load: %w", threadID, err)
	}
	tf.RecordWrite(path, content)
	if err := store.SaveThreadFiles(a.paths, tf); err != nil {
		return fmt.Errorf("fileadapter: record file write %s: save: %w", threadID, err)
	}
	a.touchTurnScope(store.ThreadFilesRel(threadID))
	return nil
}

// RecordFileCommit loads the thread's §3.9 tracked-file sidecar, sets the
// git-commit pointer on the path's entry, and saves the sidecar. The
// commit timestamp is stamped here from clock.Timeline() in RFC3339 form
// — consistent with how eventlog stamps events — so the port signature
// carries no timestamp. committedTurn is recorded for the §3.9
// git-minimization retention window. Surfaces ThreadFiles.RecordCommit's
// untracked-path error to the caller (a commit with no preceding write).
func (a *FileAdapter) RecordFileCommit(ctx context.Context, threadID, path, hash string, committedTurn int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tf, err := store.LoadThreadFiles(a.paths, threadID)
	if err != nil {
		return fmt.Errorf("fileadapter: record file commit %s: load: %w", threadID, err)
	}
	committedAt := clock.Timeline().Format(time.RFC3339)
	if err := tf.RecordCommit(path, hash, committedAt, committedTurn); err != nil {
		return fmt.Errorf("fileadapter: record file commit %s: %w", threadID, err)
	}
	if err := store.SaveThreadFiles(a.paths, tf); err != nil {
		return fmt.Errorf("fileadapter: record file commit %s: save: %w", threadID, err)
	}
	a.touchTurnScope(store.ThreadFilesRel(threadID))
	return nil
}

// workspaceGitRoot resolves the workspace git root for threadID's project
// (Q1 — adapter-internal resolution, keeping the port path-free): thread →
// spine record → project → CurrentRootPath → store.FindGitRoot walked from
// it. Returns ok=false when the chain cannot be completed (thread/project
// absent, no recorded root, root not under a git repo, or the recorded path
// is stale/moved). A false ok is the SAFE answer — it makes the aging gate
// refuse to drop and the recovery path return ErrFileVersionUnreachable. A
// non-nil error is reserved for an I/O failure that prevented resolution
// (it must not be swallowed into a silent "not reachable").
func (a *FileAdapter) workspaceGitRoot(threadID string) (root string, ok bool, err error) {
	rec, found, err := store.FindSpineRecord(a.paths, threadID)
	if err != nil {
		return "", false, fmt.Errorf("find spine: %w", err)
	}
	if !found || rec.Project == "" {
		return "", false, nil
	}
	meta, err := store.LoadProjectMeta(a.paths, rec.Project)
	if err != nil {
		if errors.Is(err, memops.ErrProjectNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load project %s: %w", rec.Project, err)
	}
	if meta.CurrentRootPath == "" {
		return "", false, nil
	}
	gitRoot, found, err := store.FindGitRoot(meta.CurrentRootPath)
	if err != nil {
		return "", false, fmt.Errorf("find git root: %w", err)
	}
	if !found {
		return "", false, nil
	}
	return gitRoot, true, nil
}

// gitPathForShow normalizes a tracked-file path into the git-root-relative
// form that `git show <hash>:<path>` / `git cat-file -e <hash>:<path>`
// require. §3.9 tracked paths are caller-supplied (delta.Meta["path"]) and
// documented as workspace-relative, but nothing enforces that at this
// boundary; an absolute path would make git reject the pathspec. An
// absolute path under gitRoot is rebased to gitRoot; anything else (an
// already-relative path, or an absolute path that escapes gitRoot) is
// returned unchanged.
//
// This is the ONE shared normalization used by BOTH the aging gate
// (BlobReachable) and the recovery read (ShowFileAtCommit), so the two
// consult git with identical inputs. That is what keeps the "one shared
// predicate" claim literally true: for any given (root, hash, path) the
// gate refuses aging exactly when recovery would fail, and drops a chain
// exactly when recovery would succeed — they can never diverge.
func gitPathForShow(gitRoot, path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(gitRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path // not under gitRoot — git rejects it identically in both paths
	}
	return rel
}

// AgeFileChains applies the §3.9 git-minimization policy to threadID's
// tracked-file store, gated by workspace-git reachability. A thread with no
// tracked-file sidecar (zero files) is a no-op — no empty sidecar is
// written.
//
// store.ThreadFiles.AgeOut computes the window-expired CANDIDATES (pure, no
// git, no mutation). For each candidate the adapter consults the headline
// §3.9.1 reachability gate: a chain is dropped (via tf.DropChain) ONLY IF
// the BLOB at the recorded commit hash AND path is confirmed reachable in
// the workspace repo (store.BlobReachable — the same `hash:path` the
// recovery read consults, not merely the commit). An unreachable blob —
// amend/rebase/gc/move, a path absent at that commit (path-form mismatch,
// rename before commit), or git unavailable — REFUSES aging: the chain is
// retained and a dedup/chain-age-refused event is logged. No durable
// content is ever aged away without an application-reachable recovery path.
//
// When anything actually ages, the updated sidecar is saved and one
// dedup/chain-aged event-log line records the aged paths and bytes freed
// (the demand-sizing forensic data — kept exact, like the archival byte
// counts). A pass that refuses every candidate writes no sidecar (nothing
// changed) but still logs each refusal.
func (a *FileAdapter) AgeFileChains(ctx context.Context, threadID string, currentTurn int) ([]string, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	tf, err := store.LoadThreadFiles(a.paths, threadID)
	if err != nil {
		return nil, 0, fmt.Errorf("fileadapter: age file chains %s: load: %w", threadID, err)
	}
	if len(tf.Files) == 0 {
		// No sidecar (or an empty one) — nothing to age, and we must not
		// materialize an empty sidecar where none existed.
		return nil, 0, nil
	}
	candidates := tf.AgeOut(currentTurn, clock.Timeline())
	if len(candidates) == 0 {
		return nil, 0, nil
	}

	root, rootOK, err := a.workspaceGitRoot(threadID)
	if err != nil {
		return nil, 0, fmt.Errorf("fileadapter: age file chains %s: resolve workspace root: %w", threadID, err)
	}

	var agedPaths []string
	var bytesFreed int
	for _, path := range candidates {
		e, ok := tf.Entry(path)
		if !ok {
			continue // defensive: candidate disappeared between calls
		}
		reachable := false
		reason := "workspace-root-unresolved"
		if rootOK {
			r, rerr := store.BlobReachable(root, e.LastCommit, gitPathForShow(root, path))
			if rerr != nil {
				return agedPaths, bytesFreed, fmt.Errorf("fileadapter: age file chains %s: reachability %s: %w", threadID, path, rerr)
			}
			reachable = r
			if !reachable {
				reason = "blob-unreachable"
			}
		}
		if !reachable {
			// Refuse to age: retain the chain, log the skipped reclamation
			// so it is forensically visible (Inv 3). Details are adapter-
			// built (path, hash, reason) — never user content.
			if logErr := eventlog.Log(a.paths, "dedup", "chain-age-refused",
				fmt.Sprintf("thr=%s path=%s hash=%s reason=%s", threadID, path, e.LastCommit, reason)); logErr != nil {
				return agedPaths, bytesFreed, fmt.Errorf("fileadapter: age file chains %s: log refusal: %w", threadID, logErr)
			}
			a.touchTurnScope(eventlog.DayLogRel(clock.Timeline()))
			continue
		}
		if freed, ok := tf.DropChain(path); ok {
			bytesFreed += freed
			agedPaths = append(agedPaths, path)
		}
	}

	if len(agedPaths) == 0 {
		// Every candidate was refused — nothing mutated, so no sidecar
		// write. Refusals were already logged above.
		return nil, 0, nil
	}
	if err := store.SaveThreadFiles(a.paths, tf); err != nil {
		return nil, 0, fmt.Errorf("fileadapter: age file chains %s: save: %w", threadID, err)
	}
	a.touchTurnScope(store.ThreadFilesRel(threadID))
	if err := eventlog.Log(a.paths, "dedup", "chain-aged",
		fmt.Sprintf("thr=%s paths=%s bytes=%d", threadID, strings.Join(agedPaths, ","), bytesFreed)); err != nil {
		return agedPaths, bytesFreed, fmt.Errorf("fileadapter: age file chains %s: log: %w", threadID, err)
	}
	a.touchTurnScope(eventlog.DayLogRel(clock.Timeline()))
	return agedPaths, bytesFreed, nil
}

// GetFileVersion recovers a tracked file's committed content at a recorded
// commit hash — the §3.9.1 recovery path (port doc on MemoryOps). Fast
// path: when the path's chain is still retained and the hash matches
// LastCommit, the live literal is returned with no git access. Otherwise
// the blob is read read-only from the workspace git tree
// (store.ShowFileAtCommit). ErrFileVersionUnreachable when the hash is
// unreachable / the blob is absent / the workspace root cannot be resolved.
// Never returns ("", nil).
func (a *FileAdapter) GetFileVersion(ctx context.Context, threadID, path, hash string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	tf, err := store.LoadThreadFiles(a.paths, threadID)
	if err != nil {
		return "", fmt.Errorf("fileadapter: get file version %s: load: %w", threadID, err)
	}
	// Fast path: retained chain whose LastCommit matches — return the live
	// literal directly, no workspace git. Aging only fires post-commit on an
	// unchanged-since-commit entry, so Chain.Current() is the committed
	// state. (Recovering an older in-chain version by a non-LastCommit hash
	// is out of scope — only LastCommit is a hash, §3.9.1.)
	if e, ok := tf.Entry(path); ok && e.LastCommit == hash && e.Chain.Len() > 0 {
		return e.Chain.Current(), nil
	}

	root, rootOK, err := a.workspaceGitRoot(threadID)
	if err != nil {
		return "", fmt.Errorf("fileadapter: get file version %s: resolve workspace root: %w", threadID, err)
	}
	if !rootOK {
		return "", fmt.Errorf("fileadapter: get file version %s %s@%s: %w", threadID, path, hash, memops.ErrFileVersionUnreachable)
	}
	content, present, err := store.ShowFileAtCommit(root, hash, gitPathForShow(root, path))
	if err != nil {
		return "", fmt.Errorf("fileadapter: get file version %s: show: %w", threadID, err)
	}
	if !present {
		return "", fmt.Errorf("fileadapter: get file version %s %s@%s: %w", threadID, path, hash, memops.ErrFileVersionUnreachable)
	}
	return content, nil
}

// ---------- Bootstrap and verification ----------

// Init scaffolds the substrate for first-run use.
func (a *FileAdapter) Init(ctx context.Context, opts memops.InitOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.Init(a.paths, store.InitOptions{Quiet: opts.Quiet, Logger: opts.Logger}); err != nil {
		return fmt.Errorf("fileadapter: init: %w", err)
	}
	return nil
}

// Verify performs a read-only structural validation pass.
func (a *FileAdapter) Verify(ctx context.Context) (memops.VerifyReport, error) {
	if err := ctx.Err(); err != nil {
		return memops.VerifyReport{}, err
	}
	report, err := verify.Verify(a.paths, verify.VerifyOptions{})
	if err != nil {
		return memops.VerifyReport{}, fmt.Errorf("fileadapter: verify: %w", err)
	}
	return report, nil
}

// ResolveActiveProject runs the spec §4.5.7 bootstrap waterfall.
func (a *FileAdapter) ResolveActiveProject(ctx context.Context, hints memops.BootstrapHints) (memops.BootstrapResult, error) {
	if err := ctx.Err(); err != nil {
		return memops.BootstrapResult{}, err
	}
	res, err := store.ResolveActiveProject(a.paths, hints)
	if err != nil {
		return memops.BootstrapResult{}, fmt.Errorf("fileadapter: resolve active project: %w", err)
	}
	return res, nil
}

// ---------- Configuration ----------

// LoadProviders reads providers.toml. A nonexistent file yields an
// empty map and no error. A provider with an unreadable apiKeyFile is
// omitted from the map and returned as a ProviderFault; the error is
// reserved for file-level failures.
func (a *FileAdapter) LoadProviders(ctx context.Context) (map[string]memops.Provider, []memops.ProviderFault, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	providers, faults, err := store.LoadProviders(a.paths.Providers)
	if err != nil {
		return nil, nil, fmt.Errorf("fileadapter: load providers: %w", err)
	}
	// store.LoadProviders returns memops.Providers (map[string]memops.Provider)
	// and []memops.ProviderFault directly — the domain types are owned by
	// memops — so no element-by-element bridging is needed.
	return providers, faults, nil
}

// LoadConfig reads config.toml — the chat/embedding choices. A
// nonexistent file yields a zero Config and no error.
func (a *FileAdapter) LoadConfig(ctx context.Context) (memops.Config, error) {
	if err := ctx.Err(); err != nil {
		return memops.Config{}, err
	}
	cfg, err := store.LoadConfig(a.paths.Config)
	if err != nil {
		return memops.Config{}, fmt.Errorf("fileadapter: load config: %w", err)
	}
	return cfg, nil
}
