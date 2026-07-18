package autogit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"personant/internal/store"
)

// This file holds the crash-stability additions (#94, Wave R2): the
// worktree-state observable, ResetHard (the single recovery reset verb),
// and the per-turn commit trailer helpers shared by CommitTurn (writer)
// and recovery (reader).

// TurnTrailerKey is the commit-message trailer key that carries a
// per-turn commit's turn id. Recovery reads HEADTURN from it; a HEAD
// commit without the trailer reads as "no turn recorded" (⊥) — the
// normal state for every pre-per-turn-commit history.
const TurnTrailerKey = "Personant-Turn"

// TurnCommitMessage composes the per-turn commit message: a short
// subject built from reason, then the turn trailer as the final
// paragraph so ParseTurnTrailer/HeadTurn can recover the id.
func TurnCommitMessage(turnID, reason string) string {
	subject := "turn " + turnID
	if reason != "" {
		subject += ": " + reason
	}
	return subject + "\n\n" + TurnTrailerKey + ": " + turnID + "\n"
}

// ParseTurnTrailer extracts the turn id from a commit message, or ""
// when the message carries no Personant-Turn trailer. The last matching
// line wins, per git trailer convention.
func ParseTurnTrailer(message string) string {
	const prefix = TurnTrailerKey + ":"
	turn := ""
	for _, line := range strings.Split(message, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			turn = strings.TrimSpace(rest)
		}
	}
	return turn
}

