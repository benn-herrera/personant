//go:build recall_corpus

// This file is compiled only under the `recall_corpus` build tag and
// run via `make recall-corpus-test`.
//
// It measures recall fidelity over the full Wikipedia-corpus query set
// by exercising the recall matcher DIRECTLY — `recall.ProposeFromIndex`
// against an in-memory index built once. It does NOT drive the scenario
// harness (fresh home, mock LLM, turn loop, git, per-query re-seed) for
// each query: that integration machinery, multiplied across ~1500
// corpus queries, made the measurement take over an hour and time out.
// The recall matcher is a pure function; measuring it needs only the
// function. End-to-end coverage of the mad-libs path through the real
// turn loop is retained by TestScenario_RecallMadlibs on the smaller
// hand-crafted template set.
//
// Shared helpers (madlibsDoc, loadMadlibsQueries, recallFidelity) live
// in files compiled in every build.

package scenarios

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"personant/internal/metrics"
	"personant/internal/recall"
	"personant/internal/store"
)

// corpusQueriesPath is the derived query artifact for the
// Wikipedia-corpus templates, produced by `make recall-madlibs` from
// testdata/recall_madlibs/corpus_templates/. .gitignore'd; absent →
// the calling test skips with a regeneration hint.
var corpusQueriesPath = filepath.Join("testdata", "recall_madlibs", "corpus_queries.json")

// corpusIndex builds the in-memory recall index for the corpus: one
// thread per topic, seeded with that topic's anchor set — the
// lexically-fixed stored substrate the matcher scans. Built once and
// reused for every query, which is the whole point of the direct-call
// design.
func corpusIndex(doc madlibsDoc) (spine []store.SpineRecord, threads []store.ThreadFrontmatter, threadID map[string]string) {
	threadID = make(map[string]string, len(doc.Topics))
	spine = make([]store.SpineRecord, 0, len(doc.Topics))
	threads = make([]store.ThreadFrontmatter, 0, len(doc.Topics))
	for i, tp := range doc.Topics {
		id := fmt.Sprintf("thr_%d", i+1)
		threadID[tp.Name] = id
		anchors := append([]string(nil), tp.Anchors...)
		spine = append(spine, store.SpineRecord{
			ID:      id,
			Project: "prj_1",
			Anchors: anchors,
			State:   store.ThreadActive,
		})
		threads = append(threads, store.ThreadFrontmatter{
			ID:      id,
			Project: "prj_1",
			Anchors: anchors,
		})
	}
	return spine, threads, threadID
}

// corpusQuerySymbols normalizes a query's mad-libs cells the way the
// turn loop normalizes the equivalent user #tags (SymbolTag category),
// so the direct matcher call sees exactly the symbol set a real turn
// would have coalesced for this query.
func corpusQuerySymbols(q madlibsQuery) []string {
	out := make([]string, 0, len(q.Tags))
	for _, tag := range q.Tags {
		out = append(out, store.Normalize(tag, store.SymbolTag))
	}
	return out
}

// TestRecallMadlibs_CorpusReport measures §3.4 symbolic recall over the
// whole Wikipedia-corpus query set and prints a per-topic
// precision/recall/F1 table. It asserts nothing — corpus templates are
// measure-only — it is the recall-fidelity readout that C.6's
// calibration sweep and §9.9 baseline comparison build on. Run with -v
// to see the table.
func TestRecallMadlibs_CorpusReport(t *testing.T) {
	doc := loadMadlibsQueries(t, corpusQueriesPath)
	spine, threads, threadID := corpusIndex(doc)

	run := metrics.New(map[string]string{"measurement": "recall-fidelity-corpus"})

	type agg struct {
		n, fired             int
		sumP, sumR, sumF     float64
	}
	byTopic := map[string]*agg{}
	var order []string

	for _, q := range doc.Queries {
		target, ok := threadID[q.Topic]
		if !ok {
			t.Errorf("query %s references unknown topic %q", q.ID, q.Topic)
			continue
		}
		cands := recall.ProposeFromIndex(spine, threads, corpusQuerySymbols(q), recall.Options{})
		actual := make([]string, 0, len(cands))
		for _, c := range cands {
			actual = append(actual, c.ThreadID)
		}
		p, r, f := recallFidelity([]string{target}, actual)
		run.Counter("recall_fidelity_corpus_queries", 1)
		run.Record("recall_fidelity_corpus_precision", p)
		run.Record("recall_fidelity_corpus_recall", r)
		run.Record("recall_fidelity_corpus_f1", f)

		a := byTopic[q.Topic]
		if a == nil {
			a = &agg{}
			byTopic[q.Topic] = a
			order = append(order, q.Topic)
		}
		a.n++
		a.sumP += p
		a.sumR += r
		a.sumF += f
		if r > 0 {
			a.fired++
		}
	}
	sort.Strings(order)

	var oN, oFired int
	var oP, oR, oF float64
	for _, a := range byTopic {
		oN += a.n
		oFired += a.fired
		oP += a.sumP
		oR += a.sumR
		oF += a.sumF
	}
	if oN == 0 {
		t.Fatal("no corpus queries measured")
	}

	t.Logf("recall fidelity — %d corpus topics, %d queries (symbolic Jaccard, measure-only):",
		len(order), oN)
	t.Logf("  OVERALL  precision %.3f  recall %.3f  f1 %.3f  fired %d/%d",
		oP/float64(oN), oR/float64(oN), oF/float64(oN), oFired, oN)
	for _, topic := range order {
		a := byTopic[topic]
		n := float64(a.n)
		t.Logf("  %-42s %3d  fired %2d/%-3d  P %.3f  R %.3f  F1 %.3f",
			topic, a.n, a.fired, a.n, a.sumP/n, a.sumR/n, a.sumF/n)
	}

	mPath := filepath.Join(t.TempDir(), "recall-fidelity-corpus.metrics.json")
	if err := run.WriteJSON(mPath); err != nil {
		t.Errorf("metrics write: %v", err)
	}
	t.Logf("metrics blob: %s", mPath)
}
