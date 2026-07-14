package sim

import (
	"strings"
	"testing"

	"personant/internal/scenarios"
)

// TestClockMonotonicUnderRefinement is the burndown A3 regression. Under the
// live-inference interleaving a recall-opportunity turn MISSES, so the
// generator injects execution-time refinement turns that advance the harness
// pinnedClock OFF the planned timeline; a step buffered to fire at the
// preceding natural turn's instant (the zero-TimeDelta wander/main/intra
// injected steps, and a following rapid turn) must then NOT emit an At that
// regresses the clock. Before the fix the trailing main-thread engage step
// (the step-51 shape from the 1d live run) carried the natural turn's earlier
// instant and tripped the stepSetClock non-monotonic tripwire.
//
// The test drives the generator directly (aim the harness at the unit),
// replicating stepSetClock's clock path — the ONE pinnedClock-advance path —
// and feeds a MISS on every recall opportunity to force maximal refinement
// injection. It asserts the reconstructed pinnedClock never regresses across
// the whole run, and that at least one refinement actually fired (so the
// assertion is not vacuous).
func TestClockMonotonicUnderRefinement(t *testing.T) {
	corpus := loadCorpusSlots(t)
	g := GenerateWorkload(WorkloadConfig{
		Seed: simSeed, Duration: simDayDuration, Corpus: corpus,
	}).StepSource.(*generator)

	// Mirror the harness's pinnedClock, which defaults to SimClockStart
	// (harness_setup.go). This is the independent oracle: it is NOT g.execClock,
	// so a fix that only zeroed execClock could not fool it.
	pinned := scenarios.SimClockStart
	fb := scenarios.StepFeedback{Index: -1}
	var refinements, recallOpps int
	for i := 0; ; i++ {
		step, ok := g.Next(fb)
		if !ok {
			break
		}
		if strings.HasPrefix(step.Annotation, "refinement") {
			refinements++
		}

		// Replicate stepSetClock exactly: a SleepCycle step is routed off the
		// clock path (no advance); an At-less step normalizes to
		// pinnedClock + TimeDelta; otherwise At is authoritative. Assert
		// monotonicity BEFORE advancing.
		if !step.SleepCycle {
			at := step.At
			if at.IsZero() {
				at = pinned.Add(step.TimeDelta)
			}
			if at.Before(pinned) {
				t.Fatalf("step %d (%s): non-monotonic clock: Step.At %s before prior pinnedClock %s",
					i+1, step.Annotation, at, pinned)
			}
			pinned = at
		}

		// Build the next feedback. On a genuine recall-opportunity step report a
		// MISS (fires 0 < forgiven, both > 0) to drive the refinement loop; a
		// campaign/probe step that declares no expected matches gets neutral
		// feedback and opens no episode.
		fb = scenarios.StepFeedback{Index: i}
		if n := len(step.ExpectedRecallMatches); n > 0 {
			recallOpps++
			fb.RecallExpected = n
			fb.RecallExpectedForgiven = n
			fb.RecallMatchFires = 0 // miss
		}
	}

	if recallOpps == 0 {
		t.Fatal("no recall opportunities in the run — cannot drive the refinement loop")
	}
	if refinements == 0 {
		t.Fatal("no refinement turns injected — miss feedback did not drive the refinement loop; " +
			"the monotonicity assertion would be vacuous")
	}
}
