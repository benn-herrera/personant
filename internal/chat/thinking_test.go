package chat

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/store"
)

// newThinkingFixture builds a thinking renderer over a progress with the
// given terminal determinations, capturing everything either of them
// writes. It mirrors newThinking's own wiring (both determinations are
// READ from the progress) so the test cannot drift from production.
func newThinkingFixture(interactive, ansi, on bool) (*thinking, *bytes.Buffer) {
	out := &bytes.Buffer{}
	pr := &progress{out: out, enabled: interactive, animate: ansi, lineClean: true}
	return newThinking(pr, out, on), out
}

// The regression that protects the sim harness, the scenario tests, and
// every piped invocation: a non-terminal session writes ZERO reasoning
// bytes regardless of the setting, while passing content through
// byte-for-byte.
func TestThinking_NonTTYWritesZeroReasoningBytes(t *testing.T) {
	for _, on := range []bool{false, true} {
		th, out := newThinkingFixture(false, false, on)
		th.reasoning("scratch that must never appear")
		th.reasoning("more scratch")
		th.end()
		if got := out.String(); got != "" {
			t.Errorf("showThinking=%v on a non-terminal wrote %q, want nothing", on, got)
		}
		if _, err := io.WriteString(th, "the answer\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
		if got := out.String(); got != "the answer\n" {
			t.Errorf("showThinking=%v: content not passed through verbatim: %q", on, got)
		}
	}
}

// Off is off even on a real terminal — the config default.
func TestThinking_DisabledOnTerminalWritesNothing(t *testing.T) {
	th, out := newThinkingFixture(true, true, false)
	th.reasoning("scratch")
	th.end()
	if got := out.String(); got != "" {
		t.Errorf("disabled thinking wrote %q, want nothing", got)
	}
}

// The reasoning→content transition, which is the whole risk: the answer
// must not arrive dimmed, and must not be jammed onto the tail of the
// reasoning line. One dim-open, one dim-close, a newline, then the body
// verbatim.
func TestThinking_ReasoningThenContentTransition(t *testing.T) {
	th, out := newThinkingFixture(true, true, true)
	th.reasoning("first ")
	th.reasoning("second")
	if _, err := io.WriteString(th, "the answer"); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := dimOn + "first " + "second" + dimOff + "\n" + "the answer"
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The body is intact and undimmed: everything after the reset is the
	// answer, byte-for-byte.
	body := out.String()[strings.Index(out.String(), dimOff)+len(dimOff):]
	if body != "\nthe answer" {
		t.Errorf("body after the reasoning run = %q, want %q", body, "\nthe answer")
	}
}

// A second content write must not re-open or re-close anything: the run
// is closed exactly once, at the transition.
func TestThinking_RunClosesExactlyOnce(t *testing.T) {
	th, out := newThinkingFixture(true, true, true)
	th.reasoning("scratch")
	io.WriteString(th, "one ")
	io.WriteString(th, "two")
	th.end() // defensive call from runOneTurn — must be a no-op here
	if got, want := strings.Count(out.String(), dimOff), 1; got != want {
		t.Errorf("dim reset written %d times, want %d (%q)", got, want, out.String())
	}
	if got, want := strings.Count(out.String(), dimOn), 1; got != want {
		t.Errorf("dim open written %d times, want %d (%q)", got, want, out.String())
	}
}

// The reasoning-only turn (the D6 empty-response shape): no content ever
// arrives, so runOneTurn's explicit end() is what returns the terminal to
// undimmed, column 0.
func TestThinking_ReasoningOnlyTurnEndsClean(t *testing.T) {
	th, out := newThinkingFixture(true, true, true)
	th.reasoning("thought hard, said nothing")
	th.end()
	if !strings.HasSuffix(out.String(), dimOff+"\n") {
		t.Errorf("reasoning-only turn left the terminal at %q, want a dim reset and a newline", out.String())
	}
}

// No-ANSI degradation (TERM=dumb / unset): escape codes would be printed
// literally, so the distinction is carried by words instead. The one
// thing that must not happen is reasoning that reads as the answer.
func TestThinking_NoANSIFallbackIsReadable(t *testing.T) {
	th, out := newThinkingFixture(true, false, true)
	th.reasoning("scratch")
	io.WriteString(th, "the answer")
	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Errorf("no-ANSI terminal got escape codes: %q", got)
	}
	want := thinkingOpen + "scratch" + thinkingClose + "\n" + "the answer"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Toggling off mid-run must not leave the terminal dim.
func TestThinking_SetOffClosesOpenRun(t *testing.T) {
	th, out := newThinkingFixture(true, true, true)
	th.reasoning("scratch")
	th.setOn(false)
	if !strings.HasSuffix(out.String(), dimOff+"\n") {
		t.Errorf("toggling off left the terminal at %q, want a dim reset", out.String())
	}
	th.reasoning("more scratch")
	if strings.Contains(out.String(), "more scratch") {
		t.Errorf("reasoning still rendered after /thinking off: %q", out.String())
	}
}

// Toggling on mid-session takes effect for the next delta.
func TestThinking_SetOnStartsRendering(t *testing.T) {
	th, out := newThinkingFixture(true, true, false)
	th.reasoning("hidden")
	th.setOn(true)
	th.reasoning("shown")
	got := out.String()
	if strings.Contains(got, "hidden") {
		t.Errorf("pre-toggle reasoning rendered: %q", got)
	}
	if !strings.Contains(got, "shown") {
		t.Errorf("post-toggle reasoning missing: %q", got)
	}
}

func TestParseThinkingArg(t *testing.T) {
	tests := []struct {
		name   string
		arg    string
		wantOn bool
		wantOK bool
	}{
		{"on", "on", true, true},
		{"off", "off", false, true},
		{"case-insensitive", "ON", true, true},
		{"padded", "  off  ", false, true},
		{"unrecognized", "maybe", false, false},
		{"true is not the vocabulary", "true", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			on, ok := parseThinkingArg(tc.arg)
			if on != tc.wantOn || ok != tc.wantOK {
				t.Errorf("parseThinkingArg(%q) = (%v, %v), want (%v, %v)", tc.arg, on, ok, tc.wantOn, tc.wantOK)
			}
		})
	}
}

