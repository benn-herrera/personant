package recovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"personant/internal/autogit"
	"personant/internal/crashpoint"
	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/verify"
)

// Archival stamp repair (cell 9) and the cell-12 verify gate.

// unstampedEntries returns the archive-index entries whose deletion-
// commit token never landed (the archival crash window between the
// deletion commit and the stamp commit).
func unstampedEntries(paths store.PersonantPaths) ([]memops.ArchiveEntry, error) {
	entries, err := store.LoadArchiveIndex(paths)
	if err != nil {
		return nil, err
	}
	var out []memops.ArchiveEntry
	for _, e := range entries {
		if e.CommitHash == "" {
			out = append(out, e)
		}
	}
	return out, nil
}

// stampRepair runs the cell-9 pass: for every unstamped entry, locate
// the deletion commit — the child of the entry's stored capture commit
// (ParentCommitHash) whose tree drops OriginalPath — and stamp the
// entry with its hash. An entry whose deletion commit cannot be located
// (no recorded parent, parent unreachable, or no qualifying child) is
// left refused: recovery.unrepairable is logged, RecoverThread keeps
// rejecting the empty token, and NOTHING is guessed — a fabricated hash
// would silently corrupt the one pointer archival recovery depends on.
//
// The stamp commit stages ONLY the index file. Recovery may be running
// in a cell that deliberately leaves other dirt in place (cell 3's
// hand-edit); a whole-tree stage here would adopt that dirt as a side
// effect of an unrelated repair. It also carries no verification flags:
// recovery precedes validity (see autogit.ResetHard's flag rationale).
//
// R3b: the stamp commit targets PRIMARY (archival lives there) under
// the recovery-repair exemption of INV-5, and carries the Personant-Day
// trailer = HEADDAY-at-repair-time (F3) — a standalone stamp-repair
// never advances the day; on a trailerless (pre-R3b) primary it falls
// back to the current day, the bootstrap carve-out.
func stampRepair(ctx context.Context, paths store.PersonantPaths, rep *memops.RecoveryReport) error {
	unstamped, err := unstampedEntries(paths)
	if err != nil {
		return fmt.Errorf("recovery: stamp repair: read index: %w", err)
	}
	if len(unstamped) == 0 {
		// Re-entry convergence: a prior pass may have crashed after its
		// index write (every entry now stamped) but before the daily
		// absorb below, leaving the index as transient cell-3 dirt vs
		// daily on every subsequent open. Re-run the scoped absorb — an
		// index already at daily HEAD is an empty commit, swallowed.
		return absorbStampIntoDaily(ctx, paths)
	}
	rep.CellsHit = append(rep.CellsHit, Cell9StampRepair)

	var repaired []memops.ArchiveEntry
	for _, e := range unstamped {
		if e.ParentCommitHash == "" {
			rep.Unrepairable = append(rep.Unrepairable, e.ThrID)
			logEvent(paths, "unrepairable", "thr="+e.ThrID+" reason=no-parent-commit-recorded")
			continue
		}
		delHash, found, lerr := LocateDeletionCommit(ctx, paths, e.ParentCommitHash, e.OriginalPath)
		if lerr != nil {
			return fmt.Errorf("recovery: stamp repair %s: %w", e.ThrID, lerr)
		}
		if !found {
			rep.Unrepairable = append(rep.Unrepairable, e.ThrID)
			logEvent(paths, "unrepairable", "thr="+e.ThrID+" reason=deletion-commit-not-located")
			continue
		}
		e.CommitHash = delHash
		repaired = append(repaired, e)
	}
	if len(repaired) == 0 {
		return nil
	}

	if err := store.AppendArchiveEntries(paths, repaired); err != nil {
		return fmt.Errorf("recovery: stamp repair: write index: %w", err)
	}
	indexRel, err := archiveIndexRel(paths)
	if err != nil {
		return fmt.Errorf("recovery: stamp repair: %w", err)
	}
	if err := autogit.Add(ctx, paths, autogit.Primary, indexRel); err != nil {
		return fmt.Errorf("recovery: stamp repair: stage index: %w", err)
	}
	crashpoint.At(cpStampPreCommit)
	// Personant-Day = HEADDAY at repair time (never advances the day).
	repairDay, ok, derr := autogit.HeadDay(ctx, paths, autogit.Primary)
	if derr != nil {
		return fmt.Errorf("recovery: stamp repair: read HEADDAY: %w", derr)
	}
	if !ok {
		repairDay = autogit.CurrentDay() // trailerless pre-R3b primary — bootstrap
	}
	msg := autogit.WithDayTrailer(
		fmt.Sprintf("recovery: stamp %d archive entr%s", len(repaired), plural(len(repaired))), repairDay)
	if err := autogit.Commit(ctx, paths, autogit.Primary, msg, 0, 0); err != nil {
		// The index write already landed; an empty commit here means a
		// re-entered pass committed it before crashing. Converged — accept.
		if !errors.Is(err, git.ErrEmptyCommit) {
			return fmt.Errorf("recovery: stamp repair: commit: %w", err)
		}
	}
	if err := absorbStampIntoDaily(ctx, paths); err != nil {
		return err
	}
	for _, e := range repaired {
		rep.StampRepaired = append(rep.StampRepaired, e.ThrID)
		logEvent(paths, "stamp-repaired", "thr="+e.ThrID+" commit="+e.CommitHash)
	}
	return nil
}

