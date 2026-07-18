package sim

import (
	"context"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	"personant/internal/autogit"
	"personant/internal/memops/fileadapter"
	"personant/internal/recovery"
	"personant/internal/scenarios"
	"personant/internal/store"
)

// TestSimBarrier2Day is the #94 R3b barrier-crossing rung: a 2-simulated-
// day mock run MUST cross one day barrier end-to-end — the sim's
// OnSimDayClose fires MaybeDayBarrier at the day-0→day-1 boundary — and
// leave the dual-repo evidence behind:
//
//   - exactly one DayCommitMessage-shape commit on PRIMARY for the
//     sealed day (INV-2), with HEADDAY defined and no tag path (F3);
//   - the DAILY DB reborn at the barrier: its history holds only day-1
//     turns + the baseline, far shallower than the run's total turns;
//   - watermark == daily HEAD (INV-4) and a clean terminal Reconcile;
//   - the §6.3 gauges recorded (barrier_count, barrier/day-commit/
//     morning-init durations, day_commit_bytes, daily loose objects).
//
// It reuses the full runSimRung acceptance gates (mock path: every hard
// gate active), so "the barrier does not perturb the workload" comes
// for free.
func TestSimBarrier2Day(t *testing.T) {
	corpus := loadCorpusSlots(t)
	h := runSimRung(t, withRunTimestampSuffix("sim-barrier-2d"), 2*simDayDuration, corpus, nil, false, nil, "")
	ctx := context.Background()

	m, err := readMetrics(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}
	barriers := m.Counters[scenarios.MetricBarrierCount]
	if barriers < 1 {
		t.Fatalf("barrier_count = %d, want >= 1 (a 2d run crosses one day boundary)", barriers)
	}
	for _, key := range []string{
		scenarios.MetricBarrierDurationMs,
		scenarios.MetricDayCommitDurationMs,
		scenarios.MetricMorningInitDurationMs,
		scenarios.MetricDayCommitBytes,
		scenarios.MetricDailyLooseObjects,
	} {
		if len(m.Histograms[key]) == 0 {
			t.Errorf("gauge %s recorded no samples", key)
		}
	}

	// Primary evidence: HEADDAY defined; exactly one day-commit for the
	// sealed day (day 0 of the sim grid = the epoch-day of SimClockStart).
	day, ok, err := autogit.HeadDay(ctx, h.Paths, autogit.Primary)
	if err != nil || !ok {
		t.Fatalf("HEADDAY after barrier: ok=%v err=%v, want defined (F3)", ok, err)
	}
	wantDay := autogit.DayIndexOf(scenarios.SimClockStart)
	if day != wantDay {
		t.Errorf("HEADDAY = %d, want sealed sim day 0 = %d", day, wantDay)
	}
	pri, err := autogit.Open(h.Paths, autogit.Primary)
	if err != nil {
		t.Fatalf("open primary: %v", err)
	}
	priHead, err := pri.Head()
	if err != nil {
		t.Fatalf("primary HEAD: %v", err)
	}
	iter, err := pri.Log(&git.LogOptions{From: priHead.Hash()})
	if err != nil {
		t.Fatalf("primary log: %v", err)
	}
	dayCommits, priCommits := 0, 0
	for {
		c, cerr := iter.Next()
		if cerr != nil {
			break
		}
		priCommits++
		first, _, _ := strings.Cut(c.Message, "\n")
		if strings.HasPrefix(strings.TrimSpace(first), "day ") {
			dayCommits++
		}
	}
	iter.Close()
	if dayCommits != int(barriers) {
		t.Errorf("primary day-commit count = %d, want %d (one per sealed day)", dayCommits, barriers)
	}

	// Daily evidence: present, reborn (shallow — only day-1 turns + the
	// baseline, NOT the whole run), watermark anchored to its HEAD.
	if st := autogit.ProbeDaily(h.Paths); st != autogit.DailyPresent {
		t.Fatalf("daily state = %v, want present", st)
	}
	dly, err := autogit.Open(h.Paths, autogit.Daily)
	if err != nil {
		t.Fatalf("open daily: %v", err)
	}
	dlyHead, err := dly.Head()
	if err != nil {
		t.Fatalf("daily HEAD: %v", err)
	}
	dIter, err := dly.Log(&git.LogOptions{From: dlyHead.Hash()})
	if err != nil {
		t.Fatalf("daily log: %v", err)
	}
	dailyCommits := 0
	sawBaseline := false
	for {
		c, cerr := dIter.Next()
		if cerr != nil {
			break
		}
		dailyCommits++
		if strings.HasPrefix(c.Message, "daily baseline") {
			sawBaseline = true
		}
	}
	dIter.Close()
	if !sawBaseline {
		t.Error("daily history has no 'daily baseline' commit — the daily was not reborn at the barrier")
	}
	turns := int(m.Counters[scenarios.MetricTurns])
	if dailyCommits >= turns {
		t.Errorf("daily history depth %d >= total turns %d — the barrier did not evaporate day-0 per-turn history", dailyCommits, turns)
	}
	// INV-4: the watermark is DAILY-anchored. Mid-day it legitimately
	// lags daily HEAD (per-turn commits advance HEAD; only rebuilds
	// stamp), so the assertion is the OPEN contract: a fresh Reconcile
	// converges with no crash cells and leaves watermark == daily HEAD.
	if _, present, werr := store.ReadDerivedWatermark(h.Paths); werr != nil || !present {
		t.Fatalf("watermark missing after run: present=%v err=%v", present, werr)
	}
	// The harness does not run chat's session-close backstop Checkpoint,
	// so harness-side residue can sit markerless-dirty vs daily (cell 3 —
	// legitimate, absorbed at the next full sweep). Run that sweep here,
	// exactly as a real session close would, then assert the open contract.
	closer := fileadapter.NewFileAdapter(h.Paths)
	if err := closer.Checkpoint(ctx, "barrier-rung close"); err != nil {
		t.Fatalf("session-close checkpoint: %v", err)
	}
	rep, err := closer.Reconcile(ctx)
	if err != nil {
		t.Fatalf("post-run Reconcile: %v", err)
	}
	for _, c := range rep.CellsHit {
		if c != recovery.Cell1Clean && c != recovery.Cell2DerivedStale {
			t.Errorf("post-run Reconcile hit crash cell %s (cells=%v)", c, rep.CellsHit)
		}
	}
	wm, present, err := store.ReadDerivedWatermark(h.Paths)
	if err != nil || !present {
		t.Fatalf("watermark after reconcile: present=%v err=%v", present, err)
	}
	dh, err := autogit.HeadHash(ctx, h.Paths, autogit.Daily)
	if err != nil {
		t.Fatalf("daily HEAD: %v", err)
	}
	if wm != dh {
		t.Errorf("watermark %s != daily HEAD %s after open (INV-4)", wm, dh)
	}

	t.Logf("barrier rung: barriers=%d primary_commits=%d day_commits=%d daily_depth=%d turns=%d barrier_ms=%v morning_init_ms=%v day_commit_bytes=%v",
		barriers, priCommits, dayCommits, dailyCommits, turns,
		m.Histograms[scenarios.MetricBarrierDurationMs],
		m.Histograms[scenarios.MetricMorningInitDurationMs],
		m.Histograms[scenarios.MetricDayCommitBytes])
}
