package fileadapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"personant/internal/autogit"
	"personant/internal/clock"
	"personant/internal/crashpoint"
	"personant/internal/eventlog"
	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/recovery"
	"personant/internal/store"
)

// #94 R3b §7: barrier fixtures — mid-barrier on-disk states constructed
// on synthetic homes (by armed crashpoints or directly), driven through
// the ADAPTER's Reconcile (the completion owner), asserting the
// worktree-as-truth invariants: no reset ever (RevertedPaths ALWAYS
// empty on barrier/archival fixtures), ScopedRestored non-empty ONLY on
// the §2.7 persistent-failure fixture, exactly one day-commit per day
// (INV-2), batch completion never half-archived (F1), daily reborn with
// watermark == daily HEAD (INV-4), derived fresh, idempotent second
// Reconcile.

// pinBarrierClock pins Timeline at an explicit instant (the multi-day
// counterpart to pinClock's fixed value).
func pinBarrierClock(t *testing.T, when time.Time) {
	t.Helper()
	restore := clock.SetTimeline(func() time.Time { return when })
	t.Cleanup(restore)
}

var (
	bDay0 = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	bDay1 = time.Date(2026, 5, 30, 9, 0, 0, 0, time.UTC)
	bDay2 = time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
)

// newBarrierHome stands up an Init'd home under bDay0 with `retired`
// retired threads (coldest-first thr_1..) and reconciles it once so the
// watermark exists. Returns the adapter.
func newBarrierHome(t *testing.T, retired int) *FileAdapter {
	t.Helper()
	pinBarrierClock(t, bDay0)
	a := newAdapter(t)
	ctx := context.Background()
	for i := 1; i <= retired; i++ {
		id := fmt.Sprintf("thr_%d", i)
		seedThread(t, a, id, "prj_1", "## Turn 1\n\nbody "+id+"\n")
		rec, found, err := store.FindSpineRecord(a.paths, id)
		if err != nil || !found {
			t.Fatalf("find seeded %s: %v", id, err)
		}
		rec.State = memops.ThreadResolved
		// Coldest-first by seed order.
		rec.StateChanged = fmt.Sprintf("2026-03-%02dT%02d:00:00Z", (i/24)+1, i%24)
		if err := store.UpdateSpineRecord(a.paths, rec); err != nil {
			t.Fatalf("retire %s: %v", id, err)
		}
	}
	if _, err := a.Reconcile(ctx); err != nil {
		t.Fatalf("baseline Reconcile: %v", err)
	}
	return a
}

// dayCommitCount walks primary history counting DayCommitMessage-shape
// commits for `day`. It also asserts NO personant-day/* tag exists (F3
// removed the tag path entirely).
func dayCommitCount(t *testing.T, a *FileAdapter, day int) int {
	t.Helper()
	repo, err := autogit.Open(a.paths, autogit.Primary)
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("primary HEAD: %v", err)
	}
	iter, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	defer iter.Close()
	count := 0
	subject := "day " + fmt.Sprint(day)
	for {
		c, cerr := iter.Next()
		if cerr != nil {
			break
		}
		first, _, _ := strings.Cut(c.Message, "\n")
		if strings.TrimSpace(first) == subject {
			if d, ok := autogit.ParseDayTrailer(c.Message); ok && d == day {
				count++
			}
		}
	}
	if tags, terr := repo.Tags(); terr == nil {
		_ = tags.ForEach(func(ref *plumbing.Reference) error {
			if strings.Contains(ref.Name().String(), "personant-day") {
				t.Errorf("personant-day tag %s exists — the tag path is GONE (F3)", ref.Name())
			}
			return nil
		})
	}
	return count
}

// assertBarrierTerminal asserts the post-completion invariants shared by
// every barrier fixture: exactly one day-commit for `day`, HEADDAY
// defined, daily present and clean with watermark == daily HEAD, derived
// fresh, and a second Reconcile that is a clean no-op.
func assertBarrierTerminal(t *testing.T, a *FileAdapter, day int) {
	t.Helper()
	ctx := context.Background()
	if n := dayCommitCount(t, a, day); n != 1 {
		t.Errorf("day-commit count for day %d = %d, want exactly 1 (INV-2)", day, n)
	}
	if got, ok, err := autogit.HeadDay(ctx, a.paths, autogit.Primary); err != nil || !ok {
		t.Errorf("HEADDAY undefined after barrier: ok=%v err=%v (F3)", ok, err)
	} else if got < day {
		t.Errorf("HEADDAY = %d, want >= %d", got, day)
	}
	if st := autogit.ProbeDaily(a.paths); st != autogit.DailyPresent {
		t.Fatalf("daily state after barrier = %v, want present", st)
	}
	dailyHead, err := autogit.HeadHash(ctx, a.paths, autogit.Daily)
	if err != nil {
		t.Fatalf("daily HEAD: %v", err)
	}
	wm, present, err := store.ReadDerivedWatermark(a.paths)
	if err != nil || !present || wm != dailyHead {
		t.Errorf("watermark = (%q,%v,%v), want daily HEAD %s (INV-4)", wm, present, err, dailyHead)
	}
	if check, err := index.Check(a.paths, index.Options{Quiet: true}); err != nil || !check.OK() {
		t.Errorf("derived not fresh after barrier: err=%v drifts=%+v (F5)", err, check.Drifts)
	}
	if _, present, err := store.ReadMarker(a.paths); err != nil || present {
		t.Errorf("marker after barrier: present=%v err=%v, want absent", present, err)
	}
	rep, err := a.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(rep.CellsHit) != 1 || rep.CellsHit[0] != recovery.Cell1Clean {
		t.Errorf("second Reconcile cells = %v, want clean terminal", rep.CellsHit)
	}
}

