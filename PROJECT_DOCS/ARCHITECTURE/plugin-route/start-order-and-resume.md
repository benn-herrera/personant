### Start order and resume

- **The memory service is listening before opencode starts,** including opencode's managed
  background server, which sweeps and resumes interrupted executions at boot (RP §§2, 5.3).
- **Recovery turn.** The managed server resumes an interrupted turn with a synthetic continue
  message and a model step that has no prompt hook; the `context` hook still fires (RP §2). The
  service opens a recovery turn on a context call with no open turn, derives the turn's suffix from
  the hook input or stored message IDs rather than from process memory, composes the request and
  ingests the resumed reply (SPEC §1.6, §4.5.8 (10)).
- **Reconcile at service start.** After startup recovery (SPEC §4.5.8), the service diffs
  opencode's stored messages — the durable record of what the user saw — against memory's last
  committed turn, and re-ingests each turn opencode completed that memory rolled back, or records
  it as lost (RP §5.3, item 2). **Open:** U8 — whether reconcile skips turns rolled back by
  retraction; no record distinguishes a retraction rollback from a crash rollback.
- **opencode's data home never lives inside `~/.personant/`.** Crash recovery quarantines untracked
  files there, which would include opencode's database (see "Single spelunking location").
- **Restart persistence.** Only the working set reloads (`working-set.json`). History and the turn
  counter, which drives checkpoint cadence, reset on every relaunch, so after a restart the TUI
  shows the conversation while the model does not (RP §5.3, item 6). **Open:** U9 — whether
  in-run history and the turn counter are persisted, rebuilt from opencode's store, or left to the
  threads.
- **Resume cases not exercised** (RP §6): a graceful stop with a turn in flight, a standalone-kill
  claim later swept by a managed server, resume budget, background-job, subagent and shell
  recovery, and how a real model answers a resumed request.

