package fileadapter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"

	"personant/internal/autogit"
	"personant/internal/clock"
	"personant/internal/crashpoint"
	"personant/internal/eventlog"
	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/recovery"
	"personant/internal/store"
	"personant/internal/verify"
)

// The day barrier (#94 R3b §2) — homed HERE, in the adapter (F4): it
// needs archiveBatch, both repo handles, the day-commit, and daily
// lifecycle, all of which this package owns. Core recovery only detects
// an interrupted barrier (op=barrier marker → RecoveryReport.Pending)
// and this adapter completes it (completePending, called from
// Reconcile's completion loop).
//
// Sequence, under the op=barrier marker {op: barrier, day: N}:
//
//	B0  write marker (after the no-in-flight-scope check)
//	B1  archival-if-pressure → PRIMARY, via archiveBatch inside the
//	    already-open barrier scope (no nested op=archival marker, F4)
//	B2  day-commit worktree → PRIMARY (`Personant-Day: N`,
//	    AllowEmptyCommits — F3; spine-integrity gate on the staged tree
//	    BEFORE the mint, structural errors only — F5/F1: a spine-broken
//	    day-commit can never land), guarded by the INV-2
//	    day-commit-shape predicate
//	B3  rebuild derived from the now-committed worktree (absorbs
//	    overnight hand-edits and B1's removals — F5)
//	B4  nuke .git-daily
//	B5  re-init daily + Personant-Turn-less baseline commit → H_d
//	B6  stamp watermark = H_d (INV-4); assert derived-fresh (the
//	    once-a-day index.Check, moved off B2 per F5); clear marker
//
// Only B1/B2 mutate primary; B4/B5 are pure daily-DB lifecycle; B3/B6
// touch derived + watermark (operational, disposable). The worktree is
// untouched by B2-B6 and only reduced (never lost — bytes land in
// primary first) by B1's removals.

// Crashpoints (#94 R3b §7) — one per step boundary; R4 exhausts them.
var (
	cpBarrierPreArchival   = crashpoint.Register("barrier.preArchival")
	cpBarrierPostArchival  = crashpoint.Register("barrier.postArchival.preDayCommit")
	cpBarrierPostDayCommit = crashpoint.Register("barrier.postDayCommit.preRebuild")
	cpBarrierPostRebuild   = crashpoint.Register("barrier.postRebuild.preNuke")
	cpBarrierMidNuke       = crashpoint.Register("barrier.midNuke")
	cpBarrierPostNuke      = crashpoint.Register("barrier.postNuke.preInit")
	cpBarrierPostInit      = crashpoint.Register("barrier.postInit.preWatermark")
	cpBarrierPostWatermark = crashpoint.Register("barrier.postWatermark.preMarkerClear")
)

