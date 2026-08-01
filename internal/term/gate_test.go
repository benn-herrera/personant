package term

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Two mechanical gates, both ordinary Go tests so `make test-fe` runs
// them (SOLUTION.md §6, IMPLEMENTATION-PLAN.md §1).
//
//  1. NO DIRECT TERMINAL ACCESS outside this package. Go cannot express
//     capability confinement across a struct field, so the compiler
//     enforces only that term's own wrappers are unexported. liner is the
//     standing proof that a dependency can mutate termios where no
//     compiler rule of ours reaches, which is why this is a source check
//     rather than a design principle.
//
//  2. NO time.Sleep IN TERMINAL TEST FILES. Two of the five §2 bugs were
//     declared fixed on evidence that turned out to be an idle machine.
//     A terminal assertion must synchronize on a happens-before, never on
//     a duration.
//
// # The allowlists, and why they are the important part
//
// At W1 internal/chat still uses liner and still calls termios directly.
// A strict gate would fail on the day it lands, and a gate weakened to
// pass is worse than no gate at all. So each rule carries an EXPLICIT
// allowlist whose every entry names the wave that removes it, plus an
// assertion that the list HAS NOT GROWN. The gate's job in W1 is to stop
// NEW violations while the known ones are burned down on schedule.
//
// The ceiling is `<=`, not `==`, on purpose: burning an entry down must
// not break the build of the commit that burns it, while adding one must.
//
// # Stated limitation
//
// The std-fd rule inspects NON-TEST source only. A test naming os.Stdout
// is building a fixture; the rule protects the product's terminal
// ownership, and extending it to fixtures would trade real signal for
// noise. The two IMPORT rules apply to test files as well, because a test
// that pokes termios directly is exactly the thing that would go
// unnoticed.

type gateRule int

const (
	ruleTermios gateRule = iota // imports golang.org/x/sys/unix
	ruleLiner                   // imports github.com/peterh/liner
	ruleStdFD                   // names os.Stdin / os.Stdout / os.Stderr
	ruleSleep                   // calls time.Sleep in a terminal test file
)

func (r gateRule) String() string {
	switch r {
	case ruleTermios:
		return "imports golang.org/x/sys/unix (termios/ioctl)"
	case ruleLiner:
		return "imports github.com/peterh/liner"
	case ruleStdFD:
		return "names os.Stdin/os.Stdout/os.Stderr"
	default:
		return "calls time.Sleep"
	}
}

// exemption is one known, scheduled violation. note names the wave that
// removes it, or says plainly that nothing will.
type exemption struct {
	path string // repo-relative, exact
	rule gateRule
	note string
}

// terminalAccessAllowlist is the burn-down list for gate 1.
//
// MUST NOT GROW. Adding an entry means a package outside internal/term
// started talking to the terminal on its own, which is the defect class
// this whole design removes.
var terminalAccessAllowlist = []exemption{
	// --- W3: liner leaves, and chat's private termios goes with it ---
	//
	// internal/chat/input.go's liner entry was BURNED DOWN by W2, not
	// weakened: the read path moved inside this package, so the one liner
	// import in the tree is internal/term's own (readline.go) and the gate
	// exempts internal/term by design — it is the package allowed to know.
	// liner itself does not leave until W3; what left is the SECOND owner.
	{"internal/chat/term_unix.go", ruleTermios,
		"W3 — captureTerm/enterCbreak/readStdin; term takes mode ownership and the single reader in W3"},
	{"internal/chat/termios_bsd.go", ruleTermios,
		"W3 — the ioctl constants term_unix.go needs; deleted with it"},
	{"internal/chat/termios_linux.go", ruleTermios,
		"W3 — as termios_bsd.go"},

	// --- W5: the defaulting moves to the composition root ---
	{"internal/chat/chat.go", ruleStdFD,
		"W5 — Options.Std* defaulting; the process fds are named once here and handed straight to term.Open, and cmd/ is where that belongs"},

	// --- permanent, and stated rather than left implicit ---
	{"cmd/main.go", ruleStdFD,
		"permanent — cmd/ is the composition root: naming the process's real fds is precisely its job, and these writes happen before any session terminal exists"},
	{"cmd/models.go", ruleStdFD,
		"permanent — composition root, as cmd/main.go"},
	{"cmd/ping.go", ruleStdFD,
		"permanent — composition root, as cmd/main.go"},
	{"internal/log/log.go", ruleStdFD,
		"permanent — internal/log is the SUBSTRATE's leveled stderr sink, not a terminal client: it serves cmd/ verbs and background subsystems that have no session terminal"},
}

// maxTerminalAccessExemptions is the ratchet. W1 set it at 9; W2 burned
// chat's liner entry down and lowered it to 8. Lower it as waves burn
// further entries down.
const maxTerminalAccessExemptions = 8

// sleepBanAllowlist is the burn-down list for gate 2.
var sleepBanAllowlist = []exemption{
	{"internal/chat/escwatch_test.go", ruleSleep,
		"W3 — a busy-wait guard in a scripted reader fake; escwatch.go and its test are deleted when the decoder lands"},
	{"internal/chat/shellescape_test.go", ruleSleep,
		"permanent — child-process reaping, not terminal timing: the sleep is inside a fake command, and the assertion it feeds is about exit codes"},
}

const maxSleepExemptions = 2

