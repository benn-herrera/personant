# Personant: cold read of THESIS + SPEC

## 1. What it does and does not do

Personant is a memory layer for a coding agent running under unmodified opencode. A plugin routes each model request to a local, single-writer memory service. The service builds the request fresh from a git-versioned plain-text store rather than replaying the session transcript. Work is recorded as **threads**, one per problem, and each turn is assigned to one of them. Threads decay and close through a state machine, are archived into git, and are brought back by symbolic recall and, if configured, embedding recall. Models make bounded judgments: the topic tag, the closure summary, clustering and vetting review. Deterministic code writes the store, and crash recovery is journaled so that at most one turn is lost.

It does not provide the UI, tools, permissions, providers, session storage or workflow, which are opencode's. It is not a database, a vector database, a network server, or a mirror of the user's repository. In v0.1 it also does not do cross-project engagement, recall of archived threads, or eviction while a turn is running.

## 2. Expected parts and their ordinary analogue

| Part | Resembles |
|---|---|
| opencode plugin | Host extension / request middleware (like an editor extension) |
| Memory service | Local single-writer daemon (like a language server) |
| Memory store (`~/.personant`, text plus git) | Dotfiles-style git repo of JSONL/Markdown/YAML |
| Turn journal plus startup recovery | Write-ahead log plus crash recovery (SQLite WAL style) |
| Turn repo and history repo | Two-tier snapshots: hourly and daily, e.g. Time Machine thinning |
| Derived indexes (`symbols.jsonl`, digests, watermark) | Inverted index / `updatedb`-style rebuildable cache |
| Symbol extraction and normalization | Regex tokenizer / lexer |
| Topic-tag parser | Line-anchored regex parser for in-band markup |
| Recall (Jaccard, embedding kNN, model check) | Small hybrid search: set-similarity plus ANN |
| Working-set composer with slots | Budgeted template renderer / priority layout |
| Active set and dormant set | LRU cache |
| Staging buffer (K-turn citation window) | TTL cache |
| Tracked-file store and content ids | Content-addressed blob store (git blob ids) plus diff window |
| Thread lifecycle and closure queue | State machine with timers / issue-tracker states |
| Cold-storage archive (`archive/index.jsonl`) | Git-backed archive tier with a manifest |
| Event log | Daily-rotated append-only log (syslog/logrotate) |
| CLI | git-style subcommand tree |
| Directives | Layered config by precedence (gitconfig system/global/local) |
| Vetting write path | Pre-commit review with an audit trail |
| Summarizer / clusterer / judgment calls | Single-purpose LLM prompt calls |
| Key redactor | Log/secret scrubber |
| `config.toml` | TOML settings file |
| Format revision plus migrations | Schema-version file plus migration chain |
| Mock LLM and simulation harness | Scripted test double plus seeded discrete-event simulator / load test |

## 3. Contradictions

### THESIS vs SPEC

1. **Composed vs replayed.**
   - THESIS "What is new" 1: "Personant replaces both: each request is assembled afresh from memory, so nothing depends on a session's length."
   - SPEC §3.1: "The whole request — system prompt + replayed transcript tail + current user input…"
   - SPEC §3.9 also implies transcript rows are sent until memory has captured them: "a transcript row is dropped from the request once memory has captured it."
2. **Findable afterwards.**
   - THESIS "What is new" 2: "rests when work on it stops, and stays findable afterwards."
   - SPEC §3.8.4: "Archived threads are **never candidates for live recall** in v0.1."
3. **Across projects.**
   - THESIS: "work picks up across sessions and across projects as if there had never been a boundary."
   - SPEC §3.2: "cross-project engagement is not built in v0.1 and is declined."
   - SPEC §3.1: a cross-project fetch "is declined at the fetch seam."
4. **Model access.**
   - THESIS table, Model access: "Calls made through the host."
   - SPEC §8.2.1: "The memory service calls models itself… opencode has no embedding provider, so the embedder needs Personant configuration." See also Q4-A.
5. **Behavior memory scope.**
   - THESIS "Three kinds": behavior memory is "how the agent thinks and conducts itself… Universal."
   - SPEC §2.6: "A change proposed to the defaults or user-wide directive file… is a behavior-memory change." Those files carry numeric tuning parameters (§2.6.1, e.g. `recall.cross-project-threshold`), which are not comportment.

