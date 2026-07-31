package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/tools"
)

// §6.1.1 web.search tests. The provider is faked: the contract under
// test is the TRICHOTOMY (found / empty / failed) and the local query
// cap, none of which is a property of any particular backend. Exa's own
// wire handling is tested in exa_test.go.

// fakeProvider returns canned results or a canned error, and counts
// calls so the cap can be measured at the seam it actually protects.
type fakeProvider struct {
	results []SearchResult
	err     error
	calls   int
}

func (f *fakeProvider) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	f.calls++
	return f.results, f.err
}

func searchHandler(t *testing.T, cfg SearchConfig) tools.Handler {
	t.Helper()
	tool, err := NewSearchTool(cfg)
	if err != nil {
		t.Fatalf("NewSearchTool: %v", err)
	}
	if tool.Spec.Name != ToolNameSearch {
		t.Fatalf("tool name = %q, want %q", tool.Spec.Name, ToolNameSearch)
	}
	if tool.Tier != 0 || tool.Mutates {
		t.Fatalf("web.search must be tier 0 and non-mutating; got tier=%d mutates=%v", tool.Tier, tool.Mutates)
	}
	return tool.Handler
}

func query(t *testing.T, h tools.Handler, ctx context.Context, q string) (string, error) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"query": q})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := h(ctx, args)
	return string(out), err
}

// TestSearchTrichotomy is the wave's most important behavioral test.
// "Search failed" and "search found nothing" must be distinguishable BY
// THE MODEL, from the text alone — a silent-empty on failure teaches it
// that the web has no answer, and it then says so with confidence.
func TestSearchTrichotomy(t *testing.T) {
	found := &fakeProvider{results: []SearchResult{
		{Title: "Sourdough hydration", URL: "https://example.com/a", Snippet: "Water to flour by weight.", Published: "2026-03-14"},
		{Title: "Crumb structure", URL: "https://example.com/b", Snippet: "Alveoli form where gluten stretches."},
	}}
	empty := &fakeProvider{}
	failed := &fakeProvider{err: errors.New("dial tcp 1.2.3.4:443: connect: connection refused")}

	foundOut, err := query(t, searchHandler(t, SearchConfig{Provider: found}), context.Background(), "hydration")
	if err != nil {
		t.Fatalf("found: unexpected error: %v", err)
	}
	emptyOut, err := query(t, searchHandler(t, SearchConfig{Provider: empty}), context.Background(), "hydration")
	if err != nil {
		t.Fatalf("empty: an empty result is a SUCCESS, not an error: %v", err)
	}
	failedOut, failErr := query(t, searchHandler(t, SearchConfig{Provider: failed}), context.Background(), "hydration")
	if failErr == nil {
		t.Fatalf("failed: a backend failure must be an error, not empty content; got:\n%s", failedOut)
	}

	// Found: ranked, with the fields the model needs to pick a URL.
	for _, want := range []string{"Sourdough hydration", "https://example.com/a", "Water to flour", "2026-03-14", "web.fetch"} {
		if !strings.Contains(foundOut, want) {
			t.Errorf("found result missing %q:\n%s", want, foundOut)
		}
	}

	// Empty: says it SUCCEEDED and matched nothing.
	if !strings.Contains(emptyOut, "0 results") || !strings.Contains(emptyOut, "successfully") {
		t.Errorf("empty result does not state that the search ran and matched nothing:\n%s", emptyOut)
	}
	if !strings.Contains(emptyOut, "EMPTY RESULT") {
		t.Errorf("empty result is not explicitly labelled:\n%s", emptyOut)
	}

	// Failed: says NO search happened, and says what not to conclude.
	failText := failErr.Error()
	if !strings.Contains(failText, "could not be performed") || !strings.Contains(failText, "NO search happened") {
		t.Errorf("failure text does not state that no search happened: %s", failText)
	}
	if !strings.Contains(failText, "Do not report that nothing was found") {
		t.Errorf("failure text does not warn against the wrong conclusion: %s", failText)
	}

	// And the two are not merely different strings — the distinguishing
	// claims are mutually exclusive.
	if strings.Contains(failText, "matched 0 results") {
		t.Error("failure text claims an empty result set")
	}
	if strings.Contains(emptyOut, "could not be performed") {
		t.Error("empty text claims a failure")
	}
}

