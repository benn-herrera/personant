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
	for _, want := range []string{
		"tmp/", "history", "providers.toml", ".recall-cache/",
		"op-in-progress.json", "turn-journal.jsonl", "derived-watermark",
	} {
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

	// .gitignore is refresh-with-preservation: the seeded block is
	// installed authoritatively, and the pre-existing (user) line is
	// preserved outside it rather than clobbered.
	gi, err := os.ReadFile(paths.Gitignore)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	if !strings.Contains(string(gi), seedGitignoreBody) {
		t.Errorf("gitignore missing the seeded block:\n%s", gi)
	}
	if !strings.Contains(string(gi), strings.TrimSuffix(string(tampered), "\n")) {
		t.Errorf("pre-existing gitignore line was clobbered:\n%s", gi)
	}
}

// TestInitGitignorePreservesUserLines: a user-added ignore entry outside
// the managed block survives re-Init byte-identically, and re-Init on the
// merged result is idempotent. Clobbering user entries would convert
// their deliberately-ignored scratch into recovery-sweep fodder.
func TestInitGitignorePreservesUserLines(t *testing.T) {
	hasGit(t)
	paths := initIntoTempDir(t)

	const userLine = "my-scratch/\n"
	f, err := os.OpenFile(paths.Gitignore, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open gitignore: %v", err)
	}
	if _, err := f.WriteString(userLine); err != nil {
		t.Fatalf("append user line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close gitignore: %v", err)
	}
	before, err := os.ReadFile(paths.Gitignore)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	after, err := os.ReadFile(paths.Gitignore)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	// The block was already canonical and the user line sits outside it,
	// so re-Init must be a byte-identical no-op.
	if string(after) != string(before) {
		t.Errorf("re-Init modified a gitignore whose managed block was already canonical\nbefore: %q\n after: %q", before, after)
	}
	if !strings.Contains(string(after), userLine) {
		t.Errorf("user gitignore line clobbered by re-Init:\n%s", after)
	}
}

// TestInitGitignoreLegacyUpgrade: a pre-marker gitignore (old seed
// content plus a user line) upgrades to the marked layout — seed lines
// fold into the managed block without duplication, the user line is
// preserved.
func TestInitGitignoreLegacyUpgrade(t *testing.T) {
	hasGit(t)
	home := t.TempDir()
	paths := PathsForHome(home)

	const userLine = "my-legacy-scratch/"
	legacy := seedGitignoreBody + userLine + "\n"
	if err := os.WriteFile(paths.Gitignore, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy gitignore: %v", err)
	}

	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	got, err := os.ReadFile(paths.Gitignore)
	if err != nil {
		t.Fatalf("read gitignore: %v", err)
	}
	s := string(got)
	if strings.Count(s, gitignoreBeginMarker) != 1 {
		t.Errorf("managed block installed %d times, want 1:\n%s", strings.Count(s, gitignoreBeginMarker), s)
	}
	if strings.Count(s, "\n"+userLine+"\n") != 1 {
		t.Errorf("user line not preserved exactly once:\n%s", s)
	}
	// Old seed entries fold into the block — no duplicate entry lines.
	if strings.Count(s, "\nworking-set.json\n") != 1 {
		t.Errorf("seed entry duplicated by legacy upgrade:\n%s", s)
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

	// A pre-existing primary is the legacy-upgrade shape: Init must NOT
	// birth the daily DB (#94 R3b F2) — Reconcile's §2.5/cell-12 rows own
	// that discrimination.
	if _, err := os.Stat(paths.GitDaily); !os.IsNotExist(err) {
		t.Errorf(".git-daily after Init over existing .git: err=%v, want absent (legacy upgrade is Reconcile's call)", err)
	}
}

// TestInitDailyBirthGreenfieldOnly — #94 R3b F2: Init births .git-daily
// only when it also scaffolds a fresh primary (true greenfield). An
// existing home whose daily is missing must stay daily-less through
// Init, so Reconcile can discriminate greenfield vs benign-morning vs
// legacy-adopt (an Init-minted baseline would commit un-adopted legacy
// content into a clean-looking daily and blind cell-12's verify gate).
func TestInitDailyBirthGreenfieldOnly(t *testing.T) {
	hasGit(t)
	paths := initIntoTempDir(t)

	// Greenfield: daily born alongside the fresh primary.
	if _, err := os.Stat(paths.GitDaily); err != nil {
		t.Fatalf(".git-daily after greenfield Init: %v, want present", err)
	}

	// Regress to the legacy-upgrade shape (primary present, daily gone);
	// a re-run Init must leave the daily ABSENT.
	if err := os.RemoveAll(paths.GitDaily); err != nil {
		t.Fatalf("remove .git-daily: %v", err)
	}
	if err := Init(paths, InitOptions{Quiet: true}); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	if _, err := os.Stat(paths.GitDaily); !os.IsNotExist(err) {
		t.Errorf(".git-daily after re-Init on daily-less home: err=%v, want still absent (F2)", err)
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
