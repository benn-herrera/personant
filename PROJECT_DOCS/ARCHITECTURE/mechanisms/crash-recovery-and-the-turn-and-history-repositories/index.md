### Crash recovery and the turn and history repositories

Standard pattern: THESIS "The building blocks", row "Durability and crash recovery".

The memory service can be hard-killed (SIGKILL, power loss) at any instant, so recovery is designed
as a **deterministic cold-start reconciliation** — no signal handler doing teardown work, no model
in the loop, no user acknowledgement. On-disk state alone determines the outcome. The load-bearing
property: **the store working tree is the truth; git is a recovery-point index over it.** No
authoritative byte of the store working tree lives only in the turn repository, so the turn repository can be recreated from the store working tree and losing it is never losing content. Archived threads live only in the history repository (cold storage, SPEC §3.8), so a missing history repository is refuse-to-open. (Spec §4.5.8.)

Two git repositories track the one store working tree: the turn repository, per-turn and
disposable, and the history repository, day-grain and permanent (cadence, cost bar and ≤1-turn
durability: SPEC §3.11). The in-flight turn's raw `(prompt, response)` bytes are protected by a
durable append journal written *before* any authoritative write, so even the one at-risk turn's
content survives.

Recovery reduces to a small state machine over on-disk observables (operation marker, in-flight
journal, turn and history repository HEAD trailers, watermark, turn-repository presence) that
classifies into one case and repairs idempotently — a torn turn resets **only the turn repository**
to ≤1-turn-ago; a day-rollover or archival crash **rolls forward** (never resets the store working
tree, which would destroy up to a day); a broken hand-edit is **quarantined byte-exact, never
deleted**. Ruled out permanently: loss-bar extension (commit batching / ≤N-turn windows — ≤1 turn
is the bar), turn-repository durability guarantees (disposable by construction), and
history-repository auto-recreation (a missing history repository is refuse-to-open, not a benign
recreate).

Under opencode the bar is judged against turns the user saw complete (SPEC §4.5.8 (10)): a turn
opencode recorded as terminal that memory rolled back is lost until reconcile re-ingests it (see
"Start order and resume").

Contents:

- [Observables](observables.md)
- [Turn transaction and journal](turn-transaction-and-journal.md)
- [Recovery cases](recovery-cases.md)
- [Repository invariants](repository-invariants.md)
- [Day rollover](day-rollover.md)
- [Mechanism names](mechanism-names.md)
