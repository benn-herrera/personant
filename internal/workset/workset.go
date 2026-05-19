// Package workset assembles the working-set inputs (spec §3.1) that
// feed prompt.BuildSystemPrompt.
//
// Phase 2.e-A populates all five layers (E directives + conventions,
// A1 current-project spine, A2 cross-project digests, B active thread
// bodies, C dormant thread summaries) with byte-budget-driven
// truncation. v0.1 uses byte counts as a token proxy (§6.5).
//
// Layer composition is read-only; the §3.0.5 "no-bypass" contract is
// preserved because Compose mutates no on-disk state. A failure on one
// layer is logged via opts.Logger and that layer renders empty;
// neighbouring layers proceed.
package workset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"personant/internal/dedup"
	"personant/internal/memops"
	"personant/internal/prompt"
	"personant/internal/store"
)

// State carries the inputs Compose needs to render layer content.
//
// ActiveThreads / DormantThreads are ordered: index 0 is most-recently
// engaged. The turn package owns the LRU update path
// (closeTurnAndUpdateEngagement); workset is read-only and trusts the
// ordering it receives.
type State struct {
	Paths          store.PersonantPaths
	ActiveProject  memops.ProjectMeta
	ActiveThreads  []string // Layer B membership; most-recently-engaged first
	DormantThreads []string // Layer C membership; most-recently-engaged first
	Budget         Budget
}

// ComposeOptions tweaks rendering behavior. Logger receives non-fatal
// warnings (a missing digest, an absent conventions file). nil → silent.
type ComposeOptions struct {
	Logger func(format string, args ...any)
}

// Compose builds the SystemPromptElements for one turn. Each layer is
// rendered independently and truncated to its byte budget; A2 has a
// per-project cap as well.
//
// If a renderer fails for one layer, that layer is empty and the
// failure is logged via opts.Logger; other layers proceed. The only
// hard error returned is an unset ActiveProject.ID — without it there
// is no spine to render and downstream prompt assembly is malformed.
func Compose(state State, opts ComposeOptions) (prompt.SystemPromptElements, error) {
	if state.ActiveProject.ID == "" {
		return prompt.SystemPromptElements{}, fmt.Errorf("workset: ActiveProject.ID is empty")
	}
	logf := opts.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}
	budget := state.Budget
	if budget.Total == 0 {
		budget = DefaultBudget()
	}

	layerE := renderLayerE(state, budget, logf)
	layerA1, err := renderLayerA1(state, budget)
	if err != nil {
		logf("workset: render A1: %v", err)
		layerA1 = ""
	}
	layerA2 := renderLayerA2(state, budget, logf)
	layerB := renderLayerB(state, budget, logf)
	layerC := renderLayerC(state, budget, logf)

	return prompt.SystemPromptElements{
		LayerE:  layerE,
		LayerA1: layerA1,
		LayerA2: layerA2,
		LayerB:  layerB,
		LayerC:  layerC,
	}, nil
}

// RenderSpineDisplay turns one SpineRecord into its spec §2.2.2
// display form:
//
//	thr_<id> [<anchors joined with ", ">] — <summary> [<state>]
//
// State markers are uppercased (WIP, RESOLVED, etc.) to match the
// example in §2.2.2. An empty state field renders without the
// trailing `[...]` so partially-populated records render visibly
// rather than producing `... [ ]`.
func RenderSpineDisplay(rec memops.SpineRecord) string {
	anchors := strings.Join(rec.Anchors, ", ")
	if rec.State == "" {
		return fmt.Sprintf("%s [%s] — %s", rec.ID, anchors, rec.Summary)
	}
	return fmt.Sprintf("%s [%s] — %s [%s]",
		rec.ID, anchors, rec.Summary, strings.ToUpper(string(rec.State)))
}

// ---------- Layer A1: current project's spine ----------

func renderLayerA1(state State, budget Budget) (string, error) {
	records, err := store.SpineRecordsByProject(state.Paths, state.ActiveProject.ID)
	if err != nil {
		return "", fmt.Errorf("load spine: %w", err)
	}
	lines := make([]string, 0, len(records))
	for _, rec := range records {
		lines = append(lines, RenderSpineDisplay(rec))
	}
	return truncateToBudget(strings.Join(lines, "\n"), budget.LayerA1, "Layer A1"), nil
}

