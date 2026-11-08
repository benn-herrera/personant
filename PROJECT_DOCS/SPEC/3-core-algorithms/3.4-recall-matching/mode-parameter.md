#### Mode parameter

`recall.ack-mode: banded | always` ([§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)), default `banded` — the behavior
above. `always` asks about every surfaced candidate: one offer followed by a decline-reason
question, and nothing is fetched without an ack.

Every candidate the memory service touches is logged: `recall.accept` (with `ack=human|auto`, [§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md)) or
`recall.decline` (with a [§4.3](../../4-user-surface/4.3-decline-categorization/index.md) reason).

