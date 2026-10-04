# ARCHITECTURE – Personant

**Audience:** AI agents (and new contributors) who need to orient quickly. Companions:

| Document | Purpose |
|---|---|
| `THESIS.md` | why personant has the shape it has — read first |
| `ARCHITECTURE.md` (you are here) | principles, patterns, mechanisms, mental models |
| `CONVENTIONS.md` | house rules for AI agents working in this repo |
| `SPEC.md` | operational *specification* — field-level schemas, algorithms, APIs |
| `README.md` | user-facing description; getting started |

Read `THESIS.md` first, then this file. Descend into `SPEC.md` for execution detail;
`CONVENTIONS.md` for the contract on agent behavior.

**Reading time:** ~35–40 minutes.

---

## What this is

Personant is a single-user, single-agent runtime that gives an AI assistant **persistent working
memory across arbitrary projects with continuity**. The agent has one continuous career — no session
boundaries, no compaction-driven information loss, cross-project recognition.

The role of the agent is **research assistant**, not general-capability. This constraint is
load-bearing — Personant is not, and will not become, a polyglot agent-coding system.

---

## Applying the thesis (load-bearing)

THESIS.md states why personant has this shape. Architecturally it reduces to:

> **Deterministic state as canonical. LLM in narrow judgment roles. Human acknowledgement at
> high-leverage moments only.**

This recurs at every layer. When making a design decision, ask:

1. Could deterministic code do this without involving the LLM?
2. If the LLM is involved, is its role narrow and well-bounded?
3. Is there a human ack at this point because it's high-leverage, or out of caution?

| Tier | Role |
|---|---|
| Deterministic (Go runtime) | canonical state holder; integrity enforcer; build/query/index operations; autonomic git management of `~/.personant/` |
| LLM | narrow generative/judgment roles: topic tagging, summary drafting, anchor selection, recognition, dissection clustering |
| Human | ack at three load-bearing moments: closure (retirement), opportunistic recall surface, fallback dissection trigger. For closure and recall the ack stays where the leverage is (the exception queue; the middle-confidence recall band; every explicit act like `/done`) and drops for the routine case, which is applied automatically with one committed line and an `ack=auto` event |

**Two warning signs that a proposed change is wrong:**

- It pushes canonical state into the LLM. (E.g., "let the model summarize threads on the fly." No:
  the curator-summary at retirement is the canonical text; the model wrote it once, deterministic
  code stores it.)
- It removes a human ack at one of the three load-bearing moments. Or it adds an ack at a
  non-load-bearing moment, increasing friction without value.

---

## Recurring patterns

These patterns appear at multiple layers. Recognize and use them.

### Propose-and-ack

The LLM never directly mutates state that matters. It *proposes*; deterministic code *executes* on
user ack.

- **Closure:** curator drafts retirement summary → user acks → deterministic code writes thread
  file + spine entry.
- **Workspace writes:** LLM writes to `.personant/tmp/foo.md` via `fs.tmp_write` → emits
  `fs.propose_promote(tmp_id, target_path)` → user acks → deterministic code moves the file.
- **Recall surfacing:** deterministic match fires → the middle-confidence band is acked → explicit
  fetch into Layer B. A match above its tier's auto threshold is fetched without asking, with one
  committed line (user ruling 2026-08-05, §3.4 AMENDED): a candidate the matcher is confident about
  is not a judgment call, and a prompt the user clears reflexively is worse than no prompt.

The ack is the **integrity gate** at the moment its accuracy matters most.

### Autonomic vs LLM-controlled

"Out of scope at the LLM tool layer" ≠ "out of scope at the runtime layer."

The runtime performs many operations the LLM cannot. Most notably **git**:

- The runtime owns `~/.personant/`'s git tree autonomically (init, then the **dual-repo cadence** of
  SPEC §3.11/§4.5.8 — a scoped per-turn commit to the disposable **daily** DB, one day-grain commit
  per day to the permanent **primary** DB, *not* on every canonical write) via the
  `internal/autogit` wrapper, which composes in-process go-git operations with policy-driven
  systemic-validation checks (`CheckDerivedFresh`, `CheckSpineIntegrity`) declared as bitflags on
  each call (gating the once-a-day primary writes; daily commits are ungated). No git pre-commit
  hook is installed: validation runs as part of personant's own logic at the event points where it's
  required. Checks are *systemic*, parameterized only by paths: `CheckDerivedFresh` wraps `personant
  index check` and `CheckSpineIntegrity` wraps `personant verify`, declared as `GitCheckFlags`
  bitflags (the `preFlags` and `postFlags` arguments; 64 slots) on each call. Operation-tied
  verifications (for example, an archive entry resolving after a recovery checkout) live in
  purpose-specific wrappers that compose `autogit.Checkout`, not in flags. Same lifecycle status as
  writing to `spine.jsonl`.
