#### File layout — `internal/scenarios/`

| File | Responsibility |
|---|---|
| `harness.go` | Core types: `Step`, `Scenario`, `Harness`, `StepSource`, `StepFeedback`, `RecallAck`, `ClosureAck`, `RecallFidelityMode`. Narrow optional interfaces (`threadIndexer`, `cacheSweeper`, `treeRebuilder`, `intraDivergenceProbe`, `cosineOpsReporter`). The `sliceSource` shim that adapts a fixed `[]Step` to `StepSource`. `SimClockStart`, `SimDayLength`, `SimWorkdayStart`, `SimDayIndex`. |
| `harness_setup.go` | `newHarness` (per-scenario isolation, clock default, telemetry wiring) and `scriptedCurator`. |
| `harness_run.go` | `RunScenario`, `runStep` (the seven named phase helpers: `stepSetup` / `stepSetClock` / `stepExecTurn` / `stepScrapeEventLog` / `stepMeasureRecall` / `stepRecordMetrics` / `stepCloseDays`), `restartSession`, and the final-gauge helpers (`foldEventLines`, `peakHistorySymbols`). |
| `harness_resolvers.go` | Per-step resolver builders (`recallResolverFor`, `closureResolverFor`) and the invariant-cadence policy (`perStepInvariants`, `runInvariants`). |
| `recall_fidelity.go` | Recall-fidelity measurement (`recordRecallFidelity`), the incremental log-tailer (`logTailer`), and `classifyRecoverability`. |
| `invariants.go` | The invariant validator set (thread registry integrity, index drift, project refs, last-active validity, frontmatter-registry sync, engagement consistency, thread accounting, closed-thread consistency, archive resolvability, dedup consistency, budget overflow: the `Verify*` functions). `DefaultInvariants` = `cheapDefaultInvariants` (per-step: `VerifyLastActiveValid`, `VerifyNoBudgetOverflow`) + `heavyDefaultInvariants` (store-scale: once per sim day and once at end of run). |
| `metric_keys.go` | Cross-seam metric-key table (see "State-ownership map" below). |
| `sim/workload.go` | On-demand workload generator (`generator`, `GenerateWorkload`, `runWorkDay`/`runSession`/`runDayOff`). |
| `sim/campaign.go`, `sim/wander.go`, `sim/intra.go` | Specialized step-sequence builders for the campaign, within-thread wander, and intra-thread recall workload variants. |
| `sim/telemetry.go` | Per-day snapshot emission and the sim daily-record schema. |
| `sim/sim_test.go`, `sim/sim_rung_test.go` | `TestSim` (entry point of the acceptance ladder, SPEC §9.1) and span-by-span regression guards. |
| `opencode/` | the prototype's opencode driver and executor (on `probe-path`; see "opencode-in-loop harness"). |

**State-ownership map — single source for every mutable harness value**

A seam failure (above) comes from two independent sources claiming the same value.
Each of the following is owned in exactly one place:

| Value | Owner | Rule |
|---|---|---|
| Simulated clock | `Harness.pinnedClock` — a **slaved copy** of `Step.At`. `stepSetClock` does `pinnedClock = Step.At`, never integrates a delta. One clock advance path; see SPEC §9.4.1 for the single-clock rationale. | Never set `pinnedClock` from a delta except as described for the execution-time refinement step (a refinement turn is one the generator injects after a recall step misses), which normalises `At = pinnedClock + TimeDelta` before the set. |
| Day index | Derived: `SimDayIndex(pinnedClock)`. Never a parallel counter. | Any code that needs "which sim-day is this?" calls `SimDayIndex(h.pinnedClock)`. |
| Generator exec clock | `generator.execClock` — an exact generator-side **mirror** of `pinnedClock`, maintained by `reconcileAt`: every emitted step's `At` is re-anchored forward to it when execution-time refinement turns advanced the clock past the step's planned instant; an `At`-less refinement advances it by `TimeDelta`, mirroring the harness normalize. | The harness never reads it; it exists so the fix lives at the source (the generator) and `stepSetClock`'s non-monotonic warning stays a pure indicator — mirror drift is exactly what triggers it, so the indicator stays armed. |
| Cross-seam metric keys | `metric_keys.go` — the single const block. Harness writes them; sim reads them. | A key rename is one edit; a compile error if a reference is missed. Keys owned entirely inside `sim/` are NOT mirrored here. |
| Crash-scenario coverage | `crashScenarioCoverage` (`crash_matrix_test.go`) — the single greppable table mapping each registered `crashpoint` name to its scenario. `TestCrashPointCoverageGate` enumerates `crashpoint.RegisteredNames()` against it. | A registered crashpoint with no scenario **fails the suite** — the mechanical check that stops silent kill-point rot. The `TurnOracle` is a local immutable pre-crash baseline, not shared mutable state, so it is deliberately not mapped here. |
| Archived thread set | `Harness.archivedThreadIDs` — **index-derived** via `refreshArchivedSet` from the authoritative archive index (`store.LoadArchiveIndex`, entries with `RecoveredAt == ""`), reassigned wholesale; refreshed when a tailed batch carries an archive or restore line (the change trigger, not the source of truth). | The archival-forgiveness filter in recall fidelity and `VerifyThreadAccounting` both read this set; a restored thread drops out the instant its index entry records the restore, and a log-emission gap can no longer under-count it. |
| Created thread set | `Harness.createdThreadIDs` — folded incrementally from tailed `thread.created` log lines. | Same pattern as above. |
| Recall-preservation divergence tally | Accumulated directly in `runStep` (not read from `measure.Service` atomics post-run), so the tally survives `RestartSession`-driven Service churn. | A restart installs a fresh Service whose atomics start at zero; reading post-run would lose measurement probes (a measurement probe is a scripted step with `ExpectedRecallMatches` ground truth) from prior sessions. |
| Active-set oracle | Generator-owned. Cross-checked each step against `StepFeedback.RuntimeLayerB` in two directions, both ==0 checks on mock and live-embedding run configurations and report-only under live inference: forward (`layerb_shadow_divergence`, service-resident threads absent from the oracle: LRU agreement) and reverse (`layerb_shadow_reverse_divergence`, checked against `rtActive` (the harness's mirror of the service's active set), which also replicates measurement-carrier displacement (a measurement carrier is a synthetic thread created to host a recall-measurement query, `sim/workload.go`) and §3.5 turn-idle decay-triggered closing). One of the two declared exceptions to SPEC §9.4's event-log-only recall scoring. | The oracle's LRU is the generator's basis for expected-match sets; a divergence would corrupt recall measurements. |

