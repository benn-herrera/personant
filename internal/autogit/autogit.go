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
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"personant/internal/index"
	"personant/internal/memops"
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

	opts := &git.CommitOptions{Author: store.CommitSignature(repo)}
	if _, err := wt.Commit(msg, opts); err != nil {
		return fmt.Errorf("autogit.Commit: commit: %w", err)
	}

	if err := applyFlags(ctx, paths, postFlags); err != nil {
		return fmt.Errorf("%w: %w", ErrPostOpVerification, err)
	}
	return nil
}

// CommitWithHash is Commit but returns the hash of the commit it creates.
// The archival batch needs the deletion commit's hash to write into the
// archive index (recovery resolves the parent from it, Q3); a plain
// Commit hides the hash. Same pre/post-flag and ErrPostOpVerification
// semantics as Commit. On a post-flag failure the commit has already been
// written, so the hash is returned alongside the wrapped error so the
// caller can still reference it.
func CommitWithHash(ctx context.Context, paths store.PersonantPaths, msg string, preFlags, postFlags GitCheckFlags) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("autogit.CommitWithHash: %w", err)
	}
	if err := applyFlags(ctx, paths, preFlags); err != nil {
		return "", fmt.Errorf("autogit.CommitWithHash: pre-flag: %w", err)
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return "", fmt.Errorf("autogit.CommitWithHash: open repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("autogit.CommitWithHash: worktree: %w", err)
	}
	hash, err := wt.Commit(msg, &git.CommitOptions{Author: store.CommitSignature(repo)})
	if err != nil {
		return "", fmt.Errorf("autogit.CommitWithHash: commit: %w", err)
	}
	if err := applyFlags(ctx, paths, postFlags); err != nil {
		return hash.String(), fmt.Errorf("%w: %w", ErrPostOpVerification, err)
	}
	return hash.String(), nil
}

// ParentCommitHash returns the first-parent hash of the given commit. The
// personant substrate has linear, single-parent history, so the first
// parent is THE parent (Q3): an archived thread's bytes live in the
// deletion commit's parent, and recovery restores the subtree from there.
// Returns an error if the commit has no parent (a root commit cannot have
// archived a thread).
func ParentCommitHash(ctx context.Context, paths store.PersonantPaths, commitHash string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("autogit.ParentCommitHash: %w", err)
	}
	if commitHash == "" {
		return "", errors.New("autogit.ParentCommitHash: commitHash is empty")
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return "", fmt.Errorf("autogit.ParentCommitHash: open repo: %w", err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return "", fmt.Errorf("autogit.ParentCommitHash: load commit %s: %w", commitHash, err)
	}
	if len(commit.ParentHashes) == 0 {
		return "", fmt.Errorf("autogit.ParentCommitHash: commit %s has no parent", commitHash)
	}
	return commit.ParentHashes[0].String(), nil
}

// HeadHash returns the hash of the current HEAD commit in paths.Home.
// The archival batch uses it to read the integrity token from the
// committed tree (TreeHashAt) at the point where HEAD IS the capture
// commit (the deletion commit's parent-to-be) — gitignore-safe and
// mode-faithful, unlike hashing the worktree.
func HeadHash(ctx context.Context, paths store.PersonantPaths) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("autogit.HeadHash: %w", err)
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return "", fmt.Errorf("autogit.HeadHash: open repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("autogit.HeadHash: HEAD: %w", err)
	}
	return head.Hash().String(), nil
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