// ---------- /thinking, driven through a real session ----------

// writeConfig lands a config.toml in the scaffolded home. The provider
// pool is written by scaffoldHome; this only adds the [chat] choices.
func writeConfig(t *testing.T, paths store.PersonantPaths, body string) {
	t.Helper()
	if err := os.WriteFile(paths.Config, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// The three /thinking forms, in one session so the state transitions are
// observed rather than assumed. The session is non-interactive (scripted
// stdin), so "on" reports honestly that nothing will be shown.
func TestSlashThinkingTransitions(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, errb := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/thinking\n/thinking on\n/thinking\n/thinking off\n/thinking\n/quit\n")

	want := []string{
		"thinking display: off",
		"thinking display: on (not a terminal — nothing will be shown)",
		"thinking display: on (not a terminal — nothing will be shown)",
		"thinking display: off",
		"thinking display: off",
	}
	// The non-interactive reader echoes its "> " prompt onto the same
	// line, so match on the report rather than the line start.
	var got []string
	for line := range strings.SplitSeq(out, "\n") {
		if i := strings.Index(line, "thinking display:"); i >= 0 {
			got = append(got, line[i:])
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("state reports =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if errb != "" {
		t.Errorf("unexpected stderr: %q", errb)
	}
	if !strings.Contains(helpText(), "/thinking") {
		t.Error("/help does not advertise /thinking")
	}
}

// An unrecognized argument is a usage error, not a silent no-op: the user
// who typed /thinking yes must not be left believing it took.
func TestSlashThinkingBadArgIsUsageError(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, errb := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/thinking yes\n/thinking\n/quit\n")

	if !strings.Contains(errb, "usage: /thinking [on|off]") {
		t.Errorf("expected usage error, got stderr: %q", errb)
	}
	if !strings.Contains(out, "thinking display: off") {
		t.Errorf("rejected argument changed the state: %q", out)
	}
}

// config.toml [chat] showThinking establishes the session default, and
// /thinking overrides it for the session only.
func TestConfigShowThinkingIsTheSessionDefault(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{"absent section", "", "off"},
		{"absent field", "[chat]\ndefaultModel = \"local/test-model\"\n", "off"},
		{"explicitly false", "[chat]\nshowThinking = false\n", "off"},
		{"explicitly true", "[chat]\nshowThinking = true\n", "on"},
		// An unrecognized key in [chat] must not refuse the config: a
		// display preference from a newer binary is not a reason to
		// block the session.
		{"unknown key tolerated", "[chat]\nshowThinking = true\nshowSomethingElse = 3\n", "on"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			paths := scaffoldHome(t)
			writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
			if tc.config != "" {
				writeConfig(t, paths, tc.config)
			}
			out, _ := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
				"prj_1", "/thinking\n/quit\n")
			if !strings.Contains(out, "thinking display: "+tc.want) {
				t.Errorf("config %q → want default %s, got:\n%s", tc.config, tc.want, out)
			}
		})
	}
}
