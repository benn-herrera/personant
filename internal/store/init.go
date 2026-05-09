package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// InitOptions controls the behavior of Init.
//
// Logger receives one log line per scaffold step, formatted printf-style.
// A nil Logger is silent. Quiet, when true, suppresses logger calls
// regardless of whether Logger is set.
type InitOptions struct {
	Quiet  bool
	Logger func(format string, args ...any)
}

// Init scaffolds $PERSONANT_HOME according to spec §2.1.
//
// The operation is idempotent by construction: directories are created with
// MkdirAll, files are written only if absent, git init is skipped if .git/
// already exists, and the initial commit is made only if the repo has no
// commits yet. Hand-edited content under canonical paths is never overwritten.
//
// Pre-commit hook installation (spec §8.3) is a separate step and not handled
// here.
func Init(paths PersonantPaths, opts InitOptions) error {
	logf := func(format string, args ...any) {
		if opts.Quiet || opts.Logger == nil {
			return
		}
		opts.Logger(format, args...)
	}

	if paths.Home == "" {
		return errors.New("init: PersonantPaths.Home is empty")
	}

	dirs := []struct {
		label string
		path  string
	}{
		{"home", paths.Home},
		{"threads/", paths.ThreadsDir},
		{"projects/", paths.ProjectsDir},
		{"directives/", paths.DirectivesDir},
		{"logs/", paths.LogsDir},
		{"logs/archive/", paths.LogsArchive},
		{"tmp/", paths.TmpDir},
	}
	for _, d := range dirs {
		created, err := mkdirIfMissing(d.path)
		if err != nil {
			return fmt.Errorf("init: mkdir %s: %w", d.label, err)
		}
		if created {
			logf("init: created %s", d.path)
		}
	}

	emptyFiles := []string{paths.Spine, paths.Symbols}
	for _, p := range emptyFiles {
		created, err := touchIfMissing(p)
		if err != nil {
			return fmt.Errorf("init: touch %s: %w", filepath.Base(p), err)
		}
		if created {
			logf("init: created %s", p)
		}
	}

	seedFiles := []struct {
		path    string
		content string
	}{
		{filepath.Join(paths.DirectivesDir, "defaults.md"), seedDefaultsMD},
		{filepath.Join(paths.DirectivesDir, "user.md"), seedUserMD},
		{paths.Providers, seedProvidersTOML},
		{paths.Readme, seedReadmeMD},
		{paths.Gitignore, seedGitignore},
	}
	for _, s := range seedFiles {
		created, err := writeIfMissing(s.path, []byte(s.content))
		if err != nil {
			return fmt.Errorf("init: write %s: %w", filepath.Base(s.path), err)
		}
		if created {
			logf("init: wrote %s", s.path)
		}
	}

	if err := initGit(paths.Home, logf); err != nil {
		return err
	}

	logf("init: ok")
	return nil
}

// mkdirIfMissing returns (true, nil) if it created the directory, (false, nil)
// if it already existed, and (false, err) on failure.
func mkdirIfMissing(path string) (bool, error) {
	stat, err := os.Stat(path)
	if err == nil {
		if !stat.IsDir() {
			return false, fmt.Errorf("path %s exists and is not a directory", path)
		}
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, err
	}
	return true, nil
}

// touchIfMissing creates an empty file at path if it does not exist. Uses
// O_EXCL to avoid clobbering content created concurrently.
func touchIfMissing(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, f.Close()
}

// writeIfMissing writes content to path if and only if path does not exist.
// Idempotent: a second call after the file is created (with any content) is a
// no-op.
func writeIfMissing(path string, content []byte) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	if _, werr := f.Write(content); werr != nil {
		f.Close()
		return false, werr
	}
	if cerr := f.Close(); cerr != nil {
		return false, cerr
	}
	return true, nil
}

// initGit ensures Home is a git repository with at least one commit.
//   - If .git/ already exists, it is left untouched.
//   - Otherwise `git init` is run on Home.
//   - If HEAD has no commits, an initial "personant init" commit is made.
//     GIT_*_NAME / GIT_*_EMAIL env vars are set to a personant fallback only
//     when not already present in the inherited environment, so user-level
//     git config still wins at runtime when configured.
func initGit(home string, logf func(format string, args ...any)) error {
	gitDir := filepath.Join(home, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Already a repo. Still try the initial commit if the repo has no
		// commits yet — handles a half-initialized state from an earlier
		// failed run.
		if commitErr := initialCommitIfEmpty(home, logf); commitErr != nil {
			return commitErr
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("init: stat .git: %w", err)
	}

	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("init: git not found in PATH: %w", err)
	}

	if err := runGit(home, nil, "init", "-q"); err != nil {
		return fmt.Errorf("init: git init: %w", err)
	}
	logf("init: git init %s", home)

	if err := initialCommitIfEmpty(home, logf); err != nil {
		return err
	}
	return nil
}

// initialCommitIfEmpty creates the bootstrap commit if the repo at home has
// no HEAD yet. Quiet on a repo that already has commits.
func initialCommitIfEmpty(home string, logf func(format string, args ...any)) error {
	// `git rev-parse HEAD` prints to stderr ("unknown revision") on a fresh
	// repo and exits non-zero. We treat that as the empty-repo signal.
	cmd := exec.Command("git", "-C", home, "rev-parse", "--verify", "HEAD")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err == nil {
		// HEAD already exists; nothing to do.
		return nil
	}

	env := commitEnv(home)
	if err := runGit(home, env, "add", "."); err != nil {
		return fmt.Errorf("init: git add: %w", err)
	}
	if err := runGit(home, env, "commit", "-q", "-m", "personant init"); err != nil {
		return fmt.Errorf("init: git commit: %w", err)
	}
	logf("init: git initial commit")
	return nil
}

