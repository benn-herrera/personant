# Personant: a cold read of THESIS and SPEC

These answers come from the two documents only. Where an answer relies on general knowledge or on documents I don't have (ARCHITECTURE, opencode's behaviour), I say so at that point.

## 1. What it does and doesn't do

Personant is a memory layer for one person's coding agent running under unmodified opencode. A plugin forwards each model request to a local single-writer memory service. That service rebuilds the request from durable memory: directives, a thread registry, active threads and recalled threads. It does not replay a session transcript or compact it.

Work is recorded by **thread**, meaning by problem rather than by session. Threads gain symbols, decay, close with a model-drafted summary, archive into git, and can be found again through symbolic and embedding recall. All storage is plain text in git with journaled crash recovery.

Personant does not provide the UI, tools, permissions, providers, session storage or interaction modes; those belong to opencode. It also does not mirror the user's repository, and it lets a model decide only narrow, validated things.

## 2. Expected parts and their ordinary counterparts

The comparisons in this list come from general engineering knowledge, not from the documents.

| Part | Most resembles |
|---|---|
| opencode plugin | Editor/host extension that hooks requests (like an HTTP middleware) |
| Memory service | Local daemon with one writer, like a language server |
| Memory store (`~/.personant`, plain text + git) | Git-backed flat-file data directory, like a dotfiles repo |
| Turn journal + startup recovery state machine | Write-ahead log with redo recovery, like SQLite WAL or fsck-at-boot |
| Two git repositories (turn repo and history repo) | Short-lived local snapshots plus a daily long-term snapshot |
| Symbol index, digests, embedding cache | Rebuildable secondary indexes / materialized views |
| Symbol extraction + normalization | Regex tokenizer plus canonicalization, like ctags |
| Topic-tag parser | Line-anchored structured-output parser |
| Recall (Jaccard + cosine + thresholds) | Hybrid keyword and vector similarity search, in-process |
| Composer (budgeted slots) | Prompt template assembler with per-section quotas, like a CSS box-layout pass |
| Working set (active and dormant sets) | LRU cache |
| Tracked-file store + content ids | Content-addressable blob store, like git objects or an rsync delta |
| Thread lifecycle | State machine with TTL expiry |
| Staging buffer / citation window | Time-windowed promotion buffer, like generational GC |
| Summarizer / clusterer / recall judgment calls | Single-shot LLM classification or summarization calls |
| Cold storage (archive index + git) | Archive tier with a manifest, like `git archive` plus an index |
| Event log | Rotated, append-only structured log, like syslog |
| CLI `personant` | Maintenance subcommand tree, like `git fsck` or `brew doctor` |
| Directives | Layered config with precedence, like `.editorconfig` or git config scopes |
| Vetting write path | Pre-commit review queue with an audit trail |
| Project identity resolver | Workspace detection by git remote or path, like an IDE's project root detection |
| Format revision + migrations | Schema migration chain, like Alembic or Rails migrations |
| Redactor | Secret scrubber, like log-masking filters |
| Simulation harness + mock LLM | Deterministic seeded load/soak test with a stubbed dependency |

## 3. Contradictions

### SPEC against THESIS

**T1. "Composed, not replayed."**
- THESIS ("What is new" 1): "each request is assembled afresh from memory… nothing depends on a session's length."
- SPEC §3.1: "The whole request — system prompt + **replayed transcript tail** + current user input…"
- SPEC §3.9: "a transcript row is dropped from the request **once memory has captured it**". This means rows are replayed until they are captured.
- SPEC §1.4 also says "Where replay cannot reproduce the content".

**T2. Model access.**
- THESIS table, Model access: "Calls made through the host."
- SPEC §8.2.1: "The memory service calls models itself… opencode has no embedding provider, so the embedder needs Personant configuration… whatever else is decided."

**T3. Across projects with no boundary.**
- THESIS: "work picks up… across projects as if there had never been a boundary."
- SPEC §3.2: "cross-project engagement is not built in v0.1 and is declined".
- SPEC §3.1: "A fetched thread belonging to another project is declined at the fetch seam."

**T4. "Stays findable afterwards."**
- THESIS ("What is new" 2): a problem's record "stays findable afterwards."
- SPEC §3.8.4: "Archived threads are **never candidates for live recall** in v0.1." (See also S1.)

