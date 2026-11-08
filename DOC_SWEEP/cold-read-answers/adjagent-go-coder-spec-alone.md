# Personant SPEC: cold read

All answers come from the SPEC alone. Where an answer depends on something else, it is marked **[outside]**.

## 1. What it does and does not do

Personant is a plugin plus a memory service that runs under unmodified opencode. It takes ownership of every model request: it builds the request from persistent memory organised into **threads** (one thread per problem being worked on), recalls older relevant threads, removes repeated copies of file content, and replaces opencode's compaction. It stores memory as plain text in a git-backed store. That store has a per-turn journal, crash recovery that loses at most one turn, a daily history commit, and git-based archiving of old threads. It does **not** own the UI, sessions, tools, permissions, providers, agents or workflow, and it never rewrites opencode's transcript. In v0.1 it also leaves out several things: budget enforcement during a turn, cross-project engagement, recall over archived threads, keeping file history, and every directive parameter except the two `ack-mode` settings.

## 2. Expected parts and the ordinary thing each resembles

| Part | Resembles |
|---|---|
| opencode plugin / request hook | request middleware or interceptor |
| Memory service process | long-running local daemon |
| Per-delta pipeline (§3.0) | stream-processing / ETL pipeline |
| Symbol extraction + normalisation | search-engine analyser (regex lexer, case folding, stop words) |
| Topic-tag parser | regex line parser |
| Thread registry + thread directories | append-mostly document store; issue tracker |
| `symbols.jsonl` | inverted index |
| Embedding index + `.recall-cache/` | vector index with a persisted cache |
| Recall scorer with bands | retrieval ranker with confidence thresholds |
| Working-set composer, slot budgets, active set | prompt builder with fixed quotas; LRU cache |
| Tracked-file store, content ids, diffs | content-addressed blob store (git objects) plus a diff engine |
| Thread states and closure | ticket workflow state machine; idle-item reaper |
| Staging buffer / citation window | TTL cache |
| Summarizer, clusterer, judge, embedder clients | LLM API clients |
| Journal, turn repository, history repository, recovery | database WAL, checkpointing and crash recovery; nightly backup |
| Cold storage + `archive/index.jsonl` | archive tier with a catalogue |
| Event log | append-only structured audit log |
| Directive precedence | layered config (system < global < local, like git config) |
| Project resolver | workspace detection keyed on git remote and path |
| `personant` CLI | admin tool, like `git fsck` plus subcommands |
| Session commands | chat slash-command handlers |
| Key redactor | log scrubber / DLP filter |
| Format revision + migrations | schema migrations |
| Memory vetting write path | PR review gate with linters |
| Consolidation ("sleep") | nightly batch compaction, like LSM compaction |
| Mock LLM, simulation, invariant validators | discrete-event simulator; seeded fuzz/property testing |

## 3. Self-contradictions

1. **§1** "Personant does not define interaction modes, a workflow, or how much the user takes part." vs **§2.6.1** `closure.ack-mode` "always: every decayed thread is put to the user for an ack". Ack modes control exactly how much the user takes part.
2. **§1.1** "Each turn is assigned to a thread" and **§3.2** "A turn belongs to exactly one thread — its owner" vs **§3.3** "the turn proceeds without a topic tag — no engagement update, no excerpt, no owner".
3. **§3.4** "All non-transient turn content MUST be locatable by recall" and **§3.2** "The turn's excerpt is never dropped" vs **§3.3**, where an untagged conversational turn gets "no excerpt", together with **§2.3** "Durable turn content lives only in excerpts". Prompts and responses are decision-class (§3.10.1), so that content is non-transient yet stored nowhere.
4. **§1.4** "File content evicted from a request … can be brought back on demand" vs **§3.9.2** "Such a file is shown only by its content-id lines; no mechanism yet brings its content back". This holds if a file over the share threshold counts as budget eviction.
5. **§2.1** "A user's direct edit to an authoritative file is never destroyed by crash recovery: it is absorbed (§4.5.8 case 3)" vs **§4.5.8(4)** case 4 "reset the store working tree to the turn repository's HEAD", with **§4.5.8(2)** "A hand-edit is therefore absorbed at the next full staging pass … not the next turn".
6. **§2.1** "a state-changing git operation on the memory store fails while any derived file is stale" vs three passages:
   - **§4.5.8(7)** "Intra-day derived staleness — by design. The turn path never sets the watermark", with a commit on every turn.
   - **§4.5.8(5)**, where the day-commit step runs *before* the derived-rebuild step.
   - **§8.3** "No git pre-commit hook is installed", so a manual commit does not fail.
