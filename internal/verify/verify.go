// Package verify implements `personant verify` — a read-only structural
// validation pass over the canonical state in $PERSONANT_HOME (spec
// §2.1, §2.2, §2.5.1). It checks SpineRecord and ProjectMeta hard
// limits, cross-references between them, and delegates derived-file
// drift detection to internal/index.Check.
//
// Verify is non-mutating: it never writes a file, never invokes git,
// and never touches the LLM. Findings are returned as a Report; the
// caller (cmd/verify.go) decides on exit code and output formatting.
package verify

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"personant/internal/index"
	"personant/internal/store"
)

// Finding is one schema/constraint violation. Path locates the file or
// record (e.g. "spine.jsonl[42]" or "projects/prj_3/meta.json"); Field
// names the offending field; Message is a human-readable explanation.
type Finding struct {
	Path    string
	Field   string
	Message string
}

// Report aggregates the result of a Verify run.
//
//   - Errors:   schema/constraint violations (cause non-zero exit)
//   - Warnings: recoverable issues (missing meta.json, malformed
//     directives) — informational, do not affect exit code
//   - Drift:    pass-through descriptions from index.Check; treated as
//     errors for the purpose of the exit code. A derived file out of
//     sync with canonical state is a hard failure — autogit's
//     CheckDerivedFresh post-flag enforces the same invariant at every
//     state-changing git op in the home tree.
type Report struct {
	Errors   []Finding
	Warnings []Finding
	Drift    []string
}

// HasErrors reports whether the run found any error-grade issues
// (schema violations or index drift). Warnings alone do not flip this.
func (r Report) HasErrors() bool { return len(r.Errors) > 0 || len(r.Drift) > 0 }

// VerifyOptions configures Verify. A nil Logger is silent; Quiet
// suppresses logging regardless of Logger.
type VerifyOptions struct {
	Quiet  bool
	Logger func(format string, args ...any)
}

// defaultEntryMaxChars is the spec §2.6.1 default for
// spine.entry-max-chars. Used when directives/defaults.md is missing
// or unparseable.
const defaultEntryMaxChars = 200

// Verify walks the canonical state at paths and returns a Report.
//
// Errors-or-drift in the Report drive the caller's non-zero exit; the
// returned error from Verify itself signals an I/O or programming
// failure that prevented validation from completing (e.g. spine.jsonl
// failed to read at all). A malformed-but-readable record produces an
// Error finding, not a returned error.
func Verify(paths store.PersonantPaths, opts VerifyOptions) (Report, error) {
	// opts.Logger is reserved for per-step progress reporting; nothing
	// in the v0.1 implementation emits through it. Findings flow back
	// through Report so the cobra layer owns presentation order.
	_ = opts

	report := Report{}

	entryMax, dirWarn := readEntryMaxChars(paths.DirectivesDir)
	if dirWarn != nil {
		report.Warnings = append(report.Warnings, *dirWarn)
	}

	// Spine: read JSONL, validate per-record schema + cross-refs.
	spine, err := store.ReadSpine(paths.Spine)
	if err != nil {
		return Report{}, fmt.Errorf("verify: read spine: %w", err)
	}

	knownProjects := loadKnownProjects(paths.ProjectsDir)
	checkSpine(&report, spine, entryMax, knownProjects)

	// Project meta: validate every projects/prj_<n>/meta.json that exists.
	checkProjectMetas(&report, paths.ProjectsDir)

	// Index drift: delegate to internal/index.
	idxOpts := index.Options{Quiet: true}
	chk, ierr := index.Check(paths, idxOpts)
	if ierr != nil {
		return Report{}, fmt.Errorf("verify: index check: %w", ierr)
	}
	for _, d := range chk.Drifts {
		if d.Detail != "" {
			report.Drift = append(report.Drift, fmt.Sprintf("%s: %s (%s)", d.Path, d.Status, d.Detail))
		} else {
			report.Drift = append(report.Drift, fmt.Sprintf("%s: %s", d.Path, d.Status))
		}
	}

	return report, nil
}

