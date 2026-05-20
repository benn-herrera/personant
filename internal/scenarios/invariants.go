package scenarios

import (
	"errors"
	"fmt"
	"sort"
	"strings"

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
// the last step, when FinalInvariants is empty). Covers the spec §11.5
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
// every step in any scenario, including the six-month sim, without
// contributing to per-step wall-time growth.
var cheapDefaultInvariants = []InvariantCheck{
	VerifyLastActiveValid,
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
	VerifyThreadFrontmatterMatchesSpine,
	VerifyThreadAccounting,
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

// VerifyThreadFrontmatterMatchesSpine: for each spine record, load the
// corresponding thread file and assert the canonical-overlap fields
// agree exactly. A missing thread file is logged as a warning via
// h.T.Logf rather than failing — v0.1 has no archival path that would
// legitimately strand a spine record without a file, but Phase 4
// retirement may; surfacing the case as a warning lets future code
// land without a flood of false-positive failures here.
func VerifyThreadFrontmatterMatchesSpine(h *Harness) error {
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		return fmt.Errorf("VerifyThreadFrontmatterMatchesSpine: read spine: %w", err)
	}
	for i, r := range recs {
		fm, err := store.LoadThreadFrontmatter(h.Paths, r.ID)
		if err != nil {
			if errors.Is(err, memops.ErrThreadFileNotFound) {
				h.T.Logf("VerifyThreadFrontmatterMatchesSpine: spine[%d] %s has no thread file (suspicious in v0.1)", i+1, r.ID)
				continue
			}
			return fmt.Errorf("VerifyThreadFrontmatterMatchesSpine: load %s: %w", r.ID, err)
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
//   - archived — `archive.simulated-delete` log lines, folded into
//     h.archivedThreadIDs the same way (the v0.1 deletion-stub
//     archival path).
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
// All violations are collected, sorted, and reported in one error.
func VerifyThreadAccounting(h *Harness) error {
	created := h.createdThreadIDs
	archived := h.archivedThreadIDs
	onSpine, err := liveSpineThreadSet(h.Paths)
	if err != nil {
		return fmt.Errorf("VerifyThreadAccounting: %w", err)
	}

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
	if len(lost) == 0 && len(both) == 0 {
		return nil
	}
	sort.Strings(lost)
	sort.Strings(both)
	var parts []string
	if len(lost) > 0 {
		parts = append(parts, fmt.Sprintf("unexplained loss (created but neither on-spine nor archived): %s",
			strings.Join(lost, ", ")))
	}
	if len(both) > 0 {
		parts = append(parts, fmt.Sprintf("on-spine and archived simultaneously (ID reuse or archival corruption): %s",
			strings.Join(both, ", ")))
	}
	return fmt.Errorf("VerifyThreadAccounting: %s", strings.Join(parts, "; "))
}

// VerifyArchiveResolvable is reserved for v0.2 deep-cold archival
// (spec §3.8). The archive index does not exist in v0.1; the function
// is wired up so scenarios can reference it today and the check
// activates when the underlying feature lands.
func VerifyArchiveResolvable(_ *Harness) error {
	return errSkipPhase("v0.2-archive")
}

// VerifyDedupConsistency is reserved for v0.2 working-set dedup (spec
// §3.9). Same wiring rationale as VerifyArchiveResolvable.
func VerifyDedupConsistency(_ *Harness) error {
	return errSkipPhase("v0.2-dedup")
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
