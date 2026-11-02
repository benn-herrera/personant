package scenarios

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"personant/internal/dedup"
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

func TestVerifyArchiveResolvable_PassOnEmptyIndex(t *testing.T) {
	h := invariantHarness(t)
	if err := VerifyArchiveResolvable(h); err != nil {
		t.Fatalf("empty archive index should pass, got %v", err)
	}
}

func TestVerifyArchiveResolvable_FailsOnUnstampedEntry(t *testing.T) {
	// An entry with an empty commit_hash is the deletion↔stamp crash window:
	// it cannot self-resolve its parent, and RecoverThread refuses it.
	h := invariantHarness(t)
	e := memops.ArchiveEntry{
		ThrID:        "thr_9",
		CommitHash:   "", // un-stamped
		TreeHash:     "cafebabecafebabecafebabecafebabecafebabe",
		ArchivedAt:   "2026-05-05T12:00:00Z",
		OriginalPath: "threads/thr_9",
		Project:      "prj_1",
	}
	if err := store.AppendArchiveEntries(h.Paths, []memops.ArchiveEntry{e}); err != nil {
		t.Fatalf("append archive entry: %v", err)
	}
	if err := VerifyArchiveResolvable(h); err == nil {
		t.Fatalf("expected fail on un-stamped entry; passed")
	}
}

func TestVerifyArchiveResolvable_PassOnDriftRecordEntry(t *testing.T) {
	// A drift entry (empty tree_hash) recovers record-only from the index
	// snapshot — no git tree to resolve — so a stamped one resolves fine.
	h := invariantHarness(t)
	e := memops.ArchiveEntry{
		ThrID:        "thr_9",
		CommitHash:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		TreeHash:     "", // drift: no on-disk body
		ArchivedAt:   "2026-05-05T12:00:00Z",
		OriginalPath: "threads/thr_9",
		Project:      "prj_1",
	}
	if err := store.AppendArchiveEntries(h.Paths, []memops.ArchiveEntry{e}); err != nil {
		t.Fatalf("append archive entry: %v", err)
	}
	if err := VerifyArchiveResolvable(h); err != nil {
		t.Fatalf("drift-record entry should resolve, got %v", err)
	}
}

// seedEventLog writes a day-log file and tails+folds it into the
// harness's cumulative created set — the same sequence runStep performs
// after each turn. Lets the accounting tests exercise
// VerifyThreadAccounting (which reads the cumulative sets off the
// Harness) without driving a full scenario. The archived set is NOT
// driven by log lines (F4/F8) — it is index-derived; use
// seedArchiveEntry to mark a thread archived.
func seedEventLog(t *testing.T, h *Harness, body string) {
	t.Helper()
	writeLogFile(t, h, body)
	lines, err := h.tailer.poll()
	if err != nil {
		t.Fatalf("seedEventLog: tailer.poll: %v", err)
	}
	h.foldEventLines(lines)
}

// seedArchiveEntry writes a canonical archive-index entry for thrID with the
// given RecoveredAt (empty ⇒ currently archived) and refreshes the harness's
// index-derived archived set — the post-F4/F8 source of truth for "archived,
// not yet recovered."
func seedArchiveEntry(t *testing.T, h *Harness, thrID, recoveredAt string) {
	t.Helper()
	e := memops.ArchiveEntry{
		ThrID:        thrID,
		CommitHash:   "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		TreeHash:     "cafebabecafebabecafebabecafebabecafebabe",
		ArchivedAt:   "2026-05-01T12:00:00Z",
		OriginalPath: "threads/" + thrID,
		Project:      "prj_1",
		RecoveredAt:  recoveredAt,
	}
	if err := store.AppendArchiveEntries(h.Paths, []memops.ArchiveEntry{e}); err != nil {
		t.Fatalf("seedArchiveEntry %s: %v", thrID, err)
	}
	h.refreshArchivedSet()
}

