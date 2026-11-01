### Fallback dissection (worst-case path)

Designed, not built: no package implements the dissector.

When budget pressure exceeds threshold AND normal retirement isn't catching up:

- Dissector LLM clusters the oldest poorly-threaded content into semantically related groups.
- Each cluster produces a thread file + spine entry, identical in shape to a normal retirement.
- Each cluster resolves through the closure path (**Open:** U4 — the channel for its interactive
  step).

Handles three failure modes with one mechanism: wandering user, single-thread overrun,
never-cleanly-threaded content. **Quality is best-effort by design** — graceful degradation under
stress, not catastrophic loss. (Spec §3.6.)

