Answered from the three documents alone; nothing else was consulted.

## 1. What the system is made of

Personant is a memory layer for an opencode coding agent with two parts: a TypeScript plugin that opencode loads, which holds no memory state, and a Go memory service that is the only writer of a git-versioned plain-text store under `~/.personant`. The plugin forwards opencode's hooks and events to the service over loopback HTTP + JSON. The service runs every content addition ("delta") through a fixed pipeline: symbol extraction, engagement, dedup, budget, logging. It assigns each turn to exactly one *thread*, the durable record of one problem, and answers `session.context` by building the whole request afresh from budgeted slots: directives, the thread registry, other projects, active and dormant threads, plus opencode's instructions, expectations and tool definitions. Recall (symbolic Jaccard, optional embeddings, an exact lexical floor) surfaces older threads, and threads close on engagement decay with a model-drafted summary and move to cold storage under registry pressure. A per-turn journal, a disposable per-turn git repository and a permanent day-grain history repository give crash durability, models are asked only narrow judgments through opencode, and a mock-model 120-day simulation is the acceptance instrument. Much of the opencode route exists only as a prototype on branch `probe-path`.

## 2. Contradictions

"(flagged)" means ARCHITECTURE itself acknowledges the conflict. "Judgment" marks items that rest on my reading rather than literal opposition.

### ARCHITECTURE vs SPEC / THESIS

1. **The service calling a model.**
   - THESIS "The building blocks", Model access: "the memory service never calls a model". SPEC §8.2.1: "The memory service never calls a model."
   - ARCH "Model access and inference telemetry": "Only the in-process turn emits `model.inference-telemetry` today (`internal/turn/turn.go`), from its own provider response." ARCH Module map, `internal/model/`: "OpenAI-compatible `HTTPClient`".

2. **The "one exception" to using opencode's choices.**
   - SPEC §1: "Personant uses it as it is (§1.7's database cleanup is the one exception)". ARCH "Boundary" says the same.
   - ARCH "Integration shape": "the `session.title` and `session.compaction` hooks supply `result`, which skips the model call".
   - Judgment: title generation is outside context management, so this is a second exception.

3. **Closing-summary bound.**
   - SPEC §2.2: summary "bounded by `spine.entry-max-chars`". SPEC §1.8: "mechanical checks on write (schema, length, …)". THESIS, Model judgments: "drafts text that deterministic code validates before writing".
   - ARCH "The model drafts, deterministic code writes": "Length is not checked before the write" (flagged).
   - The same section's `SpineRecord` comment ("≤ spine.entry-max-chars") contradicts ARCH internally.

4. **Exception classification for close-outs.**
   - SPEC §3.5: "Exception — the thread is anchor-rich (≥ 6 anchors)… Routine — everything else."
   - ARCH "Event log format", `retire.pending`: "`reason=anchors=N|turns=N`". That is a turn-count criterion SPEC does not define.

5. **Read-only turns.**
   - SPEC §3.3: "A turn that read a file is binding-required, like one that wrote one."
   - ARCH "Topic-tag mechanics": "whether such a turn should count as conversational is an open calibration question."

6. **The unclosed `*new-topic` token.**
   - SPEC §5.1.2 "Unclosed inner literal": `*new-topic` "is accepted as the new-topic sentinel".
   - ARCH "Topic-tag mechanics": each entry is "validated against `thr_\d+|\*new-topic\*`". The drafts table says the same.

7. **Unknown-thread-id owner fallback.**
   - SPEC §3.2: ownership "falls back to the most recently engaged **valid** referenced thread".
   - ARCH "Active-set and dormant-set order": ranks by `last_engaged_turn`, "counted in the service run's turn counter", which "inverts recency across a relaunch".

8. **Turn-based decay.**
   - SPEC §3.5: decay "fires when `last_engaged` exceeds `engagement.decay-turns` OR `engagement.decay-time`".
   - ARCH "Closing a thread": "A stored value above the current counter skips the turn threshold… only the wall-clock threshold is a durable decay timer" (flagged under U9).

