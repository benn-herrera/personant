# Sim time: one clock, everything derived

**Status:** design, pre-implementation — for adversarial seam review before any code.
**Supersedes the mechanism added by:** #108 (day-off `SleepCycle` marker), #120 (per-sim-day
series via `OnSimDayClose`), #121 (re-anchored daily clock). Those three composed the day-off
collapse described below; this is the de-fragmentation that retires the composition.

## 1. The defect, and its class

A live 120d run showed every weekly day-off **collapsing**: no 0-turn day-off record, the
following work day's turns mislabeled into the day-off's calendar slot, and the day numbering
silently compressing by one per week. The integrity canary (added mid-investigation) fired:

```
step 5825 ... SKIPPED-DAY-CLOSE integrity violation: clock 2026-05-08 12:13:30
is at-or-past nextHeavyAt 2026-05-08 12:00:00 after firing one day-close
expected exactly one 0-turn day (the day-off), got 0
```

Root cause is **not** the day-off marker, the daily series, or the re-anchor individually — each
is correct alone. It is a **multiple-source-of-truth antipattern** at the generator↔harness seam:

- The **generator** tracks "which day" as a hand-incremented counter `g.dayIndex` (mutated once
  in `generateNextDay`), with day starts jittered `N·24h ± 15min`.
- The **harness** has **no access** to that counter — the only thing crossing the seam is a
  `TimeDelta` duration per `Step`. So it **reconstructs a second, independent** notion of "which
  day" by integrating deltas onto `pinnedClock` and watching it cross a **rigid** `nextHeavyAt`
  grid at `N·24h`.

Two day-trackers, in two coordinate systems (jittered vs rigid), are guaranteed to drift by the
±15min jitter. On a normal day the first turn jumps only ~11h (< 24h) so it crosses ≤1 grid line
and the drift is invisible. The **day-off** is the one step whose follow-on jump is a full ~24h;
when the day-off's −15min jitter lands its anchor *before* the grid line, that single 24h jump
sails across **two** grid lines, and the one-close-per-step reconstruction skips one. All the
epicycles that accreted around this — drain loops, the canary, jitter-case analysis — are scaffolding
propping up a reconstruction **that should not exist**.

### Review-gap note
No prior architecture review covered this because the colliding pieces never coexisted at review
time: the sim-vs-reality MAD (#52/#78) predates #120/#121, and the day-close-on-grid seam was
assembled piecemeal by accreted edits afterward. This is the accreted-composition failure mode —
which is why this change is going through a review of the **assembled seam as a unit**, not another
point patch.

## 2. Principle: the clock is the only time primitive

> Timestamp is clock. Period. The clock changes; **every** other time-shaped value in the sim world
> is *derived* — current date/time from the clock value, elapsed time from clock deltas. No stored
> day counter, no rival grid.

```
clock : time.Time (UTC)                       ← THE primitive; the only thing that mutates
  ├─ now / date   = clock
  ├─ elapsed      = clock.Sub(clockStart)
  ├─ dayIndex     = int(clock.Sub(clockStart) / dayLength)     ← DERIVED (both sides, same fn)
  ├─ isDayOff     = clock.Weekday() == time.Sunday             ← DERIVED from the real calendar
  └─ done         = dayIndex >= int(Duration / dayLength)      ← DERIVED
```

Both the generator and the harness derive `dayIndex` with the same function — **but the keystone is
that they derive it from the same _value_, not just the same _formula_.** (Review B1: the harness
today *accumulates* `pinnedClock += Σ TimeDelta` while the generator *re-anchors* `simNow = dayStart(N)`;
"same formula, two clocks" is the original bug relocated.) The fix is §3.4: the harness clock is a
**slaved copy** of the generator's absolute re-anchored instant, not an independent integrator — so
there is genuinely one clock, and the harness's parallel grid is deleted. The day-close fires when
the *derived* `dayIndex` ticks.

## 3. Design decisions

### 3.1 UTC, anchored at a Monday midnight
Sim time is a `time.Time` in **UTC** — no DST, so every day is exactly 24h and `Sub()/24h` is exact
(in a DST zone a "day" is 23/25h and this breaks; a sim wants uniform days regardless). Absolute
clock control means we *choose* the anchor:

```
clockStart = time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)   // a MONDAY, 00:00
```

(Current anchor is `2026-05-01 12:00 UTC` — a **Friday at noon**, which is why grid lines sat at
12:00 and the day-off landed on an arbitrary weekday.) With a Monday anchor, `dayIndex 6` (the 7th
day) is **Sunday** — the weekend day-off falls out of the real calendar, deterministically.

### 3.2 `dayIndex` and `isDayOff` are derived, via stdlib
- `dayIndex := int(clock.Sub(clockStart) / dayLength)` — stdlib `Duration` division, exact in UTC.
- `isDayOff := clock.Weekday() == time.Sunday` — let `time.Weekday()` decide; **delete the
  `dayIndex % 7 == 6` modular arithmetic** entirely. (If we later want a 2-day weekend, it's
  `Saturday || Sunday` — still pure calendar, no counter.)
- Day advancement reads as calendar intent: `clockStart.AddDate(0, 0, n)` / fixed `24h` (identical
  in UTC).

Go's `time` has **no** single "elapsed whole days" call by design (Duration's largest unit is
`Hours()`); the correct primitive for fixed 24h days is `Sub()/24h` **in UTC**, plus `Weekday()`
for the calendar structure. We are not hand-rolling calendar math; we are using the stdlib API for
exactly what it is built to answer.