// ---------- Layer E: directives + conventions ----------

func renderLayerE(state State, budget Budget, logf func(string, ...any)) string {
	var b strings.Builder

	directiveSources := []struct {
		header string
		path   string
		// optional: only warn if absent
		optional bool
	}{
		{"defaults", filepath.Join(state.Paths.DirectivesDir, "defaults.md"), true},
		{"user", filepath.Join(state.Paths.DirectivesDir, "user.md"), true},
		{
			"project: " + state.ActiveProject.Name,
			filepath.Join(state.Paths.DirectivesDir, state.ActiveProject.ID, "project.md"),
			true,
		},
	}
	for _, src := range directiveSources {
		body, err := readDirectiveBody(src.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && src.optional {
				continue
			}
			logf("workset: layer E: read %s: %v", src.path, err)
			continue
		}
		if body == "" {
			continue
		}
		writeSection(&b, src.header, body)
	}

	for _, conv := range state.ActiveProject.ConventionsPaths {
		data, err := os.ReadFile(conv)
		if err != nil {
			logf("workset: layer E: conventions %s: %v", conv, err)
			continue
		}
		writeSection(&b, "conventions: "+conv, string(data))
	}

	return truncateToBudget(b.String(), budget.LayerE, "Layer E")
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
	trimmed := strings.TrimLeft(content, " \t\n\r")
	if !strings.HasPrefix(trimmed, "---") {
		return content
	}
	// Strip exactly the leading whitespace we just trimmed, then look at
	// remaining lines.
	rest := trimmed[len("---"):]
	// The opening delimiter line must end here at a newline (not at a
	// `---X` token).
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") && rest != "" {
		return content
	}
	// Skip the rest of the opening delimiter line.
	if i := strings.Index(rest, "\n"); i >= 0 {
		rest = rest[i+1:]
	} else {
		// No newline after `---` — treat as no frontmatter.
		return content
	}
	// Find the closing delimiter line.
	for {
		nl := strings.Index(rest, "\n")
		var line string
		if nl < 0 {
			line = rest
		} else {
			line = rest[:nl]
		}
		if strings.TrimRight(line, " \t\r") == "---" {
			if nl < 0 {
				return ""
			}
			body := rest[nl+1:]
			// One conventional blank line after the closing delimiter is
			// stripped; everything else preserved verbatim.
			if strings.HasPrefix(body, "\n") {
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

func writeSection(b *strings.Builder, header, body string) {
	b.WriteString("=== ")
	b.WriteString(header)
	b.WriteString(" ===\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n")
}

// ---------- Layer A2: cross-project digests ----------

func renderLayerA2(state State, budget Budget, logf func(string, ...any)) string {
	metas, err := store.ListProjects(state.Paths)
	if err != nil {
		logf("workset: layer A2: list projects: %v", err)
		return ""
	}
	type projectLine struct {
		lastActive string
		line       string
	}
	others := make([]projectLine, 0, len(metas))
	for _, m := range metas {
		if m.ID == state.ActiveProject.ID || m.ID == store.DefaultProjectID {
			continue
		}
		digest, ok := loadDigest(state.Paths, m.ID, logf)
		if !ok {
			continue
		}
		raw := renderDigestLine(m, digest)
		others = append(others, projectLine{
			lastActive: m.LastActive,
			line:       truncateRunes(raw, budget.PerProjectDigestBytes),
		})
	}
	// Sort by last_active desc; stable secondary by id ascending so
	// equal-LastActive projects render in a deterministic order.
	sort.SliceStable(others, func(i, j int) bool {
		if others[i].lastActive != others[j].lastActive {
			return others[i].lastActive > others[j].lastActive
		}
		return others[i].line < others[j].line
	})
	if len(others) == 0 {
		return ""
	}
	lines := make([]string, len(others))
	for i, p := range others {
		lines[i] = p.line
	}
	return truncateToBudget(strings.Join(lines, "\n"), budget.LayerA2, "Layer A2")
}

// loadDigest reads <Home>/projects/<id>/digest.json. A missing digest
// is a warning, not an error — the project simply contributes no A2
// line. ok=false signals "skip this project."
func loadDigest(paths store.PersonantPaths, id string, logf func(string, ...any)) (memops.ProjectDigest, bool) {
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

// renderDigestLine produces the per-project A2 line:
//
//	<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>
func renderDigestLine(meta memops.ProjectMeta, d memops.ProjectDigest) string {
	name := meta.Name
	if name == "" {
		name = d.DisplayName
	}
	if name == "" {
		name = meta.ID
	}
	const maxRecentAnchors = 5
	anchors := d.RecentAnchors
	if len(anchors) > maxRecentAnchors {
		anchors = anchors[:maxRecentAnchors]
	}
	return fmt.Sprintf("%s (%s): %s :: %s",
		name, meta.ID, d.OneLineSummary, strings.Join(anchors, ", "))
}

// ---------- Layer B: active thread bodies ----------

func renderLayerB(state State, budget Budget, logf func(string, ...any)) string {
	if len(state.ActiveThreads) == 0 || budget.LayerB <= 0 {
		return ""
	}
	limit := len(state.ActiveThreads)
	if budget.BTopK > 0 && limit > budget.BTopK {
		limit = budget.BTopK
	}
	// Per-thread share. A single oversized thread is truncated to its
	// share so it does not crowd out its peers.
	perThread := budget.LayerB
	if limit > 0 {
		perThread = budget.LayerB / limit
		if perThread < 256 {
			perThread = 256 // floor: no thread renders empty just from arithmetic
		}
	}

	rendered := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		id := state.ActiveThreads[i]
		body, err := renderThreadBody(state.Paths, id, perThread, logf)
		if err != nil {
			logf("workset: layer B: %s: %v", id, err)
			continue
		}
		rendered = append(rendered, truncateToBudget(body, perThread, "thread "+id))
	}
	if len(rendered) == 0 {
		return ""
	}
	joined := strings.Join(rendered, "\n---\n")
	return truncateToBudget(joined, budget.LayerB, "Layer B")
}

// renderThreadBody loads a thread and renders it as:
//
//	# <summary> (<id>) — <state>
//	[anchors]: a, b, c, d
//
//	<body>
//
//	=== tracked files ===
//	<one block per §3.9.2 tracked file>
//
// The body is assembled from the thread's recency-windowed turn-excerpt
// files. byteBudget is passed to store.ReadThreadBody so only ~budget
// worth of the most recent excerpts is read (newest-first), not all
// retained turns — the outer truncateToBudget still trims the rendered
// result, but the budget hint keeps the read itself bounded. The
// tracked-files section is appended only when the thread has a
// files.json sidecar with ≥1 entry; absent or empty sidecar renders
// nothing extra, keeping no-tracked-file threads byte-identical.
func renderThreadBody(paths store.PersonantPaths, id string, byteBudget int, logf func(string, ...any)) (string, error) {
	fm, err := store.LoadThreadFrontmatter(paths, id)
	if err != nil {
		return "", err
	}
	body, err := store.ReadThreadBody(paths, id, byteBudget)
	if err != nil {
		return "", err
	}
	state := strings.ToUpper(string(fm.State))
	if state == "" {
		state = "UNKNOWN"
	}
	var b strings.Builder
	b.WriteString("# ")
	if fm.Summary != "" {
		b.WriteString(fm.Summary)
	} else {
		b.WriteString(fm.ID)
	}
	b.WriteString(" (")
	b.WriteString(fm.ID)
	b.WriteString(") — ")
	b.WriteString(state)
	b.WriteString("\n[anchors]: ")
	b.WriteString(strings.Join(fm.Anchors, ", "))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))

	appendTrackedFiles(&b, paths, id, logf)
	return b.String(), nil
}

// appendTrackedFiles appends the §3.9.2 tracked-files section to b for
// thread id. A missing sidecar yields an empty store and the section is
// omitted entirely. A LiveWindow failure on one file is logged via logf
// and that file skipped (tolerate-and-continue, matching renderLayerB).
func appendTrackedFiles(b *strings.Builder, paths store.PersonantPaths, id string, logf func(string, ...any)) {
	tf, err := store.LoadThreadFiles(paths, id)
	if err != nil {
		logf("workset: layer B: %s: load tracked files: %v", id, err)
		return
	}
	if len(tf.Files) == 0 {
		return
	}
	paths2 := make([]string, 0, len(tf.Files))
	for p := range tf.Files {
		paths2 = append(paths2, p)
	}
	sort.Strings(paths2)

	var section strings.Builder
	for _, p := range paths2 {
		entry := tf.Files[p]
		window, err := entry.Chain.LiveWindow(dedup.LiveDiffWindow)
		if err != nil {
			logf("workset: layer B: %s: tracked file %s: %v", id, p, err)
			continue
		}
		writeTrackedFile(&section, entry, window)
	}
	if section.Len() == 0 {
		return
	}
	b.WriteString("\n\n=== tracked files ===\n")
	b.WriteString(strings.TrimRight(section.String(), "\n"))
}

// writeTrackedFile renders one tracked file's §3.9.2 block: a path
// header (with the commit hash when committed), the current literal,
// then the older entries oldest-first — diffs as labelled unified-diff
// blocks, identifiers as their bracketed line.
func writeTrackedFile(b *strings.Builder, entry *store.FileEntry, window []dedup.WindowEntry) {
	b.WriteString("--- ")
	b.WriteString(entry.Path)
	if entry.LastCommit != "" {
		b.WriteString(" [committed ")
		b.WriteString(entry.LastCommit)
		b.WriteString("]")
	}
	b.WriteString(" ---\n")

	// window is oldest-first; the last entry is current. Render the
	// current literal first, then the older history in temporal order.
	for _, e := range window {
		if e.Kind != "current" {
			continue
		}
		b.WriteString(e.Text)
		if !strings.HasSuffix(e.Text, "\n") {
			b.WriteString("\n")
		}
	}
	for _, e := range window {
		switch e.Kind {
		case "diff":
			fmt.Fprintf(b, "[version %d diff]\n", e.Version)
			b.WriteString(e.Text)
			if !strings.HasSuffix(e.Text, "\n") {
				b.WriteString("\n")
			}
		case "identifier":
			fmt.Fprintf(b, "[version %d] %s\n", e.Version, e.Text)
		}
	}
	b.WriteString("\n")
}

// ---------- Layer C: dormant thread summaries ----------

func renderLayerC(state State, budget Budget, logf func(string, ...any)) string {
	if len(state.DormantThreads) == 0 || budget.LayerC <= 0 {
		return ""
	}
	lines := make([]string, 0, len(state.DormantThreads))
	for _, id := range state.DormantThreads {
		rec, found, err := store.FindSpineRecord(state.Paths, id)
		if err != nil {
			logf("workset: layer C: find %s: %v", id, err)
			continue
		}
		if !found {
			logf("workset: layer C: %s: not in spine", id)
			continue
		}
		lines = append(lines, RenderSpineDisplay(rec))
	}
	if len(lines) == 0 {
		return ""
	}
	return truncateToBudget(strings.Join(lines, "\n"), budget.LayerC, "Layer C")
}

// ---------- truncation helpers ----------

// truncateToBudget returns s if its byte length is ≤ budget; otherwise
// it cuts at the last rune boundary that fits and appends a marker.
// A non-positive budget returns "".
func truncateToBudget(s string, budget int, label string) string {
	if budget <= 0 {
		return ""
	}
	if len(s) <= budget {
		return s
	}
	marker := fmt.Sprintf("\n... [%s truncated; budget=%d bytes] ...\n", label, budget)
	if len(marker) >= budget {
		// Pathological: marker alone exceeds budget. Return the marker
		// itself, hard-truncated; honesty over completeness.
		return truncateRunes(marker, budget)
	}
	cut := budget - len(marker)
	return truncateRunes(s, cut) + marker
}

// truncateRunes returns the longest prefix of s whose byte length is
// ≤ n that ends on a UTF-8 rune boundary.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	// Walk back from byte n until we land on a rune start.
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
