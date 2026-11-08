### Thread registry record (`spine.jsonl`)

The file `spine.jsonl` holds the thread registry: one JSON object per line, sorted by `id`, so
diffs stay line-grain.

```typescript
interface SpineRecord {
  id: string;                        // /^thr_\d+$/; monotonic serial
  project: string;                   // /^prj_\d+$/; "prj_default" for unscoped threads
  anchors: string[];                 // projected from history_symbols and stored; 0..anchor.projection-max (8)
  anchors_projected_at_turn: number; // owner turn of last projection change (SPEC §3.2)
  summary: string;                   // 100-150 char closing summary, ≤ spine.entry-max-chars; set from the first response at creation; replaced by the closing summary
  description: string;               // triggering utterance; set once, never rewritten
  state: ThreadState;                // the SPEC §2.2.1 states, as their lowercase names
  created: string;                   // RFC3339
  last_engaged: string;              // RFC3339; read by engagement decay, dormant order, digests
  last_engaged_turn: number;          // service-run turn counter at last engagement; restarts each relaunch (U9); read by turn-based decay and the unknown-thread-id owner fallback
  state_changed: string;             // RFC3339
  turn_count: number;                // owner turns of this thread (SPEC §3.0.4)
  recall_fires: number;              // recall matches that led to a fetch; orders each symbol's thread lists in symbols.jsonl
}
```

`anchors_projected_at_turn` and `description` decode as 0 and empty on old records. `anchors` and `anchors_projected_at_turn` are derived (the anchor projection computes them from `history_symbols`) but stored in the authoritative record. The display line and the registry slot are generated from the record alone (SPEC §2.2, §2.2.2); without the stored copy, composing a request would read every `thread.md`. They are written at owner-turn close and at closing (`internal/turn/engage.go`, `internal/turn/closure.go`). `personant index rebuild` does not regenerate them, and no drift check covers them.

At owner-turn close, after merge → project → trim (see "Projection"), the projected anchors are written
to the record; `anchors_projected_at_turn` is set to the current owner-turn index only when the
projected set differs from the prior one. Every engagement rewrites `last_engaged`,
`last_engaged_turn` and `turn_count` (SPEC §3.2).

Contents:

- [Constraints enforced by `personant verify`](constraints-enforced-by-personant-verify.md)
