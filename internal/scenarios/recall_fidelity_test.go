package scenarios

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"personant/internal/metrics"
	"personant/internal/store"
)

// readCounter writes the run's metrics blob to a temp file and returns the
// named counter (0 if absent). The metrics package exposes no in-memory
// getter; round-tripping through the on-disk schema is the supported read
// path and matches what the §9.4 baseline comparison actually consumes.
func readCounter(t *testing.T, run *metrics.Run, name string) int64 {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.json")
	if err := run.WriteJSON(p); err != nil {
		t.Fatalf("readCounter: write metrics: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("readCounter: read metrics: %v", err)
	}
	var doc struct {
		Counters map[string]int64 `json:"counters"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("readCounter: unmarshal: %v", err)
	}
	return doc.Counters[name]
}

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
	// createdThreadIDs is log-derived; archivedThreadIDs is index-derived
	// (F4/F8). Seed the canonical index so the archive.archived line's
	// refresh has something to read — the line is the REFRESH TRIGGER, not
	// the data source. thr_2 is archived (RecoveredAt empty); thr_3 has a
	// retained breadcrumb but was recovered, so it must be excluded.
	seedArchiveEntry(t, h, "thr_2", "")
	seedArchiveEntry(t, h, "thr_3", "2026-05-02T12:00:00Z")
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

// TestArchivedSet_IndexDerived_DroppedLogLine is the F4 guard: a thread is
// durably archived (canonical index entry written, spine record gone) but its
// archive.archived forensic log line was dropped — the mid-loop eventlog.Log
// failure window in ArchiveThreads. Pre-fix the archived set was built by
// folding that log line, so the missing line under-counted the set and a step
// that expected the archived thread tripped recall_unexplained_absence
// (a false integrity alarm) and VerifyThreadAccounting saw an unexplained
// loss. Post-fix the set derives from the index (refreshed on spine-shrink,
// independent of the log), so both stay clean.
func TestArchivedSet_IndexDerived_DroppedLogLine(t *testing.T) {
	h := invariantHarness(t)
	h.Metrics = metrics.New(nil)

	// thr_1 stays on the spine. thr_2 was created, then durably archived:
	// canonical index entry present (RecoveredAt empty), spine record absent.
	// NO archive.archived log line is folded — that line was "dropped".
	seedThread(t, h, validRecord()) // thr_1
	h.createdThreadIDs["thr_1"] = struct{}{}
	h.createdThreadIDs["thr_2"] = struct{}{}
	seedArchiveEntry(t, h, "thr_2", "") // index entry; refreshArchivedSet runs inside

	// A step that expected to recall thr_2 (now archived) must forgive it as
	// archived-recoverable, NOT count it as an unexplained absence. liveSpine
	// is the in-scope set runStep would have read (#11): thr_1 only.
	recs, err := store.ReadSpine(h.Paths.Spine)
	if err != nil {
		t.Fatalf("live spine: %v", err)
	}
	live := spineIDSet(recs)
	_, forgiven := recordRecallFidelity(t, h, 0, "f4", RecallStrict, []string{"thr_2"}, nil, live)
	if forgiven != 0 {
		t.Errorf("archived expected thread should be forgiven (0 kept); got %d", forgiven)
	}
	if got := readCounter(t, h.Metrics, "recall_unexplained_absence"); got != 0 {
		t.Errorf("recall_unexplained_absence must stay 0 for a durably-archived thread with a dropped log line; got %d", got)
	}
	if got := readCounter(t, h.Metrics, "recall_archived_recoverable"); got != 1 {
		t.Errorf("expected 1 recall_archived_recoverable; got %d", got)
	}

	// Accounting balances: thr_2 is created and archived (index-derived),
	// thr_1 is created and on-spine — disjoint union holds with no log line.
	if err := VerifyThreadAccounting(h); err != nil {
		t.Errorf("VerifyThreadAccounting must balance from the index, not the log: %v", err)
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

// TestRuntimeEmitsMatchFireMarker is the #2 log-marker CONTRACT TEST. The
// harness measures symbolic recall by substring-scraping internal/turn's
// plain-text turn-close log for the markerMatchFire string (fireSetForMarker).
// That coupling is a silent-failure seam: a runtime edit to the log string
// would zero every recall_fidelity_* series AND the gate on it, which then
// passes 0-vs-0 — a parse bug masquerading as a recall collapse (a ghost
// regression).
//
// This test pins the contract directly. It drives a real turn that the runtime
// recalls a peer thread on, then asserts the literal markerMatchFire substring
// — exactly the bytes fireSetForMarker scrapes — is present in the raw
// event-log files. If a future runtime change renames the marker, this fails
// loudly with the actual log content, instead of letting the recall series go
// silently dark. It deliberately reads the raw bytes (not matchFireSet) so the
// assertion is on the literal contract string, not on the parser that consumes
// it.
func TestRuntimeEmitsMatchFireMarker(t *testing.T) {
	sc := Scenario{
		Name: "match-fire-marker-contract",
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
				Annotation:            "create thr_3; user-tags overlap thr_1 → runtime fires spine.match-fire",
				ExpectedRecallMatches: []string{"thr_1"},
			},
		},
	}
	h := RunScenario(t, sc)

	// Read the raw event-log bytes and assert the literal contract marker is
	// present. The scrape strips the trailing space when cutting tokens, so
	// the contract is the full markerMatchFire constant including it.
	entries, err := os.ReadDir(h.Paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	var combined strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(h.Paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		combined.Write(body)
	}
	log := combined.String()
	if !strings.Contains(log, markerMatchFire) {
		t.Fatalf("runtime did not emit the harness-scraped marker %q — the recall-measurement contract is broken (ghost-regression seam); event log:\n%s",
			markerMatchFire, log)
	}
	// And the parser the harness actually uses agrees: thr_1 is in the scraped
	// set. This ties the literal-string assertion to fireSetForMarker so a
	// change that satisfies one but not the other still fails.
	if got := fireSetForMarker(strings.Split(log, "\n"), markerMatchFire); !contains(got, "thr_1") {
		t.Fatalf("fireSetForMarker did not extract thr_1 from the runtime log; got %v", got)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
