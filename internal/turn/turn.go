// Package turn implements the spec §3.0 turn loop and the
// onContextDelta hook chain that every content-emitting code path must
// pass through (§3.0.5). One Run = one user turn end-to-end.
package turn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"personant/internal/eventlog"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/store"
	"personant/internal/workset"
)

// State is the per-session mutable runtime state passed to Run. Most
// fields are stable across a session; the coalesce buffer is reset at
// the start of every Run.
type State struct {
	Paths         store.PersonantPaths
	ActiveProject store.ProjectMeta
	Provider      store.Provider
	Client        model.Client

	// Model overrides the provider's DefaultModel when non-empty.
	Model string

	// Temperature / MaxTokens — when 0, the provider's default is used.
	Temperature float64
	MaxTokens   int

	// History accumulates assistant/user messages across the session so the
	// model sees prior turns. The system prompt is recomputed every turn
	// from working-set state (the spine may have changed); only the
	// user/assistant exchange is replayed from History.
	History []model.Message

	// turn-scoped state
	coalesce *coalesceBuffer

	// nowFn is a clock source used for last_engaged / created timestamps.
	// Tests inject a deterministic clock; production callers leave it nil
	// and Run substitutes time.Now.
	nowFn func() time.Time
}

// NewState constructs a State for a chat session. The coalesce buffer
// is initialized empty; Client must be non-nil (the chat REPL passes
// either an HTTPClient or a MockClient, never nil).
func NewState(paths store.PersonantPaths, project store.ProjectMeta, provider store.Provider, client model.Client) *State {
	return &State{
		Paths:         paths,
		ActiveProject: project,
		Provider:      provider,
		Client:        client,
		coalesce:      newCoalesceBuffer(),
	}
}

// SetClock pins the time source for tests. Production callers leave it
// alone and time.Now is used.
func (s *State) SetClock(fn func() time.Time) { s.nowFn = fn }

