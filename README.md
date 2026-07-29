# Personant

A single-user, single-agent runtime that gives an AI assistant persistent working memory across arbitrary projects. The agent has one continuous career: unified memory, no session boundaries, no compaction-driven information loss, and cross-project recognition.

## Requirements and Supported Platforms

- Go 1.26.1+
- macOS or Linux
- No host runtime dependencies beyond Go — ships as a single binary

## Quick Start Guide

```sh
git clone <repo>
cd personant
make build          # produces bin/personant
make test           # go vet + go test (compiles everything; skips live tests)
./bin/personant     # defaults to chat REPL
```

On first run, `bin/personant` initializes `~/.personant/` as a git repository. Override the home directory with `$PERSONANT_HOME`.

## Using the Chat REPL

`bin/personant` (equivalently `personant chat`) opens an interactive REPL. Type a message and press Enter to take a turn; the assistant streams its reply. Lines beginning with `/` are commands:

| Command | Effect |
|---|---|
| `/help` | list commands |
| `/topic <name>` | start a new thread and engage it |
| `/done [thr_id\|name]` | close the active (or named) thread |
| `/pause [thr_id\|name]` | pause the active (or named) thread |
| `/resume [thr_id\|name]` | resume a paused thread |
| `/back-to <thr_id\|name>` | re-engage a thread into the working set |
| `/project` | print active project info |
| `/project rename <new-name>` | rename the active project |
| `/project switch <name-or-id>` | switch to a known project |
| `/thinking [on\|off]` | show/hide the model's live reasoning (dimmed); bare form reports the state |
| `/stats` | session and spine statistics |
| `/version` | version identity + this home's on-disk format |
| `/quit`, `/exit` | end the session |

`/no-revisit`, `/cd-project`, and `/model` are recognized but stubbed for a later phase.

While a turn is working — before the first token, and again while it closes out — a one-line indicator names the phase the runtime is in (composing context, waiting on the model, searching memory, closing turn) with an elapsed counter, and advertises `esc to abort` while the turn can still be abandoned. It appears only after a couple of seconds, only on a real terminal, and it clears itself the instant the response starts arriving; a piped or redirected run gets no decoration at all.

A thinking model's reasoning is **off by default and never stored**. `config.toml`'s `[chat] showThinking` sets the session default and `/thinking on|off` overrides it for the running session; when on, the reasoning stream is rendered live and dimmed as it arrives, closed off on its own line before the answer begins (on a terminal with no ANSI support it is bracketed `[thinking] … [/thinking]` instead). It is scratch, not an answer: it is never journaled, never feeds symbol extraction, and is never replayed to the model. A piped or redirected run emits none of it regardless of the setting.

Line editing and history come from `liner`: in-line editing plus Up/Down history recall. History persists across sessions in `~/.personant/history` (deduplicated, capped at 1000 entries).

Two keys, two meanings. **Ctrl-C ends the session** — at the prompt it is exactly `/exit` (silent; working set checkpointed, session-end logged), and during a turn it abandons the turn and unwinds to that same clean shutdown with a one-line notice. A second Ctrl-C during shutdown forces an immediate exit. Ctrl-D at the prompt ends the session too. **Esc retracts the turn in flight** and drops you back at the prompt with the session intact. Esc means "that input was a mistake", not "stop generating" — so nothing from the abandoned turn is kept, including any response text already on screen (a `[cancelled …]` marker says so, since scrolled output cannot be erased). A retracted prompt does not enter memory; it comes back at the next prompt as an editable default (and in `↑` history), and re-submitting it is the only way it ever does. The next launch opens without a recovery banner, because an abort is an ordinary action rather than a crash. Esc is honoured up to the point a turn starts writing; after that the turn finishes. On a piped or redirected run nothing here changes.

> The front end is a **dogfood-minimum** REPL: the interactive surface is deliberately small and has not yet been hardened by direct human use.

## Build Targets

