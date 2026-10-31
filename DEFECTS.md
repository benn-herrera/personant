# DEFECTS – Personant

Known defects in the memory system and its harness, not yet fixed. Each entry states the fact and where it
lives; evidence is dated.

---

## D1. Per-turn commit cost grows with the number of tracked files

**Fact.** The cost of committing a turn grows with the total number of files tracked in the daily git
repository, so per-turn cost rises steadily as the store fills. This contradicts SPEC.md §3.11 ("O(write-set),
no tree walk … so per-turn cost stays flat"), §9's requirement that per-operation latency stay flat over 120
days, and §2.3 ("per-turn write cost does not grow with thread length") for the commit step.

**Where.**
- `CommitTurn` (`internal/memops/fileadapter/fileadapter_recovery.go`) stages the turn's write set through
  `autogit.AddPaths` (`internal/autogit/autogit.go`), which calls go-git `Worktree.AddWithOptions` once per path
  with `SkipStatus`.
- In go-git v5.19.2 each such call decodes the whole index and re-encodes and rewrites it, and `Worktree.Commit`
  decodes it again and builds the tree from every entry — per turn O(k·N), k = paths written this turn,
  N = tracked files.
- N grows without bound: each daily repository starts from a full copy of the tree, and turn-excerpt files under
  a live thread's `turns/` are never removed (archival removes only retired threads).

**Evidence** (measured 2026-10-30 at commit `2864dbe`; mock inference, seed 24095, in-process, no opencode):
- 9-day run `test/rundata/sim-workload-seed24095-dur216h0m0s.261030144050`, first vs last eighth of turns:

  | | first eighth | last eighth |
  |---|---|---|
  | `turn_duration_ms` | 58.5 ms | 130.5 ms |
  | `turn_commit_ms` | 13.3 ms | 58.6 ms |
  | `turn_gc_ms` | 1.7 ms | 7.7 ms |
  | `turn_journal_ms` | 9.2 ms | 9.1 ms |
  | remainder | 34.2 ms | 55.1 ms |

- The spine is bounded (archival holds `spine_size` between `ArchiveLowWater` and `ArchiveHighWater`); cost
  tracks cumulative threads created, not spine size.
- 30-day run with opencode in the loop, stopped at sim-day 24
  (`test/rundata/sim-workload-seed24095-dur720h0m0s.261030173227`): mean per-turn cost 88 ms on day 1, 341 ms
  on day 24; the daily repository index held 15,375 entries. opencode added a constant ~1.45× factor; the growth
  is memory-side.
- The remaining ~21 ms of non-commit growth in the 9-day run is unattributed. One candidate, unmeasured:
  `turnFileNumbers` (`internal/store/thread_io.go`) lists a thread's whole `turns/` directory on every
  `ReadThreadBody`.

---

## D2. `recall_query_latency_*` does not measure recall

**Fact.** The sim's `recall_query_latency_p50/p95/p99` gauges are copies of the turn-duration percentiles, not
recall-query timings.

**Where.** `metricRecallQueryLatencyP50/P95/P99` in `internal/scenarios/sim/workload.go`, populated in
`internal/scenarios/sim/telemetry.go`.

---

## D3. `TestLoadConfigFixture` expects a stale model name

**Fact.** `internal/store/config_test.go` `TestLoadConfigFixture` expects `Chat.DefaultModel ==
"dummy-router/gemma-main"`; the committed fixture `test/config.toml` sets `dummy-router/gemma-26B-A4B-it`
(changed in `f97a5e3`, "model name normalization"). `just test` fails on it. The test is the stale side.

---

## D4. The scenario harness never runs the startup reconcile

**Fact.** `newHarness` (`internal/scenarios/harness_setup.go`) does not run the startup reconcile production
runs, so a harness home's files stay uncommitted and crash recovery quarantines them. Any crash scenario run
through the shared harness loses its uncommitted files.

**Where.** `newHarness`; surfaced by the resume probe (OPENCODE_RESUME_PROBE.md).

---

## Documentation errata

Factual errors in the opencode reports (v2.2.2/v2.2.3), found by later measurement (OPENCODE_PREPROBE_RESULTS.md
§2):
- OPENCODE_INTEGRATION_OPTIONS.md §2.3 lists `session.usage.recorded` as an event plugins can use; it is not
  subscribable. The usable event is `session.usage.updated`.
- OPENCODE_INTEGRATION_OPTIONS.md §2.6 names the provider config key `provider.<id>`; the key is `providers`.
- OPENCODE_SERVER_SURVEY.md lists `session.status` and `session.idle` events; nothing publishes them.
