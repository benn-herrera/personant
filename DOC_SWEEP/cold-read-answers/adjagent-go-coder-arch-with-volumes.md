# Personant: reading of THESIS, SPEC and ARCHITECTURE

Below, "ARCH" means ARCHITECTURE. Section names in «» are ARCHITECTURE headings, and § numbers are SPEC sections. Where an answer rests on anything outside the three documents, it is marked there.

## 1. What the system is made of

1. Personant has two pieces under unmodified opencode:
   - a stateless TypeScript plugin, which forwards opencode hook inputs and events;
   - a Go memory service, which it reaches over loopback HTTP + JSON. The service is the single writer of memory and composes every model request.
2. The service maps hooks onto turn phases (`BeginTurn` … `CloseTurn`). These feed every content addition (a "delta") through one per-delta pipeline: symbol extraction, engagement, dedup, budget check, logging.
3. Memory is plain text under `~/.personant`:
   - a thread registry (`spine.jsonl`);
   - per-thread directories of excerpts and tracked-file content;
   - derived indexes;
   - an event log.

   All of it is protected by a write-ahead journal and two git repositories: a disposable per-turn one and a permanent per-day one.
4. Each request is rebuilt from memory into budgeted slots: directives, registry, other-projects, active threads, dormant threads, and three opencode-content slots. Threads move through a decay-driven lifecycle, are found again by symbolic and embedding recall, and are archived to git-backed cold storage.
5. Around this sit:
   - a maintenance CLI;
   - startup crash recovery;
   - a mock-LLM simulation harness that serves as the acceptance instrument.

## 2. Contradictions involving ARCH

**Model access and configuration**

1. **The service calling a model.**
   - ARCH says:
     - «Model access and inference telemetry»: "(b) Through a Personant provider pool under `~/.config/personant/`".
     - «Module map» `internal/model/`: "OpenAI-compatible `HTTPClient`".
     - «Acceptance-run mechanics»: "Live run configurations use `reaper.local` (gemma-4 family inference…)".
   - SPEC §8.2.1 says: "The memory service never calls a model… Personant holds no endpoint, provider configuration or API key". The THESIS Model access row says the same.
   - SPEC is split on this itself: §5.2, §5.3 and §1.8 still list U5 as open.
2. **Secret-bearing files.**
   - ARCH says:
     - «Storage non-negotiables» Configuration row: "`providers.toml`: Open, U5 … secret-bearing files separated".
     - «One inspection location»: "whether `providers.toml` and `api_keys/` exist".
   - SPEC §2.1 says: "Personant reads no API key and keeps no secret-bearing file".
3. **Embedding provider.**
   - ARCH «Model access…»: "the embedder still needs Personant configuration because opencode has no embedding provider".
   - SPEC §8.2.2: "The reference names one of opencode's providers and a model it serves". SPEC §8.2.1 has embeddings requested from opencode.
4. **Session titles.**
   - ARCH «Integration shape»: "the `session.title` and `session.compaction` hooks supply `result`, which skips the model call".
   - Against this, ARCH «Boundary» says "Outside context management, Personant uses what opencode has chosen, as it is", and SPEC §1 says the same. Titles are not in SPEC §1's list of what Personant owns.
5. **Directive read time.**
   - ARCH «The composed request and its slots»: directives are "resolved by directive precedence at the start of a service run".
   - SPEC §8.2.4: precedence is walked "at parameter-read time".

**Continuity across restarts**

6. **Context lost on restart.**
   - ARCH «Start order and resume»: "The transcript tail and the turn counter… reset on every relaunch, so after a restart the TUI shows the conversation while the model does not".
   - THESIS «What is new» 1: "assembled afresh from memory… nothing is lost to compaction", and the stated effect is "as if there had never been a boundary".
7. **Decay timers versus a resetting counter.**
   - ARCH says the turn counter resets on relaunch (item 6). It also says:
     - «Acceptance-run mechanics»: "decay-triggered closing keys off `TurnNumber − LastEngagedTurn`".
     - «Active-set and dormant-set order»: `last_engaged_turn` is "service-run-scoped".
   - SPEC §2.2.1 and §3.5 make `engagement.decay-turns` a decided rule, and nothing reconciles the two.
   - If the `Personant-Turn` id is this counter, a reset could also make §4.5.8's `head turn id == turn` test misclassify a torn turn as case 5. This rests on an inference; the documents never say what the turn id is.

**Git checks**

