package turn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/prompt"
)

// Probe-local split of RunWithInfo into phase entry points that an external
// agent loop can drive from its own hook events. RunTurnInProcess recomposes
// the phases into the in-process turn and must behave exactly like
// RunWithInfo with out = io.Discard, minus the tool-round branch. Callers
// driving the phases directly serialize every phase call on one mutex.

// Turn is one in-flight turn between BeginTurn and CloseTurn or AbortTurn.
type Turn struct {
	state            *State
	userInput        string
	turnID           string
	preTurn          sessionState
	canonicalStarted bool
	aborted          bool
	journalDur       time.Duration
	barrier          memops.DayBarrierResult

	response string
	usage    model.Usage
	body     string

	closed    bool
	closeInfo TurnInfo
	closeErr  error
}

// BeginTurn runs everything before request composition: budget, live-turn
// reject, day barrier, per-turn resets, snapshot, TurnNumber++, prompt
// journal, staging GC, pre-event deltas and the user.prompt delta. A nil
// Turn means the failure came before the snapshot; otherwise the caller
// rolls back with AbortTurn on error.
func BeginTurn(ctx context.Context, state *State, preEvents []Delta, userInput string) (*Turn, error) {
	if state == nil {
		return nil, errors.New("turn: nil state")
	}
	if state.Client == nil {
		return nil, errors.New("turn: nil model client")
	}
	if state.Budget.Total == 0 {
		state.Budget = memops.DefaultBudget()
	}
	if err := checkUserInput(userInput, state.Budget); err != nil {
		return nil, err
	}
	bres, berr := state.Ops.MaybeDayBarrier(ctx)
	if berr != nil {
		return nil, fmt.Errorf("turn: day barrier: %w", berr)
	}
	for _, id := range bres.ArchivedThreads {
		state.ActiveThreads = removeString(state.ActiveThreads, id)
		state.DormantThreads = removeString(state.DormantThreads, id)
	}
	if state.coalesce == nil {
		state.coalesce = newCoalesceBuffer()
	}
	if state.staging == nil {
		state.staging = newStagingBuffer()
	}
	state.coalesce.reset()
	state.fileEdits = state.fileEdits[:0]
	state.turnOwner = ""
	state.tagReprompted = false
	state.emptyReprompted = false
	state.structuralCreates = 0
	state.structuralRetires = 0
	state.structuralWIPs = 0

	t := &Turn{state: state, userInput: userInput, barrier: bres}
	t.preTurn = snapshotSession(state)
	state.TurnNumber++
	t.turnID = newTurnID(state.TurnNumber)

	journalStart := clock.Profiling()
	jerr := state.Ops.JournalTurn(ctx, t.turnID, memops.TurnContentPrompt, []byte(userInput))
	t.journalDur = clock.Since(journalStart)
	if jerr != nil {
		return t, fmt.Errorf("turn: journal prompt: %w", jerr)
	}

	if n := pruneStaging(state); n > 0 {
		_ = state.Ops.Log(ctx, memops.LogCategoryStaging, "evicted",
			fmt.Sprintf("count=%d turn=%d", n, state.TurnNumber))
	}
	if err := IngestToolResult(ctx, t, preEvents); err != nil {
		return t, err
	}
	if err := onContextDelta(ctx, state, Delta{Source: memops.SourceUserPrompt, Content: userInput}); err != nil {
		return t, err
	}
	return t, nil
}

// ComposeRequest composes the working set into the system prompt (with any
// D6 reminders this turn has fired) and returns it with the bounded history
// tail to replay before the user message.
func ComposeRequest(ctx context.Context, t *Turn) (string, []model.Message, error) {
	state := t.state
	ws, err := state.Ops.ComposeWorkingSet(ctx, memops.WorksetInput{
		ActiveProject:  state.ActiveProject,
		ActiveThreads:  state.ActiveThreads,
		DormantThreads: state.DormantThreads,
		Budget:         state.Budget,
	})
	if err != nil {
		return "", nil, err
	}
	sp := prompt.BuildSystemPrompt(prompt.SystemPromptElements{
		LayerE:  ws.LayerE,
		LayerA1: ws.LayerA1,
		LayerA2: ws.LayerA2,
		LayerB:  ws.LayerB,
		LayerC:  ws.LayerC,
	})
	if state.tagReprompted {
		sp += "\n\n" + prompt.TopicTagReminder
	}
	if state.emptyReprompted {
		sp += "\n\n" + prompt.EmptyResponseReminder
	}
	return sp, boundHistoryTail(state.History, state.Budget), nil
}

// IngestToolResult bounds task-class result deltas and fires each through
// the §3.0 chain.
func IngestToolResult(ctx context.Context, t *Turn, deltas []Delta) error {
	for _, d := range boundTaskResultDeltas(deltas, t.state.Budget) {
		if err := onContextDelta(ctx, t.state, d); err != nil {
			return err
		}
	}
	return nil
}

