package recovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"personant/internal/autogit"
	"personant/internal/clock"
	"personant/internal/crashpoint"
	"personant/internal/eventlog"
	"personant/internal/memops"
	"personant/internal/store"
)

// Filesystem phases of recovery: the log-tail heal, the unconditional
// tmp- residue sweep, the untracked-debris sweep, the quarantine that
// makes both sweeps (and the reset) non-lossy, and the logs/
// preserve-restore bracket around a reset.

// quarantineParentName is the directory under RecoveryDir that holds
// one <reconcile-timestamp>/ subtree per quarantining reconcile pass.
const quarantineParentName = "quarantine"

// quarantine is a reconcile pass's non-lossy destination for every
// destructive filesystem action. Swept files are MOVED here (preserving
// their home-relative subpath) instead of being deleted, and dirty
// tracked paths are byte-snapshotted here before a reset reverts them.
//
// Why recovery never deletes: in-flight-debris identification is
// heuristic (see the honesty note on sweepUntrackedDebris — scoped
// per-turn commits don't sweep, so an untracked hand file can predate
// the crashed op's window by any number of turns), so the destructive
// paths are made non-lossy at trivial cost. Everything lands under
// recovery/quarantine/<reconcile-timestamp>/<original-relative-path>;
// recovery/ is gitignored, so quarantined bytes survive any later reset
// and are never committed. Each action is recorded in the report and as
// a recovery.quarantined event.
type quarantine struct {
	paths store.PersonantPaths
	dir   string // absolute recovery/quarantine/<stamp>; created lazily
	rep   *memops.RecoveryReport
}

func newQuarantine(paths store.PersonantPaths, rep *memops.RecoveryReport) *quarantine {
	stamp := clock.Timeline().UTC().Format("20060102T150405.000000000Z")
	return &quarantine{
		paths: paths,
		dir:   filepath.Join(paths.RecoveryDir, quarantineParentName, stamp),
		rep:   rep,
	}
}

// dst returns the quarantine destination for a home-relative slash path,
// creating parent directories.
func (q *quarantine) dst(rel string) (string, error) {
	d := filepath.Join(q.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
		return "", fmt.Errorf("mkdir quarantine dir: %w", err)
	}
	return d, nil
}

// move relocates the live file at home-relative rel into quarantine.
func (q *quarantine) move(rel string) error {
	dst, err := q.dst(rel)
	if err != nil {
		return err
	}
	src := filepath.Join(q.paths.Home, filepath.FromSlash(rel))
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("quarantine %s: %w", rel, err)
	}
	q.record(rel)
	return nil
}

// snapshot copies the current bytes of home-relative rel into quarantine,
// leaving the live file in place (the caller's reset reverts it). A
// missing source is deletion-dirt: the reset restores the HEAD copy, so
// there are no pre-reset bytes to save.
func (q *quarantine) snapshot(rel string) error {
	src := filepath.Join(q.paths.Home, filepath.FromSlash(rel))
	data, err := os.ReadFile(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", rel, err)
	}
	dst, err := q.dst(rel)
	if err != nil {
		return err
	}
	if err := store.WriteFileAtomic(dst, data); err != nil {
		return err
	}
	q.record(rel)
	return nil
}

func (q *quarantine) record(rel string) {
	relDir, err := filepath.Rel(q.paths.Home, q.dir)
	if err != nil {
		relDir = q.dir
	}
	q.rep.QuarantineDir = filepath.ToSlash(relDir)
	q.rep.Quarantined = append(q.rep.Quarantined, rel)
	logEvent(q.paths, "quarantined", "path="+rel+" dir="+q.rep.QuarantineDir)
}

// logsPreserveDirName is the staging directory (under RecoveryDir) that
// holds full copies of tracked log files across a reset. It exists only
// between preserveLogs and the restore's cleanup; its presence at open
// means a prior recovery pass crashed mid-reset.
const logsPreserveDirName = "logs-preserve"

func logsPreserveDir(paths store.PersonantPaths) string {
	return filepath.Join(paths.RecoveryDir, logsPreserveDirName)
}

