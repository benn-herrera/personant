package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"personant/internal/clock"
	"personant/internal/version"
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
//   - Install-shipped templates (directives/defaults.md, README.md) are
//     *always rewritten* to match the binary. They are bootstrap
//     snapshots, not user content. .gitignore is the hybrid: its seeded
//     block is rewritten to match the binary, user lines outside the
//     block are preserved (see writeGitignoreMerged).
//   - Files that accrue canonical state (directives/user.md,
//     providers.toml) are *preserved* if present; re-init cannot
//     reconstruct them.
//   - Canonical/derived data files (spine.jsonl, symbols.jsonl) are
//     created empty if missing and never truncated.
//   - The §9.1 home format stamp (version.toml) is written only when
//     missing; an existing stamp is never rewritten (see the step itself
//     for why re-stamping would be actively wrong).
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
		{"archive/", paths.ArchiveDir},
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

	// Home on-disk format stamp (§9.1): TOUCH-IF-MISSING, deliberately NOT
	// the rewriteFiles treatment below. Init rewrites install-shipped
	// templates on every run so they match the binary, but the format stamp
	// is not a template — it is the home's own record of which layout its
	// bytes are in. Rewriting it would silently re-declare an unmigrated
	// older home as current, skipping the migration that makes it readable.
	// An existing value is therefore left exactly as found; only a home that
	// has never been stamped gets one. The file is canonical and tracked —
	// it is NOT in the seeded .gitignore block, so the initial commit below
	// includes it.
	createdVersion, err := writeIfMissing(paths.HomeVersion, homeVersionTOML(version.CurrentHomeFormat))
	if err != nil {
		return fmt.Errorf("init: write %s: %w", filepath.Base(paths.HomeVersion), err)
	}
	if createdVersion {
		logf("init: wrote %s", paths.HomeVersion)
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
	}
	for _, s := range rewriteFiles {
		if err := writeAlways(s.path, []byte(s.content)); err != nil {
			return fmt.Errorf("init: write %s: %w", filepath.Base(s.path), err)
		}
		logf("init: wrote %s", s.path)
	}

	// .gitignore is rewrite-with-preservation: the seeded block is
	// authoritative (refreshed to match the binary, like the templates
	// above), but user-added ignore lines outside it survive. A wholesale
	// rewrite would clobber user entries, converting their deliberately-
	// ignored scratch into untracked files that recovery's debris sweep
	// then quarantines out of the tree.
	changed, err := writeGitignoreMerged(paths.Gitignore)
	if err != nil {
		return fmt.Errorf("init: write .gitignore: %w", err)
	}
	if changed {
		logf("init: wrote %s", paths.Gitignore)
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

	createdPrimary, err := initGit(paths.Home, logf)
	if err != nil {
		return err
	}
	// The daily DB is born by Init ONLY alongside a fresh primary (true
	// greenfield). An existing home lacking a daily is the LEGACY-UPGRADE
	// shape and must stay daily-less through Init: Reconcile's §2.5 rows
	// (greenfield vs benign-morning, discriminated by the watermark) and
	// cell-12's verify-gated adopt are the discriminators, and an
	// Init-minted daily baseline would blind them — it commits un-adopted
	// legacy content into a clean-looking daily, skipping the cell-12
	// structural gate, and turns the benign-morning index.Check
	// stamp-only path into a spurious full rebuild. No code path observes
	// a daily-less substrate either way: the open sequence is Init →
	// Reconcile, and Reconcile's morning-init/adopt rows mint the daily
	// before anything reads it.
	if createdPrimary {
		if err := initGitDaily(paths, logf); err != nil {
			return err
		}
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

// The managed-block markers for .gitignore. Init keeps the block between
// them authoritative while preserving everything outside it.
const (
	gitignoreBeginMarker = "# >>> personant managed (rewritten by init; add your entries outside this block) >>>"
	gitignoreEndMarker   = "# <<< personant managed <<<"
)

// writeGitignoreMerged installs the seeded gitignore block, preserving
// user-added lines, and reports whether the file changed. Idempotent:
// a re-run against its own output writes nothing.
func writeGitignoreMerged(path string) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	merged := mergeGitignore(existing)
	if bytes.Equal(existing, merged) {
		return false, nil
	}
	return true, os.WriteFile(path, merged, 0o644)
}

// mergeGitignore computes the .gitignore content for this install: the
// seeded block (authoritative) plus every user line outside it. Three
// shapes of existing content:
//   - none → just the marked block;
//   - markers present → the block's content is replaced with the seed,
//     lines before/after the markers are kept in place;
//   - no markers (pre-marker install, or a hand-created file) → lines
//     that exactly match a seeded line are treated as stale seed and
//     dropped (they now live in the block); everything else is user
//     content and is preserved below the block.
func mergeGitignore(existing []byte) []byte {
	block := gitignoreBeginMarker + "\n" + seedGitignoreBody + gitignoreEndMarker
	if len(existing) == 0 {
		return []byte(block + "\n")
	}
	lines := strings.Split(strings.TrimSuffix(string(existing), "\n"), "\n")

	begin, end := -1, -1
	for i, l := range lines {
		switch l {
		case gitignoreBeginMarker:
			if begin == -1 {
				begin = i
			}
		case gitignoreEndMarker:
			end = i
		}
	}

	var out []string
	if begin != -1 && end > begin {
		out = append(out, lines[:begin]...)
		out = append(out, block)
		out = append(out, lines[end+1:]...)
	} else {
		seeded := make(map[string]struct{})
		for _, l := range strings.Split(strings.TrimSuffix(seedGitignoreBody, "\n"), "\n") {
			seeded[l] = struct{}{}
		}
		out = append(out, block)
		for _, l := range lines {
			if _, isSeed := seeded[l]; !isSeed {
				out = append(out, l)
			}
		}
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// initGit ensures Home is a git repository with at least one commit and
// reports whether it CREATED the repo (false when .git/ pre-existed —
// the greenfield-vs-legacy signal Init's daily-birth decision keys on).
//   - If .git/ already exists, it is opened with go-git.PlainOpen and
//     left otherwise untouched.
//   - Otherwise go-git.PlainInit creates the repo.
//   - If HEAD has no commits, an initial "personant init" commit is made
//     via go-git's worktree. Author identity falls back to a personant
//     fallback when the repo's git config has no user.name/user.email.
//
// No git binary is required on $PATH; the work is done in-process.
func initGit(home string, logf func(format string, args ...any)) (created bool, err error) {
	gitDir := filepath.Join(home, ".git")

	var repo *git.Repository
	if _, err := os.Stat(gitDir); err == nil {
		opened, oerr := git.PlainOpen(home)
		if oerr != nil {
			return false, fmt.Errorf("init: open existing repo: %w", oerr)
		}
		repo = opened
	} else if errors.Is(err, os.ErrNotExist) {
		madeRepo, cerr := git.PlainInit(home, false)
		if cerr != nil {
			return false, fmt.Errorf("init: git init: %w", cerr)
		}
		repo = madeRepo
		created = true
		logf("init: git init %s", home)
	} else {
		return false, fmt.Errorf("init: stat .git: %w", err)
	}

	if err := initialCommitIfEmpty(repo, logf); err != nil {
		return created, err
	}
	return created, nil
}

// initGitDaily ensures the DAILY recovery DB (#94 R3b: <home>/.git-daily,
// a git-dir divorced from the shared worktree) exists with a baseline
// commit. Called ONLY on the greenfield path (Init created the primary
// this run — see the caller): on an existing home a missing daily is
// the legacy-upgrade shape that Reconcile's §2.5/cell-12 rows must
// discriminate, and birthing it here would blind them. Idempotent: an
// existing .git-daily is left entirely alone (a half-created or corrupt
// one is recovery's recreate-from-worktree case, not init's).
//
// The daily DB is DISPOSABLE scaffolding: nuked and reborn at each day
// barrier; the worktree is the truth and primary (.git) is the career
// history. Both git-dirs are covered by the seeded .gitignore block
// (written above, before the baseline Add honors it — INV-6).
func initGitDaily(paths PersonantPaths, logf func(format string, args ...any)) error {
	if _, err := os.Stat(paths.GitDaily); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("init: stat .git-daily: %w", err)
	}
	// NOT git.Init: with a divorced git-dir it writes a `.git` gitdir-link
	// FILE into the worktree, colliding with primary's real `.git` dir.
	// Storage init + HEAD ref is git.Init minus that link (autogit's
	// InitDaily does the same at runtime).
	storer := filesystem.NewStorage(osfs.New(paths.GitDaily), cache.NewObjectLRUDefault())
	if err := storer.Init(); err != nil {
		return fmt.Errorf("init: git init daily: %w", err)
	}
	if err := storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.Master)); err != nil {
		return fmt.Errorf("init: daily HEAD: %w", err)
	}
	repo, err := git.Open(storer, osfs.New(paths.Home))
	if err != nil {
		return fmt.Errorf("init: open daily: %w", err)
	}
	logf("init: git init %s", paths.GitDaily)
	if err := initialCommitIfEmpty(repo, logf); err != nil {
		return fmt.Errorf("init: daily baseline: %w", err)
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
		Author: CommitSignature(repo),
	}); err != nil {
		return fmt.Errorf("init: git commit: %w", err)
	}
	logf("init: git initial commit")
	return nil
}

