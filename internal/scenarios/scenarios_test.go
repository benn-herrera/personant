package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/model"
	"personant/internal/store"
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
		for _, hs := range thr.Frontmatter.HistorySymbols {
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

// assertHistorySymbolsCap returns an InvariantCheck asserting the
// thread's history_symbols length never exceeds the spec §2.6.1 cap
// (default 40).
func assertHistorySymbolsCap(threadID string, cap int) InvariantCheck {
	return func(h *Harness) error {
		thr, err := store.LoadThread(h.Paths, threadID)
		if err != nil {
			return fmt.Errorf("assertHistorySymbolsCap: load %s: %w", threadID, err)
		}
		if n := len(thr.Frontmatter.HistorySymbols); n > cap {
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
			meta2 := store.ProjectMeta{
				ID:               "prj_2",
				Name:             "second",
				CurrentRootPath:  h.Paths.Home + "/p2",
				Created:          now,
				LastActive:       now,
				ConventionsPaths: []string{},
				SymbolPatterns:   []store.ProjectPattern{},
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

	// We will append four synthesized steps after the user-supplied
	// Setup-projected ones; build the full mock queue up front so the
	// scripted client matches turn count exactly.
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
	allSteps := append(append([]Step{}, sc.Steps...), additional...)

	// Build the response queue.
	respQueue := make([]model.Response, 0, len(allSteps))
	for _, s := range allSteps {
		respQueue = append(respQueue, s.MockResponse)
	}
	h.Mock = model.NewScriptedMock(respQueue, nil)
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
	RunScenario(t, sc)

	// Locate the metrics JSON. RunScenario writes to <t.TempDir>/<name>.metrics.json.
	// We don't have the harness handle (RunScenario does not return one
	// by design — scenarios are fire-and-assert), so we walk t.TempDir's
	// children looking for the .metrics.json file.
	matches, err := findMetricsBlobs(t)
	if err != nil {
		t.Fatalf("locate metrics blob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no metrics blob found under t.TempDir")
	}

	body, err := os.ReadFile(matches[0])
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

// findMetricsBlobs walks the test's TempDir-parent recursively and
// returns paths to any *.metrics.json files. The harness writes its
// metrics blob to its own t.TempDir() call (different sub-dir than
// this function's t.TempDir() call, but sharing the same parent —
// see Go's testing.TempDir docs), so we walk the shared parent.
func findMetricsBlobs(t *testing.T) ([]string, error) {
	t.Helper()
	root := filepath.Dir(t.TempDir())
	matches := []string{}
	err := filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".metrics.json") {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}
