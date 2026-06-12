package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/store"
	"personant/internal/turn"
)

// assertThreadCount returns an InvariantCheck asserting len(spine) == n.
func assertThreadCount(n int) InvariantCheck {
	return func(h *Harness) error {
		recs, err := store.ReadSpine(h.Paths.Spine)
		if err != nil {
			return fmt.Errorf("assertThreadCount: read spine: %w", err)
		}
		if len(recs) != n {
			ids := make([]string, len(recs))
			for i, r := range recs {
				ids[i] = r.ID
			}
			return fmt.Errorf("assertThreadCount: got %d (%v), want %d", len(recs), ids, n)
		}
		return nil
	}
}

// assertThreadTurnCount returns an InvariantCheck asserting that the
// named thread's spine record turn_count equals n.
func assertThreadTurnCount(id string, n int) InvariantCheck {
	return func(h *Harness) error {
		rec, found, err := store.FindSpineRecord(h.Paths, id)
		if err != nil {
			return fmt.Errorf("assertThreadTurnCount: find %s: %w", id, err)
		}
		if !found {
			return fmt.Errorf("assertThreadTurnCount: %s not in spine", id)
		}
		if rec.TurnCount != n {
			return fmt.Errorf("assertThreadTurnCount: %s turn_count got %d want %d", id, rec.TurnCount, n)
		}
		return nil
	}
}

// assertHistorySymbolsContain returns an InvariantCheck asserting the
// thread's frontmatter has a history_symbols entry for the given
// normalized symbol with at least the specified count.
func assertHistorySymbolsContain(threadID, normalized string, minCount int) InvariantCheck {
	return func(h *Harness) error {
		thr, err := store.LoadThread(h.Paths, threadID)
		if err != nil {
			return fmt.Errorf("assertHistorySymbolsContain: load %s: %w", threadID, err)
		}
		for _, hs := range thr.Meta.HistorySymbols {
			if hs.Normalized == normalized {
				if hs.Count < minCount {
					return fmt.Errorf("assertHistorySymbolsContain: %s.%s count %d < %d",
						threadID, normalized, hs.Count, minCount)
				}
				return nil
			}
		}
		return fmt.Errorf("assertHistorySymbolsContain: %s missing %q in history_symbols", threadID, normalized)
	}
}

// assertSpineMatchFires returns an InvariantCheck that walks every log
// file under h.Paths.LogsDir, counts `spine.match-fire <thrID>` lines
// per thread, and verifies the totals match `want`. A thread present
// in `want` with count 0 means "no match-fire expected for this
// thread"; a thread NOT in `want` is unconstrained (i.e. only listed
// threads are checked). totalCount, when non-negative, additionally
// asserts the total spine.match-fire line count across the run.
func assertSpineMatchFires(want map[string]int, totalCount int) InvariantCheck {
	return func(h *Harness) error {
		entries, err := os.ReadDir(h.Paths.LogsDir)
		if err != nil {
			return fmt.Errorf("assertSpineMatchFires: read logs dir: %w", err)
		}
		got := map[string]int{}
		total := 0
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(h.Paths.LogsDir, e.Name()))
			if err != nil {
				return fmt.Errorf("assertSpineMatchFires: read %s: %w", e.Name(), err)
			}
			for line := range strings.SplitSeq(string(body), "\n") {
				// Match shape: "<RFC3339> spine.match-fire <thrID> score=...".
				_, rest, ok := strings.Cut(line, "spine.match-fire ")
				if !ok {
					continue
				}
				total++
				thrID, _, _ := strings.Cut(rest, " ")
				got[thrID]++
			}
		}
		for thrID, w := range want {
			if got[thrID] != w {
				return fmt.Errorf("assertSpineMatchFires: %s match-fire count got %d want %d (all observed: %v)",
					thrID, got[thrID], w, got)
			}
		}
		if totalCount >= 0 && total != totalCount {
			return fmt.Errorf("assertSpineMatchFires: total match-fire count got %d want %d (per-thread: %v)",
				total, totalCount, got)
		}
		return nil
	}
}

// assertHistorySymbolsCap returns an InvariantCheck asserting the
// thread's history_symbols length never exceeds the spec §2.6.1 cap
// (default 40).
func assertHistorySymbolsCap(threadID string, cap int) InvariantCheck {
	return func(h *Harness) error {
		thr, err := store.LoadThread(h.Paths, threadID)
		if err != nil {
			return fmt.Errorf("assertHistorySymbolsCap: load %s: %w", threadID, err)
		}
		if n := len(thr.Meta.HistorySymbols); n > cap {
			return fmt.Errorf("assertHistorySymbolsCap: %s history_symbols=%d > cap=%d", threadID, n, cap)
		}
		return nil
	}
}