// CommitSignature returns the author/committer signature for commits
// against repo. If the repo's git config (including local + global
// scopes via go-git's ConfigScoped) yields a user.name + user.email
// pair, those are used (preserving user identity). Otherwise the
// personant fallback identity ("personant" <personant@localhost>)
// kicks in so a freshly-initialized tempdir with no git config still
// produces a valid commit.
//
// Used by store.Init for the bootstrap commit and by internal/autogit
// for every subsequent commit; consolidating here keeps the default
// identity defined in exactly one place.
func CommitSignature(repo *git.Repository) *object.Signature {
	cfg, err := repo.Config()
	if err == nil && cfg.User.Name != "" && cfg.User.Email != "" {
		return &object.Signature{
			Name:  cfg.User.Name,
			Email: cfg.User.Email,
			When:  clock.Timeline(),
		}
	}
	return &object.Signature{
		Name:  "personant",
		Email: "personant@localhost",
		When:  clock.Timeline(),
	}
}

// Seed content. Verbatim from the implementation spec for the init step.
// See Init for the per-file rewrite-vs-preserve policy: install-shipped
// templates (defaults.md, README.md) are rewritten on every init; the
// .gitignore seeded block is rewritten with user lines preserved; accrual
// files (user.md, providers.toml) are preserved if present.

