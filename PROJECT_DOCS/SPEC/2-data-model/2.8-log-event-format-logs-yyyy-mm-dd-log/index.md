### 2.8 Log event format (`logs/YYYY-MM-DD.log`)

Format → ARCHITECTURE «Event log format» (line format, examples, event vocabulary and reserved
names). A person inspecting the log, and the measurement regime, rely on this: every decision the [§9](../../9-measurement-and-validation-regime/index.md) measurement regime and the feedback-directive-tuning loop need is recorded as
a plain-text, append-only, single-line event; no event carries multi-line content, and an event
absent from the vocabulary cannot be measured. Durable turn content is never in the log ([§2.3](../2.3-thread-file-format-threads-thr-id.md)).

Contents:

- [§2.8 Per-turn opening-line contract](per-turn-opening-line-contract.md)
