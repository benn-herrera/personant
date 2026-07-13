package turn

import (
	"context"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// §5.5 mid-turn reprompt cross-project hole (wave-2 fixup, BD-7 follow-up).
// fetchThreadForReprompt must apply the SAME §3.2 cross-project policy the
// turn-close engagement resolver (resolveEngagedThread) applies: a mid-turn
// fetch of a thread belonging to another project is declined, never promoted
// into Layer B. The two seams share crossProjectDecline so the policy lives in
// ONE place.

// TestFetchThreadForReprompt_CrossProjectDeclined — a reprompt tag naming a
// foreign-project thread is declined: the fetch returns false, the thread is
// NOT promoted into ActiveThreads, and it is NOT marked recallSurfaced (the
// thread.fetched delta never fired). Behavioral proof that the decline branch
// ran; the forensic decline log is emitted for the human, not asserted here
// (a log-string match would checksum the message, not the policy).
func TestFetchThreadForReprompt_CrossProjectDeclined(t *testing.T) {
	paths, meta := newTestHome(t)
	// thr_foreign belongs to another project; the active project is meta.ID.
	seedActiveThread(t, paths, "prj_other", "thr_foreign", 3, []string{"alpha", "beta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))

	if fetchThreadForReprompt(context.Background(), state, "thr_foreign") {
		t.Fatalf("fetchThreadForReprompt(foreign-project thread) returned true; want false (declined)")
	}
	// Not promoted anywhere in the working set.
	for _, id := range state.ActiveThreads {
		if id == "thr_foreign" {
			t.Errorf("declined foreign thread present in ActiveThreads %v", state.ActiveThreads)
		}
	}
	for _, id := range state.DormantThreads {
		if id == "thr_foreign" {
			t.Errorf("declined foreign thread present in DormantThreads %v", state.DormantThreads)
		}
	}
	// The decline runs BEFORE promoteToLayerB, so recallSurfaced is never
	// marked (the thread.fetched delta never fired either).
	if _, ok := state.recallSurfaced["thr_foreign"]; ok {
		t.Errorf("declined foreign thread marked recallSurfaced; got %v", state.recallSurfaced)
	}
}

// TestFetchThreadForReprompt_SameProjectPromotes — a same-project reprompt is
// unchanged by the cross-project guard: the fetch succeeds and promotes the
// thread to the front of ActiveThreads (the guard is inert for the active
// project, including the empty-project sentinel handled by crossProjectDecline).
func TestFetchThreadForReprompt_SameProjectPromotes(t *testing.T) {
	paths, meta := newTestHome(t)
	seedActiveThread(t, paths, meta.ID, "thr_local", 3, []string{"alpha", "beta"})

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))

	if !fetchThreadForReprompt(context.Background(), state, "thr_local") {
		t.Fatalf("fetchThreadForReprompt(same-project thread) returned false; want success")
	}
	if len(state.ActiveThreads) == 0 || state.ActiveThreads[0] != "thr_local" {
		t.Errorf("same-project fetch must promote thr_local to front of ActiveThreads; got %v", state.ActiveThreads)
	}
	if _, ok := state.recallSurfaced["thr_local"]; !ok {
		t.Errorf("same-project fetch must mark thr_local recallSurfaced; got %v", state.recallSurfaced)
	}
}
