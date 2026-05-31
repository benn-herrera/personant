# Design: optional inference-in-loop + embedding-in-loop simulation (#98)

Status: DESIGN / build contract. Scope: sim harness + a build-tagged live entry point.
Branch: `initial-implementation`. Build order: embedding-in-loop FIRST (the critical
recall-testing path), then inference-in-loop. Checkpoint (commit) per increment.

## 0. Why

The acceptance sim runs mock-LLM + nil-embedder → symbolic-only §3.4 recall. The #96
within-thread-wander finding showed symbolic Jaccard structurally cannot recall
non-monotonic threads (a 4-hop wanderer scores ~5/21 < 0.4 even on its current topic).
§3.4 makes embedding the PRIMARY recall scan precisely for this. reaper.local (gemma-4
inference + `nomicai-modernbert-embed-base-bf16` embeddings, non-metered) lifts the
"no unmetered inference/embedding" constraint that made symbolic-only a necessity.

## 1. Organizing principle: two toggles, two questions, one boundary

The recall-fidelity oracle derives `ExpectedRecallMatches` from the GENERATOR's
deterministic symbols. So the two live toggles differ in what they can MEASURE:

| toggle | what's real | oracle ground truth | measures |
|---|---|---|---|
| **embedding-in-loop** (real embedder, MOCK inference) | §3.4 layer-2 recall | **survives** — canned thread bodies are deterministic | does real embedding recall recover the threads symbolic Jaccard misses (closes the #96 gap); symbolic-vs-embedding head-to-head against the SAME expected set |
| **inference-in-loop** (real chat model) | turn responses | **invalidated** — live response symbols ≠ canned | LLM-behaviors only: tag-emission discipline, real symbol extraction, streaming, §5.5 re-prompt |

**Boundary (load-bearing):** mock = the deterministic acceptance gate (UNCHANGED;
`drainSteps` + `TestGenerateWorkload_Deterministic` stay mock-only). Live = an opt-in
MEASUREMENT mode, NEVER the acceptance gate. Embedding-in-loop is ~deterministic (fixed
text → stable vectors) but the embedding API is external, so it is measurement, not gate.

The two toggles are INDEPENDENT (either/both/neither). They map onto seams that already
exist:
- chat `model.Client` lives on `turn.State` (the sim sets `h.State.Client`; mock today).
- `model.Embedder` lives on the recall `measure.Service` (`state.Recaller`; nil today →
  symbolic-only).

## 2. Config source + provider selection (reqs 2–4)

- Live mode reads `test/rundata/test.providers.toml` + `test/rundata/test.config.toml`
  (USER-provided, gitignored — `test/rundata*/` is in .gitignore; confirmed present).
  NEVER touch `~/.personant` (no read, no write, no operate).
- `store.LoadProviders(test/rundata/test.providers.toml)` + parse `test.config.toml`
  `[chat]` / `[embedding]` model refs (`memops.ParseModelRef`) → select chat
  provider/model and embedding provider/model. `NewHTTPClient(provider)` /
  `NewHTTPEmbedder(provider, model, dims)`. The runtime loader resolves `apiKeyFile`
  targets — the agent NEVER reads those key files into context.
- **Usable-endpoint definition:** provider resolves (BaseURL + key) AND the endpoint is
  reachable (cheap reachability check — a models-list/ping or a trivial request).
- **FAIL (not skip) with a helpful message** when: the files are MISSING →
  `"test.providers.toml and test.config.toml are required in test/rundata/. Suggestion:
  copy ~/.personant/{config,providers}.toml to test/rundata/test.{config,providers}.toml
  (apiKeyFile paths may need to be absolute)."`; or present but NO usable endpoint →
  a clear endpoint-unreachable/unconfigured failure. Asking for live with nothing live =
  loud failure.

## 3. Gating (runtime flags on a single TestSim — NOT build tags)

> **Implemented design (supersedes the original build-tag plan).** The first
> cut used a `//go:build live_sim` tag + a separate `TestSimLive` + a `make
> sim-live` target. Both were removed: (a) build-tag-gated tests are excluded
> from the normal compile and silently bit-rot (the antipattern we then swept
> repo-wide — `integration`/`recall_corpus` were de-tagged too); (b) a separate
> always-skipping `TestSimLive` is the silent-skip antipattern (looks like
> coverage, never runs). The live mode is now toggles on the SINGLE `TestSim`.

- **One `TestSim`, two runtime flags, default off:** `-sim.live-embedding` and
  `-sim.live-inference` (independent — either/both/neither). The live code ALWAYS
  COMPILES (part of `make test`), so it can't rot; EXECUTION is gated by the flags.
- **both-false IS the mock acceptance gate** — symbolic-only (nil-embedder stand-in) +
  scripted mock responses, all hard gates active (`recall_unexplained_absence == 0`,
  wander coherence, steady-state). It RUNS under `make test` (1d) and `make sim`; it does
  NOT skip. A flag on installs that live element; both off touches no config/endpoint.
- **`make sim` is the single target**, parametrized by `LIVE_EMBEDDING` / `LIVE_INFERENCE`
  Makefile vars (both default false → mock gate) passed as the `-sim.live-*=<bool>` form,
  e.g. `make sim LIVE_EMBEDDING=true DURATION=1d`. No separate `sim-live` target.
- Span via `-sim.duration` (`DURATION` var); inference-in-loop is refused past a 1-sim-day
  cap (§6), so `LIVE_INFERENCE=true` needs a cap-safe `DURATION` (default is 1w).

## 4. The toggle seams

- **Embedding-in-loop:** replace the sim's `state.Recaller` (currently
  `measure.NewService(ops, nil)` → symbolic-only) with
  `measure.NewService(ops, NewHTTPEmbedder(...))` + `Prepare(ctx)` (builds the
  session-scoped thread-embedding index). Mock inference UNCHANGED → canned bodies →
  oracle still asserts. Turn-close already logs `spine.embed-match-fire` alongside
  `spine.match-fire`, so both layers' fires are observable per turn.
