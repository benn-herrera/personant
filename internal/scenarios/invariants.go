package scenarios

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"personant/internal/autogit"
	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/verify"
)

// InvariantCheck is one validator. Returns nil on pass, a structured
// error on fail. Invariants never call t.Fatal directly; the harness
// translates errors into t.Errorf so a single run surfaces every
// problem.
type InvariantCheck func(h *Harness) error

// DefaultInvariants is the suite that runs after every step (and after
// the last step, when FinalInvariants is empty). Covers the spec §9.4
// baseline.
//
// VerifyClosedThreadConsistency is deliberately excluded: it inspects
// the in-memory session's ActiveThreads/DormantThreads, which is only
// meaningful after a closure scenario has run. Promoting it would make
// every non-closure scenario assert a property about a working set that
// closure never touched — true but vacuous. Closure scenarios opt it in
// explicitly.
//
// Internally DefaultInvariants is partitioned into a cheap subset (run
// every step) and a heavy subset (run every step by default, but a
// Scenario may opt into a time-cadenced firing of the heavy checks via
// Scenario.HeavyInvariantCadence — useful for long-running simulations
// where the heavy checks' full-spine sweeps make per-step firing the
// dominant wall-time cost). End-of-run always runs the full suite, so
// the acceptance-gate behavior is preserved regardless of cadence.
//
// The exported DefaultInvariants concatenates the two so callers that
// embed it as a baseline (e.g. FinalInvariants: append(DefaultInvariants,
// ...)) keep the same full-suite semantics they always had.
var DefaultInvariants = append(append([]InvariantCheck{}, cheapDefaultInvariants...), heavyDefaultInvariants...)

// cheapDefaultInvariants are the per-step-safe checks: strictly O(1) or
// O(touched-this-step) cost. No full-spine reads, no project enumeration,
// no folds over cumulative state that grows with the run. Safe to fire on
// every step in any scenario, including the four-month sim, without
// contributing to per-step wall-time growth.
//
// VerifyNoBudgetOverflow re-composes the working set (the same bounded work
// the turn itself does — active/dormant membership + active-project spine,
// all flat in steady state), so its per-step cost does not grow with run
// length; it belongs with the cheap tier per D1 (a per-step belt-and-
// suspenders on the mock-rung byte budget).
var cheapDefaultInvariants = []InvariantCheck{
	VerifyLastActiveValid,
	VerifyNoBudgetOverflow,
}

// heavyDefaultInvariants are the substrate-scale checks: each one does
// at least one O(threads-on-disk) pass, and several do more
// (VerifyIndexFresh does two; VerifyThreadAccounting folds the
// monotonically-growing created/archived sets AND reads the spine).
// They make per-step invariant firing super-linear in a long simulation.
// A scenario may opt into a time-cadenced firing via
// Scenario.HeavyInvariantCadence; they always fire once at end-of-run
// (the FinalInvariants path) regardless of cadence.
var heavyDefaultInvariants = []InvariantCheck{
	VerifySpineIntegrity,
	VerifyIndexFresh,
	VerifyProjectReferences,
	VerifyThreadMetaMatchesSpine,
	VerifyThreadAccounting,
	// §3.8/§3.9 checks are substrate-scale: VerifyArchiveResolvable walks the
	// archive index doing git object lookups (NOT cheap), VerifyDedupConsistency
	// reads every live thread's files.json sidecar. They ride the same
	// heavy-cadence the other full-spine checks use — per-step on handwritten
	// scenarios, day-cadenced + end-of-run on the sim.
	VerifyArchiveResolvable,
	VerifyDedupConsistency,
}

// VerifySpineIntegrity wraps verify.Verify and surfaces any errors.
// Index drift is reported separately by VerifyIndexFresh; verify.Verify
// also reports drift but we let the dedicated check own that channel
// so errors aren't double-counted when both run.
func VerifySpineIntegrity(h *Harness) error {
	report, err := verify.Verify(h.Paths, verify.VerifyOptions{Quiet: true})
	if err != nil {
		return fmt.Errorf("VerifySpineIntegrity: verify failed: %w", err)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("VerifySpineIntegrity: %d schema errors: %s",
			len(report.Errors), formatFindings(report.Errors))
	}
	return nil
}

