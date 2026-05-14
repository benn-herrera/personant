package turn

import (
	"context"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

func TestProvisionalRetention_KnownSources(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   memops.RetentionClass
	}{
		{"user.prompt", "user.prompt", memops.RetentionDecision},
		{"model.response", "model.response", memops.RetentionDecision},
		{"tool.result", "tool.result", memops.RetentionTask},
		{"user.shell-capture", "user.shell-capture", memops.RetentionTask},
		{"thread.fetched", "thread.fetched", memops.RetentionDecision},
		{"digest.refresh", "digest.refresh", memops.RetentionDecision},
		{"slash.injected", "slash.injected", memops.RetentionDecision},
		{"directive.reloaded", "directive.reloaded", memops.RetentionDecision},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := provisionalRetention(tc.source)
			if got != tc.want {
				t.Errorf("provisionalRetention(%q): got %q want %q", tc.source, got, tc.want)
			}
		})
	}
}

func TestProvisionalRetention_UnknownSourceDefaultsToDecision(t *testing.T) {
	got := provisionalRetention("some.new.thing")
	if got != memops.RetentionDecision {
		t.Errorf("unknown source: got %q want %q (safe default)", got, memops.RetentionDecision)
	}
}

func TestStagingBuffer_AddLookupRemove(t *testing.T) {
	b := newStagingBuffer()
	if b.len() != 0 {
		t.Errorf("fresh buffer len: got %d want 0", b.len())
	}

	b.add(stagedSymbol{
		Normalized: "foo.go",
		Raw:        "foo.go",
		Source:     store.SourceDeterministic,
		StagedAt:   1,
	})
	if b.len() != 1 {
		t.Errorf("after add len: got %d want 1", b.len())
	}

	got, ok := b.lookup("foo.go")
	if !ok {
		t.Fatalf("lookup foo.go: not found")
	}
	if got.Normalized != "foo.go" || got.StagedAt != 1 {
		t.Errorf("lookup foo.go: got %+v", got)
	}

	if _, ok := b.lookup("missing"); ok {
		t.Errorf("lookup missing: should be absent")
	}

	b.remove("foo.go")
	if b.len() != 0 {
		t.Errorf("after remove len: got %d want 0", b.len())
	}
	if _, ok := b.lookup("foo.go"); ok {
		t.Errorf("lookup after remove: should be absent")
	}

	// remove of absent key is a no-op
	b.remove("never-existed")
}

func TestStagingBuffer_AddOverwritesAndRefreshes(t *testing.T) {
	b := newStagingBuffer()
	b.add(stagedSymbol{Normalized: "foo.go", Raw: "foo.go", Source: store.SourceDeterministic, StagedAt: 1})
	b.add(stagedSymbol{Normalized: "foo.go", Raw: "foo.go", Source: store.SourceDeterministic, StagedAt: 5})

	if b.len() != 1 {
		t.Fatalf("len after re-add: got %d want 1", b.len())
	}
	got, ok := b.lookup("foo.go")
	if !ok {
		t.Fatalf("lookup foo.go: not found")
	}
	if got.StagedAt != 5 {
		t.Errorf("StagedAt after refresh: got %d want 5 (newer wins)", got.StagedAt)
	}
}

func TestStagingBuffer_EvictBefore(t *testing.T) {
	b := newStagingBuffer()
	b.add(stagedSymbol{Normalized: "a", Raw: "a", Source: store.SourceDeterministic, StagedAt: 1})
	b.add(stagedSymbol{Normalized: "b", Raw: "b", Source: store.SourceDeterministic, StagedAt: 3})
	b.add(stagedSymbol{Normalized: "c", Raw: "c", Source: store.SourceDeterministic, StagedAt: 5})

	n := b.evictBefore(4)
	if n != 2 {
		t.Errorf("evictBefore(4): got %d want 2", n)
	}
	if b.len() != 1 {
		t.Errorf("len after evict: got %d want 1", b.len())
	}
	if _, ok := b.lookup("c"); !ok {
		t.Errorf("c should survive (StagedAt=5 >= cutoff=4)")
	}
	if _, ok := b.lookup("a"); ok {
		t.Errorf("a should be evicted (StagedAt=1 < cutoff=4)")
	}
	if _, ok := b.lookup("b"); ok {
		t.Errorf("b should be evicted (StagedAt=3 < cutoff=4)")
	}
}

// recordingAdapter wraps a real FileAdapter and captures every Delta
// passed through EmitDelta. The rest of the MemoryOps surface is
// inherited from the embedded adapter via Go's method promotion — only
// EmitDelta is shadowed.
//
// Used to assert on the Retention field that onContextDelta installs
// before forwarding to the adapter; that field is invisible at the
// event-log substrate level because the v0.1 file adapter ignores it.
type recordingAdapter struct {
	*fileadapter.FileAdapter
	emitted []memops.Delta
}

func (r *recordingAdapter) EmitDelta(ctx context.Context, delta memops.Delta) error {
	r.emitted = append(r.emitted, delta)
	return r.FileAdapter.EmitDelta(ctx, delta)
}

func newRecordingState(t *testing.T) (*State, *recordingAdapter) {
	t.Helper()
	paths, meta := newChainHome(t)
	rec := &recordingAdapter{FileAdapter: fileadapter.NewFileAdapter(paths)}
	state := NewState(rec, meta, store.Provider{}, nil)
	return state, rec
}

func TestOnContextDelta_SetsRetentionFromSource(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   memops.RetentionClass
	}{
		{"tool.result is task", "tool.result", memops.RetentionTask},
		{"user.shell-capture is task", "user.shell-capture", memops.RetentionTask},
		{"user.prompt is decision", "user.prompt", memops.RetentionDecision},
		{"model.response is decision", "model.response", memops.RetentionDecision},
		{"thread.fetched is decision", "thread.fetched", memops.RetentionDecision},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, rec := newRecordingState(t)
			err := onContextDelta(context.Background(), state, Delta{
				Source:  tc.source,
				Content: "irrelevant",
			})
			if err != nil {
				t.Fatalf("onContextDelta: %v", err)
			}
			if len(rec.emitted) != 1 {
				t.Fatalf("emitted len: got %d want 1", len(rec.emitted))
			}
			if got := rec.emitted[0].Retention; got != tc.want {
				t.Errorf("Retention: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestOnContextDelta_RespectsExplicitRetention(t *testing.T) {
	state, rec := newRecordingState(t)
	// user.shell-capture would default to RetentionTask; the explicit
	// RetentionDecision must survive — this is the future `##` / `/keep`
	// reference-material override path.
	err := onContextDelta(context.Background(), state, Delta{
		Source:    "user.shell-capture",
		Content:   "irrelevant",
		Retention: memops.RetentionDecision,
	})
	if err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}
	if len(rec.emitted) != 1 {
		t.Fatalf("emitted len: got %d want 1", len(rec.emitted))
	}
	if got := rec.emitted[0].Retention; got != memops.RetentionDecision {
		t.Errorf("explicit override: got %q want %q (caller's value must survive)",
			got, memops.RetentionDecision)
	}
}
