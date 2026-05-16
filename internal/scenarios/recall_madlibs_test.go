package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/store"
)

// madlibsQueriesPath is the derived query artifact produced by
// testdata/recall_madlibs/generate.py. It is .gitignore'd; `make
// recall-madlibs` (a dependency of `make test`) regenerates it.
var madlibsQueriesPath = filepath.Join("testdata", "recall_madlibs", "queries.json")

type madlibsTopic struct {
	Name    string   `json:"name"`
	Anchors []string `json:"anchors"`
}

type madlibsQuery struct {
	ID        string   `json:"id"`
	Template  string   `json:"template"`
	Topic     string   `json:"topic"`
	Mode      string   `json:"mode"`
	Tags      []string `json:"tags"`
	UserInput string   `json:"user_input"`
}

type madlibsDoc struct {
	Seed    int            `json:"seed"`
	Topics  []madlibsTopic `json:"topics"`
	Queries []madlibsQuery `json:"queries"`
}

// loadMadlibsQueries reads the generated query set. When the artifact
// is absent the calling test is skipped with a regeneration hint —
// `go test ./...` run without `make` degrades gracefully rather than
// hard-failing.
func loadMadlibsQueries(t *testing.T) madlibsDoc {
	t.Helper()
	body, err := os.ReadFile(madlibsQueriesPath)
	if os.IsNotExist(err) {
		t.Skipf("recall-madlibs artifact %s absent — run `make recall-madlibs`", madlibsQueriesPath)
	}
	if err != nil {
		t.Fatalf("read %s: %v", madlibsQueriesPath, err)
	}
	var doc madlibsDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", madlibsQueriesPath, err)
	}
	if len(doc.Topics) == 0 || len(doc.Queries) == 0 {
		t.Fatalf("%s: empty topics or queries", madlibsQueriesPath)
	}
	return doc
}

// TestScenario_RecallMadlibs drives the Phase C.2/C.3 mad-libs query
// set through the harness end-to-end. Each query becomes its own
// isolated scenario: a fresh home seeded with one thread per topic,
// then a single turn carrying the mad-libs query.
//
// Per-query isolation is deliberate. A multi-step scenario would have
// each query's *new-topic* throwaway thread linger as a confounding
// recall target for later queries that sample overlapping cells; a
// fresh home per query keeps the match surface to exactly the
// pristine seeded topics.
//
// Two query modes share this driver:
//
//   - strict (C.2): every column cell is an anchor of the query's
//     topic, so each query clears the §3.4 Jaccard threshold for
//     exactly one topic. The harness fails on any drift.
//   - measure-only (C.3): adversarial templates — vocabulary drift,
//     stop-word leak, false friends — whose queries are expected to
//     under- or mis-fire. ExpectedRecallMatches still names the
//     ground-truth topic, but the harness only records
//     recall_fidelity_adversarial_* and never fails.
func TestScenario_RecallMadlibs(t *testing.T) {
	doc := loadMadlibsQueries(t)

	// Threads are seeded in topic-array order → thr_1, thr_2, ...
	threadID := map[string]string{}
	for i, tp := range doc.Topics {
		threadID[tp.Name] = fmt.Sprintf("thr_%d", i+1)
	}

	for _, q := range doc.Queries {
		target, ok := threadID[q.Topic]
		if !ok {
			t.Errorf("query %s references unknown topic %q", q.ID, q.Topic)
			continue
		}
		mode := RecallStrict
		switch q.Mode {
		case "", "strict":
			mode = RecallStrict
		case "measure-only":
			mode = RecallMeasureOnly
		default:
			t.Errorf("query %s has unknown mode %q", q.ID, q.Mode)
			continue
		}
		t.Run(q.ID, func(t *testing.T) {
			sc := Scenario{
				Name:  "recall-madlibs-" + q.ID,
				Setup: seedMadlibsThreads(t, doc),
				Steps: []Step{
					{
						UserInput: q.UserInput,
						// The throwaway *new-topic* thread is engaged this
						// turn → excluded from recall. Its anchors are the
						// query's own cells, so the turn's coalesced symbol
						// set is exactly those cells.
						MockResponse: NewMockResponseWithTag(
							[]string{"*new-topic*"}, q.Tags,
							"Working from the query terms."),
						Annotation:            q.ID,
						ExpectedRecallMatches: []string{target},
						RecallMode:            mode,
					},
				},
			}
			RunScenario(t, sc)
		})
	}
}

