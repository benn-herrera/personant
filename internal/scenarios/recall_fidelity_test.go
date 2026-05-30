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

func TestFoldEventLines(t *testing.T) {
	h := invariantHarness(t)
	h.foldEventLines([]string{
		"ts thread.created thr_1 anchors=4 project=prj_1",
		"ts thread.created thr_2 anchors=4 project=prj_1",
		"ts archive.archived thr=thr_2 project=prj_1 bytes=512",
		"ts spine.match-fire thr_1 score=0.5",
		"",
	})
	if got, want := sortedKeys(h.createdThreadIDs), []string{"thr_1", "thr_2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("createdThreadIDs: got %v want %v", got, want)
	}
	if got, want := sortedKeys(h.archivedThreadIDs), []string{"thr_2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("archivedThreadIDs: got %v want %v", got, want)
	}
}

// TestLogTailer_MissingLogsDir confirms a tailer over a non-existent
// logs directory returns nothing and no error — "no directory" and
// "no events" are the same observation.
func TestLogTailer_MissingLogsDir(t *testing.T) {
	h := invariantHarness(t)
	// invariantHarness does not write any log file; LogsDir may not exist.
	lines, err := h.tailer.poll()
	if err != nil {
		t.Fatalf("expected nil error for missing logs dir, got %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected no lines, got %v", lines)
	}
}

// TestLogTailer_Incremental is the focused correctness test for the
// O(N²) fix: it appends lines across two day-files between polls and
// asserts each poll returns exactly the lines appended since the
// previous one — including a fresh day-file appearing mid-run.
func TestLogTailer_Incremental(t *testing.T) {
	h := invariantHarness(t)
	if err := os.MkdirAll(h.Paths.LogsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	dayA := filepath.Join(h.Paths.LogsDir, "2026-05-18.log")
	dayB := filepath.Join(h.Paths.LogsDir, "2026-05-19.log")

	append := func(t *testing.T, path, body string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		if _, err := f.WriteString(body); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		f.Close()
	}
	poll := func(t *testing.T) []string {
		t.Helper()
		lines, err := h.tailer.poll()
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		return lines
	}

	// Poll 1: one file, two lines.
	append(t, dayA, "l1\nl2\n")
	if got, want := poll(t), []string{"l1", "l2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("poll 1: got %v want %v", got, want)
	}

	// Poll 2: same file grew by one line — only the new line returns.
	append(t, dayA, "l3\n")
	if got, want := poll(t), []string{"l3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("poll 2: got %v want %v", got, want)
	}

	// Poll 3: nothing appended anywhere.
	if got := poll(t); len(got) != 0 {
		t.Fatalf("poll 3: expected no lines, got %v", got)
	}

	// Poll 4: a fresh day-file appears mid-run (simulated midnight) and
	// the old file also grows — both deltas come back, old day first.
	append(t, dayA, "l4\n")
	append(t, dayB, "m1\nm2\n")
	if got, want := poll(t), []string{"l4", "m1", "m2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("poll 4: got %v want %v", got, want)
	}

	// Poll 5: a shrinking file is append-only corruption — must error.
	if err := os.WriteFile(dayA, []byte("short\n"), 0o644); err != nil {
		t.Fatalf("truncate dayA: %v", err)
	}
	if _, err := h.tailer.poll(); err == nil {
		t.Fatal("poll 5: expected corruption error on shrunk file, got nil")
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

func TestMatchFireSet(t *testing.T) {
	// One step's worth of tailed lines: two distinct threads fire (one
	// twice — set semantics dedup it), plus unrelated non-match lines.
	lines := []string{
		"ts thread.created thr_1 anchors=4 project=prj_1",
		"ts spine.match-fire thr_1 score=0.5",
		"ts spine.match-fire thr_5 score=0.6",
		"ts spine.match-fire thr_1 score=0.7",
		"ts archive.archived thr=thr_9 project=prj_1 bytes=1",
	}
	got := matchFireSet(lines)
	want := []string{"thr_1", "thr_5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("matchFireSet: got %v want %v", got, want)
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
