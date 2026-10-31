# DOC RECALIBRATION PLAN – Personant

Brings SPEC.md, ARCHITECTURE.md, CONVENTIONS.md, ROADMAP.md and README.md in line with the plugin
route. THESIS.md is not changed. Where OPENCODE_ROUTE_DECISIONS.md (cited **RD**) and
ACTIVE_DESIGN.md (cited **AD**) differ, RD governs (RD preamble). Report shorthand: **IO** =
OPENCODE_INTEGRATION_OPTIONS.md, **PR** = OPENCODE_PROBE_RESULTS.md, **RP** =
OPENCODE_RESUME_PROBE.md, **CI** = OPENCODE_CONTEXT_INJECTIONS.md, **PP** =
OPENCODE_PREPROBE_RESULTS.md. Decisions the user must make are **U1–U11** (section 9).

## 0. Rules for every rewrite

1. **SPEC section numbers do not move.** Go code cites them heavily: `git grep -c -E '§[0-9]' --
   '*.go'` summed → 1788 references in 257 files. A removed section leaves its number unused, as §1
   and §7 already are. New content goes in a new number or a new subsection. Never renumber.
2. **Contract documents state what is true now or has been decided; open items are marked
   inline.** A clause that depends on an RD "Open" item states the decided part and carries an
   explicit `Open:` line naming the decision in words (SPEC §0 defines the form). The same item
   also appears in ROADMAP.md, which sequences its resolution.
3. **opencode internals are cited, not restated.** opencode file:line citations stay in the
   reports. ARCHITECTURE cites report sections. The pinned opencode version is stated in one place:
   the `sim-opencode` recipe's version check (AD "First work" → Components, item 7).
4. **DEFECTS.md is never written into the contract.** SPEC and ARCHITECTURE keep the required
   behaviour; section 7 maps each defect to the clauses it breaks.
5. **The self-built harness code stays until its replacement works** (ROADMAP B1). Until then,
   docs call it superseded, not product.