// healLogTails caps any torn final line in the daily event logs with a
// single newline (eventlog.HealTail — additive, never truncates) and
// returns the names of the files it healed. Only top-level *.log files
// are line-oriented; logs/archive/ holds rotated tarballs and is not
// touched.
func healLogTails(paths store.PersonantPaths) ([]string, error) {
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read logs dir: %w", err)
	}
	var healed []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		p := filepath.Join(paths.LogsDir, e.Name())
		torn, err := tailIsTorn(p)
		if err != nil {
			return healed, err
		}
		if !torn {
			continue
		}
		if err := eventlog.HealTail(p); err != nil {
			return healed, err
		}
		healed = append(healed, e.Name())
	}
	return healed, nil
}

// tailIsTorn reports whether path is non-empty and its last byte is not
// a newline.
func tailIsTorn(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		return false, nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false, fmt.Errorf("read last byte %s: %w", path, err)
	}
	return last[0] != '\n', nil
}

// sweepTmpResidue quarantines atomic-write temp files (tmp-<base>
// siblings left by store.WriteFileAtomic), UNCONDITIONALLY — see
// Reconcile phase 1. Scope is the substrate's own residue pattern, not
// every tmp-* name: a file is swept only when its sibling target exists
// (the WriteFileAtomic convention — the strongest residue signal) or it
// sits directly in a canonical-namespace directory the substrate writes
// atomically (see atomicResidue). A hand-created tmp-* file anywhere
// else is not ours and survives untouched; one inside the canonical
// namespace is indistinguishable from residue and is quarantined, never
// deleted, so it stays recoverable byte-exact. Excluded subtrees: .git
// (not ours), tmp/ (agent drafting scratch, whose contents are
// user-shaped and merely happen to live under a directory named tmp),
// recovery/ (this package's own staging + quarantine; its residue is
// managed by restorePreservedLogs, and walking the quarantine would
// re-sweep already-quarantined files forever). Returns home-relative
// slash paths of swept files, sorted by walk order (lexical).
func sweepTmpResidue(paths store.PersonantPaths, q *quarantine) ([]string, error) {
	var swept []string
	err := filepath.WalkDir(paths.Home, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil // racing nothing; home may be sparse pre-Init
			}
			return walkErr
		}
		if d.IsDir() {
			if p == filepath.Join(paths.Home, ".git") || p == paths.GitDaily ||
				p == paths.TmpDir || p == paths.RecoveryDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), "tmp-") || !atomicResidue(paths, p) {
			return nil
		}
		rel, relErr := filepath.Rel(paths.Home, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if err := q.move(rel); err != nil {
			return err
		}
		swept = append(swept, rel)
		return nil
	})
	if err != nil {
		return swept, err
	}
	return swept, nil
}

// atomicResidue reports whether the tmp-* file at abs matches the
// WriteFileAtomic residue shape: its rename target (the sibling with
// the tmp- prefix stripped) exists, or its parent is a canonical-
// namespace directory the substrate writes atomically (home root,
// directives/, archive/, and the threads/ and projects/ subtrees).
func atomicResidue(paths store.PersonantPaths, abs string) bool {
	dir := filepath.Dir(abs)
	target := filepath.Join(dir, strings.TrimPrefix(filepath.Base(abs), "tmp-"))
	if _, err := os.Stat(target); err == nil {
		return true
	}
	switch dir {
	case paths.Home, paths.DirectivesDir, paths.ArchiveDir:
		return true
	}
	return underDir(dir, paths.ThreadsDir) || underDir(dir, paths.ProjectsDir)
}

