# OPENCODE ROUTE DECISIONS – Personant

Decisions, established requirements and open questions from the opencode integration work that no contract
document (THESIS, SPEC, ARCHITECTURE, CONVENTIONS) holds yet. Input to recalibrating those documents. Where a
decision is already written in ACTIVE_DESIGN.md it is cited, not restated.

Sources: ACTIVE_DESIGN.md, OPENCODE_SERVER_SURVEY.md, OPENCODE_TUI_LAUNCH.md, OPENCODE_INTEGRATION_OPTIONS.md,
OPENCODE_PREPROBE_RESULTS.md, OPENCODE_PROBE_RESULTS.md, OPENCODE_RESUME_PROBE.md, OPENCODE_CONTEXT_INJECTIONS.md,
DEFECTS.md.

---

## Decided

**Route and scope**
- Personant runs under unmodified opencode as a server plugin plus an out-of-process Go memory service; the full
  Go server port is a parked fallback (ACTIVE_DESIGN.md BLUF; ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md).
- Personant solves context management only; everything else conforms to opencode without evaluation, and the
  boundary is as ACTIVE_DESIGN.md "Theme" states.
- opencode is the harness. Charm's Crush is not a candidate: it reads provider API keys from environment variables
  and runs its TUI in-process with its backend.
- The target opencode version is the installed release; the source clone is kept on that release's tag
  (ACTIVE_DESIGN.md).

**Behaviour under the plugin route**
- Every model request is composed by the memory service through the `session.context` hook; opencode's own
  automatic compaction is off and plugin-scheduled checkpoints only bound opencode's history reads
  (ACTIVE_DESIGN.md "Integration shape" §§1–2).
- A response's tool results are applied to memory in tool-call order, never arrival order: opencode runs one
  response's tool calls concurrently (OPENCODE_PROBE_RESULTS.md).
- Turn close keys on `session.execution.succeeded`; `experimental.idle` only decides checkpoints and can fire
  twice per turn (OPENCODE_PROBE_RESULTS.md).
- Model requests are counted at the provider side; `session.model.request` reports title and compaction kinds
  even when hooks answered them.
- The memory service starts and is listening before opencode starts.
- A crash of the plugin or memory service is a crash hazard to prevent, not a mode the design works around.
- MCP is supported in full: server instructions, tool definitions, tool and resource results, and prompts
  (OPENCODE_CONTEXT_INJECTIONS.md I2, M1, M6, R9, T1, T4).
- Code Mode and opencode's JavaScript plugins run unchanged under this route; ROADMAP_PLANS/code-mode-support.md
  applies only to the fallback.
- opencode config is read as opencode reads it; Personant-only settings live under `~/.config/personant/` with no
  overlap (ACTIVE_DESIGN.md §6). opencode database cleanup follows ACTIVE_DESIGN.md §7.

**Sessions, commands, layout**
- Project mapping, session-as-window, session deletion leaving memory untouched, and `/compact` / `/clear` as
  no-ops: ACTIVE_DESIGN.md §9.
- Mechanics of the no-ops (OPENCODE_CONTEXT_INJECTIONS.md): `/compact` cannot be refused; the plugin answers the
  `session.compaction` hook with a `result` so no model call is made, and `session.compaction.started` carries a
  `reason` that separates a user `/compact` from Personant's checkpoints. `/clear` is client-side only; it is a
  no-op because memory is not keyed on session.
- Every slot defines budget, internal priority, overflow form and underusage; underusage flows from fixed slots to
  dynamic ones and a lender reclaims first (ACTIVE_DESIGN.md §10).
- Fork and revert are to be designed together with sub-mind split and re-merge: both are decisions about
  incorporating a branch's work or not.

**Process**
- Probe work runs on a throwaway branch; system defects found there are recorded in DEFECTS.md, not fixed there.
- Probe runs use mock inference only.
- Coder packages run one at a time in the shared tree; no git worktrees.
- Probe plans are exempt from the checkpoint gate and the post-sprint review (CONVENTIONS.md).
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
  stored message id for history rows.