// assertBatchCompleted asserts the F1 terminal: every archive-index
// entry is stamped with ONE shared deletion commit, off spine, off disk.
func assertBatchCompleted(t *testing.T, a *FileAdapter) {
	t.Helper()
	entries, err := store.LoadArchiveIndex(a.paths)
	if err != nil {
		t.Fatalf("LoadArchiveIndex: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no archive entries — the batch vanished")
	}
	delHash := ""
	for _, e := range entries {
		if e.CommitHash == "" {
			t.Errorf("entry %s unstamped after completion", e.ThrID)
			continue
		}
		if e.TreeHash == "" {
			t.Errorf("entry %s became a drift (empty TreeHash) — re-archive-as-drift is forbidden (F1)", e.ThrID)
		}
		if delHash == "" {
			delHash = e.CommitHash
		} else if e.CommitHash != delHash {
			t.Errorf("entry %s stamped %s, batch already stamped %s — want ONE deletion commit", e.ThrID, e.CommitHash, delHash)
		}
		if _, serr := os.Stat(store.ThreadDir(a.paths, e.ThrID)); serr == nil {
			t.Errorf("archived thread %s still on disk", e.ThrID)
		}
		if _, found, ferr := store.FindSpineRecord(a.paths, e.ThrID); ferr != nil || found {
			t.Errorf("archived thread %s still on spine (found=%v err=%v)", e.ThrID, found, ferr)
		}
	}
}

// crashRun invokes fn expecting the armed crashpoint to fire; it
// recovers the *Crash sentinel (any other panic is rethrown).
func crashRun(t *testing.T, point string, fn func()) {
	t.Helper()
	disarm := crashpoint.Arm(point)
	defer disarm()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("armed crashpoint %s did not fire", point)
		}
		if _, ok := r.(*crashpoint.Crash); !ok {
			panic(r)
		}
	}()
	fn()
}

// TestBarrier_CrashWalk kills the barrier at EVERY registered top-level
// step boundary and at every archiveBatch sub-window (fixture (d)), then
// drives the adapter's Reconcile and asserts the full roll-forward
// terminal: no reset, no duplicate day-commit, batch completed, daily
// reborn, idempotent second pass.
func TestBarrier_CrashWalk(t *testing.T) {
	const pressure = memops.ArchiveHighWater + 10 // B1 fires with target 60
	points := []string{
		"barrier.preArchival",
		"archiveBatch.postCapture.preMembership",
		"archiveBatch.postMembership.preRemove",
		"archiveBatch.midRemove",
		"archiveBatch.postRemove.preSpine",
		"archiveBatch.postSpine.preDeletionCommit",
		"archiveBatch.postDeletionCommit.preStamp",
		"archiveBatch.postStamp",
		"barrier.postArchival.preDayCommit",
		"barrier.postDayCommit.preRebuild",
		"barrier.postRebuild.preNuke",
		"barrier.midNuke",
		"barrier.postNuke.preInit",
		"barrier.postInit.preWatermark",
		"barrier.postWatermark.preMarkerClear",
		"barrier.postDayCommitPreTags",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			a := newBarrierHome(t, pressure)
			ctx := context.Background()
			pinBarrierClock(t, bDay1)
			day := autogit.DayIndexOf(bDay1) - 1

			crashRun(t, point, func() { _, _ = a.MaybeDayBarrier(ctx) })

			// The op=barrier marker (carrying the day) survives the crash as
			// the completion's re-entry token.
			m, present, err := store.ReadMarker(a.paths)
			if err != nil || !present || m.Op != store.OpBarrier || m.Day != day {
				t.Fatalf("marker after crash: %+v present=%v err=%v, want op=barrier day=%d", m, present, err, day)
			}

			// A FRESH adapter (the restarted process) reconciles: detection
			// types the pending barrier, the adapter completes it.
			b := NewFileAdapter(a.paths)
			rep, err := b.Reconcile(ctx)
			if err != nil {
				t.Fatalf("Reconcile after crash at %s: %v", point, err)
			}
			if rep.Pending != nil {
				t.Errorf("port Reconcile leaked a non-nil Pending")
			}
			if rep.ResetPerformed || len(rep.RevertedPaths) != 0 {
				t.Errorf("barrier recovery performed a reset (reverted=%v) — roll-forward only (F1/INV-1)", rep.RevertedPaths)
			}
			if len(rep.ScopedRestored) != 0 {
				t.Errorf("ScopedRestored = %v, want empty on the normal path", rep.ScopedRestored)
			}
			hasBarrierCell := false
			for _, c := range rep.CellsHit {
				if c == recovery.CellBarrier {
					hasBarrierCell = true
				}
			}
			if !hasBarrierCell {
				t.Errorf("CellsHit = %v, missing %s", rep.CellsHit, recovery.CellBarrier)
			}

			// Every kill point converges to a fully-archived batch: with
			// membership recorded, completion finishes the recorded worklist;
			// before membership (preArchival / postCapture.preMembership) no
			// deletion can have begun and the batch RE-FORMS via completion's
			// re-run of the B1 pressure drain (§2.3 "before B1 → re-run
			// barrier from B1"), so the day's archival pressure is never
			// silently deferred.
			assertBatchCompleted(t, b)
			// Un-archived survivors keep their bytes (worktree is truth).
			if _, err := os.Stat(store.ThreadDir(b.paths, fmt.Sprintf("thr_%d", pressure))); err != nil {
				t.Errorf("warmest thread lost: %v", err)
			}
			assertBarrierTerminal(t, b, day)
		})
	}
}

