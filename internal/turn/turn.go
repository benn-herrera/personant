// Package turn implements the spec §3.0 turn loop and the
// onContextDelta hook chain that every content-emitting code path must
// pass through (§3.0.5). One Run = one user turn end-to-end.
package turn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
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

	// ActiveThreads is Layer B membership: thread IDs the working set
	// renders as full thread bodies (per workset.Compose). Index 0 is
	// the most-recently-engaged thread. The list is bounded by
	// Budget.BTopK; overflow demotes to the head of DormantThreads.
	ActiveThreads []string

	// DormantThreads is Layer C membership: thread IDs the working set
	// renders as spine display lines. Index 0 is the most-recently
	// demoted (or independently engaged) thread. The list is capped by
	// dormantThreadsCap; the on-disk byte budget is honored at render
	// time by workset.Compose.
	DormantThreads []string

	// Budget is the byte budget composed into the working set. Defaults
	// to workset.DefaultBudget() at NewState; future directive plumbing
	// (Phase 3+) will recompute this per-turn.
	Budget workset.Budget

	// turn-scoped state
	coalesce *coalesceBuffer

	// nowFn is a clock source used for last_engaged / created timestamps.
	// Tests inject a deterministic clock; production callers leave it nil
	// and Run substitutes time.Now.
	nowFn func() time.Time
}

// dormantThreadsCap is the v0.1 maximum count for State.DormantThreads.
// The byte budget on Layer C is enforced at workset.Compose render
// time; this count cap is a coarser upstream bound to keep the slice
// from growing unboundedly across a long session. Once the directive
// layer can plumb actual byte counts to the LRU update path (Phase
// 3+), the count cap goes away.
const dormantThreadsCap = 20

