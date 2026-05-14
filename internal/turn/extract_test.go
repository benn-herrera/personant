package turn

import (
	"context"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

// extractCoalesce is a thin helper that drives deterministicExtract on
// a fresh State and returns the coalesce buffer for inspection. The
// synthetic Delta is decision-class so post-B.2 routing lands extracted
// symbols in coalesce; staging-routed cases have their own tests in
// staging_test.go.
func extractCoalesce(t *testing.T, content string) *coalesceBuffer {
	t.Helper()
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, nil)
	deterministicExtract(context.Background(), state, Delta{
		Source:    "user.prompt",
		Content:   content,
		Retention: memops.RetentionDecision,
	})
	return state.coalesce
}

func TestDeterministicExtractURL(t *testing.T) {
	buf := extractCoalesce(t, "see https://example.com/foo, please")
	const want = "https://example.com/foo"
	sym, ok := buf.symbols[want]
	if !ok {
		t.Fatalf("URL not extracted; symbols=%v", buf.symbols)
	}
	if sym.Source != store.SourceDeterministic {
		t.Errorf("source: got %q want %q", sym.Source, store.SourceDeterministic)
	}
	if sym.Raw != want {
		t.Errorf("raw: got %q want %q (trailing comma should be stripped)", sym.Raw, want)
	}
}

func TestDeterministicExtractFilePath(t *testing.T) {
	buf := extractCoalesce(t, "check internal/turn/turn.go and ./README.md")
	for _, want := range []string{"internal/turn/turn.go", "./README.md"} {
		sym, ok := buf.symbols[want]
		if !ok {
			t.Errorf("file path %q not extracted; symbols=%v", want, buf.symbols)
			continue
		}
		if sym.Source != store.SourceDeterministic {
			t.Errorf("%q source: got %q want %q", want, sym.Source, store.SourceDeterministic)
		}
	}
}

func TestDeterministicExtractHexID(t *testing.T) {
	buf := extractCoalesce(t, "commit a1b2c3d4 reverts that")
	if _, ok := buf.symbols["a1b2c3d4"]; !ok {
		t.Errorf("hex ID not extracted; symbols=%v", buf.symbols)
	}

	// `0xa1b2c3d4` is a code literal — must be rejected. `#abc123ff`
	// is a CSS color / fragment-id — also rejected.
	buf2 := extractCoalesce(t, "value is 0xa1b2c3d4 or color #abc123ff in code")
	for _, reject := range []string{"a1b2c3d4", "abc123ff"} {
		if _, ok := buf2.symbols[reject]; ok {
			t.Errorf("hex %q should be rejected (code/color literal); symbols=%v", reject, buf2.symbols)
		}
	}
}

func TestDeterministicExtractCoalesceWithUserPrompt(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, nil)

	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "user.prompt",
		Content: "#trefoil https://wiki/trefoil",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	tag, ok := state.coalesce.symbols["trefoil"]
	if !ok {
		t.Fatalf("user tag missing; symbols=%v", state.coalesce.symbols)
	}
	if tag.Source != store.SourceUser {
		t.Errorf("tag source: got %q want %q", tag.Source, store.SourceUser)
	}

	url, ok := state.coalesce.symbols["https://wiki/trefoil"]
	if !ok {
		t.Fatalf("URL missing; symbols=%v", state.coalesce.symbols)
	}
	if url.Source != store.SourceDeterministic {
		t.Errorf("URL source: got %q want %q", url.Source, store.SourceDeterministic)
	}
}

// TestDeterministicExtract_ToolResultStagesNotCoalesces is the B.2
// behavior flip: tool.result is task-class, so its extracted symbol now
// lands in state.staging, not state.coalesce. The same delta would have
// populated coalesce pre-B.2.
func TestDeterministicExtract_ToolResultStagesNotCoalesces(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, store.Provider{}, nil)

	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "tool.result",
		Content: "wrote internal/store/foo.go",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	staged, ok := state.staging.lookup("internal/store/foo.go")
	if !ok {
		t.Fatalf("path not staged from tool.result; staging.len=%d", state.staging.len())
	}
	if staged.Normalized != "internal/store/foo.go" {
		t.Errorf("staged Normalized: got %q want %q", staged.Normalized, "internal/store/foo.go")
	}
	if staged.Source != store.SourceDeterministic {
		t.Errorf("staged Source: got %q want %q", staged.Source, store.SourceDeterministic)
	}
	if staged.StagedAt != state.TurnNumber {
		t.Errorf("staged StagedAt: got %d want %d (current turn)", staged.StagedAt, state.TurnNumber)
	}
	if _, ok := state.coalesce.symbols["internal/store/foo.go"]; ok {
		t.Errorf("symbol should NOT be in coalesce (task-class routes to staging); symbols=%v", state.coalesce.symbols)
	}
	if len(state.coalesce.symbols) != 0 {
		t.Errorf("coalesce should be empty after task-only delta; got %v", state.coalesce.symbols)
	}
}

func TestDeterministicExtractIgnoresIrrelevantContent(t *testing.T) {
	buf := extractCoalesce(t, "hello world, no patterns here")
	if len(buf.symbols) != 0 {
		t.Errorf("unexpected symbols: %v", buf.symbols)
	}
}
