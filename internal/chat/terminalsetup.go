package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"personant/internal/term"
)

// /terminal-setup — configure the hosting terminal for Shift+Enter
// multi-line input (SPEC §4.3.1, user ruling 2026-08-05).
//
// Legacy terminal input encodes Enter and Shift+Enter as the same byte, so
// Shift+Enter is a line break only where the terminal has been told to send
// something a decoder can tell apart. §4.3.1 documents the per-terminal
// recipe; this command detects the host and either PERFORMS the one recipe
// that is a file write (Zed) or PRINTS the one that is not.
//
// # Writing outside the home is the exception, and is treated as one
//
// Every other write personant makes lands under $PERSONANT_HOME. This one
// lands in the user's editor config, which is new territory, so the
// AGENTS.md "ack at high-leverage moments only" rule does not apply to it:
// the write is confirmed EVERY time, the previous bytes are backed up
// first, and off a terminal — where there is nobody to ask — it never
// happens at all. A file whose structure defeats byte-safe insertion is
// REFUSED with the snippet printed for manual paste; corrupting a
// hand-maintained config is worse than not helping.

const (
	// envTermProgram is the primary terminal identity. envLCTerminal is
	// the fallback: ssh forwards LC_* by default and TERM_PROGRAM is not,
	// so a remote session is often identifiable only through it.
	envTermProgram = "TERM_PROGRAM"
	envLCTerminal  = "LC_TERMINAL"
)

// terminalKind is the host terminal at the resolution this command acts
// at: one kind per distinct recipe, not per emulator.
type terminalKind int

const (
	kindUnknown terminalKind = iota
	kindITerm2
	kindZed
	kindVSCode
	kindAppleTerminal
	kindWezTerm
)

// terminalIDs maps the lowercased env value to a kind. Both spellings of
// iTerm2 are here on purpose: $TERM_PROGRAM says "iTerm.app" and
// $LC_TERMINAL says "iTerm2", and they name the same terminal.
var terminalIDs = map[string]terminalKind{
	"iterm.app":      kindITerm2,
	"iterm2":         kindITerm2,
	"zed":            kindZed,
	"vscode":         kindVSCode,
	"apple_terminal": kindAppleTerminal,
	"wezterm":        kindWezTerm,
}

var terminalNames = map[terminalKind]string{
	kindITerm2:        "iTerm2",
	kindZed:           "Zed",
	kindVSCode:        "VS Code",
	kindAppleTerminal: "Terminal.app",
	kindWezTerm:       "WezTerm",
}

// hostTerminal is what the environment says we are running under.
type hostTerminal struct {
	Kind terminalKind

	// Name is the display name for a known kind, or the raw env value for
	// an unrecognized one. Empty when nothing identified the terminal.
	Name string

	// EnvVar / EnvVal are what the detection actually read, reported so
	// the user can see WHY personant thinks it is where it is.
	EnvVar string
	EnvVal string
}

// detectTerminal resolves the host from the environment. $TERM_PROGRAM is
// primary and $LC_TERMINAL is the fallback, but an UNRECOGNIZED
// $TERM_PROGRAM does not stop the fallback from being consulted: under ssh
// the local value may have been inherited by a remote shell that is not the
// terminal at all, while LC_TERMINAL is forwarded by the terminal itself.
// The first recognized value wins; failing that, the first value of any
// kind is reported as an unknown terminal.
func detectTerminal() hostTerminal {
	var unknown hostTerminal
	for _, name := range []string{envTermProgram, envLCTerminal} {
		val := strings.TrimSpace(os.Getenv(name))
		if val == "" {
			continue
		}
		if kind, ok := terminalIDs[strings.ToLower(val)]; ok {
			return hostTerminal{Kind: kind, Name: terminalNames[kind], EnvVar: name, EnvVal: val}
		}
		if unknown.EnvVar == "" {
			unknown = hostTerminal{Name: val, EnvVar: name, EnvVal: val}
		}
	}
	return unknown
}

