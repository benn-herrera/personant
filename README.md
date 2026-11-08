# README – Personant

> **Work in progress.** Personant is being rebuilt to run under opencode. The opencode plugin and
> memory service are not built yet, and parts of this document describe the target, not the
> current tree. Progress: [`ROADMAP.md`](ROADMAP.md) Part A.

Personant is a memory system for a coding agent, designed to run under unmodified
[opencode](https://opencode.ai) as a server plugin plus a Go memory service. It owns what enters
every model request, so the agent keeps one continuous career across sessions and projects.
What it is built from and what is new: [`THESIS.md`](THESIS.md); operating model:
[`SPEC.md`](SPEC.md) §1.

## Why

An assistant without a past cannot be a partner. Memory must be continuous — and it must be owned
by something that does not forget.

Today's assistants live in sessions. Each conversation starts from zero, and long ones are compacted
until what mattered is gone. Whatever the model learned about the work, the person, or the last
project was never kept.

The aim is a durable assistant with one continuous career.

## Developer
<https://keyoxide.org/wkd/benn@bennherrera.dev>

## Requirements and Supported Platforms

- Go 1.26.1+ (`go.mod`)
- macOS or Linux
- Development only: Python 3, standard library only. `just recall-madlibs` runs it, and every
  test and sim recipe depends on that recipe. Running Personant does not need Python.
- To run the plugin route: opencode at the pinned release. The plugin and the production service are
  not built yet, so the pin is not yet stated anywhere; see
  [`ARCHITECTURE.md`](ARCHITECTURE.md) "Plugin route".

## Quick Start Guide

**The plugin route is under construction.** There is no plugin to install and no service to start
under opencode yet. What is built and what remains: [`ROADMAP.md`](ROADMAP.md) Part A; the design:
[`ARCHITECTURE.md`](ARCHITECTURE.md) "Plugin route".

What the current tree builds and runs:

```sh
git clone <repo>
cd personant
just build          # compiles every package and produces bin/personant
./bin/personant version
```

`bin/personant` carries the memory-store commands (`init`, `index`, `verify`, `archive`, `version`;
[`SPEC.md`](SPEC.md) §4.1). Memory initializes at `~/.personant/` as a git repository; override with
`$PERSONANT_HOME` or `--home`.

## How an opencode Session Relates to Memory

An opencode session is a window into one continuous memory, not a container for it. Contract and
open items: [`SPEC.md`](SPEC.md) §1.1.

## Where Settings Live

opencode's configuration, Personant's own settings and the memory home: [`SPEC.md`](SPEC.md) §8.1,
§8.2.2 and §2.1; [`ARCHITECTURE.md`](ARCHITECTURE.md) "One inspection location". The current
code does not match them yet (ROADMAP A12).

## Build Targets

```sh
just build                # compile every package, then bin/personant (edit gate, step 1)
just test-run <pkg> [re]  # run only the touched test(s) (edit gate, step 2)
just test                 # checkpoint gate: fmt-check + go vet + go test, cache-scoped
just test-changed         # diagnostic: only the packages the working tree could affect
just test-nocache         # `test` with the test cache defeated
just test-race            # diagnostic: -race over the packages with concurrent code
just fmt                  # gofmt -w over cmd/ and internal/
just fmt-check            # fail on gofmt drift
just cover                # coverage report across all packages
just bench                # recall hot-path benchmarks (pkg and bench_re arguments)
just sim                  # acceptance simulation (see Sim Knobs)
just sim-completeness-rung  # live-embedder recall-completeness rung
just sim-tokenceiling-rung  # live-inference whole-request token-ceiling rung
just sim-shadow-slow-test # opt-in slow sim rungs
just integration-test     # live reaper endpoint tests (opt-in)
just recall-madlibs       # regenerate derived query fixtures from committed templates
just recall-corpus-sweep-data  # synonym-depth query sets for the calibration sweep
just recall-embed-data    # embed the corpus (networked; needs reaper)
just recall-corpus-test   # Wikipedia corpus recall-fidelity measurement (opt-in)
just recall-corpus-fetch  # refresh the committed Wikipedia corpus (networked, slow)
just add-dependency <mod>@<ver>  # pin one vetted module
just update-dependencies  # upgrade the whole module graph
just agents               # install the agent set
just clean                # remove bin/personant
```

`just` alone lists every recipe with its description.

**Gates** ([`CONVENTIONS.md`](CONVENTIONS.md) "Two gates"):

- **Edit gate:** `just build` plus `just test-run <pkg> <regexp>` for the touched tests.
- **Checkpoint gate:** `just test`, the full suite, scoped by Go's test cache.
- **Diagnostic:** `just test-changed` runs only the packages the working tree could affect;
  `just LIST=1 test-changed` prints the selection without running it.

### Sim Knobs

`just sim` runs `TestSim` in `internal/scenarios/sim/`. By default it is the mock, deterministic
acceptance simulation: scripted mock responses and symbolic-only recall (nil embedder).

```sh
just sim                                          # mock gate, default span (1w)
just DURATION=1d sim                              # named span
just DURATION=30d sim                             # bare-day form
just DURATION=168h sim                            # Go duration form
just LIVE_EMBEDDING=true sim                      # real embedder (reaper required)
just LIVE_INFERENCE=true DURATION=1d sim          # real inference, capped at 1 sim-day
just LIVE_EMBEDDING=true LIVE_INFERENCE=true DURATION=1d sim
just sim-completeness-rung                        # live embedder, several sim-days
just sim-tokenceiling-rung                        # live inference, 1 sim-day
```

| Knob | Values | Effect |
|---|---|---|
| `DURATION` | `1d`, `1w`, `<N>d`, Go duration (`168h`) | simulated span; default `1w` |
| `LIVE_EMBEDDING` | `true` / `false` (default `false`) | use the real embedder |
| `LIVE_INFERENCE` | `true` / `false` (default `false`) | use real inference; refused past 1 sim-day |
| `B1X4_COMPLETENESS_DURATION` | same forms as `DURATION`; default `4d` | span of `sim-completeness-rung` |

Knobs are `just` variables (`just DURATION=1d sim`; overrides go before the recipe name) or environment variables (`DURATION=1d just
sim`). Both live toggles read `test/rundata/test.{providers,config}.toml` (user-provided,
gitignored) and need a reachable `reaper.local` endpoint; a missing or unreachable endpoint is a
hard failure, not a skip. On macOS, sim runs are wrapped in `caffeinate` and `taskpolicy` against
idle sleep and background-QoS demotion.

The mock runs (`just sim`, and the sim package under `just test`) run without the embedder, so
embedding recall and its completeness floor do not execute there ([`SPEC.md`](SPEC.md) §3.4). `sim-completeness-rung` covers
that case, and `sim-tokenceiling-rung` measures the whole-request token ceiling; neither is part of
`just test`.

### Live and slow test opt-ins

Slow and live tests always compile, so a refactor that breaks them fails `just test`. They gate
execution at runtime through environment variables; `//go:build` tags are not used, because tagged
tests drop out of the normal compile and rot silently.

| Opt-in | Set by | Effect |
|---|---|---|
| `PERSONANT_LIVE_TESTS=1` | `just integration-test` | live reaper endpoint tests; unreachable = failure |
| `PERSONANT_CORPUS_TESTS=1` | `just recall-corpus-test` | slow Wikipedia corpus measurement |
| `PERSONANT_SLOW_SIM_TESTS=1` | `just sim-shadow-slow-test` | slow sim rungs |
| `-sim.live-embedding` / `-sim.live-inference` | `just LIVE_*=true sim` | live sim elements |

## Module Map

See [`ARCHITECTURE.md`](ARCHITECTURE.md) "Module map".

## Storage Layout

Memory is plain text in git under `~/.personant/` (override with `$PERSONANT_HOME` or `--home`).
The tree: [`ARCHITECTURE.md`](ARCHITECTURE.md) "Store directory layout"; outcomes: [`SPEC.md`](SPEC.md) §2.

## Crash Recovery

Contract: [`SPEC.md`](SPEC.md) §4.5.8, and §1.6 for turns opencode itself interrupted. Mechanism:
[`ARCHITECTURE.md`](ARCHITECTURE.md) "Crash recovery and the turn and history repositories".

## Status

The plugin route is not built; sequencing is in [`ROADMAP.md`](ROADMAP.md) Part A. The memory
runtime beneath it (storage, turn chain, recall, closing, archival, crash recovery) exists and is
exercised in-process by the acceptance simulation ([`SPEC.md`](SPEC.md) §9.1). Open decisions are
listed in `ROADMAP.md` "Pending decisions"; known defects are `ROADMAP.md` items.

`personant version` prints the version lines. What they are: [`SPEC.md`](SPEC.md) §9.1 "Version
lines" and [`CONVENTIONS.md`](CONVENTIONS.md) "Versioning and acceptance".

## Design Principles

[`ARCHITECTURE.md`](ARCHITECTURE.md) "Applying the thesis", "The model drafts, deterministic code
writes" and "Storage non-negotiables".

## Third Party Acknowledgements

| Package | Author / Org | License | Use |
|---|---|---|---|
| `github.com/BurntSushi/toml` | BurntSushi | MIT | `providers.toml` and `config.toml` parsing |
| `codeberg.org/readeck/go-readability/v2` | Readeck contributors | MIT | `web.fetch` — boilerplate-stripped article extraction (self-built harness) |
| `github.com/JohannesKaufmann/html-to-markdown/v2` | Johannes Kaufmann | MIT | `web.fetch` — HTML → Markdown conversion (self-built harness) |
| `github.com/go-git/go-git/v5` | go-git contributors | Apache 2.0 | `internal/autogit` — automatic git operations on `~/.personant/` |
| `github.com/go-git/go-billy/v5` | go-git contributors | Apache 2.0 | `internal/autogit`, `internal/store` — filesystem layer under go-git |
| `golang.org/x/net` | The Go Authors | BSD 3-Clause | `web.fetch` — HTML parsing (self-built harness) |
| `github.com/spf13/cobra` | Steve Francia | Apache 2.0 | CLI subcommand dispatch (`cmd/`) |
| `gopkg.in/yaml.v3` | Canonical Ltd. | MIT / Apache 2.0 | Thread frontmatter read/write |
