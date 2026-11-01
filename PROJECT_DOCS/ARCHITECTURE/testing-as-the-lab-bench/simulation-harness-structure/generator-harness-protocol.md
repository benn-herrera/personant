#### Generator ↔ harness protocol

The `StepSource` interface is the only coupling between the on-demand workload generator and the
harness drive loop. The protocol is:

1. The generator emits `Step` structs carrying an **absolute `Step.At`** (the step's position on the
   simulated clock, derived from `SimClockStart`). This field is mandatory for sim steps;
   `stepSetClock` sets `pinnedClock = Step.At`.
2. After each turn, the harness passes `StepFeedback` back into `StepSource.Next`. `StepFeedback`
   carries: `RecallMatchFireIDs` / `EmbedMatchFireIDs` / `IntraMatchFireIDs` (what the runtime
   surfaced), `RuntimeLayerB` (the authoritative Layer-B membership for shadow cross-check),
   `TargetRecoverable` (archival-forgiveness predicate for probe scoring), and
   `RecallExpectedForgiven` (the forgiven expected count the hit/miss counter must use).
3. The **log-marker contract**: the harness scrapes specific runtime log strings
   (`spine.match-fire`, `spine.embed-match-fire`, `spine.intra-match-fire`, `thread.created`,
   `archive.archived`) as the measurement signal; these strings are pinned by contract tests so a
   rename breaks a test, not a measurement.