// primaryTags returns every tag on primary as name→target-commit-hash
// (lightweight tags point directly at the commit).
func primaryTags(t *testing.T, a *FileAdapter) map[string]string {
	t.Helper()
	repo, err := autogit.Open(a.paths, autogit.Primary)
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	tags, err := repo.Tags()
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	out := map[string]string{}
	_ = tags.ForEach(func(ref *plumbing.Reference) error {
		out[ref.Name().Short()] = ref.Hash().String()
		return nil
	})
	return out
}

// findTagByPrefix returns the single tag whose name starts with prefix,
// asserting exactly one exists (idempotence: no duplicate life-event
// tags).
func findTagByPrefix(t *testing.T, tags map[string]string, prefix string) (name, target string) {
	t.Helper()
	var hits []string
	for n := range tags {
		if strings.HasPrefix(n, prefix) {
			hits = append(hits, n)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("tags matching %q = %v, want exactly 1", prefix, hits)
	}
	return hits[0], tags[hits[0]]
}

// TestBarrier_LifecycleTags is the focused derivation test: the barrier
// mints one primary tag per mappable §2.8 life event, at the right
// commit, with a round-trippable stamp — created/retired/project at the
// day-commit, archived at the archival commit.
func TestBarrier_LifecycleTags(t *testing.T) {
	a := newBarrierHome(t, memops.ArchiveHighWater+10) // pressure so B1 archives
	ctx := context.Background()

	// Day-N life events (clock is still pinned at bDay0 by newBarrierHome).
	if err := eventlog.Log(a.paths, "thread", "created", "thr_created1 via=slash-topic project=prj_1"); err != nil {
		t.Fatalf("log created: %v", err)
	}
	if err := eventlog.Log(a.paths, "retire", "complete", "thr=thr_retired1 resolution=resolved"); err != nil {
		t.Fatalf("log retire: %v", err)
	}
	if err := eventlog.Log(a.paths, "project", "created", "id=prj_created1 name=Fresh"); err != nil {
		t.Fatalf("log project: %v", err)
	}

	pinBarrierClock(t, bDay1)
	day := autogit.DayIndexOf(bDay1) - 1
	if _, err := a.MaybeDayBarrier(ctx); err != nil {
		t.Fatalf("barrier: %v", err)
	}

	dayCommit, err := autogit.HeadHash(ctx, a.paths, autogit.Primary)
	if err != nil {
		t.Fatalf("day-commit hash: %v", err)
	}
	tags := primaryTags(t, a)

	// created / retired / project → the day-commit, stamp round-trips.
	for _, tc := range []struct{ prefix, kind string }{
		{"thread/thr_created1/created_", "created"},
		{"thread/thr_retired1/retired_", "retired"},
		{"project/prj_created1/created_", "project-created"},
	} {
		name, target := findTagByPrefix(t, tags, tc.prefix)
		if target != dayCommit {
			t.Errorf("%s tag %s → %s, want day-commit %s", tc.kind, name, target, dayCommit)
		}
		if _, perr := autogit.ParseTagStamp(strings.TrimPrefix(name, tc.prefix)); perr != nil {
			t.Errorf("%s tag %s stamp does not round-trip: %v", tc.kind, name, perr)
		}
	}

	// archived → the archival commit from the index (NOT the day-commit).
	entries, err := store.LoadArchiveIndex(a.paths)
	if err != nil {
		t.Fatalf("LoadArchiveIndex: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no archive entries — pressure did not archive")
	}
	archTargets := map[string]string{}
	for _, e := range entries {
		archTargets[e.ThrID] = e.CommitHash
	}
	archivedTags := 0
	for name, target := range tags {
		parts := strings.Split(name, "/")
		if len(parts) != 3 || parts[0] != "thread" || !strings.HasPrefix(parts[2], "archived_") {
			continue
		}
		archivedTags++
		id := parts[1]
		if want := archTargets[id]; target != want || want == dayCommit {
			t.Errorf("archived tag %s → %s, want archival commit %s (!= day-commit)", name, target, want)
		}
		if _, perr := autogit.ParseTagStamp(strings.TrimPrefix(parts[2], "archived_")); perr != nil {
			t.Errorf("archived tag %s stamp does not round-trip: %v", name, perr)
		}
	}
	if archivedTags == 0 {
		t.Error("no thread/*/archived_* tags minted despite archival pressure")
	}
	_ = day
}

// TestBarrier_MorningCompletionThenNextDay is kill-timing walk 1
// (§2.3): a day-N barrier interrupted mid-B1 is completed the NEXT
// morning (clock at N+1). Roll-forward lands trailered-but-non-day-shape
// commits for N, so the INV-2 shape guard still mints the real
// day-commit N; the subsequent poll then seals N+1. Two distinct
// day-commits, no fork, exactly one each.
func TestBarrier_MorningCompletionThenNextDay(t *testing.T) {
	a := newBarrierHome(t, memops.ArchiveHighWater+10)
	ctx := context.Background()
	pinBarrierClock(t, bDay1)
	dayN := autogit.DayIndexOf(bDay1) - 1

	crashRun(t, "archiveBatch.postDeletionCommit.preStamp", func() { _, _ = a.MaybeDayBarrier(ctx) })

	// Next morning: clock at N+2's... the day after the crash day.
	pinBarrierClock(t, bDay2)
	b := NewFileAdapter(a.paths)
	if _, err := b.Reconcile(ctx); err != nil {
		t.Fatalf("morning Reconcile: %v", err)
	}
	assertBatchCompleted(t, b)
	if n := dayCommitCount(t, b, dayN); n != 1 {
		t.Fatalf("day-commit count for interrupted day %d = %d, want 1", dayN, n)
	}
	// The open-poll seals the day that completed while we were away.
	res, err := b.MaybeDayBarrier(ctx)
	if err != nil {
		t.Fatalf("MaybeDayBarrier: %v", err)
	}
	dayN1 := autogit.DayIndexOf(bDay2) - 1
	if len(res.DaysSealed) != 1 || res.DaysSealed[0] != dayN1 {
		t.Fatalf("DaysSealed = %v, want [%d]", res.DaysSealed, dayN1)
	}
	if n := dayCommitCount(t, b, dayN); n != 1 {
		t.Errorf("day %d re-sealed (count %d) — INV-2 fork", dayN, n)
	}
	if n := dayCommitCount(t, b, dayN1); n != 1 {
		t.Errorf("day %d count = %d, want 1", dayN1, n)
	}
}

// TestBarrier_NoRefireAfterSeal — §2.1 poll semantics: after sealing,
// the poll is a no-op for the same day (targets advance strictly
// forward), and the INV-2 guard skips a re-driven B2.
func TestBarrier_NoRefireAfterSeal(t *testing.T) {
	a := newBarrierHome(t, 3)
	ctx := context.Background()
	pinBarrierClock(t, bDay1)
	day := autogit.DayIndexOf(bDay1) - 1

	res, err := a.MaybeDayBarrier(ctx)
	if err != nil {
		t.Fatalf("MaybeDayBarrier: %v", err)
	}
	if len(res.DaysSealed) != 1 || res.DaysSealed[0] != day {
		t.Fatalf("DaysSealed = %v, want [%d]", res.DaysSealed, day)
	}
	for i := 0; i < 3; i++ {
		res2, err := a.MaybeDayBarrier(ctx)
		if err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		if len(res2.DaysSealed) != 0 {
			t.Fatalf("poll %d re-fired: %v", i, res2.DaysSealed)
		}
	}
	// A FRESH adapter (no cache) must also not re-fire.
	res3, err := NewFileAdapter(a.paths).MaybeDayBarrier(ctx)
	if err != nil || len(res3.DaysSealed) != 0 {
		t.Fatalf("cold poll re-fired: %v err=%v", res3.DaysSealed, err)
	}
	if n := dayCommitCount(t, a, day); n != 1 {
		t.Errorf("day-commit count = %d, want 1", n)
	}
}

// TestBarrier_EmptyDayStillSeals — F3: a barrier that captured nothing
// still mints exactly one trailered day-commit (AllowEmptyCommits), so
// HEADDAY is always defined and there is no tag path.
func TestBarrier_EmptyDayStillSeals(t *testing.T) {
	a := newBarrierHome(t, 0) // empty home, nothing to capture
	ctx := context.Background()
	pinBarrierClock(t, bDay1)
	day := autogit.DayIndexOf(bDay1) - 1

	// Seal day-1. Then advance another day WITHOUT any content change:
	// the second seal is a genuinely empty day.
	if _, err := a.MaybeDayBarrier(ctx); err != nil {
		t.Fatalf("first barrier: %v", err)
	}
	pinBarrierClock(t, bDay2)
	res, err := a.MaybeDayBarrier(ctx)
	if err != nil {
		t.Fatalf("empty-day barrier: %v", err)
	}
	day2 := autogit.DayIndexOf(bDay2) - 1
	if len(res.DaysSealed) != 1 || res.DaysSealed[0] != day2 {
		t.Fatalf("DaysSealed = %v, want [%d]", res.DaysSealed, day2)
	}
	if n := dayCommitCount(t, a, day2); n != 1 {
		t.Errorf("empty-day commit count = %d, want 1", n)
	}
	if got, ok, err := autogit.HeadDay(ctx, a.paths, autogit.Primary); err != nil || !ok || got != day2 {
		t.Errorf("HEADDAY = (%d,%v,%v), want %d", got, ok, err, day2)
	}
	_ = day
}

// TestReconcile_BenignMorning is fixture (e): marker absent, daily
// missing, watermark present — the NORMAL morning, not a crash. The
// adapter's Reconcile morning-inits a fresh daily; nothing else moves.
func TestReconcile_BenignMorning(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()
	priBefore, err := autogit.HeadHash(ctx, a.paths, autogit.Primary)
	if err != nil {
		t.Fatalf("primary HEAD: %v", err)
	}
	if err := autogit.NukeDaily(a.paths); err != nil {
		t.Fatalf("nuke daily: %v", err)
	}

	b := NewFileAdapter(a.paths)
	rep, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("benign-morning Reconcile: %v", err)
	}
	found := false
	for _, c := range rep.CellsHit {
		if c == recovery.CellMorningInit {
			found = true
		}
	}
	if !found {
		t.Errorf("CellsHit = %v, want %s", rep.CellsHit, recovery.CellMorningInit)
	}
	if rep.ResetPerformed {
		t.Error("benign morning performed a reset")
	}
	if got, _ := autogit.HeadHash(ctx, b.paths, autogit.Primary); got != priBefore {
		t.Error("benign morning touched primary")
	}
	if st := autogit.ProbeDaily(b.paths); st != autogit.DailyPresent {
		t.Fatalf("daily = %v, want present", st)
	}
	wm, present, _ := store.ReadDerivedWatermark(b.paths)
	dh, _ := autogit.HeadHash(ctx, b.paths, autogit.Daily)
	if !present || wm != dh {
		t.Errorf("watermark %q != reborn daily HEAD %q", wm, dh)
	}
	rep2, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(rep2.CellsHit) != 1 || rep2.CellsHit[0] != recovery.Cell1Clean {
		t.Errorf("second pass cells = %v, want clean", rep2.CellsHit)
	}
}

