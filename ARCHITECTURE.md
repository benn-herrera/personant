# Personant — Architecture

**Audience:** AI agents (and new contributors) who need to orient quickly. Companions:

| Document | Purpose |
|---|---|
| `ARCHITECTURE.md` (you are here) | principles, patterns, mental models — read first |
| `AGENTS.md` | house rules for AI agents working in this repo |
| `outline.md` | design *narrative* — the *why* of every decision |
| `spec.md` | operational *specification* — field-level schemas, algorithms, APIs |
| `README.md` | user-facing description; getting started |

Read this file first. Descend into the others for detail.

**Reading time:** ~10 minutes.

---

## What this is

Personant is a single-user, single-agent runtime that gives an AI assistant **persistent working memory across arbitrary projects with continuity**. The agent has one continuous career — no session boundaries, no compaction-driven information loss, cross-project recognition.

The role of the agent is **research assistant**, not general-capability. This constraint is load-bearing — Personant is not, and will not become, a polyglot agent-coding system.

---

## The architectural thesis (load-bearing)

> **Deterministic state as canonical. LLM in narrow judgment roles. Human acknowledgement at high-leverage moments only.**

This thesis recurs at every layer. When making a design decision, ask:

1. Could deterministic code do this without involving the LLM?
2. If the LLM is involved, is its role narrow and well-bounded?
3. Is there a human ack at this point because it's high-leverage, or out of caution?

| Tier | Role |
|---|---|
| Deterministic (Go runtime) | canonical state holder; integrity enforcer; build/query/index operations; autonomic git management of `~/.personant/` |
| LLM | narrow generative/judgment roles: topic tagging, summary drafting, anchor selection, recognition, dissection clustering |
| Human | final ack at three load-bearing moments: closure (retirement), opportunistic recall surface, fallback dissection trigger |

**Two warning signs that a proposed change is wrong:**

- It pushes canonical state into the LLM. (E.g., "let the model summarize threads on the fly." No: the curator-summary at retirement is the canonical text; the model wrote it once, deterministic code stores it.)
- It removes a human ack at one of the three load-bearing moments. Or it adds an ack at a non-load-bearing moment, increasing friction without value.

---

## Recurring patterns

These patterns appear at multiple layers. Recognize and use them.

### Propose-and-ack

The LLM never directly mutates state that matters. It *proposes*; deterministic code *executes* on user ack.

- **Closure:** curator drafts retirement summary → user acks → deterministic code writes thread file + spine entry.
- **Workspace writes:** LLM writes to `.personant/tmp/foo.md` via `fs.tmp_write` → emits `fs.propose_promote(tmp_id, target_path)` → user acks → deterministic code moves the file.
- **Recall surfacing:** deterministic match fires → user acks → explicit fetch into Layer B.

The ack is the **integrity gate** at the moment its accuracy matters most.

### Autonomic vs LLM-controlled

"Out of scope at the LLM tool layer" ≠ "out of scope at the runtime layer."

The runtime performs many operations the LLM cannot. Most notably **git**:

- The runtime owns `~/.personant/`'s git tree autonomically (init, add/commit on every canonical mutation, pre-commit hook). Same lifecycle status as writing to `spine.jsonl`.
- The runtime issues read-only git queries against the user's *workspace* (`git ls-files`, `git status`, `git diff`) for permission-tier classification and ack-prompt diff rendering.
- The LLM has **no git tool**.

When something feels like it needs to "be done," ask: can the runtime do this autonomically? If yes, that's the answer.

### Single spelunking location

Everything Personant does lives under `~/.personant/`. No `/tmp/`. No state scattered across the host.

Forensic inspection is `cd ~/.personant && grep -r ...`. If something can't be found there, it shouldn't exist.

### No speculative prefetch

Every loaded thread is loaded because the model said it was needed.

Spine is always in window → model recognizes prior topic from spine entries → emits topic tag → corresponding threads fetched into Layer B → response generated. The model is the recognition oracle, not a heuristic guess.

### Asymmetric cost: prove ahead of time

When the cost of being wrong is far higher than the cost of being right, the bar shifts toward "prove it."

- The architectural thesis cannot be validated by inspection. The §11 measurement regime exists because "use it and find out for six months and then revise if it's wrong" has no graceful recovery — you'd have to either complex-refactor accumulated memory or lose it all.
- The migration-cost asymmetry favored YAML over TOML for thread frontmatter: switching later costs a script + validation pass over every thread file; the immediate gain from going off-standard was modest. Pick the standard path.

When evaluating a "modest gain now" decision, explicitly compute the migration cost if we change our mind later. If it's high, default to the standard path.

### Approved-when-earned dependencies

Stdlib-first, but a dep that earns its keep is fine. Two failure modes to avoid:

