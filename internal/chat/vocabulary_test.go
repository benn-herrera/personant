package chat

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/recall/measure"
	"personant/internal/turn"
)

// The user-facing vocabulary gate (user ruling 2026-08-05).
//
// The ruling — user-facing surfaces say "topic"; the storage layer keeps
// "thread" — is only worth having if it survives the next person who writes
// a string. So it is not a style note: this test renders the front end's
// user-facing text and fails on the word, everywhere except the two places
// the ruling itself allows.
//
// Deliberately NOT in scope, and why:
//   - §2.8 event-log text. Machine-read, parsed by the sim oracle and the
//     recall-fidelity harness; renaming it would break readers to please a
//     reader that does not exist.
//   - `thr_N` ids. An id scheme, not a noun — the user types them back.
//   - Go identifiers (ThreadFilter, PauseThread, …). The port's vocabulary.
const vocabForbidden = "thread"

// vocabAllowed is the ONE user-facing sentence permitted to say "thread":
// the /help hierarchy header's bridge to the storage vocabulary. Naming it
// as a literal (rather than allowing a count) means moving or rewording it
// re-opens the question rather than silently widening the exemption.
const vocabAllowed = "topic    — a conversation strand within the project (stored as thread thr_N)"

// TestHelpTextVocabulary: /help is the densest user-facing surface in the
// program, and the one place the allowed appearance lives.
func TestHelpTextVocabulary(t *testing.T) {
	help := helpText()
	if !strings.Contains(help, vocabAllowed) {
		t.Fatalf("/help is missing the hierarchy bridge line %q:\n%s", vocabAllowed, help)
	}
	// The header is two lines and precedes the command list: a user reading
	// "project" and "topic" as synonyms is what it exists to prevent.
	lines := strings.SplitN(help, "\n", 4)
	if len(lines) < 3 || !strings.HasPrefix(lines[0], "project ") || lines[1] != vocabAllowed || lines[2] != "" {
		t.Errorf("/help does not open with the two-line hierarchy header:\n%s", help)
	}
	assertNoThread(t, "/help", strings.Replace(help, vocabAllowed, "", 1))
}

// TestUserFacingStringsVocabulary renders the rest of the front end's
// user-facing text — the recall offer in both shapes, the closure offer,
// the /topics roster including its empty and hint lines — and holds every
// byte of it to the ruling.
func TestUserFacingStringsVocabulary(t *testing.T) {
	t.Run("recall offer lines", func(t *testing.T) {
		for _, c := range vocabRecallCandidates() {
			assertNoThread(t, "recallOfferLines", strings.Join(recallOfferLines(1, c), "\n"))
		}
	})

	// The offer PREAMBLE — "related topic:" / "related topics:" — is built
	// inside the resolver, so the resolver is what has to be run. "n"
	// declines; the bytes are what the user would have read.
	t.Run("recall offer preamble", func(t *testing.T) {
		for _, name := range []string{"single", "several"} {
			cands := vocabRecallCandidates()
			if name == "single" {
				cands = cands[:1]
			}
			var out bytes.Buffer
			tm := openTestTerm(t, strings.NewReader("n\n"), &out, &out)
			resolve := interactiveRecallResolver(tm, turn.RecallAckBanded)
			if _, err := resolve(context.Background(), turn.RecallOffer{Candidates: cands}); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			assertNoThread(t, "recall offer ("+name+")", out.String())
		}
	})

	t.Run("closure offer", func(t *testing.T) {
		var out bytes.Buffer
		tm := openTestTerm(t, strings.NewReader("s\n"), &out, &out)
		resolve := interactiveClosureResolver(tm)
		_, err := resolve(context.Background(), turn.ClosureOffer{
			ThreadID: "thr_3", Summary: "thickener pinned at 0.4",
		})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		assertNoThread(t, "closure offer", out.String())
	})

	t.Run("topics roster", func(t *testing.T) {
		recs := []memops.SpineRecord{
			topicRec("thr_1", "emulsion separating", "thickener pinned", memops.ThreadActive, time.Hour, time.Hour),
			topicRec("thr_2", "supplier shortlist", "", memops.ThreadPaused, 3*day, 2*day),
			topicRec("thr_3", "shear model choice", "picked Carreau", memops.ThreadResolved, 5*day, 4*day),
		}
		assertNoThread(t, "/topics", strings.Join(renderTopics(recs, []string{"thr_1"}, rosterNow, false), "\n"))
		assertNoThread(t, "/topics (empty)", strings.Join(renderTopics(nil, nil, rosterNow, false), "\n"))
	})
}

// vocabRecallCandidates covers the three evidence kinds the offer renders,
// so no per-tier string escapes the sweep.
func vocabRecallCandidates() []turn.RecallCandidate {
	return []turn.RecallCandidate{
		{
			Result: measure.Result{
				ThreadID: "thr_3", Score: 0.56,
				Symbolic: &measure.SymbolicHit{Score: 0.56, MatchedSymbols: []string{"emulsion"}},
			},
			Display: "why is the emulsion separating", Gist: "thickener ratio pinned at 0.4",
		},
		{
			Result: measure.Result{
				ThreadID: "thr_4", Score: 0.81,
				IntraThread: &measure.IntraThreadHit{Score: 0.81, Turns: []int{12, 40}},
			},
			Display: "the long-running rheology topic", Gist: "shear-thinning model selection",
		},
		{
			Result: measure.Result{
				ThreadID: "thr_9", Score: 0.62,
				Embedding: &measure.EmbeddingHit{Score: 0.62},
			},
			Display: "surfactant sourcing", Gist: "supplier shortlist",
		},
	}
}

// assertNoThread fails on any case-insensitive occurrence of the forbidden
// word, naming the line so the fix is obvious. `thr_N` does not match it.
func assertNoThread(t *testing.T, surface, text string) {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(strings.ToLower(line), vocabForbidden) {
			t.Errorf("%s says %q to the user — the ruling is \"topic\" (see %s):\n  %s",
				surface, vocabForbidden, "the /help hierarchy header for the one exception", line)
		}
	}
}
