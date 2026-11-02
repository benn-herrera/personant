### Thread directory (`threads/thr_<id>/`)

- **`thread.md`** — YAML frontmatter (canonical for metadata) and a single `# <title>` line; no
  accumulating body, rewritten in full on every engagement.
- **`turns/`** — the warm tier: one terse excerpt file per primary-engagement turn, named by the
  thread's turn number zero-padded to 7 digits (`turns/0000024.md`). Appending a turn writes one
  small loose file; the turn path never writes the folio.
- **`folio.jsonl`** — the cold tier: the thread's bound excerpts, one JSONL line each (see
  "Excerpt tiers"). Absent when nothing is bound.
- **`files.jsonl`, `files/`** — the tracked-file store (SPEC §3.9); see "Tracked-file store".

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

[file read #1a2b3c4d5e6f] src/a.go
[file write #9f8e7d6c5b4a] src/a.go
[file commit @abc1234] src/a.go

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
- **Excerpt tiers.** A thread's excerpts are split between loose files (`turns/`) and its folio so
  the tracked-file count does not grow with thread length (SPEC §2.3). Tiers change only in the
  day barrier's step B1b (`store.SettleExcerptTiers`, see "Crash stability and the dual-repo
  substrate").
  - *Folio format.* JSONL, one line per bound excerpt, keys in the order
    `{"name":"0000024.md","content":"<the file's exact bytes>"}`, sorted ascending by (turn number,
    name) with no repeated name, written and read through the generic `store.WriteJSONL` /
    `store.ReadJSONL` (atomic write, default HTML escaping as in every home JSONL file). A line is
    malformed if it fails to decode, its `name` is not an excerpt name, or `content` is absent.
  - *Placement rule.* A pure function of canonical state: the spine record's state and the
    thread's excerpt names. A retired thread (`memops.IsRetiredState`) binds every excerpt; any
    other thread keeps its newest `store.HotExcerptTail` (= `ThreadTurnWindow` +
    `memops.EmbeddingDebtCap` = 528, the assembly window plus the steady-state embedding-debt
    window) loose and binds the rest. An excerpt whose folio line would exceed `ReadJSONL`'s
    1 MiB line cap stays loose, and a name in both tiers with different bytes (a hand edit) moves
    in neither direction. Loose files can therefore sit permanently below folio entries, as can a
    hand-made one: nothing assumes the folio is a strict prefix of the loose files. Idleness and
    Layer B/C membership are not placement signals.
  - *Tier-transparent readers.* `ReadThreadBody`, `ReadDebtWindowExcerpts` and
    `ReadThreadExcerpts` return the union of both tiers, each name once, the loose copy winning
    for a name in both, byte-identical whichever tier holds it. A reader decodes the folio only
    when it needs more than min(loose count, `HotExcerptTail`) of the newest excerpts, so a warm
    thread after a barrier never opens it; `ReadThreadExcerpts` reads it whenever one exists. A
    read never moves an excerpt: a re-engaged retired thread reads its bound excerpts from the
    folio until the next barrier unbinds its hot tail. `store.ListExcerptEntries` and
    `store.ReadFolio` serve `personant verify`'s folio checks.
  - *UTF-8.* Excerpt content is valid UTF-8 (it arrives through JSON decoding). Bind asserts it
    before writing anything for the thread, since Go's JSON encoder would silently replace invalid
    bytes; a violation fails the barrier with `store.ErrExcerptNotUTF8` naming the file, never a
    skip or a repair.
- **Tracked-file store.** One index and its content files per thread, written at turn close
  for the owner's file events (`store.ApplyFileEvents`) and pruned at barrier step B1c
  (`store.PruneTrackedFiles`).
  - *Index format.* `files.jsonl`, one line per path, sorted by `path`, keys in the order
    `{"path","id","size","turn","at","changes":[{"turn","at","from","to","diff","size"}]}`,
    written through `store.WriteJSONL`. `path` is the event's path as given. `id` is the git
    blob id of the current bytes, `size` their length in bytes. `turn` and `at` are the owner
    excerpt number and timestamp of the latest read or write. `changes` holds the last
    `dedup.live-diff-window` id transitions, oldest first; `diff` says whether the diff was
    kept, and the change's `size` is the diff's length (0 when not kept).
  - *Content files.* `files/<id>` holds the current bytes; `files/<from>-<to>.diff` a kept
    forward unified diff.
  - *Write order.* Blob and diff files are written (`WriteFileAtomic`) before the index that
    names them, so the index never names an absent file; the turn path never deletes.
  - *Retention.* B1c keeps entries with `turn > TurnCount − ThreadTurnWindow` and the blob and
    `diff:true` files they name. It deletes every other file under `files/` and a legacy
    `files.json`, rewriting the index before deleting. The store per thread is bounded by the
    paths touched within the assembly window × (current size + the diff window).
  - *Identifier lines.* Each file event of a turn is one line in the owner's excerpt,
    between the header and `**user:**`. Composition never parses them; which positions
    expand comes from the index.
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