6. **Detail level follows U2**, decided before any writer starts (section 1).
7. **Review and format:** every rewritten document is read by agents, so each gets a
   prompt-engineer review before first use (`~/.claude/CLAUDE.md` "Durable model-facing
   artifacts"). Wrap at 100 columns (CONVENTIONS.md "`.md` files wrap at 100 columns").

## 1. Preconditions before any writer is dispatched

- **U1 (landing branch)** and **U2 (balance)** are decided; they shape every rewrite.
- **The ACTIVE_PLAN.md spot check is done or waived.** CONVENTIONS.md exempts probe plans from the
  full post-sprint review but still requires "a spot check that the data it gathers measures what
  it claims". No record shows it ran: `ls ACTIVE_PLAN_REVIEW_BURNDOWN.md` → no such file; `git log
  --format='%h %s' main..probe-path | grep -i -E 'review|spot'` → only 0d4e7ce, b3dac90 (plan
  reviews) and c489c45.
- A section blocked by U3–U10 is written once its decision is made; everything else proceeds.

## 2. SPEC.md — seat: tech-writer

**Stay** (memory content; only fix cross-references to removed sections): §0; §2.2, §2.2.1,
§2.2.2; §2.3; §2.4; §2.5.2; §2.6; §2.6.1; §2.7.1–§2.7.4; §3.0.2; §3.0.3; §3.0.5; §3.2; §3.3;
§3.6; §3.7; §3.8.1–§3.8.4; §3.9.2; §3.9.3; §3.10.2–§3.10.5; §3.10.7; §3.10.8; §3.11; §4.5.2;
§4.5.6; §4.5.8 (1), (2), (4)–(9); §5.1.1; §5.1.2; §5.4; §8.2.4; §8.3; §9.3; §9.4.1.

**Change**

| Section | Change | Settled by |
|---|---|---|
| top NOTE | remove once the rewrite lands | — |
| §2.1 | Remove `history` (REPL) and `tmp/` (§6.3 goes). Add: opencode's data home is never under `~/.personant/`. `providers.toml`, `config.toml` and `api_keys/` are placed by U5. | RD Requirements "Resume and recovery"; AD §6 |
| §2.5.1 | Keep the schema. Whether `conventions_paths` overlaps with the instructions slot is decided with the per-slot answers. Note the overlap; do not resolve it. | RD Open "Per-slot answers" |
| §2.8 | Remove events whose producer goes: `user.shell`, `user.shell-capture-dropped`, `model.switched`, `system.turn-aborted` (pending U8), `topic.re-prompt cause=missing-thread` (pending U3). Fold §6.4's `tool` events into §2.8 as memory-log events of applied tool deltas. `model.inference-telemetry` needs a source under the route (ARCHITECTURE "Serving deployment"). | RD Decided; AD Theme |
| §3.0.1 | `user.shell-capture` now comes from opencode's user shell (`!cmd`; CI M9; `session.shell.ended`). Add the `fs.read`/`fs.write`/`fs.commit` rows already listed in §3.10.1. `thread.fetched` waits on U3. | AD §4; CI §1.2 |
| §3.0.4 | A turn opens on a prompt **or** on a context call with no preceding prompt (a recovery turn). A `steer` prompt can arrive mid-turn. A response's tool results apply in tool-call order. | RD Requirements "Resume"; RD Decided (call order); CI §0 |
| §3.1 | Add the instructions, opencode-expectations and tool-definition slots. Each slot has four answers (budget, internal priority, overflow form, underusage). Underusage flows from fixed slots to dynamic ones. The whole-request bound includes tool definitions. The "§5.5 mid-turn fetch" paragraph waits on U3. | AD §10; RD Open "Tool-definition slot" |
| §3.4 | "Recall surface" and "Banding": the ASK channel under opencode is a yes/no prompt with no pick-list. The exact channel comes from U4. Matching, the completeness invariant and the scorer stay. | IO §3 R6; U4 |
| §3.5 | Keep the decay, routine/exception and curator rules. Rewrite the channels: the boundary drain "at clean session exit", `/closures`, `/done`, `[e]dit` and the `/back-to` revision path all wait on U4. An opencode session never "exits", so the drain needs a new boundary. | AD §9; U4 |
| §3.9.1, §3.9.4 | Replace the `fs.propose_promote` references with opencode's `read`/`write`/`edit`. Required: the tracked-file store holds file bytes, not `read`'s formatted output. `write` content comes from the tool input. | RD Requirements "Other lessons"; PR §5 |
| §3.10.1 | The `##`/`/keep` override waits on U4. Everything else stays. | U4 |
| §3.10.6, §6.5 | Resolve the contradiction (section 8, C1). Then move §6.5's bound into §3.1 as the bound on tool output in composed requests. Add the thunk outcome (N5). | RD Requirements "Composition" |
| §4 intro, §4.1 | Keep `init`, `index`, `verify`, `version`, `archive`, `search`, `deps`, `log` and the `--home` / `--allow-newer-home` flags. Remove `chat` (bare `personant` defaults to it) and `permissions list`. `ping` and `models` wait on U5. | AD Theme |
| §4.2 | Remove the commands that alias opencode behaviour: `/model`, `/models`, `/thinking`, `/key`, `/cd-project`, `/terminal-setup`, `/help`, `/version`, `/quit`. List the memory-native commands (`/topic*`, `/topics`, `/done`, `/closures`, `/pause`, `/resume`, `/back-to`, `/no-revisit`, `/keep`, `/threads`, `/spine`, `/index-rebuild`, `/project*`, `/stats`) as open per command (U4). Keep the vocabulary bridge. | RD "Sessions, commands, layout"; U4 |
| §4.3 | Keep the enum. How a reason is captured waits on U4. | U4 |
| §4.5.1 | Remove "Two cwds" (the shell cwd goes). The active project root becomes opencode's session directory. | AD §9 |
| §4.5.3–§4.5.5, §4.5.7 | Under opencode, the waterfall resolves from opencode's session directory. Its interactive steps — last-active confirmation, final fallback, remote adopt and collision prompts, file-move prompt — need a channel (U4). | AD §9; U4 |
| §4.5.8 | (3) "surfaced in the REPL banner" waits on U4. Add three things: reconcile against opencode's stored messages at service start; the recovery turn; the ≤1-turn bar judged against turns the user saw complete. | RD Requirements "Resume"; RP §4 |
| §5.1 intro, §5.1.3, §5.5 | Wait on U3. Stripping the tag from the user-visible text has no route under opencode (AD "Open decision"). | RD Open "Topic tag" |
| §5.2, §5.3 | Keep the prompt contracts. How the service reaches the model is U5. | IO §3 R10 |
| §8.1 | Drop the Wikimedia rationale from the `[user]` step. Keep `[user]` only if home commits use it; the writer checks the code. Add: init writes the settings opencode must hold (`compaction.auto: false`, the plugin entry) in opencode's own format. | AD §6 |
| §8.2, §8.2.1–§8.2.3 | Rewrite for U5. Personant-only settings live under `~/.config/personant/` and never overlap opencode's. Remove `[chat]`, `[search]` and `showThinking`. Keep `[embedding]`. The redaction policy keeps only the parts that still have a producer. | AD §6 |
| §9.1 | The gate, ladder and honesty clause stay. "Two version lines" waits on U6. | U6 |
| §9.2 | If ROADMAP A1 keeps the endpoint, add `model.MockServer`: the mock served as an OpenAI-compatible endpoint. | RD Open "Probe code" |
| §9.4 | "Measures only through the event log, never by introspecting runtime state" contradicts the Layer-B shadow check (section 8, C5). Make it true. | — |

**Remove**: §4.3.1, §4.3.2, §4.3.3 (except the memory half of the retraction rule, which U8 keeps
or drops), §4.3.4; §4.4–§4.4.4; §4.6 (opencode renders); §6.1–§6.1.9, §6.2–§6.2.7, §6.3 (tools
and permissions are opencode's — AD Theme "Boundary"). From §6.4, the `tool` events move to §2.8
and the `permissions` events go. §6.5 moves to §3.1. Section 6 keeps its number, empty except a
pointer to §3.1.

**New** (§1 is free; use it):
- **N1 §1 Operating model** (AD BLUF, "Theme"; RD Decided). Personant runs under unmodified
  opencode and owns what enters every request — composition, compaction and summarization, recall,
  sub-mind split and re-merge; opencode owns the rest. opencode's plugins and Code Mode work as is.
- **N2 Sessions.** An opencode session is a window into one continuous memory. Turns route to
  threads by topic, never by session. Deleting a session leaves memory untouched. A session never
  requires `/clear` or `/compact`, and neither discards memory (AD §9; RD "Sessions, commands,
  layout"). Fork and revert: not specified yet, see ROADMAP B6.
- **N3 MCP.** opencode delivers all MCP content — server instructions, tool definitions, tool and
  resource results, prompts — and every kind reaches composition. MCP prompt expansions are
  treated as user input (RD Decided "MCP"; RD Open "MCP prompt expansions").
- **N4 opencode content** (RD Requirements "Composition"). Instructions reach the model current,
  never as a stale baseline plus diffs; a nested `AGENTS.md` opencode injects once stays available
  while it applies; opencode's expectations (todo list, agent mode, a pending question or
  permission, reminders in force, child session IDs, background results) are always present as
  current state; nothing is counted twice when re-injected.
- **N5 Evicted content** (RD Requirements, thunk): content dropped from a request for budget is
  named in it at the version it had and can be brought back on demand.
- **N6 Determinism** (RD Decided): memory state does not depend on the order in which concurrent
  tool calls complete.
- **N7 Resume** (RD Requirements "Resume"): every request of a turn opencode resumes is composed by
  memory; at start, turns opencode kept but memory rolled back are re-ingested.
- **N8 opencode storage** (AD §7): checkpoints do not make opencode's database grow without bound;
  cleanup never breaks a kept session's scrollback, fork or revert.

## 3. ARCHITECTURE.md — seat: tech-writer (after SPEC)

**Stay**: Propose-and-ack (closure and recall bullets); Asymmetric cost; Port-and-adapter;
Substrate non-negotiables, with Substrate evolution and Port abstraction policy; Transient-data
lifecycle; Key abstractions; Thread lifecycle; Spine entries are the recognition surface; Symbol
extraction; Recall mechanisms; Closure and retirement (mechanism; ack channel per U4); Fallback
dissection; Multi-project as lifelong accrual; Personalization through directive accrual; Crash
stability and the dual-repo substrate; Memory consolidation stub; Minimize infrastructural prompts;
Testing as the lab bench; Simulation-harness structure; How decisions evolve; Provenance (trim the
"agent runtime" naming).

**Change**

| Heading | Change | Settled by |
|---|---|---|
| top NOTE, document table | Remove the NOTE. THESIS is purpose; add ROADMAP.md and DEFECTS.md. Drop "Reading time". | `~/.claude/CLAUDE.md` "Project documents" |
| What this is | A context-management and long-lived memory system under opencode. Remove the "research assistant, not polyglot coding" role: tools are opencode's. | AD Theme; RD Decided |
| Applying the thesis | Keep "deterministic state canonical / LLM narrow / ack at high leverage" as this implementation's principle. Stop presenting it as a reduction of THESIS.md, whose text no longer states it (`git show 44215be -- THESIS.md` removed that paragraph). | THESIS.md |
| Autonomic vs LLM-controlled | Home git bullets stay. The workspace git queries stay only for the §3.9 reachability check. "The LLM has no git tool" becomes "the LLM never touches `~/.personant/` git"; it has opencode's shell. | AD Theme |
| Single spelunking location | Three locations now: `~/.personant/`, `~/.config/personant/`, and opencode's own data home, never inside the first. RD cites this heading, so keep it. | RD Requirements; AD §6 |
| No speculative prefetch, Topic-tag mechanics | Wait on U3. | RD Open |
| Constraint as feature | The constraint becomes "context management only; conform to opencode". Drop the Python lift (ROADMAP). | AD Theme |
| Approved-when-earned | Add `modernc.org/sqlite` first for any Go SQLite need (AD §7 GC). `mattn/go-sqlite3` stays out. SQLite-as-canonical stays rejected. | RD Decided "Process" |
| The §3.0 hook chain | Add the delta-source → opencode-mechanism table (AD §4, with RD's turn-close row). Apply in call order. Correlate by opencode message ID. | RD Decided; RD Requirements |
| The layered working set | Add the new slots with the four answers and lending (AD §10). Values come from ROADMAP A5. | AD §10 |
| Serving deployment | opencode makes the provider call now, so the service no longer receives oMLX's timing block. Say where `model.inference-telemetry` comes from, or retire the event in SPEC §2.8. | AD Theme |
| Model-family as platform | Waits on U10. | — |
| Concurrent sessions | Keep the stub. It is now reachable (RD Open); link ROADMAP. | RD Open |
| Project identity | The waterfall input is opencode's session directory. The interactive steps wait on U4. | AD §9 |
| Testing as the lab bench | Move "Next testing change on deck" to ROADMAP (it is future intent). Add the opencode-in-loop harness (AD "First work"; PR §1) if ROADMAP A1 keeps it. | `~/.claude/CLAUDE.md` future intent → ROADMAP |
| Anti-patterns | Drop the `vim` row. "Have the LLM run `git commit`" becomes: on the home tree. Add: "re-implement something opencode owns" → conform to opencode. | AD Theme |
| Document map, Mental model | Update for the new set. | — |

**Remove**: Terminal ownership; Shell escape; Math rendering; Tool surface (bounded, role-shaped)
(registry, web tools, upstreams); Permission tiers; Out of scope → "Outside role scope" (the
"Deferred" pointer stays); Weight-baked instinct pointer (ROADMAP keeps the entry; fix the
references in "sleep" and "model-family"). Code still in the tree for these goes in a one-line
superseded note until ROADMAP B1 removes it.

**New**

- **Plugin route** (AD BLUF, "Language", §5): a stateless TypeScript plugin and a Go service that
  is the single writer; loopback HTTP+JSON over `fetch`; a per-RPC timeout; failure policy per U7.
  One line on the harness choice, opencode over Crush (RD Decided "Harness").
- **Integration shape** (AD §§1–3, §§6–9): composition through `session.context`;
  `compaction.auto: false` plus plugin-answered checkpoints; tools through `tool.transform`;
  config; Code Mode. Wire shape: one non-empty system part, no empty parts, no mid-conversation
  system messages; the context hook resets `options.maxTokens` (RD Requirements "Composition").
- **Injection handling** (RD Requirements; CI §§1–3): instructions slot from per-source
  `{key, text}` with a content hash; nested `AGENTS.md` captured; history rows typed by joining
  on message `id`; drop on re-injection; the expectations slot; the thunk.
- **Runtime inversion**: the phase entry points and their triggers (AD "Runtime inversion" with
  RD's close trigger). PR §8's dependency report is the interface the phases need.
- **Start order and resume** (RD Decided "Start order"; RD Requirements): the service listens
  before opencode, managed background server included; recovery turn; reconcile against
  opencode's store; opencode's data home outside `~/.personant/`.
- **opencode database GC**: AD §7's six rules.
- **Module map**, moved from CONVENTIONS "Current top-level shape". Add `internal/opencodehook` and
  `opencode-plugin/` once production code exists. Mark `internal/chat`, `term`, `shell`, `tools`
  and `tools/web` superseded. README "Project Anatomy" cites this map instead of copying it.

## 4. CONVENTIONS.md — seat: prompt-engineer (after ARCHITECTURE)

**Stay**: Plan & Execute Process; Plan work rating and dispatch; Plan dispatchability;
Anti-reaccretion doctrine; Build/test "Two gates"; house rules — substrate constraints, MemoryOps
only, clock through `internal/clock`, match the data model, serialization format, verify with the
canonical docs, auxiliary Python stdlib-only, spawned processes (now binding on spawned opencode
too), ROADMAP_PLANS, 100-column wrap; dependency vetting and "permanently out".

**Change**

| Heading | Change | Settled by |
|---|---|---|
| Thesis (load-bearing) | Red flags 2 ("anticipate vs assist") and 3 ("offload judgment") cite content THESIS.md no longer states. Restate them from current THESIS headings ("Behavior vs history", "Behavior is deliberately sculpted interactively with the user") or drop them. Add: a change that re-implements something opencode owns. | THESIS.md; AD Theme |
| Plan & Execute | The example page `RIGOR_AND_BEARING_SCORING_SCHEME.md` does not exist (`ls` → no such file). Replace it with a generic description. | — |
| Language constraints | Go for the service. TypeScript only for the plugin, stateless, importing nothing beyond what the host supplies (IO §2.1). "Single-binary delivery, no host runtime dependencies" is false now that opencode and Bun are required; restate. | AD "Language" |
| Versioning & acceptance; Front-end bump contract | Wait on U6. The REPL `/version` reference goes. | U6 |
| Deps: `langchaingo/llms` line | Waits on U5. | U5 |
| Deps: readability + html-to-markdown; Terminal/fd/signal deps (liner) | Delete once ROADMAP B1 removes their consumers. Until then, mark them consumer-pending-removal. | ROADMAP B1 |
| Ensure Docs Stay Up To Date | Add DEFECTS.md: a fix removes its entry in the same change. | — |

**Remove**: the NOTE line. **Current top-level shape** → ARCHITECTURE module map, and
**Intra-thread recall metrics** plus **W1 recall-preservation QUALITY MEASURE** → ARCHITECTURE
"Testing as the lab bench" / SPEC §9.4: both are state, which the anti-reaccretion doctrine bars
from this file. **Free-API citizenship**: its only consumer, `internal/tools/web`, retires
(ROADMAP B1); remove it in that change.

**New** (RD Decided "Process" unless noted): probe work on a throwaway branch, with defects going
to DEFECTS.md and not fixed there; probe runs use mock inference only, the live sim modes
unaffected; coder packages that share a build run one at a time in the shared tree, no
worktrees; SQLite: `modernc.org/sqlite` first; conform to opencode, never edit opencode source,
keep the clone on the installed tag (AD BLUF); the plugin holds no memory logic (AD
"Language"); external state outside `~/.personant/`, only if U11 adopts it.

## 5. ROADMAP.md — seat: tech-writer (after SPEC and ARCHITECTURE; may run beside CONVENTIONS)

**Obsolete** (delete; history is in git): `fs.*` tool surface;
Alt-B/Alt-F; `git-auto` sentinel (its `[user]` consumer was the web tools); math rendering
ladder; Rust front end / back-end split; v1.0 Python computational workflow (opencode's shell
covers it); "Sub-agent runtime extension" as written (definition files, routing and runtime are
opencode's agents) — its memory half moves to B6.

**Survive unchanged**: directive-file plumbing; mid-day re-baseline trigger; banded/auto sim rung;
W1 follow-ups; turn-time budget enforcement; F-A summary-tree key; recall tuning; non-ASCII
symbols; "six-month" comments; `store.ThreadTurnWindow` port bypass; flush re-enqueue; hop-0
embedding recall; Phase 5; behavior/convention boundary (re-cite THESIS "Behavior vs history");
archive recall; synthesis-attribution residual; project-lifecycle git tags; timestamp index;
inter-agent data-sharing security; synonym clusters; phrasal-concept extraction; event-log
compaction; encrypted at-rest storage; config migration (now covers opencode-held settings too);
multi-user; weight-baked instinct; sleep cycle.

**Survive, changed**: static-prompt-scaffolding accounting folds into A5; topic-tag residuals,
tag-fidelity instrument limits and template hot-reload sit behind A9; concurrent sessions moves
from Far-horizon to B5. Remove the stale NOTE; the precedence-chain sentence gains THESIS.md.

**New sequence.** Part A blocks a first usable build; each item gets runner-target done-lines
when planned.
- **A0 Decisions** U3, U4, U5, U7, U8, U9 (section 9).
- **A1 Probe code to keep**: `sim-baseline`, `sim-fidelity`, the committed baselines,
  `model.MockServer` (RD Open). Comes first because A3's done-line uses the comparator.
- **A2 D3 fix**: `just test` fails on it, and every checkpoint commit needs that gate to pass.
- **A3 Runtime inversion for real**: AD "Runtime inversion". PR §8 lists what it needs exported.
  The in-process sim reproduces its baselines unchanged.
- **A4 Production memory service and plugin** (RD Requirements and Decided): message-ID
  correlation; call-order application; plugin-side sequence numbers; close on
  `execution.succeeded`; one system part; `maxTokens` reset; provider-side request counting.
- **A5 Per-slot answers**: every slot including instructions, expectations and tool definitions
  (RD Open). Without the tool-definition slot the whole-request ceiling cannot be enforced.
- **A6 Injection handling**: instructions slot, nested `AGENTS.md`, expectations slot, ID-join
  typing, drop of M19 and M6 (CI §5).
- **A7 Resume and recovery**: D4 fix first; recovery turn; reconcile against opencode's store;
  start order with the managed server; data home outside `~/.personant/`; restart persistence per
  U9.
- **A8 Failure policy** per U7, after a spike on what opencode does when the hook throws (RD Open).
- **A9 Topic tag** per U3 (stream-abort spike or recall tool), with the missing `derived_from`
  lineage (RD Open: two candidate causes).
- **A10 D1 fix**, re-measured past sim-day 9 (RD Open): per-turn cost rose 88 → 341 ms in 24 days.
- **A11 Install and config**: `~/.config/personant/` and the opencode-side entries (AD §6, U5),
  plus the user command and ack channel per U4.

Part B follows the first usable build.
- **B1 Retire the self-built harness**: `internal/chat`, `term`, `shell`, `tools`, `tools/web`;
  the `chat`, `ping` and `models` verbs per U5; `test-fe`/`fe-scope-check` per U6.
  **Precondition:** A4–A11 done, with one end-to-end session through opencode passing
  `oc_missed_deltas == 0` and `oc_uncomposed_requests == 0` (PR §2 criteria).
- **B2 D2 fix** (recall-latency gauges). **D4** (harness reconcile) is pulled forward into A7:
  crash scenarios run through the shared harness are not evidence until it lands.
- **B3 opencode database GC** (AD §7); storage grows 2.6–5.7 KB per turn (PR §3.4).
- **B4–B9** (RD Open unless noted): MCP prompt-expansion detection and the injection unknowns
  (hook order after reload; lowering on non-openai-chat routes); concurrent sessions; sub-mind
  split and re-merge with fork and revert (RD Decided: designed together); agents and skills
  mapping; the eventual `/compact` and `/clear` mapping; resume cases not exercised.
- **B10** Whether the §9.1 acceptance gate needs an opencode-in-loop rung: unspecified anywhere;
  decide when A3 lands.

Then the surviving Near-term, Mid-term and Far-horizon items.

**ROADMAP_PLANS/** (same change; CONVENTIONS "A plan leaves … when it is finished, abandoned or
superseded"). `OPENCODE_SERVER_REPLACEMENT.md` and `code-mode-support.md` stay (the fallback, RD Decided).
Remove, once the writer confirms each is built and ARCHITECTURE no longer cites it:
`crash-stability-implementation.md`, `dual-repo-barrier.md`, `recoverable-archival.md` (+
`-review`), `intra-thread-recall-{design,plan}.md`, `within-thread-summary-hierarchy.md`,
`inference-in-loop.md` (README "What is shipped" lists them as shipped).
`within-thread-interleaving.md` (not implemented) stays behind SPEC §9.1 "Workload interleaving".
`multicameral-runtime-intelligence.md` cites paths outside the repository, which
ROADMAP_PLANS/README.md forbids: fix the citations or move it out.

## 6. README.md — seat: tech-writer (last)

- **Stay**: Developer; Storage Layout (after the SPEC §2.1 edits); Crash recovery; Sim Knobs;
  Third Party Acknowledgements (drop the web-tool rows when B1 lands).
- **Change**: intro (the memory system under opencode); Requirements (opencode at the pinned
  release, which brings Bun; "no host runtime dependencies" goes); Quick Start (install the
  plugin, start the service before opencode per RD Decided "Start order", run `opencode`); Build
  Targets (add `sim-opencode` and the kept probe targets; `test-fe` per U6); Status (version lines
  per U6; "shipped" restated for the route); Design Principles (cite ARCHITECTURE; the three-ack
  wording follows U4).
- **Remove**: "Using the Chat REPL" — commands, multi-line input, indicator, thinking, receipts,
  line editing, keys, dogfood note. Memory commands return under U4. Project Anatomy → a pointer
  to the ARCHITECTURE module map.
- **New**: how an opencode session relates to memory (SPEC N2); where settings live (AD §6).

## 7. DEFECTS.md — contract clauses each defect violates (seat: tech-writer, after section 3)

Add a "Violates" line to each entry. The contract text stays as it is; each fix is a mechanism or
test change with no document edit (`~/.claude/CLAUDE.md` routing: bug fix → no document changes).

| Defect | Violates |
|---|---|
| D1 commit cost grows with tracked files | SPEC §3.11 "Per-turn (daily)" (O(write-set), cost stays flat). §2.3 "per-turn write cost does not grow with thread length". §9.1 "Operation runtime costs … stable" and "Steady-state … per-operation latency flat". §9.4 "Steady-state trajectory" and "Operation latency within bounds". ARCHITECTURE "Crash stability and the dual-repo substrate" ("per-turn commits are cheap"). Possibly also §4.5.8 (5) morning-init "O(active working-set bytes), independent of career-history length": each daily repo starts from a full copy of the tree (D1 "Where"). Unmeasured; say so. |
| D2 `recall_query_latency_*` copies turn duration | SPEC §9.4: metrics measure what they name. ARCHITECTURE "Testing as the lab bench" ("a measurement seam that silently fails … makes its gate pass vacuously"). |
| D3 stale model name in `TestLoadConfigFixture` | CONVENTIONS "Two gates": the checkpoint gate must pass before a commit. No SPEC clause. |
| D4 harness skips the startup reconcile | SPEC §4.5.8 startup order "init → startup recovery → (day barrier if new-day) → session load". ARCHITECTURE "Testing as the lab bench" (the harness inherits production discipline) and "Acceptance-run mechanics" crash injection, whose fixtures then start from unreconciled homes. |
| Documentation errata | No contract clause; these are errors in reports. Move each erratum into its report as a dated note when RD retires (section 10). DEFECTS keeps system defects only. |

## 8. Contradictions found

| # | Between | Finding | Resolution route |
|---|---|---|---|
| C1 | SPEC §6.5 vs §3.10.6, §2.3 | §6.5: the full tool body over the cap is "written to the engaged thread's file". §3.10.6: raw task-class bytes are never persisted. | Writer checks `git log -L` on both, and the code, to see which moved last (`~/.claude/CLAUDE.md` mismatch rule). Likely §6.5 is stale. |
| C2 | SPEC §3.0.1 vs §3.10.1; ARCHITECTURE "§3.0 hook chain" | `fs.read`/`fs.write`/`fs.commit` are sources in §3.10.1 but missing from the taxonomy tables. | Add them (section 2). |
| C3 | SPEC §2.1, §8.2 vs AD §6 | Config in `$PERSONANT_HOME` vs `~/.config/personant/`. | AD §6 governs; the remaining details are U5. |
| C4 | ARCHITECTURE "Single spelunking location" vs AD §6, RD | "Everything … lives under `~/.personant/`" vs a separate config directory and opencode's data home. | Rewrite (section 3). |
| C5 | SPEC §9.4 vs ARCHITECTURE "Simulation-harness structure" | "Never by introspecting runtime state" vs `StepFeedback.RuntimeLayerB`, read from the runtime's `ActiveThreads`. | Narrow SPEC to "recall-fidelity scoring reads the event log". The Layer-B cross-check is a declared exception. |
| C6 | SPEC §5.5, §3.1 vs RD Open | "The model does not invoke a tool" and the mid-turn fetch; the fetch never fires under opencode (PR §5). | U3. |
| C7 | SPEC §3.4 ASK pick-list vs IO §3 R6 | A plugin cannot open opencode's forms; it has a yes/no prompt only. | U4. |
| C8 | ARCHITECTURE "What this is", "Out of scope" vs AD Theme | "Research assistant; the LLM has no shell or git" vs opencode's tools as they are. | Remove (section 3). |
| C9 | CONVENTIONS "Thesis", ARCHITECTURE "Applying the thesis" and "Guarantee boundary" vs THESIS.md | They cite THESIS content the current THESIS.md does not state. | Re-cite or restate as this project's own principle (sections 3 and 4). |
| C10 | CONVENTIONS "Current top-level shape" and metric blocks vs its own anti-reaccretion doctrine | State inside CONVENTIONS. | Move (section 4). |
| C11 | CONVENTIONS vs repository | `RIGOR_AND_BEARING_SCORING_SCHEME.md` is cited and absent. | Section 4. |
| C12 | ARCHITECTURE "Serving deployment" vs AD Theme | oMLX timing is read from a provider call that opencode now makes. | Section 3. |
| C13 | AD §5 vs RD Open | AD §5 states fail-open as design; RD lists the failure policy as open. | RD governs; U7. |
| C14 | PR, AD "Pass criteria" → ACTIVE_PLAN.md | Both cite ACTIVE_PLAN's pass criteria, which retirement removes. | Section 10. |
| C15 | ACTIVE_PLAN P6 "Status: not started" vs PR §2 | Runs 1–3 are done (commit 97808f4). | Moot after retirement. Note only. |

## 9. Decisions needed from the user

Checked against THESIS, SPEC, ARCHITECTURE, CONVENTIONS, AD, RD, the reports and ROADMAP_PLANS;
none of them settles any of these.

- **U1 Landing branch — decided:** the rewritten documents land on `probe-path`.
- **U2 Balance — decided: trim in place.** Remove what the plugin route no longer has Personant own
  (harness, interaction modes, workflow), keep the memory-system detail. Personant adds one improved
  system to opencode; it does not dictate workflow or interaction modes (RD "Route and scope").
- **U3 Topic tag** (RD Open). Keeping it needs a stream-abort spike and per-protocol wire code in
  the plugin, and opencode's transcript keeps the tag unless the plugin strips it. A recall tool
  means rewriting SPEC §5.1/§5.5 and the tag-fidelity measures. Blocks SPEC §3.1 (fetch), §5,
  §2.8 (re-prompt cause) and three ROADMAP items.
- **U4 Channel for memory commands and acks.** Partly settled (OPENCODE_ROUTE_DECISIONS.md "Decided" →
  "Sessions, commands, layout"): commands that alias behaviour opencode already provides are retired;
  memory-native commands are open per command (plugin-registered command, autonomic, or dropped); the
  ack channel stays open. The text below is the original framing.
  Personant's `/topic`, `/done`, `/back-to`, `/closures` and `/project`, the ASK offer, the closure
  drain, the bootstrap prompts and the recovery banner have no route under opencode. Config-command
  expansions cannot be told apart from typed prompts (CI §2), and TUI-plugin commands are
  unverified (CI §6). (a) Plugin tools the model calls, plus a yes/no `permission.assert`; (b)
  opencode commands carrying a marker the service recognises (spoofable); (c) an out-of-band
  `personant` CLI; (d) AUTO-only, no interactive acks. Each rewrites SPEC §3.4, §3.5, §4.2, §4.3
  and §4.5.3–§4.5.8 differently; (d) also removes acks ARCHITECTURE treats as load-bearing.
- **U5 How the service reaches its own models (curator, dissector, embedder) and where their
  settings live** (IO §3 R10 allows both). (a) The plugin's `experimental.generate` over
  opencode's providers: no overlap, though the embedder still needs Personant config (opencode
  has no embedding provider). (b) A Personant provider pool under `~/.config/personant/`:
  overlaps opencode's providers when both name one endpoint, which AD §6 forbids. Decides SPEC
  §2.1, §5.2–§5.3, §8.2, the `ping`/`models` verbs and the `langchaingo/llms` rule.
- **U6 Front-end version line** (`version.FrontEnd`, its bump contract and `test-fe` tracked the
  REPL): (a) retire it, keeping substrate and home format; (b) repurpose it for the plugin.
  Decides SPEC §9.1, CONVENTIONS "Versioning", README Status and the justfile.
- **U7 Plugin failure policy** (RD Open). Fail open: opencode's own composition reaches the model
  and memory silently misses the step. Fail closed: the session stops when the service is down,
  and whether opencode allows that is unestablished. Blocks the ARCHITECTURE transport section.
- **U8 Interrupted turns.** SPEC §4.3.3: a retracted prompt never enters memory; opencode's
  interrupt keeps the prompt in the transcript the user sees. (a) Keep the rule: memory forgets
  what the TUI shows. (b) Conform to opencode: ingest the prompt and the partial turn.
- **U9 Restart persistence** (RD Open). After a restart the model sees no earlier conversation
  while the TUI shows all of it (RP §3). (a) Persist history and the turn counter. (b) Rebuild them
  from opencode's store. (c) Accept: threads carry the memory. Sets a SPEC continuity outcome.
- **U10 Model family.** ARCHITECTURE "Model-family as platform" targets one family (Gemma), but
  opencode lets the user pick any model. (a) Keep one tuned family for the memory prompts and
  warn on others. (b) Support whatever opencode runs and measure tag and curator quality per
  family. (b) grows the eval surface; U3's recall-tool option shrinks it.
- **U11 Adopt the proposed CONVENTIONS rule** (RD Open): external state never inside
  `~/.personant/` or a test home. Yes/no; the RD requirement holds either way.

## 10. Rewrite order and retirement

| Step | Document | Seat | Depends on |
|---|---|---|---|
| 0 | decisions U1, U2; ACTIVE_PLAN spot check | user; architect | — |
| 1 | SPEC.md (sections not blocked by U3–U10 first) | tech-writer + prompt-engineer review | step 0 |
| 2 | ARCHITECTURE.md | tech-writer + prompt-engineer review | SPEC (cites its §§ and outcomes) |
| 3 | CONVENTIONS.md | prompt-engineer | ARCHITECTURE (module map moved; points into its sections) |
| 3 | ROADMAP.md | tech-writer + prompt-engineer review | SPEC, ARCHITECTURE (what graduated); in parallel with CONVENTIONS, separate files |
| 4 | DEFECTS.md "Violates" lines | tech-writer | SPEC, ARCHITECTURE final headings |
| 5 | README.md | tech-writer | all of the above |
| 6 | retirement commit | coordinator | steps 1–5 landed |

Sections blocked by U3–U10 get a follow-up pass as each decision lands, before step 6.

**Retirement (step 6).** **ACTIVE_PLAN.md** goes once SPEC, ARCHITECTURE and ROADMAP hold its
outcomes (CONVENTIONS "When the work lands, its outcomes move into the contract documents"), A1
has decided the probe code, and the spot check (section 1) is done; PR §2 and AD "Pass criteria"
cite its pass criteria, so copy them into PR §2 or cite them by commit first (C14).
**ACTIVE_DESIGN.md and OPENCODE_ROUTE_DECISIONS.md** go together, after a checklist maps every AD
section and RD bullet to one home (a SPEC §, an ARCHITECTURE heading, a ROADMAP item or a
CONVENTIONS rule; a bullet with no home blocks retirement) and every citation of them is
repointed (`git grep -n -E 'ACTIVE_DESIGN|ROUTE_DECISIONS'`). **The reports stay** as dated,
tracked evidence ARCHITECTURE cites; the DEFECTS errata move into them (section 7).
