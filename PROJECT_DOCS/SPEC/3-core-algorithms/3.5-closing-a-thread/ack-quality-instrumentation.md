#### Ack-quality instrumentation

The indicator metric is **ack-edit-rate**: the
ack question offers an `[e]dit` choice ([§4.2](../../4-user-surface/4.2-session-commands/index.md) `/done`, the `/closures` drain), and `retire.ack` ([§2.8](../../2-data-model/2.8-log-event-format-logs-yyyy-mm-dd-log/index.md))
records `edited=yes|no` for EVERY acked outcome (close and wip) — `yes` only when the submitted
summary differs from the summarizer draft. `retire.ack` also carries `ack=human|auto`, and the rate is
computed over `ack=human` lines only (an auto-accept is unedited by construction and would drive the
rate toward zero). Auto-accepts are still recorded (`retire.ack ... ack=auto` plus
`retire.complete`). *Evaluating* the rate — distinguishing "drafts are perfect" from "the user
stopped reading", and judging the auto/exception split (a miscalibration shows as a zero human
edit-rate while auto-accepts are corrected by hand with `/back-to`) — needs a real human and is a
[§9.1](../../9-measurement-and-validation-regime/9.1-the-four-month-simulation-the-v0-5-0-acceptance-criterion/index.md) known unknown (accounting level 3: deferred to empirical human-use data) until then.

