package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hasGit skips the test if `git` is not available on the host.
func hasGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not in PATH: %v", err)
	}
}

// initIntoTempDir runs Init against a fresh PersonantPaths under t.TempDir().
// Returns the paths struct so the test can assert on it.
func initIntoTempDir(t *testing.T) PersonantPaths {
	t.Helper()
	home := t.TempDir()
	paths := PathsForHome(home)
	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return paths
}

func sha256OfFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestInitFreshHome(t *testing.T) {
	hasGit(t)
	paths := initIntoTempDir(t)

	wantDirs := []string{
		paths.Home,
		paths.ThreadsDir,
		paths.ProjectsDir,
		paths.DirectivesDir,
		paths.LogsDir,
		paths.LogsArchive,
		paths.TmpDir,
		filepath.Join(paths.Home, ".git"),
	}
	for _, d := range wantDirs {
		stat, err := os.Stat(d)
		if err != nil {
			t.Errorf("missing dir %s: %v", d, err)
			continue
		}
		if !stat.IsDir() {
			t.Errorf("not a dir: %s", d)
		}
	}

	wantFiles := []string{
		paths.Spine,
		paths.Symbols,
		filepath.Join(paths.DirectivesDir, "defaults.md"),
		filepath.Join(paths.DirectivesDir, "user.md"),
		paths.Providers,
		paths.Readme,
		paths.Gitignore,
	}
	for _, f := range wantFiles {
		stat, err := os.Stat(f)
		if err != nil {
			t.Errorf("missing file %s: %v", f, err)
			continue
		}
		if stat.IsDir() {
			t.Errorf("unexpected dir at %s", f)
		}
	}

	// HEAD must resolve — confirms the initial commit exists.
	cmd := exec.Command("git", "-C", paths.Home, "rev-parse", "--verify", "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git rev-parse HEAD: %v: %s", err, out)
	}

	// Spot-check .gitignore content.
	gi, err := os.ReadFile(paths.Gitignore)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	for _, want := range []string{"tmp/", "providers.toml"} {
		if !strings.Contains(string(gi), want) {
			t.Errorf("gitignore missing %q; content:\n%s", want, string(gi))
		}
	}
}

func TestInitIdempotent(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	// Snapshot every file under home except the .git tree (git's internal
	// state files have legitimately mutable bookkeeping that idempotency
	// of the scaffold itself doesn't speak to).
	snapshot := map[string]string{}
	if err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		snapshot[path] = sha256OfFile(t, path)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("second Init: %v", err)
	}

	for path, sum := range snapshot {
		got := sha256OfFile(t, path)
		if got != sum {
			t.Errorf("file mutated by second Init: %s", path)
		}
	}

	// All snapshotted paths must still exist (none deleted).
	for path := range snapshot {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file disappeared: %s: %v", path, err)
		}
	}
}

func TestInitDoesNotOverwriteUserContent(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	// Pre-create a customized defaults.md before Init runs.
	if err := os.MkdirAll(paths.DirectivesDir, 0o755); err != nil {
		t.Fatalf("mkdir directives: %v", err)
	}
	custom := []byte("---\nscope: defaults\n# user-customized; init must not clobber\n---\n\n# custom\n")
	defaultsPath := filepath.Join(paths.DirectivesDir, "defaults.md")
	if err := os.WriteFile(defaultsPath, custom, 0o644); err != nil {
		t.Fatalf("write custom defaults: %v", err)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := os.ReadFile(defaultsPath)
	if err != nil {
		t.Fatalf("read defaults: %v", err)
	}
	if string(got) != string(custom) {
		t.Errorf("defaults.md was overwritten\nwant: %q\n got: %q", custom, got)
	}
}

func TestInitGitRepoExists(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	// Pre-init git here so Init's git-init step is a no-op.
	cmd := exec.Command("git", "-C", home, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pre-git init: %v: %s", err, out)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init over existing .git: %v", err)
	}

	// HEAD should resolve — Init makes the initial commit when the repo has
	// none yet, even if .git/ predated the call.
	headCmd := exec.Command("git", "-C", home, "rev-parse", "--verify", "HEAD")
	if out, err := headCmd.CombinedOutput(); err != nil {
		t.Fatalf("git rev-parse HEAD: %v: %s", err, out)
	}
}

func TestInitDefaultsParameters(t *testing.T) {
	hasGit(t)
	paths := initIntoTempDir(t)

	defaultsPath := filepath.Join(paths.DirectivesDir, "defaults.md")
	data, err := os.ReadFile(defaultsPath)
	if err != nil {
		t.Fatalf("read defaults: %v", err)
	}
	body := string(data)

	wantParams := []string{
		"engagement.decay-turns: 8",
		"engagement.decay-time: 7d",
		"recall.symbolic-threshold: 0.4",
		"recall.cross-project-threshold: 0.5",
		"layer.b-top-k: 3",
		"anchors.cap-per-thread: 6",
		"history.cap-per-thread: 40",
		"spine.entry-max-chars: 200",
		"cross-project.digest-per-project-bytes: 150",
		"dissect.pressure-threshold: 0.90",
	}
	for _, p := range wantParams {
		if !strings.Contains(body, p) {
			t.Errorf("defaults.md missing parameter %q", p)
		}
	}
}

