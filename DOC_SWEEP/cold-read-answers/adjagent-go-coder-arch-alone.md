# Personant ARCHITECTURE: cold read

Everything below comes from the document alone. Where I relied on outside knowledge, I say so at that point.

## 1. What the system is made of

Personant is a Go **memory service** plus a thin TypeScript **plugin** loaded by unmodified opencode. That opencode is a terminal coding-agent host, and that Bun is a JS runtime, comes from outside knowledge; the document assumes both. The plugin forwards opencode hooks and events to the service over loopback HTTP+JSON and applies its answers, chiefly replacing `system` and `messages` on every model request. The service runs every context change as a "delta" through a fixed pipeline (symbol extraction → engagement → dedup → budget → logging). It assigns turns to "threads" and composes each request from budgeted slots: directives, the thread registry as display lines, other-project digests, active/dormant threads, and slots for opencode's own injections. Memory is plain text (JSONL, markdown+YAML, TOML) in one store directory, behind the `memops.MemoryOps` port and a file adapter, versioned by two in-process go-git repositories (per-turn disposable, day-grain permanent), with deterministic cold-start crash recovery. Recall is model recognition via an in-band topic tag plus deterministic Jaccard/embedding matching. A simulation harness is the measurement instrument. Much of the opencode route exists only as a prototype on branch `probe-path`, and many decisions are marked **Open**.

## 2. Self-contradictions

**Hard contradictions**

1. **Process dictated here despite the preamble.**
   - Preamble: "No project-specific process is dictated, that belongs to CONVENTIONS."
   - Against it:
     - *How decisions evolve*: "When you propose a change, ask: …"
     - *Anti-patterns to push back on*: "stop and surface the concern"
     - *Testing as the measurement instrument*: "changed through CONVENTIONS review"
     - *The per-delta pipeline*: "Code review treats any direct mutation as a bug."
2. **User-facing outcomes designed here despite the preamble.**
   - Preamble: "No user-facing behaviors or outcomes are designed here."
   - Against it: the whole of §4 below, e.g. *Integration shape*: "A user `/compact` cannot be refused."
3. **Prototype paths stated in production present tense.**
   - *Tense*: "present tense states the production design"; prototype paths such as `internal/opencodehook/` are "not on the build branch."
   - Against it:
     - *Plugin route* data flow: "the plugin forwards each to `internal/opencodehook` over loopback HTTP + JSON"
     - *Module map* rows for `internal/opencodehook/` and `opencode-plugin/`
     - *Port-and-adapter*: "Application code (`internal/turn`, `internal/opencodehook`, …)"
4. **Where the pinned release is stated.**
   - *Pinned release*: "stated in one place: the version check in the `sim-opencode` recipe."
   - *Tense*: `sim-opencode` is a prototype recipe that "resolves there" (branch `probe-path`), so on the build branch it is stated nowhere.
5. **How many places hold Personant state.**
   - *One inspection location*: "Personant's own state lives in three places."
   - Its third bullet: "opencode's data home — … no Personant state."
6. **What anchors are projected from.**
   - *Projection*: "A pure function of current salience over all retained history symbols, active and superseded alike."
   - *Anchors and symbol history*: "projection of the thread's *active* history symbols"
   - *Key abstractions*: "projected from active `history_symbols`"
7. **What "active" means.**
   - *Symbol lifecycle and anchor projection*: "`active` — currently in a top-`anchor.projection-max` slot (the zero value)."
   - *Thread directory*: "omitted ≡ "active"". Only once-central symbols flip to superseded on exit, so a never-central symbol outside the slice is "active" without holding a slot.
8. **Whether superseded symbols are ever trimmed.**
   - *Anchors and symbol history*: "retained, not trimmed — … protected from capacity trimming by its `ever_central` latch."
   - *Thread directory*: "If the protected set alone exceeds the cap, the lowest-`count` `superseded` entries go first."
9. **Whether `description` can be rewritten.**
   - *Thread registry record*: "`description`: triggering utterance; set once, never rewritten."
   - Same section: the working name "is its `description` … A rename rewrites that name."
10. **Whether non-closed threads have a summary.**
    - *Thread directory*: "a thread that is not closed has no `summary`."
    - *Thread registry record*: the display example for a `[WIP]` thread shows a summary.
    - *The composed request and its slots*: demotion sends "the active-threads slot's oldest (compressed to summary)."