// TestReconcile_CorruptDailyRecreated is fixture (c′) / F7: a corrupt
// .git-daily (exists, does not open) must classify half-created and be
// recreated from the worktree — NEVER refuse the open.
func TestReconcile_CorruptDailyRecreated(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()
	if err := autogit.NukeDaily(a.paths); err != nil {
		t.Fatalf("nuke: %v", err)
	}
	// A dir with garbage where a git-dir should be.
	if err := os.MkdirAll(a.paths.GitDaily, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a.paths.GitDaily, "HEAD"), []byte("not a ref\n"), 0o644); err != nil {
		t.Fatalf("write garbage HEAD: %v", err)
	}

	b := NewFileAdapter(a.paths)
	rep, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("corrupt-daily Reconcile refused the open: %v (F7)", err)
	}
	if st := autogit.ProbeDaily(b.paths); st != autogit.DailyPresent {
		t.Fatalf("daily = %v after recreate, want present", st)
	}
	_ = rep
}

// TestReconcile_RebaselineMarker is fixture (h) / F7: an op=rebaseline
// marker (any daily state) → rm -rf + morning-init unconditionally, NO
// primary touch, marker cleared.
func TestReconcile_RebaselineMarker(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()
	priBefore, _ := autogit.HeadHash(ctx, a.paths, autogit.Primary)
	// Half-created daily + the rebaseline marker (the §6.2 knob died
	// mid-nuke/recreate).
	if err := autogit.NukeDaily(a.paths); err != nil {
		t.Fatalf("nuke: %v", err)
	}
	if err := autogit.InitDaily(ctx, a.paths); err != nil { // storage but no HEAD commit
		t.Fatalf("half-create: %v", err)
	}
	if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpRebaseline}); err != nil {
		t.Fatalf("marker: %v", err)
	}

	b := NewFileAdapter(a.paths)
	rep, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("rebaseline Reconcile: %v", err)
	}
	foundCell := false
	for _, c := range rep.CellsHit {
		if c == recovery.CellRebaseline {
			foundCell = true
		}
	}
	if !foundCell {
		t.Errorf("CellsHit = %v, want %s", rep.CellsHit, recovery.CellRebaseline)
	}
	if got, _ := autogit.HeadHash(ctx, b.paths, autogit.Primary); got != priBefore {
		t.Error("rebaseline recovery touched primary")
	}
	if st := autogit.ProbeDaily(b.paths); st != autogit.DailyPresent {
		t.Fatalf("daily = %v, want present (recreated)", st)
	}
	if _, present, _ := store.ReadMarker(b.paths); present {
		t.Error("rebaseline marker not cleared")
	}
}

