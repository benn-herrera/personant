# OPENCODE ROUTE DECISIONS – Personant

Decisions, established requirements and open questions from the opencode integration work that no contract
document (THESIS, SPEC, ARCHITECTURE, CONVENTIONS) holds yet. Input to recalibrating those documents. Where
ACTIVE_DESIGN.md already states a decision, this document cites the section and does not restate it; where this
document and ACTIVE_DESIGN.md differ, this document governs.

Sources: ACTIVE_DESIGN.md, OPENCODE_SERVER_SURVEY.md, OPENCODE_TUI_LAUNCH.md, OPENCODE_INTEGRATION_OPTIONS.md,
OPENCODE_PREPROBE_RESULTS.md, OPENCODE_PROBE_RESULTS.md, OPENCODE_RESUME_PROBE.md, OPENCODE_CONTEXT_INJECTIONS.md,
DEFECTS.md.

---

## Decided

**Route and scope**
- Route: ACTIVE_DESIGN.md BLUF. The full Go server port is a parked fallback
  (ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md).
- Scope and the conform-to-opencode rule: ACTIVE_DESIGN.md "Theme".
- Personant is a memory system for a larger harness that has its own working patterns. All prescriptive language
  about workflow control and the human-involvement ethos is defunct: Personant does not control interaction modes,
  dictate workflow, or prescribe how or how much the user takes part. Where a pre-pivot document does any of these,
  that text goes. THESIS.md is unaffected: it prescribes no workflow, and the contract text that implements it stays.
- THESIS.md's behavior rule is a separation of stores — project work never writes behavior memory — not an
  approval requirement. No approval gate, confirmation prompt or other enforcement mechanism is built from it.
- Harness: opencode. Charm's Crush is not a candidate — it reads provider API keys from environment variables and
  runs its TUI in-process with its backend, so there is no protocol to sit under. Lands in ARCHITECTURE.md as the
  reason for the harness choice, if anywhere.
- Target version: ACTIVE_DESIGN.md (the installed release; the source clone kept on its tag).

**Behaviour under the plugin route**
- Composition and compaction: ACTIVE_DESIGN.md "Integration shape" §§1–2.
- A response's tool results are applied to memory in tool-call order, never arrival order; opencode runs one
  response's tool calls concurrently (OPENCODE_PROBE_RESULTS.md §6, lesson 3).
- Turn close keys on `session.execution.succeeded`; `experimental.idle` only decides checkpoints and can fire twice
  per turn (OPENCODE_PROBE_RESULTS.md §6, lessons 1–2).
- Model requests are counted at the provider side, never from `session.model.request`, which reports title and
  compaction kinds even when hooks answered them (OPENCODE_PROBE_RESULTS.md §6, lesson 4).
- Start order: the memory service is listening before opencode starts, including opencode's managed background
  server (OPENCODE_RESUME_PROBE.md §5.3). Lands in ARCHITECTURE.md.
- MCP: opencode delivers all MCP content — server instructions, tool definitions, tool and resource results,
  prompts — and the plugin route must handle every kind (OPENCODE_CONTEXT_INJECTIONS.md I2, M1, M6, R9, T1, T4).
  How tool definitions are budgeted and how prompt expansions are detected is open (see Open). Lands in SPEC.md as
  a supported capability.
- Code Mode and opencode's JavaScript plugins run unchanged under this route; ROADMAP_PLANS/code-mode-support.md
  applies only to the fallback.
- Config and opencode database cleanup: ACTIVE_DESIGN.md §§6–7.

**Sessions, commands, layout**
- Project mapping, sessions as windows into one memory, session deletion leaving memory untouched, and `/compact`
  and `/clear` as no-ops: ACTIVE_DESIGN.md §9.
- Mechanics of the no-ops (OPENCODE_CONTEXT_INJECTIONS.md §4): `/compact` cannot be refused; the plugin answers the
  `session.compaction` hook with a `result` so no model call is made, and `session.compaction.started` carries a
  `reason` that separates a user `/compact` from Personant's checkpoints. `/clear` is client-side only; it is a
  no-op because memory is not keyed on session.
- SPEC.md must state the outcome: a session never requires `/clear` or `/compact`, and neither command discards
  memory.
- Personant commands that alias behaviour opencode already provides are retired, not carried over: `/model`,
  `/models`, `/thinking`, `/key`, `/cd-project`, `/terminal-setup`, `/help`, `/version`, `/quit`. Memory-native
  commands have no opencode equivalent and are open (see Open). Where a memory-native command survives, the
  mechanism is a plugin-registered command (`CommandDomain.transform` → `CommandEditor.add`, whose `execute` runs
  server-side code, `packages/plugin/src/effect/command.ts`), not a `commands/*.md` file, which only expands into a
  prompt to the model.
- Slot budgets and underusage: ACTIVE_DESIGN.md §10.
- Fork and revert are designed together with sub-mind split and re-merge: both decide whether a branch's work is
  incorporated. Lands in the sub-mind design (ROADMAP.md).

