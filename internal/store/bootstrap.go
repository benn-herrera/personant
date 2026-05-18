package store

import (
	"errors"
	"fmt"
	"path/filepath"

	"personant/internal/memops"
)

// BootstrapStep identifies which branch of the waterfall produced a result.
// The numeric value is for log/debug output; callers should switch on the
// constants, not the integers.
type BootstrapStep int

const (
	StepUnset             BootstrapStep = iota
	StepExplicit                        // 1: --project flag matched a known project
	StepRemoteMatch                     // 2: CWD git remote → known project
	StepPathMatch                       // 3: CWD path → known project
	StepNeedsConfirmation               // 4: last-active prompt to user
	StepNeedsFallback                   // 5: caller offers create/switch/none
)

// String returns a human-readable form of the step (for log lines, not
// user-facing UI).
func (s BootstrapStep) String() string {
	switch s {
	case StepExplicit:
		return "explicit"
	case StepRemoteMatch:
		return "remote-match"
	case StepPathMatch:
		return "path-match"
	case StepNeedsConfirmation:
		return "needs-confirmation"
	case StepNeedsFallback:
		return "needs-fallback"
	default:
		return "unset"
	}
}

// BootstrapOptions controls ResolveActiveProject.
type BootstrapOptions struct {
	// ExplicitProject, if non-empty, short-circuits the waterfall. The
	// resolver tries to interpret it first as a prj_<n> id, then as a
	// memops.ProjectMeta.Name. Returns memops.ErrProjectNotFound on no match.
	ExplicitProject string

	// CWD is the directory used as the basis for the heuristic waterfall
	// (steps 1 and 2). Callers typically pass os.Getwd(); the parameter
	// is explicit so tests can pin a fixture path.
	CWD string
}

// BootstrapResult conveys the outcome of the waterfall. Exactly one of:
//   - Resolved is non-nil (waterfall succeeded; last-active and meta drift
//     have been persisted).
//   - Step == StepNeedsConfirmation and Candidate is non-nil (caller asks
//     the user about resuming the last-active project).
//   - Step == StepNeedsFallback (caller offers create/switch/no-project).
type BootstrapResult struct {
	Step      BootstrapStep
	Resolved  *memops.ProjectMeta
	Candidate *memops.ProjectMeta
}

// ResolveActiveProject runs the bootstrap waterfall described in spec
// §4.5.7. Pure resolution: the prompt branches surface as Step values; the
// caller (the chat REPL) implements the actual UI.
//
// On Resolved (StepExplicit / StepRemoteMatch / StepPathMatch),
// ResolveActiveProject persists drift updates to the project's meta and
// writes <Home>/last-active. On the NeedsConfirmation / NeedsFallback
// branches it does not write anything; the caller calls SaveProjectMeta and
// WriteLastActive when the user confirms.
//
// The explicit-project branch deliberately does not perform path
// drift-update — the user's --project flag specifies the project, not the
// path, so silently rewriting current_root_path on top of an explicit
// override would be surprising.
func ResolveActiveProject(paths PersonantPaths, opts BootstrapOptions) (BootstrapResult, error) {
	if opts.ExplicitProject != "" {
		meta, err := resolveExplicit(paths, opts.ExplicitProject)
		if err != nil {
			return BootstrapResult{}, err
		}
		if err := WriteLastActive(paths, meta.ID); err != nil {
			return BootstrapResult{}, fmt.Errorf("bootstrap: write last-active: %w", err)
		}
		return BootstrapResult{Step: StepExplicit, Resolved: &meta}, nil
	}

	// Heuristic waterfall step 1: git remote match.
	if opts.CWD != "" {
		gitRoot, found, err := FindGitRoot(opts.CWD)
		if err != nil {
			return BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
		}
		if found {
			origin, err := GetGitOriginURL(gitRoot)
			if err != nil {
				return BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
			}
			if origin != "" {
				meta, ok, err := FindProjectByRemote(paths, origin)
				if err != nil {
					return BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
				}
				if ok {
					if err := driftUpdatePath(paths, &meta, gitRoot); err != nil {
						return BootstrapResult{}, err
					}
					if err := WriteLastActive(paths, meta.ID); err != nil {
						return BootstrapResult{}, fmt.Errorf("bootstrap: write last-active: %w", err)
					}
					return BootstrapResult{Step: StepRemoteMatch, Resolved: &meta}, nil
				}
			}
		}

		// Step 2: CWD path match.
		meta, ok, err := FindProjectByPath(paths, opts.CWD)
		if err != nil {
			return BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
		}
		if ok {
			if err := driftUpdatePath(paths, &meta, opts.CWD); err != nil {
				return BootstrapResult{}, err
			}
			if err := WriteLastActive(paths, meta.ID); err != nil {
				return BootstrapResult{}, fmt.Errorf("bootstrap: write last-active: %w", err)
			}
			return BootstrapResult{Step: StepPathMatch, Resolved: &meta}, nil
		}
	}

	// Step 3: last-active confirmation.
	last, err := ReadLastActive(paths)
	if err != nil {
		// A malformed last-active is not fatal — fall through to fallback so
		// the user can choose explicitly.
		return BootstrapResult{Step: StepNeedsFallback}, nil
	}
	if last != "" {
		meta, err := LoadProjectMeta(paths, last)
		if err == nil {
			return BootstrapResult{Step: StepNeedsConfirmation, Candidate: &meta}, nil
		}
		if !errors.Is(err, memops.ErrProjectNotFound) {
			return BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
		}
		// last-active points at a project whose meta.json was deleted —
		// fall through to fallback.
	}

	return BootstrapResult{Step: StepNeedsFallback}, nil
}