const seedDefaultsMD = `---
scope: defaults
project: null
parameters:
  engagement.decay-turns: 8
  engagement.decay-time: 7d
  closure.ack-mode: auto
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

const seedProvidersTOML = `# personant provider configuration — the endpoint POOL.
#
# One TOML table per provider; the bracket header is the lookup key.
# This file lists what is AVAILABLE. The CHOICES that draw from this
# pool live in config.toml. See spec §8.2.1.
#
# Each entry MUST declare what the endpoint is and how to talk to it:
#   type = "inference"   an LLM endpoint (chat, embeddings, /models)
#   type = "search"      a web.search backend (spec §6.1.1)
#   api  = "openai"      OpenAI-compatible HTTP  (inference)
#   api  = "exa"         Exa search protocol     (search)
# Neither has a default — an entry missing one, or pairing a type with an
# api it does not speak, is dropped from the pool with a named warning
# rather than being guessed at.
#
# Providers deliberately name NO model: catalogues rotate constantly, so
# a model id pinned here is a field that goes stale. Models are chosen in
# config.toml (or --model); ` + "`personant models --provider <name>`" + ` lists
# what an endpoint currently serves.
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
# baseUrl    = "https://api.openai.com/v1"
# apiKeyFile = "../.api_keys/openai.txt"
# type       = "inference"
# api        = "openai"
#
# [reaper]
# baseUrl    = "http://reaper.local:4000/v1"
# apiKeyFile = "../.api_keys/reaper.txt"
# type       = "inference"
# api        = "openai"
#
# [exa]
# baseUrl    = "https://api.exa.ai/search"
# apiKeyFile = "../.api_keys/exa.txt"
# type       = "search"
# api        = "exa"
`

const seedConfigTOML = `# personant configuration — the CHOICES.
#
# These select from the endpoint POOL in providers.toml. Model
# references are "<provider>/<model>". See spec §8.2.
#
# Example:
#
# [chat]
# defaultModel = "reaper/gemma-4-main"
#
# [embedding]
# model = "reaper/nomicai-embed"
# # vectorLength = 768   # optional: Matryoshka-truncated dimension
#
# [search] tunes the optional web.search tool (spec §6.1.1). The backend
# itself is a ` + "`type = \"search\"`" + ` entry in providers.toml, which is where
# its key lives; with no such entry, web.search is simply not offered to
# the model. web.fetch needs no key and is always available.
#
# [search]
# # provider = "exa"   # only needed to pick among several search entries
# # maxPerTurn = 5     # local query caps, enforced by personant itself
# # maxPerDay  = 100
`

const seedReadmeMD = "# personant home\n" +
	"\n" +
	"This directory is managed by the **personant** runtime. It holds the\n" +
	"agent's persistent working memory across all projects.\n" +
	"\n" +
	"- `spine.jsonl` — unified thread spine (canonical).\n" +
	"- `symbols.jsonl` — inverse symbol index (derived; rebuilt from canonical).\n" +
	"- `threads/thr_<n>/` — one directory per thread: `thread.md` (frontmatter + title), `turns/` (FIFO-windowed turn excerpts), `files.json`.\n" +
	"- `projects/prj_<n>/` — per-project metadata and digests, keyed by stable handle.\n" +
	"- `directives/` — tunable behavior (defaults, user-wide, per-project).\n" +
	"- `logs/` — daily plain-text event logs.\n" +
	"- `providers.toml` — LLM provider pool (safe to scan when apiKeyFile is used).\n" +
	"- `config.toml` — chat/embedding choices drawn from the provider pool.\n" +
	"- `tmp/` — agent drafting scratch (not git-committed).\n" +
	"\n" +
	"Layout details and schemas are in the project's `SPEC.md` and `outline.md`.\n" +
	"This directory is git-managed by the runtime; you do not need to run git\n" +
	"commands here yourself.\n"

const seedGitignoreBody = `# personant home gitignore
tmp/
last-active
# Both git-dirs are ignored EXPLICITLY (#94 R3b, INV-6). git auto-ignores
# only a repo's OWN git-dir: the daily handle (.git-daily as git-dir over
# this worktree) would otherwise see .git/ as untracked, and primary would
# see .git-daily/ as untracked — either could commit a sibling git DB or
# have the recovery debris-sweep quarantine a LIVE one. The .git/ line is
# redundant-harmless for primary, load-bearing for daily.
.git/
.git-daily/
# history is the REPL line-edit history (§4.3.1) — operational state, capped
# and rewritten by the REPL. It is never canonical, so §3.11 checkpoints must
# not commit it.
history
# working-set.json is volatile session state — Layer B/C membership
# rewritten on every turn. Git-tracking it would dirty the home tree
# with LRU churn every turn; it is operational state, not canonical.
working-set.json
# Crash-stability substrate (#94, SPEC §4.5.8) — all operational, never
# canonical. They must survive a recovery reset --hard (git-ignored files
# are untouched by reset), which is precisely why they are not tracked:
#   - op-in-progress.json: the batch-op in-flight marker (archival/sleep/
#     recovery). Its survival across reset is what lets recovery read
#     "an op was running" after reverting the worktree.
#   - turn-journal.jsonl: prompt/response content journal, truncated on
#     CommitTurn. A NON-EMPTY journal is also the in-flight-TURN signal
#     (its first record carries the turn id; the append's fsync makes the
#     signal durable). Tracking it would commit transient in-flight bytes.
#   - derived-watermark: built-from-commit hash for the regeneration path;
#     a derived pointer, rebuildable, never canonical.
op-in-progress.json
turn-journal.jsonl
derived-watermark
# recovery/ holds preserved crash artifacts: journaled turn content
# surfaced after a rollback (recovered-turn-*.md, never auto-replayed
# into canonical) and the transient log-preservation staging a recovery
# reset uses. Operational forensics — must survive the reset and must
# never be committed.
recovery/
# .recall-cache/ is the derived embedding-vector cache (§3.4 / intra-thread
# recall). It is operational state — rebuildable from canonical (spine.jsonl
# + threads/*/turns/*.md) on any miss or staleness — and is NEVER canonical
# (I5). Deleting it costs CPU at next startup, never data. Git-tracking it
# would couple a regenerable derivation to its source for no information gain.
.recall-cache/
# symbols.jsonl is a derived index — rebuildable from spine.jsonl plus
# thread frontmatters via index.Rebuild. Single-source-of-truth:
# tracking the derivation alongside its canonical source creates a
# disagreement risk for no information gain. Fresh-clone substrates
# rebuild it on open (queued: explicit rebuild-on-open hook for the
# submind clone case).
symbols.jsonl
# providers.toml and config.toml are safe to git-track when apiKeyFile
# is used (no inline secrets). Keep the API-key files themselves out of
# this directory; if you place any here, git-ignore them explicitly.
`
