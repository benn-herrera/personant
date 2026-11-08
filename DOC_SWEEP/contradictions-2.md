# Round-2 contradictions (package C1, reopened for R2-C1 to R2-C40)

Reviewer: adjagent:architect, on `DOC_SWEEP/` at `5f65693`. C2 applies every class 1 resolution
verbatim, including the optional clarifications for R2-C17 and R2-C25; the dispatcher adds the
R2-C4b ROADMAP line. Dispatcher spot-checks: `memops.DominantSource` is a pure rank
(`internal/memops/memops_symbols.go`), and the managed gitignore block ignores `symbols.jsonl` but
not `digest.json` (`internal/store/init.go`). No owner calls.

I reviewed `DOC_SWEEP/` at `5f65693` and found **no genuine owner calls**. Of the 51 parts, 45 are class 1, one is class 2 (R2-C4b), one is class 4 with no C edit (part of R2-C33), and five are not contradictions.

Passages are cited by their monolith heading. "R-read" means I read the named lines with `sed -n`.

**Things you should know before C2 applies this:**
- **Working tree.** `internal/memops/memops_symbols.go` has an uncommitted change. It only alters a comment at line 115 and doesn't affect any evidence below.
- **Corrections to round-1 rulings.** Three resolutions revise text that round 1 settled:
  - **CT13, partly withdrawn (R2-C11).** CT13's wording "the most frequent source dominates, ties breaking…" is wrong against the code. `DominantSource` is a pure rank and never counts frequency. CT13's "curator label" half stays.
  - **CT18, narrowed (R2-C4a).** "A history-repository write fails if it leaves a derived file stale" becomes a rule about the whole batch: a day rollover or archival batch. A single capture commit or day-commit captures derived files before they are rebuilt.
  - **CT10, reworded (R2-C12).** "authoritative for metadata except the derived `anchors`" becomes "a stored projection".
- **Stale code comments (code findings, not document items).**
  - `fileadapter.go:395-397` names a `turn/archival.go` drain that doesn't exist.
  - `projection.go:16-18` says ever-central symbols rank in the high class, but `rankClass` (`:158-162`) tests only `IsHighSpecificity`.
  - `projection.go:156-157` says "of equal Count".
  - `fileadapter_barrier.go:629-633` says derived files "never exist in primary HEAD" (see R2-C4b).

---

**R2-C1 — Standalone archival**
- **A.** SPEC "Runs only at day rollover": "Archival runs only at the day rollover…". ARCH "Day rollover" → "Archival crash completion": "Archival fires only at the rollover's archival step".
- **B.**
  - The same paragraph: "A standalone batch runs under its own `op=archival` marker, outside any rollover".
  - "Closing a thread": "a standalone `ArchiveThreads` is crash-recoverable through the same roll-forward."
- **Code.** Matches A for product code.
  - `git grep -l -E 'ArchiveThreads?\(' -- '*.go'` returns three non-test files only: `fileadapter.go` (the wrapper at `:400`), `fileadapter_archive.go:204` (the definition) and `memops.go:215,227` (the interface). Every other hit is a test.
  - The non-test grep for `ArchiveThread(` finds no caller of the wrapper.
  - The rollover calls `archiveBatch` directly (`fileadapter_barrier.go:216`). `ArchiveThreads` writes `op=archival` (`fileadapter_archive.go:221`).
- **Record.** CT15 cut "standalone archival remains available".
- **Class 1.**
- **Resolution.**
  - "Archival crash completion": "A standalone batch runs under its own `op=archival` marker, outside any rollover; case 8 is likewise" → "Outside a rollover only tests call `ArchiveThreads`; such a batch runs under its own `op=archival` marker, and case 8 is likewise".
  - "Closing a thread": "`RecoverThread` and `ArchiveThreads` remain public port operations; a standalone `ArchiveThreads` is crash-recoverable through the same roll-forward." → "`RecoverThread` and `ArchiveThreads` are public port operations; no product path calls `ArchiveThreads` outside the rollover (tests do), and such a call is crash-recoverable through the same roll-forward (case 8)."

**R2-C2 — Structural checkpoint**
- **A.** SPEC §3.11 "Checkpoint…": a checkpoint is made "at a clean service-run stop or after a mid-run structural change (creating or closing a thread, creating a project). Every checkpoint commits the whole store working tree".
- **B.**
  - ARCH "Turn transaction and journal": "Structural changes (creating or closing a thread, creating a project) ride the per-turn commit". The per-turn commit stages only its scoped paths (`AddPaths`).
  - SPEC §4.5.8 (2): "committed at the next clean-stop checkpoint or day rollover".
  - SPEC §3.5: a routine close-out "adds no commit path of its own".
