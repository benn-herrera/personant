# Personant — agent orientation

This file is for AI agents (Claude Code, etc.) working in this repository.
Read it before making non-trivial changes. Human-facing project info is in
[`README.md`](README.md).

## Founding tenet

> **The best use of AI *by* humans is use of AI *with* humans.**
>
> The human must not abdicate their participation and impose all work
> upon the AI. The best outcome requires a fully collaborative
> engagement, with their respective strengths interleaved like the
> braided strands of a multi-material, high-performance cable.
>
> Adherence to this principle results in an enhanced and growing
> symbiotic pair rather than an atrophied parasite clinging to a stunted
> host.

This is the highest-level frame personant operates under; the
architectural thesis below and every design choice that follows
derive from it. Concretely, this means:

- **Deterministic code** does mechanical work (regex, file I/O,
  set math, atomic writes). The LLM is the wrong tool for mechanical
  jobs — expensive, non-deterministic, error-prone where a regex
  would be reliable.
- **The LLM** brings what nothing else can: pan-subject-matter
  expertise, cross-domain pattern recognition, judgment over
  ambiguity informed by broad knowledge. Not grinding — *expertise*.
- **The human** contributes direction, judgment under ownership,
  and personal-experience bridges (the "this emulsion problem
  reminds me of viscosity work in another project" link no static
  system can have).

A proposed feature or change that tries to make the agent
*anticipate* the user (rather than *assist* the user) is suspect.
The same goes for any change that lets the user offload the
judgment that's actually theirs. Personant is not building
agents-gone-wild software with promises of retirement-fund-filling
products obtained via wishful thinking and inchoate dreams.

## Canonical documents

The design is in two markdown files at the repo root. Read them, in this
order, before proposing structural changes:

1. [`ARCHITECTURE.md`](ARCHITECTURE.md) — orientation, principles,
   patterns, mechanisms, anti-patterns, navigation. Compressed
   ~12-minute read covering everything an agent needs to ground
   itself in the project's thinking style. **Read first.**
2. [`SPEC.md`](SPEC.md) — field-level schemas, algorithms, surface
   APIs. The execution-level detail.

If a question of design comes up, ARCHITECTURE.md and the spec are
authoritative. Update them when behavior changes; don't let code and
docs drift.

## Architectural thesis (load-bearing)

Personant bets that **deterministic state as canonical, LLM in narrow
judgment roles, and human acks at high-leverage moments only** scales further
than agent-figures-it-all-out alternatives. This pattern recurs at every
layer; treat any change that would invert it as a red flag.

| Tier               | Role                                                   |
|--------------------|--------------------------------------------------------|
| Deterministic (Go) | canonical state, integrity, build/query/index          |
| LLM                | topic tagging, summary drafting, anchor selection, …   |
| Human              | ack at three moments: closure, recall surface, dissect |

Two warning signs that a proposed change is wrong:

- It pushes canonical state into the LLM.
- It removes a human ack at one of the three load-bearing moments.

## Substrate non-negotiables

- **Storage is text in git.** Inspectability, history, and recoverability are
  the point. JSONL for structured records, markdown for thread bodies and
  directive files. Sorted deterministically (by ID) so diffs stay
  record-grain.
- **No SQLite as canonical state.** It's not the substrate; it can be a
  derived index later if scale forces it, but the canonical form is text.
- **Drift cannot accumulate.** Derived files (`symbols.jsonl`, project
  digests) are regenerable from canonical sources. `internal/autogit`
  enforces the gate inline: state-changing git ops in the home tree
  declare `CheckDerivedFresh` as a post-flag, failing the op on stale
  derived files. No git pre-commit hook is installed.

## Language constraints

- **Runtime: Go.** Single-binary delivery, no host runtime dependencies.
  Goroutines fit the architecture (background curator + foreground turn
  handler). Stdlib-first; bring deps only when they earn their keep.
- **Auxiliary scripts (where used): Python stdlib only.** No `pip`, no
  virtualenvs, no lockfiles. `json`, `re`, `pathlib`, `argparse`,
  `subprocess`, `urllib` cover the use cases.

The Go runtime never has a Python dependency surface; Python utilities never
accumulate a pip dependency surface.

## Repo state

