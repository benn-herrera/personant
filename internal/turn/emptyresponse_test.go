package turn

// D6 empty-response recovery tests (burn-down 2026-07b, 2026-07-15 probe
// round 3 signature (c): finish=stop with zero visible content — the
// work-switch reasoning-burn shape). The empty-response cause gets its own
// per-cause re-prompt cap; a persistent empty falls through WITHOUT
// spending the missing-tag re-prompt (the empty cause subsumes it): a
// binding turn hits the owner-default with an honest empty excerpt, a
// conversational turn surfaces the empty body, and both leave a
// system.empty-response forensic line. Shares helpers with
// tagrecovery_test.go (fsWriteDelta, spineTurnCount, newTestHome, ...).

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

// TestRunEmptyResponseRepromptRecovers — stage 1 on a conversational turn:
// the first stream is empty → exactly one re-prompt fires with
// prompt.EmptyResponseReminder appended; the second response proceeds
// normally and no persistent-empty forensic line is emitted.
func TestRunEmptyResponseRepromptRecovers(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "", FinishReason: "stop"},
		{Content: "Recovered content, no tag."},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))

	body, err := Run(context.Background(), state, "hello", io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := mock.Calls()
	if got := len(calls); got != 2 {
		t.Fatalf("mock call count: got %d want 2 (first empty stream + re-prompt)", got)
	}
	if strings.Contains(calls[0].Request.Messages[0].Content, prompt.EmptyResponseReminder) {
		t.Error("first request already carries EmptyResponseReminder")
	}
	if !strings.Contains(calls[1].Request.Messages[0].Content, prompt.EmptyResponseReminder) {
		t.Error("re-issued request missing EmptyResponseReminder")
	}
	if !strings.Contains(body, "Recovered content") {
		t.Errorf("body: %q; want the recovered second response", body)
	}

	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "topic.re-prompt cause=empty-response") {
		t.Errorf("log missing empty-response re-prompt line:\n%s", logBody)
	}
	if strings.Contains(logBody, "system.empty-response") {
		t.Errorf("persistent-empty line must not fire when the re-prompt recovered:\n%s", logBody)
	}
}

// TestRunEmptyResponsePersistentBindingOwnerDefault — a binding-required
// turn whose empty re-prompt also comes back empty. KEY cap interaction:
// exactly TWO streams — the missing-tag re-prompt must NOT additionally
// fire on the persistent-empty second response (the empty cause subsumes
// it). The turn falls through to the D6 owner-default: the edits bind to
// the most recently engaged Layer-B thread, the excerpt's agent section is
// honestly empty, and both forensic lines carry cause=empty-response.
func TestRunEmptyResponsePersistentBindingOwnerDefault(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "", FinishReason: "stop"},
		{Content: "", FinishReason: "stop"},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	state.ActiveThreads = []string{"thr_42"}
	pinClock(t, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))

	body, err := RunWithDeltas(context.Background(), state,
		[]Delta{fsWriteDelta("src/e.go")}, "edit it", io.Discard)
	if err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}
	if body != "" {
		t.Errorf("body: %q; want empty (persistent-empty surfaces as-is)", body)
	}
	if got := len(mock.Calls()); got != 2 {
		t.Fatalf("mock call count: got %d want 2 (no missing-tag third stream after a persistent empty)", got)
	}

	// Owner-default bound the turn: thr_42 owns (excerpt + turn_count++)
	// and the sidecar carries the edit.
	if got := spineTurnCount(t, paths, "thr_42"); got != 2 {
		t.Errorf("thr_42 TurnCount: got %d want 2 (seed 1 + owner-defaulted turn)", got)
	}
	tf, err := store.LoadThreadFiles(paths, "thr_42")
	if err != nil {
		t.Fatalf("LoadThreadFiles: %v", err)
	}
	if _, ok := tf.Entry("src/e.go"); !ok {
		t.Error("sidecar missing src/e.go on default owner thr_42")
	}
	// The excerpt's agent section is EMPTY — the honest record of a model
	// that produced nothing (fabricated content would forge the history).
	threadBody, err := store.ReadThreadBody(paths, "thr_42", 1<<20)
	if err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	if !strings.Contains(threadBody, "**user:** edit it") {
		t.Errorf("excerpt missing the user prompt:\n%s", threadBody)
	}
	// The LAST agent section is this turn's (the seeded excerpt precedes
	// it and carries "seed reply").
	if got := strings.TrimSpace(threadBody[strings.LastIndex(threadBody, "**agent:**")+len("**agent:**"):]); got != "" {
		t.Errorf("excerpt agent section must be empty for a persistent-empty response, got %q", got)
	}

	logBody := readDayLog(t, paths)
	want := "thread.tag-defaulted thr=thr_42 cause=empty-response reprompted=yes"
	if !strings.Contains(logBody, want) {
		t.Errorf("log missing %q:\n%s", want, logBody)
	}
	if !strings.Contains(logBody, "system.empty-response reprompted=yes") {
		t.Errorf("log missing system.empty-response forensic line:\n%s", logBody)
	}
	if strings.Contains(logBody, "cause=missing-tag") {
		t.Errorf("missing-tag machinery fired on a persistent-empty turn (cause subsumption broken):\n%s", logBody)
	}
}