**Memory vetting** (lands in SPEC.md and ARCHITECTURE.md)
- Memory text a model will later read is reviewed before it is committed. The reviewer is a model applying a fixed
  rubric of the kind used for prompt-engineer review: clarity, scope, absolutes that will be over-applied, conflicts
  with existing entries, wording another model would misread.
- Behavior memory has a single write path: every proposed change, whatever proposes it, goes through that review,
  and the outcome is recorded with provenance — what proposed it, what the review found, what accepted it.
- Acceptance is a user-configurable policy (for example manual acceptance, auto-accept on a clean review, or
  auto-accept everything). Personant ships a default and does not force a policy. Pending proposals never block a
  turn or prompt the user unasked.
- Direct edits to the store are not prevented; the versioned store records them.
- Directive accrual may propose a behavior-memory change only from patterns aggregated across projects (what
  concerns who does the work, which THESIS makes universal). A pattern observed in one project stays
  project-scoped: it may tune that project's conventions or career memory, never `directives/user.md` or the
  defaults. An accrual proposal goes through the same review and acceptance path as any other.
- Career memory is written every turn and is not reviewed per write. It is protected by bounded influence
  (budgeted slots), provenance on every entry, reversibility through the versioned store, mechanical checks on write
  (schema, length, provenance, dedup), and batch content review in the consolidation cycle.
- Standing behavior memory is re-reviewed periodically, because text that was sound for one model can mislead the
  next.

**Process** (CONVENTIONS.md material)
- Probe work runs on a throwaway branch; system defects found there are recorded in DEFECTS.md, not fixed there.
- Probe runs — runs whose purpose is integration data — use mock inference only. The existing live-inference and
  live-embedding sim modes are unaffected.
- Coder packages that share a build run one at a time in the shared working tree; git worktrees are not used.
- Probe plans are exempt from the checkpoint gate and the post-sprint review (already in CONVENTIONS.md).
- SQLite library, if Go code needs one: `modernc.org/sqlite` first.

---

## Requirements established for the real implementation

**Composition and injections** (OPENCODE_CONTEXT_INJECTIONS.md)
- The instructions slot is state rebuilt each request from the full per-source texts in the instructions system
  part's `metadata.experimental.instructions` (`{key, text}`); a source's content hash decides whether the slot
  changes. opencode's instruction-update diff messages are not used.
- A nested `AGENTS.md` opencode injects once (on a `read` into its directory) is captured into the instructions
  slot as its own keyed source.
- Content already accounted for is dropped on re-injection: by (source key, content hash) for instructions, by
  stored message `id` for history rows.
- History rows are typed by joining the hook input's message `id` against the plugin's session API; the hook input
  drops metadata on synthetic messages.
- Small opencode expectations — todo list, plan/agent mode, open question or permission, reminders still in force,
  the subagent sessionID needed to continue a child, background-job results — render as current state in a slot
  with a fixed budget (ACTIVE_DESIGN.md §10), not as history.
- Evicted file content is replaced by a thunk that pins the version (content hash), points at stored bytes when the
  content cannot be reproduced by replay, and appears as a recognition entry the model can expand (ARCHITECTURE.md
  "Spine entries are the recognition surface").
- Composed requests use one non-empty system part, no empty parts and no mid-conversation system messages; under
  that shape prompt text reaches the model byte-exact (OPENCODE_PREPROBE_RESULTS.md §5;
  OPENCODE_PROBE_RESULTS.md §6, lesson 8).
- The `context` hook resets `options.maxTokens`: opencode fits it to its own estimate of its own history, which is
  too low for a smaller composed request (OPENCODE_CONTEXT_INJECTIONS.md §3, A17).

**Resume and recovery** (OPENCODE_RESUME_PROBE.md)
- opencode's managed server resumes an interrupted turn at boot with a synthetic continue message and a model step
  that has no prompt hook; the context hook still fires. The service opens a recovery turn on a context call with
  no preceding prompt and ingests the resumed reply.
- Events are tied to opencode message ids, not to "the current turn".
- At service start, memory is reconciled against opencode's stored messages, so turns rolled back by memory
  recovery but kept by opencode are re-ingested.
- opencode's data home never lives inside `~/.personant/` (ARCHITECTURE.md "Single spelunking location"): crash
  recovery quarantines untracked files.

**Other lessons** (OPENCODE_PROBE_RESULTS.md §6)
- `fs.write` content comes from the `write` tool's input; its output is only a confirmation line (lesson 5).
- The plugin takes an event's sequence number as the event arrives, with forwarding chained (lesson 6).
- Commit dates are pinned to the memory system's clock wherever run-to-run repeatability is measured (lesson 7).

---

## Open

- **Topic tag:** keep the in-band tag (SPEC §5.1/§5.5; needs a stream abort/re-issue spike) or replace it with a
  recall tool (ACTIVE_DESIGN.md "Open decision").
- **Missing lineage under opencode:** no thread carries `derived_from` lineage through opencode, while in-process
  runs do. Cause unverified; candidates: the tool-delta ordering change (tool deltas after `user.prompt`,
  OPENCODE_PROBE_RESULTS.md §11) and the §5.5 mid-turn fetch not firing.