**T5. Indexes are derived.**
- THESIS table, Indexes: "Derived index files, rebuildable from the canonical files."
- SPEC §3.8.1: "The archive index is **authoritative** (§2.1)."

**T6. What counts as behavior memory.**
- THESIS: behavior memory is "how the agent thinks and conducts itself."
- SPEC §2.6: "A change proposed to the defaults or user-wide directive file… is a behavior-memory change". Those files hold Personant's own operating parameters (`recall.ack-mode`, thresholds), not the agent's conduct.

**T7. Personant does not define user participation.**
- SPEC §1: "Personant does not define interaction modes, a workflow, or how much the user takes part."
- SPEC §2.6.1, §3.4 and §3.5 define `closure.ack-mode: auto | always` and `recall.ack-mode: banded | always`, plus drain points and closure dialogs. These set exactly how much the user takes part.

### SPEC against itself

**S1. Recall completeness.**
- §3.4 invariant: all non-transient content "MUST be locatable by recall at every stage… archival (§3.8)"; "The ONLY content that may be unfindable is transient data."
- This conflicts with §3.8.4 (archived threads are off-recall).
- It conflicts with §3.3: an untagged conversational turn gets "no excerpt, no owner", so its decision-class content is never stored.
- It conflicts with §3.4's own "Accepted residual: a single transient embed failure leaves a gap for that batch."

**S2. "Run in parallel."**
- §3.4: "Three signals, run in parallel, not a cost cascade."
- Signal 3 is "Model judgment **on surfaced candidates**", which runs after the other two. Banding then says "signals 2/3 are cosines", but signal 3 is not a cosine.

**S3. One recall path.**
- §3.4: "there is no separate intra-thread path."
- The same section later says: "the automatic intra-thread path searches…" and logs `spine.intra-match-fire` separately.

**S4. File content transient, yet stored.**
- §3.10.6: "**Never** written to the memory store: Raw bytes of task-class deltas (file content from `fs.read`/`fs.write`…)".
- The same section and §3.9.1 say: "File content has a separate persistent home in the per-thread tracked-file store."

**S5. Every path runs the pipeline.**
- §3.0 / §3.0.5: "every path by which content enters the working window runs the per-delta pipeline… there is no bypass."
- §3.0.1: "Other-projects digest content, memory-command effects… and directive-file reloads are not pipeline deltas."
- The opencode instructions, expectations and tool-definition slots (§3.1) have no delta type either. This is a contradiction if "working window" means the composed request.

**S6. The delta list is complete.**
- §3.0.1: "These are all the deltas."
- §3.0.3: "Eviction is itself a delta."

**S7. What dedup matches on.**
- §3.0.3: "extracted identifier symbols are what dedup matches against."
- §3.9.1: the content id "is computed from its bytes alone."

**S8. `description` never rewritten.**
- §2.2: "set once at creation and never rewritten"; §2.3 says the same.
- §4.2 `/topic rename`: "Writes `description`".
- §2.3 sets `description` to "a whitespace-collapsed copy of the spawning prompt", but `/topic <name>` sets it to a user-chosen name.

**S9. Live threads have no summary.**
- §2.3: "a live thread has no `summary`."
- §4.2: "`summary` when it duplicates `description` — the shape `/topic` creation leaves."

**S10. Exception classification needs a draft that is never made.**
- §3.5: a thread is an exception if "the summarizer draft is empty or failed", yet an exception closure gets "no summarizer call."
- The same section also says "A draft that fails… is retried at the next scan… leaves its thread un-closed rather than queued."

**S11. "Engaged turns" counted with an owner-only counter.**
- §3.5: "long-engaged (≥ 15 engaged turns, `turn_count`)".
- §3.0.4: "an engaged-but-not-owner thread's `turn_count` is unchanged."

**S12. Summarizer as a symbol source.**
- §2.7.3 lists the summarizer as a symbol source.
- §3.3 pass 3: the summarizer "does **not** select anchors" and only drafts a summary. It is also called a per-delta pass (§3.0.2 step 1) but runs only at retirement.

**S13. Is the registry authoritative or derived?**
- §4.5.8: "authoritative files (`spine.jsonl`, thread files, logs)".
- §4.2: "the derived thread registry record".

**S14. What kind of file `last-active` is.**
- §4.5.7: `last-active` is "operational".
- §4.5.8: it is "derived state."

**S15. Archival only at rollover.**
- §3.8: "The archival *drain* fires **only at the day rollover**."
- §4.5.8(6): "standalone archival remains available outside the rollover."

