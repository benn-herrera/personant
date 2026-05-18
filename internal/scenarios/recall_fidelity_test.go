package scenarios

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// writeLogFile drops a day-log file with the given body under
// paths.LogsDir so the log-walk helpers can be exercised without
// driving the eventlog.
func writeLogFile(t *testing.T, h *Harness, body string) {
	t.Helper()
	if err := os.MkdirAll(h.Paths.LogsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	p := filepath.Join(h.Paths.LogsDir, "2026-05-18.log")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestLogEventThreadSet(t *testing.T) {
	h := invariantHarness(t)
	writeLogFile(t, h, "ts thread.created thr_1 anchors=4 project=prj_1\n"+
		"ts thread.created thr_2 anchors=4 project=prj_1\n"+
		"ts archive.simulated-delete thr=thr_2 project=prj_1 bytes=512\n"+
		"ts spine.match-fire thr_1 score=0.5\n"+
		"\n")

	created, err := createdThreads(h.Paths)
	if err != nil {
		t.Fatalf("createdThreads: %v", err)
	}
	if got, want := sortedKeys(created), []string{"thr_1", "thr_2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("createdThreads: got %v want %v", got, want)
	}

	archived, err := archiveDeletedThreads(h.Paths)
	if err != nil {
		t.Fatalf("archiveDeletedThreads: %v", err)
	}
	if got, want := sortedKeys(archived), []string{"thr_2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("archiveDeletedThreads: got %v want %v", got, want)
	}
}

func TestLogEventThreadSet_MissingLogsDir(t *testing.T) {
	h := invariantHarness(t)
	// invariantHarness does not write any log file; LogsDir may not exist.
	got, err := archiveDeletedThreads(h.Paths)
	if err != nil {
		t.Fatalf("expected nil error for missing logs dir, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty set, got %v", got)
	}
}

func TestRecallFidelity_EdgeCases(t *testing.T) {
	cases := []struct {
		name             string
		expected, actual []string
		p, r, f1         float64
	}{
		{"both-empty", nil, nil, 1, 1, 1},
		{"expected-empty-actual-nonempty", []string{}, []string{"thr_1"}, 0, 1, 0},
		{"expected-nonempty-actual-empty", []string{"thr_1"}, nil, 1, 0, 0},
		{"exact-match-singleton", []string{"thr_1"}, []string{"thr_1"}, 1, 1, 1},
		{"exact-match-multi", []string{"thr_1", "thr_2"}, []string{"thr_2", "thr_1"}, 1, 1, 1},
		{"one-fp-one-fn", []string{"thr_1"}, []string{"thr_2"}, 0, 0, 0},
		// E = {thr_1, thr_2, thr_3}, A = {thr_1, thr_2, thr_4}
		// TP=2, |A|=3, |E|=3 → P=2/3, R=2/3, F1=2/3
		{"mixed-tp-fp-fn", []string{"thr_1", "thr_2", "thr_3"}, []string{"thr_1", "thr_2", "thr_4"}, 2.0 / 3.0, 2.0 / 3.0, 2.0 / 3.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, r, f1 := recallFidelity(c.expected, c.actual)
			if !approxEqual(p, c.p) || !approxEqual(r, c.r) || !approxEqual(f1, c.f1) {
				t.Errorf("recallFidelity(%v, %v): got (%.4f, %.4f, %.4f) want (%.4f, %.4f, %.4f)",
					c.expected, c.actual, p, r, f1, c.p, c.r, c.f1)
			}
		})
	}
}

func TestRecallFidelity_Mismatch(t *testing.T) {
	cases := []struct {
		name                string
		expected, actual    []string
		wantUnexp, wantMiss []string
	}{
		{"exact-match", []string{"thr_1", "thr_2"}, []string{"thr_2", "thr_1"}, nil, nil},
		{"false-positive", []string{"thr_1"}, []string{"thr_1", "thr_9"}, []string{"thr_9"}, nil},
		{"false-negative", []string{"thr_1", "thr_2"}, []string{"thr_1"}, nil, []string{"thr_2"}},
		{"both", []string{"thr_1", "thr_2"}, []string{"thr_2", "thr_3"}, []string{"thr_3"}, []string{"thr_1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, m := recallFidelityMismatch(c.expected, c.actual)
			if !reflect.DeepEqual(u, c.wantUnexp) {
				t.Errorf("unexpected: got %v want %v", u, c.wantUnexp)
			}
			if !reflect.DeepEqual(m, c.wantMiss) {
				t.Errorf("missing: got %v want %v", m, c.wantMiss)
			}
		})
	}
}

func TestDiffMatchFireSet(t *testing.T) {
	pre := map[string]int{"thr_1": 2, "thr_2": 1}
	post := map[string]int{"thr_1": 3, "thr_2": 1, "thr_5": 1}
	got := diffMatchFireSet(pre, post)
	want := []string{"thr_1", "thr_5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diffMatchFireSet: got %v want %v", got, want)
	}
}

// TestScenario_RecallFidelity_Smoke exercises the Phase C.1 harness
// surface end-to-end: three steps with explicit ExpectedRecallMatches.
// Mirrors TestScenario_OpportunisticRecall_Surfaces but uses the new
// per-step ground-truth field instead of a global match-fire invariant.
func TestScenario_RecallFidelity_Smoke(t *testing.T) {
	sc := Scenario{
		Name: "recall-fidelity-smoke",
		Steps: []Step{
			{
				UserInput: "tell me about topology — #trefoil #unknot #body-topology #electron-shape",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"trefoil", "unknot", "body-topology", "electron-shape"},
					"First pass on topology."),
				Annotation:            "create thr_1; no peer threads → no match-fire",
				ExpectedRecallMatches: []string{},
			},
			{
				UserInput: "now switching topic: tell me about neutrinos",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"neutrino", "oscillation", "flavor-mixing", "pmns-matrix"},
					"Neutrinos oscillate between flavors."),
				Annotation:            "create thr_2; disjoint anchors → no match-fire",
				ExpectedRecallMatches: []string{},
			},
			{
				UserInput: "going back to topology — what about #trefoil #unknot #body-topology #electron-shape?",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"knot-theory", "manifold", "embedding", "topology-extra"},
					"More on knot theory."),
				Annotation:            "create thr_3; user-tags overlap thr_1 → expect match-fire on thr_1",
				ExpectedRecallMatches: []string{"thr_1"},
			},
		},
		FinalInvariants: append(append([]InvariantCheck{},
			DefaultInvariants...),
			assertThreadCount(3),
		),
	}
	RunScenario(t, sc)
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
