# Personant — v0.1 Architecture Outline

**Status:** living design outline. Initial decisions from the 2026-05-07/08 design discussion; refined in subsequent passes (most recently 2026-05-09 — agent role and tool surface).
**Audience:** the author, future implementation work, future agents revisiting the design.
**This document is the *why*** — narrative rationale for design decisions. Companions:
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — compressed orientation for AI agents: principles, patterns, anti-patterns, navigation. Read first.
- [`spec.md`](spec.md) — field-level schemas, algorithms, surface APIs.
- [`AGENTS.md`](AGENTS.md) — house rules for agents working in this repo.

Tables, diagrams, and schemas that already live in `ARCHITECTURE.md` or `spec.md` are not reproduced here — outline.md is prose-narrative-shaped.

---

## What Personant is

Personant is a single-user, single-agent runtime for managing an AI assistant's working memory across arbitrary projects with **continuity**. The agent has one continuous career: a unified persistent memory, no session boundaries, no compaction-style information loss, and the ability to work across many projects with cross-project recognition.

Name: portmanteau of *personal assistant*. Latin *personare* (to sound through) and *persona* (the mask one speaks through) are bonus resonances rather than load-bearing meaning.

The system inverts the typical agent-app pattern: instead of letting the model do all the work and silently accepting context loss, Personant uses **deterministic state as canonical, LLM in narrow judgment roles, and human acks at high-leverage moments only**. This thesis holds every design decision together.

---

## Architectural thesis

> **Deterministic state as canonical, LLM in narrow judgment roles, human acks at high-leverage moments only.**

The tier-by-tier responsibility table is in `ARCHITECTURE.md`. This section captures the *why* behind the thesis.

The bet: deterministic mechanisms can do most of the load-bearing work, the LLM is leveraged surgically, and the human only intervenes where their input is the most valuable signal available. This is the opposite of "agent that figures it all out" and is what enables drift-resistance over months-long timeframes.

A red flag for any future design decision: it wants to push canonical state into the LLM, or wants to skip the human ack at a high-leverage moment. Either signals a likely mistake.

---

## Role and capability scope

The agent role is **research assistant**, not general-capability agent. This constraint is load-bearing — not a v0.1 limitation that lifts in later versions, but a deliberate boundary for two reasons:

1. **Focus.** The load-bearing innovation is the context-management mechanism (continuity across projects, drift-resistant memory, opportunistic recall). Bounding the action surface lets v0.1 actually exercise that mechanism instead of being absorbed into re-implementing the broader agent-coding surface area.
2. **Anti-folly.** Reproducing a fully-featured agent-coding system (a multi-year effort) is unnecessary at this scope, infeasible at this scale, and *undesirable* — that class of system represents an engineering failure mode worth not reproducing.

A research assistant **reads and thinks** rather than **builds and runs**. The capability surface follows:

