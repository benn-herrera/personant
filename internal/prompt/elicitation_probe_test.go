// Live A/B probe for topic-tag elicitation (SPEC §5.1).
//
// Question (D4 live-run finding): is gemma-4-main's ~34% topic-tag
// omission rate a prompting artifact? Five prompt-placement variants are
// measured for strict-parse compliance over the SAME deterministic
// fixture set (paired comparison), calling the chat model DIRECTLY via
// model.HTTPClient — no sim, no substrate (aim the harness at the unit).
//
//	V0 baseline      directive at top (production BuildSystemPrompt order)
//	V1 tail-anchor   V0 + directive repeated verbatim as the final section
//	V2 terse-tail    V0 + one-line reminder at the very end
//	V3 prefill       V0 + trailing assistant message "*topic: " (OpenAI-
//	                 compatible assistant prefill; reported as unsupported
//	                 if reaper rejects or ignores it)
//	V4 format-shield V0 with a markdown-wrap warning added to the directive
//
// Live-gating follows the house convention (internal/testsupport): the
// test ALWAYS COMPILES, skips without PERSONANT_LIVE_TESTS, and under the
// opt-in an unreachable/misconfigured endpoint is a FAILURE, not a skip.
//
// The probe is RESUMABLE: every completed call is appended to
// test/rundata/elicitation_probe/calls_seed<seed>.jsonl keyed
// (seed, model, fixture, variant); a re-run skips calls already recorded.
// This lets the ~250-call run survive `make test-run`'s fixed 30m
// timeout — invoke repeatedly until the summary reports complete=true.
// The aggregate summary JSON and a rendered results table are rewritten
// after every fixture, so partial progress is always inspectable without
// -v (a passing test's t.Log output is suppressed by plain `go test`).
//
// Overridable knobs for the cheap sanity pass (config-problem guard
// before committing hours of wall-clock):
//
//	PERSONANT_PROBE_N=3 PERSONANT_PROBE_VARIANTS=V0 \
//	  PERSONANT_LIVE_TESTS=1 make test-run PKG=./internal/prompt RUN=TestElicitationProbe
package prompt_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/testsupport"
	"personant/internal/workset"
)

const (
	// probeSeed is the fixture PRNG seed. It is part of the resume key:
	// records in the calls JSONL are only reused when their seed matches,
	// because the fixtures are a pure function of (seed, index).
	probeSeed = 20260715

	// probeN is the number of fixtures (= calls per variant). Overridable
	// down for a sanity pass via PERSONANT_PROBE_N.
	probeN = 50

	// probeChatModel is the reaper-served chat model under test — the
	// same model the live sim uses (test/rundata/test.config.toml
	// defaultModel = "reaper/gemma-4-main").
	probeChatModel = "gemma-4-main"

	// probeMaxTokens mirrors the production response cap
	// (model.DefaultRequest). It must NOT be lowered to save wall-clock:
	// gemma-4-main is served with thinking mode on, and the reasoning
	// channel is not surfaced in Response.Content — a small cap truncates
	// mid-think and yields EMPTY content, polluting the "nothing" bucket
	// with a probe artifact (observed at cap 1024 in the first sanity
	// pass). Empty-content responses are tallied separately as a guard.
	probeMaxTokens = 16384

	// probeCallTimeout bounds one Consult round-trip. Prompts are
	// 150–250 KB, so prefill alone can take minutes on the reaper host.
	probeCallTimeout = 10 * time.Minute

	// probeInvocationBudget makes one test invocation stop issuing calls
	// and exit cleanly (PASS, summary complete=false) before `make
	// test-run`'s fixed 30m -timeout would panic-kill it mid-call. The
	// full ~250-call matrix is collected by re-invoking until the summary
	// reports complete=true; the resumable call log carries the state.
	probeInvocationBudget = 25 * time.Minute

	// Fixture-size targets (bytes of assembled system prompt) and spine
	// cardinality, per the probe design.
	probeSizeMin  = 150 << 10
	probeSizeMax  = 250 << 10
	spineMinLines = 60
	spineMaxLines = 200
)

// Paths are relative to this package's directory (go test runs with the
// package dir as cwd).
const (
	corpusPath  = "../scenarios/testdata/corpus/corpus.json"
	probeOutDir = "../../test/rundata/elicitation_probe"
)