**S16. History-writer rule.**
- §4.5.8(5): "written only at the day rollover, with **one** recovery-repair exemption."
- The "exhaustive" list that follows has three writers outside the rollover: case-9 back-fill, case-12 adopt, and roll-forward of standalone archival.
- The lifecycle git tags, which are also written to the history repository, are not in the list.

**S17. Hand-edits never destroyed.**
- §2.1: "A user's direct edit… is never destroyed by crash recovery: it is absorbed (case 3)."
- §4.5.8 case 4 resets "the store working tree to the turn repository's HEAD". Hand-edits are committed only at a full staging pass (§4.5.8(2)), so a torn turn discards any hand-edit not yet captured. Quarantine is promised only for untracked debris.

**S18. Retraction leaves no turn scope.**
- §4.3.3: before the first authoritative write, an interrupt leaves "no open §4.5.8 turn scope, so the next start opens quiet."
- §4.5.8(2): the prompt is journaled "before the model call. The first append arms the in-flight signal." The rollback list in §4.3.3 does not include truncating the journal.

**S19. Derived files stale during git operations.**
- §2.1: "a state-changing git operation on the memory store fails while any derived file is stale."
- §8.3: validate "derived files are fresh" before each operation on `.git/`.
- §4.5.8(7): "Intra-day derived staleness — by design… turn path never sets the watermark."
- §3.11: turn commits carry "no integrity validation."
- The day-commit step also runs before the derived-rebuild step.

**S20. When the turn repository is born.**
- §4.5.8(8): "a turn repository is born only alongside a fresh history repository."
- §4.5.8(5): it is "deleted and recreated at each day rollover", and there is a "benign morning → turn-repository init" row.

**S21. Cost of turn-repository init.**
- §4.5.8(5): turn-repository init is "O(active working-set bytes)".
- It "baseline-commit[s] the store working tree", which holds all non-archived threads, not just the working set.

**S22. Never rewrites git history.**
- §3.8.2: "Personant never rewrites git history."
- §3.11: the turn repository "is deleted and recreated at each rollover", plus there is a mid-day re-baseline. This holds only if "history" means the history repository alone.

**S23. Project resolution, two versions.**
- §4.5.7: "The two directory-matching steps **are** the resolution of §4.5.3."
- They differ on which remote is read:
  - §4.5.3 reads "`origin`… otherwise none".
  - §4.5.7 reads "`origin`… (or the first remote if no `origin`)".
- They also differ on where the lookup goes:
  - §4.5.7 also searches `historical_remote_urls`.
  - In §4.5.3, a remote that matches nothing goes straight to the question without trying a path match.

**S24. Directive parameters.**
- §2.6.1: "v0.1 honors only `closure.ack-mode` and `recall.ack-mode`… setting [others] has no effect."
- §2.6's example records a cross-project threshold "tightened from 0.5 to 0.45". Lowering a threshold loosens it, and cross-project fetch is declined in v0.1.
- §4.2 `/no-revisit` "tighten[s] threshold".
- §3.9.3 says "per-thread override [of diff format] is possible."

**S25. Evicted file content can be brought back.**
- §1.4: evicted file content "can be brought back on demand."
- §3.9.2: for files at or above the share threshold, "no mechanism yet brings its content back."

**S26. Keys never reach model context.**
- §8.2.1: a key is "**never** included in any LLM context."
- The redactor covers only `tool.result`, `fs.read`, `fs.write` and `user.shell-capture`, and "keys under 8 bytes are not scanned for." A key pasted into a `user.prompt` reaches the model.

**S27. Consolidation in or out of v0.1.**
- §3.10.8: consolidation keep/toss is "outside v0.1 scope."
- §1.8 relies on "batch content review in consolidation" to protect threads, and §3.11 relies on consolidation for packing.

**S28. Anchor protection.**
- §2.7.4: a once-central symbol "is protected from capacity trimming."
- §2.3: protected only "while this protected set fits the cap."

**S29. Registry sort order.**
- §2.2: the registry is "a single file sorted by id."
- §3.8.3 step 4: "Append a fresh thread registry record" with the restored, older id.

**S30. Version naming.**
- §9.1: "The v0.1 acceptance criterion."
- §9.1 later: "Memory service **v0.5.0** — realism convergence. The acceptance criterion above."

