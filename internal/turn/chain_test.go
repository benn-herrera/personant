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