// MaybeDayBarrier is the new-day poll (§2.1). Detection: the barrier
// fires when the current clock day (the single-clock day index) exceeds
// HEADDAY — the Personant-Day trailer of primary HEAD. The poll seals
// only COMPLETED days; its target is the most recent completed day
// (cur-1), so after sealing it the only unsatisfiable target advances
// with the clock and re-fire is impossible (INV-2 guards B2 besides).
//
// Multi-day gaps: a poll after several absent days seals ONE barrier for
// the most recent completed day — the day-commit captures the whole
// accumulated worktree, so per-empty-gap-day ceremony would add commits
// with no content. HEADDAY then jumps forward monotonically, which is
// all detection needs. (Interpretation note vs the design's "each
// completed day is sealed exactly once": gap days with no activity are
// folded into the next real seal rather than sealed individually.)
//
// HEADDAY-⊥ bootstrap: on a trailerless primary (greenfield, or the
// R2→R3b migration) the poll anchors on the DAILY DB's last-activity
// day — the first day that completes after the daily was born/last
// written is the "first completed day" the design fires for. No daily
// (first-ever open, pre-Reconcile) → no fire; Reconcile's morning-init
// always precedes this poll in the open sequence.
// Detection vs. idempotence guard — deliberately NOT conflated (§4):
// detection reads the Personant-Day trailer off HEAD regardless of
// commit shape; the no-re-fire GUARD is the stricter day-commit-shape
// predicate for the target day. A trailered-but-non-day-shape HEAD (an
// adopt, a repair) advances HEADDAY for detection but must NOT suppress
// the seal — kill-timing walk 2: adopt-on-day-N is followed by a real
// day-N commit when the day-N barrier runs.
func (a *FileAdapter) MaybeDayBarrier(ctx context.Context) (memops.DayBarrierResult, error) {
	var res memops.DayBarrierResult
	if err := ctx.Err(); err != nil {
		return res, err
	}
	cur := autogit.CurrentDay()
	target := cur - 1

	if a.sealedDayValid && a.sealedDay >= target {
		return res, nil // fast path: this poll's target is already sealed
	}
	head, ok, err := autogit.HeadDay(ctx, a.paths, autogit.Primary)
	if err != nil {
		return res, fmt.Errorf("fileadapter: day barrier: read HEADDAY: %w", err)
	}
	switch {
	case ok && head > target:
		// HEADDAY at (or past) the current day — nothing completed
		// unsealed. (Past = a clock regression; never seal "the future".)
		return res, nil
	case ok && head == target:
		// HEADDAY reached the target via SOME primary commit — sealed only
		// if that commit is the day-commit itself (INV-2 shape guard).
		isDay, derr := autogit.HeadIsDayCommit(ctx, a.paths, autogit.Primary, target)
		if derr != nil {
			return res, fmt.Errorf("fileadapter: day barrier: shape guard: %w", derr)
		}
		if isDay {
			a.sealedDay, a.sealedDayValid = target, true
			return res, nil
		}
	case !ok:
		// HEADDAY-⊥ bootstrap (trailerless primary): fire only if a day
		// completed since the daily DB was last written.
		last, dailyOK := a.dailyLastDay()
		if !dailyOK || last > target {
			return res, nil
		}
	}

	// The no-in-flight-scope check runs BEFORE B0 (§2.2): the barrier is
	// a between-turns batch op and never overlaps another scope.
	if err := a.checkNoInFlightScope(); err != nil {
		return res, fmt.Errorf("fileadapter: day barrier: %w", err)
	}
	if err := a.runBarrier(ctx, target, &res); err != nil {
		return res, err
	}
	res.DaysSealed = append(res.DaysSealed, target)
	return res, nil
}

// runBarrier drives one fresh barrier for `day`: B0 marker, then the
// B1-B6 body. An error anywhere leaves the op=barrier marker in place —
// it is the idempotent re-entry token recovery detects (Pending) and
// this adapter completes on the next open.
func (a *FileAdapter) runBarrier(ctx context.Context, day int, res *memops.DayBarrierResult) error {
	start := clock.Profiling()
	// Sample the day's loose-object accrual before B4 evaporates it (the
	// §6.2 observable). Best-effort: a count failure records zero.
	if n, lerr := autogit.LooseObjectCount(a.paths, autogit.Daily); lerr == nil {
		res.DailyLooseObjects = n
	}
	// B0 — the marker owns the WHOLE sequence including B1's batch (F4).
	if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpBarrier, Day: day}); err != nil {
		return fmt.Errorf("fileadapter: day barrier: marker: %w", err)
	}
	a.logBarrier("begin", fmt.Sprintf("day=%d", day))
	crashpoint.At(cpBarrierPreArchival)

	if err := a.drainArchivalPressure(ctx, day, res); err != nil {
		return err
	}
	crashpoint.At(cpBarrierPostArchival)

	if err := a.barrierTail(ctx, day, res, false); err != nil {
		return err
	}
	res.BarrierDuration += clock.Since(start)
	return nil
}

// drainArchivalPressure is B1: archival-if-pressure against PRIMARY,
// via archiveBatch inside the already-open barrier scope (no nested
// op=archival marker, F4). Skipped under the high-water mark; then B2
// is the sole worktree capture. Shared by the fresh run and the
// completion routine — a barrier killed before its drain re-runs it
// (§2.3 "before B1 → re-run barrier from B1"), so the crash cannot
// silently defer the day's archival pressure.
func (a *FileAdapter) drainArchivalPressure(ctx context.Context, day int, res *memops.DayBarrierResult) error {
	recs, err := a.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		return fmt.Errorf("fileadapter: day barrier: list threads: %w", err)
	}
	ids := memops.SelectArchivalCandidates(recs)
	if len(ids) == 0 {
		return nil
	}
	r, aerr := a.archiveBatch(ctx, ids, day)
	if aerr != nil {
		return fmt.Errorf("fileadapter: day barrier: archival: %w", aerr)
	}
	archived := 0
	for _, o := range r.Outcomes {
		if o.Archived {
			archived++
			res.ArchivedThreads = append(res.ArchivedThreads, o.ThrID)
		}
	}
	a.logBarrier("archived", fmt.Sprintf("day=%d count=%d", day, archived))
	// Under-drain forensics (carried over from the turn-close drain):
	// pressure the retired population cannot relieve is a standing
	// condition the long sims must see.
	if target := len(recs) - memops.ArchiveLowWater; archived < target {
		_ = eventlog.Log(a.paths, memops.LogCategoryArchive, "under-drain",
			fmt.Sprintf("spine=%d low-water=%d wanted=%d archived=%d retired-exhausted",
				len(recs)-archived, memops.ArchiveLowWater, target, archived))
	}
	return nil
}

