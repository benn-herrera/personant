# Personant — v0.1 Architecture Outline

**Status:** living design outline. Initial decisions from the 2026-05-07/08 design discussion; refined in subsequent passes (most recently 2026-05-09 — agent role and tool surface).
**Audience:** the author, future implementation work, future agents revisiting the design.
**Not a spec:** a structured summary of decisions made, with explicit watchlist items and deferrals. The companion document [`spec.md`](spec.md) carries the field-level schemas and surface APIs.

---

## What Personant is

Personant is a single-user, single-agent runtime for managing an AI assistant's working memory across arbitrary projects with **continuity**. The agent has one continuous career: a unified persistent memory, no session boundaries, no compaction-style information loss, and the ability to work across many projects with cross-project recognition.

Name: portmanteau of *personal assistant*. Latin *personare* (to sound through) and *persona* (the mask one speaks through) are bonus resonances rather than load-bearing meaning.

The system inverts the typical agent-app pattern: instead of letting the model do all the work and silently accepting context loss, Personant uses **deterministic state as canonical, LLM in narrow judgment roles, and human acks at high-leverage moments only**. This thesis holds every design decision together.

---

## Architectural thesis

| Tier | Role |
|---|---|
| Deterministic code | canonical state holder; integrity enforcer; build / query / index operations |
| LLM | narrow generative/judgment roles (topic tagging, summary drafting, anchor selection, recognition, dissection clustering) |
| Human | final ack at high-leverage moments only (closure, opportunistic recall surfacing, fallback dissection trigger) |

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

- **Storage:** text files committed to git. Inspectability, recoverability, history, branching, free `git log` / `git diff` / `git blame` / `git grep`.
- **Format:** JSONL files for structured data (spine, symbols, threads metadata); markdown for thread bodies and directive files. Sorted deterministically (by ID) so git diffs stay record-grain rather than reformat-grain.
- **Build/query layer:** thin Go package within the agent runtime reads JSONL into memory once per process; lookup is a map access — sub-millisecond at any plausible v0.1 scale. The same binary serves the agent runtime and CLI commands (`personant deps thr_42`, `personant search "Cosserat"`, etc.) for ad-hoc shell work.
- **Reversibility:** the storage choice is reversible. Query API is the contract; if scale forces SQLite later, the substrate can change without callers noticing.
- **Freshness:** pre-commit hook regenerates index files from canonical sources; commit fails on stale index. Same pattern lockfiles use.
- **Single-source policy:** if any derived index disagrees with the canonical source, the source wins and the index is rebuilt. Drift cannot accumulate.
- **Autonomic git management:** the deterministic runtime is the only entity that mutates `~/.personant/`'s git tree. `git init` (first run), `git add`/`commit` (on canonical mutations), and pre-commit hook installation/invocation are runtime concerns, performed without user ack — same lifecycle status as writing to `spine.jsonl` itself. The LLM never invokes git. (The runtime may also issue read-only git queries against the *workspace* — `ls-files`, `status`, `diff` — for permission-tier classification and ack-prompt diff rendering, but it never mutates workspace git; that's the user's territory.)

### Implementation language

- **Personant runtime: Go.** Single-binary delivery, no runtime dependency on the host machine, goroutines map naturally onto the architecture (background curator + foreground turn handler), excellent stdlib coverage (file system, JSON, regex, http, sort), mature terminal libraries (bubbletea / lipgloss / tcell), cross-platform from one source. Verbosity-to-clarity ratio is well-suited to LLM-generated code.
- **Auxiliary Python scripts (where used): stdlib only.** Any helper scripts that aren't core runtime — ad-hoc analyses, one-off utilities, build scaffolding — must use only the Python standard library. No pip dependencies, no virtualenvs, no lockfiles. The stdlib (`json`, `re`, `pathlib`, `sqlite3`, `argparse`, `subprocess`, `urllib`) covers the use cases at hand. The constraint keeps blast radius small and lifetime long.

The Go runtime never has a Python dependency surface; Python utilities never accumulate a pip dependency surface. Both languages stay portable across their own version churn for years.

---

## Storage layout (default `~/.personant/`)

