# Personant: cold read of THESIS, SPEC and ARCHITECTURE

Everything below comes from the three documents. Where I relied on outside knowledge, I say so at that point. Section references are "ARCH" for ARCHITECTURE section titles and "SPEC §n" for SPEC sections.

## 1. What the system is made of

1. Personant has two runtime pieces under unmodified opencode:
   - a TypeScript plugin, which forwards opencode's hooks and events and applies the answers;
   - a Go memory service, which the plugin reaches over loopback HTTP+JSON. It is the single writer of memory, and opencode hook events drive it through turn phases.
2. On every model request the service replaces opencode's composition with one built from memory. The request is budgeted slots in priority order: directives, the thread registry, other-projects digests, active threads and dormant threads, plus slots for opencode's own content.
3. Memory is a store directory of text files (`~/.personant`):
   - authoritative files: the thread registry, per-thread directories of turn excerpts and tracked files, project metadata, directives and the event log;
   - derived files: indexes and a recall cache;
   - two git repositories over the same files: a disposable per-turn one and a permanent one with one commit per day. A journal and a reconcile at startup handle crash recovery.
4. Every content addition runs one per-delta pipeline: extraction, engagement, dedup, budget, logging. That pipeline assigns each turn to a thread (a problem, not a session). Recall surfaces older threads by symbol overlap (Jaccard) and embeddings. Models run only narrow judgments, requested through opencode.
5. A seeded simulation with a mock LLM is the acceptance instrument.

## 2. Contradictions

**Against THESIS or SPEC**

1. **The service gets its own model client.**
   - THESIS row "Model access": "the memory service never calls a model | An inference client or provider configuration". SPEC §8.2.1: "Personant holds no endpoint, provider configuration or API key."
   - ARCH "Module map": `internal/model/` is an "OpenAI-compatible `HTTPClient` … SSE streaming". ARCH "Model access and inference telemetry", U5 option (b): "a Personant provider pool under `~/.config/personant/`".
2. **Secret-bearing files.**
   - SPEC §2.1: "keeps no secret-bearing file".
   - ARCH "Storage non-negotiables", Configuration: "`providers.toml`: Open, U5 … secret-bearing files separated". ARCH "One inspection location": "whether `providers.toml` and `api_keys/` exist … (SPEC §2.1, §8.2.1)". It cites as open what those sections state as settled.
3. **Where the config file lives.**
   - SPEC §2.1: "Personant-only settings live under `~/.config/personant/`".
   - ARCH "One inspection location": "`internal/store/paths.go` still places `config.toml` and `providers.toml` under the store directory". ARCH flags this drift itself.
4. **The tracked-file store in the permanent repository.**
   - SPEC §3.9.1: "the history repository never holds it".
   - ARCH "Crash recovery…": completion's repair "quarantine[s] the thread's `files.jsonl` and `files/` together and restore[s] both from history repository HEAD". ARCH "Tracked-file store" flags this as out of date.
5. **Archived threads.**
   - SPEC §4.5.8 (7): "History-repository loss — unrecoverable". SPEC §3.8.1: archived thread bytes are removed from the working tree and survive only in git.
   - ARCH "Crash recovery…": "No authoritative byte ever lives *only* in git, so either git repository can be recreated from the store working tree and losing one is never losing content." The same ARCH section rules out recreating the history repository.
6. **Which checks run before and after a git write.**
   - SPEC §8.3: before **and** after each git operation that changes state, check "derived files are fresh … and … structurally sound".
   - ARCH "Closing a thread": "the deletion commit runs `CheckSpineIntegrity` before and `CheckDerivedFresh` after".
7. **Cross-project recall.**
   - SPEC §3.1: "A fetched thread belonging to another project is declined at the fetch seam". SPEC §3.2: "cross-project engagement is not built in v0.1 and is declined".
   - ARCH "Recall paths", item 3: "Cross-project recall (other-projects slot → fetch) … scanned each turn". ARCH "Many projects, one career": "Cross-project recall is designed (recall path 3), not emergent". It is not marked unbuilt.
