package fileadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"

	"personant/internal/autogit"
	"personant/internal/crashpoint"
	"personant/internal/memops"
	"personant/internal/recovery"
	"personant/internal/store"
)

// Crash-stability port surface (#94, SPEC §4.5.8): Reconcile /
// JournalTurn / CommitTurn. The in-flight-turn signal is entirely inside
// this file — the application layer never names it. There is no
// Begin/End bracket on the port: the first JournalTurn append of a turn
// IS the durable "turn in flight" signal (a non-empty turn journal whose
// first record carries the turn id — the R3-addendum marker-into-journal
// fold), and CommitTurn's journal truncate is the release. The marker
// FILE (op-in-progress.json) is batch ops only (archival/sleep/
// recovery); the per-turn path never touches it, so marker-set
// durability rides the journal fsync the pipeline already pays for.

// Crashpoints for the R4 W-TURN matrix, covering the write-ordering
// windows this adapter owns (the turn pipeline's own points land with
// R3). Registered at import time for the coverage gate.
var (
	cpJournalPreAppend = crashpoint.Register("fileadapter.JournalTurn.preAppend")
	cpCommitPostAdd    = crashpoint.Register("fileadapter.CommitTurn.postAddPreCommit")
	cpCommitPostCommit = crashpoint.Register("fileadapter.CommitTurn.postCommitPreTruncate")
)

// journalKindFor maps the port's content kind onto the substrate's
// journal kind — a marker-boundary contract check: an unknown kind is a
// caller bug and is refused before anything is written.
func journalKindFor(kind memops.TurnContentKind) (store.JournalKind, error) {
	switch kind {
	case memops.TurnContentPrompt:
		return store.JournalPrompt, nil
	case memops.TurnContentResponse:
		return store.JournalResponse, nil
	default:
		return "", fmt.Errorf("fileadapter: unknown turn content kind %q", kind)
	}
}

// checkTurnScope is the shared conflict check for the per-turn verbs
// (JournalTurn/CommitTurn/ReleaseTurn): a batch-op marker file, or a
// journal owned by a DIFFERENT turn (including an unrecoverable owner —
// torn/corrupt first record), is a protocol violation and is refused —
// turns never overlap another scope, and silently adopting one would let
// two operations share one crash-recovery scope. A journal owned by
// turnID (or empty) passes.
func (a *FileAdapter) checkTurnScope(turnID string) error {
	m, present, err := store.ReadMarker(a.paths)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("%w op=%s turn=%s", memops.ErrConflictingMarker, m.Op, m.Turn)
	}
	owner, inFlight, err := store.JournalOwner(a.paths)
	if err != nil {
		return err
	}
	if inFlight && owner != turnID {
		return fmt.Errorf("%w op=turn turn=%s", memops.ErrConflictingMarker, owner)
	}
	return nil
}

// checkNoInFlightScope is the batch-op guard (Checkpoint/ArchiveThreads/
// Consolidate): ANY in-flight scope — batch marker file or non-empty
// turn journal — refuses the operation.
func (a *FileAdapter) checkNoInFlightScope() error {
	m, present, err := store.ReadMarker(a.paths)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("%w op=%s turn=%s", memops.ErrConflictingMarker, m.Op, m.Turn)
	}
	owner, inFlight, err := store.JournalOwner(a.paths)
	if err != nil {
		return err
	}
	if inFlight {
		return fmt.Errorf("%w op=turn turn=%s", memops.ErrConflictingMarker, owner)
	}
	return nil
}

// Reconcile repairs the substrate after any unclean shutdown — the
// Init → Reconcile → LoadSession opening sequence's middle step. It
// delegates to the recovery orchestrator and then drops the adapter's
// parsed-frontmatter cache: recovery may have reverted or removed
// thread.md files out from under any entries populated before this
// call, and a stale parse surviving into ProposeRecall would violate
// the cache-coherence invariant (fmcache.go).
func (a *FileAdapter) Reconcile(ctx context.Context) (memops.RecoveryReport, error) {
	if err := ctx.Err(); err != nil {
		return memops.RecoveryReport{}, err
	}
	rep, err := recovery.Reconcile(ctx, a.paths)
	a.fmCache.Reset()
	if err != nil {
		return rep, fmt.Errorf("fileadapter: reconcile: %w", err)
	}
	return rep, nil
}