// TestReconcile_WatermarkMigrationFromR2 — §3 migration: an R2-era
// watermark holds a PRIMARY hash and there is no .git-daily. First R3b
// open takes the benign-morning path, the stale value reads as stale,
// and the one-shot full rebuild fires; the watermark ends re-anchored
// to the daily baseline. No migration code, no error, exactly once.
func TestReconcile_WatermarkMigrationFromR2(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()
	// Regress the home to R2 shape: no daily, watermark = primary HEAD.
	if err := autogit.NukeDaily(a.paths); err != nil {
		t.Fatalf("nuke: %v", err)
	}
	pri, err := autogit.HeadHash(ctx, a.paths, autogit.Primary)
	if err != nil {
		t.Fatalf("primary HEAD: %v", err)
	}
	if err := store.WriteDerivedWatermark(a.paths, pri); err != nil {
		t.Fatalf("write R2 watermark: %v", err)
	}
	// Stale derived vs canonical, so the migration rebuild is observable.
	if err := os.WriteFile(a.paths.Symbols, []byte(""), 0o644); err != nil {
		t.Fatalf("stale symbols: %v", err)
	}

	b := NewFileAdapter(a.paths)
	rep, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("migration Reconcile: %v", err)
	}
	if !rep.DerivedRebuilt {
		t.Error("R2→R3b transition did not run the one-shot rebuild")
	}
	wm, present, _ := store.ReadDerivedWatermark(b.paths)
	dh, _ := autogit.HeadHash(ctx, b.paths, autogit.Daily)
	if !present || wm != dh || wm == pri {
		t.Errorf("watermark %q, want re-anchored to daily HEAD %q (was primary %q)", wm, dh, pri)
	}
	rep2, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if rep2.DerivedRebuilt {
		t.Error("migration rebuild fired twice")
	}
}