8. **Derived freshness on every git operation.**
   - ARCH asserts it in two places:
     - «Storage and record formats» drift table: "a state-changing git operation on the store directory fails while any derived file is stale".
     - «Storage non-negotiables»: `CheckDerivedFresh` "fails any state-changing git op on stale".
   - ARCH contradicts it in «Automatic vs model-controlled»: "turn-repository commits run none".
   - SPEC §4.5.8 (7) also says "Intra-day derived staleness — by design". SPEC §2.1 is split the same way.
9. **Checks before and after.**
   - SPEC §8.3: both checks run "Before and after each state-changing git operation on `.git/`".
   - ARCH «Closing a thread»: "`CheckSpineIntegrity` before and `CheckDerivedFresh` after". The day-commit gets an integrity check only; freshness is asserted steps later.

**Thread and symbol records**

10. **Order of the closing flow.**
    - ARCH «Closing a thread»: "the summarizer drafts the closing summary → the candidate is classified… An exception (… a failed or empty draft) is queued".
    - SPEC §3.5: an exception is queued with "no summarizer call", and a failed draft "leaves its thread un-closed rather than queued".
    - SPEC is split here too: its own exception criteria include "draft is empty or failed".
11. **`turn_count`.**
    - ARCH `SpineRecord`: "total turns this thread has been engaged in".
    - SPEC §3.0.4: it increments "only for the turn's owner thread".
12. **Input to the anchor projection.**
    - ARCH «Anchors and symbol history»: a "projection of the thread's *active* history symbols".
    - ARCH «Projection»: "over all retained history symbols, active and superseded alike".
    - SPEC §2.2 and §2.7.4 have the same split.
13. **Anchor length versus identifier ranking.**
    - ARCH's verify constraint: "`anchors[i]` … 3–50 chars".
    - ARCH «Projection» ranks "a high-specificity identifier" (URLs, paths, kept exactly) first, so a long URL anchor would fail `verify`.
14. **When staging entries expire.**
    - ARCH «Hook-driven turn phases»: the `CloseTurn` row includes "expiry of staged symbols".
    - ARCH «Transient-data lifecycle» and SPEC §3.10.5: "At the top of each turn".
15. **`description` versus `summary`.**
    - ARCH «Thread directory»: `description` is "truncated to the new-thread summary length bound".
    - The same bullet says "a thread that is not closed has no `summary`".
16. **"Contiguous" threads.**
    - ARCH «Key abstractions»: a thread is a "contiguous run of turns".
    - SPEC §1.1 assigns turns by problem; THESIS «What is new» 2 has a thread rest and resume; SPEC §9.1 requires interleaved workloads.

**Recall and the pipeline**

17. **Scope of recall path 2.**
    - ARCH «Recall paths» 2: it fires when symbols "match a closed thread's anchors".
    - SPEC §3.4: "Recall runs over all indexed bodies", and the match target is `anchors ∪ {h.normalized}`.
18. **Cross-project fetch.**
    - ARCH says:
      - «Recall paths» 3: "(other-projects slot → fetch)".
      - «Many projects»: "Cross-project recall is designed".
    - SPEC §3.1 and §3.2: a cross-project fetch or engagement "is declined".
19. **When `model.response` runs the pipeline.**
    - ARCH «Event observation»: "ingested at turn close".
    - SPEC §3.0.2: "The pipeline runs at semantic boundaries — complete response segment".
    - The effect is that a step's text is processed after the tool results it triggered.

**Storage classification and layout**

20. **Classification of `working-set.json` and `.recall-cache/`.**
    - ARCH's layout lists both as "operational… not drift-checked", even while labelling `.recall-cache/` "derived recall indexes".
    - SPEC §4.5.8 calls "working-set membership" derived state, and SPEC §3.4 calls the vector cache "a derived, persisted cache".
21. **Tracked-file store in the history repository.** ARCH acknowledges this one.
    - ARCH «Crash recovery…»: "restore both from history repository HEAD", and the archival capture commit is `Add(".")`.
    - SPEC §3.9.1: "the history repository never holds it".
22. **Excerpt file count.**
    - ARCH's placement rule: an excerpt over 1 MiB "stays loose… Loose files can therefore sit permanently".
    - SPEC §2.3: the file count "does not grow with thread length".
23. **Project directive path.**
    - ARCH's layout: `prj_<n>/project.md`.
    - ARCH «Personalization…»: `directives/prj_<n>/*.md`.

**Document self-consistency**

24. **Tense rule versus present-tense code facts.**
    - ARCH «Tense»: "present tense states the production design".
    - Against it:
      - «Tracked-file store»: the text "describes the current code, not the intended design".
      - «One inspection location»: "`paths.go` still places…".
25. **Prototype paths treated as current.**
    - ARCH «Tense»: the prototype paths "resolve" on `probe-path`, not the build branch.
    - Against it:
      - «Module map» lists `internal/opencodehook/`, `opencode-plugin/` and `probe_phases.go` as modules.
      - «Pinned release» states the supported release "in one place", the probe-path recipe `sim-opencode`.
