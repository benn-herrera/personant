package turn

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"personant/internal/memops"
	"personant/internal/recall/measure"
)

// joinInts renders a turn-number slice as a comma-separated token for the
// spine.intra-match-fire log line (e.g. "12,40,103"). Empty → "" .
func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// recallOfferK is the number of recall candidates surfaced to the user
// at turn close. C.6 measured top-1 recall ~73%, top-3 ~92%; surface
// the top 3 and let the user pick rather than forcing a single-candidate
// guess. It bounds the whole surfaced set — auto-accepted and asked
// together — so the banded flow can never touch more threads per turn
// than the pre-band flow offered.
const recallOfferK = 3

// §3.4 recall bands (user ruling 2026-08-05). The pre-band surface put
// EVERY candidate above the surface threshold to the user as a cryptic
// four-option question, which is the §3.5 rubber-stamp failure in its
// recall form: a prompt the user clears reflexively launders an unvetted
// fetch as a human decision. A candidate the matcher is confident about
// needs no vetting; the judgment call is the middle band.
//
// The two bars are per TIER because the scales differ: layer 1 is a
// Jaccard set overlap, layers 2/3 (embedding + intra-thread) are cosine
// similarities. Both are grounded in the C.6 corpus calibration
// (internal/scenarios/testdata/recall_madlibs, re-measured 2026-08-05
// over 301 templates × synonym-depth M ∈ 1..4):
//
//   - SYMBOLIC. Precision by threshold: 0.990 at T=0.4, 1.000 at T=0.5
//     and T=0.6, at EVERY drift depth (0.3/0.4 and 0.5/0.6 are pairwise
//     identical operating points — discrete Jaccard score gaps). An
//     auto-accept needs the precision plateau, so the bar sits inside it,
//     one step above the highest measured point rather than on it: the
//     sweep's top row is 0.6, and an unasked promotion should not be
//     calibrated at the edge of the measured range.
//   - COSINE. Precision by cosine cutoff: 0.58 at 0.55 (the surface
//     floor, scoring.DefaultCosineThreshold), 0.76-0.82 at 0.60, 0.89-0.95
//     at 0.65 — monotone and still climbing at the top of the swept range.
//     0.75 is one further step, and it is corroborated independently: it
//     is the cosine at which the #111 live dense-cluster diagnosis found
//     leaves "clearly related, not kidding" (scoring.ClearlyRelated).
//     Deliberately NOT aliased to that constant — a within-thread
//     relevance-net knob and a user-facing ack policy must be free to move
//     apart — but the agreement of two independent measurements at the
//     same value is why this is the chosen bar.
//
// Embedding and intra-thread share ONE bar: they are the same cosine
// against the same embedding space (a thread-body vector vs. a
// turn-excerpt vector), and nothing in the calibration data separates
// them. Split them if evidence ever does.
//
// Both are §9 calibration windows, not frozen values.
const (
	// recallAutoSymbolic is the layer-1 Jaccard score at or above which a
	// candidate is fetched without asking.
	recallAutoSymbolic = 0.65
	// recallAutoCosine is the layer-2/3 cosine at or above which a
	// candidate is fetched without asking.
	recallAutoCosine = 0.75
)

// RecallAckMode is the §2.6.1 recall.ack-mode directive: the policy
// governing who acks a §3.4 recall candidate.
type RecallAckMode string

const (
	// RecallAckBanded is the default: a high-confidence candidate is
	// fetched without asking (one committed line), the middle band is
	// asked, and nothing else surfaces.
	RecallAckBanded RecallAckMode = "banded"
	// RecallAckAlways restores the pre-2026-08-05 flow: every surfaced
	// candidate is put to the resolver, however confident the match.
	RecallAckAlways RecallAckMode = "always"
)

// DirectiveRecallAckMode is the §2.6.1 parameter name the front end reads
// to select a RecallAckMode. Named beside the modes so the key and its
// meaning cannot drift apart (the DirectiveClosureAckMode pattern).
const DirectiveRecallAckMode = "recall.ack-mode"

// ParseRecallAckMode maps a directive value to a mode. An unrecognized
// value yields ok=false; the caller keeps the default rather than
// refusing the session over a display-adjacent preference.
func ParseRecallAckMode(s string) (RecallAckMode, bool) {
	switch RecallAckMode(strings.ToLower(strings.TrimSpace(s))) {
	case RecallAckBanded:
		return RecallAckBanded, true
	case RecallAckAlways:
		return RecallAckAlways, true
	default:
		return "", false
	}
}

