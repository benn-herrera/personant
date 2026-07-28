package turn

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/crashpoint"
	"personant/internal/memops"
	"personant/internal/prompt"
	"personant/internal/store"
)

// Topic-tag protocol recovery surface (D6, burn-down 2026-07b A1). When an
// LLM response omits the topic tag on a turn whose deltas require owner
// binding (buffered §3.9 file edits), the workspace file is already written
// (OS-level fs.write is irreversible) and a canonical sidecar entry must
// bind to SOME thread. The former fail-loud abort
// (ErrFileEditWithoutTopicTag, MAD B2 / T1-3) is retracted: the 1d
// live-inference run showed a real model intermittently omits the tag
// (turn 91 abort after ~90 coherent turns), so refusing the turn punishes
// the user for a model hiccup. Recovery instead (spec §3.3):
//
//  1. RunWithInfo issues a single mid-turn tag re-prompt (§5.5 machinery,
//     cause=missing-tag) before this close-time code ever sees the turn.
//  2. If the response is STILL tag-less, the turn binds to the
//     deterministic owner-default below (defaultOwnerForTagless), with a
//     `thread.tag-defaulted` forensic line. The integrity concern the old
//     abort answered — an edit silently absorbed by the wrong thread — is
//     answered by determinism + forensic visibility, not refusal.
//
// logActTagDefaulted is that forensic line's act. Emitted once per
// defaulted turn, after the owner is known: thr=<bound owner>,
// cause=missing-tag|empty-response (the latter when the final response
// carried zero visible content — the D6 empty-response extension),
// reprompted=yes|no.
const logActTagDefaulted = "tag-defaulted"

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
// New-record creation takes the topic tag's anchors from
// state.coalesce.symbols (the union accumulated across the turn). The
// anchor list is advisory: 0 anchors is legal (a vague-start thread),
// and the count is never gated — the §2.2 projection owns the
// AnchorProjectionMax ceiling deterministically.
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
	// tagDefaulted records that this turn's owner was chosen by the D6
	// owner-default (no tag survived the re-prompt); the forensic line is
	// emitted below once engaged[0] — the actually-bound owner — is known.
	tagDefaulted := false
	if len(state.coalesce.threads) == 0 {
		if len(state.fileEdits) == 0 {
			// Tag-less conversational turn: nothing requires owner binding,
			// so the turn proceeds tag-less — no engagement update, no
			// excerpt, no owner; the response still reaches the user and
			// session History. extractSymbols already logged
			// topic.tag-missing for calibration. This asymmetry with the
			// binding-required branch below is deliberate (D6): a
			// conversational turn's content has no canonical write that
			// needs an owner, so spending a model round-trip (re-prompt) or
			// inventing an engagement (owner-default) to recover advisory
			// metadata would be machinery without a customer.
			return nil
		}
		// D6 owner-default (recovery stage 2 — see the file-top comment):
		// §3.9 file edits are buffered but no tag survived the mid-turn
		// re-prompt. Bind the turn to the thread that would own it absent
		// any tag; when no candidate resolves, fall through to new-thread
		// creation exactly like the D2 fallback-(ii) path (the turn's
		// excerpt and edits are never dropped). The chosen id is injected
		// into the coalesce buffer so the normal resolution/ownership/
		// binding machinery below runs unmodified — the default is an
		// input to the standard path, not a parallel one.
		//
		// Persistent-EMPTY responses (D6 extension, cause=empty-response)
		// reach this same branch: an empty response has no tag, so a
		// binding turn whose empty re-prompt also came back empty defaults
		// here. Its excerpt's agent section is then EMPTY — deliberately:
		// the user prompt and the fs edits are real and need an owner, and
		// an empty agent section is the honest record of what the model
		// produced; synthesizing placeholder content would forge the
		// thread history.
		defaultID, err := defaultOwnerForTagless(ctx, state)
		if err != nil {
			return err
		}
		if defaultID == "" {
			defaultID = prompt.NewTopicLiteral
		}
		state.coalesce.addThread(defaultID)
		tagDefaulted = true
	}

	now := clock.Timeline().Format(time.RFC3339)
	turnSymbols := state.coalesce.coalescedList()
	turnAnchors := turnAnchorList(responseBody, state.coalesce.symbolList())

	// Resolve every referenced thr_<n> against the spine FIRST. Per spec
	// §3.2 ownership assignment is a runtime decision and the topic tag is
	// advisory — this resolution pass is the runtime exercising that
	// authority (BD-7 / decision D2): an id that does not resolve — not in
	// the spine (engaged-miss) or belonging to another project
	// (cross-project decline) — is a phantom. It is logged for forensics
	// and dropped here, so it can never enter `engaged`, ActiveThreads, or
	// the persisted working set, own the turn, or receive the §3.9
	// file-edit binding.
	ids := state.coalesce.threadList()
	hasNewTopic := false
	validIDs := make([]string, 0, len(ids))
	resolved := make(map[string]memops.SpineRecord, len(ids))
	for _, id := range ids {
		if id == prompt.NewTopicLiteral {
			hasNewTopic = true
			continue
		}
		rec, ok, err := resolveEngagedThread(ctx, state, id)
		if err != nil {
			return err
		}
		if !ok {
			continue // phantom: logged by resolveEngagedThread, dropped
		}
		resolved[id] = rec
		validIDs = append(validIDs, id)
	}

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
	//
	// D2 fallback (i): when the tag's would-be owner is a phantom, the
	// most-recently-engaged VALID thread from this turn owns instead.
	ownerExisting := lowestExistingThreadID(ids)
	if _, ok := resolved[ownerExisting]; !ok {
		ownerExisting = mostRecentEngagedID(validIDs, resolved)
	}

	// Engaged thread IDs in this turn — includes resolved IDs for the
	// *new-topic* sentinel. Used to update the Layer B/C LRU below. The
	// owner is placed at index 0 so the §3.9 file-edit application binds
	// to it (applyFileEdits keys off engaged[0]).
	engaged := make([]string, 0, len(ids))

	// Owner first. The W-TURN multi-write kill point fires after EACH
	// canonical thread write in this function (owner, new-thread, each
	// non-owner engagement) — R4 targets the kth boundary via
	// crashpoint.ArmOnHit, exercising every torn prefix of the turn's
	// canonical write set.
	if ownerExisting != "" {
		if err := updateExistingThread(ctx, state, resolved[ownerExisting], true, userInput, responseBody, now, turnSymbols, turnAnchors); err != nil {
			return err
		}
		engaged = append(engaged, ownerExisting)
		crashpoint.At(cpBetweenCanonicalRenames)
	}

	if hasNewTopic {
		// The new thread owns only when no VALID existing thread was
		// referenced (pure *new-topic*, or every referenced thr_<n> was a
		// phantom). In the mixed case the existing thread already owns, so
		// the new thread is created metadata-only (no excerpt, TurnCount 0).
		newID, err := createNewThread(ctx, state, ownerExisting == "", userInput, responseBody, now, turnSymbols, turnAnchors)
		if err != nil {
			return err
		}
		engaged = append(engaged, newID)
		crashpoint.At(cpBetweenCanonicalRenames)
	}

	for _, threadID := range validIDs {
		if threadID == ownerExisting {
			continue // already handled as owner
		}
		if err := updateExistingThread(ctx, state, resolved[threadID], false, userInput, responseBody, now, turnSymbols, turnAnchors); err != nil {
			return err
		}
		engaged = append(engaged, threadID)
		crashpoint.At(cpBetweenCanonicalRenames)
	}

	// D2 fallback (ii): every referenced thread was a phantom and no
	// *new-topic* sentinel was tagged. The turn's excerpt is NEVER dropped
	// — create a new thread that owns this turn, exactly as a pure
	// *new-topic* tag would (excerpt, turn_count=1, description from the
	// prompt per spec §2.3).
	if len(engaged) == 0 {
		newID, err := createNewThread(ctx, state, true, userInput, responseBody, now, turnSymbols, turnAnchors)
		if err != nil {
			return err
		}
		engaged = append(engaged, newID)
	}

	// D6 forensic line: the owner-default bound this turn. Emitted here —
	// after the engagement blocks — so thr= names the ACTUAL bound owner
	// (engaged[0]), including a freshly created thr_<n> from the
	// no-candidate fallback. cause= names the failure that led here:
	// empty-response when the final response had zero visible content
	// (that cause subsumes the vacuous tag-lessness of an empty body),
	// missing-tag otherwise. reprompted= records whether THAT cause's
	// mid-turn re-prompt fired first.
	//
	// The reprompted=no arm is UNREACHABLE for both causes EXCEPT one
	// pathological shape (below); otherwise kept as defense-in-depth.
	// Reaching owner-default requires the FINAL response to lack a valid tag
	// (a valid tag ⇒ non-empty coalesce ⇒ this branch never runs), on a turn
	// with buffered §3.9 edits. All fs.* deltas arrive as pre-prompt events,
	// so the edit buffer is complete before any stream; the per-cause flags
	// are fresh each turn; the close-time parser is strictly more permissive
	// than the preamble scan (a tag the scan finds, Parse finds). So the
	// drain attempt of a tag-less binding turn is normally reached only with
	// the relevant flag already true — the cause's re-prompt fired first.
	//
	// The exception (cause=empty-response, reprompted=no): an all-whitespace
	// response LONGER than the bounded preamble scan (PreambleScanLineCap /
	// PreambleScanByteCap) resolves the scan as tag-less rather than empty,
	// so preambleIsEmpty never fires (it requires pre.ended) and
	// emptyReprompted stays false — the missing-tag re-prompt fires instead.
	// If that re-prompt also returns over-bound whitespace, close-time
	// strings.TrimSpace(responseBody)=="" relabels the cause empty-response,
	// yielding reprompted=no. Forensic-only: the owner-default binding and
	// the empty-agent-section record are identical either way; only the
	// logged reprompted flag differs. The arm also becomes live if a future
	// change lets the edit buffer grow after the drain decision (mid-stream
	// fs deltas), or adds a policy that skips the re-prompt (cost cap,
	// offline mode). Outside these, reprompted=no in a log is itself a
	// finding.
	if tagDefaulted {
		cause, causeReprompted := "missing-tag", state.tagReprompted
		if strings.TrimSpace(responseBody) == "" {
			cause, causeReprompted = "empty-response", state.emptyReprompted
		}
		reprompted := "no"
		if causeReprompted {
			reprompted = "yes"
		}
		_ = state.Ops.Log(ctx, memops.LogCategoryThread, logActTagDefaulted,
			"thr="+engaged[0]+" cause="+cause+" reprompted="+reprompted)
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
	// The owner (engaged[0]) is the thread the user is currently in — the
	// intra-thread (#109) engaged thread whose scrolled-out early content
	// the fine-tier pass may surface. Empty when this turn engaged nothing.
	var engagedOwner string
	if len(engaged) > 0 {
		engagedOwner = engaged[0]
	}
	// §3.4 recall is the one close-time step that can make a network call
	// (the embedding round-trip), so it gets its own label rather than
	// hiding inside PhaseClosing.
	emitPhase(state, PhaseRecall)
	if err := surfaceRecallCandidates(ctx, state, userInput, engagedSet, engagedOwner); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "error", memops.SanitizeDetail(err.Error()))
		// Non-fatal: opportunistic recall failure does not abort the turn.
	}
	return nil
}