// Variant-specific prompt fragments.
const (
	terseTailReminder = "Begin your response with the *topic: ...* tag line."

	formatShieldWarning = "Do not wrap the tag in markdown bold/italics/code fences; emit the asterisks exactly as shown."

	// formatShieldAnchor is the paragraph of TopicTagDirective the V4
	// warning is inserted before (directly after the example block). If
	// the directive text drifts, the probe fails loudly rather than
	// silently measuring a V4 identical to V0.
	formatShieldAnchor = "\n\nAfter the tag, write your response normally."

	v3Prefill = "*topic: "
)

var probeVariantNames = []string{"V0", "V1", "V2", "V3", "V4"}

// ---------- corpus ----------

type corpusFile struct {
	Articles []corpusArticle `json:"articles"`
}

type corpusArticle struct {
	Domain    string           `json:"domain"`
	Title     string           `json:"title"`
	Fragments []corpusFragment `json:"fragments"`
}

type corpusFragment struct {
	Section string `json:"section"`
	Text    string `json:"text"`
}

func loadCorpus(t *testing.T) []corpusArticle {
	t.Helper()
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read corpus snapshot: %v", err)
	}
	var c corpusFile
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("decode corpus snapshot: %v", err)
	}
	if len(c.Articles) < spineMaxLines {
		t.Fatalf("corpus has %d articles; need at least %d for the largest spine", len(c.Articles), spineMaxLines)
	}
	return c.Articles
}

// ---------- fixtures ----------

type probeFixture struct {
	Index        int
	System       string // V0 baseline system prompt
	LayerA1      string // rendered spine layer (excerpt-genre probe re-assembles around it)
	UserTurn     string
	SpineLines   int
	TargetThread string            // thr_<n> the user turn continues
	Spine        []probeSpineEntry // spine entries in fixture order (history-probe synthesis)
	TargetIdx    int               // index into Spine of TargetThread
}

// probeSpineEntry is one spine line's backing data, captured during fixture
// construction so the history-precedent probe (history_probe_test.go) can
// synthesize coherent replayed turns about the same threads. Pure capture —
// adding it consumes no extra rng draws, so fixtures remain byte-identical
// to the prior probe's (the V0 baseline comparability invariant; the
// history probe cross-checks recorded SystemBytes to enforce this).
type probeSpineEntry struct {
	ID      string
	Article corpusArticle
	Anchors []string // as rendered in the fixture's spine line
}

var (
	slugRunRE     = regexp.MustCompile(`[^a-z0-9]+`)
	blankRunRE    = regexp.MustCompile(`\n{3,}`)
	wsCollapseRE  = regexp.MustCompile(`\s+`)
	spineStatesRE = []memops.ThreadState{memops.ThreadWIP, memops.ThreadActive, memops.ThreadPaused, memops.ThreadBlocked, memops.ThreadDecided}
)