```
personant/
  spine.jsonl                    # unified across all projects, line-grain entries
  symbols.jsonl                  # inverse index, symbol → [thread_id, ...]
  threads/
    thr_<id>.md                  # full content per thread (operational notes, not pedagogy)
  projects/
    prj_<n>/                     # per-project metadata + recent-anchor digest for Layer A2 (stable internal handle)
  directives/
    defaults.md                  # system-default parameter values
    user.md                      # user-wide overrides (accrued)
    prj_<n>/...                  # project-scoped overrides
  logs/
    YYYY-MM-DD.log               # plain-text append-only event log, daily rotation
  README.md                      # explains layout for human inspection (rare path)
```

Storage is git-init'd on first run. The user gets free history and recoverability without any setup ceremony.

---

## Working-set composition (layered)

Per-turn context is composed from layers with explicit budget caps:

| Layer | Content | Allocation | Loading |
|---|---|---|---|
| E | directive files + project conventions (CLAUDE.md, AGENTS.md analog) | 5–10% | always |
| A1 | current project's spine entries (full) | 5–10% | always |
| A2 | other projects' compressed digest (per-project summary + recent anchors) | ~150 bytes / project | always |
| B | actively engaged threads (top-K full content) | up to 50% | populated by per-turn topic tag |
| C | recently engaged dormant threads (summaries) | up to 15% | decay-driven from B |
| current turn | user input + agent response | 15% | reserved |

Under budget pressure, bumpable in order: C-oldest, then B-oldest (compressed to summary). A and E are sacrosanct — losing them breaks recognition, which is the whole point.

**Recognition flow:** spine is always loaded → model recognizes prior topic directly from spine entries → emits topic tag at turn start → corresponding thread fetched into Layer B → response generated. **No speculative prefetch** — every loaded thread is loaded because the model said it was needed.

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

Display form (line in spine):

```
thr_<id> [<4–8 normalized anchor symbols, comma-separated>] — <100–150 char gist> [<state>]
```

Examples:

```
thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict; entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]
thr_92 [neutrino, helical-screw, cosserat, corrigendum] — closed-unknot → open-helical-screw correction across KB + LaTeX [resolved 2026-05-06]
thr_103 [kb, latex, canonicality, latex-is-derived] — KB inverted to canonical, LaTeX now derived [decided 2026-05-07]
```

State markers (informal vocabulary): `[WIP]`, `[blocked]`, `[paused]`, `[resolved <date>]`, `[decided <date>]`, `[abandoned]`.

JSONL form in `spine.jsonl`:

```json
{
  "id": "thr_88",
  "project": "prj_3",
  "anchors": ["trefoil", "unknot", "body-topology", "electron-shape"],
  "summary": "electron body-topology conflict; entries trf3bd / unk0bd added; awaiting Grant resolution",
  "state": "WIP",
  "created": "2026-05-06T...",
  "last_engaged": "2026-05-08T...",
  "state_changed": "2026-05-07T..."
}
```

The spine line is the load-bearing artifact. It must encode enough decision-state that the model can answer most "what was the outcome of X" questions from the line alone, without needing to fetch the thread file.

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

Storage scaffold created and git-init'd at first run. Initial parameter values:

| Knob | Default |
|---|---|
| `engagement.decay-turns` | 8 |
| `engagement.decay-time` | 7 days |
| `recall.symbolic-threshold` | 0.4 (Jaccard, permissive — tightens via accrual) |
| `recall.cross-project-threshold` | 0.5 |
| `layer.b-top-k` | 3 |
| `layer.budget-percentages` | E:5–10, A:5–10, B:50, C:15, current:15 |
| `anchors.cap-per-thread` | 6 |
| `history.cap-per-thread` | 40 |
| `spine.entry-max-chars` | 200 |
| `cross-project.digest-per-project-bytes` | 150 |
| `dissect.pressure-threshold` | 0.90 |

These are first-pass guesses. They are tuned via the directive-accrual mechanism; logged metrics show which defaults are off.

**First-run behavior:**
- Empty spine → opportunistic recall structurally disabled (no false "we discussed this before").
- Project inferred from CWD; explicit `/project <name>` overrides.
- No tutorial / onboarding ceremony — the agent is ready.
- `system.bootstrap` event written to today's log with version, paths, defaults snapshot.

