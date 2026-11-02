// Package workset assembles the working-set inputs (spec §3.1) that
// feed prompt.BuildSystemPrompt.
//
// Phase 2.e-A populates all five layers (E directives + conventions,
// A1 current-project spine, A2 cross-project digests, B active thread
// bodies, C dormant thread summaries) with byte-budget-driven
// truncation. v0.1 uses byte counts as a token proxy (§6.5).
//
// # Substrate-free
//
// Compose is a pure function over plain Go types (see Inputs). It does
// no file I/O and imports no substrate packages — all substrate reads
// live in the adapter (memops/fileadapter.ComposeWorkingSet) which
// pre-fetches the data and hands it to Compose. This mirrors the
// recall/scoring split (MAD C3 + T3-1): substrate access is the
// adapter's responsibility; the application layer composes pure
// content.
package workset

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"personant/internal/memops"
	"personant/internal/prompt"
)

// Inputs is the per-turn input shape Compose consumes. Every field is
// already-fetched plain data — Compose touches no substrate.
//
// ActiveThreads / DormantThreads are ordered: index 0 is the most
// recently engaged thread. The turn package owns the LRU update path;
// workset is read-only and trusts the ordering it receives.
//
// The substrate-fetch shape mirrors the order callers want rendered:
//   - ActiveProjectSpine is filtered to the active project.
//   - OtherProjects excludes the active project and the default project.
//   - ActiveThreadData is keyed by thread id; entries absent from the
//     map are treated as missing-on-substrate and skipped (the adapter
//     handles the warning logging when it can't load one).
//   - DormantSpine is likewise a lookup; missing ids skip.
type Inputs struct {
	ActiveProject  memops.ProjectMeta
	ActiveThreads  []string
	DormantThreads []string
	Budget         memops.Budget

	// Layer E: directive bodies (frontmatter already stripped by the
	// adapter) and arbitrary convention-file bodies in render order.
	Directives  []DirectiveSection
	Conventions []ConventionFile

	// Layer A1: spine records for the active project in canonical order
	// (id ascending, matching store.SpineRecordsByProject).
	ActiveProjectSpine []memops.SpineRecord

	// Layer A2: other-project digests already filtered (no active, no
	// default), each carrying meta + digest. Sorted by Compose.
	OtherProjects []ProjectDigestEntry

	// Layer B: full thread data for active threads. Map keyed by
	// thread id; a missing key is treated as substrate-missing and
	// silently skipped — the adapter has already logged the warning.
	ActiveThreadData map[string]ThreadData

	// Layer C: spine records for dormant threads, keyed by id. A
	// missing key is treated as substrate-missing and silently skipped.
	DormantSpine map[string]memops.SpineRecord
}

// DirectiveSection is one directive source rendered as a single Layer E
// section. The adapter does the frontmatter stripping and decides
// optional-vs-required policy before passing here.
type DirectiveSection struct {
	Header string // e.g. "defaults", "user", "project: alpha"
	Body   string
}

// ConventionFile is one convention file's content with its source path
// (the path is used as the section header in Layer E so the assistant
// can attribute conventions to their origin).
type ConventionFile struct {
	Path    string
	Content string
}

// ProjectDigestEntry pairs a project's meta with its digest for Layer
// A2 rendering. Caller pre-filters (no active project, no default).
type ProjectDigestEntry struct {
	Meta   memops.ProjectMeta
	Digest memops.ProjectDigest
}

// ThreadData is the per-thread input for Layer B rendering: metadata and
// the excerpts the adapter's budgeted walk took, oldest first, each with
// the expansions planned at its position.
type ThreadData struct {
	Meta     memops.ThreadMeta
	Excerpts []Excerpt
}

// Excerpt is one turn excerpt and the file content expanded at it, in
// render order.
type Excerpt struct {
	Turn       int
	Text       string
	Expansions []Expansion
}

// ExpansionKind is what an expansion renders.
type ExpansionKind string

const (
	ExpansionLiteral ExpansionKind = "literal"
	ExpansionDiff    ExpansionKind = "diff"
)