// NewState constructs a State for a chat session. The coalesce buffer
// is initialized empty; Client must be non-nil (the chat REPL passes
// either an HTTPClient or a MockClient, never nil).
func NewState(paths store.PersonantPaths, project store.ProjectMeta, provider store.Provider, client model.Client) *State {
	return &State{
		Paths:         paths,
		ActiveProject: project,
		Provider:      provider,
		Client:        client,
		Budget:        workset.DefaultBudget(),
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
	if state.Budget.Total == 0 {
		state.Budget = workset.DefaultBudget()
	}
	params, err := workset.Compose(
		workset.State{
			Paths:          state.Paths,
			ActiveProject:  state.ActiveProject,
			ActiveThreads:  state.ActiveThreads,
			DormantThreads: state.DormantThreads,
			Budget:         state.Budget,
		},
		workset.ComposeOptions{
			Logger: func(format string, args ...any) {
				_ = eventlog.Log(state.Paths, "workset", "warning",
					sanitizeDetail(fmt.Sprintf(format, args...)))
			},
		},
	)
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
	if err := closeTurnAndUpdateEngagement(state, userInput, full.Content); err != nil {
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

// historyCapPerThread is the v0.1 default for `history.cap-per-thread`
// (spec §2.6.1). Directive-file lookup arrives in Phase 3+; until then
// the cap is a constant.
const historyCapPerThread = 40

// closeTurnAndUpdateEngagement fires after the model.response delta.
// Per §3.0.4: engagement updates fire once per affected thread with
// the turn's coalesced symbol set as input.
//
// For each thr_<n> in state.coalesce.threads:
//   - if it exists in spine: load thread file, append turn excerpt,
//     merge history_symbols, save thread file, update spine record.
//   - if it is the literal "*new-topic*": create a new thread file
//     and append a new spine record.
//
// File order is: thread file save first, spine update second. A failure
// after the thread file write but before the spine write would leave
// the on-disk file ahead of the spine; the next engagement reload would
// reconcile (it reads the existing file). The opposite order would
// strand a spine entry pointing at a non-existent file. v0.1 accepts
// the former trade-off and logs both errors with context.
//
// New-record creation requires the topic tag's anchors. v0.1 takes
// them from state.coalesce.symbols (the union accumulated across the
// turn). Anchor count must be 4–8 (§2.2 hard range); when out of
// range, a warning is logged and the first 4 (padded with "anchor-N"
// placeholders if there are fewer) are used. v0.1 deliberately does
// not fail the turn over malformed tag output — that judgment is
// recorded in the spec changelog.
//
// Phase 2.e-A simplification: the model may have referenced thr_<n>
// in its topic tag that wasn't in Layer B at prompt-construction
// time. The §5.5 system-injected re-prompt mechanism (which would
// stop the stream, fetch the thread, and re-issue the request with
// augmented context) is deferred to 2.e-B. For now, the referenced
// thread enters ActiveThreads here, so the next turn's prompt
// includes it; the current turn's response was generated against
// whatever was in B at the start. Document this in commits and the
// spec changelog.
func closeTurnAndUpdateEngagement(state *State, userInput, responseBody string) error {
	if len(state.coalesce.threads) == 0 {
		return nil
	}

	now := state.now().Format(time.RFC3339)
	turnSymbols := state.coalesce.coalescedList()
	turnAnchors := turnAnchorList(responseBody, state.coalesce.symbolList())

	// Engaged thread IDs in this turn — includes resolved IDs for the
	// *new-topic* sentinel. Used to update the Layer B/C LRU below.
	engaged := make([]string, 0, len(state.coalesce.threads))

	for _, threadID := range state.coalesce.threadList() {
		if threadID == "*new-topic*" {
			newID, err := createNewThread(state, userInput, responseBody, now, turnSymbols, turnAnchors)
			if err != nil {
				return err
			}
			engaged = append(engaged, newID)
			continue
		}
		if err := updateExistingThread(state, threadID, userInput, responseBody, now, turnSymbols, turnAnchors); err != nil {
			return err
		}
		engaged = append(engaged, threadID)
	}

	updateLayerLRU(state, engaged)
	return nil
}

// updateLayerLRU applies the §3.1 Layer B/C eviction policy after a
// turn's engagements are committed.
//
// For each engaged thread (in coalesce-order — map iteration is
// non-deterministic, but every thread in the slice was definitely
// touched this turn so any order is correct):
//
//   - If the thread is already in ActiveThreads, move it to the
//     front (most-recent).
//   - Otherwise, prepend it. If ActiveThreads then exceeds Budget.BTopK,
//     pop the tail and prepend it to DormantThreads.
//   - If a thread newly entering ActiveThreads is currently in
//     DormantThreads, remove it from there before inserting at front.
//
// Threads not engaged this turn are left in place; decay is by
// overflow at the next engagement (per §3.1's "no per-thread decay
// counter; eviction is bounded by budget pressure"). DormantThreads
// is capped by count to keep the slice bounded across a long session.
func updateLayerLRU(state *State, engaged []string) {
	bTopK := state.Budget.BTopK
	if bTopK <= 0 {
		bTopK = workset.DefaultBTopK
	}
	for _, id := range engaged {
		// Drop from current positions in either layer.
		state.ActiveThreads = removeString(state.ActiveThreads, id)
		state.DormantThreads = removeString(state.DormantThreads, id)
		// Insert at the front of ActiveThreads.
		state.ActiveThreads = append([]string{id}, state.ActiveThreads...)
		// Overflow: tail of ActiveThreads demotes to head of
		// DormantThreads.
		for len(state.ActiveThreads) > bTopK {
			demoted := state.ActiveThreads[len(state.ActiveThreads)-1]
			state.ActiveThreads = state.ActiveThreads[:len(state.ActiveThreads)-1]
			state.DormantThreads = append([]string{demoted}, state.DormantThreads...)
		}
	}
	// Cap DormantThreads by count.
	if len(state.DormantThreads) > dormantThreadsCap {
		state.DormantThreads = state.DormantThreads[:dormantThreadsCap]
	}
}

// removeString returns ss with the first occurrence of v removed. ss
// is unmodified; the result aliases its tail when v is found at the
// head, otherwise allocates a fresh slice. Order-preserving.
func removeString(ss []string, v string) []string {
	for i, s := range ss {
		if s == v {
			out := make([]string, 0, len(ss)-1)
			out = append(out, ss[:i]...)
			out = append(out, ss[i+1:]...)
			return out
		}
	}
	return ss
}

// turnAnchorList returns the anchor list to print in the per-turn
// excerpt header. Prefers the model's own topic-tag anchors so the
// excerpt header matches the wire format exactly; falls back to the
// per-turn coalesced set when the model omitted a tag.
func turnAnchorList(responseBody string, fallback []string) []string {
	if pr, err := prompt.Parse(responseBody); err == nil {
		return append([]string(nil), pr.Tag.Anchors...)
	}
	return append([]string(nil), fallback...)
}

func updateExistingThread(state *State, threadID, userInput, responseBody, now string, turnSymbols []coalescedSymbol, turnAnchors []string) error {
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

	// Load (or synthesize) the thread file. A spine record without a
	// matching thread file is a drift state — synthesize a minimal
	// frontmatter from the spine record so the engagement still produces
	// a well-formed file going forward.
	thr, err := store.LoadThread(state.Paths, threadID)
	if err != nil && !errors.Is(err, store.ErrThreadFileNotFound) {
		return fmt.Errorf("load thread %s: %w", threadID, err)
	}
	if errors.Is(err, store.ErrThreadFileNotFound) {
		_ = eventlog.Log(state.Paths, "thread", "file-missing-resynth",
			"thr="+threadID+" project="+rec.Project)
		thr = store.Thread{Frontmatter: frontmatterFromSpine(rec)}
	}

	// Bookkeeping update on the in-memory record. The new turn_count is
	// the value used for the per-turn excerpt header and as
	// first_seen_turn for any newly introduced history symbols.
	newTurnCount := rec.TurnCount + 1
	thr.Frontmatter.LastEngaged = now
	thr.Frontmatter.TurnCount = newTurnCount
	// Mirror the canonical spine fields so the frontmatter stays in
	// sync. The body of work reads the frontmatter; the spine is the
	// outer index.
	thr.Frontmatter.ID = rec.ID
	thr.Frontmatter.Project = rec.Project
	thr.Frontmatter.Anchors = append([]string(nil), rec.Anchors...)
	thr.Frontmatter.Summary = rec.Summary
	thr.Frontmatter.State = rec.State
	if thr.Frontmatter.Created == "" {
		thr.Frontmatter.Created = rec.Created
	}
	if thr.Frontmatter.StateChanged == "" {
		thr.Frontmatter.StateChanged = rec.StateChanged
	}
	thr.Frontmatter.RecallFires = rec.RecallFires

	thr.Frontmatter.HistorySymbols = mergeHistorySymbols(thr.Frontmatter.HistorySymbols, turnSymbols, newTurnCount)
	thr.Body = appendTurnExcerpt(thr.Body, newTurnCount, now, turnAnchors, userInput, responseBody)

	if err := store.SaveThread(state.Paths, thr); err != nil {
		return fmt.Errorf("save thread %s: %w", threadID, err)
	}

	rec.LastEngaged = now
	rec.TurnCount = newTurnCount
	if err := store.UpdateSpineRecord(state.Paths, rec); err != nil {
		return err
	}
	return eventlog.Log(state.Paths, "thread", "engaged",
		threadID+" turn_count="+itoa(rec.TurnCount))
}

func createNewThread(state *State, userInput, responseBody, now string, turnSymbols []coalescedSymbol, turnAnchors []string) (string, error) {
	// NextThreadID needs the full spine, not just this project's, so a
	// new id never collides with a thread in a sibling project.
	allRecords, err := store.ReadSpine(state.Paths.Spine)
	if err != nil {
		return "", err
	}
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

	thr := store.Thread{
		Frontmatter: ThreadFrontmatter{
			ID:             newID,
			Project:        rec.Project,
			Anchors:        append([]string(nil), anchors...),
			Summary:        summary,
			State:          store.ThreadWIP,
			Created:        now,
			LastEngaged:    now,
			StateChanged:   now,
			TurnCount:      1,
			RecallFires:    0,
			HistorySymbols: mergeHistorySymbols(nil, turnSymbols, 1),
		},
		Body: newThreadBody(anchors, 1, now, turnAnchors, userInput, responseBody),
	}
	// rec.HistorySymbols is not part of SpineRecord; the field name above
	// is only for the thread file. The Frontmatter struct is built
	// in-line from store.ThreadFrontmatter via the type alias below.
	if err := store.SaveThread(state.Paths, thr); err != nil {
		return "", fmt.Errorf("save thread %s: %w", newID, err)
	}
	if err := store.AppendSpineRecord(state.Paths, rec); err != nil {
		return "", err
	}
	if err := eventlog.Log(state.Paths, "thread", "created",
		newID+" anchors="+itoa(len(anchors))+" project="+state.ActiveProject.ID); err != nil {
		return "", err
	}
	return newID, nil
}

// ThreadFrontmatter is a local alias to avoid a long-form type literal in
// the createNewThread frontmatter construction. Kept at package scope so
// the literal in the function body reads naturally.
type ThreadFrontmatter = store.ThreadFrontmatter

// frontmatterFromSpine builds a minimal-but-valid ThreadFrontmatter from
// a SpineRecord. Used when a thread's on-disk file is missing while its
// spine entry persists — the engagement update synthesizes a fresh file
// rather than failing the turn.
func frontmatterFromSpine(rec store.SpineRecord) ThreadFrontmatter {
	return ThreadFrontmatter{
		ID:           rec.ID,
		Project:      rec.Project,
		Anchors:      append([]string(nil), rec.Anchors...),
		Summary:      rec.Summary,
		State:        rec.State,
		Created:      rec.Created,
		LastEngaged:  rec.LastEngaged,
		StateChanged: rec.StateChanged,
		TurnCount:    rec.TurnCount,
		RecallFires:  rec.RecallFires,
	}
}

// mergeHistorySymbols folds the per-turn coalesced symbol set into an
// existing history_symbols list and enforces the cap (spec §2.3 +
// §2.6.1, default 40).
//
// For each per-turn symbol:
//   - if its normalized form already appears: increment count, upgrade
//     source per §2.7.3 dominance.
//   - else: append a new entry with first_seen_turn = currentTurn.
//
// Eviction policy when the merged list exceeds the cap: lowest count
// first; ties broken by lowest first_seen_turn (oldest among lowest).
// Spec §2.3 OPEN flags the precise weight formula as deferred to v0.1.1;
// v0.1 uses count alone. Order of return is stable: existing entries
// retain insertion order, new entries append in the input order.
func mergeHistorySymbols(existing []store.HistorySymbol, turnSymbols []coalescedSymbol, currentTurn int) []store.HistorySymbol {
	// Index existing by normalized for O(1) lookup.
	idx := make(map[string]int, len(existing))
	out := make([]store.HistorySymbol, len(existing))
	copy(out, existing)
	for i, h := range out {
		idx[h.Normalized] = i
	}

	// Sort turnSymbols by normalized for deterministic append order.
	turnSorted := make([]coalescedSymbol, len(turnSymbols))
	copy(turnSorted, turnSymbols)
	sort.Slice(turnSorted, func(i, j int) bool {
		return turnSorted[i].Normalized < turnSorted[j].Normalized
	})

	for _, sym := range turnSorted {
		if i, ok := idx[sym.Normalized]; ok {
			out[i].Count++
			out[i].Source = upgradeSource(out[i].Source, sym.Source)
			continue
		}
		raw := sym.Raw
		if raw == "" {
			raw = sym.Normalized
		}
		out = append(out, store.HistorySymbol{
			Raw:           raw,
			Normalized:    sym.Normalized,
			FirstSeenTurn: currentTurn,
			Count:         1,
			Source:        sym.Source,
		})
		idx[sym.Normalized] = len(out) - 1
	}

	if len(out) <= historyCapPerThread {
		return out
	}
	return evictLowestWeight(out, historyCapPerThread)
}

// upgradeSource is the persistent-history analogue of dominantSource —
// same precedence rule, applied on cumulative history rather than
// per-turn coalescing.
func upgradeSource(existing, incoming store.SymbolSource) store.SymbolSource {
	if rank(existing) >= rank(incoming) {
		return existing
	}
	return incoming
}

// evictLowestWeight returns out with the lowest-cumulative-weight entries
// removed until len == cap. Weight = count alone in v0.1; ties broken by
// lowest first_seen_turn (evict oldest among lowest-count). Stable
// ordering of the survivors is preserved.
func evictLowestWeight(out []store.HistorySymbol, cap int) []store.HistorySymbol {
	if len(out) <= cap {
		return out
	}
	type indexed struct {
		idx int
		ref *store.HistorySymbol
	}
	scored := make([]indexed, len(out))
	for i := range out {
		scored[i] = indexed{idx: i, ref: &out[i]}
	}
	// Sort: highest count first; ties broken by highest first_seen_turn
	// (newest survives when counts tie).
	sort.SliceStable(scored, func(i, j int) bool {
		a, b := scored[i].ref, scored[j].ref
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.FirstSeenTurn > b.FirstSeenTurn
	})
	keepIdx := make(map[int]struct{}, cap)
	for i := 0; i < cap; i++ {
		keepIdx[scored[i].idx] = struct{}{}
	}
	survivors := make([]store.HistorySymbol, 0, cap)
	for i := range out {
		if _, ok := keepIdx[i]; ok {
			survivors = append(survivors, out[i])
		}
	}
	return survivors
}

// appendTurnExcerpt appends a single per-turn excerpt block to body and
// returns the new body. The block format (spec §2.3 body content
// guidelines):
//
//	## Turn <N> · <RFC3339> · [a, b, c]
//
//	**user:** <userInput>
//
//	**agent:** <responseBody with topic tag stripped>
//
// Exactly one trailing newline is preserved on the returned body so
// repeated saves produce byte-identical files when nothing has changed.
func appendTurnExcerpt(body string, turnN int, when string, anchors []string, userInput, responseBody string) string {
	excerpt := renderTurnExcerpt(turnN, when, anchors, userInput, responseBody)
	if body == "" {
		return excerpt
	}
	body = strings.TrimRight(body, "\n") + "\n"
	return body + "\n" + excerpt
}

// newThreadBody renders the initial body for a freshly-created thread:
// a top-level title taken from the first anchor (or fallback) followed
// by the first turn excerpt.
func newThreadBody(anchors []string, turnN int, when string, headerAnchors []string, userInput, responseBody string) string {
	title := "new thread"
	if len(anchors) > 0 && anchors[0] != "" {
		title = anchors[0]
	}
	excerpt := renderTurnExcerpt(turnN, when, headerAnchors, userInput, responseBody)
	return "# " + title + "\n\n" + excerpt
}

func renderTurnExcerpt(turnN int, when string, anchors []string, userInput, responseBody string) string {
	stripped := responseBody
	if pr, err := prompt.Parse(responseBody); err == nil {
		stripped = pr.Body
	}
	stripped = strings.TrimRight(stripped, "\n")
	user := strings.TrimRight(userInput, "\n")

	var b strings.Builder
	b.Grow(len(userInput) + len(responseBody) + 64)
	b.WriteString("## Turn ")
	b.WriteString(itoa(turnN))
	b.WriteString(" · ")
	b.WriteString(when)
	b.WriteString(" · [")
	b.WriteString(strings.Join(anchors, ", "))
	b.WriteString("]\n\n")
	b.WriteString("**user:** ")
	b.WriteString(user)
	b.WriteString("\n\n")
	b.WriteString("**agent:** ")
	b.WriteString(stripped)
	b.WriteString("\n")
	return b.String()
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