// TestBarrier_OvernightHandEditFoldedIn is fixture (g) / F5: a
// markerless overnight hand-edit present at barrier entry is staged by
// B2 into the day-commit, B3 rebuilds derived, B6 asserts fresh.
func TestBarrier_OvernightHandEdit(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()
	// Overnight hand-edit: new turn excerpt appended to a thread by hand
	// (stales symbols.jsonl AND dirties the tree vs daily).
	handEdit := filepath.Join(store.ThreadDir(a.paths, "thr_1"), "turns", "2.md")
	if err := os.MkdirAll(filepath.Dir(handEdit), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(handEdit, []byte("## Turn 2\n\nhand-edited overnight\n"), 0o644); err != nil {
		t.Fatalf("write hand edit: %v", err)
	}

	pinBarrierClock(t, bDay1)
	day := autogit.DayIndexOf(bDay1) - 1
	// Open sequence: Reconcile (cell 3 — hand-edit NEVER reset) then the
	// barrier, which folds the edit into permanent history.
	rep, err := a.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.ResetPerformed {
		t.Fatal("hand-edit was reset")
	}
	if _, err := a.MaybeDayBarrier(ctx); err != nil {
		t.Fatalf("barrier with hand-edit: %v", err)
	}
	// The hand-edit is in the day-commit's tree (B2 staged everything).
	repo, err := autogit.Open(a.paths, autogit.Primary)
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	head, _ := repo.Head()
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("HEAD commit: %v", err)
	}
	if _, err := commit.File("threads/thr_1/turns/2.md"); err != nil {
		t.Errorf("hand-edit missing from day-commit tree: %v", err)
	}
	assertBarrierTerminal(t, a, day)
}

// TestBarrier_PersistentSpineBreak is fixture (i) — the §2.7
// quarantine-and-proceed posture: a spine-breaking overnight hand-edit
// fails B2's post-flag on the fresh run (marker left, refuse), persists
// into the completion re-drive, and is then quarantined byte-exact +
// scoped-restored from the last good primary tree; the barrier
// completes and ScopedRestored names the path.
func TestBarrier_PersistentSpineBreak(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()
	good, err := os.ReadFile(a.paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	// Overnight hand-edit that BREAKS the spine (id fails the thr_<n>
	// pattern — a structural error, not mere drift).
	broken := string(good) + `{"id":"thr_bogus","project":"prj_1","anchors":["a","b"],"summary":"bad","state":"active","created":"2026-05-29T00:00:00Z","last_engaged":"2026-05-29T00:00:00Z","state_changed":"2026-05-29T00:00:00Z","turn_count":1,"recall_fires":0}` + "\n"
	if err := os.WriteFile(a.paths.Spine, []byte(broken), 0o644); err != nil {
		t.Fatalf("break spine: %v", err)
	}

	pinBarrierClock(t, bDay1)
	day := autogit.DayIndexOf(bDay1) - 1
	// Fresh run: B2 post-flag fails; the barrier errors with its marker
	// in place (the first idempotent drive).
	if _, err := a.MaybeDayBarrier(ctx); err == nil {
		t.Fatal("barrier over a broken spine succeeded; want post-flag failure")
	}
	if m, present, _ := store.ReadMarker(a.paths); !present || m.Op != store.OpBarrier {
		t.Fatalf("marker after failed barrier: %+v present=%v, want op=barrier intact", m, present)
	}

	// Next open: completion re-drive → persistent failure → §2.7.
	b := NewFileAdapter(a.paths)
	rep, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("completion Reconcile: %v (want quarantine-and-proceed success)", err)
	}
	foundSpine := false
	for _, p := range rep.ScopedRestored {
		if p == "spine.jsonl" {
			foundSpine = true
		}
	}
	if !foundSpine {
		t.Errorf("ScopedRestored = %v, want spine.jsonl", rep.ScopedRestored)
	}
	if rep.ResetPerformed {
		t.Error("quarantine-and-proceed performed a reset (must be path-scoped restore)")
	}
	// The offending bytes are preserved byte-exact in quarantine.
	matches, err := filepath.Glob(filepath.Join(b.paths.RecoveryDir, "quarantine", "*", "spine.jsonl"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no quarantined spine copy: %v err=%v", matches, err)
	}
	qbytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read quarantined spine: %v", err)
	}
	if string(qbytes) != broken {
		t.Errorf("quarantined spine not byte-exact")
	}
	// The live spine is restored to a spine-good version.
	if check, err := index.Check(b.paths, index.Options{Quiet: true}); err != nil || !check.OK() {
		t.Errorf("derived not fresh after quarantine-and-proceed: %v %+v", err, check.Drifts)
	}
	if n := dayCommitCount(t, b, day); n != 1 {
		t.Errorf("day-commit count = %d, want exactly 1 despite the repair", n)
	}
	if _, present, _ := store.ReadMarker(b.paths); present {
		t.Error("marker not cleared after successful quarantine-and-proceed")
	}

	// F1: the completed barrier must leave primary HEAD as the day-commit
	// for N — a trailered-but-non-day-shape HEAD (the old trailing repair
	// commit) satisfies detection while failing the INV-2 shape guard,
	// re-firing runBarrier(N) on the next same-day open (second
	// day-commit, mid-day nuke of the live daily).
	if isDay, derr := autogit.HeadIsDayCommit(ctx, b.paths, autogit.Primary, day); derr != nil || !isDay {
		t.Errorf("primary HEAD not day-shape for %d after repaired barrier (isDay=%v err=%v) — same-day re-fire wedge (F1)", day, isDay, derr)
	}
	// A cold same-day re-poll must not fire a second barrier.
	res, err := NewFileAdapter(b.paths).MaybeDayBarrier(ctx)
	if err != nil {
		t.Fatalf("same-day re-poll: %v", err)
	}
	if len(res.DaysSealed) != 0 {
		t.Errorf("same-day re-poll re-fired the barrier: %v (F1)", res.DaysSealed)
	}
	if n := dayCommitCount(t, b, day); n != 1 {
		t.Errorf("day-commit count after re-poll = %d, want exactly 1 (INV-2)", n)
	}
}

