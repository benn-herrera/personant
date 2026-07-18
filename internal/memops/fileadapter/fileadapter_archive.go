package fileadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"

	"personant/internal/autogit"
	"personant/internal/clock"
	"personant/internal/crashpoint"
	"personant/internal/eventlog"
	pnlog "personant/internal/log"
	"personant/internal/memops"
	"personant/internal/store"
)

// Crashpoints for the R4 W-ARCH matrix: the op=archival marker scope
// boundaries (#94 R3). Registered at import time for the coverage gate.
var (
	cpArchivalPreMarker  = crashpoint.Register("archival.preMarker")
	cpArchivalPostMarker = crashpoint.Register("archival.postMarker")
)

// §3.8 recoverable git-based archival (task #99, design §3.2 / §7.2). This
// file holds the batch ArchiveThreads and RecoverThread — the first
// callers of internal/autogit from the adapter. Single-thread ArchiveThread
// is a thin wrapper in fileadapter.go.
//
// Archival event vocabulary on the substrate event log:
const (
	archiveLogCategory = "archive"
	// archiveActionArchived replaces the old archive.simulated-delete line.
	// It carries bytes= for demand-sizing continuity (the body byte size of
	// the archived thread's live window). The old archive.warning "NO
	// recovery path" line is GONE — archival is now genuinely recoverable.
	archiveActionArchived  = "archived"
	archiveActionRecovered = "recovered"
	// archiveActionRecoveredRecord marks recovery of a DRIFT entry — a thread
	// archived with an empty TreeHash (no on-disk directory body). Only its
	// spine record is re-added; nothing is restored from git. A distinct
	// action keeps "recovered a record" from looking like "recovered + tree-
	// verified content" in the forensic log (F6).
	archiveActionRecoveredRecord = "recovered-record"
)

// consolidateAction is the event-log action for one sleep-cycle pass
// (MemoryOps.Consolidate). reason= carries the schedule context; gc=ok|err
// records whether the substrate gc reclaimed or fell back.
const consolidateAction = "sleep-cycle"

// threadRelDir returns the repo-relative, slash-separated directory path a
// thread occupies under the home tree (e.g. "threads/thr_7"). This is the
// form autogit's tree ops (CheckoutTree, TreeHashAt) and the archive index
// (OriginalPath) speak.
func threadRelDir(threadID string) string {
	return path.Join("threads", threadID)
}

// Checkpoint creates a §3.11 substrate recovery point: it stages the whole
// worktree (Add(".")) and commits it with a message folding in `reason`. The
// caller drives the cadence (turn close on structural change, session close);
// this method is the mechanism, not the policy.
//
// A clean tree is a benign no-op. go-git rejects an empty commit with
// git.ErrEmptyCommit; we swallow it and return nil — there is no recovery
// point to make when nothing changed (archival already committed this turn's
// structural bytes, or session close after the last structural commit flushed
// everything). Any other failure propagates.
//
// Verification flags are conservative: none on pre, none on post. The cadence
// fires on every material turn, so a post-flag CheckSpineIntegrity (a full
// verify.Verify pass) would add that cost to every commit — and integrity
// gating is already owned by archival (CheckDerivedFresh|CheckSpineIntegrity
// on its deletion/recovery commits) and the standalone `personant verify`. A
// cadence commit is a durability snapshot, not an integrity gate; keeping it
// flag-free keeps per-commit cost flat.
//
// Scope guard (same family as ArchiveThreads/Consolidate): a checkpoint
// under ANY in-flight scope — batch marker file or non-empty turn
// journal — is refused. A checkpoint runs Add(".") + commit WITHOUT a
// turn trailer, so committing a scope-retained turn's torn prefix would
// make HEADTURN≠T with a clean tree — reconciling as cell 6 ("nothing
// landed") and making the torn state permanent. Refusing is the loud,
// honest choice: the caller (chat session-close) reports the unclean
// state and leaves it for the next open's Reconcile.
func (a *FileAdapter) Checkpoint(ctx context.Context, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.checkNoInFlightScope(); err != nil {
		return fmt.Errorf("fileadapter: checkpoint: %w", err)
	}
	if err := autogit.Add(ctx, a.paths, "."); err != nil {
		return fmt.Errorf("fileadapter: checkpoint: stage: %w", err)
	}
	if err := autogit.Commit(ctx, a.paths, fmt.Sprintf("checkpoint: %s", reason), 0, 0); err != nil {
		if errors.Is(err, git.ErrEmptyCommit) {
			a.resetTurnScope() // the full Add(".") staged everything anyway
			return nil         // clean tree — nothing to checkpoint
		}
		return fmt.Errorf("fileadapter: checkpoint: commit: %w", err)
	}
	// The full-tree sweep absorbed every pending scoped-write entry —
	// this is the backstop/day-barrier full Add(".") the scoped per-turn
	// CommitTurn deliberately does not pay (R3-addendum item 3).
	a.resetTurnScope()
	return nil
}

