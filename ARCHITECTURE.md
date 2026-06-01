# Personant — Architecture

**Audience:** AI agents (and new contributors) who need to orient quickly. Companions:

| Document | Purpose |
|---|---|
| `ARCHITECTURE.md` (you are here) | principles, patterns, mechanisms, mental models — read first |
| `AGENTS.md` | house rules for AI agents working in this repo |
| `SPEC.md` | operational *specification* — field-level schemas, algorithms, APIs |
| `README.md` | user-facing description; getting started |

Read this file first. Descend into `SPEC.md` for execution detail; `AGENTS.md` for the contract on agent behavior.

**Reading time:** ~12 minutes.

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

- The runtime owns `~/.personant/`'s git tree autonomically (init, add/commit on every canonical mutation) via the `internal/autogit` wrapper, which composes in-process go-git operations with policy-driven systemic-validation checks (`CheckDerivedFresh`, `CheckSpineIntegrity`) declared as bitflags on each call. No git pre-commit hook is installed: validation runs as part of personant's own logic at the event points where it's required. Same lifecycle status as writing to `spine.jsonl`.
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

- The architectural thesis cannot be validated by inspection. The §9 measurement regime exists because "use it and find out across extended accumulated usage and then revise if it's wrong" has no graceful recovery — you'd have to either complex-refactor accumulated memory or lose it all.
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

### Port-and-adapter at the substrate boundary

