// Package fileadapter implements memops.MemoryOps against personant's
// canonical file substrate (JSONL + markdown + YAML frontmatter + TOML
// providers + git on ~/.personant/). Delegates to internal/store,
// internal/index, internal/recall, internal/workset, internal/verify,
// and internal/eventlog.
//
// This is the v0.1 adapter. Future substrate adapters (SQLite,
// network-backed, in-memory simulation) implement memops.MemoryOps from
// scratch against their own storage; nothing here is intended to be
// reusable by them.
//
// # Caller migration is out of scope here (Phase A.2)
//
// This package is added alongside the existing application packages
// (internal/turn, internal/recall, internal/workset, internal/chat) that
// still call internal/store directly. Caller migration happens in A.3+.
package fileadapter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/eventlog"
	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/recall"
	"personant/internal/store"
	"personant/internal/verify"
	"personant/internal/workset"
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
}

// NewFileAdapter constructs an adapter rooted at paths.Home. Caller
// supplies the resolved paths; the adapter does not call
// store.ResolvePaths or store.PathsForHome itself so that test code can
// pin a t.TempDir() and CLI code can honor --home flags without the
// adapter being involved in the resolution.
func NewFileAdapter(paths store.PersonantPaths) *FileAdapter {
	return &FileAdapter{paths: paths}
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
	if err := writeThread(a.paths, w); err != nil {
		return fmt.Errorf("fileadapter: create thread: %w", err)
	}
	if err := store.AppendSpineRecord(a.paths, w.Spine); err != nil {
		return fmt.Errorf("fileadapter: append spine: %w", err)
	}
	return nil
}

// EngageThread rewrites thread.md and appends one turn excerpt, then
// updates the spine record. Owns the missing-thread-dir recovery: if the
// spine record exists but the thread directory is absent (drift state),
// SaveThreadFrontmatter recreates the directory — no special case
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
	if err := writeThread(a.paths, w); err != nil {
		return fmt.Errorf("fileadapter: engage thread: %w", err)
	}
	if err := store.UpdateSpineRecord(a.paths, w.Spine); err != nil {
		return fmt.Errorf("fileadapter: update spine: %w", err)
	}
	return nil
}