// TestScenario_SingleThreadLifecycle_Engagement covers the §11.4
// "single-thread lifecycle" scenario for the create + engagement
// portion. The retirement / archive / recover cuts are deferred to
// Phase 4 (see the t.Skip note at the bottom).
func TestScenario_SingleThreadLifecycle_Engagement(t *testing.T) {
	sc := Scenario{
		Name: "single-thread-lifecycle-engagement",
		Steps: []Step{
			{
				UserInput: "what is the trefoil knot?",
				MockResponse: NewMockResponseWithTag(
					[]string{"*new-topic*"},
					[]string{"trefoil", "knot", "topology", "primer"},
					"The trefoil knot is the simplest non-trivial knot."),
				Annotation: "create thread via *new-topic*",
			},
			{
				UserInput: "what's its bridge number?",
				MockResponse: NewMockResponseWithTag(
					[]string{"thr_1"},
					[]string{"trefoil", "bridge-number", "knot", "invariants"},
					"The trefoil has bridge number 2."),
				Annotation: "engage existing thr_1",
			},
			{
				UserInput: "and the unknotting number?",
				MockResponse: NewMockResponseWithTag(
					[]string{"thr_1"},
					[]string{"trefoil", "unknotting-number", "knot", "invariants"},
					"The unknotting number of the trefoil is 1."),
				Annotation: "second engagement",
			},
			{
				UserInput: "is it chiral?",
				MockResponse: NewMockResponseWithTag(
					[]string{"thr_1"},
					[]string{"trefoil", "chirality", "knot", "topology"},
					"Yes, the trefoil is chiral; the left and right handedness are non-equivalent."),
				Annotation: "third engagement",
			},
			{
				UserInput: "and its genus?",
				MockResponse: NewMockResponseWithTag(
					[]string{"thr_1"},
					[]string{"trefoil", "genus", "knot", "invariants"},
					"The trefoil has Seifert genus 1."),
				Annotation: "fourth engagement; total of 5 turns on thr_1",
			},
		},
		FinalInvariants: append(append([]InvariantCheck{},
			DefaultInvariants...),
			VerifyEngagementConsistency,
			assertThreadCount(1),
			assertThreadTurnCount("thr_1", 5),
			assertHistorySymbolsContain("thr_1", "trefoil", 5),
			assertHistorySymbolsContain("thr_1", "knot", 5),
		),
	}
	RunScenario(t, sc)

	// Retirement / closure portion of the §11.4 lifecycle scenario:
	// /retire and /closure are scheduled for Phase 4 (spec §3.5).
	// Once the closure flow lands, the planned shape is:
	//
	//   - one more Step with UserInput "/retire" (or whatever the
	//     slash-command emits) and MockResponse holding the curator-
	//     drafted summary;
	//   - a FinalInvariant asserting the thread's state == "resolved"
	//     and that VerifyArchiveResolvable passes for its archive
	//     index entry once §3.8 deep-cold lands.
	t.Logf("retirement / archive / recover portion deferred to Phase 4 (spec §3.5, §3.8)")
}

// TestScenario_MultiThreadInterleaving covers §11.4 multi-thread
// interleaving: three threads alive simultaneously with alternating
// engagement, verifying each thread's turn_count exactly matches the
// operation log.
func TestScenario_MultiThreadInterleaving(t *testing.T) {
	// Engagement pattern: 1, 2, 1, 3, 2, 3, 1
	// After: thr_1 = 3 turns, thr_2 = 2 turns, thr_3 = 2 turns.
	steps := []Step{
		{
			UserInput: "tell me about category theory",
			MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
				[]string{"category-theory", "math", "abstract", "primer"},
				"Category theory studies arrows."),
			Annotation: "create thr_1 (category theory)",
		},
		{
			UserInput: "what about lambda calculus?",
			MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
				[]string{"lambda-calculus", "computation", "logic", "primer"},
				"Lambda calculus is a formal system for computation."),
			Annotation: "create thr_2 (lambda calculus)",
		},
		{
			UserInput: "what's a functor?",
			MockResponse: NewMockResponseWithTag([]string{"thr_1"},
				[]string{"category-theory", "functor", "math", "structure"},
				"A functor maps between categories."),
			Annotation: "engage thr_1 (functor)",
		},
		{
			UserInput: "tell me about type theory",
			MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
				[]string{"type-theory", "logic", "computation", "primer"},
				"Type theory studies formal systems of types."),
			Annotation: "create thr_3 (type theory)",
		},
		{
			UserInput: "what is beta reduction?",
			MockResponse: NewMockResponseWithTag([]string{"thr_2"},
				[]string{"lambda-calculus", "beta-reduction", "computation", "evaluation"},
				"Beta reduction substitutes the argument into the function body."),
			Annotation: "engage thr_2 (beta reduction)",
		},
		{
			UserInput: "what about dependent types?",
			MockResponse: NewMockResponseWithTag([]string{"thr_3"},
				[]string{"type-theory", "dependent-types", "logic", "expressivity"},
				"Dependent types allow types to depend on values."),
			Annotation: "engage thr_3 (dependent types)",
		},
		{
			UserInput: "what's a natural transformation?",
			MockResponse: NewMockResponseWithTag([]string{"thr_1"},
				[]string{"category-theory", "natural-transformation", "math", "structure"},
				"A natural transformation maps between functors."),
			Annotation: "engage thr_1 (natural transformation)",
		},
	}

	sc := Scenario{
		Name:  "multi-thread-interleaving",
		Steps: steps,
		FinalInvariants: append(append([]InvariantCheck{},
			DefaultInvariants...),
			VerifyEngagementConsistency,
			assertThreadCount(3),
			assertThreadTurnCount("thr_1", 3),
			assertThreadTurnCount("thr_2", 2),
			assertThreadTurnCount("thr_3", 2),
		),
	}
	RunScenario(t, sc)
}

// TestScenario_ProjectSwitching covers §11.4 project switching:
// engage thr in prj_1, switch active to prj_2, engage thr in prj_2,
// switch back to prj_1, re-engage. Spine entries' project field must
// be correct; per-project bookkeeping must stay isolated.
func TestScenario_ProjectSwitching(t *testing.T) {
	sc := Scenario{
		Name: "project-switching",
		Setup: func(h *Harness) error {
			// Seed prj_2 alongside the harness-default prj_1.
			now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
			meta2 := memops.ProjectMeta{
				ID:               "prj_2",
				Name:             "second",
				CurrentRootPath:  h.Paths.Home + "/p2",
				Created:          now,
				LastActive:       now,
				ConventionsPaths: []string{},
				SymbolPatterns:   []memops.ProjectPattern{},
				IgnoreSymbols:    []string{},
			}
			return store.SaveProjectMeta(h.Paths, meta2)
		},
		Steps: []Step{
			{
				UserInput: "topic A in project 1",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"alpha", "primer", "project-1", "intro"},
					"Topic A in project 1."),
				Annotation: "create thr_1 in prj_1",
			},
			{
				UserInput: "topic B in project 1",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"beta", "primer", "project-1", "intro"},
					"Topic B in project 1."),
				Annotation: "create thr_2 in prj_1",
			},
		},
		FinalInvariants: append(append([]InvariantCheck{},
			DefaultInvariants...),
			VerifyEngagementConsistency,
		),
	}

	// Run the first half (prj_1 engagement) under RunScenario, but we
	// need to drive the project switch + further turns *within* the
	// same harness. The cleanest way without introducing a "run more
	// steps" API is to embed everything in a single Scenario by
	// teaching the harness about a SwitchProject step. For v0.1 we
	// keep RunScenario simple and instead drive this scenario directly
	// via a helper that mirrors RunScenario but allows the project
	// switch mid-sequence.
	runProjectSwitchScenario(t, sc)
}

