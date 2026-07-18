package recovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"personant/internal/autogit"
	"personant/internal/crashpoint"
	"personant/internal/eventlog"
	"personant/internal/memops"
	"personant/internal/store"
)

// Every state-machine cell is exercised against a synthetic on-disk
// home: construct the cell's exact observable tuple → Reconcile →
// assert actions, report, and invariants → Reconcile again and assert
// idempotence. The homes are real git-backed substrates (store.Init),
// not mocks — recovery's whole job is the disk.

// ---------- fixtures ----------

func newHome(t *testing.T) store.PersonantPaths {
	t.Helper()
	paths := store.PathsForHome(t.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return paths
}

// baselineHome is a home that has been reconciled once (watermark
// present — the post-upgrade steady state every cell except 12 assumes).
func baselineHome(t *testing.T) store.PersonantPaths {
	t.Helper()
	paths := newHome(t)
	rep := reconcile(t, paths)
	wantCells(t, rep, Cell2DerivedStale)
	return paths
}

func reconcile(t *testing.T, paths store.PersonantPaths) memops.RecoveryReport {
	t.Helper()
	rep, err := Reconcile(context.Background(), paths)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return rep
}

func commitAll(t *testing.T, paths store.PersonantPaths, msg string) string {
	t.Helper()
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, msg, 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return headHash(t, paths)
}

func headHash(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	h, err := autogit.HeadHash(context.Background(), paths)
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	return h
}

func writeHome(t *testing.T, paths store.PersonantPaths, rel, content string) string {
	t.Helper()
	abs := filepath.Join(paths.Home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

func readHome(t *testing.T, paths store.PersonantPaths, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(paths.Home, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func homeFileExists(paths store.PersonantPaths, rel string) bool {
	_, err := os.Stat(filepath.Join(paths.Home, filepath.FromSlash(rel)))
	return err == nil
}

func wantCells(t *testing.T, rep memops.RecoveryReport, cells ...string) {
	t.Helper()
	if !reflect.DeepEqual(rep.CellsHit, cells) {
		t.Errorf("CellsHit = %v, want %v", rep.CellsHit, cells)
	}
}

func wantMarkerAbsent(t *testing.T, paths store.PersonantPaths) {
	t.Helper()
	if _, present, err := store.ReadMarker(paths); err != nil || present {
		t.Errorf("marker after reconcile: present=%v err=%v, want absent", present, err)
	}
}

func wantWatermarkAtHead(t *testing.T, paths store.PersonantPaths) {
	t.Helper()
	wm, present, err := store.ReadDerivedWatermark(paths)
	if err != nil || !present {
		t.Fatalf("watermark: present=%v err=%v", present, err)
	}
	if head := headHash(t, paths); wm != head {
		t.Errorf("watermark %s != HEAD %s", wm, head)
	}
}

func wantJournalEmpty(t *testing.T, paths store.PersonantPaths) {
	t.Helper()
	records, torn, err := store.ScanJournal(paths)
	if err != nil || len(records) != 0 || torn != 0 {
		t.Errorf("journal after reconcile: records=%d torn=%d err=%v, want empty", len(records), torn, err)
	}
}

// eventLogged reports whether any daily log contains substr.
func eventLogged(t *testing.T, paths store.PersonantPaths, substr string) bool {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), substr) {
			return true
		}
	}
	return false
}

// recoveryArtifacts lists recovered-turn-*.md files under recovery/.
func recoveryArtifacts(t *testing.T, paths store.PersonantPaths) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(paths.RecoveryDir, "recovered-turn-*.md"))
	if err != nil {
		t.Fatalf("glob artifacts: %v", err)
	}
	return matches
}

const userMDRel = "directives/user.md"

// quarantinedContents returns the bytes of every quarantined copy of the
// home-relative path rel, across all reconcile-timestamp dirs (a crash-
// re-entry sequence legitimately spreads its quarantine over two passes).
func quarantinedContents(t *testing.T, paths store.PersonantPaths, rel string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(paths.RecoveryDir, "quarantine", "*", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("glob quarantine: %v", err)
	}
	var out []string
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			t.Fatalf("read quarantined %s: %v", m, err)
		}
		out = append(out, string(data))
	}
	return out
}

// wantQuarantined asserts rel is recoverable byte-exact from quarantine.
func wantQuarantined(t *testing.T, paths store.PersonantPaths, rel, wantContent string) {
	t.Helper()
	got := quarantinedContents(t, paths, rel)
	if len(got) == 0 {
		t.Errorf("%s not found in quarantine — the destructive path lost bytes", rel)
		return
	}
	if !slices.Contains(got, wantContent) {
		t.Errorf("no quarantined copy of %s is byte-exact: got %q, want %q", rel, got, wantContent)
	}
}

// ---------- cells 1 and 2 ----------

