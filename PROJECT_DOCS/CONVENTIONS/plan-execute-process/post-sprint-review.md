### Post-sprint review

Every activated plan — a plan whose packages are being dispatched —
wherever it lives, ends with a post-sprint review section holding the review, revision and
burndown steps (probe plans: "Probe work").
- Unless the plan names others, the reviewers are an architect and a coder, with THESIS.md,
  SPEC.md, ARCHITECTURE.md and CONVENTIONS.md as their pre-task reading. They do not get the
  plan: they review the changes against the standards and for fitness for purpose, the tests of
  all touched code included.
- The burndown list is written to a separate `<plan>_REVIEW_BURNDOWN.md` and gets a
  prompt-engineer review and revision pass before use.
- **The simulation harness (`internal/scenarios/`, which includes `internal/scenarios/sim/`)
  passes the gates production code passes:** the checkpoint gate, the plan-dispatchability review
  and the post-sprint review. No plan exempts harness changes from them (probe plans: "Probe
  work").