- The runtime issues read-only git queries against the user's *workspace* (`git ls-files`, `git
  status`, `git diff`) for permission-tier classification and ack-prompt diff rendering.
- `personant init` seeds `[user]` from the installed git binary's global identity, not go-git, whose
  config parser does not resolve `include`/`includeIf`.
- The LLM has **no git tool**.

When something feels like it needs to "be done," ask: can the runtime do this autonomically? If yes,
that's the answer.

### Single spelunking location

Everything Personant does lives under `~/.personant/`. No `/tmp/`. No state scattered across the
host.

Forensic inspection is `cd ~/.personant && grep -r ...`. If something can't be found there, it
shouldn't exist.

### No speculative prefetch

Every loaded thread is loaded because the model said it was needed.

Spine is always in window → model recognizes prior topic from spine entries → emits topic tag →
corresponding threads fetched into Layer B → response generated. The model is the recognition
oracle, not a heuristic guess.

### Asymmetric cost: prove ahead of time

When the cost of being wrong is far higher than the cost of being right, the bar shifts toward
"prove it."

- The architectural thesis cannot be validated by inspection. The §9 measurement regime exists
  because "use it and find out across extended accumulated usage and then revise if it's wrong" has
  no graceful recovery — you'd have to either complex-refactor accumulated memory or lose it all.
- The migration-cost asymmetry favored YAML over TOML for thread frontmatter: switching later costs
  a script + validation pass over every thread file; the immediate gain from going off-standard was
  modest. Pick the standard path.

When evaluating a "modest gain now" decision, explicitly compute the migration cost if we change our
mind later. If it's high, default to the standard path.

### Approved-when-earned dependencies

Stdlib-first, but a dep that earns its keep is fine. Two failure modes to avoid:

1. Reaching for a dep before there's a real consumer (speculative scaffolding).
2. Implementing complex stdlib substitutes for a dep that would just work (NIH).

Approved deps land *with their consumer*, not before. See the substrate-decisions memory for the
catalog.

**Permanently out** (architectural conflict): `langchaingo/memory`, `langchaingo/chains`,
`mattn/go-sqlite3`. SQLite as canonical state is rejected — the canonical form is text + git.

### Constraint as feature

"We don't do X" is often a load-bearing claim, not a TODO.

Personant's "research assistant, not polyglot agent-coding tool" framing is what makes the project
tractable. v1.0 will add Python execution for math/physics simulation; that's a *targeted* lift of
the shell-execution exclusion, not a general "agent can run anything."

When pressure builds to "just add X," ask: is this constraint load-bearing, or accidental?

### Port-and-adapter at the substrate boundary

Application code (`internal/turn`, `internal/chat`, `internal/recall/measure`, cmd/*, scenarios
harness) depends on `memops.MemoryOps` — the conceptual operations port — not on the substrate
directly. One adapter ships today (`internal/memops/fileadapter`: JSONL + markdown + YAML
frontmatter + TOML + go-git on `~/.personant/`); future adapters (a derived KV index, a
SQLite-backed simulation accelerator, anything substrate-shifting that future data demands)
implement the same interface without rewriting callers or tests.

Pure renderers (`internal/workset.Compose`, `internal/recall/scoring`) are **substrate-free**: they
import `memops`'s plain domain types (`SpineRecord`, `Budget`, `ThreadMeta`, ...) but never the
`MemoryOps` port itself, operating only on data the caller already fetched. What stays direct from
`internal/store` for the same reason: pure helpers (`Normalize`, `DominantSource`) and data-type
aliases (`ThreadFrontmatter`, ...). Tests inspect the substrate directly via `h.Paths` in invariants
— that's the test-side substrate validator pattern, not an application-code exception.

The asymmetric-cost discipline drove the boundary: by the time simulation data reveals an
adapter-layer change is needed, the work is local to one new adapter package rather than
codebase-wide refactoring.

---

## Substrate non-negotiables

| Decision | What | Why |
|---|---|---|
| Storage | text files in git | inspectability, history, recoverability, free `git log` / `diff` / `grep` |
| Structured records | JSONL, sorted by id | line-grain diffs, not record-grain reformat |
| Thread bodies | markdown + YAML frontmatter | hand-readable; standard tooling (Obsidian, Pandoc) |
| Configuration | TOML (`providers.toml` endpoint pool + `config.toml` choices) + markdown directives | hand-editable; secret-bearing files separated |
| Derived files | regenerable from canonical | drift cannot accumulate; `autogit.CheckDerivedFresh` fails any state-changing git op on stale |
| Git management | autonomic on `~/.personant/`, never on user's workspace | runtime owns its house; user owns theirs |

All of these are load-bearing. A change that violates them is a red flag.

### Substrate evolution: if/when scale forces it

The substrate non-negotiables above are constraints on the *canonical* layer — the source of truth
that humans inspect, git tracks, and `cat`/`grep` operate on. They do **not** preclude a fast
read-optimized **derived index** alongside the canonical, accessed through the same `MemoryOps` port
(an additional adapter or an internal optimization within the file adapter).

If the markdown/JSONL canonical substrate ever hits a critical speed ceiling — most plausibly under
long-rung simulation acceleration (especially inference-/embedding-in-loop runs), not interactive
use — the option to reach for is **[bbolt](https://github.com/etcd-io/bbolt)** (the etcd-maintained
fork of BoltDB) as a derived KV index, with the markdown-graph remaining canonical.

The reasoning, captured so future-us doesn't redo it cold:

- **Workload shape matches.** Personant is read-heavy with bursty writes at well-defined turn
  boundaries. B+ trees (bbolt's structure) beat LSM-trees (Pebble, BadgerDB) on read latency and
  avoid background-compaction stalls.
- **Pure-Go.** No CGo means the single-binary substrate non-negotiable holds; LMDB would deliver
  more raw read performance but breaks clean cross-compilation. The CGo cost isn't worth it for
  personant's likely speed envelope.
- **Production-hardened.** etcd uses bbolt at non-trivial scale; the failure modes are
  well-understood.
- **Hybrid preserves the inspectability invariants.** Canonical stays as markdown + JSONL in git;
  bbolt is rebuildable from canonical via `MemoryOps.RegenerateDerivedState`. Drift between
  canonical and bbolt is detected the same way as any derived file. `cat`/`grep`/`git log` still
  work on the canonical layer.

This is **not** a planned upgrade. The asymmetric-cost discipline says: wait for simulation data
that shows substrate I/O dominates the sim's wall-clock per simulated month. The `MemoryOps` port
makes the eventual decision local to one adapter — exactly the nimbleness it earned its keep on.

Pebble and LMDB were considered and rejected for this hypothetical role: Pebble for being
write-optimized (wrong workload shape) and LMDB for CGo (violates substrate non-negotiable). SQLite
remains a candidate if SQL-shaped queries become useful, but for KV-shaped access patterns bbolt is
the closer fit.

### Port abstraction policy

Personant's `memops` port is **substrate-agnostic by design** with **one file-based implementation
today**. The substrate non-negotiables above govern the canonical layer; this section governs the
port surface that the application layer talks to. Both disciplines apply concurrently — the
substrate stays text + git, *and* the port stays clean of substrate-specific naming.

Design rules for this layer:

1. **Default: substrate-agnostic naming and types.** Port types describe abstract concepts
   (per-thread metadata, project record, spine entry), not storage-format details. Names that would
   not make sense for an alternate substrate (SQL, bbolt, in-memory, etc.) are leaks.

2. **No ceremony for hypothetical substrates.** Don't pre-engineer abstractions, defensive
   interfaces, or migration scaffolds for substrates that don't exist. Keeping the port clean of
   substrate-leaks is a fixed-cost discipline; *adding* ongoing complexity to defend against futures
   that may never arrive is not.

3. **API surface is not frozen.** The port evolves when concrete evidence — a real second substrate,
   a discovered constraint — shows the current shape needs to change. Mutability of the port is a
   design property, not a defect; the asymmetric-cost discipline applies here too (revisit on
   evidence, not speculation).

4. **Concessions are explicit, case-by-case, user-approved.** When a substrate-specific concept
   legitimately needs to live at the port (performance, debuggability, no abstract framing exists
   yet), it's an explicit exception: name the concession, state the rationale, state what would
   cause it to be revisited. Implicit concessions — a substrate term creeping in without
   deliberation — get cleaned up to the default.

This complements (does not replace) the "Port-and-adapter at the substrate boundary" recurring
pattern above: that pattern describes *where* the boundary sits; this policy describes *what belongs
on its surface*.

---

## The §3.0 hook chain (load-bearing primitive)

Personant treats the working window as a sequence of **context-modification events**, not user-agent
turns. The "turn" is a logging convenience; the load-bearing primitive is the per-event hook chain.
The principle: **no gaps**.

**Event taxonomy:**

- `user.prompt`, `user.shell-capture`
- `model.response`, `tool.result`
- `thread.fetched`

Layer A2 digest content, slash-command effects and directive reloads are not chain deltas: they
change what the composer (`workset.Compose`) assembles.

Every event also carries a **retention class** (`task` vs `decision`) assigned by source.
`tool.result` and `user.shell-capture` are provisionally `task`; everything else is provisionally
`decision`. The class routes extracted symbols through different paths (see "Transient-data
lifecycle" below).

**Every event runs the chain in this fixed order:**

1. **Symbol extraction** (§3.3) — deterministic regex pass + parse model emissions; routing by
   retention class.
2. **Engagement signal** (§3.2) — coalesced per-turn (multi-tool-call turns don't multiply
   `turn_count`).
3. **Dedup decision** (§3.9) — content-addressed identifier replacement.
4. **Budget check** (§3.1) — bump-eviction so the budget is honored before the next event lands.
5. **Logging** (§2.8).

**Implementation contract** (§3.0.5): a single internal `onContextDelta(source, content, metadata)`
entry point. **No path mutates the working window without going through it.** Code review treats any
direct mutation as a bug.

When you write code that adds new content to the working window: route it through `onContextDelta`.
Don't invent a side-channel.

Step 4 (budget check) is a documented no-op in v0.1 (`internal/turn/chain.go`,
`TODO(phase-2-budget)`); `TestBudgetCheckIsExplicitNoOpV01` trips if it is half-enabled without a
deliberate decision. Step 5 summarizes every task-class source to a metadata line (source, path,
byte count) instead of logging the body, so a newly added task-class source inherits the
minimization.

### Transient-data lifecycle (load-bearing for recall fidelity)

Symbol pollution by one-moment-of-value content (e.g., `# git status` output, search-result noise)
would silently degrade Jaccard recall: noise inflates the union, dilutes meaningful symbols, drives
spurious matches. The lifecycle prevents this at the source:

1. **Provisional class at delta time** (mechanical, by source). `task` → symbols extracted into a
   cross-turn **staging buffer** keyed by normalized form. `decision` → symbols extracted directly
   into the per-turn coalesce buffer (the existing engagement path).
