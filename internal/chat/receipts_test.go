package chat

import (
	"io"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/tools/web"
	"personant/internal/turn"
)

// §6.1 tool receipts. The property under test throughout: a tool call
// leaves ONE line the user can read after the fact, on a terminal, and
// ZERO bytes anywhere else.

func receipt(name, gist, outcome string, isErr bool, d time.Duration) turn.ToolReceipt {
	return turn.ToolReceipt{Name: name, ArgsGist: gist, Outcome: outcome, Err: isErr, Elapsed: d}
}

func TestToolReceiptLine(t *testing.T) {
	tests := []struct {
		name string
		r    turn.ToolReceipt
		want string
	}{
		{
			"success",
			receipt("web.wikipedia", `q="general relativity"`, "ok, 4.1kB", false, 400*time.Millisecond),
			`[tool] web.wikipedia q="general relativity" → ok, 4.1kB (400ms)`,
		},
		{
			"failure is labelled from Err, not sniffed out of the text",
			receipt("web.wikidata", `q="relativity"`, "did not run: contact required", true, 90*time.Millisecond),
			`[tool] web.wikidata q="relativity" → error: did not run: contact required (90ms)`,
		},
		{
			"no arguments",
			receipt("fs.tree", "", "ok, 12B", false, 1500*time.Millisecond),
			`[tool] fs.tree → ok, 12B (1.5s)`,
		},
		{
			"seconds round to tenths",
			receipt("web.fetch", `url="https://example.com"`, "ok, 1.0kB", false, 12345*time.Millisecond),
			`[tool] web.fetch url="https://example.com" → ok, 1.0kB (12.3s)`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolReceiptLine(tc.r); got != tc.want {
				t.Errorf("toolReceiptLine() =\n %q\nwant %q", got, tc.want)
			}
		})
	}
}

