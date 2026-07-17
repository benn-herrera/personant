// Tests verify the wiring of FileAdapter onto its underlying substrate
// packages. The substrate packages have their own coverage; the goal
// here is to confirm each adapter method routes to the right call and
// preserves observable effects.
package fileadapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"personant/internal/autogit"
	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/store"
)

// pinClock freezes clock.Timeline at a fixed instant for the duration of the
// test so archival commit signatures and ArchivedAt/RecoveredAt stamps are
// deterministic (AC6) and no wall-clock leaks into committed state.
func pinClock(t *testing.T) time.Time {
	t.Helper()
	when := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	restore := clock.SetTimeline(func() time.Time { return when })
	t.Cleanup(restore)
	return when
}

// newAdapter returns a FileAdapter rooted at t.TempDir(), with the
// substrate scaffolded via store.Init. The init pass writes seed
// directives, providers.toml template, README, and runs `git init` on
// the home tree — that mirrors the production first-run state.
func newAdapter(t *testing.T) *FileAdapter {
	t.Helper()
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return NewFileAdapter(paths)
}

// rfc3339Now returns a stable RFC3339 timestamp string. SpineRecord
// validation expects RFC3339; using clock.Timeline keeps the value real
// without test flake (no parsing constraints other than RFC3339).
func rfc3339Now() string {
	return clock.Timeline().UTC().Format(time.RFC3339)
}

// validSpine returns a SpineRecord that satisfies verify.Verify's
// minimum schema (4 anchors, RFC3339 timestamps, valid state, present
// project). The adapter does not enforce these — but a few tests cross
// into Verify, which does.
func validSpine(id, project string) memops.SpineRecord {
	now := rfc3339Now()
	return memops.SpineRecord{
		ID:           id,
		Project:      project,
		Anchors:      []string{"alpha", "beta", "gamma", "delta"},
		Summary:      "test thread",
		State:        memops.ThreadActive,
		Created:      now,
		LastEngaged:  now,
		StateChanged: now,
		TurnCount:    1,
	}
}

func validFrontmatter(rec memops.SpineRecord) memops.ThreadMeta {
	return memops.ThreadMeta{
		ID:           rec.ID,
		Project:      rec.Project,
		Anchors:      rec.Anchors,
		Summary:      rec.Summary,
		State:        rec.State,
		Created:      rec.Created,
		LastEngaged:  rec.LastEngaged,
		StateChanged: rec.StateChanged,
		TurnCount:    rec.TurnCount,
	}
}

// TestFileAdapter_ImplementsMemoryOps documents the compile-time
// assertion via the var declaration in fileadapter.go. Having a named
// test ensures the assertion shows up in coverage and is grep-findable.
func TestFileAdapter_ImplementsMemoryOps(t *testing.T) {
	var _ memops.MemoryOps = (*FileAdapter)(nil)
}

func TestCreateThread_WritesSpineAndFile(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	w := memops.ThreadWrite{
		Spine:       rec,
		Meta:        validFrontmatter(rec),
		TurnExcerpt: "## Turn 1\n\ninitial body\n",
	}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	got, found, err := store.FindSpineRecord(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("FindSpineRecord: %v", err)
	}
	if !found {
		t.Fatal("spine record was not appended")
	}
	if got.ID != "thr_1" || got.Project != "prj_1" {
		t.Fatalf("spine record fields wrong: %+v", got)
	}

	if _, err := os.Stat(store.ThreadMetaPath(a.paths, "thr_1")); err != nil {
		t.Fatalf("thread.md not written: %v", err)
	}
	body, err := store.ReadThreadBody(a.paths, "thr_1", 0)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	if !strings.Contains(body, "initial body") {
		t.Fatalf("turn excerpt not written; body=%q", body)
	}
}

func TestCreateThread_DuplicateIDFails(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\n\nfirst\n"}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("first CreateThread: %v", err)
	}
	err := a.CreateThread(ctx, w)
	if !errors.Is(err, memops.ErrDuplicateThreadID) {
		t.Fatalf("expected ErrDuplicateThreadID; got %v", err)
	}
}

func TestEngageThread_UpdatesSpineAndFile(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	rec.TurnCount = 1
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\n\nv1\n"}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("seed CreateThread: %v", err)
	}

	rec.TurnCount = 7
	rec.Summary = "engaged"
	w = memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 7\n\nv2\n"}
	if err := a.EngageThread(ctx, w); err != nil {
		t.Fatalf("EngageThread: %v", err)
	}

	got, _, err := store.FindSpineRecord(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("FindSpineRecord: %v", err)
	}
	if got.TurnCount != 7 || got.Summary != "engaged" {
		t.Fatalf("spine not updated: %+v", got)
	}

	thr, err := store.LoadThread(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if !strings.Contains(thr.Body, "v2") {
		t.Fatalf("thread body not updated; got %q", thr.Body)
	}
}

