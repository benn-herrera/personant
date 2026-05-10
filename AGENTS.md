# Personant — agent orientation

This file is for AI agents (Claude Code, etc.) working in this repository.
Read it before making non-trivial changes. Human-facing project info is in
[`README.md`](README.md).

## Canonical documents

The design is in two markdown files at the repo root. Read them, in this
order, before proposing structural changes:

1. [`outline.md`](outline.md) — design rationale, watch list,
   deferrals. The "why" of every decision.
2. [`spec.md`](spec.md) — field-level schemas, algorithms,
   surface APIs. The "what" and "how". §0–§2 and §6 are substantive;
   §3–§5 and §7+ are stubbed.

If a question of design comes up, the outline and spec are authoritative.
Update them when behavior changes; don't let code and docs drift.

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

The runtime is a skeleton. Current shape:

```
cmd/main.go                 cobra root (no subcommands implemented yet)
internal/log/               level-aware logger (stderr + file split)
internal/util/paths.go      PersonantPaths — $PERSONANT_HOME resolution
outline.md             design rationale
spec.md                §0–§2 and §6 substantive; §3–§5 and §7+ stubbed
Makefile                    build + agents-submodule pinning
```

Phase 1 (skeleton + storage: `init`, JSONL read/write, schema validation,
pre-commit hook, `verify`) is the next implementation milestone. See
[`spec.md` §10](spec.md).

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
    loader, landed Phase 2.a); `gopkg.in/yaml.v3` (consumer: thread
    frontmatter writes, lands Phase 2.d). Do not pull these in
    speculatively; do pull them in when the consumer arrives.
  - **`langchaingo/llms`** is *compatible* but excluded from v0.1 on
    dep-hygiene + scope grounds — see substrate-decisions memory for the
    revisit conditions.
- **Match the spec's data model.** Spine records, thread frontmatter, and the
  symbol index have field-level schemas in §2. Don't invent your own.
- **Verify with the canonical docs.** Before assuming a behavior, grep the
  outline and spec. If they're silent, surface it as an open question rather
  than guessing.
- **Open questions go in `spec.md` §13.** Don't accumulate them in code
  comments or commit messages.
- **Auxiliary Python is stdlib-only.** No exceptions for "just one little
  dependency".
