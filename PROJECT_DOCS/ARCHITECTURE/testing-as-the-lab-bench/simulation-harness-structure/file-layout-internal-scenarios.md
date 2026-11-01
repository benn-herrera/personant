#### File layout — `internal/scenarios/`

| File | Responsibility |
|---|---|
| `harness.go` | Core types: `Step`, `Scenario`, `Harness`, `StepSource`, `StepFeedback`, `RecallAck`, `ClosureAck`, `RecallFidelityMode`. Narrow optional interfaces (`threadIndexer`, `cacheSweeper`, `treeRebuilder`, `intraDivergenceProbe`, `cosineOpsReporter`). The `sliceSource` shim that adapts a fixed `[]Step` to `StepSource`. `SimClockStart`, `SimDayLength`, `SimWorkdayStart`, `SimDayIndex`. |
| `harness_setup.go` | `newHarness` (per-scenario isolation, clock default, telemetry wiring) and `scriptedCurator`. |
| `harness_run.go` | `RunScenario`, `runStep` (the seven named phase helpers: `stepSetup` / `stepSetClock` / `stepExecTurn` / `stepScrapeEventLog` / `stepMeasureRecall` / `stepRecordMetrics` / `stepCloseDays`), `restartSession`, and the final-gauge helpers (`foldEventLines`, `peakHistorySymbols`). |
| `harness_resolvers.go` | Per-step resolver builders (`recallResolverFor`, `closureResolverFor`) and the invariant-cadence policy (`perStepInvariants`, `runInvariants`). |
| `recall_fidelity.go` | Recall-fidelity measurement (`recordRecallFidelity`), the incremental log-tailer (`logTailer`), and `classifyRecoverability`. |
| `invariants.go` | The invariant validator set (spine integrity, index drift, project refs, last-active validity, frontmatter-spine sync, engagement consistency, thread accounting, closed-thread consistency, archive resolvability, dedup consistency, budget overflow: the `Verify*` functions). `DefaultInvariants` = `cheapDefaultInvariants` (per-step: `VerifyLastActiveValid`, `VerifyNoBudgetOverflow`) + `heavyDefaultInvariants` (substrate-scale, day-cadenced on the sim). |
| `metric_keys.go` | Cross-seam metric-key registry (see "State-ownership map" below). |
| `sim/workload.go` | On-demand workload generator (`generator`, `GenerateWorkload`, `runWorkDay`/`runSession`/`runDayOff`). |
| `sim/campaign.go`, `sim/wander.go`, `sim/intra.go` | Specialized step-sequence builders for the campaign, within-thread wander, and intra-thread recall workload variants. |
| `sim/telemetry.go` | Per-day snapshot emission and the sim daily-record schema. |
| `sim/sim_test.go`, `sim/sim_rung_test.go` | `TestSim` (acceptance ladder entry point) and rung-by-rung regression guards. |
| `opencode/` | the opencode driver and executor (see "opencode-in-loop harness"). |

**State-ownership map — single source for every mutable harness value**

The "silent lie" failure mode (above) comes from two independent sources claiming the same value.
Each of the following is owned in exactly one place:

| Value | Owner | Rule |
|---|---|---|
| Simulated clock | `Harness.pinnedClock` — a **slaved copy** of `Step.At`. `stepSetClock` does `pinnedClock = Step.At`, never integrates a delta. One clock advance path; see SPEC §9.4.1 for the single-clock rationale. | Never set `pinnedClock` from a delta except as described for the execution-time refinement step (which normalises `At = pinnedClock + TimeDelta` before the set). |
| Day index | Derived: `SimDayIndex(pinnedClock)`. Never a parallel counter. | Any code that needs "which sim-day is this?" calls `SimDayIndex(h.pinnedClock)`. |
| Generator exec clock | `generator.execClock` — an exact generator-side **mirror** of `pinnedClock`, maintained by `reconcileAt`: every emitted step's `At` is re-anchored forward to it when execution-time refinement turns advanced the clock past the step's planned instant; an `At`-less refinement advances it by `TimeDelta`, mirroring the harness normalize. | The harness never reads it; it exists so the fix lives at the source (the generator) and `stepSetClock`'s non-monotonic warn tripwire stays a pure canary — mirror-drift is exactly what trips it, so the canary stays armed. |
| Cross-seam metric keys | `metric_keys.go` — the single const block. Harness writes them; sim reads them. | A key rename is one edit; a compile error if a reference is missed. Keys owned entirely inside `sim/` are NOT mirrored here. |
| Crash-scenario coverage | `crashScenarioCoverage` (`crash_matrix_test.go`) — the single greppable registry mapping each registered `crashpoint` name to its scenario. `TestCrashPointCoverageGate` enumerates `crashpoint.RegisteredNames()` against it. | A registered crashpoint with no scenario **fails the suite** — the mechanical gate that stops silent kill-point rot. The pre-crash `TurnOracle` is a local immutable snapshot, not shared mutable state, so it is deliberately not mapped here. |
| Archived thread set | `Harness.archivedThreadIDs` — **index-derived** via `refreshArchivedSet` from the canonical archive index (`store.LoadArchiveIndex`, entries with `RecoveredAt == ""`), reassigned wholesale; refreshed when a tailed batch carries an archive/recovery line (the change trigger, not the source of truth). | The archival-forgiveness filter in recall fidelity and `VerifyThreadAccounting` both read this set; a recovered thread drops out the instant its index entry is stamped, and a log-emission gap can no longer under-count it. |
| Created thread set | `Harness.createdThreadIDs` — folded incrementally from tailed `thread.created` log lines. | Same pattern as above. |
| W1 divergence tally | Accumulated directly in `runStep` (not read from `measure.Service` atomics post-run), so the tally survives `RestartSession`-driven Service churn. | A restart installs a fresh Service whose atomics start at zero; reading post-run would lose probes from prior sessions. |
| Layer-B shadow | Generator-owned. Cross-checked each step against `StepFeedback.RuntimeLayerB` in two directions, both ==0 gates on mock and live-embedding rungs and report-only under live inference: forward (`layerb_shadow_divergence`, runtime-resident absent from the shadow: LRU agreement) and reverse (`layerb_shadow_reverse_divergence`, checked against the runtime-mirror `rtActive`, which also replicates measurement-carrier displacement and §3.5 turn-idle decay closure). The declared exception to SPEC §9.4's event-log-only recall scoring. | The shadow LRU is the generator's oracle for expected-match sets; a divergence would corrupt recall measurements. |