1. Reaching for a dep before there's a real consumer (speculative scaffolding).
2. Implementing complex stdlib substitutes for a dep that would just work (NIH).

Approved deps land *with their consumer*, not before. See the substrate-decisions memory for the catalog.

**Permanently out** (architectural conflict): `langchaingo/memory`, `langchaingo/chains`, `mattn/go-sqlite3`. SQLite as canonical state is rejected — the canonical form is text + git.

### Constraint as feature

"We don't do X" is often a load-bearing claim, not a TODO.

Personant's "research assistant, not polyglot agent-coding tool" framing is what makes the project tractable. v1.0 will add Python execution for math/physics simulation; that's a *targeted* lift of the shell-execution exclusion, not a general "agent can run anything."

When pressure builds to "just add X," ask: is this constraint load-bearing, or accidental?

---

## Substrate non-negotiables

| Decision | What | Why |
|---|---|---|
| Storage | text files in git | inspectability, history, recoverability, free `git log` / `diff` / `grep` |
| Structured records | JSONL, sorted by id | line-grain diffs, not record-grain reformat |
| Thread bodies | markdown + YAML frontmatter | hand-readable; standard tooling (Obsidian, Pandoc) |
| Configuration | TOML (`providers.toml`) + markdown directives | hand-editable; secret-bearing files separated |
| Derived files | regenerable from canonical | drift cannot accumulate; pre-commit hook fails on stale |
| Git management | autonomic on `~/.personant/`, never on user's workspace | runtime owns its house; user owns theirs |

All of these are load-bearing. A change that violates them is a red flag.

---

## The §3.0 hook chain (load-bearing primitive)

Personant treats the working window as a sequence of **context-modification events**, not user-agent turns. The "turn" is a logging convenience; the load-bearing primitive is the per-event hook chain. The principle: **no gaps**.

**Event taxonomy:**

- `user.prompt`, `user.shell-capture`
- `model.response`, `tool.result`
- `thread.fetched`, `digest.refresh`
- `slash.injected`, `directive.reloaded`

**Every event runs the chain in this fixed order:**

