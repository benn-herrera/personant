# Personant

A single-user, single-agent runtime for AI assistants with persistent working
memory across arbitrary projects. The agent has one continuous career: unified
memory, no session boundaries, no compaction-driven information loss, and
cross-project recognition.

## Status

Two independently-versioned tracks: **substrate v0.1.0** (the runtime + `MemoryOps` API — earned through the test rigor to date) and **front end v0.0.1** (the chat REPL loop is closed but untested by any direct human means and missing much minimally-required interactive behavior). The substrate is driven toward **v0.5.0** by a *realism-convergence* acceptance gate — six months of simulated usage with every identified element of realism accounted for (simulated, modeled-and-attempted, or honestly parked as a known-unknown pending real-use data), converging to a run that surfaces no new realism gap. At v0.5.0 work switches to front-end logic; the front end earns v0.1.0 once a minimum interactive feature set exists, validated by a human U/X phase. See [`SPEC.md` §9.1](SPEC.md) for the normative gate.

Phase 1 (storage scaffold) is complete; Phase 2 (turn loop, chat REPL, working-set composition, scenario harness) is substantially landed. The canonical sources are:

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — orientation for AI agents and contributors; principles, patterns, mechanisms, anti-patterns. Read first.
- [`SPEC.md`](SPEC.md) — field-level schemas, algorithms, surface APIs.
- [`AGENTS.md`](AGENTS.md) — house rules for agents working in this repo.

Read ARCHITECTURE.md first for the shape of the system; read the spec when you need to write code that conforms to it.

## What it does

Personant manages an AI assistant's working memory using **deterministic
state as canonical, the LLM in narrow judgment roles, and human
acknowledgement at high-leverage moments only**. Threads of work are
recognized by the model, anchored by curated symbol sets, retired with a
human ack at closure, and surfaced opportunistically when later work touches
prior context — including across projects.

The substrate is plain text in a git repository: JSONL for structured
records, markdown for thread bodies and directive files. Inspectability,
recoverability, and history come for free.

## Storage layout

By default Personant uses `~/.personant/` (override with `$PERSONANT_HOME`).
The directory is git-init'd on first run. See
[`SPEC.md` §2.1](SPEC.md) for the full layout; a sketch:

```
~/.personant/
  spine.jsonl          unified across all projects
  symbols.jsonl        derived inverse index
  threads/             one file per thread (markdown + YAML frontmatter)
  projects/            per-project metadata + cross-project digests
  directives/          tunable behavior (defaults / user / per-project)
  logs/                YYYY-MM-DD.log, plain text, append-only
```

## Building

```sh
make                  # build bin/personant
make test             # go vet + go test
make integration-test # run integration tests
make sim DURATION=<1d|1w|1m|2m|6m> # run long-endurance usage simulation test (6m takes ~1hr)
make clean            # remove the binary
```

Go 1.26.1+. The runtime ships as a single binary; no host runtime
dependencies.

## Scope

- **In scope (v0.1):** single-user single-agent runtime, unified spine,
  multi-project recognition, opportunistic recall, closure ack flow,
  fallback dissection.
- **Out of scope:** multi-user (v2.0), encrypted-at-rest storage, sub-agent
  runtime extension, phrasal-concept symbol extraction. See ARCHITECTURE.md
  "Out of scope" for the full categorization.