// CheckoutTree restores a whole directory subtree from a specific commit
// to the working tree — the directory generalization of Checkout, which
// restores a single file. It tree-walks the entry at dirPath in the
// commit and writes every blob beneath it, recreating the subtree on
// disk under paths.Home.
//
// E1/Q1 note: staging the *removal* of a tracked directory needs no
// autogit function — go-git's AddWithOptions{All:true} (Add(".")) stages
// tracked-file deletions, verified empirically (see the Q1 probe in
// archive_recovery_test.go). So the archival/recovery flows compose
// os.RemoveAll(dir) + Add(".") for removal; CheckoutTree is the restore
// half.
//
// Q3 note: a thread's deletion commit no longer contains the directory in
// its own tree — the bytes live in the deletion commit's PARENT. Recovery
// therefore passes the PARENT commit hash here (the caller resolves the
// parent; the index does not store it for I1). CheckoutTree restores the
// subtree exactly as it exists in the commit it is given.
//
// preFlags run before the writes; postFlags run after. Per the package
// doc and the Checkout doc above, integrity verification of the restored
// subtree (tree-hash compare) is the caller's purpose-specific wrapper
// composing CheckoutTree + VerifyTreeHash — NOT a new GitCheckFlag.
func CheckoutTree(ctx context.Context, paths store.PersonantPaths, commitHash, dirPath string, preFlags, postFlags GitCheckFlags) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("autogit.CheckoutTree: %w", err)
	}
	if commitHash == "" {
		return errors.New("autogit.CheckoutTree: commitHash is empty")
	}
	if dirPath == "" {
		return errors.New("autogit.CheckoutTree: dirPath is empty")
	}
	if err := applyFlags(ctx, paths, preFlags); err != nil {
		return fmt.Errorf("autogit.CheckoutTree: pre-flag: %w", err)
	}

	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return fmt.Errorf("autogit.CheckoutTree: open repo: %w", err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return fmt.Errorf("autogit.CheckoutTree: load commit %s: %w", commitHash, err)
	}
	root, err := commit.Tree()
	if err != nil {
		return fmt.Errorf("autogit.CheckoutTree: commit tree: %w", err)
	}
	subtree, err := root.Tree(dirPath)
	if err != nil {
		return fmt.Errorf("autogit.CheckoutTree: %q in commit %s: %w", dirPath, commitHash, err)
	}

	// Walk every file entry beneath the subtree (Files() recurses) and
	// write each blob to its host-filesystem location under paths.Home.
	// The working tree IS the host filesystem under paths.Home, so a
	// direct os.WriteFile is the simplest correct restore — same rationale
	// as Checkout's single-file write.
	fi := subtree.Files()
	for {
		f, ferr := fi.Next()
		if ferr == io.EOF {
			break
		}
		if ferr != nil {
			return fmt.Errorf("autogit.CheckoutTree: walk %q: %w", dirPath, ferr)
		}
		contents, cerr := f.Contents()
		if cerr != nil {
			return fmt.Errorf("autogit.CheckoutTree: read %q: %w", f.Name, cerr)
		}
		// Write with the COMMITTED file mode (F3): the integrity token is the
		// committed tree hash, which encodes mode. A fresh restore that hard-
		// coded 0o644 would re-hash to a different tree for an executable file
		// and FALSE-fail VerifyTreeHash. f.Mode is the git tree entry mode;
		// ToOSFileMode yields the os.FileMode to persist (0o644 for a regular
		// file, 0o755 for an executable — matching WorktreeTreeHash's stat-based
		// mode derivation on the verify side).
		osMode, merr := f.Mode.ToOSFileMode()
		if merr != nil {
			return fmt.Errorf("autogit.CheckoutTree: mode of %q: %w", f.Name, merr)
		}
		relPath := path.Join(dirPath, f.Name) // f.Name is subtree-relative, slash-separated
		dst := filepath.Join(paths.Home, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("autogit.CheckoutTree: mkdir parent of %q: %w", relPath, err)
		}
		if err := os.WriteFile(dst, []byte(contents), osMode.Perm()); err != nil {
			return fmt.Errorf("autogit.CheckoutTree: write %q: %w", relPath, err)
		}
	}

	if err := applyFlags(ctx, paths, postFlags); err != nil {
		return fmt.Errorf("%w: %w", ErrPostOpVerification, err)
	}
	return nil
}

// TreeHashAt computes the git tree-object hash of the directory dirPath as
// it exists in the given commit. This is the capture primitive: at
// archival time the thread directory is still tracked at HEAD, so the
// archiver passes the HEAD commit (or, equivalently, the deletion
// commit's parent) and gets the integrity token to store in the index.
//
// Q2 resolution: go-git exposes the subtree's tree-object hash directly
// via Tree.FindEntry(dirPath).Hash — no awkward worktree hashing and no
// byte-diff fallback needed for capture. (WorktreeTreeHash below handles
// the verify side, where the restored directory is on disk, not yet in a
// commit.)
func TreeHashAt(ctx context.Context, paths store.PersonantPaths, commitHash, dirPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("autogit.TreeHashAt: %w", err)
	}
	if commitHash == "" {
		return "", errors.New("autogit.TreeHashAt: commitHash is empty")
	}
	if dirPath == "" {
		return "", errors.New("autogit.TreeHashAt: dirPath is empty")
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		return "", fmt.Errorf("autogit.TreeHashAt: open repo: %w", err)
	}
	commit, err := repo.CommitObject(plumbing.NewHash(commitHash))
	if err != nil {
		return "", fmt.Errorf("autogit.TreeHashAt: load commit %s: %w", commitHash, err)
	}
	root, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("autogit.TreeHashAt: commit tree: %w", err)
	}
	entry, err := root.FindEntry(dirPath)
	if err != nil {
		return "", fmt.Errorf("autogit.TreeHashAt: %q in commit %s: %w", dirPath, commitHash, err)
	}
	return entry.Hash.String(), nil
}

