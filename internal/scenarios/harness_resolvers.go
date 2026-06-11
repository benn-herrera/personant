package scenarios

import (
	"context"
	"errors"
	"testing"

	"personant/internal/turn"
)

// recallResolverFor builds a turn.RecallResolver from a step's scripted
// RecallAck. A nil ack yields a nil resolver — recall stays log-only.
// Otherwise the resolver accepts every offered candidate whose ThreadID
// is in ack.AcceptThreadIDs and fails the test if a wanted ID never
// appeared in the offer (a stale or mis-specified scenario).
func recallResolverFor(t *testing.T, idx int, label string, ack *RecallAck) turn.RecallResolver {
	if ack == nil {
		return nil
	}
	return func(_ context.Context, offer turn.RecallOffer) (turn.RecallResolution, error) {
		want := make(map[string]bool, len(ack.AcceptThreadIDs))
		for _, id := range ack.AcceptThreadIDs {
			want[id] = false // false = not yet seen in the offer
		}
		var accept []int
		for i, c := range offer.Candidates {
			if _, ok := want[c.ThreadID]; ok {
				accept = append(accept, i)
				want[c.ThreadID] = true
			}
		}
		for id, seen := range want {
			if !seen {
				t.Errorf("scenario step %d (%s): RecallAck accepts %s but it was not in the recall offer",
					idx+1, label, id)
			}
		}
		reason := ack.Reason
		if reason == "" {
			reason = turn.DeclineNotRelevant
		}
		return turn.RecallResolution{Accept: accept, Reason: reason}, nil
	}
}

// closureResolverFor builds a turn.ClosureResolver from a step's
// scripted ClosureAck. A nil ack yields a nil resolver — closure stays
// detection-disabled for the step (mirrors recallResolverFor). A
// non-nil ack applies its Outcome to every closure offer the step
// surfaces.
func closureResolverFor(ack *ClosureAck) turn.ClosureResolver {
	if ack == nil {
		return nil
	}
	return func(_ context.Context, _ turn.ClosureOffer) (turn.ClosureResolution, error) {
		return turn.ClosureResolution{Outcome: ack.Outcome}, nil
	}
}

// perStepInvariants resolves the invariant list to run after a step,
// applying the derived-day-tick heavy-cadence policy when the scenario
// opted into it (§3.5/M1).
//
//   - A step with an explicit Step.Invariants list bypasses the policy
//     entirely: that exact list runs (the step is expressing a precise
//     intent, e.g. "run only VerifyClosedThreadConsistency here").
//   - Otherwise, when Scenario.HeavyInvariantCadence is zero, the full
//     DefaultInvariants suite fires (legacy behavior — handwritten
//     scenarios are short and want the per-step assurance).
//   - Otherwise the cheap subset fires every step; the heavy subset
//     fires on a step that closed at least one sim-day — i.e. the DERIVED
//     day index (SimDayIndex of the slaved pinnedClock, §3.4) ticked past
//     h.lastClosedDay. This is the SAME tick the day-close fires on (M1:
//     heavy-invariant firing and day-close were one nextHeavyAt `if`;
//     they fold onto one derived trigger). runStep does the actual
//     advance of lastClosedDay + the per-day onSimDayClose fires, AFTER
//     these heavy invariants have run on settled state.
func perStepInvariants(h *Harness, step Step) []InvariantCheck {
	if len(step.Invariants) > 0 {
		return step.Invariants
	}
	if h.heavyCadence == 0 {
		return DefaultInvariants
	}
	if SimDayIndex(h.pinnedClock) > h.lastClosedDay {
		// At least one sim-day boundary closed this step (pinnedClock was set
		// to Step.At before the invariants run). Fire the heavy subset; runStep
		// then closes each crossed day (heavy is idempotent on the one settled
		// state, so it runs once regardless of how many days the catch-up loop
		// will close).
		return DefaultInvariants
	}
	return cheapDefaultInvariants
}

// runInvariants executes every check, calling t.Errorf on failure.
// Skip-marker errors (errSkipPhase) are surfaced via t.Logf so the
// developer sees what's deferred without polluting the error count.
func runInvariants(t *testing.T, h *Harness, invs []InvariantCheck, label string) {
	t.Helper()
	for _, inv := range invs {
		if inv == nil {
			continue
		}
		if err := inv(h); err != nil {
			var sk skipPhaseErr
			if errors.As(err, &sk) {
				t.Logf("scenario %s — invariant skipped (%s): %v", h.T.Name(), label, err)
				continue
			}
			t.Errorf("scenario %s [%s]: %v", h.T.Name(), label, err)
		}
	}
}