// commitEnv returns the env vars for the bootstrap commit. The personant
// fallback identity is supplied *only* if git cannot otherwise resolve a
// user.name and user.email — env-var precedence outranks git config, so
// unconditionally setting GIT_AUTHOR_* would override the user's configured
// identity. When git already has identity (env vars or config), we return
// nil and let the inherited environment carry through.
func commitEnv(home string) []string {
	if gitHasIdentity(home) {
		return nil
	}
	base := os.Environ()
	base = append(base,
		"GIT_AUTHOR_NAME=personant",
		"GIT_AUTHOR_EMAIL=personant@localhost",
		"GIT_COMMITTER_NAME=personant",
		"GIT_COMMITTER_EMAIL=personant@localhost",
	)
	return base
}

// gitHasIdentity reports whether `git -C home config user.name` and
// `user.email` both yield a non-empty value (env, local, global, or system).
// Used as the gate for whether the personant fallback identity should kick in.
func gitHasIdentity(home string) bool {
	for _, key := range []string{"user.name", "user.email"} {
		out, err := exec.Command("git", "-C", home, "config", "--get", key).Output()
		if err != nil {
			return false
		}
		if len(bytes.TrimSpace(out)) == 0 {
			return false
		}
	}
	return true
}

// runGit invokes git in dir with the given args. Stderr is captured and
// included in the returned error on failure.
func runGit(dir string, env []string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if env != nil {
		cmd.Env = env
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, string(msg))
	}
	return nil
}

// Seed content. Verbatim from the implementation spec for the init step.
// Hand-editing of the seeded files is supported; the runtime never rewrites
// them after install (see §2.6 directive precedence).

const seedDefaultsMD = `---
scope: defaults
project: null
parameters:
  engagement.decay-turns: 8
  engagement.decay-time: 7d
  recall.symbolic-threshold: 0.4
  recall.cross-project-threshold: 0.5
  layer.b-top-k: 3
  layer.budget.percentages:
    E: 8
    A1: 8
    B: 50
    C: 15
    current_turn: 15
  anchors.cap-per-thread: 6
  history.cap-per-thread: 40
  spine.entry-max-chars: 200
  cross-project.digest-per-project-bytes: 150
  dissect.pressure-threshold: 0.90
last_modified: 2026-05-09T04:08:00-07:00
modification_source: install
---

# System defaults

These are the baked-in default parameter values for the personant runtime.
Per spec §2.6, user.md and project-scoped overrides take precedence in that
order; this file is the floor.

This file is rewritten on ` + "`personant init`" + ` only when missing — it is not
re-seeded on upgrade. Hand-editing is supported but unusual; prefer setting
overrides in ` + "`user.md`" + ` or project directives.
`

const seedUserMD = `---
scope: user
project: null
parameters: {}
last_modified: 2026-05-09T04:08:00-07:00
modification_source: install
---

# User directives

This file accumulates user-wide overrides — both manually edited entries and
ones accrued automatically from ack-prompt scope grants and decline
categorizations. See spec §2.6 for precedence rules.
`

const seedProvidersTOML = `# personant LLM provider configuration
#
# One TOML table per provider. The provider name (header in brackets) is
# the lookup key used by the runtime and ` + "`/model <provider>:<model>`" + `.
#
# This file is canonical and secret-bearing. The runtime never includes
# its content in any LLM context, log line, ack prompt, or captured
# shell output. See spec §8.2.1.
#
# Example:
#
# [openai]
# baseUrl      = "https://api.openai.com/v1"
# apiKey       = "sk-..."
# defaultModel = "gpt-5.4-2026-03-05"
#
# [local]
# baseUrl      = "http://localhost:11117"
# apiKey       = "dummy"
# defaultModel = "gemma-4-26B-A4B-it-MXFP4_MOE"
`

const seedReadmeMD = "# personant home\n" +
	"\n" +
	"This directory is managed by the **personant** runtime. It holds the\n" +
	"agent's persistent working memory across all projects.\n" +
	"\n" +
	"- `spine.jsonl` — unified thread spine (canonical).\n" +
	"- `symbols.jsonl` — inverse symbol index (derived; rebuilt from canonical).\n" +
	"- `threads/` — one markdown file per thread, with YAML frontmatter.\n" +
	"- `projects/prj_<n>/` — per-project metadata and digests, keyed by stable handle.\n" +
	"- `directives/` — tunable behavior (defaults, user-wide, per-project).\n" +
	"- `logs/` — daily plain-text event logs.\n" +
	"- `providers.toml` — LLM provider config (secret-bearing; not git-committed).\n" +
	"- `tmp/` — agent drafting scratch (not git-committed).\n" +
	"\n" +
	"Layout details and schemas are in the project's `spec.md` and `outline.md`.\n" +
	"This directory is git-managed by the runtime; you do not need to run git\n" +
	"commands here yourself.\n"

const seedGitignore = `# personant home gitignore
tmp/
providers.toml
`
