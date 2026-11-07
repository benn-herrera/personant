# THESIS – Personant

Why this project has the shape it has

This thesis does not:

- Design user-facing behaviors or outcomes, that belongs to SPEC
- Put forth project mechanisms or code structure, that belongs to ARCHITECTURE
- Dictate project-specific process, that belongs to CONVENTIONS

## Personant in plain terms

Personant is a memory system for a coding agent running under opencode. It keeps a durable record
of the agent's work, organized by the problem being worked on, and for every model request it
decides what of that record the model sees. A model makes only narrow judgments; deterministic code
writes everything that is kept.

Almost all of that is built from well-known pieces at the scale of one person's work. A few pieces
are new, and they are the reason the project exists. This page names both, so that work on a
standard piece gets the standard answer and attention goes to the new ones.

## The building blocks

Each row is a standard pattern at the size Personant needs. The last column names the larger
system the piece resembles and is not — the direction work on it must not drift.

| Personant piece | The standard pattern | It is not |
|---|---|---|
| The opencode plugin | A host plugin: a stateless adapter that forwards to a local service and applies its answers | A harness, UI or agent runtime, or a second writer of memory |
| The memory service | A single-writer local process | A network server, multi-tenant service or database server |
| The memory store | Human-readable text files under version control | A database engine; or a mirror of the user's repository: the project's files and history stay in its own version control |
| Durability and crash recovery | Write-ahead journaling, commit points to recover to, an idempotent reconcile at startup | A transaction manager or a replication system |
| Indexes | Derived index files, rebuildable from the canonical files | A search engine or a catalog service |
| Symbol extraction | Pattern matching plus normalization rules | An NLP pipeline or an entity linker |
| Recall | Lexical and embedding similarity scored against thresholds, indexed in the background | A vector database or a retrieval service |
| Context composition | Prompt assembly into budgeted slots in priority order, stable parts first | A prompt framework or a general RAG pipeline |
| Content dedup | Content addressing and diffs | A version-control system |
| Thread lifecycle | A state machine with decay timers | A task tracker or a workflow engine |
| Model judgments | A prompt that asks a closed question, or drafts text that deterministic code validates before writing | Free-form generation that decides what is stored |
| Cold storage | A local, internal tiered storage and retrieval scheme | A backup system or external tiered storage |
| The event log | An append-only, rotated text log | A telemetry or metrics backend |
| The CLI | A subcommand tree for maintenance of the store | An interactive front end |
| Directives | Instruction files layered by precedence | A policy engine |
| Career memory | Notes on techniques and solutions from past work, drawn from the record and recalled by relevance | A code library or package registry, or a copy of past projects' code |
| Memory vetting | Review before commit, with a provenance record | An approval workflow system |
| Model access | Calls made through the host | An inference client or provider configuration |

Where a piece is in this table, its conventional answer is the default. A design that grows a
piece toward its "is not" column is out of scope, however natural the next step looks.

## What is new

Two things, and only these:

1. **Every request is composed, not replayed.** The standard assistant sends an accumulating
   session transcript and compacts it when it grows too long. Personant replaces both: each
   request is assembled afresh from memory, so nothing depends on a session's length and nothing
   is lost to compaction.
2. **Work is organized by problem, not by session.** The standard unit is the conversation.
   Personant's unit is the problem being worked on: its record outlives the session it started in,
   rests when work on it stops, and stays findable afterwards.

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

---

Volumes:

- [SPEC](SPEC/entry-point.md): Consumer-facing behaviors and outcomes
- [ARCHITECTURE](ARCHITECTURE/entry-point.md): How this projects' implementation meets requirements designated by SPEC
- [CONVENTIONS](CONVENTIONS/entry-point.md): Project-specific process and house rules