// runProjectSwitchScenario is a one-off variant of RunScenario that
// drives the project-switch scenario specifically: after Setup and the
// initial Steps, it switches to prj_2, engages a new thread there,
// switches back, and re-engages thr_1. The dedicated helper avoids
// burdening the general-purpose RunScenario API with mid-sequence
// project-switch hooks for a single scenario; once /project switch
// lands in the runtime (Phase 5), the generic RunScenario will drive
// it via a slash-injected Step and this helper goes away.
func runProjectSwitchScenario(t *testing.T, sc Scenario) {
	t.Helper()
	h := newHarness(t, sc)

	// Two synthesized steps run after the user-supplied Setup-projected
	// ones, with a project switch between them.
	additional := []Step{
		{
			UserInput: "topic C in project 2",
			MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
				[]string{"gamma", "primer", "project-2", "intro"},
				"Topic C in project 2."),
			Annotation: "create thr_3 in prj_2 (post-switch)",
		},
		{
			UserInput: "more on topic A in project 1",
			MockResponse: NewMockResponseWithTag([]string{"thr_1"},
				[]string{"alpha", "deep-dive", "project-1", "context"},
				"More on topic A."),
			Annotation: "re-engage thr_1 after switching back to prj_1",
		},
	}
	// The mock holds a single current-response slot; runStep installs
	// each step's response with SetResponse before driving the turn, so
	// a §5.5 mid-turn re-prompt — a second consult within one turn —
	// re-serves that step's response.
	h.Mock = model.NewScriptedMock(nil, nil)
	h.State.Client = h.Mock

	if sc.Setup != nil {
		if err := sc.Setup(h); err != nil {
			t.Fatalf("Setup: %v", err)
		}
	}

	t.Logf("scenario %s: metrics path: %s", sc.Name, h.MetricsPath)

	// Steps 1-2: engage in prj_1.
	for i, step := range sc.Steps {
		runStep(t, h, i, step)
	}

	// Switch to prj_2.
	if err := h.SwitchProject("prj_2"); err != nil {
		t.Fatalf("SwitchProject prj_2: %v", err)
	}

	// Step 3: create thr_3 in prj_2.
	runStep(t, h, len(sc.Steps), additional[0])

	// Confirm the new thread carries prj_2's id.
	rec, found, err := store.FindSpineRecord(h.Paths, "thr_3")
	if err != nil || !found {
		t.Fatalf("find thr_3: err=%v found=%v", err, found)
	}
	if rec.Project != "prj_2" {
		t.Errorf("thr_3 project: got %q want prj_2", rec.Project)
	}

	// Switch back to prj_1.
	if err := h.SwitchProject("prj_1"); err != nil {
		t.Fatalf("SwitchProject prj_1: %v", err)
	}

	// Step 4: re-engage thr_1 (in prj_1).
	runStep(t, h, len(sc.Steps)+1, additional[1])

	// Final invariants + scenario-specific assertions.
	finals := append([]InvariantCheck{}, sc.FinalInvariants...)
	finals = append(finals,
		assertThreadCount(3),
		assertThreadTurnCount("thr_1", 2), // initial + re-engage
		assertThreadTurnCount("thr_2", 1),
		assertThreadTurnCount("thr_3", 1),
		// Project assignment correctness — explicit and load-bearing
		// for §11.4 project-switching.
		func(h *Harness) error {
			cases := map[string]string{"thr_1": "prj_1", "thr_2": "prj_1", "thr_3": "prj_2"}
			for id, want := range cases {
				rec, found, err := store.FindSpineRecord(h.Paths, id)
				if err != nil || !found {
					return fmt.Errorf("project-assignment: find %s: err=%v found=%v", id, err, found)
				}
				if rec.Project != want {
					return fmt.Errorf("project-assignment: %s project got %q want %q", id, rec.Project, want)
				}
			}
			return nil
		},
	)
	// Final poll so the cumulative created/archived sets reflect every
	// event before the final invariants (incl. VerifyThreadAccounting)
	// read them. Each runStep already polls; this is the safety catch.
	if lines, err := h.tailer.poll(); err != nil {
		t.Fatalf("project-switching: final tailer.poll: %v", err)
	} else {
		h.foldEventLines(lines)
	}

	runInvariants(t, h, finals, "final[project-switching]")

	if err := h.Metrics.WriteJSON(h.MetricsPath); err != nil {
		t.Errorf("metrics write: %v", err)
	}
}