// TestSearchCapPerTurn — the runaway-loop case. The cap is enforced
// locally, on ATTEMPTS, and the refusal tells the model to stop rather
// than to retry.
func TestSearchCapPerTurn(t *testing.T) {
	pinDay(t, "2026-07-31")
	p := &fakeProvider{results: []SearchResult{{Title: "t", URL: "https://example.com"}}}
	h := searchHandler(t, SearchConfig{Provider: p, MaxPerTurn: 2, MaxPerDay: 100})

	ctx := tools.ContextWithTurn(context.Background(), 7)
	for i := 1; i <= 2; i++ {
		if _, err := query(t, h, ctx, "q"); err != nil {
			t.Fatalf("query %d under the cap failed: %v", i, err)
		}
	}
	_, err := query(t, h, ctx, "q")
	if err == nil {
		t.Fatal("third query in the same turn was not capped")
	}
	if !strings.Contains(err.Error(), "per turn") || !strings.Contains(err.Error(), "Do not retry") {
		t.Errorf("cap refusal is not actionable: %v", err)
	}
	if p.calls != 2 {
		t.Errorf("provider was called %d times under a per-turn cap of 2", p.calls)
	}

	// The next TURN gets a fresh allowance — the counter is keyed on the
	// turn number the loop puts on the context, with no reset call to
	// forget.
	if _, err := query(t, h, tools.ContextWithTurn(context.Background(), 8), "q"); err != nil {
		t.Fatalf("the next turn did not get a fresh per-turn allowance: %v", err)
	}
}

// TestSearchCapPerDay — the per-day cap binds across turns, and rolls
// over on the timeline clock rather than on a wall-clock read.
func TestSearchCapPerDay(t *testing.T) {
	restore := pinDay(t, "2026-07-31")
	p := &fakeProvider{results: []SearchResult{{Title: "t", URL: "https://example.com"}}}
	h := searchHandler(t, SearchConfig{Provider: p, MaxPerTurn: 1, MaxPerDay: 3})

	for turn := 1; turn <= 3; turn++ {
		if _, err := query(t, h, tools.ContextWithTurn(context.Background(), turn), "q"); err != nil {
			t.Fatalf("turn %d under the day cap failed: %v", turn, err)
		}
	}
	_, err := query(t, h, tools.ContextWithTurn(context.Background(), 4), "q")
	if err == nil {
		t.Fatal("fourth query in the same day was not capped")
	}
	if !strings.Contains(err.Error(), "per day") {
		t.Errorf("day-cap refusal does not name the window: %v", err)
	}
	if p.calls != 3 {
		t.Errorf("provider was called %d times under a per-day cap of 3", p.calls)
	}

	// Tomorrow is a new allowance.
	restore()
	pinDay(t, "2026-08-01")
	if _, err := query(t, h, tools.ContextWithTurn(context.Background(), 5), "q"); err != nil {
		t.Fatalf("the day cap did not roll over: %v", err)
	}
}

// TestSearchCapCountsFailedAttempts — a cap that only counted successes
// would not stop a loop against a broken backend, which is the loop most
// likely to run away.
func TestSearchCapCountsFailedAttempts(t *testing.T) {
	pinDay(t, "2026-07-31")
	p := &fakeProvider{err: errors.New("backend on fire")}
	h := searchHandler(t, SearchConfig{Provider: p, MaxPerTurn: 2})

	ctx := tools.ContextWithTurn(context.Background(), 1)
	for i := 0; i < 3; i++ {
		_, _ = query(t, h, ctx, "q")
	}
	if p.calls != 2 {
		t.Errorf("backend was called %d times; failed attempts must consume the cap too", p.calls)
	}
}

// TestSearchRejectsEmptyQuery / nil provider — the argument-shaped
// mistakes, each answered with a message naming the problem.
func TestSearchRejectsBadInput(t *testing.T) {
	h := searchHandler(t, SearchConfig{Provider: &fakeProvider{}})
	for _, args := range []string{`{}`, `{"query":"   "}`, `"{}"`} {
		if _, err := h(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %q: empty query was accepted", args)
		}
	}
	if _, err := NewSearchTool(SearchConfig{}); err == nil {
		t.Error("a search tool with no provider was built")
	}
}

// pinDay freezes the timeline clock at a date so the day-keyed counter is
// deterministic. Returns the restore func for tests that need to advance.
func pinDay(t *testing.T, day string) (restore func()) {
	t.Helper()
	at, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("parse %q: %v", day, err)
	}
	restore = clock.SetTimeline(func() time.Time { return at })
	t.Cleanup(restore)
	return restore
}