### SPEC vs itself

6. **Every turn has a thread.**
   - §1.1: "Each turn is assigned to a thread… by the problem it concerns."
   - §3.2: "A turn belongs to exactly one thread."
   - §3.3: "**No binding required**… the turn proceeds… no excerpt, no owner."
7. **Completeness invariant vs untagged turns.**
   - §3.4: "All non-transient turn content MUST be locatable."
   - §3.3: a conversational untagged turn writes "no excerpt", although `user.prompt` and `model.response` are decision-class (§3.10.1).
8. **Completeness vs archival.**
   - §3.4: locatable "at every stage… archival (§3.8)."
   - §3.8.4: "never candidates for live recall."
9. **No blind spots vs an accepted residual.**
   - §3.4 Enforcement: "may **NOT** model any 'by-design blind spot'."
   - §3.4 Completeness floor: "Accepted residual: a single transient embed failure leaves a gap."
10. **What signal 3 is.**
    - §3.4: "Three signals, run in parallel, not a cost cascade… 3. **Model judgment** on surfaced candidates."
    - §3.4 Banding: "signals 2/3 are cosines."
    - Judgment that runs on already-surfaced candidates is a cascade, and it is not a cosine.
11. **Intra-thread path.**
    - §3.4: "there is no separate intra-thread path."
    - Same section: "the automatic intra-thread path searches the engaged thread's not-yet-flushed tail lexically."
12. **No pipeline gaps vs no budget check.**
    - §3.0: "every path… runs the per-delta pipeline (… budget check…), so no content escapes… the budget check."
    - §3.0.2 step 4: "This step performs no eviction in v0.1."
13. **Only path into the window.**
    - §3.0.1: "These are all the deltas."
    - §3.0.5: "Content reaches the working window only through the §3.0 pipeline."
    - §3.1 adds Instructions, opencode-expectations and Tool-definition slots whose content has no delta type. §1.2 says all MCP content "reaches composition."
14. **Zero overflow vs unenforceable ceiling.**
    - §9.1: "Any overflow is a fail."
    - §3.1: "Without a tool-definition slot the whole-request ceiling above cannot be enforced."
    - §3.0.2: deltas "can silently starve" slots.
15. **Raw file bytes never stored.**
    - §3.10.6: "**Never** written to the memory store: Raw bytes of task-class deltas (file content from `fs.read`/`fs.write`…)."
    - §3.9.1: the tracked-file store keeps "the current content." §3.10.6 itself then says file content "has a separate persistent home."
16. **File history.**
    - §3.9.1: "Personant does not keep file history."
    - §3.9.1: "The history repository keeps each day-commit's tracked-file store."
17. **What dedup matches against.**
    - §3.0.3: "extracted identifier symbols are what dedup matches against."
    - §3.9.1: the content id "is computed from its bytes alone."
18. **Task-class bound.**
    - §3.1: "The cap is a **task-class** bound… does **not** apply to the `fs.read`/`fs.write`/`fs.commit` deltas."
    - §3.10.1: those deltas are `task`.
19. **Summarizer and exception closures.**
    - §3.5: an Exception is one where "the summarizer draft is empty or failed," and exceptions are "queued silently: no summarizer call."
    - §3.5: "A draft that fails or comes back empty… is retried at the next scan… leaves its thread un-closed rather than queued."
20. **Long-engaged threshold.**
    - §3.5: "≥ 15 engaged turns, `turn_count`."
    - §3.0.4: `turn_count` increments "only for the turn's **owner** thread," so it counts owner turns, not engaged turns.
21. **Who detects `blocked`.**
    - §2.2.1: `active → blocked`: "summarizer detects + user confirms."
    - §5.2: the summarizer is "invoked at closure" and only drafts a gist.
22. **`description` immutable.**
    - §2.2 and §2.3: `description`… "set once at creation and never rewritten."
    - §4.2 `/topic rename`: "Writes `description`."
