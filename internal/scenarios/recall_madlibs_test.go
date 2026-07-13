package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

// handcraftedQueriesPath is the derived query artifact for the
// hand-crafted C.2/C.3 templates, produced by `make recall-madlibs`.
// It is .gitignore'd; absent → the calling test skips with a
// regeneration hint rather than hard-failing.
//
// The Wikipedia-corpus query set lives in a separate artifact
// (corpus_queries.json) consumed only by the corpus tests, which
// always compile but skip at runtime unless the corpus opt-in is set
// — see recall_madlibs_corpus_test.go. The split keeps the
// heavyweight corpus run out of the default `make test`.
var handcraftedQueriesPath = filepath.Join("testdata", "recall_madlibs", "queries.json")

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

// loadMadlibsQueries reads a generated query set from path. When the
// artifact is absent the calling test is skipped with a regeneration
// hint — `go test ./...` run without `make` degrades gracefully
// rather than hard-failing.
func loadMadlibsQueries(t *testing.T, path string) madlibsDoc {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skipf("recall-madlibs artifact %s absent — run `make recall-madlibs`", path)
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc madlibsDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Topics) == 0 || len(doc.Queries) == 0 {
		t.Fatalf("%s: empty topics or queries", path)
	}
	return doc
}

// runMadlibsQuerySet drives every query in doc through the harness as
// its own isolated scenario: a fresh home seeded with one thread per
// topic, then a single turn carrying the mad-libs query.
//
// Per-query isolation is deliberate. A multi-step scenario would have
// each query's *new-topic* throwaway thread linger as a confounding
// recall target for later queries that sample overlapping cells; a
// fresh home per query keeps the match surface to exactly the
// pristine seeded topics.
//
// Two query modes share this driver:
//
//   - strict: every column cell is an anchor of the query's topic, so
//     each query clears the §3.4 Jaccard threshold for exactly one
//     topic. The harness fails on any drift.
//   - measure-only: adversarial / corpus templates whose queries are
//     expected to under- or mis-fire. ExpectedRecallMatches still
//     names the ground-truth topic, but the harness only records
//     recall_fidelity_adversarial_* and never fails.
func runMadlibsQuerySet(t *testing.T, doc madlibsDoc) {
	t.Helper()

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
				Setup: seedMadlibsThreads(doc),
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

// TestScenario_RecallMadlibs drives the hand-crafted C.2 (strict) and
// C.3 (adversarial, measure-only) mad-libs query set end-to-end. The
// Wikipedia-corpus set is exercised separately by the build-tagged
// TestScenario_RecallMadlibsCorpus.
func TestScenario_RecallMadlibs(t *testing.T) {
	runMadlibsQuerySet(t, loadMadlibsQueries(t, handcraftedQueriesPath))
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
//     precision ≤ 1 (spurious twin may fire).
func TestRecallMadlibs_AdversarialBehavior(t *testing.T) {
	doc := loadMadlibsQueries(t, handcraftedQueriesPath)

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
			rec := h.Histograms[MetricRecallFidelityAdversarialRecall]
			prec := h.Histograms[MetricRecallFidelityAdversarialPrecision]
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
			if c := h.Counters[MetricRecallFidelityMeasuredSteps]; c != 0 {
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

// metricsBlob is the subset of the §9.4 metrics JSON the recall-madlibs
// tests inspect.
type metricsBlob struct {
	Counters   map[string]int64     `json:"counters"`
	Histograms map[string][]float64 `json:"histograms"`
}

// runMadlibsMetrics runs one mad-libs query as an isolated scenario and
// returns its metrics blob. The metrics blob defaults into the
// persistent test/rundata/<name>/ run home; the returned harness's
// MetricsPath locates it for read-back.
func runMadlibsMetrics(t *testing.T, doc madlibsDoc, q madlibsQuery) metricsBlob {
	t.Helper()
	threadID := map[string]string{}
	for i, tp := range doc.Topics {
		threadID[tp.Name] = fmt.Sprintf("thr_%d", i+1)
	}
	sc := Scenario{
		Name:  "recall-madlibs-" + q.ID,
		Setup: seedMadlibsThreads(doc),
		Steps: []Step{{
			UserInput:             q.UserInput,
			MockResponse:          NewMockResponseWithTag([]string{"*new-topic*"}, q.Tags, "Working from the query terms."),
			Annotation:            q.ID,
			ExpectedRecallMatches: []string{threadID[q.Topic]},
			RecallMode:            RecallMeasureOnly,
		}},
	}
	h := RunScenario(t, sc)

	body, err := os.ReadFile(h.MetricsPath)
	if err != nil {
		t.Fatalf("read metrics blob %s: %v", h.MetricsPath, err)
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
//
// The symbol index is rebuilt exactly once, after every thread is
// written — not once per thread. Per-thread rebuild is O(n²) in topic
// count, which is tolerable for the handful of hand-crafted topics but
// catastrophic for the ~150-topic Wikipedia corpus.
func seedMadlibsThreads(doc madlibsDoc) func(*Harness) error {
	return func(h *Harness) error {
		ts := "2026-05-01T12:00:00Z"
		for i, tp := range doc.Topics {
			rec := memops.SpineRecord{
				ID:           fmt.Sprintf("thr_%d", i+1),
				Project:      h.Project.ID,
				Anchors:      append([]string(nil), tp.Anchors...),
				Summary:      tp.Name + " seed",
				State:        memops.ThreadActive,
				Created:      ts,
				LastEngaged:  ts,
				StateChanged: ts,
				TurnCount:    1,
			}
			if err := store.AppendSpineRecord(h.Paths, rec); err != nil {
				return fmt.Errorf("seedMadlibsThreads: AppendSpineRecord %s: %w", rec.ID, err)
			}
			thr := memops.Thread{
				Meta: memops.ThreadMeta{
					ID:           rec.ID,
					Project:      rec.Project,
					Anchors:      append([]string(nil), rec.Anchors...),
					Summary:      rec.Summary,
					State:        rec.State,
					Created:      rec.Created,
					LastEngaged:  rec.LastEngaged,
					StateChanged: rec.StateChanged,
					TurnCount:    rec.TurnCount,
				},
				Body: "# seed\n",
			}
			if err := store.SeedThread(h.Paths, thr); err != nil {
				return fmt.Errorf("seedMadlibsThreads: SaveThread %s: %w", rec.ID, err)
			}
		}
		return indexRebuild(h.Paths)
	}
}