### 3.3 Work-start anchored at 8am, mid-bucket — the airtightness constraint
The buckets are **midnight-to-midnight** (`clockStart` is Monday 00:00, §3.1). The work day starts at
**08:00 ± 15min**, i.e. **8 hours** into its bucket:

```
workdayStart    = 8 * time.Hour                                   // morning start, mid-bucket
dayStart(N)     = clockStart + N·dayLength + workdayStart + jitter,  jitter ∈ [-15min, +15min]
```

The symmetric ±15min jitter is kept as-is — centered at 08:00, it lands the start in [07:45, 08:15],
**8h from either midnight edge** against a 15-minute fuzz, so a day can never spill into an adjacent
bucket. (The current bug is *not* the jitter: today `dayStart(N) = clockStart + N·24h` with
`clockStart` at **noon**, so the work-start sits *exactly on* the day-close boundary and ±15min
straddles it. Moving the start to 8am inside a midnight bucket — instead of onto the boundary —
removes the straddle without touching the jitter.) A work day then runs 08:00 → ~21:30 (~13.5h),
ending ~2.5h before midnight, comfortably inside bucket N. (Session-2 bound, resolves review m1:
session 1 ends ~14:00, +~1h jittered break → session 2 ~15:00–~21:00; worst case a long-tail session
+ high-jitter break still leaves ~2.5h margin to midnight. The injected zero-delta probe/main-thread
steps carry `TimeDelta = 0` / `At` = the current instant, so they cannot cross a boundary by
construction.) The clock advances monotonically through every bucket — including the off-day (Sunday)
bucket, where the `SleepCycle` marker dwells — and `floor(clock)` increments by exactly 1 per midnight
boundary. No single step ever crosses two boundaries.

### 3.4 One clock: the harness slaves to the generator's absolute instant (resolves review B1)
This is the keystone. Today the generator works in `time.Duration` offsets (`simNow`) and re-anchors
each day; the harness independently *accumulates* `pinnedClock += Σ TimeDelta`. Same derivation
formula applied to **two independently-evolved clocks** = the original dual-source bug, one level
down. Renaming the formula does not fix it.

**The fix — make the single-clock claim mechanically true, not asserted:**
- The generator holds `clockStart` and computes `simNow` as a UTC `time.Time`; `dayIndex`/`isDayOff`
  derive from it (§3.2), termination from the un-jittered offset (§5 / n2).