// JournalTurn durably appends one record of in-flight turn content
// (fsync per append). The FIRST append of a turn is also what opens the
// turn's crash-recovery scope: a non-empty journal IS the in-flight-turn
// signal (first record carries the turn id), and the append's fsync
// makes the signal durable — the scope opens before every canonical
// write because the §3.0 pipeline journals the prompt before touching
// canonical state. No separate marker write, no extra sync.
func (a *FileAdapter) JournalTurn(ctx context.Context, turnID string, kind memops.TurnContentKind, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if turnID == "" {
		return errors.New("fileadapter: JournalTurn: turnID is empty")
	}
	storeKind, err := journalKindFor(kind)
	if err != nil {
		return err
	}
	if err := a.checkTurnScope(turnID); err != nil {
		return fmt.Errorf("fileadapter: JournalTurn %s: %w", turnID, err)
	}
	crashpoint.At(cpJournalPreAppend)
	if err := store.AppendJournal(a.paths, turnID, storeKind, content); err != nil {
		return fmt.Errorf("fileadapter: JournalTurn: %w", err)
	}
	return nil
}

// CommitTurn lands the per-turn recovery point: stage the turn's KNOWN
// WRITE SET (recorded by every adapter write since the last commit —
// spine, touched thread files, the day's event log; see turnScope in
// fileadapter.go), commit with the turn trailer (recovery reads
// HEADTURN from it), then truncate the journal — the truncate IS the
// scope release (one operation; there is no separate marker to clear).
// A crash between commit and truncate resolves to recovery's cell 5
// (HEADTURN matches the journal's turn: redundant journal, zero loss).
//
// Scoped staging (R3-addendum item 3): autogit.AddPaths stages exactly
// the recorded files — O(write-set) per turn, no whole-tree walk. The
// consequences are deliberate policy:
//   - A hand-edit made outside the adapter is NOT absorbed per-turn; it
//     stays markerless-dirty (recovery cell 3 — never reset) until the
//     next FULL sweep: the session-close/backstop Checkpoint, or the
//     TODO(R3b) day-barrier sweep (design doc, shared-contracts block).
//   - The DAY-grain Add(".") responsibility moved to those sweeps; this
//     path never pays it.
//
// A turn that changed nothing canonical produces an empty commit, which
// the substrate rejects; that is a benign no-op here (there is nothing
// to make durable) and the journal is still released.
func (a *FileAdapter) CommitTurn(ctx context.Context, turnID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if turnID == "" {
		return errors.New("fileadapter: CommitTurn: turnID is empty")
	}
	if err := a.checkTurnScope(turnID); err != nil {
		return fmt.Errorf("fileadapter: CommitTurn %s: %w", turnID, err)
	}
	staged := a.snapshotTurnScope()
	if err := autogit.AddPaths(ctx, a.paths, staged); err != nil {
		return fmt.Errorf("fileadapter: CommitTurn %s: stage: %w", turnID, err)
	}
	// The Add/Commit gap: staged-but-uncommitted is a distinct on-disk
	// state (the index moved, HEAD did not) the R4 matrix must kill in.
	crashpoint.At(cpCommitPostAdd)
	// No verification flags: this is the per-turn durability snapshot,
	// same cost rationale as Checkpoint — integrity gating stays with
	// archival's commits and standalone verify.
	if err := autogit.Commit(ctx, a.paths, autogit.TurnCommitMessage(turnID, reason), 0, 0); err != nil {
		if !errors.Is(err, git.ErrEmptyCommit) {
			return fmt.Errorf("fileadapter: CommitTurn %s: commit: %w", turnID, err)
		}
	}
	crashpoint.At(cpCommitPostCommit)
	// The staged snapshot is committed (or was already clean); drop
	// exactly those entries. Paths recorded after the snapshot (none on
	// the single-writer turn path, but Log is reachable from cleanup
	// goroutines) stay pending for the next commit.
	a.dropFromTurnScope(staged)
	if err := store.TruncateJournal(a.paths); err != nil {
		return fmt.Errorf("fileadapter: CommitTurn %s: truncate journal: %w", turnID, err)
	}
	// The count-triggered gc backstop (R3-addendum item 4) is NOT run here:
	// its repack cost would be folded into the turn_commit_ms measurement.
	// The pipeline invokes MaybeGC separately AFTER capturing that timing —
	// the scope is already released by the truncate above, so the gc still
	// runs strictly between turns.
	return nil
}