func TestCell2ThenCell1_FreshHome(t *testing.T) {
	paths := newHome(t)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell2DerivedStale)
	if !rep.DerivedRebuilt {
		t.Error("cell 2 did not rebuild derived")
	}
	if !rep.Quiet() {
		t.Errorf("cell 2 should be quiet, got %+v", rep)
	}
	wantWatermarkAtHead(t, paths)
	wantMarkerAbsent(t, paths)

	// Second open: fully clean, O(1) — cell 1, nothing rebuilt.
	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
	if rep2.DerivedRebuilt || !rep2.Quiet() {
		t.Errorf("cell 1 acted: %+v", rep2)
	}
}

func TestCell2_StaleAfterCommit(t *testing.T) {
	paths := baselineHome(t)
	writeHome(t, paths, userMDRel, "new directive content\n")
	commitAll(t, paths, "content update")

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell2DerivedStale)
	if !rep.DerivedRebuilt {
		t.Error("stale watermark did not trigger rebuild")
	}
	wantWatermarkAtHead(t, paths)
}

// ---------- cell 3: THE critical test ----------

// TestCell3_HandEditNeverReset asserts the single most correctness-
// critical rule: a markerless-dirty tree is a legitimate hand-edit and
// its exact bytes survive reconcile — no reset, no commit, no sweep.
func TestCell3_HandEditNeverReset(t *testing.T) {
	paths := baselineHome(t)
	const sentinel = "HAND EDIT SENTINEL — these exact bytes must survive recovery\n"
	writeHome(t, paths, userMDRel, sentinel)
	headBefore := headHash(t, paths)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell3HandEdit)
	if rep.ResetPerformed {
		t.Fatal("cell 3 performed a reset — the data-loss defect the design exists to prevent")
	}
	if got := readHome(t, paths, userMDRel); got != sentinel {
		t.Fatalf("hand-edit bytes did not survive: %q", got)
	}
	if headHash(t, paths) != headBefore {
		t.Error("cell 3 created a commit; the edit must be absorbed by the NEXT turn's commit, not recovery")
	}
	wantMarkerAbsent(t, paths)

	// Idempotence: a second reconcile sees the same dirty tree and makes
	// the same non-destructive call.
	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell3HandEdit)
	if got := readHome(t, paths, userMDRel); got != sentinel {
		t.Fatalf("hand-edit bytes lost on second reconcile: %q", got)
	}
}

// ---------- cells 4/5/6: the turn-marker family ----------