// recallAckMode resolves the session's effective mode. The zero value is
// banded, so a State built without setting the field gets the ruling's
// default.
func recallAckMode(state *State) RecallAckMode {
	if state.RecallAckMode == RecallAckAlways {
		return RecallAckAlways
	}
	return RecallAckBanded
}

// autoAccepts reports whether a candidate clears its tier's auto-accept
// bar. The tier is read off which layer produced the unified ranking
// score (the same rule measure.Result ranking uses): a cosine hit —
// embedding, or intra-thread with a real positive chunk score — is scored
// on the cosine scale; everything else is a symbolic Jaccard score. A
// completeness-floor-only hit (§3.4 #123: intra turns at Score 0, no
// other layer) can never auto-accept, which is correct — it is a
// findability guarantee, not a relevance claim.
func autoAccepts(r measure.Result) bool {
	if r.Embedding != nil || (r.IntraThread != nil && r.IntraThread.Score > 0) {
		return r.Score >= recallAutoCosine
	}
	return r.Score >= recallAutoSymbolic
}

// DeclineReason is the fixed-enum reason captured when a recall
// candidate is not accepted. Deliberately small for v0.1.
type DeclineReason string

const (
	DeclineNotRelevant  DeclineReason = "not-relevant"
	DeclineWrongProject DeclineReason = "wrong-project"
	DeclineAlreadyKnown DeclineReason = "already-known"
)

// RecallCandidate is one offered candidate: the recall stack's scored
// Result plus the two human-legible fields the experience layer needs to
// render a question a user can answer in two seconds (user ruling
// 2026-08-05). The runtime resolves them from the spine record — the
// front end renders, and never has to reach back into the substrate from
// inside a resolver.
//
// measure.Result is embedded, so a caller still reads c.ThreadID,
// c.Score, c.Layers() and the per-layer hits exactly as before.
type RecallCandidate struct {
	measure.Result

	// Display is the thread's working name — §2.2.2 display form, never a
	// bare thr_N. The spine Description (the utterance that spawned the
	// thread, set at creation and never rewritten); its id only if a
	// record somehow carries none.
	Display string

	// Gist is what the thread is about, in one line: the spine Summary
	// when the record has one, else the thread's projected anchors. Never
	// blank and never a raw id — a candidate the user cannot identify is
	// the thing the ruling struck down.
	Gist string
}

// RecallOffer is the set of recall candidates put to the resolver at
// turn close, ranked best-first. Indexes into Candidates are stable for a
// RecallResolution to reference. Under the banded default it holds the
// ASK band only (the auto band never reaches a resolver); under
// recall.ack-mode: always it holds every surfaced candidate.
type RecallOffer struct {
	Candidates []RecallCandidate
}

// RecallNotice is the one-line record of a recall the runtime applied
// without asking (§3.4 auto-accept band), handed to the optional
// State.OnAutoRecalled presentation hook. The runtime renders nothing
// itself — the front end owns the wording. Sibling of ClosureNotice.
type RecallNotice struct {
	ThreadID string // thr_<n>
	Display  string // the thread's working name
	Gist     string // what it is about
}

// RecallResolution is the user's (or scripted harness's) verdict on a
// RecallOffer. Accept holds indexes into RecallOffer.Candidates of
// threads to pull into Layer B; any candidate not in Accept is declined,
// and Reason is recorded against the declined remainder.
type RecallResolution struct {
	Accept []int
	Reason DeclineReason
}

// RecallResolver is the seam between the recall stack and the
// experience layer. The chat REPL implements it interactively; the
// scenario harness implements it from a pre-declared script. A nil
// resolver on State means recall stays log-only — no offer surfaced.
type RecallResolver func(ctx context.Context, offer RecallOffer) (RecallResolution, error)