// Consolidate runs one offline sleep-cycle pass (MemoryOps.Consolidate,
// task #108). Today the pass is substrate gc — autogit.GC packs the
// session's accumulated loose git objects and prunes garbage, bounding the
// home tree's on-disk footprint across long-lived sessions. It is
// extensible to spine compaction / archival advance later.
//
// gc failure is NON-FATAL: a sleep cycle is a pure optimization, so a gc
// error is logged (and recorded as gc=err on the event line) but does not
// propagate — the method returns nil so a failed reclamation never aborts
// the session it runs inside. An event-log write failure, by contrast,
// does propagate: the forensic record is the one durable signal the cycle
// ran, and losing it silently would hide the pass entirely.
func (a *FileAdapter) Consolidate(ctx context.Context, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Scope-boundary contract check + op=sleep scope (#94 R3): the gc's
	// .git surgery (repack/prune) runs under an op=sleep marker so a crash
	// mid-gc reconciles as cell 10 (clear marker; git self-heals; caches
	// self-heal). A conflicting in-flight scope (batch marker OR non-empty
	// turn journal) is refused — the sleep
	// cycle never runs inside another operation's scope. The scope clears
	// even on gc failure (non-fatal by contract, and gc failure leaves no
	// torn canonical state — it touches only .git internals). The other
	// sleep-cycle passes (RebuildTrees, SweepCache) touch only gitignored
	// recall caches, which self-heal by content-hash keying (SOLUTION
	// principle 9), so they run outside the marker by design; any future
	// sleep sub-op that mutates CANONICAL state must adopt the full
	// marker/commit protocol (cell 10's stated invariant).
	if err := a.checkNoInFlightScope(); err != nil {
		return fmt.Errorf("fileadapter: consolidate: %w", err)
	}
	if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpSleep}); err != nil {
		return fmt.Errorf("fileadapter: consolidate: marker: %w", err)
	}
	gcStatus := "ok"
	if err := autogit.GC(ctx, a.paths); err != nil {
		// Non-fatal: log and record, but do not abort the caller. The %s
		// arg keeps any error text out of format-string position.
		pnlog.Warn("fileadapter: consolidate: gc failed (non-fatal): %v", err)
		gcStatus = "err"
	}
	if err := store.ClearMarker(a.paths); err != nil {
		return fmt.Errorf("fileadapter: consolidate: clear marker: %w", err)
	}
	if err := eventlog.Log(a.paths, memops.LogCategoryConsolidate, consolidateAction,
		fmt.Sprintf("reason=%s gc=%s", reason, gcStatus)); err != nil {
		return fmt.Errorf("fileadapter: consolidate: log: %w", err)
	}
	return nil
}