// VerifyIndexFresh runs index.Rebuild (writing derived files) followed
// by index.Check (which now must report no drift). Equivalent to the
// derived-file freshness gate that autogit's CheckDerivedFresh enforces
// inline at each state-changing git op in the home tree.
//
// Rebuild-then-check is the right shape because turn.Run does not
// regenerate derived indices on every turn; the runtime's contract is
// that `index rebuild` runs at retirement time (Phase 4) or on demand.
// Inside a scenario we force the rebuild so the assertion measures
// "could the indices be regenerated?", not "did the runtime
// auto-regenerate?".
func VerifyIndexFresh(h *Harness) error {
	if err := indexRebuild(h.Paths); err != nil {
		return fmt.Errorf("VerifyIndexFresh: rebuild: %w", err)
	}
	res, err := indexCheck(h.Paths)
	if err != nil {
		return fmt.Errorf("VerifyIndexFresh: check: %w", err)
	}
	if !res.OK() {
		return fmt.Errorf("VerifyIndexFresh: %d drifts after immediate rebuild: %v",
			len(res.Drifts), res.Drifts)
	}
	return nil
}

// VerifyProjectReferences walks the spine; every record's project
// field must resolve to either a known prj_<n> with meta.json or to
// the reserved prj_default sentinel.
func VerifyProjectReferences(h *Harness) error {
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		return fmt.Errorf("VerifyProjectReferences: read spine: %w", err)
	}
	known := make(map[string]bool, 4)
	known[store.DefaultProjectID] = true
	metas, err := store.ListProjects(h.Paths)
	if err != nil {
		return fmt.Errorf("VerifyProjectReferences: list projects: %w", err)
	}
	for _, m := range metas {
		known[m.ID] = true
	}
	for i, r := range recs {
		if !known[r.Project] {
			return fmt.Errorf("VerifyProjectReferences: spine[%d] %s references unknown project %q",
				i+1, r.ID, r.Project)
		}
	}
	return nil
}

// VerifyLastActiveValid reads <Home>/last-active and asserts it is
// either empty (fresh install) or names a known project (prj_default
// counts).
func VerifyLastActiveValid(h *Harness) error {
	id, err := store.ReadLastActive(h.Paths)
	if err != nil {
		return fmt.Errorf("VerifyLastActiveValid: %w", err)
	}
	if id == "" {
		return nil
	}
	if id == store.DefaultProjectID {
		return nil
	}
	if _, err := store.LoadProjectMeta(h.Paths, id); err != nil {
		return fmt.Errorf("VerifyLastActiveValid: %s: %w", id, err)
	}
	return nil
}

// VerifyThreadMetaMatchesSpine: for each spine record, load the
// corresponding thread file and assert the canonical-overlap fields
// agree exactly. A missing thread file is logged as a warning via
// h.T.Logf rather than failing — v0.1 has no archival path that would
// legitimately strand a spine record without a file, but Phase 4
// retirement may; surfacing the case as a warning lets future code
// land without a flood of false-positive failures here.
func VerifyThreadMetaMatchesSpine(h *Harness) error {
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		return fmt.Errorf("VerifyThreadMetaMatchesSpine: read spine: %w", err)
	}
	for i, r := range recs {
		fm, err := store.LoadThreadFrontmatter(h.Paths, r.ID)
		if err != nil {
			if errors.Is(err, memops.ErrThreadFileNotFound) {
				h.T.Logf("VerifyThreadMetaMatchesSpine: spine[%d] %s has no thread file (suspicious in v0.1)", i+1, r.ID)
				continue
			}
			return fmt.Errorf("VerifyThreadMetaMatchesSpine: load %s: %w", r.ID, err)
		}
		if fm.ID != r.ID {
			return fmt.Errorf("frontmatter id %q != spine id %q (record %d)", fm.ID, r.ID, i+1)
		}
		if fm.Project != r.Project {
			return fmt.Errorf("%s: frontmatter project %q != spine %q", r.ID, fm.Project, r.Project)
		}
		if fm.Summary != r.Summary {
			return fmt.Errorf("%s: frontmatter summary differs from spine", r.ID)
		}
		if fm.State != r.State {
			return fmt.Errorf("%s: frontmatter state %q != spine %q", r.ID, fm.State, r.State)
		}
		if fm.Created != r.Created {
			return fmt.Errorf("%s: frontmatter created %q != spine %q", r.ID, fm.Created, r.Created)
		}
		if fm.LastEngaged != r.LastEngaged {
			return fmt.Errorf("%s: frontmatter last_engaged %q != spine %q", r.ID, fm.LastEngaged, r.LastEngaged)
		}
		if fm.StateChanged != r.StateChanged {
			return fmt.Errorf("%s: frontmatter state_changed %q != spine %q", r.ID, fm.StateChanged, r.StateChanged)
		}
		if fm.TurnCount != r.TurnCount {
			return fmt.Errorf("%s: frontmatter turn_count %d != spine %d", r.ID, fm.TurnCount, r.TurnCount)
		}
		if fm.RecallFires != r.RecallFires {
			return fmt.Errorf("%s: frontmatter recall_fires %d != spine %d", r.ID, fm.RecallFires, r.RecallFires)
		}
		if !stringSlicesEqual(fm.Anchors, r.Anchors) {
			return fmt.Errorf("%s: frontmatter anchors %v != spine %v", r.ID, fm.Anchors, r.Anchors)
		}
	}
	return nil
}