- **Code.** Matches B for threads.
  - R-read `turn.go:1341-1351`: "This supersedes the former §3.11 commit-on-structural-change Checkpoint".
  - `fileadapter_recovery.go:220` stages with `AddPaths`.
  - The non-test grep for `\.Checkpoint\(` finds only `commands.go:415`. That function is reached only from `/topic` (`commands.go:132`), `/done` (`:294`) and the closure-queue drain (`closure.go:404`), all strip material.
  - The clean-stop caller was `cmd/chat.go`, deleted by strip WP1 (`git log --diff-filter=D -- cmd/chat.go` → `488de08 2026-11-02`).
- **Record.** Strip WP9: "A5's service takes the structural checkpoint after `StepCreated`" (project creation).
- **Moved last.** The code note dates from `1d76780 2026-07-17`; the SPEC wording from `f724b98 2026-11-07` (sweep).
- **Class 1.**
- **Resolution.**
  - SPEC §3.11: "**Checkpoint (clean service-run stop + mid-run structural) → turn repository.** A Personant **checkpoint** is a commit made at a clean service-run stop or after a mid-run structural change (creating or closing a thread, creating a project). Every checkpoint commits the whole store working tree, hand-edits included, to the turn repository." → "**Checkpoint (clean service-run stop + project creation) → turn repository.** A Personant **checkpoint** is a commit of the whole store working tree, hand-edits included, to the turn repository, made at a clean service-run stop and after a project is created. Creating or closing a thread lands in its turn's own commit."
  - ARCH: "Structural changes (creating or closing a thread, creating a project) ride the per-turn commit and fold into the day at rollover. A hand-edit is therefore absorbed at the next full staging pass: a clean-stop checkpoint, or" → "Creating or closing a thread rides the per-turn commit and folds into the day at rollover; creating a project takes a checkpoint (SPEC §3.11). A hand-edit is therefore absorbed at the next full staging pass: a checkpoint, or".
  - SPEC §4.5.8 (2): see R2-C22.

**R2-C3 — Debris quarantine**
- **A.** SPEC "Quarantine-never-delete": debris removal quarantines "before any removal".
- **B.** ARCH case 4: "→ remove untracked debris…".
- **Code.** Matches A.
  - `recovery.go:577-581`: "sweep untracked in-flight debris (into the same quarantine)".
  - `logs.go:284-310` (`sweepUntrackedDebris`) calls `q.move(rel)` and exempts `logs/` and `recovery/`.
  - ARCH "Start order" ("quarantines untracked files") already matches.
- **Class 1.**
- **Resolution.** Case 4: "→ remove untracked debris in the authoritative-file namespace (excluding `logs/`) →" → "→ move untracked debris in the authoritative-file namespace (excluding `logs/` and `recovery/`) into the same quarantine →".

**R2-C4a — When derived files are fresh**
- **A.**
  - SPEC §3.11: "the day's commit passes … and leaves derived files fresh".
  - SPEC §8.3: "Every day-commit and archival write … leaves … its derived files fresh".
- **B.** ARCH "Day rollover" steps 5→6→9 (commit, rebuild, assert); the capture commit is checked for integrity only.
- **Code.** Matches B.
  - R-read `fileadapter_barrier.go:298-313`: the day-commit (B2), then `RebuildDerived` (B3).
  - `:409-412`: "derived legitimately lags the just-staged worktree until B3".
  - `git grep -n CheckDerivedFresh -- 'internal/memops/fileadapter/*.go' ':!*_test.go'` → only `fileadapter_archive.go:493` and `fileadapter_barrier.go:1008`, both deletion commits.
- **Class 1.** This narrows CT18 (see the corrections above).
- **Resolution.**
  - §3.11: "and leaves derived files fresh." → ", and the rollover leaves derived files fresh."
  - §8.3: "Every day-commit and archival write to `<store-dir>/.git/` leaves the memory store structurally sound (what `personant verify` checks) and its derived files fresh (what `personant index check` checks)." → "Every write to `<store-dir>/.git/` leaves the memory store structurally sound (what `personant verify` checks), and every day rollover and archival batch leaves its derived files fresh (what `personant index check` checks)."
  - SPEC §2.1: "a write to the **history repository** (the memory store's permanent day-grain git repository, §3.11) fails if it leaves" → "a day rollover or archival batch, which writes the **history repository** (the memory store's permanent day-grain git repository, §3.11), fails if it leaves".
  - In both the ARCH "Storage non-negotiables" row and the Derived row, replace "a history-repository write that leaves a derived file stale" / "a history-repository write fails if it leaves a derived file stale" with "a day rollover or archival batch that leaves a derived file stale" / "a day rollover or archival batch fails if it leaves a derived file stale".