func TestVerifyThreadAccounting_Pass(t *testing.T) {
	// thr_1 on the spine, thr_2 created then archived → disjoint union holds.
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	seedEventLog(t, h, "ts thread.created thr_1 anchors=4 project=prj_1\n"+
		"ts thread.created thr_2 anchors=4 project=prj_1\n")
	seedArchiveEntry(t, h, "thr_2", "")
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
	// thr_1 is on the spine yet also recorded as archived (not recovered).
	h := invariantHarness(t)
	seedThread(t, h, validRecord())
	seedEventLog(t, h, "ts thread.created thr_1 anchors=4 project=prj_1\n")
	seedArchiveEntry(t, h, "thr_1", "")
	if err := VerifyThreadAccounting(h); err == nil {
		t.Fatalf("expected fail on simultaneous spine+archived; passed")
	}
}

// TestVerifyThreadAccounting_RecoveredLeavesArchivedSet is the F8 guard: a
// recovered thread's index entry is RETAINED with RecoveredAt stamped, but
// the index-derived archived set excludes RecoveredAt != "" — so a thread
// that is back on the spine AND still has an archive breadcrumb does NOT
// trip the "on-spine and archived" corruption signal, regardless of whether
// the recovery gauge ran before or after the invariant sweep.
func TestVerifyThreadAccounting_RecoveredLeavesArchivedSet(t *testing.T) {
	h := invariantHarness(t)
	seedThread(t, h, validRecord()) // thr_1 back on the spine
	seedEventLog(t, h, "ts thread.created thr_1 anchors=4 project=prj_1\n")
	// thr_1 has a retained archive entry, but it was recovered (RecoveredAt set).
	seedArchiveEntry(t, h, "thr_1", "2026-05-02T12:00:00Z")
	if _, archived := h.archivedThreadIDs["thr_1"]; archived {
		t.Fatal("recovered thread must NOT remain in the archived set (RecoveredAt stamped)")
	}
	if err := VerifyThreadAccounting(h); err != nil {
		t.Fatalf("recovered thread on spine must not trip accounting; got %v", err)
	}
}

// TestVerifyNoBudgetOverflow_PassOnComposedSession drives a real turn (which
// composes the working set) and confirms every rendered layer sits within its
// byte allocation — the §3.1 truncation contract holding on a mock rung. The
// substrate-only invariantHarness has no live session, so this uses a full
// scenario harness whose h.State/h.Ops VerifyNoBudgetOverflow can compose.
func TestVerifyNoBudgetOverflow_PassOnComposedSession(t *testing.T) {
	sc := Scenario{
		Name: "budget-overflow-composed",
		Steps: []Step{
			{
				UserInput: "new topic about topology — #trefoil #unknot #body-topology #electron-shape",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"trefoil", "unknot", "body-topology", "electron-shape"},
					"First pass on topology."),
				Annotation:            "create thr_1; compose working set",
				ExpectedRecallMatches: []string{},
			},
		},
	}
	h := RunScenario(t, sc)
	if err := VerifyNoBudgetOverflow(h); err != nil {
		t.Fatalf("composed layers should be within budget, got %v", err)
	}
}

// cleanExpansionReport is a report every rule passes: one literal per
// path and id, each at its path's latest entry in expansionThreads, all
// paths under LiteralMax, a diff under its gate.
func cleanExpansionReport() memops.FileExpansionReport {
	return memops.FileExpansionReport{
		LiteralMax: 1000,
		Expansions: []memops.FileExpansion{
			{ThreadID: "thr_1", Turn: 3, Project: "prj_1", Path: "a.go", Kind: "literal", ID: "id-a", Size: 500, PathSize: 500},
			{ThreadID: "thr_1", Turn: 3, Project: "prj_1", Path: "a.go", Kind: "diff", From: "id-0", To: "id-a", Size: 349, PathSize: 500},
			{ThreadID: "thr_2", Turn: 1, Project: "prj_1", Path: "b.go", Kind: "literal", ID: "id-b", Size: 999, PathSize: 999},
			{ThreadID: "thr_3", Turn: 1, Project: "prj_2", Path: "a.go", Kind: "literal", ID: "id-c", Size: 10, PathSize: 10},
		},
	}
}