2. **Confirmation via decision-citation.** When a decision-class delta extracts a symbol whose
   normalized form matches a staged entry, **promote**: the staged entry migrates from staging into
   coalesce (where source-dominance merge folds it with the citing delta's contribution), removed
   from staging. The citing decision delta is what authored the rescue.
3. **Window-close GC.** At the top of each turn (after `TurnNumber++`, before any chain step),
   staged entries older than `stagingWindowTurns` (default 3) evict (`pruneStaging`). Their symbols
   never reach the persistent symbol index. The window is a compiled-in constant; a staging entry
   holds the normalized form, raw form, source, and the turn it was staged.

The principle: **raw task-class bytes are always discardable; only the symbols that crossed over
into a decision delta survive.** The canonical symbol index never sees transient pollution because
pollution is filtered at insert time, not removed retroactively.

---

## The layered working set

Per-turn context is composed under an authoritative whole-request **token** ceiling
(`context.token-budget`, default 200000 — #127). The token ceiling is the gate; the per-layer
**byte** budgets are *derived* from it (ceiling × a conservative ~2.5 bytes/token, text-only) and
drive pre-flight truncation, while the post-flight `usage.prompt_tokens` count is the real check.
From that derived byte total, the **live_turn** reserve (15% of the total) is carved off first and
owned by the turn package (current user input — rejected if oversize — plus current-turn tool-result
deltas and a bounded, truncation-limited recent-history tail). The remaining **memory budget** (85%
of the total) is partitioned across the memory layers:

| Layer | Content | Default | Loading |
|---|---|---|---|
| live_turn | user input + current-turn tool-result deltas + bounded history tail | 15% of total | reserved (owned by `internal/turn`) |
| E | directives + project conventions | 12% | always |
| A1 | active project's spine entries (full) | 10% | always |
| A2 | other projects' digests (compressed) | residue (~8%) | always |
| B | actively engaged threads (top-K full bodies) | 55% | populated by topic tag |
| C | recently dormant threads (summaries) | 15% | decay-driven from B |

The E/A1/A2/B/C percentages are shares of the **memory budget** (total − live_turn), not of the
total; A1+A2 together form an 18% high tier with A1 fixed and A2 the residue. `context.token-budget`
is a const (`memops.DefaultTokenCeiling`) plus a constructor override. Of the §2.6.1 parameters only
`closure.ack-mode` and `recall.ack-mode` are served by the directive layer (resolved at session
start); the rest are compiled-in defaults. Static prompt scaffolding (~1.7 KB: preamble, topic-tag
directive, recovery reminders, headers) is charged to no layer share and rides on top of the byte
proxy; the A4 acceptance test bounds it (`Total + measured scaffolding`), and a fixed deduction
would zero out the tiny ceilings the acceptance tests exercise.

**Eviction order under budget pressure:** C-oldest → B-oldest (compressed to summary). A and E are
sacrosanct. Layer C's dormant set is **not count-capped** (#127): its byte budget truncates the
rendered dormant threads at composition time (the honest overflow marker), replacing the dropped
v0.1 count cap.

Layers E and A are the *recognition* surface; Layer B is *active engagement*. The model recognizes
prior topics from A1 + A2; emits a topic tag → corresponding threads fetched into B → response
generated against augmented context.

**Names and recency state.** Layer B order and the dormant set are the runtime's `ActiveThreads` and
`DormantThreads` lists (most-recently-engaged first), persisted each turn to
`<home>/working-set.json` (gitignored) so recency survives a relaunch. A second excerpt write for
one turn is refused with `ErrTurnAlreadyOwned`. The phantom-resolution owner fallback ranks tagged
ids by `last_engaged_turn`, a session-scoped counter that is not in the spine record; it is
deliberately not used for the no-tag owner-default, which ranks by Layer B order because the counter
inverts recency across a relaunch. Layer B order cannot rank the fallback's candidates: a tagged id
may sit outside Layer B, and a same-turn §5.5 fetch reorders the list by fetch order.

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

Brief design-rationale notes for the major mechanisms. Schemas and algorithms live in `SPEC.md`;
this section captures the *why*.

### Thread lifecycle

A thread's life moves through five stages:

1. **Created** when the model emits `*new-topic*` in a topic tag, or when the user explicitly
   invokes `/topic <name>`.
2. **Active** in Layer B while engaged (model tags it in per-turn topic list).
3. **Dormant** in Layer C as engagement decays (no longer in the model's recent topic tags).
4. **Retired** to Layer A (spine only) on closure, with full content preserved in
   `threads/thr_<n>.md`.
5. **Archived** off-spine (deep cold) only if spine cardinality pressure builds — recoverable by
   explicit fetch through git.

The natural retirement boundary is "thread reached a resting point," not "context window pressure."
Resting points are recognized at retirement-prompt time; the user's ack is what makes the call.
(Spec §3.5.)

### Spine entries are the recognition surface

> The spine line is the load-bearing artifact. It must encode enough decision-state that the model
> can answer most "what was the outcome of X" questions from the line alone, without needing to
> fetch the thread file.

Display form, briefly:

```
thr_<n> [<0..8 anchors>] — <100–150 char gist> [<state>]
```

The anchor set is what makes recall possible. The summary is what makes recognition cheap. Together
they're the *whole* recognition surface for retired threads — getting them right at retirement is
the point of the curator-summary ack flow.

### Symbol extraction (three passes ordered cheapest-first)

1. **Deterministic** (every turn) — regex/parser scan over turn content extracts identifiers (file
   paths, URLs, code symbols, project-configured patterns, user `#tags`). Free, reliable, narrow.
2. **Model-emitted** (every turn) — the topic tag's anchor list provides named-entity coverage the
   deterministic pass can't reach.
3. **Curator** (at retirement) — LLM drafts the closure summary (§3.5). A human ack gates it for the
   closures that warrant one — anchor-rich or long-engaged threads, queued and acked at a session
   boundary; a routine decayed side topic is auto-accepted with the draft (user ruling 2026-08-04,
   §3.5 AMENDED). Anchors are **not** curator-picked: they are the deterministic projection of
   active `history_symbols` (below), re-derived every owner turn with no LLM involved.

**Anchors vs. history (the evolving-anchor model, §2.7.4):** `history_symbols` is the canonical,
accreting, weighted per-thread set (soft-capped ~40) and the recall match surface. `anchors` is
**not** a separate storage tier — it is the deterministic top-`AnchorProjectionMax` (computed by
`ProjectAnchors`) **projection** of the thread's *active* history symbols, re-derived each owner
turn. `memops.AnchorProjectionMax` is a compiled constant (8), not a config key: the SPEC §2.6.1
`anchor.projection-max` directive is not read. A symbol that falls out of the projection flips to
**superseded** but is **retained, not evicted** — an abandoned premise stays a findable recall
handle, protected from capacity eviction by its `ever_central` latch. 0 anchors is legal (a
vague-start thread that hasn't accreted a headline yet). The earlier frozen-at-creation /
4-minimum-anchor model is gone. (Spec §2.2, §2.7.4, §3.4.)

### Topic-tag mechanics

The tag prompt is a compiled constant in `internal/prompt/template.go` (`TopicTagDirective`); the
two recovery reminders (`TopicTagReminder`, `EmptyResponseReminder`) live in the same file, and
changing any of them is a rebuild. The owner of a multi-existing tag is the lowest `thr_N` because
the coalesce buffer is a map and does not preserve tag order. The bare `*new-topic* [...]` alias is
accepted by the bounded preamble scan (so the §5.5 fetch timing, the missing-tag trigger and the
stream-filter suppression agree) and by the stream filter's Close-time trailing-line fallback; an
all-whitespace response longer than that scan resolves as tag-less rather than empty, so it fires
the missing-tag reminder. `fs.read` deltas buffer a write entry like `fs.write`, so a tag-less
read-only turn pays the full missing-tag recovery; whether such a turn should count as
conversational is an open calibration question.

### Recall mechanisms (four, layered by cost)

1. **Model-native recognition** (default; cheapest). The spine is in window; the model recognizes
   prior topics directly when a turn engages them. Emits topic tag at turn start. Handles synonyms
   and paraphrases for free since the model is the recognition oracle.
2. **Opportunistic surfacing** (deterministic backstop). When current-turn symbols match a retired
   thread's anchors above threshold, surface a prompt: *"We talked about X 2 days ago — pick up
   there?"* User ack drives explicit fetch.
3. **Cross-project recall** (Layer A2 → fetch). Per-project anchor digests in A2 are scanned each
   turn. Same opportunistic mechanism as (2), slightly higher threshold.
4. **Intra-thread recall of scrolled-out early content** (#109/#111). For the engaged long-running
   thread, the fine-tier chunk embedding index recalls early turns that have scrolled out of the
   FIFO assembly window. The engaged thread bypasses the coarse gate and runs directly against its
   own indexed chunks. On threads long enough to have a built summary tree, an O(log n) beam descent
   (`scoring.DescendChunks`) replaces the flat O(C_main) scan — the sleep cycle builds and maintains
   the per-thread summary hierarchy (`measure.RebuildTrees`); absent or stale trees fall back to the
   flat scan with no correctness loss (W8). The fine tier is filled asynchronously, so it lags
   scroll-out by up to the embedding-debt cap; the recall-completeness invariant (SPEC §3.4) forbids
   a dead zone there, so the automatic intra path also runs a **bounded lexical pass** over exactly
   that debt window (`exact.MatchExcerptsBySymbols` over `MemoryOps.LoadDebtWindowExcerpts`, ≤ the
   cap, query-symbol match) and unions the hits — durable content stays findable continuously
   through the flush lag, no per-query embedding, no unbounded scan (#123).

   The index is maintained by a single background indexer that swaps an `atomic.Pointer` snapshot,
   so recall reads never block; a per-thread turn-count watermark stops a stale in-flight embed from
   overwriting a fresher vector, and a flush arriving at an *equal* watermark (the same-turn
   cap-flush plus dormancy-flush double enqueue) always publishes. The lexical floor scans `(1 +
   pending) × cap` excerpts: `cap` is the embedding-debt cap (16) and `pending` counts
   enqueued-but-unpublished flush jobs — 0 at steady state and at most 258 at the v0.1 queue depth
   of 256 (queue plus one in process plus one blocked enqueue). Only a genuinely failed flush
   retires a pending job without publishing. Persisted vectors and summary trees are `.vec`/`.tree`
   sidecars under `<home>/.recall-cache/` (`internal/recall/measure/veccache.go`, `treecache.go`).
   Plans: `ROADMAP_PLANS/within-thread-summary-hierarchy.md` and
   `ROADMAP_PLANS/intra-thread-recall-design.md`.

**Decline categorization** matters for accrual:

- *Not relevant* → improve matching, do not tighten threshold.
- *Not now* → no parameter change.
- *Stop offering* → tighten threshold.

Without this distinction, declined offers due to bad matches accidentally suppress good offers too.
(Spec §3.4.)

**Guarantee boundary — findability, not reminiscence.** The recall stack guarantees that durable
content is *findable* (the §3.4 completeness invariant) and that overlapping context *surfaces
opportunistically* (the layers above). It deliberately does not promise spontaneous cross-domain
reminiscence: a connection between today's problem and years-old work in a different vocabulary
surfaces only if anchors or embeddings overlap. Per the founding tenet (THESIS.md), the
serendipitous bridge ("this reminds me of…") is the *human's* contribution to the braid — a feature
that tries to make the agent volunteer it is the anticipate-the-user anti-pattern. State this
boundary when setting user expectations; do not "fix" it.

### Closure and retirement

The ack is the **integrity gate** at the highest-leverage moment. The user catches misclassification
at retirement, the only point where its accuracy matters most.

Triggers:

- Auto-prompt when no engagement for `engagement.decay-turns` OR `engagement.decay-time` wall-clock
  (either threshold; OR semantics).
- Explicit `/done` from the user.

Flow: curator drafts retirement summary + anchor symbol set → user acks (single keystroke), edits,
or defers → on ack, thread file written, spine entry generated, Layer B/C eviction follows. (Spec
§3.5.)

Deep cold archival is the next stage past retirement — **implemented and wired**
(`internal/turn/archival.go`, `cmd/archive.go` `archive list`/`archive recover`): when spine
cardinality pressure builds, the runtime archives oldest retired threads off-spine via `git rm` +
`git commit` + an `archive/index.jsonl` entry. Recovery via `git show <commit>:<path>` is one
command — no bespoke archive format. (Spec §3.8.)

A batch meets SPEC §3.8.1's three results as follows: each `threads/thr_<n>/` is confirmed tracked
at HEAD before its tree hash is taken; the capture commit is `Add(".")` of the full worktree into
primary and precedes the membership append; removals are staged by `os.RemoveAll` plus
`autogit.Add(".")`, and `RemoveSpineRecords` performs the single spine rewrite; the deletion commit
runs `CheckSpineIntegrity` before and `CheckDerivedFresh` after. Recovery (`RecoverThread`,
`autogit.CheckoutTree`) refuses with `ErrArchiveEntryNotFound` or `ErrArchiveIntegrity` and never
writes a partial thread. Aged-out file versions come back through `GetFileVersion`, gated by the
`BlobReachable` predicate; the retention window is `FileChainRetentionTurns` /
`FileChainRetentionDays` (100 / 2, `internal/store/threadfiles.go`). The SPEC keys
`dedup.chain-retention-turns` / `dedup.chain-retention-days` are currently these compiled constants,
not yet read from directives. `RecoverThread` and `ArchiveThreads` remain public port operations; a
standalone `ArchiveThreads` is crash-recoverable through the same roll-forward.

### Fallback dissection (worst-case path)

When budget pressure exceeds threshold AND normal retirement isn't catching up:

- Dissector LLM clusters the oldest poorly-threaded content into semantically related groups.
- Each cluster produces a thread file + spine entry, identical in shape to a normal retirement.
- User-acked under default conditions; absolute-emergency mode dissects without ack and notifies
  after.

Structurally the same as normal retirement, *reactively* triggered on poorly-threaded content. Same
artifacts produced; same recovery path; same invariant preserved.

Handles three failure modes with one mechanism: wandering user, single-thread overrun,
never-cleanly-threaded content. **Quality is best-effort by design** — graceful degradation under
stress, not catastrophic loss. (Spec §3.6.)

### Multi-project as lifelong accrual

One unified spine across all projects. Every thread carries a `project` tag. Layer A1 = current
project's full spine; Layer A2 = compressed cross-project digests. **Cross-project recall is a
*designed feature*, not an emergent property** — it fires through the same opportunistic mechanism
as same-project recall, with a higher threshold.

This is what makes the agent's *career* possible — accumulated experience across all projects,
transfer across domains. The architectural commitment: **one entity, many projects, lifelong
accrual**. (Spec §2.5, §4.5.)

### Personalization through directive accrual

Behavior tuning lives in inspectable directive files that accrue from feedback signals:

- `directives/defaults.md` — system defaults, baked in.
- `directives/user.md` — user-wide overrides; accrues from explicit instructions, decline
  categorizations, accept/decline running statistics.
- `directives/prj_<n>/*.md` — project-specific tuning.

The directives are *text files*. Inspectable, editable by hand, portable, version-controlled. **This
is the property that elevates the system from "smart assistant" to "real research partner": the
agent learns how the user works, *and the user can read what it learned and correct it*.** (Spec
§2.6.)

### Crash stability and the dual-repo substrate

The runtime can be hard-killed (SIGKILL, power loss) at any instant, so recovery is designed as a
**deterministic cold-start reconciliation** — no signal handler doing teardown work (a footgun that
can itself be killed), no LLM in the loop, no user acknowledgement. On-disk state alone determines
the outcome. The load-bearing property: **the worktree is the truth; git is a recovery-point index
over it.** No canonical byte ever lives *only* in git, so any git DB can be recreated from the
worktree and losing a DB is never losing content. (Spec §4.5.8, #94.)

Two git DBs track the one worktree at two cadences: a **daily** DB (`.git-daily/`, per-turn,
disposable — nuked and reborn each day) and a **primary** DB (`.git/`, one day-grain commit per
day + permanent archival anchors). This split is what keeps the durability bar and the cost bar
simultaneously satisfiable: per-turn commits are cheap (disposable daily, no integrity gating) yet
still bound loss to **≤1 turn**, while primary stays a single small pack forever (~365
commits/year). The in-flight turn's raw `(prompt, response)` bytes are protected by a durable append
journal written *before* any canonical write, so even the one at-risk turn's content survives.

Framed against the thesis: **day-grain career history in primary is *better* inspectability**, not a
compromise — `git -C ~/.personant log` reads as a clean résumé (a day per commit, permanent archival
anchors), while today's turn-grain forensics live in the disposable daily DB and, across days, in
the append-only event log. Turn-grain git archaeology across days is deliberately *not* primary's
job.

Recovery reduces to a small state machine over on-disk observables (marker file, in-flight journal,
daily/primary HEAD trailers, watermark, daily-DB presence) that classifies into one cell and repairs
idempotently — a torn turn resets **only daily** to ≤1-turn-ago; a barrier or archival crash **rolls
forward** (never resets the worktree, which would destroy up to a day); a broken hand-edit is
**quarantined byte-exact, never deleted**. Ruled out permanently: async/deferred per-turn commit
(reintroduces the torn window), loss-bar extension (commit batching / ≤N-turn windows — ≤1 turn is
the bar), daily-DB durability guarantees (disposable by construction), and primary auto-recreation
(a missing primary is refuse-to-open, not a benign recreate).

Mechanism names. A turn is two journal appends (`JournalTurn`, kinds `prompt` and `response`)
followed by `CommitTurn`, which stages only the recorded write set through `autogit.AddPaths` with
no integrity flags; `store.JournalOwner` reads the turn id from the journal's first record, and a
legacy `op=turn` marker file is still read. Core recovery (`internal/recovery`) only detects barrier
and archival cells and returns a typed `RecoveryReport.Pending` (`PendingCompletion{Kind, Day}`,
plus `ScopedRestored` for quarantined-and-restored paths); `fileadapter.Reconcile`, which owns
`ArchiveThreads` and both repo handles, completes them, which keeps `recovery` from importing the
adapter. `recovery.LocateDeletionCommit` (`internal/recovery/stamp.go`) finds an already-landed
deletion commit by a
child-of-`ParentCommitHash` tree test; `store.TruncateJournal` carries the truncate-no-fsync proof
(`TestCell5_ResurrectedTruncatedJournal`). `store.Init` births `.git-daily` only alongside a fresh
primary, so a missing daily on an existing home keeps its legacy-upgrade shape. The barrier mints
its day-commit with `AllowEmptyCommits` after `CheckSpineIntegrity` on the staged tree, asserts
`CheckDerivedFresh` at B6, and derives lifecycle tags through `autogit.TagStamp` / `ParseTagStamp`
(the only formatters), reading the sealed day's log with the tolerant `eventlog` reader and
tolerating `ErrTagExists`. Recovery handles and plumbs the `op=rebaseline` marker, but the mid-day
arming trigger is not wired (ROADMAP.md). Packing: after every `gcCheckEveryCommits` (64) daily
commits the adapter reads daily's loose-object count and runs gc at `gcLooseObjectThreshold` (5000);
`MemoryOps.Consolidate` (go-git `RepackObjects` + `Prune`) packs both repos on the day-off idle
window. Session checkpoints target daily with a full `Add(".")` sweep. The open sequence runs
`memops.GateHomeFormat` before Reconcile; `version.UnversionedHomeFormat` is pinned at 1 forever and
is never an alias of `version.CurrentHomeFormat`, so an unmigrated legacy home keeps resolving to
format 1.

### Terminal ownership — one arbiter (`internal/term`)

Personant is a CLI product, so the terminal *is* the surface — and it was never designed; it
accreted around a line-reader built for one job, until five bugs shipped in one week of dogfooding.
They were one defect: **multiple independent owners of one terminal**. `internal/term` is the sole
owner of the file descriptors, the terminal mode, the single reader, every emitted byte, and the
ownership state that says who holds what at each instant. `internal/chat` keeps POLICY (what to ask,
what a key means); `internal/shell` keeps process execution and signal delivery; everything else
becomes a client that requests an effect and can never name the fd. Output is a **2×2 — {always,
tty-only} × {scrollback, ephemeral}** — with one cell **forbidden** (a pipe has no erasure, so
"always + ephemeral" cannot exist) and stderr *inside* the arbiter as a committed channel through
the same serialization point; naming the missing (tty-only, scrollback) cell is what dissolved the
catch-all writer that had absorbed every other one. Input is a **single read path**,
`ReadLine(Question)`: there is no API that writes text and then separately reads a line, so the
shape that erased seven menu prompts cannot be spelled. The platform boundary is **two seams, not
one** — S1 device (bytes, termios, winsize; the scripted byte-level fake plugs in here, keeping
exact emitted output assertable) and S2 platform (decoded events, mode intent, size; a Windows
`ReadConsoleInput` backend would plug in here) — because `read(p []byte)` cannot receive an
`INPUT_RECORD`, and a resize is a signal rather than bytes on unix either. Verification is
deliberately three-part and none of it is a live terminal: a **fail-closed screen model** that
reconstructs what the user would see and errors on any sequence it does not model (so writer and
model share one finite alphabet and cannot drift), **plain-backend bookkeeping parity** — the
non-TTY backend keeps the same books as the TTY one, which is the only sensor for the byte-invisible
defect class of stale mode and mis-routed interrupts, the two that shipped green — and **two
mechanical grep-class gates**, one banning direct terminal access outside the package and one
banning `time.Sleep` in terminal tests, each with an explicit allowlist that must not grow. See SPEC
§4.3.1–§4.3.3 for the behavior and `mad-design/terminal-layer/` for the debate that produced it.

Mode and reader mechanics. The session mode is installed once — entry mode captured, session mode
installed, restored at exit, plus one restore/re-install pair per `$`/`#` child window — and clears
`ICANON`, `ECHO`, `IEXTEN`, `IXON` and `ISIG` while leaving `OPOST` alone. The prompt/turn
difference in blocking strategy (`VMIN`/`VTIME`) becomes `poll(2)` readiness passed as a call
argument, so a stale-mode defect cannot exist. Clearing `ISIG` delivers `Ctrl-C`, `Ctrl-Z` and
`Ctrl-\` as bytes, which lets their dispositions depend on the editor's buffer; an async signal
handler cannot read it without racing the editor. `Ctrl-Z` is handled in band (restore the entry
mode, raise `SIGTSTP` at its default disposition, re-install on `SIGCONT`); `SIGTERM`/`SIGHUP`
restore the entry mode and exit 128+signal instead of being caught and returned. There is one reader
from open to close; consumers register on a LIFO stack and receive decoded events, never the fd,
because disambiguating a bare `Esc` from an `Esc`-prefixed sequence needs lookahead held across a
timeout that a handover would lose, and a mid-turn prompt pushes a nested entry over the turn. The
reader is joined before a child starts and restarted after the child is reaped; restoring the entry
mode restores `ISIG`, the one window in which `Ctrl-C` is a real signal. Non-terminal sessions
install nothing, and the committed piped-session golden asserts byte-identical output. The editor is
the arbiter's own because a readline-class library owns the fds, mode, signal handlers and byte
reservoirs (CONVENTIONS, liner). The progress indicator is a formatter, not a writer: it hands the
finished string to the arbiter's single ephemeral slot and keeps no belief about the cursor. The
arbiter owns the single TTY predicate, the heartbeat-versus-announcement rule (it knows what
occupies the line), the pairing of the abort hint with the abort-window state, and truncation to
terminal width less one column. Phase labels and tool receipts use the same announce-don't-render
hook; receipts are the (tty-only, scrollback) cell of the 2×2.

### Shell escape (`internal/shell`)

Each `$`/`#` line runs a fresh `$SHELL -c` instead of a persistent shell: a long-lived shell on
pipes needs a sentinel-marker protocol with a timeout and hang recovery, per-command execution gets
exit codes for free and cannot hang the session, and SPEC §4.4.3 already excludes the applications a
persistent PTY would serve. The REPL-facing seam is identical, so a persistent shell could replace
it later. The cwd comes from an epilogue appended to every command that reports the final directory
on a dedicated file descriptor (fd 3), never inside stdout, and re-exits with the command's status.
The REPL asks the runner first and ends the session whenever the runner declines, so any instant in
which a command runs and the runner says otherwise would let `Ctrl-C` quit personant. The runner
therefore publishes the child's process group as early as possible, treats having a pgid as having a
child, tracks interrupt ownership separately from deliverability (an interrupt claimed before the
group exists is queued), and the parent re-asserts `setpgid` after `fork` so `kill(-pid)` cannot
find no group. Spawning a child and handing over the terminal (`term.Handoff`) compose in one
function so the window cannot leak.

### Math rendering

The well-trodden LaTeX→terminal-image path (renderer, kitty / OSC 1337 / sixel emission, Windows
parity) exists in Rust and not in Go, so rendering and protocol-encoding work stays on the Rust side
in every future option; personant takes no Go rendering or sixel-encoding dependency. SPEC §4.6
states the binding protocol principle.

### Memory consolidation — the "sleep" cycle

Designed, partially built. The day-off idle window already fires `MemoryOps.Consolidate` (git
gc/repack on both git DBs, SPEC §3.11) and the within-thread summary-tree rebuild
(`measure.RebuildTrees`, #111). The fuller offline-reorganization vision — re-packing fragmented
structures, compacting the spine, advancing archival, and the SPEC §3.10.8 content-retention
keep/toss precision layer, which cites this section by name — is designed, not built. Full design
capture: **ROADMAP.md → Far-horizon, "Memory consolidation."**

### Weight-baked instinct from outcome history

Full design capture — the substrate-stays-recall/weights-become-instinct framing, signal sources,
training shape, and hard constraints carried over from the substrate-as-canonical thesis — moved to
**ROADMAP.md** (far horizon; not v0.1, not v1.0, not v2.0). Left here as a pointer because the
"sleep cycle" section above and the model-family-as-platform section below both reference it.

### Serving deployment

The local provider is **oMLX**, connected directly (an OpenAI-compatible endpoint with no proxy in
between). oMLX sends the extended inference-timing block that `model.inference-telemetry` logs (SPEC
§2.8: `ttft_ms`, `prefill_ms`, `gen_ms`, `total_ms`, tokens/second and token counts); most other
providers do not, and the event is then simply absent.

### Model-family as platform (v1.0 platform coupling) {#model-family-as-platform-v10-platform-coupling}

Personant v1.0 targets **one LLM model family**. Adding a second family is a per-family engineering
investment — not a configuration change and not a portability layer.

The reason is structural: cross-platform desktop frameworks (Qt, JVM, Flutter) rest on a
*deterministic substrate* — byte arithmetic, GUI APIs, HTML/CSS semantics. You abstract over the
layer above; the layer below is reliable. LLMs have no such substrate. Each family emerged from a
different RLHF process on different data with different reward signals. "Helpful" lands differently.
"Be terse" lands differently. "Emit valid JSON" has different failure rates. They are not different
implementations of a spec; they are different organisms with somewhat-different priors.

**Why a portability layer is a trap.** Cross-family mismatches don't throw errors — they produce
output that "looks reasonable enough" but is subtly wrong. The failure surface is silent, gradual,
and statistical only; no artifact points to anything; fixes may just exploit one model's habits
rather than solving the root. For Personant this is especially dangerous: weeks of
subtly-misclassified threads can accumulate before recall fidelity drops measurably, and by then you
have corrupted continuity with no clean rewind.

**The honest answer is gross replication** — three layers per supported family: (1) base intent
prompts (model-agnostic conceptual spec), (2) family-specific implementation prompts (tuned to
elicit the spec'd outcome from *this* family), (3) per-family eval suites (proving the outcome meets
spec). All three require maintenance per family; engineering cost is linear. No shortcut.

**Architectural mechanism.** Keep the model-family surface narrow and isolated. A
`model-family-adapter` package holds system prompts, tool-call expectations, output-stream
interpretation, and embedding model assumptions. Everything else (substrate, port boundaries,
ack-gated tool surface, recall scoring, dedup, autonomic git) is model-agnostic and survives
substrate migration cleanly. v1.0 ships with one adapter; each future family is a
weeks-of-engineering addition, not a feature flip.

**v1.0 target: the Gemma family** served locally via the `reaper` provider (unmetered inference on
the M5 Max hardware). The local setup essentially eliminates the vendor-coupling tradeoff: worst
case is local infrastructure maintenance, not vendor pricing or API deprecation. It also makes
roadmap items tractable that were vendor-cost-gated: Python-workflow inference steps and
weight-baked consolidation runs are both free at the token level.

Size tiering maps onto Personant's role split:

| Tier | Role |
|---|---|
| E2B | Narrow classifications: symbol-extraction tiebreaks, recall judgment on borderline cases |
| E4B | Swarm work: submind subdirectories doing parallel exploration |
| 26B-A4B | Main interaction model (MoE — 4B compute path, large capacity) |
| 31B dense | Heavy reasoning: closure summaries, curator decisions, Python computational workflow |

**256K context is the sim/working-set design target.** This affects Layer B byte budgets, closure
pressure, recall query depth, and the mock-client's need to simulate token counts for
budget-exhaustion testing.

### Minimize infrastructural prompts

> **Every infrastructural prompt is a tax paid forever. Every deterministic algorithm is a fixed
> cost paid once.**

This is the operating principle for Personant's prompt engineering strategy. When designing a
feature, the default question is: *can deterministic code do this without involving the LLM?* If
yes, it should. The LLM is non-deterministic, costly, slow, opaque, and — because of the
[model-family-as-platform](#model-family-as-platform-v10-platform-coupling) principle — every
infrastructural prompt multiplies: each prompt requires re-tuning per supported family.

Where deterministic code already holds the load in Personant:

- **Recall scoring** — deterministic Jaccard over symbol sets. The LLM provides topic tags; math
  decides relevance.
- **§3.9 dedup** — deterministic byte-content hashing + clock-aging.
- **Working-set composition** — deterministic Layer-A/B/C rendering with deterministic byte budgets.
- **Closure decay** — deterministic engagement-count + timer thresholds.
- **Symbol extraction** — deterministic regex passes (LLM supplements, never replaces).
- **Spine + autogit** — all deterministic.

Where the LLM legitimately earns its place (inherently fuzzy, no deterministic shortcut):

- Topic-tag emission — recognizing what a turn is about from free-form conversation.
- Chat response content — the actual work the LLM exists to do.
- Closure summaries — compressing conversation history into key takeaways at retirement.

The corollary: **even within LLM tasks, keep prompts narrow.** Constrain the input space; constrain
the output space; validate output deterministically before trusting it. A broadened prompt expands
both the family-tuning surface and the silent-drift surface.

### Concurrent sessions

Designed, not built: multiple live conversations open against one shared memory — multitasking one
career, since Personant's single-unified-awareness premise rules out the CWD-scoped-context shortcut
other agent tools use. Distinguished from multi-user (v2.0, locked): concurrent sessions interleave
on *shared* canonical state. Raises thread safety, per-session working-set scoping, and explicit
cross-reference-vs-isolation declarations; also a fragmentation source the "sleep" cycle above would
need to clear. Full design capture: **ROADMAP.md → Far-horizon, "Concurrent sessions."**

---

## Tool surface (bounded, role-shaped)

**Seventeen external tools** (SPEC §6.1): read-and-think (11) + draft-and-mutate (6). The LLM never
names a workspace path on a write call; all mutations go through `fs.propose_*` (ack-gated).

| Read / think | Draft / mutate |
|---|---|
| `fs.read` | `fs.tmp_write` |
| `fs.list` | `fs.tmp_read` |
| `fs.grep` | `fs.tmp_list` |
| `web.fetch` | `fs.propose_promote` |
| `web.search` | `fs.propose_rename` |
| `web.wikipedia` | `fs.propose_delete` |
| `web.wiktionary` | |
| `web.wikidata` | |
| `web.arxiv` | |
| `web.crossref` | |
| `model.consult` | |

**Out of scope (intentional, not deferred):** shell execution, git operations *by the LLM*, package
management, build, test orchestration, IDE integration, deployment, direct filesystem writes outside
`propose_*`. The runtime may perform analogous operations *autonomically*; the exclusion is at the
LLM tool layer.

**v1.0 will add Python-only computational tools** (`python.run`, `python.format`, `python.lint`) for
math/physics simulation. Targeted; not a general "agent can run anything."

**Mechanism:** the registry (`internal/tools`), the turn-side execution loop, and the web tools
(`internal/tools/web`). The inventory and the machinery to run it stay deliberately separate
concerns: a registry with nothing in it sends no `tools` field, which is also how an *optional* tool
is expressed (no search backend configured → `web.search` is simply absent, never "present but
broken"). Properties worth carrying: a dispatch **never fails** (unknown tool, handler error,
handler timeout are all results the model can recover from, because a provider that saw N calls
requires N replies), and tool execution is **at-least-once** under crash replay, which is fine for
read-only tools and is mechanically refused for mutating ones. See SPEC §6.1.4.

**The web tools, and what they are shaped against.** `web.fetch` is a plain GET → readability
extraction → Markdown, with no headless browser. Its two interesting decisions are both about
*honesty over apparent success*: a client-rendered page that yields no text returns an explicit
"requires JavaScript rendering" signal rather than an empty body (a model told the page is blank
concludes the information does not exist, and says so confidently), and a binary content-type is
refused rather than parsed into plausible garbage. `web.search` sits behind a one-method
`SearchProvider` interface, and its central contract is the same instinct: **"search failed" and
"search found nothing" are never conflated** — a silent empty on failure teaches the model that the
web has no answer. All web tools are tier 0 with a `http`/`https` **scheme allowlist** as the
boundary in place of an acknowledgement (SPEC §6.2.7): a user cannot adjudicate whether a URL is an
internal-network probe, and a prompt they learn to clear is worse than a mechanical bound. A local
per-turn/per-day query cap on search is enforced in our own code — an LLM in a retry loop against a
metered API is a real failure mode, and a provider dashboard reports it only after the money is
gone.

**Registry mechanics.** `internal/tools` maps name → (`model.ToolSpec`, handler, tier, mutates); a
handler is `(ctx, raw JSON args) → (bytes, error)`. `Registry.Specs()` returns nil when empty, so no
`tools` field is sent; `Dispatch` derives a per-call deadline from the turn context and reports only
caller cancellation to the caller; `Register` refuses a tool declaring `Mutates`, the mechanical
trip-wire for SPEC §6.1.4's at-least-once decision. The execution loop is
`internal/turn/toolloop.go`, reusing the §5.5/§3.3 re-issue path; the identity-only
`Chunk.ToolCalls` view separates a tool-call-only response from an empty one and feeds the
`running <tool>` phase label (`turn.IsToolPhase` is the single membership test the front end's abort
allow-list uses). Receipts are announced through the same presentation hook as phases.

**Web-tool upstreams.** `web.wikipedia` is a separate tool rather than a `SearchProvider` backend:
folding it in would turn the encyclopedia-versus-open-web choice into a config pin, at the cost of
one extra spec block in the request prefix. It queries the REST `GET
https://en.wikipedia.org/w/rest.php/v1/search/page?q=&limit=` (`pages[].{id,key,title,excerpt}`):
matched terms arrive wrapped in `<span class="searchmatch">` with the prose escaped, so tags are
removed and entities resolved in one tokenizer pass (two passes in the wrong order turn an escaped
`&lt;script&gt;` back into a tag); the returned `key` is already URL-safe and is concatenated, not
re-encoded; English is a host swap. `web.wiktionary` shares that path, decoder and renderer
(`internal/tools/web/wikimedia.go`) with its own host entry (`en.wiktionary.org`) in the per-host
politeness table, since the gate keys on the host a request reaches. `web.arxiv` reads an Atom 1.0
feed (`GET http://export.arxiv.org/api/query?search_query=&max_results=`, `encoding/xml`) and drops
an entry whose Atom `id` is not an `/abs/` URL; its ~3 s spacing is the only non-zero entry in
`politeness.go`. `web.crossref` queries `https://api.crossref.org/works?query=&rows=`; `title` and
`container-title` are arrays that are routinely empty and `issued.date-parts` can be `[[null]]`; the
contact address in the User-Agent selects the polite pool. `web.wikidata` uses the Action API
(`wbsearchentities`) because the REST search returns `title` = `"Q11452"` with no label anywhere in
the payload; it deliberately does not send `maxlag` (a bot courtesy that would turn a lagging
cluster into a failed read), decodes the in-band failure that arrives with HTTP 200 (an `error`
object and no `search` key), and builds the canonical URL from the `id` because `url` is
protocol-relative and `concepturi` is the RDF IRI.

---

## Permission tiers

Three tiers, with accrual via directive files (`directives/<project>/permissions.md`):

| Tier | Default | Covers |
|---|---|---|
| 0 (silent) | never ack | reads; writes inside `.personant/**`; `fs.tmp_*` |
| 1 (first-time ack with scope grant) | ack once, then auto-allow within granted scope | `propose_promote` (new + overwrite); `propose_rename` to workspace |
| 2 (always individual ack) | every time | `propose_delete` on tracked files; rename overwriting tracked; batches over a threshold; sensitive patterns |

The ack prompt is the accrual UI: single-keystroke `[y]/[d]/[p]/[n]/[e]` choices grant scopes that
accumulate in directive files. Friction trends toward zero as the system learns the user's working
pattern.

The active-project boundary is an additional axis: ops on paths *outside* the active project root
bump tier (§6.2.6).

`providers.toml` is **secret-bearing** with a hybrid redaction policy: agent-initiated reads refuse
outright; user-initiated `#` captures redact before reaching context. The capture half is
implemented (front end 0.0.7): the resolved-key set from the loaded pool is substituted out of the
capture text on the path into context — per occurrence, so the rest of the output survives — while
the user's terminal keeps the unredacted bytes it already streamed. The event says only that
redaction fired and how many bytes went; §8.2.1's "key material never appears in any log line" makes
recording the match itself a bug, not a diagnostic.

`$`/`#` are the counter-example that makes the tier model coherent: they take the user's line
straight to `$SHELL -c` as a shell string, with metacharacters intact and no tier check, because
§4.4.4 makes them user-initiated at the user's own privilege. The future model-facing `fs.*` tools
are the inverse — LLM-supplied arguments, argv exec, no shell interpolation. Same repo, opposite
rule, and the difference is *who authored the string*.

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

When a project gains a remote URL after creation (local-only → published), the runtime detects and
offers to adopt it as canonical identity.

---

## Testing as the lab bench

Spec §9 is **not a quality gate bolted on after features land**. It is the measurement instrument by
which the architectural thesis gets proven empirically — and the substrate by which the techniques
evolve iteratively.

The cost of getting this wrong without proof ahead of time is **asymmetric and severe**: extended
accumulated memory state under a structurally-wrong storage strategy has no graceful recovery. The
simulation regime exists *because* proof must come ahead of time.

**The harness is a first-class proof instrument, not ancillary code.** Because §9 is the instrument
by which the thesis is *proven* — and the primary driver of technique development (the "what does
the simulation say?" loop below) — the simulation harness (`internal/scenarios/` + `sim/`) is a
load-bearing deliverable, co-equal with the substrate, not test scaffolding that may be left to
unsupervised agentic accretion. It inherits the **full production discipline**:
single-source-of-truth (no value, key, or contract owned in two places), DRY-as-named-constants,
*designed in these documents before it is coded*, and changed only through the same review bar
(adversarial/MAD review on accreted territory). The stake is sharper than velocity: a neglected
instrument does not merely slow development, it can **lie** — a measurement seam that silently fails
(a renamed metric key, a drifted log marker, a dormant second clock path) makes its gate pass
vacuously, so the harness reports success while measuring *nothing*. A miscalibrated proof apparatus
invalidates the proof, not just the schedule; since this same harness is the v0.1
realism-convergence gate, the gate's authority *is* the harness's integrity.

**Harness ↔ system-under-test coupling rule.** The harness imports the system's *public contract
surface*, never its *implementation detail*. The discriminator: **would a production consumer
reasonably expect access to this value?** Yes → it is contract; export it if needed and reference
the public symbol (never mirror it). No → it is internal; the harness observes its *effect* through
a public API/predicate, or tolerates the imprecision — it never imports or copies the internal
value. (A shadow oracle still re-implements the system's *logic* independently — importing a public
*scalar parameter* like the SPEC §2.3 assembly window is not an independence breach; importing or
mirroring an *internal* one like the §6.5 flush-batching cap is.)

**Layers** (in build order):

1. **Unit tests** — deterministic state operations, JSONL round-trips, schema validation.
2. **Scenario tests** — handwritten lifecycle flows; mock LLM via canned responses; metrics emission
   per scenario.
3. **Invariant validators** — callable from any test (spine integrity, index drift, project refs,
   last-active validity, frontmatter-spine sync, engagement consistency).
4. **Churn tests** — randomized seeded sequences; invariants after every operation.
5. **Calibration tests** — same scenario, parameter sweeps; metrics matrices identify operating
   points.
6. **Cross-run baseline comparison** — stored baselines (git-tracked); regression detection.
7. **Acceptance simulation harness** — synthetic workload, logical-clock acceleration, steady-state
   assertions; the top rung is 120 days / four months (§9.1).

**v0.1 acceptance is the realism-convergence simulation gate passing** (top rung 120 days / four
months; SPEC §9.1), not Phase 5 feature-completeness — and it is a *converging loop*, not a one-shot
pass. Memory quality maintained throughout, zero out-of-context-space events, runtime costs bounded
and stable, steady state demonstrated such that another full span would run without degradation.

When making technique changes (parameter tweaks, algorithm adjustments), the question is always
"what does the simulation say?" — not "does it compile and pass invariants?" Bake metrics emission
into new code from day one; bolting it on later is much more expensive.

**Next testing change on deck — user-contributed file content.** The §3.9 file-editing workload
currently models the *agentic* edit shape: file content arrives via `fs.read`/`fs.write` tool deltas
while the user prompt stays generic. The planned variant models the *user-dictated* shape — the user
prompt itself carries the literal values and lines being added. The §3.9 reverse-delta store is
content-agnostic to source, so storage / dedup / clock-aging is already covered; the variant exists
to exercise the upstream paths that differ — user-prompt symbol extraction, the §3.0 transient-data
classification (`RetentionDecision` vs `RetentionTask`), and the §3.9.2/§3.9.4 live-window handling
of the same literal appearing in both the prompt and the file.

### Simulation-harness structure

The harness's load-bearing role (above) and the single-clock temporal model (SPEC §9.4.1) are the
design constraints. This subsection maps the implementation onto them — the structural inventory a
developer needs to orient without re-deriving it from the code.

**File layout — `internal/scenarios/`**

| File | Responsibility |
|---|---|
| `harness.go` | Core types: `Step`, `Scenario`, `Harness`, `StepSource`, `StepFeedback`, `RecallAck`, `ClosureAck`, `RecallFidelityMode`. Narrow optional interfaces (`threadIndexer`, `cacheSweeper`, `treeRebuilder`, `intraDivergenceProbe`, `cosineOpsReporter`). The `sliceSource` shim that adapts a fixed `[]Step` to `StepSource`. `SimClockStart`, `SimDayLength`, `SimWorkdayStart`, `SimDayIndex`. |
| `harness_setup.go` | `newHarness` (per-scenario isolation, clock default, telemetry wiring) and `scriptedCurator`. |
| `harness_run.go` | `RunScenario`, `runStep` (the six named phase helpers: `stepSetup` / `stepSetClock` / `stepExecTurn` / `stepScrapeEventLog` / `stepMeasureRecall` / `stepRecordMetrics` / `stepCloseDays`), `restartSession`, and the final-gauge helpers (`foldEventLines`, `peakHistorySymbols`). |
| `harness_resolvers.go` | Per-step resolver builders (`recallResolverFor`, `closureResolverFor`) and the invariant-cadence policy (`perStepInvariants`, `runInvariants`). |
| `recall_fidelity.go` | Recall-fidelity measurement (`recordRecallFidelity`), the incremental log-tailer (`logTailer`), and `classifyRecoverability`. |
| `invariants.go` | The full invariant validator set (`VerifySpineIntegrity`, `VerifyIndexFresh`, `VerifyProjectReferences`, `VerifyLastActiveValid`, `VerifyThreadMetaMatchesSpine`, `VerifyEngagementConsistency`, `VerifyThreadAccounting`, `VerifyClosedThreadConsistency`, `VerifyArchiveResolvable`, `VerifyDedupConsistency`, `VerifyNoBudgetOverflow`). `DefaultInvariants` = `cheapDefaultInvariants` (per-step: `VerifyLastActiveValid`, `VerifyNoBudgetOverflow`) + `heavyDefaultInvariants` (substrate-scale, day-cadenced on the sim). |
| `metric_keys.go` | Cross-seam metric-key registry (see "State-ownership map" below). |
| `sim/workload.go` | On-demand workload generator (`generator`, `GenerateWorkload`, `runWorkDay`/`runSession`/`runDayOff`). |
| `sim/campaign.go`, `sim/wander.go`, `sim/intra.go` | Specialized step-sequence builders for the campaign, within-thread wander, and intra-thread recall workload variants. |
| `sim/telemetry.go` | Per-day snapshot emission and the sim daily-record schema. |
| `sim/sim_test.go`, `sim/sim_rung_test.go` | `TestSim` (acceptance ladder entry point) and rung-by-rung regression guards. |

**State-ownership map — single source for every mutable harness value**

The "silent lie" failure mode (above) comes from two independent sources claiming the same value.
Each of the following is owned in exactly one place:

| Value | Owner | Rule |
|---|---|---|
| Simulated clock | `Harness.pinnedClock` — a **slaved copy** of `Step.At`. `stepSetClock` does `pinnedClock = Step.At`, never integrates a delta. One clock advance path; see SPEC §9.4.1 for the single-clock rationale. | Never set `pinnedClock` from a delta except as described for the execution-time refinement step (which normalises `At = pinnedClock + TimeDelta` before the set). |
| Day index | Derived: `SimDayIndex(pinnedClock)`. Never a parallel counter. | Any code that needs "which sim-day is this?" calls `SimDayIndex(h.pinnedClock)`. |
| Generator exec clock | `generator.execClock` — an exact generator-side **mirror** of `pinnedClock`, maintained by `reconcileAt` (burndown A3): every emitted step's `At` is re-anchored forward to it when execution-time refinement turns advanced the clock past the step's planned instant; an `At`-less refinement advances it by `TimeDelta`, mirroring the harness normalize. | The harness never reads it; it exists so the fix lives at the source (the generator) and `stepSetClock`'s non-monotonic warn tripwire stays a pure canary — mirror-drift is exactly what trips it, so the canary stays armed. |
| Cross-seam metric keys | `metric_keys.go` — the single const block. Harness writes them; sim reads them. | A key rename is one edit; a compile error if a reference is missed. Keys owned entirely inside `sim/` are NOT mirrored here. |
| Crash-scenario coverage | `crashScenarioCoverage` (`crash_matrix_test.go`) — the single greppable registry mapping each registered `crashpoint` name to its scenario. `TestCrashPointCoverageGate` enumerates `crashpoint.RegisteredNames()` against it. | A registered crashpoint with no scenario **fails the suite** — the mechanical gate that stops silent kill-point rot. The pre-crash `TurnOracle` is a local immutable snapshot, not shared mutable state, so it is deliberately not mapped here. |
| Archived thread set | `Harness.archivedThreadIDs` — **index-derived** via `refreshArchivedSet` from the canonical archive index (`store.LoadArchiveIndex`, entries with `RecoveredAt == ""`), reassigned wholesale; refreshed when a tailed batch carries an archive/recovery line (the change trigger, not the source of truth) (F4/F8). | The archival-forgiveness filter in recall fidelity and `VerifyThreadAccounting` both read this set; a recovered thread drops out the instant its index entry is stamped, and a log-emission gap can no longer under-count it. |
| Created thread set | `Harness.createdThreadIDs` — folded incrementally from tailed `thread.created` log lines. | Same pattern as above. |
| W1 divergence tally | Accumulated directly in `runStep` (not read from `measure.Service` atomics post-run), so the tally survives `RestartSession`-driven Service churn. | A restart installs a fresh Service whose atomics start at zero; reading post-run would lose probes from prior sessions. |
| Layer-B shadow | Generator-owned. Cross-checked each step against `StepFeedback.RuntimeLayerB` (the runtime's authoritative `ActiveThreads`) **two-sided**, both hard **==0 gates on mock rungs** (and live-embedding; report-only under live inference, where the real model engages off-plan and the shadow is id-blind): forward (runtime-resident absent from shadow, `layerb_shadow_divergence`) is the LRU-agreement gate; reverse (mirror-retained but runtime-evicted, `layerb_shadow_reverse_divergence`) is checked against a **runtime-mirror** (`rtActive`, built at execution time so it observes refinement turns and session restarts) that replicates the two legitimate eviction sources the engage()-only forward shadow does not — measurement-carrier displacement and §3.5 turn-idle decay closure — so a reverse residual is a REAL mirror/runtime LRU disagreement, not modeled over-retention (BD-4 / B3, closed 363→0). | The shadow LRU is the generator's oracle for expected-match sets; a forward divergence would make recall measurements wrong; the reverse mirror catches a mirror-held thread the runtime evicted — both directions now gated ==0 on the plan-following runs. |

**Generator ↔ harness protocol**

The `StepSource` interface is the only coupling between the on-demand workload generator and the
harness drive loop. The protocol is:

1. The generator emits `Step` structs carrying an **absolute `Step.At`** (the step's position on the
   simulated clock, derived from `SimClockStart`). This field is mandatory for sim steps;
   `stepSetClock` sets `pinnedClock = Step.At`.
2. After each turn, the harness passes `StepFeedback` back into `StepSource.Next`. `StepFeedback`
   carries: `RecallMatchFireIDs` / `EmbedMatchFireIDs` / `IntraMatchFireIDs` (what the runtime
   surfaced), `RuntimeLayerB` (the authoritative Layer-B membership for shadow cross-check),
   `TargetRecoverable` (archival-forgiveness predicate for probe scoring), and
   `RecallExpectedForgiven` (the forgiven expected count the hit/miss counter must use).
3. Two pinned contracts prevent silent drift: **(a) log-marker contract** — the harness scrapes
   specific runtime log strings (`spine.match-fire`, `spine.embed-match-fire`,
   `spine.intra-match-fire`, `thread.created`, `archive.archived`) as the measurement signal; these
   strings are pinned by contract tests so a rename breaks a test, not a measurement; **(b)
   metric-key contract** — every counter or histogram key that crosses the seam is in
   `metric_keys.go`; a rename compiles only if every reference is updated.

**Acceptance-run mechanics**

- `internal/scenarios/sim/` holds the acceptance simulation (`TestSim`, the recall-madlibs
  corpus-slot workload); `just sim DURATION=<N>d` runs a chosen span (aliases `1d`/`1w`, Go duration
  forms such as `168h`) and `just test` runs the default `1d` rung. Metrics come from
  `internal/metrics`; scenario steps carry `ExpectedRecallMatches` ground truth scored in
  `RecallStrict` or `RecallMeasureOnly` mode, with baselines under
  `testdata/baselines/<scenario>.json`.
- The mock LLM client lives in `internal/model` beside the production `HTTPClient` and implements
  the same `Client` interface; only the constructor differs.
- The default gate (`just test` / `just sim`) runs with a nil embedder, so the fine tier and the
  lexical completeness floor do not execute there; `just sim-completeness-rung` is the
  embedder-enabled rung that asserts every flush-lag dead-zone probe is surfaced. The scenario
  harness pins `recall.ack-mode: always` (a scripted `Step.RecallAck` is a human ack).
- The per-turn boundary of the log is `context.modified source=user.prompt`: the A2 tag-fidelity
  grader segments observed turns on it, so suppressing, reordering or duplicating that line must
  revisit the grader. `recall_unexplained_absence` (an expected thread that is off-spine and absent
  from the archive index) is always a substrate integrity failure and must stay 0, while an
  archived-recoverable expected match is forgiven (`TargetRecoverable`, `RecallExpectedForgiven`).
- Long-rung symbolic precision is bounded by corpus saturation: the ground-truth oracle is
  `FamilySize`-bounded, so topically legitimate extras from non-sibling threads score as precision
  misses. Making long rungs measure memory scaling again needs the corpus vocabulary to scale with
  thread population (per-thread symbol salting), a queued sim improvement.
- Live rungs use `reaper.local` (gemma-4 family inference, `nomicai-modernbert-embed-base-bf16`
  embeddings). Service-in-loop raises per-turn cost from tens of milliseconds to single-digit
  seconds against roughly 20 minutes for a mock 120-day run, so the mock regime stays the default
  loop; an empirical sweet-spot hunt (service calls on a fraction of turns, or only recall-bearing
  turns) is how coverage per unit of overhead gets maximized. The evolving-anchor workload variants
  (`vague-new`, `drift`, `invert`, interleave) report `drift_recall_origin`/`drift_recall_dest`,
  `abandoned_premise_recall`, `superseded_precision`, `ever_central_count`/`history_len` and
  `projection_churn` through `metric_keys.go`.
- Cadence: the generator's day functions (`sim/workload.go` `runWorkDay`/`runSession`/`runDayOff`)
  and the harness day-close share SPEC §9.4.1's clock contract; the defect it closed was a jittered
  generator counter beside a rigid `nextHeavyAt` grid, which let a day-off's 24h jump skip a
  day-close. The day-off emits a `SleepCycle` marker step the harness intercepts to drive
  consolidation, and a day-close is stamped `simDayCloseDate(d) = SimClockStart + d·SimDayLength`
  (`internal/scenarios/harness.go`). Tests:
  `TestCadence_DayStartsNoPrecession` (at least 9 days), `TestCadence_DayOffPreservesPhase` and
  `TestDayOffThroughHarness`. A simulated-clock read inside one turn (`clock.Timeline()` within
  `turn.Run`) returns the same instant; closure decay keys off `TurnNumber − LastEngagedTurn`.
- Crash injection: `internal/crashpoint` provides named hooks (`crashpoint.At(name)`), test-armed
  and a no-op in production, registered at every kill point; `TestCrashPointCoverageGate` fails the
  suite when a registered point lacks a scenario in `crashScenarioCoverage`. `RestartWithCrash`
  (`internal/scenarios/restart.go`) arms a point, drives the turn or operation, discards in-memory
  state and drives `Reconcile` against the on-disk result. The matrix covers the turn orderings
  (`turn.postJournalPreCanonical`, `turn.betweenCanonicalRenames`, `fileadapter.CommitTurn.*`,
  `store.WriteFileAtomic.{preRename,tornWrite}`, `fileadapter.JournalTurn.preAppend`), every barrier
  step (`barrier.preArchival` through `barrier.postWatermark.preMarkerClear`), every archival
  sub-window (`archiveBatch.postCapture.preMembership` through `archiveBatch.postStamp`), standalone
  archival, recovery internals and morning-init. Each fixture asserts the full invariant suite plus
  `VerifyThreadMetaMatchesSpine`, at most one turn of loss against a pre-crash oracle, exact journal
  bytes, empty `RevertedPaths` on barrier and archival paths, exactly one day-commit, and an
  idempotent second `Reconcile`. The matrix is unit-grade (synthetic homes, inside the 30-minute
  checkpoint budget); `TestSimBarrier2Day` adds a 2-day barrier-crossing rung to the default suite.

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

### Deferred (real future work)

Moved to **ROADMAP.md** (near-term / mid-term / far-horizon tiers), which now owns this catalog,
including the full design capture for the "sleep" cycle and concurrent sessions — the Mechanisms
section above keeps only a short now-stub for each (designed-not-built status plus the interface
fact other docs cite), since SPEC §3.10.8 and the archive-index schema still need a live anchor.

---

## Anti-patterns to push back on

If you see one of these proposed (or are about to write it), stop and surface the concern:

| Anti-pattern | Why it's wrong | Reach for instead |
|---|---|---|
| "Let the LLM decide where to put state" | Pushes canonical state into the LLM | Deterministic code holds state; LLM proposes via tmp + ack |
| "Ack every closure, however routine" | An ack the user clears reflexively launders an unread summary as human-verified — the §3.5 rubber-stamp failure, reached through frequency (AMENDED 2026-08-04) | Keep the ack where the leverage is (anchor-rich / long-engaged threads), batched at a session boundary; auto-accept the routine ones. Removing the ack from the high-leverage moments entirely is still the anti-pattern |
| "Ack every recall candidate, however confident" | Same failure in its recall form (AMENDED 2026-08-05): a routine, cryptic four-option question — `[1] thr_3 score=0.56 (intra-thread)` — is answered by whatever clears it fastest, which launders an unvetted fetch as a human decision | Band it: auto-fetch above the tier's auto threshold with one committed line, ask the middle band with a question a user can answer in two seconds (display name, gist, why it matched), surface nothing below. And if an ack recurs after the user answered it, the defect is the missing state, not the user |
| "Use SQLite for the spine" | Substrate violation; loses inspectability + git-diffability | Text + git; SQLite stays in reserve as a *derived* index if scale forces it |
| "Have the LLM run `git commit`" | Mixes LLM-controlled with autonomic git | Runtime commits autonomically; LLM has no git tool |
| "Add this dep speculatively" | Approved-when-earned violation | Wait until consumer is real; bring dep in *with* consumer |
| "Tests can come later" | Asymmetric-cost violation | Measurement infrastructure built alongside features, not after |
| "Mock LLM is too narrow; let's use a real LLM in tests" | Adds non-determinism, network dependency, cost | Mock with scripted/generated responses; real LLM for `just integration-test` only |
| "Just add `vim` shell-escape support" | Pulls in PTY mode-handoff complexity | Held until empirical pressure; user can suspend personant |
| "Speculatively prefetch threads we *might* need" | Violates "every loaded thread is needed" | Model emits topic tag; runtime fetches what was named |
| "Shorten the topic-tag instruction to save tokens" | Topic-tag emission is the load-bearing recognition signal | The system prompt's topic-tag directive is non-optional |
| "Reset the worktree when it's dirty on startup" | A markerless-dirty tree is a legitimate hand-edit; resetting on dirtiness destroys user work (and, off primary, a whole day) | Gate `reset --hard` on the turn marker/journal signal, never on dirtiness; absorb hand-edits forward (Spec §4.5.8 cell 3) |
| "Commit durability writes async / deferred (flush later)" | A commit lagging its canonical writes reintroduces the torn-window ambiguity the marker/commit protocol exists to eliminate | Commit ordering is synchronous, always; cost comes from cheaper steps (scoped add, disposable daily), never from widening the loss window |

---

## Document map

| You want | Look here |
|---|---|
| Architecture orientation | `ARCHITECTURE.md` (this file) |
| House rules for AI agents | `CONVENTIONS.md` |
| Field-level schemas + algorithms + APIs | `SPEC.md` |
| User-facing description, getting started | `README.md` |
| Substrate-level decision history | `ARCHITECTURE.md` §"Substrate non-negotiables" |
| v0.1 acceptance criteria | `SPEC.md` §9.1 |

---

## How decisions evolve

The first-pass design is guaranteed to have holes. Personant evolves by:

1. **Calibration scenarios** — parameter sweeps over canonical workloads; metrics matrices identify
   operating points.
2. **Directive accrual** — per-user / per-project tuning happens automatically through ack patterns.
3. **Empirical pressure on watch items** — known weak spots (categorized below) surface via
   instrumentation; targeted work follows the data.

When you propose a change, ask:

- Does it have a measurement story? (How will we know it worked?)
- Does it commit to a specific value, or open a calibration window?
- Does it preserve or invert a recurring pattern (above)?
- Does it create migration cost we can't recover from?

**Categories of known weakness** the simulation regime is designed to surface and quantify:

- **Symbol-extraction noise** — synonym fragmentation, low-information leakage, model emission
  drift.
- **Recall precision** — cross-thread symbol collision, anchor selection quality at retirement.
- **Deterministic-pass coverage** — per-project regex curation needs.

These aren't blockers; they're *expected* findings.

---

## Mental model summary

If you internalize one thing from this document, make it this:

> The runtime is the canonical-state holder. The LLM is a narrow judgment instrument. The human
> appears at three high-leverage moments. Every working-window mutation goes through the §3.0 chain.
> Every dependency earns its keep with a real consumer. Testing is the lab bench, not a quality
> gate. Constraints are load-bearing.

Recognize these patterns, push back on violations, and you'll be on safe ground. Read `SPEC.md` for
execution detail.

---

## Provenance

The architecture is shaped more like an **agent runtime** than a chat app. Memory hierarchy, paging
mechanism, lookup index, personalization layer, persistence substrate, instrumentation, multi-agent
capability. The naming follows: Personant is a runtime, not a feature set.

The design originated in conversation 2026-05-07/08, building on observations about Claude Code's
compaction-driven information loss, the AVE-KB's text-in-git knowledge structure, and the asymmetry
between current AI systems' session-bounded context and what a long-term research partnership
actually requires. AVE-KB conventions (text + git, frontmatter, claim-quality propagation, derived
indexes) were the inspiration; the system is independent.

The name "Personant" is a portmanteau of *personal assistant*. Latin *personare* (to sound through)
and *persona* (the mask one speaks through) are bonus resonances rather than load-bearing meaning.
