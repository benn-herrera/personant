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

**Versioning & acceptance (read before judging "done").** Three version
lines, normatively defined in **SPEC.md §9.1**; the current **values live
in `internal/version`** and are never restated in prose — a prose copy is
a second definition that goes stale on the next bump. `personant version`
(and `--version`, and the REPL's `/version`) prints the live ones.

- **substrate** (`version.Substrate`) — this runtime + the `MemoryOps` API.
- **front end** (`version.FrontEnd`) — U/X + feature logic. REPL-closed
  with the dogfood-minimum interactive set now in place (slash commands,
  `liner` line editing + history, the §4.3.3 interrupt/abort keys, the
  §4.3.2 phase-labeled turn progress indicator). Dogfooding is now driving
  it — the progress indicator and the Esc-abort key both landed from
  direct human use.
- **home on-disk `format`** (`version.CurrentHomeFormat`) — a plain
  integer in `<home>/version.toml`, deliberately NOT semver: a layout is
  either readable by a binary or it is not. A home written by a NEWER
  binary is **REFUSED** at the process boundary, and the gate runs
  **before** Reconcile (running crash recovery against a layout this
  binary cannot interpret is exactly the wrong move). `--allow-newer-home`
  overrides the refusal at the user's risk, leaving a loud stderr warning
  plus a `system.home-format-override` event.

**Front-end bump contract (binding when you change the front end).**
During the living-with phase, **every front-end (CLI/REPL/UX) feature
change bumps `version.FrontEnd`'s PATCH digit** unless the developer
specifies otherwise. MINOR bumps are developer fiat at the milestone
gates. The two semver lines move independently: a substrate-only change
does not touch `FrontEnd`, and a front-end-only change does not touch
`Substrate`.

The four-month (120-day) simulation gate converges the *substrate*
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
- Intra-thread recall + within-thread summary hierarchy (#109/#111).
  Chunk-level hierarchical embedding index for the engaged long-running
  thread: coarse→fine with the engaged thread bypassing the coarse gate
  (§4.1 step 3); turn-excerpt retained on disk past the assembly window
  (durable content source fix, Inc 4); async single-indexer goroutine +
  `atomic.Pointer` snapshot + dispatch watermark (I1/I2); persisted
  `.vec` vector cache (O(changed) startup); sim shadow-chunk oracle +
  Candidate-A workload + intra-thread probe. **W1 recall-preservation is a
  reported quality measure, not a gate (#119)** — divergence was 0/251 on
  live 14d after Finding A (exemplar-set propagation fix, d8e32b3 — pool =
  union of every child's full `.Vectors` rather than child primaries; closes
  the structural W1 gap); a later 1m rung showed a within-Kf substitution
  (2/559, strict_miss=1) at 1.000 embedding recall, which reframed W1 from a
  build-blocking gate to an approximation-drift canary (exact/exhaustive
  recall is the separate #117 tier). Finding B
  (intra coherence gate scoped to mock; report-only on live, #96 boundary)
  shipped. Beam width k raised 4→8 (9c9493d), briefly reverted to 4, then
  restored to k=8 (45bae9c — k=4 fails W1 at zero cost savings).
  `.tree` sidecar persisted (treecache.go, Inc C). Sleep-cycle
  `RebuildTrees` builds/reconciles the summary trees offline (Inc D).
  Still open: cost-minimization (re-verify W1==0 at narrower beam after
  exemplar fix confirms the keys carry recall); brute-force O(n) backstop
  for user-asserted-confidence fallback.
- Crash stability / startup recovery (#94, SPEC §4.5.8). Waves **R1–R4
  landed**, **R5 docs** (this pass). The substrate is now a **dual-repo**
  git scheme: a disposable **daily** DB (`.git-daily/`, scoped per-turn
  commits, nuked + reborn each day) and a permanent **primary** DB
  (`.git/`, one day-grain commit/day + archival anchors, ~365
  commits/year). The worktree is the truth; git is a recovery-point
  index over it, so any DB is recreatable from the worktree and losing
  one is never losing content. Recovery is a deterministic cold-start
  state machine (no signal handlers, no LLM, no ack) bounding loss to
  **≤1 turn**: a turn's raw bytes are journaled before any canonical
  write (`turn-journal.jsonl` = the in-flight signal, no separate turn
  marker), torn turns reset **only daily**, barrier/archival crashes
  **roll forward** (never reset the worktree), broken hand-edits are
  **quarantined byte-exact, never deleted**. Batch ops use the
  `op-in-progress.json` marker (`archival|sleep|recovery|barrier|
  rebaseline`); the day barrier (B0–B6) moves integrity gating off the
  per-turn path to once-a-day (spine on B2, derived on B6). Key packages:
  `internal/recovery`, `internal/crashpoint`, `internal/autogit`
  (dual-handle `Primary`/`Daily`), `internal/store` (marker/journal/
  watermark), and the barrier home in `internal/memops/fileadapter`.
  No new `make` target — the crash-injection matrix is unit-grade in
  `internal/scenarios` (runs under `make test`, gated by
  `TestCrashPointCoverageGate`); a 2-day barrier-crossing rung
  (`TestSimBarrier2Day`) runs in the default sim suite. Carry-forward:
  §6.2 mid-day re-baseline **arming** trigger not yet wired (recovery
  side is); intra-day derived-watermark revisit is parked by design.

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
- July 2026 review burn-down (`design/review-burndown-2026-07.md`,
  Waves 1–5): instrument integrity (cross-seam metric-key registry,
  non-vacuous thread accounting, live invariant validators), runtime
  correctness (§3.4 (1+P)×cap completeness floor closing the flush-lag
  dead zone, phantom-engaged-owner fallback, blob-at-`hash:path` aging
  gate sharing one predicate with recovery), concurrency/robustness,
  DRY/dead-code removal, and the docs accuracy sweep (Wave 5). Also
  landed the **dogfood-minimum chat REPL**: the
  `/topic /done /pause /resume /back-to /project rename|switch /thinking`
  slash set, always-log `retire.ack` closure edit-ack with `edited=` logging,
  `liner`-backed line editing + `~/.personant/history`, and the §4.3.3
  interrupt/abort keys: **Ctrl-C ends the session** (at the prompt it is
  `/exit`, silent; mid-turn it abandons the turn first, and a second
  Ctrl-C during shutdown forces immediate exit), while **Esc aborts the
  in-flight turn** and returns to the prompt. Esc runs the terminal in
  **cbreak** (`ICANON`/`ECHO` off, `VMIN`/`VTIME` set; `OPOST` and `ISIG`
  deliberately left ON — raw mode would staircase output and turn Ctrl-C
  into a byte), entered between prompts and restored before the next one.
  The abort window is gated on an allow-list of PRE-CANONICAL turn phases
  so an aborted turn releases its §4.5.8 recovery scope and the next open
  stays quiet. Esc is **retraction, not stop-generating**: the §4.3.3
  retraction rule makes a pre-canonical abort roll the session-volatile
  runtime state (turn counter, §3.10 staging buffer, Layer B/C, history,
  recall marks, embedding debt) back to its pre-turn value —
  `internal/turn/rollback.go`, invariant-tested by
  `TestPreCanonicalAbort_NoSessionResidue` — so a retracted prompt cannot
  shape the next turn. The text comes back only as an editable default at
  the next prompt; the durable trace is one `system.turn-aborted` event
  line, never the text.
- **§4.4 shell escape** (`internal/shell`, front end 0.0.7): `$`
  fire-and-forget and `#` capture-into-next-turn, built as **per-command
  `$SHELL -c`** rather than SPEC §4.4.2's long-lived `$SHELL -i` (the
  amendment is recorded in §4.4.2 — the persistent shell mainly buys state
  persistence that §4.4.3 already makes moot, while per-command execution
  gets exit codes for free and cannot hang the session). The §4.5.1 shell
  cwd is tracked by a **cwd-reporting epilogue on a dedicated fd**, never by
  parsing `cd`, and stays distinct from the active project root. Ctrl-C
  during a command is an **invariant, not a best effort** (front end
  0.0.10, SPEC §4.4.2): from the runner's commit point until the child is
  reaped a SIGINT never reaches the session-cancel path. Interrupt
  OWNERSHIP is tracked separately from DELIVERABILITY — the runner claims
  the signal before `cmd.Start`, and an interrupt claimed before the
  process group exists is queued and delivered the instant it does, because
  publishing the pgid earlier only makes the bad window smaller and a small
  window is what a loaded machine finds. The deterministic regression test
  drives a SIGINT into that window through `shell.ArmHook` /
  `shell.HookChildStarting` — the same Arm/At shape as `internal/crashpoint`
  but a rendezvous rather than a panic (and deliberately NOT registered
  there, since crashpoint's registry feeds the R4 coverage gate). The child
  gets its **own process group** so Ctrl-C kills the command rather than the
  session; the **startup terminal mode** is restored around the child
  (liner holds `ICANON`/`ECHO` off for the whole session, so a naively
  spawned child inherits a terminal with no echo); **stdin is the null
  device** so a §4.4.3 interactive app fails fast instead of wedging, and a
  non-zero exit is announced. A `#` capture rides the existing `preEvents`
  slot into the next turn — buffered, in order, non-durable by design — and
  is **redacted** against the resolved provider keys on the context path
  only (§8.2.1; the terminal keeps the unredacted bytes, and the
  `permissions.redaction-fire` event records a count, never a match).
- **§6.1 tool surface, waves 1–3** (front end 0.0.9). Wave 1 landed the
  index-keyed merge of streamed tool-call deltas (`model.Chunk.ToolCalls`
  is IDENTITY-ONLY — ID + function, never args; the merged calls come from
  `Final()`). Wave 2 landed the **registry** (`internal/tools`:
  name → spec + handler + tier + mutates, substrate-free) and the
  **execution loop** in `internal/turn` — see SPEC §6.1.4. An empty
  registry sends no `tools` field. Load-bearing points: a tool-call-only response is byte-identical
  to the §3.3 empty-response shape and is told apart by the identity-only
  chunk view; the tool-round cap is a SEPARATE counter from the §5.5/§3.3
  re-prompt caps; `Dispatch` never fails (unknown tool / handler error /
  timeout all become results the model can recover from) except on a
  cancelled turn; the §6.5 cap is applied once, to the delta, and the
  bounded bytes build the tool message too; and the user is never left with
  silence — a text-less turn emits one bracketed runtime notice.
  **Tool execution is AT-LEAST-ONCE under #94 crash replay** (calls and
  results are deliberately not journaled) — harmless for the read-only
  §6.1.1 tools, NOT harmless for a mutating one, so
  `tools.Registry.Register` mechanically REFUSES a tool declaring
  `Mutates`. Revisit that decision before any mutating tool lands.
  Fixed in passing: the §6.5 byte cap bounded `tool.result` ONLY, so a
  `# cat bigfile` blew the current-turn budget unbounded —
  `boundToolResultDeltas` is now **`boundTaskResultDeltas`** and covers
  `user.shell-capture` too, deliberately NOT the task-class `fs.*` deltas
  whose content is the stored file body.
  **Wave 3 landed the two real tools** (`internal/tools/web`), the first
  entries the registry ever ships with:
  - **`web.fetch`** — plain GET → readability extraction → Markdown, with
    NO headless browser. Scheme allowlist is `http`/`https` only, on the
    initial URL and every redirect hop: it blocks `file://`, `data:`,
    `ftp://` and everything else, and it deliberately does NOT block
    loopback or any IP range (user ruling — a deliberately exposed local
    server is fair game; do not "harden" this into SSRF filtering).
    Timeout + redirect cap + a size cap enforced WHILE READING. HTML goes
    through the pipeline, Markdown/text/JSON pass through, binary is
    REFUSED rather than parsed as garbage. **Empty-shell detection** is
    the load-bearing behaviour: a client-rendered SPA returns an explicit
    "requires JavaScript rendering" signal instead of a blank body,
    because a model told "the page is blank" answers confidently and
    wrongly. Readability metadata (title/byline/date/language) is
    surfaced with the content — staleness judgement is the point.
  - **`web.search`** — ranked results behind a ONE-METHOD
    `SearchProvider` interface (Exa is the first backend). The contract
    that matters: **"search failed" and "search found nothing" are
    distinguishable and the model is told which** — a failure is an error
    result saying NO search happened, an empty index is a success result
    saying it ran and matched zero. A **local per-turn/per-day query
    cap** (config `[search] maxPerTurn`/`maxPerDay`, defaults 5/100) is
    enforced in our code, bounds ATTEMPTS not successes, and rides the
    existing `tool.error` event for logging. Counters are in-process: the
    per-turn cap is exact (keyed on the turn number the loop puts on the
    tool context), the per-day cap resets on restart.
  - Tier ruling for both (SPEC §6.2.7, closing a real spec gap): **tier 0
    (silent), with the scheme allowlist as the boundary instead of an
    ack** — a user cannot meaningfully adjudicate whether a URL is an
    internal-network probe, and a prompt they learn to clear is worse
    than a mechanical bound.
  - Configuration: the search backend is a **providers.toml pool entry**
    (`type = "search"`, `api = "exa"`, its own `apiKeyFile`), selected by
    `config.toml [search] provider`. No key ever appears in config.toml,
    and `ValidateConfig` says NOTHING about `[search]` — it gates one
    optional tool and must never refuse a session.
  - Pool entries gained **required `type`/`api` fields** (no defaults —
    there is no installed base to be compatible with, and the only
    plausible default is a silent guess about an endpoint about to
    receive a credential). An invalid pair is faulted out at load with a
    named reason. `defaultModel` was **removed** from the provider entry
    (user ruling: catalogues rotate, so a model id pinned on a
    connectivity record goes stale, and the stale fallback fires exactly
    when the configured model is unavailable). The chat model now comes
    from `config.toml [chat]` or `--model`, verified once at session open
    against the one provider's `/models`.

Implemented and wired into the turn loop (pending full acceptance-validation):
- Thread closure / retirement (§3.5): curator-drafted summary + ack flow;
  `internal/turn/closure.go` decay-triggered scan fires at turn close.
- Recoverable deep-cold archival (§3.8): `internal/turn/archival.go` +
  `internal/memops/fileadapter/fileadapter_archive.go`; cardinality-pressure
  scan fires at turn close.
- Working-set content dedup / git minimization (§3.9): `internal/dedup` +
  `AgeFileChains`, applied per engaged thread.

Queued:
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
  transient-data event-log compaction + class-aware tool-output budget.
- v1.0: Python computational workflow (math/physics simulation).

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
internal/tools/web/         §6.1.1 web.fetch + web.search: readability →
                            Markdown extraction, empty-shell detection, the
                            SearchProvider seam + Exa backend, local query
                            caps
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
AGENTS.md                   this file
Makefile                    build + agents-submodule pinning +
                            serve-local-api
```

## Build / test

```sh
make build            # bin/personant (compile only — the cheapest edit check)
make add-dependency MOD=<module>@<version>   # pin ONE vetted dep into go.mod/go.sum
                      #   (the AGENTS.md vetting checklist is a PRECONDITION, not
                      #   something the target can check; `update-dependencies` is
                      #   the wrong tool — its `go get -u ./...` churns the whole graph)
make fmt              # gofmt -w the Go source roots (cmd/, internal/) — fix formatting drift
make test             # CHECKPOINT GATE (substrate): fmt-check (drift fails the gate) + go vet + go test $(GOPKGS) (full suite, incl. multi-day sim rungs; Go's test cache is ON — unchanged packages return instantly)
make test-nocache     # `make test` with the cache DEFEATED (--count=1) — the forced-clean full run: pre-push, suspected cache artifact, post-toolchain change
make test-be          # alias of `make test` — the same full suite, named for symmetry with test-fe
make test-fe          # CHECKPOINT GATE (front-end-only change): scoped ~15s suite; mechanically REFUSES if the diff leaves cmd/ + internal/chat/ + internal/version/ + internal/shell/ + internal/term/
make test-run PKG=<pkg> RUN=<regexp>  # EDIT GATE: run only the touched test(s) — seconds, not minutes
make test-race        # DIAGNOSTIC (not a gate): -race over $(RACEPKGS), the packages with real concurrency; ~70s
make test-changed     # DIAGNOSTIC (not a gate): runs only the packages the working tree changed, plus their reverse-dependency closure. LIST=1 prints the selection without running it
make integration-test # live reaper embedder/recall tests (opt-in)
make sim              # acceptance rung-walk (mock); LIVE_EMBEDDING=true / LIVE_INFERENCE=true for live-mode
                      # DURATION=<span>  override sim span (default 1w for mock; e.g. 1d, 14d, 30d or <N>d / Go duration 168h)
                      #   named rungs: 1d|1w  bare-day form: <N>d (e.g. 30d, 120d)  Go duration: 168h
make recall-corpus-test # corpus recall-fidelity measurement (opt-in)
make clean
```

Go 1.26.1+.

**Two gates — do not conflate them.** `make test` (full `go vet` + `go test`
across `$(GOPKGS)`, *including* the sim package's multi-day mock rungs — minutes
of wall-clock) is the **CHECKPOINT GATE**: run it **once, before committing a
checkpoint**, to catch cross-package regressions. It is **not** an edit gate.
Re-running the whole suite between edits while iterating is the failure mode that
turns a one-line fix into an hour of mostly-irrelevant testing — don't. To verify
a specific edit landed, use the **EDIT GATE**: `make build` (compile) plus
`make test-run PKG=<pkg> RUN=<regexp>` to run only the touched test(s). Breaking
the compound suite down to the relevant test is the correct tactic during
iteration; the full suite is the final pre-commit checkpoint, not a per-edit
reflex. **Commit at meaningful checkpoints** — a complete, self-consistent change
— not per-edit, and run the checkpoint gate once at that point. (`make test` is
still NEVER substituted by a raw `go test`; the slow/live opt-in tests below stay
gated either way.)

**The checkpoint gate is scope-dependent — and the scoping is ASYMMETRIC.**
Which command satisfies the checkpoint gate depends on what the change touches:

- **Front-end-only change** (files confined to `cmd/`, `internal/chat/`,
  `internal/version/`, `internal/shell/`, `internal/term/`) →
  **`make test-fe`**. ~15s.
- **Anything touching the substrate** — any other `internal/` package —
  → **`make test`** (a.k.a. **`make test-be`**), the full suite. **No exception.**

The asymmetry is structural, not a budget compromise. `cmd/`,
`internal/chat/`, `internal/shell/`, and `internal/term/` are a
**dependency leaf**: nothing outside that set imports them
(`internal/shell` — the §4.4 `$`/`#` escape — and `internal/term` — the
terminal arbiter — only by `internal/chat`),
so a change confined to them cannot regress the substrate, and the scoped
target is sound *by the import graph* rather than by anyone's estimate of
blast radius. The reverse does not hold — the front end **imports** the
substrate, so a substrate change can regress the front end and must run
everything. **There is no back-end saving**; `test-be` is a pure alias of
`test` and exists only so the fast target has an unambiguous counterpart to
name. A symmetric-looking pair of target names invites exactly the wrong
inference; do not draw it.

`internal/version/` is inside the front-end scope because the front-end bump
contract makes **every** front-end change touch `version.FrontEnd` — a
"cmd/ + chat/ only" rule would reject every legitimate front-end commit. It
is also imported by `eventlog`, `memops`, `store`, and `fileadapter`, so
`test-fe` runs the three cheap ones as insurance against a version const bump
breaking a substrate assertion (`fileadapter` is excluded — 229s, pure
substrate).

**The guard is mechanical; do not reason your way past it.** `test-fe`
depends on `fe-scope-check`, which computes the changed `.go` set from
`git diff --name-only HEAD` plus untracked files and **refuses, naming the
offending paths**, if any of them fall outside the three front-end prefixes.
Scope classification is therefore a property of the diff, not an agent's
self-assessment — an agent neither needs to, nor may, talk itself into the
fast path. If the guard refuses, the answer is `make test`. (The guard
inspects Go source only: a Makefile, doc, or testdata edit does not trip it.
Those are covered by the mechanical-diff exception below or by the caller's
judgment.)

**Mechanical-diff exception (user-ratified 2026-07-17).** A commit whose
diff is semantics-preserving **by construction** — `gofmt`/`make fmt`
output, comment-only, or docs-only changes — commits on the EDIT GATE
(`make build` + `make fmt-check` + touched tests if any) without the
full checkpoint suite: gofmt operates on the AST and cannot change
logic, and burning suite-minutes on it is the attention tax formatting
uniformity exists to eliminate. The next substantive commit's checkpoint
gate still covers the whole tree, so any freak regression surfaces one
commit later, attributably. The exception is construction-based, not
size-based: a "small" logic change is NOT mechanical; anything touching
executable code paths, go.mod dependency versions, or test assertions
pays the full gate.

**`make test-race` is a DIAGNOSTIC, not a third gate.** It runs the race
detector over `$(RACEPKGS)` — the packages that either launch a goroutine in
non-test code or run two goroutines against shared state in their own tests
(`chat`, `recall/measure`, `model`, `eventlog`, `metrics`, `log`, and
`scenarios` exact-package). ~70 seconds. Run it **deliberately, when you touch
concurrent code**: a new goroutine, a shared field, a lock, a channel, a
ticker. It is **not** a prerequisite of `test`, `test-be`, or `test-fe`, and
must not become one — wiring it into a gate re-inflates exactly the cost the
fe/be split exists to hold down, and on a diff that added no concurrency it
finds nothing by construction. The EDIT GATE / CHECKPOINT GATE pair is
unchanged; this is a tool that sits beside them.

The scoping rule is in the Makefile comment and is empirical, not
aesthetic: `-race` instruments every package linked into a test binary, so a
package whose suite is single-goroutine contributes cost and zero signal —
the detector reports only accesses it actually observes from two goroutines.
That is why `internal/scenarios/sim` (~1605s unraced; hours under `-race`)
and `internal/memops/fileadapter` (229s, entirely sequential) are excluded
while still being *instrumented transitively* as linked dependencies of the
concurrent suites. Do not "fix" `$(RACEPKGS)` by broadening it to
`$(GOPKGS)`; that produces a target nobody will ever wait for, which is the
same as no target.

**Go's test cache is ON for the routine gates (`test`, `test-be`, `test-fe`,
`test-changed`).** `--count=1` used to be hard-coded into `test`, which meant
every invocation re-executed the 1625s sim package even when nothing in it had
moved. The cache key is content-addressed over a package's source, its full
transitive dependency set, the testdata bytes a test actually reads, and the
environment variables it calls `os.Getenv` on — so it invalidates precisely when
a result could have changed, computed by the toolchain rather than estimated by
an agent. A cached PASS is a real PASS for that content. The forced-clean path
is **preserved, not removed**: **`make test-nocache`** is the same full suite
with `--count=1`, for when a genuinely fresh run is the point (before a push,
when a cached PASS is itself the suspicion, after a toolchain or `go.mod`
change). Any scoped target can be forced the same way with
`make test-fe GOTESTCOUNT=--count=1`. The deliberate/heavyweight targets —
`test-race`, `test-run`, `sim*`, `integration-test`, `recall-corpus-test`,
`cover` — keep `--count=1` hard-coded on purpose: they are invoked *because* a
fresh run is wanted, and their cache keys do not cover what they actually probe
(endpoint reachability, goroutine scheduling, an observed `-v` report).

**`make test-changed` is a DIAGNOSTIC, not a gate — same standing as
`test-race`.** It computes the changed paths from the working tree
(`git diff --name-only HEAD` + untracked, scoped to `$(GOSRCDIRS)`), maps each to
its owning package by walking up to the nearest directory containing `*.go`, and
then runs the **reverse-dependency closure**: every package whose test binary
links a changed package, inverted out of `go list -test -f '{{.Deps}}'` so that a
dependent reached *only* through `_test.go` imports is still caught. The selected
set is always printed before it runs (`LIST=1` prints and stops). A clean tree
selects nothing and says so; a `go.mod`/`go.sum` change selects everything; a
change outside `cmd/`/`internal/` is listed but selects nothing. It is a **fast
iteration aid** and **a green `test-changed` does NOT satisfy the checkpoint
gate** — the fe/be contract above is unchanged (front-end-only diff →
`test-fe`; anything touching the substrate → the full `test`). Do not let it
become a third scoping rule: it is derived from the working tree, so it is only
as honest as the tree at the moment it ran, and it has no `fe-scope-check`-style
mechanical refusal behind it.

Slow / live tests use a **runtime opt-in**, not build tags: they always
compile (so a refactor that breaks them fails `make test`), and gate
EXECUTION at runtime — they `t.Skip` unless their opt-in is present, so
bare `make test` compiles and skips them. The opt-ins live in
`internal/testsupport`: `PERSONANT_LIVE_TESTS` (live reaper endpoint;
under it an unreachable endpoint is a FAILURE, not a skip),
`PERSONANT_CORPUS_TESTS` (slow corpus measurement),
`PERSONANT_SLOW_SIM_TESTS` (`make sim-shadow-slow-test`), and the
`-sim.live-embedding` / `-sim.live-inference` flags (live sim). The
`make` targets above set the right opt-in. Do not reintroduce
`//go:build` tags for conditional execution — tagged tests are excluded
from the normal compile and bit-rot silently.

**`sim-shadow-slow-test` broadened (#94 R4 suite-budget move).**
`PERSONANT_SLOW_SIM_TESTS` now gates not just the 14d
`TestShadowLayerB_ReverseDivergence` arm but also the fixed-24h #98
embedding head-to-head machinery rungs
(`TestSimEmbeddingHeadToHead_Machinery`,
`TestSimNoEmbedder_HeadToHeadAbsent`) — moved out of the default
`make test` run so the R4 crash-injection matrix fits the 30-min
checkpoint budget. All still ALWAYS COMPILE under `make test`; only
their EXECUTION is opted in. The crash matrix itself is *not* gated
here — it is fast unit-grade and runs in the default suite.

**Intra-thread recall metrics (#109/#111).** The sim emits the following at every rung; the W1 divergence is a reported quality measure (not build-blocking, #119):

*Counters (m.Counters):*
- `recall_intra_descent_divergence` — run-total top-Kf leaf set difference between summary-tree descent and flat scan over every W1 probe on a live-embedding run. **REPORTED QUALITY MEASURE, NOT A GATE (#119):** the within-thread summary tree is an approximate O(log n) recall heuristic; a nonzero means it substituted a within-top-Kf leaf, NOT necessarily lost recall (see the classification below). Exact/exhaustive recall is the separate exact tier (#117 grep + flat-scan), not this approximate path. Logged as an approximation-drift canary.
- `recall_intra_descent_probes` — denominator for the above (number of W1 probes evaluated; distinguishes "gate passed" from "no probe ran").
- `recall_intra_w1_strict_miss` — of the divergent probes: a strictly-better leaf was pruned (real recall loss; fix keys or beam).
- `recall_intra_w1_tie` — of the divergent probes: an equal-cosine leaf was substituted (tie-boundary, not a recall loss; fix tie-break).
- `recall_intra_w1_tree_mismatch` — of the divergent probes: the flat scan ranked a leaf the tree does not contain (staleness/build edge).
- `recall_intra_tree_rebuild_calls` — within-thread summary-tree (re)builds fired across sleep cycles; F-B trip-wire (watch for unbounded growth with main-thread length, escalates to the hybrid-rebalancing MAD).

*Gauges (m.Gauges):*
- `recall_intra_hop_recall` / `recall_intra_embed_hop_recall` — symbolic (predicted) and embedding (observed) intra-thread recall by hop distance; the #109 fidelity curve. Reported, not gated to a floor — the curve is the deliverable.
- `recall_intra_recall_bydepth` / `recall_intra_embed_recall_bydepth` — same recall, bucketed by TURN-DEPTH (currentTurn−targetTurn, log-scale powers of B=16) keyed `_d<bucket>` (+`_obs`); the multi-year-thread no-decay axis of the #109 curve. Symbolic on every run, embedding-observed live-only. Report-only — no gate, no floor.
- `recall_intra_coherence_divergence` — symbolic oracle vs. runtime intra fine-tier disagreement; **HARD gate == 0 on mock runs** (mock embedder → symbolic oracle valid); report-only on live-embedding runs (symbolic oracle ≠ embedding recall by design, #96). Per SPEC §3.4 there is **no debt-window dead zone**: the oracle asserts recall across the FULL scrolled-out range (including the recent tail), so any divergence is a real defect — never tolerated async-flush lag. The former `recall_intra_blindspot_misses` metric and the "by-design lag" carve-out are retracted (#124): the runtime keeps debt-window content findable continuously via the bounded lexical completeness floor (#123), so the oracle no longer excludes any scrolled-out probe target.
- `recall_query_cosine_ops` — measured per-query cosine comparisons (coarse + fine population + engaged-thread descent or flat scan); the headline perf-bend metric.

**W1 recall-preservation QUALITY MEASURE (#111 §7.1; reframed from gate→measure in #119):** `recall_intra_descent_divergence` is a reported approximation-drift canary, **not a build-blocking gate**. The within-thread summary tree is an approximate O(log n) recall heuristic that trades exactness for speed; a nonzero divergence means the heuristic substituted a within-top-Kf leaf, NOT necessarily that recall was lost (a `strict_miss` is a real ranking defect worth raising the beam for; a `tie`/`tree_mismatch` is a sub-perceptible boundary effect — see the classification counters). Exact/exhaustive recall is the job of the separate **exact tiers (#117 grep + flat-scan)**, where it is guaranteed; holding the approximate tree to exact descent-vs-flat set-equality was stricter than its own purpose (recall-correctness). The value is still LOGGED on every rung as a drift canary. There is NO runtime rebalance trigger: tree re-clustering is a sleep-time operation only (the #108 consolidation cycle's `RebuildTrees`, on staleness), so the measure informs nothing at runtime — it is purely observed.

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
    gitops/policy refactor);
    `github.com/peterh/liner` (consumer: `internal/chat/` for the §4.3.1
    REPL line editing + persistent history; lightest well-trodden
    readline-class dep, one transitive dep `mattn/go-runewidth`);
    `codeberg.org/readeck/go-readability/v2` v2.1.2 +
    `github.com/JohannesKaufmann/html-to-markdown/v2` v2.5.2 (**LANDED**
    with their consumer, `web.fetch`, §6.1.1 — boilerplate-stripped article
    extraction then HTML→Markdown, so the model reads the article rather
    than the nav bar. Both MIT, pure Go, no cgo. `html-to-markdown` composes
    with `golang.org/x/net/html`, which was already in the graph via go-git
    and is now a direct require — no second parser). Do not
    pull these in speculatively; do pull them in when the consumer arrives.
  - **`langchaingo/llms`** is *compatible* but excluded on dep-hygiene
    + scope grounds for v0.1 (30+ transitive deps; a thin OpenAI-compatible
    HTTP client at ~200–300 LoC covers the single-provider need). Revisit in
    v0.2+ if any of: (1) genuine multi-provider need with format-normalization
    burden; (2) `langchaingo/embeddings` covers the §3.4 embedding-recall path
    well; (3) a future capability costs >500 LoC of custom writing.
  - **Vet adoption and maintenance before proposing a dependency — from the
    registry, not the README.** Choosing on what a project says on the tin is
    a rookie mistake. Record these in the proposal, measured, not asserted:
    (1) **last release date** — a stale port is a bus-factor bet no benchmark
    score offsets; (2) **`Imported by` count** on pkg.go.dev — the concrete
    form of "well-trodden"; (3) **deprecation status** — pkg.go.dev shows it,
    READMEs often do not; (4) **transitive dep count** — the metric that
    already excluded `langchaingo/llms` above; (5) **license**.
    **The specific trap: for a port, verify the PORT's activity, not its
    upstream's.** A port's README will describe the upstream project's release
    cadence, which says nothing about whether the port has shipped in two
    years. This is not hypothetical — it is how `go-trafilatura` (better
    extraction benchmarks, but 21 months without a release and 5 importers)
    was nearly adopted over the maintained readability fork above, on a
    ~1-point F1 difference. **Default to the well-trodden option** unless the
    off-standard gain is genuinely substantial: weight the cost of being
    wrong, not just the benchmark delta. A stale dependency's cost lands
    later, on whoever has to replace it mid-feature.
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
