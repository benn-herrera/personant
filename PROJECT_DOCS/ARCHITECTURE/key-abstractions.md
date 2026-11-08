## Key abstractions

| Concept | Definition | Spec |
|---|---|---|
| **Thread registry record** | one line of `spine.jsonl`; authoritative thread bookkeeping (id, project, anchors, summary, state, timestamps, turn_count, recall_fires) | "Storage and record formats" |
| **Thread** | the turns, not necessarily consecutive, that work on one specific problem (SPEC §1.1, §3.2); identified by `thr_<n>` (stable forever); a directory holding `thread.md` (YAML frontmatter), serial turn excerpts and a tracked-file store | "Storage and record formats" |
| **Project** | stable handle `prj_<n>` plus display name, rooted at one directory | "Project metadata", SPEC §4.5 |
| **Anchor symbol** | 0..8 normalized symbols, projected from retained `history_symbols` (see "Symbol extraction") and stored in the record; drive recall and appear in the display line | "Symbol lifecycle and anchor projection" |
| **History symbol** | per-thread accumulated symbol (`raw`, `normalized`, `first_seen_turn`, `count`, `source`) plus a `lifecycle` (`active`/`superseded`) and an `ever_central` latch; the thread's evolving identity and the recall match set; capped per `history.cap-per-thread`, trimmed with ever-central + high-specificity protection | "Storage and record formats" |
| **Symbol category** | `identifier` (preserve case), `entity` (lowercase + hyphenate), `tag` (lowercase, leading `#` stripped) | "Symbol normalization" |
| **Symbol source** | `deterministic` / `model` / `user` / `curator`; dominance: `curator > user > model > deterministic` | "Symbol source" |
| **Delta** | one content addition to the composed request | SPEC §3.0 |
| **Retention class** | `task` (default-transient: `tool.result`, `user.shell-capture`, `fs.*`) vs `decision` (default-persistent: `user.prompt`, `model.response`, ...). Drives the two-stage transient-data lifecycle. | SPEC §3.0.1, §3.10.1 |
| **Slot** | one named, budgeted region of a composed request (a memory slot or an opencode-content slot) with a budget, internal priority, overflow form and unused-share rule | SPEC §3.1 |
| **`MemoryOps` port** | the conceptual operations interface (`internal/memops`) the application layer depends on. One adapter (`fileadapter`, JSONL + markdown + go-git); callers depend only on the interface. | port-and-adapter |

---

