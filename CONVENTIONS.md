# CONVENTIONS – Personant

This file is for AI agents (Claude Code, etc.) working in this repository. Read it before making
non-trivial changes. Human-facing project info is in [`README.md`](README.md).

## Thesis (load-bearing)

THESIS.md is the frame every change here answers to; ARCHITECTURE.md's "Applying the thesis" turns
it into design questions and warning signs. Read both before a non-trivial change. These are red
flags — stop and surface them rather than proceeding:

- a change that inverts the thesis or trips one of those warning signs;
- a feature that makes the agent *anticipate* the user rather than *assist* the user;
- a change that lets the user offload judgment that is actually theirs.

## Language constraints

- **Runtime: Go.** Single-binary delivery, no host runtime dependencies. Goroutines fit the
  architecture (background curator + foreground turn handler). Stdlib-first; bring deps only when
  they earn their keep.
- **Auxiliary scripts (where used): Python stdlib only.** No `pip`, no virtualenvs, no lockfiles.
  `json`, `re`, `pathlib`, `argparse`, `subprocess`, `urllib` cover the use cases.

The Go runtime never has a Python dependency surface; Python utilities never accumulate a pip
dependency surface.

## Repo state

**Anti-reaccretion doctrine.** CONVENTIONS.md is rules and guidance for agents — **state has no
business in it.** What the system currently does is stated by `SPEC.md` and `ARCHITECTURE.md`;
history and wave narrative live in `git log`, not here. If you're about to add a sentence describing
what the system *does* or *now has* rather than what an agent *must/must not do*, it belongs in
those documents, not here — move it, don't append it. A rule's origin is a commit pointer, not
inline prose.