func (s *State) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// Run drives one complete user turn end-to-end (spec §3.0):
//
//  1. Fire user.prompt context-modify event.
//  2. Compose working-set + build system prompt.
//  3. Stream the model response via state.Client.ConsultStream, writing
//     chunks through a topic-tag stream filter to out as they arrive.
//  4. Fire model.response context-modify event (parses topic tag,
//     accumulates symbols, defers engagement) after the stream completes.
//     Per §3.0.5 the model.response delta fires once with the full body,
//     not per chunk.
//  5. Close-out the turn: fire engagement updates with the coalesced
//     symbol set (§3.0.4).
//  6. Return the response body with the topic tag stripped (matches what
//     the user saw on out).
//
// Errors at any step are wrapped and returned. The coalesce buffer is
// reset at the start of each Run so per-turn accumulation is fresh.
//
// out receives the streamed response body with the §5.1 topic tag
// suppressed; pass io.Discard to keep the streaming behavior without
// presenting tokens (e.g. tests that only assert on side-effects).
func Run(ctx context.Context, state *State, userInput string, out io.Writer) (string, error) {
	if state == nil {
		return "", errors.New("turn: nil state")
	}
	if state.Client == nil {
		return "", errors.New("turn: nil model client")
	}
	if out == nil {
		out = io.Discard
	}
	if state.coalesce == nil {
		state.coalesce = newCoalesceBuffer()
	}
	state.coalesce.reset()

	// Step 1: user.prompt delta.
	if err := onContextDelta(state, Delta{Source: "user.prompt", Content: userInput}); err != nil {
		return "", err
	}

	// Step 2: compose working-set and assemble the system prompt.
	params, err := workset.Compose(workset.State{
		Paths:         state.Paths,
		ActiveProject: state.ActiveProject,
	})
	if err != nil {
		return "", fmt.Errorf("turn: compose working set: %w", err)
	}
	systemPrompt := prompt.BuildSystemPrompt(params)

	// Step 3: LLM round-trip (streaming).
	chosenModel := state.Model
	if chosenModel == "" {
		chosenModel = state.Provider.DefaultModel
	}
	messages := make([]model.Message, 0, 2+len(state.History))
	messages = append(messages, model.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, state.History...)
	messages = append(messages, model.Message{Role: "user", Content: userInput})

	req := model.DefaultRequest(chosenModel, messages)
	// State-level overrides — when set, they win over the defaults.
	if state.Temperature != 0 {
		req.Temperature = state.Temperature
	}
	if state.MaxTokens != 0 {
		req.MaxTokens = state.MaxTokens
	}

	sr, err := state.Client.ConsultStream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("turn: model consult: %w", err)
	}
	// Stream filter strips the §5.1 topic tag from the user-visible body.
	filter := prompt.NewStreamFilter(out)
	streamErr := streamThroughFilter(sr, filter)
	// Always flush the filter (even on error) so any buffered non-tag
	// content reaches the user before we surface the failure.
	flushErr := filter.Close()
	closeErr := sr.Close()

	if streamErr != nil {
		return "", fmt.Errorf("turn: stream: %w", streamErr)
	}
	if flushErr != nil {
		return "", fmt.Errorf("turn: flush stream filter: %w", flushErr)
	}
	if closeErr != nil {
		// Close-after-EOF errors are usually benign (e.g. the body was
		// already drained); surface them but don't lose the response.
		_ = eventlog.Log(state.Paths, "model", "stream-close-warn", closeErr.Error())
	}

	full := sr.Final()

	// Step 4: model.response delta with the full accumulated body —
	// fired once, not per chunk (spec §3.0.5).
	if err := onContextDelta(state, Delta{Source: "model.response", Content: full.Content}); err != nil {
		return "", err
	}

	// Step 5: turn close — fire deferred engagement updates with the
	// coalesced symbol set.
	if err := closeTurnAndUpdateEngagement(state, full.Content); err != nil {
		return "", fmt.Errorf("turn: close: %w", err)
	}

	// Step 6: derive the topic-tag-stripped body for the return value.
	// (out has already received the same content in chunks.)
	body := full.Content
	if pr, perr := prompt.Parse(full.Content); perr == nil {
		body = pr.Body
	}
	body = strings.TrimRight(body, "\n")

	// Append to History so the next turn sees the exchange.
	state.History = append(state.History,
		model.Message{Role: "user", Content: userInput},
		model.Message{Role: "assistant", Content: full.Content},
	)

	return body, nil
}

// streamThroughFilter pumps every chunk's Content through filter until
// the StreamReader signals EOF. Errors from filter.Write are surfaced
// immediately — a sink that fails to accept bytes is not something the
// runtime can recover from per-chunk.
func streamThroughFilter(sr model.StreamReader, filter io.Writer) error {
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if chunk.Content == "" {
			continue
		}
		if _, werr := io.WriteString(filter, chunk.Content); werr != nil {
			return werr
		}
	}
}

// closeTurnAndUpdateEngagement fires after the model.response delta.
// Per §3.0.4: engagement updates fire once per affected thread with
// the turn's coalesced symbol set as input.
//
// For each thr_<n> in state.coalesce.threads:
//   - if it exists in spine: update last_engaged and turn_count++.
//   - if it is the literal "*new-topic*": create a new spine record.
//
// New-record creation requires the topic tag's anchors. v0.1 takes
// them from state.coalesce.symbols (the union accumulated across the
// turn). Anchor count must be 4–8 (§2.2 hard range); when out of
// range, a warning is logged and the first 4 (padded with "anchor-N"
// placeholders if there are fewer) are used. v0.1 deliberately does
// not fail the turn over malformed tag output — that judgment is
// recorded in the spec changelog.
func closeTurnAndUpdateEngagement(state *State, responseBody string) error {
	if len(state.coalesce.threads) == 0 {
		return nil
	}

	now := state.now().Format(time.RFC3339)

	for _, threadID := range state.coalesce.threadList() {
		if threadID == "*new-topic*" {
			if err := createNewThread(state, responseBody, now); err != nil {
				return err
			}
			continue
		}
		if err := updateExistingThread(state, threadID, now); err != nil {
			return err
		}
	}
	return nil
}