// barrierTail is B2-B6 + marker clear — shared verbatim by the fresh
// run (recovering=false) and the §2.3 idempotent completion routine
// (recovering=true, which arms the §2.7 quarantine-and-proceed posture
// on the persistent B2/B6 verify failures).
func (a *FileAdapter) barrierTail(ctx context.Context, day int, res *memops.DayBarrierResult, recovering bool) error {
	// B2 — day-commit, INV-2 guarded.
	if err := a.dayCommit(ctx, day, res, recovering); err != nil {
		return err
	}
	crashpoint.At(cpBarrierPostDayCommit)

	// B3 — rebuild derived from the now-committed worktree (F5). When B1
	// ran, its internal regen already freshened derived and this is a
	// near-no-op; it is load-bearing on the archival-skipped + overnight-
	// hand-edit path. Re-stamps the watermark against the pre-nuke daily
	// HEAD (or skips the stamp when daily is already gone — completion).
	if err := recovery.RebuildDerived(ctx, a.paths, index.Options{Quiet: true}); err != nil {
		return fmt.Errorf("fileadapter: day barrier: rebuild derived: %w", err)
	}
	crashpoint.At(cpBarrierPostRebuild)

	// B4 — nuke daily. Idempotent (rm of an absent path is a no-op). The
	// HEAD file goes first so the registered midNuke kill point leaves a
	// REAL half-nuked (half-created) on-disk state for R4.
	if _, err := os.Stat(a.paths.GitDaily); err == nil {
		_ = os.Remove(filepath.Join(a.paths.GitDaily, "HEAD"))
		crashpoint.At(cpBarrierMidNuke)
	}
	if err := autogit.NukeDaily(a.paths); err != nil {
		return fmt.Errorf("fileadapter: day barrier: %w", err)
	}
	crashpoint.At(cpBarrierPostNuke)

	// B5 — re-init daily + baseline commit of the current worktree.
	miStart := clock.Profiling()
	if err := autogit.InitDaily(ctx, a.paths); err != nil {
		return fmt.Errorf("fileadapter: day barrier: %w", err)
	}
	if err := autogit.Add(ctx, a.paths, autogit.Daily, "."); err != nil {
		return fmt.Errorf("fileadapter: day barrier: stage baseline: %w", err)
	}
	baseline, err := autogit.CommitAllowEmpty(ctx, a.paths, autogit.Daily, "daily baseline", 0, 0)
	if err != nil {
		return fmt.Errorf("fileadapter: day barrier: baseline commit: %w", err)
	}
	crashpoint.At(cpBarrierPostInit)

	// B6 — stamp watermark = baseline (INV-4; cross-barrier continuity:
	// without this every post-barrier open would see daily HEAD !=
	// watermark and spuriously full-rebuild), then assert derived-fresh
	// (the once-a-day index.Check, moved off B2 per F5).
	if err := store.WriteDerivedWatermark(a.paths, baseline); err != nil {
		return fmt.Errorf("fileadapter: day barrier: stamp watermark: %w", err)
	}
	if err := a.assertDerivedFresh(ctx, recovering); err != nil {
		return err
	}
	res.MorningInitDuration += clock.Since(miStart)
	a.logBarrier("reborn", fmt.Sprintf("day=%d baseline=%s", day, baseline))
	crashpoint.At(cpBarrierPostWatermark)

	// F1 tail guard: EVERY completed barrier must end with primary HEAD
	// being the day-commit for `day`. A trailered-but-non-day-shape HEAD
	// satisfies new-day DETECTION (HEADDAY == N) while failing the INV-2
	// shape GUARD, so the next same-day open would re-fire runBarrier(N)
	// — a second day-commit, a mid-day nuke of the live daily, and
	// day-N+1 content sealed under trailer N. No current writer commits
	// primary between B2 and here (dayCommit's §2.7 repair is a pure
	// worktree action), so this is a cheap expectation check; if a
	// future B3–B6 writer ever trips it, re-mint the day-commit rather
	// than complete a barrier that wedges the poll.
	isDay, derr := autogit.HeadIsDayCommit(ctx, a.paths, autogit.Primary, day)
	if derr != nil {
		return fmt.Errorf("fileadapter: day barrier: tail shape check: %w", derr)
	}
	if !isDay {
		a.logBarrier("re-mint", fmt.Sprintf("day=%d head-not-day-shape", day))
		if err := autogit.Add(ctx, a.paths, autogit.Primary, "."); err != nil {
			return fmt.Errorf("fileadapter: day barrier: stage re-mint: %w", err)
		}
		if _, err := autogit.CommitAllowEmpty(ctx, a.paths, autogit.Primary,
			autogit.DayCommitMessage(day), 0, 0); err != nil {
			return fmt.Errorf("fileadapter: day barrier: re-mint day-commit: %w", err)
		}
	}

	// Marker clear is the LAST step — the §2.6 loop bound depends on it.
	if err := store.ClearMarker(a.paths); err != nil {
		return fmt.Errorf("fileadapter: day barrier: clear marker: %w", err)
	}
	a.sealedDay, a.sealedDayValid = day, true
	a.logBarrier("complete", fmt.Sprintf("day=%d", day))
	return nil
}