func TestInitLoggerEmitsAndQuietSuppresses(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	var lines []string
	logger := func(format string, args ...any) {
		// Mirror the cmd-level logger's format ergonomics, not the byte
		// stream itself — the test cares about whether the callback fires
		// and what it sees.
		lines = append(lines, format)
	}
	if err := Init(paths, InitOptions{Logger: logger}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(lines) == 0 {
		t.Errorf("logger received no lines on a fresh init")
	}

	// Quiet on a second run: even though "init: ok" is the only thing left
	// to emit, Quiet must silence it.
	lines = nil
	if err := Init(paths, InitOptions{Quiet: true, Logger: logger}); err != nil {
		t.Fatalf("Init quiet: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("logger fired with Quiet=true: %v", lines)
	}
}

func TestInitEmptyHomeRejected(t *testing.T) {
	err := Init(PersonantPaths{}, InitOptions{Quiet: true})
	if err == nil {
		t.Fatal("expected error for empty Home, got nil")
	}
}

// Sanity: the canonical empty files must in fact be empty.
func TestInitCanonicalEmptyFilesAreEmpty(t *testing.T) {
	hasGit(t)
	paths := initIntoTempDir(t)

	for _, p := range []string{paths.Spine, paths.Symbols} {
		stat, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if stat.Size() != 0 {
			t.Errorf("expected %s empty, got %d bytes", p, stat.Size())
		}
	}
}

// TestInitInstallsPreCommitHook: a fresh init writes the managed
// pre-commit hook with mode 0755 and the expected marker line.
func TestInitInstallsPreCommitHook(t *testing.T) {
	hasGit(t)
	paths := initIntoTempDir(t)

	hookPath := filepath.Join(paths.Home, ".git", "hooks", "pre-commit")
	stat, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("stat pre-commit hook: %v", err)
	}
	// Owner-executable bit is the load-bearing assertion (git refuses to
	// run a non-executable hook). Permission masking by umask can drop
	// group/other bits; the install path forces 0o755 with Chmod, so
	// require the full mode here.
	if mode := stat.Mode().Perm(); mode != 0o755 {
		t.Errorf("pre-commit hook mode = %o, want 0755", mode)
	}

	body, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read pre-commit hook: %v", err)
	}
	if !strings.Contains(string(body), preCommitHookMarker) {
		t.Errorf("pre-commit hook missing marker %q\ncontent:\n%s", preCommitHookMarker, body)
	}
	// Sanity: the hook should invoke `personant index check --home`, since
	// that is the behavior the spec promises.
	if !strings.Contains(string(body), "personant index check --home") {
		t.Errorf("pre-commit hook missing `personant index check --home` invocation\ncontent:\n%s", body)
	}
}

// TestInitOverwritesManagedHook: a hook carrying the personant marker is
// rewritten on subsequent init, restoring the canonical content.
func TestInitOverwritesManagedHook(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	hookPath := filepath.Join(paths.Home, ".git", "hooks", "pre-commit")
	// Write a tampered version that still carries the marker (simulating
	// "managed but drifted" state, e.g. an older personant version's hook).
	tampered := "#!/bin/sh\n" + preCommitHookMarker + " (tampered version)\nexit 0\n"
	if err := os.WriteFile(hookPath, []byte(tampered), 0o755); err != nil {
		t.Fatalf("write tampered hook: %v", err)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("second Init: %v", err)
	}

	got, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if string(got) == tampered {
		t.Fatalf("managed hook was not rewritten\ncontent:\n%s", got)
	}
	if !strings.Contains(string(got), "personant index check --home") {
		t.Errorf("rewritten hook missing canonical body\ncontent:\n%s", got)
	}
}

// TestInitPreservesUnmanagedHook: a hook without the marker is left
// untouched and a warning is logged.
func TestInitPreservesUnmanagedHook(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	// We need the hooks directory to exist before we drop the user's hook
	// in. Run a no-op git init by hand to create the .git tree, then
	// pre-place an unmanaged hook before personant Init runs.
	cmd := exec.Command("git", "-C", home, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pre-git init: %v: %s", err, out)
	}
	hooksDir := filepath.Join(home, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	hookPath := filepath.Join(hooksDir, "pre-commit")
	userHook := []byte("#!/bin/sh\n# my own hook — no personant marker here\necho hi\nexit 0\n")
	if err := os.WriteFile(hookPath, userHook, 0o755); err != nil {
		t.Fatalf("write user hook: %v", err)
	}

	var logLines []string
	logger := func(format string, args ...any) {
		logLines = append(logLines, format)
	}
	if err := Init(paths, InitOptions{Logger: logger}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if string(got) != string(userHook) {
		t.Errorf("unmanaged hook was clobbered\nwant: %q\n got: %q", userHook, got)
	}

	// A warning should have been logged. The exact wording is checked
	// loosely; we want to fail if the warning silently disappeared.
	var sawWarn bool
	for _, l := range logLines {
		if strings.Contains(l, "not personant-managed") {
			sawWarn = true
			break
		}
	}
	if !sawWarn {
		t.Errorf("expected warning about unmanaged hook; got log:\n%s", strings.Join(logLines, "\n"))
	}
}

// TestInitHookInstallIdempotent: running Init twice over a freshly
// scaffolded home leaves the hook bytes byte-identical.
func TestInitHookInstallIdempotent(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	hookPath := filepath.Join(paths.Home, ".git", "hooks", "pre-commit")
	first := sha256OfFile(t, hookPath)

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	second := sha256OfFile(t, hookPath)

	if first != second {
		t.Errorf("hook content changed across no-op re-init: %s vs %s", first, second)
	}
}

// Confirm that touchIfMissing's O_EXCL semantics survive an existing
// non-empty file (idempotency for spine.jsonl in particular: re-running
// init must not blank a populated spine).
func TestInitDoesNotBlankPopulatedCanonicalFiles(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)
	// First scaffold.
	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	// Simulate runtime having written a real spine record.
	payload := []byte(`{"id":"thr_1"}` + "\n")
	if err := os.WriteFile(paths.Spine, payload, 0o644); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	// Re-init.
	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	got, err := os.ReadFile(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("spine.jsonl was overwritten by re-init\nwant: %q\n got: %q", payload, got)
	}
}

