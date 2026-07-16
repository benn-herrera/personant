// Live A/B probe round 3 — the EXCERPT-FORMAT and WORK-SWITCH-GENRE
// hypotheses, follow-up to TestElicitationProbe_Live (round 1: static
// placement/format exonerated, V0..V4 all 1.000 strict-start) and
// TestHistoryPrecedentProbe_Live (round 2: tag-stripped replayed history
// mostly exonerated — 4% omission at saturated k=12). Two suspects remain
// for the D4 live-run ~34% omission:
//
//	CELL E   Layer-B excerpt format. Identical to V0 except the Layer-B
//	         section of the system prompt is rendered in the REAL
//	         production excerpt format (turn/excerpt.go renderTurnExcerpt
//	         + workset renderThreadBody header + store ReadThreadBody
//	         blank-line join): "## Turn N · timestamp · [anchors]" blocks
//	         with "**user:** ..." / "**agent:** ..." where the agent body
//	         is TAG-STRIPPED the production way. Same corpus content,
//	         sized to V0's assembled prompt. Hypothesis: the model's
//	         largest in-prompt corpus of its own voice is untagged,
//	         teaching omission from inside the system prompt.
//	CELL G   Work-switch genre. Identical to V0 except the request carries
//	         k=4 TAGGED history pairs (exactly H4T's) and the user turn
//	         SWITCHES to a different spine topic mid-conversation, phrased
//	         in the sim workload's terse work-switch register
//	         (workload.go defaultUserInput: "working on #<tag> and <tag>")
//	         — the shape of the original turn-91 failure. Genre includes
//	         both the switch decision AND the prompt register.
//	CELL EG  Both combined — the D4-realistic worst case.
//
// Baselines V0 (clean continuation) and H4T (tagged-history continuation)
// are CARRIED from the shared call log, never re-paid; their presence and
// the V0/H4T SystemBytes agreement are hard preconditions (fixture drift
// voids the paired comparison).
//
// Resumable exactly like the prior probes, SHARING their call log
// (calls_seed<seed>.jsonl keyed seed/model/fixture/variant). Summary
// artifacts are written separately as summary_round3_seed<seed>.{json,txt}.
// Sanity pass:
//
//	PERSONANT_PROBE_N=3 PERSONANT_LIVE_TESTS=1 \
//	  make test-run PKG=./internal/prompt RUN=TestExcerptGenreProbe
package prompt_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/testsupport"
)

const (
	// switchDestOffset places cell G's switch destination at spine offset
	// 5 past the target — guaranteed distinct from the continuation target
	// (offset 0) and from every replayed history pair's topic (offsets
	// 1..4, buildHistoryPairs), since spineCount >= 60 > 5.
	switchDestOffset = 5

	// switchHistoryK matches H4T so the carried H4T row is the exact
	// same-history continuation control for cell G.
	switchHistoryK = 4

	// excerptTurnStride spaces synthesized turn timestamps within one
	// thread's excerpt window. Cosmetic realism only.
	excerptTurnStride = 90 * time.Second
)

// round3ReportNames orders the summary rows: carried baselines first.
var round3ReportNames = []string{"V0", "H4T", "E", "G", "EG"}

var round3Cells = []string{"E", "G", "EG"}

// excerptBaseTime anchors the synthesized excerpt timestamps (the SPEC
// §2.3 example's timestamp). Fixed construction, not a clock read.
var excerptBaseTime = time.Date(2026, 5, 8, 3, 12, 0, 0, time.FixedZone("", -7*3600))

// renderExcerptBlock is a faithful mirror of the unexported production
// renderer turn/excerpt.go renderTurnExcerpt, minus the trailing newline
// (store.ReadThreadBody trims it before the blank-line join):
//
//	## Turn <N> · <RFC3339> · [a, b, c]
//
//	**user:** <userInput>
//
//	**agent:** <responseBody, topic tag stripped>
func renderExcerptBlock(turnN int, when time.Time, anchors []string, user, agent string) string {
	return fmt.Sprintf("## Turn %d · %s · [%s]\n\n**user:** %s\n\n**agent:** %s",
		turnN, when.Format(time.RFC3339), strings.Join(anchors, ", "),
		strings.TrimRight(user, "\n"), strings.TrimRight(agent, "\n"))
}

