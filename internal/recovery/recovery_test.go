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

func commitAll(t *testing.T, paths store.PersonantPaths, which autogit.Repo, msg string) string {
	t.Helper()
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, which, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, which, msg, 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return headHash(t, paths, which)
}

func headHash(t *testing.T, paths store.PersonantPaths, which autogit.Repo) string {
	t.Helper()
	h, err := autogit.HeadHash(context.Background(), paths, which)
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
	if head := headHash(t, paths, autogit.Daily); wm != head {
		t.Errorf("watermark %s != daily HEAD %s", wm, head)
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
	commitAll(t, paths, autogit.Daily, "content update")

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
	headBefore := headHash(t, paths, autogit.Daily)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell3HandEdit)
	if rep.ResetPerformed {
		t.Fatal("cell 3 performed a reset — the data-loss defect the design exists to prevent")
	}
	if got := readHome(t, paths, userMDRel); got != sentinel {
		t.Fatalf("hand-edit bytes did not survive: %q", got)
	}
	if headHash(t, paths, autogit.Daily) != headBefore {
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
//
// R3-addendum: the journal appends themselves ARE the in-flight-turn
// signal (MARKER(turn,t2) := non-empty journal, first record t2); no
// marker file is written. The cell MEANINGS are unchanged — every
// assertion below is identical to the marker-file era.
func tornTurnHome(t *testing.T) (store.PersonantPaths, string) {
	t.Helper()
	paths := newHome(t)
	if err := eventlog.Log(paths, "system", "baseline", "committed-line"); err != nil {
		t.Fatalf("eventlog: %v", err)
	}
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, autogit.Daily, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.Daily, autogit.TurnCommitMessage("t1", "baseline"), 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	reconcile(t, paths) // watermark at t1's commit

	committed := readHome(t, paths, userMDRel)

	// In-flight turn t2, torn mid-canonical-write. The first journal
	// append opens the scope — no marker file exists for op=turn.
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
	if turn, _ := autogit.HeadTurn(context.Background(), paths, autogit.Daily); turn != "t1" {
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
	if err := autogit.Add(ctx, paths, autogit.Daily, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.Daily, autogit.TurnCommitMessage("t2", "1 event"), 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Crash window: commit landed, journal never truncated — the
	// non-empty journal is still signalling turn t2 in flight.
	if err := store.AppendJournal(paths, "t2", store.JournalResponse, []byte("already committed")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	headBefore := headHash(t, paths, autogit.Daily)

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
	if headHash(t, paths, autogit.Daily) != headBefore {
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
	if err := store.AppendJournal(paths, "t1", store.JournalPrompt, []byte("PROMPT-ONLY: model call never returned")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	headBefore := headHash(t, paths, autogit.Daily)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell6TurnNoWrites)
	if rep.ResetPerformed {
		t.Error("cell 6 reset a clean tree")
	}
	if headHash(t, paths, autogit.Daily) != headBefore {
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

// TestCell6_TornOnlyJournalQuarantined asserts the F2 fix: a journal whose
// only content is torn/corrupt (no well-formed records but bytes present)
// has its RAW bytes quarantined before phase 7 truncates the live file,
// per "recovery never deletes" — not silently discarded. A non-empty
// journal is the op=turn signal; with an unrecoverable turn id and a clean
// tree it classifies as cell 6.
func TestCell6_TornOnlyJournalQuarantined(t *testing.T) {
	paths := baselineHome(t)
	// One corrupt line WITH a trailing newline: ScanJournal parses it to
	// zero records and counts it torn (records==0, torn==1, bytes present).
	const raw = "{not valid json at all}\n"
	writeHome(t, paths, "turn-journal.jsonl", raw)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell6TurnNoWrites)
	if rep.TornJournalRecords == 0 {
		t.Error("torn journal records not counted")
	}
	// The raw bytes are recoverable byte-exact from quarantine, and the live
	// journal is released — no artifact (there were no parseable records).
	wantQuarantined(t, paths, "turn-journal.jsonl", raw)
	if !eventLogged(t, paths, "recovery.quarantined") {
		t.Error("missing recovery.quarantined event")
	}
	wantJournalEmpty(t, paths)
	wantMarkerAbsent(t, paths)
}

// ---------- cells 7/8/9: archival family ----------

// archivedUnstampedHome builds the exact archival crash-window history
// on PRIMARY (where archival lives under R3b): a capture commit holding
// the thread's bytes, then ONE deletion commit containing both the
// directory removal and the index entry with an empty stamp (in real
// archival the unstamped entries are committed inside the deletion
// commit; the crash hits before the stamp commit). A final DAILY sync
// commit (dailyMsg — lets a caller give it a turn trailer) keeps the
// worktree clean vs daily, mirroring the real batch's daily absorb.
// Returns the deletion commit hash the repair must locate.
func archivedUnstampedHome(t *testing.T, paths store.PersonantPaths, dailyMsg string) string {
	t.Helper()
	writeHome(t, paths, "threads/thr_5/thread.md", "archived thread body\n")
	capture := commitAll(t, paths, autogit.Primary, "archive: capture 1 thread(s)")
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
	deletion := commitAll(t, paths, autogit.Primary, "archive: 1 thread(s)")
	commitAll(t, paths, autogit.Daily, dailyMsg)
	return deletion
}

func TestCell9_StampRepair(t *testing.T) {
	paths := baselineHome(t)
	deletion := archivedUnstampedHome(t, paths, "daily sync")

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
	// The stamp commit is surgical: index file only, committed (and
	// absorbed into daily so the tree stays clean vs daily HEAD).
	wt, err := autogit.Worktree(context.Background(), paths, autogit.Daily)
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
			ParentCommitHash: headHash(t, paths, autogit.Primary), // real commit, but it never held the path
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
	// The daily sync commit carries turn trailer t1 so HEADTURN (a DAILY
	// observable) is well-defined.
	deletion := archivedUnstampedHome(t, paths, autogit.TurnCommitMessage("t1", "archive: 1 thread(s)"))
	committed := readHome(t, paths, userMDRel)

	// Torn turn t2 on top, no reconcile between the two damages. The
	// journal append is the turn signal.
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

// TestCell8_ArchivalDetectionOnly — R3b/F1 supersedes the R2 reset: an
// op=archival marker makes core recovery DETECT (typed Pending, marker
// LEFT IN PLACE, nil error) and never reset or complete — the adapter
// owns the roll-forward completion (fileadapter tests exercise it).
// Detection keys on marker presence ALONE: no unstamped entries exist
// in this fixture, and Pending must still be returned.
func TestCell8_ArchivalDetectionOnly(t *testing.T) {
	paths := baselineHome(t)
	committed := readHome(t, paths, userMDRel)
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpArchival, Day: 42}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	writeHome(t, paths, userMDRel, committed+"MID-ARCHIVAL TORN WRITE\n")

	rep := reconcile(t, paths)
	if rep.Pending == nil || rep.Pending.Kind != memops.PendingArchival {
		t.Fatalf("Pending = %+v, want PendingArchival", rep.Pending)
	}
	if rep.Pending.Day != 42 {
		t.Errorf("Pending.Day = %d, want 42 (marker-day)", rep.Pending.Day)
	}
	if rep.ResetPerformed {
		t.Fatal("detection performed a reset — F1 forbids it (roll-forward only)")
	}
	if len(rep.RevertedPaths) != 0 {
		t.Fatalf("RevertedPaths = %v, want empty on the archival detection path", rep.RevertedPaths)
	}
	// The worktree is untouched and the marker survives as the
	// completion's re-entry token.
	if got := readHome(t, paths, userMDRel); got != committed+"MID-ARCHIVAL TORN WRITE\n" {
		t.Errorf("detection mutated the worktree: %q", got)
	}
	m, present, err := store.ReadMarker(paths)
	if err != nil || !present || m.Op != store.OpArchival || m.Day != 42 {
		t.Fatalf("marker after detection: %+v present=%v err=%v, want intact op=archival day=42", m, present, err)
	}

	// Idempotent: detection again, same answer.
	rep2 := reconcile(t, paths)
	if rep2.Pending == nil || rep2.Pending.Kind != memops.PendingArchival {
		t.Fatalf("second detection Pending = %+v", rep2.Pending)
	}
}

// TestBarrierMarkerDetectionOnly mirrors cell 8 for op=barrier: typed
// Pending carrying the marker-day, marker intact, nothing mutated.
func TestBarrierMarkerDetectionOnly(t *testing.T) {
	paths := baselineHome(t)
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpBarrier, Day: 7}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	headBefore := headHash(t, paths, autogit.Daily)

	rep := reconcile(t, paths)
	if rep.Pending == nil || rep.Pending.Kind != memops.PendingBarrier || rep.Pending.Day != 7 {
		t.Fatalf("Pending = %+v, want PendingBarrier day=7", rep.Pending)
	}
	if rep.ResetPerformed || headHash(t, paths, autogit.Daily) != headBefore {
		t.Error("barrier detection mutated the substrate")
	}
	m, present, err := store.ReadMarker(paths)
	if err != nil || !present || m.Op != store.OpBarrier || m.Day != 7 {
		t.Fatalf("marker after detection: %+v present=%v err=%v", m, present, err)
	}
}

// ---------- cell 10: sleep ----------

func TestCell10_SleepMarker(t *testing.T) {
	paths := baselineHome(t)
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpSleep}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	headBefore := headHash(t, paths, autogit.Daily)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell10Sleep)
	if rep.ResetPerformed || headHash(t, paths, autogit.Daily) != headBefore {
		t.Error("cell 10 must be a pure marker-clear")
	}
	if rep.ClearedOp != "sleep" {
		t.Errorf("ClearedOp = %q", rep.ClearedOp)
	}
	wantMarkerAbsent(t, paths)
}

// TestCell10_SleepPreservesJournal asserts the F1 fix: a non-empty turn
// journal present under an op=sleep marker (the post-CommitTurn pressure-gc
// window — power loss there can resurrect a not-yet-durably-truncated
// journal while turn T's own commit is likewise not durable) is PRESERVED
// before phase 7 truncates it, mirroring cell 8's contract-anomaly
// handling, rather than silently discarded.
func TestCell10_SleepPreservesJournal(t *testing.T) {
	paths := baselineHome(t)
	if err := store.WriteMarker(paths, store.Marker{Op: store.OpSleep}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := store.AppendJournal(paths, "t7", store.JournalPrompt, []byte("SLEEP-WINDOW PROMPT")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
	if err := store.AppendJournal(paths, "t7", store.JournalResponse, []byte("SLEEP-WINDOW RESPONSE")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell10Sleep)
	// The journaled content survives as a surfaced artifact (the anomaly
	// note), keyed to the journal's own turn id...
	if rep.PreservedTurn != "t7" || rep.PreservedContentPath == "" {
		t.Errorf("preserved turn/path = %q/%q, want t7 + a path", rep.PreservedTurn, rep.PreservedContentPath)
	}
	arts := recoveryArtifacts(t, paths)
	if len(arts) != 1 {
		t.Fatalf("artifacts = %v, want one (the preserved sleep-window journal)", arts)
	}
	if content := readHome(t, paths, "recovery/"+filepath.Base(arts[0])); !strings.Contains(content, "SLEEP-WINDOW RESPONSE") {
		t.Errorf("journal bytes missing from artifact:\n%s", content)
	}
	if !eventLogged(t, paths, "recovery.journal-recovered") {
		t.Error("missing recovery.journal-recovered event")
	}
	// ...and the live journal is released.
	wantJournalEmpty(t, paths)
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
	headBefore := headHash(t, paths, autogit.Primary)

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell12LegacyAdopt, CellMorningInit)
	if rep.AdoptCommit == "" {
		t.Fatal("no adopt commit recorded")
	}
	if headHash(t, paths, autogit.Primary) == headBefore {
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

// ---------- F2: Init leaves legacy homes daily-less (#94 R3b) ----------
//
// The real open sequence is Init → Reconcile. Init births the daily ONLY
// on true greenfield (fresh primary); on an existing home lacking a
// daily it must leave it ABSENT, so Reconcile's §2.5 rows and cell-12's
// verify-gated adopt discriminate as designed. An Init-minted baseline
// would commit un-adopted legacy content into a clean-looking daily —
// skipping the cell-12 structural gate entirely (the review's gap) and
// turning the benign-morning stamp-only path into a spurious rebuild.

const brokenSpineLine = `{"id":"thr_bogus","project":"prj_default","anchors":["a"],"summary":"bad","state":"wip","created":"2026-07-17T00:00:00Z","last_engaged":"2026-07-17T00:00:00Z","state_changed":"2026-07-17T00:00:00Z","turn_count":1,"recall_fires":0}` + "\n"

func TestCell12_UpgradedLegacyBrokenSpineRefusedThroughInit(t *testing.T) {
	paths := newHome(t)
	// Regress to the legacy-upgrade shape: primary present, daily gone,
	// watermark never written, structurally broken spine dirty vs primary.
	if err := autogit.NukeDaily(paths); err != nil {
		t.Fatalf("nuke daily: %v", err)
	}
	writeHome(t, paths, "spine.jsonl", brokenSpineLine)
	headBefore := headHash(t, paths, autogit.Primary)

	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	if st := autogit.ProbeDaily(paths); st == autogit.DailyPresent {
		t.Fatal("Init rebirthed the daily on a legacy home (F2) — the cell-12 gate would never see the dirt")
	}

	if _, err := Reconcile(context.Background(), paths); err == nil {
		t.Fatal("broken upgraded-legacy home opened; want cell-12 pre-adopt refusal")
	}
	if headHash(t, paths, autogit.Primary) != headBefore {
		t.Error("refused adopt committed to primary")
	}
}

func TestCell12_CleanLegacyAdoptsThroughInit(t *testing.T) {
	paths := newHome(t)
	if err := autogit.NukeDaily(paths); err != nil {
		t.Fatalf("nuke daily: %v", err)
	}
	const legacy = "legacy uncommitted content\n"
	writeHome(t, paths, userMDRel, legacy)

	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	if st := autogit.ProbeDaily(paths); st == autogit.DailyPresent {
		t.Fatal("Init rebirthed the daily on a legacy home (F2)")
	}

	rep := reconcile(t, paths)
	wantCells(t, rep, Cell12LegacyAdopt, CellMorningInit)
	if rep.AdoptCommit == "" {
		t.Fatal("no adopt commit recorded")
	}
	if got := readHome(t, paths, userMDRel); got != legacy {
		t.Errorf("legacy content damaged by adopt: %q", got)
	}
	wantWatermarkAtHead(t, paths)
	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
}

func TestBenignMorning_StampOnlyThroughInit(t *testing.T) {
	paths := baselineHome(t) // watermark present; derived genuinely fresh
	if err := autogit.NukeDaily(paths); err != nil {
		t.Fatalf("nuke daily: %v", err)
	}
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	if st := autogit.ProbeDaily(paths); st == autogit.DailyPresent {
		t.Fatal("Init rebirthed the daily — benign morning belongs to Reconcile (F2)")
	}

	rep := reconcile(t, paths)
	wantCells(t, rep, CellMorningInit)
	if rep.DerivedRebuilt {
		t.Error("benign morning ran a full rebuild on genuinely fresh derived — want the index.Check stamp-only path")
	}
	wantWatermarkAtHead(t, paths)
	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell1Clean)
}

// TestCell9_ReentryAbsorbsStampIntoDaily — the stampRepair re-entry
// window: a prior pass crashed after its index write (every entry
// stamped, primary stamp commit landed) but before the daily absorb.
// The re-entered pass sees zero unstamped entries and must still re-run
// the scoped absorb — otherwise the index reads as transient cell-3
// dirt on every subsequent open.
func TestCell9_ReentryAbsorbsStampIntoDaily(t *testing.T) {
	paths := baselineHome(t)
	entry := memops.ArchiveEntry{
		ThrID:            "thr_5",
		ParentCommitHash: headHash(t, paths, autogit.Primary),
		CommitHash:       "", // pre-repair: unstamped
		TreeHash:         "x",
		ArchivedAt:       "2026-07-17T00:00:00Z",
		OriginalPath:     "threads/thr_5",
	}
	// The index is TRACKED in daily at its pre-repair (unstamped) state —
	// the real window's shape (the batch's daily absorb carried it there).
	if err := store.AppendArchiveEntries(paths, []memops.ArchiveEntry{entry}); err != nil {
		t.Fatalf("AppendArchiveEntries: %v", err)
	}
	commitAll(t, paths, autogit.Daily, "seed index into daily")
	// The crashed repair pass: index rewritten stamped + primary stamp
	// commit landed; the daily absorb never ran.
	entry.CommitHash = "deadbeef"
	if err := store.AppendArchiveEntries(paths, []memops.ArchiveEntry{entry}); err != nil {
		t.Fatalf("AppendArchiveEntries (stamp): %v", err)
	}
	commitAll(t, paths, autogit.Primary, "recovery: stamp 1 archive entry")

	// Pass 1 classifies the index dirt (cell 3) and the re-entered stamp
	// phase absorbs it into daily — scoped, never a full sweep.
	rep := reconcile(t, paths)
	wantCells(t, rep, Cell3HandEdit)
	wt, err := autogit.Worktree(context.Background(), paths, autogit.Daily)
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if CanonicalDirty(wt.DirtyPaths) {
		t.Errorf("index still dirty vs daily after re-entered absorb: %v", wt.DirtyPaths)
	}
	// The absorb moved daily HEAD; pass 2 re-stamps derived and pass 3 is
	// the clean terminal.
	rep2 := reconcile(t, paths)
	wantCells(t, rep2, Cell2DerivedStale)
	rep3 := reconcile(t, paths)
	wantCells(t, rep3, Cell1Clean)
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

// TestCell5_ResurrectedTruncatedJournal is the truncate-fsync-drop proof
// (R3-addendum item 2): TruncateJournal carries no fsync, so a power
// loss after CommitTurn returns can resurrect the just-committed turn's
// stale-but-truncated journal. That resurrection state is EXACTLY the
// commit-landed-truncate-lost crash window — journal non-empty with
// turn T, HEADTURN==T, clean tree — which is cell 5's already-handled
// redundant-journal case: truncate again, no artifact, zero loss. This
// test constructs the resurrection state directly and asserts cell-5
// handling, which is what licenses dropping the fsync.
func TestCell5_ResurrectedTruncatedJournal(t *testing.T) {
	paths := baselineHome(t)
	writeHome(t, paths, userMDRel, "turn t3 content\n")
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, autogit.Daily, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.Daily, autogit.TurnCommitMessage("t3", ""), 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// The resurrected journal: CommitTurn's truncate happened in the page
	// cache but never reached disk; the pre-truncate bytes are back.
	if err := store.AppendJournal(paths, "t3", store.JournalResponse, []byte("redundant")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	rep := reconcile(t, paths)
	if !slices.Contains(rep.CellsHit, Cell5TurnCommitted) {
		t.Errorf("CellsHit = %v, want cell-5 (resurrected journal is the redundant-journal case)", rep.CellsHit)
	}
	wantJournalEmpty(t, paths)
	if len(recoveryArtifacts(t, paths)) != 0 {
		t.Error("redundant journal content was preserved as an artifact")
	}
	if rep.ResetPerformed {
		t.Error("journal residue triggered a reset")
	}
}

// TestCell6_StaleJournalFromOlderTurnPreservedNotLost covers the other
// resurrection corner: a stale journal whose turn is an OLDER committed
// turn (HEAD has advanced past it — possible only if the resurrected
// truncate predates the last commit's own journal appends, i.e. a
// hand-restored or doubly-stale journal). HEADTURN≠T with a clean tree
// classifies as cell 6: the content is preserved to an artifact
// (redundant with a commit, but preserve-not-guess is the journal
// discipline) and the journal truncated. No reset, no loss.
func TestCell6_StaleJournalFromOlderTurnPreservedNotLost(t *testing.T) {
	paths := baselineHome(t)
	writeHome(t, paths, userMDRel, "turn t9 content\n")
	ctx := context.Background()
	if err := autogit.Add(ctx, paths, autogit.Daily, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.Daily, autogit.TurnCommitMessage("t9", ""), 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := store.AppendJournal(paths, "t2", store.JournalResponse, []byte("stale older-turn bytes")); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	rep := reconcile(t, paths)
	if !slices.Contains(rep.CellsHit, Cell6TurnNoWrites) {
		t.Errorf("CellsHit = %v, want cell-6", rep.CellsHit)
	}
	if rep.ResetPerformed {
		t.Error("stale journal triggered a reset on a clean tree")
	}
	if len(recoveryArtifacts(t, paths)) != 1 {
		t.Error("stale journal content was not preserved")
	}
	wantJournalEmpty(t, paths)
}

// TestLegacyTurnMarkerFileStillHonored: a pre-addendum home can carry an
// op=turn marker FILE (WriteMarker refuses to create one now, so the
// test writes the bytes directly). Recovery still honors it as the turn
// signal and clears it — the read-tolerance contract in store.ReadMarker.
func TestLegacyTurnMarkerFileStillHonored(t *testing.T) {
	paths := baselineHome(t)
	writeHome(t, paths, "op-in-progress.json", `{"op":"turn","turn":"t1"}`)

	rep := reconcile(t, paths)
	if !slices.Contains(rep.CellsHit, Cell6TurnNoWrites) {
		t.Errorf("CellsHit = %v, want cell-6 (legacy marker, clean tree, nothing landed)", rep.CellsHit)
	}
	if rep.ClearedOp != "turn" || rep.ClearedTurn != "t1" {
		t.Errorf("cleared op/turn = %q/%q, want turn/t1", rep.ClearedOp, rep.ClearedTurn)
	}
	wantMarkerAbsent(t, paths)
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