// TestBarrier_PersistentDerivedBreak is fixture (j) — the §2.7
// quarantine-and-proceed posture for a PERSISTENT B6 CheckDerivedFresh
// failure. A rebuild fault (index.SetRebuildFaultInjector) makes every
// derived rebuild emit a bogus symbol Check never recomputes, so the drift
// survives both B3's rebuild AND B6's post-quarantine rebuild — the only way
// to reach the derived quarantine path, since ordinary derived corruption is
// silently fixed by the rebuild before B6 ever checks. Asserts: while the
// fault persists the barrier refuses-to-open with the op=barrier marker
// intact and the offending derived bytes quarantined byte-exact (no corrupt
// day-commit, no accretion); once the fault clears to a single residual
// rebuild, the completion converges to exactly one day-commit with
// ScopedRestored naming the quarantined+regenerated derived path.
func TestBarrier_PersistentDerivedBreak(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()

	bogus := store.SymbolRecord{Symbol: "__crash_matrix_bogus__", Threads: []string{"thr_1"}}
	corrupt := index.SetRebuildFaultInjector(func(s []store.SymbolRecord) []store.SymbolRecord {
		return append(append([]store.SymbolRecord{}, s...), bogus)
	})

	pinBarrierClock(t, bDay1)
	day := autogit.DayIndexOf(bDay1) - 1

	// Fresh run: B3 rebuilds corrupt derived; B6's assert (recovering=false)
	// fails plainly, marker left in place.
	if _, err := a.MaybeDayBarrier(ctx); err == nil {
		t.Fatal("barrier over persistent derived corruption succeeded; want B6 failure")
	}
	if m, present, _ := store.ReadMarker(a.paths); !present || m.Op != store.OpBarrier {
		t.Fatalf("marker after failed barrier: %+v present=%v, want op=barrier intact", m, present)
	}

	// Completion re-drive while the fault persists: §2.7 quarantines the
	// offending derived, rebuild still corrupts, re-check still drifts →
	// refuse, marker intact.
	if _, err := NewFileAdapter(a.paths).Reconcile(ctx); err == nil {
		t.Fatal("completion Reconcile succeeded while derived fault persists; want §2.7 refuse")
	}
	if m, present, _ := store.ReadMarker(a.paths); !present || m.Op != store.OpBarrier || m.Day != day {
		t.Fatalf("marker after refused derived open: %+v present=%v, want op=barrier day=%d intact", m, present, day)
	}
	// Unlike a spine break (fixture i, where B2's gate fails and NO day-commit
	// lands), a derived break lets B2's spine-good day-commit land — derived
	// is not checked until B6 — so exactly one day-commit exists, and the
	// INV-2 guard must not duplicate it across the refused re-drives.
	if n := dayCommitCount(t, a, day); n != 1 {
		t.Errorf("day-commit count while derived-broken = %d, want exactly 1 (spine-good day-commit lands once; no INV-2 duplicate)", n)
	}
	// The offending derived bytes are preserved byte-exact in quarantine
	// (this is exactly the derived-quarantine path the findingRel abs-path
	// fix makes reachable).
	matches, err := filepath.Glob(filepath.Join(a.paths.RecoveryDir, "quarantine", "*", "symbols.jsonl"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no quarantined symbols copy: %v err=%v", matches, err)
	}
	qbytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read quarantined symbols: %v", err)
	}
	if !strings.Contains(string(qbytes), "__crash_matrix_bogus__") {
		t.Error("quarantined symbols not byte-exact (missing the injected bogus record)")
	}

	// Reduce the fault to a single residual rebuild: the next completion's
	// B3 corrupts (so B6 quarantines), but B6's post-quarantine rebuild is
	// Check-clean → ScopedRestored records the regenerated derived path and
	// the barrier converges.
	corrupt()
	remaining := 1
	oneShot := index.SetRebuildFaultInjector(func(s []store.SymbolRecord) []store.SymbolRecord {
		if remaining > 0 {
			remaining--
			return append(append([]store.SymbolRecord{}, s...), bogus)
		}
		return s
	})
	defer oneShot()

	b := NewFileAdapter(a.paths)
	rep, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("converging Reconcile: %v (want quarantine-and-proceed success)", err)
	}
	foundSymbols := false
	for _, p := range rep.ScopedRestored {
		if p == "symbols.jsonl" {
			foundSymbols = true
		}
	}
	if !foundSymbols {
		t.Errorf("ScopedRestored = %v, want symbols.jsonl (derived scoped-restore via regenerate)", rep.ScopedRestored)
	}
	if rep.ResetPerformed {
		t.Error("derived quarantine-and-proceed performed a reset (must be path-scoped)")
	}
	assertBarrierTerminal(t, b, day)
}