// surfaceRecallCandidates runs the §3.4 recall stack for this turn,
// logs the per-layer matches, and — when a RecallResolver is installed
// — bands the top recallOfferK candidates (user ruling 2026-08-05):
//
//   - AUTO band (score at or above its tier's bar, see autoAccepts) — the
//     thread is fetched through the same chokepoint an accepted candidate
//     takes, with no prompt, one committed line via State.OnAutoRecalled,
//     and recall.accept ack=auto.
//   - ASK band (surfaced but under the bar) — put to the resolver as ONE
//     offer block, with the display name / gist / why-it-matched fields a
//     user can act on. recall.accept ack=human on acceptance.
//   - Below the surface threshold — never reaches here (the recall stack
//     never returns it).
//
// With no resolver it stays log-only, and that includes the auto band:
// the resolver is the "an experience layer is attached" signal, and a
// session with no way to ack must not start fetching on its own (the
// §3.5 precedent, where auto-accept also requires the resolver). The
// harness's unmeasured steps therefore keep byte-identical behaviour.
//
// recall.ack-mode: always restores the pre-ruling flow verbatim — every
// surfaced candidate goes to the resolver.
//
// On accept, applyRecallResolution increments SpineRecord.RecallFires
// (and the mirrored ThreadMeta.RecallFires) via
// MemoryOps.RecordRecallFire — the §2.2 "matches that resulted in
// fetch" counter. A counter-write failure is logged and swallowed; it
// never aborts turn close.
//
// Recall runs entirely behind the measure.Recaller interface — this
// function knows nothing of symbolic Jaccard, embedding cosine, or the
// embedding index. It hands the Recaller the turn's symbols and text
// and logs whatever merged candidates come back, per layer.
//
// engagedSet (threads engaged this turn) is excluded — an engaged
// thread doesn't shadow the turn's own engagement signal. A Recaller
// error is non-fatal: the caller logs and swallows it (recall is
// opportunistic and must never abort the turn).
//
// engagedOwner is the thread the user is currently in (the turn's owner).
// It is passed as Request.Engaged so the §4.1 step-3 intra-thread pass
// scans its scrolled-out early-content chunks (the #109 case) even though
// it is in engagedSet at the thread level. Empty when nothing was engaged.
func surfaceRecallCandidates(ctx context.Context, state *State, userInput string, engagedSet map[string]struct{}, engagedOwner string) error {
	if state.Recaller == nil {
		return nil
	}
	results, err := state.Recaller.Recall(ctx, measure.Request{
		QuerySymbols: state.coalesce.symbolList(),
		QueryText:    userInput,
		Project:      state.ActiveProject.ID,
		Exclude:      engagedSet,
		Engaged:      engagedOwner,
		// Bound for the §3.4 recall-completeness lexical pass (#123): the
		// embedding-debt CAP, the max scrolled-out tail that may be awaiting
		// its async fine-tier flush. Passed only when an embedder-capable
		// recaller is installed (debtWindowBound), so the fine tier — and thus
		// the lag dead zone — actually exists; a symbolic-only setup has no
		// fine tier and keeps its byte-identical prior behaviour (0 → no debt
		// pass). The cap, not the live debt, so the floor stays continuous
		// across a debt-cap flush's in-flight window (see Request docs).
		EngagedDebtWindow: debtWindowBound(state, engagedOwner),
		// Turn-excerpts of the engaged thread an accepted recall already
		// pulled into the working window this session — the accept-suppression
		// fix (user ruling 2026-08-05). Without it the intra pass re-proposes
		// the excerpt the user just accepted, every turn the topic continues:
		// the engaged thread cannot be excluded at the thread level (the intra
		// pass targets it BY id), and the accept left no mark. See
		// State.recallWindowTurns.
		EngagedInWindow: state.recallWindowTurns[engagedOwner],
	})
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}

	querySize := len(state.coalesce.symbols)
	queryChars := len(strings.TrimSpace(userInput))
	for _, r := range results {
		// Per-layer logging — the spine.match-fire / spine.embed-match-fire
		// vocabulary is preserved so the recall-fidelity test harness and
		// the corpus measurements keep parsing it unchanged.
		if r.Symbolic != nil {
			details := fmt.Sprintf("%s score=%.2f matched=%s query_size=%d",
				r.ThreadID, r.Symbolic.Score,
				strings.Join(r.Symbolic.MatchedSymbols, ","), querySize)
			if err := state.Ops.Log(ctx, memops.LogCategorySpine, "match-fire", details); err != nil {
				return fmt.Errorf("log spine.match-fire: %w", err)
			}
		}
		if r.Embedding != nil {
			details := fmt.Sprintf("%s score=%.3f query_chars=%d",
				r.ThreadID, r.Embedding.Score, queryChars)
			if err := state.Ops.Log(ctx, memops.LogCategorySpine, "embed-match-fire", details); err != nil {
				return fmt.Errorf("log spine.embed-match-fire: %w", err)
			}
		}
		// Intra-thread (#109) fine-tier hit on the engaged thread: an early,
		// scrolled-out turn-excerpt matched the query. Mirrors the
		// embed-match-fire emission (design §7) — the additive
		// spine.intra-match-fire event the §8 oracle scores. Only non-user
		// values reach the format string: the thread id, the matched
		// turn-excerpt number(s), and the best chunk score.
		if r.IntraThread != nil {
			details := fmt.Sprintf("%s score=%.3f turns=%s query_chars=%d",
				r.ThreadID, r.IntraThread.Score, joinInts(r.IntraThread.Turns), queryChars)
			if err := state.Ops.Log(ctx, memops.LogCategorySpine, "intra-match-fire", details); err != nil {
				return fmt.Errorf("log spine.intra-match-fire: %w", err)
			}
		}
	}

	if state.RecallResolver == nil || len(results) == 0 {
		return nil
	}
	surfaced := describeCandidates(ctx, state, results[:min(len(results), recallOfferK)])

	if recallAckMode(state) == RecallAckAlways {
		return putOffer(ctx, state, surfaced)
	}

	// Banded. The auto band is applied first so an accepted thread is
	// already resident before the ask block is rendered.
	var ask []RecallCandidate
	for _, c := range surfaced {
		if autoAccepts(c.Result) {
			if acceptRecallCandidate(ctx, state, c, ackAuto) && state.OnAutoRecalled != nil {
				state.OnAutoRecalled(RecallNotice{ThreadID: c.ThreadID, Display: c.Display, Gist: c.Gist})
			}
			continue
		}
		// A candidate already resident in Layer B is already IN the context
		// the offer proposes to pull it into, so asking is a question with no
		// content — and under the auto band it would re-fire, and re-print,
		// every turn the topic continued. The runtime answers it:
		// already-known, the §4.3 reason that exactly describes it. An
		// intra-thread hit is exempt — its subject is the engaged thread's
		// early content, which residency says nothing about.
		if c.IntraThread == nil && slices.Contains(state.ActiveThreads, c.ThreadID) {
			if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "decline",
				fmt.Sprintf("thr=%s reason=%s", c.ThreadID, DeclineAlreadyKnown)); err != nil {
				return fmt.Errorf("log recall.decline: %w", err)
			}
			continue
		}
		ask = append(ask, c)
	}
	if len(ask) == 0 {
		return nil
	}
	return putOffer(ctx, state, ask)
}