func TestEngageThread_MissingFileFallback(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	// Seed spine record only by going through CreateThread, then delete
	// the thread file out from under us to simulate drift.
	rec := validSpine("thr_1", "prj_1")
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\n\nv1\n"}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("seed CreateThread: %v", err)
	}
	if err := os.RemoveAll(store.ThreadDir(a.paths, "thr_1")); err != nil {
		t.Fatalf("remove thread dir: %v", err)
	}

	// EngageThread should materialize the file from the supplied
	// frontmatter and body, not fail with ErrThreadFileNotFound.
	rec.TurnCount = 3
	w = memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 3\n\nrebuilt\n"}
	if err := a.EngageThread(ctx, w); err != nil {
		t.Fatalf("EngageThread: %v", err)
	}
	thr, err := store.LoadThread(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThread: %v", err)
	}
	if !strings.Contains(thr.Body, "rebuilt") {
		t.Fatalf("rebuilt body missing; got %q", thr.Body)
	}
}

func TestEngageThread_MissingSpineErrors(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_99", "prj_1")
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec)}
	err := a.EngageThread(ctx, w)
	if !errors.Is(err, memops.ErrThreadNotFound) {
		t.Fatalf("expected ErrThreadNotFound; got %v", err)
	}
}

func TestListThreads_FilterByProject(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	create := func(id, project string) {
		rec := validSpine(id, project)
		w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec)}
		if err := a.CreateThread(ctx, w); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	create("thr_1", "prj_1")
	create("thr_2", "prj_2")
	create("thr_3", "prj_1")

	got, err := a.ListThreads(ctx, memops.ThreadFilter{Project: "prj_1"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 threads in prj_1; got %d (%+v)", len(got), got)
	}
	for _, r := range got {
		if r.Project != "prj_1" {
			t.Fatalf("foreign project leaked into result: %+v", r)
		}
	}
}

func TestProposeRecall_PassThrough(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec)}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Anchors are {alpha, beta, gamma, delta}. Query of two known anchors
	// yields Jaccard 2/4 = 0.5 >= default threshold 0.4.
	cands, err := a.ProposeRecall(ctx, []string{"alpha", "beta"}, memops.RecallOptions{Project: "prj_1"})
	if err != nil {
		t.Fatalf("ProposeRecall: %v", err)
	}
	if len(cands) != 1 || cands[0].ThreadID != "thr_1" {
		t.Fatalf("expected one candidate for thr_1; got %+v", cands)
	}
	if cands[0].Score < 0.4 {
		t.Fatalf("score should clear default threshold; got %v", cands[0].Score)
	}
}

func TestComposeWorkingSet_PassThrough(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	// Seed a project meta and one thread; A1 reads the project's spine
	// records and renders display lines.
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := a.CreateProject(ctx, meta); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	rec := validSpine("thr_1", "prj_1")
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec)}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("seed thread: %v", err)
	}

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{
		ActiveProject: meta,
	})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	if !strings.Contains(out.LayerA1, "thr_1") {
		t.Fatalf("LayerA1 should mention thr_1; got %q", out.LayerA1)
	}
}

func TestEmitDelta_WritesEventLog(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	d := memops.Delta{
		Source:  "user.prompt",
		Content: "hello",
	}
	if err := a.EmitDelta(ctx, d); err != nil {
		t.Fatalf("EmitDelta: %v", err)
	}

	// Look for any *.log under LogsDir that contains "context.modified".
	entries, err := os.ReadDir(a.paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		if strings.Contains(string(data), "context.modified") &&
			strings.Contains(string(data), "source=user.prompt") &&
			strings.Contains(string(data), "bytes=5") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected context.modified line with source and byte count in event log")
	}
}

func TestInit_Idempotent(t *testing.T) {
	paths := store.PathsForHome(t.TempDir())
	a := NewFileAdapter(paths)
	ctx := context.Background()

	if err := a.Init(ctx, memops.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := a.Init(ctx, memops.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("second Init (should be idempotent): %v", err)
	}
	// Substrate should be usable after a second init.
	if _, err := store.ReadSpine(paths.Spine); err != nil {
		t.Fatalf("spine read after re-init: %v", err)
	}
}

// readEventLog returns the concatenated contents of every *.log file
// under the adapter's LogsDir.
func readEventLog(t *testing.T, a *FileAdapter) string {
	t.Helper()
	entries, err := os.ReadDir(a.paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		b.Write(data)
	}
	return b.String()
}

// seedThread creates a thread via the adapter and returns its repo-relative
// dir path. The thread directory is uncommitted in the worktree at this
// point (only `store.Init` committed) — exactly the state archival sees in a
// running session.
func seedThread(t *testing.T, a *FileAdapter, id, project, body string) {
	t.Helper()
	rec := validSpine(id, project)
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: body}
	if err := a.CreateThread(context.Background(), w); err != nil {
		t.Fatalf("seed CreateThread %s: %v", id, err)
	}
}