// absorbStampIntoDaily stages the archive index (scoped — never a full
// sweep, which would adopt unrelated cell-3 dirt) into DAILY and
// commits it flag-free, swallowing the empty commit. The index rewrite
// changes the worktree after daily's last commit while the stamp commit
// lands only in primary; without this absorb the repaired home reads
// markerless-dirty (cell 3) on every open, and a later torn-turn reset
// to daily HEAD could revert the repair. Runs on the main stampRepair
// path AND on its re-entry (zero unstamped entries — a prior pass may
// have crashed between index write and absorb), so both converge to a
// clean tree. A home with no archive index yet is a no-op.
func absorbStampIntoDaily(ctx context.Context, paths store.PersonantPaths) error {
	if _, err := os.Stat(paths.ArchiveIndex); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("recovery: stamp absorb: stat index: %w", err)
	}
	indexRel, err := archiveIndexRel(paths)
	if err != nil {
		return fmt.Errorf("recovery: stamp absorb: %w", err)
	}
	if err := autogit.AddPaths(ctx, paths, autogit.Daily, []string{indexRel}); err != nil {
		return fmt.Errorf("recovery: stamp absorb: stage index (daily): %w", err)
	}
	if err := autogit.Commit(ctx, paths, autogit.Daily,
		"recovery: stamp absorb", 0, 0); err != nil && !errors.Is(err, git.ErrEmptyCommit) {
		return fmt.Errorf("recovery: stamp absorb: commit: %w", err)
	}
	return nil
}

// archiveIndexRel returns the archive index's home-relative,
// slash-separated path — the form autogit's scoped staging speaks.
func archiveIndexRel(paths store.PersonantPaths) (string, error) {
	rel, err := filepath.Rel(paths.Home, paths.ArchiveIndex)
	if err != nil {
		return "", fmt.Errorf("index path: %w", err)
	}
	return filepath.ToSlash(rel), nil
}

// LocateDeletionCommit walks primary HEAD's ancestry for the child of
// parentHash whose tree no longer contains originalPath while the
// parent's tree does — the deletion commit. History is linear
// (single-parent) in this substrate; children appear before their
// parent in the walk, so the search stops once parentHash itself is
// reached. A deletion commit orphaned by a reset is unreachable from
// HEAD and correctly reports not-found (its entry stays refused).
//
// Exported (#94 R3b): the adapter's F1 roll-forward completion uses it
// as the deletion-commit discriminator — NEVER worktree-clean-vs-HEAD,
// which an overnight hand-edit would fool into minting a duplicate.
func LocateDeletionCommit(ctx context.Context, paths store.PersonantPaths, parentHash, originalPath string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if originalPath == "" {
		return "", false, nil
	}
	repo, err := autogit.Open(paths, autogit.Primary)
	if err != nil {
		return "", false, fmt.Errorf("open primary repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", false, fmt.Errorf("HEAD: %w", err)
	}
	parent := plumbing.NewHash(parentHash)

	iter, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return "", false, fmt.Errorf("log: %w", err)
	}
	defer iter.Close()

	var match string
	err = iter.ForEach(func(c *object.Commit) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.Hash == parent {
			return errStopIter // walked past every possible child
		}
		isChild := false
		for _, p := range c.ParentHashes {
			if p == parent {
				isChild = true
				break
			}
		}
		if !isChild {
			return nil
		}
		childHas, err := treeHasPath(c, originalPath)
		if err != nil {
			return err
		}
		if childHas {
			return nil // path still present — not the deletion commit
		}
		parentCommit, err := repo.CommitObject(parent)
		if err != nil {
			return fmt.Errorf("load parent %s: %w", parentHash, err)
		}
		parentHas, err := treeHasPath(parentCommit, originalPath)
		if err != nil {
			return err
		}
		if !parentHas {
			return nil // parent never held the path — wrong lineage
		}
		match = c.Hash.String()
		return errStopIter
	})
	if err != nil && !errors.Is(err, errStopIter) && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	return match, match != "", nil
}

var errStopIter = errors.New("stop iteration")

func treeHasPath(c *object.Commit, path string) (bool, error) {
	tree, err := c.Tree()
	if err != nil {
		return false, fmt.Errorf("tree of %s: %w", c.Hash, err)
	}
	if _, err := tree.FindEntry(path); err != nil {
		if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("find %q in %s: %w", path, c.Hash, err)
	}
	return true, nil
}

// verifyStructural runs the read-only structural validation pass for
// the cell-12 adopt gate. The caller gates on report.Errors only —
// derived Drift is expected on a legacy home and is rebuilt immediately
// after the adopt.
func verifyStructural(paths store.PersonantPaths) (memops.VerifyReport, error) {
	return verify.Verify(paths, verify.VerifyOptions{Quiet: true})
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
