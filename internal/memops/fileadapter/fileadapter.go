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
	"context"
	"errors"
	"fmt"

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

// CreateThread atomically writes the thread file then appends the
// spine record. Pre-checks for a duplicate ID so a duplicate does not
// produce a stray thread file. AppendSpineRecord also enforces
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
	if err := store.SaveThread(a.paths, memops.Thread{Frontmatter: w.Frontmatter, Body: w.Body}); err != nil {
		return fmt.Errorf("fileadapter: save thread file: %w", err)
	}
	if err := store.AppendSpineRecord(a.paths, w.Spine); err != nil {
		return fmt.Errorf("fileadapter: append spine: %w", err)
	}
	return nil
}

// EngageThread rewrites the thread file and updates the spine record.
// Owns the missing-file fallback: if the spine record exists but the
// thread file is absent (drift state), the adapter materializes a fresh
// file from the supplied ThreadWrite rather than failing.
func (a *FileAdapter) EngageThread(ctx context.Context, w memops.ThreadWrite) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, found, err := store.FindSpineRecord(a.paths, w.Spine.ID); err != nil {
		return fmt.Errorf("fileadapter: find spine: %w", err)
	} else if !found {
		return fmt.Errorf("fileadapter: engage thread %s: %w", w.Spine.ID, memops.ErrThreadNotFound)
	}
	// SaveThread is an unconditional write — it creates the file if
	// absent. That is the missing-file fallback in one line: no special
	// case needed.
	if err := store.SaveThread(a.paths, memops.Thread{Frontmatter: w.Frontmatter, Body: w.Body}); err != nil {
		return fmt.Errorf("fileadapter: save thread file: %w", err)
	}
	if err := store.UpdateSpineRecord(a.paths, w.Spine); err != nil {
		return fmt.Errorf("fileadapter: update spine: %w", err)
	}
	return nil
}

// LoadThread reads and parses the thread file for threadID.
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
	thr, err := store.LoadThread(a.paths, threadID)
	if err != nil {
		if errors.Is(err, memops.ErrThreadFileNotFound) {
			// Drift: spine is canonical; frontmatter re-syncs on next
			// engagement. Not a failure.
			return nil
		}
		return fmt.Errorf("fileadapter: load thread: %w", err)
	}
	thr.Frontmatter.RecallFires = newCount
	if err := store.SaveThread(a.paths, thr); err != nil {
		return fmt.Errorf("fileadapter: save thread file: %w", err)
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

// ---------- Symbol index / recall ----------

// ProposeRecall runs the §3.4 layer-1 symbolic Jaccard pre-filter.
func (a *FileAdapter) ProposeRecall(ctx context.Context, query []string, opts memops.RecallOptions) ([]memops.RecallCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	recallOpts := recall.Options{
		Threshold: opts.Threshold,
		Project:   opts.Project,
		Exclude:   opts.Exclude,
		Limit:     opts.Limit,
	}
	cands, err := recall.Propose(a.paths, query, recallOpts)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: propose recall: %w", err)
	}
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
	out := memops.CheckResult{}
	if len(result.Drifts) > 0 {
		out.Drifts = make([]memops.Drift, len(result.Drifts))
		for i, d := range result.Drifts {
			out.Drifts[i] = memops.Drift{Path: d.Path, Status: d.Status, Detail: d.Detail}
		}
	}
	return out, nil
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
		Budget:         worksetBudgetFromMemops(in.Budget),
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

func worksetBudgetFromMemops(b memops.Budget) workset.Budget {
	return workset.Budget{
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
	out := memops.VerifyReport{
		Drift: report.Drift,
	}
	if len(report.Errors) > 0 {
		out.Errors = make([]memops.VerifyFinding, len(report.Errors))
		for i, f := range report.Errors {
			out.Errors[i] = memops.VerifyFinding{Path: f.Path, Field: f.Field, Message: f.Message}
		}
	}
	if len(report.Warnings) > 0 {
		out.Warnings = make([]memops.VerifyFinding, len(report.Warnings))
		for i, f := range report.Warnings {
			out.Warnings[i] = memops.VerifyFinding{Path: f.Path, Field: f.Field, Message: f.Message}
		}
	}
	return out, nil
}

// ResolveActiveProject runs the spec §4.5.7 bootstrap waterfall.
func (a *FileAdapter) ResolveActiveProject(ctx context.Context, hints memops.BootstrapHints) (memops.BootstrapResult, error) {
	if err := ctx.Err(); err != nil {
		return memops.BootstrapResult{}, err
	}
	res, err := store.ResolveActiveProject(a.paths, store.BootstrapOptions{
		ExplicitProject: hints.ExplicitProject,
		CWD:             hints.CWD,
	})
	if err != nil {
		return memops.BootstrapResult{}, fmt.Errorf("fileadapter: resolve active project: %w", err)
	}
	return memops.BootstrapResult{
		Step:      bootstrapStepFromStore(res.Step),
		Resolved:  res.Resolved,
		Candidate: res.Candidate,
	}, nil
}

// bootstrapStepFromStore maps the store-side BootstrapStep enum to its
// memops twin. Both packages use the same iota order; the switch is
// explicit to make the dependency obvious to readers and to fail loudly
// if either side reorders.
func bootstrapStepFromStore(s store.BootstrapStep) memops.BootstrapStep {
	switch s {
	case store.StepUnset:
		return memops.StepUnset
	case store.StepExplicit:
		return memops.StepExplicit
	case store.StepRemoteMatch:
		return memops.StepRemoteMatch
	case store.StepPathMatch:
		return memops.StepPathMatch
	case store.StepNeedsConfirmation:
		return memops.StepNeedsConfirmation
	case store.StepNeedsFallback:
		return memops.StepNeedsFallback
	default:
		return memops.StepUnset
	}
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
