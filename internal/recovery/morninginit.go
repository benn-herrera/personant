package recovery

import (
	"context"
	"fmt"

	"personant/internal/autogit"
	"personant/internal/crashpoint"
	"personant/internal/index"
	"personant/internal/store"
)

// Morning-init (#94 R3b §2.5): the daily-DB birth primitive — exactly
// the barrier's B5+B6 in isolation, reused as a recovery/first-open
// step so no code path ever observes a daily-less substrate. It is
// shared by core recovery (the daily-missing rows, op=rebaseline) and
// the adapter's barrier (B5), which is why it lives here: recovery
// cannot import the adapter (F4), but the adapter already imports
// recovery.

// Crashpoints for the R4 matrix — registered at import time for the
// coverage gate.
var (
	cpMorningInitPreBaseline  = crashpoint.Register("morninginit.preBaseline")
	cpMorningInitPostBaseline = crashpoint.Register("morninginit.postBaseline.preWatermark")
)

// MorningInit (re)creates .git-daily from the current worktree: nuke any
// half-created remnant (idempotent — rm of an absent path is a no-op,
// and the daily DB is disposable by construction, §9), init the daily
// storage, baseline-commit the whole worktree (Personant-Turn-less,
// AllowEmptyCommits so even a bare home mints a valid HEAD), and stamp
// the derived watermark = baseline hash (INV-4).
//
// Watermark honesty: the watermark claims "derived was built against
// this daily commit", so MorningInit stamps directly only when derived
// is genuinely fresh (index.Check clean); otherwise it runs
// RebuildDerived — which rebuilds and stamps the same baseline hash.
// This is the §2.5 "full rebuild runs iff derived is stale vs the
// reborn baseline" rule, and the R2→R3b watermark migration path: a
// stale R2 (primary-hash) watermark never matches and simply reads as
// stale here, forcing the one-shot rebuild — no migration code.
//
// Cost is O(active working-set bytes), independent of career-history
// length (§6.1) — it hashes the worktree, never primary history.
func MorningInit(ctx context.Context, paths store.PersonantPaths) (baselineHash string, rebuilt bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := autogit.NukeDaily(paths); err != nil {
		return "", false, err
	}
	if err := autogit.InitDaily(ctx, paths); err != nil {
		return "", false, err
	}
	if err := autogit.Add(ctx, paths, autogit.Daily, "."); err != nil {
		return "", false, fmt.Errorf("stage baseline: %w", err)
	}
	crashpoint.At(cpMorningInitPreBaseline)
	baselineHash, err = autogit.CommitAllowEmpty(ctx, paths, autogit.Daily, "daily baseline", 0, 0)
	if err != nil {
		return "", false, fmt.Errorf("baseline commit: %w", err)
	}
	crashpoint.At(cpMorningInitPostBaseline)

	res, err := index.Check(paths, index.Options{Quiet: true})
	if err != nil {
		return baselineHash, false, fmt.Errorf("check derived: %w", err)
	}
	if res.OK() {
		if err := store.WriteDerivedWatermark(paths, baselineHash); err != nil {
			return baselineHash, false, err
		}
		return baselineHash, false, nil
	}
	// Stale (the benign-morning "iff stale" arm / R2→R3b transition):
	// rebuild, which re-stamps the watermark against daily HEAD — the
	// baseline just committed.
	if err := RebuildDerived(ctx, paths, index.Options{Quiet: true}); err != nil {
		return baselineHash, true, fmt.Errorf("rebuild derived: %w", err)
	}
	return baselineHash, true, nil
}