- History rows are typed by joining the hook input's message `id` against the plugin's session API; the hook input
  drops metadata on synthetic messages.
- Small opencode expectations (todo list, plan/agent mode, open question or permission, reminders still in force,
  the subagent sessionID needed to continue a child, background-job results) render as current state in a fixed
  slot, not as history.
- Evicted file content is replaced by a thunk that pins the version (content hash), points at stored bytes when the
  content cannot be reproduced by replay, and appears as a recognition line the model can expand.
- Composed requests use one non-empty system part, no empty parts and no mid-conversation system messages; under
  that shape prompt text reaches the model byte-exact (OPENCODE_PREPROBE_RESULTS.md §5).

**Resume and recovery** (OPENCODE_RESUME_PROBE.md)
- opencode's managed server resumes an interrupted turn at boot with a synthetic continue message and a model step
  that has no prompt hook; the context hook still fires. The service opens a recovery turn on a context call with
  no preceding prompt and ingests the resumed reply.
- Events are tied to opencode message ids, not to "the current turn".
- At service start, memory is reconciled against opencode's stored messages so turns rolled back by memory recovery
  but kept by opencode are re-ingested.
- opencode's data home never lives inside a Personant home: crash recovery quarantines untracked files.

**Other lessons** (OPENCODE_PROBE_RESULTS.md)
- `fs.write` content comes from the `write` tool's input; its output is only a confirmation line.
- The plugin takes an event's sequence number when the event arrives.
- Commit dates are pinned to the memory system's clock wherever run-to-run repeatability is measured.

---

## Open

- **Topic tag:** keep the in-band tag (SPEC §5.1/§5.5; needs a stream abort/re-issue spike) or replace it with a
  recall tool (ACTIVE_DESIGN.md "Open decision"). Under opencode no thread carries `derived_from` lineage while
  in-process runs do; the likely cause, unverified, is that the §5.5 mid-turn fetch cannot fire.
- **Restart persistence:** whether in-session history and the turn counter survive a restart; today only the
  working set reloads.
- **Fork, revert and sub-minds:** how a branch's work is incorporated or discarded.
- **Agents and skills:** whether and how opencode agent definitions and skills are supported (candidate mapping:
  agent definitions as Layer E directives scoped per agent; skill content as transient task-class).
- **`/compact` and `/clear`:** what they eventually map to.
- **Concurrent sessions:** two opencode sessions against one memory (ARCHITECTURE.md "Concurrent sessions"; the
  plugin route makes it reachable).
- **Per-slot answers:** the budget, priority, overflow form and underusage of each slot, including the new
  instructions and opencode-expectations slots.
- **Tool-definition slot:** built-in and MCP tool definitions are sent with every request and count against the
  token ceiling, but no layer holds them and OPENCODE_CONTEXT_INJECTIONS.md §5 classifies none of them. They need
  a slot with the four answers; candidate overflow forms are omitting rarely used tools from a request or listing
  them by name with definitions on demand. MCP tools are recognisable by the `<server>_` name prefix.
- **MCP prompt expansions** cannot be told apart from typed prompts in the hook input (OPENCODE_CONTEXT_INJECTIONS.md
  §2); they are handled as user input unless a detection route is found.
- **Probe code worth keeping on the main line:** the sim baseline comparator (`sim-baseline`, `sim-fidelity`,
  committed baselines) and `model.MockServer`.
- **SPEC outcome to state:** a session never requires `/clear` or `/compact`; neither command discards memory.
- **Rules proposed, not yet adopted:** CONVENTIONS.md — never place external state (an agent's data home,
  telemetry) inside a Personant home; `~/.claude/CLAUDE.md` — parallel coder dispatches never use git worktrees;
  relayed review findings are sorted by who acts.