**R2-C4b — Whether derived files are in the history repository**
- **A.** ARCH "Quarantine-and-proceed": "derived paths, absent from the history repository".
- **B.** Code tracks `digest.json`.
  - R-read `init.go:701-751` (`seedGitignoreBody`): `symbols.jsonl` is ignored with the stated reason "Single-source-of-truth: tracking the derivation alongside its canonical source creates a disagreement risk". `digest.json` is not ignored.
  - `git grep -n digest -- internal/store/init.go` → only `:566` and `:687`, neither of them an ignore line.
  - `fileadapter_barrier.go:629-633` assumes derived artifacts "never exist in primary HEAD".
- **Moved last.** The `digest.json` writer dates from `feaf7b3 2026-05-09`. The ignore rule came at `6fa2515 2026-05-19` ("derived data, single source of truth") and the barrier's assumption at `7ce5aef 2026-07-18`. The tracking is older, but it breaks the code's own later rule.
- **Class 2.** The document states the design; no doc edit.
- **ROADMAP Near-term draft:** "- [ ] **`digest.json` is tracked in both repositories.** The managed gitignore block (`internal/store/init.go` `seedGitignoreBody`) ignores `symbols.jsonl` as derived but not `projects/prj_<n>/digest.json`, so the day-commit step captures each digest before the derived-rebuild step refreshes it, and a history-repository commit can pair a `spine.jsonl` with a stale digest. Quarantine-and-proceed treats derived files as absent from history-repository HEAD (`internal/memops/fileadapter/fileadapter_barrier.go`). Ignoring it changes the stored `.gitignore` and needs the tracked copies removed from both repositories (CONVENTIONS "Pre-production: no transition scaffolding"). (ARCHITECTURE "Store directory layout", "Day rollover".)"

**R2-C5 — `.git/` filed as operational**
- **A.** The ARCH storage table's Operational row lists `.git/`.
- **B.** SPEC §2.1 defines operational files as runtime state, "neither authoritative nor derived". SPEC (5) calls the history repository "irreplaceable", and archived threads' bytes live only there.
- **Code.** `.git/` is ignored (`init.go:707`). Archival removes thread directories (`fileadapter_archive.go:461` `RemoveAll`, as in CT36).
- **Class 1.**
- **Resolution.**
  - Operational row: drop "`.git/`, " from the examples. In its cell, "both repositories' git-dirs included, so" → "`.git/` and `.git-daily/` included, so".
  - After the table add: "`.git/`, the history repository, is in no row: system-managed and gitignored like the operational files, it is the only home of archived threads (SPEC §3.8.1) and so irreplaceable; a missing one is refuse-to-open."

**R2-C6 — "Tag strip"**
- **A.** ARCH "Hook-driven turn phases", `IngestModelResponse`: "tag strip (remove the topic tag from the stored and shown response)".
- **B.** ARCH "Topic-tag mechanics" Open U3; SPEC §5.1.1: "under opencode this has no mechanism".
- **Code.** R-read `git show probe-path:internal/turn/probe_phases.go` lines 150-200. `prompt.Parse` sets `t.body`, which `CloseTurn` returns. The full content is journaled and goes into the delta. opencode's text is not touched.
- **Class 1.**
- **Resolution.** → "tag strip (from the body the service keeps for in-run history; whether the text opencode stores and shows is stripped is U3, see "Topic-tag mechanics")".

**R2-C7 — Unclosed `*new-topic`**
- **A.** SPEC "Unclosed inner literal": the unclosed form is accepted.
- **B.** ARCH validates entries against `thr_\d+|\*new-topic\*`, and the drafts table says the same.
- **Code.** Matches A: `parser.go:169-181` `newTopicUnclosed` ("parseThreadList accepts it … normalized to NewTopicLiteral").
- **Class 1.**
- **Resolution.**
  - "Topic-tag mechanics": "each entry validated against `thr_\d+|\*new-topic\*`." → "each entry validated against `thr_\d+|\*new-topic\*`, the unclosed `*new-topic` accepted and normalized to `*new-topic*` (SPEC §5.1.2)."
  - Drafts table: "must match `thr_\d+` or `*new-topic*`;" → "must match `thr_\d+` or `*new-topic*` (the unclosed `*new-topic` is normalized to it);".

**R2-C8 — Per-turn opening-line contract**
- **A.** SPEC §2.8: "every other line a turn produces follows it".
- **B.** ARCH `BeginTurn` runs the day rollover, staged-symbol expiry and pre-event deltas before the `user.prompt` delta.
- **Code.** Matches B.
  - Order in `turn.go`: `MaybeDayBarrier` at `:787`, the `staging.evicted` log at `:881-883`, pre-event deltas at `:897-903`, then `user.prompt` at `:906`.
  - The grader keys only on markers logged after the prompt (`tagfidelity.go:510-516`).
