package chat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
)

// /terminal-setup is the one command that writes OUTSIDE $PERSONANT_HOME,
// so the tests are weighted toward the two questions that follow from
// that: does it ever write when it should not, and can what it writes
// corrupt a file it did not author.
//
// Every test that could touch a real keymap points $XDG_CONFIG_HOME at a
// t.TempDir() first — zedKeymapPath consults it before $HOME — so a test
// run cannot reach the developer's own Zed config.

// fixedNow keeps the timestamped backup path deterministic.
var fixedNow = time.Date(2026, 8, 5, 14, 30, 15, 0, time.UTC)

const fixedBackupSuffix = ".personant-bak-20260805-143015"

func TestDetectTerminal(t *testing.T) {
	tests := []struct {
		name     string
		term     string
		lcTerm   string
		wantKind terminalKind
		wantName string
		wantVar  string
	}{
		{"iterm", "iTerm.app", "", kindITerm2, "iTerm2", envTermProgram},
		{"zed", "zed", "", kindZed, "Zed", envTermProgram},
		{"vscode", "vscode", "", kindVSCode, "VS Code", envTermProgram},
		{"apple", "Apple_Terminal", "", kindAppleTerminal, "Terminal.app", envTermProgram},
		{"wezterm", "WezTerm", "", kindWezTerm, "WezTerm", envTermProgram},
		{"case folded", "ITERM.APP", "", kindITerm2, "iTerm2", envTermProgram},
		{"padded", "  zed  ", "", kindZed, "Zed", envTermProgram},

		// $LC_TERMINAL is what survives ssh, and it spells iTerm2
		// differently from $TERM_PROGRAM.
		{"lc_terminal fallback", "", "iTerm2", kindITerm2, "iTerm2", envLCTerminal},
		{"lc_terminal rescues an unknown TERM_PROGRAM", "tmux", "iTerm2", kindITerm2, "iTerm2", envLCTerminal},
		{"term_program wins when both are known", "zed", "iTerm2", kindZed, "Zed", envTermProgram},

		// Unrecognized: the raw value is reported so the user can see what
		// personant looked at.
		{"unknown", "ghostty", "", kindUnknown, "ghostty", envTermProgram},
		{"nothing set", "", "", kindUnknown, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envTermProgram, tc.term)
			t.Setenv(envLCTerminal, tc.lcTerm)

			got := detectTerminal()
			if got.Kind != tc.wantKind || got.Name != tc.wantName || got.EnvVar != tc.wantVar {
				t.Errorf("detectTerminal() = %+v, want kind %v name %q var %q",
					got, tc.wantKind, tc.wantName, tc.wantVar)
			}
		})
	}
}

// The keymap fixtures. Named rather than files: every one of them is
// small, and the assertions are byte-level, so the input and the expected
// output want to be readable side by side.
const (
	// fxEmptyArray is what Zed itself leaves behind.
	fxEmptyArray = "[]\n"

	// fxCommented is the shape the insertion has to survive: a header
	// comment, an existing entry, and a comment AFTER the last entry, with
	// no entry following it.
	fxCommented = `// my zed keymap
[
  // editor keys
  {"context": "Editor", "bindings": {"ctrl-s": "workspace::Save"}}
  // trailing note, no entry after it
]
`

	// fxTrailingComma is legal in Zed's parser and must not be turned into
	// a double comma.
	fxTrailingComma = `[
  {"context": "Editor", "bindings": {}},
]
`

	// fxSingleLine has no newline anywhere; the closing bracket must not
	// end up on the inserted entry's line.
	fxSingleLine = `[{"context": "Editor", "bindings": {}}]`

	// fxBracketInString: a `]` inside a string value is not structure.
	fxBracketInString = `[
  {"context": "Editor", "bindings": {"ctrl-s": "a ] b"}}
]
`

	// fxOtherContext binds shift-enter somewhere that is NOT the terminal,
	// which does not configure the thing this command configures.
	fxOtherContext = `[
  {"context": "Editor", "bindings": {"shift-enter": "editor::Newline"}}
]
`

	// fxConfigured already has the binding, by whatever route.
	fxConfigured = `[
  {"context": "Terminal", "bindings": {"shift-enter": "something"}}
]
`

	// fxConfiguredCompound: the context is a Zed predicate, not a bare
	// name.
	fxConfiguredCompound = `[
  {"context": "Terminal && mode == normal", "bindings": {"shift-enter": "x"}}
]
`

	// fxShiftEnterInComment must NOT read as configured: a commented-out
	// binding binds nothing.
	fxShiftEnterInComment = `[
  // {"context": "Terminal", "bindings": {"shift-enter": "x"}}
  {"context": "Editor", "bindings": {}}
]
`
)

