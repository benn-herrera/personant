// workset_compose.go is the substrate-fetch half of MemoryOps.ComposeWorkingSet.
// It loads everything workset.Compose needs from the file substrate
// (spine, project metas, digests, directive markdown, conventions
// files, thread frontmatters + bodies, tracked-file sidecars + their
// pre-computed live windows) into a substrate-free workset.Inputs, then
// hands off to workset.Compose for pure rendering.
//
// Per-substrate-source failures (a missing digest, an unreadable thread)
// are non-fatal: the affected layer contribution is silently omitted on
// the workset side and a workset.warning event-log line is emitted here
// so the loss is observable. This mirrors the C3 pattern for recall:
// substrate I/O + its warnings live in the adapter; the application
// package consumes pre-fetched plain types.

package fileadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"personant/internal/dedup"
	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/workset"
)

// ComposeWorkingSet builds the layer-by-layer working-set content for
// one turn. Non-fatal substrate-fetch warnings are emitted to the event
// log as workset.warning lines (the substrate's canonical destination);
// any caller-supplied a.Logger is also notified.
func (a *FileAdapter) ComposeWorkingSet(ctx context.Context, in memops.WorksetInput) (memops.WorksetLayers, error) {
	if err := ctx.Err(); err != nil {
		return memops.WorksetLayers{}, err
	}
	logf := func(format string, args ...any) {
		_ = a.Log(ctx, "workset", "warning", sanitizeWorksetDetail(fmt.Sprintf(format, args...)))
		if a.Logger != nil {
			a.Logger(format, args...)
		}
	}

	budget := in.Budget
	if budget.Total == 0 {
		budget = memops.DefaultBudget()
	}

	inputs := workset.Inputs{
		ActiveProject:      in.ActiveProject,
		ActiveThreads:      in.ActiveThreads,
		DormantThreads:     in.DormantThreads,
		Budget:             budget,
		Directives:         a.loadDirectiveSections(in.ActiveProject, logf),
		Conventions:        a.loadConventions(in.ActiveProject, logf),
		ActiveProjectSpine: a.loadActiveSpine(in.ActiveProject.ID, logf),
		OtherProjects:      a.loadOtherProjectDigests(in.ActiveProject.ID, logf),
		ActiveThreadData:   a.loadActiveThreadData(in.ActiveThreads, budget, logf),
		DormantSpine:       a.loadDormantSpine(in.DormantThreads, logf),
	}

	params, err := workset.Compose(inputs, workset.ComposeOptions{Logger: logf})
	if err != nil {
		return memops.WorksetLayers{}, fmt.Errorf("fileadapter: compose working set: %w", err)
	}
	return memops.WorksetLayers{
		LayerE:  params.LayerE,
		LayerA1: params.LayerA1,
		LayerA2: params.LayerA2,
		LayerB:  params.LayerB,
		LayerC:  params.LayerC,
	}, nil
}

// ---------- Layer A1 fetch ----------

// loadActiveSpine returns the current project's spine records. A read
// failure logs a warning and yields a nil slice; workset renders Layer
// A1 empty in that case.
func (a *FileAdapter) loadActiveSpine(projectID string, logf func(string, ...any)) []memops.SpineRecord {
	records, err := store.SpineRecordsByProject(a.paths, projectID)
	if err != nil {
		logf("workset: layer A1: load spine for %s: %v", projectID, err)
		return nil
	}
	return records
}

// ---------- Layer E fetch ----------

// directive filenames are stable across the codebase.
const (
	directiveDefaultsFile = "defaults.md"
	directiveUserFile     = "user.md"
	directiveProjectFile  = "project.md"
)

// loadDirectiveSections returns the Layer E directive bodies in the
// canonical render order (defaults → user → project). Each source is
// optional: a missing file contributes nothing and is not warned
// about; a present-but-unreadable file is warned.
func (a *FileAdapter) loadDirectiveSections(active memops.ProjectMeta, logf func(string, ...any)) []workset.DirectiveSection {
	sources := []struct {
		header string
		path   string
	}{
		{"defaults", filepath.Join(a.paths.DirectivesDir, directiveDefaultsFile)},
		{"user", filepath.Join(a.paths.DirectivesDir, directiveUserFile)},
		{
			"project: " + active.Name,
			filepath.Join(a.paths.DirectivesDir, active.ID, directiveProjectFile),
		},
	}
	out := make([]workset.DirectiveSection, 0, len(sources))
	for _, src := range sources {
		body, err := readDirectiveBody(src.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			logf("workset: layer E: read %s: %v", src.path, err)
			continue
		}
		if body == "" {
			continue
		}
		out = append(out, workset.DirectiveSection{Header: src.header, Body: body})
	}
	return out
}