// ArchiveThreads archives a batch of retired threads (design §3.2).
//
// # Index-needs-commit-hash ordering (the crux) and its crash-safety
//
// An archive index entry's CommitHash names the DELETION commit, and
// recovery resolves the thread's bytes from that commit's PARENT (Q3). But
// a commit's hash is only known AFTER it is written — circular with putting
// it in a file that the same commit contains. We resolve this with a
// bounded, per-drain commit shape (the design blesses two; this uses three,
// still O(1) per drain, never O(K)):
//
//  1. CAPTURE commit — stage the whole worktree (Add(".")) and commit. In a
//     running session thread directories are written to the worktree but
//     never committed per-turn (only `personant init` commits), so this
//     pins every to-be-archived directory into a committed tree. This
//     commit becomes the PARENT of the deletion commit and is where the
//     archived bytes live. Tree hashes are captured here (worktree == this
//     commit's state). If the worktree is already clean, go-git rejects the
//     empty commit; we tolerate that (the dirs are already at HEAD).
//
//  2. DELETION commit — os.RemoveAll each dir, one RemoveSpineRecords, regen
//     derived, append index entries (CommitHash still empty), then
//     Add(".") + CommitWithHash, capturing the deletion commit hash H. Its
//     parent is the capture commit, which holds the bytes.
//
//  3. STAMP commit — rewrite the index entries with CommitHash = H and
//     commit that single-file change.
//
// Crash-safety (invariant 1: losing the index must not lose data):
//   - Crash after (1): worktree unchanged except a no-op capture; nothing
//     archived; fully consistent.
//   - Crash between (1) and (2)'s commit: worktree ahead of HEAD (dirs
//     removed, spine/derived/index written but uncommitted). Recoverable —
//     the capture commit still holds every thread's bytes, and startup
//     reconcile (#94) realigns. No bytes are lost.
//   - Crash after (2) but before (3): the deletion commit holds the bytes in
//     its parent; the index entries exist but with empty CommitHash. This is
//     the one window where an index entry cannot self-recover by lookup, but
//     the data is NOT lost (reachable by git log), and reconcile can re-stamp
//     by matching OriginalPath against the deletion commit. RecoverThread
//     guards against an empty CommitHash explicitly.
//   - Crash after (3): fully committed, fully recoverable.
//
// The batch is atomic-per-drain in the sense invariant 3 requires: a thread
// is either fully archived (off worktree + spine, in index + reachable
// commit) or untouched; no thread ends off-spine yet absent from both the
// index and a reachable commit.
func (a *FileAdapter) ArchiveThreads(ctx context.Context, threadIDs []string) (memops.ArchiveResult, error) {
	if err := ctx.Err(); err != nil {
		return memops.ArchiveResult{}, err
	}

	// Scope-boundary contract check (#94 R3): archival runs strictly
	// BETWEEN turns in its own op=archival scope — an in-flight scope of
	// ANY kind here (batch marker or non-empty turn journal) is a protocol
	// violation (a turn's window still open, a prior archival's scope
	// never released) and is refused before any mutation; silently
	// adopting it would let two operations share one crash-recovery scope.
	if err := a.checkNoInFlightScope(); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: %w", err)
	}

	// Sort + dedup the requested ids so the batch is order-independent and
	// the index stays line-sorted by thr_id (invariant 4).
	ids := dedupSorted(threadIDs)

	// (1) Validate + capture. Build the set of ids that actually have a
	// spine record to archive; the rest are recorded as skips. Capture each
	// archivable thread's spine snapshot (summary/anchors/project) and body
	// size before any mutation.
	type capture struct {
		rec      memops.SpineRecord
		bodySize int
		hasDir   bool // false ⇒ drift: spine record with no thread directory
	}
	caps := make(map[string]capture, len(ids))
	var archivable []string
	result := memops.ArchiveResult{Outcomes: make([]memops.ArchiveOutcome, 0, len(ids))}
	for _, id := range ids {
		rec, found, err := store.FindSpineRecord(a.paths, id)
		if err != nil {
			return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: find spine %s: %w", id, err)
		}
		if !found {
			result.Outcomes = append(result.Outcomes, memops.ArchiveOutcome{
				ThrID: id, Skipped: true, Reason: "no spine record",
			})
			continue
		}
		bodySize := 0
		if body, err := store.ReadThreadBody(a.paths, id, 0); err == nil {
			bodySize = len(body)
		} else if !errors.Is(err, memops.ErrThreadFileNotFound) {
			return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: read body %s: %w", id, err)
		}
		// A spine record whose directory is absent (drift) is still
		// archivable — the spine record must leave the spine — but there are
		// no bytes to capture or recover. It gets an index entry with an
		// empty TreeHash (an un-recoverable breadcrumb), matching the old
		// stub's bytes=0 tolerance.
		hasDir := false
		if fi, serr := os.Stat(store.ThreadDir(a.paths, id)); serr == nil && fi.IsDir() {
			hasDir = true
		} else if serr != nil && !errors.Is(serr, os.ErrNotExist) {
			return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: stat dir %s: %w", id, serr)
		}
		caps[id] = capture{rec: rec, bodySize: bodySize, hasDir: hasDir}
		archivable = append(archivable, id)
	}

	if len(archivable) == 0 {
		// Nothing to archive — no commits, no index write. The skips are
		// already recorded in result.Outcomes.
		return result, nil
	}

	// (1, cont.) Freshen derived state BEFORE the capture commit. The
	// capture pre-flag CheckSpineIntegrity (verify.Verify) treats a stale
	// symbols.jsonl / missing digest.json as drift and would reject the
	// commit; regenerating first makes the pre-mutation state genuinely
	// consistent. In production this is a no-op (derived is kept fresh per
	// turn); it heals a substrate whose derived state was never built (e.g.
	// bulk-seeded threads). The post-removal state is regenerated again
	// below (step 5).
	if err := a.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: pre-regenerate derived: %w", err)
	}

	// CAPTURE commit: pin the to-be-archived directories into a committed
	// tree so the deletion commit's parent holds their bytes.
	if err := autogit.Add(ctx, a.paths, "."); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: stage capture: %w", err)
	}
	// The capture commit's pre-flag CheckSpineIntegrity validates the OLD
	// (pre-mutation) state — don't archive on a broken spine (design §3.2
	// step 6). It runs BEFORE any destructive mutation (the RemoveAll /
	// RemoveSpineRecords below), so a broken spine aborts cleanly with
	// nothing removed. The deletion commit then carries no pre-flag (we are
	// mid-transaction) and validates the END state in its post-flag.
	if err := autogit.Commit(ctx, a.paths, fmt.Sprintf("archive: capture %d thread(s)", len(archivable)),
		autogit.CheckSpineIntegrity, 0); err != nil {
		// An empty capture commit means the worktree was already clean and
		// the directories are already committed at HEAD — fine, proceed. (A
		// pre-flag failure returns the check error, NOT ErrEmptyCommit, so
		// this only swallows the genuinely-clean case.)
		if !errors.Is(err, git.ErrEmptyCommit) {
			return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: capture commit: %w", err)
		}
	}

	// Capture tree hashes from the COMMITTED tree at HEAD — which, after the
	// capture commit above (or, if that commit was empty, the pre-existing
	// HEAD), is exactly the deletion commit's parent-to-be: the tree recovery
	// restores from (F3). Reading the committed tree (TreeHashAt) rather than
	// the worktree (WorktreeTreeHash) makes the token invariant under the
	// transform recovery applies: it sees only tracked files (gitignore-safe)
	// with their committed modes. A worktree hash would include a gitignored
	// file that the commit never staged, or an executable bit the restore
	// would not reproduce, and FALSE-fail a perfectly recoverable thread.
	// A drift thread with no directory has no bytes — empty TreeHash (its dir
	// is absent from the committed tree too, so TreeHashAt would error; we
	// skip it, preserving the existing empty-TreeHash breadcrumb semantics).
	captureHash, err := autogit.HeadHash(ctx, a.paths)
	if err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: resolve capture commit: %w", err)
	}
	treeHashes := make(map[string]string, len(archivable))
	for _, id := range archivable {
		if !caps[id].hasDir {
			continue
		}
		th, err := autogit.TreeHashAt(ctx, a.paths, captureHash, threadRelDir(id))
		if err != nil {
			return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: tree hash %s: %w", id, err)
		}
		treeHashes[id] = th
	}

	// Open the op=archival marker scope (#94, SOLUTION principle 6) —
	// written before the first destructive mutation (the RemoveAll loop
	// below) and cleared only after the stamp commit lands. A crash inside
	// the scope reconciles as cell 8 (reset to the last stable
	// archival-sequence point; redrain re-triggers on the next pressure
	// check). Everything above this line is non-destructive: validation,
	// derived regen (idempotent), and the capture commit — a markerless
	// crash there leaves a self-consistent home. An in-process error AFTER
	// this point deliberately leaves the marker set: the worktree may be
	// torn, and failing loudly into the recovery path beats continuing the
	// session on it.
	crashpoint.At(cpArchivalPreMarker)
	if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpArchival}); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: marker: %w", err)
	}
	crashpoint.At(cpArchivalPostMarker)

	// (2) Stage removals: remove each thread directory + invalidate its
	// frontmatter cache entry. os.RemoveAll on a missing dir is a no-op.
	for _, id := range archivable {
		if err := os.RemoveAll(store.ThreadDir(a.paths, id)); err != nil {
			return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: remove dir %s: %w", id, err)
		}
		a.fmCache.Invalidate(id)
	}

	// (3) One spine rewrite for the whole batch.
	if err := store.RemoveSpineRecords(a.paths, archivable); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: remove spine records: %w", err)
	}

	// (4) Append sorted index entries (CommitHash filled in after the
	// deletion commit, step 6). ArchivedAt is clock.Timeline() (AC6).
	archivedAt := clock.Timeline().Format(time.RFC3339)
	entries := make([]memops.ArchiveEntry, 0, len(archivable))
	for _, id := range archivable {
		c := caps[id]
		entries = append(entries, memops.ArchiveEntry{
			ThrID:            id,
			TreeHash:         treeHashes[id],
			ParentCommitHash: captureHash,
			ArchivedAt:       archivedAt,
			OriginalPath:     threadRelDir(id),
			SpineSummary:     c.rec.Summary,
			Anchors:          c.rec.Anchors,
			Project:          c.rec.Project,
		})
	}
	if err := store.AppendArchiveEntries(a.paths, entries); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: append index: %w", err)
	}

	// (5) Regenerate derived state ONCE so the deletion commit passes
	// CheckDerivedFresh — each removed thread dangles symbols.jsonl refs.
	if err := a.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: regenerate derived: %w", err)
	}

	// (6) DELETION commit: stage everything (removals + spine + index +
	// derived) and commit once. No pre-flag — the OLD-state spine integrity
	// was validated by the capture commit's pre-flag before any mutation;
	// here we are mid-transaction. The post-flag CheckDerivedFresh |
	// CheckSpineIntegrity validates the END state (regen fresh, spine still
	// consistent after the batch removal).
	if err := autogit.Add(ctx, a.paths, "."); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: stage deletion: %w", err)
	}
	delHash, err := autogit.CommitWithHash(ctx, a.paths,
		fmt.Sprintf("archive: %d thread(s)", len(archivable)),
		0, autogit.CheckDerivedFresh|autogit.CheckSpineIntegrity)
	if err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: deletion commit: %w", err)
	}
	result.CommitHash = delHash

	// (6, cont.) STAMP the deletion commit hash into each index entry, then
	// commit the index-only change. The entries already exist (step 4); we
	// re-append them with CommitHash set — AppendArchiveEntries replaces by
	// thr_id and keeps the file sorted.
	for i := range entries {
		entries[i].CommitHash = delHash
	}
	if err := store.AppendArchiveEntries(a.paths, entries); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: stamp index: %w", err)
	}
	if err := autogit.Add(ctx, a.paths, "."); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: stage stamp: %w", err)
	}
	if err := autogit.Commit(ctx, a.paths,
		fmt.Sprintf("archive: stamp %d index entr%s", len(archivable), plural(len(archivable))),
		0, autogit.CheckSpineIntegrity); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: stamp commit: %w", err)
	}

	// Close the op=archival marker scope: the batch's final commit landed,
	// so the mutation is fully covered by recovery points. The event-log
	// emission below is logs/-only (never canonical) and stays outside the
	// scope by design. The batch's Add(".") sweeps also absorbed any
	// pending scoped-write entries.
	a.resetTurnScope()
	if err := store.ClearMarker(a.paths); err != nil {
		return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: clear marker: %w", err)
	}

	// (7) Emit one archive.archived line per id (forensic + harness fold
	// key). bytes= kept for demand-sizing continuity. project= attributes
	// pressure per project even though the drain is substrate-global.
	for _, id := range archivable {
		c := caps[id]
		if err := eventlog.Log(a.paths, archiveLogCategory, archiveActionArchived,
			fmt.Sprintf("thr=%s project=%s bytes=%d", id, c.rec.Project, c.bodySize)); err != nil {
			return memops.ArchiveResult{}, fmt.Errorf("fileadapter: archive batch: log %s: %w", id, err)
		}
		result.Outcomes = append(result.Outcomes, memops.ArchiveOutcome{ThrID: id, Archived: true})
	}

	return result, nil
}