// Expansion is file content rendered inside an excerpt: a file's current
// content (ExpansionLiteral, named by ID) or a unified diff
// (ExpansionDiff, from From to To). Size is the content's length in
// bytes. An Omitted expansion carries no Content and renders as a note
// naming the content and its size.
type Expansion struct {
	Kind     ExpansionKind
	Path     string
	ID       string
	From, To string
	Size     int
	Content  string
	Omitted  bool
}

// ComposeOptions tweaks rendering behavior. Logger receives the §3.1
// render-stage warning: a layer that was handed inputs (and a positive
// budget) but produced nothing because the pre-fetched data for every
// one of its entries was absent. nil → silent.
//
// This is deliberately the *aggregate* render-outcome signal, not a
// per-entry one: substrate-fetch failures (a missing thread file, an
// unreadable digest) are diagnosed and logged entry-by-entry by the
// adapter as it fetches (memops/fileadapter, workset.warning event
// lines). The pure renderer's exclusive, non-duplicative contribution
// is "the whole layer collapsed to empty despite having something to
// render" — which only it is positioned to observe.
type ComposeOptions struct {
	Logger func(format string, args ...any)
}

// Compose builds the SystemPromptElements for one turn. Each layer is
// rendered independently and truncated to its byte budget; A2 has a
// per-project cap as well.
//
// If a renderer cannot produce output for a layer, that layer is empty
// — substrate-fetch warnings are the adapter's responsibility (it
// already logged them on the way in). The only hard error returned is
// an unset ActiveProject.ID — without it there is no spine to render
// and downstream prompt assembly is malformed.
func Compose(in Inputs, opts ComposeOptions) (prompt.SystemPromptElements, error) {
	if in.ActiveProject.ID == "" {
		return prompt.SystemPromptElements{}, fmt.Errorf("workset: ActiveProject.ID is empty")
	}
	budget := in.Budget
	if budget.Total == 0 {
		budget = memops.DefaultBudget()
	}

	el := prompt.SystemPromptElements{
		LayerE:  renderLayerE(in, budget),
		LayerA1: renderLayerA1(in, budget),
		LayerA2: renderLayerA2(in, budget),
		LayerB:  renderLayerB(in, budget),
		LayerC:  renderLayerC(in, budget),
	}

	// §3.1: "A failure to render one layer ... emits a workset.warning
	// log line and that layer renders empty; neighbour layers proceed."
	// Layers B and C are the only two the pure renderer can collapse to
	// empty despite inputs: each looks its entries up in a pre-fetched
	// map (ActiveThreadData / DormantSpine) and skips misses, so if every
	// entry is absent the layer renders "" even though ids were supplied.
	// A non-positive layer budget is an intentional allocation, not a
	// render failure, so it is not warned.
	warnEmptyLayer(opts.Logger, "B", el.LayerB, len(in.ActiveThreads), budget.LayerB)
	warnEmptyLayer(opts.Logger, "C", el.LayerC, len(in.DormantThreads), budget.LayerC)

	return el, nil
}

