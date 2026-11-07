# CONVENTIONS – Personant

Project-specific process and house rules

Must-see:

- No user-facing behaviors or outcomes are designed here, that belongs to SPEC
- No project mechanisms or code structure are put forth, that belongs to ARCHITECTURE
- Always read [Two gates](#two-gates--do-not-conflate-them) before choosing a gate check

## Before a change

Read THESIS and ARCHITECTURE "Applying the thesis" before a non-trivial change to this
repository, and ARCHITECTURE "Substrate non-negotiables" before touching storage, persistence or
derived files. Do not write the following changes; report them to whoever assigned the work, with the
row or clause they match, and continue only on that person's direction:

- a change that matches a row of ARCHITECTURE "Anti-patterns to push back on";
- a change that adds a path from a session's project work into behavior or process memory other
  than SPEC §1.8's single write path, or that promotes a single-project pattern into behavior
  memory (SPEC §1.8 "Directive accrual").

## Plan & Execute Process

- active plan: `ACTIVE_PLAN.md` at the repo root, or `ACTIVE_PLAN/entry-point.md` when the plan is
  diced
- reference search: `git grep -l '<name>' -- '*.go'`

### Sim harness gates

The simulation harness (`internal/scenarios/`, which includes `internal/scenarios/sim/`) is always
shipped code: it passes the checkpoint gate, the dispatchability review and the post-plan review,
and no plan exempts harness changes from them (probe plans: "Probe work").

### Probe work

A probe is work whose purpose is integration data; a probe plan is a plan made of probe work, and a
probe run is a run whose purpose is integration data.

- Probe work runs on a throwaway branch, never on `main` or an implementation branch.
- A system defect found during probe work is recorded as a ROADMAP item stating the fact and where
  it lives, and is not fixed on the probe branch.
- Probe runs use mock inference only: no live model and no live embedder. The live-inference and
  live-embedding sim modes stay available to every run that is not a probe run.
- **A probe plan is exempt from the checkpoint gate and the post-sprint review.** Its packages gate
  only on the runs that make its data trustworthy, plus `just test-run` for any production package
  they add a file to; its review is a spot check that the data it gathers measures what it claims.

## Language constraints

### Memory service: Go

Stdlib-first; a dependency enters only under "Dependencies" below.
### TypeScript usage limited to plugin

The plugin is TypeScript, in `opencode-plugin/` and nowhere else. It takes no package dependency:
it imports only what the opencode host supplies (ARCHITECTURE "Plugin route").
### Auxiliary scripts: Python, stdlib only

No `pip`, no virtualenvs, no lockfiles, and no
exception for one small dependency. **Python 3.9 is the floor** (what macOS's command-line tools
ship): no `match`, no runtime `X | Y` unions (use `typing.Optional`/`Union`), no `zip(strict=)`,
no 3.10+ stdlib APIs. The shipped Go binary never depends on Python; Python may
generate committed or derived fixtures through a runner target.

**Exception: user-installed tooling.** A Python tool the user has chosen and wired in — installed
into the project virtualenv by a `just` recipe (today `docdicer`, via `_venv`) — is used through
that recipe. Agents never add, remove or upgrade such a tool, its package source or the virtualenv
setup on their own initiative; they propose it to the user instead.
## Repo state

### Pre-production: no transition scaffolding

Replacing a mechanism before a release still writes a home-format migration when a stored format
changes, so existing dev and sim homes keep opening.
### Versioning and acceptance

The version lines and the home's on-disk
format are defined in SPEC §9.1. Their current values live in `internal/version` and are never
restated in prose; `personant version` prints them. The substrate line is semver; its MINOR bumps
are developer fiat at the milestone gates.

An acceptance claim states its coverage (simulated, modeled, parked) as SPEC §9.1's honesty clause
requires.

## Build / test

Run `just` (default recipe `info`) for the current recipe list; the target inventory lives in the
justfile.

### Two gates — do not conflate them

- edit gate: `just build` plus `just test-run <pkg> <regexp>` for only the touched tests
- checkpoint gate: `just test` (`fmt-check`, `go vet` and `go test` across the Go packages)

Go's test cache scopes `just test`, so a package reruns only when its sources, dependencies,
the files its tests read, the env vars they read or the test flags change, so the sim package (tens
of minutes) runs only when a change could alter its outcome. The cache keys what tests read by stat
info (size, mode, mtime), not content, and covers every directory a test stats, opens or
path-resolves. So a generator that rewrites a file tests read leaves identical content untouched; a
test never creates or removes entries in a directory another package's tests read (run homes under
`test/rundata/` are emptied, never replaced); and a miss is diagnosed with
`just test-cache-why <pkg>`, which names the changed input. Probe plans gate as "Probe work" states.

### Docs stay current

ROADMAP.md is updated when an item ships or is superseded; a known defect is a ROADMAP item,
checked off by the change that fixes it.

## House rules for agents

### Dependencies

General dependency discipline: ARCHITECTURE "Approved-when-earned
dependencies". `go.mod` is the catalog.
- **Permanently out** (architectural conflict): `langchaingo/memory`, `langchaingo/chains`,
  `mattn/go-sqlite3`. Do not reintroduce.
- **SQLite:** any Go SQLite need takes `modernc.org/sqlite` first.
- **Open:** U5 — how the service reaches its own models decides whether `langchaingo/llms` is
  needed. Until then `langchaingo/llms` stays out; the OpenAI-compatible client in
  `internal/model` covers the need.
