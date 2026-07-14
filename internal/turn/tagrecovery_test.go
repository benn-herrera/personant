package turn

// D6 missing-tag recovery tests (burn-down 2026-07b A1): the mid-turn tag
// re-prompt, the close-time owner-default, the conversational asymmetry,
// and the per-cause re-prompt cap interaction with the §5.5 fetch. The
// retracted-abort terminal (owner-default → new thread on an empty working
// window) lives in turn_test.go as
// TestRunFileEditWithoutTopicTagOwnerDefaultsToNewThread.

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/store"
)

// fsWriteDelta is the pre-prompt §3.9 file-edit event the D6 tests buffer
// to make the turn binding-required.
func fsWriteDelta(path string) Delta {
	return Delta{Source: memops.SourceFSWrite, Content: "v1", Meta: map[string]string{"path": path}}
}

// spineTurnCount reads thrID's spine TurnCount, failing the test on any
// read error or a missing record.
func spineTurnCount(t *testing.T, paths store.PersonantPaths, thrID string) int {
	t.Helper()
	records, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	for _, rec := range records {
		if rec.ID == thrID {
			return rec.TurnCount
		}
	}
	t.Fatalf("spine record %s not found", thrID)
	return 0
}

// TestRunTagRepromptRecoversBinding — D6 stage 1. First stream lacks a tag
// while fs.write is buffered → exactly one re-prompt fires with
// prompt.TopicTagReminder appended; the second response's tag binds
// normally (owner, excerpt, sidecar), and no owner-default is needed.
func TestRunTagRepromptRecoversBinding(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "Plain response, no tag. FIRST."},
		{Content: "*topic: thr_42 [a, b, c, d]*\nSECOND."},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	// thr_42 already in Layer B so the second response cannot also trigger
	// a §5.5 fetch — this test isolates the missing-tag cause.
	state.ActiveThreads = []string{"thr_42"}
	pinClock(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

	body, err := RunWithDeltas(context.Background(), state,
		[]Delta{fsWriteDelta("src/a.go")}, "edit it", io.Discard)
	if err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}
	calls := mock.Calls()
	if len(calls) != 2 {
		t.Fatalf("mock call count: got %d want 2 (first stream + tag re-prompt)", len(calls))
	}
	// The reminder must be absent from the first request and present in
	// the re-issued one.
	if strings.Contains(calls[0].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("first request already carries TopicTagReminder")
	}
	if !strings.Contains(calls[1].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("re-issued request missing TopicTagReminder")
	}
	// Aborted first stream must not leak.
	if !strings.Contains(body, "SECOND") || strings.Contains(body, "FIRST") {
		t.Errorf("body: %q; want SECOND only", body)
	}

	// The second response's tag bound normally: thr_42 owns the turn.
	if got := spineTurnCount(t, paths, "thr_42"); got != 2 {
		t.Errorf("thr_42 TurnCount: got %d want 2 (seed 1 + owned turn)", got)
	}
	tf, err := store.LoadThreadFiles(paths, "thr_42")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if _, ok := tf.Entry("src/a.go"); !ok {
		t.Error("sidecar missing src/a.go on thr_42")
	}

	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "topic.re-prompt cause=missing-tag") {
		t.Errorf("log missing missing-tag re-prompt line:\n%s", logBody)
	}
	if strings.Contains(logBody, "tag-defaulted") {
		t.Errorf("owner-default must not fire when the re-prompt recovered the tag:\n%s", logBody)
	}
}