**S31. Fresh stores and the format revision.**
- §9.1: init "never writes `version.toml`"; "No revision → treated as revision 1… always 1."
- A store freshly created by a binary at revision N>1 is therefore misread as revision 1 and migrated.

**S32. Sunday idle span and decay.**
- §9.4.1: the Sunday idle span "crosses the §3.5 wall-clock decay threshold."
- That span is about 34.5 h (Saturday ~21:30 to Monday 08:00), but `engagement.decay-time: 7d`.

**S33. Startup order.**
- §4.5.8: "init → startup recovery → (day rollover) → service-run load."
- §9.1: "format check → startup recovery → service-run load."

**S34. Number of test levels.**
- §9.4: "Four test levels — scenario, churn, calibration, and the acceptance simulation."
- §9.3: "Unit testing is level 1."

**S35. `/compact` and `/clear`.**
- §1.1: "**Open:** what `/compact` and `/clear` eventually map to."
- §4.2 treats it as decided: "`/compact` and `/clear` are opencode's, and neither discards memory."

**S36. Overflow guarantee.**
- §9.1: "Zero out-of-context-space events… every §3.1 slot, including tool definitions."
- §3.1: "Without a tool-definition slot the whole-request ceiling above cannot be enforced"; that slot's budget is Open. The §2.6.1 percentages leave no share for the three opencode slots.

**Gap rather than contradiction:** THESIS names **career memory** as one of three kinds, "recalled by relevance". SPEC never gives it storage or a recall path; §3.4 recalls threads only.

## 4. SPEC requirements that cross a THESIS "It is not"

Strength is my own judgment.

**C1. Model access** (is not "an inference client or provider configuration"). Clear.
- §8.2.1: "The memory service calls models itself".
- §8.2.2: `[embedding] model`, `vectorLength`, "passed as the `dimensions` parameter on the embeddings call".
- §5.1 (U3): "stream abort-and-re-issue, built per provider protocol."
- Why it crosses: Personant's own provider config and per-protocol call handling are an inference client.

**C2. Memory store** (is not "a mirror of the user's repository") and **Career memory** (is not "a copy of past projects' code"). Clear.
- §3.9.1: the tracked-file store holds user file bytes plus diffs.
- §3.9.1 also: "The history repository keeps each day-commit's tracked-file store."
- Why it crosses: user project file content is kept permanently in Personant's own version control.

**C3. Model judgments** (is not "free-form generation that decides what is stored"). Clear for the clusterer; strong for the rest.
- §5.1.1: model tag symbols are "folded into `history_symbols`" with no validation beyond normalization.
- §2.7.1: "entities (model-emitted named concepts)" are stored.
- §5.3: the clusterer "asks for a small number of clusters with brief rationale", an open question whose answer decides what gets retired.
- §3.5: a routine summary is auto-accepted with only a non-empty check.

**C4. Plugin** (is not "a harness… or agent runtime, or a second writer"). Clear.
- §3.1 / §5.5: abort the in-flight stream and re-issue, up to 4 streams per turn with injected reminders. This is agent-loop control.
- §5.1: strip the tag from the text "opencode stores and shows".
- §1.7: cleanup of opencode's database.
- SPEC assigns these to the memory service. Concluding that the plugin must carry them is my inference from the THESIS row that the plugin is the only adapter to the host. Either way they cross §1's "opencode owns… session storage".

**C5. Durability** (is not "a transaction manager"). Strong.
- §4.5.8: a "Turn transaction protocol"; batch op markers (`archival`, `sleep`, `recovery`, `barrier`, `rebaseline`); a 12-case recovery table; roll-forward completion with re-entry tokens; back-fill repair.
- Why it crosses: this goes well past "journal plus commit points plus one idempotent reconcile at startup."

**C6. Recall** (is not "a vector database or a retrieval service"). Arguable.
- §3.4: a coarse/fine hierarchical vector index, a persisted vector cache keyed by content hash, async flush with backlog limits, "summary-tree descent", an "exact lexical / flat-scan level", and a guarantee of "no performance decay" into the thousands.

**C7. Thread lifecycle** (is not "a task tracker or a workflow engine"). Arguable.
- §2.2.1: `blocked` / `wip` / `resolved` / `decided` / `abandoned` states.
- §3.5: an exception queue, drain points, `/closures`, and an ack dialog with `[e]dit`.

