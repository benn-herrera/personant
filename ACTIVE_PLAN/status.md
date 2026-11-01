## Status

The coordinator updates Status at every transition (dispatched, edit gate passed, at checkpoint,
committed) and fills the Evidence column (commit hash; run directory where a package runs a sim).

| Package | Rating | Depends on | Status | Evidence |
|---|---|---|---|---|
| WP0 Instrumentation and the "before" run | mechanical | — | done | `1cecf0c`; before-run `test/rundata/sim-workload-seed24095-dur216h0m0s.261101065943` (day 7 is a 0-turn day: tc = ix = 0) |
| WP1 Store: folio format, tier-transparent reads, settle | expert | WP0 | done | `df11746` |
| WP2 Home format 2 and the migration registry | mechanical | WP1 | done | `c656f8d` |
| WP3 `personant verify` folio checks | mechanical | WP1 | done | `454bbbf` |
| WP4 Barrier step B1b, crash matrix, contract documents | expert | WP2, WP3 | done | `631cd48` |
| WP5 Measurement, DEFECTS.md D1, ROADMAP.md A11 text (not ticked) | expert | WP4 | done: commit flat, D1 removed; per-turn growth elsewhere → ROADMAP A11a | after-run `…dur216h0m0s.261101100435`; 120d stopped at day 54 `…dur2880h0m0s.261101104107` |

WP0–WP4 touch Go, so they run one at a time in the shared tree, in the order above
(`PROJECT_DOCS/CONVENTIONS/plan-execute-process/plan-work-rating-and-dispatch.md`). Each package ends
at a checkpoint: `just test` once, then a commit by the coordinator.

---

