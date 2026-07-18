package recovery

import (
	"context"
	"fmt"

	"personant/internal/autogit"
	"personant/internal/index"
	pnlog "personant/internal/log"
	"personant/internal/store"
)

// RebuildDerived is THE derived-state regeneration path: it rebuilds
// every derived artifact (symbols.jsonl + per-project digests) from
// canonical sources and then advances the derived watermark to the
// current recovery point. The watermark is written HERE and nowhere
// else — regeneration and watermark are one operation, which is what
// keeps the watermark's claim ("derived was built against commit X")
// impossible to forge by a code path that rebuilt nothing.
//
// Honesty of the stamp when the tree is dirty at regen time (archival
// regenerates mid-transaction, before its own commits): the stamped
// hash can then lag the canonical state the rebuild actually read. That
// is SAFE, never false-fresh, because the reader (Reconcile) trusts the
// watermark only on a canonically-clean worktree: dirt at the next open
// forces the stale path regardless, and a commit after the regen moves
// HEAD away from the stamp, also forcing the stale path. The only state
// that can present watermark==HEAD with a clean tree is one where the
// rebuild genuinely ran against that commit's content.
//
// A substrate without a resolvable recovery point (no git history — the
// scenario harness's bare homes) rebuilds and skips the stamp: no
// watermark is strictly slower at next open, never wrong.
func RebuildDerived(ctx context.Context, paths store.PersonantPaths, opts index.Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := index.Rebuild(paths, opts); err != nil {
		return err
	}
	head, err := autogit.HeadHash(ctx, paths)
	if err != nil {
		pnlog.Debug("recovery.RebuildDerived: no recovery point to stamp (skipping watermark): %v", err)
		return nil
	}
	if err := store.WriteDerivedWatermark(paths, head); err != nil {
		return fmt.Errorf("stamp derived watermark: %w", err)
	}
	return nil
}
