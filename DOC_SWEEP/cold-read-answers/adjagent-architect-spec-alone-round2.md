## 1. What it does and what it doesn't

Personant is a plugin for opencode, backed by a local memory service, that takes over everything that goes into each model request. It builds each request from a long-lived memory of "threads", one per problem being worked on, kept in plain-text files tracked in git. It shrinks long conversations to fit the context budget, closes threads that go quiet, and brings related past threads back into the request ("recall"). It does not own the terminal UI, sessions, the message format, tools, permissions, providers or the shell; all of those stay with opencode. It also never calls a model directly (it asks opencode), and it defines no workflow and no prescribed level of user involvement.

## 2. Expected parts

The comparisons below come from my general knowledge, not from the SPEC.

| Part | Ordinary thing it resembles |
|---|---|
| opencode plugin | Request-interceptor middleware or a proxy |
| Memory service | Local daemon or sidecar, like a language server |
| Per-delta pipeline (§3.0) | Event-ingest middleware chain |
| Symbol extractor and topic-tag parser | Regex extractor (like ctags); line-grammar parser |
| Thread registry and thread files | Plain-text issue tracker in git, with a state machine |
| Symbol index | Inverted index |
| Recall (Jaccard + embeddings) | Hybrid lexical and vector search, as in RAG retrieval |
| Embedding cache | Vector store |
| Active and dormant sets | LRU cache |
| Composer with slot budgets | Prompt template renderer with quotas |
| Decay and close-out | Stale-issue bot |
| Tracked-file dedup | Content-addressed blob store (git object ids) plus diffs |
| Staging buffer | Cache entries that expire after a few turns |
| Turn and history repositories, recovery | Write-ahead log plus daily snapshots; database crash recovery |
| Cold storage | Archive tier |
| Directives | Layered config (like git's system, global and local config) |
| Project resolver | Workspace detection keyed on the git remote |
| Format revision | Schema migrations |
| Memory vetting | Review and approval queue |
| `personant` CLI | git-style subcommand CLI |
| Event log | Append-only structured log |
| §9 harness and mock LLM | Seeded simulation with a fake clock; record/replay test double; crash-injection testing |

## 3. Contradictions

**Direct contradictions**

1. **Pipeline order.** §3.0.2 lists step 5, "**Logging** … emit the source-specific event", after step 4, the budget check. §3.0.3 says: "Budget check runs last."
2. **What counts as engagement.**
   - §2.2.1: "currently **engaged** (referenced by a turn's symbols or by its **topic tag**".
   - §3.0.2 step 2: "the threads the turn engages (§3.2: those its topic tag names)".
3. **Who detects `blocked`.**
   - §2.2.1 table: "`active` | `blocked` | summarizer detects + user confirms".
   - §5.2: "The summarizer is invoked when a thread closes".
4. **Closing without a question.**
   - §2.2.1: "`active` | `wip` / `resolved` / … | closing question with chosen outcome".
   - §3.5: "A **routine** close-out is applied without asking: … `resolution=resolved`".
5. **When hand-edits are committed.**
   - §4.5.8(2): "committed at the next clean-stop checkpoint or day rollover, not with the next turn".
   - §3.11: a checkpoint is also made "after a mid-run structural change (creating or closing a thread …). Every checkpoint commits the whole store working tree, hand-edits included".
6. **Close-out commit path.**
   - §3.5: a routine close-out "adds no commit path of its own".
   - §3.11: closing a thread triggers a checkpoint commit.
7. **Can a hand-edit be reverted?**
   - §2.1: "where a torn turn's reset would revert it, preserved byte-exact in quarantine … first".
   - §4.5.8(2): "never reverted"; §4.5.8(4): "a hand-edit is never reset".
8. **Torn-turn content.**
   - §4.5.8(3): "they are never replayed into memory. The user re-issues".
   - §4.5.8(10): "A turn that opencode kept and memory rolled back in crash recovery is re-ingested". The sources differ (the journal versus opencode's stored messages), but the outcome conflicts.
9. **Tracked-file store and the history repository.**
   - §3.9.1: "the history repository never holds it".
   - §2.3 puts the tracked-file store inside the thread directory. §3.8.1 says "Each archived thread's bytes stay reachable in the history repository". §4.5.8(5) says the rollover "commits the day … to the history repository".
10. **Project resolution: §4.5.3 versus §4.5.7.**
    - Which remote: §4.5.3 says "`origin` by default; otherwise none"; §4.5.7 says "or the first remote if no `origin`".
    - Which URLs are searched: §4.5.3 searches "`remote_urls`"; §4.5.7 searches "`remote_urls` and `historical_remote_urls`".
    - Path lookup: §4.5.3 checks paths only under "**Else (no remote at this path)**"; §4.5.7 checks them whenever "step 1 produced no match".
    - No match: §4.5.3 goes straight to asking; §4.5.7 first does a "Last-active confirmation".
11. **Budget guarantee.** §3.1 says "derived from it conservatively, so a request within its slot budgets is within the ceiling". The same section says "Static prompt scaffolding … is charged to no slot" and "Without a tool-definition slot the whole-request ceiling … cannot be enforced".
12. **Diff threshold basis.**
    - §3.9.1: "of the new content's size".
    - §3.9.2: "of the file's current size".

**Softer conflicts, depending on how they are read**

13. **When the history repository is written.**
    - §3.11: "written only at day rollover, apart from crash recovery".
    - §3.8.3: "the restore is committed to the memory store's git history". It doesn't say which repository.
    - §4.5.8(8): "adopted into the history repository on first open".
14. **One commit per day.**
    - §3.11: "exactly one day-grain commit … (an empty day still yields one)".
    - §4.5.8(5): the rollover "runs at the first service-run open or turn of a new day". A day on which the service never runs gets no rollover.
15. **Budget demotion.**
    - §3.0.2 step 4: "triggers budget demotion before the next delta lands".
    - The note in the same step: "This step performs no budget demotion in v0.1". The SPEC flags this one itself.
16. **Tool-output cap.**
    - §3.1: "(default 8 KB; configurable)".
    - §2.6.1: "v0.1 honors only `closure.ack-mode` and `recall.ack-mode`".
17. **Number of slots.** §3.1 says "Every slot — the five memory slots and the three above", which makes eight. It then counts "live turn" as a dynamic slot, and live turn is not one of the eight.
18. **Turn opening.**
    - §3.0.4: "A turn opens on a `user.prompt` event".
    - Same section: "**Open:** … whether it opens a new turn or joins the turn in progress".
19. **Cross-project recall.**
    - §3.4: auto-fetch at "≥ … 0.65 … loaded through the SAME fetch path", with cross-project candidates able to surface.
    - §3.1: "A thread of another project is declined … neither loaded".
20. **Decay units.**
    - §3.5: "fires when `last_engaged` exceeds `engagement.decay-turns`".
    - §3.0.4: "`last_engaged` (a thread's recency timestamp)". A timestamp can't be compared with a turn count.
21. **Extraction passes.** §3.3 is titled "Symbol extraction (three passes)", but says of the third pass: "It contributes no symbols."
22. **Binding-required turns.**
    - §3.3: "(buffered §3.9 file edits)".
    - Later in §3.3: "A turn that read a file is binding-required".
23. **Content-id line.**
    - §3.9.1: "content id".
    - §3.10.6: "a content-id or commit-hash prefix".
24. **Files per thread.**
    - §2.3: "Appending a turn writes one small file".
    - Same section: "The number of files in a thread's directory does not grow with its count of excerpts". The day rollover may reconcile these, but the SPEC doesn't say so.
25. **Model access.**
    - §8.2.1: "never calls a model … holds no endpoint".
    - §3.3: "the request re-issued". §5.1: "built per provider protocol".
26. **Project key in directives.**
    - §2.6: "project name for project-scoped".
    - §4.5.2: the display name is mutable, and the key is `prj_<n>`.
27. **Interaction modes.**
    - §1: "does not define interaction modes … or how much the user takes part".
    - The `auto|always` and `banded|always` modes in §2.6.1 define exactly that.
28. **opencode's transcript.**
    - §3.9: "opencode's stored transcript is never rewritten".
    - §1 and §1.7: Personant cleans up opencode's database.
