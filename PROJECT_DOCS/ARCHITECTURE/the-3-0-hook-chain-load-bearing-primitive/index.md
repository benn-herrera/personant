## The §3.0 hook chain (load-bearing primitive)

Personant treats the working window as a sequence of **context-modification events**, not
user-agent turns. The "turn" is a logging convenience; the load-bearing primitive is the per-event
hook chain. The principle: **no gaps**.

**Event taxonomy** (SPEC §3.0.1; the opencode mechanism behind each is in "Event observation"):

- `user.prompt`, `user.shell-capture`
- `model.response`, `tool.result`
- `fs.read`, `fs.write`, `fs.commit`
- `thread.fetched` (**Open:** U3 — how a fetch is triggered under opencode)

Layer A2 digest content, memory-command effects and directive reloads are not chain deltas: they
change what the composer (`workset.Compose`) assembles.

**Every event runs the chain in this fixed order:**

1. **Symbol extraction** (§3.3) — deterministic regex pass + parse model emissions; routing by
   retention class.
2. **Engagement signal** (§3.2) — coalesced per-turn (multi-tool-call turns don't multiply
   `turn_count`).
3. **Dedup decision** (§3.9) — content-addressed identifier replacement.
4. **Budget check** (§3.1) — bump-eviction so the budget is honored before the next event lands.
5. **Logging** (SPEC §2.8; see "Event log format").

Delta order and turn attribution: see "Ordering and correlation".

**Implementation contract** (§3.0.5): a single internal `onContextDelta(source, content, metadata)`
entry point. **No path mutates the working window without going through it.** Code review treats any
direct mutation as a bug.

Step 4 (budget check) is a documented no-op in v0.1 (`internal/turn/chain.go`,
`TODO(phase-2-budget)`); `TestBudgetCheckIsExplicitNoOpV01` trips if it is half-enabled without a
deliberate decision. Step 5 summarizes every task-class source to a metadata line (source, path,
byte count) instead of logging the body, so a newly added task-class source inherits the
minimization.

Contents:

- [Transient-data lifecycle (load-bearing for recall fidelity)](transient-data-lifecycle-load-bearing-for-recall-fidelity.md)
