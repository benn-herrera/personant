package turn

import (
	"bytes"
	"context"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// TestRunWithInfoSurfacesPromptTokens drives one turn through a mock model
// client whose response carries a known Usage.PromptTokens and asserts
// RunWithInfo surfaces exactly that value in TurnInfo.PromptTokens. This is
// the §2.1 plumbing contract: the provider's prompt-token count for the
// fully-assembled request must reach the caller without re-derivation. The
// value flows through honestly — no special-casing of the mock path.
func TestRunWithInfoSurfacesPromptTokens(t *testing.T) {
	paths, meta := newTestHome(t)

	const wantTokens = 4242
	mock := model.NewScriptedMock([]model.Response{
		{
			Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world.",
			Usage:   model.Usage{PromptTokens: wantTokens, CompletionTokens: 2, TotalTokens: wantTokens + 2},
		},
	}, nil)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	var out bytes.Buffer
	body, info, err := RunWithInfo(context.Background(), state, nil, "test prompt", &out)
	if err != nil {
		t.Fatalf("RunWithInfo: %v", err)
	}
	if info.PromptTokens != wantTokens {
		t.Errorf("TurnInfo.PromptTokens = %d; want %d", info.PromptTokens, wantTokens)
	}
	if body == "" {
		t.Errorf("expected a non-empty body")
	}
}

// TestRunWithDeltasDropsTurnInfo locks in the thin-wrapper contract: the
// existing RunWithDeltas entry point still returns exactly (body, err) and
// compiles unchanged for callers that do not need the per-turn measurements.
func TestRunWithDeltasDropsTurnInfo(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{
			Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world.",
			Usage:   model.Usage{PromptTokens: 4242},
		},
	}, nil)

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	// Two-value form must compile and run; this is the compile-time guard
	// against the wrapper's signature drifting.
	body, err := RunWithDeltas(context.Background(), state, nil, "test prompt", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}
	if body == "" {
		t.Errorf("expected a non-empty body")
	}
}
