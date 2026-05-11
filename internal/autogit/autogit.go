// Package autogit is the policy-aware autonomic git surface for
// personant's home tree (~/.personant/.git/).
//
// Two distinct validation concerns:
//
//   - Systemic validation: substrate-wide consistency checks ("are we
//     generally good before/after this op?"). Parameter-less; operates
//     on the whole substrate. Declared as GitCheckFlags bitmasks on
//     each autonomic operation.
//
//   - Specific verification: operation-tied parameterized verifications
//     ("did this particular archive recover correctly?"). Lives inside
//     purpose-specific wrapper functions that compose autogit
//     operations with their own verification. NOT expressed through
//     this package's bitflag surface — flags carry kind, not arguments.
//
// Implementation uses go-git (github.com/go-git/go-git/v5) in-process
// rather than shelling out, for six-month-sim performance, test
// isolation (no system git required), context-cancellation support,
// and typed errors.
//
// The package sits above internal/store, internal/index, and
// internal/verify in the layering: it composes substrate operations
// (paths only — no other store-state knowledge) with policy checks
// (index.Check, verify.Verify). store/init.go uses go-git directly for
// the initial commit because that path has no canonical state to
// validate yet — adding a dependency from store to autogit would
// introduce a layering cycle for no benefit.
package autogit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"personant/internal/index"
	"personant/internal/store"
	"personant/internal/verify"
)

// GitCheckFlags is the bitmask of systemic validation checks runnable
// before/after a state-changing git operation. Vocabulary is small and
// bounded; 64 flag slots cover any plausible growth.
type GitCheckFlags uint64

const (
	// CheckDerivedFresh verifies symbols.jsonl and per-project digest.json
	// are up to date with canonical sources. Wraps index.Check.
	CheckDerivedFresh GitCheckFlags = 1 << iota

	// CheckSpineIntegrity wraps verify.Verify in full structural-
	// validation mode: schema bounds, ID uniqueness, RFC3339 timestamps,
	// anchor cardinality, project references resolve.
	CheckSpineIntegrity
)

// ErrPostOpVerification distinguishes "op failed" from "op succeeded
// but verification surfaced drift." Wrap-compatible via errors.Is. A
// pre-op flag failure returns the underlying check error without this
// sentinel; a post-op flag failure wraps it.
var ErrPostOpVerification = errors.New("autogit: post-op verification failed")

// Commit creates a commit in paths.Home with the given message.
// preFlags run before the commit; postFlags run after a successful
// commit. A pre-flag failure aborts before any git mutation; a
// post-flag failure is wrapped with ErrPostOpVerification (the commit
// has already been written and HEAD has moved).
//
// The caller is responsible for staging changes via Add before calling
// Commit. Empty commits are rejected by go-git with ErrEmptyCommit;
// callers that want bookkeeping commits must stage something first.
func Commit(ctx context.Context, paths store.PersonantPaths, msg string, preFlags, postFlags GitCheckFlags) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("autogit.Commit: %w", err)
	}
	if err := applyFlags(ctx, paths, preFlags); err != nil {
		return fmt.Errorf("autogit.Commit: pre-flag: %w", err)
	}

	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return fmt.Errorf("autogit.Commit: open repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("autogit.Commit: worktree: %w", err)
	}

	opts := &git.CommitOptions{Author: defaultSignature(repo)}
	if _, err := wt.Commit(msg, opts); err != nil {
		return fmt.Errorf("autogit.Commit: commit: %w", err)
	}

	if err := applyFlags(ctx, paths, postFlags); err != nil {
		return fmt.Errorf("%w: %w", ErrPostOpVerification, err)
	}
	return nil
}

// Add stages files matching the given patterns. Patterns are paths
// relative to paths.Home; the literal pattern "." means "all changes
// in the worktree, honoring .gitignore." Callers that want to stage
// every change in the worktree should pass "." (or call with no
// patterns, which is treated as "."). Each named path is staged
// individually so a typo in one pattern surfaces as an error rather
// than silently dropping that file.
func Add(ctx context.Context, paths store.PersonantPaths, patterns ...string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("autogit.Add: %w", err)
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return fmt.Errorf("autogit.Add: open repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("autogit.Add: worktree: %w", err)
	}

	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	for _, p := range patterns {
		if p == "." {
			if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
				return fmt.Errorf("autogit.Add: add all: %w", err)
			}
			continue
		}
		if _, err := wt.Add(p); err != nil {
			return fmt.Errorf("autogit.Add: add %q: %w", p, err)
		}
	}
	return nil
}

// Checkout restores a specific file from a specific commit to the
// working tree. Implemented via tree-walk + raw write of the blob,
// not via worktree.Checkout (which is whole-tree-only) and not via
// worktree.Restore (which restores only from HEAD).
//
// preFlags run before the write; postFlags run after a successful
// write. For archive recovery — which needs *specific* verification
// of the restored blob hash — build a purpose-specific wrapper that
// composes Checkout with its own post-checkout verification. Do not
// extend GitCheckFlags for that.
func Checkout(ctx context.Context, paths store.PersonantPaths, commitHash, filePath string, preFlags, postFlags GitCheckFlags) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("autogit.Checkout: %w", err)
	}
	if commitHash == "" {
		return errors.New("autogit.Checkout: commitHash is empty")
	}
	if filePath == "" {
		return errors.New("autogit.Checkout: filePath is empty")
	}
	if err := applyFlags(ctx, paths, preFlags); err != nil {
		return fmt.Errorf("autogit.Checkout: pre-flag: %w", err)
	}

	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return fmt.Errorf("autogit.Checkout: open repo: %w", err)
	}
	hash := plumbing.NewHash(commitHash)
	commit, err := repo.CommitObject(hash)
	if err != nil {
		return fmt.Errorf("autogit.Checkout: load commit %s: %w", commitHash, err)
	}
	file, err := commit.File(filePath)
	if err != nil {
		return fmt.Errorf("autogit.Checkout: %q in commit %s: %w", filePath, commitHash, err)
	}
	contents, err := file.Contents()
	if err != nil {
		return fmt.Errorf("autogit.Checkout: read %q: %w", filePath, err)
	}

	// Write the blob to the working tree as a plain file. filePath is
	// repo-relative; resolve against paths.Home. We use os.WriteFile
	// (not the billy worktree filesystem) because the working tree IS
	// the host filesystem under paths.Home, and a direct write is the
	// simplest correct expression of "restore this blob to that path."
	dst := filepath.Join(paths.Home, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("autogit.Checkout: mkdir parent of %q: %w", filePath, err)
	}
	if err := os.WriteFile(dst, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("autogit.Checkout: write %q: %w", filePath, err)
	}

	if err := applyFlags(ctx, paths, postFlags); err != nil {
		return fmt.Errorf("%w: %w", ErrPostOpVerification, err)
	}
	return nil
}

