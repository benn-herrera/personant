package turn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/prompt"
)

// Topic-tag protocol violation surface (MAD B2 / T1-3). When an LLM emits
// fs.write tool calls without a same-turn topic tag, the workspace file is
// already written (OS-level fs.write is irreversible) but no engaged thread
// exists to bind the canonical §3.9 sidecar entry against. The runtime
// fails the turn loudly: substrate state does not advance, the unsynced
// paths are logged for human reconciliation, and the error names the
// paths so an upstream tutoring system can surface the violation.
//
// Future hardening: enforce topic-tag-first at stream parse time so the
// fs.write tool call can be rejected before the OS-level write. Out of
// scope for B2 — see MAD T1-3 follow-up.
const (
	logCatFS                          = "fs"
	logActUnsyncedNoTopicTag          = "unsynced-no-topic-tag"
	errMsgFileEditWithoutTopicTagHead = "turn aborted: fs.write without topic tag (spec §3.0/§3.3 protocol violation); substrate state not advanced; unsynced paths: "
)

// ErrFileEditWithoutTopicTag is returned by closeTurnAndUpdateEngagement
// when a turn buffers file edits but engages no thread. Callers and
// tests can match via errors.Is.
var ErrFileEditWithoutTopicTag = errors.New("fs.write without topic tag")

// ErrTurnAlreadyOwned is returned by claimTurnOwner when a second thread
// attempts to take ownership of a turn whose excerpt has already been
// written. A turn belongs to exactly one thread (spec §3.2); the
// excerpt-bearing write fires for exactly one thread per turn. This is a
// structural guard — the reworked engage loop writes the excerpt once;
// the guard ensures a future bug cannot double-write one turn's content
// to two threads.
var ErrTurnAlreadyOwned = errors.New("turn: excerpt already owned by another thread")

// claimTurnOwner records threadID as this turn's single owner. It
// succeeds once per turn; a second claim by a different thread returns
// ErrTurnAlreadyOwned. A repeat claim by the same thread is idempotent
// (the recovery-resynthesis path may re-enter for the same owner). The
// stamp is turn-scoped (State.turnOwner), reset at the top of every Run.
func claimTurnOwner(state *State, threadID string) error {
	if state.turnOwner == "" || state.turnOwner == threadID {
		state.turnOwner = threadID
		return nil
	}
	return fmt.Errorf("%w: owner=%s attempted=%s", ErrTurnAlreadyOwned, state.turnOwner, threadID)
}

