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

// logTailer is the harness's stateful, incremental reader of the
// <home>/logs/ event-log directory. It tracks a per-file byte offset
// and, on each poll(), returns only the lines appended since the
// previous poll. This replaces the old full-directory walks that ran
// once per step — re-reading all of run-so-far on every step was
// O(N²) in turn count and dominated long-simulation wall time.
//
// The event log is append-only and daily-rotated (<YYYY-MM-DD>.log):
// an existing file only ever grows, and a new day-file may appear
// mid-run when the simulated clock crosses midnight. The tailer
// handles both: a file not yet seen starts at offset 0, and a file
// that has shrunk is treated as corruption (an error, not silent
// mis-parse).
type logTailer struct {
	dir     string
	offsets map[string]int64
}

// newLogTailer builds a tailer over the given logs directory. The
// directory need not exist yet — the eventlog only materializes a day
// file when something is first written.
func newLogTailer(dir string) *logTailer {
	return &logTailer{dir: dir, offsets: map[string]int64{}}
}

// poll reads every <YYYY-MM-DD>.log under the logs directory from its
// stored offset to EOF, advances the offset, and returns the complete
// lines appended since the previous poll (across all files, in
// directory order). A trailing partial line is not expected — poll is
// called between turns when no write is in flight — but a trailing
// empty fragment from a final newline is dropped.
//
// A missing logs directory returns nil, nil: "no directory" and "no
// new events" are the same observation, matching the pre-refactor
// full-walk helpers.
func (lt *logTailer) poll() ([]string, error) {
	entries, err := os.ReadDir(lt.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("logTailer.poll: read logs dir: %w", err)
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		name := e.Name()
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("logTailer.poll: stat %s: %w", name, err)
		}
		off := lt.offsets[name]
		size := info.Size()
		if size < off {
			return nil, fmt.Errorf("logTailer.poll: %s shrank (%d < %d) — append-only log corruption",
				name, size, off)
		}
		if size == off {
			continue
		}
		f, err := os.Open(filepath.Join(lt.dir, name))
		if err != nil {
			return nil, fmt.Errorf("logTailer.poll: open %s: %w", name, err)
		}
		buf := make([]byte, size-off)
		n, err := f.ReadAt(buf, off)
		f.Close()
		if err != nil && n != len(buf) {
			return nil, fmt.Errorf("logTailer.poll: read %s: %w", name, err)
		}
		lt.offsets[name] = size
		for _, line := range strings.Split(string(buf[:n]), "\n") {
			if line == "" {
				continue
			}
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// eventThreadID extracts a thread ID from a log line whose content
// contains the given `<category>.<action> ` marker. The ID is the
// first whitespace token after the marker; if idPrefix is non-empty it
// is stripped from that token (e.g. "thr=" → ""). Returns "", false
// when the line does not carry the marker, the prefix is absent, or
// the token is empty. This is the shared line-parsing primitive used
// by both the per-step match-fire extraction and the cumulative
// created/archived folds.
func eventThreadID(line, marker, idPrefix string) (string, bool) {
	_, rest, ok := strings.Cut(line, marker)
	if !ok {
		return "", false
	}
	tok, _, _ := strings.Cut(rest, " ")
	if idPrefix != "" {
		stripped, found := strings.CutPrefix(tok, idPrefix)
		if !found {
			return "", false
		}
		tok = stripped
	}
	if tok == "" {
		return "", false
	}
	return tok, true
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

// matchFireSet returns the sorted set of thread IDs that have a
// `spine.match-fire` event among the given log lines (typically one
// step's worth, freshly tailed). The set semantics — one entry per
// thread regardless of multiple fires within the same step — is what
// recall-fidelity precision/recall is measured against: the question
// is *which threads* were surfaced, not *how many times*.
func matchFireSet(lines []string) []string {
	seen := map[string]struct{}{}
	for _, line := range lines {
		if id, ok := eventThreadID(line, "spine.match-fire ", ""); ok {
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
	archived := h.archivedThreadIDs
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