// dayCommit is B2: stage EVERYTHING (including overnight hand-edits
// Reconcile's cell 3 deferred — F5) and mint the trailered day-commit
// with AllowEmptyCommits (F3: an empty day still seals, so HEADDAY is
// always defined and there is no tag path). Guarded (INV-2): a HEAD
// that is already a DayCommitMessage-shape commit for `day` skips the
// mint — the guard is the shape match, NOT the trailer (every primary
// commit is trailered, so a roll-forward stamp or an adopt must not
// satisfy it).
//
// Integrity gate: STRUCTURAL spine integrity only — verify Errors,
// never Drift: derived legitimately lags the just-staged worktree until
// B3 rebuilds it (F5), so the CheckSpineIntegrity flag (whose HasErrors
// folds derived drift in) would false-fail every mid-day-stale barrier.
// The gate runs on the STAGED worktree BEFORE the mint — byte-for-byte
// the tree the commit would capture, so it gates exactly what a
// post-flag would, with the stronger consequence that a spine-broken
// day-commit can NEVER land: "primary HEAD is always spine-good" (§2.7)
// holds for B2 too, which makes HEAD the universal scoped-restore
// source and keeps INV-2's exactly-one-day-commit true even across a
// §2.7 repair (F1: the one mint happens only after the worktree is
// proven good, so no repair/re-mint commits ever fork or trail it).
//
// Failure semantics: fresh run → error with the marker in place (the
// first idempotent drive; nothing landed on primary). Completion
// re-drive (recovering=true) → the failure has persisted across one
// idempotent re-drive, so the §2.7 quarantine-and-proceed posture runs
// (spineQuarantineRepair): quarantine the offending paths' bytes,
// scoped-restore exactly those paths from primary HEAD, re-verify once,
// refuse (marker intact, no primary mutation) if still failing.
func (a *FileAdapter) dayCommit(ctx context.Context, day int, res *memops.DayBarrierResult, recovering bool) error {
	already, err := autogit.HeadIsDayCommit(ctx, a.paths, autogit.Primary, day)
	if err != nil {
		return fmt.Errorf("fileadapter: day barrier: day-commit guard: %w", err)
	}
	if already {
		if !recovering {
			return nil // idempotent skip within a live run
		}
		// Completion re-drive with the day-commit already on HEAD: the
		// commit itself was spine-good when minted (the pre-mint gate),
		// but the worktree may have been hand-broken while the marker was
		// left. Re-assert; §2.7 on failure — a pure WORKTREE repair
		// (restore source == HEAD), so HEAD stays the day-commit and no
		// primary commit is minted.
		rep, verr := verify.Verify(a.paths, verify.VerifyOptions{Quiet: true})
		if verr != nil {
			return fmt.Errorf("fileadapter: day barrier: re-verify spine: %w", verr)
		}
		if len(rep.Errors) > 0 {
			if err := a.spineQuarantineRepair(ctx, rep.Errors); err != nil {
				return err
			}
			a.logBarrier("day-committed", fmt.Sprintf("day=%d repaired=true", day))
		}
		return nil
	}
	preBytes := primaryObjectBytes(a.paths)
	start := clock.Profiling()
	if err := autogit.Add(ctx, a.paths, autogit.Primary, "."); err != nil {
		return fmt.Errorf("fileadapter: day barrier: stage day-commit: %w", err)
	}
	rep, verr := verify.Verify(a.paths, verify.VerifyOptions{Quiet: true})
	if verr != nil {
		return fmt.Errorf("fileadapter: day barrier: verify spine: %w", verr)
	}
	if len(rep.Errors) > 0 {
		if !recovering {
			return fmt.Errorf("fileadapter: day barrier: day-commit spine check: %d structural error(s), first: %s: %s",
				len(rep.Errors), rep.Errors[0].Path, rep.Errors[0].Message)
		}
		if err := a.spineQuarantineRepair(ctx, rep.Errors); err != nil {
			return err
		}
		// The restore changed the worktree; re-stage before the mint.
		if err := autogit.Add(ctx, a.paths, autogit.Primary, "."); err != nil {
			return fmt.Errorf("fileadapter: day barrier: restage day-commit: %w", err)
		}
	}
	if _, cerr := autogit.CommitAllowEmpty(ctx, a.paths, autogit.Primary,
		autogit.DayCommitMessage(day), 0, 0); cerr != nil {
		return fmt.Errorf("fileadapter: day barrier: day-commit: %w", cerr)
	}
	res.DayCommitDuration += clock.Since(start)
	if post := primaryObjectBytes(a.paths); post > preBytes {
		res.DayCommitBytes += post - preBytes
	}
	a.logBarrier("day-committed", fmt.Sprintf("day=%d", day))
	return nil
}