// closeTurnAndUpdateEngagement fires after the model.response delta.
// Per §3.0.4: engagement updates fire once per affected thread with
// the turn's coalesced symbol set as input.
//
// For each thr_<n> in state.coalesce.threads:
//   - if it exists in spine: load thread file, append turn excerpt,
//     merge history_symbols, save thread file, update spine record.
//   - if it is the literal "*new-topic*": create a new thread file
//     and append a new spine record.
//
// File order is: thread file save first, spine update second. A failure
// after the thread file write but before the spine write would leave
// the on-disk file ahead of the spine; the next engagement reload would
// reconcile (it reads the existing file). The opposite order would
// strand a spine entry pointing at a non-existent file. v0.1 accepts
// the former trade-off and logs both errors with context.
//
// New-record creation requires the topic tag's anchors. v0.1 takes
// them from state.coalesce.symbols (the union accumulated across the
// turn). Anchor count must be 4–8 (§2.2 hard range); when out of
// range, a warning is logged and the first 4 (padded with "anchor-N"
// placeholders if there are fewer) are used. v0.1 deliberately does
// not fail the turn over malformed tag output — that judgment is
// recorded in the spec changelog.
//
// §5.5 mid-turn fetch (Phase 2.e-B) handles the common case where the
// model's topic tag references a thr_<n> not in Layer B at
// prompt-construction time: Run aborts the in-flight stream, loads the
// thread, and re-issues the request with augmented context. The
// close-time LRU update here still picks up any threads that the
// mid-turn fetch couldn't service (e.g. a missing thread file →
// thread.fetch-miss) or didn't trigger (re-prompt cap reached, or the
// model's second-stream tag introduces a new thr_<n> that we no longer
// re-prompt for). Those threads enter ActiveThreads at turn close so
// the next turn's prompt includes them.
func closeTurnAndUpdateEngagement(ctx context.Context, state *State, userInput, responseBody string) error {
	if len(state.coalesce.threads) == 0 {
		// No thread was engaged this turn. If §3.9 file edits are buffered,
		// the LLM has violated the spec §3.0/§3.3 topic-tag protocol: a
		// canonical workspace write must travel with a topic tag so the
		// edit binds to a thread's tracked-file sidecar. The workspace file
		// already exists on disk (OS-level fs.write is irreversible) but
		// the substrate has no thread to attach to. Silently dropping the
		// edit would mask the violation, so fail the turn loudly: do not
		// advance substrate state, log each unsynced path so a human can
		// reconcile, and return an error naming the paths.
		//
		// Future: enforce topic-tag-first at stream parse time to reject
		// the fs.write tool call before the OS-level write — see MAD T1-3
		// follow-up.
		if len(state.fileEdits) > 0 {
			paths := unsyncedEditPaths(state.fileEdits)
			for _, p := range paths {
				_ = state.Ops.Log(ctx, logCatFS, logActUnsyncedNoTopicTag,
					"path="+memops.SanitizeDetail(p)+" reason=no-engaged-thread")
			}
			return fmt.Errorf("%s%s: %w",
				errMsgFileEditWithoutTopicTagHead,
				strings.Join(paths, ", "),
				ErrFileEditWithoutTopicTag)
		}
		return nil
	}

	now := clock.Timeline().Format(time.RFC3339)
	turnSymbols := state.coalesce.coalescedList()
	turnAnchors := turnAnchorList(responseBody, state.coalesce.symbolList())

	// Determine this turn's single owner (spec §3.2). A turn belongs to
	// exactly one thread — the one that receives the excerpt and the
	// turn_count++. Every other referenced thread is engaged-but-not-
	// owner: it gets recency + history_symbols, no excerpt, no count.
	//
	// Owner-selection rule (§5.1.1, advisory tag → runtime decision):
	//   - ≥1 existing thread referenced: the lowest-id existing thread
	//     owns. The coalesce buffer is a map, so tag order is not
	//     preserved; lowest thr_N id is the deterministic tiebreak
	//     standing in for "first-listed".
	//   - pure *new-topic* (no existing referenced): the new thread owns
	//     (genesis turn — the only available owner).
	//   - mixed [thr_N, *new-topic*]: the existing thr_N owns; the new
	//     thread is created metadata-only.
	ids := state.coalesce.threadList()
	ownerExisting := lowestExistingThreadID(ids)

	// Engaged thread IDs in this turn — includes resolved IDs for the
	// *new-topic* sentinel. Used to update the Layer B/C LRU below. The
	// owner is placed at index 0 so the §3.9 file-edit application binds
	// to it (applyFileEdits keys off engaged[0]).
	engaged := make([]string, 0, len(ids))

	// Owner first.
	if ownerExisting != "" {
		if err := updateExistingThread(ctx, state, ownerExisting, true, userInput, responseBody, now, turnSymbols, turnAnchors); err != nil {
			return err
		}
		engaged = append(engaged, ownerExisting)
	}

	for _, threadID := range ids {
		if threadID == prompt.NewTopicLiteral {
			// The new thread owns only when no existing thread was
			// referenced (pure *new-topic*). In the mixed case the
			// existing thread already owns, so the new thread is created
			// metadata-only (no excerpt, TurnCount 0).
			newID, err := createNewThread(ctx, state, ownerExisting == "", userInput, responseBody, now, turnSymbols, turnAnchors)
			if err != nil {
				return err
			}
			engaged = append(engaged, newID)
			continue
		}
		if threadID == ownerExisting {
			continue // already handled as owner
		}
		if err := updateExistingThread(ctx, state, threadID, false, userInput, responseBody, now, turnSymbols, turnAnchors); err != nil {
			return err
		}
		engaged = append(engaged, threadID)
	}

	// §3.9 step-3 close: apply this turn's buffered file edits to the
	// primary engaged thread (engaged[0] — for a `work` turn that is the
	// turn's single engaged thread). A RecordFileCommit error (an
	// untracked path — e.g. a commit with no preceding write) is
	// non-fatal: log it and continue, do not abort the turn.
	if len(state.fileEdits) > 0 {
		applyFileEdits(ctx, state, engaged[0])
	}

	// §3.9 git-minimization: age out committed-file reverse-delta chains
	// on every engaged thread. This runs regardless of whether this turn
	// touched any file — a long-committed chain on a thread engaged only
	// for conversation must still age. A non-empty agedPaths result is
	// expected; an error is non-fatal (logged, consistent with the other
	// close-time substrate calls).
	for _, id := range engaged {
		if _, _, err := state.Ops.AgeFileChains(ctx, id, state.TurnNumber); err != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryDedup, "error",
				"thr="+id+" chain-age "+memops.SanitizeDetail(err.Error()))
		}
	}

	updateLayerLRU(state, engaged)

	engagedSet := make(map[string]struct{}, len(engaged))
	for _, id := range engaged {
		engagedSet[id] = struct{}{}
	}
	if err := surfaceRecallCandidates(ctx, state, userInput, engagedSet); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "error", memops.SanitizeDetail(err.Error()))
		// Non-fatal: opportunistic recall failure does not abort the turn.
	}
	return nil
}

