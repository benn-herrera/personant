#### Constraints enforced by `personant verify`

| Field | Constraint |
|---|---|
| `id` | `/^thr_\d+$/`, globally unique |
| `project` | `/^prj_\d+$/`, references an existing project (or `prj_default`) |
| `anchors.length` | 0 to `anchor.projection-max` inclusive. 0 is legal (a vague-start thread); verify flags only an over-ceiling count, a memory service bug, since the model never controls this count. |
| `anchors[i]` | normalized form; 3–50 chars |
| `summary.length` | ≤ `spine.entry-max-chars` |
| `state` | a defined state name |
| timestamps | RFC3339; `created` ≤ `last_engaged` |

**Display form** — the display line, generated from the record and never stored; what the model sees of a thread:

```
thr_<id> [<anchors joined with ", ">] — <summary> [<STATE>]
```

`<STATE>` is the state name uppercased.

Example: `thr_88 [trefoil, unknot] — electron body-topology conflict;
entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]`.

