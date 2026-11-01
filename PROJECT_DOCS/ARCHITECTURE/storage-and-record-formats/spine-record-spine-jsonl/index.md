### Spine record (`spine.jsonl`)

One JSON object per line, sorted by `id`, so diffs stay line-grain.

```typescript
interface SpineRecord {
  id: string;                        // /^thr_\d+$/; monotonic serial
  project: string;                   // /^prj_\d+$/; "prj_default" for unscoped threads
  anchors: string[];                 // [derived] 0..anchor.projection-max (8)
  anchors_projected_at_turn: number; // [derived] owner turn of last projection change (SPEC §3.2)
  summary: string;                   // 100-150 char gist, ≤ spine.entry-max-chars; at retirement
  description: string;               // triggering utterance; set once, never rewritten
  state: ThreadState;                // the SPEC §2.2.1 states, as their lowercase names
  created: string;                   // RFC3339
  last_engaged: string;              // RFC3339; read by closure decay, dormant order, digests
  state_changed: string;             // RFC3339
  turn_count: number;                // total turns this thread has been engaged in
  recall_fires: number;              // recall matches that led to a fetch; orders symbols.jsonl
}
```

`anchors_projected_at_turn` and `description` decode as 0 and empty on old records.

Contents:

- [Constraints enforced by `personant verify`](constraints-enforced-by-personant-verify.md)