// excerptTurn synthesizes one replayed turn about fragment f of spine
// entry e: a terse user ask plus an agent body carrying the fragment
// near-verbatim (size parity with V0's section-format body, same corpus
// content). The agent body is produced by building a VALID §5.1.2-tagged
// response and stripping it exactly the way production does
// (prompt.Parse → Body, TrimRight) — so the untagged precedent shown to
// the model is precisely what renderTurnExcerpt stores.
func excerptTurn(t *testing.T, e probeSpineEntry, f corpusFragment, turnN int, when time.Time) string {
	t.Helper()
	user := fmt.Sprintf("Continuing %s (%s) — work through the %s material and fold it into the thread.",
		e.ID, e.Article.Title, f.Section)

	anchor := e.Article.Domain
	if len(e.Anchors) > 0 {
		anchor = e.Anchors[0]
	}
	body := fmt.Sprintf("Key material from the %s section:\n\n%s\n\nFolded into %s's running %s picture.",
		f.Section, bodyText(f), e.ID, anchor)

	tagLine := fmt.Sprintf("*topic: %s [%s]*", e.ID, strings.Join(e.Anchors, ", "))
	tagged := tagLine + "\n\n" + body
	pr, err := prompt.Parse(tagged)
	if err != nil || len(pr.Tag.Threads) != 1 || pr.Tag.Threads[0] != e.ID {
		t.Fatalf("excerpt probe: synthesized tag for %s is not a valid §5.1.2 tag (err=%v)", e.ID, err)
	}
	stripped := strings.TrimRight(pr.Body, "\n") // the production strip
	return renderExcerptBlock(turnN, when, e.Anchors, user, stripped)
}

// buildExcerptSystem assembles the cell-E system prompt: same Layer A1
// spine, same corpus content and thread order as the fixture's V0 body
// (target thread first with its last fragment reserved for the user
// turn, then following spine entries), but with Layer B rendered in the
// production on-disk-excerpt shape:
//
//   - per thread, the workset renderThreadBody header
//     ("# <summary> (<id>) — <STATE>" + "[anchors]: ...")
//   - one "## Turn ..." excerpt block per fragment, blank-line joined
//     (store.ReadThreadBody's chronological join)
//   - threads joined with "\n---\n" (workset renderLayerB)
//
// Sized against len(fix.System) so E's prompt matches V0's within one
// fragment (the same overshoot granularity buildFixtures itself has).
func buildExcerptSystem(t *testing.T, fix probeFixture) string {
	t.Helper()
	target := len(fix.System)
	overhead := len(prompt.TopicTagDirective) + 2048 // same slack as buildFixtures
	n := len(fix.Spine)

	var body strings.Builder
	appendThread := func(spineOff int, reserveLast bool) bool {
		e := fix.Spine[(fix.TargetIdx+spineOff)%n]
		if body.Len() > 0 {
			body.WriteString("\n---\n")
		}
		fmt.Fprintf(&body, "# %s (%s) — %s\n[anchors]: %s\n\n",
			summaryFor(e.Article), e.ID,
			strings.ToUpper(string(memops.ThreadWIP)),
			strings.Join(e.Anchors, ", "))

		frags := e.Article.Fragments
		if reserveLast {
			frags = frags[:len(frags)-1]
		}
		// Deterministic cosmetic turn numbering/timestamps: an engaged
		// thread mid-life, older threads earlier in the day.
		turnBase := 8 + (fix.TargetIdx+spineOff)%40
		threadStart := excerptBaseTime.Add(-time.Duration(spineOff) * time.Hour)
		for fi, f := range frags {
			if fi > 0 {
				body.WriteString("\n\n")
			}
			body.WriteString(excerptTurn(t, e, f, turnBase+fi,
				threadStart.Add(time.Duration(fi)*excerptTurnStride)))
			if len(fix.LayerA1)+body.Len()+overhead >= target {
				return true
			}
		}
		return false
	}
	full := appendThread(0, true)
	for j := 1; !full && j < n; j++ {
		full = appendThread(j, false)
	}
	return prompt.BuildSystemPrompt(prompt.SystemPromptElements{
		LayerA1: fix.LayerA1,
		LayerB:  body.String(),
	})
}

