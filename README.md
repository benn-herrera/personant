# Personant

A single-user, single-agent runtime for AI assistants with persistent working
memory across arbitrary projects. The agent has one continuous career: unified
memory, no session boundaries, no compaction-driven information loss, and
cross-project recognition.

## Status

**v0.1, pre-implementation.** The design is settled and documented; the
runtime itself is a Go skeleton. The canonical sources are:

- [`outline.md`](outline.md) — design rationale, "why" decisions
  were made the way they were, watch list, deferrals.
- [`spec.md`](spec.md) — field-level schemas, algorithms, surface
  APIs. §0–§2 and §6 are substantive; §3–§5 and §7+ are stubbed.

Read the outline first if you want the shape of the system; read the spec
when you need to write code that conforms to it.

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
[`spec.md` §2.1](spec.md) for the full layout; a sketch:

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
make clean            # remove the binary
```

Go 1.26.1+. The runtime ships as a single binary; no host runtime
dependencies.

## Scope

- **In scope (v0.1):** single-user single-agent runtime, unified spine,
  multi-project recognition, opportunistic recall, closure ack flow,
  fallback dissection.
- **Out of scope:** multi-user (v2.0), encrypted-at-rest storage, sub-agent
  runtime extension, phrasal-concept symbol extraction. See the outline for
  the full deferral list.
