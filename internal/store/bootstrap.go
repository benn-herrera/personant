package store

import (
	"errors"
	"fmt"
	"path/filepath"

	"personant/internal/memops"
)

// ResolveActiveProject runs the bootstrap waterfall described in spec
// §4.5.7. Pure resolution: the prompt branches surface as
// memops.BootstrapStep values; the caller (the chat REPL) implements the
// actual UI.
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
func ResolveActiveProject(paths PersonantPaths, hints memops.BootstrapHints) (memops.BootstrapResult, error) {
	if hints.ExplicitProject != "" {
		meta, err := resolveExplicit(paths, hints.ExplicitProject)
		if err != nil {
			return memops.BootstrapResult{}, err
		}
		if err := WriteLastActive(paths, meta.ID); err != nil {
			return memops.BootstrapResult{}, fmt.Errorf("bootstrap: write last-active: %w", err)
		}
		return memops.BootstrapResult{Step: memops.StepExplicit, Resolved: &meta}, nil
	}

	// Heuristic waterfall step 1: git remote match.
	if hints.CWD != "" {
		gitRoot, found, err := FindGitRoot(hints.CWD)
		if err != nil {
			return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
		}
		if found {
			origin, err := GetGitOriginURL(gitRoot)
			if err != nil {
				return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
			}
			if origin != "" {
				meta, ok, err := FindProjectByRemote(paths, origin)
				if err != nil {
					return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
				}
				if ok {
					if err := driftUpdatePath(paths, &meta, gitRoot); err != nil {
						return memops.BootstrapResult{}, err
					}
					if err := WriteLastActive(paths, meta.ID); err != nil {
						return memops.BootstrapResult{}, fmt.Errorf("bootstrap: write last-active: %w", err)
					}
					return memops.BootstrapResult{Step: memops.StepRemoteMatch, Resolved: &meta}, nil
				}
			}
		}

		// Step 2: CWD path match.
		meta, ok, err := FindProjectByPath(paths, hints.CWD)
		if err != nil {
			return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
		}
		if ok {
			if err := driftUpdatePath(paths, &meta, hints.CWD); err != nil {
				return memops.BootstrapResult{}, err
			}
			if err := WriteLastActive(paths, meta.ID); err != nil {
				return memops.BootstrapResult{}, fmt.Errorf("bootstrap: write last-active: %w", err)
			}
			return memops.BootstrapResult{Step: memops.StepPathMatch, Resolved: &meta}, nil
		}
	}

	// Step 3: last-active confirmation.
	last, err := ReadLastActive(paths)
	if err != nil {
		// A malformed last-active is not fatal — fall through to fallback so
		// the user can choose explicitly.
		return memops.BootstrapResult{Step: memops.StepNeedsFallback}, nil
	}
	if last != "" {
		meta, err := LoadProjectMeta(paths, last)
		if err == nil {
			return memops.BootstrapResult{Step: memops.StepNeedsConfirmation, Candidate: &meta}, nil
		}
		if !errors.Is(err, memops.ErrProjectNotFound) {
			return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
		}
		// last-active points at a project whose meta.json was deleted —
		// fall through to fallback.
	}

	return memops.BootstrapResult{Step: memops.StepNeedsFallback}, nil
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
