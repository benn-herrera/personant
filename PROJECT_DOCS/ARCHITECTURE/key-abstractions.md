## Key abstractions

| Concept | Definition | Spec |
|---|---|---|
| **Spine record** | one line of `spine.jsonl`; canonical thread bookkeeping (id, project, anchors, summary, state, timestamps, turn_count, recall_fires) | "Storage and record formats" |
| **Thread** | contiguous run of turns united by working on one specific problem; identified by `thr_<n>` (stable forever); a directory holding `thread.md` (YAML frontmatter), serial turn excerpts and a tracked-file sidecar | "Storage and record formats" |
| **Project** | three-layer identity: stable handle `prj_<n>`, mutable display name, optional canonical external identity (normalized git remote URL) | "Project metadata", SPEC §4.5 |
| **Anchor symbol** | `[derived]` 0..8 normalized symbols, projected from active `history_symbols` (see "Symbol extraction"); drive recall and surface in the spine line | "Symbol lifecycle and anchor projection" |
| **History symbol** | per-thread accumulated symbol (`raw`, `normalized`, `first_seen_turn`, `count`, `source`) plus a `lifecycle` (`active`/`superseded`) and an `ever_central` latch; the thread's evolving identity and the recall match set; capped per `history.cap-per-thread`, evicted with ever-central + high-specificity protection | "Storage and record formats" |
| **Symbol category** | `identifier` (preserve case), `entity` (lowercase + hyphenate), `tag` (lowercase, leading `#` stripped) | "Symbol normalization" |
| **Symbol source** | `deterministic` / `model` / `user` / `curator`; dominance: `curator > user > model > deterministic` | "Symbol source" |
| **Context-modification delta** | one event to the chain (§3.0); content-modifying event with source + content + metadata + retention class | SPEC §3.0 |
| **Retention class** | `task` (default-transient: `tool.result`, `user.shell-capture`, `fs.*`) vs `decision` (default-persistent: `user.prompt`, `model.response`, ...). Drives the two-stage transient-data lifecycle. | SPEC §3.0.1, §3.10.1 |
| **Slot** | one region of a composed request (a layer or an opencode-content slot) with a budget, internal priority, overflow form and underusage rule | SPEC §3.1 |
| **`MemoryOps` port** | the conceptual operations interface (`internal/memops`) the application layer depends on. One adapter today (`fileadapter`, JSONL + markdown + go-git); future adapters swap in without rewriting callers. | port-and-adapter |

---

