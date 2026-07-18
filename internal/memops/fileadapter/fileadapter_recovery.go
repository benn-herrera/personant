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
// JournalTurn / CommitTurn. The marker lifecycle is entirely inside
// this file — the application layer never names the marker (no
// Begin/End bracket on the port; the first JournalTurn of a turn sets
// it as a side effect, CommitTurn clears it, Reconcile repairs it).

// Crashpoints for the R4 W-TURN matrix, covering the write-ordering
// windows this adapter owns (the turn pipeline's own points land with
// R3). Registered at import time for the coverage gate.
var (
	cpJournalPreMarker  = crashpoint.Register("fileadapter.JournalTurn.preMarker")
	cpJournalPostMarker = crashpoint.Register("fileadapter.JournalTurn.postMarkerPreAppend")
	cpCommitPostCommit  = crashpoint.Register("fileadapter.CommitTurn.postCommitPreMarkerClear")
	cpCommitPostClear   = crashpoint.Register("fileadapter.CommitTurn.postClearPreTruncate")
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
// (fsync per append). The FIRST call of a turn writes the op=turn
// marker before the append — the marker precedes every canonical write
// because the §3.0 pipeline journals the prompt before touching
// canonical state, and this call precedes even that journal append.
//
// Contract checks at the boundary: an in-flight marker belonging to a
// DIFFERENT operation or turn is a protocol violation (turns never
// overlap another marker scope) and is refused — silently adopting it
// would let two operations share one crash-recovery scope.
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
	m, present, err := store.ReadMarker(a.paths)
	if err != nil {
		return fmt.Errorf("fileadapter: JournalTurn: %w", err)
	}
	if present && (m.Op != store.OpTurn || m.Turn != turnID) {
		return fmt.Errorf("fileadapter: JournalTurn %s: conflicting in-flight marker op=%s turn=%s", turnID, m.Op, m.Turn)
	}
	crashpoint.At(cpJournalPreMarker)
	if !present {
		if err := store.WriteMarker(a.paths, store.Marker{Op: store.OpTurn, Turn: turnID}); err != nil {
			return fmt.Errorf("fileadapter: JournalTurn: %w", err)
		}
	}
	crashpoint.At(cpJournalPostMarker)
	if err := store.AppendJournal(a.paths, turnID, storeKind, content); err != nil {
		return fmt.Errorf("fileadapter: JournalTurn: %w", err)
	}
	return nil
}

// CommitTurn lands the per-turn recovery point: stage the whole
// worktree, commit with the turn trailer (recovery reads HEADTURN from
// it), clear the marker, truncate the journal — in exactly that order,
// so a crash between any two steps resolves to a defined recovery cell
// (5, or the markerless journal-residue cleanup) with zero loss.
//
// A turn that changed nothing canonical produces an empty commit, which
// the substrate rejects; that is a benign no-op here (there is nothing
// to make durable) and the marker/journal are still released.
func (a *FileAdapter) CommitTurn(ctx context.Context, turnID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if turnID == "" {
		return errors.New("fileadapter: CommitTurn: turnID is empty")
	}
	m, present, err := store.ReadMarker(a.paths)
	if err != nil {
		return fmt.Errorf("fileadapter: CommitTurn: %w", err)
	}
	if present && (m.Op != store.OpTurn || m.Turn != turnID) {
		return fmt.Errorf("fileadapter: CommitTurn %s: conflicting in-flight marker op=%s turn=%s", turnID, m.Op, m.Turn)
	}
	if err := autogit.Add(ctx, a.paths, "."); err != nil {
		return fmt.Errorf("fileadapter: CommitTurn %s: stage: %w", turnID, err)
	}
	// No verification flags: this is the per-turn durability snapshot,
	// same cost rationale as Checkpoint — integrity gating stays with
	// archival's commits and standalone verify.
	if err := autogit.Commit(ctx, a.paths, autogit.TurnCommitMessage(turnID, reason), 0, 0); err != nil {
		if !errors.Is(err, git.ErrEmptyCommit) {
			return fmt.Errorf("fileadapter: CommitTurn %s: commit: %w", turnID, err)
		}
	}
	crashpoint.At(cpCommitPostCommit)
	if err := store.ClearMarker(a.paths); err != nil {
		return fmt.Errorf("fileadapter: CommitTurn %s: clear marker: %w", turnID, err)
	}
	crashpoint.At(cpCommitPostClear)
	if err := store.TruncateJournal(a.paths); err != nil {
		return fmt.Errorf("fileadapter: CommitTurn %s: truncate journal: %w", turnID, err)
	}
	return nil
}