func slug(s string) string {
	return strings.Trim(slugRunRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// anchorsFor derives 4–6 realistic anchor symbols from an article: its
// domain, title slug, and a couple of section slugs, run through the real
// symbol normalizer so the spine vocabulary matches production form.
func anchorsFor(a corpusArticle, rng *rand.Rand) []string {
	cands := []string{a.Domain, slug(a.Title)}
	for _, f := range a.Fragments {
		cands = append(cands, slug(f.Section))
		if len(cands) >= 8 {
			break
		}
	}
	seen := make(map[string]bool)
	out := make([]string, 0, 6)
	for _, c := range cands {
		n := memops.Normalize(c, memops.SymbolEntity)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	want := 4 + rng.Intn(3)
	if len(out) > want {
		out = out[:want]
	}
	return out
}

// summaryFor produces a spine-summary-sized gist from the article's lead
// fragment (whitespace-collapsed, truncated).
func summaryFor(a corpusArticle) string {
	s := wsCollapseRE.ReplaceAllString(a.Fragments[0].Text, " ")
	if len(s) > 110 {
		s = s[:110] + "…"
	}
	return s
}

// bodyText lightly cleans a fragment for Layer-B body use (collapse the
// corpus's display-math blank-line runs; otherwise verbatim).
func bodyText(f corpusFragment) string {
	return blankRunRE.ReplaceAllString(f.Text, "\n\n")
}

// buildFixtures constructs n deterministic fixtures. Each fixture uses its
// own rng seeded probeSeed+index, so fixture i is identical regardless of
// n (PERSONANT_PROBE_N sanity subsets stay comparable with the full run).
func buildFixtures(t *testing.T, articles []corpusArticle, n int) []probeFixture {
	t.Helper()
	fixtures := make([]probeFixture, 0, n)
	for i := 0; i < n; i++ {
		rng := rand.New(rand.NewSource(probeSeed + int64(i)))
		sizeTarget := probeSizeMin + rng.Intn(probeSizeMax-probeSizeMin+1)
		spineCount := spineMinLines + rng.Intn(spineMaxLines-spineMinLines+1)
		perm := rng.Perm(len(articles))

		// Spine (Layer A1): one display-form line per article.
		entries := make([]probeSpineEntry, 0, spineCount)
		id := 1 + rng.Intn(40)
		var spineLines []string
		for j := 0; j < spineCount; j++ {
			a := articles[perm[j]]
			e := probeSpineEntry{ID: fmt.Sprintf("thr_%d", id), Article: a}
			id += 1 + rng.Intn(4)
			e.Anchors = anchorsFor(a, rng)
			rec := memops.SpineRecord{
				ID:      e.ID,
				Anchors: e.Anchors,
				Summary: summaryFor(a),
				State:   spineStatesRE[rng.Intn(len(spineStatesRE))],
			}
			spineLines = append(spineLines, workset.RenderSpineDisplay(rec))
			entries = append(entries, e)
		}

		// Target thread: the spine topic the user turn continues. Needs
		// ≥2 fragments (one reserved for the user turn).
		targetIdx := rng.Intn(spineCount)
		for len(entries[targetIdx].Article.Fragments) < 2 {
			targetIdx = (targetIdx + 1) % spineCount
		}
		target := entries[targetIdx]

		// Layer B: engaged-thread bodies — target thread first, then
		// following spine entries, appending fragments until the
		// assembled prompt reaches the size target. The target article's
		// last fragment is reserved for the user turn.
		layerA1 := strings.Join(spineLines, "\n")
		overhead := len(prompt.TopicTagDirective) + 2048 // preamble + headers slack
		var body strings.Builder
		appendThread := func(e probeSpineEntry, reserveLast bool) bool {
			fmt.Fprintf(&body, "### %s — %s (engaged)\n\n", e.ID, e.Article.Title)
			frags := e.Article.Fragments
			if reserveLast {
				frags = frags[:len(frags)-1]
			}
			for _, f := range frags {
				fmt.Fprintf(&body, "#### %s\n\n%s\n\n", f.Section, bodyText(f))
				if len(layerA1)+body.Len()+overhead >= sizeTarget {
					return true
				}
			}
			return false
		}
		full := appendThread(target, true)
		for j := 1; !full && j < spineCount; j++ {
			full = appendThread(entries[(targetIdx+j)%spineCount], false)
		}

		system := prompt.BuildSystemPrompt(prompt.SystemPromptElements{
			LayerA1: layerA1,
			LayerB:  body.String(),
		})

		// User turn: clearly continues the target spine topic.
		reserved := target.Article.Fragments[len(target.Article.Fragments)-1]
		excerpt := wsCollapseRE.ReplaceAllString(reserved.Text, " ")
		if len(excerpt) > 600 {
			excerpt = excerpt[:600] + "…"
		}
		primaryAnchor := target.Article.Domain
		user := fmt.Sprintf(
			"Back to %s (%s): I just read this passage on %s —\n\n%s\n\n"+
				"How does this fit with what we already have in that thread? "+
				"Keep building on our %s analysis.",
			target.ID, target.Article.Title, reserved.Section, excerpt, primaryAnchor)

		fixtures = append(fixtures, probeFixture{
			Index:        i,
			System:       system,
			LayerA1:      layerA1,
			UserTurn:     user,
			SpineLines:   spineCount,
			TargetThread: target.ID,
			Spine:        entries,
			TargetIdx:    targetIdx,
		})
	}
	return fixtures
}

// buildMessages assembles the request messages for one (fixture, variant).
func buildMessages(t *testing.T, fix probeFixture, variant string) []model.Message {
	t.Helper()
	sys := fix.System
	switch variant {
	case "V0", "V3":
		// V0 baseline; V3 shares the baseline system prompt.
	case "V1":
		sys += "\n\n" + prompt.TopicTagDirective
	case "V2":
		sys += "\n\n" + terseTailReminder
	case "V4":
		replaced := strings.Replace(sys, formatShieldAnchor,
			"\n\n"+formatShieldWarning+formatShieldAnchor, 1)
		if replaced == sys {
			t.Fatalf("V4 anchor %q not found in system prompt — TopicTagDirective text drifted; update formatShieldAnchor", formatShieldAnchor)
		}
		sys = replaced
	default:
		t.Fatalf("unknown variant %q", variant)
	}
	msgs := []model.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: fix.UserTurn},
	}
	if variant == "V3" {
		msgs = append(msgs, model.Message{Role: "assistant", Content: v3Prefill})
	}
	return msgs
}

// ---------- classification ----------

// localStrictShapeRE is a LOCAL copy of the SPEC §5.1.2 tag-shape regex,
// used only for malformed-shape classification (a shape match whose tag
// fails prompt.Parse means the thread list was invalid). Deliberately not
// shared with the parser package: another agent owns parser.go in-flight;
// the primary metrics use only its stable public surface (Parse,
// PreambleScan).
var localStrictShapeRE = regexp.MustCompile(`(?m)^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$`)

var shapeRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"bold-wrap", regexp.MustCompile(`(?m)^\s*\*\*+\s*topic:`)},
	{"backtick-inline", regexp.MustCompile("(?m)^\\s*`+\\s*\\*?topic:")},
	{"missing-leading-asterisk", regexp.MustCompile(`(?m)^\s*topic:\s*[^\[\n]+\[[^\]\n]*\]`)},
	{"missing-trailing-asterisk", regexp.MustCompile(`(?m)^\s*\*topic:\s*[^\[\n]+\[[^\]\n]*\]\s*$`)},
	{"no-anchor-brackets", regexp.MustCompile(`(?m)^\s*\*topic:[^\[\]\n]*\*\s*$`)},
	{"unclosed-anchor-list", regexp.MustCompile(`(?m)^\s*\*topic:\s*[^\[\n]+\[[^\]\n]*$`)},
}

