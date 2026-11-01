#### Per-turn boundary contract

Exactly one `context.modified source=user.prompt` line per prompted
turn, and every other line a turn produces follows it before the next turn's boundary; measurement
segments turns on it, so it is never suppressed, reordered, or duplicated. A recovery turn ([§3.0.4](../../3-core-algorithms/3.0-context-modification-events-the-architectural-primitive/3.0.4-per-turn-coalescing-for-engagement.md))
has no prompt and so no such line. **Open:** the boundary line of a recovery turn (resume design).


---