// snapshotDir reads every file under a thread directory into a name→content
// map so a recovery can be checked byte-for-byte.
func snapshotDir(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

// TestArchiveThenRecover_RoundTrip — the I2 keystone. Archiving a thread
// takes it off the spine and removes its directory, records it in the sorted
// archive index, and logs archive.archived (NOT archive.warning). Recovering
// it puts it back on the spine as wip, restores the directory byte-exact
// (tree-hash verified), and retains the index entry with RecoveredAt stamped.
func TestArchiveThenRecover_RoundTrip(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	body := "## Turn 1\n\nthe thread body\n"
	seedThread(t, a, "thr_1", "prj_1", body)
	dir := store.ThreadDir(a.paths, "thr_1")
	before := snapshotDir(t, dir)
	if len(before) == 0 {
		t.Fatal("seeded thread dir is empty")
	}

	// --- Archive ---
	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}

	if _, found, err := store.FindSpineRecord(a.paths, "thr_1"); err != nil {
		t.Fatalf("FindSpineRecord: %v", err)
	} else if found {
		t.Error("spine record still present after archival")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("thread dir still present after archival: err=%v", err)
	}

	entry, found, err := store.FindArchiveEntry(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("FindArchiveEntry: %v", err)
	}
	if !found {
		t.Fatal("archive index has no entry for thr_1")
	}
	if entry.CommitHash == "" || entry.TreeHash == "" {
		t.Fatalf("archive entry missing commit/tree hash: %+v", entry)
	}
	if entry.OriginalPath != "threads/thr_1" || entry.Project != "prj_1" {
		t.Fatalf("archive entry fields wrong: %+v", entry)
	}
	if entry.RecoveredAt != "" {
		t.Errorf("RecoveredAt should be empty before recovery; got %q", entry.RecoveredAt)
	}

	log := readEventLog(t, a)
	wantLine := "archive.archived thr=thr_1 project=prj_1 bytes=" + strconv.Itoa(len(body))
	if !strings.Contains(log, wantLine) {
		t.Errorf("event log missing %q\n%s", wantLine, log)
	}
	if strings.Contains(log, "archive.warning") {
		t.Errorf("archive.warning line must be gone\n%s", log)
	}
	if strings.Contains(log, "archive.simulated-delete") {
		t.Errorf("archive.simulated-delete line must be gone\n%s", log)
	}

	// --- Recover ---
	rec, err := a.RecoverThread(ctx, "thr_1")
	if err != nil {
		t.Fatalf("RecoverThread: %v", err)
	}
	if rec.ID != "thr_1" || rec.State != memops.ThreadWIP {
		t.Fatalf("recovered spine record wrong: %+v", rec)
	}

	if _, found, err := store.FindSpineRecord(a.paths, "thr_1"); err != nil {
		t.Fatalf("FindSpineRecord after recovery: %v", err)
	} else if !found {
		t.Error("thread not back on spine after recovery")
	}

	after := snapshotDir(t, dir)
	if len(after) != len(before) {
		t.Fatalf("recovered dir file count %d != original %d", len(after), len(before))
	}
	// The git restore is byte-exact (the tree-hash verify in RecoverThread
	// already proved that). Recovery then deliberately re-stamps thread.md
	// with state=wip / last_engaged=now per design §7.2, so thread.md is NOT
	// expected to be byte-identical. Every OTHER file (the turn-excerpt body,
	// files.json) must be byte-exact — that is the recoverable content the
	// archival round-trip exists to preserve.
	for name, content := range before {
		if name == "thread.md" {
			if !strings.Contains(after[name], "state: wip") {
				t.Errorf("recovered thread.md should be state=wip; got %q", after[name])
			}
			continue
		}
		if after[name] != content {
			t.Errorf("file %q not byte-exact after recovery:\n got %q\nwant %q", name, after[name], content)
		}
	}

	// Index entry retained as a breadcrumb with RecoveredAt stamped.
	entry, found, err = store.FindArchiveEntry(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("FindArchiveEntry after recovery: %v", err)
	}
	if !found {
		t.Fatal("archive entry must be retained after recovery (breadcrumb)")
	}
	if entry.RecoveredAt == "" {
		t.Error("RecoveredAt not stamped on recovery")
	}
}

// seedThreadWithDerivedFrom seeds a thread (seedThread) and then overwrites its
// frontmatter so its history_symbols carry the given §2.7.3 DerivedFrom origin
// provenance. Returns the history_symbols slice as written, for the caller to
// compare against the post-recovery frontmatter.
func seedThreadWithDerivedFrom(t *testing.T, a *FileAdapter, id, project string, syms []memops.HistorySymbol) []memops.HistorySymbol {
	t.Helper()
	seedThread(t, a, id, project, "## Turn 1\n\nbody\n")
	fm, err := store.LoadThreadFrontmatter(a.paths, id)
	if err != nil {
		t.Fatalf("seedDerivedFrom load %s: %v", id, err)
	}
	fm.HistorySymbols = syms
	if err := store.SaveThreadFrontmatter(a.paths, id, fm); err != nil {
		t.Fatalf("seedDerivedFrom save %s: %v", id, err)
	}
	a.fmCache.Invalidate(id)
	return syms
}

// assertDerivedFromEqual asserts two history_symbols slices carry identical
// DerivedFrom values per Normalized symbol — exact and order-stable (§2.7.3
// keeps DerivedFrom sorted+deduped). It keys by Normalized so it tolerates the
// slice order being incidental while pinning each symbol's provenance exactly.
func assertDerivedFromEqual(t *testing.T, want, got []memops.HistorySymbol) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("history_symbols count: got %d want %d", len(got), len(want))
	}
	gotByNorm := make(map[string][]string, len(got))
	for _, s := range got {
		gotByNorm[s.Normalized] = s.DerivedFrom
	}
	for _, w := range want {
		g, ok := gotByNorm[w.Normalized]
		if !ok {
			t.Fatalf("recovered frontmatter missing symbol %q", w.Normalized)
		}
		if len(g) != len(w.DerivedFrom) {
			t.Errorf("symbol %q DerivedFrom: got %#v want %#v", w.Normalized, g, w.DerivedFrom)
			continue
		}
		for i := range w.DerivedFrom {
			if g[i] != w.DerivedFrom[i] {
				t.Errorf("symbol %q DerivedFrom[%d]: got %q want %q (order-stable mismatch)", w.Normalized, i, g[i], w.DerivedFrom[i])
			}
		}
	}
}