var looseAttemptRE = regexp.MustCompile(`(?i)\btopic\s*:`)

// fenceWrapped reports whether a tag-looking line sits inside a ``` fence.
func fenceWrapped(content string) bool {
	parts := strings.Split(content, "```")
	for i := 1; i < len(parts); i += 2 {
		if looseAttemptRE.MatchString(parts[i]) {
			return true
		}
	}
	return false
}

type classification struct {
	StrictStart    bool
	StrictAnywhere bool
	NearMiss       bool
	Nothing        bool
	TagStart       int
	Shapes         []string
}

// classify scores one response body against the strict parser (primary)
// and the local loose matcher (secondary).
//
//   - StrictAnywhere: prompt.Parse accepts the response (first fully valid
//     tag anywhere).
//   - StrictStart: additionally, prompt.PreambleScan locates a leading tag
//     within the production preamble bound (64 lines / 8 KB) — the
//     production-accepting "tag at response start" definition.
//   - NearMiss: no strict-valid tag, but an attempted/malformed tag shape
//     is present.
//   - Nothing: no strict tag and no recognizable attempt.
func classify(content string) classification {
	var c classification
	buf := []byte(content)
	if len(buf) == 0 || buf[len(buf)-1] != '\n' {
		// PreambleScan requires the tag line's terminating newline; a
		// complete response ending exactly at the tag would otherwise
		// scan as undecided.
		buf = append(buf, '\n')
	}
	start, _, found, _ := prompt.PreambleScan(buf)
	if _, err := prompt.Parse(content); err == nil {
		c.StrictAnywhere = true
	}
	c.StrictStart = found && c.StrictAnywhere
	c.TagStart = start

	for _, r := range shapeRules {
		if r.re.MatchString(content) {
			c.Shapes = append(c.Shapes, r.name)
		}
	}
	if fenceWrapped(content) {
		c.Shapes = append(c.Shapes, "fence-wrap")
	}
	if !c.StrictAnywhere && localStrictShapeRE.MatchString(content) {
		c.Shapes = append(c.Shapes, "well-formed-bad-thread-list")
	}
	if !c.StrictAnywhere && len(c.Shapes) == 0 && looseAttemptRE.MatchString(content) {
		c.Shapes = append(c.Shapes, "other-attempt")
	}
	c.NearMiss = !c.StrictAnywhere && len(c.Shapes) > 0
	c.Nothing = !c.StrictAnywhere && !c.NearMiss
	return c
}

// ---------- persistence (resumable call log + summary) ----------