11. **Where the summary lives.**
    - *Thread directory*: "`summary` … in frontmatter only."
    - *Thread registry record*: `summary` is a field of `spine.jsonl`, and the `thread.md` example frontmatter has none.
12. **Whether stale derived files block every git operation.**
    - *Storage non-negotiables*: "`CheckDerivedFresh` fails any state-changing git op on stale."
    - *Store directory layout*: "a state-changing git operation on the store directory fails while any derived file is stale."
    - Against both:
      - *Automatic vs model-controlled*: "turn-repository commits run none."
      - *Crash recovery*: "`CommitTurn` … with no integrity flags."
13. **What is gitignored.**
    - *Store directory layout* drift table: "All but `.git/`, `recovery/` and the shipped files are gitignored; the turn and history repositories are both in the managed gitignore block."
    - The layout marks every gitignored operational file except `last-active`.
14. **How often the history repository is committed.**
    - *Store directory layout*: "`.git/` … one day-grain commit per day."
    - *Closing a thread*: archival makes a separate "capture commit … into the history repository" and a "deletion commit."
15. **Whether content can live only in git.**
    - *Crash recovery*: "No authoritative byte ever lives *only* in git."
    - *Thread lifecycle*: an archived thread is "restorable by explicit fetch through git."
    - *Closing a thread*: archival removes it "via `git rm` + `git commit`."
16. **How archival removal is done.**
    - *Closing a thread*: "via `git rm` + `git commit`."
    - Same section: "removals are staged by `os.RemoveAll` plus `autogit.Add(".")`."
17. **Whether the history repository holds the tracked-file store.** The document flags this one itself.
    - *Tracked-file store*: "the history repository never holds the tracked-file store."
    - *Crash recovery*: "restore both from history repository HEAD."
    - *Closing a thread*: the capture commit is "`Add(".")` of the full store working tree."
18. **When staged symbols expire.**
    - *Transient-data lifecycle*: they expire "At the top of each turn (after `TurnNumber++`, before any pipeline step)."
    - *Hook-driven turn phases*: the `CloseTurn` row also covers "expiry of staged symbols."
19. **Where Normalize and DominantSource live.**
    - *Module map*: `memops` holds "`Normalize`, `DominantSource`."
    - *Port-and-adapter*: "What stays direct from `internal/store` …: pure helpers (`Normalize`, `DominantSource`)."
20. **Which database is the scale reserve.**
    - *Storage evolution*: the option is bbolt; "SQLite only if queries become SQL-shaped."
    - *Anti-patterns*: "SQLite stays in reserve as a *derived* index if scale forces it."
21. **Whether real models run in tests.**
    - *Anti-patterns*: "real model for `just integration-test` only."
    - *Acceptance-run mechanics*: "Live run configurations use `reaper.local` (gemma-4 family inference …)."

**Tensions** (contradictory unless a term is read narrowly)

22. **Every mutation through the pipeline?**
    - *Per-delta pipeline*: "No path mutates the working window without going through it."
    - Same section: digests, command effects and directive reloads "are not deltas."
    - *Injection handling*: the instructions slot is "Rebuilt each request."
23. **Is the turn load-bearing?**
    - *Per-delta pipeline*: "The "turn" is a logging convenience."
    - Against it: the per-turn repository, the "≤1 turn" loss bar, and engagement "coalesced per turn."
24. **Is loading ever a heuristic?**
    - *No speculative prefetch*: "the choice is not a heuristic guess."
    - *Recall paths*: a deterministic threshold match is "fetched with one visible line and no question."
25. **Can pressure close threads?**
    - *Thread lifecycle*: threads close "not under 'context window pressure.'"
    - *Fallback dissection*: under budget pressure, clusters "resolve through the closing flow."
26. **Are threads contiguous?**
    - *Key abstractions*: a thread is a "contiguous run of turns."
    - Against it: threads go dormant and are re-engaged.
27. **Does the model ever mutate state?**
    - *The model drafts*: "The model never directly mutates authoritative state."
    - *Automatic vs model-controlled*: the agent's shell "can reach it, and nothing blocks it."
28. **Does the application layer depend only on the port?**
    - *Module map*: "The application layer depends only on this."
    - *State-ownership map*: the harness, listed as application code, reads `store.LoadArchiveIndex`.
