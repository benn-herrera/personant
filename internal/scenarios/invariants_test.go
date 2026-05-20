package scenarios

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/store"
)

// invariantHarness builds a Harness pointed at a fresh, valid home —
// no scenario, no metrics writeout, just enough state to exercise
// each invariant individually.
func invariantHarness(t *testing.T) *Harness {
	t.Helper()
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	meta := memops.ProjectMeta{
		ID:               "prj_1",
		Name:             "alpha",
		CurrentRootPath:  tmp,
		Created:          now,
		LastActive:       now,
		ConventionsPaths: []string{},
		SymbolPatterns:   []memops.ProjectPattern{},
		IgnoreSymbols:    []string{},
	}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("SaveProjectMeta: %v", err)
	}
	if err := store.WriteLastActive(paths, meta.ID); err != nil {
		t.Fatalf("WriteLastActive: %v", err)
	}
	if err := store.WriteSpine(paths.Spine, nil); err != nil {
		t.Fatalf("WriteSpine: %v", err)
	}
	// Pre-rebuild so VerifyIndexFresh starts clean.
	if err := indexRebuild(paths); err != nil {
		t.Fatalf("indexRebuild: %v", err)
	}
	return &Harness{
		Paths:             paths,
		Project:           meta,
		T:                 t,
		tailer:            newLogTailer(paths.LogsDir),
		createdThreadIDs:  map[string]struct{}{},
		archivedThreadIDs: map[string]struct{}{},
	}
}

// seedThread writes a paired (spine record, thread file) so tests can
// exercise the cross-reference invariants without driving the runtime.
func seedThread(t *testing.T, h *Harness, rec memops.SpineRecord) {
	t.Helper()
	if err := store.AppendSpineRecord(h.Paths, rec); err != nil {
		t.Fatalf("AppendSpineRecord: %v", err)
	}
	thr := memops.Thread{
		Meta: memops.ThreadMeta{
			ID:           rec.ID,
			Project:      rec.Project,
			Anchors:      append([]string(nil), rec.Anchors...),
			Summary:      rec.Summary,
			State:        rec.State,
			Created:      rec.Created,
			LastEngaged:  rec.LastEngaged,
			StateChanged: rec.StateChanged,
			TurnCount:    rec.TurnCount,
			RecallFires:  rec.RecallFires,
		},
		Body: "# seed\n",
	}
	if err := store.SeedThread(h.Paths, thr); err != nil {
		t.Fatalf("SaveThread: %v", err)
	}
	if err := indexRebuild(h.Paths); err != nil {
		t.Fatalf("indexRebuild: %v", err)
	}
}

func validRecord() memops.SpineRecord {
	return memops.SpineRecord{
		ID:           "thr_1",
		Project:      "prj_1",
		Anchors:      []string{"alpha", "beta", "gamma", "delta"},
		Summary:      "ok",
		State:        memops.ThreadActive,
		Created:      "2026-05-01T12:00:00Z",
		LastEngaged:  "2026-05-01T12:00:00Z",
		StateChanged: "2026-05-01T12:00:00Z",
		TurnCount:    1,
	}
}

