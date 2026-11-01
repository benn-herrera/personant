## Post-sprint review

Run after WP5 lands (CONVENTIONS "Post-sprint review",
`PROJECT_DOCS/CONVENTIONS/plan-execute-process/post-sprint-review.md`).

1. **Review.** An architect and a coder, each with THESIS (`PROJECT_DOCS/entry-point.md`), SPEC
   (`PROJECT_DOCS/SPEC/entry-point.md`), ARCHITECTURE (`PROJECT_DOCS/ARCHITECTURE/entry-point.md`)
   and CONVENTIONS (`PROJECT_DOCS/CONVENTIONS/entry-point.md`) as pre-task reading, starting at each
   entry point, and without this plan, review the changes across WP0–WP5 against the standards and
   for fitness for purpose. That includes the tests of all touched code: `internal/store`,
   `internal/verify`, `internal/memops`, `internal/memops/fileadapter`, `internal/autogit`,
   `internal/scenarios`, `internal/scenarios/sim`, `internal/version`, `internal/turn`, `cmd`. The
   range is the commits from WP0 through WP5 (`git log` on this branch).
2. **Burndown.** The architect merges both reviews into `ACTIVE_PLAN_REVIEW_BURNDOWN.md`
   (severity, finding, location, guidance).
3. **Prompt-engineer pass.** The burndown gets a prompt-engineer review and an architect revision
   before any item is dispatched.
4. **Revision.** Burndown items are dispatched one Go package at a time, each with the edit gate
   and a final `just test` checkpoint.
5. **Close.** When the burndown is empty, the outcomes are already in SPEC and ARCHITECTURE (WP2,
   WP4), and this file is replaced by the next plan.
