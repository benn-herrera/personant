package turn

import (
	"context"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// nameThread gives a seeded thread the §2.2.2 display fields /topic would
// have left on it — Description and Summary set to the same working name —
// through the ordinary meta-only write. seedThreadWithAnchors deliberately
// seeds no description, and the rename's interesting case is precisely the
// record where the two text fields agree.
func nameThread(t *testing.T, state *State, id, name string) {
	t.Helper()
	ctx := context.Background()
	rec, found, err := state.Ops.FindThread(ctx, id)
	if err != nil || !found {
		t.Fatalf("find %s: %v (found=%v)", id, err, found)
	}
	rec.Description, rec.Summary = name, name
	if err := state.Ops.EngageThread(ctx, memops.ThreadWrite{
		Spine: rec, Meta: frontmatterFromSpine(rec),
	}); err != nil {
		t.Fatalf("name %s: %v", id, err)
	}
}

// TestRenameTopicIsDisplayOnly is the load-bearing test for /topic rename:
// the rename is a DISPLAY change and nothing else. It pins all four halves
// of that claim on one renamed thread —
//
//   - the display name moves, in the derived spine AND the canonical §2.3
//     frontmatter (they must not drift);
//   - the anchors do not move, and a §3.4 recall query over the OLD tags
//     still fires spine.match-fire — a renamed topic keeps matching on
//     exactly what it matched on before;
//   - `/back-to <new-name>` resolves;
//   - `/back-to <old-name>` does NOT. Old names are not aliases. A name the
//     roster no longer shows is a name the user cannot see to type, and
//     keeping it live would make two topics collide on it after a reuse.
func TestRenameTopicIsDisplayOnly(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha", "beta", "gamma", "delta"})
	ctx := context.Background()

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	nameThread(t, state, "thr_1", "emulsion separating")
	state.ActiveThreads = []string{"thr_1"}

	id, old, err := RenameTopic(ctx, state, "  braid groups  ")
	if err != nil {
		t.Fatalf("RenameTopic: %v", err)
	}
	if id != "thr_1" || old != "emulsion separating" {
		t.Fatalf("RenameTopic = (%q, %q), want (thr_1, emulsion separating)", id, old)
	}

	rec, found, err := state.Ops.FindThread(ctx, "thr_1")
	if err != nil || !found {
		t.Fatalf("find after rename: %v (found=%v)", err, found)
	}
	if rec.Description != "braid groups" {
		t.Errorf("spine description = %q, want %q", rec.Description, "braid groups")
	}
	// Summary was a duplicate of Description (the /topic-created shape), so
	// it follows — otherwise the old name would resurface as the gist.
	if rec.Summary != "braid groups" {
		t.Errorf("spine summary = %q, want it to follow the rename", rec.Summary)
	}
	if got := strings.Join(rec.Anchors, ","); got != "alpha,beta,gamma,delta" {
		t.Errorf("rename moved the anchors: %q", got)
	}
	if rec.State != memops.ThreadActive || rec.StateChanged != "2026-04-01T00:00:00Z" {
		t.Errorf("rename touched the state fields: state=%q state_changed=%q", rec.State, rec.StateChanged)
	}

	fm, err := state.Ops.LoadThreadMeta(ctx, "thr_1")
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}
	if fm.Description != rec.Description || fm.Summary != rec.Summary {
		t.Errorf("canonical frontmatter (%q/%q) drifted from the spine (%q/%q)",
			fm.Description, fm.Summary, rec.Description, rec.Summary)
	}

	// Recall still fires on the pre-rename tags.
	state.coalesce.addSymbol("alpha", "alpha", memops.SourceUser)
	state.coalesce.addSymbol("beta", "beta", memops.SourceUser)
	if err := surfaceRecallCandidates(ctx, state, "", map[string]struct{}{}, ""); err != nil {
		t.Fatalf("surfaceRecallCandidates: %v", err)
	}
	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "spine.match-fire thr_1 score=0.50 matched=alpha,beta") {
		t.Errorf("a renamed topic stopped matching its own anchors:\n%s", logBody)
	}
	if !strings.Contains(logBody, "thread.renamed thr=thr_1 old=emulsion separating new=braid groups") {
		t.Errorf("missing thread.renamed event:\n%s", logBody)
	}

	// Name resolution follows the display name, and only the current one.
	if got, err := ResolveThreadRef(ctx, state, "braid groups"); err != nil || got != "thr_1" {
		t.Errorf("ResolveThreadRef(new name) = (%q, %v), want thr_1", got, err)
	}
	if _, err := ResolveThreadRef(ctx, state, "emulsion separating"); err == nil {
		t.Error("the old name still resolves — old names must not become aliases")
	}
}

// TestRenameTopicKeepsACuratorSummary: when Summary is genuinely a gist
// rather than a second copy of the name, it is CONTENT and survives the
// rename. Only Description — the display name — moves.
func TestRenameTopicKeepsACuratorSummary(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha"})
	ctx := context.Background()

	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	nameThread(t, state, "thr_1", "emulsion separating")
	rec, _, err := state.Ops.FindThread(ctx, "thr_1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	rec.Summary = "thickener pinned at 0.4"
	if err := state.Ops.EngageThread(ctx, memops.ThreadWrite{Spine: rec, Meta: frontmatterFromSpine(rec)}); err != nil {
		t.Fatalf("seed summary: %v", err)
	}
	state.ActiveThreads = []string{"thr_1"}

	if _, _, err := RenameTopic(ctx, state, "braid groups"); err != nil {
		t.Fatalf("RenameTopic: %v", err)
	}
	after, _, err := state.Ops.FindThread(ctx, "thr_1")
	if err != nil {
		t.Fatalf("find after rename: %v", err)
	}
	if after.Description != "braid groups" {
		t.Errorf("description = %q, want %q", after.Description, "braid groups")
	}
	if after.Summary != "thickener pinned at 0.4" {
		t.Errorf("rename ate the curator summary: %q", after.Summary)
	}
}

// TestRenameTopicGuards: the two refusals — nothing engaged, and an empty
// new name. Duplicate display names are deliberately NOT a refusal: /topic
// creation permits them, so rename does too.
func TestRenameTopicGuards(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadWithAnchors(t, paths, meta.ID, "thr_1", []string{"alpha"})
	seedThreadWithAnchors(t, paths, meta.ID, "thr_2", []string{"beta"})
	ctx := context.Background()
	newState := func() *State {
		return NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	}

	if _, _, err := RenameTopic(ctx, newState(), "braid groups"); err == nil {
		t.Error("rename with an empty working set: got nil error, want no-active-topic")
	}
	engaged := newState()
	engaged.ActiveThreads = []string{"thr_1"}
	if _, _, err := RenameTopic(ctx, engaged, "   "); err == nil {
		t.Error("rename to an empty name: got nil error")
	}

	// Duplicates are allowed, matching CreateTopic. Both topics end up named
	// "shared", and the ambiguity surfaces where it already did — in the
	// name resolver, which names the candidates.
	s := newState()
	s.ActiveThreads = []string{"thr_1"}
	if _, _, err := RenameTopic(ctx, s, "shared"); err != nil {
		t.Fatalf("rename thr_1: %v", err)
	}
	s.ActiveThreads = []string{"thr_2"}
	if _, _, err := RenameTopic(ctx, s, "shared"); err != nil {
		t.Fatalf("rename thr_2 to a duplicate name: %v", err)
	}
	_, err := ResolveThreadRef(ctx, s, "shared")
	if err == nil || !strings.Contains(err.Error(), "matches multiple topics") {
		t.Errorf("duplicate name resolution = %v, want the ambiguity error", err)
	}
}
