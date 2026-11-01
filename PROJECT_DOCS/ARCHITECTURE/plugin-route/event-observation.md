### Event observation

Every context change reaches the §3.0 chain (see "The §3.0 hook chain") through one of these
opencode mechanisms:

| Context change | opencode mechanism |
|---|---|
| `user.prompt` | `session.prompt` hook, before the prompt is stored |
| `tool.result`, `fs.read`, `fs.write` | `tool.execute.after` |
| `fs.commit` | `tool.execute.after` of the shell tool, for a commit of a tracked path |
| `user.shell-capture` | `session.shell.ended` event (CI M9) |
| `model.response` | `session.text.ended` and `session.step.ended` events, ingested at turn close |
| turn close | `session.execution.succeeded` |
| turn abort | `session.execution.failed`, `session.execution.interrupted` |
| fork, revert, delete | `session.forked`, `session.revert.committed`, `session.deleted` events |

The service records which deltas it has applied per turn, so a missing one is detectable (the
`oc_missed_deltas` gate, see "opencode-in-loop harness").

The probe service maps only `read`, `write` and a shell `git commit` to deltas
(`internal/opencodehook/service.go`, `tool`); other tools' `tool.result` deltas are not mapped.

**Open:** `edit` — SPEC §3.0.1 requires the post-edit file bytes as an `fs.write` delta; no probe
established that opencode's `edit` exposes them, and the probe does not map `edit`.

**Open:** fork, revert and sub-minds — how a branch's work is incorporated or discarded (SPEC
§1.1), decided together. The events above are observed; what memory does on them is undecided.

**Open:** agents and skills — whether and how opencode agent definitions and skills are supported.
A candidate mapping: agent definitions as Layer E directives scoped per agent, skill content as
transient task-class.

**Open:** lineage — no thread carries `derived_from` through opencode, while in-process runs do
(PR §5). The cause is unverified; candidates are tool deltas now applying after `user.prompt` (PR
§11) and the mid-turn fetch not firing.

