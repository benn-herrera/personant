package prompt

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TagMissReplayDirEnv names the environment variable that points
// TestTagMissReplay at a rundata tagmiss/ capture directory (the raw
// model-response bodies a live-inference rung retained for its tag-miss
// turns — see scenarios.stepCaptureTagMiss). Unset → the test skips clean,
// so `make test` is unaffected.
const TagMissReplayDirEnv = "PERSONANT_TAGMISS_REPLAY_DIR"

// TestTagMissReplay is the cheap-instrument acceptance harness for parser
// changes: it re-parses EVERY captured tag-miss body from a live run and
// reports how the current parser classifies each —
//
//   - parsed-valid-now: Parse succeeds (a parser fix recovered the tag)
//   - still-near-miss(reason): Parse fails, ClassifyNearMiss fires
//   - still-nothing: Parse fails, no near-miss candidate (true omission)
//
// A parser tolerance is accepted by REPLAY against the capture, not by a
// live rerun: the capture is the ground truth of what the model actually
// emitted. Evidence run for the §5.1.2 unclosed-inner-literal tolerance:
// test/rundata/sim-workload-seed24095-dur24h0m0s.260716100051/tagmiss/
// (380 files; ~364 expected to recover).
func TestTagMissReplay(t *testing.T) {
	dir := os.Getenv(TagMissReplayDirEnv)
	if dir == "" {
		t.Skipf("%s unset — set it to a rundata tagmiss/ directory to replay captured tag misses", TagMissReplayDirEnv)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read replay dir %s: %v", dir, err)
	}

	var (
		total, validNow, nothing int
		nearMissByReason         = map[string]int{}
		residueFiles             []string // still-near-miss + still-nothing, for forensics
	)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		total++
		if _, err := Parse(string(body)); err == nil {
			validNow++
			continue
		}
		if nm, ok := ClassifyNearMiss(string(body)); ok {
			nearMissByReason[nm.Reason]++
			residueFiles = append(residueFiles, e.Name()+" (near-miss:"+nm.Reason+")")
			continue
		}
		nothing++
		residueFiles = append(residueFiles, e.Name()+" (nothing)")
	}
	if total == 0 {
		t.Fatalf("replay dir %s contains no .txt captures", dir)
	}

	reasons := make([]string, 0, len(nearMissByReason))
	for r := range nearMissByReason {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	t.Logf("=== tag-miss replay: %s ===", dir)
	t.Logf("captures:          %d", total)
	t.Logf("parsed-valid-now:  %d", validNow)
	for _, r := range reasons {
		t.Logf("still-near-miss:   %d reason=%s", nearMissByReason[r], r)
	}
	t.Logf("still-nothing:     %d", nothing)
	sort.Strings(residueFiles)
	for _, f := range residueFiles {
		t.Logf("  residue: %s", f)
	}
}
