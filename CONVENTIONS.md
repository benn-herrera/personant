# CONVENTIONS – Personant

House rules for agents working in this repository. Purpose: THESIS.md. Outcomes: SPEC.md.
Mechanisms, the module map and design anti-patterns: ARCHITECTURE.md. `Open:` lines take the form
SPEC.md §0 defines.

## Before a change

Read THESIS.md and ARCHITECTURE.md "Applying the thesis" before a non-trivial change to this
repository. Do not write the following changes; report them to whoever assigned the work, with the
row or clause they match, and continue only on that person's direction:

- a change that matches a row of ARCHITECTURE.md "Anti-patterns to push back on";
- a change that adds a path from a session's project work into behavior memory other than SPEC.md
  §1.8's single write path, or that promotes a single-project pattern (SPEC.md §1.8 "Directive
  accrual").

## Plan & Execute Process

- **`ACTIVE_PLAN.md`** at the repo root is the one plan under way: decisions confirmed at kickoff,
  work-package boundaries, sequencing and status. Dispatched agents execute out of it; its status
  updates as work packages land. It carries outcomes, their consequences and the principles still
  steering open choices — not decision history, which lives in commits. When the work lands, its
  outcomes move into the contract documents and the file is replaced by the next plan.
- **`ROADMAP_PLANS/`** holds partly-planned work that is not under way: a plan with enough design
  done that someone could pick it up. A plan worth keeping goes there, never into a gitignored tree
  (`mad-design/`, `mad-review/`, `.claude-temp/`). A plan there cites only tracked paths.
  - A plan leaves `ROADMAP_PLANS/` in the same change that makes it the active plan, finishes,
    abandons or supersedes it. A superseding plan first absorbs every section, decision and piece
    of evidence it still relies on, stated as its own, and cites nothing in the plan it replaces;
    then the old plan is deleted.
- Working drafts and half-formed notes are scratch (`.claude-temp/`), not plans.
- **A plan may name a normative page outside the contract documents, and that page outranks the
  plan.** The contract documents are THESIS.md, SPEC.md, ARCHITECTURE.md and CONVENTIONS.md. A plan
  may make a page of its own subject normative — for example, a scoring rubric the contract cites —
  and on that subject the page is the requirement while the plan is the route. Where a plan row and
  the page disagree, the work implements the page and the plan's line is reported as moved, in the
  commit that lands it; the landing does not edit the page, and the contract documents cite it
  rather than restate it. A plan that has such a page names it, so a reader knows which source
  settles a disagreement.
- **Post-sprint review.** Every activated plan — a plan whose packages are being dispatched —
  wherever it lives, ends with a post-sprint review section holding the review, revision and
  burndown steps (probe plans: "Probe work").
  - Unless the plan names others, the reviewers are an architect and a coder, with THESIS.md,
    SPEC.md, ARCHITECTURE.md and CONVENTIONS.md as their pre-task reading. They do not get the
    plan: they review the changes against the standards and for fitness for purpose, the tests of
    all touched code included.
  - The burndown list is written to a separate `<plan>_REVIEW_BURNDOWN.md` and gets a
    prompt-engineer review and revision pass before use.
- **The simulation harness (`internal/scenarios/`, which includes `internal/scenarios/sim/`)
  passes the gates production code passes:** the checkpoint gate, the plan-dispatchability review
  and the post-sprint review. No plan exempts harness changes from them (probe plans: "Probe
  work").

### Probe work

A probe is work whose purpose is integration data; a probe plan is a plan made of probe work, and a
probe run is a run whose purpose is integration data.

- Probe work runs on a throwaway branch, never on `main` or an implementation branch.
- A system defect found during probe work is recorded in DEFECTS.md, in the entry form its header
  states, and is not fixed on the probe branch.
- Probe runs use mock inference only: no live model and no live embedder. The live-inference and
  live-embedding sim modes stay available to every run that is not a probe run.
- **A probe plan is exempt from the checkpoint gate and the post-sprint review.** Its packages gate
  only on the runs that make its data trustworthy, plus `just test-run` for any production package
  they add a file to; its review is a spot check that the data it gathers measures what it claims.

### Plan work rating and dispatch

- Every plan rates each dispatch **mechanical** or **expert**.
  - The rating decides the model: a mechanical dispatch runs on a stepped-down model, as the
    operator's global instructions specify (`~/.claude/CLAUDE.md` "Step-down dispatches"); an
    expert dispatch runs on the seat's pinned model (`~/.claude/CLAUDE.md` "Subagent handling",
    "Coder dispatches").
  - Rate what the dispatch must decide, not how much it types.
    - mechanical: a package whose work is settled by the plan's own text — a rename, a
      transcription, a test that mirrors a stated rule.
    - expert: one that must choose between defensible options, diagnose behaviour, or write
      model-facing prose.
