package turn

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

// readLogLinesContaining walks every *.log under paths.LogsDir and
// returns the lines containing substr. Pattern mirrors
// assertSpineMatchFires in internal/scenarios/scenarios_test.go.
func readLogLinesContaining(t *testing.T, paths store.PersonantPaths, substr string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	var lines []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			if strings.Contains(line, substr) {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// readPromotionLogLines is a back-compat shim around
// readLogLinesContaining for the B.3 promotion tests; new callers
// should use the general helper directly.
func readPromotionLogLines(t *testing.T, paths store.PersonantPaths) []string {
	t.Helper()
	return readLogLinesContaining(t, paths, "staging.promoted")
}

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
		Source:     memops.SourceDeterministic,
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
	b.add(stagedSymbol{Normalized: "foo.go", Raw: "foo.go", Source: memops.SourceDeterministic, StagedAt: 1})
	b.add(stagedSymbol{Normalized: "foo.go", Raw: "foo.go", Source: memops.SourceDeterministic, StagedAt: 5})

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
	b.add(stagedSymbol{Normalized: "a", Raw: "a", Source: memops.SourceDeterministic, StagedAt: 1})
	b.add(stagedSymbol{Normalized: "b", Raw: "b", Source: memops.SourceDeterministic, StagedAt: 3})
	b.add(stagedSymbol{Normalized: "c", Raw: "c", Source: memops.SourceDeterministic, StagedAt: 5})

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
	state := NewState(rec, meta, memops.Provider{}, nil)
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

// B.2 routing tests: task-class symbols land in staging, decision-class
// symbols land in coalesce. The four cases below pin down the matrix
// (task, decision, mixed-turn, explicit override).

func TestRouting_TaskClassSymbolsGoToStaging(t *testing.T) {
	state, _ := newRecordingState(t)
	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "user.shell-capture",
		Content: "ls returned: src/main.go src/util.go",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	for _, want := range []string{"src/main.go", "src/util.go"} {
		staged, ok := state.staging.lookup(want)
		if !ok {
			t.Errorf("staging missing %q; staging.len=%d", want, state.staging.len())
			continue
		}
		if staged.Source != memops.SourceDeterministic {
			t.Errorf("staged %q Source: got %q want %q", want, staged.Source, memops.SourceDeterministic)
		}
		if staged.StagedAt != state.TurnNumber {
			t.Errorf("staged %q StagedAt: got %d want %d", want, staged.StagedAt, state.TurnNumber)
		}
	}
	if len(state.coalesce.symbols) != 0 {
		t.Errorf("coalesce symbols should be empty for pure task delta; got %v", state.coalesce.symbols)
	}
}

func TestRouting_DecisionClassSymbolsGoToCoalesce(t *testing.T) {
	state, _ := newRecordingState(t)
	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "user.prompt",
		Content: "look at internal/foo.go and #notes",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	path, ok := state.coalesce.symbols["internal/foo.go"]
	if !ok {
		t.Fatalf("coalesce missing internal/foo.go; symbols=%v", state.coalesce.symbols)
	}
	if path.Source != memops.SourceDeterministic {
		t.Errorf("path Source: got %q want %q", path.Source, memops.SourceDeterministic)
	}

	tag, ok := state.coalesce.symbols["notes"]
	if !ok {
		t.Fatalf("coalesce missing #notes; symbols=%v", state.coalesce.symbols)
	}
	if tag.Source != memops.SourceUser {
		t.Errorf("tag Source: got %q want %q", tag.Source, memops.SourceUser)
	}

	if state.staging.len() != 0 {
		t.Errorf("staging should be empty for pure decision delta; len=%d", state.staging.len())
	}
}

func TestRouting_MixedTurnIsolatesByClass(t *testing.T) {
	state, _ := newRecordingState(t)
	ctx := context.Background()

	if err := onContextDelta(ctx, state, Delta{
		Source:  "user.prompt",
		Content: "verify staging worked. #verify",
	}); err != nil {
		t.Fatalf("user.prompt delta: %v", err)
	}
	if err := onContextDelta(ctx, state, Delta{
		Source:  "tool.result",
		Content: "wrote internal/store/foo.go",
	}); err != nil {
		t.Fatalf("tool.result delta: %v", err)
	}
	if err := onContextDelta(ctx, state, Delta{
		Source:  "model.response",
		Content: "*topic: *new-topic* [alpha, beta, gamma, delta]*\nDone. See internal/bar.go",
	}); err != nil {
		t.Fatalf("model.response delta: %v", err)
	}

	// coalesce: decision-class arrivals.
	wantCoalesce := map[string]memops.SymbolSource{
		"verify":          memops.SourceUser,
		"alpha":           memops.SourceModel,
		"beta":            memops.SourceModel,
		"gamma":           memops.SourceModel,
		"delta":           memops.SourceModel,
		"internal/bar.go": memops.SourceDeterministic,
	}
	for norm, wantSrc := range wantCoalesce {
		sym, ok := state.coalesce.symbols[norm]
		if !ok {
			t.Errorf("coalesce missing %q; symbols=%v", norm, state.coalesce.symbols)
			continue
		}
		if sym.Source != wantSrc {
			t.Errorf("coalesce %q Source: got %q want %q", norm, sym.Source, wantSrc)
		}
	}
	if _, leaked := state.coalesce.symbols["internal/store/foo.go"]; leaked {
		t.Errorf("coalesce contains task-class symbol internal/store/foo.go; symbols=%v", state.coalesce.symbols)
	}

	// staging: exactly the task-class symbol.
	if state.staging.len() != 1 {
		t.Errorf("staging len: got %d want 1", state.staging.len())
	}
	staged, ok := state.staging.lookup("internal/store/foo.go")
	if !ok {
		t.Fatalf("staging missing internal/store/foo.go")
	}
	if staged.Source != memops.SourceDeterministic {
		t.Errorf("staged Source: got %q want %q", staged.Source, memops.SourceDeterministic)
	}
	if staged.StagedAt != state.TurnNumber {
		t.Errorf("staged StagedAt: got %d want %d", staged.StagedAt, state.TurnNumber)
	}
}

// B.3 promotion tests: a decision-class delta that cites a previously
// staged symbol promotes it out of staging and into coalesce. The
// staged Source merges with the citing delta's via §2.7.3 dominance
// (handled by coalesce.addSymbol; no B.3-specific code).

// TestPromotion_SameTurn_DecisionCitesStaged — a task delta stages
// internal/store/foo.go in the same turn that a user.prompt cites it.
// Result: staging empty, coalesce holds the symbol exactly once, and
// the day's eventlog records a staging.promoted line.
func TestPromotion_SameTurn_DecisionCitesStaged(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	ctx := context.Background()

	if err := onContextDelta(ctx, state, Delta{
		Source:  "tool.result",
		Content: "modified internal/store/foo.go",
	}); err != nil {
		t.Fatalf("tool.result delta: %v", err)
	}
	if _, ok := state.staging.lookup("internal/store/foo.go"); !ok {
		t.Fatalf("pre-promotion: expected staging entry; staging=%d coalesce=%v",
			state.staging.len(), state.coalesce.symbols)
	}

	if err := onContextDelta(ctx, state, Delta{
		Source:  "user.prompt",
		Content: "look at internal/store/foo.go for context",
	}); err != nil {
		t.Fatalf("user.prompt delta: %v", err)
	}

	if _, ok := state.staging.lookup("internal/store/foo.go"); ok {
		t.Errorf("post-promotion: staging should not contain promoted symbol; staging.len=%d",
			state.staging.len())
	}
	if state.staging.len() != 0 {
		t.Errorf("post-promotion: staging.len=%d want 0", state.staging.len())
	}
	sym, ok := state.coalesce.symbols["internal/store/foo.go"]
	if !ok {
		t.Fatalf("post-promotion: coalesce missing symbol; symbols=%v", state.coalesce.symbols)
	}
	if sym.Source != memops.SourceDeterministic {
		t.Errorf("promoted Source: got %q want %q (both sides Deterministic; dominance returns Deterministic)",
			sym.Source, memops.SourceDeterministic)
	}
	// Coalesce is a map keyed by normalized form; "exactly once" is
	// implicit (no duplicate keys). Verify only the cited symbol is
	// present so we're not accidentally counting other extractions.
	if len(state.coalesce.symbols) != 1 {
		t.Errorf("coalesce should hold exactly the promoted symbol; symbols=%v", state.coalesce.symbols)
	}

	lines := readPromotionLogLines(t, paths)
	if len(lines) != 1 {
		t.Fatalf("expected 1 staging.promoted log line; got %d (%v)", len(lines), lines)
	}
	if !strings.Contains(lines[0], "normalized=internal/store/foo.go") {
		t.Errorf("log line missing normalized=...: %q", lines[0])
	}
	if !strings.Contains(lines[0], "turn_span=0") {
		t.Errorf("same-turn promotion should report turn_span=0; got %q", lines[0])
	}
}

// TestPromotion_CrossTurn_DecisionCitesStagedFromEarlierTurn — bump
// TurnNumber between the task and decision deltas to simulate a turn
// boundary without invoking Run. Verifies turn_span is reported
// correctly in the promotion log line.
func TestPromotion_CrossTurn_DecisionCitesStagedFromEarlierTurn(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	ctx := context.Background()

	state.TurnNumber = 1
	if err := onContextDelta(ctx, state, Delta{
		Source:  "tool.result",
		Content: "see https://example.com/x",
	}); err != nil {
		t.Fatalf("tool.result delta: %v", err)
	}
	staged, ok := state.staging.lookup("https://example.com/x")
	if !ok {
		t.Fatalf("pre-promotion: staging missing URL; staging.len=%d", state.staging.len())
	}
	if staged.StagedAt != 1 {
		t.Errorf("StagedAt: got %d want 1", staged.StagedAt)
	}

	state.TurnNumber = 2
	if err := onContextDelta(ctx, state, Delta{
		Source:  "user.prompt",
		Content: "yes about https://example.com/x",
	}); err != nil {
		t.Fatalf("user.prompt delta: %v", err)
	}

	if _, ok := state.staging.lookup("https://example.com/x"); ok {
		t.Errorf("post-promotion: staging should not contain promoted URL; staging.len=%d", state.staging.len())
	}
	if _, ok := state.coalesce.symbols["https://example.com/x"]; !ok {
		t.Fatalf("post-promotion: coalesce missing URL; symbols=%v", state.coalesce.symbols)
	}

	lines := readPromotionLogLines(t, paths)
	if len(lines) != 1 {
		t.Fatalf("expected 1 staging.promoted log line; got %d (%v)", len(lines), lines)
	}
	for _, want := range []string{
		"normalized=https://example.com/x",
		"staged_at=1",
		"cited_at=2",
		"turn_span=1",
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line missing %q: %q", want, lines[0])
		}
	}
}

// TestPromotion_NoCrossReference_StagedSymbolPersists — a decision
// delta that does NOT cite any staged symbol must leave staging
// untouched. B.4 (window-close GC) is responsible for eventual
// eviction; B.3 only promotes via citation.
func TestPromotion_NoCrossReference_StagedSymbolPersists(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)
	ctx := context.Background()

	if err := onContextDelta(ctx, state, Delta{
		Source:  "tool.result",
		Content: "src/foo.go src/bar.go",
	}); err != nil {
		t.Fatalf("tool.result delta: %v", err)
	}
	if state.staging.len() != 2 {
		t.Fatalf("pre-decision: staging.len=%d want 2", state.staging.len())
	}

	if err := onContextDelta(ctx, state, Delta{
		Source:  "user.prompt",
		Content: "unrelated topic #thoughts",
	}); err != nil {
		t.Fatalf("user.prompt delta: %v", err)
	}

	for _, want := range []string{"src/foo.go", "src/bar.go"} {
		if _, ok := state.staging.lookup(want); !ok {
			t.Errorf("staging should still contain %q (no citation); staging.len=%d", want, state.staging.len())
		}
	}
	if state.staging.len() != 2 {
		t.Errorf("staging.len: got %d want 2", state.staging.len())
	}
	if _, ok := state.coalesce.symbols["thoughts"]; !ok {
		t.Errorf("coalesce should contain #thoughts; symbols=%v", state.coalesce.symbols)
	}
	for _, leak := range []string{"src/foo.go", "src/bar.go"} {
		if _, ok := state.coalesce.symbols[leak]; ok {
			t.Errorf("coalesce should NOT contain staged-but-uncited %q; symbols=%v",
				leak, state.coalesce.symbols)
		}
	}

	if lines := readPromotionLogLines(t, paths); len(lines) != 0 {
		t.Errorf("expected zero staging.promoted lines; got %d (%v)", len(lines), lines)
	}
}