func TestVerifySpineIntegrity_Pass(t *testing.T) {
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	if err := VerifySpineIntegrity(h); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestVerifySpineIntegrity_FailsOnTooFewAnchors(t *testing.T) {
	h := invariantHarness(t)
	rec := validRecord()
	rec.Anchors = []string{"a", "b"} // <4
	if err := store.AppendSpineRecord(h.Paths, rec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := VerifySpineIntegrity(h); err == nil {
		t.Fatalf("expected fail; passed")
	}
}

func TestVerifyIndexFresh_PassAfterRebuild(t *testing.T) {
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	if err := VerifyIndexFresh(h); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestVerifyIndexFresh_RebuildHealsDrift(t *testing.T) {
	// VerifyIndexFresh rebuilds-then-checks; even if the on-disk
	// derived files were stale, the rebuild restores them, so the
	// check passes. This exercises the heal path; a separate test for
	// "indices cannot be regenerated" would require a deliberately
	// corrupted spine which is more naturally tested by the sub-checks.
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	// Corrupt symbols.jsonl on disk; VerifyIndexFresh's rebuild step
	// must overwrite it.
	if err := os.WriteFile(h.Paths.Symbols, []byte("garbage\n"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if err := VerifyIndexFresh(h); err != nil {
		t.Fatalf("expected pass after rebuild heal, got %v", err)
	}
}

func TestVerifyProjectReferences_Pass(t *testing.T) {
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	if err := VerifyProjectReferences(h); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestVerifyProjectReferences_FailsOnUnknownProject(t *testing.T) {
	h := invariantHarness(t)
	rec := validRecord()
	rec.Project = "prj_99" // never created
	// Bypass invariants in seedThread (which would re-rebuild and fail
	// on the dangling reference); write spine directly.
	if err := store.AppendSpineRecord(h.Paths, rec); err != nil {
		t.Fatalf("AppendSpineRecord: %v", err)
	}
	thr := memops.Thread{
		Meta: memops.ThreadMeta{
			ID:      rec.ID,
			Project: rec.Project,
			Anchors: rec.Anchors, Summary: rec.Summary, State: rec.State,
			Created: rec.Created, LastEngaged: rec.LastEngaged, StateChanged: rec.StateChanged,
			TurnCount: rec.TurnCount,
		},
	}
	if err := store.SeedThread(h.Paths, thr); err != nil {
		t.Fatalf("save thread: %v", err)
	}
	if err := VerifyProjectReferences(h); err == nil {
		t.Fatalf("expected fail; passed")
	}
}

func TestVerifyProjectReferences_AllowsDefault(t *testing.T) {
	h := invariantHarness(t)
	rec := validRecord()
	rec.Project = store.DefaultProjectID
	seedThread(t, h, rec)
	if err := VerifyProjectReferences(h); err != nil {
		t.Fatalf("prj_default reference should pass: %v", err)
	}
}

func TestVerifyLastActiveValid_PassEmpty(t *testing.T) {
	h := invariantHarness(t)
	if err := os.Remove(h.Paths.LastActive); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove: %v", err)
	}
	if err := VerifyLastActiveValid(h); err != nil {
		t.Fatalf("empty last-active should pass: %v", err)
	}
}

func TestVerifyLastActiveValid_PassKnownProject(t *testing.T) {
	h := invariantHarness(t)
	if err := VerifyLastActiveValid(h); err != nil {
		t.Fatalf("known prj_1 should pass: %v", err)
	}
}

func TestVerifyLastActiveValid_FailsOnUnknownProject(t *testing.T) {
	h := invariantHarness(t)
	if err := os.WriteFile(h.Paths.LastActive, []byte("prj_99\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := VerifyLastActiveValid(h); err == nil {
		t.Fatalf("expected fail; passed")
	}
}

func TestVerifyLastActiveValid_FailsOnMalformed(t *testing.T) {
	h := invariantHarness(t)
	if err := os.WriteFile(h.Paths.LastActive, []byte("not-a-pid\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := VerifyLastActiveValid(h); err == nil {
		t.Fatalf("expected fail; passed")
	}
}

func TestVerifyThreadMetaMatchesSpine_Pass(t *testing.T) {
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	if err := VerifyThreadMetaMatchesSpine(h); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestVerifyThreadMetaMatchesSpine_FailsOnDivergedTurnCount(t *testing.T) {
	h := invariantHarness(t)
	rec := validRecord()
	seedThread(t, h, rec)
	// Mutate the spine record's turn_count without updating the file.
	rec.TurnCount = 99
	if err := store.UpdateSpineRecord(h.Paths, rec); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := VerifyThreadMetaMatchesSpine(h); err == nil {
		t.Fatalf("expected fail on diverged turn_count; passed")
	}
}

func TestVerifyEngagementConsistency_Pass(t *testing.T) {
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	if err := VerifyEngagementConsistency(h); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestVerifyEngagementConsistency_FailsOnDrift(t *testing.T) {
	h := invariantHarness(t)
	rec := validRecord()
	seedThread(t, h, rec)
	rec.TurnCount = 99
	if err := store.UpdateSpineRecord(h.Paths, rec); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := VerifyEngagementConsistency(h); err == nil {
		t.Fatalf("expected fail; passed")
	}
}

func TestVerifyArchiveResolvable_Skipped(t *testing.T) {
	h := invariantHarness(t)
	err := VerifyArchiveResolvable(h)
	var sk skipPhaseErr
	if !errors.As(err, &sk) {
		t.Fatalf("expected skipPhaseErr, got %v", err)
	}
}

func TestVerifyDedupConsistency_Skipped(t *testing.T) {
	h := invariantHarness(t)
	err := VerifyDedupConsistency(h)
	var sk skipPhaseErr
	if !errors.As(err, &sk) {
		t.Fatalf("expected skipPhaseErr, got %v", err)
	}
}

// seedEventLog writes a day-log file and tails+folds it into the
// harness's cumulative created/archived sets — the same sequence
// runStep performs after each turn. Lets the accounting tests exercise
// VerifyThreadAccounting (which now reads the cumulative sets off the
// Harness) without driving a full scenario.
func seedEventLog(t *testing.T, h *Harness, body string) {
	t.Helper()
	writeLogFile(t, h, body)
	lines, err := h.tailer.poll()
	if err != nil {
		t.Fatalf("seedEventLog: tailer.poll: %v", err)
	}
	h.foldEventLines(lines)
}

func TestVerifyThreadAccounting_Pass(t *testing.T) {
	// thr_1 on the spine, thr_2 created then archived → disjoint union holds.
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	seedEventLog(t, h, "ts thread.created thr_1 anchors=4 project=prj_1\n"+
		"ts thread.created thr_2 anchors=4 project=prj_1\n"+
		"ts archive.simulated-delete thr=thr_2 project=prj_1 bytes=512\n")
	if err := VerifyThreadAccounting(h); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

func TestVerifyThreadAccounting_FailsOnUnexplainedLoss(t *testing.T) {
	// thr_2 was created but is neither on the spine nor archived.
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	seedEventLog(t, h, "ts thread.created thr_1 anchors=4 project=prj_1\n"+
		"ts thread.created thr_2 anchors=4 project=prj_1\n")
	if err := VerifyThreadAccounting(h); err == nil {
		t.Fatalf("expected fail on unexplained loss; passed")
	}
}

func TestVerifyThreadAccounting_FailsOnSpineAndArchived(t *testing.T) {
	// thr_1 is on the spine yet also recorded as archived.
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	seedEventLog(t, h, "ts thread.created thr_1 anchors=4 project=prj_1\n"+
		"ts archive.simulated-delete thr=thr_1 project=prj_1 bytes=512\n")
	if err := VerifyThreadAccounting(h); err == nil {
		t.Fatalf("expected fail on simultaneous spine+archived; passed")
	}
}

// TestDefaultInvariantsPassOnFreshHome confirms the entire default
// suite is satisfied by an empty-but-initialized home — the starting
// state for every scenario.
func TestDefaultInvariantsPassOnFreshHome(t *testing.T) {
	h := invariantHarness(t)
	for _, inv := range DefaultInvariants {
		if err := inv(h); err != nil {
			t.Errorf("default invariant failed on fresh home: %v", err)
		}
	}
}

// Sanity: HarnessPaths exposes the spine path; spot-check the file
// exists and is readable as part of the fresh-home setup. Provides
// a runtime contract check at the boundary between invariantHarness
// and the test body — a regression in fixtures would surface here
// before any subtle invariant assertion fires.
func TestInvariantHarnessFixtureWiring(t *testing.T) {
	h := invariantHarness(t)
	if _, err := os.Stat(filepath.Join(h.Paths.Home, "spine.jsonl")); err != nil {
		t.Fatalf("spine.jsonl missing from fresh home: %v", err)
	}
	if h.Paths.Spine == "" {
		t.Fatalf("Paths.Spine empty")
	}
	if h.Project.ID != "prj_1" {
		t.Fatalf("default project not seeded: %+v", h.Project)
	}
}
