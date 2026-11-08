# Personant ARCHITECTURE: cold read

Everything below comes from the document alone. Where a guess relies on outside knowledge, I say so at that point.

## 1. What the system is made of

Personant runs inside an unmodified opencode as two pieces:
- **A TypeScript plugin** that holds no state. It forwards opencode's hook calls and events, and applies the answers it gets back.
- **A Go memory service** that the plugin calls over loopback HTTP + JSON. The service is the only writer of memory and composes every model request.

Inside the service, one handler per hook runs a turn phase in `internal/turn`: begin, compose, ingest tool result, ingest response, close, abort. Every phase reaches storage only through the `MemoryOps` port. Behind that port, one file adapter keeps memory in a plain-text store directory (JSONL, markdown with YAML, TOML), tracked by two automatically managed git repositories: one committed per turn and disposable, one committed per day and permanent. Around this sit:
- recall packages: lexical, embedding and summary-tree recall
- a pure working-set renderer
- crash recovery that rebuilds state from what is on disk
- a simulation harness that serves as the acceptance instrument.

## 2. Self-contradictions

1. **The document's own scope rule against its content.**
   - "Must-see": "No user-facing behaviors or outcomes are designed here" and "No project-specific process is dictated, that belongs to CONVENTIONS".
   - Against that, it states user-facing outcomes (all of §4 below) and dictates process. Examples: "Anti-patterns to push back on": "stop and surface the concern"; "The per-delta pipeline": "Code review treats any direct mutation as a bug"; "How decisions evolve": "When you propose a change, ask…"; "Testing as the measurement instrument": "changed through CONVENTIONS review".
2. **The tense rule against prototype paths.**
   - "Tense": prototype facts are "marked 'the prototype'", and its paths, including `internal/turn/probe_phases.go`, `internal/scenarios/opencode/` and `sim-opencode`, are "not on the build branch".
   - "Pinned release": "The supported opencode release is stated in one place: the version check in the `sim-opencode` recipe". That production fact lives in a recipe that is not on the build branch.
   - "Module map", `internal/turn/` row: "the phase entry points (`probe_phases.go`)", not marked as prototype.
3. **Where the tracked-file store lives.**
   - "Tracked-file store": "the history repository never holds the tracked-file store".
   - "Crash recovery…": "restore both [`files.jsonl` and `files/`] from history repository HEAD". The document flags this one itself, but it still states both.
4. **Content that lives only in git.**
   - "Crash recovery…": "No authoritative byte ever lives *only* in git… losing one is never losing content."
   - "Closing a thread": archival works "via `git rm` + `git commit`", and "Thread lifecycle" says archived threads are "restorable by explicit fetch through git".
5. **Commits per day in the history repository.**
   - "Store directory layout": "`.git/` # history repository: permanent, one day-grain commit per day".
   - "Closing a thread": archival makes a "capture commit… into the history repository" and a "deletion commit". "Exactly one day-commit" is also asserted in "Acceptance-run mechanics".
6. **The stale-derived-file check.**
   - "Storage non-negotiables": `CheckDerivedFresh` "fails any state-changing git op on stale"; the drift-policy table says "a state-changing git operation on the store directory fails while any derived file is stale".
   - "Automatic vs model-controlled": "turn-repository commits run none". "Crash recovery…": `CommitTurn` stages "with no integrity flags".
7. **Whether the git repositories are gitignored.**
   - Drift-policy table: "All but `.git/`, `recovery/` and the shipped files are gitignored".
   - Same cell: "the turn and history repositories are both in the managed gitignore block".
   - Related: `last-active` is marked operational but, unlike its neighbours in the layout, not gitignored.
8. **What the anchor projection ranks over.**
   - "Projection": "over all retained history symbols, active and superseded alike".
   - "Anchors and symbol history": "projection of the thread's *active* history symbols". "Key abstractions": "projected from active `history_symbols`".
9. **Whether superseded symbols are ever trimmed.**
   - "Symbol lifecycle…": superseded symbols are "Retained, not trimmed". "Anchors and symbol history": "retained, not trimmed".
   - "Thread directory": "If the protected set alone exceeds the cap, the lowest-`count` `superseded` entries go first."
10. **Whether `description` changes.**
    - `SpineRecord`: "`description`: triggering utterance; set once, never rewritten".
    - "Display form": the working name "is its `description`… A rename rewrites that name".
11. **The summary shown in the display example.**
    - "Thread directory": "a thread that is not closed has no `summary`". `SpineRecord`: summary is "drafted at close".
    - "Display form" example: a `[WIP]` thread showing a summary ("electron body-topology conflict; …").
    - The same `thr_88` has 4 anchors in that example but 2 in the frontmatter example.
    - Frontmatter is "authoritative for metadata" and includes `anchors`, while `SpineRecord` marks `anchors` "[derived]".