**C8. Memory vetting** (is not "an approval workflow system"). Arguable; SPEC explicitly disclaims gates.
- §1.8: pending proposals, "manual acceptance / auto-accept on a clean review", provenance of "what accepted it", and periodic re-review.

**C9. Event log** (is not "a telemetry or metrics backend"). Arguable.
- §2.8: "an event absent from the vocabulary cannot be measured."
- §9.4: "Recall-fidelity scoring reads the event log."
- §4.5.8: git tags are derived from the log.

**C10. Cold storage** (is not "a backup system"). Weak.
- §4.5.8: the history repository takes a full-store day commit, is "irreplaceable", and its loss means refuse-to-open.

**C11. Symbol extraction** (is not "an entity linker"). Weak.
- §2.7.3: per-symbol `derived_from` links symbols to source threads.
- §2.7.1: model-extracted entities.

## 5. Terms I could not understand

| Term | First appears | My guess |
|---|---|---|
| sub-mind split and re-merge | §1 | Running a subagent (child session) with its own composed context, then merging its record back |
| Code Mode | §1 | An opencode feature (outside knowledge needed): the model writes code that calls tools |
| U3, U4, U5, U7, U8, U9 | §1 (U7) | IDs in an open-decisions register I don't have |
| `steer` delivery | §3.0.4 | opencode injecting a user message mid-turn (outside knowledge) |
| opencode's checkpointing | §1.7 | opencode marking a point after which old rows aren't re-read (outside knowledge) |
| reminders still in force; background-job results | §1.3 | opencode system reminders; output of detached shell jobs |
| working window vs working set | §3.0 / §3.1 | Possibly the same thing (the composed request); possibly window = live turn and set = memory slots |
| spine / `SpineRecord` / spine entries | §1.4, §2.2 | Legacy name for the thread registry |
| E, A1, A2, B, C, Layer X, `LayerB` | §2.6.1 | Legacy slot names; the mapping is given only partly |
| `folio.jsonl` | §3.8.1 | Per-thread packed file of older excerpts |
| warm window; tiers | §4.5.8(5) | Probably the recent-excerpt window; storage as individual files vs the folio |
| summary tree / summary-tree descent | §3.4 | Hierarchical summaries of a long thread used for approximate intra-thread search |
| exact lexical / flat-scan level | §3.4 | Linear text scan over all excerpts |
| retire / retirement | §2.2 | Same as closure |
| curator (`retire.curator-error`) | §3.5 | The summarizer |
| accrual; recall accrual loop | §2.6, §4.3 | Feedback directive tuning |
| consolidation / "sleep" cycle | §1.8 | An offline batch job, run on idle days, that reviews and packs memory; its scope is unclear |
| `lifecycle` frontmatter field | §3.4 | Per-symbol active/superseded label |
| `anchors_projected_at_turn` | §3.2 | Turn index of the last anchor change |
| thread registry size pressure; low-water mark | §3.5, §4.5.8 | Undefined thresholds that trigger archival |
| single-clock day index | §4.5.8(5) | Days since some epoch; the time zone is unstated |
| day-commit-shape commit | §4.5.8(1) | A commit made by the day-commit step, as opposed to a back-fill or adopt commit that also carries a trailer |
| benign morning | §4.5.8(4) | Turn repository missing but watermark present |
| managed `.gitignore` block | §4.5.8(5) | A section of `.gitignore` that Personant owns |
| standalone-kill claim swept by a managed server; resume budget | §4.5.8(10) | No confident guess; probably opencode server-mode process management |
| the runtime | §4.5.5 | The memory service |
| fallback dissection | §3.6 | Emergency batch retirement under budget pressure |
| orientation preamble | §3.1 | Fixed intro text in the system prompt |
| day-off idle window | §3.11 | A simulation concept carried into a product rule; perhaps "idle day" |
| adversarial probes; `superseded_precision`; `abandoned_premise_recall`; flush-lag findability-gap probe | §9.4, §3.4, §9.1 | Simulation metrics and injected test queries, defined in ARCHITECTURE or the harness |
| metronomic edit cadence | §9.1 | Regular, periodic edits to the same files |
| `model.MockServer`; probe-code retention; ROADMAP B6a | §9.2, §3.9.2 | Code symbol, retention of test-probe code, and a roadmap item; none available to me |
| ARCHITECTURE "Plugin route", "Spine entries are the recognition surface", "Testing as the lab bench" | §1, §1.4, §9.3 | Sections of a document I don't have |