**Versioning & acceptance (read before judging "done").** Two
independently-versioned tracks: the **substrate** (this runtime + the
`MemoryOps` API; currently **v0.1.0**) and the **front end** (U/X +
feature logic; currently **v0.0.1**, REPL-closed but untested by direct
human means). The four-month (120-day) simulation gate converges the *substrate*
toward **v0.5.0** — and it is a *realism-convergence* gate, not a
one-shot pass: "done" means the full top-rung span with **every identified realism
element accounted for** (simulated, OR modeled-and-attempted with unit
tests, OR honestly parked as a known-unknown pending real-use data),
converging to a run that surfaces no new gap. A green sim run on a tidy
workload is *form*, not *function* — do not report acceptance on it. The
honesty clause is binding: state coverage (simulated vs. modeled vs.
parked); a forgotten realism element is the failure, a documented
deferral is not. Normative definition: **SPEC.md §9.1**. At substrate
v0.5.0, work switches to front-end logic (which then earns its own
v0.1.0 via a human U/X phase and supplies the empirical data that closes
parked substrate known-unknowns).

Complete:
- Phase 1: skeleton + storage scaffold.
- Phase 2: turn loop, topic-tag parsing, chat REPL, layered working-set
  composition, scenario harness, mid-turn thread re-prompt (§5.5).
- Phase 3: opportunistic recall via symbolic Jaccard (within-project,
  log-only surface; cross-project + UI deferred until Phase C drives
  the parameter calibration).
- Phase A: `MemoryOps` port + `FileAdapter`; application layer
  (turn / chat / cmd / scenarios) migrated to depend on the port.
  `internal/autogit` (go-git-backed wrapper) handles autonomic
  git operations on the home tree.
- Phase B: transient-data lifecycle. Source-driven retention class
  on every delta; task-class symbols stage cross-turn instead of
  polluting coalesce; decision-class citation promotes; window-close
  GC evicts uncited entries at turn K+1.
- §3.4 layer-2 embedding recall (runtime). `model.Embedder` +
  provider `embeddingModel`; `recall.ProposeEmbedding` cosine
  matcher; a session-scoped in-memory thread-embedding index
  (`State.BuildEmbeddingIndex`); turn close logs
  `spine.embed-match-fire` alongside symbolic `spine.match-fire`.
  Opt-in per provider, graceful symbolic-only fallback. Motivated
  by the C.6 finding that symbolic Jaccard recall collapses under
  vocabulary drift — spec §3.4 was rewritten from a Jaccard-pre-filter
  cascade to embedding-primary parallel signals.