8. **What `turn_count` counts.**
   - SPEC §3.0.4: "`turn_count` increments by 1 only for the turn's owner thread".
   - ARCH `SpineRecord` comment: "total turns this thread has been engaged in".
9. **Where the topic tag may appear.**
   - SPEC §5.1.2: "a tag-shaped line beyond the window is not recognized".
   - ARCH "Topic-tag mechanics": the bare alias is accepted "by the stream filter's Close-time trailing-line fallback".
10. **When directives are read.**
    - SPEC §8.2.4: the service "walks the precedence chain at parameter-read time".
    - ARCH "The composed request and its slots": the two honored keys are "resolved by directive precedence at the start of a service run".
11. **A failed summary draft.**
    - SPEC §3.5 ("/done…"): a failed draft "is retried at the next scan … leaves its thread un-closed rather than queued".
    - ARCH "Symbol extraction", pass 3: a thread "whose draft failed, is queued as an exception". SPEC §3.5's classification list also says this, so SPEC disagrees with itself here.
12. **Career memory.**
    - THESIS row "Career memory": "Notes on techniques and solutions … drawn from the record". SPEC §1.8 routes a single-project lesson to "career memory (a technique)".
    - ARCH "Memory vetting": "Career-memory protection uses the slots, the turn and history repositories and consolidation". That treats the thread record as career memory.
13. **How a thread becomes dormant.**
    - SPEC §3.1 LRU rule: a thread is demoted to the dormant set when the active set exceeds `layer.b-top-k`. Decay closes threads (§3.5).
    - ARCH "Thread lifecycle", stage 3: "Dormant … as engagement decays".
14. **Thread definition.**
    - SPEC §1.1 and §3.2: turns are assigned by problem, and threads rest and re-engage.
    - ARCH "Key abstractions": a thread is a "**contiguous** run of turns".
15. **Session commands.**
    - SPEC §4.2: the command table.
    - ARCH "Integration shape", U4 list: it adds `/keep`, `/threads`, `/spine` and `/index-rebuild`, which SPEC never defines.
16. **Project names.**
    - SPEC §4.5.2: the display name is a "user-chosen string".
    - ARCH `ProjectMeta.name`: `/^[a-z][a-z0-9-]*$/`.
17. **Classification of the recall cache.**
    - SPEC §3.4: "a **derived**, persisted cache (§2.1 `.recall-cache/`)".
    - ARCH "Store directory layout": `.recall-cache/` is "operational … not drift-checked".
18. **Replay (follows SPEC, conflicts with THESIS).**
    - THESIS "What is new" 1: "Every request is composed, not replayed".
    - ARCH "Integration shape": the answer includes "the transcript tail". SPEC §3.1 has "replayed transcript tail" too.

**ARCHITECTURE against itself**

19. **What the anchor projection ranks.**
    - ARCH "Anchors and symbol history": "projection of the thread's *active* history symbols". "Key abstractions" says the same.
    - ARCH "Projection": "over all retained history symbols, active and superseded alike". This matches SPEC §2.7.4, "retained symbol history".
20. **Anchor length.**
    - ARCH verify constraint: "`anchors[i]` … 3–50 chars".
    - ARCH "Projection": a "high-specificity identifier" (URL, file path) ranks first. Many URLs and paths are over 50 characters, so the projection can produce anchors verify rejects.
21. **Stale derived files and git writes.**
    - ARCH "Storage non-negotiables": "`CheckDerivedFresh` fails any state-changing git op on stale".
    - ARCH "Automatic vs model-controlled": the checks "are applied to history-repository writes; turn-repository commits run none". SPEC §2.1 sides with the first quote; SPEC §3.11 sides with the second.
22. **Where `Normalize` lives.**
    - ARCH "Module map": `Normalize` and `DominantSource` are in `memops`, which "imports no internal package".
    - ARCH "Port-and-adapter": "What stays direct from `internal/store` … pure helpers (`Normalize`, `DominantSource`)".