- Each emitted `Step` carries the **absolute simulated instant** of its turn (`Step.At time.Time`),
  not only the relative `TimeDelta`. The harness **sets** `pinnedClock = step.At` — it does **not**
  integrate a delta. `pinnedClock` becomes a *slaved copy* of the generator's one clock, so the two
  cannot diverge by construction (no independent accumulator to drift).
  - `TimeDelta` MAY remain on the `Step` for handwritten scenarios that advance relatively
    (`heavyCadence == 0`, no day-close), but for the sim path `At` is authoritative and the harness
    prefers it. A `Step` from the sim always carries `At`; a handwritten step without `At` keeps the
    legacy accumulate path. (Decide in review: dual-field vs. `At`-only with a helper that derives
    `At` from a running base for handwritten scenarios. Recommendation: `At`-only, with a thin
    `sliceSource` shim that stamps `At` from accumulated `TimeDelta` so handwritten scenarios are
    unchanged at their call sites — keeps one authoritative field on the wire.)
- **Delete the `td < 0 → 0` clamps** (`workload.go:1637 runDayOff`, `runSession`). A silent clamp is
  exactly the swallow that hides a re-anchor-before-prior-turn divergence (the original bug's shape).
  Replace with a hard assert `turnInstant >= simNow` (monotonic clock) — §3.3's 8am mid-bucket anchor
  makes re-anchor always land at-or-after the prior turn, so the assert is unreachable in correct
  operation and fires loudly if that ever breaks.

### 3.5 Harness day-close: driven by the derived `dayIndex`, grid deleted
- `simDayIndex(clock) := int(clock.Sub(clockStart) / dayLength)` is a single shared helper used by
  the generator, the harness, and tests — the one derivation, in one place.
- Remove `nextHeavyAt`-as-day-close. After a step sets `pinnedClock` (§3.4), compute
  `cur := simDayIndex(pinnedClock)`; while `lastClosedDay < cur`, fire one `onSimDayClose` per
  intervening day (`lastClosedDay++`). With §3.3 this fires exactly once per day in normal operation,
  but it is now **correct by construction** (day identity is unambiguous), not an epicycle fighting a
  coordinate mismatch.
- **Stamp = grid-exact, trigger = jittered crossing (resolves review M2).** The day-close date MUST
  be the canonical jitter-free instant `dayCloseDate(N) := clockStart.Add(N · dayLength)`, NOT the
  triggering `pinnedClock` (which jitters ±15min around the boundary). Under the midnight anchor a
  trigger-instant stamp would truncate to different calendar dates either side of midnight and break
  `sim_date` contiguity (`TestDayOffThroughHarness` asserts consecutive `sim_date`s are exactly 24h
  apart). Consecutive grid instants are exactly 24h apart by construction → contiguity holds. In the
  catch-up loop each intervening close `k` carries its own grid date `clockStart + k·dayLength`.
- The day-off `SleepCycle` step sets the clock into the Sunday bucket and fires that day's close with
  zero turns → the 0-turn day-off record falls out naturally.

**Heavy-invariant cadence folds onto the same tick (resolves review M1 — this is a prerequisite, not
an option).** `perStepInvariants` today is ONE coupled `if (!pinnedClock.Before(nextHeavyAt))` that
*simultaneously* fires the heavy invariant subset, stamps the day-close, and advances `nextHeavyAt`.
You cannot delete the grid for day-close while leaving it for heavy invariants — they are the same
statement. So the heavy invariants move onto the derived-day tick too: the catch-up loop fires the
heavy invariant subset **and** the day-close for each crossed day. Heavy invariants already fire
"once per sim-day" by intent (a long-sim throughput relaxation, not a grid-aligned semantic), so the
derived `dayIndex` increment is a strictly better trigger. One loop, one source, both consumers;
`nextHeavyAt` is deleted entirely.

## 4. What this deletes

- `g.dayIndex` manual counter → derived.
- The harness `nextHeavyAt`-for-day-close grid → gone.
- The drain-loop-as-fix, the OLD skipped-day-close canary ("a second cadence boundary was swallowed",
  `harness_run.go:432`), and all jitter-straddle case analysis → deleted; that failure mode is
  structurally gone with the slaved clock + grid-exact stamp.
- `dayIndex % 7 == 6` → `Weekday() == Sunday`.
- The `td < 0 → 0` clamps → hard `>= 0` asserts (§3.4).

**Replacement forensic asserts (resolves review m3) — cheap, non-load-bearing tripwires, not gates:**
- Generic harness: `step.At >= prior pinnedClock` (monotonic / non-negative advance) — catches the
  clamp-hiding-a-bug case from B1.
- Sim path only: no single *natural* (non-day-off) step advances `>= 2·dayLength` — the only
  legitimate ≥24h jump is the known single day-off. Routed through `internal/log`, forensic not fatal
  (the catch-up loop correctly handles N days; this is a "did the workload do something absurd"
  canary, not a correctness gate). NOTE the inversion from the old canary: ">1 day in one step" is no
  longer an error in general (it is the future vacation path) — the sim-side bound is workload-shape
  forensics, not a structural invariant.

## 5. Determinism
Day count stays a pure function of `Duration`: termination derives from the **un-jittered** offset
(`int(dayIndex·dayLength / dayLength)`, today's RNG-free `dayStartOffset` path — resolves review n2:
do NOT route termination through `simDayIndex(simNow)`, since `simNow` carries jitter and a
high-jitter final day could flip the count by one). With the 8am mid-bucket anchor the ±15min fuzz
never moves a day across a midnight edge, so `(Seed, Duration)` → identical step stream holds.

**Draw-order invariant (resolves review m4):** the single `g.rng.Float64()` jitter draw in `dayStart`
must remain the *only* per-day RNG draw and must occur at the same point in the per-day sequence; the
`+workdayStart` (8h) offset is RNG-free and added *after* the draw. `simNow` becoming `time.Time` and
`Step` gaining `At` must not add or reorder any draw. The existing byte-compare determinism test
(`drainSteps`, `sim_test.go:1271-1289`) is the gate — it catches any reorder.

## 6. Impacted code (concrete)
- `internal/scenarios/sim/workload.go`: `simNow` → `time.Time`; introduce `clockStart`; `dayStart`
  8am mid-bucket anchor; `dayStartOffset`/termination/`runDayOff`/`generateNextDay` derive `dayIndex`
  and `isDayOff` from the clock; drop `g.dayIndex++` and `%7`. Constants: keep `dayLength` and the
  symmetric `dayStartJitter` (±15min, unchanged); add `workdayStart = 8h`.
- `internal/scenarios/harness_setup.go`: `pinned` → `clockStart` (Monday **2026-05-04 00:00 UTC**);
  delete `nextHeavyAt` init entirely (both day-close AND heavy invariants fold onto the derived tick,
  §3.5/M1).
- `internal/scenarios/harness_run.go` / `harness_resolvers.go`: `runStep` **sets** `pinnedClock =
  step.At` (§3.4) instead of `+= TimeDelta`; replace the `perStepInvariants` `nextHeavyAt` branch and
  the `closeDayTail`/`advanceAndCloseDay`/old-canary scaffolding (the failed patch) with the single
  derived-`dayIndex` catch-up loop firing heavy invariants + day-close per crossed day, stamped with
  the grid-exact date (§3.5). Add the replacement forensic asserts (§4).
- `internal/scenarios/harness.go`: add `Step.At time.Time` (§3.4) + doc; `OnSimDayClose` doc; the
  `RunScenario`/`sliceSource` shim that stamps `At` for handwritten scenarios.
- Shared `simDayIndex(clock)` helper (one home, used everywhere).
- **`internal/scenarios/sim/sim_test.go` (resolves review m5 — a concrete migration miss):** the
  cadence helpers move with the anchor. `TestCadence_DayStartsNoPrecession` computes
  `anchor := N·dayLength` and asserts `drift` within `±dayStartJitter`; under the 8am offset the
  boundaries sit at `N·dayLength + workdayStart`, so this test **fails as written** and must re-anchor
  to `N·dayLength + workdayStart`. Also touch `cadenceDayStartThreshold`/`dayStartBoundaries`/
  `signedGridDrift`/`TestCadence_DayOffPreservesPhase`/`TestDailyStatsSeries`, and the
  `TestDayOffThroughHarness` replacement (§8). Re-validate the 5h boundary-detection threshold still
  sits cleanly between the new ~1h break and the new ~10.5h overnight (it does).
- **`SPEC.md` §9.4.1 (resolves review m6):** rewrite bullet 1 (Monday-midnight anchor + 8am work-start
  offset), bullet 4 (`Weekday()==Sunday` day-off, replacing "every 7th day / index 6"), and DELETE
  bullet 5's `is_day_off ? 24h : 0` hand-formulation (now automatic from `Weekday()`); keep the
  determinism bullet. Checkpoint requirement per AGENTS.md "Ensure Docs Stay Up To Date."

## 7. Review dispositions (RESOLVED — folded above)
The adversarial seam review returned **SOUND-WITH-NOTED-FIXES**; all open questions are now decided
and folded into §3–§6:
1. **Heavy-invariant cadence** → folds onto the derived-day tick (§3.5/M1). It was a *prerequisite*,
   not an option: heavy-invariant firing and day-close are the same `nextHeavyAt` `if`.
2. **Canary** → old "swallowed-boundary" canary deleted (its failure mode is structurally gone);
   replaced by cheap non-load-bearing forensic asserts — generic monotonic-advance + sim-side
   `<2·dayLength` workload-shape tripwire (§4/m3).
3. **`sim_date` stamp** → grid-exact `clockStart + N·dayLength`, decoupled from the jittered trigger
   instant, so contiguity holds under the midnight anchor (§3.5/M2). `TestDailyStatsSeries`'
   correctness invariant preserved; `finalize` uses `PinnedClock()` which lands ~21:30 on the last
   day — safely mid-date.
4. **`time.Time` vs `Duration`** → `simNow` becomes `time.Time`; `Step` carries absolute `At`; harness
   *slaves* to it (§3.4/B1). Draw-order invariant pinned (§5/m4); `drainSteps` byte-compare is the
   determinism gate.
5. **SPEC §9.4.1** → concrete bullet rewrite scoped in §6/m6.

Residual judgment calls the coder should confirm-in-PR (not blockers): `At`-only vs dual-field wire
format (recommendation: `At`-only with a `sliceSource` shim, §3.4); exact home of the `simDayIndex`
helper.

## 8. Test plan (edit-gate friendly)
- **Generator-level (fast, `make test-run`):** `dayIndex`/`isDayOff` derive correctly across a
  day-off and a week boundary; one-sided jitter never crosses a bucket edge (assert
  `dayStart(N) ∈ [N·24h, N·24h+span)`); day count = `int(Duration/dayLength)` exactly; `(Seed,
  Duration)` determinism preserved.
- **Harness-level (the layer the generator-only guards missed):** drive a ≥9-day run through the
  harness; assert one day-close per calendar day in order, contiguous `sim_date`, the Sunday day-off
  is the single 0-turn record, clock advances exactly 24h across it, and the demoted sanity assert
  (if kept) never fires. This is the `TestDayOffThroughHarness` replacement, now backed by a sound
  mechanism rather than reconstruction.
- Update `TestCadence_*`, `TestDailyStatsSeries`, SPEC §9.4.1.

## 9. Acceptance
- The 120d live-embedding validation shows exactly one 0-turn Sunday record per week, work-day starts
  within `[N·24h, N·24h+span)` of the Monday-anchored grid through every day-off, contiguous
  `sim_date`, day count == `int(Duration/dayLength)`.
- No `nextHeavyAt`-for-day-close, no `g.dayIndex` counter, no `%7`, no load-bearing canary remain.
- `make test` green; the day-off guard runs as a checkpoint-gate test (≈one 9-day run), verifiable in
  isolation via the edit gate.