In progress:
- Phase C: recall-fidelity test infrastructure. Six-step plan in the
  `project_personant_recall_fidelity_design.md` memory.
  - **C.1 (done):** harness extension —
    `Step.ExpectedRecallMatches []string` + `recall_fidelity_*`
    precision/recall/F1 histograms and measured/unmeasured-step
    counters in the §9.4 metrics blob; strict-set assertion via
    `t.Errorf` on mismatch.
  - **C.2 (done):** mad-libs query generator. Hand-crafted topic
    templates + `generate.py` (Python stdlib, seeded) under
    `internal/scenarios/testdata/recall_madlibs/`. Output
    `queries.json` is a derived artifact — `.gitignore`'d,
    regenerated by `make recall-madlibs` (a `make test`
    dependency). `TestScenario_RecallMadlibs` drives each query as
    an isolated scenario; absent artifact → `t.Skip`.
  - **C.3 (done):** adversarial templates. `Step.RecallMode`
    (`RecallStrict` / `RecallMeasureOnly`); measure-only steps
    record `recall_fidelity_adversarial_*` and never fail the test.
    Template schema gained `topic` + `mode`. Four adversarial
    templates: vocabulary-drift, stop-word leak, and a false-friend
    topic pair (`macro-economics` / `monetary-policy`, 4 shared
    anchors). `TestRecallMadlibs_AdversarialBehavior` locks in the
    documented per-probe behavior.
  - **C.4 (done):** Wikipedia corpus pipeline.
    `test/tools/wikipedia_corpus.py` (stdlib-only) mines a curated
    152-article seed list (`wikipedia_corpus_articles.txt`, 8
    related domains × 19), chunks by section, labels by
    article/section/domain, and vendors a snapshot to
    `internal/scenarios/testdata/corpus/corpus.json` (~5 MB, 1090
    fragments). The snapshot is *committed* — Wikipedia is a
    non-reproducible source; the mining is a one-off, not a
    deterministic regen. `make recall-corpus-fetch` is the
    heavyweight, networked refresh op — NOT part of `make test`.
  - **C.5 (done):** LLM-assisted mad-libs template authoring.
    152 corpus templates (one per Wikipedia article, 8 domains),
    Sonnet-authored in batches and human-curated, under
    `corpus_templates/`. Recipe: 5 columns of 4 tight
    interchangeable synonyms (canonical term first = the stored
    anchor), `measure-only`. The corpus-backed scenario tests
    ALWAYS COMPILE (part of the `make test` compile); EXECUTION is
    runtime-gated on the `PERSONANT_CORPUS_TESTS` opt-in (set by
    `make recall-corpus-test`) — without it they skip, so they are out
    of the `make test` run. Hand-crafted
    C.2/C.3 templates and corpus templates generate separate
    `queries.json` / `corpus_queries.json` derived artifacts.
  - **C.6 (done — initial sweep):** calibration sweep. The corpus
    recall measurement was rebuilt to call `recall.ProposeFromIndex`
    directly (was driving the full scenario harness per query —
    ~78 min / timeout; now <1 s). The generator gained
    `--synonym-depth M`; `make recall-corpus-sweep-data` emits
    `corpus_queries_m1..m4.json`. `TestRecallMadlibs_CorpusCalibration`
    sweeps M ∈ {1..4} × threshold ∈ {0.2..0.6} into a metrics matrix.
    Findings: M=1 (zero-drift) recall 1.0 at every threshold; recall
    collapses with drift and threshold (M=4/T=0.4 → 0.098);
    thresholds 0.3 and 0.4 are equivalent operating points (discrete
    Jaccard score gaps); precision stays ≥0.98 for T ≥ 0.3.
    Still open, deferred until the data demands it: enriching the
    stored-thread symbol set beyond 5 anchors, and moving recall off
    symmetric Jaccard to an asymmetric overlap coefficient.
  - **C.6 embedding head-to-head (done):** `nomicai-embed` (via the
    `reaper` provider, non-metered) embedding-recall measured against
    the same corpus. `test/tools/embed_corpus.py` →
    `embeddings.json` (gitignored, `make recall-embed-data`);
    `TestRecallMadlibs_EmbedCalibration` sweeps M × cosine threshold.
    Finding: embedding recall is drift-ROBUST — ~0.97 recall at M=4
    where symbolic Jaccard collapses to ~0.10 — but precision-poor at
    the high-recall thresholds (~0.21 at cos 0.45). The two are
    complementary: Jaccard = high-precision/low-recall, embeddings =
    high-recall/lower-precision. Validates the §3.4 layered-recall
    design. `TestRecallMadlibs_EmbedRanking` reframes it as ranking:
    the true topic is the #1 cosine match ~73% of the time, top-3
    ~92%, MRR ~0.83 — and drift-INVARIANT (M=1 ≈ M=4). The
    threshold-matrix "precision problem" was the top-10-above-cutoff
    candidate policy, not a ranking weakness; a top-1/top-3 recall
    policy on embeddings is strong.
- Anchor-lifecycle redesign (SPEC §2.2 / §2.7.4 / §3.4 / §5.1):
  evolving anchors. `anchors` is a deterministic re-derived *projection*
  of active `history_symbols` (top-`AnchorProjectionMax`=8), not a
  frozen birth certificate; 0-anchor vague threads are legal; the
  4-minimum floor is deleted; a symbol that drops out of the projection
  becomes `superseded` and is **retained, not evicted**, protected from
  capacity eviction by an `ever_central` latch (abandoned premises stay
  findable). Shipped in increments (schema → projection + lifecycle
  state machine + idempotent-write guard → lifecycle-aware scorer →
  contract loosening + SPEC deltas → coupled sim workload + metrics) and
  validated across the acceptance ladder.

Queued:
- Phase 4: closure / retirement (curator-drafted summaries; ack flow;
  spine state transitions).
- Phase 5: cross-project digest, fallback dissection, directive
  accrual.
- Startup recovery after unclean shutdown (v0.1 substrate requirement,
  SPEC §4.5.8): reconcile/rebuild stale derived state on open after a
  crash; exercise the normal shutdown→resume cycle. Distinct from
  §3.8 archival recovery.
- Realism-convergence backlog (SPEC §9.1): the open list of realism
  elements gating substrate v0.5.0 — within-thread topic interleaving
  (non-monotonic threads; in progress), thread-as-synthesis, the Lens-B
  gaps, transient-data fidelity, and inference-/embedding-in-loop
  coverage (see below).
- Inference-/embedding-in-loop simulation (SPEC §9.1): wire real
  `reaper.local` inference (gemma-4 family) and embedding
  (`nomicai-modernbert-embed-base-bf16`) into the acceptance sim,
  individually configurable, to close the coverage gap left by the
  mock LLM / nil-embedder regime (notably embedding-primary §3.4 recall,
  which the symbolic-only sim cannot exercise). Transitional + needs an
  empirical sweet-spot hunt: per-turn cost rises from ~10s of ms to
  single-digit seconds, trading away the ~20-min/4-month iteration
  budget, so the goal is max coverage/rigor per unit per-turn overhead.