// TestRunOwnerDefaultBindsMostRecentlyEngaged — D6 stage 2 with a populated
// working window. Two prior turns engage thr_42 then thr_43; the third turn
// buffers fs.write and both its responses are tag-less. The owner-default
// must bind the turn to thr_43 — the most recently engaged valid Layer-B
// thread (highest LastEngagedTurn, the D2 fallback-(i) ordering) — write
// the excerpt there, bind the edits there, and emit thread.tag-defaulted
// with reprompted=yes.
func TestRunOwnerDefaultBindsMostRecentlyEngaged(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")
	seedThreadAndSpine(t, paths, meta.ID, "thr_43")

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nT1."},
		{Content: "*topic: thr_43 [a, b, c, d]*\nT2."},
		{Content: "No tag here. T3-FIRST."},
		{Content: "Still no tag. T3-RETRY."},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	// Both threads pre-populated in Layer B so turns 1-2 don't spend §5.5
	// fetches; the queue then maps one response per consult as scripted.
	state.ActiveThreads = []string{"thr_42", "thr_43"}
	pinClock(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

	ctx := context.Background()
	if _, err := Run(ctx, state, "work on 42", io.Discard); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if _, err := Run(ctx, state, "now 43", io.Discard); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if _, err := RunWithDeltas(ctx, state,
		[]Delta{fsWriteDelta("src/b.go")}, "edit it", io.Discard); err != nil {
		t.Fatalf("turn 3: %v", err)
	}
	if got := len(mock.Calls()); got != 4 {
		t.Fatalf("mock call count: got %d want 4 (2 plain turns + tag-less turn with one re-prompt)", got)
	}

	// thr_43 (LastEngagedTurn 2 > thr_42's 1) owns turn 3: excerpt
	// (TurnCount 1+1+1=3) and the sidecar binding. thr_42 stays at 2.
	if got := spineTurnCount(t, paths, "thr_43"); got != 3 {
		t.Errorf("thr_43 TurnCount: got %d want 3 (owner-default owned turn 3)", got)
	}
	if got := spineTurnCount(t, paths, "thr_42"); got != 2 {
		t.Errorf("thr_42 TurnCount: got %d want 2 (not the default owner)", got)
	}
	tf, err := store.LoadThreadFiles(paths, "thr_43")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if _, ok := tf.Entry("src/b.go"); !ok {
		t.Error("sidecar missing src/b.go on default owner thr_43")
	}

	logBody := readDayLog(t, paths)
	want := "thread.tag-defaulted thr=thr_43 cause=missing-tag reprompted=yes"
	if !strings.Contains(logBody, want) {
		t.Errorf("log missing %q:\n%s", want, logBody)
	}
}

// TestRunTaglessConversationalTurnProceedsWithoutRecovery — decision (c):
// a tag-less turn with NO binding-required deltas keeps the pre-D6
// behavior — no re-prompt, no owner-default, no engagement; the turn
// proceeds tag-less with only the topic.tag-missing calibration line. The
// recovery machinery is scoped to turns whose deltas require owner binding.
func TestRunTaglessConversationalTurnProceedsWithoutRecovery(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMock([]model.Response{
		{Content: "Just chatting, no tag."},
	}, nil)
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

	body, err := Run(context.Background(), state, "hello", io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(body, "Just chatting") {
		t.Errorf("body: %q", body)
	}
	if got := len(mock.Calls()); got != 1 {
		t.Fatalf("mock call count: got %d want 1 (no re-prompt on a conversational turn)", got)
	}
	records, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("spine must not advance on a tag-less conversational turn; got %d records", len(records))
	}
	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "topic.tag-missing") {
		t.Errorf("log missing topic.tag-missing calibration line:\n%s", logBody)
	}
	if strings.Contains(logBody, "tag-defaulted") || strings.Contains(logBody, "cause=missing-tag") {
		t.Errorf("recovery machinery fired on a conversational turn:\n%s", logBody)
	}
}

// TestRunRepromptCapOnePerCauseBothCausesOneTurn — decision (d): the two
// re-prompt causes are capped independently, one each. Response 1 is
// tag-less (→ missing-tag re-prompt); response 2 tags a thread outside
// Layer B (→ §5.5 missing-thread re-prompt); response 3 drains. Three
// streams total — the documented ≤ 1 + 2 bound — and the turn binds
// normally with no owner-default.
func TestRunRepromptCapOnePerCauseBothCausesOneTurn(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "No tag. FIRST."},
		{Content: "*topic: thr_42 [a, b, c, d]*\nSECOND."},
		{Content: "*topic: thr_42 [a, b, c, d]*\nTHIRD."},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	// thr_42 NOT in ActiveThreads: response 2's tag triggers the fetch.
	pinClock(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

	body, err := RunWithDeltas(context.Background(), state,
		[]Delta{fsWriteDelta("src/c.go")}, "edit it", io.Discard)
	if err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}
	calls := mock.Calls()
	if got := len(calls); got != 3 {
		t.Fatalf("mock call count: got %d want 3 (one re-prompt per cause)", got)
	}
	if !strings.Contains(body, "THIRD") {
		t.Errorf("body: %q; want THIRD", body)
	}
	// The reminder must survive the same-turn §5.5 fetch recompose: once the
	// tag re-prompt fires, EVERY later recomposition in the turn re-appends
	// TopicTagReminder (buildSystemPrompt reads state.tagReprompted), so the
	// fetch path cannot silently drop it.
	if !strings.Contains(calls[1].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("re-issued request (post tag re-prompt) missing TopicTagReminder")
	}
	if !strings.Contains(calls[2].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("post-fetch recompose dropped TopicTagReminder")
	}

	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "topic.re-prompt cause=missing-tag") {
		t.Errorf("log missing missing-tag re-prompt line:\n%s", logBody)
	}
	if !strings.Contains(logBody, "topic.re-prompt cause=missing-thread") {
		t.Errorf("log missing missing-thread re-prompt line:\n%s", logBody)
	}
	if strings.Contains(logBody, "tag-defaulted") {
		t.Errorf("owner-default must not fire when the tag recovered:\n%s", logBody)
	}

	// The recovered tag bound normally.
	if got := spineTurnCount(t, paths, "thr_42"); got != 2 {
		t.Errorf("thr_42 TurnCount: got %d want 2", got)
	}
	tf, terr := store.LoadThreadFiles(paths, "thr_42")
	if terr != nil {
		t.Fatalf("LoadThreadFiles: %v", terr)
	}
	if _, ok := tf.Entry("src/c.go"); !ok {
		t.Error("sidecar missing src/c.go on thr_42")
	}
}