// VerifyEngagementConsistency: replay the operation log via the
// turn-count and last-engaged fields and assert the on-disk thread
// frontmatter matches the spine's bookkeeping. v0.1 keeps both in
// sync turn-by-turn (the same write path updates both); this invariant
// fires the cross-check explicitly.
//
// Per §3.0.4: turn_count increments by 1 per turn per affected thread
// regardless of how many deltas in the turn touched it. The harness's
// per-step metrics (engaged_existing_threads, threads_created) provide
// the operation log; this invariant compares them against the spine
// totals.
func VerifyEngagementConsistency(h *Harness) error {
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		return fmt.Errorf("VerifyEngagementConsistency: read spine: %w", err)
	}
	for _, r := range recs {
		fm, err := store.LoadThreadFrontmatter(h.Paths, r.ID)
		if err != nil {
			if errors.Is(err, memops.ErrThreadFileNotFound) {
				continue
			}
			return fmt.Errorf("VerifyEngagementConsistency: load %s: %w", r.ID, err)
		}
		if fm.TurnCount != r.TurnCount {
			return fmt.Errorf("VerifyEngagementConsistency: %s frontmatter turn_count %d != spine %d",
				r.ID, fm.TurnCount, r.TurnCount)
		}
		if fm.LastEngaged != r.LastEngaged {
			return fmt.Errorf("VerifyEngagementConsistency: %s frontmatter last_engaged %q != spine %q",
				r.ID, fm.LastEngaged, r.LastEngaged)
		}
	}
	return nil
}

// VerifyClosedThreadConsistency: every thread whose spine state is a
// retired state (resolved / decided / abandoned) must NOT appear in
// State.ActiveThreads or State.DormantThreads and must carry a non-empty
// Summary. This is the §3.5 closure-flow invariant — a closed thread
// that leaked back into the active or dormant working set, or was
// retired without a curator summary, is a closure-flow bug. The apply
// path evicts a retired thread from both layers; WIP threads
// legitimately live in DormantThreads, so the Dormant check is kept
// specific to the three retired states.
func VerifyClosedThreadConsistency(h *Harness) error {
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		return fmt.Errorf("VerifyClosedThreadConsistency: read spine: %w", err)
	}
	active := make(map[string]bool, len(h.State.ActiveThreads))
	for _, id := range h.State.ActiveThreads {
		active[id] = true
	}
	dormant := make(map[string]bool, len(h.State.DormantThreads))
	for _, id := range h.State.DormantThreads {
		dormant[id] = true
	}
	for _, r := range recs {
		switch r.State {
		case memops.ThreadResolved, memops.ThreadDecided, memops.ThreadAbandoned:
		default:
			continue
		}
		if active[r.ID] {
			return fmt.Errorf("VerifyClosedThreadConsistency: %s is %s but still in ActiveThreads", r.ID, r.State)
		}
		if dormant[r.ID] {
			return fmt.Errorf("VerifyClosedThreadConsistency: %s is %s but still in DormantThreads", r.ID, r.State)
		}
		if r.Summary == "" {
			return fmt.Errorf("VerifyClosedThreadConsistency: %s is %s but has an empty summary", r.ID, r.State)
		}
	}
	return nil
}