26. **Session command list.**
    - ARCH's U4 list includes `/keep`, `/threads`, `/spine` and `/index-rebuild`.
    - SPEC §4.2 defines none of them.
27. **How archival removes threads.**
    - ARCH «Closing a thread» says first "via `git rm` + `git commit`".
    - It then says "removals are staged by `os.RemoveAll` plus `autogit.Add(".")`".
28. **Where `Normalize` and `DominantSource` live.**
    - The module map places them in `memops`.
    - «Port-and-adapter» says they "stay direct from `internal/store`".
29. **SQLite as the reserve index.**
    - ARCH «Anti-patterns»: "SQLite stays in reserve as a *derived* index".
    - ARCH «Storage evolution»: bbolt is the option; "Rejected: … SQLite only if queries become SQL-shaped".
30. **Real model in tests.**
    - ARCH «Anti-patterns»: "real model for `just integration-test` only".
    - Elsewhere:
      - `sim-completeness-rung` uses a live embedder;
      - live run configurations use `reaper.local`;
      - SPEC §9.1 calls inference-in-loop runs "required for convergence".
31. **"Three places" of state.**
    - ARCH «One inspection location»: "Personant's own state lives in three places".
    - The third place listed is opencode's data home, with "no Personant state".
32. **Career memory.**
    - ARCH «Memory vetting»: "Career-memory protection uses the slots, the turn and history repositories and consolidation".
    - SPEC §1.8 describes those protections for "Threads". THESIS describes career memory as notes "drawn from the record".

## 3. Growth toward a THESIS "It is not" column

1. **Model access: not "an inference client or provider configuration".**
   - What ARCH builds: `internal/model` `HTTPClient`; the U5 option (b) provider pool; the default in-process executor (`runwithinfo`), which places the model call itself; the Gemma tuning target "via the `reaper` provider".
   - Why it crosses: the service holds an inference client and a provider configuration.
2. **Plugin: not "a harness, UI or agent runtime".**
   - What ARCH builds:
     - The U3 spike: "read the head of the streaming response…, cancel upstream, re-issue, return the second stream… per-protocol wire code in the plugin".
     - The alternative route: "a full Go port of opencode's server".
   - Why it crosses: the plugin would drive model requests (an agent runtime), and the port rebuilds the host. That also crosses Model access.
   - Borderline: plugin-registered commands and `permission.assert` ack questions edge toward a UI.
3. **Memory service: not a "multi-tenant service".**
   - What ARCH builds: «Concurrent sessions»: "Distinguished from multi-user (v2.0, locked)".
   - Why it crosses: a multi-user line is planned. Concurrent sessions for one user are arguably within bounds.
4. **Memory store: not "a database engine". Indexes: not "a search engine or catalog service".**
   - What ARCH builds: «Storage evolution»: "bbolt as a derived KV index".
   - Why it crosses: ARCH carves out "derived only"; THESIS makes no such carve-out.
5. **Memory store: not "a mirror of the user's repository". Content dedup: not "a version-control system". Career memory: not "a copy of past projects' code".**
   - What ARCH builds: the tracked-file store keeps user-file blobs under git object ids plus a forward diff chain. Per «Tracked-file store» and «Crash recovery», the current code commits it to the permanent history repository, and archival capture commits pin it.
   - Why it crosses: past projects' code would persist in career history. ARCH flags the day-commit path but not the archival capture.
6. **Event log: not "a telemetry or metrics backend".**
   - What ARCH builds: «Event log format» `model.inference-telemetry` (`ttft_ms`, `prefill_ms`, tokens/s…).
   - Why it crosses: this is telemetry, not a decision. SPEC §2.8 scopes the log to decisions.
7. **Model judgments: not "Free-form generation that decides what is stored".**
   - What ARCH builds:
     - Routine closing "takes the draft as written", and no validator is named.
     - The clusterer: "Each cluster produces a thread file + thread registry record".
   - Why it crosses: model output determines stored text and thread boundaries.
8. **Recall: not "a vector database".** This is a judgment call.
   - What ARCH builds: a hierarchical coarse/fine vector index, a persisted `.vec`/`.tree` cache, summary trees with beam descent, and an async indexer with atomic snapshots.
   - Why it may cross: this is vector-database internals, beyond "similarity scored against thresholds".

Considered and not listed:
- the durability machinery (multi-step markers, roll-forward), which stays within journaling plus commit points;
- the loopback HTTP listener, which is local;
- the thread-closing queue.