// zedBinding is the entry installed into Zed's keymap.json. The text sent
// is ESC CR — the universal Alt+Enter form, which rides the decoder's Meta
// path and therefore works on every Zed version, rather than the CSI-u form
// which would depend on one.
const zedBinding = `{"context": "Terminal", "bindings": {"shift-enter": ["terminal::SendText", "\u001b\r"]}}`

// zedProvenance marks the entry as personant's, so a user reading the file
// six months from now knows what put it there and can delete it.
const zedProvenance = `// added by personant /terminal-setup: Shift+Enter inserts a line break (SPEC §4.3.1)`

// zedFreshKeymap is the whole file written when the user has none.
const zedFreshKeymap = zedProvenance + "\n[\n  " + zedBinding + "\n]\n"

const zedIndent = "  "

const recipeITerm2 = `iTerm2 3.5 and newer sends Shift+Enter as CSI-u (ESC[13;2u) with no
configuration, and personant decodes that — there is nothing to write.
If Shift+Enter does not insert a line break, update iTerm2, or map it by
hand: Settings > Profiles > Keys > Key Mappings > +, press Shift+Enter,
action "Send Escape Sequence", escape [13;2u.`

const recipeZed = `Zed — in ~/.config/zed/keymap.json:
  ` + zedBinding + `
Run /terminal-setup from inside Zed's terminal and it installs that for
you, with a backup and a confirmation.`

const recipeVSCode = `VS Code — in keybindings.json (Preferences > Keyboard Shortcuts > the
"Open Keyboard Shortcuts (JSON)" icon):
  {"key": "shift+enter", "command": "workbench.action.terminal.sendSequence",
   "args": {"text": "\u001b\r"}, "when": "terminalFocus"}`

const recipeWezTerm = `WezTerm — in ~/.wezterm.lua, inside your keys table:
  {key="Enter", mods="SHIFT", action=wezterm.action.SendString("\x1b\r")}`

const recipeAppleTerminal = `Terminal.app cannot send a Shift+Enter that anything can tell apart from
Enter. Turn on Settings > Profiles > Keyboard > "Use Option as Meta key";
Option+Enter then inserts a line break. Nothing is written.`

const recipeUniversal = `Alt/Option+Enter already inserts a line break, unconfigured, everywhere.
Shift+Enter additionally needs the terminal to send something distinct
from a bare Enter — which is all these recipes do.`

var terminalRecipes = map[terminalKind]string{
	kindITerm2:        recipeITerm2,
	kindZed:           recipeZed,
	kindVSCode:        recipeVSCode,
	kindAppleTerminal: recipeAppleTerminal,
	kindWezTerm:       recipeWezTerm,
}

// recipeOrder is the print order for the unknown-terminal case: map
// iteration order is not one.
var recipeOrder = []terminalKind{kindITerm2, kindZed, kindVSCode, kindWezTerm, kindAppleTerminal}

// confirmFunc asks the user to approve a write and reports whether they
// did. A NIL confirmFunc means there is nobody to ask — a piped session,
// the harness, a test — and is the whole non-interactive rule: no prompt,
// and therefore no write.
type confirmFunc func(preamble []string, prompt string) (bool, error)

// cmdTerminalSetup implements /terminal-setup (§4.2). It is the wiring
// only: the terminal supplies the confirmation and the writer, and
// [terminalSetup] holds the behavior.
func cmdTerminalSetup(ctx context.Context, tm *term.Terminal) error {
	var confirm confirmFunc
	if tm.Interactive() {
		confirm = func(preamble []string, prompt string) (bool, error) {
			answer, err := tm.ReadLine(ctx, term.ActivityAsk, term.Question{
				Preamble: preamble,
				Prompt:   prompt,
				Keys:     "yn",
			})
			if term.IsEndOrAbort(err) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return strings.EqualFold(strings.TrimSpace(answer.Text), "y"), nil
		}
	}
	return terminalSetup(tm.Out(), detectTerminal(), confirm)
}