// TestScenario_LongHaulHistorySymbolsEvict drives 50 turns on thr_1
// with rotating anchor sets that intentionally exceed the
// history.cap-per-thread default (40). Verifies the cap holds, that
// eviction picks lowest-count entries, and that turn_count tracks the
// operation log exactly.
func TestScenario_LongHaulHistorySymbolsEvict(t *testing.T) {
	const turns = 50
	const historyCap = 40

	steps := make([]Step, 0, turns)

	// First step: create thr_1.
	steps = append(steps, Step{
		UserInput: "long-haul thread starts",
		MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
			[]string{"longhaul", "anchor-a", "anchor-b", "anchor-c"},
			"Long-haul thread initial turn."),
		Annotation: "create thr_1 (long-haul)",
	})

	// Subsequent steps: rotate anchor sets so we encounter ~50 distinct
	// symbols across 50 turns. Each step keeps "longhaul" pinned (to
	// guarantee one high-count survivor) and rotates the other three
	// anchors through a 60-anchor pool.
	for i := 1; i < turns; i++ {
		anchors := []string{
			"longhaul",
			fmt.Sprintf("rot-%03d", i*3),
			fmt.Sprintf("rot-%03d", i*3+1),
			fmt.Sprintf("rot-%03d", i*3+2),
		}
		steps = append(steps, Step{
			UserInput: fmt.Sprintf("turn %d", i+1),
			MockResponse: NewMockResponseWithTag([]string{"thr_1"},
				anchors,
				fmt.Sprintf("Long-haul turn %d body.", i+1)),
			Annotation: fmt.Sprintf("engage thr_1 turn %d", i+1),
		})
	}

	// Per-step invariants are expensive on a 50-turn run (the index
	// rebuild fires every turn). Run the cheap-and-essential subset
	// per-step and the full default suite at the end.
	cheap := []InvariantCheck{
		VerifySpineIntegrity,
		VerifyProjectReferences,
		assertHistorySymbolsCap("thr_1", historyCap),
	}
	for i := range steps {
		steps[i].Invariants = cheap
	}

	sc := Scenario{
		Name:  "long-haul-history-symbols-evict",
		Steps: steps,
		FinalInvariants: append(append([]InvariantCheck{},
			DefaultInvariants...),
			VerifyEngagementConsistency,
			assertThreadCount(1),
			assertThreadTurnCount("thr_1", turns),
			assertHistorySymbolsCap("thr_1", historyCap),
			// The pinned "longhaul" anchor was emitted on every turn;
			// it must survive eviction and carry a count of `turns`.
			assertHistorySymbolsContain("thr_1", "longhaul", turns),
		),
	}
	RunScenario(t, sc)
}

// TestScenarioMetricsBlobShapeIsStable spot-checks the metrics-blob
// JSON shape against the §11.6-derived schema. Cross-version
// comparison (§11.9) depends on this layout being stable, so the
// shape contract is exercised explicitly. A single short scenario
// is sufficient — the schema is the same for every scenario.
func TestScenarioMetricsBlobShapeIsStable(t *testing.T) {
	sc := Scenario{
		Name: "metrics-shape",
		Steps: []Step{
			{
				UserInput: "create",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"shape", "metric", "blob", "stable"},
					"Created."),
			},
			{
				UserInput: "engage",
				MockResponse: NewMockResponseWithTag([]string{"thr_1"},
					[]string{"shape", "metric", "blob", "stable"},
					"Engaged."),
			},
		},
	}
	h := RunScenario(t, sc)

	// RunScenario writes the metrics blob into the persistent
	// test/rundata/<name>/ run home and returns the harness; h.MetricsPath
	// locates it directly.
	body, err := os.ReadFile(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob: %v", err)
	}
	// On request (PERSONANT_METRICS_SAMPLE_PATH set), copy the blob to
	// a stable location so a developer can inspect it post-run.
	// Self-contained — no external scripts required.
	if dst := os.Getenv("PERSONANT_METRICS_SAMPLE_PATH"); dst != "" {
		if werr := os.WriteFile(dst, body, 0o644); werr != nil {
			t.Logf("sample-copy: %v", werr)
		} else {
			t.Logf("sample-copy: wrote %s (%d bytes)", dst, len(body))
		}
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse metrics blob: %v", err)
	}

	for _, key := range []string{"started_at", "ended_at", "labels", "counters", "histograms", "gauges"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("metrics blob missing top-level key %q", key)
		}
	}

	labels, _ := doc["labels"].(map[string]any)
	if labels["scenario"] != "metrics-shape" {
		t.Errorf("labels.scenario: got %v want metrics-shape", labels["scenario"])
	}

	counters, _ := doc["counters"].(map[string]any)
	if counters["turns"] == nil {
		t.Errorf("counters.turns missing")
	}
	if counters["threads_created"] == nil {
		t.Errorf("counters.threads_created missing")
	}
	// Phase C.1 recall-fidelity accounting: every Step is either
	// measured (ExpectedRecallMatches != nil) or unmeasured. The
	// shape test's steps leave the field nil → unmeasured_steps
	// must be present.
	if counters["recall_fidelity_unmeasured_steps"] == nil {
		t.Errorf("counters.recall_fidelity_unmeasured_steps missing")
	}

	histograms, _ := doc["histograms"].(map[string]any)
	if histograms["turn_duration_ms"] == nil {
		t.Errorf("histograms.turn_duration_ms missing")
	}
	if histograms["response_bytes"] == nil {
		t.Errorf("histograms.response_bytes missing")
	}

	gauges, _ := doc["gauges"].(map[string]any)
	if gauges["final_spine_size"] == nil {
		t.Errorf("gauges.final_spine_size missing")
	}
}