- **External tool inventory is bounded** at twelve tools, split between read/think (`fs.read`, `fs.list`, `fs.grep`, `web.fetch`, `web.search`, `model.consult`) and draft/mutate (`fs.tmp_write`, `fs.tmp_read`, `fs.tmp_list`, `fs.propose_promote`, `fs.propose_rename`, `fs.propose_delete`).
- **The LLM never directly mutates the user's workspace.** Writes flow through `.personant/tmp/`; deterministic code promotes drafts to workspace targets through ack-gated `propose_*` operations. This narrows the LLM's failure surface and keeps every workspace mutation a recorded, acked event.
- **`.personant/` is the single spelunking location** for everything the agent does. No usage of `/tmp/`, no scattered state across the host system.
- **Permission accrual replaces per-call ack.** Ack prompts offer scope-grant keystrokes; granted scopes accumulate in `directives/prj_<n>/permissions.md`. Friction trends toward zero as the system learns the user's working pattern. Loud acks are reserved for genuinely consequential ops (deletes of tracked files, batches over a threshold, dirs containing foreign git repos).
- **User retains full shell power.** Constraining the *LLM* surface does not constrain the *user*. The personant prompt accepts `$ <cmd>` (run in user's shell, output streams to terminal) and `# <cmd>` (run and capture output into next-turn context) — user-initiated shell access that bypasses the LLM tool inventory entirely. The runtime owns a long-lived interactive shell subprocess across the personant session. Slash commands like `/cd-project`, `/project switch|rename`, `/model` provide user controls without involving the LLM.
- **Project identity is dual-layered.** Every project has a stable internal handle (`prj_<n>`) used as storage key and spine reference. When the project's git tree has a remote, the *normalized* remote URL is its canonical external identity — survives file moves, survives renames. Display name is mutable. Path is just an attribute. This means moving files on disk doesn't break project association, and `/project rename` is a metadata-only op.

See `spec.md` §4 for the user surface (slash commands, shell escape, project identity), §6 for the tool surface and permission policy, and §2.5.1 for the project metadata schema.

---

## Substrate

The substrate non-negotiables (text in git, JSONL records, markdown bodies, derived-not-canonical indexes, autonomic git management, git as archival substrate) are tabulated in `ARCHITECTURE.md`. This section addresses the *reasoning* for those choices and is the place to record substrate decisions as they evolve.

The reasoning underneath: text-in-git buys inspectability, recoverability, history, and branching for free. JSONL sorted by ID keeps git diffs record-grain rather than reformat-grain. The build/query layer is a thin Go package that reads JSONL into memory once per process; lookup is map-access at sub-millisecond scale. The storage choice is reversible — the query API is the contract, so a future SQLite *index* (canonical state staying as text) can be added without callers noticing.

Drift cannot accumulate: any derived index disagreeing with the canonical source loses; the index gets rebuilt. The pre-commit hook is what enforces this — same pattern lockfiles use.

### Implementation language

- **Personant runtime: Go.** Single-binary delivery, no runtime dependency on the host machine, goroutines map naturally onto the architecture (background curator + foreground turn handler), excellent stdlib coverage (file system, JSON, regex, http, sort), mature terminal libraries (bubbletea / lipgloss / tcell), cross-platform from one source. Verbosity-to-clarity ratio is well-suited to LLM-generated code.
- **Auxiliary Python scripts (where used): stdlib only.** Any helper scripts that aren't core runtime — ad-hoc analyses, one-off utilities, build scaffolding — must use only the Python standard library. No pip dependencies, no virtualenvs, no lockfiles. The stdlib (`json`, `re`, `pathlib`, `sqlite3`, `argparse`, `subprocess`, `urllib`) covers the use cases at hand. The constraint keeps blast radius small and lifetime long.

The Go runtime never has a Python dependency surface; Python utilities never accumulate a pip dependency surface. Both languages stay portable across their own version churn for years.

---

## Storage layout (default `~/.personant/`)

The full directory layout (spine, symbols, threads, projects, directives, logs, providers, archive index, last-active marker, tmp scratch, .git tree) is in `spec.md` §2.1, with the canonical / derived / operational / secret-bearing classification table. Storage is git-init'd on first run; the user gets free history and recoverability with no setup ceremony.

---

## Working-set composition (layered)

The layer table (E / A1 / A2 / B / C / current_turn with default budget percentages) and eviction order are tabulated in `ARCHITECTURE.md`. Detailed algorithms are in `spec.md` §3.1 and §3.0 (the context-modification event chain that does the per-event work).

The narrative reasoning: *Layers E and A are recognition surface; Layer B is active engagement.* Spine is always in window so the model can recognize prior topics directly. The model emits a topic tag → corresponding threads are fetched into B → the augmented context is what generates the response. **No speculative prefetch** — every loaded thread is loaded because the model said it was needed.

A and E are sacrosanct under budget pressure because losing them breaks recognition, which is the whole point.

**Content de-redundification** (v0.2; spec §3.9). When the same file content is included in working context across many turns (file read, modified, re-read), the runtime de-redundifies: literal current state at the most-recent position; N most-recent diffs in literal form; older states replaced with content-addressed identifiers. Persistent storage keeps the full diff chain with periodic literal anchors (every K-th change) to prevent cumulative drift. Diff format starts as unified diff with a planned natural-language fallback for cases where the model has trouble applying. The persistent vs. live distinction is structural — same primitives (content-addressed identifiers, diffs, anchor literals) serve different concerns.

---

## Threads

- **Unit:** a contiguous run of turns united by working on one specific problem. Coarser than a turn, finer than a session-or-project. The natural retirement boundary is "thread reached a resting point," not "context window pressure."
- **ID:** serial (`thr_<n>`), assigned by `nextId()` in deterministic code. Sortability is free.
- **Project tag:** every thread carries a `project` field. Threads without an obvious project default to the active project's tag.
- **Lifecycle:**
  1. **Created** when model emits `*new-topic*` in topic tag, OR explicit `/topic <name>` from user.
  2. **Active** in Layer B while engaged (model tags it in per-turn topic list).
  3. **Drifts to Layer C** as engagement decays (no longer in model's recent topic tags).
  4. **Retires to Layer A** (spine only) on closure, with full content in `threads/thr_<id>.md`.
  5. **Archives off-spine** (deep cold) only if spine cardinality pressure builds; recoverable by explicit fetch.

---

## Spine entry format (schema)

The full schema (field types, validation constraints, JSONL wire form, display rendering) is in `spec.md` §2.2. The narrative point worth holding onto:

> The spine line is the load-bearing artifact. It must encode enough decision-state that the model can answer most "what was the outcome of X" questions from the line alone, without needing to fetch the thread file.

Display form, briefly:

```
thr_<id> [<4–8 anchors>] — <100–150 char gist> [<state>]
```

Example lines (the gist is what the model reads to recognize a prior topic):

```
thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict; entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]
thr_92 [neutrino, helical-screw, cosserat, corrigendum] — closed-unknot → open-helical-screw correction across KB + LaTeX [resolved 2026-05-06]
```

The anchor set is what makes recall possible. The summary is what makes recognition cheap. Together they're the *whole* recognition surface for retired threads — getting them right at retirement is the point of the curator-summary ack flow.

---

## Symbol-anchor extractor (first working definition)

Three passes, ordered cheapest-first:

1. **Deterministic** (every turn) — regex/parser scan over turn content extracts identifiers (file paths, URLs, code symbols, project-configured patterns like AVE-KB's six-char claim IDs, user `#tags`).
2. **Model-emitted** (every turn) — model's topic tag includes per-thread symbol list of named entities/concepts ("engages thr_42 via [Cosserat sector, neutrino oscillation, helical screw]").
3. **Curator** (at retirement) — LLM picks 4–8 anchor symbols from accumulated history; user-tagged symbols auto-promote to anchors. User ack at retirement gates the choice.

**Symbol categories** (for v0.1):
- *Identifiers* (deterministic, pattern-extractable)
- *Entities* (model-emitted, named concepts)
- *Tags* (user-emitted, `#hash-style`)
- *Phrasal concepts* — deferred to v0.2.

**Normalization:** lowercase, hyphenate internal whitespace, strip non-essential punctuation, drop stop words from multi-word entities. Identifiers preserved exactly (case-sensitive). Stored as `{raw, normalized}` pair.

**Anchors vs. history:** anchors are the curated 4–8 that drive recall and surface in the spine; history is the soft-capped (~30–50) accumulated set used as a deeper match surface but not surfaced.

**Failure modes & v0.1 handling:**
- Hallucinated model emissions — validate against turn substring; discard if no match.
- Synonym fragmentation — accept in v0.1 (model handles equivalence at recall time); v0.2 may add cluster resolution.
- Low-info leakage — stopword filter + minimum-length + per-project ignore list.
- Project namespace collisions — symbols scoped by project context.

---

## Recall mechanisms

Three complementary mechanisms, layered by cost:

1. **Model-native recognition** (default). The spine is in window; the model recognizes prior topics directly when a turn engages them. Emits topic tag at turn start. Handles synonyms and paraphrases for free since the model is the recognition oracle. No new ML infrastructure.
2. **Opportunistic surfacing** (deterministic backstop). When current-turn symbols (or embeddings) match a retired/latent thread anchor above threshold, surface a prompt: *"We talked about X 2 days ago — pick up there?"* User ack drives explicit fetch.
3. **Cross-project recall** (Layer A2 → fetch). Per-project anchor digests in A2 are scanned each turn. When current-turn symbols match an other-project anchor, the *"reminds me of what we did on Peanut Butter Surprise"* surface fires. Same opportunistic mechanism, slightly higher threshold.

**Decline categorization** (matters for accrual):
- *Not relevant* → improve matching, do not tighten threshold.
- *Not now* → no parameter change.
- *Stop offering* → tighten threshold.

Without this, declined offers due to bad matches accidentally suppress good offers too.

---

## Closure and retirement

**Triggers:**
- Auto-prompt when thread has had no engagement for `engagement.decay-turns` turns AND `engagement.decay-time` wall-clock.
- Explicit `/done` from user.

**Engagement signal** is symbol-anchored, not raw turn count: a turn engages a thread if the model's topic tag includes it OR if the turn's symbols overlap the thread's anchors above threshold. Multi-thread interleaving doesn't fire spurious decay; light reference doesn't reset the timer.

**Flow:**
1. Curator drafts retirement summary + anchor symbol set.
2. User acks (single keystroke), edits, or defers.
3. On ack: thread file written to `threads/thr_<id>.md`; spine entry generated; Layer B/C eviction follows.

**The ack is the integrity gate** — the user catches misclassification at the moment of retirement, the only point where its accuracy matters most. This is high-leverage human verification baked into the natural flow.

**Deep cold archival** (v0.2; spec §3.8) is the next stage past retirement: when spine cardinality pressure builds, the runtime archives the oldest retired threads off-spine via `git rm` + `git commit` + an `archive/index.jsonl` entry. Recovery via `git show <commit>:<path>` is one command; the entry preserves the spine summary and anchors so search across archived material is possible without recovering the file. No bespoke archive format — git's existing content-addressed object store does the work.

---

## Fallback dissection (worst-case path)

When budget pressure exceeds threshold (default 90%) AND normal retirement isn't catching up:

- Dissector LLM clusters the oldest poorly-threaded content into semantically related groups.
- Each cluster produces a thread file + spine entry, identical in shape to a normal retirement.
- User-acked under default conditions (ack-or-defer prompt with proposed clustering); absolute-emergency mode dissects without ack and notifies after.

This is structurally the same as normal retirement, *reactively* triggered on poorly-threaded content. Same artifacts produced; same recovery path; same invariant preserved.

Handles three failure modes with one mechanism: wandering user, single-thread overrun, never-cleanly-threaded content. Quality is best-effort by design — graceful degradation under stress, not catastrophic loss.

---

## Multi-project model

- **One unified spine** across all projects.
- **`project` tag** on every thread.
- **Layer A1** = current project full spine; **Layer A2** = compressed cross-project digest (~150 bytes per other project: project-level summary + recent anchor symbols).
- **Cross-project recall** as a designed feature, not an emergent property. Fires through the same opportunistic surfacing mechanism with a higher threshold.

This is what makes the agent's *career* possible — it accumulates experience across all projects and can transfer across domains. The architectural commitment is: one entity, many projects, lifelong accrual.

---

## Personalization (directive files)

Behavior tuning lives in inspectable directive files that accrue under the hood from feedback signals:

- **System defaults:** `directives/defaults.md` — initial values, baked in.
- **User overrides:** `directives/user.md` — accrue from explicit user instructions, decline categorizations, accept/decline running statistics, spontaneous-curiosity signals.
- **Project overrides:** `directives/prj_<n>/*.md` — project-specific tuning.

The directives are text files. Inspectable, editable by hand, portable, version-controlled. This is the property that elevates the system from "smart assistant" to "real research partner": the agent learns how the user works, *and the user can read what it learned and correct it.*

Same pattern extends naturally to closure prompting frequency, prefetch aggressiveness, interruption tolerance, summary verbosity, etc. Each directive file is a small text file accruing independently.

---

## Bootstrap defaults

The full parameter table (engagement decay, recall thresholds, layer budgets, anchor caps, etc.) lives in `spec.md` §2.6.1 — that's the single source of truth and the first place to look when calibrating. The values are first-pass guesses; the calibration regime (spec §11.8) tunes them empirically against canonical workloads.

**First-run behavior:** empty spine → opportunistic recall structurally disabled (no false "we discussed this before"). Project resolved per the §4.5.7 bootstrap waterfall (git remote → CWD path → last-active prompt → fallback). No tutorial / onboarding ceremony — the agent is ready. A `system.bootstrap` event lands in today's log with version, paths, and defaults snapshot.

---

## Logging

Format and event vocabulary are in `spec.md` §2.8. The narrative point: **liberal logging — when in doubt, log it.** Cost of a log line is essentially zero; the value of having a forensic record when something surprises you later is high. Schema-promote frequently-queried log patterns to structured form when patterns emerge — not before.

---

## Watch list (known weak spots; instrumentation will reveal scale)

The numbered watch list (the canonical, navigable form, kept up-to-date as items resolve or new ones surface) lives in `spec.md` §12. The narrative point worth holding onto:

> The first-pass design is guaranteed to have holes. Instrumentation is what makes v0.2 design empirical instead of guessed.

Categories of known weakness, broadly:
- **Symbol-extraction noise** — synonym fragmentation, low-information leakage, model emission drift.
- **Recall precision** — cross-thread symbol collision, anchor selection quality at retirement.
- **Deterministic-pass coverage** — per-project regex curation needs.

These aren't blockers; they're *expected* findings the simulation regime (spec §11) is designed to surface and quantify.

---

## Out of scope for v0.1

### Deferred (may revisit in later versions)

- **Multi-user** — v2.0 boundary; locked.
- **Configuration migration on system upgrade** — v1.0+ concern, not blocking; will not be intractable when it arrives.
- **UI specifics** — borrow patterns from Claude Code / OpenCode rather than reinventing prompts and slash commands.
- **Encrypted at-rest storage** — back-of-mind; continuous-running undermines most of its value.
- **Synonym cluster resolution** — deferred to v0.2 unless empirical pressure forces earlier.
- **Sub-agent runtime extension** — architecture supports it (sub-agents would have their own spines, threads, directives in the same substrate, with optional cross-agent visibility); instantiate when a sub-agent use case actually demands it.
- **Symbol decay over time** — defer; storage cost is negligible at v0.1 scale.
- **Phrasal-concept symbol extraction** — deferred to v0.2.
- **Computational research workflow** — targeted for v1.0. Python authoring (via the existing `propose_promote` flow) *plus execution* of scripts, simulations, and formatter/linter tooling (`black`, `isort`, `flake8`) for math/physics projects. Expands the role from "reads and thinks" to "reads, thinks, and computes." Requires a sandboxed subprocess execution surface for the LLM, stdout/stderr capture, resource limits, and lifecycle management. **Not on the v0.1 dev list** — held until the memory-context mechanism is proven out. The shell-execution exclusion in role scope lifts only for this targeted capability when it lands; it does not become a general "agent can now run anything."

### Outside role scope (research-assistant boundary)

These are *not* deferred-to-future-versions items — they are deliberate non-goals consistent with the research-assistant role. They do not enter scope by accident.

The exclusions below are at the **LLM tool inventory** layer. The deterministic runtime may perform analogous operations *autonomically* (without LLM or user direction at the moment of invocation) when they are required to maintain the canonical KB — most notably autonomic git management of `~/.personant/` itself (init, add, commit, pre-commit hook), and read-only git queries against the workspace (`git ls-files`, `git status`, `git diff`) for permission-tier classification and ack-prompt diff rendering. What follows is what the *LLM* cannot do.

- **Shell command execution and subprocess spawning** — outside research-assistant role.
- **LLM-controlled git operations** — the LLM has no `git` tool. The user owns workspace git; the runtime owns `~/.personant/` git autonomically.
- **Package management, build, test orchestration** — outside research-assistant role.
- **IDE / editor integration** — outside research-assistant role.
- **Deployment, service control, running-system manipulation** — outside research-assistant role.
- **Direct filesystem writes outside the `propose_*` channel** — every workspace mutation must be a recorded, acked event.

---

## Recap: pattern consistency as design signal

The thesis recurs at every layer of the design — from how spine entries get curated to how directive files accrue to how cross-project recognition fires to how workspace mutations route through `propose_*`. That consistency is the strongest signal the design is converging toward something coherent rather than a collection of clever pieces. The pattern itself, applied to a new design question, is usually the right move.

The success criterion: months-long seamless continuity. Externally, the user just talks to a partner who picks up where they left off. Internally, the substrate is doing constant work to make that appearance honest.

**Operationalized acceptance (v0.1):** the system passes a **six-month simulated workload** with synthetic-but-realistic operation patterns. Memory quality maintained throughout. Zero out-of-context-space events. Operation runtime costs (recall, retirement, archival, resurrection from deep cold) measured and stable. After six simulated months the system must be in a state demonstrating it could run another six months without degradation. The measurement regime — see spec.md §11 — is what makes "months-long continuity" a proven property rather than an aspiration; iterating the underlying techniques without it reduces to guesswork.

---

## Project lineage / shape

The architecture is shaped more like an **agent runtime** than a chat app. Memory hierarchy, paging mechanism, lookup index, personalization layer, persistence substrate, instrumentation, multi-agent capability. The naming follows: Personant is a runtime, not a feature set.

The design originated in conversation 2026-05-07/08, building on observations about Claude Code's compaction-driven information loss, the AVE-KB's text-in-git knowledge structure, and the asymmetry between current AI systems' session-bounded context and what a long-term research partnership actually requires. AVE-KB conventions (text + git, frontmatter, claim-quality propagation, derived indexes) were the inspiration; the system is independent.

---

## Companion documents

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — orientation, principles, patterns, anti-patterns, navigation map. Read first.
- [`spec.md`](spec.md) — field-level material:
  - Storage schemas (JSONL): §2
  - Context-modification event chain: §3.0
  - Working-set composition: §3.1
  - Deep cold archival (v0.2): §3.8
  - Working-set content dedup (v0.2): §3.9
  - User surface (CLI + slash commands + shell escape + project root): §4
  - Tool surface and permission tiers: §6
  - Bootstrap, lifecycle, providers.toml, pre-commit hook: §8
  - Implementation milestones: §10 (Phase 1 done; §10.1 tracks v0.2 / v1.0)
  - Measurement and validation regime: §11 (six-month simulation acceptance gate at §11.1)
  - Watch list: §12; open questions: §13
- [`AGENTS.md`](AGENTS.md) — house rules for AI agents in this repo.

The outline is for orientation in narrative form; ARCHITECTURE.md is for orientation in compressed form; spec is for execution. All four are living and edited in place as decisions evolve.
