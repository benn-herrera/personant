### Integration shape

Each element names the opencode mechanism it rides on (integration-options report §2.3 unless
noted).

- **Composition: the `session.context` hook.** It fires on every primary step (a step of the main agent's model loop, not opencode's title or compaction call) and every retry;
  `system`, `messages`, `tools` and `options` are all mutable. The plugin sends the hook input to
  the service and replaces `system` and `messages` with the answer. The service builds the answer
  as `turn.ComposeRequest` does: the composed working set (SPEC §3.1) as the system prompt, the transcript
  tail (the newest messages of the service run's in-run history, bounded to 35% of the live-turn reserve; `boundHistoryTail`, `internal/turn/livebudget.go`), the current user input and this turn's tool messages. Tool-call/tool-result pairs in the
  returned `messages` stay intact (integration-options report §4, item 4). Any other model call is
  kept off the provider: the `session.title` and `session.compaction` hooks supply `result`, which
  skips the model call, so no model request reaches the provider uncomposed while the plugin answers its hooks (SPEC §1); on a hook failure the prototype fails open (see "Failure handling", U7).
- **Wire shape.** A composed request carries one non-empty system part, no empty parts and no
  mid-conversation system messages. Under that shape prompt text reaches the model byte-exact
  (pre-probe results §5; probe results §6, lesson 8). The hook also resets `options.maxTokens`:
  opencode fits it to its own estimate of its own message history, which is too low for a smaller
  composed request (context-injections report §3).
- **Compaction: `compaction.auto: false` plus plugin-answered checkpoint compactions.** With auto
  compaction off only manual compaction runs, overflow included. opencode reads stored message rows
  from the latest compaction onward on every step (integration-options report §2.4), so the service
  schedules checkpoint compactions at a turn cadence to keep that read bounded (**Open:** the
  cadence parameter's name and default; the prototype takes it as a constructor argument,
  `-sim.checkpoint-cadence`). The plugin enqueues one with `session.compact` and the
  `session.compaction` hook answers with `result`. The checkpoint compaction's summary is a fixed
  placeholder that the `context` hook discards on replay. A checkpoint compaction is a read bound,
  not a memory mechanism. `experimental.idle` (an opencode plugin hook, integration-options report §2.3) only decides whether a checkpoint compaction is due:
  it fires before the turn's final step event and again after one, so it never closes a turn
  (probe results §6, lessons 1–2).
- **`/compact` and `/clear`** (context-injections report §4). A user `/compact` cannot be refused;
  it is answered like a checkpoint compaction, and `session.compaction.started` carries a `reason`
  that separates the two. `/clear` is client-side only; it discards nothing because memory is not
  keyed on the session. This is how SPEC §1.1 holds. **Open:** what the two commands eventually map
  to, and whether a TUI plugin can replace or disable them (context-injections report §6).
- **Tools.** If U3 adds a recall tool, it registers through `tool.transform`; a tool invented inside the
  `context` hook is dropped.
- **No commands.** The plugin registers no command and asks the user nothing; memory work
  runs automatically (SPEC §4).
- **Config.** `personant init` writes the settings opencode must hold (`compaction.auto: false`,
  the plugin entry) into opencode's own config in opencode's own format. Personant-only settings
  live in `~/.config/personant/` and never overlap opencode's (see "One inspection location").
- **Code Mode and plugins.** opencode's own server runs, so its JavaScript plugins and Code Mode (SPEC §1)
  work unchanged.
- **Sessions and projects.** The project resolves from opencode's session directory alone (see
  "Project identity"). A session reads and writes the one memory; turns
  are assigned to threads, never to sessions; deleting a session does not touch memory
  (SPEC §1.1).