// TestScenario_OpportunisticRecall_Surfaces exercises the §3.4
// opportunistic recall mechanism end-to-end across a multi-turn arc.
// Three threads are created with disjoint anchor sets except that
// turn 3's coalesced symbol set overlaps thr_1's anchors above the
// 0.4 Jaccard threshold. The recall matcher (Phase 3.c/3.d) must
// observe thr_1 as a candidate and emit exactly one `spine.match-fire`
// event for it.
//
// Jaccard arithmetic for turn 3 (the only turn that should produce a
// match-fire):
//   - thr_1 anchors: {trefoil, unknot, body-topology, electron-shape}
//   - turn 3 query (after coalesce): user-tags + model-emitted anchors
//     = {trefoil, unknot, body-topology, electron-shape} (user)
//     ∪ {knot-theory, manifold, embedding, topology-extra} (model)
//     = 8 distinct symbols
//   - intersection with thr_1 = 4
//   - union with thr_1 = 8
//   - score = 4/8 = 0.50 ≥ 0.4 ✓
//   - thr_2 anchors disjoint from query → score 0, dropped
//   - thr_3 engaged this turn → excluded via engagedSet
//
// Turns 1 and 2 produce no match-fire (turn 1 has no other threads to
// match against; turn 2's neutrino-flavored anchors don't overlap
// thr_1).
func TestScenario_OpportunisticRecall_Surfaces(t *testing.T) {
	sc := Scenario{
		Name: "opportunistic-recall-surfaces",
		Steps: []Step{
			{
				UserInput: "tell me about topology — #trefoil #unknot #body-topology #electron-shape",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"trefoil", "unknot", "body-topology", "electron-shape"},
					"First pass on topology."),
				Annotation: "create thr_1 (topology)",
			},
			{
				UserInput: "now switching topic: tell me about neutrinos",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"neutrino", "oscillation", "flavor-mixing", "pmns-matrix"},
					"Neutrinos oscillate between flavors."),
				Annotation: "create thr_2 (neutrinos; disjoint from thr_1)",
			},
			{
				UserInput: "going back to topology — what about #trefoil #unknot #body-topology #electron-shape?",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"knot-theory", "manifold", "embedding", "topology-extra"},
					"More on knot theory."),
				Annotation: "create thr_3; user-tags overlap thr_1 anchors → recall match-fire for thr_1",
			},
		},
		FinalInvariants: append(append([]InvariantCheck{},
			DefaultInvariants...),
			VerifyEngagementConsistency,
			assertThreadCount(3),
			// Recall semantics — load-bearing for this scenario.
			//   thr_1: 1 fire (from turn 3)
			//   thr_2: 0 fires (anchors disjoint from every turn's query)
			//   thr_3: 0 fires (created in turn 3; engaged ⇒ excluded that turn)
			//   total: 1 across the whole run
			assertSpineMatchFires(map[string]int{
				"thr_1": 1,
				"thr_2": 0,
				"thr_3": 0,
			}, 1),
		),
	}
	RunScenario(t, sc)
}

// assertThreadHistorySymbolsExclude asserts that none of the given
// normalized symbols appear in the named thread's frontmatter
// history_symbols. Used by the transient-data pollution scenario to
// prove that task-class chaff staged in an earlier turn never crossed
// over into persistent thread state.
func assertThreadHistorySymbolsExclude(threadID string, symbols []string) InvariantCheck {
	return func(h *Harness) error {
		thr, err := store.LoadThread(h.Paths, threadID)
		if err != nil {
			return fmt.Errorf("assertThreadHistorySymbolsExclude: load %s: %w", threadID, err)
		}
		for _, s := range symbols {
			for _, hs := range thr.Meta.HistorySymbols {
				if hs.Normalized == s {
					return fmt.Errorf("assertThreadHistorySymbolsExclude: %s history_symbols contains %q (should have been evicted)",
						threadID, s)
				}
			}
		}
		return nil
	}
}

// assertSymbolIndexExcludes regenerates the persistent derived state
// (symbols.jsonl) and asserts that none of the given normalized
// symbols appear as a record. The regeneration is required because
// the harness does not rebuild the index between operations; the
// invariant call is the synchronization point.
func assertSymbolIndexExcludes(symbols []string) InvariantCheck {
	return func(h *Harness) error {
		if err := h.Ops.RegenerateDerivedState(context.Background(), memops.IndexBuildOptions{Quiet: true}); err != nil {
			return fmt.Errorf("assertSymbolIndexExcludes: RegenerateDerivedState: %w", err)
		}
		records, err := store.ReadSymbols(h.Paths.Symbols)
		if err != nil {
			return fmt.Errorf("assertSymbolIndexExcludes: read symbols.jsonl: %w", err)
		}
		present := make(map[string]struct{}, len(records))
		for _, r := range records {
			present[r.Symbol] = struct{}{}
		}
		for _, s := range symbols {
			if _, found := present[s]; found {
				return fmt.Errorf("assertSymbolIndexExcludes: symbol %q polluted the index (should have been evicted at window-close)", s)
			}
		}
		return nil
	}
}

