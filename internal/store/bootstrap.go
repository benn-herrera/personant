package store

import (
	"fmt"
	"path/filepath"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
)

// ResolveActiveProject resolves the active project from hints.Dir alone
// (spec §4.5.7): the project whose current_root_path is the directory
// (StepPathMatch), else a new project rooted there and named for the
// directory's base name (StepCreated, persisted here), else prj_default
// when no directory is given (StepDefault). Nothing inside the directory
// is read, so a moved or copied directory is a new project.
func ResolveActiveProject(paths PersonantPaths, hints memops.BootstrapHints) (memops.BootstrapResult, error) {
	if hints.Dir == "" {
		meta, err := LoadProjectMeta(paths, DefaultProjectID)
		if err != nil {
			return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
		}
		return memops.BootstrapResult{Step: memops.StepDefault, Project: meta}, nil
	}
	if !filepath.IsAbs(hints.Dir) {
		return memops.BootstrapResult{}, fmt.Errorf("bootstrap: project directory %q is not absolute", hints.Dir)
	}
	dir := filepath.Clean(hints.Dir)

	meta, ok, err := FindProjectByPath(paths, dir)
	if err != nil {
		return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
	}
	if ok {
		return memops.BootstrapResult{Step: memops.StepPathMatch, Project: meta}, nil
	}

	name := filepath.Base(dir)
	if !memops.ValidProjectName(name) {
		return memops.BootstrapResult{}, fmt.Errorf("bootstrap: directory base name %q is not a valid project name", name)
	}
	metas, err := ListProjects(paths)
	if err != nil {
		return memops.BootstrapResult{}, fmt.Errorf("bootstrap: %w", err)
	}
	now := clock.Timeline().UTC().Format(time.RFC3339)
	meta = memops.ProjectMeta{
		ID:               NextProjectID(metas),
		Name:             name,
		CurrentRootPath:  dir,
		Created:          now,
		LastActive:       now,
		ConventionsPaths: []string{},
		SymbolPatterns:   []memops.ProjectPattern{},
		IgnoreSymbols:    []string{},
	}
	if err := SaveProjectMeta(paths, meta); err != nil {
		return memops.BootstrapResult{}, fmt.Errorf("bootstrap: create project: %w", err)
	}
	return memops.BootstrapResult{Step: memops.StepCreated, Project: meta}, nil
}