7. **§2.2** `description` is "set once at creation and never rewritten" vs **§4.2** `/topic rename` "Writes `description` … to both the authoritative §2.3 frontmatter and the derived thread registry record".
8. **§2.3** "a live thread has no `summary`" vs **§4.2** "`summary` when it duplicates `description` — the shape `/topic` creation leaves".
9. **§2.2.2** "its `description`, falling back to `summary`, falling back to the id" vs **§3.5** "(description → summary → projected anchors)".
10. **§2.2.1** "`active` → `wip`/`resolved`/`decided`/`abandoned` | closure question with chosen outcome" vs **§3.5** "A routine closure is applied without asking". Separately, the §3.8.3 restore to `state = wip` has no row in the table.
11. **§2.3** "Appending a turn writes one small file" vs **§2.3** "The number of files in a thread's directory does not grow with thread length". Only the rollover excerpt-storage step can reconcile these, and §4.5.8(5) says "Tiers change at no other time", so file count grows within a day.
12. **§2.3** "while this protected set fits the cap; if the protected set alone exceeds it, the least-counted superseded entries go first" vs **§2.7.4** "a symbol that was ever central is protected from capacity trimming". §2.7.4 states the protection unconditionally.
13. **§2.6** "threshold tightened from 0.5 to 0.45" vs **§2.6.1** `recall.cross-project-threshold: 0.5 # higher bar`. Lowering the bar loosens it. The same §2.6.1 also says setting that key "has no effect" in v0.1.
14. **§2.6** frontmatter "`project:` … project name for project-scoped" vs **§4.5.2** "Renames touch only the display name". A rename would orphan the directive.
15. **§2.6.1** "ceiling × ~2.5 conservative B/tok" vs **§3.1** "~2.5 chars/token". Bytes and characters differ for non-ASCII text.
16. **§2.8** the `context.modified source=user.prompt` line "is never suppressed" vs **§3.0.2** step 5 "a `context.modified` event if the delta materially changed window contents".
17. **§3.0.1** "These are all the deltas the memory service emits through the §3.0 pipeline" vs **§3.0.3** "Eviction is itself a delta, but recursive pipeline runs are capped at depth 1".
18. **§3.0.5** "Content reaches the working window only through the §3.0 pipeline; there is no bypass" vs **§1.2** MCP content: "Every kind reaches composition", plus the §3.1 instructions, expectations and tool-definition slots. None of these is in the delta taxonomy. Whether this is a real contradiction depends on what "working window" means (see Q4).
19. **§3.0.3** "extracted identifier symbols are what dedup matches against" vs **§3.9.1** "A file content's id is computed from its bytes alone".
20. **§3.1** "`fs.read`/`fs.write`/`fs.commit` deltas, whose content is the file body written to the per-thread tracked-file store" vs **§3.0.1** `fs.commit` carries "path + commit hash; no file bytes".
21. **§3.4** completeness covers lifecycle stages including "archival (§3.8)" vs **§3.8.4** "Archived threads are never candidates for live recall in v0.1".
22. **§3.4** "Completeness holds continuously, not eventually … no lag-window gap" vs **§3.4** "Accepted residual: a single transient embed failure leaves a gap for that batch". The same section also claims "exact recall is guaranteed by the separate exact lexical / flat-scan level", which would rule out any gap.
23. **§3.4** "there is no separate intra-thread path" vs **§3.4** "the automatic intra-thread path searches the engaged thread's not-yet-flushed tail lexically".
24. **§3.4** "3. Model judgment" vs **§3.4** Banding "signal 1 is a Jaccard set overlap, signals 2/3 are cosines".
25. **§3.8.4** "the §3.4 scan covers the thread registry + per-thread frontmatter" vs **§3.4** fine level "per-turn-excerpt chunk vectors".
26. **§3.5** Exception includes "the summarizer draft is empty or failed", and an exception is "queued silently: no summarizer call" vs **§3.5** "A persistently failing summarizer therefore leaves its thread un-closed rather than queued". The same section calls the queue "derived, never stored", but a draft failure cannot be derived from stored state.
27. **§3.5** "long-engaged (≥ 15 engaged turns, `turn_count`)" vs **§3.0.4** "an engaged-but-not-owner thread's `turn_count` is unchanged".
28. **§3.8** "The archival drain fires only at the day rollover" vs **§3.8** "Standalone archival is crash-recoverable". **§4.5.8(6)** contains both halves in one sentence: "fires only at the day rollover … standalone archival remains available outside the rollover".
29. **§4.5.8(5)** "written only at the day rollover, with one recovery-repair exemption" vs the same rule's "exhaustive" list. That list names three non-rollover writers: back-fill, adopt, and roll-forward completion. **§3.8.3** step 5 also commits a restore "to the memory store's git history", and that commit is not on the list.
30. **§3.9.1** "of the new content's size" vs **§3.9.2** "of the file's current size", for the same `dedup.diff-literal-threshold`.
31. **§3.10.6** "Never written to the memory store: Raw bytes of task-class deltas (file content from `fs.read`/`fs.write` …)" vs **§3.9.1** "What is stored is file bytes", in the thread directory.
32. **§3.10.6** "a content-id or commit-hash prefix" vs **§3.9.1**, where the content id takes "the form a git object id for those bytes takes".
33. **§3.3** lists "Summarizer" as the third of "Three passes per §3.0.2 step 1" vs the same item: "It does not select anchors". It runs at retirement, not per delta.
34. **§4.2** `/back-to <thr_id>` vs **§4.2** "`/back-to <old-name>` stops resolving".
35. **§4.3.3** "Before the first authoritative write, an interrupt leaves memory exactly as if the turn never happened: no open §4.5.8 turn scope … No durable full-text artifact" vs **§4.5.8(2)** "Append the prompt record to the journal (fsync) before the model call. The first append arms the in-flight signal", plus (3), where journal bytes are "written to a recovery artifact".
36. **§4.5.3** "`origin` by default; otherwise none", lookup by `remote_urls`, exact directory vs **§4.5.7** "or the first remote if no `origin`", `historical_remote_urls`, "(or any ancestor)". §4.5.7 also says "The two directory-matching steps are the resolution of §4.5.3."
37. **§4.5.7** `last-active` "is operational (§2.1)" vs **§4.5.8** "derived state (… the `last-active` file)".
38. **§4.5.8(3)** "never auto-replayed into the authoritative files … The user re-issues if they choose" vs **§4.5.8(10)** / **§1.6** turns "rolled back (case 4/6) … re-ingested".
39. **§4.5.8(8)** a missing turn repository "stays absent through init (a turn repository is born only alongside a fresh history repository)" vs the init rows "benign morning (watermark) → turn-repository init" and **(7)** "the turn repository is recreated from the store working tree".
40. **§4.5.8(9)** "backed by a 2-day simulation span" vs **§9.1** "The ladder is the set of spans run": 1d, 7d, then 15d through 120d.
41. **§8.2.1** "An API key … is never included in any LLM context" vs the redactor's scope: "`tool.result`, `fs.read`, `fs.write`, `user.shell-capture`" only, and "keys under 8 bytes are not scanned for".
42. **§9.1** "The v0.1 acceptance criterion" vs **§9.1** "v0.5.0 — realism convergence. The acceptance criterion above".
43. **§9.1** Evolving anchors are "exercised end-to-end by a … premise-inversion and interleave workload" vs **§9.1** open requirement "Workload interleaving … Until it does".
44. **§9.4.1** "Its idle span crosses the §3.5 wall-clock decay threshold" vs **§2.6.1** `engagement.decay-time: 7d`. By the arithmetic of §9.4.1's own times, a day-off idle span is about 34 hours.
45. **§9.4** "Four test levels — scenario, churn, calibration, and the acceptance simulation" and **§9.2** "The same mock serves every test level" vs **§9.3** "Unit testing is level 1 … no mock LLM is needed".
46. **§2.2** registry is "a single file sorted by id" vs **§3.8.3** "Append a fresh thread registry record". The archive index has the same conflict: "sorted by `thr_id`" vs "durably appended".

