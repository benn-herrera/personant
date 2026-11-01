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

**Diagnosis** (measured 2026-11-01 at commit `06ee2e2` plus `internal/autogit/turn_commit_bench_test.go`;
mock inference, seed 24095, Apple M3 Max):

- **Inputs**, read from the two homes above with read-only `git --git-dir=<home>/.git-daily
  log | diff-tree -r | ls-tree -r HEAD` and `git --git-dir=<home>/.git ls-tree -r <day commit>`:
  - k, files per turn commit: 4 in 725, 5 in 281 and 6 in 14 of 1,020 (9-day); 304 / 118 / 6 of 428
    (30-day). Every turn commit modifies `spine.jsonl`, the day's `logs/<date>.log` and one or more
    `thread.md`, and adds one `turns/<n>.md`.
  - Bytes per written file (last 50 turn commits, 9-day): log 611 KB, `spine.jsonl` 128 KB, `thread.md`
    5.8 KB, excerpt 269 B; `files.json` in 5 of 50 at 46 KB mean (30-day: 4 of 50, 3.0 MB mean, 11.9 MB
    max). Log and spine are re-hashed and stored whole on every turn.
  - N grows about 1,000 per working day: 1,122 at the day-1 primary commit, 6,003 at day 8 and 7,201
    in the final daily index (9-day); 14,921 at day 24 (30-day). The growth is `threads/*/turns/*`
    excerpts, one per turn: 5,660 of 6,003 (9-day day 8), 13,473 of 14,921 (30-day day 24).
  - Excerpts concentrate in one thread: `thr_2/turns/` holds 4,006 of 6,772 (9-day) and 10,832 of
    13,901 (30-day); the median live thread holds 6. Half the turn commits add to `thr_2` (510 of
    1,020; 218 of 428).
  - Daily index file: 689,000 B at N = 7,201; 1,580,840 B at N = 15,375.
  - The 30-day daily index tracks 1,174 entries under `opencode/`.
- **Commit sub-operations**, `just bench ./internal/autogit BenchmarkTurnCommit` (synthetic daily repo
  in the home layout, k = 4, packed baseline; ms per turn):

  | N | index load | index save | `AddPaths` | `Commit` | both | single-load stage + `Commit` | `Commit`, turn in a short thread |
  |---|---|---|---|---|---|---|---|
  | 1,000 | 0.68 | 0.79 | 15.6 | 5.2 | 20.8 | 10.8 + 5.3 = 16.1 | 5.4 |
  | 4,000 | 2.5 | 2.5 | 29.9 | 10.9 | 40.8 | 14.6 + 11.1 = 25.6 | 9.2 |
  | 16,000 | 9.9 | 9.6 | 88.0 | 31.2 | 119.2 | 28.7 + 30.9 = 59.6 | 23.3 |

  - Each index load and each save is linear in N at about 0.6 µs per entry. `AddPaths` is
    k × (load + save) plus about 10 ms of blob writes that do not vary with N. Single-load staging is one
    load, one save and the same blob writes.
  - `AddPaths` + `Commit` is linear in N at about 6.6 µs per entry over a 14 ms floor; single-load
    staging gives about 2.9 µs per entry. At N = 7,200 the fit gives 61 ms; the 9-day run's last-eighth
    `turn_commit_ms` is 58.6 ms.
  - `Commit` is one index load plus the tree build. A turn written to a short thread instead of the
    wide `thr_main/turns/` costs 7.9 ms less at N = 16,000: that changed tree is re-encoded and stored
    as a new loose object every turn.
- **`wt.Commit` split**, CPU profile at N = 16,000 (`just bench-profile ./internal/autogit
  'BenchmarkTurnCommit/addpaths/N=16000$' commit16k 15s`, then `just pprof -top -cum
  -focus='autogit\.Commit$' <profile>`; taken while a sim run shared the machine): index decode 39%;
  `buildTreeHelper.BuildTree` 59%, made of object writes (`SetEncodedObject`, mostly zlib) 40%,
  `HasEncodedObject` 17% and the in-memory tree build (`commitIndexEntry`) 2%; pack-index load 4%;
  commit object and ref update under 3%.
- **No cache tree.** In go-git v5.19.2 `BuildTree` (`worktree_commit.go:206-218`) rebuilds every tree
  from every index entry on each commit, and `copyTreeToStorageRecursive` (`:268-296`) encodes and
  hashes every tree, writing only those whose hash is absent (`:292`). The decoder reads a `TREE`
  extension into `idx.Cache` (`plumbing/format/index/decoder.go:287-289`), nothing on the commit path
  reads it, and the encoder writes no extensions (`plumbing/format/index/encoder.go:43-62`), so the
  first `SetIndex` drops one. `Index.Entry` is a linear scan (`plumbing/format/index/index.go:73-82`).