- **Inference-in-loop:** replace `h.State.Client` (the mock) with `NewHTTPClient(...)`.
  Live inference breaks **two** things, not one: (a) the recall oracle (live response
  symbols ≠ canned), AND (b) — deeper — the **generator's forward-planning coherence**.
  The on-demand generator plans which threads to engage/create from the *canned* topic
  tags; a real model emits *different* tags → the runtime engages/creates different
  threads → every subsequent generated step (which assumes the planned state) is
  incoherent. So inference-in-loop is NOT a coherent multi-day recall workload — it is a
  **short behavior-validation mode** (per the T2-1 framing): measure real-model behaviors
  the mock structurally can't — tag-emission discipline (parseable `*topic:*` rate,
  preamble-before-tag T1-1), real symbol extraction, streaming shape, §5.5 mid-turn
  re-prompt. ALL oracle/coherence-dependent assertions + hard gates
  (`recall_unexplained_absence`, wander coherence, recall-fidelity) are SKIPPED in this
  mode (they assume canned responses). Capped SHORT (T2-1 duration cap). Streaming: the
  harness uses blocking `Consult` today; a real client may stream — confirm `Consult`
  suffices or drain `ConsultStream`. Combinable with live-embedding (fullest live mode,
  closest to production, fully oracle-blind).

## 5. Embedding recall measurement (the #98 payoff)

With embedding-in-loop + mock inference, the workload is deterministic and
`ExpectedRecallMatches` is known. Measure, against the SAME expected set, a
symbolic-vs-embedding head-to-head:
- embedding recall/precision/F1 (parallel to the existing symbolic
  `recall_fidelity_*`), per the §3.4 layered design.
- The #96 hop-graded buckets are the high-value view: for wandered threads, does
  embedding recall recover origin/abandoned topics at hop distances where symbolic
  decays to ~0? This quantifies whether embedding closes the gap #96 measured.
- Keep symbolic metrics running in the same run (both layers fire) so the comparison is
  apples-to-apples on one workload.

## 6. Cost / the empirical sweet-spot hunt (Inc 4)

Per-turn cost: ~10s-of-ms (mock) → single-digit seconds (live). Strategies to MEASURE,
not assume: embed/infer only on recall-bearing turns; sample a fraction of turns; tier
choice is PREFILL-dominated (measure turns/hour per gemma tier — smaller-prefill may
win). T2-1 duration cap on live runs (~2–4 sim days, wall ≤ ~8hr). Keep mock as the
default fast loop; reserve full live for periodic deep validation. Reconcile/extend the
T2-1 live-inference regime in the test-infrastructure memory.

## 7. Increments (checkpoint per increment)

> **As-built note:** Inc 1 used a `//go:build live_sim` tag + `make sim-live`;
> a later pass de-tagged everything to runtime-skip and folded the live test
> into the single `TestSim` / `make sim` (§3). The increment descriptions below
> are the original plan; the gating they describe was superseded.

1. **Config plumbing + gating scaffold** — entry point + config load: read
   `test/rundata/test.{providers,config}.toml`; provider/model selection;
   usable-endpoint check; fail-with-suggestion guard; the two runtime flags parsed but
   wired as NO-OPS (default mock/nil path unchanged). No live recall yet. Gate: `make
   test` unaffected; live invocation with no flags runs the
   mock path; missing/unusable config fails with the message.
2. **Embedding-in-loop** (THE critical path) — install the real embedder + `Prepare`;
   mock inference; symbolic-vs-embedding recall-fidelity head-to-head (§5), incl. the #96
   hop buckets. Short rung first; confirm the embedding layer recovers wandered-thread
   recalls symbolic misses. Checkpoint.
3. **Inference-in-loop** — real chat model; oracle-OFF behavior-validation mode (tag
   discipline / extraction / streaming); short runs under the duration cap. Checkpoint.
4. **Sweet-spot hunt** — sampling strategies + per-tier turns/hour + duration caps; pick
   the operating point; reconcile the T2-1 memory. Checkpoint.

## 8. Invariants / non-negotiables

- Mock-deterministic acceptance gate untouched; live is never the gate.
- Never read/write/operate in `~/.personant`; never read `apiKeyFile` targets into agent
  context.
- No provider/endpoint info in version-controlled files (config lives in gitignored
  `test/rundata/`).
- `make test` stays mock-only and green; the live path is build-tag-isolated.