// defaultOwnerForTagless picks the D6 owner-default: the thread that would
// own this turn had no tag ever been required — the most recently engaged
// valid thread in the model's working window. Candidates are Layer B
// (state.ActiveThreads): those are the threads whose bodies the model was
// looking at when it emitted the tag-less response, so the conversation's
// current thread is among them whenever one exists.
//
// Ranking is state.ActiveThreads ORDER (index 0 = most recently engaged;
// the first resolving candidate wins). The list order is the persisted
// honest recency signal (turn.go State.ActiveThreads): it survives
// SaveWorkingSet/LoadSession across a relaunch. The earlier ranking by
// spine LastEngagedTurn (shared with mostRecentEngagedID) was
// ANTI-recency after a restart: LastEngagedTurn stores the SESSION-scoped
// State.TurnNumber, which resets on LoadSession, so a stale prior-session
// thread (turn 91) outranked the thread actively worked this session
// (turn 3) — exactly inverted. The D2 fallback-(i) ranking
// (mostRecentEngagedID) deliberately does NOT share this fix; see its doc.
//
// Candidates that fail to resolve — not in the spine, or another project's
// record — are skipped SILENTLY: this scan judges the runtime's own LRU,
// not a model emission, so the engaged-miss / engaged-cross-project
// forensic lines (which attribute a phantom to the tag) would mislabel the
// event. A FindThread failure is a substrate error and propagates — D6
// forbids aborting for a missing TAG, not for a broken substrate.
//
// Returns "" when no candidate resolves (empty Layer B, or every entry
// stale); the caller then routes to new-thread creation, the same terminal
// the D2 fallback-(ii) path uses.
func defaultOwnerForTagless(ctx context.Context, state *State) (string, error) {
	for _, id := range state.ActiveThreads {
		rec, found, err := state.Ops.FindThread(ctx, id)
		if err != nil {
			return "", fmt.Errorf("resolve owner-default candidate %s: %w", id, err)
		}
		if !found {
			continue
		}
		if rec.Project != "" && rec.Project != state.ActiveProject.ID {
			continue
		}
		return id, nil
	}
	return "", nil
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

// logActAnchorOverflow is the runtime-self-assertion log act emitted when a
// projection returns more than AnchorProjectionMax anchors. The bound is the
// runtime's own invariant (the projection owns it deterministically), so a
// violation is a substrate bug, not a model contract breach — it is LOGGED
// for forensics, never aborts the turn (Build-Plan §Increment-2 invariant).
const logActAnchorOverflow = "anchor-projection-overflow"

// projectAnchorsForTurn runs ProjectAnchors and asserts the projected-count
// invariant. It is the single owner-turn / creation projection entry point
// so the merge → project → evict sequence and the self-assertion live in one
// place. The returned slice / changed flag are passed straight through to
// the caller, which writes them onto the spine + frontmatter and applies the
// cap (capHistorySymbols) AFTER projection so eviction consumes the latched
// EverCentral flags (Risk R5).
func projectAnchorsForTurn(ctx context.Context, state *State, merged []memops.HistorySymbol, turn int) (anchors []string, updated []memops.HistorySymbol, changed bool) {
	anchors, updated, changed = ProjectAnchors(merged, memops.AnchorProjectionMax, turn)
	if len(anchors) > memops.AnchorProjectionMax {
		// Runtime boundary self-assertion: the projection must never exceed
		// its own ceiling. Log and proceed (do not abort the turn).
		_ = state.Ops.Log(ctx, memops.LogCategoryThread, logActAnchorOverflow,
			"count="+strconv.Itoa(len(anchors))+" max="+strconv.Itoa(memops.AnchorProjectionMax))
	}
	return anchors, updated, changed
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

// resolveEngagedThread resolves a tagged thr_<n> id against the spine.
// ok=true carries the spine record for an existing thread in the active
// project. Per spec §3.2 the topic tag is advisory — ownership and
// engagement are runtime decisions — so an id that fails to resolve is a
// phantom the runtime refuses to engage (BD-7 / D2):
//   - not in the spine → thread.engaged-miss (the model named a thread
//     that does not exist; kept forensically visible — future phases may
//     surface it as a recall miss or a hallucination signal);
//   - another project's record → thread.engaged-cross-project
//     (cross-project engagement is reserved for Phase 5; v0.1 warns and
//     declines to mutate a record that belongs to another project).
//
// A non-nil error is a substrate failure (FindThread / Log), never the
// phantom signal itself.
func resolveEngagedThread(ctx context.Context, state *State, threadID string) (memops.SpineRecord, bool, error) {
	rec, found, err := state.Ops.FindThread(ctx, threadID)
	if err != nil {
		return memops.SpineRecord{}, false, err
	}
	if !found {
		return memops.SpineRecord{}, false, state.Ops.Log(ctx, memops.LogCategoryThread, "engaged-miss",
			"thr="+threadID+" reason=not-in-spine")
	}
	// Cross-project decline (shared policy, see crossProjectDecline): another
	// project's record is a phantom this seam refuses to mutate. A log error
	// (declined==true, err!=nil) propagates as a substrate failure; a clean
	// decline returns ok=false with nil error.
	if declined, err := crossProjectDecline(ctx, state, threadID, rec.Project, "engaged-cross-project"); declined || err != nil {
		return memops.SpineRecord{}, false, err
	}
	return rec, true, nil
}

// mostRecentEngagedID returns the D2 fallback-(i) owner: among the
// turn's VALID engaged threads, the one most recently engaged before
// this turn (highest LastEngagedTurn; ties broken by lowest thr_<n> id
// for determinism). Returns "" when validIDs is empty — the caller then
// falls through to fallback (ii), new-thread creation.
//
// This ranking deliberately KEEPS LastEngagedTurn despite the F1
// session-scoped-counter finding that moved defaultOwnerForTagless to
// ActiveThreads order (LastEngagedTurn resets meaning across a relaunch,
// so cross-session comparisons invert recency). Two reasons, decided on
// the evidence:
//   - The candidates here are the turn's OWN tagged ids — threads the
//     model explicitly named this turn. All of them get engaged either
//     way; ranking only picks which receives the excerpt / edit binding,
//     so a stale-ranked choice is still a thread the model asserted is
//     relevant NOW. The tag-less default (F1's scope) chooses among
//     threads the model did NOT name, where anti-recency binds an edit
//     to unrelated stale work — a categorically worse misbind.
//   - ActiveThreads order cannot rank this candidate set: a tagged id
//     may sit outside Layer B entirely (no rank), and a just-completed
//     §5.5 fetch puts the fetched id at index 0 — fetch order, not
//     engagement recency, which would corrupt the tiebreak it was meant
//     to fix. The latent cross-session inversion here requires a phantom
//     would-be owner PLUS ≥2 valid tagged threads whose LastEngagedTurn
//     values span sessions; revisit only if forensics ever show it.
func mostRecentEngagedID(validIDs []string, resolved map[string]memops.SpineRecord) string {
	best := ""
	bestTurn, bestN := -1, -1
	for _, id := range validIDs {
		rec := resolved[id]
		n, _ := threadIDNum(id)
		if best == "" || rec.LastEngagedTurn > bestTurn ||
			(rec.LastEngagedTurn == bestTurn && n < bestN) {
			best, bestTurn, bestN = id, rec.LastEngagedTurn, n
		}
	}
	return best
}

// updateExistingThread applies this turn's engagement to an existing
// thread whose spine record rec has already been resolved (and
// project-checked) by resolveEngagedThread. When owner is true the
// thread receives the turn excerpt and a turn_count++ (it owns this
// turn's content per spec §3.2); when false it is engaged-but-not-owner
// — recency (last_engaged / last_engaged_turn) and history_symbols merge
// and state→active resurrection apply, but NO excerpt is written and
// turn_count is left unchanged.
func updateExistingThread(ctx context.Context, state *State, rec memops.SpineRecord, owner bool, userInput, responseBody, now string, turnSymbols []coalescedSymbol, turnAnchors []string) error {
	threadID := rec.ID

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

	merged := mergeHistorySymbols(fm.HistorySymbols, turnSymbols, mergeTurn)

	// §2.7.3 origin provenance: union each recall-surfaced thread's id into
	// the DerivedFrom of any merged symbol (newly-emitted OR re-emitted)
	// that coincides with its symbol set. Origins accumulate monotonically,
	// so newness is not a gate — the union is applied to every coinciding
	// entry. Runs on the full merged set BEFORE project/cap — ProjectAnchors
	// copies but does not touch DerivedFrom, and capHistorySymbols only
	// drops entries, so the field survives both. selfID = threadID (a
	// thread is never its own origin).
	populateDerivedFrom(merged, surfacedSymbolSets(ctx, state, threadID), threadID)

	// Merge → project → evict (Build-Plan Risk R5). Only the OWNER turn
	// re-projects (SOLUTION §2: "Engaged-non-owner threads do not
	// re-project"). The projection runs on the full merged set so its
	// EverCentral latches are in place BEFORE eviction reads them; a symbol
	// entering the projection this turn is thereby never evicted this turn.
	// An engaged-non-owner thread skips projection but still enforces the
	// hard cap on its merged history (the merge step no longer caps).
	if owner {
		projected, projectedSyms, changed := projectAnchorsForTurn(ctx, state, merged, mergeTurn)
		fm.HistorySymbols = capHistorySymbols(projectedSyms)
		rec.Anchors = projected
		fm.Anchors = append([]string(nil), projected...)
		if changed {
			rec.AnchorsProjectedAtTurn = mergeTurn
		}
	} else {
		fm.HistorySymbols = capHistorySymbols(merged)
	}

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
	// §6.2 debt-cap hook: once the owner has written more excerpts than the
	// assembly window holds, each new owner excerpt pushes one out of the
	// assembled context (it stays retained on disk for the §3.4 fine tier).
	// Signal that scroll-out so the debt counter can trigger a flush. Inert
	// when no embedder is installed (I7).
	if owner && mergeTurn > store.ThreadTurnWindow {
		recordExcerptScrollOut(state, threadID)
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

	summary := summarizeForNewThread(responseBody)
	description := descriptionFromNewThread(userInput)

	// The owner records one turn; a metadata-only new thread owns no turn
	// (TurnCount 0). history_symbols first_seen_turn uses the same count.
	turnCount := 0
	if owner {
		turnCount = 1
	}

	// A new thread runs its initial projection at creation: its merged
	// history is this first turn's symbols. The anchor headline is the
	// deterministic projection of that set (§2.2 / §2.7.x), NOT the raw
	// model emission — the projection owns the AnchorProjectionMax ceiling
	// and latches EverCentral. The anchor list is advisory: 0 symbols yields
	// a 0-anchor (vague-start) thread, which is legal. Merge → project →
	// evict ordering applies (Risk R5): project on the full merged set, then
	// cap. A genuinely-new thread is always "changed", so the watermark is
	// stamped at creation.
	merged := mergeHistorySymbols(nil, turnSymbols, turnCount)
	// §2.7.3 origin provenance — the PRIMARY synthesis case: a new thread
	// created in a turn that recalled prior threads carries each parent's
	// symbol forward, acquiring that parent's id as the symbol's origin
	// (multi-parent provenance distributed across the synthesized symbols).
	// selfID = newID.
	populateDerivedFrom(merged, surfacedSymbolSets(ctx, state, newID), newID)
	anchors, projectedSyms, _ := projectAnchorsForTurn(ctx, state, merged, turnCount)
	historySymbols := capHistorySymbols(projectedSyms)

	rec := memops.SpineRecord{
		ID:                     newID,
		Project:                state.ActiveProject.ID,
		Anchors:                anchors,
		Summary:                summary,
		Description:            description,
		State:                  memops.ThreadActive,
		Created:                now,
		LastEngaged:            now,
		StateChanged:           now,
		TurnCount:              turnCount,
		LastEngagedTurn:        state.TurnNumber,
		AnchorsProjectedAtTurn: turnCount,
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
		HistorySymbols:  historySymbols,
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
	// §3.11: a thread creation is a structural change. Count it; the
	// turn-close cadence check (turn.go step 5e) commits once per turn.
	state.structuralCreates++
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