// TestBarrier_RefusedOpensDoNotAccrete is F1's accretion arm: while the
// §2.7 posture keeps refusing (the quarantine destination is wedged
// shut here), every refused open must leave primary EXACTLY as it found
// it — no day-commit/repair pair accreted per attempt, and no
// spine-broken day-commit ever landing — and the eventual successful
// completion converges to exactly one day-commit N.
func TestBarrier_RefusedOpensDoNotAccrete(t *testing.T) {
	a := newBarrierHome(t, 2)
	ctx := context.Background()
	good, err := os.ReadFile(a.paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	broken := string(good) + `{"id":"thr_bogus","project":"prj_1","anchors":["a","b"],"summary":"bad","state":"active","created":"2026-05-29T00:00:00Z","last_engaged":"2026-05-29T00:00:00Z","state_changed":"2026-05-29T00:00:00Z","turn_count":1,"recall_fires":0}` + "\n"
	if err := os.WriteFile(a.paths.Spine, []byte(broken), 0o644); err != nil {
		t.Fatalf("break spine: %v", err)
	}
	// Wedge §2.7: a FILE where recovery/ must be a directory makes the
	// quarantine move fail deterministically, so every completion attempt
	// refuses (marker intact) without ever "fixing" the spine.
	if err := os.WriteFile(a.paths.RecoveryDir, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("wedge recovery dir: %v", err)
	}

	pinBarrierClock(t, bDay1)
	day := autogit.DayIndexOf(bDay1) - 1
	if _, err := a.MaybeDayBarrier(ctx); err == nil {
		t.Fatal("barrier over a broken spine succeeded; want refusal")
	}
	headWedged, err := autogit.HeadHash(ctx, a.paths, autogit.Primary)
	if err != nil {
		t.Fatalf("primary HEAD: %v", err)
	}

	for i := 1; i <= 2; i++ {
		if _, err := NewFileAdapter(a.paths).Reconcile(ctx); err == nil {
			t.Fatalf("refused open %d succeeded; want §2.7 refuse", i)
		}
		if m, present, _ := store.ReadMarker(a.paths); !present || m.Op != store.OpBarrier || m.Day != day {
			t.Fatalf("marker after refused open %d: %+v present=%v, want op=barrier day=%d intact", i, m, present, day)
		}
	}
	if h, herr := autogit.HeadHash(ctx, a.paths, autogit.Primary); herr != nil || h != headWedged {
		t.Errorf("refused opens moved primary HEAD %s → %s — commits accreted while wedged (F1)", headWedged, h)
	}
	if n := dayCommitCount(t, a, day); n != 0 {
		t.Errorf("day-commit count while wedged = %d, want 0 (a spine-broken day-commit must never land)", n)
	}

	// Unwedge; the next open completes and mints exactly one day-commit.
	if err := os.Remove(a.paths.RecoveryDir); err != nil {
		t.Fatalf("unwedge recovery dir: %v", err)
	}
	b := NewFileAdapter(a.paths)
	if _, err := b.Reconcile(ctx); err != nil {
		t.Fatalf("post-unwedge Reconcile: %v", err)
	}
	if n := dayCommitCount(t, b, day); n != 1 {
		t.Errorf("day-commit count after completion = %d, want exactly 1 (no accretion, INV-2)", n)
	}
	if isDay, derr := autogit.HeadIsDayCommit(ctx, b.paths, autogit.Primary, day); derr != nil || !isDay {
		t.Errorf("primary HEAD not day-shape after completion (isDay=%v err=%v)", isDay, derr)
	}
	if _, present, _ := store.ReadMarker(b.paths); present {
		t.Error("marker not cleared after completion")
	}
	res, err := NewFileAdapter(b.paths).MaybeDayBarrier(ctx)
	if err != nil || len(res.DaysSealed) != 0 {
		t.Errorf("same-day re-poll after completion: sealed=%v err=%v, want no fire", res.DaysSealed, err)
	}
}

// TestArchiveThreads_StandaloneCrashRollsForward — cell 8 under F1: a
// STANDALONE ArchiveThreads killed mid-batch is detected on marker
// presence alone and roll-forward COMPLETED by the adapter (no reset,
// no re-archive-as-drift), with the op=archival marker cleared last.
func TestArchiveThreads_StandaloneCrashRollsForward(t *testing.T) {
	pinBarrierClock(t, bDay0)
	a := newAdapter(t)
	ctx := context.Background()
	for i := 1; i <= 4; i++ {
		seedThread(t, a, fmt.Sprintf("thr_%d", i), "prj_1", "## Turn 1\n\nbody\n")
	}
	if _, err := a.Reconcile(ctx); err != nil {
		t.Fatalf("baseline Reconcile: %v", err)
	}

	crashRun(t, "archiveBatch.midRemove", func() {
		_, _ = a.ArchiveThreads(ctx, []string{"thr_1", "thr_2", "thr_3"})
	})

	m, present, err := store.ReadMarker(a.paths)
	if err != nil || !present || m.Op != store.OpArchival {
		t.Fatalf("marker after crash: %+v present=%v err=%v", m, present, err)
	}

	b := NewFileAdapter(a.paths)
	rep, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rep.ResetPerformed || len(rep.RevertedPaths) != 0 {
		t.Errorf("archival completion reset the worktree: %v", rep.RevertedPaths)
	}
	assertBatchCompleted(t, b)
	// The un-batched thread survives untouched.
	if _, err := os.Stat(store.ThreadDir(b.paths, "thr_4")); err != nil {
		t.Errorf("survivor thread lost: %v", err)
	}
	if _, present, _ := store.ReadMarker(b.paths); present {
		t.Error("op=archival marker survived completion")
	}
	rep2, err := b.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if len(rep2.CellsHit) != 1 || rep2.CellsHit[0] != recovery.Cell1Clean {
		t.Errorf("terminal cells = %v, want clean", rep2.CellsHit)
	}
}