23. **Live threads have no summary.**
    - §2.3: "a live thread has no `summary`."
    - §4.2: "`summary` when it duplicates `description` — the shape `/topic` creation leaves."
    - §3.8.3: a restored thread is `wip` with its summary from the restored frontmatter.
24. **Hand-edits never destroyed.**
    - §2.1: "A user's direct edit… is never destroyed by crash recovery."
    - §4.5.8 (4), case 4: "reset the store working tree to the turn repository's HEAD." Hand-edits are not in the scoped per-turn commit (§4.5.8 (2)), so the reset discards them. That also conflicts with "Quarantine-never-delete."
25. **Git operations and stale derived files.**
    - §2.1: "a state-changing git operation on the memory store fails while any derived file is stale."
    - §4.5.8 (7): "Intra-day derived staleness — by design."
    - §3.11: turn-repo commits "carry **no integrity validation**."
26. **History-writer exhaustiveness.**
    - §4.5.8 (5): "the exhaustive set of history-repository writers is… **nothing else**."
    - §4.5.8 (6): "standalone archival remains available outside the rollover."
    - §3.8.3 step 5: restore commits "to the memory store's git history."
    - §4.5.8 (5) Lifecycle git tags: tags are created on the history repository.
27. **Archival only at rollover.**
    - §3.8: "fires **only at the day rollover**."
    - §4.5.8 (6): "standalone archival remains available outside the rollover."
28. **Which commit a restore reads.**
    - §3.8.1: "a restore reads from [`parent_commit_hash`] directly rather than walking `commit_hash`'s first parent."
    - §3.8.3 step 2: "Resolve the deletion commit's **parent**."
29. **Exactly one recovery case.**
    - §4.5.8 (1): "classifies into exactly one **case**."
    - §4.5.8 (4): case 7 is "(composite)" and case 9 applies "(any case, post-normalization)."
30. **Re-ingesting turns.**
    - §4.5.8 (3): "a turn's derivation — tag parse, symbol extraction, engagement — is not replayable."
    - §4.5.8 (10): a rolled-back turn "is re-ingested."
31. **Retracted prompts.**
    - §4.3.3: "Before the first authoritative write, an interrupt leaves memory exactly as if the turn never happened… the next start opens quiet," and "No durable full-text artifact."
    - §4.5.8 (2) step 1: the prompt is journaled with fsync "before the model call." The non-empty journal "*is* the in-flight-turn signal" and is "preserved + surfaced."
32. **Which remote identifies a project.**
    - §4.5.3: "`origin` by default; otherwise none"; it looks up "by `remote_urls`."
    - §4.5.7: "or the first remote if no `origin`"; it looks up "`remote_urls` and `historical_remote_urls`," including ancestors.
    - §4.5.7 also claims these steps "are the resolution of §4.5.3."
33. **Canonical remote position.**
    - §4.5.2: remote identity is "`remote_urls[0]`."
    - §4.5.4: "Appends new URL to `remote_urls`."
34. **Example directive vs v0.1 behavior.**
    - §2.6 example: "accepted 12/15 of last cross-project recall offers; threshold tightened from 0.5 to 0.45." Lowering a threshold loosens it.
    - §2.6.1: only the two ack-modes are honored.
    - §3.2: cross-project is declined.
35. **Fixed parameters vs overrides.**
    - §2.6.1: "setting it in a directive file has no effect."
    - §3.9.3: "per-thread override is possible."
    - §4.2 `/no-revisit`: "tighten threshold."
36. **Protected symbols.**
    - §2.7.4: "a symbol that was ever central is protected from capacity trimming."
    - §2.3: protected only "while this protected set fits the cap."
37. **Who takes part.**
    - §1: "Personant does not define interaction modes… or how much the user takes part."
    - §2.6.1: `closure.ack-mode` and `recall.ack-mode` define exactly that.
38. **Ownership of opencode storage.**
    - §1: "opencode owns… session storage."
    - §1.7: requires that opencode's database not grow without bound and defines how "Cleanup of opencode's database" behaves.
39. **One write path.**
    - §1.8: "Every proposed change, whatever proposes it, goes through it."
    - §1.8: "Direct edits… are not prevented."
40. **Eviction brings content back.**
    - §1.4: evicted file content "can be brought back on demand."
    - §3.9.2: for files at or above the share threshold, "no mechanism yet brings its content back."
