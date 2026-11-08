## Invariants (the failure modes they prevent)

1. **Remove an import edge before the package it points at.** `internal/turn` imports
   `internal/tools` in production code (`toolloop.go`, `turn.go`), so deleting `tools` first
   breaks the runtime. Every package leaves `just build` green.
2. **No transition scaffolding** (CONVENTIONS "Pre-production: no transition scaffolding"). No
   test-only seam keeps a resolver or ack mode alive, no flag restores the old flow, and no warning
   names the retired home files. The harness takes the runtime's autonomic behaviour; it does not
   script around it.
3. **One stored-format change, gated.** WP9 drops three `meta.json` fields and bumps the home
   format with a restamp-only step: older `meta.json` files keep the fields, and the decoder
   ignores them. Nothing else stored changes. The thread-state enum keeps every value: SPEC §3.8.3
   recovery still writes `wip`, and stored threads carry the rest. `meta.json` `last_active`
   stays. Any further change that would make a stored file read differently is reported as a
   finding, not made.
4. **Test infrastructure never becomes product configuration.** Live endpoints come from
   `internal/testsupport`, the incumbent that already resolves `reaper` with a
   `PERSONANT_REAPER_URL` override. The HTTP client stays in `internal/model` as the in-process
   runtime's and the live tests' client. `./cmd` does not link it (criterion 3).
5. **One write path each.** An autonomic recall fetch goes through `acceptRecallCandidate` →
   `fetchThroughChain`, the SPEC §3.0.5 chokepoint. An autonomic closure goes through the single
   closure write that `applyClosureResolution` performs today.
6. **The harness observes effects, never internals** (ARCHITECTURE "Harness ↔ system-under-test
   coupling rule"). The sim's runtime mirror learns which threads recall fetched from the step's
   observable output: `recall.accept` lines scraped into `StepFeedback`, the same way
   `RecallMatchFireIDs` is filled. It never calls `autoAccepts` and never copies a threshold.
7. **Removal is proven by machine, not by reading.** Every package's done-line carries the grep
   that must come back empty, or list only the hits it names.

Left to the implementer: internal helper layout inside `internal/turn` after the deletions; the
names of the `StepFeedback` field and of the reshaped closure write; the wording of log details
within the event vocabulary that section 2 fixes.

