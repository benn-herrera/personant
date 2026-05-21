package turn

import (
	"context"
	"os"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

func newChainHome(t *testing.T) (store.PersonantPaths, memops.ProjectMeta) {
	t.Helper()
	tmp := t.TempDir()
	paths := store.PathsForHome(tmp)
	for _, dir := range []string{paths.ThreadsDir, paths.ProjectsDir, paths.LogsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := store.WriteSpine(paths.Spine, nil); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: tmp}
	if err := store.SaveProjectMeta(paths, meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	return paths, meta
}

func TestChainExtractsUserHashTags(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "user.prompt",
		Content: "checking #alpha-beta and #foo_2 and not #InvalidUpper",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}
	got := state.coalesce.symbols
	// Tag normalization (§2.7.2) strips punctuation other than `-`, so
	// `foo_2` becomes `foo2`.
	for _, want := range []string{"alpha-beta", "foo2"} {
		if _, ok := got[want]; !ok {
			t.Errorf("expected symbol %q in %v", want, got)
		}
	}
	// Uppercase-leading tags are excluded by the regex's character class.
	if _, ok := got["invalidupper"]; ok {
		t.Errorf("uppercase-led tag should not match: %v", got)
	}
}

func TestChainExtractsModelTopicTag(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "model.response",
		Content: "*topic: thr_5, thr_9 [trefoil, unknot, body-topology, electron-shape]*\nBody.",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}
	for _, want := range []string{"trefoil", "unknot", "body-topology", "electron-shape"} {
		if _, ok := state.coalesce.symbols[want]; !ok {
			t.Errorf("missing anchor %q in %v", want, state.coalesce.symbols)
		}
	}
	for _, want := range []string{"thr_5", "thr_9"} {
		if _, ok := state.coalesce.threads[want]; !ok {
			t.Errorf("missing thread %q in %v", want, state.coalesce.threads)
		}
	}
}

func TestChainTopicTagAbsentIsNonFatal(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "model.response",
		Content: "no topic tag here",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}
	if len(state.coalesce.symbols) != 0 || len(state.coalesce.threads) != 0 {
		t.Errorf("missing-tag should leave coalesce empty; got symbols=%v threads=%v",
			state.coalesce.symbols, state.coalesce.threads)
	}
}

// TestBudgetCheckIsExplicitNoOpV01 is a TRIPWIRE, not an endorsement.
//
// SPEC §3.0.2 step 4 (budget check) is a deliberate no-op in v0.1: no
// turn-time eviction or truncation fires when a delta lands. This test
// documents that contract by driving an oversized delta — one whose
// content dwarfs any plausible layer byte budget — through the chain and
// asserting the chain did NOT react with eviction/truncation: the spine
// is untouched and no eviction/truncation event is logged at the Step-4
// stage.
//
// If someone later wires real §3.1 budget enforcement into the chain
// without making that a deliberate decision, this test fails and forces
// the conversation (sim-vs-reality MAD T0-2 / finding B7). When the
// budget check is intentionally implemented, delete or invert this test
// as part of that change — its failure is the signal, not a bug.
func TestBudgetCheckIsExplicitNoOpV01(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	spineBefore, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine before: %v", err)
	}

	// A delta whose content is far larger than context.byte-budget
	// (65536, SPEC §2.6.1). If Step 4 ever enforces budgets, an
	// oversized delta is exactly what would trigger eviction.
	oversized := strings.Repeat("oversized-delta-payload ", 8192) // ~192 KB
	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "model.response",
		Content: oversized,
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	// Contract 1: the chain's Step-4 no-op must not mutate persistent
	// engagement state. Eviction would surface as spine churn.
	spineAfter, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine after: %v", err)
	}
	if len(spineAfter) != len(spineBefore) {
		t.Fatalf("budget check is supposed to be a no-op, but the spine changed (%d → %d records); "+
			"if you intentionally implemented §3.0.2 step 4, update this tripwire deliberately",
			len(spineBefore), len(spineAfter))
	}

	// Contract 2: no eviction/truncation event was logged by the chain.
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(paths.LogsDir + string(os.PathSeparator) + e.Name())
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		for _, marker := range []string{"evict", "truncat", "budget"} {
			if strings.Contains(string(data), marker) {
				t.Fatalf("budget check is supposed to be a no-op, but the chain logged %q "+
					"(found in %s); if you intentionally implemented §3.0.2 step 4, "+
					"update this tripwire deliberately", marker, e.Name())
			}
		}
	}
}

func TestChainLogsContextModified(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	if err := onContextDelta(context.Background(), state, Delta{Source: "user.prompt", Content: "hello"}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}
	// Find the log file (we don't pin a clock, so any *.log file under LogsDir works).
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(entries))
	}
	data, err := os.ReadFile(paths.LogsDir + string(os.PathSeparator) + entries[0].Name())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "context.modified source=user.prompt bytes=5") {
		t.Errorf("expected context.modified line; got %q", string(data))
	}
}