23. **Prototype paths.**
    - ARCH "Tense": the prototype's paths "resolve [on `probe-path`] … not on the build branch".
    - ARCH "Module map" lists `opencode-plugin/`, `internal/opencodehook/` and `probe_phases.go` as packages in the tree. ARCH "Pinned release": the release is stated "in one place: the version check in the `sim-opencode` recipe", which is itself a prototype recipe.
24. **Ownership of opencode's database.**
    - ARCH "Boundary": "Outside context management, Personant uses what opencode has chosen, as it is."
    - ARCH "opencode database cleanup": it deletes rows directly in opencode's SQLite and runs `VACUUM`. SPEC §1 ("opencode owns … session storage") against §1.7 is the same split.
25. **How archival removes threads.**
    - ARCH "Closing a thread": "via `git rm` + `git commit`".
    - Same section, next paragraph: "removals are staged by `os.RemoveAll` plus `autogit.Add(".")`".
26. **The plan for a derived index.**
    - ARCH anti-patterns: "SQLite stays in reserve as a *derived* index".
    - ARCH "Storage evolution": the reserve is bbolt, "SQLite only if queries become SQL-shaped".

## 3. Parts built toward their THESIS "It is not" column

1. **Model access → "an inference client or provider configuration".**
   - ARCH quotes: `internal/model/` "OpenAI-compatible `HTTPClient` … SSE streaming"; U5 (b) "a Personant provider pool"; `providers.toml` and `api_keys/` left open.
   - Why it crosses: this is exactly the client and provider configuration the row excludes.
2. **The plugin → "a harness … or agent runtime".**
   - ARCH quote ("No speculative prefetch", U3): the in-band-tag option needs the plugin to "read the head of the streaming response in the `session.http.response` hook, cancel upstream, re-issue, return the second stream", with "per-protocol wire code in the plugin".
   - Why it crosses: the plugin would be steering the model stream per provider, not just forwarding.
3. **Memory store → "a mirror of the user's repository". Career memory → "a copy of past projects' code".**
   - ARCH quote ("Tracked-file store"): the current code commits `files/<id>` (current file bytes) and kept diffs to the permanent history repository.
   - Why it crosses: user code accumulates in permanent, cross-project history. ARCH marks this as current code, not the intent.
4. **Recall → "a vector database or a retrieval service". Indexes → "a search engine".**
   - ARCH quotes ("Recall paths" 4): hierarchical coarse and fine vectors, a summary tree with "O(log n) beam descent", "a single background indexer that swaps an `atomic.Pointer` index snapshot", persisted `.vec`/`.tree` sidecars, plus a separate "exact lexical (flat-scan) recall level".
   - Why it crosses: that is a small vector database and search engine inside the service. This is my judgment. SPEC §3.4 requires much of it.
5. **Event log → "a telemetry or metrics backend".**
   - ARCH quote: `model.inference-telemetry` with "`ttft_ms=`, `prefill_ms=`, `gen_ms=` … `prefill_tps=`, `gen_tps=`", and the log is "the memory service↔harness measurement contract".
   - Why it crosses: provider performance telemetry is not a memory decision. ARCH concedes it may be retired.
6. **Durability → "a transaction manager or a replication system".**
   - ARCH quote ("Crash recovery…"): two git repositories tracking one working tree, a "turn transaction", an `op=barrier` re-entry token, roll-forward completion, a 12-case state machine.
   - Why it crosses: it goes past journal plus commit points plus one reconcile. This is my judgment; most of it is SPEC §4.5.8.
7. **Memory service → "a network server, multi-tenant service".**
   - ARCH quotes: loopback HTTP, and "Several opencode servers sharing one service: see 'Concurrent sessions'".
   - Why it crosses: the loopback listener has a stated reason. Serving several hosts moves toward multi-tenant. This one is borderline.

## 4. Unstated departures from the standard pattern