// Count-triggered gc (R3-addendum item 4). The day-off sleep cycle is
// the CADENCE gc trigger; this is the PRESSURE trigger — per-turn
// commits accrue a few loose objects each, and a session that never
// crosses a day-off would otherwise grow .git unboundedly. Counting
// loose objects is a readdir sweep (cheap but not free), so it runs
// every gcCheckEveryCommits per-turn commits, and the gc fires only
// when the count has crossed gcLooseObjectThreshold.
const (
	gcLooseObjectThreshold = 5000
	gcCheckEveryCommits    = 64
)

// MaybeGC runs the loose-object pressure check and, past the threshold,
// one Consolidate pass (op=sleep scope, repack+prune). Strictly
// best-effort: every failure is logged by Consolidate itself or simply
// ignored — gc is an optimization and must never fail a turn. The
// pipeline calls it once per turn, after CommitTurn (see the port doc):
// the turn scope is already released, so this is a between-turns op, and
// keeping it out of CommitTurn keeps its repack cost out of turn_commit_ms.
func (a *FileAdapter) MaybeGC(ctx context.Context) {
	a.commitsSinceGCCheck++
	if a.commitsSinceGCCheck < gcCheckEveryCommits {
		return
	}
	a.commitsSinceGCCheck = 0
	n, err := autogit.LooseObjectCount(a.paths)
	if err != nil || n < gcLooseObjectThreshold {
		return
	}
	// Consolidate refuses under any in-flight scope; the turn's scope
	// was released above, so the normal case proceeds. A refusal (e.g.
	// stale batch marker) is not this path's problem — swallow it.
	_ = a.Consolidate(ctx, fmt.Sprintf("loose-objects=%d>=%d", n, gcLooseObjectThreshold))
}

// ReleaseTurn releases an aborted turn's scope without a commit — see
// the port doc for the contract. The WT observable it gates on is
// recovery.CanonicalDirty (tracked changes outside logs/), the exact
// predicate Reconcile uses: a logs-only-dirty tree is the steady
// operating state (the event log is written continuously between
// recovery points), so truncating the journal — the scope release —
// leaves a shape recovery classifies as clean (cell 1/2), and the log
// dirt absorbs into a later commit — cell-3-consistent, and no
// commit-per-error under a provider-outage retry loop. Canonical dirt
// beyond logs/ means the "nothing canonical happened" precondition does
// not hold (caller bug, or a mid-turn hand edit); the full CommitTurn
// path then makes the state durable rather than leaving torn-looking
// bytes with the scope released.
func (a *FileAdapter) ReleaseTurn(ctx context.Context, turnID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if turnID == "" {
		return errors.New("fileadapter: ReleaseTurn: turnID is empty")
	}
	if err := a.checkTurnScope(turnID); err != nil {
		return fmt.Errorf("fileadapter: ReleaseTurn %s: %w", turnID, err)
	}
	wt, err := autogit.Worktree(ctx, a.paths)
	if err != nil {
		return fmt.Errorf("fileadapter: ReleaseTurn %s: worktree state: %w", turnID, err)
	}
	if recovery.CanonicalDirty(wt.DirtyPaths) {
		return a.CommitTurn(ctx, turnID, "aborted")
	}
	if err := store.TruncateJournal(a.paths); err != nil {
		return fmt.Errorf("fileadapter: ReleaseTurn %s: truncate journal: %w", turnID, err)
	}
	return nil
}
