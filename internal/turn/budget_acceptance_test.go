package turn

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/prompt"
	"personant/internal/store"
)

// captureClient wraps a model.Client and records the messages of the last
// request it served. It lets a test inspect the BYTE size of the
// fully-assembled request (system prompt + replayed history + user input)
// — the mock-independent A4 pre-flight gate (the mock's PromptTokens is a
// canned 8, so the token assertion is meaningless against it).
type captureClient struct {
	inner model.Client
	mu    sync.Mutex
	last  []model.Message
}

func (c *captureClient) record(req model.Request) {
	c.mu.Lock()
	c.last = append([]model.Message(nil), req.Messages...)
	c.mu.Unlock()
}

func (c *captureClient) Consult(ctx context.Context, req model.Request) (model.Response, error) {
	c.record(req)
	return c.inner.Consult(ctx, req)
}

func (c *captureClient) ConsultStream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	c.record(req)
	return c.inner.ConsultStream(ctx, req)
}

func (c *captureClient) ListModels(ctx context.Context) ([]model.ModelInfo, error) {
	return c.inner.ListModels(ctx)
}

func (c *captureClient) assembledBytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, m := range c.last {
		n += len(m.Content)
	}
	return n
}

// seedThread appends a spine record so a scripted turn can engage it.
func seedThread(t *testing.T, paths store.PersonantPaths, projectID, id string) {
	t.Helper()
	rec := memops.SpineRecord{
		ID:           id,
		Project:      projectID,
		Anchors:      []string{"alpha", "beta", "gamma", "delta"},
		Summary:      "fixture-" + id,
		State:        memops.ThreadActive,
		Created:      "2026-04-01T00:00:00Z",
		LastEngaged:  "2026-04-01T00:00:00Z",
		StateChanged: "2026-04-01T00:00:00Z",
		TurnCount:    1,
	}
	if err := store.AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("append spine %s: %v", id, err)
	}
}

// TestRejectOversizeUserInput — the §3.4/Q3 reject path end-to-end: a
// userInput larger than its live-turn share is rejected with a
// user-facing message BEFORE any model round-trip, and leaves no
// half-applied state (TurnNumber does not advance, no request issued).
func TestRejectOversizeUserInput(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	mock := model.NewScriptedMock(nil, nil)
	cap := &captureClient{inner: mock}
	// Small ceiling → small live-turn reserve → small userInput share.
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, cap,
		WithTokenCeiling(1000))

	share, _, _ := liveTurnShares(state.Budget)
	if share <= 0 {
		t.Fatalf("expected a positive userInput share at ceiling 1000")
	}
	oversize := strings.Repeat("z", share+1)

	beforeTurn := state.TurnNumber
	_, _, err := RunWithInfo(context.Background(), state, nil, oversize, io.Discard)
	if err == nil {
		t.Fatalf("oversize input must be rejected")
	}
	if !IsInputExceedsBudget(err) {
		t.Fatalf("expected reject classification, got %v", err)
	}
	if state.TurnNumber != beforeTurn {
		t.Errorf("rejected turn must not advance TurnNumber: before=%d after=%d", beforeTurn, state.TurnNumber)
	}
	if cap.assembledBytes() != 0 {
		t.Errorf("rejected turn must not issue a request; assembled=%d", cap.assembledBytes())
	}
}

