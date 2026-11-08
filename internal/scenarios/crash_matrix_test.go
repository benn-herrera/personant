package scenarios

// #94 Wave R4 — the crash-injection matrix's mechanical coverage gate and
// its integration-grade W-TURN / morning-init arming scenarios.
//
// The matrix is split by grade, on purpose:
//
//   - UNIT grade (synthetic homes, adapter/recovery driven directly) lives
//     in the OWNING packages, where the fine per-window assertions belong:
//     internal/memops/fileadapter (TestBarrier_CrashWalk arms all 8 barrier
//     + 7 archiveBatch points; the archival + barrier fixtures),
//     internal/recovery (the cell walks + the reentry arming loop),
//     internal/store (the WriteFileAtomic torn-write/pre-rename arming).
//   - INTEGRATION grade lives HERE: the W-TURN family driven through the
//     real turn pipeline (turn.RunWithInfo), which is the only way to prove
//     the turn/journal/commit crashpoints are correctly PLACED — that a real
//     kill at point X leaves the on-disk state the recovery cells assume —
//     plus the ≤1-turn-loss-vs-oracle and recall-completeness dimensions the
//     unit walks omit; and the two R4-deferred morning-init points.
//
// crashScenarioCoverage below is the greppable registry the coverage gate
// enforces: every crashpoint.Register'd name MUST map to the scenario that
// covers its crash. A future wave that registers a point and forgets the
// scenario fails TestCrashPointCoverageGate — the design's
// registered-point-without-scenario safety net.

import (
	"context"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/object"

	"personant/internal/autogit"
	"personant/internal/crashpoint"
	"personant/internal/dedup"
	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
	"personant/internal/turn"
)