---

## Logging

- One file per day at `logs/YYYY-MM-DD.log`, append-only.
- Plain text, timestamped lines, free-form details. No structured event schema in v0.1.
- Liberal logging — when in doubt, log it. Cost of an extra line is essentially zero.
- Rotation: 90 days plain; older archived/gzipped.
- Schema-promote frequently-queried log patterns to structured form when patterns emerge — not before.

Examples of natural events:

```
2026-05-08T02:55:44 thread.engaged thr_42 [trefoil, unknot] turn=1247
2026-05-08T02:55:44 spine.match-fire thr_88 type=symbolic
2026-05-08T02:55:44 retire.prompt thr_42 inactivity=12 ack=yes
2026-05-08T02:55:44 dissect.fire reason=budget-pressure clusters=4
```

---

## Watch list (known weak spots; instrumentation will reveal scale)

1. **Synonym fragmentation** — same concept emitted as different surface forms across threads. Most likely v0.2 driver.
2. **Anchor selection quality at retirement** — curator may pick subtly-wrong anchors. Track which anchors actually fire recall later; anchors that never trigger over months are dead weight, anchors that fire repeatedly are load-bearing.
3. **Cross-thread symbol collision** — single-symbol matches return ambiguous threads. May force richer match (combinations) or thread-cluster concept.
4. **Low-information symbol leakage** — generic words leaking past stopword filters.
5. **Model emission drift** — model behavior changes silently affecting symbol consistency.
6. **Pattern coverage in deterministic pass** — per-project regex curation needs.

The first-pass design is guaranteed to have holes. Instrumentation is what makes v0.2 design empirical instead of guessed.

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

## Architectural thesis recap

The system bets that **deterministic state + narrow LLM roles + minimal human acks at high-leverage moments** scales further than any of the alternatives. Concretely:

- LLM is in narrow roles only — topic tagging, summary drafting, anchor selection, dissection clustering, recall recognition. Never the canonical-state holder.
- Deterministic code holds canonical state, enforces integrity, and provides query/build operations.
- Human acks at exactly three moments: closure, opportunistic recall surfacing, fallback dissection trigger. Three places the user pays a small UX cost; everything else is deterministic background.

This pattern recurs at every layer of the design — from how spine entries get curated to how directive files accrue to how cross-project recognition fires. That consistency is the strongest signal the design is converging toward something coherent rather than a collection of clever pieces.

The success criterion: months-long seamless continuity. Externally, the user just talks to a partner who picks up where they left off. Internally, the substrate is doing constant work to make that appearance honest.

---

## Project lineage / shape

The architecture is shaped more like an **agent runtime** than a chat app. Memory hierarchy, paging mechanism, lookup index, personalization layer, persistence substrate, instrumentation, multi-agent capability. The naming follows: Personant is a runtime, not a feature set.

The design originated in conversation 2026-05-07/08, building on observations about Claude Code's compaction-driven information loss, the AVE-KB's text-in-git knowledge structure, and the asymmetry between current AI systems' session-bounded context and what a long-term research partnership actually requires. AVE-KB conventions (text + git, frontmatter, claim-quality propagation, derived indexes) were the inspiration; the system is independent.

---

## Companion document

[`spec.md`](spec.md) carries the field-level material:
- Storage schema definitions (JSONL field-level): §2
- Tool surface and permission tiers: §6
- Go package layout for the runtime; CLI command surface: §4, §7 *(stubs as of 2026-05-09)*
- Slash command catalog (borrowed where possible from Claude Code / OpenCode): §4 *(stub)*
- Directive file format specification: §2.6, §6.2.3
- Implementation milestone breakdown: §10
- Testing approach (especially for instrumented behavior): §11

The outline is for orientation; the spec is for execution. Both documents are living and edited in place as decisions evolve.

Both files are temporary scaffolding for the current pre-implementation phase. Once the project is sufficiently mature, this content will migrate into `ARCHITECTURE.md` (with companion updates to `AGENTS.md` and `README.md`) and these files will go away.
