## The per-delta pipeline (§3.0)

The memory service takes content in as **deltas** (SPEC §3.0: each is one content addition to the
window); a turn groups deltas. Every delta runs the per-delta pipeline, a fixed sequence of steps,
so no content entering the window skips a step (**no gaps**).

**Delta taxonomy** (SPEC §3.0.1; the opencode mechanism behind each is in "Event observation"):

- `user.prompt`, `user.shell-capture`
- `model.response`, `tool.result`
- `fs.read`, `fs.write`, `fs.commit`
- `thread.fetched` (**Open:** U3 — how a fetch is triggered under opencode)

Other-projects slot digest content and directive reloads are not deltas:
they change what the composer (`workset.Compose`) assembles.

**Every delta runs the pipeline in this fixed order:**

1. **Symbol extraction** (§3.3) — deterministic regex pass + parse model emissions; routing by
   retention class.
2. **Engagement signal** (§3.2) — coalesced per turn in the turn buffer (multi-tool-call turns
   don't multiply `turn_count`).
3. **Dedup decision** (§3.9) — buffered; identifier lines and the tracked-file store update at
   turn close; replacement at composition.
4. **Budget check** (§3.1) — budget demotion so the budget is honored before the next delta lands.
   Demotion adds no delta: it emits a log line and ends.
5. **Logging** (SPEC §2.8; see "Event log format").

Delta order and turn attribution: see "Ordering and correlation".

**Implementation contract** (§3.0.5): a single internal `onContextDelta(source, content, metadata)`
entry point. **No delta reaches the working window without going through it.**

Step 4 (budget check) is a documented no-op in v0.1 (`internal/turn/chain.go`,
`TODO(phase-2-budget)`); `TestBudgetCheckIsExplicitNoOpV01` trips if it is half-enabled without a
deliberate decision. Step 5 summarizes every task-class source to a metadata line (source, path,
byte count) instead of logging the body, so a newly added task-class source inherits the
minimization.

Contents:

- [Transient-data lifecycle (load-bearing for recall fidelity)](transient-data-lifecycle-load-bearing-for-recall-fidelity.md)