func terminalSetup(out io.Writer, host hostTerminal, confirm confirmFunc) error {
	if host.EnvVar == "" {
		fmt.Fprintf(out, "no terminal identified ($%s and $%s are both unset)\n",
			envTermProgram, envLCTerminal)
	} else {
		fmt.Fprintf(out, "detected terminal: %s ($%s=%s)\n", host.Name, host.EnvVar, host.EnvVal)
	}
	fmt.Fprintln(out)

	switch host.Kind {
	case kindZed:
		return setupZed(out, confirm)
	case kindUnknown:
		fmt.Fprintln(out, recipeUniversal)
		for _, kind := range recipeOrder {
			fmt.Fprintln(out)
			fmt.Fprintln(out, terminalRecipes[kind])
		}
		return nil
	default:
		fmt.Fprintln(out, terminalRecipes[host.Kind])
		return nil
	}
}

// zedStatus is what planZedKeymap decided.
type zedStatus int

const (
	zedCreate     zedStatus = iota // no keymap (or an empty one): write a whole file
	zedInsert                      // insert into the existing top-level array
	zedConfigured                  // a Terminal-context shift-enter binding is already there
	zedRefuse                      // the file cannot be edited byte-safely
)

// zedPlan is the decided write, computed before anything is asked and
// before anything is written, so the confirmation can show exactly what
// will happen and the refusal can happen without side effects.
type zedPlan struct {
	status  zedStatus
	path    string
	backup  string      // "" when there is no existing file to back up
	orig    []byte      // the bytes the backup preserves
	content []byte      // the bytes to write
	mode    fs.FileMode // the existing file's mode, or 0o644
	reason  string      // why, for zedRefuse
}

func (p zedPlan) lines() []string {
	l := make([]string, 0, 3)
	if p.status == zedCreate && p.backup == "" {
		l = append(l, "keymap: "+p.path+"  (does not exist — will be created)")
	} else {
		l = append(l, "keymap: "+p.path)
	}
	if p.backup != "" {
		l = append(l, "backup: "+p.backup)
	}
	return append(l, "entry:  "+zedBinding)
}

// zedKeymapPath resolves Zed's keymap. $XDG_CONFIG_HOME is honored where
// set — Zed honors it on Linux, and setting it at all is a deliberate act —
// and the resolved path is printed before any write either way, so a
// disagreement is visible rather than silent.
func zedKeymapPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "zed", "keymap.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "zed", "keymap.json"), nil
}

func setupZed(out io.Writer, confirm confirmFunc) error {
	path, err := zedKeymapPath()
	if err != nil {
		return err
	}
	plan, err := planZedKeymap(path, time.Now())
	if err != nil {
		return err
	}

	switch plan.status {
	case zedConfigured:
		fmt.Fprintf(out, "%s already binds shift-enter in a Terminal context.\n", plan.path)
		fmt.Fprintln(out, "nothing to do — no file was touched.")
		return nil
	case zedRefuse:
		fmt.Fprintf(out, "refusing to edit %s: %s.\n", plan.path, plan.reason)
		fmt.Fprintln(out, "add this to the top-level array by hand:")
		fmt.Fprintln(out, zedIndent+zedBinding)
		return nil
	}

	if confirm == nil {
		fmt.Fprintln(out, "not a terminal — nothing will be written. the change this would make:")
		for _, l := range plan.lines() {
			fmt.Fprintln(out, zedIndent+l)
		}
		return nil
	}

	ok, err := confirm(plan.lines(), "write it? [y]es / [n]o: ")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "not written.")
		return nil
	}
	if err := writeZedKeymap(plan); err != nil {
		return err
	}

	fmt.Fprintf(out, "wrote %s\n", plan.path)
	if plan.backup != "" {
		fmt.Fprintf(out, "backup: %s\n", plan.backup)
	}
	fmt.Fprintln(out, "Zed reloads keybindings live, so Shift+Enter should insert a line")
	fmt.Fprintln(out, "break in the NEXT input you type. If it does not, restart Zed.")
	return nil
}