- **Moved last.** The contract sentence dates from `b85337f 2026-07-16`; the pre-prompt order from `f0e85fb`/`2ec27c7 2026-05-14`.
- **Class 1.**
- **Resolution.** "Exactly one `context.modified source=user.prompt` line per prompted turn, and every other line a turn produces follows it before the next turn's opening line; measurement segments" → "Exactly one `context.modified source=user.prompt` line per prompted turn. Lines from the turn's preparation — the day rollover, staged-symbol expiry and deltas that arrive ahead of the prompt — precede it; every other line the turn produces follows it before the next turn's opening line. Measurement segments".

**R2-C9 — Recall-fidelity exceptions**
- **A.** SPEC §9.4: "The one declared exception is the active-set oracle-agreement check".
- **B.** ARCH "Archived thread set" row: the archive index (`store.LoadArchiveIndex`) feeds the archival-forgiveness filter.
- **Code.** Matches B: `harness_run.go:803` `refreshArchivedSet`.
- **Moved last.** The code dates from `29f2306 2026-05-30`; the SPEC sentence from `60a6bd4 2026-10-31`.
- **Class 1.**
- **Resolution.**
  - SPEC: "The one declared exception is the active-set oracle-agreement check above, which compares the generator's model with the memory service's active set." → "Two reads are declared exceptions: the active-set oracle-agreement check above, which compares the generator's model with the memory service's active set, and the set of archived threads, read from the archive index (§3.8.1) so that an archived expected match is excluded rather than counted a miss (§3.8.4)."
  - ARCH oracle row: "The declared exception to" → "One of the two declared exceptions to".

**R2-C10 — `defaults.md`**
- **A.** ARCH layout: "read-only after install".
- **B.** The storage table says `directives/*.md` are "hand-editable"; SPEC §2.6 says a direct edit "is not prevented".
- **Code.** `init.go:48-52` and `:140-148`: `defaults.md` is "always rewritten to match the binary" on every init.
- **Class 1.**
- **Resolution.**
  - Layout: "# baked-in defaults (read-only after install)" → "# baked-in defaults; rewritten from the binary by every `personant init`".
  - Authoritative row: "Source of truth; hand-editable." → "Source of truth; hand-editable (`directives/defaults.md` is rewritten by every `personant init`)."
  - SPEC unchanged.

**R2-C11 — How the dominant source is chosen**
- **A.** ARCH "Key abstractions": a pure dominance order.
- **B.** ARCH "Symbol source": "the most frequent …; ties break". The `SymbolRecord` comment: "most common source".
- **Code.** Matches A: `memops_symbols.go:260` `DominantSource` compares `sourceRank` only, and `index/symbols.go:91` folds every emission through it.
- **Class 1.** This partly withdraws CT13 (see the corrections above).
- **Resolution.**
  - "Symbol source": "When several sources emit a symbol, `source_dominant` is the most frequent across its symbol-history entries; ties break `curator > user > model > deterministic`." → "When several sources emit a symbol, the highest-ranked wins, `curator > user > model > deterministic` (`memops.DominantSource`): within a turn, across turns and in `source_dominant`."
  - `SymbolRecord`: "// most common source across emissions" → "// highest-ranked source across emissions (see "Symbol source")".

**R2-C12 — Whether anchors are stored**
- **A.** ARCH "Anchors and symbol history": "`anchors` is **not** a separately stored set". `[derived]` labels appear in `SpineRecord` and in "Key abstractions".
- **B.** "Thread registry record": stored, "`personant index rebuild` does not regenerate them".
- **Code.** Matches B: `engage.go:622-623` writes `rec.Anchors` and `fm.Anchors`.
- **Class 1.** This rewords CT10 (see the corrections above).
- **Resolution.**
  - "`anchors` is **not** a separately stored set — it is the deterministic" → "`anchors` is the deterministic".
  - After "re-derived each owner turn" add "and stored in the thread registry record and frontmatter".
  - `SpineRecord`: "// [derived] 0..anchor.projection-max (8)" → "// projected from history_symbols and stored; 0..anchor.projection-max (8)". "// [derived] owner turn of last projection change" → "// owner turn of last projection change".
  - Key abstractions: "`[derived]` 0..8 normalized symbols, projected" → "0..8 normalized symbols, projected … and stored in the record,".
  - "Thread directory": "except the derived `anchors`" → "except `anchors`, a stored projection".

