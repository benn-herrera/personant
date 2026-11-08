## Before a change

Read THESIS and ARCHITECTURE "Applying the thesis" before a non-trivial change to this
repository, and ARCHITECTURE "Storage non-negotiables" before touching storage, persistence or
derived files. Do not write the following changes; report them to whoever assigned the work, with the
row or clause they match, and continue only on that person's direction:

- a change that matches a row of ARCHITECTURE "Anti-patterns to push back on";
- a change that adds a path from a session's project work into behavior or process memory other
  than SPEC §1.8's single write path, or that promotes a single-project pattern into behavior
  memory (SPEC §1.8 "Feedback directive tuning").

When you propose a change or make a design decision, ask:

- Does it have a measurement story? (How will we know it worked?)
- Does it commit to a specific value, or set a provisional value to be calibrated?
- Does it preserve or invert a recurring pattern (ARCHITECTURE "Recurring patterns")?
- Does it create migration cost we can't recover from?
- Could deterministic code do this without involving the model?
- If the model is involved, is its role narrow and well-bounded?

Also:

- Bake metrics emission into new code from day one; bolting it on later is much more expensive.

