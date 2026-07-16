// Live A/B probe for the HISTORY-PRECEDENT hypothesis — follow-up to
// TestElicitationProbe_Live (design/burndown-2026-07b.md D4 findings).
//
// The prior probe proved single-turn tag compliance is 100% at production
// settings (V0..V4 all 1.000 strict-start), so the D4 ~34% omission must
// come from something the single-turn shape did not model. Prime suspect:
// the replayed conversation history showing the model its own prior
// responses WITHOUT topic tags, teaching omission by in-context precedent.
//
// Code-level grounding (read before interpreting results): production's
// session History replays assistant bodies TAG-INTACT — turn.RunWithInfo
// appends full.Content (internal/turn/turn.go, History append). The
// tag-STRIPPED precedent production actually shows the model comes from
// (a) the Layer-B turn excerpts rendered into the system prompt
// (internal/turn/excerpt.go renderTurnExcerpt strips via prompt.Parse),
// and (b) on a live run, the ~34% of history turns where the model itself
// omitted the tag — its own omissions feeding back as precedent. The
// stripped-vs-tagged A/B below isolates exactly that variable.
//
// Cells (paired: same seed/fixtures as the prior probe, same scoring):
//
//	V0     k=0 baseline — CARRIED from the prior probe's call log, never
//	       re-paid; its presence (and SystemBytes agreement) is a hard
//	       precondition so the paired comparison stays valid.
//	H4S    V0 request + k=4 replayed user/assistant pairs, assistant
//	       bodies tag-STRIPPED (production Parse→Body+TrimRight strip)
//	H4T    identical pairs, assistant bodies BEGIN with a valid §5.1.2 tag
//	H12S   k=12, stripped
//	H12T   k=12, tagged
//	H12ST  OPTIONAL remedy cell, auto-activated only once a completed
//	       stripped cell reproduces omission (> historyRemedyThreshold
//	       strict-start non-compliance): H12S + the V2 terse tail reminder
//	       — the cheapest candidate remedy, measured in the failing
//	       condition.
//
// The hypothesis test is the stripped-vs-tagged delta at each k.
//
// Message shape mirrors turn.RunWithInfo exactly: system + history tail
// (pair-aligned user/assistant alternation, oldest first) + current user
// turn. Pairs are synthesized deterministically from the fixture's own
// spine articles (the target thread's Layer-B neighbors, never the target
// itself, so the fixture's "Back to thr_N" user turn stays coherent).
// Budget honesty: total replayed bytes are asserted against the
// production history-tail share (liveTurnPctHistoryTail=35% of
// LiveTurnReserve — turn/livebudget.go boundHistoryTail arithmetic), so
// production would replay ALL of these pairs; none would be
// recency-dropped, and the whole request stays under the production
// ceiling.
//
// Resumable exactly like the prior probe, SHARING its call log
// (calls_seed<seed>.jsonl, keyed seed/model/fixture/variant — which is
// how the V0 baseline is carried for free). Summary artifacts are written
// separately as summary_history_seed<seed>.{json,txt}. Sanity pass:
//
//	PERSONANT_PROBE_N=3 PERSONANT_LIVE_TESTS=1 \
//	  make test-run PKG=./internal/prompt RUN=TestHistoryPrecedentProbe
package prompt_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/testsupport"
)

const (
	// historyTailPctMirror mirrors turn/livebudget.go liveTurnPctHistoryTail
	// (unexported there). If the production share drifts, the honesty
	// assertion below silently loosens/tightens — worth a grep when
	// re-running this probe after budget changes.
	historyTailPctMirror = 35

	// Excerpt bounds sized so a pair is ≈2.1 KB and k=12 pairs (~25 KB)
	// fit the ~26 KB production history-tail share computed above.
	historyUserExcerptMax = 260
	historyBodyLeadMax    = 700
	historyBodyFollowMax  = 500

	// historyRemedyThreshold is the strict-start non-compliance rate a
	// completed stripped cell must exceed to activate the H12ST remedy
	// cell ("the stripped cells reproduce omission").
	historyRemedyThreshold = 0.10
)

// historyBaselineVariant is the prior probe's production-shape cell,
// reused as this probe's k=0 row.
const historyBaselineVariant = "V0"