// expansionThreads is the Layer B index cleanExpansionReport and the
// literals the tests add are drawn from.
func expansionThreads() []dedup.PlanThread {
	at := func(h int) time.Time { return time.Date(2026, 5, 1, h, 0, 0, 0, time.UTC) }
	return []dedup.PlanThread{
		{ID: "thr_1", Project: "prj_1", Entries: []dedup.FileEntry{
			{Path: "a.go", ID: "id-a", Size: 500, Turn: 3, At: at(3)},
			{Path: "big.go", ID: "id-big", Size: 1000, Turn: 1, At: at(1)},
			{Path: "copy.go", ID: "id-a", Size: 500, Turn: 2, At: at(2)},
		}},
		{ID: "thr_2", Project: "prj_1", Entries: []dedup.FileEntry{
			{Path: "b.go", ID: "id-b", Size: 999, Turn: 1, At: at(1)},
		}},
		{ID: "thr_3", Project: "prj_2", Entries: []dedup.FileEntry{
			{Path: "a.go", ID: "id-c", Size: 10, Turn: 1, At: at(1)},
		}},
	}
}

// renderedLayerB is a Layer B holding the end marker of every expansion in r.
func renderedLayerB(r memops.FileExpansionReport) string {
	var b strings.Builder
	for _, x := range r.Expansions {
		b.WriteString(expansionEndMarker(x) + "\n")
	}
	return b.String()
}

// checkRendered checks r against a Layer B that renders all of it.
func checkRendered(r memops.FileExpansionReport) error {
	return checkFileExpansions(r, renderedLayerB(r), expansionThreads())
}

func TestFileExpansionCheck_PassesClean(t *testing.T) {
	if err := checkRendered(cleanExpansionReport()); err != nil {
		t.Fatalf("clean report failed: %v", err)
	}
}

func TestFileExpansionCheck_FailsOnMissingEndMarker(t *testing.T) {
	r := cleanExpansionReport()
	for i := range r.Expansions {
		shown := r
		shown.Expansions = slices.Delete(slices.Clone(r.Expansions), i, i+1)
		if err := checkFileExpansions(r, renderedLayerB(shown), expansionThreads()); err == nil {
			t.Errorf("expansion %d reported without its end marker in layer B passed", i)
		}
	}
}

func TestFileExpansionCheck_FailsOnLiteralNotAtLatest(t *testing.T) {
	cases := []struct {
		name  string
		entry dedup.FileEntry // added to thr_1 (prj_1), which precedes thr_2's b.go
	}{
		{"newer entry in another thread", dedup.FileEntry{Path: "b.go", ID: "id-b", Size: 999, Turn: 4, At: time.Date(2026, 5, 1, 2, 0, 0, 0, time.UTC)}},
		{"equal at in an earlier thread", dedup.FileEntry{Path: "b.go", ID: "id-b", Size: 999, Turn: 4, At: time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := cleanExpansionReport()
			threads := expansionThreads()
			threads[0].Entries = append(threads[0].Entries, c.entry)
			if err := checkFileExpansions(r, renderedLayerB(r), threads); err == nil {
				t.Fatal("b.go's literal at thr_2 turn 1 passed with its latest entry at thr_1 turn 4")
			}
		})
	}
	t.Run("no index entry", func(t *testing.T) {
		r := cleanExpansionReport()
		r.Expansions = append(r.Expansions, memops.FileExpansion{
			ThreadID: "thr_1", Turn: 1, Project: "prj_1", Path: "gone.go", Kind: "literal", ID: "id-g", Size: 5, PathSize: 5,
		})
		if err := checkRendered(r); err == nil {
			t.Fatal("a literal of a path no index names passed")
		}
	})
}