// loadConventions reads each ConventionsPaths entry into a
// ConventionFile. An unreadable file is warned and skipped.
func (a *FileAdapter) loadConventions(active memops.ProjectMeta, logf func(string, ...any)) []workset.ConventionFile {
	if len(active.ConventionsPaths) == 0 {
		return nil
	}
	out := make([]workset.ConventionFile, 0, len(active.ConventionsPaths))
	for _, p := range active.ConventionsPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			logf("workset: layer E: conventions %s: %v", p, err)
			continue
		}
		out = append(out, workset.ConventionFile{Path: p, Content: string(data)})
	}
	return out
}

// readDirectiveBody loads a directive markdown file and returns its
// body with any YAML frontmatter (between `---` lines at file head)
// stripped. A file with no frontmatter returns its full content.
//
// Errors propagate; os.IsNotExist is the caller's signal for an
// optional file that simply doesn't exist yet.
func readDirectiveBody(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return stripFrontmatter(string(data)), nil
}

// stripFrontmatter returns content with a leading `---\n...\n---\n`
// block removed. If no opening delimiter is found on the first line
// (after optional whitespace), content is returned unchanged.
//
// The closing delimiter must be matched against an entire line — an
// embedded `---` inside a code block in the body therefore stays put.
func stripFrontmatter(content string) string {
	// Tolerate leading whitespace.
	trimmed := trimLeftWS(content)
	if !hasPrefix(trimmed, "---") {
		return content
	}
	rest := trimmed[len("---"):]
	// The opening delimiter line must end at a newline (not a `---X`).
	if !hasPrefix(rest, "\n") && !hasPrefix(rest, "\r\n") && rest != "" {
		return content
	}
	// Skip the rest of the opening delimiter line.
	nl := indexByte(rest, '\n')
	if nl < 0 {
		return content
	}
	rest = rest[nl+1:]
	for {
		nl := indexByte(rest, '\n')
		var line string
		if nl < 0 {
			line = rest
		} else {
			line = rest[:nl]
		}
		if trimRightWS(line) == "---" {
			if nl < 0 {
				return ""
			}
			body := rest[nl+1:]
			// One conventional blank line after the closing delimiter is
			// stripped; everything else preserved verbatim.
			if len(body) > 0 && body[0] == '\n' {
				body = body[1:]
			}
			return body
		}
		if nl < 0 {
			// No closing delimiter — treat the input as having no
			// frontmatter rather than swallowing the whole file.
			return content
		}
		rest = rest[nl+1:]
	}
}

// minimal byte helpers — avoids dragging in strings for the hot path
// while keeping the helper local to the substrate-format code.
func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
func trimLeftWS(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return s[i:]
}
func trimRightWS(s string) string {
	j := len(s)
	for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[:j]
}

// ---------- Layer A2 fetch ----------

// loadOtherProjectDigests returns the digest entries for every project
// except the active one and the default escape-hatch. A missing digest
// file is silent (the project simply has no A2 line); a present-but-
// malformed digest is warned and skipped.
func (a *FileAdapter) loadOtherProjectDigests(activeID string, logf func(string, ...any)) []workset.ProjectDigestEntry {
	metas, err := store.ListProjects(a.paths)
	if err != nil {
		logf("workset: layer A2: list projects: %v", err)
		return nil
	}
	out := make([]workset.ProjectDigestEntry, 0, len(metas))
	for _, m := range metas {
		if m.ID == activeID || m.ID == memops.DefaultProjectID {
			continue
		}
		digest, ok := loadProjectDigest(a.paths, m.ID, logf)
		if !ok {
			continue
		}
		out = append(out, workset.ProjectDigestEntry{Meta: m, Digest: digest})
	}
	return out
}

// loadProjectDigest reads <Home>/projects/<id>/digest.json. A missing
// digest is a warning, not an error — the project simply contributes
// no A2 line. ok=false signals "skip this project."
func loadProjectDigest(paths store.PersonantPaths, id string, logf func(string, ...any)) (memops.ProjectDigest, bool) {
	digestPath := filepath.Join(paths.ProjectsDir, id, "digest.json")
	data, err := os.ReadFile(digestPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logf("workset: layer A2: read %s: %v", digestPath, err)
		}
		return memops.ProjectDigest{}, false
	}
	var d memops.ProjectDigest
	if err := json.Unmarshal(data, &d); err != nil {
		logf("workset: layer A2: parse %s: %v", digestPath, err)
		return memops.ProjectDigest{}, false
	}
	return d, true
}