// TestScenario_TransientDataPollutionPrevention drives a turn where a
// tool.result delta carries substantial noise (file paths the model
// never cites again) alongside a signal symbol (a path the user
// references in the next decision-class delta). The transient-data
// lifecycle (B.1-B.4) must:
//
//   - Stage all the noise + signal symbols from the task delta.
//   - Promote ONLY the signal (the one the user.prompt cites) into
//     the persistent symbol index.
//   - Evict the noise at window-close (3 turns after staging).
//
// After window-close, the persistent symbol index must show no trace
// of the noise — that is what "the index is not polluted by task
// chaff" means concretely.
//
// This is the B.5 narrow scenario: it covers the symbol-pollution
// aspect of transient-data only. The broader shell-capture story is
// still gated on §4.4 + v0.2 dedup (see TransientShellCapture_Stub).
func TestScenario_TransientDataPollutionPrevention(t *testing.T) {
	noise := []string{
		"internal/foo.go",
		"internal/bar.go",
		"internal/baz.go",
		"internal/junk.go",
		"internal/scratch.go",
	}
	const signal = "src/important.go"

	sc := Scenario{
		Name: "transient-data-pollution-prevention",
		Steps: []Step{
			{
				// Turn 1: tool.result pre-event stages signal+noise;
				// user.prompt cites only the signal → promotion of
				// signal into coalesce, noise stays staged with
				// StagedAt=1. Window K=3 means noise is evicted at the
				// top of turn 4.
				PreEvents: []turn.Delta{
					{
						Source: "tool.result",
						Content: "ls returned: internal/foo.go internal/bar.go internal/baz.go " +
							"internal/junk.go internal/scratch.go src/important.go",
					},
				},
				UserInput: "focus on src/important.go for the next task",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"focus", "task", "important", "work"},
					"Looking at src/important.go now."),
				Annotation: "create thr_1 with task pre-event + signal citation",
				Invariants: []InvariantCheck{
					assertThreadCount(1),
					assertHistorySymbolsContain("thr_1", signal, 1),
					assertThreadHistorySymbolsExclude("thr_1", noise),
				},
			},
			{
				// Turn 2: unrelated topic.
				UserInput: "now describe category theory",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"category", "theory", "math", "abstract"},
					"Category theory studies arrows."),
				Annotation: "create thr_2 (unrelated)",
			},
			{
				// Turn 3: unrelated topic. Noise is still staged
				// (StagedAt=1; cutoff at top of turn 3 is 3-3+1=1, not
				// yet aged out).
				UserInput: "what about lambda calculus",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"lambda", "calculus", "computation", "logic"},
					"Lambda calculus is a formal system."),
				Annotation: "create thr_3 (unrelated)",
			},
			{
				// Turn 4: top-of-turn pruneStaging fires with cutoff =
				// 4-3+1 = 2; the StagedAt=1 noise evicts before any
				// chain step in this turn can observe it.
				UserInput: "one more question - what about type theory",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"types", "theory", "formal", "system"},
					"Type theory studies typed expressions."),
				Annotation: "create thr_4; window-close GC evicts noise",
			},
		},
		FinalInvariants: append(append([]InvariantCheck{},
			DefaultInvariants...),
			VerifyEngagementConsistency,
			assertThreadCount(4),
			// The signal must have made it through promotion into
			// thr_1's persistent state.
			assertHistorySymbolsContain("thr_1", signal, 1),
			// No thread may carry noise in its persistent history_symbols.
			assertThreadHistorySymbolsExclude("thr_1", noise),
			assertThreadHistorySymbolsExclude("thr_2", noise),
			assertThreadHistorySymbolsExclude("thr_3", noise),
			assertThreadHistorySymbolsExclude("thr_4", noise),
			// And the persistent inverse index must not list any noise
			// path as a known symbol (window-close evicted them before
			// they could be cited).
			assertSymbolIndexExcludes(noise),
		),
	}
	RunScenario(t, sc)
}

// TestScenario_TransientShellCapture_Stub is a forward-marker test for
// a scenario that v0.1 acceptance (§11.1 six-month simulation) requires
// but which currently cannot run.
//
// The intended scenario:
//
//   - Drive several user.prompt → model.response turns normally.
//   - Between turns (or as part of a turn's chain) inject one or more
//     `user.shell-capture` events carrying 1–2 pages of unique
//     transient content (e.g. simulated `# cat large-paper.md` output)
//     that is referenced exactly once in the immediately following
//     turn and never again.
//   - Assert: the transient content does not appear in any spine
//     record's anchors / summary; does not bloat history_symbols on
//     any thread (transient ≠ anchored); contributes to
//     deterministic-pass extracted symbols when those land (Phase 3);
//     evicts cleanly from working-window layers under budget pressure
//     (Phase 2.e); is replaced by content-addressed identifiers per
//     §3.9 dedup (v0.2).
//
// Why this is skipped today:
//
//   - The runtime's §3.0 chain currently no-ops on `tool.result` and
//     `user.shell-capture` events; only `user.prompt` and
//     `model.response` are processed substantively.
//   - The chat REPL stubs `$` / `#` shell escape with a "not yet
//     implemented" message; the long-lived `$SHELL -i` subprocess
//     specified in §4.4 hasn't landed.
//   - Layer-B/C eviction under budget pressure is a Phase 2.e
//     deliverable; without it, the "evicts cleanly" assertion has
//     nothing to validate.
//   - Working-set dedup (§3.9) is v0.2; the
//     content-addressed-identifier replacement isn't testable until
//     that work lands.
//
// What this test costs while skipped:
//
//   - Visibility. A scenario at this name in this file means a future
//     agent (or future maintainer) inspecting the test surface knows
//     this gap exists and is tracked. The v0.1 acceptance gate at
//     §11.1 will not be honestly green without this scenario
//     activated.
//   - Negative space. When the dependencies land, this test should be
//     filled in (not just have its t.Skip removed) — the scenario
//     details above are the design intent; the implementation needs
//     extending Harness with an event-injection API beyond the
//     current Step{UserInput, MockResponse} shape.
func TestScenario_TransientShellCapture_Stub(t *testing.T) {
	t.Skip("requires §4.4 shell escape + Phase 2.e budget eviction + v0.2 dedup; activate when those land. See test godoc.")
}

// assertLogContains is an InvariantCheck that reads every day-log file
// under h.Paths.LogsDir and fails unless the concatenation contains
// want. Used to assert §3.5 closure log lines fired.
func assertLogContains(want string) InvariantCheck {
	return func(h *Harness) error {
		entries, err := os.ReadDir(h.Paths.LogsDir)
		if err != nil {
			return fmt.Errorf("assertLogContains: read logs dir: %w", err)
		}
		var all strings.Builder
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(h.Paths.LogsDir, e.Name()))
			if err != nil {
				return fmt.Errorf("assertLogContains: read %s: %w", e.Name(), err)
			}
			all.Write(body)
		}
		if !strings.Contains(all.String(), want) {
			return fmt.Errorf("assertLogContains: log missing %q", want)
		}
		return nil
	}
}