type callRecord struct {
	Seed             int64    `json:"seed"`
	Model            string   `json:"model"`
	Fixture          int      `json:"fixture"`
	Variant          string   `json:"variant"`
	Error            string   `json:"error,omitempty"`
	StrictStart      bool     `json:"strict_start"`
	StrictAnywhere   bool     `json:"strict_anywhere"`
	NearMiss         bool     `json:"near_miss"`
	Nothing          bool     `json:"nothing"`
	EmptyContent     bool     `json:"empty_content,omitempty"`
	Shapes           []string `json:"shapes,omitempty"`
	V3Continued      bool     `json:"v3_continued,omitempty"`
	V3OwnTag         bool     `json:"v3_own_tag,omitempty"`
	FinishReason     string   `json:"finish_reason,omitempty"`
	PromptTokens     int      `json:"prompt_tokens,omitempty"`
	CompletionTokens int      `json:"completion_tokens,omitempty"`
	LatencyMS        int64    `json:"latency_ms"`
	SystemBytes      int      `json:"system_bytes"`
	HistoryBytes     int      `json:"history_bytes,omitempty"` // history-precedent probe: replayed-pair bytes
	Head             string   `json:"head,omitempty"`
}

func recordKey(fixture int, variant string) string {
	return fmt.Sprintf("%03d|%s", fixture, variant)
}

func callsPath() string {
	return filepath.Join(probeOutDir, fmt.Sprintf("calls_seed%d.jsonl", probeSeed))
}

// loadPriorRecords reads the resumable call log, keeping only records that
// match this probe's seed + model (fixtures are a pure function of seed).
func loadPriorRecords(t *testing.T) map[string]callRecord {
	t.Helper()
	prior := make(map[string]callRecord)
	raw, err := os.ReadFile(callsPath())
	if os.IsNotExist(err) {
		return prior
	}
	if err != nil {
		t.Fatalf("read prior call log: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec callRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// Tolerate a truncated line (killed mid-append): the call is
			// simply re-measured on this run. Never wedge the resume.
			t.Logf("skipping corrupt call-log line (will re-measure): %v", err)
			continue
		}
		if rec.Seed != probeSeed || rec.Model != probeChatModel {
			continue
		}
		prior[recordKey(rec.Fixture, rec.Variant)] = rec
	}
	return prior
}

func appendRecord(t *testing.T, rec callRecord) {
	t.Helper()
	f, err := os.OpenFile(callsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open call log: %v", err)
	}
	defer f.Close()
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal call record: %v", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatalf("append call record: %v", err)
	}
}

// ---------- aggregation ----------

type variantAgg struct {
	Name               string         `json:"name"`
	N                  int            `json:"n"`
	StrictStart        int            `json:"strict_start"`
	StrictStartRate    float64        `json:"strict_start_rate"`
	StrictAnywhere     int            `json:"strict_anywhere"`
	StrictAnywhereRate float64        `json:"strict_anywhere_rate"`
	NearMiss           int            `json:"near_miss"`
	NearMissRate       float64        `json:"near_miss_rate"`
	Nothing            int            `json:"nothing"`
	NothingRate        float64        `json:"nothing_rate"`
	EmptyContent       int            `json:"empty_content"`
	ShapeTally         map[string]int `json:"shape_tally"`
	FinishReasons      map[string]int `json:"finish_reasons"`
	MeanLatencyMS      int64          `json:"mean_latency_ms"`
	V3Continued        int            `json:"v3_continued,omitempty"`
	V3OwnTag           int            `json:"v3_own_tag,omitempty"`
	V3Errors           int            `json:"v3_errors,omitempty"`
}

