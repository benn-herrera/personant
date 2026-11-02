## Before a change

Read THESIS and ARCHITECTURE "Applying the thesis" before a non-trivial change to this
repository, and ARCHITECTURE "Substrate non-negotiables" before touching storage, persistence or
derived files. Do not write the following changes; report them to whoever assigned the work, with the
row or clause they match, and continue only on that person's direction:

- a change that matches a row of ARCHITECTURE "Anti-patterns to push back on";
- a change that adds a path from a session's project work into behavior memory other than SPEC
  §1.8's single write path, or that promotes a single-project pattern (SPEC §1.8 "Directive
  accrual").

