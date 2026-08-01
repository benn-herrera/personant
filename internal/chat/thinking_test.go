package chat

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/store"
	"personant/internal/term"
)

// newThinkingFixture builds a thinking renderer over a terminal with the
// given determinations, capturing every byte it emits. Both
// determinations are READ FROM term — the one TTY predicate and the one
// ANSI determination — so the test cannot drift from production.
//
// What is asserted here is POLICY: whether reasoning is offered to the
// channel at all. How the channel renders it (dim brackets, the words
// fallback, closing the run before content) belongs to term and is
// asserted there, which is the whole point of the W1 collapse.
func newThinkingFixture(t *testing.T, interactive, ansi, on bool) (*thinking, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	opts := term.Options{Stdout: out}
	if interactive {
		opts.Platform = term.NewUnixPlatform(&fakeDevice{out: out, size: term.Size{Cols: 80, Rows: 24}})
		if ansi {
			opts.TermEnv = "xterm-256color"
		}
	}
	tm, err := term.Open(opts)
	if err != nil {
		t.Fatalf("term.Open: %v", err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	return newThinking(tm, on), out
}

// The regression that protects the sim harness, the scenario tests, and
// every piped invocation: a non-terminal session writes ZERO reasoning
// bytes regardless of the setting, while passing content through
// byte-for-byte.
func TestThinking_NonTTYWritesZeroReasoningBytes(t *testing.T) {
	for _, on := range []bool{false, true} {
		th, out := newThinkingFixture(t, false, false, on)
		th.reasoning("scratch that must never appear")
		th.reasoning("more scratch")
		if got := out.String(); got != "" {
			t.Errorf("showThinking=%v on a non-terminal wrote %q, want nothing", on, got)
		}
	}
}

// Off is off even on a real terminal — the config default.
func TestThinking_DisabledOnTerminalWritesNothing(t *testing.T) {
	th, out := newThinkingFixture(t, true, true, false)
	th.reasoning("scratch")
	if got := out.String(); got != "" {
		t.Errorf("disabled thinking wrote %q, want nothing", got)
	}
}

// Toggling off stops offering deltas to the channel; toggling on
// resumes. The RENDERING of the run — dim brackets, the words fallback
// on a dumb terminal, and closing the run before content lands — is
// term's and is asserted in internal/term, where the one serialization
// point that performs it lives.
func TestThinking_ToggleGatesTheChannel(t *testing.T) {
	th, out := newThinkingFixture(t, true, true, true)
	th.reasoning("shown")
	if !strings.Contains(out.String(), "shown") {
		t.Fatalf("enabled thinking rendered nothing: %q", out.String())
	}
	th.setOn(false)
	th.reasoning("hidden after off")
	if strings.Contains(out.String(), "hidden after off") {
		t.Errorf("reasoning still rendered after /thinking off: %q", out.String())
	}
	th.setOn(true)
	th.reasoning("shown again")
	if !strings.Contains(out.String(), "shown again") {
		t.Errorf("post-toggle reasoning missing: %q", out.String())
	}
}

// A non-terminal session shows nothing whatever the setting says, and
// showing() is the single predicate that says so — both halves, one
// place.
func TestThinking_ShowingNeedsBothHalves(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		interactive, on, want bool
	}{
		{"terminal + on", true, true, true},
		{"terminal + off", true, false, false},
		{"pipe + on", false, true, false},
		{"pipe + off", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th, _ := newThinkingFixture(t, tc.interactive, true, tc.on)
			if got := th.showing(); got != tc.want {
				t.Errorf("showing() = %v, want %v", got, tc.want)
			}
		})
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
		{"explicitly false", "[chat]\ndefaultModel = \"local/test-model\"\nshowThinking = false\n", "off"},
		{"explicitly true", "[chat]\ndefaultModel = \"local/test-model\"\nshowThinking = true\n", "on"},
		// An unrecognized key in [chat] must not refuse the config: a
		// display preference from a newer binary is not a reason to
		// block the session.
		{"unknown key tolerated", "[chat]\ndefaultModel = \"local/test-model\"\nshowThinking = true\nshowSomethingElse = 3\n", "on"},
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