// crashScenarioCoverage maps each registered crashpoint to the crash
// scenario that covers it (arming walk, or the recovery cell whose fixture
// reproduces the crash's on-disk state where arming is a degenerate
// clean-home case). Values are fully-qualified test names so a human/CI can
// grep them. This is the single source of truth the coverage gate enforces
// for completeness; adding a crashpoint without an entry here fails the gate.
var crashScenarioCoverage = map[string]string{
	// W-TURN family — armed here through the real turn pipeline.
	"turn.postJournalPreCanonical":                 "scenarios.TestCrashMatrix_WTurn",
	"turn.betweenCanonicalRenames":                 "scenarios.TestCrashMatrix_WTurn (ArmOnHit k-sweep)",
	"fileadapter.JournalTurn.preAppend":            "scenarios.TestCrashMatrix_WTurn",
	"fileadapter.CommitTurn.postAddPreCommit":      "scenarios.TestCrashMatrix_WTurn",
	"fileadapter.CommitTurn.postCommitPreTruncate": "scenarios.TestCrashMatrix_WTurn",

	// store atomic-write torn windows — armed precisely in the owning
	// package; also crossed by the W-TURN turn (torn canonical write).
	"store.WriteFileAtomic.preRename": "store.TestWriteFileAtomicPreRenameCrash (also scenarios.TestCrashMatrix_WTurn)",
	"store.WriteFileAtomic.tornWrite": "store.TestWriteFileAtomicTornWriteCrash (also scenarios.TestCrashMatrix_WTurn)",

	// Excerpt-tier settle windows — armed inside a live barrier's B1b and
	// in the owning package.
	"folio.settle.midLooseWrite":                 "fileadapter.TestBarrier_CrashWalk (also store.TestFolioSettleCrashConverges)",
	"folio.settle.postLooseWrite.preFolioWrite":  "fileadapter.TestBarrier_CrashWalk (also store.TestFolioSettleCrashConverges)",
	"folio.settle.postFolioWrite.preLooseRemove": "fileadapter.TestBarrier_CrashWalk (also store.TestFolioSettleCrashConverges)",
	"folio.settle.midLooseRemove":                "fileadapter.TestBarrier_CrashWalk (also store.TestFolioSettleCrashConverges)",

	// Tracked-file prune (B1c) windows — armed inside a live barrier and,
	// for the store's own point, in the owning package.
	"barrier.prune.postIndex":        "fileadapter.TestBarrier_CrashWalk (also store.TestPruneTrackedFilesCrashAfterIndexConverges)",
	"barrier.postPrune.preDayCommit": "fileadapter.TestBarrier_CrashWalk",

	// Tracked-file turn write: between the store's blob and diff writes and
	// its index write.
	"fs.applyEvents.preIndex": "scenarios.TestCrashMatrix_WTurnFileEvents",

	// morning-init — the two R4-deferred points, armed here on a greenfield
	// home whose first Reconcile is where daily is born.
	"morninginit.preBaseline":               "scenarios.TestCrashMatrix_MorningInit",
	"morninginit.postBaseline.preWatermark": "scenarios.TestCrashMatrix_MorningInit",

	// Barrier top-level + archiveBatch sub-windows — TestBarrier_CrashWalk
	// arms every one and asserts the roll-forward terminal (F1, one
	// day-commit per day).
	"barrier.preArchival":                      "fileadapter.TestBarrier_CrashWalk",
	"barrier.postArchival.preDayCommit":        "fileadapter.TestBarrier_CrashWalk",
	"barrier.postSettle.preDayCommit":          "fileadapter.TestBarrier_CrashWalk",
	"barrier.postDayCommit.preRebuild":         "fileadapter.TestBarrier_CrashWalk",
	"barrier.postRebuild.preNuke":              "fileadapter.TestBarrier_CrashWalk",
	"barrier.midNuke":                          "fileadapter.TestBarrier_CrashWalk",
	"barrier.postNuke.preInit":                 "fileadapter.TestBarrier_CrashWalk",
	"barrier.postInit.preWatermark":            "fileadapter.TestBarrier_CrashWalk",
	"barrier.postWatermark.preMarkerClear":     "fileadapter.TestBarrier_CrashWalk",
	"barrier.postDayCommitPreTags":             "fileadapter.TestBarrier_CrashWalk (also TestBarrier_LifecycleTags)",
	"archiveBatch.postCapture.preMembership":   "fileadapter.TestBarrier_CrashWalk",
	"archiveBatch.postMembership.preRemove":    "fileadapter.TestBarrier_CrashWalk",
	"archiveBatch.midRemove":                   "fileadapter.TestBarrier_CrashWalk",
	"archiveBatch.postRemove.preSpine":         "fileadapter.TestBarrier_CrashWalk",
	"archiveBatch.postSpine.preDeletionCommit": "fileadapter.TestBarrier_CrashWalk",
	"archiveBatch.postDeletionCommit.preStamp": "fileadapter.TestBarrier_CrashWalk",
	"archiveBatch.postStamp":                   "fileadapter.TestBarrier_CrashWalk",

	// Standalone archival marker windows.
	"archival.postMarker": "fileadapter.TestArchiveMarkerCrash / TestArchiveThreads_StandaloneCrashRollsForward",
	// archival.preMarker: a kill before the marker write leaves a clean,
	// markerless home (cell 1); the standalone roll-forward test owns the
	// mid-batch windows.
	"archival.preMarker": "fileadapter.TestArchiveThreads_StandaloneCrashRollsForward (pre-marker ⇒ clean home / cell 1)",

	// Recovery reentry points — armed by the recovery cell-11 reentry loop.
	"recovery.resetDone.preSweep":      "recovery.TestCell11_CrashInsideRecoveryConverges",
	"recovery.logsRestored.preCleanup": "recovery.TestCell11_CrashInsideRecoveryConverges",
	"recovery.done.preMarkerClear":     "recovery.TestCell11_CrashInsideRecoveryConverges",
	// stamp / adopt reentry: their crash re-detects the unstamped/greenfield
	// state and re-completes idempotently, exercised by the cell fixtures.
	"recovery.stampRepair.preCommit": "recovery.TestCell9_ReentryAbsorbsStampIntoDaily (idempotent stamp re-drive)",
	"recovery.adopt.preCommit":       "recovery.TestCell12_VerifyGateRefusesThenRetryConverges (idempotent adopt re-drive)",
}

