package turn

import (
	"context"
	"io"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// TestLoadSessionRestoresWorkingSet drives several turns that build up
// Layer B/C membership, then simulates a clean shutdown→relaunch via
// LoadSession and asserts the working set was reconstructed from the
// substrate rather than cold-starting empty.
func TestLoadSessionRestoresWorkingSet(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	ops := fileadapter.NewFileAdapter(paths)

	// Four new-topic turns. With Budget.BTopK defaulting to 3, the fourth
	// turn overflows Layer B, demoting the oldest thread into Layer C —
	// so both lists are non-empty when we restart.
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [alpha, beta, gamma, delta]*\nFirst."},
		{Content: "*topic: *new-topic* [epsilon, zeta, eta, theta]*\nSecond."},
		{Content: "*topic: *new-topic* [iota, kappa, lambda, mu]*\nThird."},
		{Content: "*topic: *new-topic* [nu, xi, omicron, pi]*\nFourth."},
	}, nil)

	state := NewState(ops, meta, memops.Provider{}, mock)
	for i := 0; i < 4; i++ {
		mock.SetScriptedStep(i)
		if _, err := Run(context.Background(), state, "prompt", io.Discard); err != nil {
			t.Fatalf("turn %d: Run: %v", i+1, err)
		}
	}

	wantActive := append([]string(nil), state.ActiveThreads...)
	wantDormant := append([]string(nil), state.DormantThreads...)
	if len(wantActive) == 0 || len(wantDormant) == 0 {
		t.Fatalf("precondition: expected non-empty Layer B and C; got active=%v dormant=%v",
			wantActive, wantDormant)
	}

	// Simulate a clean relaunch: discard the in-memory State, rebuild
	// from the substrate exactly as a fresh process launch would.
	rebuilt, err := LoadSession(context.Background(), ops, meta, memops.Provider{}, mock)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if !equalStrs(rebuilt.ActiveThreads, wantActive) {
		t.Errorf("ActiveThreads not restored: got %v want %v", rebuilt.ActiveThreads, wantActive)
	}
	if !equalStrs(rebuilt.DormantThreads, wantDormant) {
		t.Errorf("DormantThreads not restored: got %v want %v", rebuilt.DormantThreads, wantDormant)
	}

	// Session-volatile state must NOT survive: TurnNumber resets to zero.
	if rebuilt.TurnNumber != 0 {
		t.Errorf("TurnNumber should reset on relaunch: got %d want 0", rebuilt.TurnNumber)
	}
}

// TestLoadSessionFreshHome confirms LoadSession on a home that never
// completed a turn yields an empty working set and no error — the
// fresh-launch path.
func TestLoadSessionFreshHome(t *testing.T) {
	paths, meta := newTestHome(t)
	ops := fileadapter.NewFileAdapter(paths)

	state, err := LoadSession(context.Background(), ops, meta, memops.Provider{}, nil)
	if err != nil {
		t.Fatalf("LoadSession on fresh home: %v", err)
	}
	if len(state.ActiveThreads) != 0 || len(state.DormantThreads) != 0 {
		t.Errorf("fresh home should yield empty working set; got active=%v dormant=%v",
			state.ActiveThreads, state.DormantThreads)
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