// checkSpine validates each SpineRecord against the spec §2.2 hard
// limits and accumulates Findings on report.
func checkSpine(report *Report, spine []store.SpineRecord, entryMax int, knownProjects map[string]bool) {
	seenIDs := make(map[string]int, len(spine)) // id → first line where seen
	for i, rec := range spine {
		loc := fmt.Sprintf("spine.jsonl[%d]", i+1)

		// id: regex + global uniqueness.
		switch {
		case rec.ID == "":
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "id", Message: "id is empty"})
		case !store.ThreadIDPattern.MatchString(rec.ID):
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "id",
				Message: fmt.Sprintf("id %q does not match %s", rec.ID, store.ThreadIDPattern.String())})
		default:
			if first, dup := seenIDs[rec.ID]; dup {
				report.Errors = append(report.Errors, Finding{Path: loc, Field: "id",
					Message: fmt.Sprintf("duplicate id %q (also at spine.jsonl[%d])", rec.ID, first)})
			} else {
				seenIDs[rec.ID] = i + 1
			}
		}

		// project: regex + cross-ref. "prj_default" is reserved (spec
		// §2.5.1) and exempt from the strict /^prj_\d+$/ pattern.
		switch {
		case rec.Project == "":
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "project", Message: "project is empty"})
		case rec.Project == "prj_default":
			// reserved; always valid as a reference.
		case !store.ProjectIDPattern.MatchString(rec.Project):
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "project",
				Message: fmt.Sprintf("project %q does not match %s", rec.Project, store.ProjectIDPattern.String())})
		case !knownProjects[rec.Project]:
			// Mirror index.Check policy: missing meta.json is a warning, not an error.
			report.Warnings = append(report.Warnings, Finding{Path: loc, Field: "project",
				Message: fmt.Sprintf("missing meta.json for %s; using default display_name", rec.Project)})
		}

		// anchors cardinality.
		switch {
		case len(rec.Anchors) < 4:
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "anchors",
				Message: fmt.Sprintf("anchors length %d < 4", len(rec.Anchors))})
		case len(rec.Anchors) > 8:
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "anchors",
				Message: fmt.Sprintf("anchors length %d > 8", len(rec.Anchors))})
		}

		// anchor length + duplicates.
		seenAnchors := make(map[string]struct{}, len(rec.Anchors))
		for _, a := range rec.Anchors {
			if n := len(a); n < 3 || n > 50 {
				report.Errors = append(report.Errors, Finding{Path: loc, Field: "anchors",
					Message: fmt.Sprintf("anchor %q length %d outside [3,50]", a, n)})
			}
			if _, dup := seenAnchors[a]; dup {
				report.Errors = append(report.Errors, Finding{Path: loc, Field: "anchors",
					Message: fmt.Sprintf("duplicate anchor %q within record", a)})
			}
			seenAnchors[a] = struct{}{}
		}
		// TODO(phase-2-anchors): category-aware normalization validation
		// (identifier preserves case; entity is lowercase + hyphenated;
		// tag has leading '#' stripped) — requires per-project pattern
		// info + symbol-source tracking we don't yet have. Spec §2.7.2.

		// summary length.
		if len(rec.Summary) > entryMax {
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "summary",
				Message: fmt.Sprintf("summary length %d > spine.entry-max-chars %d", len(rec.Summary), entryMax)})
		}

		// state enum.
		if !validThreadState(rec.State) {
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "state",
				Message: fmt.Sprintf("state %q is not a valid ThreadState", rec.State)})
		}

		// timestamps: parseable + ordered. state_changed is parse-only;
		// no ordering constraint is specified relative to the others.
		created, ok1 := parseRFC3339(rec.Created, loc, "created", report)
		lastEng, ok2 := parseRFC3339(rec.LastEngaged, loc, "last_engaged", report)
		_, _ = parseRFC3339(rec.StateChanged, loc, "state_changed", report)
		if ok1 && ok2 && created.After(lastEng) {
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "created",
				Message: fmt.Sprintf("created %s after last_engaged %s", rec.Created, rec.LastEngaged)})
		}

		// non-negative counters.
		if rec.TurnCount < 0 {
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "turn_count",
				Message: fmt.Sprintf("turn_count %d < 0", rec.TurnCount)})
		}
		if rec.RecallFires < 0 {
			report.Errors = append(report.Errors, Finding{Path: loc, Field: "recall_fires",
				Message: fmt.Sprintf("recall_fires %d < 0", rec.RecallFires)})
		}
	}
}