41. **Fresh store treated as old.**
    - §9.1: "`personant init`… never writes `version.toml`."
    - §9.1: "No revision → treated as revision 1… always 1." A freshly initialized current-layout store would therefore be "migrated" from revision 1.
42. **Release line for acceptance.**
    - §9.1 heading: "(v0.1 acceptance criterion)."
    - §9.1 Version lines: "v0.5.0 — realism convergence. The acceptance criterion above."
43. **Day-off vs decay.**
    - §9.4.1: a day-off's "idle span crosses the §3.5 wall-clock decay threshold." A day-off is about 24–34h.
    - §2.6.1: `engagement.decay-time: 7d`.
44. **Commit count.**
    - §3.11: "~365 commits/year."
    - §3.11 and §3.8.1: plus archive, capture, back-fill and adopt commits.
45. **Bytes vs chars.**
    - §2.6.1: "~2.5 conservative B/tok."
    - §3.1: "~2.5 chars/token."
46. **Content id vs prefix.**
    - §3.9.1: the line carries the "content id (a commit hash for `fs.commit`)."
    - §3.10.6: "a content-id or commit-hash prefix."

## 4. SPEC duties that cross a THESIS "It is not"

- **A. Model access → "inference client or provider configuration".**
  - SPEC: §8.2.1 "The memory service calls models itself"; option (b) is "a Personant provider pool"; §8.2.2 has `[embedding] model`, and `vectorLength` is "passed as the `dimensions` parameter on the embeddings call."
  - Why it crosses: that is provider configuration plus a direct API client, which the row excludes. The embedder cannot be routed through the host at all.
- **B. Model access / plugin → "inference client"; "harness… agent runtime".**
  - SPEC: §5.1 "a stream abort-and-re-issue, built per provider protocol"; §3.1 and §5.5 "aborts the in-flight stream… re-issues the request"; §3.3 re-prompts.
  - Why it crosses: per-protocol stream control and request retries are an inference client and agent-loop behavior. Which component does this rests on ARCHITECTURE, which I don't have.
- **C. Memory store → "mirror of the user's repository"; Career memory → "copy of past projects' code"; Content dedup → "version-control system".**
  - SPEC: §3.9.1 keeps the current bytes of every file read or written, plus diffs, and "The history repository keeps each day-commit's tracked-file store." §4.5.8 (5) calls that repository "career history… irreplaceable." Content ids are git blob ids.
  - Why it crosses: it is a permanent, day-grain versioned copy of the user's project code.
- **D. Durability → "transaction manager".**
  - SPEC: §4.5.8 has a "Turn transaction protocol," generic batch-op markers (`archival|sleep|recovery|barrier|rebaseline`), roll-forward completion across two repositories, and a 12-case recovery state machine.
  - Why it crosses: this goes beyond WAL plus reconcile into multi-step transaction management. This is my judgment.
- **E. Recall → "vector database or retrieval service".**
  - SPEC: §3.4 has a hierarchical coarse/fine embedding index, async incremental flush with stale-write guards, a persisted vector cache keyed by content hash, a summary tree, an exact lexical level, and a "no performance decay" requirement into thousands of threads.
  - Why it crosses: that is a purpose-built vector store with its own maintenance and scaling contract.
- **F. Thread lifecycle → "task tracker or workflow engine".**
  - SPEC: §2.2.1 states `paused`, `blocked`, `wip`, `resolved`, `decided`, `abandoned`, with `/pause`, `/resume`, `/done`. §3.5 adds an exception queue, drain points, `/closures` and "pending review" announcements.
  - Why it crosses: those are issue-tracker statuses plus a review queue, not decay timers.
- **G. Memory vetting → "approval workflow system".**
  - SPEC: §1.8 has acceptance modes ("manual acceptance, auto-accept on a clean review…"), pending proposals, provenance of "what accepted it," and periodic re-review.
  - Why it crosses: those are configurable approval states and a re-approval cadence. The SPEC's disclaimer "no approval gate… is built" does not remove them.