// The defeat cases: files this command must refuse rather than edit.
const (
	fxNoClose              = "[\n  {\"context\": \"Editor\", \"bindings\": {}}\n"
	fxTopLevelObject       = "{\n  \"context\": \"Editor\"\n}\n"
	fxTrailingJunk         = "[]\n{}\n"
	fxUnterminatedString   = "[{\"context\": \"Terminal]\n"
	fxUnterminatedComment  = "[]\n/* what happened here\n"
	fxCloserThanOpenerOnly = "]\n"
)

func writeKeymap(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// tempKeymapPath points $XDG_CONFIG_HOME at a temp dir and returns the
// keymap path under it. The file is NOT created.
func tempKeymapPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := zedKeymapPath()
	if err != nil {
		t.Fatalf("zedKeymapPath: %v", err)
	}
	return path
}

func TestZedPlanCreatesWhenKeymapIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zed", "keymap.json")

	plan, err := planZedKeymap(path, fixedNow)
	if err != nil {
		t.Fatalf("planZedKeymap: %v", err)
	}
	if plan.status != zedCreate {
		t.Fatalf("status = %v, want zedCreate", plan.status)
	}
	if plan.backup != "" {
		t.Errorf("backup = %q, want none: there is no file to back up", plan.backup)
	}
	if string(plan.content) != zedFreshKeymap {
		t.Errorf("content =\n%s\nwant\n%s", plan.content, zedFreshKeymap)
	}
	// The file we author must itself be a keymap Zed can read, and the
	// entry must survive a JSON round trip as ESC CR.
	assertArrayGrewByOne(t, "[]", string(plan.content))

	var arr []struct {
		Context  string              `json:"context"`
		Bindings map[string][]string `json:"bindings"`
	}
	scan, _ := stripJSONC(string(plan.content))
	if err := json.Unmarshal([]byte(scan), &arr); err != nil {
		t.Fatalf("fresh keymap does not parse: %v", err)
	}
	if len(arr) != 1 || arr[0].Context != "Terminal" {
		t.Fatalf("fresh keymap = %+v, want one Terminal entry", arr)
	}
	if got := arr[0].Bindings["shift-enter"]; len(got) != 2 || got[1] != "\x1b\r" {
		t.Errorf("shift-enter sends %q, want [terminal::SendText, ESC CR]", got)
	}
}