// parseRFC3339 wraps time.Parse and emits a Finding on failure. Returns
// the parsed time and a bool indicating whether parsing succeeded.
// parseRFC3339 reports nothing if the value parses cleanly.
func parseRFC3339(v, loc, field string, report *Report) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		report.Errors = append(report.Errors, Finding{Path: loc, Field: field,
			Message: fmt.Sprintf("%s %q is not RFC3339: %v", field, v, err)})
		return time.Time{}, false
	}
	return t, true
}

// validThreadState reports whether s is one of the spec §2.2.1 enum values.
func validThreadState(s store.ThreadState) bool {
	switch s {
	case store.ThreadActive, store.ThreadPaused, store.ThreadBlocked,
		store.ThreadWIP, store.ThreadResolved, store.ThreadDecided,
		store.ThreadAbandoned:
		return true
	}
	return false
}

// loadKnownProjects scans projectsDir for prj_<n>/meta.json files and
// returns a set of project ids. The set is used purely as a
// cross-reference oracle for spine.project — schema validation of the
// meta files themselves happens in checkProjectMetas. Unreadable
// projectsDir is treated as empty (no known projects).
func loadKnownProjects(projectsDir string) map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !isProjectDirName(e.Name()) {
			continue
		}
		metaPath := filepath.Join(projectsDir, e.Name(), "meta.json")
		if _, err := os.Stat(metaPath); err == nil {
			out[e.Name()] = true
		}
	}
	return out
}

// isProjectDirName reports whether name is a recognized project
// directory under projects/. Accepts both /^prj_\d+$/ and the reserved
// "prj_default" handle (spec §2.5.1).
func isProjectDirName(name string) bool {
	return name == "prj_default" || store.ProjectIDPattern.MatchString(name)
}

// checkProjectMetas validates each projects/prj_<n>/meta.json in turn.
// Iterates in sorted order so output is deterministic.
func checkProjectMetas(report *Report, projectsDir string) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		report.Errors = append(report.Errors, Finding{Path: projectsDir, Field: "",
			Message: fmt.Sprintf("read projects/: %v", err)})
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if !isProjectDirName(name) {
			// Out-of-pattern directories under projects/ are unusual
			// but not strictly fatal — surface as a warning.
			report.Warnings = append(report.Warnings, Finding{
				Path:    filepath.Join("projects", name),
				Field:   "",
				Message: fmt.Sprintf("directory name %q does not match %s and is not the reserved prj_default", name, store.ProjectIDPattern.String()),
			})
			continue
		}
		metaPath := filepath.Join(projectsDir, name, "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// No meta.json for this project; treated as a warning
				// at cross-ref time (see checkSpine). No finding here.
				continue
			}
			report.Errors = append(report.Errors, Finding{
				Path:    filepath.Join("projects", name, "meta.json"),
				Field:   "",
				Message: fmt.Sprintf("read: %v", err),
			})
			continue
		}
		var meta store.ProjectMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			report.Errors = append(report.Errors, Finding{
				Path:    filepath.Join("projects", name, "meta.json"),
				Field:   "",
				Message: fmt.Sprintf("parse: %v", err),
			})
			continue
		}
		checkProjectMeta(report, name, meta)
	}
}

