## Status

The coordinator fills the Evidence column (commit hash; run directory where a package runs a sim).

| Package | Rating | Depends on | Status | Evidence |
|---|---|---|---|---|
| WP0 Instrumentation and the "before" run | mechanical | — | not started | |
| WP1 Store: folio format, tier-transparent reads, settle | expert | WP0 | not started | |
| WP2 Home format 2 and the migration registry | mechanical | WP1 | not started | |
| WP3 `personant verify` folio checks | mechanical | WP1 | not started | |
| WP4 Barrier step B1b, crash matrix, contract documents | expert | WP2, WP3 | not started | |
| WP5 Measurement, DEFECTS.md D1, ROADMAP.md A11 text (not ticked) | expert | WP4 | not started | |

WP0–WP4 touch Go, so they run one at a time in the shared tree, in the order above
(`PROJECT_DOCS/CONVENTIONS/plan-execute-process/plan-work-rating-and-dispatch.md`). Each package ends
at a checkpoint: `just test` once, then a commit by the coordinator.

---