// VerifyThreadAccounting asserts the substrate never drops or
// double-counts a thread: every thread that was ever created must end
// up in exactly one of {on the spine, archived}. It is the disjoint
// union  created = on-spine ⊎ archived.
//
// The three sets are reconstructed from durable evidence:
//   - created  — `thread.created` log lines, folded incrementally into
//     h.createdThreadIDs by the harness's per-step log tailer.
//   - archived — `archive.archived` log lines, folded into
//     h.archivedThreadIDs the same way (the §3.8 recoverable git-based
//     archival path — off the live spine but preserved + recoverable).
//   - onSpine  — thread IDs currently on the spine.
//
// The created/archived sets come off the Harness rather than a fresh
// log walk: re-reading all of run-so-far on every step was O(N²) in a
// long simulation. The harness tails the event log once per turn and
// these cumulative sets only grow.
//
// For every created ID exactly one of {on-spine, archived} must hold:
//   - Neither → unexplained loss: the substrate silently dropped a
//     thread (a real bug).
//   - Both → the thread is on the spine yet recorded as archived — ID
//     reuse or archival corruption.
//
// It is TWO-DIRECTIONAL (BD-2):
//
//   - Forward (created ⟶ spine⊎archived): every created ID ends in exactly one
//     of {on-spine, archived}. Neither = unexplained loss; both = ID reuse /
//     corruption.
//   - Reverse (run-created spine ⟶ created): every thread the runtime CREATED
//     DURING THE RUN and left on the spine must appear in the harness's created
//     set. A gap means the harness never observed a real creation — the
//     silent-failure mode a renamed `thread.created` marker
//     (turn/engage.go) produces, which the forward direction alone cannot
//     catch (it only inspects IDs the harness already folded).
//   - Non-vacuity: an entirely empty created set while the spine holds
//     run-created threads is the canonical marker-drift signature and is called
//     out distinctly.
//
// The reverse direction is scoped to threads CREATED DURING THE RUN, excluding
// pre-seeded fixtures. The seed-vs-create boundary is the thread's Created
// timestamp: the runtime stamps every thread it creates with the simulated
// clock (clock.Timeline(), pinned by the harness to a value never preceding
// SimClockStart within a run), whereas fixtures written straight to the spine
// by store.SeedThread to represent pre-existing history carry an earlier
// historical timestamp and never emit a marker (see threadCreatedDuringRun).
//
// All violations are collected, sorted, and reported in one error.
func VerifyThreadAccounting(h *Harness) error {
	created := h.createdThreadIDs
	archived := h.archivedThreadIDs
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		return fmt.Errorf("VerifyThreadAccounting: read spine: %w", err)
	}
	onSpine := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		onSpine[r.ID] = struct{}{}
	}

	// Forward direction.
	var lost, both []string
	for id := range created {
		_, isSpine := onSpine[id]
		_, isArchived := archived[id]
		switch {
		case !isSpine && !isArchived:
			lost = append(lost, id)
		case isSpine && isArchived:
			both = append(both, id)
		}
	}

	// Reverse direction + non-vacuity.
	var unobserved []string
	runCreatedOnSpine := 0
	for _, r := range recs {
		if !threadCreatedDuringRun(r.Created) {
			continue // pre-seeded fixture — the harness never observes its creation
		}
		runCreatedOnSpine++
		if _, ok := created[r.ID]; !ok {
			unobserved = append(unobserved, r.ID)
		}
	}

	if len(lost) == 0 && len(both) == 0 && len(unobserved) == 0 {
		return nil
	}
	sort.Strings(lost)
	sort.Strings(both)
	sort.Strings(unobserved)
	var parts []string
	if len(lost) > 0 {
		parts = append(parts, fmt.Sprintf("unexplained loss (created but neither on-spine nor archived): %s",
			strings.Join(lost, ", ")))
	}
	if len(both) > 0 {
		parts = append(parts, fmt.Sprintf("on-spine and archived simultaneously (ID reuse or archival corruption): %s",
			strings.Join(both, ", ")))
	}
	if len(unobserved) > 0 {
		if len(created) == 0 {
			parts = append(parts, fmt.Sprintf(
				"created set is EMPTY while %d run-created thread(s) are on the spine — thread.created marker drift? %s",
				runCreatedOnSpine, strings.Join(unobserved, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf(
				"run-created but never observed in the created set (thread.created marker gap): %s",
				strings.Join(unobserved, ", ")))
		}
	}
	return fmt.Errorf("VerifyThreadAccounting: %s", strings.Join(parts, "; "))
}

// threadCreatedDuringRun reports whether a spine record's Created timestamp
// falls at or after the simulated-run clock anchor (SimClockStart) — the
// seed-vs-create boundary VerifyThreadAccounting's reverse direction uses. An
// unparseable timestamp is treated as NOT run-created: a malformed Created is
// VerifySpineIntegrity's channel, and swallowing it here avoids a spurious
// accounting failure on a record another check already owns.
func threadCreatedDuringRun(created string) bool {
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		return false
	}
	return !t.Before(SimClockStart)
}