9. **Mid-run structural checkpoint.**
   - SPEC §3.11: a checkpoint follows "a mid-run structural change (creating or closing a thread, creating a project). Every checkpoint commits the whole store working tree, hand-edits included".
   - ARCH "Turn transaction and journal": "Structural changes… ride the per-turn commit", using scoped `AddPaths`. Hand-edits are "absorbed at the next full staging pass: a clean-stop checkpoint, or… the day rollover".

10. **Derived freshness on history writes.**
    - SPEC §3.11: "the day's commit passes thread registry integrity validation and leaves derived files fresh". SPEC §8.3: "Every day-commit and archival write… leaves… its derived files fresh".
    - ARCH "Day rollover": step 5 commits after an integrity check only, step 6 rebuilds derived files afterwards, and step 9 asserts freshness. ARCH "Closing a thread": the capture commit "runs `CheckSpineIntegrity` before", with no derived-freshness check.
    - ARCH also contradicts itself on whether derived files are in the history repository at all. "Quarantine-and-proceed" says "derived paths, absent from the history repository", while the storage table says "a history-repository write fails if it leaves a derived file stale".

11. **Standalone archival.**
    - SPEC §3.8: "Archival runs only at the day rollover, so the history repository is written only then, apart from crash recovery."
    - ARCH "Archival crash completion": "A standalone batch runs under its own `op=archival` marker, outside any rollover".
    - This also contradicts ARCH's own "Archival fires only at the rollover's archival step" and the history-writer rule's "by nothing else".

12. **Debris removal without quarantine.**
    - SPEC §4.5.8 (4) Quarantine-never-delete: "The torn-turn debris removal… quarantine byte-exact… before any removal".
    - ARCH recovery case 4: "quarantine dirty tracked bytes… → reset… → remove untracked debris". Only tracked bytes are quarantined.
    - Internally, ARCH "Start order and resume" says "Crash recovery quarantines untracked files".

13. **Lifecycle tag coverage.**
    - SPEC §4.5.8 (5): a tag "for each thread created, closed or archived and each project created".
    - ARCH "Day rollover": "life events on earlier idle gap-days… go untagged".

14. **Per-turn opening-line contract.**
    - SPEC §2.8: "every other line a turn produces follows it".
    - ARCH "Hook-driven turn phases", `BeginTurn`: day rollover, staged-symbol expiry and pre-event deltas all run before the `user.prompt` delta.
    - Judgment: their log lines (`barrier.*`, `staging.evicted`, pre-event `context.modified`) would therefore precede the opening line.

15. **Recall-fidelity scoring source.**
    - SPEC §9.4: scoring "reads the event log… The one declared exception is the active-set oracle-agreement check".
    - ARCH "State-ownership map": the archival-forgiveness filter in recall fidelity reads a set derived "from the authoritative archive index (`store.LoadArchiveIndex`)". That is a second exception.

16. **`defaults.md` writability.**
    - SPEC §2.6: a change "proposed to the defaults or user-wide directive file, by feedback directive tuning… follows §1.8; a user's direct edit is not prevented."
    - ARCH "Store directory layout": `defaults.md` is "read-only after install". The ARCH storage table also lists `directives/*.md` as "hand-editable", so ARCH contradicts itself too.

17. **The `curator` symbol source.**
    - SPEC §2.7.3: emission is "deterministic extraction, model or user". SPEC §3.3: the summarizer "contributes no symbols".
    - ARCH `SymbolSource` includes `"curator"`, and "Symbol source" says "a thread's anchor counts as `curator`-sourced".
    - Internally, "Key abstractions" gives a pure dominance order ("curator > user > model > deterministic"), while "Symbol source" says "most frequent…; ties break curator > …".

18. **History repository filed as operational.**
    - SPEC §2.1 defines operational files as "gitignored runtime state the memory service manages to run". SPEC §4.5.8 (5) calls the history repository "irreplaceable".
    - ARCH storage table puts `.git/` in the Operational row: "not drift-checked".

19. **`.recall-cache/` classification.**
    - SPEC §3.4: "The vectors are derived and regenerable".
    - ARCH: `.recall-cache/` is "filed operational" (flagged, with reasons).

20. **Excerpt bundle legibility.**
    - SPEC §2.1: "inspectable with ordinary tools".
    - ARCH "Excerpt storage tiers": "an excerpt cannot be read in place… That legibility cost… is accepted" (flagged).