**R2-C13 — When the registry record changes**
- **A.** SPEC §3.2: "the thread's registry record changes only when its anchor set does". ARCH: "the record is rewritten only when the projected anchor set differs".
- **B.** Per-turn `turn_count`, `last_engaged` and `last_engaged_turn`.
- **Code.** Matches B: R-read `engage.go:637-650`. Every engagement writes `rec` (`EngageThread{Spine: rec}`). Only `AnchorsProjectedAtTurn` is gated on `changed` (`:630-632`).
- **Class 1.**
- **Resolution.**
  - SPEC: "re-derives the thread's anchors (§2.7.4); the thread's registry record changes only when its anchor set does." → "re-derives the thread's anchors (§2.7.4) and records them in its registry record."
  - ARCH: "the record is rewritten only when the projected anchor set differs from the prior one; `anchors_projected_at_turn` is then set to the current owner-turn index." → "the projected anchors are written to the record; `anchors_projected_at_turn` is set to the current owner-turn index only when the projected set differs from the prior one. Every engagement rewrites `last_engaged`, `last_engaged_turn` and `turn_count` (SPEC §3.2)."

**R2-C14 — Projection ranking**
- **Side.** ARCH "Projection": "(1) class: a high-specificity identifier above an ordinary symbol of equal count; (2) `count`".
- **Code.** Class is primary over any count: `projection.go:137-141` (`symbolRankLess`), `:158-162`.
- **Class 1.**
- **Resolution.** "a high-specificity identifier above an ordinary symbol of equal count;" → "a high-specificity identifier (URL, file path, git SHA) above any ordinary symbol, whatever their counts;".

**R2-C15 — Tense**
- **A.** "Tense": the baselines are ROADMAP A2.
- **B.** "Acceptance-run mechanics" cites `testdata/baselines/` and `just sim-baseline` as present; "Cold storage archival (implemented…)".
- **Code.** `git ls-files internal/scenarios/testdata/baselines` → empty; `git grep -n -E '^sim-baseline|^sim-fidelity' -- justfile` → empty. Both exist on `probe-path` (`git ls-tree`; `git show probe-path:justfile` at `:487`, `:495`). ROADMAP A2 is the record.
- **Class 1.** "Invariant for the production phase split" states the production design, so it stays.
- **Resolution.**
  - "Cold storage archival (implemented; `…fileadapter_archive.go`," → "Cold storage archival (`…fileadapter_archive.go`;".
  - "with baselines under `testdata/baselines/<scenario>.json`. `just sim-baseline` records the committed baseline for a duration and `just sim-fidelity` re-runs the mock sim under a chosen turn executor and compares against it." → ". The baseline comparator — committed baselines under `testdata/baselines/<scenario>.json`, `just sim-baseline` (records one for a duration) and `just sim-fidelity` (re-runs the mock sim under a chosen executor and compares) — is the prototype's, on `probe-path` (ROADMAP A2)."

**R2-C16 — What the loose-object check runs**
- **Side.** "Mechanism names": the check "runs gc"; "Memory consolidation": it runs `Consolidate`.
- **Code.** `fileadapter_recovery.go:271-286` (`MaybeGC`) calls `a.Consolidate(...)`. `fileadapter_archive.go:141-175` runs gc on both repositories. The simulation calls it at `harness_run.go:864`.
- **Class 1.**
- **Resolution.** "Packing: after every … runs gc at `gcLooseObjectThreshold` (5000); `MemoryOps.Consolidate` (go-git `RepackObjects` + `Prune`) packs both repositories on the simulation's day off (the harness's `SleepCycle` step)." → "Packing: `MemoryOps.Consolidate` (go-git `RepackObjects` + `Prune`) packs both repositories. After every `gcCheckEveryCommits` (64) turn-repository commits the adapter reads the turn repository's loose-object count and runs `Consolidate` at `gcLooseObjectThreshold` (5000); the simulation's day off (the harness's `SleepCycle` step) also runs it."

**R2-C17 — Not a contradiction.** The first-parent fallback serves only entries written before `parent_commit_hash` existed (`fileadapter_archive.go:578-591`), and Personant makes no merge commits (`git grep -n -i merge -- internal/autogit/*.go ':!*_test.go'` → none).
- **Optional clarification.** Step 2: "an entry lacking it is restored" → "an entry lacking it (written before the field existed) is restored", and append ", the capture commit, since Personant makes no merge commits".

**R2-C18 — Key for project-scoped directives**
- **Side.** SPEC §2.6 example: "project name for project-scoped".
- **Code.** `directives.go:52-55` uses the path `directives/<projectID>/project.md`. The frontmatter `project` field is not read (`:21-25`).
- **Class 1.**
- **Resolution.** "# null for defaults/user; project name for project-scoped" → "# null for defaults/user; the project's id (prj_<n>) for project-scoped".