// TestRunOwnerDefaultSurvivesRestartRecency — F1 (burn-down 2026-07b Wave-A):
// the owner-default must rank by the persisted ActiveThreads order, NOT by
// spine LastEngagedTurn. LastEngagedTurn stores the session-scoped TurnNumber
// (resets on LoadSession), so after a relaunch a prior session's high turn
// count (thr_42 at turn 91) outranked the thread actively worked THIS session
// (thr_43 at turn 2) — anti-recency. Session 1 works thr_42 at a high turn
// number and persists the working set; session 2 (LoadSession) works thr_43,
// then hits a double-miss tag-less binding turn. The default must bind
// thr_43 — the session-current thread — not the stale thr_42.
func TestRunOwnerDefaultSurvivesRestartRecency(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")
	seedThreadAndSpine(t, paths, meta.ID, "thr_43")
	ops := fileadapter.NewFileAdapter(paths)
	pinClock(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	ctx := context.Background()

	// Session 1: one turn on thr_42 late in a long session (TurnNumber 90 →
	// the turn runs as 91, so thr_42's spine LastEngagedTurn persists as 91).
	// Run's step 5d persists ActiveThreads=[thr_42] for session 2 to load.
	mock1 := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nS1."},
	})
	state1 := NewState(ops, meta, memops.Provider{}, mock1)
	state1.ActiveThreads = []string{"thr_42"}
	state1.TurnNumber = 90
	if _, err := Run(ctx, state1, "prior-session work", io.Discard); err != nil {
		t.Fatalf("session 1 turn: %v", err)
	}

	// Session 2: fresh LoadSession (TurnNumber resets to 0, working set
	// reloads [thr_42]). Work thr_43 for two turns — its LastEngagedTurn
	// values (1, 2) are numerically far below thr_42's stale 91. Turn 1's
	// thr_43 tag is outside Layer B, so the §5.5 fetch consumes one extra
	// scripted consult.
	mock2 := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "*topic: thr_43 [a, b, c, d]*\nS2-T1."}, // aborted by fetch
		{Content: "*topic: thr_43 [a, b, c, d]*\nS2-T1."}, // re-issued
		{Content: "*topic: thr_43 [a, b, c, d]*\nS2-T2."},
		{Content: "No tag. S2-T3-FIRST."},
		{Content: "Still no tag. S2-T3-RETRY."},
	})
	state2, err := LoadSession(ctx, ops, meta, memops.Provider{}, mock2)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(state2.ActiveThreads) == 0 || state2.ActiveThreads[0] != "thr_42" {
		t.Fatalf("precondition: loaded working set %v, want [thr_42]", state2.ActiveThreads)
	}
	if _, err := Run(ctx, state2, "switch to 43", io.Discard); err != nil {
		t.Fatalf("session 2 turn 1: %v", err)
	}
	if _, err := Run(ctx, state2, "more 43", io.Discard); err != nil {
		t.Fatalf("session 2 turn 2: %v", err)
	}
	if _, err := RunWithDeltas(ctx, state2,
		[]Delta{fsWriteDelta("src/f1.go")}, "edit it", io.Discard); err != nil {
		t.Fatalf("session 2 turn 3: %v", err)
	}

	// The double-miss turn binds to thr_43 (ActiveThreads[0], the honest
	// persisted recency), not thr_42 (stale prior-session LastEngagedTurn 91).
	logBody := readDayLog(t, paths)
	want := "thread.tag-defaulted thr=thr_43 cause=missing-tag reprompted=yes"
	if !strings.Contains(logBody, want) {
		t.Errorf("log missing %q (anti-recency regression?):\n%s", want, logBody)
	}
	if strings.Contains(logBody, "tag-defaulted thr=thr_42") {
		t.Error("owner-default bound stale prior-session thr_42 (F1 anti-recency bug)")
	}
	tf, err := store.LoadThreadFiles(paths, "thr_43")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if _, ok := tf.Entry("src/f1.go"); !ok {
		t.Error("sidecar missing src/f1.go on session-current thr_43")
	}
}