// TestArchiveThenRecover_PreservesDerivedFrom is the #105 guard: §2.7.3
// symbol-level origin provenance (HistorySymbol.DerivedFrom) lives in plain
// thread.md YAML frontmatter, so the #99 git-tree capture/restore round-trip
// should preserve it verbatim with NO archival code change. This proves it:
// archive a thread whose history_symbols carry DerivedFrom origin ids, recover
// it, and assert every symbol's DerivedFrom survives exact and order-stable.
func TestArchiveThenRecover_PreservesDerivedFrom(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	want := seedThreadWithDerivedFrom(t, a, "thr_1", "prj_1", []memops.HistorySymbol{
		// Carried-in symbol with multiple sorted+deduped origins.
		{Raw: "manifold", Normalized: "manifold", FirstSeenTurn: 3, Count: 7, Source: memops.SourceModel, DerivedFrom: []string{"thr_origin_a", "thr_origin_b"}},
		// Carried-in symbol with a single origin.
		{Raw: "geodesic", Normalized: "geodesic", FirstSeenTurn: 5, Count: 2, Source: memops.SourceUser, DerivedFrom: []string{"thr_origin_c"}},
		// Organic symbol — no provenance (the common case); zero value must
		// stay zero across the round-trip.
		{Raw: "curvature", Normalized: "curvature", FirstSeenTurn: 6, Count: 1, Source: memops.SourceDeterministic},
	})

	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}
	if _, err := a.RecoverThread(ctx, "thr_1"); err != nil {
		t.Fatalf("RecoverThread: %v", err)
	}

	got, err := store.LoadThreadFrontmatter(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThreadFrontmatter after recovery: %v", err)
	}
	assertDerivedFromEqual(t, want, got.HistorySymbols)
}

// TestArchive_DanglingDerivedFromOriginTolerated is the #105 dangling-origin
// guard. DerivedFrom is an opaque string set (§2.7.3 "provenance only; never
// dereferenced") — archiving an origin thread X must not affect a live child
// thread Z whose history_symbols carry DerivedFrom=[X].
//
// (a) is asserted directly: X is archived (off-spine, dir gone) while Z stays
// live, and Z still loads with DerivedFrom=[X] intact — a dangling/archived
// origin reference is tolerated because nothing dereferences it.
//
// (b) "recovering Z never requires X to be present" holds BY CONSTRUCTION and
// is documented rather than re-asserted: RecoverThread resolves Z's bytes from
// Z's own deletion-commit parent and rebuilds Z's spine record from Z's own
// recovered frontmatter (fileadapter_archive.go RecoverThread steps 3-5). It
// never reads, lists, or validates any DerivedFrom origin — the string is
// copied verbatim, never followed. There is no archival API surface where X's
// presence could gate Z's recovery, so (b) is not independently testable
// without inventing a dependency the design deliberately does not have.
func TestArchive_DanglingDerivedFromOriginTolerated(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	// Origin X — plain seeded thread, will be archived.
	seedThread(t, a, "thr_91", "prj_1", "## Turn 1\n\norigin body\n")
	// Child Z — its sole symbol's provenance points at X.
	wantZ := seedThreadWithDerivedFrom(t, a, "thr_92", "prj_1", []memops.HistorySymbol{
		{Raw: "manifold", Normalized: "manifold", FirstSeenTurn: 2, Count: 3, Source: memops.SourceModel, DerivedFrom: []string{"thr_91"}},
	})

	// Archive the origin only; Z stays live.
	if err := a.ArchiveThread(ctx, "thr_91"); err != nil {
		t.Fatalf("ArchiveThread origin: %v", err)
	}
	if _, found, err := store.FindSpineRecord(a.paths, "thr_91"); err != nil {
		t.Fatalf("FindSpineRecord origin: %v", err)
	} else if found {
		t.Error("origin still on spine after archival")
	}
	if _, err := os.Stat(store.ThreadDir(a.paths, "thr_91")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("origin dir still present after archival: err=%v", err)
	}

	// (a) Z still loads fine with its dangling DerivedFrom=[thr_91] intact.
	gotZ, err := store.LoadThreadFrontmatter(a.paths, "thr_92")
	if err != nil {
		t.Fatalf("LoadThreadFrontmatter child after origin archived: %v", err)
	}
	assertDerivedFromEqual(t, wantZ, gotZ.HistorySymbols)
}