// tornTurnHome builds the cell-4 observable tuple: a baseline committed
// with turn trailer t1, then an in-flight turn t2 that journaled
// prompt+response, dirtied a tracked file, dropped untracked debris,
// and appended an event-log line beyond HEAD — killed before commit.
func tornTurnHome(t *testing.T) (store.PersonantPaths, string) {
	t.Helper()
	paths := newHome(t)
	if err := eventlog.Log(paths, "system", "baseline", "committed-line"); err != nil {
		t.Fatalf("eventlog: %v", err)
	}
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.TurnCommitMessage("t1", "baseline"), 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	reconcile(t, paths) // watermark at t1's commit

	committed := readHome(t, paths, userMDRel)

	// In-flight turn t2, torn mid-canonical-write:
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpTurn, Turn: "t2"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := store.AppendJournal(paths, "t2", store.JournalPrompt, []byte("PROMPT-BYTES: what is the plan?")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	if err := store.AppendJournal(paths, "t2", store.JournalResponse, []byte("RESPONSE-BYTES: the plan is 42")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	writeHome(t, paths, userMDRel, committed+"TORN CANONICAL WRITE\n")
	writeHome(t, paths, "threads/thr_99/thread.md", "in-flight debris\n")
	if err := eventlog.Log(paths, "system", "inflight", "beyond-HEAD-line"); err != nil {
		t.Fatalf("eventlog: %v", err)
	}
	return paths, committed
}

func assertTornTurnRecovered(t *testing.T, paths store.PersonantPaths, committedUserMD string) {
	t.Helper()
	if got := readHome(t, paths, userMDRel); got != committedUserMD {
		t.Errorf("tracked file not reverted: %q", got)
	}
	if homeFileExists(paths, "threads/thr_99/thread.md") {
		t.Error("untracked debris survived the sweep")
	}
	if !eventLogged(t, paths, "committed-line") || !eventLogged(t, paths, "beyond-HEAD-line") {
		t.Error("event-log lines lost across the reset (logs/ exemption violated)")
	}
	arts := recoveryArtifacts(t, paths)
	if len(arts) != 1 {
		t.Fatalf("artifacts = %v, want exactly one", arts)
	}
	content := readHome(t, paths, "recovery/"+filepath.Base(arts[0]))
	if !strings.Contains(content, "PROMPT-BYTES: what is the plan?") ||
		!strings.Contains(content, "RESPONSE-BYTES: the plan is 42") {
		t.Errorf("artifact missing journaled bytes:\n%s", content)
	}
	wantJournalEmpty(t, paths)
	wantMarkerAbsent(t, paths)
	wantWatermarkAtHead(t, paths)
	if turn, _ := autogit.HeadTurn(context.Background(), paths); turn != "t1" {
		t.Errorf("HEADTURN = %q, want t1 (t2 must be rolled back, ≤1-turn loss)", turn)
	}
	// Non-lossy destructive path: the swept debris file and the pre-reset
	// bytes of the dirty tracked file are recoverable byte-exact from
	// quarantine.
	wantQuarantined(t, paths, "threads/thr_99/thread.md", "in-flight debris\n")
	wantQuarantined(t, paths, userMDRel, committedUserMD+"TORN CANONICAL WRITE\n")
}

func TestCell4_TornTurn(t *testing.T) {
	paths, committed := tornTurnHome(t)
	// Hand-created while the marker was stale (crash → user adds a file →
	// reopen): indistinguishable from op debris, so it IS swept — but it
	// must land in quarantine byte-exact, never be deleted.
	const handNote = "created while the marker was stale\n"
	writeHome(t, paths, "hand-note.md", handNote)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell4TornTurn)
	if !rep.ResetPerformed {
		t.Fatal("cell 4 did not reset")
	}
	if len(rep.RevertedPaths) == 0 || rep.RevertedPaths[0] != userMDRel {
		t.Errorf("RevertedPaths = %v, want to include %s first", rep.RevertedPaths, userMDRel)
	}
	if !reflect.DeepEqual(rep.DebrisSwept, []string{"hand-note.md", "threads/thr_99/thread.md"}) {
		t.Errorf("DebrisSwept = %v", rep.DebrisSwept)
	}
	if !strings.HasPrefix(rep.QuarantineDir, "recovery/quarantine/") {
		t.Errorf("QuarantineDir = %q, want under recovery/quarantine/", rep.QuarantineDir)
	}
	for _, rel := range []string{userMDRel, "hand-note.md", "threads/thr_99/thread.md"} {
		if !slices.Contains(rep.Quarantined, rel) {
			t.Errorf("Quarantined = %v, missing %s", rep.Quarantined, rel)
		}
	}
	if homeFileExists(paths, "hand-note.md") {
		t.Error("hand-created file survived the sweep in place (tree must converge to the recovery point)")
	}
	wantQuarantined(t, paths, "hand-note.md", handNote)
	if !eventLogged(t, paths, "recovery.quarantined") {
		t.Error("missing recovery.quarantined event")
	}
	if rep.PreservedTurn != "t2" || rep.PreservedContentPath == "" {
		t.Errorf("preserved turn/path = %q/%q", rep.PreservedTurn, rep.PreservedContentPath)
	}
	if rep.ClearedOp != "turn" || rep.ClearedTurn != "t2" {
		t.Errorf("cleared op/turn = %q/%q", rep.ClearedOp, rep.ClearedTurn)
	}
	if rep.Quiet() {
		t.Error("cell 4 must be banner-visible")
	}
	assertTornTurnRecovered(t, paths, committed)
	if !eventLogged(t, paths, "recovery.rollback") || !eventLogged(t, paths, "recovery.journal-recovered") {
		t.Error("missing recovery.rollback / recovery.journal-recovered events")
	}

	// Idempotence: the second open is a clean cell 1, and the terminal
	// state is unchanged.
	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
	assertTornTurnRecovered(t, paths, committed)
}

func TestCell5_TurnCommittedPreClear(t *testing.T) {
	paths := baselineHome(t)
	writeHome(t, paths, userMDRel, "turn t2 content\n")
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.TurnCommitMessage("t2", "1 event"), 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Crash window: commit landed, marker + journal never released.
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpTurn, Turn: "t2"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := store.AppendJournal(paths, "t2", store.JournalResponse, []byte("already committed")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	headBefore := headHash(t, paths)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell5TurnCommitted)
	if rep.ResetPerformed {
		t.Error("cell 5 reset a committed turn")
	}
	if rep.PreservedContentPath != "" || len(recoveryArtifacts(t, paths)) != 0 {
		t.Error("cell 5 preserved redundant journal content (the commit already holds it)")
	}
	if got := readHome(t, paths, userMDRel); got != "turn t2 content\n" {
		t.Errorf("committed turn content damaged: %q — cell 5 must be ZERO loss", got)
	}
	if headHash(t, paths) != headBefore {
		t.Error("cell 5 moved HEAD")
	}
	wantJournalEmpty(t, paths)
	wantMarkerAbsent(t, paths)

	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
}

func TestCell6_CrashDuringModelCall_TrailerAbsent(t *testing.T) {
	// Baseline HEAD carries NO turn trailer (a pre-R3 history): HEADTURN
	// is ⊥, which must read as "turn t1 not committed" — cell 6, never a
	// false cell 5.
	paths := baselineHome(t)
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpTurn, Turn: "t1"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := store.AppendJournal(paths, "t1", store.JournalPrompt, []byte("PROMPT-ONLY: model call never returned")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	headBefore := headHash(t, paths)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell6TurnNoWrites)
	if rep.ResetPerformed {
		t.Error("cell 6 reset a clean tree")
	}
	if headHash(t, paths) != headBefore {
		t.Error("cell 6 moved HEAD")
	}
	arts := recoveryArtifacts(t, paths)
	if len(arts) != 1 {
		t.Fatalf("artifacts = %v, want one (the prompt)", arts)
	}
	if content := readHome(t, paths, "recovery/"+filepath.Base(arts[0])); !strings.Contains(content, "PROMPT-ONLY") {
		t.Errorf("prompt bytes missing from artifact:\n%s", content)
	}
	if rep.PreservedTurn != "t1" {
		t.Errorf("PreservedTurn = %q", rep.PreservedTurn)
	}
	wantJournalEmpty(t, paths)
	wantMarkerAbsent(t, paths)

	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
	if got := recoveryArtifacts(t, paths); len(got) != 1 {
		t.Errorf("second reconcile changed artifacts: %v", got)
	}
}

// ---------- cells 7/8/9: archival family ----------

// archivedUnstampedHome builds the exact archival crash-window history:
// a capture commit holding the thread's bytes, then ONE deletion commit
// containing both the directory removal and the index entry with an
// empty stamp (in real archival the unstamped entries are committed
// inside the deletion commit; the crash hits before the stamp commit).
// deletionMsg lets a caller give the deletion commit a turn trailer.
// Returns the deletion commit hash the repair must locate.
func archivedUnstampedHome(t *testing.T, paths store.PersonantPaths, deletionMsg string) string {
	t.Helper()
	writeHome(t, paths, "threads/thr_5/thread.md", "archived thread body\n")
	capture := commitAll(t, paths, "archive: capture 1 thread(s)")
	if err := os.RemoveAll(filepath.Join(paths.Home, "threads", "thr_5")); err != nil {
		t.Fatalf("remove thread dir: %v", err)
	}
	if err := store.AppendArchiveEntries(paths, []memops.ArchiveEntry{{
		ThrID:            "thr_5",
		ParentCommitHash: capture,
		CommitHash:       "", // the crash window: stamp never landed
		TreeHash:         "irrelevant-here",
		ArchivedAt:       "2026-07-17T00:00:00Z",
		OriginalPath:     "threads/thr_5",
	}}); err != nil {
		t.Fatalf("AppendArchiveEntries: %v", err)
	}
	return commitAll(t, paths, deletionMsg)
}

func TestCell9_StampRepair(t *testing.T) {
	paths := baselineHome(t)
	deletion := archivedUnstampedHome(t, paths, "archive: 1 thread(s)")

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell2DerivedStale, Cell9StampRepair)
	if !reflect.DeepEqual(rep.StampRepaired, []string{"thr_5"}) {
		t.Fatalf("StampRepaired = %v", rep.StampRepaired)
	}
	entry, found, err := store.FindArchiveEntry(paths, "thr_5")
	if err != nil || !found {
		t.Fatalf("FindArchiveEntry: found=%v err=%v", found, err)
	}
	if entry.CommitHash != deletion {
		t.Errorf("stamped %s, want deletion commit %s", entry.CommitHash, deletion)
	}
	if !eventLogged(t, paths, "recovery.stamp-repaired") {
		t.Error("missing recovery.stamp-repaired event")
	}
	// The stamp commit is surgical: index file only, committed.
	wt, err := autogit.Worktree(context.Background(), paths)
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for _, p := range wt.DirtyPaths {
		if !strings.HasPrefix(p, "logs/") {
			t.Errorf("dirt left after stamp repair: %v", wt.DirtyPaths)
			break
		}
	}
	wantWatermarkAtHead(t, paths)

	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
}

