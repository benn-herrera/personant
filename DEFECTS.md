# DEFECTS – Personant

Known defects in the memory system and its harness, not yet fixed. Each entry states the fact and where it
lives; evidence is dated.

---

## D2. `recall_query_latency_*` does not measure recall

**Fact.** The sim's `recall_query_latency_p50/p95/p99` gauges are copies of the turn-duration percentiles, not
recall-query timings.

**Where.** `metricRecallQueryLatencyP50/P95/P99` in `internal/scenarios/sim/workload.go`, populated in
`internal/scenarios/sim/telemetry.go`.

---

## D4. The scenario harness never runs the startup reconcile

**Fact.** `newHarness` (`internal/scenarios/harness_setup.go`) does not run the startup reconcile production
runs, so a harness home's files stay uncommitted and crash recovery quarantines them. Any crash scenario run
through the shared harness loses its uncommitted files.

**Where.** `newHarness`; surfaced by the resume probe (OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md).

---

## Documentation errata

Factual errors in the opencode reports (v2.2.2/v2.2.3), found by later measurement (OPENCODE_RESEARCH/OPENCODE_PREPROBE_RESULTS.md
§2):
- OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md §2.3 lists `session.usage.recorded` as an event plugins can use; it is not
  subscribable. The usable event is `session.usage.updated`.
- OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md §2.6 names the provider config key `provider.<id>`; the key is `providers`.
- OPENCODE_RESEARCH/OPENCODE_SERVER_SURVEY.md lists `session.status` and `session.idle` events; nothing publishes them.
