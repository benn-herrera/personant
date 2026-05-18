package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"personant/internal/memops"
)

// DefaultProjectID is the reserved id for the no-project escape hatch
// described in spec §2.1 / §2.5.1.
const DefaultProjectID = "prj_default"

// LoadProjectMeta reads projects/<id>/meta.json and returns the parsed
// metadata.
//
// The prj_default project is special: if its meta.json is absent on disk,
// LoadProjectMeta returns a synthetic memops.ProjectMeta with Name: "default" and
// empty path/remote fields. This mirrors §2.5.1's reservation: prj_default
// is always available even when no file has been written for it.
//
// For any other project, a missing directory or meta.json yields
// memops.ErrProjectNotFound (wrapped with the project id in the message).
func LoadProjectMeta(paths PersonantPaths, projectID string) (memops.ProjectMeta, error) {
	metaPath := filepath.Join(paths.ProjectsDir, projectID, "meta.json")
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
	metaPath := filepath.Join(dir, "meta.json")

	tmp, err := os.CreateTemp(dir, ".meta-*.tmp")
	if err != nil {
		return fmt.Errorf("save project %s: create temp: %w", meta.ID, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	w := bufio.NewWriter(tmp)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		tmp.Close()
		return fmt.Errorf("save project %s: encode: %w", meta.ID, err)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("save project %s: flush: %w", meta.ID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("save project %s: fsync: %w", meta.ID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save project %s: close temp: %w", meta.ID, err)
	}
	if err := os.Rename(tmpPath, metaPath); err != nil {
		return fmt.Errorf("save project %s: rename: %w", meta.ID, err)
	}
	cleanup = false
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
		metaPath := filepath.Join(paths.ProjectsDir, id, "meta.json")
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

// FindProjectByRemote returns the project whose remote_urls or
// historical_remote_urls contains the given URL after both are normalized
// per §2.5.1. The input is normalized inside this function; callers do not
// have to pre-normalize, though they may.
//
// Returns (zero, false, nil) if no match is found. Errors only on i/o
// failures during the project listing.
func FindProjectByRemote(paths PersonantPaths, remoteURL string) (memops.ProjectMeta, bool, error) {
	target := NormalizeRemoteURL(remoteURL)
	if target == "" {
		return memops.ProjectMeta{}, false, nil
	}
	metas, err := ListProjects(paths)
	if err != nil {
		return memops.ProjectMeta{}, false, err
	}
	for _, m := range metas {
		for _, u := range m.RemoteURLs {
			if NormalizeRemoteURL(u) == target {
				return m, true, nil
			}
		}
		for _, u := range m.HistoricalRemoteURLs {
			if NormalizeRemoteURL(u) == target {
				return m, true, nil
			}
		}
	}
	return memops.ProjectMeta{}, false, nil
}

// FindProjectByPath returns the project whose CurrentRootPath or
// HistoricalRootPaths contains absPath. Path comparison is byte-exact
// after filepath.Clean — symlink resolution is the caller's responsibility.
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
		for _, p := range m.HistoricalRootPaths {
			if filepath.Clean(p) == target {
				return m, true, nil
			}
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

// NormalizeRemoteURL canonicalizes a git remote URL per §2.5.1:
//   - host is lowercased
//   - trailing ".git" stripped
//   - "git@host:owner/name" rewritten to "https://host/owner/name"
//
// Returns "" for input that is empty, has no host, or cannot be parsed.
// Callers may treat "" as "unidentifiable" rather than an error.
func NormalizeRemoteURL(rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return ""
	}

	// SCP-style SSH: user@host:path. The colon-separator distinguishes this
	// from a real URL like "ssh://user@host/path"; net/url cannot parse it.
	if host, path, ok := splitSCPSSH(raw); ok {
		host = strings.ToLower(host)
		path = strings.TrimPrefix(path, "/")
		path = strings.TrimSuffix(path, ".git")
		if host == "" || path == "" {
			return ""
		}
		return "https://" + host + "/" + path
	}

	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		// "github.com/foo/bar" without scheme — unparseable as a remote URL
		// for our purposes (no host can be identified deterministically).
		return ""
	}
	host := strings.ToLower(u.Host)
	path := strings.TrimSuffix(u.Path, ".git")
	if path == "" || path == "/" {
		return ""
	}
	// SSH and Git protocol normalize to the canonical HTTPS form so that
	// "git@host:owner/name", "ssh://git@host/owner/name", and
	// "https://host/owner/name" collide on lookup.
	if scheme == "ssh" || scheme == "git" {
		scheme = "https"
		// ssh URLs may carry a userinfo (git@) we do not want in the
		// canonical form.
		return scheme + "://" + host + path
	}
	return scheme + "://" + host + path
}

// splitSCPSSH detects the "user@host:path" SCP-style git URL form and
// returns its host and path components. Returns ok=false for inputs that
// are not in this form (notably anything with "://", or that lacks a colon
// before the first slash).
func splitSCPSSH(raw string) (host, path string, ok bool) {
	if strings.Contains(raw, "://") {
		return "", "", false
	}
	colon := strings.Index(raw, ":")
	if colon < 0 {
		return "", "", false
	}
	if firstSlash := strings.Index(raw, "/"); firstSlash >= 0 && firstSlash < colon {
		return "", "", false
	}
	hostPart := raw[:colon]
	pathPart := raw[colon+1:]
	if at := strings.LastIndex(hostPart, "@"); at >= 0 {
		hostPart = hostPart[at+1:]
	}
	if hostPart == "" || pathPart == "" {
		return "", "", false
	}
	return hostPart, pathPart, true
}
