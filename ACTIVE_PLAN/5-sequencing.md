## 5. Sequencing

Serial, one package at a time: WP1 → WP2 → WP3 → WP4 → WP5 → WP6 → WP7 → WP8 → WP9 → WP10.

- WP1 before WP3: `tidy-dependencies` exists before WP3 needs it.
- WP3 before WP4: WP3 deletes `toolloop_test.go`, one of WP4's callers.
- WP4 before WP5: WP5 deletes `memops.Provider`, the type of WP4's parameter.
- WP5 → WP6 → WP7: each `just sim` run is the next package's metrics baseline. WP6 is expected
  to leave closure counts unchanged, so WP7's diff isolates recall.
- WP7 after WP6: WP7 removes `ackKind`, which WP6 leaves for it, and renames `installAckPolicy`.
- WP8 after WP6 and WP7: the helpers it deletes lose their last callers there; its CONVENTIONS
  unused-number edit assumes WP7's.
- WP9 after WP8: both edit `memops.go` and `fileadapter.go`.
- Contract edits land in the package that motivates them (section 2). ROADMAP lands in WP10.

Mechanical: WP1, WP2, WP4, WP8, WP10. Expert: WP3, WP5, WP6, WP7, WP9.

WP3–WP10's contract rows are mapped onto the documents as rediced by the doc sweep.

| WP | State |
|---|---|
| WP1 | done |
| WP2 | done |
| WP3 | done |
| WP4 | done |
| WP5 | done |
| WP6 | not started |
| WP7 | not started |
| WP8 | not started |
| WP9 | not started |
| WP10 | not started |
| Post-sprint review | not started |

