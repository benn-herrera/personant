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
> symbiote rather than an atrophied parasite clinging to a stunted
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
2. [`spec.md`](spec.md) — field-level schemas, algorithms, surface
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
  digests) are regenerable from canonical sources. A pre-commit hook fails
  on stale derived files.

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

Phase 1 (skeleton + storage) is complete; Phase 2 (turn loop + topic
tagging + chat REPL + working-set composition + scenario harness) is
substantially landed; the v0.2 surface (deep cold archival, dedup,
re-prompt) is queued. Current top-level shape:

```
cmd/                        cobra subcommands (init, index, verify, ping,
                            models, chat — bare `personant` defaults to chat)
internal/store/             canonical types, paths, JSONL helpers, init,
                            spine ops, project ops, providers, thread I/O,
                            bootstrap, etc.
internal/turn/              §3.0 chain, turn loop, per-turn coalescing
internal/chat/              REPL, slash dispatch, bootstrap UX
internal/workset/           layered context composition (E/A1/A2/B/C)
internal/prompt/            template + topic-tag parser + stream filter
internal/model/             OpenAI-compatible HTTP client + scripted/generated
                            mock + SSE streaming
internal/scenarios/         scenario harness, invariants, metrics
internal/{eventlog,metrics,
  index,verify,
  ping,modellist,log}/      supporting subsystems
ARCHITECTURE.md             orientation (read first)
spec.md                     operational spec
README.md                   user-facing
AGENTS.md                   this file
Makefile                    build + agents-submodule pinning + serve-local-api
```

## Build / test

```sh
make build            # bin/personant
make test             # go vet + go test ./... --count=1
make integration-test
make clean
```

Go 1.26.1+.

## House rules for agents

- **Be deliberate about dependencies.** Two categories:
  - **Permanently out** (architectural conflict with the substrate
    non-negotiables): `langchaingo/memory`, `langchaingo/chains`,
    `mattn/go-sqlite3`. Do not reintroduce.
  - **Approved-when-earned** (compatible; pull in *with their consumer*,
    not before): `github.com/BurntSushi/toml` (consumer: `providers.toml`
    loader); `gopkg.in/yaml.v3` (consumer: thread frontmatter writes).
    Do not pull these in speculatively; do pull them in when the
    consumer arrives.
  - **`langchaingo/llms`** is *compatible* but excluded on dep-hygiene
    + scope grounds — see substrate-decisions memory for the revisit
    conditions.
- **Match the spec's data model.** Spine records, thread frontmatter, and the
  symbol index have field-level schemas in §2. Don't invent your own.
- **Verify with the canonical docs.** Before assuming a behavior, grep
  ARCHITECTURE.md and the spec. If they're silent, surface it as an open
  question rather than guessing.
- **Auxiliary Python is stdlib-only.** No exceptions for "just one little
  dependency".