func TestRouting_ExplicitDecisionOverrideRoutesToCoalesce(t *testing.T) {
	state, _ := newRecordingState(t)
	// shell-capture defaults to RetentionTask; explicit RetentionDecision
	// override (future `##` / `/keep` path) must route extracted symbols
	// into coalesce, not staging.
	if err := onContextDelta(context.Background(), state, Delta{
		Source:    "user.shell-capture",
		Content:   "ref material: see https://arxiv.org/abs/foo.bar",
		Retention: memops.RetentionDecision,
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	sym, ok := state.coalesce.symbols["https://arxiv.org/abs/foo.bar"]
	if !ok {
		t.Fatalf("coalesce missing url; symbols=%v", state.coalesce.symbols)
	}
	if sym.Source != memops.SourceDeterministic {
		t.Errorf("url Source: got %q want %q", sym.Source, memops.SourceDeterministic)
	}
	if state.staging.len() != 0 {
		t.Errorf("staging should be empty under explicit decision override; len=%d", state.staging.len())
	}
}

// B.4 window-close GC: pruneStaging unit tests + Run-level integration.

// TestPruneStaging_NoopBeforeFirstTurn documents the pre-Run guard:
// when TurnNumber is still zero (state freshly constructed, never
// Run-ed), pruneStaging must not touch the buffer regardless of what
// is in it.
func TestPruneStaging_NoopBeforeFirstTurn(t *testing.T) {
	state := &State{staging: newStagingBuffer(), TurnNumber: 0}
	state.staging.add(stagedSymbol{
		Normalized: "before-any-turn",
		Raw:        "before-any-turn",
		Source:     memops.SourceDeterministic,
		StagedAt:   0,
	})
	if got := pruneStaging(state); got != 0 {
		t.Errorf("pruneStaging at TurnNumber=0: got %d evicted, want 0", got)
	}
	if _, ok := state.staging.lookup("before-any-turn"); !ok {
		t.Errorf("pre-Run entry must survive; staging.len=%d", state.staging.len())
	}
}

// TestPruneStaging_NoopWithinWindow — at TurnNumber=3, cutoff = 3-3+1
// = 1, so nothing has StagedAt < 1. The buffer is untouched even
// though entries from three different turns are present.
func TestPruneStaging_NoopWithinWindow(t *testing.T) {
	state := &State{staging: newStagingBuffer(), TurnNumber: 3}
	for _, sa := range []int{1, 2, 3} {
		state.staging.add(stagedSymbol{
			Normalized: "s" + itoa(sa),
			Raw:        "s" + itoa(sa),
			Source:     memops.SourceDeterministic,
			StagedAt:   sa,
		})
	}
	if got := pruneStaging(state); got != 0 {
		t.Errorf("pruneStaging at TurnNumber=3: got %d evicted, want 0 (cutoff=1, nothing older)", got)
	}
	if state.staging.len() != 3 {
		t.Errorf("staging.len: got %d want 3", state.staging.len())
	}
}

// TestPruneStaging_EvictsAtWindowClose — at TurnNumber=4, cutoff =
// 4-3+1 = 2. The StagedAt=1 entry evicts; StagedAt=2 and StagedAt=3
// survive. This is the first turn at which any entry can age out.
func TestPruneStaging_EvictsAtWindowClose(t *testing.T) {
	state := &State{staging: newStagingBuffer(), TurnNumber: 4}
	for _, sa := range []int{1, 2, 3} {
		state.staging.add(stagedSymbol{
			Normalized: "s" + itoa(sa),
			Raw:        "s" + itoa(sa),
			Source:     memops.SourceDeterministic,
			StagedAt:   sa,
		})
	}
	if got := pruneStaging(state); got != 1 {
		t.Errorf("pruneStaging at TurnNumber=4: got %d evicted, want 1", got)
	}
	if _, ok := state.staging.lookup("s1"); ok {
		t.Errorf("s1 (StagedAt=1, cutoff=2) should be evicted")
	}
	for _, want := range []string{"s2", "s3"} {
		if _, ok := state.staging.lookup(want); !ok {
			t.Errorf("%s should survive (StagedAt >= cutoff=2)", want)
		}
	}
}

// TestPruneStaging_EvictsMultiple — at TurnNumber=10, cutoff=8. Two
// entries (StagedAt=1 and StagedAt=5) fall below cutoff and evict;
// StagedAt=8 and StagedAt=10 survive (both >= 8).
func TestPruneStaging_EvictsMultiple(t *testing.T) {
	state := &State{staging: newStagingBuffer(), TurnNumber: 10}
	for _, sa := range []int{1, 5, 8, 10} {
		state.staging.add(stagedSymbol{
			Normalized: "s" + itoa(sa),
			Raw:        "s" + itoa(sa),
			Source:     memops.SourceDeterministic,
			StagedAt:   sa,
		})
	}
	if got := pruneStaging(state); got != 2 {
		t.Errorf("pruneStaging at TurnNumber=10: got %d evicted, want 2", got)
	}
	for _, gone := range []string{"s1", "s5"} {
		if _, ok := state.staging.lookup(gone); ok {
			t.Errorf("%s should be evicted (StagedAt < cutoff=8)", gone)
		}
	}
	for _, kept := range []string{"s8", "s10"} {
		if _, ok := state.staging.lookup(kept); !ok {
			t.Errorf("%s should survive (StagedAt >= cutoff=8)", kept)
		}
	}
}

// TestRun_EvictsStaledStagingAtTopOfRun — end-to-end through Run:
// pre-seed staging with an entry at StagedAt=1, set TurnNumber=3 so
// Run bumps it to 4 (cutoff = 4-3+1 = 2 -> evicts StagedAt=1). The
// model mock returns a benign topic-tag response so the rest of Run
// completes without error.
func TestRun_EvictsStaledStagingAtTopOfRun(t *testing.T) {
	paths, meta := newChainHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)

	// Pre-seed: a staged entry from turn 1, and three turns already
	// elapsed. Run will bump TurnNumber 3 -> 4; cutoff = 2; "old" evicts.
	state.staging.add(stagedSymbol{
		Normalized: "old",
		Raw:        "old",
		Source:     memops.SourceDeterministic,
		StagedAt:   1,
	})
	state.TurnNumber = 3

	if _, err := Run(context.Background(), state, "hello", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, ok := state.staging.lookup("old"); ok {
		t.Errorf("expected 'old' evicted at top of turn 4; staging.len=%d", state.staging.len())
	}

	lines := readLogLinesContaining(t, paths, "staging.evicted")
	if len(lines) != 1 {
		t.Fatalf("expected 1 staging.evicted log line; got %d (%v)", len(lines), lines)
	}
	for _, want := range []string{"count=1", "turn=4"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line missing %q: %q", want, lines[0])
		}
	}
}

// TestRun_DoesNotEvictBeforeWindowClose — same seeding pattern but
// TurnNumber=2 going in; Run bumps to 3; cutoff = 3-3+1 = 1; nothing
// has StagedAt < 1, so no eviction and no log line.
func TestRun_DoesNotEvictBeforeWindowClose(t *testing.T) {
	paths, meta := newChainHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)

	state.staging.add(stagedSymbol{
		Normalized: "still-fresh",
		Raw:        "still-fresh",
		Source:     memops.SourceDeterministic,
		StagedAt:   1,
	})
	state.TurnNumber = 2

	if _, err := Run(context.Background(), state, "hello", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, ok := state.staging.lookup("still-fresh"); !ok {
		t.Errorf("expected 'still-fresh' to survive (cutoff=1 at turn 3); staging.len=%d", state.staging.len())
	}
	if lines := readLogLinesContaining(t, paths, "staging.evicted"); len(lines) != 0 {
		t.Errorf("expected zero staging.evicted log lines; got %d (%v)", len(lines), lines)
	}
}
