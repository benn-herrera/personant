## 6. Post-sprint review

After C3:
1. **Review.** An architect and a coder review the changes, the tests of all touched code
   included. Their pre-task reading is THESIS.md, SPEC.md, ARCHITECTURE.md and CONVENTIONS.md, not
   this plan. The harness changes (`internal/scenarios/`) are reviewed like production code.
2. **Burndown.** Findings go to `ACTIVE_PLAN_REVIEW_BURNDOWN.md`. That file gets a prompt-engineer
   review and an architect revision before any fix is dispatched.
3. **Fixes.** The burndown items run as packages under the same gates; a final `just test` precedes
   the closing commit.