21. **File count in a thread directory.**
    - SPEC §2.3: "The number of files… does not grow with its count of excerpts."
    - ARCH: an excerpt over the 1 MiB line cap "stays loose", and conflicting names move in neither direction. This is an edge case.

22. **Tracked-file content in the history repository.**
    - SPEC §3.9.1: "the history repository never holds it". THESIS: the memory store is not "a mirror of the user's repository".
    - ARCH: the capture commit is "`Add(".")` of the full store working tree into the history repository", and recovery restores "`files.jsonl` and `files/`… from history repository HEAD" (flagged in both documents).

23. **Config location.**
    - SPEC §2.1 / §8.2.1 place Personant settings under `~/.config/personant/` and say Personant holds no provider configuration.
    - ARCH "One inspection location": `paths.go` "still places `config.toml` and `providers.toml` under the store directory" (flagged). This also contradicts ARCH "Storage non-negotiables".

24. **Missing CLI verbs.**
    - SPEC §4.1 lists `search`, `deps` and `log`.
    - ARCH Module map: "`cmd/` has none of them" (flagged).

25. **Composed vs replayed (judgment).**
    - THESIS "What is new": "composed, not replayed".
    - ARCH "Integration shape" includes a transcript tail from in-run history. SPEC §3.1 also says "replayed transcript tail", so the tension is shared with SPEC.

26. **The primary recall signal (judgment; an omission rather than a direct opposition).**
    - SPEC §3.4: embedding cosine "is the primary recall scan".
    - ARCH "Recall paths (four…)" lists no inter-thread embedding path, and path 2 is described as symbolic only.

27. **Thread protection by consolidation.**
    - SPEC §1.8: threads are protected by "batch content review in consolidation". ARCH "Memory vetting": threads are protected by "…consolidation".
    - ARCH "Memory consolidation" builds only gc and tree rebuild, and the keep/toss stage is "not built".

### ARCHITECTURE against itself

28. **Registry record rewrites.** "the record is rewritten only when the projected anchor set differs". Yet `SpineRecord` carries per-turn `last_engaged`, `last_engaged_turn` and `turn_count`, and decay reads `last_engaged_turn` "stored in the thread registry record". SPEC §3.2 and §3.0.4 share this conflict.
29. **Projection ranking.** It ranks "(1) class: a high-specificity identifier above an ordinary symbol *of equal count*; (2) `count` descending". It is unclear whether class or count is primary.
30. **Anchor length.** The verify constraint "`anchors[i]`… 3–50 chars" conflicts with the projection, which ranks identifiers (case-preserved URLs and paths) first, and verify gates history writes (flagged via ROADMAP).
31. **"Derived" anchors.** `anchors` are marked `[derived]`, but "`personant index rebuild` does not regenerate them, and no drift check covers them". The Derived row says "Regenerable", and THESIS's Indexes row says "rebuildable".
32. **Tense.**
    - "Tense": "present tense states the production design… the production service and plugin are not built".
    - Elsewhere, present tense describes current code: "Cold storage archival (implemented…)", and the tracked-file section "describes the current code, not the intended design".
33. **Baselines.**
    - "Tense": carrying "the committed baselines" to the build branch "is ROADMAP A2".
    - "Acceptance-run mechanics" and "Invariant for the production phase split" cite `internal/scenarios/testdata/baselines/` and `just sim-baseline` as present.
34. **Consolidate trigger.**
    - "Memory consolidation": outside the simulation, `Consolidate` "runs only from the turn-repository loose-object check".
    - "Mechanism names": that check "runs gc", and `Consolidate` runs on the simulation's day off.
35. **Restore fallback.**
    - The restore step 2 fallback reads "the deletion commit's first parent".
    - "Archive index" says walking the first parent "picks the wrong lineage on a merge commit".
36. **Log rotation.** The event log is described as "daily-rotated" and "daily rotation", while `logs/archive/` is annotated "log rotation is not built".
37. **Example inconsistencies (minor).**
    - `"(3,2)-torus knot"` normalizes to `3-2-torus-knot`, but under the entity rule (punctuation stripped, whitespace hyphenated) it would be `32-torus-knot`.
    - The display example shows `[WIP]`, against "lowercase names".
    - The bootstrap example shows `home-format=1`, though it records the effective revision after migration to at least 3.