**Versioning & acceptance (read before judging "done").** Three version lines, normatively defined
in **SPEC.md §9.1**; the current **values live in `internal/version`** and are never restated in
prose — a prose copy is a second definition that goes stale on the next bump. `personant version`
(and `--version`, and the REPL's `/version`) prints the live ones.

- **substrate** (`version.Substrate`) — this runtime + the `MemoryOps` API.
- **front end** (`version.FrontEnd`) — U/X + feature logic. Current feature set: `SPEC.md` §4.
- **home on-disk `format`** (`version.CurrentHomeFormat`) — a plain integer in
  `<home>/version.toml`, deliberately NOT semver: a layout is either readable by a binary or it is
  not. A home written by a NEWER binary is **REFUSED** at the process boundary, and the gate runs
  **before** Reconcile (running crash recovery against a layout this binary cannot interpret is
  exactly the wrong move). `--allow-newer-home` overrides the refusal at the user's risk, leaving a
  loud stderr warning plus a `system.home-format-override` event.

**Front-end bump contract (binding when you change the front end).** During the living-with phase,
**every front-end (CLI/REPL/UX) feature change bumps `version.FrontEnd`'s PATCH digit** unless the
developer specifies otherwise. MINOR bumps are developer fiat at the milestone gates. The two semver
lines move independently: a substrate-only change does not touch `FrontEnd`, and a front-end-only
change does not touch `Substrate`.

The four-month (120-day) simulation gate converges the *substrate* toward **v0.5.0** — and it is a
*realism-convergence* gate, not a one-shot pass: "done" means the full top-rung span with **every
identified realism element accounted for** (simulated, OR modeled-and-attempted with unit tests, OR
honestly parked as a known-unknown pending real-use data), converging to a run that surfaces no new
gap. A green sim run on a tidy workload is *form*, not *function* — do not report acceptance on it.
The honesty clause is binding: state coverage (simulated vs. modeled vs. parked); a forgotten
realism element is the failure, a documented deferral is not. Normative definition: **SPEC.md
§9.1**. At substrate v0.5.0, work switches to front-end logic (which then earns its own v0.1.0 via a
human U/X phase and supplies the empirical data that closes parked substrate known-unknowns).

Future work: `ROADMAP.md`'s Near-term / Mid-term / Far-horizon tiers.

Current top-level shape:

```
cmd/                        cobra subcommands (init, index, verify,
                            ping, models, chat, version — bare
                            `personant` defaults to chat). Persistent
                            flags: `--home`, `--allow-newer-home`
                            (§9.1 newer-home override); `--version`
                            prints the short identity line. Every verb
                            that opens the substrate goes through the
                            one `openHome` helper (format gate, then
                            Reconcile); `version` is deliberately
                            ungated and never fails on a broken home.
internal/memops/            MemoryOps port: interface + the domain
                            types it trades in (SpineRecord, Thread,
                            ProjectMeta, Provider, Config, the symbol
                            enums, Normalize/DominantSource) — the
                            application layer depends only on this;
                            imports no internal package
internal/memops/fileadapter/  the v0.1 substrate-backed adapter
internal/store/             file substrate: paths, JSONL helpers,
                            init, spine ops, project ops, providers,
                            thread I/O, bootstrap — depends on memops
                            for the domain types
internal/autogit/           go-git-backed autonomic git wrapper with
                            bitflag systemic-validation policy
internal/turn/              §3.0 chain, turn loop, per-turn coalescing,
                            transient-data staging buffer, §3.4 recall
                            surface, §3.5 decay-triggered closure flow
internal/recall/scoring/    pure Jaccard/cosine recall primitives
                            (substrate-free, memops domain model only);
                            chunk-tier: chunk.go (ProposeChunks,
                            relevance-net cap), descend.go
                            (DescendChunks beam descent, SummaryNode,
                            exemplar-set keys), cluster.go (BuildTree,
                            ExemplarSummarizer, agglomerative
                            clusterByCosine)
internal/recall/measure/    application-side recall stack (Service,
                            Recaller) — uses memops port for substrate
                            access; composes scoring primitives; async
                            single-indexer goroutine + atomic.Pointer
                            snapshot; .recall-cache sidecars: veccache.go
                            (.vec per-thread vectors), treecache.go (.tree
                            per-thread summary trees); sleep-cycle tree
                            builder: treebuilder.go (RebuildTrees /
                            buildTreeSnapshot); W1 divergence probe:
                            intraThreadDivergence / classifyW1Divergence
internal/curator/           closure-summary drafting (§3.5/§5.2):
                            model-backed summary + anchor selection
internal/chat/              REPL, slash dispatch, bootstrap UX
internal/term/              sole owner of the terminal: fds, mode, the
                            single reader, every emitted byte, and the
                            ownership state. Four output channels
                            (Out/Diag/Reasoning/Status), one read path.
                            internal/chat keeps policy only. See
                            mad-design/terminal-layer/
internal/tools/             §6.1 tool registry + dispatch + the shared
                            arg-decoder and turn-context seam
                            (substrate-free). The turn-side execution loop
                            lives in internal/turn/toolloop.go
internal/tools/web/         §6.1.1 web.fetch + web.search + the query tools
                            (wikipedia, wiktionary, wikidata, arxiv, crossref;
                            wikimedia.go holds the REST family's shared path,
                            decoder and renderer):
                            readability → Markdown extraction, empty-shell
                            detection, the SearchProvider seam + Exa backend,
                            local query caps, and politeness.go — the ONE
                            free-API citizenship layer (contact-bearing UA,
                            per-host serial gate + spacing table,
                            Retry-After)
internal/shell/             §4.4 shell escape: per-command `$SHELL -c`,
                            shell-cwd tracking via the fd-3 cwd epilogue,
                            bounded capture, §8.2.1 key redaction
internal/workset/           layered context composition (E/A1/A2/B/C)
internal/prompt/            template + topic-tag parser + stream filter
internal/model/             OpenAI-compatible HTTP client + scripted/
                            generated mock + SSE streaming
internal/scenarios/         scenario harness, invariants, metrics
internal/{eventlog,metrics,
  index,verify,clock,
  ping,modellist,log}/      supporting subsystems
ARCHITECTURE.md             orientation (read first)
SPEC.md                     operational spec
README.md                   user-facing
CONVENTIONS.md                   this file
ROADMAP.md                  future intent, outside the precedence chain
justfile                    build + test + dependency-vetting recipes
```

## Build / test

Go 1.26.1+. Run `just` (default recipe `info`) for the current recipe list with docstrings — the
target inventory lives in the justfile, not here.

**Two gates — do not conflate them.** `just test` (full `go vet` + `go test` across GOPKGS,
*including* the sim package's multi-day mock rungs — minutes of wall-clock) is the **CHECKPOINT
GATE**: run it **once, before committing a checkpoint**, to catch cross-package regressions. It is
**not** an edit gate. Re-running the whole suite between edits while iterating is the failure mode
that turns a one-line fix into an hour of mostly-irrelevant testing — don't. To verify a specific
edit landed, use the **EDIT GATE**: `just build` (compile) plus `just test-run <pkg> <regexp>` to
run only the touched test(s). Breaking the compound suite down to the relevant test is the correct
tactic during iteration; the full suite is the final pre-commit checkpoint, not a per-edit reflex.
**Commit at meaningful checkpoints** — a complete, self-consistent change — not per-edit, and run
the checkpoint gate once at that point. (`just test` is still NEVER substituted by a raw `go test`;
the slow/live opt-in tests stay gated either way.)

**Intra-thread recall metrics (#109/#111).** The sim emits the following at every rung; the W1
divergence is a reported quality measure (not build-blocking, #119):

*Counters (m.Counters):*
- `recall_intra_descent_divergence` — run-total top-Kf leaf set difference between summary-tree
  descent and flat scan over every W1 probe on a live-embedding run. **REPORTED QUALITY MEASURE, NOT
  A GATE (#119):** the within-thread summary tree is an approximate O(log n) recall heuristic; a
  nonzero means it substituted a within-top-Kf leaf, NOT necessarily lost recall (see the
  classification below). Exact/exhaustive recall is the separate exact tier (#117 grep + flat-scan),
  not this approximate path. Logged as an approximation-drift canary.
- `recall_intra_descent_probes` — denominator for the above (number of W1 probes evaluated;
  distinguishes "gate passed" from "no probe ran").
- `recall_intra_w1_strict_miss` — of the divergent probes: a strictly-better leaf was pruned (real
  recall loss; fix keys or beam).
- `recall_intra_w1_tie` — of the divergent probes: an equal-cosine leaf was substituted
  (tie-boundary, not a recall loss; fix tie-break).
- `recall_intra_w1_tree_mismatch` — of the divergent probes: the flat scan ranked a leaf the tree
  does not contain (staleness/build edge).
- `recall_intra_tree_rebuild_calls` — within-thread summary-tree (re)builds fired across sleep
  cycles; F-B trip-wire (watch for unbounded growth with main-thread length, escalates to the
  hybrid-rebalancing MAD).

*Gauges (m.Gauges):*
- `recall_intra_hop_recall` / `recall_intra_embed_hop_recall` — symbolic (predicted) and embedding
  (observed) intra-thread recall by hop distance; the #109 fidelity curve. Reported, not gated to a
  floor — the curve is the deliverable.
- `recall_intra_recall_bydepth` / `recall_intra_embed_recall_bydepth` — same recall, bucketed by
  TURN-DEPTH (currentTurn−targetTurn, log-scale powers of B=16) keyed `_d<bucket>` (+`_obs`); the
  multi-year-thread no-decay axis of the #109 curve. Symbolic on every run, embedding-observed
  live-only. Report-only — no gate, no floor.
- `recall_intra_coherence_divergence` — symbolic oracle vs. runtime intra fine-tier disagreement;
  **HARD gate == 0 on mock runs** (mock embedder → symbolic oracle valid); report-only on
  live-embedding runs (symbolic oracle ≠ embedding recall by design, #96). Per SPEC §3.4 there is
  **no debt-window dead zone**: the oracle asserts recall across the FULL scrolled-out range
  (including the recent tail), so any divergence is a real defect — never tolerated async-flush lag.
  The former `recall_intra_blindspot_misses` metric and the "by-design lag" carve-out are retracted
  (#124): the runtime keeps debt-window content findable continuously via the bounded lexical
  completeness floor (#123), so the oracle no longer excludes any scrolled-out probe target.
- `recall_query_cosine_ops` — measured per-query cosine comparisons (coarse + fine population +
  engaged-thread descent or flat scan); the headline perf-bend metric.

**W1 recall-preservation QUALITY MEASURE (#111 §7.1; reframed from gate→measure in #119):**
`recall_intra_descent_divergence` is a reported approximation-drift canary, **not a build-blocking
gate**. The within-thread summary tree is an approximate O(log n) recall heuristic that trades
exactness for speed; a nonzero divergence means the heuristic substituted a within-top-Kf leaf, NOT
necessarily that recall was lost (a `strict_miss` is a real ranking defect worth raising the beam
for; a `tie`/`tree_mismatch` is a sub-perceptible boundary effect — see the classification
counters). Exact/exhaustive recall is the job of the separate **exact tiers (#117 grep +
flat-scan)**, where it is guaranteed; holding the approximate tree to exact descent-vs-flat
set-equality was stricter than its own purpose (recall-correctness). The value is still LOGGED on
every rung as a drift canary. There is NO runtime rebalance trigger: tree re-clustering is a
sleep-time operation only (the #108 consolidation cycle's `RebuildTrees`, on staleness), so the
measure informs nothing at runtime — it is purely observed.

**Ensure Docs Stay Up To Date** - CONVENTIONS.md, README.md, ARCHITECTURE.md, SPEC.md must be
brought up to date when committing checkpoints. ROADMAP.md tracks future intent and is not a
contract document, but keep it current when an item ships or gets superseded.

## House rules for agents

- **Substrate constraints are non-negotiable and live in ARCHITECTURE.md.** Read ARCHITECTURE.md's
  "Substrate non-negotiables" before touching storage, persistence, or derived files.
- **Dependencies — project-specific rules.** General dependency discipline lives in
  ARCHITECTURE.md's "Approved-when-earned dependencies" pattern.
  - **Permanently out** (architectural conflict): `langchaingo/memory`, `langchaingo/chains`,
    `mattn/go-sqlite3`. Do not reintroduce.
  - **`langchaingo/llms`**: compatible but excluded for v0.1 (30+ transitive deps; a thin ~200–300
    LoC OpenAI-compatible HTTP client covers the single-provider need). Revisit if a genuine
    multi-provider need arises, `langchaingo/embeddings` covers the §3.4 embedding-recall path, or a
    future capability costs >500 LoC of custom writing.
  - **Approved-when-earned, landed with their consumer**: `github.com/BurntSushi/toml`
    (`providers.toml` loader); `gopkg.in/yaml.v3` (thread frontmatter);
    `github.com/go-git/go-git/v5` (`internal/autogit`); `codeberg.org/readeck/go-readability/v2`
    v2.1.2 + `github.com/JohannesKaufmann/html-to-markdown/v2` v2.5.2 (`web.fetch`, §6.1.1 — article
    extraction + HTML→Markdown; both MIT, pure Go, no cgo).
  - **Terminal/fd/signal dependencies**: before proposing one, check whether it can be given the
    fds/mode rather than taking them. `github.com/peterh/liner` was removed on exactly this ground —
    a readline-class library owns the process's terminal (hardcoded fds, its own termios and
    `SIGWINCH` handler, private byte reservoirs), conflicting with `internal/term`. Do not
    reintroduce liner. (Full autopsy: commit c1ddea2, wave sequence git log 07e5ccd..c1ddea2.)
  - **Vet adoption and maintenance before proposing a dependency — from the registry, not the
    README.** Record, measured not asserted: (1) last release date; (2) `Imported by` count on
    pkg.go.dev; (3) deprecation status; (4) transitive dep count; (5) license. For a port, verify
    the PORT's activity, not its upstream's. Default to the well-trodden option unless the
    off-standard gain is substantial. (Origin: the go-trafilatura near-miss — better benchmarks, 21
    months stale, 5 importers, nearly chosen over the maintained readability fork above on a
    ~1-point F1 difference; commit 5692c75.)
- **Application code talks to `MemoryOps`, not to the substrate directly.** See ARCHITECTURE.md's
  "Port-and-adapter at the substrate boundary" for the package-level detail and the substrate-free
  exceptions.
- **All clock reads go through `internal/clock`.** Direct reads of the `time` package clock
  (`time.Now`, `time.Since`) are forbidden outside `internal/clock`. Use `clock.Timeline()` for
  simulated-world timestamps (overridable by the acceptance simulation) and `clock.Profiling()` /
  `clock.Since()` for real-time and latency measurement.
- **Match the spec's data model.** Spine records, thread frontmatter, and the symbol index have
  field-level schemas in §2. Don't invent your own.
- **Serialization format: legible to whoever is debugging.** A human or agent chasing a problem
  reads the file raw, so legibility is a requirement. JSON is the format to resist — quote noise, no
  comments, nesting tracked by brace-counting. A new file, record stream, or command output takes:

  | Shape | Format |
  |---|---|
  | flat records, one per line, sorted or append-only | JSONL — the line is the record: `grep` returns whole records, `git diff` isolates the changed one, id order stays stable |
  | nested, or read as a document (command output, configuration, frontmatter) | YAML when tree-shaped (deep nesting, nulls, top-level lists); TOML when flat, hand-edited configuration |

  Machine-emitted YAML decodes into typed structs and quotes string values on emit, so implicit
  typing (`no`→bool, `1.10`→float) cannot bite. A format outside this mapping states its reason in
  the change that introduces it.
- **Verify with the canonical docs.** Before assuming a behavior, grep ARCHITECTURE.md and the spec.
  If they're silent, surface it as an open question rather than guessing.
- **Auxiliary Python is stdlib-only.** No exceptions for "just one little dependency".
- **Spawned processes: self-limiting, or verified dead** (origin: 52 orphaned busy-loops surviving
  overnight, ~50 core-hours, load average 109 — commit 4fcb0e5). Binding on any background process
  an agent starts (load generators, watchers, servers, children of stress harnesses):
  1. **Fail safe, not fail hot.** Every spawned process must bound its own lifetime — a loop with an
     iteration cap, or a `timeout N` wrapper. A bare `while :; do :; done &` has "spin forever" as
     its failure mode, and any cleanup bug converts it into orphaned load.
  2. **Kill by collected PIDs, never `$(jobs -p)`.** Record `$!` at spawn and kill that list. In
     zsh, `$(jobs -p)` expands in a command-substitution subshell whose job table is EMPTY (bash
     special-cases this; zsh does not), so `kill $(jobs -p)` is a silent no-op.
  3. **Verify the kill.** A post-cleanup count probe (`ps ... | wc -l`) returning 0 is part of the
     task's definition of done. Nonzero is a failed gate, not a warning.
  4. **Never silence cleanup stderr.** `kill ... 2>/dev/null` ate the usage error that would have
     exposed the no-op on its first run. A cleanup command's stderr is the only witness when it
     breaks.
  5. **Prefer in-process contention.** `go test -cpu 8,8,...` and `-count` create scheduler pressure
     inside the test process and leak nothing; external burners are a last resort, and carry rules
     1–4.
- **Free-API citizenship is a HARD REQUIREMENT** (commit 8e62cb4). Personant complies with ALL
  etiquette and required behaviors of every free/volunteer/donor-funded API it consumes — we will
  not abuse the generosity of volunteer and donor offerings. Binding consequences for any tool that
  talks to such a service:
  1. **Read the service's published policy before coding against it**, and record the specific
     obligations in the tool's doc comment (e.g. Wikimedia's User-Agent policy REQUIRES contact
     information and is enforced; Crossref's `mailto` routes to the polite pool; arXiv asks ~3s
     between requests).
  2. **Descriptive User-Agent with contact info**, sourced from config — never compiled-in personal
     data. Tools whose service REQUIRES contact info REFUSE to run without it configured, with a
     message naming the config key; silent non-compliance is not a fallback.
  3. **Serial requests per host as a stated guarantee**, plus any service-specific minimum spacing,
     honored via a shared per-host politeness table — one mechanism, not per-tool
     re-implementations.
  4. **Honor `429`/`503` `Retry-After`**: one honest retry after the stated delay, then a clean tool
     error. Never hammer through.
  5. **Local per-tool query caps** so an agent loop cannot convert one user request into unbounded
     upstream load.

- **Partly-planned work that isn't ready to execute lives in `ROADMAP_PLANS/`, not in scratch.**
  `mad-design/` and the repository's other scratch trees are gitignored, so a plan parked there is
  one power-cycle from gone. A plan worth keeping moves to `ROADMAP_PLANS/`, which is tracked. It's
  good practice for such a plan to correspond to a `ROADMAP.md` item, but that is a habit, not a
  rule — nothing requires or checks it.
  - **A plan leaves `ROADMAP_PLANS/` when it is finished, abandoned or superseded — in the same
    change.** A superseding plan first absorbs every section, decision and piece of evidence it
    still relies on, stated as its own, and cites nothing in the plan it replaces; then the old plan
    is deleted. A plan still on disk reads as work waiting to be done.
- **`.md` files wrap at 100 columns** — the project documents (`THESIS.md`, `SPEC.md`,
  `ARCHITECTURE.md`, `CONVENTIONS.md`, `ROADMAP.md`, READMEs), the planning documents
  (`ACTIVE_PLAN.md`, everything in `ROADMAP_PLANS/`), and every template (`.tmpl.md`). Prose and
  list items break at 100, list continuations indented under their bullet. Headings, table rows and
  URLs that cannot break stay whole.