- **Live attribution**, CPU profiles from `just DURATION=1d sim-profile`, `8d` and `9d`. Day 9 is the
  9d profile minus the 8d one (`just pprof -top -cum -focus='turn\.RunWithInfo$' -diff_base=<8d>
  <9d>`); the runs' per-day turn counts match, so the 8d run is a prefix of the 9d run. The profiled 9d
  run reproduces the symptom: its last 850 turns average 126.5 ms `turn_duration_ms` and 57.6 ms
  `turn_commit_ms`. CPU ms per turn, day 1 (1,015 turns) and day 9 (1,020 turns):

  | function | day 1 | day 9 | growth |
  |---|---|---|---|
  | `turn.RunWithInfo` | 20.7 | 83.3 | +62.6 |
  | `CommitTurn` | 10.5 | 50.0 | +39.5 |
  | — `IndexStorage.Index` (4 in `AddPaths`, 1 in `Commit`) | 2.2 | 18.6 | +16.5 |
  | — `IndexStorage.SetIndex` (4 in `AddPaths`) | 1.9 | 18.0 | +16.1 |
  | — `buildTreeHelper.BuildTree` | 3.0 | 9.0 | +5.9 |
  | — of which `HasEncodedObject` | 2.2 | 6.4 | +4.2 |
  | — blob writes (`copyFileToStorage`) | 2.6 | 5.4 | +2.8 |
  | `MaybeGC` | 0.76 | 3.4 | +2.6 |
  | — `RepackObjects` | 0.65 | 3.0 | +2.4 |
  | `ComposeWorkingSet` | 5.0 | 15.2 | +10.2 |
  | — `ReadThreadBody` | 4.7 | 12.3 | +7.6 |
  | — of which `turnFileNumbers` | 1.0 | 5.3 | +4.3 |
  | — `loadTrackedFiles`, `dedup.(*Chain).LiveWindow` | < 0.2 each | 1.9 each | +1.9 each |
  | `closeTurnAndUpdateEngagement` | 2.5 | 7.9 | +5.4 |
  | — `store.ReadSpine` (whole turn) | 1.5 | 6.6 | +5.1 |
  | `surfaceRecallCandidates` | 0.50 | 3.1 | +2.6 |
  | `MaybeDayBarrier` | 0.38 | 2.2 | +1.8 |
  | `ReadDebtWindowExcerpts` | 0.20 | 1.7 | +1.5 |

  - Commit growth is the index round trip: load and save add 32.6 of the 39.5 ms.
  - The remainder (`RunWithInfo` less `CommitTurn` and `MaybeGC`) is 9.4 → 29.8 ms, +20.4 ms,
    which matches the ~21 ms unattributed growth above. `ComposeWorkingSet` adds 10.2, spine reads
    and writes in `closeTurnAndUpdateEngagement` 5.4, recall surfacing 2.6 and the day barrier 1.8.
    `turnFileNumbers` is confirmed: its `os.ReadDir` goes 0.83 → 3.99 ms per turn under
    `ReadThreadBody` and 0.20 → 1.35 ms under `ReadDebtWindowExcerpts` (`just pprof -peek
    'store\.turnFileNumbers$' -focus='turn\.RunWithInfo$' …`). The spine grew from 61 to 256 records
    over the 9 days; `ReadSpine` is called from `FindSpineRecord`, `UpdateSpineRecord`,
    `SpineRecordsByProject` and `ListThreads`.
  - Outside `RunWithInfo`, the harness invariant `VerifyNoBudgetOverflow` calls `ComposeWorkingSet`
    again per step (13.6 s on day 9); it is not inside `turn_duration_ms`.
- **GC**, from the profiled 9d run's `turn_gc_ms` series (first and last eighth, 977 turns each; script
  over the metrics blob): repacks 1 → 2 per eighth, mean repack 1,509 → 3,834 ms; the every-64th-commit
  loose-object count costs 0.09 ms per turn in both. In the day-9 profile the daily repack is 2.3 s and
  the primary repack 1.1 s (`fileadapter_archive.go:170` and `:176`); on day 1 the primary repack is
  0.01 s.
- **Not established.**
  - Profiles sample CPU only (51% of wall on day 1); fsync and other off-CPU time are not in them, and
    darwin samples land mostly on `syscall.rawsyscalln`, so self time is unusable and only cumulative
    time by caller is reported. The wall-clock split per sub-operation is the benchmark's.
  - Why `ReadThreadBody`'s excerpt reads (`os.ReadFile`) grow from 3.8 to 8.2 ms per turn while the
    assembly window is bounded: call counts were not measured.
  - Why `HasEncodedObject` grows from 2.2 to 6.4 ms: tree count (about two per live thread) and the
    loose-object fan-out size were not separated.
  - The 30-day run was not profiled; its decomposition is the benchmark extrapolated to N = 15,375.

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

**Where.** `newHarness`; surfaced by the resume probe (OPENCODE_RESUME_PROBE.md).

---

## Documentation errata

Factual errors in the opencode reports (v2.2.2/v2.2.3), found by later measurement (OPENCODE_PREPROBE_RESULTS.md
§2):
- OPENCODE_INTEGRATION_OPTIONS.md §2.3 lists `session.usage.recorded` as an event plugins can use; it is not
  subscribable. The usable event is `session.usage.updated`.
- OPENCODE_INTEGRATION_OPTIONS.md §2.6 names the provider config key `provider.<id>`; the key is `providers`.
- OPENCODE_SERVER_SURVEY.md lists `session.status` and `session.idle` events; nothing publishes them.