// Tag creates a lightweight tag at a specific commit (or HEAD if
// commitHash == ""). Used for project/thread lifecycle markers per the
// git-tags-lifecycle convention. No validation policy: tagging is
// metadata-only and cannot leave the substrate in an inconsistent
// state.
func Tag(ctx context.Context, paths store.PersonantPaths, name, commitHash string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("autogit.Tag: %w", err)
	}
	if name == "" {
		return errors.New("autogit.Tag: name is empty")
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return fmt.Errorf("autogit.Tag: open repo: %w", err)
	}
	var hash plumbing.Hash
	if commitHash == "" {
		head, err := repo.Head()
		if err != nil {
			return fmt.Errorf("autogit.Tag: HEAD: %w", err)
		}
		hash = head.Hash()
	} else {
		hash = plumbing.NewHash(commitHash)
	}
	if _, err := repo.CreateTag(name, hash, nil); err != nil {
		return fmt.Errorf("autogit.Tag: create %q: %w", name, err)
	}
	return nil
}

// applyFlags walks bits low-to-high and invokes each enabled check.
// Canonical order keeps the error surface deterministic: a failure on
// CheckDerivedFresh always wins over a CheckSpineIntegrity failure
// when both would fire on the same call, so tests and forensic logs
// see stable output.
func applyFlags(ctx context.Context, paths store.PersonantPaths, flags GitCheckFlags) error {
	if flags == 0 {
		return nil
	}
	for bit := 0; bit < 64; bit++ {
		mask := GitCheckFlags(1) << bit
		if mask > flags {
			break
		}
		if flags&mask == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runCheck(paths, mask); err != nil {
			return err
		}
	}
	return nil
}

// runCheck dispatches a single flag to its check function. Kept as a
// switch (not a map) so the compiler enforces total coverage as flags
// are added.
func runCheck(paths store.PersonantPaths, flag GitCheckFlags) error {
	switch flag {
	case CheckDerivedFresh:
		res, err := index.Check(paths, index.Options{Quiet: true})
		if err != nil {
			return fmt.Errorf("CheckDerivedFresh: %w", err)
		}
		if !res.OK() {
			return fmt.Errorf("CheckDerivedFresh: %d drift(s): %s",
				len(res.Drifts), summarizeDrifts(res.Drifts))
		}
		return nil
	case CheckSpineIntegrity:
		report, err := verify.Verify(paths, verify.VerifyOptions{Quiet: true})
		if err != nil {
			return fmt.Errorf("CheckSpineIntegrity: %w", err)
		}
		if report.HasErrors() {
			return fmt.Errorf("CheckSpineIntegrity: %d error(s)/%d drift(s): %s",
				len(report.Errors), len(report.Drift),
				summarizeFindings(report.Errors, report.Drift))
		}
		return nil
	default:
		return fmt.Errorf("autogit: unknown GitCheckFlag 0x%x", uint64(flag))
	}
}

func summarizeDrifts(drifts []index.Drift) string {
	if len(drifts) == 0 {
		return ""
	}
	lim := len(drifts)
	suffix := ""
	if lim > 3 {
		lim = 3
		suffix = fmt.Sprintf(" (+%d more)", len(drifts)-3)
	}
	parts := make([]string, 0, lim)
	for i := 0; i < lim; i++ {
		d := drifts[i]
		parts = append(parts, fmt.Sprintf("%s %s", d.Status, d.Path))
	}
	return strings.Join(parts, "; ") + suffix
}

func summarizeFindings(errs []verify.Finding, drift []string) string {
	parts := make([]string, 0, 3)
	for i, e := range errs {
		if i >= 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s: %s", e.Path, e.Message))
	}
	for _, d := range drift {
		if len(parts) >= 3 {
			break
		}
		parts = append(parts, "drift: "+d)
	}
	return strings.Join(parts, "; ")
}

// defaultSignature returns a Signature for commit author/committer.
// When the repo's git config has user.name + user.email both set,
// those are used (preserving user identity). Otherwise the personant
// fallback identity ("personant" <personant@localhost>) is used so a
// fresh tempdir with no git config still produces a valid commit.
func defaultSignature(repo *git.Repository) *object.Signature {
	cfg, err := repo.Config()
	if err == nil && cfg.User.Name != "" && cfg.User.Email != "" {
		return &object.Signature{
			Name:  cfg.User.Name,
			Email: cfg.User.Email,
			When:  time.Now(),
		}
	}
	return &object.Signature{
		Name:  "personant",
		Email: "personant@localhost",
		When:  time.Now(),
	}
}