// TestA4PreflightAssembledBytesWithinBudget — A4: the byte-driven
// pre-flight bounds the WHOLE request, mock-independently. Even with an
// oversized memory state AND a large (but within-reserve) user input, the
// assembled request bytes stay within the derived byte budget
// (Budget.Total) plus the STATIC prompt scaffolding. This is the
// structural guarantee the mock sim relies on (no token assertion against
// the mock's canned 8).
//
// Scaffolding term (2026-07-16): the #127 partition allocates 100% of
// Budget.Total to the memory layers + the live-turn reserve — the static
// prompt scaffolding (orientation preamble, TopicTagDirective, section
// headers/joins; ~1.7 KB) was never given a share. The old bare
// `≤ Budget.Total` assertion held only on integer-truncation and
// layer-under-fill slack at this tiny 2000-token ceiling, and any
// legitimate directive edit broke it (the evidence-directed D4 ambiguity
// clause did). The bound below states the actual structural contract:
// every budget-governed component is within its share; the constant
// scaffolding rides on top, measured (not guessed) from the same builder
// the runtime uses. The authoritative whole-request gate remains the
// POST-flight `usage.prompt_tokens ≤ TokenCeiling` (I1) — bytes are the
// conservative 2.5 B/token pre-flight proxy, so the scaffolding term does
// not endanger the token ceiling. Charging the scaffolding a real share
// inside BudgetForCeiling is a #127 partition-design follow-up (a fixed
// deduction would zero the tiny ceilings tests use).
func TestA4PreflightAssembledBytesWithinBudget(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	// Several seeded threads so the working set has real content to compose
	// into the system prompt (Layers A1/C exercise the memory byte budget).
	for i := 1; i <= 8; i++ {
		seedThread(t, paths, meta.ID, itoaThreadID(i))
	}

	mock := model.NewScriptedMock(nil, nil)
	cap := &captureClient{inner: mock}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, cap,
		WithTokenCeiling(2000))

	// The static scaffolding, measured exactly: build the system prompt
	// with 1-byte sentinel layers (all five headers + joins present — the
	// worst case; empty layers omit their headers) and subtract the five
	// sentinel bytes. No re-prompt fires in this test, so the D6 reminders
	// (which append only on a re-prompted turn) are correctly absent.
	sentinel := prompt.SystemPromptElements{
		LayerE: "x", LayerA1: "x", LayerA2: "x", LayerB: "x", LayerC: "x",
	}
	scaffolding := len(prompt.BuildSystemPrompt(sentinel)) - 5

	// Fixed sanity ceiling beside the structural bound: the structural
	// `≤ Total + scaffolding` assertion below self-inflates — a 50KB directive
	// regression widens the bound with it and would still pass. This absolute
	// cap catches runaway directive growth (bump deliberately when the
	// directive legitimately grows).
	const maxScaffoldingBytes = 4096
	if scaffolding > maxScaffoldingBytes {
		t.Fatalf("static prompt scaffolding %d bytes exceeds sanity ceiling %d — "+
			"directive/header growth regression (bump maxScaffoldingBytes if intended)",
			scaffolding, maxScaffoldingBytes)
	}

	// Drive several turns engaging thr_1 so Layer B/C populate and History
	// accumulates, then assert the assembled request stays within budget.
	share, _, _ := liveTurnShares(state.Budget)
	userInput := strings.Repeat("q", share) // exactly at the reject boundary (allowed)
	for i := 0; i < 6; i++ {
		mock.SetResponse(model.Response{
			Content: "*topic: thr_1 [alpha, beta, gamma, delta]*\nresponse body " + itoaThreadID(i),
		})
		if _, _, err := RunWithInfo(context.Background(), state, nil, userInput, io.Discard); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if got, bound := cap.assembledBytes(), state.Budget.Total+scaffolding; got > bound {
			t.Fatalf("turn %d assembled %d bytes exceeds derived byte budget %d (Total %d + static scaffolding %d)",
				i, got, bound, state.Budget.Total, scaffolding)
		}
	}
}

