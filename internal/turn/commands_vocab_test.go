package turn

import (
	"context"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// The turn-side half of the user-facing vocabulary gate (user ruling
// 2026-08-05, extended to the substrate 2026-08-06).
//
// internal/chat's vocabulary_test holds the FRONT END's strings to the
// ruling, but a slash command's failure text is composed HERE and reaches
// the user verbatim through chat's `command error: %v`. Sweeping those
// strings without a gate on this side leaves the enforcement one package
// short of the surface it is protecting.
//
// Deliberately NOT in scope, exactly as on the chat side: §2.8 event-log
// text and its `thr=` detail strings (machine vocabulary, parsed by the sim
// oracle and the fidelity harness) and the `thr_N` id scheme, which the
// user types back. Every log call in commands.go builds its own detail
// string; none of them formats one of the errors below.
const vocabForbidden = "thread"

// TestCommandErrorVocabulary exercises every error constructor a user can
// reach from a slash command and fails on the forbidden word. The cases
// are the reachable failure modes of ResolveThreadRef / targetThread —
// bad id, foreign project, unmatched name, ambiguous name, empty ref, and
// no active topic — plus the /done pre-flight.
func TestCommandErrorVocabulary(t *testing.T) {
	paths, meta := newTestHome(t)
	// Two same-project threads sharing a word, so a name lookup can be
	// ambiguous; one foreign-project thread for the cross-project decline.
	seedActiveThread(t, paths, meta.ID, "thr_1", 3, []string{"alpha", "beta"})
	seedActiveThread(t, paths, meta.ID, "thr_2", 3, []string{"gamma", "delta"})
	seedActiveThread(t, paths, "prj_other", "thr_9", 3, []string{"epsilon"})

	newState := func() *State {
		return NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{},
			model.NewScriptedMock(nil, nil))
	}
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(state *State) error
	}{
		{"unknown id", func(s *State) error {
			_, err := ResolveThreadRef(ctx, s, "thr_404")
			return err
		}},
		{"foreign project", func(s *State) error {
			_, err := ResolveThreadRef(ctx, s, "thr_9")
			return err
		}},
		{"no name match", func(s *State) error {
			_, err := ResolveThreadRef(ctx, s, "nothing matches this")
			return err
		}},
		{"ambiguous name", func(s *State) error {
			// Both seeded records' summaries end in " summary".
			_, err := ResolveThreadRef(ctx, s, "summary")
			return err
		}},
		{"empty ref", func(s *State) error {
			_, err := ResolveThreadRef(ctx, s, "  ")
			return err
		}},
		{"no active topic", func(s *State) error {
			// Empty working set → no current owner for a bare /pause.
			s.ActiveThreads = nil
			_, err := PauseThread(ctx, s, "")
			return err
		}},
		{"empty topic name", func(s *State) error {
			_, err := CreateTopic(ctx, s, "  ")
			return err
		}},
		{"rename with nothing engaged", func(s *State) error {
			s.ActiveThreads = nil
			_, _, err := RenameTopic(ctx, s, "braid groups")
			return err
		}},
		{"rename to an empty name", func(s *State) error {
			s.ActiveThreads = []string{"thr_1"}
			_, _, err := RenameTopic(ctx, s, "  ")
			return err
		}},
		{"done without curator", func(s *State) error {
			return ManualClosure(ctx, s, "thr_1")
		}},
		{"done on unknown id", func(s *State) error {
			// With the flow installed, /done's failure comes from the shared
			// ref resolver — the path a user actually hits.
			s.Curator = stubCurator{summary: "gist"}
			s.ClosureResolver = func(context.Context, ClosureOffer) (ClosureResolution, error) {
				return ClosureResolution{Outcome: ClosureDefer}, nil
			}
			return ManualClosure(ctx, s, "thr_404")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(newState())
			if err == nil {
				t.Fatalf("%s: got nil error, want a user-facing failure", tt.name)
			}
			if strings.Contains(strings.ToLower(err.Error()), vocabForbidden) {
				t.Errorf("%s says %q to the user — the ruling is \"topic\": %v",
					tt.name, vocabForbidden, err)
			}
		})
	}
}

// TestThreadDisplayGist covers the ONE §2.2.2 fallback chain the recall
// offer and the closure offer both read (display.go). The blank-gist case
// is the contract the callers depend on: threadGist yields "" rather than
// inventing filler, because the right last resort is the renderer's call.
func TestThreadDisplayGist(t *testing.T) {
	tests := []struct {
		name              string
		rec               memops.SpineRecord
		display, gist     string
		wantDisplayIsID   bool
		wantGistIsBlanked bool
	}{
		{
			name:    "description and summary",
			rec:     memops.SpineRecord{ID: "thr_1", Description: "why is it separating", Summary: "thickener at 0.4"},
			display: "why is it separating", gist: "thickener at 0.4",
		},
		{
			name:    "summary doubles as the name",
			rec:     memops.SpineRecord{ID: "thr_2", Description: "knot theory", Summary: "knot theory", Anchors: []string{"braid", "genus"}},
			display: "knot theory", gist: "braid, genus",
		},
		{
			name:    "no description",
			rec:     memops.SpineRecord{ID: "thr_3", Summary: "supplier shortlist"},
			display: "supplier shortlist", gist: "",
		},
		{
			name:    "anchors capped",
			rec:     memops.SpineRecord{ID: "thr_4", Description: "d", Anchors: []string{"a", "b", "c", "d", "e"}},
			display: "d", gist: "a, b, c, d",
		},
		{
			name:    "nothing at all",
			rec:     memops.SpineRecord{ID: "thr_5"},
			display: "thr_5", gist: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := threadDisplay(tt.rec); got != tt.display {
				t.Errorf("threadDisplay = %q, want %q", got, tt.display)
			}
			if got := threadGist(tt.rec); got != tt.gist {
				t.Errorf("threadGist = %q, want %q", got, tt.gist)
			}
		})
	}
}