// planZedKeymap decides what to do with the keymap at path without
// touching it.
//
// # The safety conditions, stated
//
// The file is JSONC (Zed permits comments) and hand-maintained, so it is
// never parsed and re-serialized: every byte outside the insertion point
// survives verbatim. Comments are blanked to spaces in a SCAN COPY that
// preserves offsets, and all structural decisions are made against that
// copy, so a `]` or a `"shift-enter"` inside a comment or a string cannot
// be mistaken for structure.
//
// Insertion happens only when all of these hold:
//
//   - every string and block comment in the file is terminated;
//   - the first non-blank byte is `[`;
//   - that `[` has a matching close, and it is a `]`;
//   - nothing but whitespace follows that `]`.
//
// The entry is inserted immediately after the last element (or after the
// `[` of an empty array, or after an existing trailing comma), with a comma
// added only when one is needed. Anything else — a top-level object, an
// unterminated string, unbalanced brackets, trailing junk — is REFUSED.
func planZedKeymap(path string, now time.Time) (zedPlan, error) {
	plan := zedPlan{path: path, mode: 0o644}

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		plan.status = zedCreate
		plan.content = []byte(zedFreshKeymap)
		return plan, nil
	}
	if err != nil {
		return zedPlan{}, fmt.Errorf("read %s: %w", path, err)
	}
	if info, err := os.Stat(path); err == nil {
		plan.mode = info.Mode().Perm()
	}
	plan.orig = raw
	plan.backup = fmt.Sprintf("%s.personant-bak-%s", path, now.Format("20060102-150405"))

	src := string(raw)
	scan, ok := stripJSONC(src)
	if !ok {
		plan.status = zedRefuse
		plan.reason = "it has an unterminated string or block comment"
		return plan, nil
	}
	if strings.TrimSpace(scan) == "" {
		// Nothing to preserve and nothing to corrupt, but the file exists,
		// so it is still backed up before being replaced.
		plan.status = zedCreate
		plan.content = []byte(zedFreshKeymap)
		return plan, nil
	}
	if zedShiftEnterConfigured(scan) {
		plan.status = zedConfigured
		return plan, nil
	}

	at, comma, newline, reason := zedInsertPoint(scan)
	if reason != "" {
		plan.status = zedRefuse
		plan.reason = reason
		return plan, nil
	}

	insert := "\n" + zedIndent + zedProvenance + "\n" + zedIndent + zedBinding
	if comma {
		insert = "," + insert
	}
	if newline {
		insert += "\n"
	}
	plan.status = zedInsert
	plan.content = []byte(src[:at] + insert + src[at:])
	return plan, nil
}

// zedInsertPoint returns the offset the entry goes at, whether a comma must
// precede it and whether a newline must follow it, or a non-empty reason
// why none of them can be determined safely. scan must be the
// comment-blanked copy.
func zedInsertPoint(scan string) (at int, comma, newline bool, reason string) {
	open := indexNonSpace(scan, 0)
	if open < 0 || scan[open] != '[' {
		return 0, false, false, "its top-level value is not a JSON array"
	}
	closeAt, ok := matchArrayClose(scan, open)
	if !ok {
		return 0, false, false, "its top-level array has no matching closing bracket"
	}
	if indexNonSpace(scan, closeAt+1) >= 0 {
		return 0, false, false, "it has content after the top-level array"
	}
	last := lastNonSpaceBefore(scan, closeAt)
	at = last + 1
	// An empty array or an existing trailing comma both need no comma of
	// ours; anything else is a preceding element. A newline is needed only
	// where the closing bracket would otherwise land on our entry's line.
	return at, scan[last] != '[' && scan[last] != ',', !strings.Contains(scan[at:closeAt], "\n"), ""
}