12. **Where `Normalize` and `DominantSource` live.**
    - "Module map": they are among `internal/memops/`'s domain types.
    - "Port-and-adapter…": "What stays direct from `internal/store`…: pure helpers (`Normalize`, `DominantSource`)".
13. **`prj_default` and the project ID pattern.**
    - `ProjectMeta.id`: `/^prj_\d+$/`.
    - "Project metadata": `prj_default` has metadata ("empty path fields, `name: default`"). The pattern excludes it.
    - Also: "reserved for threads created before any project was active" against "Project resolution" step 4, "no project (`prj_default`)", which can be chosen at any time.
14. **What SQLite is held in reserve for.**
    - "Anti-patterns": "SQLite stays in reserve as a *derived* index if scale forces it".
    - "Storage evolution": under scale the choice is bbolt; "SQLite only if queries become SQL-shaped".
15. **How many excerpts the completeness floor scans.**
    - "Recall paths" item 4: "≤ the limit".
    - Next paragraph: "scans `(1 + pending) × limit` excerpts".
16. **When staged symbols expire.**
    - "Transient-data lifecycle": "At the top of each turn (after `TurnNumber++`, before any pipeline step)".
    - "Hook-driven turn phases": `CloseTurn` also covers "expiry of staged symbols". The `staging` log row: "expiry at window close".
17. **Whether every window change passes through the pipeline.**
    - "The per-delta pipeline": "No path mutates the working window without going through it."
    - Same section: "Other-projects slot digest content, session-command effects and directive reloads are not deltas: they change what the composer… assembles."
18. **Whether loading is ever a guess.**
    - "No speculative prefetch": "never guessed… The model recognizes the thread; the choice is not a heuristic guess."
    - "Recall paths" item 2: a deterministic score above the auto threshold "is fetched with one visible line and no question".
19. **Whether Personant defines interaction.**
    - "Applying the thesis": "Personant defines no interaction mode or workflow".
    - It defines session commands (`/done`, `/topic*`, …), ack questions and recall offers ("Integration shape", "Recall paths").
20. **Where Personant's state lives.**
    - "One inspection location": "Personant's own state lives in three places".
    - The third place: "opencode's data home — … no Personant state". The section title also says one location.
21. **Correctness across restarts.**
    - "Closing a thread": the exception queue is "correct across restarts".
    - "Start order and resume": "the turn counter… reset[s] on every relaunch", yet decay is keyed on turns (`engagement.decay-turns`; "`TurnNumber − LastEngagedTurn`").
22. **Where real models are allowed in tests.**
    - "Anti-patterns": "real model for `just integration-test` only".
    - "Acceptance-run mechanics": live run configurations against `reaper.local`; the state-ownership map: "report-only under live inference". No `integration-test` recipe is named anywhere.
23. **Whether a thread's turns are contiguous.**
    - "Key abstractions": a thread is a "contiguous run of turns".
    - Threads go dormant and are re-engaged ("a re-engaged closed thread", "Tier-transparent readers").

## 3. Terms not understood from the document

| Term | First appears | My guess |
|---|---|---|
| U3, U4, U5, U7, U8, U9, U10 | "Failure handling" (U7) | IDs in an open-questions list kept elsewhere |
| ROADMAP A2, A3, A5 | "Tense" | Numbered ROADMAP items |
| Effect defect | "Failure handling" | An unhandled failure in opencode's internal runtime (Effect library; outside knowledge) |
| Code Mode | "Integration shape" | An opencode feature where the model writes code to call tools |
| `CommandDomain.transform` / `CommandEditor.add` | "Integration shape" | opencode's plugin API for registering commands |
| opencode-expectations slot | U4 ack-questions paragraph | The "Expectations slot", defined later under a different name |
| directives-slot, task-class | "Event observation" | Directives slot; the transient retention class (both explained later) |
| `steer` prompt | "Close ordering" | An opencode user message sent mid-turn |
| managed background server; standalone-kill claim | "Start order and resume" | A long-lived opencode daemon; a run killed outside that daemon whose execution it later picks up |
| run-volatile state; pre-authoritative rollback | phase table / U8 | In-memory state of the current run; a rollback before any authoritative write lands |
| `b-top-k` | "Recent-excerpt window" | How many active threads get excerpt windows |
| coarse level | "Recall paths" item 4 | Thread-level embedding index (the "fine level" is per chunk) |
| `C_main` | same | Chunk count of the engaged thread |
| the §5.5 fetch / mid-turn fetch | "Integration shape" area / "Topic-tag mechanics" | Abort and re-prompt with a thread named in the tag but not yet loaded |
| drain point; `[e]dit`; `always` mode | "Closing a thread" | The moment queued exception closings are shown to the user; ack-UI options |
| measurement probe; rung; acceptance ladder | "Recall paths" / file layout | A scripted recall check step; increasing simulation spans |
| measurement-carrier displacement | state-ownership map | Undecipherable |
| `W1-diag`, `net-cap-hit`, `under-drain` | event table | Undecipherable diagnostics |
| cells, Cell5, case 3/9/12 | event table / "Crash recovery" | Cases in a recovery state machine defined in SPEC §4.5.8 |
| `op=rebaseline`, "mid-day arming trigger" | "Crash recovery" | Undecipherable |
| day-off idle window, `SleepCycle`, morning-init | "Crash recovery" | An idle period that triggers consolidation; unclear how it maps to production |
| debt window | "Recall paths" (`LoadDebtWindowExcerpts`) | The unembedded-excerpt backlog |
| owner / owner turn | "Recent-excerpt window" | The thread a turn's excerpt is written to |
| the shipped files | drift-policy table | Probably `README.md`, `defaults.md`, `version.toml` |
| the 30-minute check interval | "Acceptance-run mechanics" | A CI or test time budget |
| behavior / process / career memory | "The model drafts…" | Memory classes defined in SPEC §1.8 |
| `reaper` provider vs oMLX | "Model-family as platform" | `reaper` is the opencode provider name for the local oMLX host (outside knowledge) |
| "Grant", `trf3bd`/`unk0bd` | display example | Content from the example domain (a person; entry IDs) |
| `spine` | layout | Legacy name for the thread registry; the meaning can be inferred, the name is never explained |