// resolveExplicit looks up a project either by id (matching ProjectIDPattern
// or DefaultProjectID) or by Name. Returns memops.ErrProjectNotFound when neither
// lookup hits.
func resolveExplicit(paths PersonantPaths, ref string) (memops.ProjectMeta, error) {
	// Try as id first. ProjectIDPattern matches prj_<n>; DefaultProjectID
	// is the documented sentinel.
	if ref == DefaultProjectID || memops.ProjectIDPattern.MatchString(ref) {
		meta, err := LoadProjectMeta(paths, ref)
		if err == nil {
			return meta, nil
		}
		if !errors.Is(err, memops.ErrProjectNotFound) {
			return memops.ProjectMeta{}, fmt.Errorf("bootstrap: %w", err)
		}
		// Fall through and try by name — an "id-shaped" string that does not
		// resolve as an id is, on this machine, just a plausible name.
	}
	metas, err := ListProjects(paths)
	if err != nil {
		return memops.ProjectMeta{}, fmt.Errorf("bootstrap: %w", err)
	}
	for _, m := range metas {
		if m.Name == ref {
			return m, nil
		}
	}
	return memops.ProjectMeta{}, fmt.Errorf("bootstrap: project %q: %w", ref, memops.ErrProjectNotFound)
}

// driftUpdatePath rewrites meta.CurrentRootPath to newPath if the two
// differ (after filepath.Clean). The previous CurrentRootPath, if non-empty
// and not already in HistoricalRootPaths, is appended to history. The
// updated meta is persisted via SaveProjectMeta.
//
// No-op when the paths already agree (after clean); in that case meta is
// unchanged and not rewritten.
func driftUpdatePath(paths PersonantPaths, meta *memops.ProjectMeta, newPath string) error {
	if newPath == "" {
		return nil
	}
	clean := filepath.Clean(newPath)
	current := ""
	if meta.CurrentRootPath != "" {
		current = filepath.Clean(meta.CurrentRootPath)
	}
	if clean == current {
		return nil
	}
	if current != "" && !containsPath(meta.HistoricalRootPaths, current) {
		meta.HistoricalRootPaths = append(meta.HistoricalRootPaths, current)
	}
	meta.CurrentRootPath = clean
	if err := SaveProjectMeta(paths, *meta); err != nil {
		return fmt.Errorf("bootstrap: drift-update %s: %w", meta.ID, err)
	}
	return nil
}

func containsPath(haystack []string, needle string) bool {
	for _, p := range haystack {
		if filepath.Clean(p) == needle {
			return true
		}
	}
	return false
}