func TestCell9_UnrepairableRefusedNeverGuessed(t *testing.T) {
	paths := baselineHome(t)
	if err := store.AppendArchiveEntries(paths, []memops.ArchiveEntry{
		{
			ThrID:        "thr_7",
			CommitHash:   "",
			TreeHash:     "x",
			ArchivedAt:   "2026-07-17T00:00:00Z",
			OriginalPath: "threads/thr_7",
			// No ParentCommitHash: nothing to anchor the locate walk.
		},
		{
			ThrID:            "thr_8",
			ParentCommitHash: headHash(t, paths), // real commit, but it never held the path
			CommitHash:       "",
			TreeHash:         "x",
			ArchivedAt:       "2026-07-17T00:00:00Z",
			OriginalPath:     "threads/never-existed",
		},
	}); err != nil {
		t.Fatalf("AppendArchiveEntries: %v", err)
	}

	rep := reconcile(t, paths) // must SUCCEED — unrepairable is refused, not fatal
	if !reflect.DeepEqual(rep.Unrepairable, []string{"thr_7", "thr_8"}) {
		t.Fatalf("Unrepairable = %v", rep.Unrepairable)
	}
	if len(rep.StampRepaired) != 0 {
		t.Errorf("StampRepaired = %v, want none", rep.StampRepaired)
	}
	for _, id := range []string{"thr_7", "thr_8"} {
		entry, found, err := store.FindArchiveEntry(paths, id)
		if err != nil || !found {
			t.Fatalf("FindArchiveEntry %s: found=%v err=%v", id, found, err)
		}
		if entry.CommitHash != "" {
			t.Errorf("%s was stamped with %q — a GUESSED hash; must stay refused", id, entry.CommitHash)
		}
	}
	if !eventLogged(t, paths, "recovery.unrepairable") {
		t.Error("missing recovery.unrepairable event")
	}
	if rep.Quiet() {
		t.Error("unrepairable entries must be banner-visible")
	}
}

