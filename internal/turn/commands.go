package turn

// commands.go is the exported seam the chat REPL drives for user-initiated
// slash commands (§4.2) that must reach turn-package internals: the Layer
// B/C working-set primitives (touchActiveLRU / promoteToLayerB), the §3.5
// closure flow (applyClosureResolution), and the thread state-write pattern
// that mirrors spine + frontmatter. Slash commands run BETWEEN turns (not
// inside Run), so each function here owns its own persistence: it saves the
// working set, and takes a §3.11 structural checkpoint only when it created
// or retired a thread. State transitions that are not §3.11-structural
// (pause / resume / re-engage) ride the session-close commit like any other
// content-only change.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
)

// CurrentOwnerThread returns the thread the user is currently "in" — the
// front of Layer B (ActiveThreads[0]) — or "" when the working set is
// empty. It is the default target for /done and /pause when the user gives
// no explicit thread reference.
func CurrentOwnerThread(state *State) string {
	if len(state.ActiveThreads) > 0 {
		return state.ActiveThreads[0]
	}
	return ""
}

// ResolveThreadRef resolves a user-typed thread reference — either a
// thr_<n> id or a working name — to a concrete thread id within the active
// project. A name matches case-insensitively against a thread's description
// or summary (exact preferred, else substring). An ambiguous name is an
// error naming the candidates so the user can disambiguate with an id.
func ResolveThreadRef(ctx context.Context, state *State, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("command: empty thread reference")
	}
	if memops.ThreadIDPattern.MatchString(ref) {
		rec, found, err := state.Ops.FindThread(ctx, ref)
		if err != nil {
			return "", fmt.Errorf("command: find thread %s: %w", ref, err)
		}
		if !found {
			return "", fmt.Errorf("command: no such thread %s", ref)
		}
		if rec.Project != "" && rec.Project != state.ActiveProject.ID {
			return "", fmt.Errorf("command: thread %s belongs to another project", ref)
		}
		return ref, nil
	}

	recs, err := state.Ops.ListThreads(ctx, memops.ThreadFilter{Project: state.ActiveProject.ID})
	if err != nil {
		return "", fmt.Errorf("command: list threads: %w", err)
	}
	lref := strings.ToLower(ref)
	var exact, partial []string
	for _, r := range recs {
		switch {
		case strings.EqualFold(r.Description, ref) || strings.EqualFold(r.Summary, ref):
			exact = append(exact, r.ID)
		case strings.Contains(strings.ToLower(r.Description), lref) ||
			strings.Contains(strings.ToLower(r.Summary), lref):
			partial = append(partial, r.ID)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("command: no thread matching %q", ref)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("command: %q matches multiple threads (%s); use a thr_ id",
			ref, strings.Join(matches, ", "))
	}
}

// CreateTopic implements /topic <name>: it creates a new thread with the
// given working name and engages it (Layer B front) so the next turn's
// content binds to it — the user-driven analogue of the model emitting a
// *new-topic* tag (§4.2). The thread is created metadata-only (TurnCount 0,
// no excerpt); the first turn against it writes the first excerpt. A
// creation is a §3.11 structural change, so it takes a checkpoint.
//
// Engagement uses touchActiveLRU, not promoteToLayerB: a user-forced topic
// is DIRECT engagement, not a recall surface, so it must not acquire the
// §2.7.3 recallSurfaced provenance marker.
func CreateTopic(ctx context.Context, state *State, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("topic: empty topic name")
	}
	newID, err := state.Ops.NextThreadID(ctx)
	if err != nil {
		return "", fmt.Errorf("topic: next id: %w", err)
	}
	now := clock.Timeline().Format(time.RFC3339)
	rec := memops.SpineRecord{
		ID:              newID,
		Project:         state.ActiveProject.ID,
		Summary:         name,
		Description:     name,
		State:           memops.ThreadActive,
		Created:         now,
		LastEngaged:     now,
		StateChanged:    now,
		TurnCount:       0,
		LastEngagedTurn: state.TurnNumber,
	}
	fm := frontmatterFromSpine(rec)
	if err := state.Ops.CreateThread(ctx, memops.ThreadWrite{Spine: rec, Meta: fm}); err != nil {
		return "", fmt.Errorf("topic: create thread %s: %w", newID, err)
	}
	if err := state.Ops.Log(ctx, memops.LogCategoryThread, "created",
		newID+" via=slash-topic project="+state.ActiveProject.ID); err != nil {
		return "", err
	}
	touchActiveLRU(state, newID)
	persistWorkingSet(ctx, state)
	checkpointCommand(ctx, state, "structural: +1 thread (/topic)")
	return newID, nil
}