- **H. Model judgments → "free-form generation that decides what is stored".**
  - SPEC: §5.1.1 tag symbols are "folded into `history_symbols`" and §3.3 says "folded in directly"; they are open-vocabulary entities that drive anchors. §1.8 offers "auto-accept everything" for model-proposed directive text. §3.6 has the clusterer group content "into threads for retirement."
  - Why it crosses: the model's open output is written to the store with no validation beyond length or shape.
- **I. Event log → "telemetry or metrics backend".**
  - SPEC: §2.8 says the log records "every decision the §9 measurement regime… need[s]… an event absent from the vocabulary cannot be measured." §9.4 says "Recall-fidelity scoring reads the event log… tests pin the scraped event names."
  - Why it crosses: the log's schema is governed by metrics consumption. This is borderline.

Considered and not counted: CLI (`search`, `deps` and `log` are queries, but nothing is interactive) and directives (precedence plus parameters is not a policy engine).

## 5. Terms I could not resolve from the two documents

| Term | First appears | Guess |
|---|---|---|
| sub-mind split and re-merge | §1 | Forking child or subagent sessions and merging their memory back |
| Code Mode | §1 | An opencode feature (outside knowledge needed) |
| U3, U4, U5, U7, U8, U9 | §1 (U7) | IDs in an open-decisions list not provided |
| ROADMAP B6a | §3.9.2 | A ROADMAP item, not provided |
| "Unused → ARCHITECTURE «…»" | §2.2 | Section content moved out; heading kept to preserve numbering |
| spine / `SpineRecord` / `spine_summary` / `spine.match-fire` | §2.2 | Former name of the thread registry |
| Layer X / LayerB / `layer.b-top-k` | §2.6.1 | Former names for the slots (B = active-threads) |
| working window vs working set | §3.0 / §3.1 | Probably the same thing: the composed context |
| `steer` delivery | §3.0.4 | opencode mid-turn user message (outside knowledge) |
| bump-eviction | §3.0.2 | Evicting the oldest content to admit a new delta |
| engagement "threshold" | §3.0.2 step 2 | An unnamed overlap threshold; possibly `recall.symbolic-threshold` |
| surface threshold for cosine | §3.4 | Undefined; only auto-fetch thresholds are named |
| summary tree / summary-tree descent | §3.4 | Hierarchical summaries over long threads |
| exact lexical / flat-scan level | §3.4 | A grep-style scan over all excerpts |
| consolidation / "sleep" cycle / `op=sleep` | §1.8 | Offline maintenance pass; product trigger unclear |
| accrual | §2.6 | The feedback-tuning loop as a write source |
| `retire.curator-error` ("curator") | §3.5 | Former name of the summarizer |
| folio.jsonl | §3.8.1 | Packed file for older excerpts |
| warm window / tiers | §4.5.8 (5) | Probably the recent-excerpt window |
| legacy `files.json` | §4.5.8 (5) | Older tracked-file-store format |
| low-water mark; "registry size pressure" | §4.5.8 (5) / §3.5 | Undefined archival thresholds |
| single-clock day index; `Personant-Day: N` origin | §4.5.8 (5) | Days since some epoch; time zone unspecified |
| day-commit-shape commit | §4.5.8 (5) | A commit produced by the day-commit step, as distinct from back-fill or adopt commits |
| benign morning; stamp-only path | §4.5.8 (4) / (8) | First open of a day with the turn repo absent |
| turn scope | §4.3.3 | A non-empty journal |
| operational (file class) | §2.1 | Gitignored runtime state, neither authoritative nor derived |
| managed `.gitignore` block | §4.5.8 (5) | Section of `.gitignore` owned by Personant |
| standalone-kill claim / managed server / resume budget | §4.5.8 (10) Open | opencode server-mode concepts (outside knowledge) |
| probe-code retention; `model.MockServer` | §9.2 | Whether test-probe code stays in the build |
| non-sibling threads | §9.1 | Threads not from the same simulated topic family |
| TEXT-ONLY (byte shares) | §2.6.1 | Images and other non-text excluded from byte math |
| orientation preamble; fallback reminders | §3.1 | Fixed system-prompt scaffolding |
| matryoshka | §8.2.2 | Truncatable-dimension embeddings (outside knowledge) |
