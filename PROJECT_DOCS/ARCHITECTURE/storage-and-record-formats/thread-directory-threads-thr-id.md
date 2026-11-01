### Thread directory (`threads/thr_<id>/`)

- **`thread.md`** — YAML frontmatter (canonical for metadata) and a single `# <title>` line; no
  accumulating body, rewritten in full on every engagement.
- **`turns/`** — one terse excerpt file per primary-engagement turn, named by the thread's turn
  number zero-padded to 7 digits (`turns/0000024.md`). Appending a turn writes one small file.
- **`files.json`** — the tracked-file sidecar (SPEC §3.9).

```markdown
---
id: thr_88
project: prj_3                       # internal handle; the display name is in meta.json
state: wip
created: 2026-05-06T14:23:00-07:00
last_engaged: 2026-05-08T03:12:00-07:00
state_changed: 2026-05-07T19:42:00-07:00
turn_count: 24
recall_fires: 3
description: how does the trefoil's body topology constrain electron shape?
anchors:
  - trefoil
  - unknot
history_symbols:
  - {raw: "trefoil", normalized: "trefoil", first_seen_turn: 142, count: 17, source: deterministic}
  - {raw: "(3,2)-torus knot", normalized: "3-2-torus-knot", first_seen_turn: 145, count: 4,
     source: model}
---

# Body topology — trefoil vs unknot
```

A turn-excerpt file (`turns/0000024.md`):

```markdown
## Turn 24 · 2026-05-08T03:12:00-07:00 · [trefoil, unknot, body-topology]

**user:** [terse prompt excerpt]

**agent:** [terse response excerpt, topic tag stripped]
```

- **Assembly window vs retention.** The working-set assembler reads at most 512 (default;
  calibratable) of a thread's most recent excerpts, sized so that at `b-top-k = 3` each thread's
  window stays within LayerB/3 bytes; the body is read newest-first up to a byte budget, then
  joined oldest to newest. Excerpts are retained past the window, remain recall-indexed, and are
  the durable source the chunk-level index embeds for intra-thread recall. An excerpt is
  decision-class content; raw task-class output is never written as one (SPEC §3.10.6). Durable
  turn content lives only in excerpts, never the event log. Retention trimming is the deferred
  keep/toss layer (SPEC §3.10.8); v0.1 keeps all excerpts.
- **`description` vs `summary`.** `description` is a whitespace-collapsed copy of the spawning
  prompt, truncated to the new-thread summary length bound, set deterministically (no LLM
  paraphrase). `summary` is the curator's closure gist, in frontmatter only (never in a turn file);
  a live thread has no `summary`.
- **No parent thread.** No parent-thread field exists or is synthesized (the flat model, SPEC §3.2).
  Origin provenance is per symbol (`derived_from`); thread-level lineage is reserved.

```typescript
interface HistorySymbol {
  raw: string;                      // surface form as encountered
  normalized: string;               // normalized form (see "Symbol normalization")
  first_seen_turn: number;
  count: number;                    // total emissions across all turns
  source: SymbolSource;
  lifecycle?: SymbolLifecycle;      // canonical supersession state; omitted ≡ "active"
  ever_central?: boolean;           // latched true on first entry to the projection; never cleared
  last_active_turn?: number;        // last turn in the active projection; omitted/0 ≡ never-active
  derived_from?: string[];          // origin thread ids ("Symbol source"); omitted/empty ≡ organic
}
type SymbolSource = "deterministic" | "model" | "user" | "curator";
type SymbolLifecycle = "active" | "superseded";  // "evicted" is the absence of the entry
```

A record decoding without the lifecycle fields is well-formed (the zero values above);
`verify`/index rebuild regenerate the derived projection from canonical on first run.

`history_symbols` is capped at `history.cap-per-thread` (default 40) over active and superseded
entries together. Over the cap, eviction operates on the **evictable** partition only: a symbol is
evictable iff it is not `ever_central` and not a high-specificity identifier (URL, file path, git
SHA). Within it, lowest `count` goes first, ties broken by lowest `first_seen_turn`. If the
protected set alone exceeds the cap, the lowest-`count` `superseded` entries go first.