// TestRecallMadlibs_AdversarialBehavior locks in that each C.3
// adversarial template actually probes the failure mode its
// description claims — a guard against a template edit silently
// turning an adversarial probe into a trivially-passing query.
//
// For one representative query per adversarial template it runs the
// scenario, reads the metrics blob, and checks the adversarial
// recall/precision sample:
//
//   - vocabulary drift  → recall 0 (no canonical anchor overlap).
//   - stop-word leak    → recall 0 (union inflated below threshold).
//   - false-friend pair → recall 1 (correct topic always fires);
//                         precision ≤ 1 (spurious twin may fire).
func TestRecallMadlibs_AdversarialBehavior(t *testing.T) {
	doc := loadMadlibsQueries(t)

	want := map[string]struct{ recall, precisionMax float64 }{
		"emulsion-rheology-drift":     {recall: 0, precisionMax: 1},
		"knot-topology-stopword":      {recall: 0, precisionMax: 1},
		"macro-economics-falsefriend": {recall: 1, precisionMax: 1},
		"monetary-policy-falsefriend": {recall: 1, precisionMax: 1},
	}
	seen := map[string]bool{}

	for _, q := range doc.Queries {
		exp, adversarial := want[q.Template]
		if !adversarial || seen[q.Template] {
			continue
		}
		seen[q.Template] = true
		q := q
		t.Run(q.ID, func(t *testing.T) {
			h := runMadlibsMetrics(t, doc, q)
			rec := h.Histograms["recall_fidelity_adversarial_recall"]
			prec := h.Histograms["recall_fidelity_adversarial_precision"]
			if len(rec) != 1 || len(prec) != 1 {
				t.Fatalf("%s: expected one adversarial sample, got recall=%v precision=%v",
					q.ID, rec, prec)
			}
			if rec[0] != exp.recall {
				t.Errorf("%s: adversarial recall got %v want %v", q.ID, rec[0], exp.recall)
			}
			if prec[0] > exp.precisionMax {
				t.Errorf("%s: adversarial precision got %v want ≤ %v", q.ID, prec[0], exp.precisionMax)
			}
			// Adversarial steps must never touch the clean series.
			if c := h.Counters["recall_fidelity_measured_steps"]; c != 0 {
				t.Errorf("%s: adversarial step leaked into clean measured_steps (%d)", q.ID, c)
			}
		})
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("no query found for adversarial template %q", name)
		}
	}
}

// metricsBlob is the subset of the §9.6 metrics JSON the recall-madlibs
// tests inspect.
type metricsBlob struct {
	Counters   map[string]int64     `json:"counters"`
	Histograms map[string][]float64 `json:"histograms"`
}

// runMadlibsMetrics runs one mad-libs query as an isolated scenario and
// returns its metrics blob. MetricsPath is pinned so the blob can be
// read back before t.TempDir is reclaimed.
func runMadlibsMetrics(t *testing.T, doc madlibsDoc, q madlibsQuery) metricsBlob {
	t.Helper()
	threadID := map[string]string{}
	for i, tp := range doc.Topics {
		threadID[tp.Name] = fmt.Sprintf("thr_%d", i+1)
	}
	mPath := filepath.Join(t.TempDir(), q.ID+".metrics.json")
	sc := Scenario{
		Name:        "recall-madlibs-" + q.ID,
		MetricsPath: mPath,
		Setup:       seedMadlibsThreads(t, doc),
		Steps: []Step{{
			UserInput:             q.UserInput,
			MockResponse:          NewMockResponseWithTag([]string{"*new-topic*"}, q.Tags, "Working from the query terms."),
			Annotation:            q.ID,
			ExpectedRecallMatches: []string{threadID[q.Topic]},
			RecallMode:            RecallMeasureOnly,
		}},
	}
	RunScenario(t, sc)

	body, err := os.ReadFile(mPath)
	if err != nil {
		t.Fatalf("read metrics blob %s: %v", mPath, err)
	}
	var blob metricsBlob
	if err := json.Unmarshal(body, &blob); err != nil {
		t.Fatalf("parse metrics blob: %v", err)
	}
	return blob
}

// seedMadlibsThreads returns a Scenario.Setup that writes one seeded
// thread per distinct topic (spine record + thread file), anchor sets
// taken verbatim from the topic. IDs are assigned thr_1, thr_2, … in
// topic-array order so the caller can resolve a topic name to its
// thread ID positionally.
func seedMadlibsThreads(t *testing.T, doc madlibsDoc) func(*Harness) error {
	return func(h *Harness) error {
		ts := "2026-05-01T12:00:00Z"
		for i, tp := range doc.Topics {
			rec := store.SpineRecord{
				ID:           fmt.Sprintf("thr_%d", i+1),
				Project:      h.Project.ID,
				Anchors:      append([]string(nil), tp.Anchors...),
				Summary:      tp.Name + " seed",
				State:        store.ThreadActive,
				Created:      ts,
				LastEngaged:  ts,
				StateChanged: ts,
				TurnCount:    1,
			}
			seedThread(t, h, rec)
		}
		return nil
	}
}
