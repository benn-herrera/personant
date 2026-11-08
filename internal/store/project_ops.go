package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"personant/internal/memops"
)

// DefaultProjectID re-exports memops.DefaultProjectID so substrate code
// that already imports store can keep using it without an extra import.
// The canonical definition lives in package memops (the domain layer).
const DefaultProjectID = memops.DefaultProjectID

// ProjectMetaFileName is the per-project metadata filename inside each
// projects/<id>/ directory. The full path is constructed via
// filepath.Join(paths.ProjectsDir, projectID, ProjectMetaFileName).
const ProjectMetaFileName = "meta.json"

// LoadProjectMeta reads projects/<id>/meta.json and returns the parsed
// metadata.
//
// The prj_default project is special: if its meta.json is absent on disk,
// LoadProjectMeta returns a synthetic memops.ProjectMeta with Name: "default" and
// an empty path. This mirrors §2.5.1's reservation: prj_default
// is always available even when no file has been written for it.
//
// For any other project, a missing directory or meta.json yields
// memops.ErrProjectNotFound (wrapped with the project id in the message).
func LoadProjectMeta(paths PersonantPaths, projectID string) (memops.ProjectMeta, error) {
	metaPath := filepath.Join(paths.ProjectsDir, projectID, ProjectMetaFileName)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if projectID == DefaultProjectID {
				return memops.ProjectMeta{ID: DefaultProjectID, Name: "default"}, nil
			}
			return memops.ProjectMeta{}, fmt.Errorf("load project %s: %w", projectID, memops.ErrProjectNotFound)
		}
		return memops.ProjectMeta{}, fmt.Errorf("load project %s: %w", projectID, err)
	}
	var meta memops.ProjectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return memops.ProjectMeta{}, fmt.Errorf("load project %s: parse %s: %w", projectID, metaPath, err)
	}
	return meta, nil
}

// SaveProjectMeta writes projects/<id>/meta.json. Creates the project
// directory if it does not exist. Atomic: encode to a temp file in the
// project directory, fsync, rename.
func SaveProjectMeta(paths PersonantPaths, meta memops.ProjectMeta) error {
	if meta.ID == "" {
		return errors.New("save project meta: ID is empty")
	}
	dir := filepath.Join(paths.ProjectsDir, meta.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save project %s: mkdir: %w", meta.ID, err)
	}
	metaPath := filepath.Join(dir, ProjectMetaFileName)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		return fmt.Errorf("save project %s: encode: %w", meta.ID, err)
	}
	if err := WriteFileAtomic(metaPath, buf.Bytes()); err != nil {
		return fmt.Errorf("save project %s: %w", meta.ID, err)
	}
	return nil
}

// ListProjects returns every known memops.ProjectMeta on disk, sorted by id.
//
// Walks paths.ProjectsDir for prj_<n> subdirectories with a present
// meta.json. The prj_default entry is included only if its meta.json is
// present (per §4.5.7 the default project is reserved by convention but
// has no on-disk record until something writes one). Subdirectories whose
// names do not match ProjectIDPattern (and that are not prj_default) are
// skipped silently.
func ListProjects(paths PersonantPaths) ([]memops.ProjectMeta, error) {
	entries, err := os.ReadDir(paths.ProjectsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list projects: read %s: %w", paths.ProjectsDir, err)
	}
	out := make([]memops.ProjectMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if id != DefaultProjectID && !memops.ProjectIDPattern.MatchString(id) {
			continue
		}
		metaPath := filepath.Join(paths.ProjectsDir, id, ProjectMetaFileName)
		if _, err := os.Stat(metaPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Directory exists but no meta.json yet — skip silently. In
				// particular this is the documented behavior for prj_default.
				continue
			}
			return nil, fmt.Errorf("list projects: stat %s: %w", metaPath, err)
		}
		meta, err := LoadProjectMeta(paths, id)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		out = append(out, meta)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// FindProjectByPath returns the project whose CurrentRootPath is absPath.
// Path comparison is byte-exact after filepath.Clean — symlink resolution
// is the caller's responsibility.
//
// Returns (zero, false, nil) if no match is found.
func FindProjectByPath(paths PersonantPaths, absPath string) (memops.ProjectMeta, bool, error) {
	if absPath == "" {
		return memops.ProjectMeta{}, false, nil
	}
	target := filepath.Clean(absPath)
	metas, err := ListProjects(paths)
	if err != nil {
		return memops.ProjectMeta{}, false, err
	}
	for _, m := range metas {
		if m.CurrentRootPath != "" && filepath.Clean(m.CurrentRootPath) == target {
			return m, true, nil
		}
	}
	return memops.ProjectMeta{}, false, nil
}

// NextProjectID returns the next available prj_<n> id given the current set
// of memops.ProjectMeta records — max(existing n) + 1, or "prj_1" if metas is
// empty. The reserved prj_default id is ignored when computing the maximum.
func NextProjectID(metas []memops.ProjectMeta) string {
	max := 0
	for _, m := range metas {
		n, ok := parseSerialID(m.ID, "prj_")
		if !ok {
			continue
		}
		if n > max {
			max = n
		}
	}
	return "prj_" + strconv.Itoa(max+1)
}
