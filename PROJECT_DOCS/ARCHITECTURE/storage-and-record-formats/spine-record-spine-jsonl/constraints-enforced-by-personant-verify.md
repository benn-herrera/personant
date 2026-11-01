#### Constraints enforced by `personant verify`

| Field | Constraint |
|---|---|
| `id` | `/^thr_\d+$/`, globally unique |
| `project` | `/^prj_\d+$/`, references an existing project (or `prj_default`) |
| `anchors.length` | 0 to `anchor.projection-max` inclusive. 0 is legal (a vague-start thread); verify flags only an over-ceiling count, a substrate bug, since the model never controls this count. |
| `anchors[i]` | normalized form; 3–50 chars |
| `summary.length` | ≤ `spine.entry-max-chars` |
| `state` | a defined state name |
| timestamps | RFC3339; `created` ≤ `last_engaged` |

**Display form** — generated from the record, never stored; what the model sees in working context:

```
thr_<id> [<anchors joined with ", ">] — <summary> [<state>]
```

Example: `thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict;
entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]`.

The human half of a thread's display — the working name a user reads and types back — is its
`description`, falling back to `summary`, falling back to the id. A rename rewrites that name and
nothing else: the id, the anchors, `history_symbols` and everything recall matches on are
untouched. The user-facing rendering is `thr_N: display — gist` (SPEC §3.4).

