package scenarios

import (
	"testing"
	"time"

	"personant/internal/model"
)

// TestScenarioValidate covers the #9 run-start config gate: each contradictory
// or no-op-inducing field combination must fail loud, and the valid shapes the
// real scenarios use (handwritten Steps; the sim's StepSource + cadence +
// day-close + paired live client/model) must pass. validate() is exercised
// directly — the unit under test — rather than through RunScenario, so the
// table stays fast and needs no turn machinery.
func TestScenarioValidate(t *testing.T) {
	// stubSource is a minimal non-nil StepSource for the precedence cases; it
	// is never driven (validate does not call Next).
	stub := stepSourceFunc(func(StepFeedback) (Step, bool) { return Step{}, false })
	liveClient := model.NewScriptedMock(nil, nil)

	cases := []struct {
		name    string
		sc      Scenario
		wantErr bool
	}{
		// --- contradictory / no-op-inducing: must fail loud ---
		{
			name:    "day-close handler without cadence",
			sc:      Scenario{OnSimDayClose: func(*Harness, int, time.Time) {}},
			wantErr: true,
		},
		{
			name: "both StepSource and Steps",
			sc: Scenario{
				StepSource: stub,
				Steps:      []Step{{UserInput: "hi"}},
			},
			wantErr: true,
		},
		{
			name:    "LiveModel without LiveClient",
			sc:      Scenario{LiveModel: "gemma-4"},
			wantErr: true,
		},
		{
			name:    "LiveClient without LiveModel",
			sc:      Scenario{LiveClient: liveClient},
			wantErr: true,
		},

		// --- valid shapes: must pass ---
		{
			name:    "empty scenario",
			sc:      Scenario{},
			wantErr: false,
		},
		{
			name:    "handwritten Steps only",
			sc:      Scenario{Steps: []Step{{UserInput: "hi"}}},
			wantErr: false,
		},
		{
			name:    "StepSource only",
			sc:      Scenario{StepSource: stub},
			wantErr: false,
		},
		{
			name: "day-close handler with cadence (sim shape)",
			sc: Scenario{
				StepSource:            stub,
				HeavyInvariantCadence: 24 * time.Hour,
				OnSimDayClose:         func(*Harness, int, time.Time) {},
			},
			wantErr: false,
		},
		{
			name: "paired LiveClient and LiveModel",
			sc: Scenario{
				StepSource: stub,
				LiveClient: liveClient,
				LiveModel:  "gemma-4",
			},
			wantErr: false,
		},
		{
			name:    "cadence without day-close handler",
			sc:      Scenario{StepSource: stub, HeavyInvariantCadence: 24 * time.Hour},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.sc.validate()
			if tc.wantErr && err == nil {
				t.Fatalf("validate() = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validate() = %v, want nil", err)
			}
		})
	}
}

// stepSourceFunc adapts a func to the StepSource interface for the validate
// table (the source is never driven; validate only inspects nilness).
type stepSourceFunc func(StepFeedback) (Step, bool)

func (f stepSourceFunc) Next(fb StepFeedback) (Step, bool) { return f(fb) }