## 4. Passages stating user-facing behavior or outcomes

**"What this is"**
- "persistent working memory across arbitrary projects with continuity: one continuous career, no session boundaries, no compaction-driven information loss, cross-project recognition"

**"Failure handling"**
- "fail open (opencode's own composition reaches the model and memory misses the step) or fail closed (the request is refused)"

**"Integration shape"**
- "A user `/compact` cannot be refused"
- "`/clear`… discards nothing because memory is not keyed on the session"
- "deleting a session does not touch memory"
- "An ack question asks the user to confirm a step"

**"Ordering and correlation"**
- "This makes memory state independent of completion order"

**"Injection handling"**
- "so that nothing is stale and nothing is counted twice"

**"Start order and resume"**
- "after a restart the TUI shows the conversation while the model does not"

**"The model drafts, deterministic code writes"**
- "a thread closed through the routine path and a recall candidate in the auto-fetch band apply without asking"

**"No speculative prefetch"**
- "Every loaded thread is loaded because it was recognized as needed, never guessed."

**"One inspection location"**
- "Forensic inspection is `cd ~/.personant && grep -r ...`"

**"Storage non-negotiables"**
- "hand-readable; standard tooling (Obsidian, Pandoc)"
- "hand-editable"

**"Module map", `cmd/` row**
- "`version`… never fails on a broken store directory"

**"Display form"**
- "the working name a user reads and types back… A rename rewrites that name and nothing else… The user-facing rendering is `thr_N: display — gist`"

**"Thread lifecycle"**
- "Archived… restorable by explicit fetch"

**"Display lines are how the model recognizes threads"**
- "the model can answer most 'what was the outcome of X' questions from the line alone"

**"Recall paths"**
- "Handles synonyms and paraphrases for free"
- "fetched with one visible line and no question; the band… is asked: 'We talked about X 2 days ago — pick up there?'; nothing below surfaces… With no channel to the user, recall stays log-only."

**"Guarantee boundary"**
- "guarantees that durable content is findable… does not promise spontaneous cross-domain reminiscence… State this boundary when setting user expectations"

**"Closing a thread"**
- "A routine closing is applied without asking, with one visible line… An exception… is queued silently."
- Restore "never writes a partial thread"

**"Fallback dissection"**
- "Quality is best-effort by design — graceful degradation under stress, not catastrophic loss."

**"Personalization…"**
- "The user can read what the agent learned and correct it."
- "By default a pattern seen in one project does not reach `user.md`… The user's policy can change this routing"

**"Many projects, one career"**
- "one entity, many projects, one continuous career"

**"Crash recovery…"**
- "no user acknowledgement"
- "≤1 turn is the bar"
- "a broken hand-edit is quarantined byte-exact, never deleted"
- "the bar is judged against turns the user saw complete"

**"Model-family as platform"**
- "with a warning on others"

**"Concurrent sessions"**
- "multiple live opencode sessions open against one shared memory"

**"Project identity"**
- "survives file moves and renames"
- Display name: "yes (`/project rename`)… UI rendering, command targeting"
- "the memory service detects it and adopts it as the project's remote identity"

**"Event log format", `system.home-format-override`**
- "always paired with a stderr warning"

**"Applying the thesis"**
- "Personant defines no interaction mode or workflow"

**"Anti-patterns"**
- "resetting on dirtiness destroys user work"