// VerifyArchiveResolvable asserts every §3.8 archive-index entry is
// structurally recoverable — the runtime evidence that the recoverable
// git-based archival path (fileadapter_archive.go) never strands a thread off
// the spine without a way back.
//
// For each archive/index.jsonl entry it mirrors RecoverThread's preconditions
// without mutating anything:
//   - CommitHash must be non-empty (RecoverThread's first guard: an un-stamped
//     entry sits in the deletion↔stamp crash window and cannot self-resolve).
//   - A drift entry (empty TreeHash) recovers record-only from the index
//     snapshot; there is no on-disk body and nothing further to resolve.
//   - A content entry's bytes live in the capture commit (the deletion commit's
//     parent). Resolve the parent exactly as recovery does — prefer the stored
//     ParentCommitHash, else walk CommitHash's first parent — then confirm the
//     committed subtree hash matches the stored integrity token. TreeHashAt
//     loads the commit + tree objects, so a present, matching result proves
//     both are in the home repo and a recovery lookup is possible.
//
// Recovered entries (RecoveredAt set) are checked too: their capture commit
// stays reachable in history, so it must still resolve.
//
// This is a heavy check: it touches git per content entry. It runs at the
// heavy cadence, not per step (see heavyDefaultInvariants).
func VerifyArchiveResolvable(h *Harness) error {
	entries, err := store.LoadArchiveIndex(h.Paths)
	if err != nil {
		return fmt.Errorf("VerifyArchiveResolvable: load archive index: %w", err)
	}
	ctx := context.Background()
	var problems []string
	for _, e := range entries {
		if e.CommitHash == "" {
			problems = append(problems, fmt.Sprintf("%s: empty commit_hash (un-stamped; cannot self-recover)", e.ThrID))
			continue
		}
		if e.TreeHash == "" {
			continue // drift entry — record-only recovery, no git tree to resolve
		}
		parent := e.ParentCommitHash
		if parent == "" {
			parent, err = autogit.ParentCommitHash(ctx, h.Paths, autogit.Primary, e.CommitHash)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: resolve parent of %s: %v", e.ThrID, e.CommitHash, err))
				continue
			}
		}
		got, err := autogit.TreeHashAt(ctx, h.Paths, autogit.Primary, parent, e.OriginalPath)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: subtree %q at %s unresolvable: %v", e.ThrID, e.OriginalPath, parent, err))
			continue
		}
		if got != e.TreeHash {
			problems = append(problems, fmt.Sprintf("%s: tree-hash mismatch (index %s != commit %s)", e.ThrID, e.TreeHash, got))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("VerifyArchiveResolvable: %d unresolvable archive entries: %s",
			len(problems), strings.Join(problems, "; "))
	}
	return nil
}

// VerifyDedupConsistency asserts every live thread's §3.9 tracked-file sidecar
// (threads/<id>/files.json) round-trips: it loads, and every reverse-delta
// chain reconstructs byte-exactly.
//
// A present-but-corrupt sidecar fails LoadThreadFiles (a missing one is the
// benign fresh state and loads as empty). For each tracked file, every recorded
// version must Reconstruct without error — a broken delta or missing literal
// anchor surfaces here — and Current() must agree with the newest reconstructed
// version (the newest is always stored as a literal). Chains are short (bounded
// by the live window + anchor cadence), so the per-file cost is small; the
// enumeration over live threads is what puts it in the heavy tier.
func VerifyDedupConsistency(h *Harness) error {
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		return fmt.Errorf("VerifyDedupConsistency: read spine: %w", err)
	}
	var problems []string
	for _, r := range recs {
		tf, err := store.LoadThreadFiles(h.Paths, r.ID)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: sidecar unloadable: %v", r.ID, err))
			continue
		}
		for path, fe := range tf.Files {
			if fe == nil {
				problems = append(problems, fmt.Sprintf("%s:%s: nil file entry", r.ID, path))
				continue
			}
			chain := &fe.Chain
			n := chain.Len()
			for v := 0; v < n; v++ {
				if _, rerr := chain.Reconstruct(v); rerr != nil {
					problems = append(problems, fmt.Sprintf("%s:%s: version %d unreconstructable: %v", r.ID, path, v, rerr))
				}
			}
			if n > 0 {
				if cur, rerr := chain.Reconstruct(n - 1); rerr == nil && cur != chain.Current() {
					problems = append(problems, fmt.Sprintf("%s:%s: Current() disagrees with newest reconstructed version", r.ID, path))
				}
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("VerifyDedupConsistency: %d inconsistent tracked-file chain(s): %s",
			len(problems), strings.Join(problems, "; "))
	}
	return nil
}

