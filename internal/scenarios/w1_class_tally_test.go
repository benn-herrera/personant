package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/metrics"
)

// TestW1ClassTally_SurvivesRecallerSwap is the #111 regression guard: the W1
// classification tally the harness accumulates from the per-probe class must
// reach the SAME surface the sim reads (the recall_intra_w1_* COUNTERS in the
// metrics blob), and must SUM across a recaller swap.
//
// The bug it guards: the run-total tally was read post-run from the live
// measure.Service's atomics. Because a RestartSession step rebuilds the Service
// per session (each fresh instance zeroes its W1 atomics), a strict-miss
// classified on an earlier instance — e.g. the run-end max-history probe before
// the final-session restart — read as 0 from the final instance, even though
// recall_intra_descent_divergence (accumulated at the harness call site) was
// nonzero. The two metrics disagreed.
//
// The fix accumulates the class at the same call site and moment as the
// divergence counter, into the harness-held metrics.Run, which is one object
// for the whole run regardless of how many Services it created and closed. This
// test simulates that instance churn by feeding per-probe classes "from
// instance A" and then "from instance B" (a recaller swap between them) into one
// harness Metrics, and asserts the blob the sim reads reflects every probe.
func TestW1ClassTally_SurvivesRecallerSwap(t *testing.T) {
	h := &Harness{Metrics: metrics.New(nil)}

	// Probes on the first Service instance (pre-restart): one strict-miss, one
	// tie, plus a non-divergent probe (empty class — bumps nothing).
	recordW1Class(h, w1ClassStrictMiss)
	recordW1Class(h, w1ClassTie)
	recordW1Class(h, "") // div==0 probe: must not bump any bucket

	// --- RestartSession here: the recaller is rebuilt. A post-run read of the
	// NEW Service's atomics would see only what follows; the harness counter
	// must retain what preceded. ---

	// Probes on the second Service instance (post-restart, max history): the
	// run-end strict-miss the original bug dropped, plus a tree-mismatch.
	recordW1Class(h, w1ClassStrictMiss)
	recordW1Class(h, w1ClassTreeMismatch)

	// Read back through the exact surface the sim uses: write the blob and
	// decode its Counters (sim's readMetrics → m.Counters[...]).
	blob := filepath.Join(t.TempDir(), "metrics.json")
	if err := h.Metrics.WriteJSON(blob); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
	body, err := os.ReadFile(blob)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	var doc struct {
		Counters map[string]int64 `json:"counters"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}

	// Both strict-misses must be present — the pre-restart one is exactly what
	// a final-instance atomic read dropped.
	if got := doc.Counters[metricRecallIntraW1StrictMiss]; got != 2 {
		t.Errorf("%s = %d, want 2 (both pre- and post-swap strict-misses must survive the recaller swap)",
			metricRecallIntraW1StrictMiss, got)
	}
	if got := doc.Counters[metricRecallIntraW1Tie]; got != 1 {
		t.Errorf("%s = %d, want 1", metricRecallIntraW1Tie, got)
	}
	if got := doc.Counters[metricRecallIntraW1TreeMismatch]; got != 1 {
		t.Errorf("%s = %d, want 1", metricRecallIntraW1TreeMismatch, got)
	}
}