func updateExistingThread(state *State, threadID, now string) error {
	rec, found, err := store.FindSpineRecord(state.Paths, threadID)
	if err != nil {
		return err
	}
	if !found {
		// The model named a thread that does not exist. v0.1 logs and
		// continues; future phases may surface this as a recall miss or a
		// hallucination signal.
		return eventlog.Log(state.Paths, "thread", "engaged-miss",
			"thr="+threadID+" reason=not-in-spine")
	}
	if rec.Project != "" && rec.Project != state.ActiveProject.ID {
		// Cross-project engagement is reserved for Phase 5; v0.1 warns and
		// declines to mutate a record that belongs to another project.
		return eventlog.Log(state.Paths, "thread", "engaged-cross-project",
			"thr="+threadID+" project="+rec.Project+" active="+state.ActiveProject.ID)
	}
	rec.LastEngaged = now
	rec.TurnCount++
	if err := store.UpdateSpineRecord(state.Paths, rec); err != nil {
		return err
	}
	return eventlog.Log(state.Paths, "thread", "engaged",
		threadID+" turn_count="+itoa(rec.TurnCount))
}

func createNewThread(state *State, responseBody, now string) error {
	records, err := store.SpineRecordsByProject(state.Paths, state.ActiveProject.ID)
	if err != nil {
		return err
	}
	// NextThreadID needs the full spine, not just this project's, so a
	// new id never collides with a thread in a sibling project.
	allRecords, err := store.ReadSpine(state.Paths.Spine)
	if err != nil {
		return err
	}
	_ = records // (used implicitly via project's spine; keep slice for symmetry)
	newID := store.NextThreadID(allRecords)

	anchors := state.coalesce.symbolList()
	if len(anchors) < 4 || len(anchors) > 8 {
		_ = eventlog.Log(state.Paths, "thread", "anchor-cardinality",
			"new-thread anchors="+itoa(len(anchors))+" using-first-4-with-padding")
		anchors = padOrTruncateAnchors(anchors, 4)
	}

	summary := summarizeForNewThread(responseBody)

	rec := store.SpineRecord{
		ID:           newID,
		Project:      state.ActiveProject.ID,
		Anchors:      anchors,
		Summary:      summary,
		State:        store.ThreadWIP,
		Created:      now,
		LastEngaged:  now,
		StateChanged: now,
		TurnCount:    1,
	}
	if err := store.AppendSpineRecord(state.Paths, rec); err != nil {
		return err
	}
	return eventlog.Log(state.Paths, "thread", "created",
		newID+" anchors="+itoa(len(anchors))+" project="+state.ActiveProject.ID)
}

// padOrTruncateAnchors enforces the §2.2 hard range [4, 8]. If the
// input has fewer than min entries, append "anchor-<n>" placeholders.
// If it has more than 8, take the first 8.
func padOrTruncateAnchors(in []string, min int) []string {
	out := append([]string(nil), in...)
	if len(out) > 8 {
		out = out[:8]
	}
	for i := len(out); i < min; i++ {
		out = append(out, fmt.Sprintf("anchor-%d", i+1))
	}
	return out
}

// summarizeForNewThread takes the response body's stripped form (tag
// already removed by the caller-provided string, or the raw body) and
// returns a short summary. v0.1 keeps it crude: first 120 chars,
// collapsed whitespace, ellipsized if truncated. Phase 4 retirement
// will replace this with a curator-drafted summary.
func summarizeForNewThread(body string) string {
	const maxLen = 120
	stripped := body
	if pr, err := prompt.Parse(body); err == nil {
		stripped = pr.Body
	}
	stripped = strings.TrimSpace(stripped)
	stripped = strings.Join(strings.Fields(stripped), " ")
	if stripped == "" {
		return "(new topic)"
	}
	if len(stripped) <= maxLen {
		return stripped
	}
	return stripped[:maxLen-3] + "..."
}
