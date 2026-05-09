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

// TestInitPreservesAccruedState: files that accrue canonical state
// (user.md, providers.toml) survive re-init with their custom contents
// intact. Re-init cannot reconstruct ack-prompt scope grants, decline
// categorizations, or hand-entered API keys; preserving these is the
// point.
func TestInitPreservesAccruedState(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	if err := os.MkdirAll(paths.DirectivesDir, 0o755); err != nil {
		t.Fatalf("mkdir directives: %v", err)
	}

	customUser := []byte("---\nscope: user\nparameters:\n  recall.symbolic-threshold: 0.35\n---\n\n# user-edited\n")
	userPath := filepath.Join(paths.DirectivesDir, "user.md")
	if err := os.WriteFile(userPath, customUser, 0o644); err != nil {
		t.Fatalf("write custom user.md: %v", err)
	}

	customProviders := []byte("[openai]\nbaseUrl = \"https://api.openai.com/v1\"\napiKey = \"sk-fake-but-mine\"\n")
	if err := os.WriteFile(paths.Providers, customProviders, 0o644); err != nil {
		t.Fatalf("write custom providers.toml: %v", err)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gotUser, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("read user.md: %v", err)
	}
	if string(gotUser) != string(customUser) {
		t.Errorf("user.md was overwritten\nwant: %q\n got: %q", customUser, gotUser)
	}

	gotProviders, err := os.ReadFile(paths.Providers)
	if err != nil {
		t.Fatalf("read providers.toml: %v", err)
	}
	if string(gotProviders) != string(customProviders) {
		t.Errorf("providers.toml was overwritten\nwant: %q\n got: %q", customProviders, gotProviders)
	}
}

// TestInitRefreshesShippedTemplates: install-shipped templates
// (defaults.md, README.md, .gitignore) are rewritten on re-init even when
// pre-existing on disk with non-canonical content. The on-disk copy is a
// refreshable snapshot, not user content.
func TestInitRefreshesShippedTemplates(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	if err := os.MkdirAll(paths.DirectivesDir, 0o755); err != nil {
		t.Fatalf("mkdir directives: %v", err)
	}

	tampered := []byte("# tampered — init must rewrite this\n")
	defaultsPath := filepath.Join(paths.DirectivesDir, "defaults.md")
	for _, p := range []string{defaultsPath, paths.Readme, paths.Gitignore} {
		if err := os.WriteFile(p, tampered, 0o644); err != nil {
			t.Fatalf("write tampered %s: %v", p, err)
		}
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cases := []struct {
		path string
		want string
	}{
		{defaultsPath, seedDefaultsMD},
		{paths.Readme, seedReadmeMD},
		{paths.Gitignore, seedGitignore},
	}
	for _, c := range cases {
		got, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("read %s: %v", c.path, err)
		}
		if string(got) != c.want {
			t.Errorf("%s was not refreshed to canonical content\ngot: %q\nwant: %q", c.path, got, c.want)
		}
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

// TestInitInstallsPreCommitHook: a fresh init writes the canonical
// pre-commit hook with mode 0755.
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
	// Sanity: the hook should invoke `personant index check --home`, since
	// that is the behavior the spec promises.
	if !strings.Contains(string(body), "personant index check --home") {
		t.Errorf("pre-commit hook missing `personant index check --home` invocation\ncontent:\n%s", body)
	}
}

// TestInitRewritesPreCommitHook: any pre-existing pre-commit hook is
// overwritten with the canonical script. The hook is wholly agent-owned;
// there is no "leave unmanaged hook alone" branch.
func TestInitRewritesPreCommitHook(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	// Stand up the .git tree so we can pre-place a hook before Init runs.
	cmd := exec.Command("git", "-C", home, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pre-git init: %v: %s", err, out)
	}
	hooksDir := filepath.Join(home, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks: %v", err)
	}
	hookPath := filepath.Join(hooksDir, "pre-commit")
	preexisting := []byte("#!/bin/sh\n# arbitrary pre-existing hook\necho hi\nexit 0\n")
	if err := os.WriteFile(hookPath, preexisting, 0o755); err != nil {
		t.Fatalf("write preexisting hook: %v", err)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if string(got) == string(preexisting) {
		t.Fatalf("pre-existing hook was not rewritten\ncontent:\n%s", got)
	}
	if !strings.Contains(string(got), "personant index check --home") {
		t.Errorf("rewritten hook missing canonical body\ncontent:\n%s", got)
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