// RecoverThread restores an archived thread from its deletion commit and
// re-adds it to the spine (design §7.2).
func (a *FileAdapter) RecoverThread(ctx context.Context, thrID string) (memops.SpineRecord, error) {
	if err := ctx.Err(); err != nil {
		return memops.SpineRecord{}, err
	}

	// (1) Look up the archive entry.
	entry, found, err := store.FindArchiveEntry(a.paths, thrID)
	if err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: %w", thrID, err)
	}
	if !found {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: %w", thrID, memops.ErrArchiveEntryNotFound)
	}
	if entry.CommitHash == "" {
		// The stamp commit never landed (crash window §ArchiveThreads). The
		// bytes are still reachable by git log, but this entry cannot resolve
		// its parent without reconcile (#94). Surface, don't guess.
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: archive entry has no commit hash (un-stamped)", thrID)
	}

	// (2) Empty-TreeHash drift recovery is a DISTINCT outcome (F6). A thread
	// archived with no on-disk directory (spine record with no body) stored an
	// empty TreeHash as a breadcrumb. There are no bytes to restore and no
	// tree to verify — running CheckoutTree (the parent commit never contained
	// the directory) would error, and an empty-vs-empty VerifyTreeHash would
	// FALSE-pass as if bytes were verified. Surface it explicitly: the thread
	// had no directory body, so we re-add only its spine record from the index
	// snapshot, restore nothing, and return the recovered record. This is
	// recovery of a record, not of content — distinct from a verified
	// content recovery below.
	if entry.TreeHash == "" {
		return a.recoverDriftRecord(ctx, thrID, entry)
	}

	// (3) The bytes live in the deletion commit's PARENT — the capture commit
	// (Q3). Prefer the explicitly-stored ParentCommitHash (F6): it pins the
	// exact capture commit, so recovery does not assume the deletion commit's
	// first parent IS the capture commit (false on a future merge commit —
	// v2.0 submind-via-clone integrates via git merge). Fall back to walking
	// the first parent only for an older entry written before the field
	// existed (greenfield — no migration, but don't crash on empty).
	parent := entry.ParentCommitHash
	if parent == "" {
		parent, err = autogit.ParentCommitHash(ctx, a.paths, entry.CommitHash)
		if err != nil {
			return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: resolve parent: %w", thrID, err)
		}
	}
	if err := autogit.CheckoutTree(ctx, a.paths, parent, entry.OriginalPath, 0, 0); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: restore subtree: %w", thrID, err)
	}

	// (4) Verify the restored directory against the stored tree hash. On
	// mismatch, delete the partial restore and abort (invariant 2: never put
	// a partial thread on the spine).
	if err := autogit.VerifyTreeHash(a.paths, entry.OriginalPath, entry.TreeHash); err != nil {
		_ = os.RemoveAll(store.ThreadDir(a.paths, thrID))
		a.fmCache.Invalidate(thrID)
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: %w", thrID, err)
	}

	// (5) Build a fresh spine record from the RECOVERED frontmatter
	// (canonical — anchors/summary may have evolved past the index snapshot).
	// Reuse the stable id; state=wip; last_engaged=now.
	fm, err := store.LoadThreadFrontmatter(a.paths, thrID)
	if err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: load recovered frontmatter: %w", thrID, err)
	}
	now := clock.Timeline().Format(time.RFC3339)
	// BD-9: stamp BOTH engagement-recency fields the §3.5 decay scan reads so a
	// just-recovered thread cannot be immediately re-offered for retirement, and
	// so the rebuilt spine record does not silently diverge from the restored
	// frontmatter (a spine↔frontmatter sync violation checkThreads would not
	// catch — it compares only id/project).
	//
	//   - LastEngaged=now is the wall-clock recency signal (turn.decayEligible's
	//     idle=sysRef-LastEngaged measure); recovery IS activity now, so it
	//     cannot read as idle-past-decayTime.
	//   - LastEngagedTurn is the turn-denominated recency the scan reads. This
	//     adapter is session-agnostic (no current TurnNumber reaches it), so the
	//     honest value is the frontmatter's own last_engaged_turn — the same
	//     value written back below, keeping spine and frontmatter in agreement.
	//     A stored turn from the pre-archival session is > a fresh session's low
	//     TurnNumber, which decayEligible treats as "prior session, skip the turn
	//     signal" — exactly the intended no-immediate-retire behavior. Leaving it
	//     0 (the old bug) reads as "very old this session" and trips decay.
	//   - AnchorsProjectedAtTurn: the frontmatter carries no dedicated field, so
	//     the honest watermark is fm.TurnCount — the recovered Anchors reflect
	//     the projection as of the thread's last owned turn (the createNewThread
	//     / closure convention: project-at-turn-count). Leaving it 0 would falsely
	//     read as "anchors never re-projected".
	rec := memops.SpineRecord{
		ID:                     thrID,
		Project:                fm.Project,
		Anchors:                fm.Anchors,
		Summary:                fm.Summary,
		Description:            fm.Description,
		State:                  memops.ThreadWIP,
		Created:                fm.Created,
		LastEngaged:            now,
		StateChanged:           now,
		TurnCount:              fm.TurnCount,
		RecallFires:            fm.RecallFires,
		LastEngagedTurn:        fm.LastEngagedTurn,
		AnchorsProjectedAtTurn: fm.TurnCount,
	}
	if err := store.AppendSpineRecord(a.paths, rec); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: append spine: %w", thrID, err)
	}

	// (6) Reflect the recovered state into the thread frontmatter so the spine
	// and frontmatter agree (state/last_engaged), and keep the cache coherent.
	fm.State = memops.ThreadWIP
	fm.LastEngaged = now
	fm.StateChanged = now
	if err := store.SaveThreadFrontmatter(a.paths, thrID, fm); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: save frontmatter: %w", thrID, err)
	}
	a.fmCache.Put(fm)

	// (7) Stamp RecoveredAt on the index entry (KEPT as a breadcrumb,
	// invariant 4 — never removed), regen derived so the recovered thread
	// re-enters symbols.jsonl, then commit the recovery.
	entry.RecoveredAt = now
	if err := store.AppendArchiveEntries(a.paths, []memops.ArchiveEntry{entry}); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: stamp index: %w", thrID, err)
	}
	if err := a.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: regenerate derived: %w", thrID, err)
	}
	if err := autogit.Add(ctx, a.paths, "."); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: stage: %w", thrID, err)
	}
	if err := autogit.Commit(ctx, a.paths, "archive: recover "+thrID,
		0, autogit.CheckDerivedFresh|autogit.CheckSpineIntegrity); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: commit: %w", thrID, err)
	}
	a.resetTurnScope() // the recovery commit's Add(".") absorbed pending entries

	if err := eventlog.Log(a.paths, archiveLogCategory, archiveActionRecovered,
		fmt.Sprintf("thr=%s project=%s", thrID, rec.Project)); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover thread %s: log: %w", thrID, err)
	}

	return rec, nil
}