- v0.2: deep cold archival via git; working-set content dedup;
  shell escape `$`/`#` with long-lived subprocess; transient-data
  event-log compaction + class-aware tool-output budget.
- v1.0: Python computational workflow (math/physics simulation).

Current top-level shape:

```
cmd/                        cobra subcommands (init, index, verify,
                            ping, models, chat — bare `personant`
                            defaults to chat)
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
                            (substrate-free, memops domain model only)
internal/recall/measure/    application-side recall stack (Service,
                            Recaller) — uses memops port for substrate
                            access; composes scoring primitives
internal/curator/           closure-summary drafting (§3.5/§5.2):
                            model-backed summary + anchor selection
internal/chat/              REPL, slash dispatch, bootstrap UX
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
AGENTS.md                   this file
Makefile                    build + agents-submodule pinning +
                            serve-local-api
```

## Build / test

```sh
make build            # bin/personant
make test             # go vet + go test ./... --count=1
make integration-test # live reaper embedder/recall tests (opt-in)
make sim              # acceptance rung-walk (mock); LIVE_EMBEDDING=true / LIVE_INFERENCE=true for live-mode
make recall-corpus-test # corpus recall-fidelity measurement (opt-in)
make clean
```

Go 1.26.1+.

Slow / live tests use a **runtime opt-in**, not build tags: they always
compile (so a refactor that breaks them fails `make test`), and gate
EXECUTION at runtime — they `t.Skip` unless their opt-in is present, so
bare `make test` compiles and skips them. The opt-ins live in
`internal/testsupport`: `PERSONANT_LIVE_TESTS` (live reaper endpoint;
under it an unreachable endpoint is a FAILURE, not a skip),
`PERSONANT_CORPUS_TESTS` (slow corpus measurement), and the
`-sim.live-embedding` / `-sim.live-inference` flags (live sim). The
`make` targets above set the right opt-in. Do not reintroduce
`//go:build` tags for conditional execution — tagged tests are excluded
from the normal compile and bit-rot silently.

**Ensure Docs Stay Up To Date** - AGENTS.md, README.md, ARCHITECTURE.md, SPEC.md must be brought up to date when committing checkpoints.

## House rules for agents

- **Be deliberate about dependencies.** Two categories:
  - **Permanently out** (architectural conflict with the substrate
    non-negotiables): `langchaingo/memory`, `langchaingo/chains`,
    `mattn/go-sqlite3`. Do not reintroduce.
  - **Approved-when-earned** (compatible; pull in *with their consumer*,
    not before): `github.com/BurntSushi/toml` (consumer: `providers.toml`
    loader); `gopkg.in/yaml.v3` (consumer: thread frontmatter writes);
    `github.com/go-git/go-git/v5` (consumer: `internal/autogit/` for
    autonomic git operations on `~/.personant/.git/`; landed during the
    gitops/policy refactor). Do not pull these in speculatively; do
    pull them in when the consumer arrives.
  - **`langchaingo/llms`** is *compatible* but excluded on dep-hygiene
    + scope grounds — see substrate-decisions memory for the revisit
    conditions.
- **Application code talks to `MemoryOps`, not to the substrate directly.**
  `internal/turn`, `internal/chat`, `internal/recall/measure`, cmd/*, and
  the scenarios harness all depend on the port (`memops.MemoryOps`).
  Pure renderers (`internal/workset.Compose`, `internal/recall/scoring`)
  are substrate-free and operate on plain types pre-fetched by the
  adapter — they import neither the port nor the substrate. Pure helpers
  (`store.Normalize`, `store.DominantSource`) and data-type aliases stay
  direct because they're not substrate operations. Tests inspect the
  substrate directly via `h.Paths` in invariants — that's the test-side
  substrate validator pattern; it's the right access.
- **All clock reads go through `internal/clock`.** Direct reads of the
  `time` package clock (`time.Now`, `time.Since`) are forbidden outside
  `internal/clock`. Use `clock.Timeline()` for simulated-world timestamps
  (overridable by the acceptance simulation) and `clock.Profiling()` /
  `clock.Since()` for real-time and latency measurement.
- **Match the spec's data model.** Spine records, thread frontmatter, and the
  symbol index have field-level schemas in §2. Don't invent your own.
- **Verify with the canonical docs.** Before assuming a behavior, grep
  ARCHITECTURE.md and the spec. If they're silent, surface it as an open
  question rather than guessing.
- **Auxiliary Python is stdlib-only.** No exceptions for "just one little
  dependency".
