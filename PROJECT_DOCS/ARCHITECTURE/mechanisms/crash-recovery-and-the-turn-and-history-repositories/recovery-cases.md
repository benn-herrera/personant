#### Recovery cases

Turn cases 4, 5 and 6 evaluate against the turn repository; case 12 against the history
repository. An archive entry awaiting back-fill is an entry of the "Archive index" whose
`commit_hash` is still empty.

| Case | Trigger | Action |
|---|---|---|
| 1 clean | no marker, turn repository state present, store working tree clean against turn repository HEAD, turn repository HEAD == watermark, journal empty | no-op — O(1) open |
| 2 derived-stale | no marker, clean, derived stale | rebuild derived; set watermark = turn repository HEAD |
| 3 hand-edit | markerless dirty against turn repository HEAD, watermark present | never reset — a legitimate hand-edit, absorbed at the next full staging pass |
| 4 torn turn | journal non-empty, dirty, `head turn id ≠ turn` | preserve and surface the journal → quarantine dirty tracked bytes byte-exact → reset the store working tree to the turn repository's HEAD (≤1 turn) → move untracked debris in the authoritative-file namespace (excluding `logs/` and `recovery/`) into the same quarantine → re-append log bytes → truncate the journal |
| 5 turn committed | journal non-empty, clean, `head turn id == turn` | redundant journal (turn already committed) — truncate, zero loss |
| 6 turn no-writes | journal non-empty, clean, `head turn id ≠ turn` | nothing landed structurally — preserve and surface the journal, truncate |
| 7 (composite) | case 4 coinciding with an archive entry awaiting back-fill | case 4, then the case 9 back-fill pass |
| 8 archival | `op=archival` marker | detect only → reported as a pending completion; the roll-forward completes the batch (no reset, no re-archive as drift); marker left in place until completion |
| 9 back-fill repair | archive entry awaiting back-fill (any case, post-normalization) | locate the deletion commit (the child of the entry's `parent_commit_hash`, by tree comparison) and back-fill its hash; unlocatable ⇒ `recovery.unrepairable`, the entry stays refused, never guessed |
| 10 consolidation | `op=sleep` marker | clear the marker; the memory service self-heals (gc and rebuild are idempotent) |
| 11 recovery re-entry | `op=recovery` (crash during recovery) | re-run under the original marker; every phase is idempotent, so it converges |
| 12 legacy adopt | dirty, no watermark ever | greenfield or legacy: verify-gated adopt-commit to the history repository (`Personant-Day` trailer), back-fill repair, full rebuild, then turn-repository init (see "Day rollover") |

Turn- and history-repository rows:

| Op | Action |
|---|---|
| `op=barrier` | detect only → reported as a pending completion; the memory service runs the idempotent day-rollover completion. The turn repository state observable is irrelevant to classification. |
| `op=rebaseline` | `rm -rf .git-daily` plus turn-repository init, unconditionally; no history-repository touch |
| turn-repository init rows (no marker, turn repository missing or half-created) | greenfield (no watermark) → adopt if the history repository is dirty, then turn-repository init and rebuild; benign morning (a service-run open that finds the turn repository removed by the last day rollover: the normal first open of a day; turn repository missing, watermark present) → turn-repository init, rebuild iff derived is stale; interrupted init or corrupt directory → `rm -rf`, then turn-repository init. A missing turn repository is normal, never a data-loss signature. |

An orthogonal heal runs on every path, before classification: an additive event-log tail-heal (last
byte ≠ `\n` ⇒ append one `\n`, never truncate).