1. **"Human-readable text files" (memory store).**
   - `folio.jsonl` stores excerpts as JSON strings "with default HTML escaping", so `<` and `&` become `\u003c`/`\u0026`, and newlines are escaped.
   - `logs/archive/*.tar.gz` is gzipped.
   - Both work against the "One inspection location" claim, "`grep -r`". Neither the loss of readability nor its reason is stated. (The escaping behavior is outside knowledge of Go's JSON encoder.)
2. **"Derived index files, rebuildable" (indexes).**
   - `anchors` and `anchors_projected_at_turn` are marked "[derived]" but live inside the authoritative, hand-editable `spine.jsonl`.
   - `thread_count` is "[derived]" inside authoritative `meta.json`.
   - No reason is given for mixing derived fields into authoritative files.
3. **"A stateless adapter" (plugin).**
   - "Sequence numbers are taken in the plugin", and it keeps a "fail-open count".
   - A reason is given for the sequencing, but it is never named as a departure from statelessness.
4. **"A state machine with decay timers" (thread lifecycle).**
   - Decay "keys off `TurnNumber − LastEngagedTurn`", and "the turn counter … reset[s] on every relaunch" ("Start order and resume").
   - So the decay timer is not durable across restarts. ARCH notes the reset only for checkpoint cadence.
5. **"A closed question, or drafts text that deterministic code validates" (model judgments).**
   - Topic-tag symbols are "folded in directly". A routine close "takes the draft as written".
   - The only stated validation is normalization or length. ARCH "The model drafts, deterministic code writes" claims validation without naming any.
6. **"Instruction files layered by precedence" (directives).** Precedence is resolved once per service run (finding 2.10), with no reason stated.

## 5. Terms I could not resolve from the documents

| Term | First appears in ARCH | Guess |
|---|---|---|
| sub-mind split and re-merge | "What this is" | forking subagent or child sessions and merging their memory back |
| Code Mode | "Integration shape" | an opencode feature for running code or tool scripts |
| Effect defect | "Failure handling" | an unrecoverable error in the Effect library (outside knowledge) |
| `steer` prompt | "Hook-driven turn phases" | a user message injected while a turn is running |
| live-turn reject; pre-event deltas / `PreEvents` | `BeginTurn` row | refusing a prompt that already exceeds the live-turn reserve; harness-injected file deltas before the prompt |
| transcript tail (its extent) | "Integration shape" | recent opencode messages replayed; length unstated |
| bitflag policy, "64 slots" | "Module map", "Automatic vs model-controlled" | a 64-bit check mask |
| embedding debt, `EmbeddingDebtCap`, debt window | "Excerpt storage tiers" | the unembedded backlog |
| warm window / `HotExcerptTail` | "Excerpt storage tiers" (SPEC: warm) | the recent-excerpt window plus the backlog limit (528) |
| C_main, beam descent | "Recall paths" | chunk count of the engaged thread; top-k tree search |
| `LayerB`, "Layer X", `layerb_*`, E/A1/A2/B/C | "Recent-excerpt window" | legacy slot names; B = active threads |
| curator, retire, spine, accrual | "Module map", event table | old names for summarizer, closing, thread registry, and feedback directive tuning |
| sleep, barrier, reborn, morning-init, benign morning, stamp / stamped, cells, re-mint | event table, "Crash recovery…" | consolidation; the day-rollover marker; re-creating the per-turn repository; back-fill; recovery cases |
| measurement-carrier displacement; `rtActive` | "State-ownership map" | harness probe threads displacing threads in the LRU; the harness's copy of the service's active set |
| execution-time refinement | "State-ownership map" | extra steps (re-prompts, acks) that advance the clock during execution |
| `W1-diag`, `net-cap-hit` | event table | unknown diagnostic; the surfaced-candidate cap was reached |
| "the 30-minute check interval" | "Acceptance-run mechanics" | a CI or test time budget |
| managed server, standalone-kill claim, resume budget | "Start order and resume" | opencode run modes and a retry allowance (unknown) |
| "signed" commits | "Automatic vs model-controlled" | author/committer identity, not GPG |
| "new-thread summary length bound" | "description vs summary" | an unstated truncation length |
| "the shipped files" | layout table | `README.md`, `defaults.md` |
| v2.0 "locked" | "Concurrent sessions" | a version held out of scope |
| U3–U10, A12a; ROADMAP A2, A3, A5, B6a | throughout | IDs from open-item and roadmap registers that are not in the input; U10 appears only in ARCH |