**R2-C19 — Examples**
- **(a) Class 1.** `normalizeEntity` (`memops_symbols.go:171-190`) turns "(3,2)-torus knot" into the tokens "32-torus" and "knot". Change `normalized: "3-2-torus-knot"` → `normalized: "32-torus-knot"`.
- **(b) Class 1.** `workset.go:216-217` uppercases the state. Display form: `[<state>]` → `[<STATE>]`, and add "(the state name uppercased)".
- **(c) Not a contradiction.** `version=0.1.0 home-format=1` is a consistent pair: format 2 arrived at `c656f8d 2026-11-01`, after 0.2.0 (`9d9bf23 2026-08-04`).

**R2-C20 — "Budget check runs last"**
- **Code.** `chain.go:89-123`: step 4, the budget check, then step 5, logging.
- **Class 1.**
- **Resolution.** "Budget check runs last." → "Budget check runs after dedup; logging runs last."

**R2-C21 — What counts as engaged**
- **Code.** Engagement follows the tag (`state.coalesce.threads`, `engage.go:105`). The owner default is injected into the same set (`:118-122`). Symbols do not engage.
- **Class 1.**
- **Resolution.** "currently **engaged** (referenced by a turn's symbols or by its **topic tag**, the line that opens each model response and names the threads the turn engages, §5.1; §3.2)" → "currently **engaged** (named by a turn's **topic tag**, the line that opens each model response and names the threads the turn engages, §5.1, or bound as its owner by the §3.3 fallback; §3.2)".

**R2-C22 — "Never reverted"**
- **Code.** `recovery.go:584-590`: a hand-edit is "indistinguishable from op debris, so the revert keeps a byte-exact copy".
- **Class 1.** Builds on CT16.
- **Resolution.**
  - (2): "A hand-edit to an authoritative file is committed at the next clean-stop checkpoint or day rollover, not with the next turn, and is never reverted." → "A hand-edit to an authoritative file is committed at the next checkpoint (§3.11) or day rollover, not with the next turn. Recovery never reverts it, except where a torn turn's reset covers its file; its bytes are then preserved in quarantine first ((4), §2.1)."
  - (4): "a hand-edit is never reset; a torn turn is reset to the last committed turn after its dirty bytes are quarantined;" → "a hand-edit is never reset outside a torn turn; a torn turn is reset to the last committed turn after its dirty bytes, a hand-edit's included, are quarantined;".

**R2-C23 — Torn-turn content**
- **Record.** ARCH "Start order and resume" (reconcile) and "Turn transaction" (the journal is "never auto-replayed"). Neither is built in this tree.
- **Class 1.**
- **Resolution.** (3): "they are never replayed into memory. The user re-issues if they choose." → "the journal bytes are never replayed into memory. A torn turn that opencode kept is re-ingested from opencode's stored messages ((10)); otherwise the user re-issues if they choose."

**R2-C24 — Durability bar**
- **(a) Journal-write failure: class 1.** `turn.go:855-861` aborts the turn when the prompt journal write fails.
  - §4.5.8 and §3.11: "recoverable from the moment the prompt is submitted" → "recoverable from the moment the prompt is journaled; a failed journal write aborts the turn ((7))".
- **(b) History-repository loss: not a contradiction.** The bar is stated "under any termination timing", and losing the repository is storage loss, which (7) already lists.

**R2-C25 — Not a contradiction.** These are two checks with the same parameter. Keeping a diff compares it with the new content (`diff.go:35-42`); rendering compares it with the current size (`plan.go:158-175`).
- **Optional edit.** In the §2.6.1 comment, after "(§3.9.1)" add "; a kept diff at or above it of the file's current size does not render (§3.9.2)".

**R2-C26 — Slot budgets vs. the ceiling**
- **Record.** The same section: scaffolding is charged to no slot, and there is no tool-definition slot.
- **Class 1.**
- **Resolution.** "so a request within its slot budgets is within the ceiling." → "so the memory slots stay within their share of the ceiling."

**R2-C27 — Slot count**
- **Record.** `livebudget.go:64-66` (live-turn shares).
- **Class 1.**
- **Resolution.**
  - "Every slot — the five memory slots and the three above —" → "Every slot — the five memory slots, the live-turn reserve and the three above —".
  - "directives, project conventions and instructions, the registry" → "directives (project convention files included), instructions, the registry".

**R2-C28 — Decay units**
- **Code.** `closure.go:226-247` (`decayEligible`): idle turns ≥ the turn threshold, or wall-clock time since `last_engaged` ≥ the time threshold.
- **Class 1.**
- **Resolution.** "Engagement decay fires when `last_engaged` exceeds `engagement.decay-turns` OR `engagement.decay-time` (§2.6.1):" → "Engagement decay fires when a thread has gone unengaged for at least `engagement.decay-turns` turns OR at least `engagement.decay-time` since its `last_engaged` (§2.6.1):".