// switchUserTurn returns cell G's user input — a mid-conversation switch
// to a different spine topic, phrased in the sim workload's terse
// work-switch register (workload.go defaultUserInput:
// "working on #<tag> and <tag>") — and the destination thread ID.
func switchUserTurn(fix probeFixture) (string, string) {
	dest := fix.Spine[(fix.TargetIdx+switchDestOffset)%len(fix.Spine)]
	first, second := dest.Article.Domain, slug(dest.Article.Title)
	if len(dest.Anchors) > 0 {
		first = dest.Anchors[0]
	}
	if len(dest.Anchors) > 1 {
		second = dest.Anchors[1]
	}
	return fmt.Sprintf("working on #%s and %s", first, second), dest.ID
}

// round3Messages assembles the request for one (fixture, cell). Returns
// the messages, the system-prompt byte size (recorded for drift checks),
// and the replayed-history byte total. History cells reuse the round-2
// budget-honesty gate: replayed bytes must fit the production
// history-tail share, or production would have recency-dropped pairs.
func round3Messages(t *testing.T, fix probeFixture, cell string) ([]model.Message, int, int) {
	t.Helper()
	var sys, user string
	var pairs []model.Message
	switch cell {
	case "E":
		sys, user = buildExcerptSystem(t, fix), fix.UserTurn
	case "G":
		sys = fix.System
		user, _ = switchUserTurn(fix)
		pairs = buildHistoryPairs(t, fix, switchHistoryK, true)
	case "EG":
		sys = buildExcerptSystem(t, fix)
		user, _ = switchUserTurn(fix)
		pairs = buildHistoryPairs(t, fix, switchHistoryK, true)
	default:
		t.Fatalf("unknown round-3 cell %q", cell)
	}
	histBytes := 0
	for _, m := range pairs {
		histBytes += len(m.Content)
	}
	if share := historyShareBytes(); histBytes > share {
		t.Fatalf("round-3 probe: fixture %d %s replayed history is %d bytes > production history-tail share %d",
			fix.Index, cell, histBytes, share)
	}
	msgs := make([]model.Message, 0, len(pairs)+2)
	msgs = append(msgs, model.Message{Role: "system", Content: sys})
	msgs = append(msgs, pairs...)
	msgs = append(msgs, model.Message{Role: "user", Content: user})
	return msgs, len(sys), histBytes
}

// ---------- the probe ----------