Application code (`internal/turn`, `internal/chat`, `internal/recall`, `internal/workset`, cmd/*, scenarios harness) depends on `memops.MemoryOps` — the conceptual operations port — not on the substrate directly. One adapter ships today (`internal/memops/fileadapter`: JSONL + markdown + YAML frontmatter + TOML + go-git on `~/.personant/`); future adapters (a derived KV index, a SQLite-backed simulation accelerator, anything substrate-shifting that future data demands) implement the same interface without rewriting callers or tests.

What stays direct from `internal/store`: pure helpers (`Normalize`, `DominantSource`), data-type aliases (`SpineRecord`, `ThreadFrontmatter`, ...), and substrate inspection in test invariants (where `h.Paths` access is the *correct* pattern — invariants are validators of the substrate, not application code).

The asymmetric-cost discipline drove the boundary: by the time simulation data reveals an adapter-layer change is needed, the work is local to one new adapter package rather than codebase-wide refactoring.

---

## Substrate non-negotiables

| Decision | What | Why |
|---|---|---|
| Storage | text files in git | inspectability, history, recoverability, free `git log` / `diff` / `grep` |
| Structured records | JSONL, sorted by id | line-grain diffs, not record-grain reformat |
| Thread bodies | markdown + YAML frontmatter | hand-readable; standard tooling (Obsidian, Pandoc) |
| Configuration | TOML (`providers.toml`) + markdown directives | hand-editable; secret-bearing files separated |
| Derived files | regenerable from canonical | drift cannot accumulate; `autogit.CheckDerivedFresh` fails any state-changing git op on stale |
| Git management | autonomic on `~/.personant/`, never on user's workspace | runtime owns its house; user owns theirs |

All of these are load-bearing. A change that violates them is a red flag.

### Substrate evolution: if/when scale forces it

The substrate non-negotiables above are constraints on the *canonical* layer — the source of truth that humans inspect, git tracks, and `cat`/`grep` operate on. They do **not** preclude a fast read-optimized **derived index** alongside the canonical, accessed through the same `MemoryOps` port (an additional adapter or an internal optimization within the file adapter).

If the markdown/JSONL canonical substrate ever hits a critical speed ceiling — most plausibly under long-rung simulation acceleration (especially inference-/embedding-in-loop runs), not interactive use — the option to reach for is **[bbolt](https://github.com/etcd-io/bbolt)** (the etcd-maintained fork of BoltDB) as a derived KV index, with the markdown-graph remaining canonical.

The reasoning, captured so future-us doesn't redo it cold:

- **Workload shape matches.** Personant is read-heavy with bursty writes at well-defined turn boundaries. B+ trees (bbolt's structure) beat LSM-trees (Pebble, BadgerDB) on read latency and avoid background-compaction stalls.
- **Pure-Go.** No CGo means the single-binary substrate non-negotiable holds; LMDB would deliver more raw read performance but breaks clean cross-compilation. The CGo cost isn't worth it for personant's likely speed envelope.
- **Production-hardened.** etcd uses bbolt at non-trivial scale; the failure modes are well-understood.
- **Hybrid preserves the inspectability invariants.** Canonical stays as markdown + JSONL in git; bbolt is rebuildable from canonical via `MemoryOps.RegenerateDerivedState`. Drift between canonical and bbolt is detected the same way as any derived file. `cat`/`grep`/`git log` still work on the canonical layer.

This is **not** a planned upgrade. The asymmetric-cost discipline says: wait for simulation data that shows substrate I/O dominates the sim's wall-clock per simulated month. The `MemoryOps` port makes the eventual decision local to one adapter — exactly the nimbleness it earned its keep on.

Pebble and LMDB were considered and rejected for this hypothetical role: Pebble for being write-optimized (wrong workload shape) and LMDB for CGo (violates substrate non-negotiable). SQLite remains a candidate if SQL-shaped queries become useful, but for KV-shaped access patterns bbolt is the closer fit.

### Port abstraction policy

Personant's `memops` port is **substrate-agnostic by design** with **one file-based implementation today**. The substrate non-negotiables above govern the canonical layer; this section governs the port surface that the application layer talks to. Both disciplines apply concurrently — the substrate stays text + git, *and* the port stays clean of substrate-specific naming.

Design rules for this layer:

1. **Default: substrate-agnostic naming and types.** Port types describe abstract concepts (per-thread metadata, project record, spine entry), not storage-format details. Names that would not make sense for an alternate substrate (SQL, bbolt, in-memory, etc.) are leaks.

2. **No ceremony for hypothetical substrates.** Don't pre-engineer abstractions, defensive interfaces, or migration scaffolds for substrates that don't exist. Keeping the port clean of substrate-leaks is a fixed-cost discipline; *adding* ongoing complexity to defend against futures that may never arrive is not.

3. **API surface is not frozen.** The port evolves when concrete evidence — a real second substrate, a discovered constraint — shows the current shape needs to change. Mutability of the port is a design property, not a defect; the asymmetric-cost discipline applies here too (revisit on evidence, not speculation).

4. **Concessions are explicit, case-by-case, user-approved.** When a substrate-specific concept legitimately needs to live at the port (performance, debuggability, no abstract framing exists yet), it's an explicit exception: name the concession, state the rationale, state what would cause it to be revisited. Implicit concessions — a substrate term creeping in without deliberation — get cleaned up to the default.

This complements (does not replace) the "Port-and-adapter at the substrate boundary" recurring pattern above: that pattern describes *where* the boundary sits; this policy describes *what belongs on its surface*.

---

## The §3.0 hook chain (load-bearing primitive)

Personant treats the working window as a sequence of **context-modification events**, not user-agent turns. The "turn" is a logging convenience; the load-bearing primitive is the per-event hook chain. The principle: **no gaps**.

**Event taxonomy:**

- `user.prompt`, `user.shell-capture`
- `model.response`, `tool.result`
- `thread.fetched`, `digest.refresh`
- `slash.injected`, `directive.reloaded`

Every event also carries a **retention class** (`task` vs `decision`) assigned by source. `tool.result` and `user.shell-capture` are provisionally `task`; everything else is provisionally `decision`. The class routes extracted symbols through different paths (see "Transient-data lifecycle" below).

**Every event runs the chain in this fixed order:**

1. **Symbol extraction** (§3.3) — deterministic regex pass + parse model emissions; routing by retention class.
2. **Engagement signal** (§3.2) — coalesced per-turn (multi-tool-call turns don't multiply `turn_count`).
3. **Dedup decision** (§3.9) — content-addressed identifier replacement (v0.2).
4. **Budget check** (§3.1) — bump-eviction so the budget is honored before the next event lands.
5. **Logging** (§2.8).

**Implementation contract** (§3.0.5): a single internal `onContextDelta(source, content, metadata)` entry point. **No path mutates the working window without going through it.** Code review treats any direct mutation as a bug.

When you write code that adds new content to the working window: route it through `onContextDelta`. Don't invent a side-channel.

### Transient-data lifecycle (load-bearing for recall fidelity)

Symbol pollution by one-moment-of-value content (e.g., `# git status` output, search-result noise) would silently degrade Jaccard recall: noise inflates the union, dilutes meaningful symbols, drives spurious matches. The lifecycle prevents this at the source:

1. **Provisional class at delta time** (mechanical, by source). `task` → symbols extracted into a cross-turn **staging buffer** keyed by normalized form. `decision` → symbols extracted directly into the per-turn coalesce buffer (the existing engagement path).
2. **Confirmation via decision-citation.** When a decision-class delta extracts a symbol whose normalized form matches a staged entry, **promote**: the staged entry migrates from staging into coalesce (where source-dominance merge folds it with the citing delta's contribution), removed from staging. The citing decision delta is what authored the rescue.
3. **Window-close GC.** At the top of each turn (after `TurnNumber++`, before any chain step), staged entries older than `stagingWindowTurns` (default 3) evict. Their symbols never reach the persistent symbol index.

The principle: **raw task-class bytes are always discardable; only the symbols that crossed over into a decision delta survive.** The canonical symbol index never sees transient pollution because pollution is filtered at insert time, not removed retroactively.

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
| **Anchor symbol** | `[derived]` 0..8 normalized symbols per thread; a re-derived **projection** of the thread's *active* `history_symbols` (not a frozen birth certificate), recomputed deterministically each owner turn — no LLM; drive recall and surface in the spine line | §2.2, §2.7.4 |
| **History symbol** | per-thread accumulated symbol (`raw`, `normalized`, `first_seen_turn`, `count`, `source`) plus a `lifecycle` (`active`/`superseded`) and an `ever_central` latch; the thread's evolving identity and the recall match set; capped per `history.cap-per-thread`, evicted with ever-central + high-specificity protection | §2.3, §2.7.4 |
| **Symbol category** | `identifier` (preserve case), `entity` (lowercase + hyphenate), `tag` (lowercase, leading `#` stripped) | §2.7.1 |
| **Symbol source** | `deterministic` / `model` / `user` / `curator`; dominance: `curator > user > model > deterministic` | §2.7.3 |
| **Context-modification delta** | one event to the chain (§3.0); content-modifying event with source + content + metadata + retention class | §3.0 |
| **Retention class** | `task` (default-transient: `tool.result`, `user.shell-capture`) vs `decision` (default-persistent: `user.prompt`, `model.response`, ...). Drives the two-stage transient-data lifecycle. | §3.0.1 |
| **`MemoryOps` port** | the conceptual operations interface (`internal/memops`) the application layer depends on. One adapter today (`fileadapter`, JSONL + markdown + go-git); future adapters swap in without rewriting callers. | port-and-adapter |

---

## Mechanisms

Brief design-rationale notes for the major mechanisms. Schemas and algorithms live in `SPEC.md`; this section captures the *why*.

### Thread lifecycle

A thread's life moves through five stages:

1. **Created** when the model emits `*new-topic*` in a topic tag, or when the user explicitly invokes `/topic <name>`.
2. **Active** in Layer B while engaged (model tags it in per-turn topic list).
3. **Dormant** in Layer C as engagement decays (no longer in the model's recent topic tags).
4. **Retired** to Layer A (spine only) on closure, with full content preserved in `threads/thr_<n>.md`.
5. **Archived** off-spine (deep cold; v0.2) only if spine cardinality pressure builds — recoverable by explicit fetch through git.

The natural retirement boundary is "thread reached a resting point," not "context window pressure." Resting points are recognized at retirement-prompt time; the user's ack is what makes the call. (Spec §3.5.)

### Spine entries are the recognition surface

> The spine line is the load-bearing artifact. It must encode enough decision-state that the model can answer most "what was the outcome of X" questions from the line alone, without needing to fetch the thread file.

Display form, briefly:

```
thr_<n> [<0..8 anchors>] — <100–150 char gist> [<state>]
```

The anchor set is what makes recall possible. The summary is what makes recognition cheap. Together they're the *whole* recognition surface for retired threads — getting them right at retirement is the point of the curator-summary ack flow.

### Symbol extraction (three passes ordered cheapest-first)

1. **Deterministic** (every turn) — regex/parser scan over turn content extracts identifiers (file paths, URLs, code symbols, project-configured patterns, user `#tags`). Free, reliable, narrow.
2. **Model-emitted** (every turn) — the topic tag's anchor list provides named-entity coverage the deterministic pass can't reach.
3. **Curator** (at retirement) — LLM drafts the closure summary (§3.5); the user ack gates it. Anchors are **not** curator-picked: they are the deterministic projection of active `history_symbols` (below), re-derived every owner turn with no LLM involved.

**Anchors vs. history (the evolving-anchor model, §2.7.4):** `history_symbols` is the canonical, accreting, weighted per-thread set (soft-capped ~40) and the recall match surface. `anchors` is **not** a separate storage tier — it is the deterministic top-`AnchorProjectionMax` (8) **projection** of the thread's *active* history symbols, re-derived each owner turn. A symbol that falls out of the projection flips to **superseded** but is **retained, not evicted** — an abandoned premise stays a findable recall handle, protected from capacity eviction by its `ever_central` latch. 0 anchors is legal (a vague-start thread that hasn't accreted a headline yet). The earlier frozen-at-creation / 4-minimum-anchor model is gone. (Spec §2.2, §2.7.4, §3.4.)

### Recall mechanisms (three, layered by cost)

1. **Model-native recognition** (default; cheapest). The spine is in window; the model recognizes prior topics directly when a turn engages them. Emits topic tag at turn start. Handles synonyms and paraphrases for free since the model is the recognition oracle.
2. **Opportunistic surfacing** (deterministic backstop). When current-turn symbols match a retired thread's anchors above threshold, surface a prompt: *"We talked about X 2 days ago — pick up there?"* User ack drives explicit fetch.
3. **Cross-project recall** (Layer A2 → fetch). Per-project anchor digests in A2 are scanned each turn. Same opportunistic mechanism as (2), slightly higher threshold.

**Decline categorization** matters for accrual:

- *Not relevant* → improve matching, do not tighten threshold.
- *Not now* → no parameter change.
- *Stop offering* → tighten threshold.

Without this distinction, declined offers due to bad matches accidentally suppress good offers too. (Spec §3.4.)

### Closure and retirement

The ack is the **integrity gate** at the highest-leverage moment. The user catches misclassification at retirement, the only point where its accuracy matters most.

Triggers:

- Auto-prompt when no engagement for `engagement.decay-turns` OR `engagement.decay-time` wall-clock (either threshold; OR semantics).
- Explicit `/done` from the user.

Flow: curator drafts retirement summary + anchor symbol set → user acks (single keystroke), edits, or defers → on ack, thread file written, spine entry generated, Layer B/C eviction follows. (Spec §3.5.)

Deep cold archival is the next stage past retirement (v0.2): when spine cardinality pressure builds, the runtime archives oldest retired threads off-spine via `git rm` + `git commit` + an `archive/index.jsonl` entry. Recovery via `git show <commit>:<path>` is one command — no bespoke archive format. (Spec §3.8.)

### Fallback dissection (worst-case path)

When budget pressure exceeds threshold AND normal retirement isn't catching up:

- Dissector LLM clusters the oldest poorly-threaded content into semantically related groups.
- Each cluster produces a thread file + spine entry, identical in shape to a normal retirement.
- User-acked under default conditions; absolute-emergency mode dissects without ack and notifies after.

Structurally the same as normal retirement, *reactively* triggered on poorly-threaded content. Same artifacts produced; same recovery path; same invariant preserved.

Handles three failure modes with one mechanism: wandering user, single-thread overrun, never-cleanly-threaded content. **Quality is best-effort by design** — graceful degradation under stress, not catastrophic loss. (Spec §3.6.)

### Multi-project as lifelong accrual

One unified spine across all projects. Every thread carries a `project` tag. Layer A1 = current project's full spine; Layer A2 = compressed cross-project digests. **Cross-project recall is a *designed feature*, not an emergent property** — it fires through the same opportunistic mechanism as same-project recall, with a higher threshold.

This is what makes the agent's *career* possible — accumulated experience across all projects, transfer across domains. The architectural commitment: **one entity, many projects, lifelong accrual**. (Spec §2.5, §4.5.)

### Personalization through directive accrual

Behavior tuning lives in inspectable directive files that accrue from feedback signals:

- `directives/defaults.md` — system defaults, baked in.
- `directives/user.md` — user-wide overrides; accrues from explicit instructions, decline categorizations, accept/decline running statistics.
- `directives/prj_<n>/*.md` — project-specific tuning.

The directives are *text files*. Inspectable, editable by hand, portable, version-controlled. **This is the property that elevates the system from "smart assistant" to "real research partner": the agent learns how the user works, *and the user can read what it learned and correct it*.** (Spec §2.6.)

### Memory consolidation — the "sleep" cycle (future consideration)

Personant's memory layering is deliberately analogous to organic memory: **working short-term** (the live working set), **consolidated long-term** (the spine + thread bodies, mostly read), **archival deep memory** (off-spine, rarely touched), and a **metadata layer** for operating on active context fast — tapping long-term memory read-only, *activating* it read/write, or unpacking archival entries.

Under sustained working use this organization fragments unavoidably: threads close out of order, the spine accretes, derived structures and archival boundaries drift from their ideal packing. In-turn maintenance keeps the substrate *correct* but not *orderly*.

The intended remedy is an offline **consolidation cycle** — the system's equivalent of organic sleep. During idle time (the day off, or any unused window) the runtime would run larger-scale reorganization it cannot afford mid-turn: re-packing fragmented structures into orderly arrangements, compacting the spine, advancing archival, **running `git gc`/repack on the substrate** (the commit-on-structural-change cadence of SPEC §3.11 accumulates loose objects between sessions — the sim measured ~130 commits/sim-day; packing them is a sleep-cycle job, not a working-hours one), and making the final keep/toss calls on data the faster in-turn transient-data lifecycle (§3.0) left questionable. Working hours stay responsive; the heavy reorganization happens when nothing is waiting on it.

This is a future consideration, not v0.1 — but the v0.1 acceptance simulation already supplies the hook: the day-off is a real idle window in the workload model, and closure (§3.5) / archival (§3.8) are exactly the mechanisms a consolidation pass would tidy.

### Weight-baked instinct from outcome history (far-future consideration)

A second, longer-horizon kind of consolidation: once local fine-tuning is mature and stable, the system's *outcome record* — not its content — becomes a training signal for a personal alignment adapter on the underlying model. **Substrate stays recall; weights become instinct.**

The premise is that a transformer has finite parameter capacity, and "baking memories" by tuning a model on raw substrate content is a bad trade: it displaces pretrained factual capacity for narrow recall the substrate already holds verifiably. The smarter move is to *not* train on facts at all. Train on the *shape of what worked and what failed* — the latent outcome distribution across long substrate history.

**Signal sources, all already latent in a mature substrate:**

- **Decision durability.** Threads that closed cleanly accomplishing what they set out to do vs. threads abandoned. Architectural calls that survived in later commits vs. ones reversed. Cited decisions (load-bearing) vs. uncited (noise) — adjacent to the §3.0 transient-data lifecycle's decision-vs-task classification.
- **Estimate calibration.** Where estimates exist alongside actuals, the delta is signal.
- **External durability.** For project repos the personant assisted on: code that survived refactors vs. code that got deleted. Git history of the workspace, not just the substrate.
- **User correction patterns.** Repeat pushback on the same shape of suggestion — the model's prior is misaligned for this user in a recoverable, low-rank way.

**The training shape is not SFT on substrate content.** It's preference-pair learning (DPO/IPO-style) over `(situation, approach_taken, observed_outcome)` triples, with outcomes drawn from the signal sources above. The product is a small, low-rank LoRA targeting later layers and alignment-relevant attention heads — empirically the locus where "style and instruction-following" already live in current models.

**What the consolidated model contributes is taste, not knowledge.** The base model already knows the best-practice patterns; what it doesn't have is the *shape of when patterns apply and when they fail in this user's domain* — the prior that distinguishes a senior partner from a junior reciter. That kind of contribution ("this architectural move tends to ossify under load; I've seen that fail three ways") is the kind of partnership the founding tenet — AI with humans, not by humans alone — actually cashes out to at the cognition-shape level rather than the conversation-shape level.

**Hard constraints carried over from the substrate-as-canonical thesis:**

- Substrate remains source of truth. Weights are an optimization on instinct, never a replacement for recall. Lose the substrate and you must still have an auditable, correctable record of what happened.
- Each training pass is gated, reviewed, and reversible. Pre-tuned model preserved; new LoRA tagged with the substrate snapshot hash it was trained from; eval pass (recall-fidelity + general-capability probes) gates promotion; failed evals roll back.
- Substrate items used in a training pass get annotated (`consolidated_at:`) — don't bake the same thing twice; supports later forensic queries on what shaped the model.

**Not v0.1, not v1.0, not v2.0** — depends on mature local fine-tuning infrastructure, accumulated outcome-labeled substrate at scale, and a tested eval methodology. Captured here because the architecture has a clean place to land it and because the framing — *substrate-as-canonical, weights-as-instinct, outcome-history-as-signal* — is the kind of design call that's much easier to commit to early than to retrofit later. Adjacent to the "sleep cycle" above: same offline-consolidation framing, different consolidation target (model weights instead of substrate organization).

### Submind via clone — isolated exploration and frontier-model collaboration (future consideration)

Personant's substrate is a git tree already; a **submind** is a subdirectory clone of that tree, with the primary's home as the submind's `origin`. The submind operates as a full personant on a named branch in its local clone — its own spine, its own threads, its own working-set discipline — and integrates back via standard git push + merge. Branch isolation means the submind never collides with the primary until merge; the existing substrate machinery handles the rest.

**Why subminds earn their architectural slot:**

- **Speculative exploration with a commit boundary.** Try a thought experiment in a submind; decide whether the result is worth integrating. The primary's belief state isn't disturbed by the exploration.
- **Specialization without context-switch cost.** A submind focused on one task continues while the primary continues with the broader context.
- **Parallel triangulation.** Two submind subdirectories pursuing variant hypotheses of the same problem are just two subdirectories; the user (or the primary) decides at merge time which to integrate.
- **Naming discipline via universal mind_id suffix.** Every personant has a `mind_id`; every autonomically-generated identifier in that personant is suffixed. Suffixes are flat (`<generation>-<timestamp>`), not chained; immediate-parent provenance is held in metadata. Cross-submind name collisions are impossible by construction; the merge is git-trivial on the namespaced substrate.

**Submind as the natural home for frontier-model collaboration.** A submind can run a *two-model* configuration: a local **liaison model** (gemma family — E4B for swarm-cheap, 26B-A4B for default judgment quality) handling all family-stable infrastructural prompts (topic-tagging, symbol extraction, recall scoring, closure summaries), and a **guest model** (a frontier model — Claude / GPT / Gemini / etc.) providing the actual reasoning content. Inside the submind:

- The liaison model handles every surface where family-stable consistency matters (substrate metadata, recall behavior, closure decisions).
- The guest model produces the conversational content — what goes into threads, turn excerpts, hot state.
- Substrate stays gemma-shaped (infrastructural); content is guest-shaped.
- The submind's branch identity carries the provenance — no per-symbol provenance tags, no per-turn guest-engagement aggregator needed. Which submind contained the work *is* the provenance.

This isolation is what makes frontier-model collaboration architecturally safe rather than a cross-family contamination risk. The main personant's substrate stays family-stable (per the [model-family-as-platform](#model-family-as-platform-v10-platform-coupling) principle below — every infrastructural prompt belongs to one family); the submind container bounds the foreign model's behavioral influence to where its capability is wanted (the content surface) and away from where family-stability is load-bearing (the infrastructural surface). Eval-by-comparison falls out for free: spawn a submind with `guest=Claude`, another with `guest=gemma-31B-local`, compare at merge time — same task, same substrate format, two perspectives.

**Distinguished from concurrent sessions (below).** Subminds are *isolated clones with a merge integration point*. Concurrent sessions are *interleaved multitasking on shared canonical state*. They answer different questions: subminds give you a commit boundary ("explore divergent hypotheses without bleeding belief state"); concurrent sessions give you simultaneous live attention on the same memory ("two coordinated tasks at once"). They can coexist; they are not substitutes.

**Merge semantics.** The viable spectrum, captured for design completeness:

- **Archive-only minimal path:** submind archives its own active work before merge; primary git-merges only the append-only archive. Eliminates running-state conflicts by construction; primary never reconciles a hot thread, only absorbs frozen archive entries. Tradeoff: belief-state from divergent thought doesn't silently flow back; combining a submind's version of a topic with the primary's is a manual review-and-incorporate operation. Most conservative.
- **Structural merge:** once names are globally unique (suffix scheme above), the submind's live spine entries, thread directories, and working-set membership can travel back as-is. `git merge` handles the append-only files via union driver; suffix-disjoint namespaces avoid conflict. Pre-clone threads are read-only inside the submind; continuations create a new namespaced thread (`thr_42-S1` with `derived_from: thr_42` frontmatter) — the same topic now has two threads in the merged primary, and both surface naturally on the same recall query.
- **Communication channel during life.** Pub/sub IPC between subminds and primary while the submind is active — status events, queries, results — *not* substrate read/write across the boundary. Merge is by definition a *termination*; there is no continuing submind activity after merge.

**Nested subminds.** The architecture is naturally recursive — a submind IS a personant; by symmetry it can spawn its own submind. Suffix composition (`thr_42-S1-S2`) gives unambiguous provenance at any depth. Merge propagates one level at a time. No structural depth limit; the real bound is the user's review-budget at each merge gate.

Future consideration, post-v0.1; plausibly v2.0. The frontier-collaboration use case may end up being the primary motivator for prioritizing submind work — it turns submind from "speculative isolation mechanism" into "the natural home for inviting frontier reasoning without sacrificing family-stable substrate."

### Concurrent sessions — multitasking one career (future consideration)

A user routinely interleaves work — two tasks open at once, attention alternating. CWD-scoped agents (Claude Code, opencode, …) get this for free: each working directory is its own isolated context. Personant cannot take that shortcut — its premise is a **single unified awareness and career**, so a separate context per directory would fragment the very thing the system exists to keep whole. Personant must instead genuinely **multitask**: multiple live conversations open against one shared memory.

Consequences, in rough order of when they bite:

- **Thread safety.** Concurrent sessions read and mutate shared canonical state (spine, thread files, the §3.0 chain). The runtime is currently single-session; concurrent sessions need real synchronization at the substrate boundary.
- **Per-session working set.** Each session carries its own active threads (its own Layer B) and must track which is which — "the active thread" becomes session-scoped, not global.
- **Cross-reference vs. isolation.** Two concurrently-active threads may legitimately want to cross-reference — often desirable. But the user must be able to declare two lines of work **unrelated**: an explicit "do not conflate these; neither thread's context bleeds into the other." Recall and working-set composition would have to honor that boundary.

Concurrency is also a fragmentation *source*: interleaved multitasking touches threads out of order and lets per-session working sets drift apart. That makes the results of concurrent sessions prime material for the **"sleep" cycle** above — the two future mechanisms are coupled, concurrency creates the tangle and offline consolidation clears it.

Future consideration, not v0.1 — and distinct from multi-*user* (v2.0): this is one user, one career, many concurrent conversations.

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

Spec §9 is **not a quality gate bolted on after features land**. It is the measurement instrument by which the architectural thesis gets proven empirically — and the substrate by which the techniques evolve iteratively.

The cost of getting this wrong without proof ahead of time is **asymmetric and severe**: extended accumulated memory state under a structurally-wrong storage strategy has no graceful recovery. The simulation regime exists *because* proof must come ahead of time.

**Layers** (in build order):

1. **Unit tests** — deterministic state operations, JSONL round-trips, schema validation.
2. **Scenario tests** — handwritten lifecycle flows; mock LLM via canned responses; metrics emission per scenario.
3. **Invariant validators** — callable from any test (spine integrity, index drift, project refs, last-active validity, frontmatter-spine sync, engagement consistency).
4. **Churn tests** — randomized seeded sequences; invariants after every operation.
5. **Calibration tests** — same scenario, parameter sweeps; metrics matrices identify operating points.
6. **Cross-run baseline comparison** — stored baselines (git-tracked); regression detection.
7. **Acceptance simulation harness** — synthetic workload, logical-clock acceleration, steady-state assertions; the top rung is 120 days / four months (§9.1).

**v0.1 acceptance is the realism-convergence simulation gate passing** (top rung 120 days / four months; SPEC §9.1), not Phase 5 feature-completeness — and it is a *converging loop*, not a one-shot pass. Memory quality maintained throughout, zero out-of-context-space events, runtime costs bounded and stable, steady state demonstrated such that another full span would run without degradation.

When making technique changes (parameter tweaks, algorithm adjustments), the question is always "what does the simulation say?" — not "does it compile and pass invariants?" Bake metrics emission into new code from day one; bolting it on later is much more expensive.

**Next testing change on deck — user-contributed file content.** The §3.9 file-editing workload currently models the *agentic* edit shape: file content arrives via `fs.read`/`fs.write` tool deltas while the user prompt stays generic. The planned variant models the *user-dictated* shape — the user prompt itself carries the literal values and lines being added. The §3.9 reverse-delta store is content-agnostic to source, so storage / dedup / clock-aging is already covered; the variant exists to exercise the upstream paths that differ — user-prompt symbol extraction, the §3.0 transient-data classification (`RetentionDecision` vs `RetentionTask`), and the §3.9.2/§3.9.4 live-window handling of the same literal appearing in both the prompt and the file.

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
- Offline memory-consolidation cycle — the "sleep" cycle (future; see Mechanisms)
- Weight-baked instinct from outcome history — personal alignment LoRA from substrate's outcome record (far-future; see Mechanisms)
- Submind via clone — isolated exploration and frontier-model collaboration (future; see Mechanisms)
- Concurrent sessions — one user multitasking across multiple live conversations (future; see Mechanisms)
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
| Field-level schemas + algorithms + APIs | `SPEC.md` |
| User-facing description, getting started | `README.md` |
| Substrate-level decision history | persistent memory: `project_personant_substrate.md` |
| v0.1 acceptance criteria | `SPEC.md` §9.1 |

---

## How decisions evolve

The first-pass design is guaranteed to have holes. Personant evolves by:

1. **Calibration scenarios** — parameter sweeps over canonical workloads; metrics matrices identify operating points.
2. **Directive accrual** — per-user / per-project tuning happens automatically through ack patterns.
3. **Empirical pressure on watch items** — known weak spots (categorized below) surface via instrumentation; targeted work follows the data.

When you propose a change, ask:

- Does it have a measurement story? (How will we know it worked?)
- Does it commit to a specific value, or open a calibration window?
- Does it preserve or invert a recurring pattern (above)?
- Does it create migration cost we can't recover from?

**Categories of known weakness** the simulation regime is designed to
surface and quantify:

- **Symbol-extraction noise** — synonym fragmentation, low-information leakage, model emission drift.
- **Recall precision** — cross-thread symbol collision, anchor selection quality at retirement.
- **Deterministic-pass coverage** — per-project regex curation needs.

These aren't blockers; they're *expected* findings.

---

## Mental model summary

If you internalize one thing from this document, make it this:

> The runtime is the canonical-state holder. The LLM is a narrow judgment instrument. The human appears at three high-leverage moments. Every working-window mutation goes through the §3.0 chain. Every dependency earns its keep with a real consumer. Testing is the lab bench, not a quality gate. Constraints are load-bearing.

Recognize these patterns, push back on violations, and you'll be on safe ground. Read `SPEC.md` for execution detail.

---

## Provenance

The architecture is shaped more like an **agent runtime** than a chat app. Memory hierarchy, paging mechanism, lookup index, personalization layer, persistence substrate, instrumentation, multi-agent capability. The naming follows: Personant is a runtime, not a feature set.

The design originated in conversation 2026-05-07/08, building on observations about Claude Code's compaction-driven information loss, the AVE-KB's text-in-git knowledge structure, and the asymmetry between current AI systems' session-bounded context and what a long-term research partnership actually requires. AVE-KB conventions (text + git, frontmatter, claim-quality propagation, derived indexes) were the inspiration; the system is independent.

The name "Personant" is a portmanteau of *personal assistant*. Latin *personare* (to sound through) and *persona* (the mask one speaks through) are bonus resonances rather than load-bearing meaning.