// TestCrashPointCoverageGate is the mechanical coverage gate: it fails the suite if any
// registered crashpoint lacks a crash scenario, or if the coverage map
// names a point that is not registered (a stale entry that would silently
// stop protecting anything). Enumeration is complete because the scenarios
// package imports the whole substrate stack (store, turn, memops/fileadapter,
// recovery), so every package-var Register call has run by test time.
func TestCrashPointCoverageGate(t *testing.T) {
	registered := crashpoint.RegisteredNames()
	regSet := make(map[string]bool, len(registered))
	for _, n := range registered {
		regSet[n] = true
	}

	for _, n := range registered {
		if _, ok := crashScenarioCoverage[n]; !ok {
			t.Errorf("registered crashpoint %q has NO crash scenario — add an entry to "+
				"crashScenarioCoverage naming the test that kills there (or reproduces its "+
				"on-disk state) before this point can ship", n)
		}
	}
	for n := range crashScenarioCoverage {
		if !regSet[n] {
			t.Errorf("crashScenarioCoverage names %q, which is not a registered crashpoint — "+
				"stale entry: the point was renamed or removed, so the map no longer protects it", n)
		}
	}

	// Guard against an import regression silently emptying enumeration (a
	// vacuously-green gate is worse than a red one). One canonical point per
	// registering package must be present.
	for _, want := range []string{
		"store.WriteFileAtomic.tornWrite",
		"turn.betweenCanonicalRenames",
		"fileadapter.CommitTurn.postAddPreCommit",
		"barrier.midNuke",
		"archiveBatch.postStamp",
		"recovery.done.preMarkerClear",
		"morninginit.preBaseline",
	} {
		if !regSet[want] {
			t.Errorf("crashpoint %q not registered at gate time — the scenarios package no "+
				"longer links its package; enumeration is incomplete and the gate is vacuous", want)
		}
	}
}

// buildThreeThreadHome runs a fresh scenario that creates thr_1..thr_3 via
// *new-topic* turns and returns the settled harness, ready for one more
// (crashing) turn. Each W-TURN case builds its own so the crash always lands
// on the identical pre-crash baseline.
func buildThreeThreadHome(t *testing.T) *Harness {
	t.Helper()
	sc := Scenario{
		Name: "crash-matrix-wturn-base",
		Steps: []Step{
			{
				UserInput: "first topic",
				MockResponse: NewMockResponseWithTag(
					[]string{"*new-topic*"}, []string{"alpha", "one", "seed"}, "first."),
			},
			{
				UserInput: "second topic",
				MockResponse: NewMockResponseWithTag(
					[]string{"*new-topic*"}, []string{"beta", "two", "seed"}, "second."),
			},
			{
				UserInput: "third topic",
				MockResponse: NewMockResponseWithTag(
					[]string{"*new-topic*"}, []string{"gamma", "three", "seed"}, "third."),
			},
		},
	}
	h := RunScenario(t, sc)
	// Settle the home into a clean R3b state before injecting a crash: a
	// full-sweep Checkpoint commits the setup-time project meta and every
	// thread into daily (per-turn staging is scoped, so setup state is not
	// otherwise in daily HEAD), and a Reconcile stamps the derived watermark
	// against daily HEAD. Without this a torn-turn reset to daily HEAD would
	// sweep the never-committed project meta as in-flight debris — a
	// test-substrate artifact, not a recovery defect.
	ctx := context.Background()
	if err := h.Ops.Checkpoint(ctx, "crash-matrix settle"); err != nil {
		t.Fatalf("settle Checkpoint: %v", err)
	}
	if _, err := fileadapter.NewFileAdapter(h.Paths).Reconcile(ctx); err != nil {
		t.Fatalf("settle Reconcile: %v", err)
	}
	return h
}

// TestCrashMatrix_WTurn is the integration-grade W-TURN matrix: it kills a
// real turn (turn.RunWithInfo) at each turn/journal/commit/atomic-write
// crashpoint, then models the restart (fresh adapter Reconcile) and runs the
// full post-recovery assertion suite, including the ≤1-turn structural-loss
// bound against a pre-crash oracle. turn.betweenCanonicalRenames is swept
// with ArmOnHit(k) over a 3-thread engagement so every torn prefix of the
// turn's canonical write set is exercised.
func TestCrashMatrix_WTurn(t *testing.T) {
	type wcase struct {
		name  string
		point string
		hit   int // ArmOnHit(hit); 0 ⇒ fire on next crossing
	}
	cases := []wcase{
		{"journalPreAppend", "fileadapter.JournalTurn.preAppend", 0},
		{"postJournalPreCanonical", "turn.postJournalPreCanonical", 0},
		{"atomicPreRename", "store.WriteFileAtomic.preRename", 0},
		{"atomicTornWrite", "store.WriteFileAtomic.tornWrite", 0},
		{"betweenRenames.k1", "turn.betweenCanonicalRenames", 1},
		{"betweenRenames.k2", "turn.betweenCanonicalRenames", 2},
		{"betweenRenames.k3", "turn.betweenCanonicalRenames", 3},
		{"commitPostAddPreCommit", "fileadapter.CommitTurn.postAddPreCommit", 0},
		{"commitPostCommitPreTruncate", "fileadapter.CommitTurn.postCommitPreTruncate", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := buildThreeThreadHome(t)
			// Engage all three threads so betweenCanonicalRenames is crossed
			// three times; the single-fire points still fire at their spot.
			h.Mock.SetResponse(NewMockResponseWithTag(
				[]string{"thr_1", "thr_2", "thr_3"},
				[]string{"alpha", "beta", "gamma", "probe"},
				"engaging three threads at once."))
			oracle := SnapshotTurnOracle(t, h.Paths)

			drive := func() {
				_, _, _ = turn.RunWithInfo(context.Background(), h.State, nil, "crash-probe turn", io.Discard)
			}
			var opts []crashpoint.ArmOption
			if c.hit > 0 {
				opts = append(opts, crashpoint.ArmOnHit(c.hit))
			}
			RestartWithCrash(t, h.Paths, c.point, drive, opts...)

			// A fresh adapter over the recovered home; in-memory state is gone.
			AssertPostRecovery(t, h.Paths, fileadapter.NewFileAdapter(h.Paths), &oracle)
		})
	}
}