## 4. Unstated departures from a THESIS standard pattern

1. **Plugin ("stateless adapter").** ARCH: "Sequence numbers are taken in the plugin… forwarding chained", plus a fail-open count. A reason is given for the sequence numbers (out-of-order RPCs), but ARCH claims only "holds no memory state" and never states the departure from "stateless".
2. **Memory store ("human-readable text files").**
   - `logs/archive/YYYY-MM.tar.gz` is binary. It also appears in none of the authoritative/derived/operational classes. No reason is given.
   - `folio.jsonl` stores markdown with "default HTML escaping". Bundling has a stated reason; the escaping does not.
3. **Indexes ("derived, rebuildable").** `.recall-cache/` is filed as operational and not drift-checked. `working-set.json` holds state that cannot be rebuilt. No reason is given for either.
4. **Recall.** An approximate summary-tree beam descent "replaces the flat O(C_main) scan". The only reason given is the big-O; the link to SPEC §3.4's no-decay requirement is not drawn.
5. **Directives ("layered by precedence").** Resolution happens once per service run (item 5 in §2), with no reason.
6. **Model judgments ("drafts text that deterministic code validates").** No validation step for the summarizer draft is described (item 7 in §3).
7. **Thread lifecycle ("state machine with decay timers").** Turn-based decay sits on counters that are not persisted (item 7 in §2), and only U9 is cited.
8. **Career memory.** ARCH has no mechanism for career memory and does not say that none is built. It relabels thread protections as career-memory protection.
9. **Model access.** The default executor's direct model call is never stated as a departure; only the U5 open item exists.

## 5. ARCH terms not understandable from the three documents

| Term | First appears | Guess |
|---|---|---|
| sub-mind split and re-merge | «What this is» | Forking child or subagent sessions and merging their memory back |
| U3–U10, ROADMAP A2/A3/A5 | «Tense» / «Failure handling» | IDs in an external decision register or roadmap. Each item's subject is inferable from its own sentence; U10 appears only in ARCH |
| Effect defect | «Failure handling» | An unrecoverable failure in opencode's Effect runtime |
| Code Mode | «Integration shape» | An opencode mode where the model writes code that calls tools |
| `experimental.idle`, `session.http.response`, `permission.assert`, `experimental.generate`, `CommandDomain.transform`/`CommandEditor.add` | «Integration shape» and later | opencode plugin APIs; their meaning rests on the research reports, not these documents |
| managed (background) server | «Start order and resume» | opencode's daemon, which resumes interrupted executions |
| live-turn reject | «Hook-driven turn phases» | Refusing a prompt that exceeds the `live_turn` reserve |
| `C_main` | «Recall paths» 4 | Chunk count of the engaged thread |
| beam descent | «Module map» | Top-k search down a tree of summary vectors |
| measurement-carrier displacement | «State-ownership map» | Simulator-injected probe threads pushing others out of the active set |
| execution-time refinement step/turns | «State-ownership map» | Turns the generator inserts at run time in response to feedback |
| `W1-diag`, `net-cap-hit` | «Event log format» | A diagnostic for some window "W1"; the 3-candidate surface cap being hit |
| cells (`cells=`, `TestCell5_…`), `stamped=`/`stamp-repaired` | «Crash recovery» / log table | Cell = a recovery case; stamp = back-fill repair |
| `bound=`/`unbound=` | `barrier.folio-settled` | Counts of excerpts moved into and out of the bundle |
| v2.0, locked | «Concurrent sessions» | Multi-user deferred to v2.0, with the decision frozen |
| day-off idle window (in production) | «Crash recovery» packing | A production idle period; it is defined only for the simulator (Sunday) |
| 30-minute check interval | «Acceptance-run mechanics» | A CI or test-suite time budget |
| shipped files | Layout drift table | Files `init` writes (README, `defaults.md`, `.gitignore`) |
| the SQLite choice | «Approved-when-earned» | The driver for the opencode database cleanup |
| Personant provider pool | «Model access» | Personant's own list of model endpoints |
| 64 slots (`GitCheckFlags`) | «Automatic vs model-controlled» | A uint64 bitmask |
| "signed" commits | «Automatic vs model-controlled» | Author/committer identity, possibly not cryptographic signing |
| curator | «Module map» | The summarizer; SPEC says "summarizer" |
| debt window / `EmbeddingDebtCap` | «Thread directory» | The unembedded backlog |
| `PreEvents` | «opencode-in-loop harness» | File events scripted ahead of a step's model turn |
| reaper / `reaper.local`, gemma-4, oMLX | «Model access» / «Model-family» | A local inference host and model family. My session context, not the documents, identifies `reaper.local` as a Mac serving oMLX |
