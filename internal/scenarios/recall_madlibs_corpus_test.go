//go:build recall_corpus

// This file is compiled only under the `recall_corpus` build tag. It
// exercises the Wikipedia-corpus recall-fidelity query set, which is
// far larger than the hand-crafted C.2/C.3 set — one isolated scenario
// per query across the whole corpus. That is too heavy for the default
// `make test`; run it via `make recall-corpus-test`
// (go test -tags recall_corpus).
//
// Shared helpers (madlibsDoc, loadMadlibsQueries, runMadlibsQuerySet,
// runMadlibsMetrics, seedMadlibsThreads) live in recall_madlibs_test.go,
// which is compiled in every build.

package scenarios

import (
	"path/filepath"
	"sort"
	"testing"
)

// corpusQueriesPath is the derived query artifact for the
// Wikipedia-corpus templates, produced by `make recall-madlibs` from
// testdata/recall_madlibs/corpus_templates/. .gitignore'd; absent →
// the calling test skips with a regeneration hint.
var corpusQueriesPath = filepath.Join("testdata", "recall_madlibs", "corpus_queries.json")

// TestScenario_RecallMadlibsCorpus drives the full Wikipedia-corpus
// query set through the harness end-to-end — the heavyweight
// counterpart of TestScenario_RecallMadlibs. Corpus templates are
// measure-only, so this records recall_fidelity_adversarial_* and
// never fails on a miss.
func TestScenario_RecallMadlibsCorpus(t *testing.T) {
	runMadlibsQuerySet(t, loadMadlibsQueries(t, corpusQueriesPath))
}

// TestRecallMadlibs_CorpusReport prints per-topic recall-fidelity
// aggregates over the corpus query set. It asserts nothing — corpus
// templates are measure-only by nature — it is a readout: mean
// precision / recall / F1 over each topic's vocabulary-drifted query
// set, the headline number C.6's calibration sweep will later move.
// Run with -v to see the table.
func TestRecallMadlibs_CorpusReport(t *testing.T) {
	doc := loadMadlibsQueries(t, corpusQueriesPath)

	byTopic := map[string][]madlibsQuery{}
	var order []string
	for _, q := range doc.Queries {
		if _, seen := byTopic[q.Topic]; !seen {
			order = append(order, q.Topic)
		}
		byTopic[q.Topic] = append(byTopic[q.Topic], q)
	}
	sort.Strings(order)

	t.Logf("recall-fidelity over Wikipedia-corpus topics (measure-only):")
	t.Logf("  %-24s %6s  %6s  %9s %9s %9s", "topic", "n", "fire", "precision", "recall", "f1")
	for _, topic := range order {
		qs := byTopic[topic]
		var sumP, sumR, sumF float64
		fired := 0
		for _, q := range qs {
			blob := runMadlibsMetrics(t, doc, q)
			sumP += mean(blob.Histograms["recall_fidelity_adversarial_precision"])
			sumR += mean(blob.Histograms["recall_fidelity_adversarial_recall"])
			sumF += mean(blob.Histograms["recall_fidelity_adversarial_f1"])
			if mean(blob.Histograms["recall_fidelity_adversarial_recall"]) > 0 {
				fired++
			}
		}
		n := float64(len(qs))
		t.Logf("  %-24s %6d  %3d/%-3d %9.3f %9.3f %9.3f",
			topic, len(qs), fired, len(qs), sumP/n, sumR/n, sumF/n)
	}
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}