// TestRunTagRepromptRearmsAcrossTurns — the missing-tag re-prompt cap is
// PER TURN (state.tagReprompted resets at the top of every Run): two
// consecutive tag-less binding turns each get exactly one re-prompt, and
// the fresh turn's FIRST request must not carry a stale TopicTagReminder
// from the prior turn.
func TestRunTagRepromptRearmsAcrossTurns(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "No tag. T1-FIRST."},
		{Content: "*topic: thr_42 [a, b, c, d]*\nT1-RETRY."},
		{Content: "No tag. T2-FIRST."},
		{Content: "*topic: thr_42 [a, b, c, d]*\nT2-RETRY."},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	state.ActiveThreads = []string{"thr_42"}
	pinClock(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

	ctx := context.Background()
	if _, err := RunWithDeltas(ctx, state,
		[]Delta{fsWriteDelta("src/r1.go")}, "edit one", io.Discard); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if _, err := RunWithDeltas(ctx, state,
		[]Delta{fsWriteDelta("src/r2.go")}, "edit two", io.Discard); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	calls := mock.Calls()
	if got := len(calls); got != 4 {
		t.Fatalf("mock call count: got %d want 4 (one re-prompt per turn)", got)
	}
	// Turn 2's first request is a FRESH turn: no leftover reminder.
	if strings.Contains(calls[2].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("turn 2's first request carries a stale TopicTagReminder")
	}
	if !strings.Contains(calls[3].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("turn 2's re-issued request missing TopicTagReminder")
	}
	logBody := readDayLog(t, paths)
	if got := strings.Count(logBody, "topic.re-prompt cause=missing-tag"); got != 2 {
		t.Errorf("missing-tag re-prompt count: got %d want 2 (one per turn):\n%s", got, logBody)
	}
	if strings.Contains(logBody, "tag-defaulted") {
		t.Errorf("owner-default fired though each turn's re-prompt recovered:\n%s", logBody)
	}
	if got := spineTurnCount(t, paths, "thr_42"); got != 3 {
		t.Errorf("thr_42 TurnCount: got %d want 3 (seed 1 + two recovered turns)", got)
	}
}

// TestRunFetchFirstThenTagReprompt — cause ordering fetch-first→tag-second
// (the reverse of TestRunRepromptCapOnePerCauseBothCausesOneTurn, which pins
// tag-first→fetch-second): response 1 tags a thread outside Layer B (§5.5
// fetch re-prompt), response 2 is tag-less with buffered edits (missing-tag
// re-prompt), response 3 tags and binds. Three streams, both causes spent,
// no owner-default; the reminder appears only after the tag cause fires.
func TestRunFetchFirstThenTagReprompt(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "*topic: thr_42 [a, b, c, d]*\nFIRST."}, // fetch fires: thr_42 not in Layer B
		{Content: "No tag. SECOND."},                      // tag re-prompt fires
		{Content: "*topic: thr_42 [a, b, c, d]*\nTHIRD."}, // binds
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))

	body, err := RunWithDeltas(context.Background(), state,
		[]Delta{fsWriteDelta("src/d.go")}, "edit it", io.Discard)
	if err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}
	calls := mock.Calls()
	if got := len(calls); got != 3 {
		t.Fatalf("mock call count: got %d want 3 (fetch re-prompt + tag re-prompt)", got)
	}
	if !strings.Contains(body, "THIRD") {
		t.Errorf("body: %q; want THIRD", body)
	}
	// The fetch recompose precedes the tag cause: no reminder yet on the
	// second request; present on the third.
	if strings.Contains(calls[1].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("post-fetch request carries TopicTagReminder before the tag cause fired")
	}
	if !strings.Contains(calls[2].Request.Messages[0].Content, prompt.TopicTagReminder) {
		t.Error("post-tag-re-prompt request missing TopicTagReminder")
	}

	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "topic.re-prompt cause=missing-thread") {
		t.Errorf("log missing missing-thread re-prompt line:\n%s", logBody)
	}
	if !strings.Contains(logBody, "topic.re-prompt cause=missing-tag") {
		t.Errorf("log missing missing-tag re-prompt line:\n%s", logBody)
	}
	if strings.Contains(logBody, "tag-defaulted") {
		t.Errorf("owner-default must not fire when the tag recovered:\n%s", logBody)
	}
	if got := spineTurnCount(t, paths, "thr_42"); got != 2 {
		t.Errorf("thr_42 TurnCount: got %d want 2", got)
	}
	tf, terr := store.LoadThreadFiles(paths, "thr_42")
	if terr != nil {
		t.Fatalf("LoadThreadFiles: %v", terr)
	}
	if _, ok := tf.Entry("src/d.go"); !ok {
		t.Error("sidecar missing src/d.go on thr_42")
	}
}