## 4. Terms the document does not define

| Term | First appears | Guess |
|---|---|---|
| U3, U4, U5, U7, U8, U9 | §1 (U7) | ids in an unresolved-decisions register held elsewhere |
| "sub-mind split and re-merge" | §1 | forking a sub-agent context and merging its results back |
| Code Mode | §1 | an opencode feature **[outside]** |
| "Plugin route" and other ARCHITECTURE section names | §1 | sections of ARCHITECTURE **[outside]** |
| behavior / process / career memory | §1.8 | kinds of memory; the SPEC says "THESIS defines the kinds" **[outside]** |
| "Unused →" heading marker | §2.2 | the content moved to ARCHITECTURE; yet text still follows it |
| spine, `SpineRecord`, `spine.*` events | §1.4 / §2.2 | old name for the thread registry |
| E, A1, A2, B, C, `LayerB`, "Layer X" | §2.6.1 / §3.1 | legacy slot names; only partly mapped to current names |
| working window (vs working set, composed request) | §3.0 | the content of the model request |
| bump-eviction | §3.0.2 | dropping the oldest content when over budget |
| `steer` delivery | §3.0.4 | opencode's mid-turn user message **[outside]** |
| "the active thread" (singular) | §4.2 `/topic rename`, `/done`, `/pause` | the front of the active set, or the last turn's owner |
| "the engaged thread" (singular) | §3.4 | the current turn's owner |
| `lifecycle` frontmatter field | §3.4 | per-symbol active/superseded label |
| summary tree; exact lexical / flat-scan level | §3.4 | hierarchical thread summaries; a grep over all excerpts. Neither is specified anywhere |
| stand-in line vs content-id line | §1.4 / §3.0.2 | probably the same thing for files |
| `folio.jsonl`, "turns/", tiers, warm window | §3.8.1 / §4.5.8(5) | packed file for older excerpts; per-turn files; the recent-excerpt window |
| curator (`retire.curator-error`) | §3.5 | the summarizer |
| decay-detection scan | §3.5 | the periodic decay check; when it runs is not stated |
| low-water mark | §4.5.8(5) | registry-size threshold below which archival is skipped |
| `op=barrier`, `op=sleep`, "sleep cycle" | §4.5.8(1) / §3.10.8 | day-rollover marker; consolidation |
| single-clock day index | §4.5.8(5) | day number from wall-clock time; its epoch in production is not given |
| day-commit-shape commit | §4.5.8(5) | a commit with the day-commit step's tree and trailer pattern |
| checkpoint, "mid-run structural" | §1.7 / §3.11 | two meanings: opencode's checkpointing and Personant's full-staging commit |
| managed `.gitignore` block | §4.5.8(5) | a Personant-owned section of `.gitignore` |
| accrual; "recall accrual loop" | §2.6 / §4.3 | feedback directive tuning |
| orientation preamble | §3.1 | fixed intro text in the system prompt |
| budget-pressure fraction / dissection | §2.6.1 / §3.6 | fraction of the memory budget in use; what it is a fraction of is not stated |
| recognition / "recognition surface" | §1.4 | the model identifying a prior thread from its display line |
| ROADMAP B6a | §3.9.2 | a roadmap item **[outside]** |
| `model.MockServer`, "probe-code retention" | §9.2 | mock served over HTTP; keeping probe code |
| managed server, standalone-kill claim, resume budget | §4.5.8(10) | opencode run and resume modes **[outside]** |
| `superseded_precision`, `abandoned_premise_recall` | §3.4 | simulation metrics, never defined |
| matryoshka-capable | §8.2.2 | embeddings that can be truncated to a shorter vector **[outside]** |
| Jaccard, cosine, RE2 | §2.6.1 / §5.1.2 | standard terms **[outside]**, used without definition |

There is also no §7; the numbering skips from §6 to §8.