// TestArchiveThenRecover_GitignoreAndMode is the F3 regression guard (#101).
// Before the fix the integrity token was computed via WorktreeTreeHash (hashes
// every on-disk file, ignoring .gitignore; derives mode from os.Stat) while the
// capture commit honored .gitignore and CheckoutTree restored at hardcoded
// 0o644. So a thread dir containing (i) a gitignored file or (ii) an executable
// file produced a captured token that a perfectly recoverable thread could not
// reproduce on restore — recovery FALSE-failed with ErrArchiveIntegrity.
//
// This thread dir carries both hazards. Post-fix, recovery must SUCCEED: the
// token is the committed-tree hash (gitignored file excluded, exec bit encoded)
// and the restore preserves committed mode, so the verify-side WorktreeTreeHash
// of the fresh restore matches. The gitignored file legitimately does not come
// back (never committed) and must NOT trip the integrity check.
func TestArchiveThenRecover_GitignoreAndMode(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nthe thread body\n")
	dir := store.ThreadDir(a.paths, "thr_1")

	// Add a .gitignore rule matching a file we drop inside the thread dir, so
	// the capture commit's Add(".") skips it (the file is on disk but never
	// committed — exactly the divergence F3 is about).
	gi, err := os.ReadFile(a.paths.Gitignore)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if err := os.WriteFile(a.paths.Gitignore, append(gi, []byte("\n*.ignoreme\n")...), 0o644); err != nil {
		t.Fatalf("append .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scratch.ignoreme"), []byte("volatile scratch\n"), 0o644); err != nil {
		t.Fatalf("write gitignored file: %v", err)
	}
	// An executable thread file: stat-derived mode (0o755) must match the
	// committed tree-entry mode after restore.
	execBody := "#!/bin/sh\necho hi\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(execBody), 0o755); err != nil {
		t.Fatalf("write executable file: %v", err)
	}

	// --- Archive ---
	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("thread dir still present after archival: err=%v", err)
	}

	// --- Recover --- must NOT FALSE-fail on the integrity check.
	if _, err := a.RecoverThread(ctx, "thr_1"); err != nil {
		t.Fatalf("RecoverThread FALSE-failed (F3 regression): %v", err)
	}

	// The tracked executable file came back byte-exact AND retained its exec bit
	// (proving restore preserved committed mode, not hardcoded 0o644).
	runPath := filepath.Join(dir, "run.sh")
	got, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatalf("read recovered run.sh: %v", err)
	}
	if string(got) != execBody {
		t.Errorf("recovered run.sh not byte-exact: got %q want %q", got, execBody)
	}
	fi, err := os.Stat(runPath)
	if err != nil {
		t.Fatalf("stat recovered run.sh: %v", err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("recovered run.sh lost its executable bit: mode=%v", fi.Mode())
	}

	// The gitignored file was never committed, so it legitimately does not come
	// back — and (the whole point) that absence did not trip the integrity check.
	if _, err := os.Stat(filepath.Join(dir, "scratch.ignoreme")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("gitignored file unexpectedly restored: err=%v", err)
	}
}

// TestArchive_StoresParentCommitHash_RecoveryUsesIt is the F6 guard: archival
// stores the capture commit explicitly as ParentCommitHash (the deletion
// commit's actual parent), and recovery restores from THAT stored hash rather
// than walking the deletion commit's first parent. Pre-fix recovery resolved
// the parent via CommitObject(CommitHash).ParentHashes[0] — an unconditional
// first-parent assumption that a future merge commit would break. We prove the
// stored hash is load-bearing by corrupting CommitHash to a bogus value (so
// any walk-parent path would fail to load the commit) while leaving
// ParentCommitHash valid: recovery must still succeed from the stored parent.
func TestArchive_StoresParentCommitHash_RecoveryUsesIt(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nthe thread body\n")
	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}

	entry, found, err := store.FindArchiveEntry(a.paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("FindArchiveEntry: found=%v err=%v", found, err)
	}
	if entry.ParentCommitHash == "" {
		t.Fatal("ParentCommitHash must be stored at archival (F6)")
	}
	// The stored parent must equal the deletion commit's actual first parent —
	// proving it pins the real capture commit, not some unrelated hash.
	walked, err := autogit.ParentCommitHash(ctx, a.paths, entry.CommitHash)
	if err != nil {
		t.Fatalf("walk parent of deletion commit: %v", err)
	}
	if entry.ParentCommitHash != walked {
		t.Fatalf("stored ParentCommitHash %q != deletion-commit parent %q", entry.ParentCommitHash, walked)
	}

	// Corrupt CommitHash so the walk-parent fallback would fail to load it,
	// then recover: success proves recovery used the stored ParentCommitHash.
	entry.CommitHash = "0000000000000000000000000000000000000000"
	if err := store.AppendArchiveEntries(a.paths, []memops.ArchiveEntry{entry}); err != nil {
		t.Fatalf("rewrite entry with bogus CommitHash: %v", err)
	}
	rec, err := a.RecoverThread(ctx, "thr_1")
	if err != nil {
		t.Fatalf("RecoverThread must succeed via stored ParentCommitHash despite bogus CommitHash: %v", err)
	}
	if rec.ID != "thr_1" || rec.State != memops.ThreadWIP {
		t.Fatalf("recovered record wrong: %+v", rec)
	}
	if _, found, _ := store.FindSpineRecord(a.paths, "thr_1"); !found {
		t.Error("thread not back on spine after recovery via stored parent")
	}
}