// unsyncedEditPaths returns the de-duplicated, insertion-ordered list of
// file paths in edits. Used by the fail-loud topic-tag-protocol branch in
// closeTurnAndUpdateEngagement so each path is logged exactly once and the
// returned error names paths in the order they were buffered.
func unsyncedEditPaths(edits []fileEdit) []string {
	out := make([]string, 0, len(edits))
	seen := make(map[string]struct{}, len(edits))
	for _, fe := range edits {
		if _, ok := seen[fe.path]; ok {
			continue
		}
		seen[fe.path] = struct{}{}
		out = append(out, fe.path)
	}
	return out
}

// applyFileEdits flushes this turn's buffered §3.9 file-edit events into
// threadID's tracked-file store, in buffer (chain) order. A write folds
// content into the path's version chain via RecordFileWrite; a commit
// records the git-commit pointer via RecordFileCommit. A commit against
// an untracked path (no preceding write) is non-fatal — RecordFileCommit
// returns an error, which is logged as fs/commit-untracked and skipped so
// the turn still completes.
func applyFileEdits(ctx context.Context, state *State, threadID string) {
	for _, fe := range state.fileEdits {
		switch fe.kind {
		case fileEditWrite:
			if err := state.Ops.RecordFileWrite(ctx, threadID, fe.path, fe.content); err != nil {
				_ = state.Ops.Log(ctx, memops.LogCategoryFS, "write-error",
					"thr="+threadID+" path="+fe.path+" err="+memops.SanitizeDetail(err.Error()))
			}
		case fileEditCommit:
			if err := state.Ops.RecordFileCommit(ctx, threadID, fe.path, fe.hash, state.TurnNumber); err != nil {
				_ = state.Ops.Log(ctx, memops.LogCategoryFS, "commit-untracked",
					"thr="+threadID+" path="+fe.path+" err="+memops.SanitizeDetail(err.Error()))
			}
		}
	}
}

// lowestExistingThreadID returns the deterministic owner among the
// turn's referenced thread IDs: the existing thr_<n> with the lowest
// numeric n. The *new-topic* sentinel is ignored. Returns "" when no
// thr_<n> id is present (a pure *new-topic* turn). The coalesce buffer
// is a map, so the model's tag order is not preserved — lowest-id is the
// documented, deterministic stand-in for "first-listed" (spec §5.1.1).
func lowestExistingThreadID(ids []string) string {
	owner := ""
	ownerN := -1
	for _, id := range ids {
		if id == prompt.NewTopicLiteral {
			continue
		}
		n, ok := threadIDNum(id)
		if !ok {
			continue
		}
		if owner == "" || n < ownerN {
			owner, ownerN = id, n
		}
	}
	return owner
}

