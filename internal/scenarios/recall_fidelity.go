package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"personant/internal/store"
)

// matchFireCounts walks every <YYYY-MM-DD>.log under paths.LogsDir and
// returns the per-thread total of `spine.match-fire` events. Used by
// the harness to capture pre/post snapshots around a Step so per-step
// recall-fidelity numbers can be computed without instrumenting the
// turn-package surface.
//
// A missing LogsDir returns an empty map and nil error: the eventlog
// only materializes the day file when something is written, so "no
// directory" and "no events" are the same observation.
func matchFireCounts(paths store.PersonantPaths) (map[string]int, error) {
	out := map[string]int{}
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("matchFireCounts: read logs dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("matchFireCounts: read %s: %w", e.Name(), err)
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			_, rest, ok := strings.Cut(line, "spine.match-fire ")
			if !ok {
				continue
			}
			thrID, _, _ := strings.Cut(rest, " ")
			if thrID == "" {
				continue
			}
			out[thrID]++
		}
	}
	return out, nil
}

// logEventThreadSet walks every <YYYY-MM-DD>.log under paths.LogsDir
// and returns the set of thread IDs that appear in lines containing the
// given `<category>.<action> ` marker. The thread ID is taken from the
// first whitespace token after the marker; if idPrefix is non-empty it
// is stripped from that token (e.g. "thr=" → ""). This is the shared
// log-walk used by both the recall-fidelity archival-forgiveness filter
// and the VerifyThreadAccounting invariant.
//
// A missing LogsDir returns an empty set and nil error, matching
// matchFireCounts: the eventlog only materializes a day file when
// something is written.
func logEventThreadSet(paths store.PersonantPaths, marker, idPrefix string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("logEventThreadSet: read logs dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("logEventThreadSet: read %s: %w", e.Name(), err)
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			_, rest, ok := strings.Cut(line, marker)
			if !ok {
				continue
			}
			tok, _, _ := strings.Cut(rest, " ")
			if idPrefix != "" {
				stripped, found := strings.CutPrefix(tok, idPrefix)
				if !found {
					continue
				}
				tok = stripped
			}
			if tok == "" {
				continue
			}
			out[tok] = struct{}{}
		}
	}
	return out, nil
}

// archiveDeletedThreads returns the set of thread IDs that appear in
// `archive.simulated-delete thr=<id> ...` log lines — threads removed
// by the v0.1 deletion-stub archival path and therefore genuinely
// unrecallable.
func archiveDeletedThreads(paths store.PersonantPaths) (map[string]struct{}, error) {
	return logEventThreadSet(paths, "archive.simulated-delete ", "thr=")
}

// createdThreads returns the set of thread IDs that appear in
// `thread.created <id> ...` log lines.
func createdThreads(paths store.PersonantPaths) (map[string]struct{}, error) {
	return logEventThreadSet(paths, "thread.created ", "")
}

// liveSpineThreadSet returns the set of thread IDs currently present on
// the spine.
func liveSpineThreadSet(paths store.PersonantPaths) (map[string]struct{}, error) {
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		return nil, fmt.Errorf("liveSpineThreadSet: read spine: %w", err)
	}
	out := make(map[string]struct{}, len(recs))
	for _, r := range recs {
		out[r.ID] = struct{}{}
	}
	return out, nil
}