1. **Symbol extraction** (§3.3) — deterministic regex pass + parse model emissions.
2. **Engagement signal** (§3.2) — coalesced per-turn (multi-tool-call turns don't multiply `turn_count`).
3. **Dedup decision** (§3.9) — content-addressed identifier replacement (v0.2).
4. **Budget check** (§3.1) — bump-eviction so the budget is honored before the next event lands.
5. **Logging** (§2.8).

**Implementation contract** (§3.0.5): a single internal `onContextDelta(source, content, metadata)` entry point. **No path mutates the working window without going through it.** Code review treats any direct mutation as a bug.

When you write code that adds new content to the working window: route it through `onContextDelta`. Don't invent a side-channel.

---

## The layered working set

Per-turn context is composed from layers with explicit byte budgets:

| Layer | Content | Default | Loading |
|---|---|---|---|
| E | directives + project conventions | 8% | always |
| A1 | active project's spine entries (full) | 8% | always |
| A2 | other projects' digests (compressed) | residue ~4% | always |
| B | actively engaged threads (top-K full bodies) | 50% | populated by topic tag |
| C | recently dormant threads (summaries) | 15% | decay-driven from B |
| current_turn | user input + model response | 15% | reserved |

**Eviction order under budget pressure:** C-oldest → B-oldest (compressed to summary). A and E are sacrosanct.

Layers E and A are the *recognition* surface; Layer B is *active engagement*. The model recognizes prior topics from A1 + A2; emits a topic tag → corresponding threads fetched into B → response generated against augmented context.

---

## Key abstractions

| Concept | Definition | Spec |
|---|---|---|
| **Spine record** | one line of `spine.jsonl`; canonical thread bookkeeping (id, project, anchors, summary, state, timestamps, turn_count, recall_fires) | §2.2 |
| **Thread** | contiguous run of turns united by working on one specific problem; identified by `thr_<n>` (stable forever); has a markdown file with YAML frontmatter | §2.3 |
| **Project** | three-layer identity: stable handle `prj_<n>`, mutable display name, optional canonical external identity (normalized git remote URL) | §2.5.1, §4.5 |
| **Anchor symbol** | curated 4–8 normalized symbols per thread; drive recall and surface in spine line | §2.7 |
| **History symbol** | per-thread accumulated symbol with `raw`, `normalized`, `first_seen_turn`, `count`, `source`; capped per `history.cap-per-thread` | §2.3 |
| **Symbol category** | `identifier` (preserve case), `entity` (lowercase + hyphenate), `tag` (lowercase, leading `#` stripped) | §2.7.1 |
| **Symbol source** | `deterministic` / `model` / `user` / `curator`; dominance: `curator > user > model > deterministic` | §2.7.3 |
| **Context-modification delta** | one event to the chain (§3.0); content-modifying event with source + content + metadata | §3.0 |

---

## Tool surface (bounded, role-shaped)

**Twelve external tools** total. Read-and-think (6) + draft-and-mutate (6). The LLM never names a workspace path on a write call; all mutations go through `fs.propose_*` (ack-gated).

| Read / think | Draft / mutate |
|---|---|
| `fs.read` | `fs.tmp_write` |
| `fs.list` | `fs.tmp_read` |
| `fs.grep` | `fs.tmp_list` |
| `web.fetch` | `fs.propose_promote` |
| `web.search` | `fs.propose_rename` |
| `model.consult` | `fs.propose_delete` |

**Out of scope (intentional, not deferred):** shell execution, git operations *by the LLM*, package management, build, test orchestration, IDE integration, deployment, direct filesystem writes outside `propose_*`. The runtime may perform analogous operations *autonomically*; the exclusion is at the LLM tool layer.

**v1.0 will add Python-only computational tools** (`python.run`, `python.format`, `python.lint`) for math/physics simulation. Targeted; not a general "agent can run anything."

---

## Permission tiers

Three tiers, with accrual via directive files (`directives/<project>/permissions.md`):

| Tier | Default | Covers |
|---|---|---|
| 0 (silent) | never ack | reads; writes inside `.personant/**`; `fs.tmp_*` |
| 1 (first-time ack with scope grant) | ack once, then auto-allow within granted scope | `propose_promote` (new + overwrite); `propose_rename` to workspace |
| 2 (always individual ack) | every time | `propose_delete` on tracked files; rename overwriting tracked; batches over a threshold; sensitive patterns |

The ack prompt is the accrual UI: single-keystroke `[y]/[d]/[p]/[n]/[e]` choices grant scopes that accumulate in directive files. Friction trends toward zero as the system learns the user's working pattern.

The active-project boundary is an additional axis: ops on paths *outside* the active project root bump tier (§6.2.6).

`providers.toml` is **secret-bearing** with a hybrid redaction policy: agent-initiated reads refuse outright; user-initiated `#` captures redact before reaching context.

---

## Project identity (dual-layered)

| Layer | Form | Mutable? | Purpose |
|---|---|---|---|
| Internal handle | `prj_<n>` | no — assigned at creation | storage key (`projects/prj_<n>/`); spine reference |
| External canonical identity | normalized git remote URL | yes (rare) | "is this the same project I've seen before?" — survives file moves and renames |
| Display name | user-chosen string | yes (`/project rename`) | UI rendering, slash-command targeting |

**Bootstrap waterfall** at startup (§4.5.7):

1. `--project` flag (deterministic override).
2. Git remote of CWD → known project's `remote_urls` / `historical_remote_urls`.
3. CWD path → known project's `current_root_path` / `historical_root_paths`.
4. `last-active` confirmation prompt.
5. Final fallback: `[c]reate / [s]witch / [n]o project`.

When a project gains a remote URL after creation (local-only → published), the runtime detects and offers to adopt it as canonical identity.

---

## Testing as the lab bench

Spec §11 is **not a quality gate bolted on after features land**. It is the measurement instrument by which the architectural thesis gets proven empirically — and the substrate by which the techniques evolve iteratively.

The cost of getting this wrong without proof ahead of time is **asymmetric and severe**: six months of accumulated memory state under a structurally-wrong storage strategy has no graceful recovery. The simulation regime exists *because* proof must come ahead of time.

**Layers** (in build order):

1. **Unit tests** — deterministic state operations, JSONL round-trips, schema validation.
2. **Scenario tests** — handwritten lifecycle flows; mock LLM via canned responses; metrics emission per scenario.
3. **Invariant validators** — callable from any test (spine integrity, index drift, project refs, last-active validity, frontmatter-spine sync, engagement consistency).
4. **Churn tests** — randomized seeded sequences; invariants after every operation.
5. **Calibration tests** — same scenario, parameter sweeps; metrics matrices identify operating points.
6. **Cross-run baseline comparison** — stored baselines (git-tracked); regression detection.
7. **Six-month simulation harness** — synthetic workload, logical-clock acceleration, steady-state assertions.

**v0.1 acceptance is the six-month simulation passing**, not Phase 5 feature-completeness. Memory quality maintained throughout, zero out-of-context-space events, runtime costs bounded and stable, steady state demonstrated such that another six months would run without degradation.

When making technique changes (parameter tweaks, algorithm adjustments), the question is always "what does the simulation say?" — not "does it compile and pass invariants?" Bake metrics emission into new code from day one; bolting it on later is much more expensive.

---

## Out of scope (deliberately)

Two distinct categories, often confused:

### Outside role scope (research-assistant boundary; permanent)

The LLM has no tool for any of these:

- Shell command execution and subprocess spawning
- Git operations *by the LLM* (runtime does its own; user does theirs)
- Package management, build, test orchestration
- IDE / editor integration
- Deployment, service control, running-system manipulation
- Direct filesystem writes outside the `propose_*` channel

These are not "v0.2 / v0.3" — they are role-bounded.

### Deferred (real future work; on the roadmap)

- Multi-user (v2.0; locked)
- Configuration migration on system upgrade (v1.0+)
- Encrypted at-rest storage (back-of-mind)
- Synonym cluster resolution (v0.2+ if empirical pressure)
- Sub-agent runtime extension (when use case demands)
- Phrasal-concept symbol extraction (v0.2)
- Computational research workflow — Python only (v1.0; *not* polyglot)
- Deep cold archival via git (v0.2)
- Working-set content dedup (v0.2)
- REPL line editing + history (v0.1 polish)
- Shell escape (`$`/`#`) implementation with long-lived `$SHELL -i` subprocess (v0.1 polish; PTY mode-handoff for nested apps held until empirical pressure)

---

## Anti-patterns to push back on

If you see one of these proposed (or are about to write it), stop and surface the concern:

| Anti-pattern | Why it's wrong | Reach for instead |
|---|---|---|
| "Let the LLM decide where to put state" | Pushes canonical state into the LLM | Deterministic code holds state; LLM proposes via tmp + ack |
| "Skip the ack at closure to make it faster" | Removes the integrity gate at its highest-leverage moment | Friction at high-leverage moments is a feature |
| "Use SQLite for the spine" | Substrate violation; loses inspectability + git-diffability | Text + git; SQLite stays in reserve as a *derived* index if scale forces it |
| "Have the LLM run `git commit`" | Mixes LLM-controlled with autonomic git | Runtime commits autonomically; LLM has no git tool |
| "Add this dep speculatively" | Approved-when-earned violation | Wait until consumer is real; bring dep in *with* consumer |
| "Tests can come later" | Asymmetric-cost violation | Measurement infrastructure built alongside features, not after |
| "Mock LLM is too narrow; let's use a real LLM in tests" | Adds non-determinism, network dependency, cost | Mock with scripted/generated responses; real LLM for `make integration-test` only |
| "Just add `vim` shell-escape support" | Pulls in PTY mode-handoff complexity | Held until empirical pressure; user can suspend personant |
| "Speculatively prefetch threads we *might* need" | Violates "every loaded thread is needed" | Model emits topic tag; runtime fetches what was named |
| "Shorten the topic-tag instruction to save tokens" | Topic-tag emission is the load-bearing recognition signal | The system prompt's topic-tag directive is non-optional |

---

## Document map

| You want | Look here |
|---|---|
| Architecture orientation | `ARCHITECTURE.md` (this file) |
| House rules for AI agents | `AGENTS.md` |
| The *why* of decisions, narrative form | `outline.md` |
| Field-level schemas + algorithms + APIs | `spec.md` |
| User-facing description, getting started | `README.md` |
| Substrate-level decision history | persistent memory: `project_personant_substrate.md` |
| v0.1 acceptance criteria | `spec.md` §11.1 |
| Roadmap (phases, post-v0.1) | `spec.md` §10, §10.1 |
| Open questions (numbered, navigable) | `spec.md` §13 |
| Watch list (known weak spots) | `spec.md` §12 |

---

## How decisions evolve

The first-pass design is guaranteed to have holes. Personant evolves by:

1. **Watch list** (`spec.md` §12) — named known weak spots. Instrumentation surfaces them; v0.2+ work targets them.
2. **Open questions** (`spec.md` §13) — inline `[OPEN: ...]` markers, aggregated; calibration runs decide.
3. **Calibration scenarios** — parameter sweeps over canonical workloads; metrics matrices identify operating points.
4. **Directive accrual** — per-user / per-project tuning happens automatically through ack patterns.

When you propose a change, ask:

- Does it have a measurement story? (How will we know it worked?)
- Does it commit to a specific value, or open a calibration window?
- Does it preserve or invert a recurring pattern (above)?
- Does it create migration cost we can't recover from?

Honest answers shape whether the change lands now, lands behind a directive, lands behind a watch-list entry, or stays an open question.

---

## Mental model summary

If you internalize one thing from this document, make it this:

> The runtime is the canonical-state holder. The LLM is a narrow judgment instrument. The human appears at three high-leverage moments. Every working-window mutation goes through the §3.0 chain. Every dependency earns its keep with a real consumer. Testing is the lab bench, not a quality gate. Constraints are load-bearing.

Recognize these patterns, push back on violations, and you'll be on safe ground. Read `outline.md` for the rationale; read `spec.md` for execution detail.
