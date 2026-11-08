#### Per-turn opening-line contract

Exactly one `context.modified source=user.prompt` line per prompted
turn. Lines from the turn's preparation — the day rollover, staged-symbol expiry and deltas that arrive ahead of the prompt — precede it; every other line the turn produces follows it before the next turn's opening line.
Measurement segments turns on it, so it is never suppressed, reordered, or duplicated. A resumed
turn ([§3.0.4](../../3-core-algorithms/3.0-all-content-enters-through-one-pipeline-deltas/3.0.4-turn-boundaries-and-per-turn-engagement-coalescing.md)) has no user message and so no such line. **Open:** the opening line of a resumed turn
(resume design).


---