// TestCell7_TornTurnCoincidingWithUnstamped: the archival crash window
// (deletion committed, stamp never landed) followed — with NO reconcile
// in between — by a torn turn. One reconcile must run the full cell-4
// sequence AND the cell-9 stamp pass, in that order (ARCH is evaluated
// post-normalization, on the reset tree, where the unstamped entry sits
// committed at HEAD).
func TestCell7_TornTurnCoincidingWithUnstamped(t *testing.T) {
	paths := newHome(t)
	// Deletion commit carries turn trailer t1 so HEADTURN is well-defined.
	deletion := archivedUnstampedHome(t, paths, autogit.TurnCommitMessage("t1", "archive: 1 thread(s)"))
	committed := readHome(t, paths, userMDRel)

	// Torn turn t2 on top, no reconcile between the two damages.
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpTurn, Turn: "t2"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := store.AppendJournal(paths, "t2", store.JournalPrompt, []byte("cell-7 prompt")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	writeHome(t, paths, userMDRel, committed+"TORN\n")

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell4TornTurn, Cell9StampRepair)
	if !rep.ResetPerformed {
		t.Error("cell 7 did not reset the torn turn")
	}
	if !reflect.DeepEqual(rep.StampRepaired, []string{"thr_5"}) {
		t.Errorf("StampRepaired = %v", rep.StampRepaired)
	}
	entry, found, err := store.FindArchiveEntry(paths, "thr_5")
	if err != nil || !found || entry.CommitHash != deletion {
		t.Errorf("entry stamp = %q found=%v err=%v, want %s", entry.CommitHash, found, err, deletion)
	}
	if got := readHome(t, paths, userMDRel); got != committed {
		t.Errorf("torn write survived: %q", got)
	}
	wantJournalEmpty(t, paths)
	wantMarkerAbsent(t, paths)
	wantWatermarkAtHead(t, paths)

	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
}

func TestCell8_ArchivalDirty(t *testing.T) {
	paths := baselineHome(t)
	committed := readHome(t, paths, userMDRel)
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpArchival}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	writeHome(t, paths, userMDRel, committed+"MID-ARCHIVAL TORN WRITE\n")
	writeHome(t, paths, "archive/debris.tmpfile", "uncommitted archival residue\n")
	// Hand-created during the stale marker window — swept, but recoverable.
	const handNote = "idle-window hand file\n"
	writeHome(t, paths, "hand-note.md", handNote)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell8Archival)
	if !rep.ResetPerformed {
		t.Fatal("cell 8 did not reset")
	}
	if got := readHome(t, paths, userMDRel); got != committed {
		t.Errorf("tracked file not reverted: %q", got)
	}
	if homeFileExists(paths, "archive/debris.tmpfile") || homeFileExists(paths, "hand-note.md") {
		t.Error("archival debris survived")
	}
	if rep.ClearedOp != "archival" {
		t.Errorf("ClearedOp = %q", rep.ClearedOp)
	}
	// Everything the destructive pass touched is recoverable byte-exact:
	// the dirty tracked file's pre-reset bytes and both untracked files.
	wantQuarantined(t, paths, userMDRel, committed+"MID-ARCHIVAL TORN WRITE\n")
	wantQuarantined(t, paths, "archive/debris.tmpfile", "uncommitted archival residue\n")
	wantQuarantined(t, paths, "hand-note.md", handNote)
	for _, rel := range []string{userMDRel, "archive/debris.tmpfile", "hand-note.md"} {
		if !slices.Contains(rep.Quarantined, rel) {
			t.Errorf("Quarantined = %v, missing %s", rep.Quarantined, rel)
		}
	}
	wantMarkerAbsent(t, paths)
	wantWatermarkAtHead(t, paths)

	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
}

// ---------- cell 10: sleep ----------