- Coder packages that share a build run one at a time in the shared working tree; every package
  that touches Go code shares `just build`. No dispatch uses a git worktree — neither
  `git worktree add` nor a worktree-isolated agent. For a baseline, read the committed side with
  `git show HEAD:<path>`.

### Plan dispatchability

A work package is dispatchable only when it:

- names its scope in files, packages or symbols — never a span of design sections;
- carries the command and its answer, or the quoted line, for each fact it acts on;
- if it changes a type, a signature or a test double, names in its scope every file that references
  it — `git grep -l '<Name>' -- '*.go'`, test files included — and carries that command and its
  answer;
- states its done-line in runner targets;
- has received a usability review by the prompt engineer (its definition contains the criteria);
- has been revised by the architect on the prompt engineer's findings.

A plan whose packages lack these is not dispatchable. It is not offered for approval as a dispatch
plan, and none of its packages is dispatched.

## Language constraints

- **Memory service: Go.** Stdlib-first; a dependency enters only under "Dependencies" below.
- **Plugin: TypeScript, in `opencode-plugin/` and nowhere else.** It takes no package dependency:
  it imports only what the opencode host supplies (ARCHITECTURE.md "Plugin route").
- **Auxiliary scripts: Python, stdlib only.** No `pip`, no virtualenvs, no lockfiles, and no
  exception for one small dependency. The shipped Go binary never depends on Python; Python may
  generate committed or derived fixtures through a runner target.

## Repo state

**Anti-reaccretion doctrine.** CONVENTIONS.md is rules and guidance for agents — **state has no
business in it.** What the system currently does is stated by SPEC.md and ARCHITECTURE.md; history
lives in `git log`. A sentence describing what the system *does* or *now has*, rather than what an
agent *must* or *must not* do, belongs in those documents: move it, don't append it here. A rule's
origin is a commit pointer, not inline prose.

The package layout is ARCHITECTURE.md "Module map". Future work is ROADMAP.md.

**Versioning & acceptance (read before judging "done").** The version lines and the home's on-disk
format are defined in SPEC.md §9.1. Their current values live in `internal/version` and are never
restated in prose; `personant version` prints them. The substrate line is semver; its MINOR bumps
are developer fiat at the milestone gates.

**Open:** U6 — whether `version.FrontEnd`, its bump contract and `just test-fe` are retired or
repurposed for the plugin.

Never report acceptance from a green run alone: an acceptance claim states its coverage (simulated,
modeled, parked) as SPEC.md §9.1's honesty clause requires.

## Build / test

Run `just` (default recipe `info`) for the current recipe list; the target inventory lives in the
justfile.