// TestRunEmptyResponsePersistentConversational — a conversational turn
// (no binding-required deltas) whose re-prompt also comes back empty: the
// empty body surfaces to the caller as today (no engagement, no
// owner-default), and system.empty-response keeps the blank turn
// forensically visible — the REPL prints nothing for it.
func TestRunEmptyResponsePersistentConversational(t *testing.T) {
	paths, meta := newTestHome(t)

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "", FinishReason: "stop"},
		{Content: "", FinishReason: "stop"},
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))

	body, err := Run(context.Background(), state, "hello", io.Discard)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if body != "" {
		t.Errorf("body: %q; want empty", body)
	}
	if got := len(mock.Calls()); got != 2 {
		t.Fatalf("mock call count: got %d want 2 (one re-prompt, then accept the empty)", got)
	}
	records, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("spine must not advance on a persistent-empty conversational turn; got %d records", len(records))
	}

	logBody := readDayLog(t, paths)
	if !strings.Contains(logBody, "topic.re-prompt cause=empty-response") {
		t.Errorf("log missing empty-response re-prompt line:\n%s", logBody)
	}
	if !strings.Contains(logBody, "system.empty-response reprompted=yes") {
		t.Errorf("log missing system.empty-response forensic line:\n%s", logBody)
	}
	if strings.Contains(logBody, "tag-defaulted") {
		t.Errorf("owner-default fired on a conversational turn:\n%s", logBody)
	}
}

// TestRunEmptyThenFetchThenTagRepromptAllCausesCapped — the full worst
// case of the three-cause bound (≤ 3 re-prompts, ≤ 4 streams): response 1
// is empty (→ empty-response re-prompt), response 2 tags a thread outside
// Layer B (→ §5.5 missing-thread re-prompt), response 3 is tag-less with
// buffered edits (→ missing-tag re-prompt), response 4 drains and binds.
// Four streams total, all three causes spent once, no owner-default.
func TestRunEmptyThenFetchThenTagRepromptAllCausesCapped(t *testing.T) {
	paths, meta := newTestHome(t)
	seedThreadAndSpine(t, paths, meta.ID, "thr_42")

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "", FinishReason: "stop"},                // empty-response re-prompt
		{Content: "*topic: thr_42 [a, b, c, d]*\nSECOND."}, // fetch: thr_42 not in Layer B
		{Content: "No tag. THIRD."},                        // missing-tag re-prompt
		{Content: "*topic: thr_42 [a, b, c, d]*\nFOURTH."}, // binds
	})
	mock.RecordCalls = true
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	// thr_42 NOT in ActiveThreads: response 2's tag triggers the fetch.
	pinClock(t, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))

	body, err := RunWithDeltas(context.Background(), state,
		[]Delta{fsWriteDelta("src/g.go")}, "edit it", io.Discard)
	if err != nil {
		t.Fatalf("RunWithDeltas: %v", err)
	}
	calls := mock.Calls()
	if got := len(calls); got != 4 {
		t.Fatalf("mock call count: got %d want 4 (one re-prompt per cause, three causes)", got)
	}
	if !strings.Contains(body, "FOURTH") {
		t.Errorf("body: %q; want FOURTH", body)
	}
	// Both reminders survive later same-turn recompositions.
	last := calls[3].Request.Messages[0].Content
	if !strings.Contains(last, prompt.EmptyResponseReminder) {
		t.Error("final request dropped EmptyResponseReminder")
	}
	if !strings.Contains(last, prompt.TopicTagReminder) {
		t.Error("final request missing TopicTagReminder")
	}

	logBody := readDayLog(t, paths)
	for _, cause := range []string{"cause=empty-response", "cause=missing-thread", "cause=missing-tag"} {
		if !strings.Contains(logBody, "topic.re-prompt "+cause) {
			t.Errorf("log missing re-prompt line for %s:\n%s", cause, logBody)
		}
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
	if _, ok := tf.Entry("src/g.go"); !ok {
		t.Error("sidecar missing src/g.go on thr_42")
	}
}