// HeadTurn returns the turn id recorded in HEAD's commit-message trailer,
// or "" (⊥) when HEAD carries none — which covers every commit made
// outside the per-turn CommitTurn path (init, checkpoints, archival,
// pre-upgrade histories). Under the dual-repo scheme turns commit to
// Daily, so recovery reads HEADTURN there; Primary commits never carry
// the turn trailer (§4).
func HeadTurn(ctx context.Context, paths store.PersonantPaths, which Repo) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("autogit.HeadTurn: %w", err)
	}
	repo, err := openRepo(paths, which)
	if err != nil {
		return "", fmt.Errorf("autogit.HeadTurn: open %s repo: %w", which, err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("autogit.HeadTurn: HEAD: %w", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", fmt.Errorf("autogit.HeadTurn: load HEAD commit: %w", err)
	}
	return ParseTurnTrailer(commit.Message), nil
}

// WorktreeState is the recovery-facing summary of `git status`: which
// tracked paths differ from HEAD (index or worktree side), and which
// paths are untracked. Ignored files appear in neither list — go-git's
// Status honors .gitignore, which is load-bearing here: the marker,
// journal, watermark, and recovery artifacts are gitignored precisely so
// they never read as dirt or debris.
type WorktreeState struct {
	// DirtyPaths are tracked, repo-relative slash paths whose staging or
	// worktree state differs from HEAD (modified, deleted, staged-added,
	// renamed). Sorted.
	DirtyPaths []string
	// Untracked are repo-relative slash paths present on disk, absent
	// from HEAD and the index, and not gitignored. Sorted. These are the
	// candidate set for recovery's untracked-debris sweep.
	Untracked []string
}

// Worktree computes the WorktreeState of paths.Home against the selected
// repo's HEAD. Turn-scope dirtiness is a DAILY observable (turns commit
// to daily); archival/barrier code asks Primary.
func Worktree(ctx context.Context, paths store.PersonantPaths, which Repo) (WorktreeState, error) {
	if err := ctx.Err(); err != nil {
		return WorktreeState{}, fmt.Errorf("autogit.Worktree: %w", err)
	}
	repo, err := openRepo(paths, which)
	if err != nil {
		return WorktreeState{}, fmt.Errorf("autogit.Worktree: open %s repo: %w", which, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return WorktreeState{}, fmt.Errorf("autogit.Worktree: worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return WorktreeState{}, fmt.Errorf("autogit.Worktree: status: %w", err)
	}

	var state WorktreeState
	for path, fs := range status {
		switch {
		case fs.Staging == git.Untracked && fs.Worktree == git.Untracked:
			state.Untracked = append(state.Untracked, path)
		case fs.Staging == git.Unmodified && fs.Worktree == git.Unmodified:
			// go-git can report a no-op entry; skip.
		default:
			state.DirtyPaths = append(state.DirtyPaths, path)
		}
	}
	sort.Strings(state.DirtyPaths)
	sort.Strings(state.Untracked)
	return state, nil
}

// ResetHard reverts every tracked path in paths.Home to its HEAD state
// and resets the index to HEAD, returning the repo-relative paths it
// reverted (sorted) so the caller's derived rebuild stays scoped to
// O(changed). Untracked and gitignored files are never touched — the
// untracked-debris sweep is a separate, explicitly-scoped recovery step,
// and gitignored operational files (marker, journal, watermark, recovery
// artifacts) must survive the reset by design.
//
// Semantics match `git reset --hard HEAD` for tracked content: modified
// files are restored to their committed bytes and mode, deleted tracked
// files are recreated, and a staged-but-never-committed file is dropped
// from the index but LEFT ON DISK (it becomes untracked — exactly git's
// behavior, and exactly what recovery's debris sweep expects to find).
// The implementation is a mixed index reset plus per-file restore from
// the HEAD tree rather than go-git's whole-tree hard checkout, so the
// no-touch guarantee for untracked/ignored files holds by construction.
//
// Policy flags: ResetHard deliberately takes NO GitCheckFlags. Every
// other state-changing verb in this package can gate on systemic
// validity; a reset cannot, because it runs precisely when the substrate
// is known-inconsistent (recovery, marker present). A pre-flag would
// spuriously fail on the very damage the reset exists to undo, and a
// post-flag CheckDerivedFresh would spuriously fail because derived
// state is stale by construction until recovery's own rebuild step runs.
// Reset PRECEDES validity; validation is the recovery orchestrator's
// job, after normalization.
//
// Dual-repo note (INV-1): ResetHard is only ever CALLED against Daily —
// resetting the worktree to primary HEAD (which lags by up to a day)
// would silently destroy the day. The selector exists for uniformity
// and greppability, not because a Primary reset is ever legitimate.
func ResetHard(ctx context.Context, paths store.PersonantPaths, which Repo) (revertedPaths []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("autogit.ResetHard: %w", err)
	}
	repo, err := openRepo(paths, which)
	if err != nil {
		return nil, fmt.Errorf("autogit.ResetHard: open %s repo: %w", which, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("autogit.ResetHard: worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("autogit.ResetHard: status: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("autogit.ResetHard: HEAD: %w", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("autogit.ResetHard: load HEAD commit: %w", err)
	}

	// (1) Index → HEAD. MixedReset never touches the worktree, so
	// untracked/ignored files are structurally out of reach.
	if err := wt.Reset(&git.ResetOptions{Mode: git.MixedReset, Commit: head.Hash()}); err != nil {
		return nil, fmt.Errorf("autogit.ResetHard: index reset: %w", err)
	}

	// (2) Worktree: restore every non-untracked status path that exists
	// in the HEAD tree. A path absent from HEAD (staged-add debris) is
	// left on disk as untracked, per the contract above.
	for path, fs := range status {
		if fs.Staging == git.Untracked && fs.Worktree == git.Untracked {
			continue
		}
		file, ferr := commit.File(path)
		if ferr != nil {
			if errors.Is(ferr, object.ErrFileNotFound) {
				continue // not in HEAD — becomes untracked, sweep territory
			}
			return nil, fmt.Errorf("autogit.ResetHard: %q in HEAD: %w", path, ferr)
		}
		contents, cerr := file.Contents()
		if cerr != nil {
			return nil, fmt.Errorf("autogit.ResetHard: read %q: %w", path, cerr)
		}
		osMode, merr := file.Mode.ToOSFileMode()
		if merr != nil {
			return nil, fmt.Errorf("autogit.ResetHard: mode of %q: %w", path, merr)
		}
		dst := filepath.Join(paths.Home, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("autogit.ResetHard: mkdir parent of %q: %w", path, err)
		}
		if err := os.WriteFile(dst, []byte(contents), osMode.Perm()); err != nil {
			return nil, fmt.Errorf("autogit.ResetHard: write %q: %w", path, err)
		}
		revertedPaths = append(revertedPaths, path)
	}
	sort.Strings(revertedPaths)
	return revertedPaths, nil
}