29. **Is the registry slot ever truncated?**
    - *Many projects*: the registry slot holds "the full thread registry."
    - *The composed request and its slots*: "Each rendered slot truncates to its byte budget."
30. **Designed or open?**
    - *Concurrent sessions*: "Designed, not built … Full design capture."
    - Same section: "**Open:** two concurrent opencode sessions."
31. **Event log example vs the rest.**
    - *Event log format* example emits `dissect.fire`, but that name is listed as reserved and the clusterer is "not built."
    - The example shows `home-format=1` and no `home=`, while migrations reach revision 3 and the table lists `home=`.
32. **How `symbols.jsonl` is ordered.**
    - *Thread registry record*: `recall_fires` "orders symbols.jsonl."
    - *Symbol index*: the file is "sorted by `symbol`."

## 3. Terms not understood from the document

| Term | First appears | Guess |
|---|---|---|
| sub-mind split and re-merge | What this is | forking work into child sessions/subagents and merging results back |
| career / career memory | What this is; Memory vetting | the agent's whole cross-project history, as distinct from behavior and process memory |
| behavior memory, process memory | Recurring patterns | learned directives about conduct, and about project workflow |
| Effect defect; Promise-API hook | Failure handling | opencode's Effect-library error channel (outside knowledge) |
| U3–U10 (no U1, U2, U6); ROADMAP A2/A3/A5 | Tense; Failure handling | IDs in an open-items list and roadmap held elsewhere |
| baseline comparator | Tense | `sim-baseline`/`sim-fidelity` and the committed baselines |
| primary step | Integration shape | a main-agent model call, as opposed to title/compaction calls |
| transcript tail | Integration shape | the last N raw conversation turns |
| working set | Integration shape | the composed memory content (active plus dormant threads) |
| Code Mode | Integration shape | an opencode code-execution feature |
| opencode-expectations slot | Integration shape | the "Expectations slot" of Injection handling |
| live-turn reject, day rollover, closing scan, tag strip | Hook-driven turn phases | refuse a second concurrent turn; daily maintenance; decay check; remove the topic tag from output |
| pre-authoritative rollback, run-volatile state | Hook-driven turn phases / U8 | undo before authoritative writes; in-memory per-run state |
| steer prompt | Close ordering | a user message injected mid-turn |
| retraction / retracted prompt | U8 | a user-withdrawn or interrupted prompt |
| managed background server, standalone-kill claim | Start order and resume | opencode daemon; a dead process's claim on an execution |
| topic tag (format) | Plugin route onward | a model-emitted header naming threads; only `*new-topic* [...]` is shown |
| owner / owner turn | Thread directory | the thread a turn's excerpt is filed under |
| LayerB, b-top-k | Thread directory | active-threads slot budget (defined inline); number of active threads composed |
| debt window, embedding debt | Tier-transparent readers | excerpts not yet embedded (partly defined) |
| coarse level / fine level, C_main | Module map; Recall paths | thread-level vs chunk-level index; chunk count of the engaged thread |
| §5.5 fetch / mid-turn fetch | Hook-driven/No speculative prefetch | re-prompting once a tag names a non-loaded thread |
| drain point, clean-exit drain point, `always` mode, `[e]dit` | Closing a thread | when queued closings are put to the user; ack modes |
| auto-fetch band, surface threshold | Recurring patterns | score bands that auto-load or ask |
| shipped files | Store directory layout | `README.md`, `defaults.md`, `version.toml` |
| managed gitignore block, ignore-block rule | Store directory layout | a Personant-owned section of `.gitignore` |
| barrier, day-commit, freshness-assert step, morning-init, rebaseline, lifecycle tags | Crash recovery | day-rollover step names; git tags marking thread lifecycle |
| cells, case 3/9/12 | Crash recovery; event table | rows of a recovery-state matrix in SPEC |
| orphan add, scoped-restore | Crash recovery | a file added since HEAD; restoring only named paths |
| spine / curator / retire / dissect | storage names, events | thread registry / summarizer / closing / fallback clustering |
| GitCheckFlags "64 slots", systemic-validation policy | Automatic vs model-controlled | a 64-bit bitmask of check functions |
| day-off idle window | Applying/Crash recovery | a simulated non-working day for consolidation |
| measurement probe, measurement-carrier displacement, rung/ladder | Testing | scored recall test turns; probes displacing active threads; progressively longer sim spans |
| W1-diag, net-cap-hit, flush-backlog, corpus-saturation floor | Event log; Acceptance-run | diagnostics and an expected precision decline |
| v2.0, locked | Concurrent sessions | a frozen future milestone |
| honest truncation notice | The composed request and its slots | a marker telling the model content was cut |
| tag-fidelity measures, accept-rate/edit-rate | No speculative prefetch; Recurring patterns | how often tags are correct; human ack ratios |
| reaper provider, `personant-mock` | Model-family; harness | local inference host; the mock provider ID |