// threadIDNum parses the numeric suffix of a "thr_<n>" id. ok is false
// when the id does not match that shape.
func threadIDNum(id string) (int, bool) {
	const prefix = "thr_"
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(id[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

// updateExistingThread applies this turn's engagement to an existing
// thread. When owner is true the thread receives the turn excerpt and a
// turn_count++ (it owns this turn's content per spec §3.2); when false
// it is engaged-but-not-owner — recency (last_engaged / last_engaged_turn)
// and history_symbols merge and state→active resurrection apply, but NO
// excerpt is written and turn_count is left unchanged.
func updateExistingThread(ctx context.Context, state *State, threadID string, owner bool, userInput, responseBody, now string, turnSymbols []coalescedSymbol, turnAnchors []string) error {
	rec, found, err := state.Ops.FindThread(ctx, threadID)
	if err != nil {
		return err
	}
	if !found {
		// The model named a thread that does not exist. v0.1 logs and
		// continues; future phases may surface this as a recall miss or a
		// hallucination signal.
		return state.Ops.Log(ctx, memops.LogCategoryThread, "engaged-miss",
			"thr="+threadID+" reason=not-in-spine")
	}
	if rec.Project != "" && rec.Project != state.ActiveProject.ID {
		// Cross-project engagement is reserved for Phase 5; v0.1 warns and
		// declines to mutate a record that belongs to another project.
		return state.Ops.Log(ctx, memops.LogCategoryThread, "engaged-cross-project",
			"thr="+threadID+" project="+rec.Project+" active="+state.ActiveProject.ID)
	}

	// Load only the thread's frontmatter — the new format appends one
	// turn-excerpt file per engagement, so the prior body is never
	// loaded or rewritten. The adapter owns the missing-thread-dir
	// recovery: EngageThread recreates the directory when the spine
	// record exists but the on-disk thread is absent, so we don't
	// special-case ErrThreadFileNotFound here.
	fm, err := state.Ops.LoadThreadMeta(ctx, threadID)
	if err != nil && !errors.Is(err, memops.ErrThreadFileNotFound) {
		return fmt.Errorf("load thread %s: %w", threadID, err)
	}
	if errors.Is(err, memops.ErrThreadFileNotFound) {
		fm = frontmatterFromSpine(rec)
	}

	// Bookkeeping update on the in-memory record. turn_count increments
	// ONLY for the owner — an engaged-but-not-owner thread had no turn
	// added to it (spec §3.2). The merge turn index (used as
	// first_seen_turn for newly introduced history symbols, and as the
	// owner's excerpt-header turn number) is the post-increment count for
	// the owner and the unchanged count for a non-owner.
	mergeTurn := rec.TurnCount
	if owner {
		mergeTurn = rec.TurnCount + 1
	}
	fm.LastEngaged = now
	fm.LastEngagedTurn = state.TurnNumber
	fm.TurnCount = mergeTurn
	// Re-engagement resurrects the thread per the §2.2.1 state-transition
	// table (wip/paused/resolved/decided/abandoned → active). Engagement
	// promotes to active unconditionally when not already active; an
	// already-active thread keeps its state_changed timestamp untouched.
	if rec.State != memops.ThreadActive {
		rec.State = memops.ThreadActive
		rec.StateChanged = now
	}
	// Mirror the canonical spine fields so the frontmatter stays in
	// sync. The body of work reads the frontmatter; the spine is the
	// outer index.
	fm.ID = rec.ID
	fm.Project = rec.Project
	fm.Anchors = append([]string(nil), rec.Anchors...)
	fm.Summary = rec.Summary
	fm.Description = rec.Description
	fm.State = rec.State
	if fm.Created == "" {
		fm.Created = rec.Created
	}
	// StateChanged is mirrored from the canonical spine unconditionally:
	// an engagement that resurrects the thread to active bumps the spine
	// timestamp, and a stale frontmatter value would desync the file.
	fm.StateChanged = rec.StateChanged
	fm.RecallFires = rec.RecallFires

	fm.HistorySymbols = mergeHistorySymbols(fm.HistorySymbols, turnSymbols, mergeTurn)

	rec.LastEngaged = now
	rec.LastEngagedTurn = state.TurnNumber
	rec.TurnCount = mergeTurn

	// The owner gets the turn excerpt + the single-owner claim; an
	// engaged-but-not-owner thread gets a meta-only update (empty
	// TurnExcerpt per the §2.3 ThreadWrite contract).
	var excerpt string
	if owner {
		if err := claimTurnOwner(state, threadID); err != nil {
			return err
		}
		excerpt = renderTurnExcerpt(mergeTurn, now, turnAnchors, userInput, responseBody)
	}
	if err := state.Ops.EngageThread(ctx, memops.ThreadWrite{
		Spine:       rec,
		Meta:        fm,
		TurnExcerpt: excerpt,
	}); err != nil {
		return fmt.Errorf("engage thread %s: %w", threadID, err)
	}
	act := "engaged"
	if !owner {
		act = "engaged-non-owner"
	}
	return state.Ops.Log(ctx, memops.LogCategoryThread, act,
		threadID+" turn_count="+strconv.Itoa(rec.TurnCount))
}

// createNewThread allocates a new thread for the *new-topic* sentinel.
// When owner is true (pure *new-topic* turn) the new thread owns this
// turn's content: it is created with the excerpt and TurnCount 1, and it
// claims the single-owner stamp. When owner is false (mixed
// [thr_N, *new-topic*] turn — an existing thread already owns) the new
// thread is created metadata-only per spec §3.2: empty excerpt,
// TurnCount 0, Description + anchors + history_symbols set, owning no
// turn. Description is the triggering utterance either way (spec §2.3).
func createNewThread(ctx context.Context, state *State, owner bool, userInput, responseBody, now string, turnSymbols []coalescedSymbol, turnAnchors []string) (string, error) {
	// NextThreadID returns the next available thr_<n> id, scanning the
	// full spine so a new id never collides with a thread in a sibling
	// project.
	newID, err := state.Ops.NextThreadID(ctx)
	if err != nil {
		return "", err
	}

	anchors := state.coalesce.symbolList()
	if len(anchors) < memops.MinAnchorsPerThread || len(anchors) > memops.MaxAnchorsPerThread {
		_ = state.Ops.Log(ctx, memops.LogCategoryThread, "anchor-cardinality",
			"new-thread anchors="+strconv.Itoa(len(anchors))+" using-first-"+strconv.Itoa(memops.MinAnchorsPerThread)+"-with-padding")
		anchors = padOrTruncateAnchors(anchors, memops.MinAnchorsPerThread)
	}

	summary := summarizeForNewThread(responseBody)
	description := descriptionFromNewThread(userInput)

	// The owner records one turn; a metadata-only new thread owns no turn
	// (TurnCount 0). history_symbols first_seen_turn uses the same count.
	turnCount := 0
	if owner {
		turnCount = 1
	}

	rec := memops.SpineRecord{
		ID:              newID,
		Project:         state.ActiveProject.ID,
		Anchors:         anchors,
		Summary:         summary,
		Description:     description,
		State:           memops.ThreadActive,
		Created:         now,
		LastEngaged:     now,
		StateChanged:    now,
		TurnCount:       turnCount,
		LastEngagedTurn: state.TurnNumber,
	}

	frontmatter := ThreadMeta{
		ID:              newID,
		Project:         rec.Project,
		Anchors:         append([]string(nil), anchors...),
		Summary:         summary,
		Description:     description,
		State:           memops.ThreadActive,
		Created:         now,
		LastEngaged:     now,
		StateChanged:    now,
		TurnCount:       turnCount,
		RecallFires:     0,
		LastEngagedTurn: state.TurnNumber,
		HistorySymbols:  mergeHistorySymbols(nil, turnSymbols, turnCount),
	}

	var excerpt string
	if owner {
		if err := claimTurnOwner(state, newID); err != nil {
			return "", err
		}
		excerpt = renderTurnExcerpt(1, now, turnAnchors, userInput, responseBody)
	}

	if err := state.Ops.CreateThread(ctx, memops.ThreadWrite{
		Spine:       rec,
		Meta:        frontmatter,
		TurnExcerpt: excerpt,
	}); err != nil {
		return "", fmt.Errorf("create thread %s: %w", newID, err)
	}
	act := "created"
	if !owner {
		act = "created-meta-only"
	}
	if err := state.Ops.Log(ctx, memops.LogCategoryThread, act,
		newID+" anchors="+strconv.Itoa(len(anchors))+" project="+state.ActiveProject.ID); err != nil {
		return "", err
	}
	return newID, nil
}