type historyVariant struct {
	name      string
	k         int  // replayed user/assistant pairs
	tagged    bool // assistant bodies begin with a valid §5.1.2 tag
	terseTail bool // append the V2 terse tail reminder to the system prompt
}

var historyBaseVariants = []historyVariant{
	{name: "H4S", k: 4},
	{name: "H4T", k: 4, tagged: true},
	{name: "H12S", k: 12},
	{name: "H12T", k: 12, tagged: true},
}

var historyRemedyVariant = historyVariant{name: "H12ST", k: 12, terseTail: true}

// historyShareBytes is the byte budget production's boundHistoryTail
// grants the replayed history tail under the default budget.
func historyShareBytes() int {
	return memops.DefaultBudget().LiveTurnReserve * historyTailPctMirror / 100
}

// runeSafeCut returns s truncated to at most max bytes on a rune boundary.
func runeSafeCut(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// historyExcerpt collapses whitespace and truncates to max bytes with an
// ellipsis, mirroring the fixture builder's user-turn excerpt treatment.
func historyExcerpt(text string, max int) string {
	s := strings.TrimSpace(wsCollapseRE.ReplaceAllString(text, " "))
	if len(s) <= max {
		return s
	}
	return runeSafeCut(s, max) + "…"
}

// buildHistoryPairs synthesizes k replayed user/assistant pairs for fix,
// oldest first. Pair p (0-based) discusses the spine entry at offset k-p
// past the target (so the most recent pair is the target's nearest
// Layer-B neighbor and the target itself never appears — offsets 1..k are
// all distinct and non-zero since k < spine length). The stripped and
// tagged variants share the SAME body, differing only in the leading tag
// line, so tag presence is the only manipulated variable. Stripping is
// done the way production does it (turn.RunWithInfo step 6 /
// excerpt.go renderTurnExcerpt): prompt.Parse → Body, TrimRight newlines.
func buildHistoryPairs(t *testing.T, fix probeFixture, k int, tagged bool) []model.Message {
	t.Helper()
	n := len(fix.Spine)
	if k >= n {
		t.Fatalf("history probe: k=%d pairs need k < %d spine entries", k, n)
	}
	msgs := make([]model.Message, 0, 2*k)
	for p := 0; p < k; p++ {
		e := fix.Spine[(fix.TargetIdx+k-p)%n]
		f := e.Article.Fragments[p%len(e.Article.Fragments)]

		user := fmt.Sprintf(
			"Turning to %s (%s) — I want to work through the %s material: %s\n\n"+
				"What should we take from this for the %s picture?",
			e.ID, e.Article.Title, f.Section,
			historyExcerpt(f.Text, historyUserExcerptMax), e.Article.Domain)

		txt := strings.TrimSpace(wsCollapseRE.ReplaceAllString(f.Text, " "))
		lead := historyExcerpt(txt, historyBodyLeadMax)
		follow := ""
		if len(txt) > historyBodyLeadMax {
			start := historyBodyLeadMax
			for start < len(txt) && !utf8.RuneStart(txt[start]) {
				start++
			}
			follow = historyExcerpt(txt[start:], historyBodyFollowMax)
		}
		anchor := e.Article.Domain
		if len(e.Anchors) > 0 {
			anchor = e.Anchors[0]
		}
		body := fmt.Sprintf("The load-bearing point in this %s passage: %s", f.Section, lead)
		if follow != "" {
			body += "\n\nCarrying that further: " + follow
		}
		body += fmt.Sprintf(
			"\n\nFor %s this reinforces the %s line we have been developing; "+
				"I've folded it into the thread's running picture.", e.ID, anchor)

		tagLine := fmt.Sprintf("*topic: %s [%s]*", e.ID, strings.Join(e.Anchors, ", "))
		taggedContent := tagLine + "\n\n" + body
		pr, err := prompt.Parse(taggedContent)
		if err != nil || len(pr.Tag.Threads) != 1 || pr.Tag.Threads[0] != e.ID {
			t.Fatalf("history probe: synthesized tag for %s is not a valid §5.1.2 tag (err=%v) — the tagged cell would teach a broken precedent", e.ID, err)
		}
		assistant := taggedContent
		if !tagged {
			assistant = strings.TrimRight(pr.Body, "\n") // the production strip
		}
		msgs = append(msgs,
			model.Message{Role: "user", Content: user},
			model.Message{Role: "assistant", Content: assistant},
		)
	}
	return msgs
}

// historyMessages assembles the full request for one (fixture, variant),
// mirroring turn.RunWithInfo's shape: system + history tail + user. It
// returns the messages and the replayed-history byte total, and fails the
// test if that total exceeds the production history-tail share (the
// budget-honesty gate: production would have replayed every pair).
func historyMessages(t *testing.T, fix probeFixture, v historyVariant) ([]model.Message, int) {
	t.Helper()
	sys := fix.System
	if v.terseTail {
		sys += "\n\n" + terseTailReminder
	}
	pairs := buildHistoryPairs(t, fix, v.k, v.tagged)
	histBytes := 0
	for _, m := range pairs {
		histBytes += len(m.Content)
	}
	if share := historyShareBytes(); histBytes > share {
		t.Fatalf("history probe: fixture %d %s replayed history is %d bytes > production history-tail share %d (boundHistoryTail would drop pairs — shrink the excerpt bounds)",
			fix.Index, v.name, histBytes, share)
	}
	msgs := make([]model.Message, 0, len(pairs)+2)
	msgs = append(msgs, model.Message{Role: "system", Content: sys})
	msgs = append(msgs, pairs...)
	msgs = append(msgs, model.Message{Role: "user", Content: fix.UserTurn})
	return msgs, histBytes
}

// strippedReproducedOmission reports whether any COMPLETED stripped cell
// (all n fixtures recorded) shows strict-start non-compliance above the
// remedy threshold. An incomplete cell is undecided, never a trigger.
func strippedReproducedOmission(records map[string]callRecord, n int) bool {
	for _, name := range []string{"H4S", "H12S"} {
		total, miss := 0, 0
		complete := true
		for i := 0; i < n; i++ {
			rec, ok := records[recordKey(i, name)]
			if !ok || rec.Error != "" {
				complete = false
				break
			}
			total++
			if !rec.StrictStart {
				miss++
			}
		}
		if complete && total > 0 && float64(miss)/float64(total) > historyRemedyThreshold {
			return true
		}
	}
	return false
}

// activeHistoryVariants returns the cells to collect: the four base cells,
// plus the H12ST remedy cell once a completed stripped cell has reproduced
// omission. Deterministic across resumed invocations (a pure function of
// the call log), so the remedy activates identically wherever the run is
// resumed.
func activeHistoryVariants(records map[string]callRecord, n int) []historyVariant {
	out := append([]historyVariant(nil), historyBaseVariants...)
	if strippedReproducedOmission(records, n) {
		out = append(out, historyRemedyVariant)
	}
	return out
}

func historyReportNames(vs []historyVariant) []string {
	names := []string{historyBaselineVariant}
	for _, v := range vs {
		names = append(names, v.name)
	}
	return names
}

// ---------- the probe ----------

func TestHistoryPrecedentProbe_Live(t *testing.T) {
	testsupport.RequireLive(t)

	n := envIntDefault(t, "PERSONANT_PROBE_N", probeN)
	articles := loadCorpus(t)
	fixtures := buildFixtures(t, articles, n)
	fstats := statFixtures(fixtures)
	t.Logf("fixtures: n=%d seed=%d system bytes min/mean/max = %d/%d/%d, history-tail share=%d bytes",
		n, probeSeed, fstats.SystemBytesMin, fstats.SystemBytesMean, fstats.SystemBytesMax, historyShareBytes())

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

	// Precondition: the k=0 baseline is carried from the prior probe, not
	// re-paid — and it anchors comparability, so fixture drift is fatal.
	for _, fix := range fixtures {
		rec, ok := records[recordKey(fix.Index, historyBaselineVariant)]
		if !ok {
			t.Fatalf("history probe: no %s baseline record for fixture %d — run TestElicitationProbe_Live to completion first (k=0 is carried from its call log, never re-paid)",
				historyBaselineVariant, fix.Index)
		}
		if rec.SystemBytes != len(fix.System) {
			t.Fatalf("history probe: fixture %d rebuilt system prompt is %d bytes but the %s baseline recorded %d — buildFixtures drifted; the paired comparison is void",
				fix.Index, len(fix.System), historyBaselineVariant, rec.SystemBytes)
		}
	}

	flush := func(vs []historyVariant, complete bool) {
		names := historyReportNames(vs)
		aggs := aggregate(records, names)
		writeSummary(t, probeSummary{
			Seed:         probeSeed,
			Model:        probeChatModel,
			Temperature:  0, // model.DefaultRequest pins temperature 0
			MaxTokens:    probeMaxTokens,
			N:            n,
			Variants:     names,
			Complete:     complete,
			UpdatedAt:    clock.Profiling().UTC().Format(time.RFC3339),
			FixtureStats: fstats,
			Results:      aggs,
		}, renderTable(aggs), fmt.Sprintf("summary_history_seed%d", probeSeed), "_history",
			complete && n == probeN)
	}

	invocationStart := clock.Profiling()
	budgetExhausted := false
	issued := 0
	// The outer loop re-derives the active variant set after a full pass:
	// completing the base cells may activate the remedy cell, whose calls
	// (including earlier fixtures) are then collected on the next pass.
outer:
	for {
		variants := activeHistoryVariants(records, n)
		pending := 0
		for _, fix := range fixtures {
			for _, v := range variants {
				key := recordKey(fix.Index, v.name)
				if _, ok := records[key]; ok {
					continue
				}
				pending++
				if clock.Since(invocationStart) > probeInvocationBudget {
					budgetExhausted = true
					break outer
				}

				msgs, histBytes := historyMessages(t, fix, v)
				req := model.DefaultRequest(probeChatModel, msgs)
				req.MaxTokens = probeMaxTokens

				ctx, cancel := context.WithTimeout(context.Background(), probeCallTimeout)
				start := clock.Profiling()
				resp, err := client.Consult(ctx, req)
				latency := clock.Since(start)
				cancel()
				if err != nil {
					testsupport.FailOnErr(t, fmt.Sprintf("consult fixture %d %s", fix.Index, v.name), err)
				}

				content := resp.Content
				c := classify(content)
				head := content
				if len(head) > 240 {
					head = head[:240]
				}
				rec := callRecord{
					Seed: probeSeed, Model: probeChatModel,
					Fixture: fix.Index, Variant: v.name,
					StrictStart: c.StrictStart, StrictAnywhere: c.StrictAnywhere,
					NearMiss: c.NearMiss, Nothing: c.Nothing,
					EmptyContent:     strings.TrimSpace(content) == "",
					Shapes:           c.Shapes,
					FinishReason:     resp.FinishReason,
					PromptTokens:     resp.Usage.PromptTokens,
					CompletionTokens: resp.Usage.CompletionTokens,
					LatencyMS:        latency.Milliseconds(),
					SystemBytes:      len(fix.System),
					HistoryBytes:     histBytes,
					Head:             head,
				}
				appendRecord(t, rec)
				records[key] = rec
				issued++
				t.Logf("[+%d] fixture=%02d %s: start=%v anywhere=%v near=%v shapes=%v hist=%dB lat=%.1fs",
					issued, fix.Index, v.name,
					c.StrictStart, c.StrictAnywhere, c.NearMiss, c.Shapes,
					histBytes, latency.Seconds())
				flush(variants, false)
			}
		}
		if pending == 0 {
			break
		}
	}

	variants := activeHistoryVariants(records, n)
	complete := true
	for _, fix := range fixtures {
		for _, v := range variants {
			if _, ok := records[recordKey(fix.Index, v.name)]; !ok {
				complete = false
			}
		}
	}
	flush(variants, complete)

	t.Logf("history-precedent probe results (model=%s temp=0 max_tokens=%d n=%d seed=%d, k=0 baseline carried from %s):\n%s",
		probeChatModel, probeMaxTokens, n, probeSeed, historyBaselineVariant,
		renderTable(aggregate(records, historyReportNames(variants))))
	if strippedReproducedOmission(records, n) {
		t.Logf("remedy cell %s ACTIVE (a completed stripped cell exceeded %.0f%% strict-start non-compliance)",
			historyRemedyVariant.name, historyRemedyThreshold*100)
	}
	if !complete {
		t.Logf("probe INCOMPLETE (budget-exhausted=%v) — re-run to resume from the call log", budgetExhausted)
	}
}