// TestRecoverDriftRecord_DistinctOutcome is the F6 empty-TreeHash guard: a
// drift thread (spine record with no on-disk directory) is archived with an
// empty TreeHash. Recovery must take the distinct record-recovery path — it
// re-adds the spine record from the index snapshot, restores NO content, and
// logs archive.recovered-record — rather than running an empty-vs-empty tree
// hash compare that would FALSE-pass as a verified content recovery.
func TestRecoverDriftRecord_DistinctOutcome(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	// Seed a normal thread, then delete its directory out from under the spine
	// to manufacture drift (spine record present, no dir).
	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nx\n")
	if err := os.RemoveAll(store.ThreadDir(a.paths, "thr_1")); err != nil {
		t.Fatalf("remove thread dir: %v", err)
	}

	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread drift: %v", err)
	}
	entry, found, err := store.FindArchiveEntry(a.paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("FindArchiveEntry: found=%v err=%v", found, err)
	}
	if entry.TreeHash != "" {
		t.Fatalf("drift entry must have empty TreeHash; got %q", entry.TreeHash)
	}

	rec, err := a.RecoverThread(ctx, "thr_1")
	if err != nil {
		t.Fatalf("RecoverThread drift: %v", err)
	}
	if rec.ID != "thr_1" || rec.State != memops.ThreadWIP {
		t.Fatalf("recovered drift record wrong: %+v", rec)
	}
	// Spine record is back; no directory was restored (there were no bytes).
	if _, found, _ := store.FindSpineRecord(a.paths, "thr_1"); !found {
		t.Error("drift record not back on spine after recovery")
	}
	if _, serr := os.Stat(store.ThreadDir(a.paths, "thr_1")); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("drift recovery must restore NO directory; stat=%v", serr)
	}
	// The forensic trail distinguishes a record recovery from a content one.
	log := readEventLog(t, a)
	if !strings.Contains(log, "archive.recovered-record thr=thr_1") {
		t.Errorf("expected archive.recovered-record line for drift recovery\n%s", log)
	}
	if strings.Contains(log, "archive.recovered thr=thr_1 ") {
		t.Errorf("drift recovery must NOT log a content-recovery line\n%s", log)
	}
	// Breadcrumb retained with RecoveredAt stamped.
	entry, _, err = store.FindArchiveEntry(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("FindArchiveEntry after recovery: %v", err)
	}
	if entry.RecoveredAt == "" {
		t.Error("RecoveredAt not stamped on drift recovery")
	}
}

// TestRecoverThread_IntegrityFailure — a tampered archived blob fails the
// tree-hash check, returns ErrArchiveIntegrity, and never puts the thread on
// the spine (invariant 2).
func TestRecoverThread_IntegrityFailure(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\noriginal\n")
	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}

	// Corrupt the stored integrity token so the restored (correct) tree no
	// longer matches — the integrity check must fire.
	entry, _, err := store.FindArchiveEntry(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("FindArchiveEntry: %v", err)
	}
	entry.TreeHash = "0000000000000000000000000000000000000000"
	if err := store.AppendArchiveEntries(a.paths, []memops.ArchiveEntry{entry}); err != nil {
		t.Fatalf("tamper index: %v", err)
	}

	_, err = a.RecoverThread(ctx, "thr_1")
	if !errors.Is(err, memops.ErrArchiveIntegrity) {
		t.Fatalf("RecoverThread tampered: got %v, want ErrArchiveIntegrity", err)
	}
	if _, found, ferr := store.FindSpineRecord(a.paths, "thr_1"); ferr != nil {
		t.Fatalf("FindSpineRecord: %v", ferr)
	} else if found {
		t.Error("integrity-failed thread must NOT be on the spine")
	}
	if _, serr := os.Stat(store.ThreadDir(a.paths, "thr_1")); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("partial restore must be removed on integrity failure: stat=%v", serr)
	}
}

// TestRecoverThread_UnknownIDNotFound — recovering a thread with no archive
// index entry returns ErrArchiveEntryNotFound.
func TestRecoverThread_UnknownIDNotFound(t *testing.T) {
	a := newAdapter(t)
	_, err := a.RecoverThread(context.Background(), "thr_999")
	if !errors.Is(err, memops.ErrArchiveEntryNotFound) {
		t.Fatalf("RecoverThread unknown id: got %v, want ErrArchiveEntryNotFound", err)
	}
}

// decayTripsForTest mirrors the ACTIVE-thread read path of
// turn.decayEligible (internal/turn/closure.go): a thread trips the §3.5
// decay scan when it is idle past EITHER the turn-count threshold OR the
// wall-clock threshold. It is replicated here (not imported) to keep this
// adapter test decoupled from package turn; the field semantics it
// exercises — WHICH recency fields the scan reads — are the point of BD-9.
// A stored turn LARGER than the current turnNumber is treated as a prior
// session and the turn signal is skipped, exactly as decayEligible does.
func decayTripsForTest(lastEngagedTurn int, lastEngaged string, turnNumber int, sysRef time.Time) bool {
	const decayTurns = 8
	const decayTime = 7 * 24 * time.Hour
	if turnNumber >= lastEngagedTurn && turnNumber-lastEngagedTurn >= decayTurns {
		return true
	}
	if t, err := time.Parse(time.RFC3339, lastEngaged); err == nil {
		if sysRef.Sub(t) >= decayTime {
			return true
		}
	}
	return false
}