// TestA3LiveTurnAndMemoryNeverSilentlyTraded — A3 / I2.
//
// (a) Large memory + a normal live turn: the live turn (user input) is
//     fully present in the assembled request.
// (b) Normal memory + an oversized (but in-reserve) live turn: the memory
//     layers are still composed (system prompt non-empty), not zeroed to
//     fit the live turn.
func TestA3LiveTurnAndMemoryNeverSilentlyTraded(t *testing.T) {
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	t.Run("live-turn-present-with-large-memory", func(t *testing.T) {
		paths, meta := newTestHome(t)
		for i := 1; i <= 10; i++ {
			seedThread(t, paths, meta.ID, itoaThreadID(i))
		}
		mock := model.NewScriptedMock(nil, nil)
		cap := &captureClient{inner: mock}
		state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, cap)
		mock.SetResponse(model.Response{Content: "*topic: thr_1 [alpha, beta, gamma, delta]*\nok"})

		const marker = "USER-INTENT-MARKER-PRESENT"
		if _, _, err := RunWithInfo(context.Background(), state, nil, marker, io.Discard); err != nil {
			t.Fatalf("run: %v", err)
		}
		// The live turn (user message) must be fully present.
		cap.mu.Lock()
		last := cap.last
		cap.mu.Unlock()
		found := false
		for _, m := range last {
			if m.Role == "user" && strings.Contains(m.Content, marker) {
				found = true
			}
		}
		if !found {
			t.Errorf("live turn not present in assembled request despite large memory")
		}
	})

	t.Run("memory-composed-with-oversized-live-turn", func(t *testing.T) {
		paths, meta := newTestHome(t)
		for i := 1; i <= 6; i++ {
			seedThread(t, paths, meta.ID, itoaThreadID(i))
		}
		mock := model.NewScriptedMock(nil, nil)
		cap := &captureClient{inner: mock}
		state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, cap,
			WithTokenCeiling(2000))
		mock.SetResponse(model.Response{Content: "*topic: thr_1 [alpha, beta, gamma, delta]*\nok"})

		// Oversized live turn, but within its reserve (at the boundary).
		share, _, _ := liveTurnShares(state.Budget)
		userInput := strings.Repeat("L", share)
		if _, _, err := RunWithInfo(context.Background(), state, nil, userInput, io.Discard); err != nil {
			t.Fatalf("run: %v", err)
		}
		// The system prompt (memory layers) must NOT be zeroed to fit the
		// live turn: it carries the seeded spine (A1) content.
		cap.mu.Lock()
		last := cap.last
		cap.mu.Unlock()
		var sys string
		for _, m := range last {
			if m.Role == "system" {
				sys = m.Content
			}
		}
		if strings.TrimSpace(sys) == "" {
			t.Errorf("memory layers zeroed to fit an oversized live turn (I2 violation)")
		}
		if !strings.Contains(sys, "thr_") {
			t.Errorf("system prompt lacks composed spine content: %q", sys)
		}
	})
}

// TestA5LayerCExceedsOldCeiling — A5 / I6 (count-cap consistency, LRU
// side): with the count cap dropped, the dormant set accumulates past the
// old 20-thread / ~4KB ceiling, so Layer C content can exceed it. Mock-
// independent: drives the LRU directly, then composes.
func TestA5LayerCExceedsOldCeiling(t *testing.T) {
	paths, meta := newTestHome(t)

	// Seed many dormant threads with full-length (200-char) summaries so
	// the composed Layer C, were the old 20-count cap still in force, would
	// be bounded near ~4KB. With the cap dropped and the byte budget the
	// sole bound, the composed Layer C can far exceed it.
	const n = 60
	dormant := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		id := itoaThreadID(i)
		rec := memops.SpineRecord{
			ID:           id,
			Project:      meta.ID,
			Anchors:      []string{"alpha", "beta"},
			Summary:      strings.Repeat("s", 180),
			State:        memops.ThreadWIP,
			Created:      "2026-04-01T00:00:00Z",
			LastEngaged:  "2026-04-01T00:00:00Z",
			StateChanged: "2026-04-01T00:00:00Z",
			TurnCount:    1,
		}
		if err := store.AppendSpineRecord(paths, rec); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		dormant = append(dormant, id)
	}

	ops := fileadapter.NewFileAdapter(paths)
	ws, err := ops.ComposeWorkingSet(context.Background(), memops.WorksetInput{
		ActiveProject:  meta,
		DormantThreads: dormant,
		Budget:         memops.DefaultBudget(),
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	const oldCeiling = 4096
	if len(ws.LayerC) <= oldCeiling {
		t.Errorf("Layer C content (%d bytes) did not exceed the old ~4KB count-cap ceiling; "+
			"the dropped count cap should let the byte budget fill", len(ws.LayerC))
	}
}