// underDir reports whether p is root or lies beneath it.
func underDir(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

// sweepUntrackedDebris quarantines untracked, non-gitignored files
// after a reset.
//
// HONESTY NOTE (R3-addendum item 3): debris identification here is
// HEURISTIC, bounded by quarantine — no longer proof-clean. The old
// proof ("every prior successful commit's Add('.') leaves the worktree
// clean of untracked non-ignored files, so anything untracked now was
// written since the last commit") lost its premise when per-turn
// commits became scoped to the turn's recorded write set: a scoped
// commit does NOT sweep, so an untracked hand-created file can survive
// arbitrarily many turns and still be present at a crash — genuinely
// predating the crashed op's window. It was already only
// approximately true before (a hand-created file during a stale-marker
// idle window was indistinguishable from op debris). What makes the
// sweep safe is not the identification but the destination: files are
// MOVED into quarantine, never deleted (the R2 consolidation), so a
// misidentified hand file is recoverable byte-exact. The destructive
// path stays (the worktree converges to its recovery point) while
// remaining non-lossy at trivial cost. Exemptions: logs/ (append-only forensics —
// an entirely new day's log file is untracked and legitimate) and
// recovery/ (this package's own preserved artifacts + quarantine; also
// gitignored, listed here as belt-and-braces for homes whose .gitignore
// predates the entry). Empty parent directories left behind are pruned.
func sweepUntrackedDebris(ctx context.Context, paths store.PersonantPaths, q *quarantine) ([]string, error) {
	// Untracked-ness is a DAILY observable: turns commit to daily, so
	// in-flight turn debris is whatever daily HEAD does not know. Both
	// git-dirs are gitignored (INV-6), so neither can appear here.
	wt, err := autogit.Worktree(ctx, paths, autogit.Daily)
	if err != nil {
		return nil, err
	}
	var swept []string
	for _, rel := range wt.Untracked {
		if strings.HasPrefix(rel, "logs/") || strings.HasPrefix(rel, "recovery/") {
			continue
		}
		abs := filepath.Join(paths.Home, filepath.FromSlash(rel))
		if _, serr := os.Stat(abs); errors.Is(serr, os.ErrNotExist) {
			continue // already gone (racing re-entry) — nothing to preserve
		}
		if err := q.move(rel); err != nil {
			return swept, fmt.Errorf("quarantine debris: %w", err)
		}
		swept = append(swept, rel)
		pruneEmptyParents(paths, filepath.Dir(abs))
	}
	return swept, nil
}

// pruneEmptyParents removes now-empty directories from dir upward,
// stopping at the home root, any scaffold directory (those are part of
// the Init layout and must survive even when empty), or the first
// non-empty directory. Best-effort — a leftover empty directory is
// cosmetic, never load-bearing.
func pruneEmptyParents(paths store.PersonantPaths, dir string) {
	scaffold := map[string]struct{}{
		paths.Home: {}, paths.ThreadsDir: {}, paths.ProjectsDir: {},
		paths.DirectivesDir: {}, paths.LogsDir: {}, paths.LogsArchive: {},
		paths.ArchiveDir: {}, paths.TmpDir: {}, paths.RecoveryDir: {},
	}
	for strings.HasPrefix(dir, paths.Home) {
		if _, isScaffold := scaffold[dir]; isScaffold {
			return
		}
		if err := os.Remove(dir); err != nil {
			return // non-empty or otherwise busy — stop
		}
		dir = filepath.Dir(dir)
	}
}

// preserveLogs copies every tracked log file's CURRENT bytes into the
// preserve staging dir before a reset reverts them. Preserve-once: a
// file already staged is never overwritten, so a re-entered recovery
// (whose live logs may already be truncated by the first pass's reset)
// cannot clobber the fuller copy with a shorter one.
func preserveLogs(ctx context.Context, paths store.PersonantPaths) error {
	rels, err := trackedLogFiles(ctx, paths)
	if err != nil {
		return err
	}
	for _, rel := range rels {
		src := filepath.Join(paths.Home, filepath.FromSlash(rel))
		data, rerr := os.ReadFile(src)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				continue // deleted in worktree; reset restores the HEAD copy
			}
			return fmt.Errorf("read %s: %w", rel, rerr)
		}
		dst := filepath.Join(logsPreserveDir(paths), filepath.FromSlash(rel))
		if _, serr := os.Stat(dst); serr == nil {
			continue // preserve-once
		} else if !errors.Is(serr, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", dst, serr)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir preserve dir: %w", err)
		}
		if err := store.WriteFileAtomic(dst, data); err != nil {
			return err
		}
	}
	return nil
}