// IngestModelResponse takes the turn's full response content and usage:
// tag strip, response journal, ceiling and telemetry logs, the empty-response
// forensic line, and the model.response delta.
func IngestModelResponse(ctx context.Context, t *Turn, content string, usage model.Usage) error {
	state := t.state
	t.response = content
	t.usage = usage

	body := content
	if pr, perr := prompt.Parse(content); perr == nil {
		body = pr.Body
	}
	t.body = strings.TrimRight(body, "\n")

	journalStart := clock.Profiling()
	jerr := state.Ops.JournalTurn(ctx, t.turnID, memops.TurnContentResponse, []byte(content))
	t.journalDur += clock.Since(journalStart)
	if jerr != nil {
		return fmt.Errorf("turn: journal response: %w", jerr)
	}

	switch {
	case state.Budget.TokenCeiling <= 0:
	case usage.PromptTokens == 0:
		_ = state.Ops.Log(ctx, memops.LogCategorySystem, "context-ceiling-unenforceable",
			fmt.Sprintf("reason=usage-unavailable ceiling=%d turn=%d",
				state.Budget.TokenCeiling, state.TurnNumber))
	case usage.PromptTokens > state.Budget.TokenCeiling:
		_ = state.Ops.Log(ctx, memops.LogCategorySystem, "context-ceiling-breach",
			fmt.Sprintf("prompt_tokens=%d ceiling=%d turn=%d",
				usage.PromptTokens, state.Budget.TokenCeiling, state.TurnNumber))
	}
	if usage.HasTelemetry() {
		_ = state.Ops.Log(ctx, memops.LogCategoryModel, "inference-telemetry",
			usage.TelemetryLogDetail()+" turn="+strconv.Itoa(state.TurnNumber))
	}
	if strings.TrimSpace(content) == "" {
		reprompted := "no"
		if state.emptyReprompted {
			reprompted = "yes"
		}
		_ = state.Ops.Log(ctx, memops.LogCategorySystem, "empty-response",
			"reprompted="+reprompted+" turn="+strconv.Itoa(state.TurnNumber))
	}
	return onContextDelta(ctx, state, Delta{Source: memops.SourceModelResponse, Content: content})
}

// CloseTurn is the canonical close: engagement, closure scan, CommitTurn,
// MaybeGC, SaveWorkingSet and the history append. It returns the
// tag-stripped body. Idempotent: a second call returns the first result.
func CloseTurn(ctx context.Context, t *Turn) (string, TurnInfo, error) {
	if !t.closed {
		t.closed = true
		t.closeInfo, t.closeErr = closeProbeTurn(ctx, t)
	}
	if t.closeErr != nil {
		return "", TurnInfo{TurnID: t.turnID}, t.closeErr
	}
	return t.body, t.closeInfo, nil
}

func closeProbeTurn(ctx context.Context, t *Turn) (TurnInfo, error) {
	state := t.state
	t.canonicalStarted = true
	if err := closeTurnAndUpdateEngagement(ctx, state, t.userInput, t.response); err != nil {
		return TurnInfo{}, fmt.Errorf("turn: close: %w", err)
	}
	if err := surfaceClosureCandidates(ctx, state); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategoryRetire, "error", memops.SanitizeDetail(err.Error()))
	}
	reason := ""
	if n := state.structuralCreates + state.structuralRetires + state.structuralWIPs; n > 0 {
		reason = fmt.Sprintf("structural: +%d thread, -%d retired, ~%d wip",
			state.structuralCreates, state.structuralRetires, state.structuralWIPs)
	}
	commitStart := clock.Profiling()
	if cerr := state.Ops.CommitTurn(ctx, t.turnID, reason); cerr != nil {
		return TurnInfo{}, fmt.Errorf("turn %s: commit: %w [%w]", t.turnID, cerr, ErrMarkerRetained)
	}
	commitDur := clock.Since(commitStart)

	gcStart := clock.Profiling()
	state.Ops.MaybeGC(ctx)
	gcDur := clock.Since(gcStart)

	if err := state.Ops.SaveWorkingSet(ctx, state.ActiveThreads, state.DormantThreads); err != nil {
		_ = state.Ops.Log(ctx, memops.LogCategorySession, "working-set-save-error", memops.SanitizeDetail(err.Error()))
	}

	state.History = append(state.History,
		model.Message{Role: "user", Content: t.userInput},
		model.Message{Role: "assistant", Content: t.response},
	)
	if histCap := 2 * sessionHistoryCapTurns; len(state.History) > histCap {
		drop := len(state.History) - histCap
		state.History = append(state.History[:0:0], state.History[drop:]...)
	}

	return TurnInfo{
		PromptTokens:    t.usage.PromptTokens,
		TurnID:          t.turnID,
		CommitDuration:  commitDur,
		JournalDuration: t.journalDur,
		GCDuration:      gcDur,
		Barrier:         t.barrier,
		Telemetry:       t.usage.Telemetry,
	}, nil
}