// TestGate_NoDirectTerminalAccess is gate 1.
func TestGate_NoDirectTerminalAccess(t *testing.T) {
	assertRatchet(t, "terminalAccessAllowlist", len(terminalAccessAllowlist), maxTerminalAccessExemptions)

	found := map[exemptionKey]bool{}
	forEachGoFile(t, func(rel string, f *ast.File, includeTests bool) {
		if strings.HasPrefix(rel, "internal/term/") {
			return // the arbiter is the one package allowed to know
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			switch {
			case p == "golang.org/x/sys/unix":
				found[exemptionKey{rel, ruleTermios}] = true
			case strings.HasPrefix(p, "github.com/peterh/liner"):
				found[exemptionKey{rel, ruleLiner}] = true
			}
		}
		if !includeTests && namesStdFD(f) {
			found[exemptionKey{rel, ruleStdFD}] = true
		}
	})
	checkAgainst(t, "no-direct-terminal-access", found, terminalAccessAllowlist,
		"internal/term owns the terminal: the fds, the mode, the reader and every emitted byte.\n"+
			"Route the effect through term's API instead.")
}

// TestGate_NoSleepInTerminalTests is gate 2. Jurisdiction is every test
// file in the two packages that touch the terminal.
func TestGate_NoSleepInTerminalTests(t *testing.T) {
	assertRatchet(t, "sleepBanAllowlist", len(sleepBanAllowlist), maxSleepExemptions)

	jurisdiction := []string{"internal/term", "internal/chat"}
	found := map[exemptionKey]bool{}
	forEachGoFile(t, func(rel string, f *ast.File, includeTests bool) {
		if !includeTests {
			return
		}
		if !underAny(rel, jurisdiction) {
			return
		}
		if callsSleep(f) {
			found[exemptionKey{rel, ruleSleep}] = true
		}
	})
	checkAgainst(t, "no-sleep-in-terminal-tests", found, sleepBanAllowlist,
		"A terminal assertion must synchronize on a happens-before — a channel send the code under\n"+
			"test must consume, or a join — never on a duration. Two of the five §2 bugs were declared\n"+
			"fixed on evidence that was really an idle machine.")
}

// --- gate machinery ---------------------------------------------------

type exemptionKey struct {
	path string
	rule gateRule
}

// assertRatchet is the has-not-grown half. Without it an allowlist is a
// place to put anything, and the gate reports on a set the offender
// controls.
func assertRatchet(t *testing.T, name string, got, max int) {
	t.Helper()
	if got > max {
		t.Fatalf("%s has GROWN to %d entries (ceiling %d).\n"+
			"An allowlist that grows is a gate that has been switched off. If the new entry\n"+
			"is genuinely unavoidable, raise the ceiling deliberately and say why in the entry.",
			name, got, max)
	}
}

// checkAgainst reports violations the allowlist does not cover. A STALE
// entry — one whose violation is gone — is reported too, at info level:
// leaving it behind hides the fact that a wave already burned it down.
func checkAgainst(t *testing.T, gate string, found map[exemptionKey]bool, allow []exemption, remedy string) {
	t.Helper()
	allowed := map[exemptionKey]string{}
	for _, e := range allow {
		allowed[exemptionKey{e.path, e.rule}] = e.note
	}

	var unlisted []string
	for k := range found {
		if _, ok := allowed[k]; !ok {
			unlisted = append(unlisted, k.path+" — "+k.rule.String())
		}
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Errorf("%s: %d file(s) violate the gate with no allowlist entry:\n  %s\n\n%s\n"+
			"If it is genuinely unavoidable for now, add an allowlist entry naming the WAVE that\n"+
			"removes it and raise the ceiling deliberately.",
			gate, len(unlisted), strings.Join(unlisted, "\n  "), remedy)
	}

	var stale []string
	for k := range allowed {
		if !found[k] {
			stale = append(stale, k.path+" — "+k.rule.String())
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Logf("%s: %d allowlist entr%s no longer needed — the wave that removes them has landed; "+
			"drop them and lower the ceiling:\n  %s",
			gate, len(stale), map[bool]string{true: "y is", false: "ies are"}[len(stale) == 1],
			strings.Join(stale, "\n  "))
	}
}

// forEachGoFile walks the Go source roots from the repo root and parses
// every file. test/ is deliberately not walked: it holds no Go code, and
// test/api_keys is unreadable by design.
func forEachGoFile(t *testing.T, visit func(rel string, f *ast.File, isTest bool)) {
	t.Helper()
	const repoRoot = "../.."
	fset := token.NewFileSet()
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			rel, rerr := filepath.Rel(repoRoot, p)
			if rerr != nil {
				return rerr
			}
			visit(filepath.ToSlash(rel), f, strings.HasSuffix(p, "_test.go"))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}

// namesStdFD reports whether the file names os.Stdin, os.Stdout or
// os.Stderr. It is an AST check, not a grep, so a doc comment mentioning
// os.Stdin is not a violation — and the comment on chat.Options says
// exactly that.
func namesStdFD(f *ast.File) bool {
	if !imports(f, "os") {
		return false
	}
	hit := false
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" {
			return true
		}
		switch sel.Sel.Name {
		case "Stdin", "Stdout", "Stderr":
			hit = true
		}
		return true
	})
	return hit
}

func callsSleep(f *ast.File) bool {
	if !imports(f, "time") {
		return false
	}
	hit := false
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "time" && sel.Sel.Name == "Sleep" {
			hit = true
		}
		return true
	})
	return hit
}

func imports(f *ast.File, path string) bool {
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && p == path {
			return true
		}
	}
	return false
}

func underAny(rel string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(rel, r+"/") {
			return true
		}
	}
	return false
}