// spineQuarantineRepair is the §2.7 quarantine-and-proceed posture for
// a persistent B2 spine-integrity failure. It NEVER resets and NEVER
// commits: the offending paths (from the verify findings) are
// quarantined byte-exact, scoped-restored from primary HEAD — which is
// always spine-good now that B2's gate runs before the mint, so no
// parent walk or day-shape special case is needed — and the single
// §2.7 re-run is the re-verify at the end. A path absent from HEAD's
// tree is an orphan add: quarantined and removed (nothing valid exists
// to restore to). The CALLER owns any subsequent staging/mint, so a
// completed barrier always ends with primary HEAD being the day-commit
// (F1 — no trailing repair commit to leave HEAD non-day-shape and
// re-fire the barrier on the next same-day open), and a refused open
// leaves primary exactly as it found it (no accretion while wedged).
func (a *FileAdapter) spineQuarantineRepair(ctx context.Context, findings []memops.VerifyFinding) error {
	if len(findings) == 0 {
		return errors.New("fileadapter: day barrier: spine verify failed but reported no offending paths")
	}
	head, err := autogit.HeadHash(ctx, a.paths, autogit.Primary)
	if err != nil {
		return err
	}
	q := newBarrierQuarantine(a.paths)
	seen := map[string]struct{}{}
	for _, f := range findings {
		rel := findingRel(f.Path)
		if rel == "" {
			continue
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		if _, qerr := q.quarantineAndRemove(rel); qerr != nil {
			return fmt.Errorf("fileadapter: day barrier: quarantine %s: %w", rel, qerr)
		}
		// Scoped restore (path-scoped checkout — never reset). A checkout
		// error means the path is absent from HEAD's tree: orphan add,
		// stays removed.
		if err := autogit.Checkout(ctx, a.paths, autogit.Primary, head, rel, 0, 0); err == nil {
			a.scopedRestored = append(a.scopedRestored, rel)
		}
	}
	if len(seen) == 0 {
		return errors.New("fileadapter: day barrier: spine verify failure carried no mappable paths")
	}
	a.barrierQuarantined = append(a.barrierQuarantined, q.moved...)
	a.barrierQuarantineDir = q.relDir()
	a.logBarrier("quarantined", fmt.Sprintf("paths=%d dir=%s restored=%d", len(q.moved), q.relDir(), len(a.scopedRestored)))

	// The single §2.7 re-run: re-verify the repaired worktree —
	// structural errors only, same rationale as dayCommit's gate
	// (derived drift belongs to B3/B6).
	rep, verr := verify.Verify(a.paths, verify.VerifyOptions{Quiet: true})
	if verr != nil {
		return fmt.Errorf("fileadapter: day barrier: post-repair verify: %w", verr)
	}
	if len(rep.Errors) > 0 {
		return fmt.Errorf("fileadapter: day barrier: spine still broken after quarantine-and-proceed (%d error(s))", len(rep.Errors))
	}
	return nil
}

// assertDerivedFresh is B6's once-a-day derived integrity gate. On
// failure during a completion re-drive (recovering=true) it runs the
// §2.7 posture scoped to derived artifacts: quarantine the offending
// derived paths (byte-exact), rebuild once, re-check; still failing →
// error with the op=barrier marker intact (refuse-to-open). Derived
// artifacts are gitignored (never in primary HEAD), so the "absent from
// HEAD's tree ⇒ orphan add ⇒ quarantine + remove" arm applies and the
// rebuild is what restores consistency.
func (a *FileAdapter) assertDerivedFresh(ctx context.Context, recovering bool) error {
	check, err := index.Check(a.paths, index.Options{Quiet: true})
	if err != nil {
		return fmt.Errorf("fileadapter: day barrier: derived assert: %w", err)
	}
	if check.OK() {
		return nil
	}
	if !recovering {
		return fmt.Errorf("fileadapter: day barrier: derived assert: %d drift(s) after rebuild", len(check.Drifts))
	}
	q := newBarrierQuarantine(a.paths)
	for _, d := range check.Drifts {
		if rel := findingRel(d.Path); rel != "" {
			if _, qerr := q.quarantineAndRemove(rel); qerr != nil {
				return fmt.Errorf("fileadapter: day barrier: quarantine derived %s: %w", rel, qerr)
			}
		}
	}
	if len(q.moved) > 0 {
		a.barrierQuarantined = append(a.barrierQuarantined, q.moved...)
		a.barrierQuarantineDir = q.relDir()
		a.logBarrier("quarantined", fmt.Sprintf("paths=%d dir=%s", len(q.moved), q.relDir()))
	}
	if err := recovery.RebuildDerived(ctx, a.paths, index.Options{Quiet: true}); err != nil {
		return fmt.Errorf("fileadapter: day barrier: rebuild after quarantine: %w", err)
	}
	check, err = index.Check(a.paths, index.Options{Quiet: true})
	if err != nil {
		return fmt.Errorf("fileadapter: day barrier: derived re-assert: %w", err)
	}
	if !check.OK() {
		return fmt.Errorf("fileadapter: day barrier: derived assert still failing after quarantine+rebuild (%d drift(s))", len(check.Drifts))
	}
	// The quarantined derived paths have now been REPLACED by the
	// rebuild — regeneration is the scoped-restore equivalent for
	// gitignored derived artifacts, which never exist in primary HEAD
	// (see the ScopedRestored field doc). Recorded only after the
	// re-check proves the replacement is good, so the field never names
	// a path the posture failed to make whole.
	a.scopedRestored = append(a.scopedRestored, q.moved...)
	return nil
}

// findingRel maps a verify/check finding path onto a home-relative file
// path: a "file[line]" location loses its bracket suffix; a path that
// names no real file maps to "" (unquarantinable — the caller skips it).
func findingRel(p string) string {
	if i := strings.IndexByte(p, '['); i >= 0 {
		p = p[:i]
	}
	return filepath.ToSlash(strings.TrimSpace(p))
}

// dailyLastDay returns the day index of the daily DB's HEAD commit —
// the HEADDAY-⊥ bootstrap anchor. ok=false when daily is absent or
// headless.
func (a *FileAdapter) dailyLastDay() (int, bool) {
	repo, err := autogit.Open(a.paths, autogit.Daily)
	if err != nil {
		return 0, false
	}
	head, err := repo.Head()
	if err != nil {
		return 0, false
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		return 0, false
	}
	return autogit.DayIndexOf(c.Committer.When), true
}

// ---------- completion (the recovery/adapter seam, F4) ----------

// completionInfo carries the adapter-side completion context merged
// into the final RecoveryReport by Reconcile's completion loop.
type completionInfo struct {
	cells          []string
	scopedRestored []string
	quarantined    []string
	quarantineDir  string
}

// completePending runs the matching completion for a typed Pending
// request. The marker is cleared only at the completion's FINAL step
// (inside barrierTail for barriers, explicitly for archival), so a
// crash mid-completion leaves it for the next process — the bounded
// two-pass loop of §2.6.
func (a *FileAdapter) completePending(ctx context.Context, p *memops.PendingCompletion) (completionInfo, error) {
	var info completionInfo
	a.scopedRestored = nil
	a.barrierQuarantined = nil
	a.barrierQuarantineDir = ""

	// The trailer day for completion commits is the MARKER day (F3); an
	// archival marker predating the Day field (0) falls back to HEADDAY-
	// at-repair, then current day (bootstrap).
	day := p.Day
	if day == 0 {
		if d, ok, err := autogit.HeadDay(ctx, a.paths, autogit.Primary); err == nil && ok {
			day = d
		} else {
			day = autogit.CurrentDay()
		}
	}

	switch p.Kind {
	case memops.PendingArchival:
		info.cells = append(info.cells, recovery.Cell8Archival)
		if err := a.completeArchivalBatch(ctx, day); err != nil {
			return info, err
		}
		if err := a.absorbIntoDaily(ctx, "archive absorb (roll-forward)"); err != nil {
			return info, err
		}
		if err := store.ClearMarker(a.paths); err != nil {
			return info, fmt.Errorf("clear archival marker: %w", err)
		}
	case memops.PendingBarrier:
		info.cells = append(info.cells, recovery.CellBarrier)
		// §2.3 idempotent barrier-recovery routine:
		//  1. complete any in-flight B1 batch from the unstamped worklist;
		//  2. re-run the B1 pressure drain (the §2.3 "before B1 → re-run
		//     barrier from B1" row): a barrier killed before/inside its
		//     drain must not silently defer the day's archival pressure.
		//     Gated on the day-commit NOT having landed — the post-B2
		//     windows can never have candidates (B1 finished before B2),
		//     and archival commits on top of a landed day-commit would
		//     leave HEAD non-day-shape (the F1 re-fire wedge);
		//  3. B2 (guarded) … B6 + marker clear — barrierTail(recovering).
		if err := a.completeArchivalBatch(ctx, day); err != nil {
			return info, err
		}
		var res memops.DayBarrierResult
		sealed, derr := autogit.HeadIsDayCommit(ctx, a.paths, autogit.Primary, day)
		if derr != nil {
			return info, fmt.Errorf("complete barrier: shape check: %w", derr)
		}
		if !sealed {
			if err := a.drainArchivalPressure(ctx, day, &res); err != nil {
				return info, err
			}
		}
		if err := a.barrierTail(ctx, day, &res, true); err != nil {
			return info, err
		}
	default:
		return info, fmt.Errorf("unknown pending completion kind %q", p.Kind)
	}
	info.scopedRestored = a.scopedRestored
	info.quarantined = a.barrierQuarantined
	info.quarantineDir = a.barrierQuarantineDir
	return info, nil
}

// completeArchivalBatch is the F1 roll-forward completion: finish a
// recorded batch from the unstamped-entry worklist (membership was
// durable BEFORE the first removal, so the worklist is deterministic).
// ensure-removed → ensure-spine-removed → regen → (deletion commit iff
// locateDeletionCommit finds none, else stamp the located one) → stamp.
// Safe to run at any §2.3 sub-window and safe to re-enter; a no-op when
// nothing is unstamped (the batch never began deleting — it re-forms on
// the next pressure check).
//
// The deletion-commit discriminator is the child-of-ParentCommitHash
// tree test (locateDeletionCommit) — NEVER worktree-clean-vs-HEAD,
// which an overnight hand-edit would fool into minting a duplicate
// deletion commit.
func (a *FileAdapter) completeArchivalBatch(ctx context.Context, day int) error {
	entries, err := store.LoadArchiveIndex(a.paths)
	if err != nil {
		return fmt.Errorf("complete archival: load index: %w", err)
	}
	var batch []memops.ArchiveEntry
	for _, e := range entries {
		if e.CommitHash == "" {
			batch = append(batch, e)
		}
	}
	if len(batch) == 0 {
		return nil
	}

	// (1) ensure-removed (absent dir = no-op) + cache coherence.
	ids := make([]string, 0, len(batch))
	for _, e := range batch {
		ids = append(ids, e.ThrID)
		if err := os.RemoveAll(store.ThreadDir(a.paths, e.ThrID)); err != nil {
			return fmt.Errorf("complete archival: remove %s: %w", e.ThrID, err)
		}
		a.fmCache.Invalidate(e.ThrID)
	}
	// (2) ensure-spine-removed (no-op when already rewritten).
	if err := store.RemoveSpineRecords(a.paths, ids); err != nil {
		return fmt.Errorf("complete archival: spine: %w", err)
	}
	// (3) regen derived (idempotent).
	if err := a.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		return fmt.Errorf("complete archival: regen derived: %w", err)
	}

	// (4) locate-or-mint the deletion commit.
	delHash := ""
	for _, e := range batch {
		if e.ParentCommitHash == "" || e.TreeHash == "" {
			continue // drift entry — cannot discriminate
		}
		h, found, lerr := recovery.LocateDeletionCommit(ctx, a.paths, e.ParentCommitHash, e.OriginalPath)
		if lerr != nil {
			return fmt.Errorf("complete archival: locate deletion commit: %w", lerr)
		}
		if found {
			delHash = h
		}
		break // one discriminator decides for the whole (single-crash) batch
	}
	if delHash == "" {
		if err := autogit.Add(ctx, a.paths, autogit.Primary, "."); err != nil {
			return fmt.Errorf("complete archival: stage deletion: %w", err)
		}
		h, cerr := autogit.CommitWithHash(ctx, a.paths, autogit.Primary,
			autogit.WithDayTrailer(fmt.Sprintf("archive: %d thread(s) (roll-forward)", len(batch)), day),
			0, autogit.CheckDerivedFresh|autogit.CheckSpineIntegrity)
		if cerr != nil {
			if !errors.Is(cerr, git.ErrEmptyCommit) {
				return fmt.Errorf("complete archival: deletion commit: %w", cerr)
			}
			// Nothing to commit AND no locatable deletion commit: leave the
			// entries unstamped — the second Reconcile pass's cell-9 stamp
			// repair refuses them honestly (never guessed).
			return nil
		}
		delHash = h
	}

	// (5) stamp.
	for i := range batch {
		batch[i].CommitHash = delHash
	}
	if err := store.AppendArchiveEntries(a.paths, batch); err != nil {
		return fmt.Errorf("complete archival: stamp index: %w", err)
	}
	if err := autogit.Add(ctx, a.paths, autogit.Primary, "."); err != nil {
		return fmt.Errorf("complete archival: stage stamp: %w", err)
	}
	if err := autogit.Commit(ctx, a.paths, autogit.Primary,
		autogit.WithDayTrailer(fmt.Sprintf("archive: stamp %d index entr%s (roll-forward)", len(batch), plural(len(batch))), day),
		0, autogit.CheckSpineIntegrity); err != nil && !errors.Is(err, git.ErrEmptyCommit) {
		return fmt.Errorf("complete archival: stamp commit: %w", err)
	}
	a.resetTurnScope()
	for _, e := range batch {
		_ = eventlog.Log(a.paths, archiveLogCategory, archiveActionArchived,
			fmt.Sprintf("thr=%s project=%s bytes=0 completed=roll-forward", e.ThrID, e.Project))
	}
	return nil
}

