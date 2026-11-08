### Start order and resume

- **The memory service is listening before opencode starts,** including opencode's managed
  background server, which sweeps and resumes interrupted executions at boot (resume probe §§2,
  5.3).
- **Resumed turn.** The managed server resumes an interrupted turn with a synthetic continue
  message and a model step that has no prompt hook; the `context` hook still fires (resume probe
  §2). The service opens a resumed turn on a context call with no open turn, derives the turn's
  suffix from the hook input or stored message IDs rather than from in-process state, composes the
  request and ingests the resumed reply (SPEC §1.6, §4.5.8 (10)).
- **Reconcile at service start.** After crash recovery (SPEC §4.5.8), the service diffs
  opencode's stored messages — the durable record of what the user saw — against memory's last
  committed turn, and re-ingests each turn opencode completed that memory rolled back, or records
  it as lost (resume probe §5.3, item 2). Reconcile must never re-ingest a turn rolled back by retraction; the record that distinguishes
  it from a crash rollback is not built (ROADMAP A8).
- **Checkpoint at a clean stop.** At a clean service-run stop the service takes a checkpoint (SPEC §3.11).
- **opencode's data home never lives inside the store directory** (default `~/.personant/`; see
  "Store directory layout"). Crash recovery quarantines untracked files there, which would include
  opencode's database (see "One inspection location").
- **Restart persistence.** Only the working set reloads (`working-set.json`). The transcript tail
  and the turn counter, which drives checkpoint-compaction cadence and turn-based decay, reset on every relaunch
  (resume probe §5.3, item 6). **Open:** U9 — whether the transcript tail and the turn counter are persisted, rebuilt from
  opencode's store, or left to the threads.
- **Resume cases not exercised** (resume probe §6) are listed in ROADMAP B9.