// TestRecoverThread_StampsRecencyForDecay is the BD-9 guard: recovery must
// stamp the recovered spine record's engagement-recency fields so the §3.5
// decay scan cannot immediately re-offer a just-recovered thread for
// retirement, and the rebuilt spine record must agree with the restored
// frontmatter on those fields (a spine↔frontmatter sync violation
// checkThreads — which compares only id/project — would not catch).
func TestRecoverThread_StampsRecencyForDecay(t *testing.T) {
	when := pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	// Seed a thread whose frontmatter carries a realistic pre-archival
	// engagement history: engaged at global session turn 42, having owned 5
	// turns. (LastEngagedTurn is the global session index; TurnCount is the
	// thread's own owned-turn count — different denominations, so 42 vs 5 is
	// normal.)
	const seedEngagedTurn = 42
	const seedTurnCount = 5
	rec := validSpine("thr_1", "prj_1")
	rec.TurnCount = seedTurnCount
	rec.LastEngagedTurn = seedEngagedTurn
	rec.AnchorsProjectedAtTurn = seedTurnCount
	fm := validFrontmatter(rec)
	fm.TurnCount = seedTurnCount
	fm.LastEngagedTurn = seedEngagedTurn
	if err := a.CreateThread(ctx, memops.ThreadWrite{Spine: rec, Meta: fm, TurnExcerpt: "## Turn 1\n\nbody\n"}); err != nil {
		t.Fatalf("seed CreateThread: %v", err)
	}

	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}
	got, err := a.RecoverThread(ctx, "thr_1")
	if err != nil {
		t.Fatalf("RecoverThread: %v", err)
	}

	wantNow := when.Format(time.RFC3339)

	// (1) The returned record stamps all three recency fields.
	if got.LastEngaged != wantNow {
		t.Errorf("recovered LastEngaged: got %q want %q (recovery stamp)", got.LastEngaged, wantNow)
	}
	if got.LastEngagedTurn != seedEngagedTurn {
		t.Errorf("recovered LastEngagedTurn: got %d want %d (mirror restored frontmatter)", got.LastEngagedTurn, seedEngagedTurn)
	}
	if got.AnchorsProjectedAtTurn != seedTurnCount {
		t.Errorf("recovered AnchorsProjectedAtTurn: got %d want %d (project-at-turn-count)", got.AnchorsProjectedAtTurn, seedTurnCount)
	}

	// (2) Persisted spine and frontmatter AGREE on the recency fields — the
	// sync guarantee BD-9 requires at write time.
	spineOut, found, err := store.FindSpineRecord(a.paths, "thr_1")
	if err != nil || !found {
		t.Fatalf("FindSpineRecord after recovery: found=%v err=%v", found, err)
	}
	fmOut, err := store.LoadThreadFrontmatter(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("LoadThreadFrontmatter after recovery: %v", err)
	}
	if spineOut.LastEngagedTurn != fmOut.LastEngagedTurn {
		t.Errorf("spine↔frontmatter LastEngagedTurn diverge: spine=%d fm=%d", spineOut.LastEngagedTurn, fmOut.LastEngagedTurn)
	}
	if spineOut.LastEngaged != fmOut.LastEngaged {
		t.Errorf("spine↔frontmatter LastEngaged diverge: spine=%q fm=%q", spineOut.LastEngaged, fmOut.LastEngaged)
	}
	if spineOut.LastEngagedTurn != seedEngagedTurn {
		t.Errorf("persisted spine LastEngagedTurn: got %d want %d", spineOut.LastEngagedTurn, seedEngagedTurn)
	}
	if spineOut.AnchorsProjectedAtTurn != seedTurnCount {
		t.Errorf("persisted spine AnchorsProjectedAtTurn: got %d want %d", spineOut.AnchorsProjectedAtTurn, seedTurnCount)
	}

	// (3) The decay-scan predicate does NOT trip immediately. Model the
	// realistic post-recovery flow: a fresh session resumes and runs a few
	// turns (turnNumber past decayTurns), then a decay scan runs. sysRef is the
	// recovery stamp (the most-recent system activity). The recovered thread's
	// turn-recency is from the pre-archival session (42 > 10 → prior-session
	// skip) and its wall-clock recency is now, so it must NOT be decay-eligible.
	const freshSessionTurn = 10 // > decayTurns (8), so the bug would bite here
	sysRef := when
	if decayTripsForTest(got.LastEngagedTurn, got.LastEngaged, freshSessionTurn, sysRef) {
		t.Errorf("recovered thread is decay-eligible immediately (turnNumber=%d): LastEngagedTurn=%d LastEngaged=%q",
			freshSessionTurn, got.LastEngagedTurn, got.LastEngaged)
	}
	// Non-vacuity: the old behavior (LastEngagedTurn unstamped → 0) WOULD trip
	// at the same turn, confirming the stamp is what prevents re-retirement.
	if !decayTripsForTest(0, got.LastEngaged, freshSessionTurn, sysRef) {
		t.Errorf("unstamped LastEngagedTurn=0 must trip decay at turnNumber=%d — test is vacuous otherwise", freshSessionTurn)
	}
}