// matchArrayClose returns the index of the `]` matching the `[` at open.
func matchArrayClose(scan string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(scan); i++ {
		switch scan[i] {
		case '"':
			end, ok := skipString(scan, i)
			if !ok {
				return 0, false
			}
			i = end
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i, scan[i] == ']'
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

// zedShiftEnterConfigured reports whether the keymap already binds
// shift-enter in a Terminal context. A shift-enter binding in some OTHER
// context (the editor, the project panel) is deliberately not a match: it
// does not make Shift+Enter work in the terminal, which is the thing being
// configured.
func zedShiftEnterConfigured(scan string) bool {
	const key = `"shift-enter"`
	for i := 0; ; {
		k := strings.Index(scan[i:], key)
		if k < 0 {
			return false
		}
		k += i
		if ctx, ok := precedingContext(scan, k); ok && strings.Contains(ctx, "Terminal") {
			return true
		}
		i = k + len(key)
	}
}

// precedingContext reads the value of the nearest `"context"` key before
// offset before. Nearest-preceding is the right relation because a Zed
// keymap is an array of {context, bindings} objects and the context is
// written first by every convention and by Zed's own documentation; a
// binding whose context follows it reads as unconfigured, which errs
// toward inserting a duplicate rather than toward silently doing nothing.
func precedingContext(scan string, before int) (string, bool) {
	const key = `"context"`
	j := strings.LastIndex(scan[:before], key)
	if j < 0 {
		return "", false
	}
	j = indexNonSpace(scan, j+len(key))
	if j < 0 || scan[j] != ':' {
		return "", false
	}
	j = indexNonSpace(scan, j+1)
	if j < 0 || scan[j] != '"' {
		return "", false
	}
	end, ok := skipString(scan, j)
	if !ok {
		return "", false
	}
	return scan[j+1 : end], true
}

// stripJSONC returns a copy of src with every comment blanked to spaces —
// same length, same offsets, newlines kept — so structural scans can be
// run against it and the results applied to the original bytes. Reports
// false for an unterminated string or block comment, which is the one
// condition under which the copy would be meaningless.
func stripJSONC(src string) (string, bool) {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '"':
			end, ok := skipString(src, i)
			if !ok {
				return "", false
			}
			i = end
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '/':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			rel := strings.Index(src[i+2:], "*/")
			if rel < 0 {
				return "", false
			}
			end := i + 2 + rel + 2
			for ; i < end; i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			i--
		}
	}
	return string(b), true
}

// skipString returns the index of the quote closing the string that opens
// at i.
func skipString(s string, i int) (int, bool) {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '"':
			return j, true
		}
	}
	return 0, false
}

func indexNonSpace(s string, from int) int {
	for i := from; i < len(s); i++ {
		if !isSpace(s[i]) {
			return i
		}
	}
	return -1
}

func lastNonSpaceBefore(s string, before int) int {
	for i := before - 1; i >= 0; i-- {
		if !isSpace(s[i]) {
			return i
		}
	}
	return -1
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// writeZedKeymap performs the confirmed plan: backup first, then the
// keymap itself.
func writeZedKeymap(plan zedPlan) error {
	if err := os.MkdirAll(filepath.Dir(plan.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(plan.path), err)
	}
	if plan.backup != "" {
		if err := os.WriteFile(plan.backup, plan.orig, plan.mode); err != nil {
			return fmt.Errorf("write backup %s: %w", plan.backup, err)
		}
	}
	if err := replaceFile(plan.path, plan.content, plan.mode); err != nil {
		return fmt.Errorf("write %s: %w", plan.path, err)
	}
	return nil
}

// replaceFile writes content to path through a temp file in the same
// directory and a rename. Zed watches this file and reloads it live, so a
// truncate-then-write would give its watcher a window in which the keymap
// is half a file.
func replaceFile(path string, content []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".personant-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename has happened

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}