// assertThreadRetired is an InvariantCheck asserting the named thread's
// spine state is wantState, its summary is non-empty, and it is no
// longer in Layer B (State.ActiveThreads).
func assertThreadRetired(threadID string, wantState memops.ThreadState) InvariantCheck {
	return func(h *Harness) error {
		rec, found, err := h.Ops.FindThread(context.Background(), threadID)
		if err != nil {
			return fmt.Errorf("assertThreadRetired: find %s: %w", threadID, err)
		}
		if !found {
			return fmt.Errorf("assertThreadRetired: %s not in spine", threadID)
		}
		if rec.State != wantState {
			return fmt.Errorf("assertThreadRetired: %s state %q, want %q", threadID, rec.State, wantState)
		}
		if rec.Summary == "" {
			return fmt.Errorf("assertThreadRetired: %s has empty summary", threadID)
		}
		for _, id := range h.State.ActiveThreads {
			if id == threadID {
				return fmt.Errorf("assertThreadRetired: %s retired but still in ActiveThreads", threadID)
			}
		}
		return nil
	}
}

// seedActiveThread writes an active thread + matching spine record into
// the harness home, with lastEngagedTurn / lastEngaged set so the §3.5
// decay scan can be exercised. Used by the closure scenario's Setup.
func seedActiveThread(h *Harness, threadID string, lastEngagedTurn int, lastEngaged string) error {
	anchors := []string{"alpha", "beta", "gamma", "delta"}
	rec := memops.SpineRecord{
		ID:              threadID,
		Project:         h.Project.ID,
		Anchors:         anchors,
		Summary:         "seeded active thread " + threadID,
		State:           memops.ThreadActive,
		Created:         "2026-04-01T00:00:00Z",
		LastEngaged:     lastEngaged,
		StateChanged:    "2026-04-01T00:00:00Z",
		TurnCount:       1,
		LastEngagedTurn: lastEngagedTurn,
	}
	if err := store.AppendSpineRecord(h.Paths, rec); err != nil {
		return fmt.Errorf("seedActiveThread: append spine: %w", err)
	}
	thr := memops.Thread{
		Meta: memops.ThreadMeta{
			ID:              rec.ID,
			Project:         rec.Project,
			Anchors:         rec.Anchors,
			Summary:         rec.Summary,
			State:           rec.State,
			Created:         rec.Created,
			LastEngaged:     rec.LastEngaged,
			StateChanged:    rec.StateChanged,
			TurnCount:       rec.TurnCount,
			LastEngagedTurn: rec.LastEngagedTurn,
		},
		Body: "# " + threadID + "\n\n## Turn 1 · 2026-04-01T00:00:00Z · [" +
			strings.Join(anchors, ", ") + "]\n\n**user:** seed\n\n**agent:** seed reply\n",
	}
	if err := store.SeedThread(h.Paths, thr); err != nil {
		return fmt.Errorf("seedActiveThread: save thread: %w", err)
	}
	return nil
}

// TestScenario_DecayTriggeredClosure drives §3.5 end-to-end: an active
// thread seeded with an old last-engaged-turn decays as idle turns
// accumulate, the curator drafts a closure summary, the scripted
// ClosureAck retires it, and the thread leaves the working set with a
// non-empty summary.
func TestScenario_DecayTriggeredClosure(t *testing.T) {
	closureInvariants := append(append([]InvariantCheck{},
		DefaultInvariants...),
		VerifyClosedThreadConsistency,
	)

	// scenarioDecayTurns mirrors turn.decayTurns (spec §2.6.1 default,
	// unexported). The seeded thread crosses the turn-based threshold
	// when State.TurnNumber reaches this value.
	const scenarioDecayTurns = 8

	steps := make([]Step, 0, scenarioDecayTurns)
	// Idle turns: the seeded thread (last_engaged_turn=0) is engaged by
	// none of them, so it crosses the turn-based threshold. No
	// ClosureAck on the idle steps before the threshold → closure is
	// detection-disabled for them.
	for i := 0; i < scenarioDecayTurns-1; i++ {
		steps = append(steps, Step{
			UserInput:    fmt.Sprintf("idle chatter %d", i+1),
			MockResponse: model.Response{Content: "Just a plain reply, nothing to engage."},
			Annotation:   fmt.Sprintf("idle turn %d (no decay yet)", i+1),
			Invariants:   closureInvariants,
		})
	}
	// The threshold turn: TurnNumber reaches decayTurns, the seeded
	// thread is decay-eligible, and the scripted ClosureAck retires it.
	steps = append(steps, Step{
		UserInput:    "one more idle turn — should trigger closure",
		MockResponse: model.Response{Content: "Another plain reply."},
		Annotation:   "decay threshold crossed → closure offered + retired",
		ClosureAck:   &ClosureAck{Outcome: turn.ClosureResolved},
		Invariants: append(append([]InvariantCheck{},
			closureInvariants...),
			assertThreadRetired("thr_1", memops.ThreadResolved),
			assertLogContains("retire.complete thr=thr_1 resolution=resolved"),
		),
	})

	sc := Scenario{
		Name: "decay-triggered-closure",
		Setup: func(h *Harness) error {
			return seedActiveThread(h, "thr_1", 0, "2026-04-01T00:00:00Z")
		},
		Steps:           steps,
		FinalInvariants: closureInvariants,
	}
	RunScenario(t, sc)
}