// PauseThread implements /pause: it transitions the thread to paused
// (§2.2.1) and removes it from the working set. ref may be a thr_<n> id, a
// working name, or "" (the current owner thread).
func PauseThread(ctx context.Context, state *State, ref string) (string, error) {
	id, err := targetThread(ctx, state, ref)
	if err != nil {
		return "", err
	}
	if _, err := writeThreadState(ctx, state, id, memops.ThreadPaused, "slash-pause"); err != nil {
		return "", err
	}
	state.ActiveThreads = removeString(state.ActiveThreads, id)
	state.DormantThreads = removeString(state.DormantThreads, id)
	persistWorkingSet(ctx, state)
	return id, nil
}

// ResumeThread implements /resume: it transitions the thread back to active
// (§2.2.1) and re-adds it to Layer B. ref may be a thr_<n> id, a working
// name, or "" (the current owner thread).
func ResumeThread(ctx context.Context, state *State, ref string) (string, error) {
	return reEngage(ctx, state, ref, "slash-resume")
}

// BackToThread implements /back-to <thr_id|name>: explicit re-engagement of
// a thread (§4.2). It resurrects the thread to active (§2.2.1) and promotes
// it into Layer B through the shared recall-promotion chokepoint
// (promoteToLayerB), so membership and the §2.7.3 recallSurfaced provenance
// marker are set exactly as a §3.4 recall-accept — a /back-to is the manual
// analogue of accepting a recalled thread.
func BackToThread(ctx context.Context, state *State, ref string) (string, error) {
	return reEngage(ctx, state, ref, "slash-back-to")
}

// reEngage is the shared body of /resume and /back-to: resolve the ref,
// resurrect to active, promote to Layer B, persist the working set. A
// re-engagement is not a §3.11 structural change (no create/retire), so it
// rides the session-close commit.
func reEngage(ctx context.Context, state *State, ref, via string) (string, error) {
	id, err := targetThread(ctx, state, ref)
	if err != nil {
		return "", err
	}
	if _, err := writeThreadState(ctx, state, id, memops.ThreadActive, via); err != nil {
		return "", err
	}
	promoteToLayerB(state, id)
	persistWorkingSet(ctx, state)
	return id, nil
}

// ManualClosure implements /done: it runs the §3.5 closure flow on demand
// for one thread — curator draft, interactive resolution, and the same
// apply path as the decay-triggered scan (applyClosureResolution). ref is
// the thread id/name, or "" for the current owner. Requires a Curator and
// ClosureResolver to be installed (as the decay scan does).
//
// A retire or WIP outcome is a §3.11 structural change: this takes a
// checkpoint when applyClosureResolution advanced the structural counters.
func ManualClosure(ctx context.Context, state *State, ref string) error {
	if state.Curator == nil || state.ClosureResolver == nil {
		return errors.New("done: closure unavailable (no curator/resolver configured)")
	}
	id, err := targetThread(ctx, state, ref)
	if err != nil {
		return err
	}
	rec, found, err := state.Ops.FindThread(ctx, id)
	if err != nil {
		return fmt.Errorf("done: find thread %s: %w", id, err)
	}
	if !found {
		return fmt.Errorf("done: thread %s not in spine", id)
	}
	thr, err := state.Ops.LoadThread(ctx, id)
	if err != nil {
		return fmt.Errorf("done: load thread %s: %w", id, err)
	}
	draft, err := state.Curator.DraftClosure(ctx, thr)
	if err != nil {
		return fmt.Errorf("done: draft closure for %s: %w", id, err)
	}
	if err := state.Ops.Log(ctx, memops.LogCategoryRetire, "prompt",
		"thr="+id+" trigger=manual"); err != nil {
		return err
	}
	offer := ClosureOffer{
		ThreadID: id,
		Summary:  draft.Summary,
		Anchors:  draft.Anchors,
		State:    rec.State,
	}
	res, err := state.ClosureResolver(ctx, offer)
	if err != nil {
		return fmt.Errorf("done: resolve closure for %s: %w", id, err)
	}

	before := state.structuralRetires + state.structuralWIPs
	if err := applyClosureResolution(ctx, state, id, draft, res); err != nil {
		return fmt.Errorf("done: apply closure for %s: %w", id, err)
	}
	// applyClosureResolution evicts a retired/WIP thread from the working
	// set; persist it. Checkpoint only when a structural (retire/WIP)
	// transition actually happened — a defer reaches no write.
	persistWorkingSet(ctx, state)
	if state.structuralRetires+state.structuralWIPs > before {
		checkpointCommand(ctx, state, "structural: /done closure")
	}
	return nil
}

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