// aggregate tallies records into per-variant rows, in the order given by
// names; records whose variant is not in names are ignored (the shared
// call log carries both probes' cells).
func aggregate(records map[string]callRecord, names []string) []variantAgg {
	byVariant := make(map[string][]callRecord)
	for _, rec := range records {
		byVariant[rec.Variant] = append(byVariant[rec.Variant], rec)
	}
	var out []variantAgg
	for _, name := range names {
		recs := byVariant[name]
		agg := variantAgg{Name: name, ShapeTally: map[string]int{}, FinishReasons: map[string]int{}}
		var latSum int64
		for _, r := range recs {
			if r.Error != "" {
				agg.V3Errors++
				continue
			}
			agg.N++
			latSum += r.LatencyMS
			if r.StrictStart {
				agg.StrictStart++
			}
			if r.StrictAnywhere {
				agg.StrictAnywhere++
			}
			if r.NearMiss {
				agg.NearMiss++
			}
			if r.Nothing {
				agg.Nothing++
			}
			if r.EmptyContent {
				agg.EmptyContent++
			}
			for _, s := range r.Shapes {
				agg.ShapeTally[s]++
			}
			if r.FinishReason != "" {
				agg.FinishReasons[r.FinishReason]++
			}
			if r.V3Continued {
				agg.V3Continued++
			}
			if r.V3OwnTag {
				agg.V3OwnTag++
			}
		}
		if agg.N > 0 {
			fn := float64(agg.N)
			agg.StrictStartRate = float64(agg.StrictStart) / fn
			agg.StrictAnywhereRate = float64(agg.StrictAnywhere) / fn
			agg.NearMissRate = float64(agg.NearMiss) / fn
			agg.NothingRate = float64(agg.Nothing) / fn
			agg.MeanLatencyMS = latSum / int64(agg.N)
		}
		if agg.N > 0 || agg.V3Errors > 0 {
			out = append(out, agg)
		}
	}
	return out
}

type fixtureStats struct {
	Count           int `json:"count"`
	SystemBytesMin  int `json:"system_bytes_min"`
	SystemBytesMean int `json:"system_bytes_mean"`
	SystemBytesMax  int `json:"system_bytes_max"`
	SpineLinesMin   int `json:"spine_lines_min"`
	SpineLinesMax   int `json:"spine_lines_max"`
	UserBytesMin    int `json:"user_bytes_min"`
	UserBytesMax    int `json:"user_bytes_max"`
}

func statFixtures(fixtures []probeFixture) fixtureStats {
	s := fixtureStats{Count: len(fixtures)}
	if len(fixtures) == 0 {
		return s
	}
	var sum int
	s.SystemBytesMin, s.SpineLinesMin, s.UserBytesMin = 1<<62, 1<<62, 1<<62
	for _, f := range fixtures {
		sb, ub := len(f.System), len(f.UserTurn)
		sum += sb
		s.SystemBytesMin = min(s.SystemBytesMin, sb)
		s.SystemBytesMax = max(s.SystemBytesMax, sb)
		s.SpineLinesMin = min(s.SpineLinesMin, f.SpineLines)
		s.SpineLinesMax = max(s.SpineLinesMax, f.SpineLines)
		s.UserBytesMin = min(s.UserBytesMin, ub)
		s.UserBytesMax = max(s.UserBytesMax, ub)
	}
	s.SystemBytesMean = sum / len(fixtures)
	return s
}

type probeSummary struct {
	Seed          int64        `json:"seed"`
	Model         string       `json:"model"`
	Temperature   float64      `json:"temperature"`
	MaxTokens     int          `json:"max_tokens"`
	N             int          `json:"n"`
	Variants      []string     `json:"variants"`
	Complete      bool         `json:"complete"`
	UpdatedAt     string       `json:"updated_at"`
	FixtureStats  fixtureStats `json:"fixture_stats"`
	V3Unsupported string       `json:"v3_unsupported,omitempty"`
	Results       []variantAgg `json:"results"`
}

func renderTable(aggs []variantAgg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-3s %4s  %-19s %-19s %-19s %-19s %6s %9s\n",
		"var", "n", "strict-start", "strict-anywhere", "near-miss", "nothing", "empty", "mean-lat")
	for _, a := range aggs {
		fmt.Fprintf(&b, "%-3s %4d  %6.3f (%3d)        %6.3f (%3d)        %6.3f (%3d)        %6.3f (%3d)        %6d %8.1fs\n",
			a.Name, a.N,
			a.StrictStartRate, a.StrictStart,
			a.StrictAnywhereRate, a.StrictAnywhere,
			a.NearMissRate, a.NearMiss,
			a.NothingRate, a.Nothing,
			a.EmptyContent,
			float64(a.MeanLatencyMS)/1000)
	}
	for _, a := range aggs {
		if len(a.ShapeTally) == 0 && a.V3Errors == 0 && a.V3Continued == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s shapes:", a.Name)
		names := make([]string, 0, len(a.ShapeTally))
		for k := range a.ShapeTally {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			fmt.Fprintf(&b, " %s=%d", k, a.ShapeTally[k])
		}
		if a.Name == "V3" {
			fmt.Fprintf(&b, "  [prefill continued=%d own-tag=%d errors=%d]",
				a.V3Continued, a.V3OwnTag, a.V3Errors)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// writeSummary writes the working-copy summary pair (<base>.json/.txt)
// and, when finalArtifact is true (a completed FULL matrix — never an
// env-subset sanity pass; the caller owns the condition), an immutable
// timestamped <ts><tsSuffix>.json artifact for later analysis.
func writeSummary(t *testing.T, sum probeSummary, table, base, tsSuffix string, finalArtifact bool) {
	t.Helper()
	raw, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(probeOutDir, base+".json"), raw, 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(probeOutDir, base+".txt"), []byte(table), 0o644); err != nil {
		t.Fatalf("write table: %v", err)
	}
	if finalArtifact {
		ts := clock.Profiling().UTC().Format("20060102T150405Z")
		if err := os.WriteFile(filepath.Join(probeOutDir, ts+tsSuffix+".json"), raw, 0o644); err != nil {
			t.Fatalf("write timestamped summary: %v", err)
		}
	}
}

// ---------- env knobs ----------

func envIntDefault(t *testing.T, name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q is not a positive integer", name, v)
	}
	return n
}

