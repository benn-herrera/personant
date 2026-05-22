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

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/recall/scoring"
	"personant/internal/store"
)

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
		Meta: validFrontmatter(rec),
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

// TestProposeRecall_IndexFilterEqualsFullScan is the Increment 6 gate:
// the symbols.jsonl candidate-filter path must return byte-identical
// candidates and scores to the full-scan path for any query. The index
// is a superset-correct narrowing — a thread only enters threads ∪
// superseded_in if it shares a query symbol, which is exactly the
// precondition for a non-empty Q ∩ T — so narrowing the loaded
// frontmatter can never drop a real match nor alter a score.
//
// Covers active-match, superseded-match (abandoned premise still in
// history_symbols), and no-match queries against the same substrate.
func TestProposeRecall_IndexFilterEqualsFullScan(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	// Three threads in prj_1 with distinct symbol surfaces:
	//   thr_1: active anchors {alpha, beta} + active history {gamma}
	//   thr_2: anchors {delta} + a SUPERSEDED history symbol {alpha}
	//          (abandoned premise — still matchable via history_symbols)
	//   thr_3: anchors {epsilon, zeta} — shares nothing with the queries
	seed := func(id string, anchors []string, hist []memops.HistorySymbol) {
		rec := validSpine(id, "prj_1")
		rec.Anchors = anchors
		fm := validFrontmatter(rec)
		fm.HistorySymbols = hist
		if err := a.CreateThread(ctx, memops.ThreadWrite{Spine: rec, Meta: fm}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	hist := func(norm string, lc memops.SymbolLifecycle) memops.HistorySymbol {
		return memops.HistorySymbol{Raw: norm, Normalized: norm, Count: 1, Source: memops.SourceModel, Lifecycle: lc}
	}
	seed("thr_1", []string{"alpha", "beta"}, []memops.HistorySymbol{hist("gamma", memops.LifecycleActive)})
	seed("thr_2", []string{"delta"}, []memops.HistorySymbol{hist("alpha", memops.LifecycleSuperseded)})
	seed("thr_3", []string{"epsilon", "zeta"}, nil)

	// Build symbols.jsonl so the candidate-filter path is exercised.
	if err := a.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		t.Fatalf("RegenerateDerivedState: %v", err)
	}

	// Reference (full-scan) candidates: call the pure scorer directly over
	// ALL frontmatter, identical options to ProposeRecall. This is the
	// pre-Inc-6 behavior the index filter must reproduce exactly.
	spine, err := store.ReadSpine(a.paths.Spine)
	if err != nil {
		t.Fatalf("ReadSpine: %v", err)
	}
	allFM, err := store.LoadAllThreadFrontmatter(a.paths, nil, nil)
	if err != nil {
		t.Fatalf("LoadAllThreadFrontmatter: %v", err)
	}

	cases := []struct {
		name  string
		query []string
	}{
		{"active-match", []string{"alpha", "beta"}},       // hits thr_1 (active) + thr_2 (superseded alpha)
		{"superseded-only", []string{"alpha"}},            // thr_1 active alpha + thr_2 superseded alpha
		{"anchor-only", []string{"delta"}},                // thr_2 via anchor
		{"no-match", []string{"omega", "psi"}},            // no thread shares these
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := scoring.ProposeFromIndex(spine, allFM, tc.query, scoring.Options{Project: "prj_1"})

			got, err := a.ProposeRecall(ctx, tc.query, memops.RecallOptions{Project: "prj_1"})
			if err != nil {
				t.Fatalf("ProposeRecall: %v", err)
			}
			if len(got) != len(ref) {
				t.Fatalf("candidate count: index-filtered %d, full-scan %d\n got=%+v\n ref=%+v",
					len(got), len(ref), got, ref)
			}
			for i := range ref {
				if got[i].ThreadID != ref[i].ThreadID {
					t.Errorf("candidate[%d] thread: index-filtered %q, full-scan %q", i, got[i].ThreadID, ref[i].ThreadID)
				}
				if got[i].Score != ref[i].Score {
					t.Errorf("candidate[%d] %s score: index-filtered %v, full-scan %v",
						i, ref[i].ThreadID, got[i].Score, ref[i].Score)
				}
				if strings.Join(got[i].MatchedSymbols, ",") != strings.Join(ref[i].MatchedSymbols, ",") {
					t.Errorf("candidate[%d] %s matched: index-filtered %v, full-scan %v",
						i, ref[i].ThreadID, got[i].MatchedSymbols, ref[i].MatchedSymbols)
				}
			}
		})
	}
}

