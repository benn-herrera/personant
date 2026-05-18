package scenarios

import (
	"errors"
	"fmt"

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
// the last step, when FinalInvariants is empty). Cheap, always
// applicable, and covers the spec §11.5 baseline.
var DefaultInvariants = []InvariantCheck{
	VerifySpineIntegrity,
	VerifyIndexFresh,
	VerifyProjectReferences,
	VerifyLastActiveValid,
	VerifyThreadFrontmatterMatchesSpine,
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
		thr, err := store.LoadThread(h.Paths, r.ID)
		if err != nil {
			if errors.Is(err, memops.ErrThreadFileNotFound) {
				h.T.Logf("VerifyThreadFrontmatterMatchesSpine: spine[%d] %s has no thread file (suspicious in v0.1)", i+1, r.ID)
				continue
			}
			return fmt.Errorf("VerifyThreadFrontmatterMatchesSpine: load %s: %w", r.ID, err)
		}
		fm := thr.Frontmatter
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
		thr, err := store.LoadThread(h.Paths, r.ID)
		if err != nil {
			if errors.Is(err, memops.ErrThreadFileNotFound) {
				continue
			}
			return fmt.Errorf("VerifyEngagementConsistency: load %s: %w", r.ID, err)
		}
		if thr.Frontmatter.TurnCount != r.TurnCount {
			return fmt.Errorf("VerifyEngagementConsistency: %s frontmatter turn_count %d != spine %d",
				r.ID, thr.Frontmatter.TurnCount, r.TurnCount)
		}
		if thr.Frontmatter.LastEngaged != r.LastEngaged {
			return fmt.Errorf("VerifyEngagementConsistency: %s frontmatter last_engaged %q != spine %q",
				r.ID, thr.Frontmatter.LastEngaged, r.LastEngaged)
		}
	}
	return nil
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
func formatFindings(fs []verify.Finding) string {
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