func selectedVariants(t *testing.T) []string {
	v := os.Getenv("PERSONANT_PROBE_VARIANTS")
	if v == "" {
		return probeVariantNames
	}
	valid := make(map[string]bool, len(probeVariantNames))
	for _, name := range probeVariantNames {
		valid[name] = true
	}
	var out []string
	for _, name := range strings.Split(v, ",") {
		name = strings.ToUpper(strings.TrimSpace(name))
		if !valid[name] {
			t.Fatalf("PERSONANT_PROBE_VARIANTS contains unknown variant %q (valid: %v)", name, probeVariantNames)
		}
		out = append(out, name)
	}
	return out
}

// ---------- the probe ----------

func TestElicitationProbe_Live(t *testing.T) {
	testsupport.RequireLive(t)

	n := envIntDefault(t, "PERSONANT_PROBE_N", probeN)
	variants := selectedVariants(t)

	articles := loadCorpus(t)
	fixtures := buildFixtures(t, articles, n)
	fstats := statFixtures(fixtures)
	t.Logf("fixtures: n=%d seed=%d system bytes min/mean/max = %d/%d/%d, spine lines %d..%d",
		n, probeSeed, fstats.SystemBytesMin, fstats.SystemBytesMean, fstats.SystemBytesMax,
		fstats.SpineLinesMin, fstats.SpineLinesMax)

	client := model.NewHTTPClient(testsupport.ReaperProvider())
	testsupport.RequireModelPresent(t, func(ctx context.Context) ([]string, error) {
		infos, err := client.ListModels(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, len(infos))
		for i, m := range infos {
			ids[i] = m.ID
		}
		return ids, nil
	}, probeChatModel)

	if err := os.MkdirAll(probeOutDir, 0o755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	records := loadPriorRecords(t)

	// A prior recorded V3 transport error means the endpoint rejected the
	// trailing assistant message — carry the unsupported verdict across
	// resumed runs instead of re-probing every invocation.
	v3Unsupported := ""
	for _, rec := range records {
		if rec.Variant == "V3" && rec.Error != "" {
			v3Unsupported = rec.Error
			break
		}
	}

	total := len(fixtures) * len(variants)
	done := 0
	for key := range records {
		var fixture int
		var variant string
		if _, err := fmt.Sscanf(key, "%d|%s", &fixture, &variant); err == nil && fixture < n {
			for _, v := range variants {
				if v == variant {
					done++
				}
			}
		}
	}
	t.Logf("probe: %d/%d calls already recorded (resumable log %s)", done, total, callsPath())

	flush := func(complete bool) {
		aggs := aggregate(records, probeVariantNames)
		table := renderTable(aggs)
		finalArtifact := complete && n == probeN && len(variants) == len(probeVariantNames)
		writeSummary(t, probeSummary{
			Seed:          probeSeed,
			Model:         probeChatModel,
			Temperature:   0, // model.DefaultRequest pins temperature 0
			MaxTokens:     probeMaxTokens,
			N:             n,
			Variants:      variants,
			Complete:      complete,
			UpdatedAt:     clock.Profiling().UTC().Format(time.RFC3339),
			FixtureStats:  fstats,
			V3Unsupported: v3Unsupported,
			Results:       aggs,
		}, table, fmt.Sprintf("summary_seed%d", probeSeed), "", finalArtifact)
	}

	// Fixture-major order: all variants of one fixture run consecutively
	// (maximizes any server-side prefix-cache reuse) and an interrupted
	// run leaves every variant near-evenly sampled.
	invocationStart := clock.Profiling()
	budgetExhausted := false
	for _, fix := range fixtures {
		if budgetExhausted {
			break
		}
		for _, variant := range variants {
			if clock.Since(invocationStart) > probeInvocationBudget {
				budgetExhausted = true
				break
			}
			key := recordKey(fix.Index, variant)
			if _, ok := records[key]; ok {
				continue
			}
			if variant == "V3" && v3Unsupported != "" {
				continue
			}

			req := model.DefaultRequest(probeChatModel, buildMessages(t, fix, variant))
			req.MaxTokens = probeMaxTokens

			ctx, cancel := context.WithTimeout(context.Background(), probeCallTimeout)
			start := clock.Profiling()
			resp, err := client.Consult(ctx, req)
			latency := clock.Since(start)
			cancel()

			if err != nil {
				if variant == "V3" {
					// A rejected trailing assistant message is the V3
					// "unsupported" finding, not a probe failure.
					v3Unsupported = err.Error()
					rec := callRecord{
						Seed: probeSeed, Model: probeChatModel,
						Fixture: fix.Index, Variant: variant,
						Error:     err.Error(),
						LatencyMS: latency.Milliseconds(), SystemBytes: len(fix.System),
					}
					appendRecord(t, rec)
					records[key] = rec
					t.Logf("V3 prefill rejected by endpoint (recorded as unsupported): %v", err)
					continue
				}
				testsupport.FailOnErr(t, fmt.Sprintf("consult fixture %d %s", fix.Index, variant), err)
			}

			content := resp.Content
			scored := content
			contentEmpty := strings.TrimSpace(content) == ""
			var v3Continued, v3OwnTag bool
			if variant == "V3" {
				// The prefill is part of the response by construction;
				// score the combined text. Continuation vs. fresh-start
				// is the supportability signal.
				v3OwnTag = strings.HasPrefix(strings.TrimLeft(content, " \t\r\n"), "*topic:")
				scored = v3Prefill + content
			}
			c := classify(scored)
			if variant == "V3" {
				v3Continued = c.StrictStart && c.TagStart == 0 && !v3OwnTag
			}

			head := content
			if len(head) > 240 {
				head = head[:240]
			}
			rec := callRecord{
				Seed: probeSeed, Model: probeChatModel,
				Fixture: fix.Index, Variant: variant,
				StrictStart: c.StrictStart, StrictAnywhere: c.StrictAnywhere,
				NearMiss: c.NearMiss, Nothing: c.Nothing,
				EmptyContent: contentEmpty, Shapes: c.Shapes,
				V3Continued: v3Continued, V3OwnTag: v3OwnTag,
				FinishReason:     resp.FinishReason,
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				LatencyMS:        latency.Milliseconds(),
				SystemBytes:      len(fix.System),
				Head:             head,
			}
			appendRecord(t, rec)
			records[key] = rec
			done++
			t.Logf("[%d/%d] fixture=%02d %s: start=%v anywhere=%v near=%v shapes=%v lat=%.1fs",
				done, total, fix.Index, variant,
				c.StrictStart, c.StrictAnywhere, c.NearMiss, c.Shapes,
				latency.Seconds())
		}
		flush(false)
	}

	// Completion check: every selected (fixture, variant) pair has a
	// record (V3 error records count — unsupported is a completed finding).
	complete := true
	for _, fix := range fixtures {
		for _, variant := range variants {
			if _, ok := records[recordKey(fix.Index, variant)]; !ok && !(variant == "V3" && v3Unsupported != "") {
				complete = false
			}
		}
	}
	flush(complete)

	aggs := aggregate(records, probeVariantNames)
	t.Logf("elicitation probe results (model=%s temp=0 max_tokens=%d n=%d seed=%d):\n%s",
		probeChatModel, probeMaxTokens, n, probeSeed, renderTable(aggs))
	if v3Unsupported != "" {
		t.Logf("V3 prefill UNSUPPORTED by endpoint: %s", v3Unsupported)
	}
	if !complete {
		t.Logf("probe INCOMPLETE (budget-exhausted=%v) — re-run to resume from the call log", budgetExhausted)
	}
}