func checkProjectMeta(report *Report, dirName string, meta store.ProjectMeta) {
	loc := filepath.Join("projects", dirName, "meta.json")

	// id: regex (or reserved handle) + matches directory name.
	switch {
	case meta.ID == "":
		report.Errors = append(report.Errors, Finding{Path: loc, Field: "id", Message: "id is empty"})
	case !isProjectDirName(meta.ID):
		report.Errors = append(report.Errors, Finding{Path: loc, Field: "id",
			Message: fmt.Sprintf("id %q does not match %s and is not prj_default", meta.ID, store.ProjectIDPattern.String())})
	case meta.ID != dirName:
		report.Errors = append(report.Errors, Finding{Path: loc, Field: "id",
			Message: fmt.Sprintf("id %q does not match directory name %q", meta.ID, dirName)})
	}

	// name regex.
	if !store.ProjectNamePattern.MatchString(meta.Name) {
		report.Errors = append(report.Errors, Finding{Path: loc, Field: "name",
			Message: fmt.Sprintf("name %q does not match %s", meta.Name, store.ProjectNamePattern.String())})
	}

	// current_root_path: must be present. We do not check disk existence
	// (file-move detection is /cd-project's domain — spec §4.5.5).
	// prj_default carries an empty path by reservation (spec §2.5.1);
	// allow that special case.
	if meta.CurrentRootPath == "" && meta.ID != "prj_default" {
		report.Errors = append(report.Errors, Finding{Path: loc, Field: "current_root_path",
			Message: "current_root_path is empty"})
	}

	// Timestamps.
	created, ok1 := parseRFC3339(meta.Created, loc, "created", report)
	lastActive, ok2 := parseRFC3339(meta.LastActive, loc, "last_active", report)
	if ok1 && ok2 && created.After(lastActive) {
		report.Errors = append(report.Errors, Finding{Path: loc, Field: "created",
			Message: fmt.Sprintf("created %s after last_active %s", meta.Created, meta.LastActive)})
	}

	// thread_count non-negative.
	if meta.ThreadCount < 0 {
		report.Errors = append(report.Errors, Finding{Path: loc, Field: "thread_count",
			Message: fmt.Sprintf("thread_count %d < 0", meta.ThreadCount)})
	}
}

// readEntryMaxChars reads spine.entry-max-chars from
// directives/defaults.md. Falls back to the spec §2.6.1 default
// (defaultEntryMaxChars) on any failure: missing file, unreadable,
// missing key, malformed integer. A non-nil Finding is returned on the
// recoverable parse-fail/missing path so the caller can record a
// warning. A truly missing file returns (default, nil) — no warning,
// since first-run scaffolding writes it and its absence is unusual but
// not actionable from verify.
//
// TODO(phase-2-yaml): replace line-grep with a proper directives
// loader once we have a YAML parser (or move directive params to TOML
// per the project's data-format preference).
func readEntryMaxChars(directivesDir string) (int, *Finding) {
	defaultsPath := filepath.Join(directivesDir, "defaults.md")
	f, err := os.Open(defaultsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Treat missing defaults.md as a warning so the user knows
			// something is off without failing the check.
			return defaultEntryMaxChars, &Finding{
				Path: filepath.Join("directives", "defaults.md"),
				Message: fmt.Sprintf("not found; falling back to default spine.entry-max-chars=%d",
					defaultEntryMaxChars),
			}
		}
		return defaultEntryMaxChars, &Finding{
			Path:    filepath.Join("directives", "defaults.md"),
			Message: fmt.Sprintf("open: %v; falling back to default spine.entry-max-chars=%d", err, defaultEntryMaxChars),
		}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		const key = "spine.entry-max-chars:"
		if !strings.HasPrefix(line, key) {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, key))
		// Strip an optional trailing comment: "200  # default".
		if hash := strings.Index(raw, "#"); hash >= 0 {
			raw = strings.TrimSpace(raw[:hash])
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return defaultEntryMaxChars, &Finding{
				Path:    filepath.Join("directives", "defaults.md"),
				Field:   "spine.entry-max-chars",
				Message: fmt.Sprintf("malformed value %q; falling back to default %d", raw, defaultEntryMaxChars),
			}
		}
		return n, nil
	}
	if err := scanner.Err(); err != nil {
		return defaultEntryMaxChars, &Finding{
			Path:    filepath.Join("directives", "defaults.md"),
			Message: fmt.Sprintf("scan: %v; falling back to default spine.entry-max-chars=%d", err, defaultEntryMaxChars),
		}
	}
	// Key not found at all — warn and fall back.
	return defaultEntryMaxChars, &Finding{
		Path:    filepath.Join("directives", "defaults.md"),
		Field:   "spine.entry-max-chars",
		Message: fmt.Sprintf("key not present; falling back to default %d", defaultEntryMaxChars),
	}
}