// WorktreeTreeHash computes the git tree-object hash of an on-disk
// directory under paths.Home, exactly as git would compute it for that
// subtree. It builds blob hashes for every regular file and assembles
// nested tree objects bottom-up, hashing each tree object's canonical
// encoding — so the result is directly comparable to TreeHashAt and to
// the integrity token captured at archival.
//
// This is the verify primitive (design §5 E3 / invariant 2): after
// recovery restores a subtree to disk, the caller re-hashes it here and
// compares against the stored tree hash. Symlinks and submodules are not
// part of a personant thread directory (thread.md + turns/ + files.json),
// so only regular files and directories are handled; an unexpected
// non-regular entry is an error rather than a silent skip.
func WorktreeTreeHash(paths store.PersonantPaths, dirPath string) (string, error) {
	if dirPath == "" {
		return "", errors.New("autogit.WorktreeTreeHash: dirPath is empty")
	}
	abs := filepath.Join(paths.Home, filepath.FromSlash(dirPath))
	hash, err := hashDirTree(abs)
	if err != nil {
		return "", fmt.Errorf("autogit.WorktreeTreeHash %q: %w", dirPath, err)
	}
	return hash.String(), nil
}

// hashDirTree recursively builds the git tree object for dir and returns
// its hash. Entries are sorted by git's tree-entry ordering (which
// object.Tree.Encode handles via the TreeEntrySorter on encode); we sort
// here too for a stable in-memory tree before encoding.
func hashDirTree(dir string) (plumbing.Hash, error) {
	dirents, err := os.ReadDir(dir)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	entries := make([]object.TreeEntry, 0, len(dirents))
	for _, de := range dirents {
		name := de.Name()
		full := filepath.Join(dir, name)
		switch {
		case de.IsDir():
			sub, err := hashDirTree(full)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			entries = append(entries, object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: sub})
		case de.Type().IsRegular():
			data, err := os.ReadFile(full)
			if err != nil {
				return plumbing.ZeroHash, err
			}
			blobHash := plumbing.ComputeHash(plumbing.BlobObject, data)
			mode := filemode.Regular
			if info, err := de.Info(); err == nil && info.Mode()&0o111 != 0 {
				mode = filemode.Executable
			}
			entries = append(entries, object.TreeEntry{Name: name, Mode: mode, Hash: blobHash})
		default:
			return plumbing.ZeroHash, fmt.Errorf("unsupported entry %q (not a regular file or directory)", full)
		}
	}
	sort.Sort(object.TreeEntrySorter(entries))

	tree := &object.Tree{Entries: entries}
	obj := &plumbing.MemoryObject{}
	if err := tree.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("encode tree: %w", err)
	}
	return obj.Hash(), nil
}

// VerifyTreeHash restores nothing; it composes the verify side of archive
// recovery: re-hash the on-disk dirPath and compare against want. On
// mismatch it returns memops.ErrArchiveIntegrity (invariant 2) so the
// caller can abort the recovery and surface a distinct integrity failure.
// This is the purpose-specific verification wrapper the package doc
// mandates — NOT a GitCheckFlag.
func VerifyTreeHash(paths store.PersonantPaths, dirPath, want string) error {
	got, err := WorktreeTreeHash(paths, dirPath)
	if err != nil {
		return fmt.Errorf("autogit.VerifyTreeHash: %w", err)
	}
	if got != want {
		return fmt.Errorf("autogit.VerifyTreeHash %q: got %s want %s: %w", dirPath, got, want, memops.ErrArchiveIntegrity)
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

func summarizeDrifts(drifts []memops.Drift) string {
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

func summarizeFindings(errs []memops.VerifyFinding, drift []string) string {
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