// ---------- small helpers ----------

// barrierQuarantine is the §2.7 byte-exact quarantine destination
// (recovery/quarantine/<stamp>-barrier/<rel>). recovery/ is gitignored,
// so quarantined bytes survive any later operation and are never
// committed.
type barrierQuarantine struct {
	paths store.PersonantPaths
	dir   string
	moved []string
}

func newBarrierQuarantine(paths store.PersonantPaths) *barrierQuarantine {
	stamp := clock.Timeline().UTC().Format("20060102T150405.000000000Z") + "-barrier"
	return &barrierQuarantine{paths: paths, dir: filepath.Join(paths.RecoveryDir, "quarantine", stamp)}
}

func (q *barrierQuarantine) relDir() string {
	if rel, err := filepath.Rel(q.paths.Home, q.dir); err == nil {
		return filepath.ToSlash(rel)
	}
	return q.dir
}

// quarantineAndRemove moves the live bytes at home-relative rel into the
// quarantine dir. A missing source reports (false, nil).
func (q *barrierQuarantine) quarantineAndRemove(rel string) (bool, error) {
	src := filepath.Join(q.paths.Home, filepath.FromSlash(rel))
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	dst := filepath.Join(q.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		return false, err
	}
	q.moved = append(q.moved, rel)
	return true, nil
}

// primaryObjectBytes sums the on-disk size of primary's object store —
// the day_commit_bytes gauge's before/after read. Best-effort: an
// unreadable entry contributes zero.
func primaryObjectBytes(paths store.PersonantPaths) int64 {
	var total int64
	root := filepath.Join(paths.Home, ".git", "objects")
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func (a *FileAdapter) logBarrier(action, details string) {
	_ = eventlog.Log(a.paths, memops.LogCategoryBarrier, action, details)
}