// TestArchiveThreads_Batch — a batch of K threads archives in ONE call:
// every thread leaves the spine and disk, every thread gets a sorted index
// entry sharing one deletion commit, and an unknown id in the batch is a
// recorded skip, not a failure.
func TestArchiveThreads_Batch(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	const k = 4
	ids := make([]string, 0, k)
	for i := 1; i <= k; i++ {
		id := "thr_" + strconv.Itoa(i)
		ids = append(ids, id)
		seedThread(t, a, id, "prj_1", "## Turn 1\n\nbody "+id+"\n")
	}

	// Include an unknown id and a shuffled order to exercise skip + sort.
	req := []string{"thr_3", "thr_999", "thr_1", "thr_4", "thr_2"}
	res, err := a.ArchiveThreads(ctx, req)
	if err != nil {
		t.Fatalf("ArchiveThreads: %v", err)
	}
	if res.CommitHash == "" {
		t.Fatal("batch result missing deletion commit hash")
	}

	archived, skipped := 0, 0
	for _, o := range res.Outcomes {
		switch {
		case o.Archived:
			archived++
		case o.Skipped:
			skipped++
			if o.ThrID != "thr_999" {
				t.Errorf("unexpected skip: %+v", o)
			}
		}
	}
	if archived != k || skipped != 1 {
		t.Fatalf("outcomes: archived=%d skipped=%d (want %d/1)", archived, skipped, k)
	}

	for _, id := range ids {
		if _, found, _ := store.FindSpineRecord(a.paths, id); found {
			t.Errorf("%s still on spine after batch", id)
		}
		if _, serr := os.Stat(store.ThreadDir(a.paths, id)); !errors.Is(serr, os.ErrNotExist) {
			t.Errorf("%s dir still present after batch: %v", id, serr)
		}
	}

	// Index is sorted by thr_id and every archived id shares the one commit.
	idx, err := store.LoadArchiveIndex(a.paths)
	if err != nil {
		t.Fatalf("LoadArchiveIndex: %v", err)
	}
	if len(idx) != k {
		t.Fatalf("index has %d entries, want %d", len(idx), k)
	}
	for i := 1; i < len(idx); i++ {
		if idx[i-1].ThrID >= idx[i].ThrID {
			t.Errorf("index not sorted by thr_id: %q then %q", idx[i-1].ThrID, idx[i].ThrID)
		}
	}
	for _, e := range idx {
		if e.CommitHash != res.CommitHash {
			t.Errorf("entry %s commit %q != batch commit %q", e.ThrID, e.CommitHash, res.CommitHash)
		}
	}
}

// TestArchiveThread_MissingFileSizeZero — a spine record whose thread file
// is absent archives cleanly and logs bytes=0. (Adapted: the log line is
// archive.archived, not archive.simulated-delete.)
func TestArchiveThread_MissingFileSizeZero(t *testing.T) {
	pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nx\n")
	if err := os.RemoveAll(store.ThreadDir(a.paths, "thr_1")); err != nil {
		t.Fatalf("remove thread dir: %v", err)
	}

	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread with missing file: %v", err)
	}
	if !strings.Contains(readEventLog(t, a), "archive.archived thr=thr_1 project=prj_1 bytes=0") {
		t.Errorf("expected bytes=0 for missing thread file\n%s", readEventLog(t, a))
	}
}

// TestArchiveThread_UnknownIDReturnsNotFound — archiving a thread with no
// spine record returns ErrThreadNotFound (the single-thread wrapper maps the
// batch's skip outcome back to the sentinel).
func TestArchiveThread_UnknownIDReturnsNotFound(t *testing.T) {
	a := newAdapter(t)
	err := a.ArchiveThread(context.Background(), "thr_999")
	if !errors.Is(err, memops.ErrThreadNotFound) {
		t.Fatalf("ArchiveThread unknown id: got %v, want ErrThreadNotFound", err)
	}
}

// TestArchiveDeterminism_NoWallClockLeak — under a pinned timeline, the
// archive deletion commit's author time is the pinned instant, not wall
// clock (AC6), and the index ArchivedAt stamp is deterministic.
func TestArchiveDeterminism_NoWallClockLeak(t *testing.T) {
	when := pinClock(t)
	a := newAdapter(t)
	ctx := context.Background()

	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nbody\n")
	res, err := a.ArchiveThreads(ctx, []string{"thr_1"})
	if err != nil {
		t.Fatalf("ArchiveThreads: %v", err)
	}

	repo, err := git.PlainOpen(a.paths.Home)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(res.CommitHash))
	if err != nil {
		t.Fatalf("load deletion commit: %v", err)
	}
	if !commit.Author.When.Equal(when) {
		t.Fatalf("deletion commit author time %v != pinned %v — wall clock leaked", commit.Author.When, when)
	}

	entry, _, err := store.FindArchiveEntry(a.paths, "thr_1")
	if err != nil {
		t.Fatalf("FindArchiveEntry: %v", err)
	}
	if entry.ArchivedAt != when.Format(time.RFC3339) {
		t.Fatalf("ArchivedAt %q != pinned %q", entry.ArchivedAt, when.Format(time.RFC3339))
	}
}

func TestVerify_OKOnFreshInit(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	report, err := a.Verify(ctx)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.HasErrors() {
		t.Fatalf("fresh init should produce no errors; got errors=%+v drift=%+v",
			report.Errors, report.Drift)
	}
}
