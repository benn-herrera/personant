# THESIS – Personant

Why this project has the shape it has

This thesis does not:

- Design user-facing behaviors or outcomes, that belongs to SPEC
- Put forth project mechanisms or code structure, that belongs to ARCHITECTURE
- Dictate project-specific process, that belongs to CONVENTIONS

## Personant in plain terms

Personant is a memory service for a coding agent running under opencode. It records every turn of
work into threads, one thread per problem being worked on, kept as plain text under git. For every
model request opencode makes, Personant composes the context from those records: a one-line entry
per past thread, the threads in play, and the current turn. A model makes only narrow judgments —
which thread a turn belongs to, a thread's summary at closure — and deterministic code writes every
byte that is kept.

Almost all of that is built from well-known pieces at the scale of one person's work. A few pieces
are new, and they are the reason the project exists. This page names both, so that work on a
standard piece gets the standard answer and attention goes to the new ones.

## The building blocks

Each row is a standard pattern at the size Personant needs. The last column names the larger
system the piece resembles and is not — the direction work on it must not drift.

| Personant piece | The standard pattern | It is not |
|---|---|---|
| The opencode plugin | A host plugin that forwards hooks and events to a local service and applies its answers; holds no state | A harness, UI or agent runtime, or a second writer of memory |
| The memory service | A single-writer local process reached over loopback HTTP + JSON | A network server, multi-tenant service or database server |
| The memory store | A directory of markdown files with YAML frontmatter and JSONL record files, versioned in git | A database engine, with SQLite only ever as a derived index; or an archive of the user's work, whose artifacts and history stay in the project's own version control |
| Durability and crash recovery | A write-ahead journal and marker file, git commits as recovery points, an idempotent reconcile at startup | A transaction manager or a replication system |
| The spine and the symbol index | JSONL index files derived from the canonical thread files and rebuildable from them | A search engine or a catalog service |
| Symbol extraction | A regex pass plus normalization rules over each event's text | An NLP pipeline or an entity linker |
| Recall | Lexical overlap and embedding similarity scored against thresholds; one background indexer publishing snapshots | A vector database or a retrieval service |
| Context composition | Prompt assembly into fixed slots, each with a byte budget, a priority order and truncation, fixed slots first for a stable prefix | A prompt framework or a general RAG pipeline |
| Content dedup | Content ids, diffs against the last kept version, references by id | A version-control system |
| Thread lifecycle | A state machine over threads with decay timers | A task tracker or a workflow engine |
| Model judgments | A prompt that asks a closed question or drafts text that deterministic code validates before writing | Free-form generation that decides what is stored |
| Archival | `git rm` plus an index entry; recovery from history | A backup system or tiered storage |
| The event log | A daily-rotated, append-only text log | A telemetry or metrics backend |
| The CLI | A cobra command tree over the store: init, index, verify, archive, version | An interactive front end |
| Directives | Markdown instruction files layered by precedence | A policy engine |
| Memory vetting | Review before commit against a fixed rubric, with a provenance record | An approval workflow system |
| Model access | Everything through opencode; Personant holds no endpoint, provider or key | An inference client or provider configuration |
| Checks | Unit tests, a scripted mock model, a deterministic simulation on a fake clock | A benchmark harness for models |

Where a piece is in this table, its conventional answer is the default. A design that departs from
the standard pattern states why, in ARCHITECTURE. A design that grows a piece toward its "is not"
column is out of scope, however natural the next step looks.

## What is new

Two things, and only these:

1. **Every request is composed, not replayed.** The standard assistant sends an accumulating
   session transcript and compacts it when it grows too long. Personant replaces both: each
   request is assembled afresh from memory, so nothing depends on a session's length and nothing
   is lost to compaction.
2. **Work is organized by problem, not by session.** The standard unit is the conversation.
   Personant's unit is the thread: the model names, each turn, the problem the turn works on; a
   thread rests and retires when work on it stops, not when a window fills; and a retired thread
   stays findable from its one-line entry.

The effect they produce together: one continuous career — work picks up across sessions and
across projects as if there had never been a boundary.

The machinery behind both is standard, and listed above. SPEC defines them.

## Three kinds of memory

The memory that steers the agent comes in three kinds, separated by scope.

- **Behavior memory**: how the agent thinks and conducts itself. General logic, comportment, and
  its approach to novel and to familiar situations. Universal.
- **Process memory**: how work is done on any project, whatever its topic. Practices that hold for
  every project, plus heuristics scoped to a category of project (a language, a platform, a kind of
  system) or to a role in the work (designing, implementing, reviewing). Its job is to raise the
  floor, so that human guidance is a delta on it rather than a bootstrap from scratch.
- **Career memory**: the toolkit. Techniques and solutions developed on specific projects that may
  serve others, grown beyond what base training supplies.

From a project, memory keeps only lessons worth carrying forward, filed under the kind of memory
they change, not the project they came from.

---

Volumes:

- [SPEC](SPEC/entry-point.md): Consumer-facing behaviors and outcomes
- [ARCHITECTURE](ARCHITECTURE/entry-point.md): How this projects' implementation meets requirements designated by SPEC
- [CONVENTIONS](CONVENTIONS/entry-point.md): Project-specific process and house rules