// putOffer logs the offer and puts it to the installed resolver, applying
// whatever comes back. ONE offer block per turn — the candidate list is
// already capped at recallOfferK.
func putOffer(ctx context.Context, state *State, cands []RecallCandidate) error {
	offer := RecallOffer{Candidates: cands}
	if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "offer", fmt.Sprintf("count=%d", len(offer.Candidates))); err != nil {
		return fmt.Errorf("log recall.offer: %w", err)
	}
	resolution, err := state.RecallResolver(ctx, offer)
	if err != nil {
		return fmt.Errorf("recall: resolve offer: %w", err)
	}
	return applyRecallResolution(ctx, state, offer, resolution)
}

// describeCandidates resolves each result's human-legible display name
// and gist from its spine record (§2.2.2) so the experience layer can
// render a question the user can answer without a lookup.
//
// It reads the spine ONCE for the whole (≤ recallOfferK) set rather than
// per candidate: the two are the same file read, and this one runs on
// every turn that surfaces anything. An unreadable spine, or a candidate
// with no record, degrades to the thread id rather than failing recall —
// the candidate is still offerable, just less legible — and the fetch that
// follows an accept would surface the real fault anyway. The filter is
// deliberately empty: an embedding or intra hit can name a thread outside
// the active project, and describing it is not promoting it (the §3.2
// guard lives on the fetch path).
func describeCandidates(ctx context.Context, state *State, results []measure.Result) []RecallCandidate {
	out := make([]RecallCandidate, len(results))
	for i, r := range results {
		out[i] = RecallCandidate{Result: r, Display: r.ThreadID, Gist: r.ThreadID}
	}
	recs, err := state.Ops.ListThreads(ctx, memops.ThreadFilter{})
	if err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "error",
			"describe-candidates err="+memops.SanitizeDetail(err.Error()))
		return out
	}
	byID := make(map[string]memops.SpineRecord, len(recs))
	for _, rec := range recs {
		byID[rec.ID] = rec
	}
	for i := range out {
		rec, found := byID[out[i].ThreadID]
		if !found {
			continue
		}
		if rec.Description != "" {
			out[i].Display = rec.Description
		}
		switch {
		case rec.Summary != "":
			out[i].Gist = rec.Summary
		case len(rec.Anchors) > 0:
			out[i].Gist = strings.Join(rec.Anchors[:min(len(rec.Anchors), gistAnchors)], ", ")
		}
	}
	return out
}