// trackedLogFiles enumerates the DAILY HEAD tree's files under logs/, as
// home-relative slash paths. These are exactly the files a reset will
// revert — resets target daily (INV-1) — so daily HEAD is the right
// tree (untracked log files are untouched by reset and exempt from
// the debris sweep, so they need no preservation).
func trackedLogFiles(ctx context.Context, paths store.PersonantPaths) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, err := autogit.Open(paths, autogit.Daily)
	if err != nil {
		return nil, fmt.Errorf("open daily repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("HEAD: %w", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("load HEAD commit: %w", err)
	}
	root, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("HEAD tree: %w", err)
	}
	logsTree, err := root.Tree("logs")
	if err != nil {
		return nil, nil // no tracked logs yet — nothing to preserve
	}
	var rels []string
	fi := logsTree.Files()
	for {
		f, ferr := fi.Next()
		if ferr == io.EOF {
			break
		}
		if ferr != nil {
			return nil, fmt.Errorf("walk logs tree: %w", ferr)
		}
		rels = append(rels, "logs/"+f.Name)
	}
	return rels, nil
}

// restorePreservedLogs merges every staged log copy back into the live
// file and removes the staging dir. Idempotent and safe against every
// crash interleaving:
//   - live file truncated by the reset → restored to the full copy;
//   - live file already restored (and possibly extended by recovery's
//     own event lines) → left alone;
//   - live file extended past the reset point (a re-entered pass logged
//     new lines before restoring) → the preserved tail is appended so
//     no forensic line from either pass is lost.
//
// A missing staging dir is the common no-op.
func restorePreservedLogs(paths store.PersonantPaths) error {
	dir := logsPreserveDir(paths)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat preserve dir: %w", err)
	}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), "tmp-") {
			return nil // tmp- residue of an interrupted preserve write
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		preserved, rerr := os.ReadFile(p)
		if rerr != nil {
			return fmt.Errorf("read preserved %s: %w", rel, rerr)
		}
		target := filepath.Join(paths.Home, rel)
		cur, terr := os.ReadFile(target)
		if terr != nil && !errors.Is(terr, os.ErrNotExist) {
			return fmt.Errorf("read live %s: %w", rel, terr)
		}
		merged, changed := mergeLogBytes(cur, preserved)
		if changed {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := store.WriteFileAtomic(target, merged); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	crashpoint.At(cpLogsRestored)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove preserve dir: %w", err)
	}
	return nil
}

// mergeLogBytes reconciles a live append-only log with its preserved
// pre-reset copy. Returns the bytes the live file should hold and
// whether that differs from cur. The relations, in order:
//
//	preserved ⊑ cur  (cur already contains everything)   → keep cur
//	cur ⊑ preserved  (reset truncated the tail)           → preserved
//	diverged past a common prefix                          → if cur
//	  already contains the preserved tail (a prior merge), keep cur;
//	  otherwise append the preserved tail to cur, so lines written by
//	  a re-entered recovery pass and lines from before the reset both
//	  survive (append-only files never lose bytes here).
//
// The divergence cut is line-aligned: the byte-wise common prefix is
// rounded DOWN to the last '\n' boundary before the tail is taken.
// Diverging lines routinely share a byte prefix — a re-entered pass's
// begin event and the pre-reset line it displaced start with near-
// identical RFC3339 timestamps — and cutting mid-line would append a
// head-chopped fragment, corrupting the log's line discipline.
func mergeLogBytes(cur, preserved []byte) ([]byte, bool) {
	if bytes.HasPrefix(cur, preserved) {
		return cur, false
	}
	if bytes.HasPrefix(preserved, cur) {
		return preserved, true
	}
	cp := commonPrefixLen(cur, preserved)
	if nl := bytes.LastIndexByte(preserved[:cp], '\n'); nl >= 0 {
		cp = nl + 1
	} else {
		cp = 0
	}
	tail := preserved[cp:]
	if len(tail) == 0 || bytes.Contains(cur, tail) {
		return cur, false
	}
	merged := make([]byte, 0, len(cur)+1+len(tail))
	merged = append(merged, cur...)
	if len(merged) > 0 && merged[len(merged)-1] != '\n' {
		merged = append(merged, '\n') // never splice into cur's final line
	}
	merged = append(merged, tail...)
	return merged, true
}

func commonPrefixLen(a, b []byte) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