// The ruling itself: the line is DIM, it is COMMITTED, and it is still
// there — still dim — after the answer has streamed over it. A receipt
// that the next write erases would leave the user exactly where they
// started, asking the model what it did.
func TestToolReceipt_CommitsOneDimLineThatSurvivesTheBody(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	emit := toolReceipts(f.tm)

	line := toolReceiptLine(receipt("web.wikipedia", `q="general relativity"`, "ok, 4.1kB", false, 400*time.Millisecond))
	emit(receipt("web.wikipedia", `q="general relativity"`, "ok, 4.1kB", false, 400*time.Millisecond))

	s := f.screen(t)
	if got := s.Row(0); got != line {
		t.Fatalf("receipt row = %q, want %q\n%s", got, line, s)
	}
	if !s.RowDim(0) {
		t.Errorf("the receipt is not dim\n%s", s)
	}
	if got := s.TotalRows(); got != 2 {
		t.Errorf("the receipt took %d rows, want 1 plus the fresh line\n%s", got, s)
	}

	if _, err := io.WriteString(f.tm.Out(), "Einstein published it in 1915.\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	s = f.screen(t)
	if got := s.Row(0); got != line {
		t.Errorf("the body overwrote the receipt: row 0 = %q\n%s", got, s)
	}
	if got := s.Row(1); got != "Einstein published it in 1915." {
		t.Errorf("the answer did not start on its own row: %q\n%s", got, s)
	}
	dim := s.DimLines()
	if len(dim) != 1 || dim[0] != line {
		t.Errorf("dim lines = %q, want exactly the receipt\n%s", dim, s)
	}
}

// The two-dim-producers question, answered by construction rather than by
// coordination: reasoning, then a receipt, then more reasoning, then the
// answer. Every run is closed where it should be — nothing bleeds, nothing
// jams onto the tail of a half-finished thought, and the answer is not dim.
func TestToolReceipt_InterleavesWithReasoningWithoutBleeding(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	th := newThinking(f.tm, true)
	emit := toolReceipts(f.tm)

	r := receipt("web.search", `q="knot theory"`, "ok, 2.0kB", false, 250*time.Millisecond)
	th.reasoning("I should look this up")
	emit(r)
	th.reasoning("that is enough to answer")
	if _, err := io.WriteString(f.tm.Out(), "Knot theory studies embeddings of circles.\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := f.screen(t)
	want := []string{
		"I should look this up",
		toolReceiptLine(r),
		"that is enough to answer",
		"Knot theory studies embeddings of circles.",
	}
	for i, w := range want {
		if got := s.Row(i); got != w {
			t.Errorf("row %d = %q, want %q\n%s", i, got, w, s)
		}
	}
	dim := s.DimLines()
	if strings.Join(dim, "\n") != strings.Join(want[:3], "\n") {
		t.Errorf("dim lines = %q, want the three scratch/receipt rows and nothing else\n%s", dim, s)
	}
	if s.RowDim(3) {
		t.Errorf("the answer came out dim — a run was left open\n%s", s)
	}
}

// Consecutive receipts — one round with several calls — each get their own
// row. Two receipts on one line would be one unreadable line.
func TestToolReceipt_ConsecutiveReceiptsEachTakeARow(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	emit := toolReceipts(f.tm)

	first := receipt("web.search", `q="knots"`, "ok, 2.0kB", false, 250*time.Millisecond)
	second := receipt("web.wikipedia", `q="knots"`, "did not run: contact required", true, 40*time.Millisecond)
	emit(first)
	emit(second)

	s := f.screen(t)
	if got := s.Row(0); got != toolReceiptLine(first) {
		t.Errorf("row 0 = %q\n%s", got, s)
	}
	if got := s.Row(1); got != toolReceiptLine(second) {
		t.Errorf("row 1 = %q\n%s", got, s)
	}
	if !s.Contains("error: did not run: contact required") {
		t.Errorf("the refusal is not legible on screen\n%s", s)
	}
	if got := s.TotalRows(); got != 3 {
		t.Errorf("two receipts took %d rows, want 2 plus the fresh line\n%s", got, s)
	}
}

// A receipt claims the line from the progress indicator, exactly as any
// other committed write does — and leaves no frame residue behind it.
func TestToolReceipt_RetiresTheIndicatorFrame(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	emit := toolReceipts(f.tm)
	f.setElapsed(5 * time.Second)
	f.p.phase(turn.PhaseRunningTool("web.search"))
	if !strings.Contains(f.slot(), "web.search") {
		t.Fatalf("the indicator did not draw: %q", f.slot())
	}

	r := receipt("web.search", `q="knots"`, "ok, 2.0kB", false, 250*time.Millisecond)
	emit(r)

	if got := f.slot(); got != "" {
		t.Errorf("the slot survived the receipt: %q", got)
	}
	s := f.screen(t)
	if got := s.Row(0); got != toolReceiptLine(r) {
		t.Errorf("row 0 = %q — an indicator fragment survived on the receipt's line\n%s", got, s)
	}
	f.p.stop()
}

// /thinking gates the reasoning SINK, not the channel. A user who turned
// the model's scratch off did not ask to stop being told what ran.
func TestToolReceipt_ShownRegardlessOfThinking(t *testing.T) {
	f := newProgressFixture(t, true, true, fixtureSize)
	th := newThinking(f.tm, false)
	emit := toolReceipts(f.tm)

	th.reasoning("scratch that must not be shown")
	r := receipt("web.wikipedia", `q="general relativity"`, "ok, 4.1kB", false, 400*time.Millisecond)
	emit(r)

	s := f.screen(t)
	if s.Contains("scratch that must not be shown") {
		t.Fatalf("/thinking off leaked reasoning\n%s", s)
	}
	if got := s.Row(0); got != toolReceiptLine(r) {
		t.Errorf("the receipt is gated by /thinking; row 0 = %q\n%s", got, s)
	}
}

// The channel property, asserted rather than assumed: a non-interactive
// session writes ZERO bytes. This is what keeps the committed piped-session
// goldens untouched BY CONSTRUCTION — receipts cannot reach a pipe.
func TestToolReceipt_NonInteractiveWritesNothing(t *testing.T) {
	f := newProgressFixture(t, false, false, fixtureSize)
	emit := toolReceipts(f.tm)
	emit(receipt("web.wikipedia", `q="general relativity"`, "ok, 4.1kB", false, 400*time.Millisecond))
	emit(receipt("web.wikidata", `q="x"`, "did not run: contact required", true, 40*time.Millisecond))
	if got := f.rendered(); got != "" {
		t.Errorf("a piped session emitted %q, want nothing", got)
	}
}

// TERM=dumb is still a TERMINAL, so the receipt IS shown — the honest
// degradation is that it cannot be dimmed, and term brackets it with words
// instead. What must not happen is an escape byte or a lost receipt.
func TestToolReceipt_DumbTerminalShowsItWithoutEscapes(t *testing.T) {
	f := newProgressFixture(t, true, false, fixtureSize)
	emit := toolReceipts(f.tm)
	r := receipt("web.wikipedia", `q="general relativity"`, "ok, 4.1kB", false, 400*time.Millisecond)
	emit(r)

	stream := f.rendered()
	if i := strings.IndexByte(stream, 0x1b); i >= 0 {
		t.Errorf("TERM=dumb emitted an escape byte at %d: %q", i, stream)
	}
	s := f.screen(t)
	if !s.Contains(toolReceiptLine(r)) {
		t.Errorf("the receipt was lost on a terminal that cannot dim it\n%s", s)
	}
	if got := s.DimLines(); len(got) != 0 {
		t.Errorf("a terminal that cannot dim produced dim cells: %q\n%s", got, s)
	}
}

// End to end, off a terminal: a real session whose model asks for a tool
// runs it — and the piped bytes carry no receipt at all. Both halves
// matter: the wiring is live in Run, and the goldens cannot move.
func TestToolReceipt_PipedSessionRunsToolsAndEmitsNoReceipt(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	mock := model.NewScriptedMockPerConsult([]model.Response{
		{
			FinishReason: "tool_calls",
			ToolCalls: []model.ToolCall{{
				ID: "c1", Function: web.ToolNameWikipedia, Args: []byte(`"{\"q\":\"general relativity\"}"`),
			}},
		},
		{Content: "*topic: *new-topic* [relativity, physics]*\nHere is what I found."},
	})
	mock.Models = []model.ModelInfo{{ID: "test-model"}}

	out, errb := runChat(t, paths, mock, "prj_1", "tell me about general relativity\n/quit\n")

	if !strings.Contains(out, "Here is what I found") {
		t.Fatalf("the tool turn did not complete:\n%s", out)
	}
	for name, stream := range map[string]string{"stdout": out, "stderr": errb} {
		if strings.Contains(stream, toolReceiptPrefix) {
			t.Errorf("a receipt reached %s — the goldens would move:\n%s", name, stream)
		}
	}
	// The event log is the durable half and is unaffected by any of this.
	if logs := readAllLogs(t, paths); !strings.Contains(logs, "tool.error name="+web.ToolNameWikipedia) &&
		!strings.Contains(logs, "tool.result name="+web.ToolNameWikipedia) {
		t.Errorf("the tool call left no §2.8 record:\n%s", logs)
	}
}