func TestCell10_SleepMarker(t *testing.T) {
	paths := baselineHome(t)
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpSleep}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	headBefore := headHash(t, paths)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell10Sleep)
	if rep.ResetPerformed || headHash(t, paths) != headBefore {
		t.Error("cell 10 must be a pure marker-clear")
	}
	if rep.ClearedOp != "sleep" {
		t.Errorf("ClearedOp = %q", rep.ClearedOp)
	}
	wantMarkerAbsent(t, paths)
}

// ---------- cell 11: re-entrancy ----------

func TestCell11_HandcraftedReentryMarker(t *testing.T) {
	paths, committed := tornTurnHome(t)
	// Model "recovery engaged, then died before acting": the observed
	// turn marker was already replaced by the recovery marker.
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpRecovery, Orig: "turn", Turn: "t2"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}

	rep := reconcile(t, paths)
	if len(rep.CellsHit) == 0 || rep.CellsHit[0] != Cell11Reentry {
		t.Fatalf("CellsHit = %v, want cell-11 first", rep.CellsHit)
	}
	if !rep.ResetPerformed {
		t.Error("re-entered torn turn did not reset")
	}
	assertTornTurnRecovered(t, paths, committed)
}

// TestCell11_CrashInsideRecoveryConverges is the load-bearing
// re-entrancy test: kill recovery at its own registered crashpoints,
// then run it again and assert the terminal state is identical to the
// crash-free run — including the forensic log lines the reset window
// makes vulnerable.
func TestCell11_CrashInsideRecoveryConverges(t *testing.T) {
	points := []string{
		"recovery.resetDone.preSweep",
		"recovery.logsRestored.preCleanup",
		"recovery.done.preMarkerClear",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			paths, committed := tornTurnHome(t)

			disarm := crashpoint.Arm(point)
			func() {
				defer func() {
					r := recover()
					if r == nil {
						t.Fatalf("armed crashpoint %s did not fire", point)
					}
					if _, ok := r.(*crashpoint.Crash); !ok {
						panic(r) // a real bug — rethrow
					}
				}()
				_, _ = Reconcile(context.Background(), paths)
			}()
			disarm()

			// The recovery marker must be holding the original context.
			m, present, err := store.ReadMarker(paths)
			if err != nil || !present || m.Op != store.OpRecovery {
				t.Fatalf("marker after crash: %+v present=%v err=%v, want op=recovery", m, present, err)
			}
			if m.Orig != "turn" || m.Turn != "t2" {
				t.Fatalf("recovery marker lost orig context: %+v", m)
			}

			// Second pass converges to the crash-free terminal state.
			rep := reconcile(t, paths)
			if len(rep.CellsHit) == 0 || rep.CellsHit[0] != Cell11Reentry {
				t.Errorf("CellsHit = %v, want cell-11 first", rep.CellsHit)
			}
			assertTornTurnRecovered(t, paths, committed)

			// And a third pass is a clean no-op.
			rep3 := reconcile(t, paths)
			wantCells(t, rep3, Cell1Clean)
		})
	}
}

// ---------- cell 12: greenfield / legacy ----------

func TestCell12_LegacyAdoptForward(t *testing.T) {
	paths := newHome(t) // NO baseline reconcile: watermark has never existed
	const legacy = "legacy uncommitted content turn\n"
	writeHome(t, paths, userMDRel, legacy)
	headBefore := headHash(t, paths)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell12LegacyAdopt)
	if rep.AdoptCommit == "" {
		t.Fatal("no adopt commit recorded")
	}
	if headHash(t, paths) == headBefore {
		t.Error("adopt did not commit")
	}
	if got := readHome(t, paths, userMDRel); got != legacy {
		t.Errorf("legacy content damaged by adopt: %q", got)
	}
	if !rep.DerivedRebuilt {
		t.Error("cell 12 must run the one-time full rebuild")
	}
	wantWatermarkAtHead(t, paths)
	wantMarkerAbsent(t, paths)
	if !eventLogged(t, paths, "recovery.adopt") {
		t.Error("missing recovery.adopt event")
	}

	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
}

func TestCell12_VerifyGateRefusesThenRetryConverges(t *testing.T) {
	paths := newHome(t)
	// Structurally invalid spine (bad id pattern) — the adopt gate must
	// refuse rather than commit corruption forward.
	writeHome(t, paths, "spine.jsonl",
		`{"id":"thr_bogus","project":"prj_default","anchors":["a"],"summary":"bad","state":"wip","created":"2026-07-17T00:00:00Z","last_engaged":"2026-07-17T00:00:00Z","state_changed":"2026-07-17T00:00:00Z","turn_count":1,"recall_fires":0}`+"\n")

	_, err := Reconcile(context.Background(), paths)
	if err == nil {
		t.Fatal("adopt of a structurally-broken home succeeded; want refusal")
	}
	// The failure leaves the recovery marker for idempotent retry.
	m, present, merr := store.ReadMarker(paths)
	if merr != nil || !present || m.Op != store.OpRecovery {
		t.Fatalf("marker after refusal: %+v present=%v err=%v, want op=recovery", m, present, merr)
	}

	// Operator fixes the spine; the retry re-enters and converges.
	writeHome(t, paths, "spine.jsonl", "")
	rep := reconcile(t, paths)
	if len(rep.CellsHit) == 0 || rep.CellsHit[0] != Cell11Reentry {
		t.Errorf("CellsHit = %v, want cell-11 first on retry", rep.CellsHit)
	}
	wantMarkerAbsent(t, paths)
	wantWatermarkAtHead(t, paths)
}

