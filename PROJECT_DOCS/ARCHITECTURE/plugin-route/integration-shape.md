### Integration shape

Each element names the opencode mechanism it rides on (IO §2.3 unless noted).

- **Composition: the `session.context` hook.** It fires on every primary step and every retry;
  `system`, `messages`, `tools` and `options` are all mutable. The plugin sends the hook input to
  the service and replaces `system` and `messages` with the answer. The service builds the answer
  as `turn.ComposeRequest` does: the composed working set as the system prompt, the bounded history
  tail, the current user input and this turn's tool messages. Tool-call/tool-result pairs in the
  returned `messages` stay intact (IO §4, item 4). Any other model call is kept off the provider:
  the `session.title` and `session.compaction` hooks supply `result`, which skips the model call.
- **Wire shape.** A composed request carries one non-empty system part, no empty parts and no
  mid-conversation system messages. Under that shape prompt text reaches the model byte-exact
  (PP §5; PR §6, lesson 8). The hook also resets `options.maxTokens`: opencode fits it to its own
  estimate of its own history, which is too low for a smaller composed request (CI §3, A17).
- **Compaction: `compaction.auto: false` plus plugin-answered checkpoints.** With auto compaction
  off only manual compaction runs, overflow included. opencode reads stored history from the latest
  compaction onward on every step (IO §2.4), so the service schedules checkpoint compactions at a
  turn cadence to keep that read bounded (**Open:** the cadence parameter's name and default; the
  probe takes it as a constructor argument, `-sim.checkpoint-cadence`). The plugin enqueues one
  with `session.compact` and the `session.compaction` hook answers with `result`. The checkpoint's
  summary is a fixed placeholder that the `context` hook discards on replay. A checkpoint is a read
  bound, not a memory mechanism. `experimental.idle` only decides whether a checkpoint is due: it
  fires before the turn's final step event and again after a checkpoint, so it never closes a turn
  (PR §6, lessons 1–2).
- **`/compact` and `/clear`** (CI §4). A user `/compact` cannot be refused; it is answered like a
  checkpoint, and `session.compaction.started` carries a `reason` that separates the two. `/clear`
  is client-side only; it discards nothing because memory is not keyed on the session. This is how
  SPEC §1.1 holds. **Open:** what the two commands eventually map to, and whether a TUI plugin can
  replace or disable them (CI §6).
- **Tools.** Any memory tool registers through `tool.transform`; a tool invented inside the
  `context` hook is dropped.
- **Config.** `personant init` writes the settings opencode must hold (`compaction.auto: false`,
  the plugin entry) into opencode's own config in opencode's own format. Personant-only settings
  live in `~/.config/personant/` and never overlap opencode's (see "Single spelunking location").
- **Code Mode and plugins.** opencode's own server runs, so its JavaScript plugins and Code Mode
  work unchanged. `ROADMAP_PLANS/code-mode-support.md` applies only to the fallback route.
- **Sessions and projects.** The project resolves from opencode's session directory through the
  bootstrap waterfall (see "Project identity"). A session is a window into the one memory; turns
  route to threads by topic, never by session; deleting a session does not touch memory
  (SPEC §1.1).
- **Memory-native commands.** A command that survives is a plugin-registered command
  (`CommandDomain.transform` → `CommandEditor.add`, whose `execute` runs server-side code), not a
  `commands/*.md` file, which only expands into a prompt to the model that cannot be told apart
  from a typed one (CI §2).

**Open:** U4 — which memory-native commands survive (`/topic*`, `/topics`, `/done`, `/closures`,
`/pause`, `/resume`, `/back-to`, `/no-revisit`, `/keep`, `/threads`, `/spine`, `/index-rebuild`,
`/project*`, `/stats`): each is a plugin-registered command, made autonomic, or dropped. Unverified:
how the TUI shows a command whose `execute` sends no prompt, and how `execute` reports a result.

**Open:** U4 — the ack channel. The recall offer, the closure review queue and the startup project
prompts ask the user something. A plugin has only yes/no `permission.assert`; opencode's form
service is not exposed to plugins (IO §2.3). The alternative is to hand the question to the model
through the opencode-expectations slot and read the answer on the next turn.

