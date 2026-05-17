package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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
// MkdirAll, git init is skipped if .git/ already exists, and the initial
// commit is made only if the repo has no commits yet.
//
// Per-file install policy on re-init:
//   - Install-shipped templates (directives/defaults.md, README.md,
//     .gitignore) are *always rewritten* to match the binary. They are
//     bootstrap snapshots, not user content.
//   - Files that accrue canonical state (directives/user.md,
//     providers.toml) are *preserved* if present; re-init cannot
//     reconstruct them.
//   - Canonical/derived data files (spine.jsonl, symbols.jsonl) are
//     created empty if missing and never truncated.
//
// Git is managed in-process via go-git: `git init` plus the bootstrap
// "personant init" commit run on the home tree. No git binary is
// required on $PATH. Substrate validation for autonomic git operations
// post-init runs through internal/autogit's GitCheckFlags bitmask —
// not through a git pre-commit hook. See spec §8.3.
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

	// Install-shipped templates: rewrite on every init to match the binary.
	// These are not user content; they are bootstrap snapshots and the point
	// of re-running init is to refresh them.
	rewriteFiles := []struct {
		path    string
		content string
	}{
		{filepath.Join(paths.DirectivesDir, "defaults.md"), seedDefaultsMD},
		{paths.Readme, seedReadmeMD},
		{paths.Gitignore, seedGitignore},
	}
	for _, s := range rewriteFiles {
		if err := writeAlways(s.path, []byte(s.content)); err != nil {
			return fmt.Errorf("init: write %s: %w", filepath.Base(s.path), err)
		}
		logf("init: wrote %s", s.path)
	}

	// Accrual / hand-edited files: preserve if present. user.md
	// accumulates ack-prompt scope grants and decline categorizations;
	// providers.toml and config.toml are hand-edited. Re-init cannot
	// reconstruct any of them.
	preserveFiles := []struct {
		path    string
		content string
	}{
		{filepath.Join(paths.DirectivesDir, "user.md"), seedUserMD},
		{paths.Providers, seedProvidersTOML},
		{paths.Config, seedConfigTOML},
	}
	for _, s := range preserveFiles {
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

// writeAlways writes content to path, replacing any existing file at that
// path. Used for install-shipped templates whose canonical version lives
// in the binary; the on-disk copy is a refreshable artifact, not user
// content.
func writeAlways(path string, content []byte) error {
	return os.WriteFile(path, content, 0o644)
}

// initGit ensures Home is a git repository with at least one commit.
//   - If .git/ already exists, it is opened with go-git.PlainOpen and
//     left otherwise untouched.
//   - Otherwise go-git.PlainInit creates the repo.
//   - If HEAD has no commits, an initial "personant init" commit is made
//     via go-git's worktree. Author identity falls back to a personant
//     fallback when the repo's git config has no user.name/user.email.
//
// No git binary is required on $PATH; the work is done in-process.
func initGit(home string, logf func(format string, args ...any)) error {
	gitDir := filepath.Join(home, ".git")

	var repo *git.Repository
	if _, err := os.Stat(gitDir); err == nil {
		opened, oerr := git.PlainOpen(home)
		if oerr != nil {
			return fmt.Errorf("init: open existing repo: %w", oerr)
		}
		repo = opened
	} else if errors.Is(err, os.ErrNotExist) {
		created, cerr := git.PlainInit(home, false)
		if cerr != nil {
			return fmt.Errorf("init: git init: %w", cerr)
		}
		repo = created
		logf("init: git init %s", home)
	} else {
		return fmt.Errorf("init: stat .git: %w", err)
	}

	if err := initialCommitIfEmpty(repo, logf); err != nil {
		return err
	}
	return nil
}

// initialCommitIfEmpty creates the bootstrap commit if the repo has no
// HEAD yet. Quiet on a repo that already has at least one commit.
func initialCommitIfEmpty(repo *git.Repository, logf func(format string, args ...any)) error {
	if _, err := repo.Head(); err == nil {
		return nil
	} else if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return fmt.Errorf("init: repo HEAD: %w", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("init: worktree: %w", err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return fmt.Errorf("init: git add: %w", err)
	}
	if _, err := wt.Commit("personant init", &git.CommitOptions{
		Author: commitSignature(repo),
	}); err != nil {
		return fmt.Errorf("init: git commit: %w", err)
	}
	logf("init: git initial commit")
	return nil
}

// commitSignature returns the author signature for the bootstrap
// commit. If the repo's git config (including local + global scopes via
// go-git's ConfigScoped) yields a user.name + user.email pair, those
// are used. Otherwise the personant fallback identity ("personant"
// <personant@localhost>) kicks in so a freshly-initialized tempdir
// with no git config still produces a valid commit.
func commitSignature(repo *git.Repository) *object.Signature {
	cfg, err := repo.Config()
	if err == nil && cfg.User.Name != "" && cfg.User.Email != "" {
		return &object.Signature{
			Name:  cfg.User.Name,
			Email: cfg.User.Email,
			When:  time.Now(),
		}
	}
	return &object.Signature{
		Name:  "personant",
		Email: "personant@localhost",
		When:  time.Now(),
	}
}

// Seed content. Verbatim from the implementation spec for the init step.
// See Init for the per-file rewrite-vs-preserve policy: install-shipped
// templates (defaults.md, README.md, .gitignore) are rewritten on every
// init; accrual files (user.md, providers.toml) are preserved if present.

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

This file is install-shipped and rewritten on every ` + "`personant init`" + `
to match the binary. Hand-editing is unsupported here — set overrides in
` + "`user.md`" + ` or project directives instead (see spec §2.6).
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

const seedProvidersTOML = `# personant LLM provider configuration — the provider POOL.
#
# One TOML table per provider; the bracket header is the lookup key.
# This file lists what is AVAILABLE. The chat/embedding CHOICES that
# draw from this pool live in config.toml. See spec §8.2.1.
#
# API keys: prefer apiKeyFile — a path to a file holding the key, kept
# out of this file so providers.toml stays safe to scan and git-track.
# apiKeyFile paths resolve relative to this file's directory. Keep the
# key files themselves out of the personant home (or git-ignore them).
# apiKeyUnsafe is the inline form — discouraged; it places a secret
# directly in this file.
#
# Example:
#
# [openai]
# baseUrl      = "https://api.openai.com/v1"
# apiKeyFile   = "../.api_keys/openai.txt"
# defaultModel = "gpt-5.4-2026-03-05"
#
# [reaper]
# baseUrl      = "http://reaper.local:4000/v1"
# apiKeyFile   = "../.api_keys/reaper.txt"
# defaultModel = "gemma-4-main"
`

const seedConfigTOML = `# personant configuration — the chat/embedding CHOICES.
#
# These select from the provider POOL in providers.toml, referenced as
# "<provider>/<model>". See spec §8.2.
#
# Example:
#
# [chat]
# defaultModel = "reaper/gemma-4-main"
#
# [embedding]
# model = "reaper/nomicai-embed"
# # vectorLength = 768   # optional: Matryoshka-truncated dimension
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
	"- `providers.toml` — LLM provider pool (safe to scan when apiKeyFile is used).\n" +
	"- `config.toml` — chat/embedding choices drawn from the provider pool.\n" +
	"- `tmp/` — agent drafting scratch (not git-committed).\n" +
	"\n" +
	"Layout details and schemas are in the project's `spec.md` and `outline.md`.\n" +
	"This directory is git-managed by the runtime; you do not need to run git\n" +
	"commands here yourself.\n"

const seedGitignore = `# personant home gitignore
tmp/
last-active
# providers.toml and config.toml are safe to git-track when apiKeyFile
# is used (no inline secrets). Keep the API-key files themselves out of
# this directory; if you place any here, git-ignore them explicitly.
`