// gistAnchors bounds the anchor fallback in a candidate's gist. Enough to
// identify the thread, short enough to stay one readable line beside a
// display name.
const gistAnchors = 4

// acceptRecallCandidate pulls one candidate into Layer B and records the
// verdict. It is the ONE accept path — the human ask band and the auto
// band both run it, so no future divergence can let one skip the fetch
// chokepoint, the counter, or the suppression mark. ack records WHO
// accepted (§2.8 recall.accept ack=human|auto), by parameter rather than
// by a resolution field so no experience layer can claim a human ack the
// human never gave (the applyClosureResolution precedent).
//
// A recall-accept promotion is a working-window mutation, so it goes
// through the SAME §3.0.5 fetch chokepoint every other Layer-B fetch does
// (G-F3 / "no gaps"): fetchThroughChain loads the thread, declines a
// cross-project candidate (§3.2 — an embedding/intra hit can name a
// foreign thread the symbolic project filter never saw, so the accept
// path needs the same guard the §5.5 reprompt has), fires the
// thread.fetched delta through onContextDelta, and promotes into Layer B
// (recording the §2.7.3 recallSurfaced origin — an accept runs at
// turn-close, so its attribution lands one turn later). On any outcome
// that prevents promotion (fetch-miss or cross-project decline, both
// already logged) it returns false with no accept log, no RecallFires
// bump and no suppression mark — exactly as the reprompt path skips an
// unpromotable thread.
//
// A log-write failure is logged-and-swallowed rather than returned: the
// promotion has already happened, and aborting here would strand the
// remaining candidates of the same offer.
func acceptRecallCandidate(ctx context.Context, state *State, c RecallCandidate, ack ackKind) bool {
	if !fetchThroughChain(ctx, state, c.ThreadID) {
		return false
	}
	if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "accept",
		fmt.Sprintf("thr=%s layers=%s ack=%s", c.ThreadID, strings.Join(c.Layers(), "+"), ack)); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "error",
			"accept-log thr="+c.ThreadID+" err="+memops.SanitizeDetail(err.Error()))
	}
	// An accept is a fetch — bump the §2.2 RecallFires counter. A
	// counter-write failure must not abort turn close or skip the remaining
	// accepted threads: log it and continue.
	if err := state.Ops.RecordRecallFire(ctx, c.ThreadID); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRecall, "fire-error",
			"thr="+c.ThreadID+" err="+memops.SanitizeDetail(err.Error()))
	}
	markRecallWindowTurns(state, c)
	return true
}

// markRecallWindowTurns records the engaged-thread turn-excerpts an
// accepted intra-thread candidate brought into the working window, so the
// §3.4 intra pass stops re-proposing them (State.recallWindowTurns). A
// candidate with no intra hit carries no turns and marks nothing — the
// thread-level layers are already handled by Layer-B residency.
func markRecallWindowTurns(state *State, c RecallCandidate) {
	if c.IntraThread == nil || len(c.IntraThread.Turns) == 0 {
		return
	}
	if state.recallWindowTurns == nil {
		state.recallWindowTurns = make(map[string]map[int]struct{})
	}
	turns := state.recallWindowTurns[c.ThreadID]
	if turns == nil {
		turns = make(map[int]struct{}, len(c.IntraThread.Turns))
		state.recallWindowTurns[c.ThreadID] = turns
	}
	for _, n := range c.IntraThread.Turns {
		turns[n] = struct{}{}
	}
}

// applyRecallResolution pulls accepted candidates into Layer B and logs
// an accept/decline verdict against every offered candidate. Declined
// candidates carry res.Reason (defaulting to DeclineNotRelevant when
// empty).
func applyRecallResolution(ctx context.Context, state *State, offer RecallOffer, res RecallResolution) error {
	accepted := make(map[int]bool, len(res.Accept))
	for _, i := range res.Accept {
		if i < 0 || i >= len(offer.Candidates) {
			return fmt.Errorf("recall: resolution accept index %d out of range [0,%d)", i, len(offer.Candidates))
		}
		accepted[i] = true
	}
	for i, c := range offer.Candidates {
		if accepted[i] {
			acceptRecallCandidate(ctx, state, c, ackHuman)
			continue
		}
		reason := res.Reason
		if reason == "" {
			reason = DeclineNotRelevant
		}
		if err := state.Ops.Log(ctx, memops.LogCategoryRecall, "decline",
			fmt.Sprintf("thr=%s reason=%s", c.ThreadID, reason)); err != nil {
			return fmt.Errorf("log recall.decline: %w", err)
		}
	}
	return nil
}
