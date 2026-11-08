package turn

// commands.go is the exported seam the chat REPL drives for user-initiated
// slash commands (§4.2) that must reach turn-package internals: the Layer
// B/C working-set primitives (touchActiveLRU / promoteToLayerB) and the
// thread state-write pattern that mirrors spine + frontmatter. Slash
// commands run BETWEEN turns (not inside Run), so each function here owns
// its own persistence: it saves the working set, and takes a §3.11
// structural checkpoint only when it created a thread. State transitions that are not §3.11-structural
// (pause / resume / re-engage) ride the session-close commit like any other
// content-only change.

import (
	"context"

	"personant/internal/memops"
)

// SwitchProject changes the active project mid-session (/project switch,
// §4.5.6). The Layer B/C working set is thread membership for the OUTGOING
// project — closure and recall are project-scoped — so it is reset to empty
// for the incoming project and persisted. v0.1 keeps no per-project
// working-set snapshot, so the switched-to project cold-starts (identical to
// a fresh LoadSession for it); the outgoing project's threads are unchanged
// on disk and reappear via recall / /back-to when it is switched back.
func SwitchProject(ctx context.Context, state *State, target memops.ProjectMeta) error {
	state.ActiveProject = target
	state.ActiveThreads = nil
	state.DormantThreads = nil
	persistWorkingSet(ctx, state)
	return nil
}

// persistWorkingSet saves Layer B/C membership after a command mutated it.
// A save failure is non-fatal (log and continue), consistent with the
// turn-close SaveWorkingSet call.
func persistWorkingSet(ctx context.Context, state *State) {
	if err := state.Ops.SaveWorkingSet(ctx, state.ActiveThreads, state.DormantThreads); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategorySession, "working-set-save-error",
			memops.SanitizeDetail(err.Error()))
	}
}