// TestProposeRecall_MissingIndexDegradesToFullScan asserts the
// robustness contract: with no symbols.jsonl on disk, ProposeRecall
// still returns the correct full-scan result rather than zero candidates
// from an absent index. A derived index is never a correctness hazard.
func TestProposeRecall_MissingIndexDegradesToFullScan(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	rec.Anchors = []string{"alpha", "beta", "gamma", "delta"}
	if err := a.CreateThread(ctx, memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Deliberately do NOT regenerate symbols.jsonl, and remove any that
	// store.Init may have laid down, so recallCandidateIDs sees a miss.
	if err := os.Remove(a.paths.Symbols); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove symbols index: %v", err)
	}

	cands, err := a.ProposeRecall(ctx, []string{"alpha", "beta"}, memops.RecallOptions{Project: "prj_1"})
	if err != nil {
		t.Fatalf("ProposeRecall: %v", err)
	}
	if len(cands) != 1 || cands[0].ThreadID != "thr_1" {
		t.Fatalf("missing index must degrade to full scan and still match thr_1; got %+v", cands)
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

// TestArchiveThread_DeletesSpineAndFileAndLogs — archival removes the
// spine record and thread file and logs archive.simulated-delete with the
// exact body byte count.
func TestArchiveThread_DeletesSpineAndFileAndLogs(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	body := "the thread body\n"
	w := memops.ThreadWrite{
		Spine:       rec,
		Meta: validFrontmatter(rec),
		TurnExcerpt: body,
	}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("seed CreateThread: %v", err)
	}

	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}

	if _, found, err := store.FindSpineRecord(a.paths, "thr_1"); err != nil {
		t.Fatalf("FindSpineRecord: %v", err)
	} else if found {
		t.Error("spine record still present after archival")
	}
	if _, err := os.Stat(store.ThreadDir(a.paths, "thr_1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("thread dir still present after archival: err=%v", err)
	}

	log := readEventLog(t, a)
	wantLine := "archive.simulated-delete thr=thr_1 project=prj_1 bytes=" + strconv.Itoa(len(body))
	if !strings.Contains(log, wantLine) {
		t.Errorf("event log missing %q\n%s", wantLine, log)
	}
	if !strings.Contains(log, "archive.warning") {
		t.Errorf("event log missing archive.warning stub line\n%s", log)
	}
}

// TestArchiveThread_MissingFileSizeZero — a spine record whose thread
// file is absent archives cleanly and logs bytes=0.
func TestArchiveThread_MissingFileSizeZero(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	rec := validSpine("thr_1", "prj_1")
	w := memops.ThreadWrite{Spine: rec, Meta: validFrontmatter(rec), TurnExcerpt: "## Turn 1\n\nx\n"}
	if err := a.CreateThread(ctx, w); err != nil {
		t.Fatalf("seed CreateThread: %v", err)
	}
	if err := os.RemoveAll(store.ThreadDir(a.paths, "thr_1")); err != nil {
		t.Fatalf("remove thread dir: %v", err)
	}

	if err := a.ArchiveThread(ctx, "thr_1"); err != nil {
		t.Fatalf("ArchiveThread with missing file: %v", err)
	}
	if !strings.Contains(readEventLog(t, a), "archive.simulated-delete thr=thr_1 project=prj_1 bytes=0") {
		t.Errorf("expected bytes=0 for missing thread file\n%s", readEventLog(t, a))
	}
}

// TestArchiveThread_UnknownIDReturnsNotFound — archiving a thread with no
// spine record returns ErrThreadNotFound.
func TestArchiveThread_UnknownIDReturnsNotFound(t *testing.T) {
	a := newAdapter(t)
	err := a.ArchiveThread(context.Background(), "thr_999")
	if !errors.Is(err, memops.ErrThreadNotFound) {
		t.Fatalf("ArchiveThread unknown id: got %v, want ErrThreadNotFound", err)
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