func TestFileExpansionCheck_FailsOnTooManyDiffs(t *testing.T) {
	r := cleanExpansionReport()
	for i := range dedup.LiveDiffWindow - 1 {
		r.Expansions = append(r.Expansions, memops.FileExpansion{
			ThreadID: "thr_1", Turn: 2, Project: "prj_1", Path: "a.go", Kind: "diff", From: fmt.Sprintf("id-p%d", i), To: fmt.Sprintf("id-q%d", i), Size: 10, PathSize: 500,
		})
	}
	if err := checkRendered(r); err != nil {
		t.Fatalf("%d diffs of prj_1:a.go failed: %v", dedup.LiveDiffWindow, err)
	}
	r.Expansions = append(r.Expansions, memops.FileExpansion{
		ThreadID: "thr_1", Turn: 1, Project: "prj_1", Path: "a.go", Kind: "diff", From: "id-r", To: "id-s", Size: 10, PathSize: 500,
	})
	if err := checkRendered(r); err == nil {
		t.Fatalf("%d diffs of prj_1:a.go passed", dedup.LiveDiffWindow+1)
	}
}

func TestFileExpansionCheck_FailsOnTwoLiteralsForOnePath(t *testing.T) {
	r := cleanExpansionReport()
	r.Expansions = append(r.Expansions, memops.FileExpansion{
		ThreadID: "thr_1", Turn: 3, Project: "prj_1", Path: "a.go", Kind: "literal", ID: "id-d", Size: 5, PathSize: 5,
	})
	if err := checkRendered(r); err == nil {
		t.Fatal("two literals of prj_1:a.go passed")
	}
}

func TestFileExpansionCheck_FailsOnSharedIDTwice(t *testing.T) {
	r := cleanExpansionReport()
	r.Expansions = append(r.Expansions, memops.FileExpansion{
		ThreadID: "thr_1", Turn: 2, Project: "prj_1", Path: "copy.go", Kind: "literal", ID: "id-a", Size: 500, PathSize: 500,
	})
	if err := checkRendered(r); err == nil {
		t.Fatal("two literals of id-a passed")
	}
}

func TestFileExpansionCheck_FailsOverLimit(t *testing.T) {
	for _, kind := range []string{"literal", "diff"} {
		t.Run(kind, func(t *testing.T) {
			r := cleanExpansionReport()
			r.Expansions = append(r.Expansions, memops.FileExpansion{
				ThreadID: "thr_1", Turn: 1, Project: "prj_1", Path: "big.go", Kind: kind, ID: "id-big", From: "id-x", To: "id-big", Size: 1, PathSize: 1000,
			})
			if err := checkRendered(r); err == nil {
				t.Fatalf("a %s of a path at LiteralMax passed", kind)
			}
		})
	}
}

func TestFileExpansionCheck_FailsOnDiffOverGate(t *testing.T) {
	r := cleanExpansionReport()
	r.Expansions = append(r.Expansions, memops.FileExpansion{
		ThreadID: "thr_1", Turn: 2, Project: "prj_1", Path: "a.go", Kind: "diff", From: "id-x", To: "id-0", Size: 350, PathSize: 500,
	})
	if err := checkRendered(r); err == nil {
		t.Fatal("a diff at 0.7 of its path size passed")
	}
}

// TestVerifyNoBudgetOverflow_NoopWithoutSession confirms the substrate-only
// invariant harness (no h.State/h.Ops) is a clean no-op rather than a panic —
// the guard that lets VerifyNoBudgetOverflow sit in DefaultInvariants without
// breaking substrate-only invariant tests.
func TestVerifyNoBudgetOverflow_NoopWithoutSession(t *testing.T) {
	h := invariantHarness(t)
	if err := VerifyNoBudgetOverflow(h); err != nil {
		t.Fatalf("expected no-op nil without a live session, got %v", err)
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