**Two gates — do not conflate them.** `just test` (full `go vet` + `go test` across the Go
packages, including the sim package's multi-day mock rungs — minutes of wall-clock) is the
**CHECKPOINT GATE**: run it **once, before committing a checkpoint**. It is **not** an edit gate;
never re-run the full suite between edits. To verify an edit landed, use the **EDIT GATE**:
`just build` plus `just test-run <pkg> <regexp>` for only the touched tests. **Commit at meaningful
checkpoints** — a complete, self-consistent change — not per edit, and run the checkpoint gate once
at that point. Probe plans gate as "Probe work" states.

**Docs stay current.** A checkpoint commit brings README.md up to date along with the contract
documents. ROADMAP.md is updated when an item ships or is superseded. A defect fix removes its
DEFECTS.md entry in the same change.

## House rules for agents

- **Substrate constraints are non-negotiable and live in ARCHITECTURE.md.** Read its "Substrate
  non-negotiables" before touching storage, persistence or derived files.
- **Dependencies.** General dependency discipline: ARCHITECTURE.md "Approved-when-earned
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
    serve only `internal/tools/web/`. Do not reintroduce `github.com/peterh/liner` or any other
    library that takes the process's terminal fds, termios or `SIGWINCH` handling: `internal/term/`
    owns the terminal (origin: commit c1ddea2). Both entries go in the change that removes their
    consumer.
- **Application code talks to `MemoryOps`, not to the substrate directly.** Package-level detail
  and the substrate-free exceptions: ARCHITECTURE.md "Port-and-adapter at the substrate boundary".
- **All clock reads go through `internal/clock`.** Direct reads of the `time` package clock
  (`time.Now`, `time.Since`) are forbidden outside `internal/clock`. Use `clock.Timeline()` for
  simulated-world timestamps and `clock.Profiling()` / `clock.Since()` for real-time and latency
  measurement.
- **Match the data model.** Spine records, thread frontmatter, the symbol index, project metadata
  and log events have field-level formats in ARCHITECTURE.md "Storage and record formats" and
  "Event log format"; directive files in SPEC.md §2.6. Don't invent your own.
- **Serialization format: legible to whoever is debugging.** A human or agent chasing a problem
  reads the file raw. JSON is the format to resist — quote noise, no comments, nesting tracked by
  brace-counting. A new file, record stream or command output takes:

  | Shape | Format |
  |---|---|
  | flat records, one per line, sorted or append-only | JSONL — the line is the record: `grep` returns whole records, `git diff` isolates the changed one, id order stays stable |
  | nested, or read as a document (command output, configuration, frontmatter) | YAML when tree-shaped (deep nesting, nulls, top-level lists); TOML when flat, hand-edited configuration |

  Machine-emitted YAML decodes into typed structs and quotes string values on emit. A format
  outside this mapping states its reason in the change that introduces it.
- **The opencode source clone (`../opencode`) is read-only.** Never edit it and never move it off
  the installed release's tag; it moves only in the change that updates the `sim-opencode` version
  check (ARCHITECTURE.md "Plugin route", "Pinned release").
- **Open:** U11 — whether to adopt the rule: never place external state (an agent's data home,
  telemetry) inside `~/.personant/` or a test home, because crash recovery quarantines untracked
  files. opencode's data home is already excluded (ARCHITECTURE.md "Single spelunking location").
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
- **Free-API citizenship is a hard requirement** (origin: commit 8e62cb4). Code that talks to a
  free, volunteer- or donor-funded API complies with all of that service's etiquette and required
  behaviors:
  1. **Read the service's published policy before coding against it**, and record its specific
     obligations in the code's doc comment.
  2. **Descriptive User-Agent with contact info**, sourced from configuration — never compiled-in
     personal data. Code for a service that requires contact info refuses to run without it
     configured, with a message naming the setting.
  3. **Serial requests per host**, plus any service-specific minimum spacing.
  4. **Honor `429`/`503` `Retry-After`**: one retry after the stated delay, then a clean error.
  5. **A local cap on requests per run**, so a loop cannot turn one request into unbounded upstream
     load.
- **`.md` files wrap at 100 columns** — the project documents (THESIS.md, SPEC.md, ARCHITECTURE.md,
  CONVENTIONS.md, ROADMAP.md, DEFECTS.md, READMEs), the planning documents (ACTIVE_PLAN.md,
  everything in `ROADMAP_PLANS/`), and every template (`.tmpl.md`). Prose and list items break at
  100, list continuations indented under their bullet. Headings, table rows and URLs that cannot
  break stay whole.