func TestZedPlanInsertion(t *testing.T) {
	entry := zedIndent + zedProvenance + "\n" + zedIndent + zedBinding

	tests := []struct {
		name  string
		src   string
		want  string
		valid bool // the result is comment-free-parseable JSON
	}{
		{
			name: "empty array",
			src:  fxEmptyArray,
			want: "[\n" + entry + "\n]\n",
			// The `[` and `]` of "[]" are adjacent, so this also pins the
			// no-preceding-element case: no comma is added.
			valid: true,
		},
		{
			name: "comments and a trailing note are preserved verbatim",
			src:  fxCommented,
			want: `// my zed keymap
[
  // editor keys
  {"context": "Editor", "bindings": {"ctrl-s": "workspace::Save"}},
` + entry + `
  // trailing note, no entry after it
]
`,
			valid: true,
		},
		{
			name: "existing trailing comma is reused, not doubled",
			src:  fxTrailingComma,
			want: `[
  {"context": "Editor", "bindings": {}},
` + entry + `
]
`,
			valid: false, // trailing comma is Zed-legal, not encoding/json-legal
		},
		{
			name:  "single line keeps the closing bracket off our line",
			src:   fxSingleLine,
			want:  `[{"context": "Editor", "bindings": {}},` + "\n" + entry + "\n]",
			valid: true,
		},
		{
			name: "a bracket inside a string is not structure",
			src:  fxBracketInString,
			want: `[
  {"context": "Editor", "bindings": {"ctrl-s": "a ] b"}},
` + entry + `
]
`,
			valid: true,
		},
		{
			name: "shift-enter in another context is not this binding",
			src:  fxOtherContext,
			want: `[
  {"context": "Editor", "bindings": {"shift-enter": "editor::Newline"}},
` + entry + `
]
`,
			valid: true,
		},
		{
			name: "a commented-out binding binds nothing",
			src:  fxShiftEnterInComment,
			want: `[
  // {"context": "Terminal", "bindings": {"shift-enter": "x"}}
  {"context": "Editor", "bindings": {}},
` + entry + `
]
`,
			valid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keymap.json")
			writeKeymap(t, path, tc.src)

			plan, err := planZedKeymap(path, fixedNow)
			if err != nil {
				t.Fatalf("planZedKeymap: %v", err)
			}
			if plan.status != zedInsert {
				t.Fatalf("status = %v, want zedInsert", plan.status)
			}
			if got := string(plan.content); got != tc.want {
				t.Errorf("content =\n%q\nwant\n%q", got, tc.want)
			}
			if plan.backup != path+fixedBackupSuffix {
				t.Errorf("backup = %q, want %q", plan.backup, path+fixedBackupSuffix)
			}

			// Independent of the exact bytes: whatever came out must still
			// be a JSON array with one more element than went in.
			if !tc.valid {
				return
			}
			assertArrayGrewByOne(t, tc.src, string(plan.content))
		})
	}
}

// assertArrayGrewByOne parses both sides (comments blanked) and checks the
// insertion added exactly one element and left the others alone.
func assertArrayGrewByOne(t *testing.T, before, after string) {
	t.Helper()
	parse := func(label, s string) []json.RawMessage {
		t.Helper()
		scan, ok := stripJSONC(s)
		if !ok {
			t.Fatalf("%s: stripJSONC refused its own fixture", label)
		}
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(scan), &arr); err != nil {
			t.Fatalf("%s is not a JSON array after comment stripping: %v\n%s", label, err, scan)
		}
		return arr
	}
	was, is := parse("before", before), parse("after", after)
	if len(is) != len(was)+1 {
		t.Fatalf("array went from %d to %d elements, want +1", len(was), len(is))
	}
	for i := range was {
		if !bytes.Equal(was[i], is[i]) {
			t.Errorf("element %d changed:\n was %s\n  is %s", i, was[i], is[i])
		}
	}
	if !strings.Contains(string(is[len(is)-1]), "terminal::SendText") {
		t.Errorf("last element is not the binding: %s", is[len(is)-1])
	}
}

func TestZedPlanAlreadyConfigured(t *testing.T) {
	for name, src := range map[string]string{
		"bare context":     fxConfigured,
		"compound context": fxConfiguredCompound,
		"our own output":   zedFreshKeymap,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keymap.json")
			writeKeymap(t, path, src)

			plan, err := planZedKeymap(path, fixedNow)
			if err != nil {
				t.Fatalf("planZedKeymap: %v", err)
			}
			if plan.status != zedConfigured {
				t.Errorf("status = %v, want zedConfigured", plan.status)
			}
		})
	}
}

