package scenarios

import "testing"

// TestRuntimeEmitsThreadCreatedMarker is the BD-2 runtime-driven contract test
// for the `thread.created` log marker (companion to
// TestRuntimeEmitsMatchFireMarker for the match-fire marker). It drives a real
// turn that the runtime creates a thread on, then asserts the harness's
// created-set folding OBSERVED that creation.
//
// The harness folds createdThreadIDs by substring-scraping internal/turn's
// `thread.created` event line (foldEventLines → eventThreadID "thread.created ",
// emitted by turn/engage.go). That is a silent-failure seam: if the runtime
// renames the marker, the fold silently misses every creation, createdThreadIDs
// goes dark, and VerifyThreadAccounting's reverse direction is the only thing
// left to notice. This test pins the contract directly at the source — a rename
// of the runtime's emission breaks THIS test (a loud, specific failure), not a
// downstream measurement that quietly reads zero.
func TestRuntimeEmitsThreadCreatedMarker(t *testing.T) {
	sc := Scenario{
		Name: "thread-created-marker-contract",
		Steps: []Step{
			{
				UserInput: "new topic about topology — #trefoil #unknot #body-topology #electron-shape",
				MockResponse: NewMockResponseWithTag([]string{"*new-topic*"},
					[]string{"trefoil", "unknot", "body-topology", "electron-shape"},
					"First pass on topology."),
				Annotation:            "runtime creates thr_1",
				ExpectedRecallMatches: []string{},
			},
		},
	}
	h := RunScenario(t, sc)

	// The runtime created thr_1 this run; the fold must have observed it via the
	// thread.created marker. A rename in turn/engage.go zeroes this set.
	if _, ok := h.createdThreadIDs["thr_1"]; !ok {
		t.Fatalf("harness created-set did not observe thr_1 via the thread.created marker — marker drift in turn/engage.go? created=%v",
			sortedKeys(h.createdThreadIDs))
	}

	// And the two-directional accounting invariant agrees: thr_1 is run-created,
	// on the spine, and folded into the created set (reverse direction green).
	if err := VerifyThreadAccounting(h); err != nil {
		t.Fatalf("VerifyThreadAccounting after a real creation: %v", err)
	}
}