// writeThread persists a ThreadWrite to the substrate: it rewrites the
// bounded thread.md frontmatter file, then appends w.TurnExcerpt as turn
// w.Frontmatter.TurnCount (a no-op when the excerpt is empty — the
// closure path's frontmatter-only update). The turn-excerpt directory is
// FIFO-windowed by store.AppendThreadTurn. Shared by CreateThread and
// EngageThread; the only difference between the two is the spine op.
func writeThread(paths store.PersonantPaths, w memops.ThreadWrite) error {
	if err := store.SaveThreadFrontmatter(paths, w.Frontmatter.ID, w.Frontmatter); err != nil {
		return fmt.Errorf("save thread.md: %w", err)
	}
	if err := store.AppendThreadTurn(paths, w.Frontmatter.ID, w.Frontmatter.TurnCount, w.TurnExcerpt); err != nil {
		return fmt.Errorf("append turn excerpt: %w", err)
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

// LoadThreadFrontmatter reads only the thread's thread.md metadata,
// skipping the turn-excerpt directory.
func (a *FileAdapter) LoadThreadFrontmatter(ctx context.Context, threadID string) (memops.ThreadFrontmatter, error) {
	if err := ctx.Err(); err != nil {
		return memops.ThreadFrontmatter{}, err
	}
	fm, err := store.LoadThreadFrontmatter(a.paths, threadID)
	if err != nil {
		return memops.ThreadFrontmatter{}, fmt.Errorf("fileadapter: load thread frontmatter: %w", err)
	}
	return fm, nil
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
	return nil
}

// ArchiveThread removes a retired thread from the active spine. v0.1 is a
// deletion STUB: the thread file and its spine record are deleted outright.
// Real §3.8 git-based archival with a recovery path is v0.2; this method
// is the stable seam across both implementations.
//
// Before deleting, the thread body's byte size is measured and logged on
// the archive.simulated-delete event line. That byte count, paired with
// the event log's clock.Timeline() stamp, is the deliberate demand-sizing
// data for designing v0.2 archival — it must stay exact.
//
// The derived index (symbols.jsonl) is intentionally NOT regenerated here.
// The cardinality-pressure trigger batches archival and regenerates once
// after the batch, so a per-call rebuild would be wasted work.
func (a *FileAdapter) ArchiveThread(ctx context.Context, threadID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rec, found, err := store.FindSpineRecord(a.paths, threadID)
	if err != nil {
		return fmt.Errorf("fileadapter: archive thread %s: %w", threadID, err)
	}
	if !found {
		return fmt.Errorf("fileadapter: archive thread %s: %w", threadID, memops.ErrThreadNotFound)
	}

	// Measure the body byte size before deletion. A missing thread is
	// size 0, not an error — the spine record alone is enough to archive.
	// NOTE: bodySize is the assembled turn-excerpt body only — it
	// excludes the YAML frontmatter and the spine.jsonl line. v0.2
	// archival sizing against this stat must account for that: real
	// on-disk cost is body + frontmatter + one spine line. The body is
	// also recency-windowed (store.ThreadTurnWindow), so it is the live
	// window, not the thread's full history.
	bodySize := 0
	if body, err := store.ReadThreadBody(a.paths, threadID, 0); err == nil {
		bodySize = len(body)
	} else if !errors.Is(err, memops.ErrThreadFileNotFound) {
		return fmt.Errorf("fileadapter: archive thread %s: read body: %w", threadID, err)
	}

	// Delete the entire thread directory — thread.md, turns/, and
	// files.json all go together. A missing directory is fine (already
	// gone). os.RemoveAll subsumes the explicit §3.9 sidecar delete that
	// the single-file format needed.
	if err := os.RemoveAll(store.ThreadDir(a.paths, threadID)); err != nil {
		return fmt.Errorf("fileadapter: archive thread %s: remove dir: %w", threadID, err)
	}
	if err := store.RemoveSpineRecord(a.paths, threadID); err != nil {
		return fmt.Errorf("fileadapter: archive thread %s: remove spine: %w", threadID, err)
	}

	// project= attributes the delete per project so v0.2 archival
	// demand-sizing can size pressure per project even though the archival
	// drain itself is substrate-global.
	if err := eventlog.Log(a.paths, "archive", "simulated-delete",
		fmt.Sprintf("thr=%s project=%s bytes=%d", threadID, rec.Project, bodySize)); err != nil {
		return fmt.Errorf("fileadapter: archive thread %s: log: %w", threadID, err)
	}
	// One loud warning per archival: v0.1 deletes outright — there is no
	// recovery path. Real recovery-capable archival lands in v0.2.
	if err := eventlog.Log(a.paths, "archive", "warning",
		"thr="+threadID+" v0.1 deletion stub — thread deleted with NO recovery path; recovery-capable archival is v0.2"); err != nil {
		return fmt.Errorf("fileadapter: archive thread %s: log warning: %w", threadID, err)
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

// workingSetFile is the on-disk shape of the persisted session
// working-set artifact (<Home>/working-set.json). Only the two ordered
// ID lists are stored; session-volatile state is deliberately omitted.
type workingSetFile struct {
	ActiveThreads  []string `json:"active_threads"`
	DormantThreads []string `json:"dormant_threads"`
}

// SaveWorkingSet persists Layer B/C membership to <Home>/working-set.json.
// The artifact is volatile session state — it is gitignored in the home
// tree (see store.Init's seedGitignore), so the per-turn rewrite does not
// dirty the substrate's git repo. Atomic: encode to a temp file in the
// home directory, fsync, rename — the same pattern as SaveProjectMeta.
func (a *FileAdapter) SaveWorkingSet(ctx context.Context, activeThreads, dormantThreads []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.paths.Home == "" {
		return errors.New("fileadapter: save working set: PersonantPaths.Home is empty")
	}

	tmp, err := os.CreateTemp(a.paths.Home, ".working-set-*.tmp")
	if err != nil {
		return fmt.Errorf("fileadapter: save working set: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(workingSetFile{
		ActiveThreads:  activeThreads,
		DormantThreads: dormantThreads,
	}); err != nil {
		tmp.Close()
		return fmt.Errorf("fileadapter: save working set: encode: %w", err)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("fileadapter: save working set: flush: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fileadapter: save working set: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fileadapter: save working set: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, a.paths.WorkingSet); err != nil {
		return fmt.Errorf("fileadapter: save working set: rename %s: %w", filepath.Base(a.paths.WorkingSet), err)
	}
	cleanup = false
	return nil
}

// LoadWorkingSet reads <Home>/working-set.json. A nonexistent file
// yields (nil, nil, nil) — the fresh-launch state — mirroring how
// GetLastActiveProject treats an absent marker. A present but malformed
// file yields an error.
func (a *FileAdapter) LoadWorkingSet(ctx context.Context) ([]string, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(a.paths.WorkingSet)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("fileadapter: load working set: %w", err)
	}
	var ws workingSetFile
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, nil, fmt.Errorf("fileadapter: load working set: parse %s: %w", a.paths.WorkingSet, err)
	}
	return ws.ActiveThreads, ws.DormantThreads, nil
}

// ---------- Symbol index / recall ----------

// ProposeRecall runs the §3.4 layer-1 symbolic Jaccard pre-filter. The
// adapter loads spine + per-thread frontmatter from its substrate, then
// hands off to recall.ProposeFromIndex (a pure function over the memops
// domain model). The recall package itself never touches the substrate
// — that is this adapter's responsibility.
func (a *FileAdapter) ProposeRecall(ctx context.Context, query []string, opts memops.RecallOptions) ([]memops.RecallCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	spine, err := store.ReadSpine(a.paths.Spine)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: propose recall: read spine: %w", err)
	}
	threads, err := store.LoadAllThreadFrontmatter(a.paths, nil)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: propose recall: load thread frontmatter: %w", err)
	}
	cands := recall.ProposeFromIndex(spine, threads, query, recall.Options{
		Threshold: opts.Threshold,
		Project:   opts.Project,
		Exclude:   opts.Exclude,
		Limit:     opts.Limit,
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

// RegenerateDerivedState rebuilds every derived artifact from canonical
// sources. For the file adapter this is symbols.jsonl + per-project
// digest.json.
func (a *FileAdapter) RegenerateDerivedState(ctx context.Context, opts memops.IndexBuildOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := index.Rebuild(a.paths, indexOptionsFromMemops(opts)); err != nil {
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

// ComposeWorkingSet builds the layer-by-layer working-set content for
// one turn. Non-fatal layer-render warnings are emitted to the event log
// as workset.warning lines (the substrate's canonical destination); any
// caller-supplied a.Logger is also notified.
func (a *FileAdapter) ComposeWorkingSet(ctx context.Context, in memops.WorksetInput) (memops.WorksetLayers, error) {
	if err := ctx.Err(); err != nil {
		return memops.WorksetLayers{}, err
	}
	state := workset.State{
		Paths:          a.paths,
		ActiveProject:  in.ActiveProject,
		ActiveThreads:  in.ActiveThreads,
		DormantThreads: in.DormantThreads,
		Budget:         in.Budget,
	}
	opts := workset.ComposeOptions{
		Logger: func(format string, args ...any) {
			_ = a.Log(ctx, "workset", "warning", sanitizeWorksetDetail(fmt.Sprintf(format, args...)))
			if a.Logger != nil {
				a.Logger(format, args...)
			}
		},
	}
	params, err := workset.Compose(state, opts)
	if err != nil {
		return memops.WorksetLayers{}, fmt.Errorf("fileadapter: compose working set: %w", err)
	}
	return memops.WorksetLayers{
		LayerE:  params.LayerE,
		LayerA1: params.LayerA1,
		LayerA2: params.LayerA2,
		LayerB:  params.LayerB,
		LayerC:  params.LayerC,
	}, nil
}

// sanitizeWorksetDetail strips newlines and tabs from a workset warning
// before it lands in a one-per-line event-log entry. Local twin of
// internal/turn.sanitizeDetail so this package keeps its narrow import
// graph (no cross-import into the turn-loop side of the application).
func sanitizeWorksetDetail(s string) string {
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\t' {
			r = append(r, ' ')
			continue
		}
		r = append(r, c)
	}
	return string(r)
}

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
	return nil
}

// AgeFileChains applies the §3.9 git-minimization policy to threadID's
// tracked-file store via store.ThreadFiles.AgeOut. A thread with no
// tracked-file sidecar (zero files) is a no-op — no empty sidecar is
// written. When anything ages, the updated sidecar is saved and one
// dedup/chain-aged event-log line records the aged paths and bytes freed
// (the demand-sizing forensic data — kept exact, like the archival byte
// counts).
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
	agedPaths, bytesFreed := tf.AgeOut(currentTurn, clock.Timeline())
	if len(agedPaths) == 0 {
		return nil, 0, nil
	}
	if err := store.SaveThreadFiles(a.paths, tf); err != nil {
		return nil, 0, fmt.Errorf("fileadapter: age file chains %s: save: %w", threadID, err)
	}
	if err := eventlog.Log(a.paths, "dedup", "chain-aged",
		fmt.Sprintf("thr=%s paths=%s bytes=%d", threadID, strings.Join(agedPaths, ","), bytesFreed)); err != nil {
		return agedPaths, bytesFreed, fmt.Errorf("fileadapter: age file chains %s: log: %w", threadID, err)
	}
	return agedPaths, bytesFreed, nil
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