// ---------- Layer B fetch ----------

// loadActiveThreadData loads frontmatter + body + tracked-file windows
// for each active thread up to BTopK. The returned map is keyed by id;
// threads that fail to load are absent (workset skips them silently —
// the warning is the adapter's responsibility, emitted here).
//
// Per-thread byte budget mirrors workset's per-thread share so the
// ReadThreadBody read is bounded: only as many recent turn excerpts as
// fit in the share are loaded.
func (a *FileAdapter) loadActiveThreadData(ids []string, budget memops.Budget, logf func(string, ...any)) map[string]workset.ThreadData {
	if len(ids) == 0 || budget.LayerB <= 0 {
		return nil
	}
	limit := len(ids)
	if budget.BTopK > 0 && limit > budget.BTopK {
		limit = budget.BTopK
	}
	perThread := workset.PerThreadBudget(budget.LayerB, limit)
	out := make(map[string]workset.ThreadData, limit)
	for i := 0; i < limit; i++ {
		id := ids[i]
		data, ok := a.loadOneThreadData(id, perThread, logf)
		if !ok {
			continue
		}
		out[id] = data
	}
	return out
}

// loadOneThreadData fetches frontmatter, body (byte-budget-bounded),
// and tracked-file windows for one thread. A first-error wins
// short-circuits: a missing thread.md yields ok=false; everything
// after the frontmatter is tolerate-and-continue (a corrupt sidecar
// is warned and rendered without the tracked-files section).
func (a *FileAdapter) loadOneThreadData(id string, perThread int, logf func(string, ...any)) (workset.ThreadData, bool) {
	fm, err := store.LoadThreadFrontmatter(a.paths, id)
	if err != nil {
		logf("workset: layer B: %s: load frontmatter: %v", id, err)
		return workset.ThreadData{}, false
	}
	body, err := store.ReadThreadBody(a.paths, id, perThread)
	if err != nil {
		logf("workset: layer B: %s: read body: %v", id, err)
		return workset.ThreadData{}, false
	}
	return workset.ThreadData{
		Frontmatter:  fm,
		Body:         body,
		TrackedFiles: a.loadTrackedFiles(id, logf),
	}, true
}

// loadTrackedFiles loads the §3.9.2 sidecar and pre-computes each
// entry's live-diff window. A missing sidecar is the empty-file case
// (no warning, no section); a corrupt sidecar is warned and the
// section is omitted entirely; a live-window failure on a single
// entry is warned and that entry is skipped.
func (a *FileAdapter) loadTrackedFiles(id string, logf func(string, ...any)) []workset.TrackedFile {
	tf, err := store.LoadThreadFiles(a.paths, id)
	if err != nil {
		logf("workset: layer B: %s: load tracked files: %v", id, err)
		return nil
	}
	if len(tf.Files) == 0 {
		return nil
	}
	paths := make([]string, 0, len(tf.Files))
	for p := range tf.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]workset.TrackedFile, 0, len(paths))
	for _, p := range paths {
		entry := tf.Files[p]
		window, err := entry.Chain.LiveWindow(dedup.LiveDiffWindow)
		if err != nil {
			logf("workset: layer B: %s: tracked file %s: %v", id, p, err)
			continue
		}
		out = append(out, workset.TrackedFile{
			Path:       entry.Path,
			LastCommit: entry.LastCommit,
			Window:     window,
		})
	}
	return out
}

// ---------- Layer C fetch ----------

// loadDormantSpine looks up each dormant thread's spine record. A
// missing record is warned and the id is absent from the returned map
// (workset skips it).
func (a *FileAdapter) loadDormantSpine(ids []string, logf func(string, ...any)) map[string]memops.SpineRecord {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]memops.SpineRecord, len(ids))
	for _, id := range ids {
		rec, found, err := store.FindSpineRecord(a.paths, id)
		if err != nil {
			logf("workset: layer C: find %s: %v", id, err)
			continue
		}
		if !found {
			logf("workset: layer C: %s: not in spine", id)
			continue
		}
		out[id] = rec
	}
	return out
}

// sanitizeWorksetDetail strips newlines and tabs from a workset warning
// before it lands in a one-per-line event-log entry. Local twin of
// internal/turn.sanitizeDetail so this package keeps its narrow import
// graph (no cross-import into the turn-loop side of the application).
func sanitizeWorksetDetail(s string) string {
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\t' {
			r = append(r, ' ')
			continue
		}
		r = append(r, c)
	}
	return string(r)
}