- **Vet adoption and maintenance before proposing a dependency — from the registry, not the
  README.** Record, measured not asserted: (1) last release date; (2) `Imported by` count on
  pkg.go.dev; (3) deprecation status; (4) transitive dep count; (5) license. For a port, verify
  the port's activity, not its upstream's. Default to the well-trodden option unless the
  off-standard gain is substantial. The record goes in the message of the commit that pins the
  module with `just add-dependency`, and is written before that recipe runs. (Origin: commit
  5692c75.)
- **Pending removal with their consumers** (ROADMAP.md B1, "Retire the self-built harness"):
  `codeberg.org/readeck/go-readability/v2` and `github.com/JohannesKaufmann/html-to-markdown/v2`
  serve only `internal/tools/web/`. Both entries go in the change that removes their consumer.
- **Application code talks to `MemoryOps`, not to the substrate directly.** Package-level detail
  and the substrate-free exceptions: ARCHITECTURE "Port-and-adapter at the substrate boundary".
### All clock reads use `internal/clock`

Direct reads of the `time` package clock
(`time.Now`, `time.Since`) are forbidden outside `internal/clock`. Use `clock.Timeline()` for
simulated-world timestamps and `clock.Profiling()` / `clock.Since()` for real-time and latency
measurement.
### Match the data model

Spine records, thread frontmatter, the symbol index, project metadata
and log events have field-level formats in ARCHITECTURE "Storage and record formats" and
"Event log format"; directive files in SPEC §2.6. Don't invent your own.
### Serialized data is debuggable

A human or agent chasing a problem
reads the file raw. JSON is the format to resist — quote noise, no comments, nesting tracked by
brace-counting. A new file, record stream or command output takes:

| Shape | Format |
|---|---|
| flat records, one per line, sorted or append-only | JSONL — the line is the record: `grep` returns whole records, `git diff` isolates the changed one, id order stays stable |
| nested, or read as a document (command output, configuration, frontmatter) | YAML when tree-shaped (deep nesting, nulls, top-level lists); TOML when flat, hand-edited configuration |

Machine-emitted YAML decodes into typed structs and quotes string values on emit. A format
outside this mapping states its reason in the change that introduces it.
### opencode clone: referent only

The opencode source clone at `../opencode` is read-only. Never edit it and never move it off
the installed release's tag; it moves only in the change that updates the `sim-opencode` version
check (ARCHITECTURE "Plugin route", "Pinned release").
- **Open:** U11 — whether to adopt the rule: never place external state (an agent's data home,
  telemetry) inside `~/.personant/` or a test home, because crash recovery quarantines untracked
  files. opencode's data home is already excluded (ARCHITECTURE "Single spelunking location").
- **Spawned processes: self-limiting, or verified dead** (origin: commit 4fcb0e5). Binding on any
  background process an agent starts — load generators, watchers, servers, opencode servers,
  children of stress harnesses:
  1. **Fail safe, not fail hot.** Every spawned process bounds its own lifetime — a loop with an
     iteration cap, or a `timeout N` wrapper. A bare `while :; do :; done &` has "spin forever" as
     its failure mode.
  2. **Kill by collected PIDs, never `$(jobs -p)`.** Record `$!` at spawn and kill that list. In
     zsh, `$(jobs -p)` expands in a subshell whose job table is empty, so `kill $(jobs -p)` is a
     silent no-op.
  3. **Verify the kill.** A post-cleanup count probe (`ps ... | wc -l`) returning 0 is part of the
     task's definition of done. Nonzero is a failed gate, not a warning.
  4. **Never silence cleanup stderr.** A cleanup command's stderr is the only witness when it
     breaks; `kill ... 2>/dev/null` hides the error that exposes a no-op.
  5. **Prefer in-process contention.** `go test -cpu 8,8,...` and `-count` create scheduler pressure
     inside the test process and leak nothing; external burners are a last resort and carry rules
     1–4.
## Document conventions

How the project documents (THESIS, SPEC, ARCHITECTURE, CONVENTIONS) are written and read.

- **JSONL schemas** use TypeScript-style `interface` notation; it is descriptive only.
- **Field markings:**
  - `field: T` — required.
  - `field?: T` — optional.
  - `[derived]` — regenerated from canonical sources; never hand-edited.
- **Pseudocode** is descriptive and language-neutral.
- **Cross-references** cite SPEC by §-numbered section identifier (e.g. "see SPEC §2.2") and the
  other documents by section title.
- **`Open:`** marks a clause whose content depends on a decision not yet made. Two forms. (1) An
  `Open:` line alone: the text around it states only what is decided. (2) An `Open:` line followed
  by text that specifies a current mechanism: that text binds the implementation until the decision
  lands. A tag such as U3 names a pending decision; ROADMAP.md lists and sequences them.
- **Unused SPEC section numbers** (§0, §4.3.1, §4.3.2, §4.3.4, §4.4–§4.4.4, §4.6, §6–§6.5, §7) are
  never reassigned; code cites sections by number. Where a heading remains, its text says what
  replaced it.
- **Moved sections.** A SPEC heading marked "Unused → ARCHITECTURE «X»" keeps its number and
  states only the outcome; the schema or format lives in ARCHITECTURE section X. A §-citation of
  that heading, in SPEC or in code, refers to X.