// recoverDriftRecord is the distinct-outcome path for recovering an archive
// entry with an empty TreeHash (F6): a drift thread that was archived with no
// on-disk directory. There are no bytes to restore and no tree to verify, so
// recovery rebuilds ONLY the spine record from the index snapshot
// (SpineSummary / Anchors / Project) — the canonical fields the entry
// preserved — and never runs the tree-hash compare (an empty-vs-empty match
// would FALSE-pass as a verified content recovery). It stamps RecoveredAt,
// commits, and logs archive.recovered-record so the forensic trail
// distinguishes a record recovery from a content recovery.
func (a *FileAdapter) recoverDriftRecord(ctx context.Context, thrID string, entry memops.ArchiveEntry) (memops.SpineRecord, error) {
	now := clock.Timeline().Format(time.RFC3339)
	rec := memops.SpineRecord{
		ID:           thrID,
		Project:      entry.Project,
		Anchors:      entry.Anchors,
		Summary:      entry.SpineSummary,
		State:        memops.ThreadWIP,
		Created:      now,
		LastEngaged:  now,
		StateChanged: now,
	}
	if err := store.AppendSpineRecord(a.paths, rec); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover drift record %s: append spine: %w", thrID, err)
	}

	entry.RecoveredAt = now
	if err := store.AppendArchiveEntries(a.paths, []memops.ArchiveEntry{entry}); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover drift record %s: stamp index: %w", thrID, err)
	}
	if err := a.RegenerateDerivedState(ctx, memops.IndexBuildOptions{Quiet: true}); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover drift record %s: regenerate derived: %w", thrID, err)
	}
	if err := autogit.Add(ctx, a.paths, "."); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover drift record %s: stage: %w", thrID, err)
	}
	if err := autogit.Commit(ctx, a.paths, "archive: recover record "+thrID,
		0, autogit.CheckDerivedFresh|autogit.CheckSpineIntegrity); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover drift record %s: commit: %w", thrID, err)
	}
	a.resetTurnScope() // the recovery commit's Add(".") absorbed pending entries
	if err := eventlog.Log(a.paths, archiveLogCategory, archiveActionRecoveredRecord,
		fmt.Sprintf("thr=%s project=%s", thrID, rec.Project)); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("fileadapter: recover drift record %s: log: %w", thrID, err)
	}
	return rec, nil
}

// ListArchivedThreads returns the archive index sorted by thr_id — a thin
// read-only wrapper over store.LoadArchiveIndex (the index is already sorted
// on every write). An empty/missing index yields a nil slice, not an error.
func (a *FileAdapter) ListArchivedThreads(ctx context.Context) ([]memops.ArchiveEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := store.LoadArchiveIndex(a.paths)
	if err != nil {
		return nil, fmt.Errorf("fileadapter: list archived threads: %w", err)
	}
	return entries, nil
}

// dedupSorted returns ids sorted ascending with duplicates removed and any
// empty id dropped.
func dedupSorted(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