**R2-C29 — "Three passes"**
- **Code.** No summarizer-sourced history symbol exists (CT13). `git grep -n -i 'three passes' -- ':!PROJECT_DOCS' ':!DOC_SWEEP'` → nothing cites the heading.
- **Class 1.**
- **Resolution.**
  - SPEC heading → "### 3.3 Symbol extraction (two passes)". "Two passes run per §3.0.2 step 1, cheapest first; the third runs when a thread closes:" → "Two passes run per §3.0.2 step 1, cheapest first:".
  - Replace item 3 with a paragraph after the list: "The summarizer, which drafts the closing summary when a thread closes (§3.5), does **not** select anchors and contributes no symbols: the anchor set is the deterministic projection of retained `history_symbols` (§2.7.4)."
  - ARCH heading → "Symbol extraction (two passes ordered cheapest-first)". Item 3 → the paragraph "At close the summarizer drafts the closing summary (§3.5); it contributes no symbols, and anchors are not summarizer-picked (below)."

**R2-C30 — Read-only turns**
- **Code.** `chain.go:80-85`: `fs.read` buffers like `fs.write`.
- **Record.** CT2's ROADMAP Near-term item (session-as-thread gives every turn an owner) removes the calibration question.
- **Class 1.**
- **Resolution.**
  - SPEC: "(buffered §3.9 file edits)" → "(buffered §3.9 file reads or writes)".
  - ARCH: cut "; whether such a turn should count as conversational is an open calibration question".

**R2-C31 — File count per thread directory**
- **Code.** The turn path writes loose files; the rollover bundles them (ARCH placement rule; `HotExcerptTail`).
- **Class 1.** Restores CT66's clause.
- **Resolution.** "The number of files in a thread's directory does not grow with its count of excerpts." → "The number of files in a thread's directory does not grow with its count of excerpts: the day rollover moves older excerpts out of individual files, and only an excerpt too large to move stays a file of its own."

**R2-C32 — "Never rewritten" vs. the database cleanup**
- **Record.** SPEC §1.7 and CT63.
- **Class 1.**
- **Resolution.** "opencode's stored transcript is never rewritten and its tool-result rows are never edited:" → "dedup never rewrites opencode's stored transcript or edits its tool-result rows (the §1.7 database cleanup is separate):".

**R2-C33 — Cross-project auto-fetch**
- **Core: not a contradiction.** The shared fetch path declines another project's thread (`recall.go:424-436`, `lru.go:178`).
- **`wrong-project` and the Banding "across projects" clause: class 4.** The strip inventory row 7 retires §4.3 and Banding. No C edit (CT60 precedent).
- **§2.6.1 comment: class 1.** ROADMAP Mid-term "Cross-project recall" is the record.
  - "# higher bar for cross-project surfacing" → "# higher bar for cross-project surfacing; cross-project recall is not built (§3.2)".

**R2-C34 — Tool-output cap**
- **Code.** `livebudget.go:64-66`, `:74-83`, `:129-141`: the cap is 15% of the live-turn reserve. It isn't a directive key, and the only override is the token ceiling's constructor argument.
- **Moved last.** The SPEC text dates from `4474610 2026-05-09`; the code from `833a443 2026-07-29`.
- **Class 1.**
- **Resolution.** "(default 8 KB; configurable)." → ": 15% of the live-turn reserve (`live_turn`, §2.6.1)."

**R2-C35 — Lifecycle tags on gap days**
- **Code.** `fileadapter_barrier.go:717`, `:752-764`: tags are filtered to the rollover's own day.
- **Class 1.**
- **Resolution.** After "…reads the store as it stood then." add: "A life event on an earlier day on which no rollover ran is not tagged."

**R2-C36 — "As if the turn never happened" vs. the event log**
- **Code.** `fileadapter.go:675-683`: `EmitDelta` logs the source and byte count only, and the log is append-only.
- **Class 1.**
- **Resolution.** "Before the first authoritative write, an interrupt leaves memory exactly as if the turn never happened: turn count, staged symbols, active and dormant sets, symbol history and recall marks included, and the next start" → "Before the first authoritative write, an interrupt leaves memory exactly as if the turn never happened — turn count, staged symbols, active and dormant sets, symbol history and recall marks included — except that the event log keeps the lines the turn already wrote (sources and byte counts, never text) and gains its `system.turn-aborted` line; the next start".

**R2-C37 — "No uncomposed request" vs. fail-open**
- **Record.** ARCH "Failure handling" and U7.
- **Class 1.**
- **Resolution.** "so no model request reaches the provider uncomposed (SPEC §1)." → "so no model request reaches the provider uncomposed while the plugin answers its hooks (SPEC §1); on a hook failure the prototype fails open (see "Failure handling", U7)."