// ---------- orthogonal phases ----------

func TestTmpResidueSweptUnconditionally(t *testing.T) {
	paths := baselineHome(t) // fully clean home — no marker cell fires
	writeHome(t, paths, "tmp-spine.jsonl", "half-written atomic temp\n")
	writeHome(t, paths, "threads/thr_3/tmp-thread.md", "torn temp in canonical namespace\n")
	// The user scratch dir is exempt even for tmp- names.
	writeHome(t, paths, "tmp/tmp-scratch.md", "user scratch\n")

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell1Clean) // no marker, clean, fresh — yet the sweep ran
	if !reflect.DeepEqual(rep.TmpSwept, []string{"threads/thr_3/tmp-thread.md", "tmp-spine.jsonl"}) {
		t.Errorf("TmpSwept = %v", rep.TmpSwept)
	}
	if homeFileExists(paths, "tmp-spine.jsonl") || homeFileExists(paths, "threads/thr_3/tmp-thread.md") {
		t.Error("tmp- residue survived")
	}
	if !homeFileExists(paths, "tmp/tmp-scratch.md") {
		t.Error("user scratch under tmp/ was swept")
	}
	// Swept ≠ deleted: both residues are recoverable byte-exact.
	wantQuarantined(t, paths, "tmp-spine.jsonl", "half-written atomic temp\n")
	wantQuarantined(t, paths, "threads/thr_3/tmp-thread.md", "torn temp in canonical namespace\n")
	if rep.Quiet() {
		t.Error("a sweep that removed files must be banner-visible")
	}
}

// TestTmpSweepScope_HandCreatedPreserved — F3: the tmp- sweep is scoped
// to the substrate's atomic-write residue pattern. A tmp-* file inside a
// canonical-namespace dir is indistinguishable from residue and is
// quarantined (recoverable, never deleted); one outside the pattern —
// no sibling target, not in a substrate-written dir — is not ours and
// survives untouched.
func TestTmpSweepScope_HandCreatedPreserved(t *testing.T) {
	paths := baselineHome(t)
	writeHome(t, paths, "threads/thr_2/tmp-draft.md", "hand-created draft\n")
	writeHome(t, paths, "notes/tmp-ideas.md", "hand-created ideas\n")

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell1Clean)
	if !reflect.DeepEqual(rep.TmpSwept, []string{"threads/thr_2/tmp-draft.md"}) {
		t.Errorf("TmpSwept = %v", rep.TmpSwept)
	}
	if homeFileExists(paths, "threads/thr_2/tmp-draft.md") {
		t.Error("canonical-namespace tmp- residue not swept")
	}
	wantQuarantined(t, paths, "threads/thr_2/tmp-draft.md", "hand-created draft\n")
	if !homeFileExists(paths, "notes/tmp-ideas.md") {
		t.Error("hand-created tmp-* outside the residue pattern was swept")
	}
}

