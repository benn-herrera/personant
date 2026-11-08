The answers below use only the SPEC text. Where an answer relies on outside knowledge, it says so.

## 1. What it does and does not do

Personant is a plugin-style memory service that runs inside an unmodified opencode. It decides what goes into every model request: it splits work into durable "threads", composes each request from fixed-size budgeted slots, and handles compaction, summarization and recall. It keeps memory as plain text under git in `~/.personant`, can recover from a crash losing at most one turn, retires idle threads to an archive, and offers relevant old threads by symbol matching and embeddings. It does not own the UI, tools, permissions, providers, session storage, agents or workflow; those stay opencode's. Its v0.1 acceptance test is a 120-day simulation run against a mock LLM, and many integration choices are still marked Open (U3–U9).

## 2. Parts I'd expect, and what each most resembles

| Part | Well-known analogue |
|---|---|
| opencode plugin that intercepts requests | Middleware / request-rewriting proxy |
| Slot composer (budgets, truncation, sharing unused space) | Prompt template engine with per-section quotas |
| Per-delta pipeline (extract → engage → dedup → budget → log) | Event-processing / ETL pipeline |
| Symbol extractor and normalizer | Search-engine text analyzer (regex tokenizer + normalization) |
| Topic-tag parser | Line-protocol parser |
| Thread registry + per-thread directories | Issue tracker on a flat-file document store |
| `symbols.jsonl` | Inverted index |
| Embedding index + `.recall-cache/` | Vector store with a derived, persisted cache |
| Recall banding (auto / ask / below) | Ranking with confidence thresholds, as in a recommender |
| Active/dormant sets | LRU cache |
| Thread states, decay, closure queue | Workflow state machine with TTL expiry |
| Staging buffer (citation window K) | TTL cache / generational promotion |
| Tracked-file store, content ids | Content-addressed store (git blobs) |
| Journal + turn repo + history repo + recovery cases | Write-ahead log + checkpointing + crash recovery (database-style) with daily snapshot rotation |
| Cold storage | Archive tier / log rotation |
| Directives with precedence | Layered config (like git's system/global/local) |
| Event log | Append-only structured audit log |
| Project identity resolver | Workspace detection (git-remote / path lookup) |
| Summarizer, clusterer, embedder, judge | LLM API clients |
| Redactor | Secret scrubber |
| Memory vetting write path | Review gate on writes, like a PR review |
| `personant` CLI | Admin CLI (`git fsck` / `git log` style) |
| Format revision + migration | Schema migrations |
| Simulation harness, mock LLM, crash injection | Discrete-event simulator + fakes + fault-injection tests |

## 3. Contradictions

**Direct:**
1. §2.2 "`description` … set once at creation and never rewritten" (and §2.3 "never rewritten") vs §4.2 `/topic rename` "Writes `description`".
2. §2.3 "a live thread has no `summary`" vs §4.2 "`summary` when it duplicates `description` — the shape `/topic` creation leaves". Also §2.2.2 says `/topic rename` "rewrites that name and nothing else", while §4.2 has it write `summary` too.
3. §2.2.2 display name: "`description`, falling back to `summary`, falling back to the id" vs §3.5 "the §2.2.2 fallback chain (description → summary → projected anchors)".
4. §2.1 "a state-changing git operation on the memory store fails while any derived file is stale" vs §4.5.8(7) "Intra-day derived staleness — by design" and §3.11 "turn-repository commits carry no integrity validation".
5. §8.3 "Before and after each state-changing git operation on `<store-dir>/.git/` … derived files are fresh" vs §4.5.8(5), where the day-commit step comes *before* the "Derived-rebuild step", and case 12 commits ("adopt-commit") before its "full rebuild".
6. §2.1 "A user's direct edit … is never destroyed by crash recovery" and §4.5.8 "Recovery never deletes authoritative bytes it cannot re-derive" vs case 4 "reset the store working tree to the turn repository's HEAD". Per §4.5.8(2), hand-edits only reach a commit "at the next full staging pass … not the next turn", so the reset would wipe them.
7. §3.8 "The archival *drain* fires **only at the day rollover**" vs §4.5.8(6) "standalone archival remains available outside the rollover". Standalone archival would also break the history-writer rule.
8. History-writer rule: "with one recovery-repair exemption" vs the list that follows it, which names three non-rollover writers (case-9 back-fill, case-12 adopt, roll-forward completion).
9. §4.5.8(8) "a turn repository is born only alongside a fresh history repository" vs §3.11 "deleted and recreated at each rollover" and (7) "recreated from the store working tree".
10. §4.5.8(3) journal bytes "are **never auto-replayed** … The user re-issues if they choose" vs (10) "A turn that opencode kept and memory rolled back (case 4/6) is re-ingested".
11. §4.3.3 "Before the first authoritative write … no open §4.5.8 turn scope, so the next start opens quiet" vs §4.5.8(2) "Append the prompt record to the journal … before the model call. The first append arms the in-flight signal". That leads to case 6, "preserve+surface", which is not a quiet start.
12. §3.2 "A **turn belongs to exactly one thread**", "The turn's excerpt is never dropped", and §1.1 "Each turn is assigned to a thread" vs §3.3 "No binding required … no excerpt, no owner".
13. §3.4 "All **non-transient** turn content MUST be locatable by recall at every stage … archival (§3.8)" vs §3.8.4 "Archived threads are **never candidates for live recall** in v0.1". The untagged turns in item 12 are also never written, so they are unfindable too.
14. §3.4 "may **NOT** model any 'by-design blind spot'" vs the same section's "Accepted residual: a single transient embed failure leaves a gap".
15. §3.4 "Three signals, run in parallel, not a cost cascade" vs signal 3, which works "on surfaced candidates" (a cascade). The same section says "signals 2/3 are cosines" but bands "intra-thread-scored" candidates as their own score, and says "there is no separate intra-thread path" but later "the automatic intra-thread path".
16. §3.8.4 "the §3.4 scan covers the thread registry + per-thread frontmatter" vs §3.4, which scans "per-turn-excerpt chunk vectors" and a lexical tail.
17. §3.5 Exception includes "the summarizer draft is empty or failed" yet is queued with "no summarizer call". The same section also says "A persistently failing summarizer therefore leaves its thread un-closed rather than queued".
18. §3.10.6 "**Never** written to the memory store: Raw bytes of task-class deltas (file content from `fs.read`/`fs.write` …)" vs the same section's "File content has a separate persistent home in the per-thread tracked-file store", and §3.9.1 "What is stored is **file bytes**".
19. §3.0.2 "every path by which content enters the working window runs the per-delta pipeline" and §3.0.5 "there is no bypass" vs §3.0.1 directives and digests "are not pipeline deltas". Instruction, MCP, expectation and tool-definition content (§1.2, §3.1) also has no delta type.
20. §3.1 instructions are a fixed slot whose "unused share is idle and is given up" vs the Open item listing "all four for the instructions … slots".
21. §3.9.1 diff threshold measured against "the new content's size" vs §3.9.2 "of the file's current size".
22. §1.4 evicted file content "can be brought back on demand" vs §3.9.2 "no mechanism yet brings its content back" for files over the share threshold.
23. §2.6.1 "setting it in a directive file has no effect" vs §3.9.3 "per-thread override is possible" and the §2.6 example "threshold tightened from 0.5 to 0.45". Lowering a threshold also loosens it rather than tightening it.
24. §4.5.3 "`origin` by default; otherwise none", lookup by `remote_urls` only, and path matching only when there is no remote vs §4.5.7 "or the first remote if no `origin`", both remote lists, and path matching "If step 1 produced no match". Yet §4.5.7 says the two "are the resolution of §4.5.3".
25. §3.8.1 a restore "reads from [`parent_commit_hash`] directly rather than walking `commit_hash`'s first parent" vs §3.8.3 step 2 "Resolve the deletion commit's **parent**".
26. §9.1 "The v0.1 acceptance criterion" vs "v0.5.0 — realism convergence. The acceptance criterion above".
27. §9.4.1 "there is exactly one clock" vs "the logical clock is the turn number".

**Tensions (lower confidence):**
- §2.2 the registry is "a single file sorted by id" vs §3.8.3 "Append a fresh thread registry record".
- §2.3 "number of files … does not grow with thread length" vs "Appending a turn writes one small file". This only holds after the rollover's excerpt-storage step.
- §3.5 "≥ 15 engaged turns, `turn_count`" vs §3.0.4, where `turn_count` counts owner turns only.
- §2.6 `project:` takes a "project name" (which can change) vs the paths `directives/prj_<n>/` and `directives/<active>/`.
- §1 "does not define … how much the user takes part" vs the `ack-mode` parameters.
- §3.1 "gives way through the existing eviction order" vs §3.0.2 "performs no eviction in v0.1".
- §3.3 the summarizer is listed as an extraction pass but "does **not** select anchors", yet §2.7.3 lists summarizer as a symbol source.

## 4. Terms the document does not define

| Term | First appears | My guess |
|---|---|---|
| opencode, Code Mode, `steer` | §1, §3.0.4 | Outside knowledge: an AI coding agent that runs in the terminal; Code Mode and `steer` are features of it |
| sub-mind split and re-merge | §1 | Branching memory for sub-agents |
| U3–U9 | §1 | IDs on an external list of unresolved decisions |
| replay | §1.4 | Re-running a read or tool call to regenerate content |
| spine / SpineRecord / `spine.*` events | §1.4, §2.2 | Old name for the thread registry |
| behavior / process / career memory | §1.8 | Kinds of memory defined in THESIS; guessed as standing rules / ways of working / reusable techniques |
| consolidation / "sleep" cycle | §1.8 | Offline batch review and repacking of memory |
| "Unused →" | §2.2 | Content moved to ARCHITECTURE; section number kept |
| accrual | §2.6 | Same as feedback directive tuning |
| working window vs working set | §3.0 | Probably the composed request vs the memory slots; never distinguished |
| engagement "threshold" | §3.0.2 | An unnamed overlap cutoff |
| Layer X, LayerB, `layerb_*` | §3.1 | Legacy names for the slots (B = active-threads) |
| recent-excerpt window size | §2.3 | An unspecified count |
| `folio.jsonl`, "warm window", "tiers" | §3.8.1, §4.5.8 | Packed storage for older excerpts; warm window = recent-excerpt window |
| summary tree; exact lexical / flat-scan level | §3.4 | A hierarchical summary index; a brute-force text scan |
| `lifecycle`, `anchors_projected_at_turn` | §3.2, §3.4 | Per-symbol active/superseded field; projection bookkeeping |
| curator (`retire.curator-error`) | §3.5 | Old name for the summarizer |
| thread registry size pressure; low-water mark | §3.8, §4.5.8 | Undefined archival thresholds |
| standalone archival | §3.8 | Archival triggered outside the rollover; no trigger is given |
| single-clock day index; day-commit-shape; gap-day | §4.5.8 | Day counter; a commit that is a day-commit specifically; idle days |
| managed `.gitignore` block | §4.5.8 | A section of `.gitignore` that Personant writes |
| resume budget; "standalone-kill claim … managed server" | §4.5.8(10) | opencode internals; I can't tell |
| `/no-revisit` "tighten threshold" | §4.2 | Raises a recall threshold, although v0.1 fixes thresholds |
| recall accrual loop | §4.3 | Feedback tuning applied to recall |
| ROADMAP B6a | §3.9.2 | An external roadmap item |
| category-3 known-unknown | §3.5 | Item 3 of the §9.1 accounting list |
| realism element; adversarial / flush-lag probes; churn / scenario / calibration | §9 | Simulation-harness concepts, partly explained |
| `superseded_precision`, `abandoned_premise_recall` | §3.4 | Metrics from the simulation |
| `model.MockServer`, probe-code retention | §9.2 | A mock LLM exposed over HTTP; whether test-probe code is kept |
| matryoshka; Jaccard; cosine | §8.2.2, §2.6.1 | Outside knowledge: standard embedding and similarity terms |