**R2-C38 — The missing inter-thread embedding path**
- **Code.** `measure.go:1191-1197` (`coarseFine`) runs a coarse pass over every thread's body vector, then a fine pass. `turn/recall.go:297` logs `spine.embed-match-fire`.
- **Class 1.**
- **Resolution.** Path 2: "that this turn did not engage, the thread is brought forward" → "that this turn did not engage — or, with an embedder configured, the turn's input is close in embedding similarity to a thread's indexed body — the thread is brought forward". Append: "The embedding scan (`measure.Service`, `coarseFine`) selects the top threads by body vector and scores each by its best chunk; it is not limited to the active project, and another project's candidate is declined at the fetch (SPEC §3.2)."

**R2-C39 — Consolidation review not built**
- **Record.** ARCH "Memory consolidation" (the keep/toss stage is not built; ROADMAP Far-horizon).
- **Class 1.**
- **Resolution.**
  - SPEC §1.8: "(the offline cycle that reviews and packs memory)." → "(the offline cycle that reviews and packs memory; the review is not yet built, §3.10.8)."
  - ARCH "Memory vetting": "by the slots, the turn and history repositories and consolidation (see" → "by the slots and the turn and history repositories; consolidation's content review is not built (see "Memory consolidation",".

**R2-C40 — Minor wording, each part class 1**
- **(a) §3.9.4 heading vs. body.** Heading "For active reasoning over content, content-id-only is insufficient" → "Active reasoning keeps full content at the most recent position".
- **(b) Content id vs. prefix.** `identifier.go:24-35` writes 12 hex for a content id and 7 for a commit. §3.9.1: "operation, content id (a commit hash for `fs.commit`) and path" → "operation, a prefix of the content id (of the commit hash for `fs.commit`) and path".
- **(c) ~365 commits plus archive commits.**
  - §3.11: "The history repository is bounded to ~365 commits a year," → "The history repository grows by at most one day-commit a day (~365 a year) plus archival's bounded commits,".
  - (5) table: "(~365 commits/year)" → "(~365 day-commits/year)".
- **(d) Digest "current".** Only the rollover, archival, restore and `index rebuild` regenerate digests: the non-test `RegenerateDerivedState` callers are in `fileadapter_archive.go`, `fileadapter_barrier.go` and `cmd/index.go`, and none in `internal/turn`.
  - §2.5.2: "and current with them" → "; between day rollovers it may lag them (§2.1)".
  - §3.7: "Each project's digest stays current with its threads and within" → "Each project's digest is brought current with its threads at each day rollover and archival, and stays within".
  - ARCH: "regenerated whenever any thread in the project updates" → "regenerated with the other derived files: at the day rollover, archival, restore and `personant index rebuild`".
- **(e) Hard cap vs. flat history.** `history.go:274-275`: "the cap … is never exceeded". §2.7.4: "flat history length and once-central count over a run is a §9 sim obligation, not a static guarantee" → "the history never exceeds its cap; that it stays below the cap with a flat once-central count over a run, so protection never has to yield, is a §9 sim obligation, not a static guarantee".
- **(f) Refuse-to-open vs. "stays refused".** `stamp.go:79-90`: an unrepairable entry is skipped and the open proceeds. (4): "stays refused, never guessed (`recovery.unrepairable`);" → "stays refused for restore, never guessed (`recovery.unrepairable`), and does not stop the open;".
- **(g) "Daily-rotated" log.** The log is one file per day (`eventlog.go:75`); moving old files is not built.
  - Module map: "daily-rotated plain-text event log" → "plain-text event log, one file per day".
  - Layout: "daily rotation" → "one file per day"; "(log rotation is not built)" → "(moving old day files there is not built)".

---

## Counts by class (51 parts)
- **Class 1:** 45. R2-C1, C2, C3, C4a, C5–C16, C18, C19a, C19b, C20–C23, C24a, C26–C32, the C33 §2.6.1 comment, C34–C39, and C40a–g.
- **Class 2:** 1 (R2-C4b).
- **Class 3:** 0.
- **Class 4:** 1 (the C33 `wrong-project` and Banding clause; no C edit).
- **Not a contradiction:** 5 (R2-C17, C19c, C24b, C25, the C33 core).

## Class-3 items
None. Two items came close, and the written record settles both:
- **R2-C4b.**
  - Reading 1: gitignore `digest.json`.
  - Reading 2: document it as tracked.
  - Settled by: the code's own derived-file rule (`6fa2515`, the `init.go` comment), the barrier's assumption and ARCHITECTURE all take reading 1.
- **R2-C10.**
  - Reading 1: an accepted feedback-tuning change to `defaults.md` is a change to the shipped defaults.
  - Reading 2: the store's copy must survive re-init.
  - Settled by: ARCHITECTURE "Personalization" ("system defaults, baked in") and `init.go:48-52` take reading 1. Nothing writes the file today.