## 4. Passages stating user-facing behaviors or outcomes

- **What this is**: "persistent working memory across arbitrary projects with continuity: one continuous career, no session boundaries, no compaction-driven information loss, cross-project recognition"
- **Failure handling (U7)**: "fail open (opencode's own composition reaches the model and memory misses the step) or fail closed (the request is refused)"
- **Integration shape**:
  - "A user `/compact` cannot be refused"
  - "`/clear` … discards nothing"
  - "deleting a session does not touch memory"
  - "`personant init` writes the settings opencode must hold … into opencode's own config"
  - "Personant-only settings live in `~/.config/personant/` and never overlap opencode's"
- **Ack questions (U4)**: "An ack question asks the user to confirm a step … The recall offer, the review queue … and the project-resolution questions at startup all ask one."
- **Ordering and correlation**: "This makes memory state independent of completion order"
- **Injection handling**: "so that nothing is stale and nothing is counted twice"
- **Start order and resume**: "after a restart the TUI shows the conversation while the model does not"
- **Active-set and dormant-set order**: "so recency survives a relaunch"
- **Applying the thesis**: "Personant defines no interaction mode or workflow"
- **The model drafts**: "a thread closed through the routine path and a recall candidate in the auto-fetch band apply without asking"
- **Automatic vs model-controlled**: "The memory service exposes no git tool for the store directory."
- **One inspection location**: "Forensic inspection is `cd ~/.personant && grep -r ...`"
- **No speculative prefetch**: "Every loaded thread is loaded because it was recognized as needed, never guessed."
- **Storage non-negotiables**: "hand-readable; standard tooling (Obsidian, Pandoc)"; "hand-editable"
- **Store directory layout**:
  - "default ~/.personant; configurable"
  - "layout documentation for human inspection"
  - "Source of truth; hand-editable"
- **Thread registry record**:
  - "the working name a user reads and types back … A rename rewrites that name and nothing else"
  - "The user-facing rendering is `thr_N: display — gist`"
- **Display lines**: "the model can answer most 'what was the outcome of X' questions from the line alone"
- **Recall paths**:
  - "Handles synonyms and paraphrases for free"
  - "fetched with one visible line and no question; the band … is asked: 'We talked about X 2 days ago — pick up there?'; nothing below surfaces … recall stays log-only"
- **Guarantee boundary**: "durable content is *findable* … does not promise spontaneous cross-domain reminiscence … State this boundary when setting user expectations"
- **Closing a thread**:
  - "applied without asking, with one visible line … An exception … is queued silently … crash-safe and correct across restarts"
  - "never writes a partial thread"
- **Fallback dissection**: "graceful degradation under stress, not catastrophic loss"
- **Many projects, one career**: "one entity, many projects, one continuous career"
- **Personalization**:
  - "The user can read what the agent learned and correct it."
  - "a pattern seen in one project does not reach `user.md` … The user's policy can change this routing"
- **Crash recovery**:
  - "no user acknowledgement"
  - "even the one at-risk turn's content survives"
  - "quarantined byte-exact, never deleted"
  - "a missing history repository is refuse-to-open"
  - "judged against turns the user saw complete"
- **Model-family as platform**: "with a warning on others"; "cross-family mismatches fail silently and statistically"
- **Project identity**:
  - "survives file moves and renames"
  - "user-chosen string | yes (`/project rename`) | UI rendering, command targeting"
  - "the memory service detects it and adopts it as the project's remote identity"
- **Module map (`cmd/`)**: "`version` … never fails on a broken store directory"
- **Event log (`home-format-override`)**: "always paired with a stderr warning"
- **Anti-patterns**: "resetting on dirtiness destroys user work"