// TestCrashMatrix_WTurnFileEvents kills a turn that carries fs.read and
// fs.write deltas. After recovery the owner's excerpts and tracked-file
// store match daily HEAD byte for byte, and they agree with each other: the
// turn's identifier line and its store entry are both present (committed)
// or both absent (torn). AssertPostRecovery's verify pass covers the
// tracked-file findings.
func TestCrashMatrix_WTurnFileEvents(t *testing.T) {
	const path = "src/crash.go"
	cases := []struct {
		name      string
		point     string
		hit       int
		committed bool
	}{
		{"applyEventsPreIndex", "fs.applyEvents.preIndex", 0, false},
		{"postJournalPreCanonical", "turn.postJournalPreCanonical", 0, false},
		{"betweenRenames.k1", "turn.betweenCanonicalRenames", 1, false},
		{"commitPostAddPreCommit", "fileadapter.CommitTurn.postAddPreCommit", 0, false},
		{"commitPostCommitPreTruncate", "fileadapter.CommitTurn.postCommitPreTruncate", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := buildThreeThreadHome(t)
			h.Mock.SetResponse(NewMockResponseWithTag(
				[]string{"thr_1", "thr_2", "thr_3"},
				[]string{"alpha", "beta", "gamma", "probe"},
				"editing a file."))
			oracle := SnapshotTurnOracle(t, h.Paths)
			pre := []turn.Delta{
				{Source: memops.SourceFSRead, Content: "v0\n", Meta: map[string]string{"path": path}},
				{Source: memops.SourceFSWrite, Content: "v1\n", Meta: map[string]string{"path": path}},
			}
			drive := func() {
				_, _, _ = turn.RunWithInfo(context.Background(), h.State, pre, "crash-probe file turn", io.Discard)
			}
			var opts []crashpoint.ArmOption
			if c.hit > 0 {
				opts = append(opts, crashpoint.ArmOnHit(c.hit))
			}
			RestartWithCrash(t, h.Paths, c.point, drive, opts...)
			AssertPostRecovery(t, h.Paths, fileadapter.NewFileAdapter(h.Paths), &oracle)

			if got, want := threadStoreAndExcerpts(t, h.Paths, "thr_1", worktreeFile(h.Paths)), threadStoreAndExcerpts(t, h.Paths, "thr_1", dailyHeadFile(t, h.Paths)); !maps.Equal(got, want) {
				t.Errorf("thr_1 excerpts and store differ from daily HEAD:\nworktree %v\ndaily    %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
			entries, err := store.ReadTrackedIndex(h.Paths, "thr_1")
			if err != nil {
				t.Fatalf("read thr_1 index: %v", err)
			}
			body, err := store.ReadThreadBody(h.Paths, "thr_1", 1<<20)
			if err != nil {
				t.Fatalf("read thr_1 body: %v", err)
			}
			hasEntry := slices.ContainsFunc(entries, func(e dedup.FileEntry) bool { return e.Path == path })
			hasLine := strings.Contains(body, "] "+path+"\n")
			if hasEntry != c.committed || hasLine != c.committed {
				t.Errorf("store entry %v, identifier line %v; want both %v", hasEntry, hasLine, c.committed)
			}
		})
	}
}

// threadStoreAndExcerpts returns the bytes of a thread's tracked-file
// index, files under files/, and loose excerpts, keyed by home-relative
// path, as read returns them. Candidates come from both the worktree and
// daily HEAD; a path read reports absent is left out.
func threadStoreAndExcerpts(t *testing.T, paths store.PersonantPaths, id string, read func(rel string) (string, bool)) map[string]string {
	t.Helper()
	rels := map[string]bool{store.TrackedIndexRel(id): true}
	dir := store.ThreadDir(paths, id)
	for _, sub := range []string{"files", "turns"} {
		des, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, d := range des {
			rels["threads/"+id+"/"+sub+"/"+d.Name()] = true
		}
	}
	for _, rel := range dailyHeadPaths(t, paths, "threads/"+id+"/") {
		if strings.HasPrefix(rel, "threads/"+id+"/files") || strings.HasPrefix(rel, "threads/"+id+"/turns/") {
			rels[rel] = true
		}
	}
	out := map[string]string{}
	for rel := range rels {
		if data, ok := read(rel); ok {
			out[rel] = data
		}
	}
	return out
}

func worktreeFile(paths store.PersonantPaths) func(string) (string, bool) {
	return func(rel string) (string, bool) {
		data, err := os.ReadFile(filepath.Join(paths.Home, filepath.FromSlash(rel)))
		return string(data), err == nil
	}
}

func dailyHeadTree(t *testing.T, paths store.PersonantPaths) *object.Tree {
	t.Helper()
	repo, err := autogit.Open(paths, autogit.Daily)
	if err != nil {
		t.Fatalf("open daily: %v", err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("daily HEAD: %v", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("daily HEAD commit: %v", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("daily HEAD tree: %v", err)
	}
	return tree
}

func dailyHeadFile(t *testing.T, paths store.PersonantPaths) func(string) (string, bool) {
	tree := dailyHeadTree(t, paths)
	return func(rel string) (string, bool) {
		f, err := tree.File(rel)
		if err != nil {
			return "", false
		}
		data, err := f.Contents()
		if err != nil {
			t.Fatalf("daily HEAD %s: %v", rel, err)
		}
		return data, true
	}
}

// dailyHeadPaths lists the files of daily HEAD whose path starts with
// prefix.
func dailyHeadPaths(t *testing.T, paths store.PersonantPaths, prefix string) []string {
	t.Helper()
	var out []string
	err := dailyHeadTree(t, paths).Files().ForEach(func(f *object.File) error {
		if strings.HasPrefix(f.Name, prefix) {
			out = append(out, f.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk daily HEAD: %v", err)
	}
	return out
}

// TestCrashMatrix_MorningInit arms the two R4-deferred morning-init points
// on a greenfield home, whose FIRST Reconcile is where the daily DB is born
// (B5+B6 in isolation). A kill mid-morning-init must roll forward on the
// next open to a clean, daily-present, watermark-stamped home.
func TestCrashMatrix_MorningInit(t *testing.T) {
	for _, point := range []string{
		"morninginit.preBaseline",
		"morninginit.postBaseline.preWatermark",
	} {
		t.Run(point, func(t *testing.T) {
			paths := store.PathsForHome(t.TempDir())
			ctx := context.Background()
			// Init births daily alongside a fresh primary, so a first Reconcile
			// is clean, not a morning-init. Settle (Init + Reconcile stamps the
			// watermark), then delete daily: absent-marker + missing-daily +
			// present-watermark is the benign-morning row (§2.5), whose recovery
			// action IS morning-init — the window we want to crash in.
			if err := fileadapter.NewFileAdapter(paths).Init(ctx, memops.InitOptions{Quiet: true}); err != nil {
				t.Fatalf("Init greenfield home: %v", err)
			}
			if _, err := fileadapter.NewFileAdapter(paths).Reconcile(ctx); err != nil {
				t.Fatalf("settle Reconcile: %v", err)
			}
			if err := os.RemoveAll(paths.GitDaily); err != nil {
				t.Fatalf("remove daily: %v", err)
			}
			// The crash lands inside the benign-morning Reconcile's morning-init;
			// the restart's Reconcile completes it.
			drive := func() {
				_, _ = fileadapter.NewFileAdapter(paths).Reconcile(ctx)
			}
			RestartWithCrash(t, paths, point, drive)
			AssertPostRecovery(t, paths, fileadapter.NewFileAdapter(paths), nil)
		})
	}
}