func TestExcerptGenreProbe_Live(t *testing.T) {
	testsupport.RequireLive(t)

	n := envIntDefault(t, "PERSONANT_PROBE_N", probeN)
	articles := loadCorpus(t)
	fixtures := buildFixtures(t, articles, n)
	fstats := statFixtures(fixtures)
	t.Logf("fixtures: n=%d seed=%d system bytes min/mean/max = %d/%d/%d",
		n, probeSeed, fstats.SystemBytesMin, fstats.SystemBytesMean, fstats.SystemBytesMax)

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

	// Preconditions: both carried baselines exist for every fixture, and
	// the rebuilt fixtures byte-match what the baselines saw — fixture
	// drift voids the paired comparison.
	for _, fix := range fixtures {
		for _, base := range []string{"V0", "H4T"} {
			rec, ok := records[recordKey(fix.Index, base)]
			if !ok {
				t.Fatalf("round-3 probe: no %s baseline record for fixture %d — run the earlier probes to completion first (baselines are carried, never re-paid)",
					base, fix.Index)
			}
			if rec.SystemBytes != len(fix.System) {
				t.Fatalf("round-3 probe: fixture %d rebuilt system prompt is %d bytes but the %s baseline recorded %d — buildFixtures drifted; the paired comparison is void",
					fix.Index, len(fix.System), base, rec.SystemBytes)
			}
		}
	}

	flush := func(complete bool) {
		aggs := aggregate(records, round3ReportNames)
		writeSummary(t, probeSummary{
			Seed:         probeSeed,
			Model:        probeChatModel,
			Temperature:  0, // model.DefaultRequest pins temperature 0
			MaxTokens:    probeMaxTokens,
			N:            n,
			Variants:     round3ReportNames,
			Complete:     complete,
			UpdatedAt:    clock.Profiling().UTC().Format(time.RFC3339),
			FixtureStats: fstats,
			Results:      aggs,
		}, renderTable(aggs), fmt.Sprintf("summary_round3_seed%d", probeSeed), "_round3",
			complete && n == probeN)
	}

	total := len(fixtures) * len(round3Cells)
	done := 0
	for _, fix := range fixtures {
		for _, cell := range round3Cells {
			if _, ok := records[recordKey(fix.Index, cell)]; ok {
				done++
			}
		}
	}
	t.Logf("probe: %d/%d calls already recorded (resumable log %s)", done, total, callsPath())

	invocationStart := clock.Profiling()
	budgetExhausted := false
	// Fixture-major order: an interrupted run leaves every cell
	// near-evenly sampled.
	for _, fix := range fixtures {
		if budgetExhausted {
			break
		}
		for _, cell := range round3Cells {
			if clock.Since(invocationStart) > probeInvocationBudget {
				budgetExhausted = true
				break
			}
			key := recordKey(fix.Index, cell)
			if _, ok := records[key]; ok {
				continue
			}

			msgs, sysBytes, histBytes := round3Messages(t, fix, cell)
			req := model.DefaultRequest(probeChatModel, msgs)
			req.MaxTokens = probeMaxTokens

			ctx, cancel := context.WithTimeout(context.Background(), probeCallTimeout)
			start := clock.Profiling()
			resp, err := client.Consult(ctx, req)
			latency := clock.Since(start)
			cancel()
			if err != nil {
				testsupport.FailOnErr(t, fmt.Sprintf("consult fixture %d %s", fix.Index, cell), err)
			}

			content := resp.Content
			c := classify(content)
			head := content
			if len(head) > 240 {
				head = head[:240]
			}
			rec := callRecord{
				Seed: probeSeed, Model: probeChatModel,
				Fixture: fix.Index, Variant: cell,
				StrictStart: c.StrictStart, StrictAnywhere: c.StrictAnywhere,
				NearMiss: c.NearMiss, Nothing: c.Nothing,
				EmptyContent:     strings.TrimSpace(content) == "",
				Shapes:           c.Shapes,
				FinishReason:     resp.FinishReason,
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				LatencyMS:        latency.Milliseconds(),
				SystemBytes:      sysBytes,
				HistoryBytes:     histBytes,
				Head:             head,
			}
			appendRecord(t, rec)
			records[key] = rec
			done++
			t.Logf("[%d/%d] fixture=%02d %s: start=%v anywhere=%v near=%v shapes=%v sys=%dB hist=%dB lat=%.1fs",
				done, total, fix.Index, cell,
				c.StrictStart, c.StrictAnywhere, c.NearMiss, c.Shapes,
				sysBytes, histBytes, latency.Seconds())
		}
		flush(false)
	}

	complete := true
	for _, fix := range fixtures {
		for _, cell := range round3Cells {
			if _, ok := records[recordKey(fix.Index, cell)]; !ok {
				complete = false
			}
		}
	}
	flush(complete)

	t.Logf("excerpt/genre probe results (model=%s temp=0 max_tokens=%d n=%d seed=%d, V0+H4T carried):\n%s",
		probeChatModel, probeMaxTokens, n, probeSeed,
		renderTable(aggregate(records, round3ReportNames)))
	if !complete {
		t.Logf("probe INCOMPLETE (budget-exhausted=%v) — re-run to resume from the call log", budgetExhausted)
	}
}