// warnEmptyLayer emits the §3.1 render-stage workset.warning when a layer
// that had inputs (n > 0) and a positive byte budget rendered to an empty
// string — i.e. the pre-fetched data for all of its entries was missing
// and the layer's whole contribution was lost. nil logger → no-op.
func warnEmptyLayer(logger func(string, ...any), layer, rendered string, n, layerBudget int) {
	if logger == nil || n == 0 || layerBudget <= 0 || rendered != "" {
		return
	}
	logger("workset: layer %s rendered empty despite %d input(s): pre-fetched data missing for all entries", layer, n)
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

func renderLayerA1(in Inputs, budget memops.Budget) string {
	if len(in.ActiveProjectSpine) == 0 {
		return ""
	}
	lines := make([]string, 0, len(in.ActiveProjectSpine))
	for _, rec := range in.ActiveProjectSpine {
		lines = append(lines, RenderSpineDisplay(rec))
	}
	return truncateToBudget(strings.Join(lines, "\n"), budget.LayerA1, "Layer A1")
}

// ---------- Layer E: directives + conventions ----------

func renderLayerE(in Inputs, budget memops.Budget) string {
	var b strings.Builder
	for _, d := range in.Directives {
		if d.Body == "" {
			continue
		}
		writeSection(&b, d.Header, d.Body)
	}
	for _, c := range in.Conventions {
		writeSection(&b, "conventions: "+c.Path, c.Content)
	}
	return truncateToBudget(b.String(), budget.LayerE, "Layer E")
}

func writeSection(b *strings.Builder, header, body string) {
	b.WriteString("=== ")
	b.WriteString(header)
	b.WriteString(" ===\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n")
}

// ---------- Layer A2: cross-project digests ----------

func renderLayerA2(in Inputs, budget memops.Budget) string {
	if len(in.OtherProjects) == 0 {
		return ""
	}
	type projectLine struct {
		lastActive string
		line       string
	}
	others := make([]projectLine, 0, len(in.OtherProjects))
	for _, p := range in.OtherProjects {
		raw := renderDigestLine(p.Meta, p.Digest)
		others = append(others, projectLine{
			lastActive: p.Meta.LastActive,
			line:       truncateRunes(raw, budget.PerProjectDigestBytes),
		})
	}
	// Sort by last_active desc; stable secondary by line ascending so
	// equal-LastActive projects render in a deterministic order.
	sort.SliceStable(others, func(i, j int) bool {
		if others[i].lastActive != others[j].lastActive {
			return others[i].lastActive > others[j].lastActive
		}
		return others[i].line < others[j].line
	})
	lines := make([]string, len(others))
	for i, p := range others {
		lines[i] = p.line
	}
	return truncateToBudget(strings.Join(lines, "\n"), budget.LayerA2, "Layer A2")
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

func renderLayerB(in Inputs, budget memops.Budget) string {
	if len(in.ActiveThreads) == 0 || budget.LayerB <= 0 {
		return ""
	}
	limit := len(in.ActiveThreads)
	if budget.BTopK > 0 && limit > budget.BTopK {
		limit = budget.BTopK
	}
	// Per-thread share. The adapter's walk sizes each thread, with its
	// ThreadSeparator, to it; the truncation here is the backstop for an
	// always-taken newest excerpt whose text alone overruns the share.
	perThread := PerThreadBudget(budget.LayerB, limit)

	rendered := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		id := in.ActiveThreads[i]
		data, ok := in.ActiveThreadData[id]
		if !ok {
			continue
		}
		body := renderThreadBody(id, data)
		rendered = append(rendered, truncateToBudget(body, perThread, "thread "+id))
	}
	if len(rendered) == 0 {
		return ""
	}
	joined := strings.Join(rendered, ThreadSeparator)
	return truncateToBudget(joined, budget.LayerB, "Layer B")
}

// ThreadSeparator joins the rendered Layer B threads.
const ThreadSeparator = "\n---\n"

// minPerThreadBudget is the floor for a single Layer B thread's per-thread
// share — the "Layer B floored against recency starvation" guarantee
// (#127): the most-recently-engaged thread always gets a meaningfully
// visible share even if peers are large. With LayerB / BTopK arithmetic,
// a tight overall budget could push the share to zero; the floor keeps
// every selected thread visible rather than dropping to "header only" or
// empty. Raised from 256 to 4096 alongside the #127 token-denominated
// budget so "floored" means a visible header + excerpt, not a bare
// frontmatter line, at the ~16× larger budget.
const minPerThreadBudget = 4096

// PerThreadBudget is the per-thread byte share for Layer B given the
// total Layer B budget and the number of threads chosen for rendering.
// The adapter walks each thread's excerpts against the same share the
// renderer truncates to, so substrate I/O matches render budget. n ≤ 0
// returns the full budget unchanged.
func PerThreadBudget(layerB, n int) int {
	if n <= 0 {
		return layerB
	}
	share := layerB / n
	if share < minPerThreadBudget {
		share = minPerThreadBudget
	}
	return share
}

// renderThreadBody renders one active thread as its header followed by
// each excerpt in chronological order. Its length is exactly the header's
// plus each excerpt's, so the adapter can size the walk with the same
// functions.
func renderThreadBody(id string, data ThreadData) string {
	var b strings.Builder
	b.WriteString(RenderThreadHeader(id, data.Meta))
	for _, e := range data.Excerpts {
		b.WriteString(RenderExcerpt(e))
	}
	return b.String()
}

// RenderThreadHeader renders a Layer B thread's header:
//
//	# <summary> (<id>) — <state>
//	[anchors]: a, b, c, d
func RenderThreadHeader(id string, fm memops.ThreadMeta) string {
	state := strings.ToUpper(string(fm.State))
	if state == "" {
		state = "UNKNOWN"
	}
	var b strings.Builder
	b.WriteString("# ")
	if fm.Summary != "" {
		b.WriteString(fm.Summary)
	} else if fm.ID != "" {
		b.WriteString(fm.ID)
	} else {
		b.WriteString(id)
	}
	b.WriteString(" (")
	if fm.ID != "" {
		b.WriteString(fm.ID)
	} else {
		b.WriteString(id)
	}
	b.WriteString(") — ")
	b.WriteString(state)
	b.WriteString("\n[anchors]: ")
	b.WriteString(strings.Join(fm.Anchors, ", "))
	return b.String()
}

// RenderExcerpt renders one excerpt as it appears in its thread: the
// separator before it, its text, then its expansions in order.
func RenderExcerpt(e Excerpt) string {
	var b strings.Builder
	b.WriteString(excerptSeparator)
	b.WriteString(strings.TrimRight(e.Text, "\n"))
	for _, x := range e.Expansions {
		b.WriteString(renderExpansion(x))
	}
	return b.String()
}

// ExcerptTextLen is the rendered length of an excerpt's text with the
// separator before it, without its expansions.
func ExcerptTextLen(text string) int {
	return len(excerptSeparator) + len(strings.TrimRight(text, "\n"))
}

const excerptSeparator = "\n\n"

// ExpansionLen is x's rendered length, computed from its header fields
// and Size without its Content: the size note's length when x is
// Omitted, the full block's otherwise.
func ExpansionLen(x Expansion) int {
	if x.Omitted {
		return len(renderExpansion(x))
	}
	x.Content = ""
	return len(renderExpansion(x)) + x.Size
}

// renderExpansion renders x as a delimited block, or as its size note
// when x is Omitted. Nothing in the frame depends on Content, which
// ExpansionLen relies on.
func renderExpansion(x Expansion) string {
	label := expansionLabel(x)
	if x.Omitted {
		return fmt.Sprintf("\n\n[%s not shown: %d bytes] %s", label, x.Size, x.Path)
	}
	return fmt.Sprintf("\n\n[%s] %s\n%s\n%s", label, x.Path, x.Content, ExpansionEndMarker(x))
}

// ExpansionEndMarker is the closing line of x's rendered block when x is
// not Omitted. It depends only on x's Kind, ID, From and To.
func ExpansionEndMarker(x Expansion) string {
	return "[end " + expansionLabel(x) + "]"
}

func expansionLabel(x Expansion) string {
	if x.Kind == ExpansionDiff {
		return fmt.Sprintf("file diff #%s -> #%s", short12(x.From), short12(x.To))
	}
	return fmt.Sprintf("file content #%s", short12(x.ID))
}

func short12(id string) string { return id[:min(12, len(id))] }

// ---------- Layer C: dormant thread summaries ----------

func renderLayerC(in Inputs, budget memops.Budget) string {
	if len(in.DormantThreads) == 0 || budget.LayerC <= 0 {
		return ""
	}
	lines := make([]string, 0, len(in.DormantThreads))
	for _, id := range in.DormantThreads {
		rec, ok := in.DormantSpine[id]
		if !ok {
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