- **Plugin failure policy:** fail open (leave the hook input unmodified, ACTIVE_DESIGN.md §5) or fail closed
  (refuse the request). What opencode does when the `context` hook throws or the service refuses is unestablished
  (OPENCODE_RESUME_PROBE.md §6).
- **Restart persistence:** whether in-session history and the turn counter survive a restart; today only the
  working set reloads.
- **Resume cases not exercised** (OPENCODE_RESUME_PROBE.md §6): a graceful stop with a turn in flight; a
  standalone-kill claim later swept by a managed server; the resume budget, background-job, subagent and shell
  recovery; how a real model answers a resumed request.
- **Memory vetting details:** the default acceptance policy; the review rubric's exact content; which model runs
  the review (overlaps DOC_RECALIBRATION_PLAN.md U5); the re-review cadence.
- **Memory-native commands:** for each of `/topic*`, `/topics`, `/done`, `/closures`, `/pause`, `/resume`,
  `/back-to`, `/no-revisit`, `/keep`, `/threads`, `/spine`, `/index-rebuild`, `/project*`, `/stats` — keep as a
  plugin-registered command, make autonomic, or drop. Unverified: how the TUI shows a command whose `execute`
  sends no prompt, and how `execute` reports a result to the user.
- **Ack channel:** the recall offer, the closure review queue and the startup project prompts ask the user
  something. Plugins have only yes/no `permission.assert`; opencode's form service is not exposed to plugins
  (OPENCODE_INTEGRATION_OPTIONS.md §2.3). The alternative is handing the question to the model through the
  opencode-expectations slot and reading the answer next turn.
- **Fork, revert and sub-minds:** how a branch's work is incorporated or discarded.
- **Agents and skills:** whether and how opencode agent definitions and skills are supported (candidate mapping:
  agent definitions as Layer E directives scoped per agent; skill content as transient task-class).
- **`/compact` and `/clear`:** what they eventually map to; whether a TUI plugin can replace or disable them
  (OPENCODE_CONTEXT_INJECTIONS.md §6).
- **Concurrent sessions:** two opencode sessions against one memory (ARCHITECTURE.md "Concurrent sessions"; the
  plugin route makes it reachable).
- **Per-slot answers:** the budget, priority, overflow form and underusage of each slot, including the
  instructions and opencode-expectations slots.
- **Tool-definition slot:** built-in and MCP tool definitions are sent with every request and count against the
  token ceiling; OPENCODE_CONTEXT_INJECTIONS.md §5 marks them pass-through, curated per agent, with no budget or
  overflow form. They need a slot with the four answers; candidate overflow forms are omitting rarely used tools
  from a request or listing them by name with definitions on demand. MCP tools are recognisable by the `<server>_`
  name prefix.
- **MCP prompt expansions** cannot be told apart from typed prompts in the hook input (OPENCODE_CONTEXT_INJECTIONS.md
  §2); they are handled as user input unless a detection route is found.
- **Injection-handling unknowns** (OPENCODE_CONTEXT_INJECTIONS.md §6): hook order after a plugin reload; how
  system-role messages are lowered on provider routes other than openai-chat.
- **Per-turn cost growth past sim-day 9:** DEFECTS.md D1 explains most of it; whether the memory system's cost keeps
  growing past day 9 after D1 is fixed is unmeasured (OPENCODE_PROBE_RESULTS.md §11).
- **Contradictions surfaced by the SPEC rewrite:**
  - Reconcile skips turns rolled back by retraction, but no record distinguishes a retraction rollback from a crash
    rollback (SPEC §4.3.3, §4.5.8 (10)).
  - §4.3.3's rollback restores run-volatile state while restart persistence of that state is open (U9).
  - `session.started active=` is logged per service run, but the active project is per opencode session; ambiguous
    with concurrent sessions.
  - The stable-prefix claim for fixed slots no longer covers the opencode-expectations slot, now classed dynamic.
  - SPEC §3.0.1 requires `edit` to yield post-edit file bytes as an `fs.write` delta; no probe established that
    opencode's `edit` exposes them.
- **Gaps surfaced by the ARCHITECTURE rewrite:**
  - Production source of tool-call order: the probe took order from ids its mock assigned (`call_<step>_<index>`);
    real provider tool-call ids carry no index. Where the service gets call order in production is unestablished.
  - The probe service maps only `read`, `write` and a shell `git commit`; `edit` and generic `tool.result` are
    unmapped, and the probe stored `read`'s formatted output where SPEC §3.9.1 requires file bytes.
  - SPEC §4.1 keeps `search`, `deps` and `log` CLI verbs that `cmd/` does not implement.
  - `internal/store/paths.go` still places `config.toml` and `providers.toml` under the home, against SPEC §2.1.
  - "The LLM never touches the home's git tree" cannot be enforced under opencode, whose shell reaches it.
- **Probe code worth keeping on the main line:** the sim baseline comparator (`sim-baseline`, `sim-fidelity`,
  committed baselines) and `model.MockServer`.
- **CONVENTIONS.md rule proposed, not adopted:** never place external state (an agent's data home, telemetry)
  inside `~/.personant/` or a test home; crash recovery quarantines untracked files.