```sh
make build                # compile bin/personant
make test                 # go vet + go test ./... (standard gate; alias: make test-be)
make test-fe              # fast scoped gate for a front-end-only change (cmd/, chat/, version/);
                          #   refuses if the diff touches the substrate — see AGENTS.md "Two gates"
make cover                # test coverage report across all packages
make bench                # recall hot-path benchmarks (see Makefile for PKG/BENCH knobs)
make sim                  # acceptance simulation (see Sim Knobs below)
make integration-test     # live reaper endpoint tests (opt-in; see below)
make recall-corpus-test   # Wikipedia corpus recall-fidelity measurement (opt-in)
make recall-madlibs       # regenerate derived query fixtures from committed templates
make clean                # remove bin/personant
```

### Sim Knobs

`make sim` runs `TestSim` in `internal/scenarios/sim/`. By default it is the **mock, deterministic acceptance gate**: symbolic-only recall with scripted mock responses.

> **Recall-completeness is symbolic-only until the embedder-enabled rung is green (B1).** The default `make sim` / `make test` gate runs with a nil embedder, so the §3.4 embedding fine tier and its bounded lexical completeness floor (#123) do **not** execute there. The recall-completeness claim (no dead zones) is proven for the embedding-flush-lag case only by the **`make sim-completeness-rung`** rung below; until it is wired and green, treat that claim as symbolic-only-validated.

```sh
make sim                                          # mock gate, default span (1w)
make sim DURATION=1d                              # named span
make sim DURATION=30d                             # bare-day form
make sim DURATION=168h                            # Go duration form
make sim LIVE_EMBEDDING=true                      # real §3.4 embedder (reaper required)
make sim LIVE_INFERENCE=true DURATION=1d          # real inference, capped at 1 sim-day
make sim LIVE_EMBEDDING=true LIVE_INFERENCE=true DURATION=1d
make sim-completeness-rung                        # B1: §3.4 completeness floor (live embedder, several sim-days)
make sim-tokenceiling-rung                        # X4: whole-request token ceiling (live inference, 1 sim-day)
```

**`DURATION`** accepts: named rungs `1d|1w`, bare-day form `<N>d` (e.g. `30d`, `120d`), or a Go duration (e.g. `168h`). Defaults to `1w` for mock runs.

**B1+X4 embedder-enabled rung.** `make sim-completeness-rung` (live embedder + mock inference) drives the main thread past the assembly window so a probe target lands in the flush-lag dead zone, then asserts the §3.4 lexical completeness floor surfaced it (with a non-vacuity guard that at least one dead-zone probe was observed). `make sim-tokenceiling-rung` (live inference, capped at 1 sim-day) injects large verbose tool-result payloads and asserts the fully-assembled request's `usage.prompt_tokens` stays within the configured ceiling. Both are bounded and **not** part of `make test`. They MEASURE the X4-PROD violation; the production token bound is #127's work.

**`LIVE_EMBEDDING=true` / `LIVE_INFERENCE=true`** opt in real inference/embedding independently. Both require `test/rundata/test.{providers,config}.toml` (user-provided, gitignored) and a reachable `reaper.local` endpoint. A missing or unreachable endpoint is a hard failure, not a skip. `LIVE_INFERENCE=true` is refused past 1 sim-day — always pair it with `DURATION=1d`.

On Darwin, long sim runs are automatically wrapped with `caffeinate` + `taskpolicy` to prevent idle-sleep and background-QoS demotion.

### Live and slow test opt-ins

Slow and live tests always compile (a refactor that breaks them fails `make test`) but gate execution at runtime via environment variables. `//go:build` tags are not used — tagged tests are excluded from the normal compile and bit-rot silently.

| Opt-in | Set by | Effect |
|---|---|---|
| `PERSONANT_LIVE_TESTS=1` | `make integration-test` | live reaper endpoint tests; unreachable = failure |
| `PERSONANT_CORPUS_TESTS=1` | `make recall-corpus-test` | slow Wikipedia corpus measurement |
| `-sim.live-embedding` / `-sim.live-inference` | `make sim LIVE_*=true` | live sim elements |

## Project Anatomy

```
cmd/                        cobra subcommands (init, index, verify, ping, models, chat)
                            bare `personant` defaults to chat
internal/memops/            MemoryOps port: the interface + domain types
                            (SpineRecord, Thread, ProjectMeta, Provider, Config,
                            symbol enums); imports nothing internal — the
                            application layer depends only on this
internal/memops/fileadapter/  v0.1 substrate-backed adapter
internal/store/             file substrate: paths, JSONL helpers, init, spine ops,
                            project ops, providers, thread I/O, bootstrap
internal/autogit/           go-git-backed autonomic git wrapper with bitflag
                            systemic-validation policy
internal/turn/              §3.0 turn chain + turn loop, per-turn coalescing,
                            transient-data staging buffer, §3.4 recall surface,
                            §3.5 decay-triggered closure flow
internal/recall/scoring/    pure Jaccard/cosine recall primitives (substrate-free);
                            chunk-tier: ProposeChunks, DescendChunks beam descent,
                            BuildTree agglomerative clustering
internal/recall/measure/    application-side recall stack (Service, Recaller);
                            async single-indexer goroutine + atomic.Pointer snapshot;
                            .recall-cache sidecars (.vec, .tree); sleep-cycle
                            tree builder; W1 divergence probe
internal/curator/           closure-summary drafting (§3.5/§5.2)
internal/chat/              REPL, slash dispatch, bootstrap UX
internal/workset/           layered context composition (E/A1/A2/B/C)
internal/prompt/            template + topic-tag parser + stream filter
internal/model/             OpenAI-compatible HTTP client + scripted/generated mock
                            + SSE streaming
internal/scenarios/         scenario harness, invariants, metrics, recall-fidelity
                            test infrastructure (C.1–C.6)
internal/{eventlog,metrics,
  index,verify,clock,
  ping,modellist,log}/      supporting subsystems
design/                     design documents (intra-thread recall, archival, etc.)
test/tools/                 Python stdlib-only tooling (madlibs_generate.py,
                            wikipedia_corpus.py, embed_corpus.py)
test/rundata/               user-provided, gitignored: test.providers.toml,
                            test.config.toml (live test credentials)
ARCHITECTURE.md             orientation for contributors and agents — read first
SPEC.md                     field-level schemas, algorithms, surface APIs
AGENTS.md                   house rules for AI agents working in this repo
```

## Storage Layout

Personant stores all state under `~/.personant/` (override with `$PERSONANT_HOME`). The directory is git-initialized on first run. See [`SPEC.md` §2.1](SPEC.md) for the full schema; a sketch:

```
~/.personant/
  spine.jsonl          canonical cross-project record (sorted by id)
  symbols.jsonl        [derived] inverse symbol → threads index
  threads/             one directory per thread (thread.md + turns/ + files.json)
  projects/            per-project metadata + Layer A2 digests
  directives/          tunable behavior (defaults / user / per-project)
  logs/                YYYY-MM-DD.log, plain text, append-only; archive/ for old months
  history              REPL line-edit history (deduplicated, capped)
  providers.toml       provider pool connectivity catalog
  config.toml          active chat + embedding provider choices
  version.toml         on-disk layout revision (`format = N`)
  api_keys/            secret-bearing key files — never read by agents
  archive/             deep cold archive index (recoverable git-based archival)
  recovery/            quarantine/ — byte-exact bytes preserved by crash recovery
  turn-journal.jsonl   in-flight-turn content journal (crash durability)
  .git/                primary git tree — permanent day-grain career history
  .git-daily/          disposable per-turn recovery DB; reborn each day
```

**Crash recovery.** Your data survives a hard kill — SIGKILL, a power
loss, a mid-turn crash. Personant journals each turn's raw prompt and
response to disk *before* touching canonical state, and commits work to
a git substrate that treats the on-disk files as the source of truth, so
on the next launch it reconciles automatically and loses at most the one
turn that was in flight. Recovered content from an interrupted turn is
**surfaced to you** in the startup banner (never silently replayed, so
nothing acts on your behalf without you seeing it). Anything a recovery
step could not safely restore is **quarantined byte-exact** under
`recovery/quarantine/` rather than deleted — your bytes are never thrown
away.

## Status

Two independently-versioned tracks. Run `personant version` for the current values — they live in `internal/version`, not in this document.

- **Substrate** (runtime + `MemoryOps` API): converging toward **v0.5.0** via a four-month (120-day) realism-convergence acceptance simulation. "Done" means every identified realism element accounted for — simulated, modeled-and-unit-tested, or honestly parked as a known-unknown — converging to a run that surfaces no new gap. See [`SPEC.md` §9.1](SPEC.md) for the normative gate.
- **Front end** (chat REPL + interactive feature set): the REPL loop is closed but has not been validated by direct human use; the interactive feature set is incomplete. Front-end **v0.1.0** gates on a human U/X phase after substrate v0.5.0 lands.

A third, separate integer versions the home's on-disk layout (`~/.personant/version.toml`). A home written by a **newer** personant is refused rather than opened — `--allow-newer-home` overrides that at your own risk, and says so loudly. `personant version` reports the home's stamp even when the home is missing or unreadable; it is the one command that never gates.

**What is shipped and validated:**

- Phase 1–3: storage scaffold, turn loop, topic tagging, REPL, layered working-set composition, scenario harness, opportunistic recall via symbolic Jaccard.
- Phase A: `MemoryOps` port + `FileAdapter`; `internal/autogit` autonomic git wrapper.
- Phase B: transient-data lifecycle with source-driven retention classification.
- §3.4 layer-2 embedding recall: `model.Embedder`, `recall.ProposeEmbedding` cosine matcher, session-scoped in-memory thread-embedding index.
- Intra-thread recall (#109/#111): chunk-level hierarchical embedding index for the engaged long-running thread, coarse→fine descent with exemplar-set keys, persisted `.vec` vector cache and `.tree` sidecar, async single-indexer goroutine + `atomic.Pointer` snapshot, sleep-cycle `RebuildTrees`, sim shadow-chunk oracle and Candidate-A workload metrics.
- Anchor-lifecycle redesign: `anchors` as a deterministic projection of active `history_symbols`; superseded anchors retained (not evicted); validated across the acceptance ladder.
- Recall-fidelity test infrastructure (C.1–C.6): mad-libs query generator, adversarial templates, 152-article Wikipedia corpus (~1090 fragments), LLM-assisted template authoring, calibration sweep, embedding head-to-head measurement.

**Implemented and wired into the turn loop (pending full acceptance-validation):**

- Thread closure / retirement (§3.5): curator-drafted summary + human-ack flow; the decay-triggered closure scan fires at turn close.
- Recoverable deep-cold archival (§3.8): `internal/turn/archival.go` + `internal/memops/fileadapter/fileadapter_archive.go`; the cardinality-pressure archival scan fires at turn close.
- Working-set content dedup / git minimization (§3.9): `internal/dedup` + `AgeFileChains`, applied per engaged thread.
- Startup crash recovery (§4.5.8, #94): dual-repo git substrate (disposable per-turn `.git-daily/` + permanent day-grain `.git/`), ≤1-turn durability journal, deterministic cold-start reconciliation, and a crash-injection matrix with a mechanical coverage gate.

**Queued:**

- Phase 5: cross-project digest, fallback dissection, directive accrual.
- Inference-/embedding-in-loop coverage (LIVE_INFERENCE / LIVE_EMBEDDING sim paths).
- v0.2: transient-data event-log compaction.

## Design Principles

Personant bets that **deterministic state as canonical, the LLM in narrow judgment roles, and human acknowledgement at high-leverage moments only** scales further than agent-figures-it-all-out alternatives. Concretely: deterministic Go code owns canonical state, integrity, and all mechanical operations; the LLM handles topic tagging, summary drafting, anchor selection, and recognition; the human acks at three load-bearing moments (thread closure, recall surface, fallback dissection). The substrate is plain text in git — inspectability, recoverability, and history come for free.

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for the full architectural orientation. [`SPEC.md`](SPEC.md) has field-level schemas and algorithms.

## Third Party Acknowledgements

| Package | Author / Org | License | Use |
|---|---|---|---|
| `github.com/BurntSushi/toml` | BurntSushi | MIT | `providers.toml` and `config.toml` parsing |
| `github.com/go-git/go-git/v5` | go-git contributors | Apache 2.0 | `internal/autogit` — autonomic git operations on `~/.personant/` |
| `github.com/peterh/liner` | Peter Harris | MIT | `internal/chat` — REPL line editing + persistent history |
| `github.com/spf13/cobra` | Steve Francia | Apache 2.0 | CLI subcommand dispatch (`cmd/`) |
| `gopkg.in/yaml.v3` | Canonical Ltd. | MIT / Apache 2.0 | Thread frontmatter read/write |