// VerifyNoBudgetOverflow asserts the §3.1 render-time truncation contract held:
// after composition, each rendered working-set layer is within its Budget byte
// allocation. It re-composes via the harness's live session (h.Ops /
// h.State) — the same ComposeWorkingSet the turn drives — and compares each
// layer's byte length against the derived per-layer cap. workset.Compose
// truncates every layer to its cap, so a violation means a future change
// bypassed that truncation (the bypass this invariant exists to catch).
//
// Per D1 (SPEC §9.1): the X4 whole-request TOKEN ceiling is the NORMATIVE
// zero-overflow gate. This per-layer BYTE check is a cheap belt-and-suspenders
// on MOCK rungs; on a live-inference rung the token ceiling governs the
// assembled request and this deterministic byte check is not the load-bearing
// signal, so it is skipped there.
func VerifyNoBudgetOverflow(h *Harness) error {
	if h.State == nil || h.Ops == nil {
		// The substrate-only invariant harness has no live session to compose;
		// budget composition is only meaningful against a running turn.State.
		return nil
	}
	if h.liveClient != nil {
		return nil // live-inference rung — the X4 token ceiling is the gate (D1)
	}
	budget := h.State.Budget
	if budget.Total == 0 {
		budget = memops.DefaultBudget()
	}
	ws, err := h.Ops.ComposeWorkingSet(context.Background(), memops.WorksetInput{
		ActiveProject:  h.State.ActiveProject,
		ActiveThreads:  h.State.ActiveThreads,
		DormantThreads: h.State.DormantThreads,
		Budget:         budget,
	})
	if err != nil {
		return fmt.Errorf("VerifyNoBudgetOverflow: compose working set: %w", err)
	}
	layers := []struct {
		name        string
		got, budget int
	}{
		{"E", len(ws.LayerE), budget.LayerE},
		{"A1", len(ws.LayerA1), budget.LayerA1},
		{"A2", len(ws.LayerA2), budget.LayerA2},
		{"B", len(ws.LayerB), budget.LayerB},
		{"C", len(ws.LayerC), budget.LayerC},
	}
	var over []string
	for _, l := range layers {
		if l.got > l.budget {
			over = append(over, fmt.Sprintf("layer %s: %d > %d bytes", l.name, l.got, l.budget))
		}
	}
	if len(over) > 0 {
		return fmt.Errorf("VerifyNoBudgetOverflow: %s (§3.1 truncation contract bypassed)", strings.Join(over, "; "))
	}
	return nil
}

// skipPhaseErr is the sentinel error type used by phase-deferred
// invariants. The harness's invariant runner type-asserts against it
// to surface a t.Logf annotation rather than counting the result as a
// failure.
type skipPhaseErr struct{ Phase string }

func (e skipPhaseErr) Error() string { return "deferred to " + e.Phase }

func errSkipPhase(phase string) error { return skipPhaseErr{Phase: phase} }

// formatFindings renders verify Findings into a single line for error
// messages. Truncates at three findings; full detail lives in the
// metrics blob.
func formatFindings(fs []memops.VerifyFinding) string {
	if len(fs) == 0 {
		return ""
	}
	lim := len(fs)
	suffix := ""
	if lim > 3 {
		lim = 3
		suffix = fmt.Sprintf(" (+%d more)", len(fs)-3)
	}
	parts := make([]string, 0, lim)
	for i := 0; i < lim; i++ {
		f := fs[i]
		parts = append(parts, fmt.Sprintf("%s: %s", f.Path, f.Message))
	}
	return joinComma(parts) + suffix
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