// TestStepSetClock_RefinementNormalizesToPriorClock pins burndown #3: there is
// exactly ONE clock-advance path. A step carrying an absolute At sets
// pinnedClock = At; the execution-time refinement step (zero At, nonzero
// TimeDelta) is NORMALIZED to prior-pinnedClock + TimeDelta — the prior
// EXECUTED instant, not the generator's day-ahead frontier — and then takes
// the same single set path. The refinement's advance is TRANSIENT: the next
// buffered step's absolute At re-anchors the clock to the planned timeline.
func TestStepSetClock_RefinementNormalizesToPriorClock(t *testing.T) {
	h := &Harness{pinnedClock: SimClockStart}

	// Step 1: a normal buffered step carries an absolute At mid-day — the
	// authoritative wire field; the clock slaves to it.
	priorTurn := SimClockStart.Add(10 * time.Hour)
	stepSetClock(h, 0, "buffered turn", Step{At: priorTurn})
	if !h.pinnedClock.Equal(priorTurn) {
		t.Fatalf("absolute At: pinnedClock = %s, want %s", h.pinnedClock, priorTurn)
	}

	// Step 2: the refinement — zero At, only a small rapid-gap TimeDelta. Its
	// At must be derived from the prior EXECUTED instant (pinnedClock), NOT
	// from any generation-frontier value. Assert clock == priorTurn + delta.
	const gap = 42 * time.Second
	stepSetClock(h, 1, "refinement attempt 2", Step{TimeDelta: gap})
	wantRefine := priorTurn.Add(gap)
	if !h.pinnedClock.Equal(wantRefine) {
		t.Fatalf("refinement: pinnedClock = %s, want prior+gap %s", h.pinnedClock, wantRefine)
	}
	// Transience + monotonicity: the refinement advanced only a few seconds and
	// stays the same calendar day, far from a midnight day-close boundary.
	if SimDayIndex(wantRefine) != SimDayIndex(priorTurn) {
		t.Fatalf("refinement crossed a day boundary: day %d → %d",
			SimDayIndex(priorTurn), SimDayIndex(wantRefine))
	}

	// Step 3: the next buffered step carries its own absolute At >= the
	// refinement's instant — re-anchoring the clock to the planned timeline.
	nextTurn := priorTurn.Add(time.Hour)
	if nextTurn.Before(wantRefine) {
		t.Fatalf("test setup: next buffered At %s must be >= refinement %s", nextTurn, wantRefine)
	}
	stepSetClock(h, 2, "next buffered turn", Step{At: nextTurn})
	if !h.pinnedClock.Equal(nextTurn) {
		t.Fatalf("re-anchor: pinnedClock = %s, want %s", h.pinnedClock, nextTurn)
	}
}

// TestScenario_WallClockDecayTriggeredClosure drives §3.5 through the
// wall-clock OR-branch end-to-end: a thread whose turn-idle count stays
// below the turn threshold but whose last_engaged timestamp is older
// than decayTime once Step.TimeDelta advances the pinned clock. This
// exercises the harness's pinnedClock advance and proves the wall-clock
// signal fires through the real turn.Run stack — the turn-based
// scenario above only crosses the turn threshold.
func TestScenario_WallClockDecayTriggeredClosure(t *testing.T) {
	closureInvariants := append(append([]InvariantCheck{},
		DefaultInvariants...),
		VerifyClosedThreadConsistency,
	)

	sc := Scenario{
		Name: "wall-clock-decay-triggered-closure",
		Setup: func(h *Harness) error {
			// Seed thr_1 last-engaged at the harness's pinned-clock start —
			// the Monday-midnight anchor SimClockStart (2026-05-04T00:00:00Z,
			// §3.1) — with last_engaged_turn=0. The sliceSource shim stamps
			// turn 1's Step.At at this anchor.
			if err := seedActiveThread(h, "thr_1", 0, "2026-05-04T00:00:00Z"); err != nil {
				return err
			}
			// Seed thr_2 (a keep-alive) engaged 1h before turn 2's clock.
			// Turn 2 advances the shimmed clock 8 days to 2026-05-12T00:00:00Z,
			// so the keep-alive sits at 2026-05-11T23:00:00Z. After the
			// B5/PRT3-F3 fix wall-clock decay measures idle relative to the
			// system's most-recent activity, not raw calendar time: this
			// keep-alive thread is the system reference, so thr_1's 8-day idle
			// is genuine neglect *while the system was in use*, not a
			// whole-system absence (vacation), and correctly fires. Without it,
			// a lone aged thread looks like a vacation and is suppressed.
			return seedActiveThread(h, "thr_2", 0, "2026-05-11T23:00:00Z")
		},
		Steps: []Step{
			{
				// Turn 1: no time advance, no decay (turn-idle=1, well
				// under the turn threshold; wall-clock not yet aged).
				UserInput:    "warm-up turn, no decay expected",
				MockResponse: model.Response{Content: "A plain reply."},
				Annotation:   "turn 1 — no decay",
				Invariants:   closureInvariants,
			},
			{
				// Turn 2: advance the pinned clock 8 days past the seed
				// (the sliceSource shim integrates this TimeDelta onto the
				// anchor → At 2026-05-12T00:00:00Z). turn-idle is still 2
				// (< turn threshold 8), so only the wall-clock branch (8d >=
				// decayTime 7d) can fire. The scripted ClosureAck retires it.
				UserInput:    "much later — wall-clock decay should trigger closure",
				MockResponse: model.Response{Content: "Another plain reply."},
				Annotation:   "turn 2 — wall-clock decay crossed → closure offered + retired",
				TimeDelta:    8 * 24 * time.Hour,
				ClosureAck:   &ClosureAck{Outcome: turn.ClosureResolved},
				Invariants: append(append([]InvariantCheck{},
					closureInvariants...),
					assertThreadRetired("thr_1", memops.ThreadResolved),
					assertLogContains("retire.prompt thr=thr_1 inactivity=idle="),
					assertLogContains("retire.complete thr=thr_1 resolution=resolved"),
				),
			},
		},
		FinalInvariants: closureInvariants,
	}
	RunScenario(t, sc)
}