// AbortTurn rolls the session back to its pre-turn snapshot and releases the
// turn's recovery scope. A no-op for a nil Turn, after canonical start, or
// on a second call.
func AbortTurn(ctx context.Context, t *Turn) {
	if t == nil || t.canonicalStarted || t.aborted {
		return
	}
	t.aborted = true
	abortPreCanonical(ctx, t.state, t.turnID, t.preTurn)
}

// RunTurnInProcess is RunWithInfo (out = io.Discard, no tool rounds)
// expressed over the phases: it owns the consult loop and its three
// re-prompt causes.
func RunTurnInProcess(ctx context.Context, state *State, preEvents []Delta, userInput string) (_ string, info TurnInfo, err error) {
	t, err := BeginTurn(ctx, state, preEvents, userInput)
	defer func() {
		if err != nil {
			AbortTurn(ctx, t)
			if t != nil && info.TurnID == "" {
				info.TurnID = t.turnID
			}
		}
	}()
	if err != nil {
		return "", TurnInfo{}, err
	}

	systemPrompt, historyTail, err := ComposeRequest(ctx, t)
	if err != nil {
		return "", TurnInfo{}, fmt.Errorf("turn: compose working set: %w", err)
	}
	recompose := func() error {
		var cerr error
		systemPrompt, historyTail, cerr = ComposeRequest(ctx, t)
		if cerr != nil {
			return fmt.Errorf("turn: recompose working set: %w", cerr)
		}
		return nil
	}

	fetchReprompted := false
	var full model.Response
	for attempt := 0; ; attempt++ {
		messages := make([]model.Message, 0, 2+len(historyTail))
		messages = append(messages, model.Message{Role: "system", Content: systemPrompt})
		messages = append(messages, historyTail...)
		messages = append(messages, model.Message{Role: "user", Content: userInput})

		req := model.DefaultRequest(state.Model, messages)
		req.Tools = state.Tools.Specs()
		if state.Temperature != 0 {
			req.Temperature = state.Temperature
		}
		if state.MaxTokens != 0 {
			req.MaxTokens = state.MaxTokens
		}

		sr, err := state.Client.ConsultStream(ctx, req)
		if err != nil {
			return "", TurnInfo{}, fmt.Errorf("turn: model consult: %w", err)
		}
		pre, err := readPreamble(state, sr)
		if err != nil {
			_ = sr.Close()
			return "", TurnInfo{}, fmt.Errorf("turn: read preamble: %w", err)
		}

		toolRound := len(pre.tools) > 0
		emptyResponse := preambleIsEmpty(pre) && !toolRound
		if emptyResponse && !state.emptyReprompted {
			state.emptyReprompted = true
			_ = sr.Close()
			_ = state.Ops.Log(ctx, memops.LogCategoryTopic, "re-prompt",
				"cause=empty-response attempt="+strconv.Itoa(attempt+1))
			if err := recompose(); err != nil {
				return "", TurnInfo{}, err
			}
			continue
		}

		if !fetchReprompted && pre.tag != nil {
			fetched := 0
			for _, thrID := range missingFromActiveB(pre.tag.Threads, state.ActiveThreads) {
				if fetchThreadForReprompt(ctx, state, thrID) {
					fetched++
				}
			}
			if fetched > 0 {
				fetchReprompted = true
				_ = sr.Close()
				_ = state.Ops.Log(ctx, memops.LogCategoryTopic, "re-prompt",
					"cause=missing-thread fetched="+strconv.Itoa(fetched)+" attempt="+strconv.Itoa(attempt+1))
				if err := recompose(); err != nil {
					return "", TurnInfo{}, err
				}
				continue
			}
		}

		if !emptyResponse && !toolRound && !state.tagReprompted && len(state.fileEdits) > 0 && preambleLacksTag(pre) {
			state.tagReprompted = true
			_ = sr.Close()
			_ = state.Ops.Log(ctx, memops.LogCategoryTopic, "re-prompt",
				"cause=missing-tag attempt="+strconv.Itoa(attempt+1))
			if err := recompose(); err != nil {
				return "", TurnInfo{}, err
			}
			continue
		}

		var streamErr error
		if !pre.ended {
			streamErr = streamThroughFilter(state, sr, io.Discard)
		}
		closeErr := sr.Close()
		if streamErr != nil {
			return "", TurnInfo{}, fmt.Errorf("turn: stream: %w", streamErr)
		}
		if closeErr != nil {
			_ = state.Ops.Log(ctx, memops.LogCategoryModel, "stream-close-warn", closeErr.Error())
		}
		full = sr.Final()
		break
	}

	if err := IngestModelResponse(ctx, t, full.Content, full.Usage); err != nil {
		return "", TurnInfo{}, err
	}
	return CloseTurn(ctx, t)
}