// targetThread resolves the thread a command acts on: the explicit ref when
// given, else the current owner thread (Layer B front). An empty ref with
// no current owner is an error naming the missing argument.
func targetThread(ctx context.Context, state *State, ref string) (string, error) {
	if strings.TrimSpace(ref) != "" {
		return ResolveThreadRef(ctx, state, ref)
	}
	if id := CurrentOwnerThread(state); id != "" {
		return id, nil
	}
	return "", errors.New("command: no active thread; specify a thread id or name")
}

// writeThreadState mirrors a thread's spine record + frontmatter with a new
// state and a fresh state_changed, using the updateExistingThread /
// applyClosureResolution write pattern (EngageThread with an empty
// TurnExcerpt — a meta-only update that leaves turns/ untouched). It does
// NOT re-project anchors or touch history_symbols: a state transition is
// not an engagement. The adapter recreates a missing thread dir on write,
// so a missing on-disk file is synthesized from the spine rather than
// failing.
func writeThreadState(ctx context.Context, state *State, threadID string, newState memops.ThreadState, via string) (memops.SpineRecord, error) {
	rec, found, err := state.Ops.FindThread(ctx, threadID)
	if err != nil {
		return memops.SpineRecord{}, fmt.Errorf("command: find thread %s: %w", threadID, err)
	}
	if !found {
		return memops.SpineRecord{}, fmt.Errorf("command: thread %s not in spine", threadID)
	}
	if rec.Project != "" && rec.Project != state.ActiveProject.ID {
		return memops.SpineRecord{}, fmt.Errorf("command: thread %s belongs to another project", threadID)
	}
	fm, err := state.Ops.LoadThreadMeta(ctx, threadID)
	if err != nil && !errors.Is(err, memops.ErrThreadFileNotFound) {
		return memops.SpineRecord{}, fmt.Errorf("command: load thread %s: %w", threadID, err)
	}
	if errors.Is(err, memops.ErrThreadFileNotFound) {
		fm = frontmatterFromSpine(rec)
	}

	now := clock.Timeline().Format(time.RFC3339)
	rec.State = newState
	rec.StateChanged = now

	fm.ID = rec.ID
	fm.Project = rec.Project
	fm.State = newState
	fm.StateChanged = now
	fm.Summary = rec.Summary
	fm.Description = rec.Description
	fm.Anchors = append([]string(nil), rec.Anchors...)
	if fm.Created == "" {
		fm.Created = rec.Created
	}
	fm.LastEngaged = rec.LastEngaged
	fm.LastEngagedTurn = rec.LastEngagedTurn
	fm.TurnCount = rec.TurnCount
	fm.RecallFires = rec.RecallFires

	if err := state.Ops.EngageThread(ctx, memops.ThreadWrite{Spine: rec, Meta: fm}); err != nil {
		return memops.SpineRecord{}, fmt.Errorf("command: write thread %s: %w", threadID, err)
	}
	if err := state.Ops.Log(ctx, memops.LogCategoryThread, "state-change",
		"thr="+threadID+" state="+string(newState)+" via="+via); err != nil {
		return memops.SpineRecord{}, err
	}
	return rec, nil
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

// checkpointCommand takes a §3.11 structural checkpoint for a command that
// created or retired a thread. Non-fatal (log and continue): the
// session-close commit is the backstop, matching the turn-close cadence.
func checkpointCommand(ctx context.Context, state *State, reason string) {
	if err := state.Ops.Checkpoint(ctx, reason); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategorySession, "checkpoint-error",
			memops.SanitizeDetail(err.Error()))
	}
}