func TestLogTailHealedAdditively(t *testing.T) {
	paths := baselineHome(t)
	if err := eventlog.Log(paths, "system", "ok", "complete-line"); err != nil {
		t.Fatalf("eventlog: %v", err)
	}
	// Tear the tail: append a partial line with no newline.
	logs, err := filepath.Glob(filepath.Join(paths.LogsDir, "*.log"))
	if err != nil || len(logs) == 0 {
		t.Fatalf("no log files: %v", err)
	}
	f, err := os.OpenFile(logs[0], os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := f.WriteString("2026-07-17T00:00:00Z system.torn half-writ"); err != nil {
		t.Fatalf("append torn: %v", err)
	}
	f.Close()
	before, _ := os.ReadFile(logs[0])

	rep := reconcile(t, paths)
	if len(rep.LogTailsHealed) != 1 {
		t.Fatalf("LogTailsHealed = %v", rep.LogTailsHealed)
	}
	after, _ := os.ReadFile(logs[0])
	// Additive: everything that was there is still there, byte for byte,
	// plus at least the healing newline (recovery events may follow).
	if !strings.HasPrefix(string(after), string(before)+"\n") {
		t.Error("heal was not additive (bytes truncated or rewritten)")
	}
}

func TestMarkerlessJournalResidueTruncated(t *testing.T) {
	// The CommitTurn crash window between marker-clear and journal-
	// truncate: markerless, clean, journal non-empty. The content is
	// redundant with the committed turn — truncate, no artifact.
	paths := baselineHome(t)
	if err := store.AppendJournal(paths, "t3", store.JournalResponse, []byte("redundant")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	rep := reconcile(t, paths)
	wantJournalEmpty(t, paths)
	if len(recoveryArtifacts(t, paths)) != 0 {
		t.Error("redundant journal content was preserved as an artifact")
	}
	if rep.ResetPerformed {
		t.Error("journal residue triggered a reset")
	}
}

func TestCorruptMarkerRefusesOpen(t *testing.T) {
	paths := baselineHome(t)
	writeHome(t, paths, "op-in-progress.json", "{not json")

	_, err := Reconcile(context.Background(), paths)
	if err == nil {
		t.Fatal("corrupt marker did not refuse the open")
	}
	// The corrupt file is left untouched for forensics.
	if got := readHome(t, paths, "op-in-progress.json"); got != "{not json" {
		t.Errorf("corrupt marker was modified: %q", got)
	}
}

// ---------- pure helpers ----------

// TestReentryMergeNeverChopsLogLines — F2 regression: kill recovery at
// resetDone.preSweep (reset already truncated the live log), re-enter —
// the re-entered pass appends its begin event BEFORE restoring the
// preserved log bytes, so live and preserved diverge on lines that share
// a timestamp byte-prefix. Without the newline-boundary rounding in
// mergeLogBytes, the byte-wise common prefix ends mid-timestamp and the
// merge emits a head-chopped fragment. Assert every merged line is
// well-formed ('<RFC3339> <event> ...') and no preserved line was lost.
func TestReentryMergeNeverChopsLogLines(t *testing.T) {
	paths, committed := tornTurnHome(t)

	disarm := crashpoint.Arm("recovery.resetDone.preSweep")
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("armed crashpoint did not fire")
			}
			if _, ok := r.(*crashpoint.Crash); !ok {
				panic(r)
			}
		}()
		_, _ = Reconcile(context.Background(), paths)
	}()
	disarm()

	reconcile(t, paths) // re-entry: appends begin, then merges preserved logs
	assertTornTurnRecovered(t, paths, committed)

	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		for i, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				t.Errorf("%s line %d malformed (head-chopped by merge?): %q", e.Name(), i+1, line)
				continue
			}
			if _, perr := time.Parse(time.RFC3339, fields[0]); perr != nil {
				t.Errorf("%s line %d does not start with an RFC3339 timestamp: %q", e.Name(), i+1, line)
			}
		}
	}
	if !eventLogged(t, paths, "committed-line") || !eventLogged(t, paths, "beyond-HEAD-line") {
		t.Error("preserved log line lost across the re-entry merge")
	}
}

func TestMergeLogBytes(t *testing.T) {
	cases := []struct {
		name        string
		cur, pres   string
		want        string
		wantChanged bool
	}{
		{"equal", "a\nb\n", "a\nb\n", "a\nb\n", false},
		{"cur-extends-preserved", "a\nb\nc\n", "a\nb\n", "a\nb\nc\n", false},
		{"reset-truncated", "a\n", "a\nb\nc\n", "a\nb\nc\n", true},
		{"target-absent", "", "a\n", "a\n", true},
		{"diverged-appends-tail", "a\nX\n", "a\nY\n", "a\nX\nY\n", true},
		{"already-merged-tail", "a\nX\nY\n", "a\nY\n", "a\nX\nY\n", false},
		// F2: diverging lines share a byte prefix (near-identical
		// timestamps) — the cut must round down to the line boundary so
		// the preserved line is appended whole, never head-chopped.
		{"diverged-mid-line", "t a\nt2 X\n", "t a\nt3 Y\n", "t a\nt2 X\nt3 Y\n", true},
		{"diverged-first-line-mid", "t2 X\n", "t3 Y\n", "t2 X\nt3 Y\n", true},
		{"cur-torn-tail-capped", "a\nhalf", "a\nb\n", "a\nhalf\nb\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := mergeLogBytes([]byte(tc.cur), []byte(tc.pres))
			if string(got) != tc.want || changed != tc.wantChanged {
				t.Errorf("mergeLogBytes(%q,%q) = (%q,%v), want (%q,%v)",
					tc.cur, tc.pres, got, changed, tc.want, tc.wantChanged)
			}
		})
	}
}

func TestDecodeOrigRejectsUnknown(t *testing.T) {
	if _, _, err := decodeOrig(store.Marker{Op: store.OpRecovery, Orig: "mystery"}); err == nil {
		t.Error("unknown orig accepted")
	}
	eff, present, err := decodeOrig(store.Marker{Op: store.OpRecovery, Orig: "none"})
	if err != nil || present || eff.Op != "" {
		t.Errorf("orig=none: (%+v,%v,%v)", eff, present, err)
	}
}
