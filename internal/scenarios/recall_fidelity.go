package scenarios

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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

// The three `spine.*-match-fire ` log markers the harness scrapes to
// reconstruct each recall layer's observed match set. These are the
// contract with internal/turn's turn-close logging — a runtime edit to
// any of these strings silently zeros the corresponding recall series
// (#2). markerMatchFire is the symbolic Jaccard layer; markerEmbedMatchFire
// the layer-2 embedding-cosine analogue; markerIntraMatchFire the §7
// intra-thread (fine-tier) analogue. TestRuntimeEmitsMatchFireMarker pins
// the symbolic marker against a real turn so a string drift fails loudly.
const (
	markerMatchFire      = "spine.match-fire "
	markerEmbedMatchFire = "spine.embed-match-fire "
	markerIntraMatchFire = "spine.intra-match-fire "
)

// fireSetForMarker returns the sorted set of thread IDs that have an event
// carrying the given marker among the supplied log lines (typically one
// step's worth, freshly tailed). The set semantics — one entry per thread
// regardless of multiple fires within the same step — is what recall-fidelity
// precision/recall is measured against: the question is *which threads* were
// surfaced, not *how many times*. The three recall layers differ only in the
// marker string (markerMatchFire / markerEmbedMatchFire / markerIntraMatchFire).
func fireSetForMarker(lines []string, marker string) []string {
	seen := map[string]struct{}{}
	for _, line := range lines {
		if id, ok := eventThreadID(line, marker, ""); ok {
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

// matchFireSet returns the symbolic Jaccard layer's match set (markerMatchFire).
func matchFireSet(lines []string) []string { return fireSetForMarker(lines, markerMatchFire) }

// embedMatchFireSet returns the layer-2 embedding-cosine match set
// (markerEmbedMatchFire). The runtime logs this line per embedding candidate
// at turn close whenever an embedder is installed (turn/recall.go), so on an
// embedding-in-loop run it is the observed embedding recall set scored
// head-to-head against the SAME expected set the symbolic layer is scored
// against (#98).
func embedMatchFireSet(lines []string) []string {
	return fireSetForMarker(lines, markerEmbedMatchFire)
}

// intraMatchFireSet returns the §7 intra-thread (fine-tier) match set
// (markerIntraMatchFire). The runtime logs this line at turn close whenever
// the engaged thread's scrolled-out early content matched the query
// (turn/recall.go), so on an embedding-in-loop run it is the observed
// intra-thread recall set the §8.2 shadow-chunk oracle scores per hop. Empty
// on the symbolic-only default (no embedder → the intra pass never fires).
func intraMatchFireSet(lines []string) []string {
	return fireSetForMarker(lines, markerIntraMatchFire)
}

// recordEmbedRecallFidelity records the embedding layer's per-step
// precision/recall/F1 into the parallel embed_recall_fidelity_* series
// (#98). It mirrors recordRecallFidelity's scoring but against the
// embedding match set, and never asserts — the embedding layer is the
// gap-closure measurement, not a gate.
//
// keptExpected is the SAME archival-forgiven expected slice the symbolic
// path scored against (recordRecallFidelity's `kept`), so the head-to-head
// is exactly apples-to-apples: both layers face one forgiven ground truth on
// one workload. A nil keptExpected is the unmeasured step (no ground truth).
func recordEmbedRecallFidelity(h *Harness, keptExpected, actual []string, measured bool) {
	if !measured {
		h.Metrics.Counter("embed_recall_fidelity_unmeasured_steps", 1)
		return
	}
	precision, recall, f1 := recallFidelity(keptExpected, actual)
	h.Metrics.Counter(MetricEmbedRecallFidelitySteps, 1)
	h.Metrics.Record(MetricEmbedRecallFidelityPrecision, precision)
	h.Metrics.Record(MetricEmbedRecallFidelityRecall, recall)
	h.Metrics.Record(MetricEmbedRecallFidelityF1, f1)
}

// recallFidelity is the per-step measurement of the symbolic Jaccard
// recall layer's behavior on a ground-truth-labeled step.
//
// Truth-in-labeling (sim-vs-reality MAD T0-1): the precision/recall/F1
// this computes reflect ONLY the symbolic Jaccard layer. The acceptance
// run uses a nil embedder, and the step's expected set is derived from
// slot-tag equality — the same signal Jaccard keys on. The headline
// figure is therefore reported as "symbolic-only recall", not bare
// "recall", until embedding recall is measured (T2-1/T3-1). The
// `recall_fidelity_*` metric keys keep their names for backward-
// compatible parsing; the labeling is enforced at the report strings.
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
	return recallFidelityFromSets(toSet(expected), toSet(actual))
}

// toSet builds a membership set from a slice of thread IDs.
func toSet(ids []string) map[string]struct{} {
	s := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

// recallFidelityFromSets is the set-based core of recallFidelity: the caller
// supplies the expected/actual membership sets so a site that also needs the
// mismatch breakdown (recordRecallFidelity) builds each set exactly once and
// passes it to both this and recallFidelityMismatchFromSets, rather than
// rebuilding the same two sets twice (#2).
func recallFidelityFromSets(expSet, actSet map[string]struct{}) (precision, recall, f1 float64) {
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

// recoverability classifies an expected/probed thread ID against the live
// recall surface, the shared verdict both recall-measurement sites apply
// (#7). The three cases mirror VerifyThreadAccounting's disjoint union:
type recoverability int

const (
	// recovOnSpine: the ID is on the live spine — a real, in-scope recall
	// target (counts as recoverable / kept).
	recovOnSpine recoverability = iota
	// recovArchived: off the live spine but in the canonical archive set —
	// preserved + recoverable via explicit fetch, off the v0.1 LIVE recall
	// surface. Forgiven: dropped from the scored expected set, not a miss.
	recovArchived
	// recovUnexplained: off the live spine and NOT archived — a genuine
	// integrity bug (the recall-measurement sibling of VerifyThreadAccounting's
	// "unexplained loss"), counted via recall_unexplained_absence and
	// conservatively treated as a real miss. A HARD ==0 gate in the sim (#100).
	recovUnexplained
)

// classifyRecoverability is the single source of truth for the
// archival-forgiveness rule (#7/#97/#100/#101): given a thread ID and the
// live-spine + archived sets, decide whether it is on-spine, forgivably
// archived, or an unexplained absence. Callers own the counter/return-shape
// side effects (the two sites differ: one batches two counters, the other
// bumps one inline and returns a bool), but the classification rule lives
// here so it can never drift between them.
func classifyRecoverability(id string, liveSpine, archived map[string]struct{}) recoverability {
	if _, onSpine := liveSpine[id]; onSpine {
		return recovOnSpine
	}
	if _, wasArchived := archived[id]; wasArchived {
		return recovArchived
	}
	return recovUnexplained
}

// recordRecallFidelity is the §9.4 metrics-blob writer for one step's
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
// recordRecallFidelity returns the FORGIVEN expected set — the expected
// matches that survive the archival/absent-from-spine filter below (kept) —
// and its count. The caller threads the count into
// StepFeedback.RecallExpectedForgiven so the recall-episode hit/miss
// counter forgives archived expectations exactly as this F1/precision path
// does, and feeds the kept slice to recordEmbedRecallFidelity so the
// embedding head-to-head scores the identical forgiven ground truth (#98).
// An unmeasured step (expected == nil) returns (nil, 0).
//
// liveSpine is the in-scope live-spine ID set runStep already read this turn
// (#11): passing it in avoids a redundant per-step O(threads) ReadSpine that
// re-derived a value already in hand.
func recordRecallFidelity(t *testing.T, h *Harness, idx int, label string, mode RecallFidelityMode, expected, actual []string, liveSpine map[string]struct{}) ([]string, int) {
	t.Helper()
	if expected == nil {
		h.Metrics.Counter("recall_fidelity_unmeasured_steps", 1)
		return nil, 0
	}

	// Archival-recoverability filter: a step's expected set may name a
	// thread that has since been archived (the §3.8 recoverable git-based
	// archival path), which takes it OFF the live recall surface for v0.1
	// (it stays preserved + recoverable via explicit fetch). Counting such
	// a thread as a recall miss understates the §3.4 algorithm's true
	// performance, so drop it from the expected set before scoring an
	// inherently-LIVE recall score. An expected thread that is off the
	// spine but NOT in the archive.archived log is an unexplained absence —
	// a genuine integrity bug, kept as a real miss and counted via
	// recall_unexplained_absence. The verdict comes from the shared
	// classifyRecoverability rule (#7) so it can never drift from runStep's
	// probe-side recoverable closure.
	archived := h.archivedThreadIDs
	var archivedRecoverable, unexplainedAbsent int64
	kept := make([]string, 0, len(expected))
	for _, id := range expected {
		switch classifyRecoverability(id, liveSpine, archived) {
		case recovOnSpine:
			kept = append(kept, id)
		case recovArchived:
			archivedRecoverable++
		case recovUnexplained:
			unexplainedAbsent++
			kept = append(kept, id)
		}
	}
	if archivedRecoverable > 0 {
		h.Metrics.Counter("recall_archived_recoverable", archivedRecoverable)
	}
	if unexplainedAbsent > 0 {
		h.Metrics.Counter(MetricRecallUnexplainedAbsence, unexplainedAbsent)
	}
	expected = kept
	// Forgiven expected count: what remains recoverable after archival/
	// absent filtering. Returned to the caller so the recall-episode
	// counter scores this step with the same forgiveness as the F1 path.
	expectedForgiven := len(kept)

	// Build the expected/actual membership sets ONCE and thread them through
	// both the score and (strict path) the mismatch breakdown, rather than
	// each helper rebuilding the same two sets (#2).
	expSet, actSet := toSet(expected), toSet(actual)
	precision, recall, f1 := recallFidelityFromSets(expSet, actSet)

	if mode == RecallMeasureOnly {
		h.Metrics.Counter(MetricRecallFidelityAdversarialSteps, 1)
		h.Metrics.Record(MetricRecallFidelityAdversarialPrecision, precision)
		h.Metrics.Record(MetricRecallFidelityAdversarialRecall, recall)
		h.Metrics.Record(MetricRecallFidelityAdversarialF1, f1)
		return expected, expectedForgiven
	}

	h.Metrics.Counter(MetricRecallFidelityMeasuredSteps, 1)
	h.Metrics.Record("recall_fidelity_precision", precision)
	h.Metrics.Record("recall_fidelity_recall", recall)
	h.Metrics.Record("recall_fidelity_f1", f1)
	unexpected, missing := recallFidelityMismatchFromSets(expSet, actSet)
	if len(unexpected) == 0 && len(missing) == 0 {
		return expected, expectedForgiven
	}
	t.Errorf("scenario step %d (%s): recall-fidelity mismatch: expected=%v actual=%v unexpected=%v missing=%v",
		idx+1, label, expected, actual, unexpected, missing)
	return expected, expectedForgiven
}

// recallFidelityMismatch returns the sorted unexpected (false-positive)
// and missing (false-negative) thread ID sets for an assertion message.
// Both slices are nil when the sets agree.
func recallFidelityMismatch(expected, actual []string) (unexpected, missing []string) {
	return recallFidelityMismatchFromSets(toSet(expected), toSet(actual))
}

// recallFidelityMismatchFromSets is the set-based core of
// recallFidelityMismatch — see recallFidelityFromSets for why the sets are
// passed in rather than rebuilt (#2).
func recallFidelityMismatchFromSets(expSet, actSet map[string]struct{}) (unexpected, missing []string) {
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