func TestZedRefusesWhatItCannotEditSafely(t *testing.T) {
	tests := []struct{ name, src, wantReason string }{
		{"no closing bracket", fxNoClose, "no matching closing bracket"},
		{"top-level object", fxTopLevelObject, "not a JSON array"},
		{"content after the array", fxTrailingJunk, "content after the top-level array"},
		{"unterminated string", fxUnterminatedString, "unterminated string or block comment"},
		{"unterminated block comment", fxUnterminatedComment, "unterminated string or block comment"},
		{"closer with no opener", fxCloserThanOpenerOnly, "not a JSON array"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := tempKeymapPath(t)
			writeKeymap(t, path, tc.src)

			plan, err := planZedKeymap(path, fixedNow)
			if err != nil {
				t.Fatalf("planZedKeymap: %v", err)
			}
			if plan.status != zedRefuse {
				t.Fatalf("status = %v, want zedRefuse (reason %q)", plan.status, plan.reason)
			}
			if !strings.Contains(plan.reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to mention %q", plan.reason, tc.wantReason)
			}

			// A refusal prints the snippet, asks nothing, and touches
			// nothing — including on the interactive path.
			var out bytes.Buffer
			if err := setupZed(&out, refuseToBeAsked(t)); err != nil {
				t.Fatalf("setupZed: %v", err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != tc.src {
				t.Errorf("keymap changed under a refusal:\n%s", got)
			}
			if b, _ := filepath.Glob(path + ".personant-bak-*"); len(b) != 0 {
				t.Errorf("a refusal left backups behind: %v", b)
			}
			if !strings.Contains(out.String(), zedBinding) {
				t.Errorf("refusal did not print the snippet for manual paste:\n%s", out.String())
			}
		})
	}
}

// refuseToBeAsked is a confirmFunc that fails the test if it is called:
// the paths that use it must never reach a prompt.
func refuseToBeAsked(t *testing.T) confirmFunc {
	t.Helper()
	return func(preamble []string, prompt string) (bool, error) {
		t.Errorf("asked for confirmation when it should not have: %q %v", prompt, preamble)
		return false, nil
	}
}

func TestZedWriteCreateBackupAndIdempotence(t *testing.T) {
	path := tempKeymapPath(t)
	writeKeymap(t, path, fxCommented)

	yes := func(preamble []string, prompt string) (bool, error) { return true, nil }

	var out bytes.Buffer
	if err := setupZed(&out, yes); err != nil {
		t.Fatalf("setupZed: %v", err)
	}

	// The backup holds the ORIGINAL bytes, exactly.
	backups, err := filepath.Glob(path + ".personant-bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v (err %v), want exactly one", backups, err)
	}
	if got, err := os.ReadFile(backups[0]); err != nil || string(got) != fxCommented {
		t.Fatalf("backup is not the original bytes (err %v):\n%s", err, got)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read keymap: %v", err)
	}
	if !strings.Contains(string(written), zedBinding) {
		t.Fatalf("keymap does not contain the binding:\n%s", written)
	}
	if !strings.Contains(out.String(), "Zed reloads keybindings live") {
		t.Errorf("no live-reload reminder after a successful write:\n%s", out.String())
	}

	// Second run: already configured, so nothing moves — not the keymap,
	// and not the backup set either.
	out.Reset()
	if err := setupZed(&out, refuseToBeAsked(t)); err != nil {
		t.Fatalf("setupZed (second run): %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read keymap: %v", err)
	}
	if !bytes.Equal(written, again) {
		t.Errorf("second run changed the file:\n%s", again)
	}
	if b, _ := filepath.Glob(path + ".personant-bak-*"); len(b) != 1 {
		t.Errorf("backups = %v, want still exactly one", b)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("second run did not report already-configured:\n%s", out.String())
	}
}