// diffMatchFireSet returns the sorted set of thread IDs whose
// match-fire count increased between pre and post. The set semantics
// (one entry per thread regardless of multiple fires within the same
// step) is what recall-fidelity precision/recall is measured against:
// the question is *which threads* were surfaced, not *how many times*.
func diffMatchFireSet(pre, post map[string]int) []string {
	seen := map[string]struct{}{}
	for id, n := range post {
		if n > pre[id] {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// recallFidelity is the per-step measurement of the symbolic Jaccard
// recall layer's behavior on a ground-truth-labeled step.
//
// Given the actual set of thread IDs that fired spine.match-fire and
// the step's declared expected set, compute precision, recall, and F1
// using the conventions:
//
//   - |E| = 0, |A| = 0 → (1, 1, 1) — perfect agreement on "nothing".
//   - |E| = 0, |A| > 0 → (0, 1, 0) — false positives, no true negatives.
//   - |E| > 0, |A| = 0 → (1, 0, 0) — no false positives, all false negatives.
//   - otherwise → TP/|A|, TP/|E|, 2PR/(P+R).
//
// Both inputs MUST be deduplicated. Order is irrelevant.
func recallFidelity(expected, actual []string) (precision, recall, f1 float64) {
	expSet := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		expSet[id] = struct{}{}
	}
	actSet := make(map[string]struct{}, len(actual))
	for _, id := range actual {
		actSet[id] = struct{}{}
	}
	tp := 0
	for id := range actSet {
		if _, ok := expSet[id]; ok {
			tp++
		}
	}
	switch {
	case len(expSet) == 0 && len(actSet) == 0:
		return 1, 1, 1
	case len(expSet) == 0:
		// FP but no FN possible — recall is undefined-but-trivially-met.
		return 0, 1, 0
	case len(actSet) == 0:
		// FN but no FP possible — precision is undefined-but-trivially-met.
		return 1, 0, 0
	}
	precision = float64(tp) / float64(len(actSet))
	recall = float64(tp) / float64(len(expSet))
	if precision+recall == 0 {
		return precision, recall, 0
	}
	f1 = 2 * precision * recall / (precision + recall)
	return precision, recall, f1
}

// recordRecallFidelity is the §9.6 metrics-blob writer for one step's
// recall-fidelity observation. When expected is nil the step is
// unmeasured (counted but no precision/recall/F1 sample).
//
// For a measured step, mode selects the metric series and the
// failure policy:
//
//   - RecallStrict     → clean recall_fidelity_* series; t.Errorf on
//     any mismatch (false positive or negative).
//   - RecallMeasureOnly → recall_fidelity_adversarial_* series; the
//     score is the deliverable, never a failure.
//
// Lives next to its helpers so the harness file stays focused on
// scenario plumbing.
func recordRecallFidelity(t *testing.T, h *Harness, idx int, label string, mode RecallFidelityMode, expected, actual []string) {
	t.Helper()
	if expected == nil {
		h.Metrics.Counter("recall_fidelity_unmeasured_steps", 1)
		return
	}

	// Archival-forgiveness filter: a step's expected set may name a
	// thread that has since been archived (the v0.1 deletion stub),
	// which makes it genuinely unrecallable. Counting such a thread as a
	// recall miss understates the §3.4 algorithm's true performance, so
	// drop it from the expected set before scoring. An expected thread
	// that is off the spine but NOT in the archive-delete log is an
	// unexplained absence — kept as a real miss.
	live, err := liveSpineThreadSet(h.Paths)
	if err != nil {
		t.Fatalf("scenario step %d (%s): recall-fidelity: live spine: %v", idx+1, label, err)
	}
	archived, err := archiveDeletedThreads(h.Paths)
	if err != nil {
		t.Fatalf("scenario step %d (%s): recall-fidelity: archive-delete log: %v", idx+1, label, err)
	}
	var forgiven int64
	kept := make([]string, 0, len(expected))
	for _, id := range expected {
		if _, onSpine := live[id]; onSpine {
			kept = append(kept, id)
			continue
		}
		if _, wasArchived := archived[id]; wasArchived {
			forgiven++
			continue
		}
		kept = append(kept, id)
	}
	if forgiven > 0 {
		h.Metrics.Counter("recall_fidelity_archival_forgiven", forgiven)
	}
	expected = kept

	precision, recall, f1 := recallFidelity(expected, actual)

	if mode == RecallMeasureOnly {
		h.Metrics.Counter("recall_fidelity_adversarial_steps", 1)
		h.Metrics.Record("recall_fidelity_adversarial_precision", precision)
		h.Metrics.Record("recall_fidelity_adversarial_recall", recall)
		h.Metrics.Record("recall_fidelity_adversarial_f1", f1)
		return
	}

	h.Metrics.Counter("recall_fidelity_measured_steps", 1)
	h.Metrics.Record("recall_fidelity_precision", precision)
	h.Metrics.Record("recall_fidelity_recall", recall)
	h.Metrics.Record("recall_fidelity_f1", f1)
	unexpected, missing := recallFidelityMismatch(expected, actual)
	if len(unexpected) == 0 && len(missing) == 0 {
		return
	}
	t.Errorf("scenario step %d (%s): recall-fidelity mismatch: expected=%v actual=%v unexpected=%v missing=%v",
		idx+1, label, expected, actual, unexpected, missing)
}

// recallFidelityMismatch returns the sorted unexpected (false-positive)
// and missing (false-negative) thread ID sets for an assertion message.
// Both slices are nil when the sets agree.
func recallFidelityMismatch(expected, actual []string) (unexpected, missing []string) {
	expSet := make(map[string]struct{}, len(expected))
	for _, id := range expected {
		expSet[id] = struct{}{}
	}
	actSet := make(map[string]struct{}, len(actual))
	for _, id := range actual {
		actSet[id] = struct{}{}
	}
	for id := range actSet {
		if _, ok := expSet[id]; !ok {
			unexpected = append(unexpected, id)
		}
	}
	for id := range expSet {
		if _, ok := actSet[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(missing)
	return unexpected, missing
}