func TestZedDeclinedWritesNothing(t *testing.T) {
	path := tempKeymapPath(t)
	writeKeymap(t, path, fxCommented)

	no := func(preamble []string, prompt string) (bool, error) { return false, nil }

	var out bytes.Buffer
	if err := setupZed(&out, no); err != nil {
		t.Fatalf("setupZed: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != fxCommented {
		t.Errorf("keymap changed after a decline (err %v):\n%s", err, got)
	}
	if b, _ := filepath.Glob(path + ".personant-bak-*"); len(b) != 0 {
		t.Errorf("a decline left backups behind: %v", b)
	}
	if !strings.Contains(out.String(), "not written") {
		t.Errorf("decline was not reported:\n%s", out.String())
	}
}

// TestZedConfirmationShowsWhatItWillDo pins the three facts the user needs
// in order to answer the question: which file, where the backup goes, and
// the exact entry.
func TestZedConfirmationShowsWhatItWillDo(t *testing.T) {
	path := tempKeymapPath(t)
	writeKeymap(t, path, fxCommented)

	var shown []string
	seen := func(preamble []string, prompt string) (bool, error) {
		shown = preamble
		return false, nil
	}
	if err := setupZed(&bytes.Buffer{}, seen); err != nil {
		t.Fatalf("setupZed: %v", err)
	}

	joined := strings.Join(shown, "\n")
	for _, want := range []string{path, ".personant-bak-", zedBinding} {
		if !strings.Contains(joined, want) {
			t.Errorf("confirmation does not show %q:\n%s", want, joined)
		}
	}
}

func TestTerminalSetupNonInteractiveNeverWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	var out bytes.Buffer
	host := hostTerminal{Kind: kindZed, Name: "Zed", EnvVar: envTermProgram, EnvVal: "zed"}
	if err := terminalSetup(&out, host, nil); err != nil {
		t.Fatalf("terminalSetup: %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("wrote into the config dir off a terminal: %v (err %v)", entries, err)
	}
	if !strings.Contains(out.String(), "not a terminal") {
		t.Errorf("did not say why it stopped:\n%s", out.String())
	}
	if !strings.Contains(out.String(), zedBinding) {
		t.Errorf("did not print the entry for manual use:\n%s", out.String())
	}
}

func TestTerminalSetupReportsEveryDetectedHost(t *testing.T) {
	tests := []struct {
		name string
		host hostTerminal
		want []string
	}{
		{
			"iterm2 needs nothing written",
			hostTerminal{Kind: kindITerm2, Name: "iTerm2", EnvVar: envTermProgram, EnvVal: "iTerm.app"},
			[]string{"detected terminal: iTerm2", "3.5 and newer", "nothing to write"},
		},
		{
			"apple terminal gets the option-as-meta instruction",
			hostTerminal{Kind: kindAppleTerminal, Name: "Terminal.app", EnvVar: envTermProgram, EnvVal: "Apple_Terminal"},
			[]string{"detected terminal: Terminal.app", "Use Option as Meta key"},
		},
		{
			"vscode gets its keybindings.json line",
			hostTerminal{Kind: kindVSCode, Name: "VS Code", EnvVar: envTermProgram, EnvVal: "vscode"},
			[]string{"keybindings.json", "sendSequence"},
		},
		{
			"wezterm gets its lua line",
			hostTerminal{Kind: kindWezTerm, Name: "WezTerm", EnvVar: envTermProgram, EnvVal: "WezTerm"},
			[]string{"wezterm.lua", "SendString"},
		},
		{
			"an unknown terminal gets every recipe",
			hostTerminal{Name: "ghostty", EnvVar: envTermProgram, EnvVal: "ghostty"},
			[]string{"detected terminal: ghostty", "iTerm2", "keymap.json", "keybindings.json", "wezterm.lua", "Use Option as Meta key"},
		},
		{
			"nothing set says so",
			hostTerminal{},
			[]string{"no terminal identified", "$TERM_PROGRAM", "$LC_TERMINAL"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := terminalSetup(&out, tc.host, refuseToBeAsked(t)); err != nil {
				t.Fatalf("terminalSetup: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("report is missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// TestSlashTerminalSetupPiped is the end-to-end half: a real piped session
// reaching the command through slash dispatch writes nothing at all.
func TestSlashTerminalSetupPiped(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv(envTermProgram, "zed")
	t.Setenv(envLCTerminal, "")

	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/terminal-setup\n/quit\n")

	if !strings.Contains(out, "detected terminal: Zed") {
		t.Errorf("detection not reported:\n%s", out)
	}
	if !strings.Contains(out, "not a terminal") {
		t.Errorf("piped session did not decline to write:\n%s", out)
	}
	if entries, err := os.ReadDir(cfg); err != nil || len(entries) != 0 {
		t.Errorf("piped session touched the config dir: %v (err %v)", entries, err)
	}
}
