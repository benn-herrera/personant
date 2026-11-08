# Contradictions (package C1)

Reviewer: adjagent:architect, on `DOC_SWEEP/` at `af53921`. C2 applies every resolution below
verbatim. Classes: 1 the document moves; 2 the code is wrong (ROADMAP Near-term line); 3 owner
call; 4 strip cut.

**Triage against the record.** The reviewer filed CT2 and CT64 as class 3. Both are settled by
the record, and the triage at each item says by what: CT2 becomes class 2 (session-as-thread
routing, strip plan §3 and WP10's U12 entry, gives every turn an owner), CT64 class 1 (SPEC §1,
"Personant owns what enters every model request"). No item goes to the owner.

**Summary.** 73 contradictions were checked; 67 are written up as CT1–CT67. After triage: 48
class 1, 2 class 2 (CT2, CT62), no class 3, and 17 class 4 (pure strip cuts or strip-plus-class-1).
Six items are dismissed with evidence (section D); the rest of the 73 are dispositioned in
section E.

**Method notes**
- **Locating passages.** Each passage was found in the current `DOC_SWEEP/*.md` by its quoted text and is cited by its monolith heading. The DOC_SWEEP copies were read whole at HEAD `af53921`.
- **Code checks.** Each code citation gives a `git grep` that I ran and its answer, or a `sed -n` read of the named lines (R-reads).
- **Moved last.** The brief's `git log -1 --format='%h %ad' --date=short -- <path>` was run on each file; the tables below hold the answers once.
  - Most `PROJECT_DOCS/` leaves were last touched by broad commits: `d5e3899` (dice move, 2026-11-01), `5b07ef7` (name sweep, 2026-11-02), and `9a3bc9c` / `b900840` (dedup plan, 2026-11-02). File grain therefore says little about when a sentence was written.
  - Where the class depends on order, I added `git log --all -S'<phrase>' --reverse --format='%h %ad' | head -1`, the sentence's first appearance.
- **"Class 1 (record)"** means code settles neither side, or isn't built, and the written record settles it. The record is cited in each item.

**Code files: last commit**
- `internal/turn/projection.go` 33895e5 2026-06-13
- `internal/turn/engage.go`, `excerpt.go`, `turn.go` 9a3bc9c 2026-11-02
- `internal/turn/closure.go` 71d7679 2026-08-06
- `internal/turn/lru.go` df11746 2026-11-01
- `internal/turn/chain.go` b900840 2026-11-02
- `internal/memops/memops_symbols.go` b0a4d96 2026-05-21
- `internal/memops/memops_types.go` 9a3bc9c 2026-11-02
- `internal/memops/memops_model.go` 488de08 2026-11-02
- `internal/recall/measure/measure.go` 2a54816 2026-08-05
- `internal/recall/scoring/scoring.go` 9b61dbf 2026-05-22
- `internal/memops/fileadapter/fileadapter_archive.go`, `fileadapter_recovery.go`, `internal/recovery/stamp.go` 7ce5aef 2026-07-18
- `internal/memops/fileadapter/fileadapter_barrier.go` b900840 2026-11-02
- `internal/recovery/recovery.go` fab97d6 2026-10-04
- `internal/store/init.go` 9a3bc9c 2026-11-02
- `internal/store/spine_ops.go` ccd325b 2026-05-29
- `internal/store/jsonl.go`, `folio.go` df11746 2026-11-01
- `internal/store/directives.go` 9d9bf23 2026-08-04
- `internal/verify/verify.go` b900840 2026-11-02
- `internal/dedup/identifier.go` 4340d0d 2026-11-01
- `internal/index/symbols.go` 952cc39 2026-07-13
- `internal/prompt/streamfilter.go` d0e4a52 2026-07-16
- `internal/eventlog/bootstrap.go`, `internal/version/version.go` 427a42a 2026-11-07
- `internal/scenarios/sim/workload.go` b900840 2026-11-02
- `cmd/main.go` 488de08 2026-11-02
- `justfile` 8a20388 2026-11-07
- `README.md` af53921 2026-11-07

**`PROJECT_DOCS/` leaves: last commit**
- **SPEC:**
  - §1 index, §1.4, §2.1, §2.2, §2.2.2, §2.3, §2.5.1, §2.7.3, §2.7.4, §9.3: 5b07ef7 2026-11-02
  - §1.8: 69d1dfd 2026-11-05
  - §2.6 index: a450370 2026-11-02
  - §2.6.1, §3.8.3, §3.9.3: 9a3bc9c 2026-11-02
  - §3.9.1, §3.9.2, §3.10.6, §4.5.8: b900840 2026-11-02
  - §9.1 "Version lines": 427a42a 2026-11-07
  - All other cited SPEC leaves: d5e3899 2026-11-01. These are §1.1, every §3.0 leaf, §3.1, §3.3 (index and missing-tag), §3.4 (index, embedding-index, banding, recall-surface), §3.5, §3.8 (index, barrier-homed, 3.8.2, 3.8.4), §3.11 per-turn, §4.2 and vocabulary-bridge, §4.3.3, §4.5.2, §4.5.3, §4.5.7, §5.2, §5.3, §8.2.1, §8.3, §9.1 index, §9.4, §9.4.1.
- **ARCHITECTURE:**
  - module-map 488de08 2026-11-02
  - boundary, plugin-route index, anti-patterns: d3fd294 2026-11-01
  - autonomic-vs-llm-controlled, home-layout, thread-directory, the-3-0-hook-chain index, key-abstractions, closure-and-retirement: 9a3bc9c 2026-11-02
  - crash-stability, the-layered-working-set index: b900840 2026-11-02
  - personalization: a450370 2026-11-02
  - event-log-format: 427a42a 2026-11-07
  - testing index, applying-the-thesis: 5b07ef7 2026-11-02
  - document-map: 7a73f93 2026-11-05
  - All other cited ARCHITECTURE leaves: d5e3899 2026-11-01.

---

## A. Architect spec-with-thesis S1–S36 (`cold-read-answers/adjagent-architect-spec-with-thesis.md`, "### SPEC against itself")

**CT1 — Recall completeness vs. archival and the embed-failure residual** (S1, first and third parts)
- **Side A.** SPEC "3.4 Recall matching", invariant: "All **non-transient** turn content MUST be locatable by recall at every stage of its lifecycle — creation, recent-excerpt-window residency, scroll-out and durable storage, archival (§3.8), and later summarization and closing (§3.5). **The ONLY content that may be unfindable is transient data deliberately discarded**…"
- **Side B.**
  - SPEC "3.8.4 Recall treatment of archived threads": "Archived threads are **never candidates for live recall** in v0.1".
  - SPEC "Embedding index — granularity…": "Accepted residual: a single transient embed failure leaves a gap for that batch…"
- **Code.** Both B sides match the code.
  - `git grep -n -F 'threadSet, supersededSet := buildThreadSet(rec, fmRec, haveFM)' -- internal/recall/scoring/scoring.go` → `scoring.go:127`, inside the loop over spine records (R-read 115–127). An archived thread has no spine record, so it is never a candidate.
  - `git grep -n -i -E 'failed flush' -- internal/recall/measure/*.go ':!*_test.go'` → `measure.go:1358: guarantees against async-flush LAG, not against failed flushes.`
- **Moved last.** scoring.go 2026-05-22 and measure.go 2026-08-05; leaves §3.4 and §3.8.4 2026-11-01 (dice).
- **Record.** `spec-cold-read.md` "SPEC against THESIS": §3.8.4 is a v0.1 scope limit, tracked in ROADMAP Mid-term "Archive recall". The failed-flush gap is ROADMAP Near-term "Flush-job re-enqueue on failure".
- **Class 1 (record).**
- **Resolution.** In the invariant:
  - Delete "archival (§3.8), " from the stage list.
  - Replace the second sentence with: "**The ONLY content that may be unfindable is transient data deliberately discarded** per §3.10 (uncited task-class symbols / raw task-bytes expired at window close, §3.10.5–§3.10.6), and two stated limits: archived threads, which are off live recall in v0.1 and restored by explicit fetch (§3.8.4), and the accepted embed-failure residual (below)."

**CT2 — Recall completeness vs. tag-less conversational turns** (S1, second part)
- **Side A.** The same §3.4 invariant as CT1.
- **Side B.** SPEC "A response without a topic tag never aborts the turn": "**No binding required** (plain conversational turn): the turn proceeds without a topic tag — no engagement update, no excerpt, no owner, no re-prompt".
- **Code.** Matches B.
  - `git grep -n -F 'Tag-less conversational turn' -- internal/turn/engage.go` → `engage.go:106`. R-read 104–116: `return nil` before any excerpt; the comment calls this "deliberate (D6)".
- **Moved last.**
  - engage.go 2026-11-02 (file); the D6 block first appears at `618337f 2026-07-14`.
  - Leaves §3.3 and §3.4 2026-11-01; the invariant first appears at `040addd 2026-06-11`. The code is newer.
- **Class 2 (triage; the reviewer filed class 3).** The reviewer found that D6 is recorded only in
  a code comment and that THESIS does not settle it. The record does: the strip plan's
  session-as-thread decision (`ROADMAP_PLANS/strip-self-built-harness/3-session-as-thread/`,
  WP10's U12 entry) routes every turn to its session's thread, so "The owner is always the
  session's thread" and no turn is left unowned. §3.4's invariant is the requirement; the
  tag-less return is the defect, and A10's routing closes it.
- **Resolution.**
  - Replace the SPEC §3.3 bullet with: "**No binding required** (plain conversational turn): no
    re-prompt; the turn's content is kept like any other turn's (§3.4); `topic.tag-missing` is
    logged for calibration."
  - ROADMAP Near-term: "- [ ] **Tag-less conversational turns are not kept.**
    `internal/turn/engage.go` returns before writing any excerpt for a turn with no topic tag and
    no file edits, so its prompt and response are unfindable, against SPEC §3.4's completeness
    invariant. Session-as-thread routing (A10) gives every turn its session's thread as owner."

**CT3 — "Run in parallel" vs. signal 3 on surfaced candidates** (S2; also vocab SPEC §5 item 5)
- **Side A.** SPEC "3.4 Recall matching": "Three signals, run in parallel, not a cost cascade". Signal 3 is "Model judgment **on surfaced candidates**".
- **Side B.** SPEC "Banding": "(signal 1 is a Jaccard set overlap, signals 2/3 are cosines)".
- **Code.** Signal 3 is not built.
  - `git grep -n -F 'Layer 3 (model' -- internal/recall/measure/measure.go` → `measure.go:313` "Layer 3 (model judgment) will slot in here later".
- **Moved last.** measure.go 2026-08-05; leaves §3.4 and banding 2026-11-01.
- **Class 1 (record)** for the parallel claim; **class 4** for the Banding sentence. The strip deletes "Banding" (`what-it-does.md`: "recall and closure offers" go; inventory row 7).
- **Resolution.**
  - §3.4: "Three signals, run in parallel, not a cost cascade:" → "Three signals; signals 1 and 2 run in parallel, not as a cost cascade, and signal 3 runs on the candidates they surface:".
  - Banding: cut "(signal 1 is a Jaccard set overlap, signals 2/3 are cosines)".

**CT4 — "No separate intra-thread path"** (S3)
- **Side A.** SPEC "Embedding index — granularity…": "One uniform mechanism serves all not-in-context recall; there is no separate intra-thread path."
- **Side B.** The same section: "the automatic intra-thread path searches the engaged thread's not-yet-flushed tail…". ARCHITECTURE "Recall paths" item 4: "The engaged thread bypasses the coarse level".
- **Code.** Matches B.
  - `git grep -n -F 'engaged-' -- internal/recall/measure/measure.go` → `:79` and `:1006` "…a fine chunk pass over the selected threads, and an engaged-thread" intra pass.
- **Moved last.** measure.go 2026-08-05; the leaf is 2026-11-01, but the phrase first appears at `fab97d6 2026-10-04`. The doc is newer and false against older code. The code holds the principles.
- **Class 1.**
- **Resolution.** Replace the sentence with: "One embedding index serves all not-in-context recall. Intra-thread recall searches the engaged thread's own indexed body in it directly, without the coarse level."

**CT5 — File content "never written" vs. the tracked-file store** (S4; ARCHITECTURE "Transient-data lifecycle" says the same)
- **Side A.** SPEC "3.10.6 What stays transient": "**Never** written to the memory store: Raw bytes of task-class deltas (file content from `fs.read`/`fs.write`, …".
- **Side B.** The same section: "File content has a separate persistent home in the per-thread tracked-file store (§3.9.1)". SPEC §3.9.1 puts that store in the store working tree.
- **Code.** Matches B.
  - `git grep -n -F 'store.ApplyFileEvents(' -- internal/memops` → `fileadapter.go:715`.
  - `git grep -n -F 'func ApplyFileEvents' -- internal/store` → `trackedfiles.go:185`.
- **Record.** `spec-cold-read.md` ruling 2.
- **Moved last.** fileadapter.go not run separately; leaf §3.10.6 2026-11-02.
- **Class 1 (ruling 2).**
- **Resolution.** In the first bullet, after "(source, path, byte count)." add: "The one exception is file bytes from `fs.read`/`fs.write`, which the tracked-file store keeps only as long as composition needs them (§3.9.1)." Delete the closing paragraph "File content has a separate persistent home…".

**CT6 — Every content path runs the pipeline vs. composer-only content** (S5; arch-alone §2 item 17; go-coder arch-alone item 22)
- **Side A.**
  - SPEC "3.0 All content enters through one pipeline": "every path by which content enters the working window runs the **per-delta pipeline**".
  - SPEC "3.0.5": "Content reaches the working window only through the §3.0 pipeline; there is no bypass."
  - ARCHITECTURE "The per-delta pipeline (§3.0)": "**No path mutates the working window without going through it.**"
- **Side B.**
  - SPEC "3.0.1": "Other-projects digest content, memory-command effects (`/back-to`, etc.) and directive-file reloads are not pipeline deltas".
  - ARCHITECTURE: "Other-projects slot digest content, session-command effects and directive reloads are not deltas".
  - The opencode slots in §3.1 have no delta type.
- **Code.**
  - Digests render in the composer, not the pipeline: `git grep -n -F 'return truncateToBudget(strings.Join(lines, "\n"), budget.LayerA2'` was not run; the cited answer is the earlier `git grep -n -i 'truncat' -- internal/workset/workset.go` → `workset.go:287` (the other-projects render).
  - The opencode slots are not built (ROADMAP A6), so code settles neither for them. ARCHITECTURE "Injection handling" ("Rebuilt each request") is the record.
- **Moved last.** Leaves §3.0, §3.0.1, §3.0.5 2026-11-01; hook-chain index 2026-11-02.
- **Class 1 (record);** the memory-command part is **class 4** (`what-it-does.md`: memory-native commands go).
- **Resolution.**
  - §3.0: "every path by which content enters the working window runs" → "every delta — every content addition from the session (§3.0.1) — runs".
  - §3.0.5 → "Every delta reaches the working window only through the §3.0 pipeline; there is no bypass. Content the composer assembles from memory already held (§3.0.1) and the opencode-content slots, rebuilt from opencode's input on every request (§1.3), are not deltas."
  - ARCHITECTURE: "**No path mutates the working window without going through it.**" → "**No delta reaches the working window without going through it.**"
  - Cut "memory-command effects (`/back-to`, etc.), " (SPEC) and "session-command effects " (ARCHITECTURE; reads "Other-projects slot digest content and directive reloads are not deltas").

**CT7 — "These are all the deltas" vs. "Budget demotion is itself a delta"** (S6)
- **Side A.** SPEC "3.0.1": "These are all the deltas the memory service emits".
- **Side B.** SPEC "3.0.3": "Budget demotion is itself a delta, but recursive pipeline runs are **capped at depth 1**".
- **Code.** No demotion delta exists; step 4 is a no-op.
  - `git grep -n -F 'TODO(phase-2-budget)' -- internal/turn/chain.go` → `chain.go:98`.
- **Moved last.** chain.go 2026-11-02; leaf 2026-11-01.
- **Class 1.**
- **Resolution.** Third bullet → "Budget check runs last. Budget demotion adds no delta: it emits a log line and is done."

**CT8 — What dedup matches on** (S7)
- **Side A.** SPEC "3.0.3": "Dedup runs after extraction (extracted identifier symbols are what dedup matches against)."
- **Side B.** SPEC "3.9.1": "A file content's id is computed from its bytes alone".
- **Code.** Matches B.
  - `git grep -n -F 'plumbing.ComputeHash(plumbing.BlobObject' -- internal/dedup/identifier.go` → `:12`.
  - `git grep -n -i symbol -- 'internal/dedup/*.go' ':!*_test.go'` → no hits.
- **Moved last.** identifier.go 2026-11-01; leaf 2026-11-01.
- **Class 1.**
- **Resolution.** → "Dedup runs after extraction; it keys file content by content id (§3.9.1)."

**CT9 — `description` never rewritten vs. `/topic` and `/topic rename`** (S8, S13; arch-alone 10; go-coder arch-alone 9)
- **Side A.** SPEC §2.2 / §2.3 and ARCHITECTURE `SpineRecord`: "set once at creation and never rewritten".
- **Side B.**
  - SPEC "4.2" `/topic rename` row: "Writes `description` … and the derived thread registry record".
  - SPEC "2.2.2": "`/topic rename` (§4.2) rewrites that name".
  - ARCHITECTURE "Thread registry record", display paragraph: "A rename rewrites that name".
- **Class 4.** `what-it-does.md`: "memory-native commands … go".
- **Resolution (cuts).**
  - SPEC §4.2: delete the `/topic <name>` and `/topic rename <new-name>` rows.
  - SPEC §2.2.2: delete from "`/topic rename` (§4.2)" through "are untouched." and the sentence "The user-facing rendering is one shape everywhere — `thr_N: display — gist` (§3.4)."
  - ARCHITECTURE: delete "A rename rewrites that name … are untouched. The user-facing rendering is `thr_N: display — gist` (SPEC §3.4)."

**CT10 — Live threads "have no summary"** (S9; arch-alone 11; go-coder arch-alone 10, 11; go-coder arch-with-volumes 15)
- **Side A.**
  - SPEC "2.3": "a live thread has no `summary`".
  - SPEC "2.2": "the `summary` is drafted when the thread closes".
  - ARCHITECTURE "Thread directory": "`summary` is the summarizer's closing summary, in frontmatter only (never in a turn file); a thread that is not closed has no `summary`".
  - ARCHITECTURE `SpineRecord`: "drafted at close".
- **Side B.**
  - ARCHITECTURE display example: a `[WIP]` thread showing a summary.
  - The `SpineRecord` field `summary`.
  - The `thread.md` example lists 2 anchors where the display shows 4, and has no summary.
  - Frontmatter is "authoritative for metadata" while `anchors` is marked "[derived]".
- **Code.** Matches B.
  - `git grep -n -F 'summary := summarizeForNewThread(responseBody)' -- internal/turn/engage.go` → `:692`; it is written to both the record and the frontmatter (R-read 708–738).
  - `wip` is not a closed state: R-read `memops_barrier.go:97–103` (`IsRetiredState` = resolved, decided, abandoned).
- **Moved last.** The code line first appears at `3b61c1d 2026-05-09`. "has no `summary`" first appears at `69f8c71 2026-10-31` ("concision cuts"). The doc is newer and introduced a claim false against older code; the code holds the principles.
- **Class 1.**
- **Resolution.**
  - SPEC §2.2: "the `summary` is drafted when the thread closes (§3.5) by the **summarizer** (…)" → "the `summary` is set from the thread's first response at creation and replaced when the thread closes (§3.5) by the **summarizer**'s draft (…)".
  - SPEC §2.3: "and never rewritten; a live thread has no `summary`." → "and never rewritten; until the thread closes, its `summary` is the one set at creation (its first response, whitespace-collapsed and truncated)."
  - ARCHITECTURE `SpineRecord` comment: "drafted at close" → "set from the first response at creation; replaced by the closing summary".
  - ARCHITECTURE "Thread directory": "in frontmatter only (never in a turn file); a thread that is not closed has no `summary`." → "in frontmatter and the thread registry record, never in a turn file; before closing it is the provisional summary set at creation."
  - "authoritative for metadata" → "authoritative for metadata except the derived `anchors`".
  - In the display example, use the frontmatter's anchors: `thr_88 [trefoil, unknot] — …`.

**CT11 — Exception closing needs a draft that is never made** (S10; architect arch-with-volumes 11; go-coder arch-with-volumes 10)
- **Side A.** SPEC "3.5": "**Exception** — the summarizer draft is empty or failed, OR …". ARCHITECTURE "Symbol extraction" pass 3: "…or one whose draft failed, is queued as an exception". ARCHITECTURE "Closing a thread": "(anchor-rich, long-engaged, or a failed or empty draft)".
- **Side B.** SPEC "3.5": "queued silently: no summarizer call"; "A draft that **fails or comes back empty is never auto-accepted** … retried at the next scan".
- **Code.** Matches B.
  - `git grep -n -F 'func closureException' -- internal/turn/closure.go` → `:330`; R-read 330–337 shows anchors/turns only.
  - `git grep -n -F '"curator-error"'` → `closure.go:452`, `:559` (retry).
- **Class 4.** `what-it-does.md`: "the closure review queue … go"; WP6 retries a failed draft.
- **Resolution (cuts).**
  - SPEC: "the summarizer draft is empty or failed, OR ".
  - ARCHITECTURE pass 3: the sentence "A routine decayed thread takes the draft as written; … (see "Closing a thread")."
  - ARCHITECTURE "Closing a thread": ", or a failed or empty draft".

**CT12 — `turn_count` semantics** (S11; architect arch-with-volumes 8; go-coder arch-with-volumes 11)
- **Side A.** SPEC "3.0.4": "`turn_count` increments by 1 only for the turn's **owner** thread".
- **Side B.** SPEC "3.5": "long-engaged (≥ 15 engaged turns, `turn_count`)". ARCHITECTURE `SpineRecord`: "total turns this thread has been engaged in".
- **Code.** Matches A.
  - `git grep -n -F 'mergeTurn = rec.TurnCount + 1' -- internal/turn/engage.go` → `:575`, under `if owner`.
- **Moved last.** engage.go 2026-11-02; spine-record leaf 2026-11-01.
- **Class 1** for ARCHITECTURE; **class 4** for the SPEC §3.5 criterion (the exception queue goes).
- **Resolution.**
  - ARCHITECTURE comment → "owner turns of this thread (SPEC §3.0.4)".
  - SPEC §3.5: cut ", OR long-engaged (≥ 15 engaged turns, `turn_count`)".

**CT13 — The summarizer as a symbol source and as a per-delta pass** (S12)
- **Side A.** SPEC "2.7.3": source "deterministic extraction, model, user or summarizer … ties breaking summarizer, user, model, deterministic". SPEC "3.3": "Three passes per §3.0.2 step 1".
- **Side B.** SPEC "3.3" pass 3: the summarizer "does **not** select anchors" and runs "when a thread closes".
- **Code.**
  - `git grep -n -F 'memops.DominantSource(b.sourceDominant, memops.SourceCurator)' -- internal/index/symbols.go` → `:91`. In the symbol index, `curator` is the label every anchor receives; no history symbol takes the summarizer as its source (`git grep -n 'SourceCurator'` → only the definition, the rank and `symbols.go`).
  - Closing anchors come from projection: R-read `closure.go:631–662`.
- **Moved last.** symbols.go 2026-07-13; leaves 2026-11-01/02.
- **Class 1.**
- **Resolution.**
  - §2.7.3: "(deterministic extraction, model, user or summarizer; the most frequent source dominates, ties breaking summarizer, user, model, deterministic)" → "(deterministic extraction, model or user; in the symbol index a thread's anchor counts as `curator`-sourced; the most frequent source dominates, ties breaking `curator`, user, model, deterministic)".
  - §3.3: "Three passes per §3.0.2 step 1, cheapest first:" → "Two passes run per §3.0.2 step 1, cheapest first; the third runs when a thread closes:". Pass 3 gets the added sentence "It contributes no symbols."

**CT14 — `last-active`: operational or derived** (S14; arch-alone 7, `last-active` part)
- **Side A.** SPEC "4.5.7": "(operational …)".
- **Side B.** SPEC "4.5.8": "derived state (… the `last-active` file)".
- **Class 4.** `what-it-does.md` project-identity bullet; WP9 drops `last-active`.
- **Resolution (cut).** In §4.5.8's opening, cut ", the `last-active` file". In the ARCHITECTURE layout line `last-active  # operational…`, the strip cuts it.

**CT15 — Archival only at rollover; the history-writer rule** (S15, S16)
- **Side A.**
  - SPEC "4.5.8 (5)", history-writer rule: "written only at the day rollover, with one recovery-repair exemption; the exhaustive set … case-9 back-fill repair, case-12 adopt, archival roll-forward completion — **nothing else**".
  - SPEC "(6)": "standalone archival remains available outside the rollover".
- **Side B.** The list has three writers outside the rollover. The lifecycle tags are history-repository writes and are not listed.
- **Code.**
  - `git grep -n -F 'autogit.Tag(ctx, a.paths, autogit.Primary'` → `fileadapter_barrier.go:780`.
  - `git grep -n -F 'hash, err := autogit.CommitWithHash(ctx, paths, autogit.Primary'` → `recovery.go:647` (adopt).
  - `git grep -n -F 'autogit.Commit(ctx, paths, autogit.Primary, msg'` → `stamp.go:120` (back-fill).
  - `git grep -n -F 'ArchiveThreads(ctx' -- internal cmd` → only the `fileadapter.go:400` wrapper and tests. Standalone archival has no product caller.
  - Restore targets the turn repository: `git grep -n -F 'autogit.Daily, "archive: recover "'` → `fileadapter_archive.go:680`.
- **Moved last.** barrier.go 2026-11-02 and recovery.go 2026-10-04; leaf 2026-11-02. The "one recovery-repair exemption" phrase first appears at `4efe4ea 2026-07-18`.
- **Class 1.**
- **Resolution.**
  - History-writer rule → "**History-writer rule:** the history repository is written by the day rollover (its archival and day-commit steps, and the lifecycle tags it derives) and by three recovery paths: case-9 back-fill repair, case-12 adopt and archival roll-forward completion — **nothing else** (the checkpoint and archive restore target the turn repository). Every such writer — day-commit and archival alike — validates thread registry integrity before minting, so history repository HEAD always passes thread registry integrity validation."
  - In (6), cut "; standalone archival remains available outside the rollover".

**CT16 — Hand-edits are never destroyed vs. the case-4 reset** (S17)
- **Side A.** SPEC "2.1": "never destroyed by crash recovery: it is absorbed (§4.5.8 case 3)".
- **Side B.** SPEC case 4: "reset the store working tree to the **turn repository's** HEAD". Quarantine is stated only for untracked debris.
- **Code.** Dirty tracked bytes are quarantined before the reset.
  - `git grep -n -F 'resetSequence is the marker-gated destructive repair: snapshot dirty'` → `recovery.go:577`.
  - The reset follows at `:607`.
- **Moved last.** The snapshot first appears at `55f767c 2026-07-17`; "is never destroyed by crash recovery" at `cf0b53a 2026-11-07` (sweep wording).
- **Class 1.**
- **Resolution.**
  - §2.1: "it is absorbed (§4.5.8 case 3)." → "it is absorbed (§4.5.8 case 3), or, where a torn turn's reset would revert it, preserved byte-exact in quarantine first (case 4)."
  - Case 4 action: prefix "quarantine dirty tracked bytes byte-exact → ".

**CT17 — Retraction leaves no turn scope vs. the journaled prompt** (S18)
- **Side A.** SPEC "The retraction rule": "no open §4.5.8 turn scope, so the next start opens quiet".
- **Side B.** SPEC (2): the prompt is journaled "before the model call. The first append arms the in-flight signal." The rollback list omits the journal.
- **Code.** The abort releases the scope.
  - `git grep -n -F 'releaseAbortedTurn(ctx, state, turnID)'` → `turn.go:612`.
  - `git grep -n -F 'store.TruncateJournal(a.paths)'` → `fileadapter_recovery.go:319` (ReleaseTurn).
- **Moved last.** ReleaseTurn first appears at `1d76780 2026-07-17`; "no open §4.5.8 turn scope" at `7b24076 2026-10-03`.
- **Class 1.**
- **Resolution.** In consequence 1, after "…to its pre-turn value", add: "and releases the turn's journal scope (§4.5.8 (2))".

**CT18 — Stale derived files and git operations** (S19; arch-alone 6; go-coder arch-alone 12; architect arch-with-volumes 6, 21; go-coder arch-with-volumes 8, 9)
- **Side A.**
  - SPEC "2.1": "a state-changing git operation on the memory store fails while any derived file is stale".
  - SPEC "8.3": "Before and after each state-changing git operation on `<store-dir>/.git/` … derived files are fresh … and … structurally sound".
  - ARCHITECTURE "Storage non-negotiables": "`autogit.CheckDerivedFresh` fails any state-changing git op on stale".
  - ARCHITECTURE drift table: the same.
  - ARCHITECTURE "Closing a thread": "the deletion commit runs `CheckSpineIntegrity` before and `CheckDerivedFresh` after".
- **Side B.** SPEC "(7) Intra-day derived staleness — by design". SPEC "3.11": turn commits carry "no integrity validation". ARCHITECTURE "Automatic vs model-controlled": "turn-repository commits run none".
- **Code.**
  - `git grep -n -E 'CheckDerivedFresh|CheckSpineIntegrity' -- 'internal/**/*.go' ':!*_test.go'` → capture commit pre-flag `fileadapter_archive.go:393` (integrity only); deletion commit post-flag `:493` (both); stamp `:515`; roll-forward `fileadapter_barrier.go:1008`, `:1033`.
  - The turn commit has no flags: `fileadapter_recovery.go:230` `(…, 0, 0)`.
  - The day-commit is verified on the staged tree before minting (R-read `barrier.go:470–481`).
- **Moved last.** Code 2026-07-18 and 2026-11-02; leaves 2026-11-01/02.
- **Class 1.**
- **Resolution.**
  - SPEC §2.1: "a state-changing git operation on the memory store fails while any derived file is stale" → "a history-repository write fails if it leaves a derived file stale (§8.3); between day rollovers derived files may lag by design (§4.5.8 (7))".
  - SPEC §8.3 second paragraph → "Around each day-commit and archival write to `<store-dir>/.git/`, the memory service runs systemic validation inside its own logic: before it, that the memory store is structurally sound (what `personant verify` performs); once it completes, that derived files are fresh (what `personant index check` verifies — `symbols.jsonl` and per-project `digest.json` match their authoritative sources). Turn-repository commits run neither (§3.11)."
  - ARCHITECTURE row: "fails any state-changing git op on stale" → "fails a history-repository write that leaves a derived file stale".
  - Drift cell: "…a state-changing git operation on the store directory fails while any derived file is stale" → "…a history-repository write fails if it leaves a derived file stale; turn-repository commits are not checked".
  - "Closing a thread": → "the capture commit runs `CheckSpineIntegrity` before; the deletion commit runs `CheckDerivedFresh` and `CheckSpineIntegrity` after".

**CT19 — When the turn repository is born** (S20)
- **Side A.** SPEC "(8)": "(a turn repository is born only alongside a fresh history repository)".
- **Side B.** SPEC (5): "deleted and recreated at each day rollover"; the "benign morning → turn-repository init" row.
- **Code.** `git grep -n -F 'func initGitDaily' -- internal/store/init.go` → `:461`; the doc comment (R-read 448–452): "Called ONLY on the greenfield path".
- **Moved last.** init.go 2026-11-02; leaf 2026-11-02.
- **Class 1.**
- **Resolution.** → "(`personant init` creates a turn repository only alongside a fresh history repository)".

**CT20 — Cost of turn-repository init** (S21)
- **Side A.** SPEC "(5)": "**O(active working-set bytes)**".
- **Side B.** The same paragraph: "baseline-commit the store working tree".
- **Code.** R-read `fileadapter_barrier.go:331–335`: `Add(ctx, …, Daily, ".")`, then "daily baseline". The whole tree is staged.
- **Moved last.** barrier.go 2026-11-02; the phrase first appears at `1d76780 2026-07-17`.
- **Class 1.**
- **Resolution.** → "It is O(store-working-tree bytes) — the live, non-archived threads — independent of career-history length."

**CT21 — "Never rewrites git history"** (S22)
- **Side A.** SPEC "3.8.2": "Personant never rewrites git history". SPEC "Two recovery surfaces": "the memory store's own git history, which Personant never rewrites".
- **Side B.** SPEC "3.11": the turn repository "is deleted and recreated at each rollover".
- **Code.** `git grep -n -E 'ResetHard\(' -- internal ':!*_test.go'` → the only call is `recovery.go:607` on `autogit.Daily`.
- **Class 1.**
- **Resolution.**
  - §3.8.2: "Personant never rewrites git history;" → "Personant never rewrites the history repository's commits (the turn repository is disposable, §3.11);".
  - "Two recovery surfaces": "the memory store's own git history, which Personant never rewrites" → "the history repository, whose commits Personant never rewrites".

**CT22 — Project resolution given two ways** (S23)
- **Side A.** SPEC "4.5.7": "The two directory-matching steps are the resolution of §4.5.3."
- **Side B.** The two sections disagree on the remote read and the lookup sets.
- **Class 4.** `what-it-does.md`: remote-URL identity goes; WP9 rewrites both sections.
- **Resolution (cut).** Cut that sentence.

**CT23 — Directive parameters** (S24)
- **Side A.** SPEC "2.6.1": "**v0.1 honors only `closure.ack-mode` and `recall.ack-mode`**".
- **Side B.**
  - SPEC "2.6" example: "User has accepted 12/15 of last cross-project recall offers; threshold tightened from 0.5 to 0.45."
  - The `/no-revisit` row: "tighten threshold".
  - SPEC "3.9.3": "The directive `dedup.diff-format` sets the global default; per-thread override is possible."
- **Code.** No per-thread or directive read of `dedup.diff-format`; parameter reads are ack-mode only. ROADMAP Near-term "Directive-file plumbing" is the record.
- **Class 4** for the example line and `/no-revisit` (recall offers and commands go); **class 1 (record)** for §3.9.3.
- **Resolution.**
  - Cut the "User has accepted…" line.
  - Cut the `/no-revisit` row.
  - §3.9.3 → "`dedup.diff-format` (§2.6.1) names the global default."

**CT24 — Evicted file content can be brought back** (S25)
- **Side A.** SPEC "1.4": file content evicted "…can be brought back on demand".
- **Side B.** SPEC "3.9.2": "**Open:** ROADMAP B6a — … no mechanism yet brings its content back."
- **Code.** Neither side is built (ROADMAP A6, B6a).
- **Class 1 (record).**
- **Resolution.** In §1.4, after "can be brought back on demand", add ", except a file at or above the share threshold, for which no mechanism yet exists (§3.9.2)".

**CT25 — Registry sort order vs. "Append" on restore** (S29)
- **Side A.** SPEC "2.2": "a single file sorted by id".
- **Side B.** SPEC "3.8.3" step 4: "Append a fresh thread registry record".
- **Code.**
  - `git grep -n -F 'records = append(records, rec)' -- internal/store/spine_ops.go` → `:26`, followed by `WriteSpine`.
  - `git grep -n -F 'func WriteSpine'` → `jsonl.go:114` "sorted by thread ID".
- **Class 1.**
- **Resolution.** "Append a fresh thread registry record" → "Add a fresh thread registry record (the registry stays sorted by id, §2.2)".

**CT26 — Version naming** (S30; vocab SPEC §5 item 6)
- **Side A.** SPEC "9.1" heading "(v0.1 acceptance criterion)"; "The v0.1 acceptance criterion:".
- **Side B.** SPEC "Version lines": "Memory service **v0.5.0** — realism convergence. The acceptance criterion above".
- **Code.** `git grep -n -E 'Substrate *=' -- internal/version/version.go` → `:29 "0.2.7"`. The service is already past 0.1.
- **Class 1 (record + code).**
- **Resolution.**
  - Heading → "9.1 The four-month simulation (the v0.5.0 acceptance criterion)".
  - "The v0.1 acceptance criterion:" → "The acceptance criterion (memory service v0.5.0, Version lines):".
  - SPEC §9.4: "**v0.1 acceptance metrics**" → "**Acceptance metrics**".
  - ARCHITECTURE "Testing as the measurement instrument": "the v0.1 acceptance criterion" → "the acceptance criterion"; "v0.1 acceptance is the SPEC §9.1 acceptance criterion" → "Acceptance is the SPEC §9.1 criterion".
  - ARCHITECTURE "Document map": "v0.1 acceptance criteria" → "The acceptance criterion".

**CT27 — Fresh stores and the format revision** (S31)
- **Side A.** SPEC "Version lines": "`personant init` runs the format check but never writes `version.toml`".
- **Side B.** "No revision → treated as revision 1". A fresh store would then be misread as revision 1.
- **Code.** `git grep -n -F 'writeIfMissing(paths.HomeVersion' -- internal/store/init.go` → `:132`, written at `CurrentHomeFormat`.
- **Moved last.** The code first appears at `aed6dcd 2026-07-27`; the SPEC wording at `cf0b53a 2026-11-07`.
- **Class 1.**
- **Resolution.** → "`personant init` runs the format check and writes `version.toml` at the current format revision only when the store has none; it never rewrites one, so it leaves an older store's revision for the next open to migrate."

**CT28 — The Sunday idle span and decay** (S32)
- **Side A.** SPEC "9.4.1": "Its idle span crosses the §3.5 wall-clock decay threshold."
- **Side B.** The day-off idle span is about 34.5 h; `engagement.decay-time: 7d`.
- **Code.**
  - `git grep -n -F 'decayTime = 7 * 24 * time.Hour' -- internal/turn/closure.go` → `:30`.
  - R-read `workload.go:1901–1906`: "Wall-clock decay is not modeled".
- **Class 1.**
- **Resolution.** → "Its idle span (~34.5 h) stays under the §3.5 wall-clock decay threshold (`engagement.decay-time`, 7d), so decay in the simulation is turn-driven."

**CT29 — Startup order** (S33)
- **Side A.** SPEC "4.5.8": "**init → startup recovery → (day rollover if new-day) → service-run load**".
- **Side B.** SPEC "Version lines": "format check → startup recovery → service-run load".
- **Code.** `git grep -n -F 'ORDERING INVARIANT (load-bearing): the gate runs BEFORE Reconcile' -- cmd/main.go` → `:71`.
- **Class 1.**
- **Resolution.** §4.5.8 → "**format check (§9.1) → startup recovery → (day rollover if new-day) → service-run load**".

**CT30 — Number of test levels** (S34)
- **Side A.** SPEC "9.4": "Four test levels — scenario, churn, calibration, and the acceptance simulation".
- **Side B.** SPEC "9.3": "Unit testing is level 1". ARCHITECTURE "Test levels" lists seven.
- **Code.** Settles nothing.
- **Class 1 (record:** ARCHITECTURE "Test levels").
- **Resolution.** §9.4 → "The scenario, churn, calibration and acceptance-simulation levels run against…".

**CT31 — Anchor protection vs. the cap** (S28; arch-alone 9; go-coder arch-alone 8)
- **Side A.**
  - SPEC "2.7.4": "a symbol that was ever central is protected from capacity trimming (§2.3)".
  - ARCHITECTURE "Symbol lifecycle…": "Retained, not trimmed".
  - ARCHITECTURE "Anchors and symbol history": "**retained, not trimmed** — … protected from capacity trimming".
- **Side B.** SPEC §2.3 and ARCHITECTURE "Thread directory": "…if the protected set alone exceeds [the cap], the least-counted superseded entries go first."
- **Code.** `history.go:271–277` (via `git grep -n -A30 '^func capHistorySymbols'`): "Only if the protected set alone exceeds the cap … evicting lowest-Count SUPERSEDED entries first".
- **Class 1.**
- **Resolution.**
  - Append "while the protected set fits the cap" to the SPEC §2.7.4 clause.
  - ARCHITECTURE lifecycle bullet: "Retained, not trimmed," → "Retained, and protected from trimming while the protected set fits the cap,".
  - "Anchors and symbol history": "**retained, not trimmed**" → "**retained**" and "protected from capacity trimming by its `ever_central` latch" → "protected from capacity trimming by its `ever_central` latch while the protected set fits the cap".

## B. ARCHITECTURE readers (arch-alone §2, arch-with-volumes §2; go-coder same)

**CT32 — What the projection ranks over** (arch-alone 8; go-coder arch-alone 6; architect arch-with-volumes 19; go-coder arch-with-volumes 12)
- **Side A.**
  - ARCHITECTURE "Anchors and symbol history": "projection of the thread's *active* history symbols".
  - "Key abstractions": "projected from active `history_symbols`".
  - SPEC §2.2: "from the thread's active symbol history".
  - SPEC §3.3: "projection of active `history_symbols`".
  - SPEC §2.6.1 comment: "top-N active history_symbols".
- **Side B.** ARCHITECTURE "Projection": "over all retained history symbols, active and superseded alike". SPEC §2.7.4: "retained symbol history".
- **Code.** `git grep -n -F 'Candidacy is current salience' -- internal/turn/projection.go` → `:24` "ALL retained symbols — active and superseded alike".
- **Moved last.** projection.go first appears at `33895e5 2026-06-13`; "active symbol history" at `69f8c71 2026-10-31`. The doc is newer and false; the code holds the principles.
- **Class 1.**
- **Resolution.** In each A quote, replace "active" with "retained": "*retained*", "retained `history_symbols`", "retained symbol history", "top-N retained history_symbols".

**CT33 — What "active" means** (go-coder arch-alone 7)
- **Side A.** ARCHITECTURE "Symbol lifecycle…": "`active` — currently in a top-`anchor.projection-max` slot (the zero value)."
- **Side B.** A never-central symbol outside the slice is also `active`.
- **Code.** `projection.go:107` (only ever-central symbols flip to superseded); `:119` "value \"\" is active".
- **Class 1.**
- **Resolution.** → "`active` — not superseded: in the projection now, or never yet central (the zero value)."

**CT34 — The scope rule vs. process content** (arch-alone 1, process part; go-coder arch-alone 1)
- **Side A.** ARCHITECTURE Must-see: "No project-specific process is dictated".
- **Side B.**
  - "Anti-patterns": "If you see one of these proposed … stop and surface the concern".
  - "The per-delta pipeline": "Code review treats any direct mutation as a bug."
  - "How decisions evolve": "When you propose a change, ask:".
  - "Testing…": "designed in these documents before coding; changed through CONVENTIONS review".
- **Record.** CONVENTIONS "Before a change" already holds the anti-pattern process.
- **Class 1 (record).**
- **Resolution.**
  - Cut the anti-patterns lead sentence.
  - Cut "Code review treats any direct mutation as a bug."
  - Cut "; designed in these documents before coding; changed through CONVENTIONS review".
  - Move the "When you propose a change, ask:" list, as written, to the end of CONVENTIONS "Before a change".

**CT35 — Prototype paths and the pinned release** (arch-alone 2; go-coder arch-alone 3, 4; architect arch-with-volumes 23; go-coder arch-with-volumes 25; novelty "Held for later packages": README L34)
- **Side A.** ARCHITECTURE "Pinned release": "stated in one place: the version check in the `sim-opencode` recipe (`justfile`)".
- **Side B.**
  - ARCHITECTURE "Tense": the prototype paths are "not on the build branch".
  - README "Requirements": "the pin is not yet stated anywhere".
  - The prototype paths appear in Module map rows, the "Plugin route" data flow, the "Port-and-adapter" list and the File layout `opencode/` row.
- **Code.** Matches B.
  - `git grep -n -E 'sim-opencode|probe-smoke|probe-deps' -- justfile` → none (only `integration-test` hits on the wider pattern).
  - `git ls-files internal/opencodehook opencode-plugin internal/turn/probe_phases.go internal/scenarios/opencode` → empty.
- **Moved last.** README 2026-11-07 and justfile 2026-11-07; leaves 2026-11-01.
- **Class 1.**
- **Resolution.**
  - "Pinned release" → "The supported opencode release is not yet stated: the production plugin and service are not built. The prototype states it in the version check of its `sim-opencode` recipe, on `probe-path`. The source clone at `../opencode` is kept on the installed release's tag, so the code read is the code run."
  - Module map `internal/turn/` row: cut ", and the phase entry points (`probe_phases.go`)".
  - Rows `internal/opencodehook/` and `opencode-plugin/`: prefix each cell with "Not in this tree (on `probe-path`): ".
  - `internal/scenarios/` row: "`opencode/` the opencode driver and executor" → "(the prototype's `opencode/` driver and executor is on `probe-path`)".
  - Data flow: "forwards each to `internal/opencodehook` over" → "forwards each to the memory service over".
  - Port-and-adapter list: cut "`internal/opencodehook`, ".
  - File layout `opencode/` row → "the prototype's opencode driver and executor (on `probe-path`; see "opencode-in-loop harness")".

**CT36 — Content that lives only in git** (arch-alone 4; go-coder arch-alone 15; architect arch-with-volumes 5)
- **Side A.** ARCHITECTURE "Crash recovery…": "No authoritative byte ever lives *only* in git, so either git repository can be recreated from the store working tree and losing one is never losing content." SPEC (5): "Every authoritative byte is a file under the store directory".
- **Side B.** Archival removes the thread directory; SPEC (7): "History-repository loss — unrecoverable"; ARCHITECTURE rules out recreating the history repository.
- **Code.** `git grep -n -F 'RemoveAll(' -- …/fileadapter_archive.go` → `:461`.
- **Class 1.**
- **Resolution.**
  - ARCHITECTURE → "No authoritative byte of the store working tree lives only in the turn repository, so the turn repository can be recreated from the store working tree and losing it is never losing content. Archived threads live only in the history repository (cold storage, SPEC §3.8), so a missing history repository is refuse-to-open."
  - SPEC (5) → "Every authoritative byte of a live thread is a file under the store directory; an archived thread's bytes are in the history repository only (§3.8.1)".

**CT37 — Commits per day in the history repository** (arch-alone 5; go-coder arch-alone 14)
- **Side A.** ARCHITECTURE layout `.git/`: "one day-grain commit per day".
- **Side B.** "Closing a thread": capture and deletion commits go to the history repository.
- **Code.** `fileadapter_archive.go:391`, `:491`, `:513` all commit to `autogit.Primary` (grep in CT15).
- **Class 1.**
- **Resolution.** → "history repository: permanent; one day-commit per day plus archival commits (SPEC §3.11)".

**CT38 — What is gitignored** (arch-alone 7; go-coder arch-alone 13)
- **Side A.** ARCHITECTURE drift table: "All but `.git/`, `recovery/` and the shipped files are gitignored".
- **Side B.** The same cell: both repositories are "in the managed gitignore block".
- **Code.**
  - `git grep -n -F '.git-daily/' -- internal/store/init.go` → `:708` (with `.git/` at `:707`).
  - `git grep -n -F 'recovery/'` → `:737`.
- **Class 1.** The `last-active` part is class 4 (CT14).
- **Resolution.**
  - Cell → "System-managed, not drift-checked, and gitignored: the managed gitignore block lists each, both repositories' git-dirs included, so neither repository sees the other as untracked (the ignore-block rule, SPEC §4.5.8)."
  - Layout `recovery/…`: "operational" → "operational, gitignored".

**CT39 — Where `Normalize` and `DominantSource` live** (arch-alone 12; go-coder arch-alone 19; architect arch-with-volumes 22; go-coder arch-with-volumes 28)
- **Side A.** "Port-and-adapter": "What stays direct from `internal/store` …: pure helpers (`Normalize`, `DominantSource`) and data-type aliases (`ThreadFrontmatter`, ...)".
- **Side B.** Module map: they are in `memops`.
- **Code.**
  - `git grep -n -E 'func (\([^)]*\) )?Normalize[A-Za-z]*\('` → `memops_symbols.go:131`.
  - `DominantSource` → `memops_symbols.go:260`.
  - `git grep -n '"personant/internal/store"' -- internal/workset internal/recall/scoring` → none.
  - `git grep -n -w ThreadFrontmatter -- '*.go'` → none.
- **Class 1.**
- **Resolution.** → "They import nothing from `internal/store`; the pure helpers they use (`Normalize`, `DominantSource`) are in `memops`."

**CT40 — `prj_default` and the project id pattern** (arch-alone 13)
- **Side A.** ARCHITECTURE `ProjectMeta.id`: "/^prj_\d+$/".
- **Side B.** `prj_default` has metadata. ARCHITECTURE "Project identity" step 4 chooses `prj_default` at any time, against "reserved for threads created before any project was active".
- **Code.** `git grep -n -E 'prj_|DefaultProjectID' -- internal/verify/verify.go` → `:126`, `:442` (`prj_default` is exempt from the pattern).
- **Class 1** for the pattern; **class 4** for step 4 (`what-it-does.md`: startup project prompts go).
- **Resolution.**
  - The id comment → "/^prj_\d+$/, or the reserved prj_default".
  - Cut step 4 "Final fallback: create / switch / no project (`prj_default`)."

**CT41 — Completeness-floor bound** (arch-alone 15)
- **Side A.** "Recall paths" item 4: "≤ the limit".
- **Side B.** The next paragraph: "scans `(1 + pending) × limit` excerpts".
- **Code.** `git grep -n -F 'debtWindow *= 1 + s.pendingFlushDepth'` → `measure.go:1035`.
- **Class 1.**
- **Resolution.** "≤ the limit, query-symbol match" → "(1 + pending) × the limit (below), query-symbol match".

**CT42 — When staged symbols expire** (arch-alone 16; go-coder arch-alone 18; go-coder arch-with-volumes 14)
- **Side A.** "Hook-driven turn phases", `CloseTurn` row: "expiry of staged symbols".
- **Side B.** The `BeginTurn` row and "Transient-data lifecycle": at the top of each turn.
- **Code.** `git grep -n 'pruneStaging(' -- 'internal/turn/*.go' ':!*_test.go'` → only `turn.go:881`, at turn start.
- **Class 1.**
- **Resolution.** In the `CloseTurn` cell, cut "expiry of staged symbols, ".

**CT43 — Whether loading is ever a guess** (arch-alone 18; go-coder arch-alone 24)
- **Side A.** "No speculative prefetch": "The model recognizes the thread; the choice is not a heuristic guess."
- **Side B.** "Recall paths" 2: an auto-band candidate "is fetched with one visible line and no question".
- **Record.** SPEC §3.4 auto-fetch band; the strip keeps the auto band.
- **Class 1 (record).**
- **Resolution.** → "The thread is recognized — by the model naming it in a topic tag, or by recall scoring it in the auto-fetch band (SPEC §3.4) — never prefetched on speculation."

**CT44 — Personant defines no interaction** (arch-alone 19)
- **Side A.** ARCHITECTURE "Applying the thesis": "Personant defines no interaction mode or workflow".
- **Side B.** Commands, acks and offers.
- **Class 4.** `what-it-does.md` "Flow control goes".
- **Resolution.** None for side A. Side B is cut by CT9, CT11, CT49, CT58 and the strip.

**CT45 — "Three places" of state** (arch-alone 20; go-coder arch-alone 5; go-coder arch-with-volumes 31)
- **Side A.** "One inspection location": "Personant's own state lives in three places:".
- **Side B.** The third place holds "no Personant state".
- **Class 1 (doc-internal).**
- **Resolution.** → "Personant's own state lives in two places, and it reads a third:".

**CT46 — Real models in tests** (arch-alone 22; go-coder arch-alone 21; go-coder arch-with-volumes 30)
- **Side A.** Anti-patterns: "real model for `just integration-test` only".
- **Side B.** Live run configurations against `reaper.local`.
- **Code.** justfile `:513 integration-test`, `:461–463 sim` (live toggles), `:490 sim-completeness-rung` (`-sim.live-embedding=true`), `:501 sim-tokenceiling-rung` (`-sim.live-inference=true`).
- **Class 1.**
- **Resolution.** → "Mock with scripted/generated responses as the default loop; a real model or embedder only in the opt-in live runs (`just integration-test`, the `sim` live toggles, `just sim-completeness-rung`, `just sim-tokenceiling-rung`)".

**CT47 — Whether a thread's turns are contiguous** (arch-alone 23; go-coder arch-alone 26; architect arch-with-volumes 14; go-coder arch-with-volumes 16)
- **Side A.** "Key abstractions" Thread: "contiguous run of turns".
- **Side B.** SPEC §1.1 and §3.2; threads rest and are re-engaged.
- **Code.** `engage.go:575`: the count advances only on owner turns.
- **Class 1.**
- **Resolution.** → "the turns, not necessarily consecutive, that work on one specific problem (SPEC §1.1, §3.2)".

**CT48 — How archival removes threads** (go-coder arch-alone 16; architect arch-with-volumes 25; go-coder arch-with-volumes 27)
- **Side A.** "via `git rm` + `git commit`".
- **Side B.** "`os.RemoveAll` plus `autogit.Add(".")`".
- **Code.** `fileadapter_archive.go:461` RemoveAll; `git grep -n -E '"rm"|wt\.Remove\(' -- internal/autogit` → none.
- **Class 1.**
- **Resolution.** → "by removing their directories, committing the removal and recording an `archive/index.jsonl` entry".

**CT49 — Exception queue "correct across restarts"** (arch-alone 21, first half)
- **Class 4** (the queue goes). Cut "so it is crash-safe and correct across restarts" together with the queue sentence that the strip removes.
- The turn-counter half is settled; see section E.

**CT50 — The turn as only a grouping** (go-coder arch-alone 23)
- **Status.** The ARCHITECTURE side is resolved since NV1. SPEC "3.0" still reads "a "turn" only groups deltas into human-readable spans".
- **Code.** The turn is the commit unit: `fileadapter_recovery.go:230`.
- **Class 1.**
- **Resolution.** → "a **turn** groups deltas, from the user message (or resumed request) that opens it to its close (§3.0.4)".

**CT51 — Whether pressure can close threads** (go-coder arch-alone 25)
- **Side A.** "Thread lifecycle": "not under 'context window pressure.'"
- **Side B.** "Fallback dissection" closes under budget pressure.
- **Code.** Not built: `git grep -n -i 'dissect' -- '*.go' ':!*_test.go'` → only `init.go:567` (seed default).
- **Class 1 (record).**
- **Resolution.** Append: "Fallback dissection is the one path that closes threads under budget pressure; it is not built (see "Fallback dissection")."

**CT52 — The application layer depends only on the port, except the harness** (go-coder arch-alone 28)
- **Side A.** Module map / "Port-and-adapter": the application layer depends only on `memops`.
- **Side B.** The harness reads `store.LoadArchiveIndex`.
- **Code.** `harness_run.go:804`, `:1034`.
- **Class 1.**
- **Resolution.** → "The scenario harness and its invariants read the memory store directly (`h.Paths`; `store.LoadArchiveIndex` for the archived thread set) — the test-side validator pattern, not an application-code exception."

**CT53 — Registry slot "full" vs. truncated** (go-coder arch-alone 29)
- **Code.** `workset.go:230`: `truncateToBudget(…, budget.LayerA1, …)`.
- **Class 1.**
- **Resolution.** "the current project's full thread registry as display lines" → "the current project's thread registry as display lines, truncated to its budget (SPEC §3.1)".

**CT54 — The event-log example** (go-coder arch-alone 31)
- **Code.** `git grep -n -F 'details := fmt.Sprintf("version=%s home-format=%d commit=%s home=%s"'` → `bootstrap.go:38`.
- **Class 1;** the `retire.ack` line is class 4.
- **Resolution.**
  - First line → "…system.bootstrap version=0.1.0 home-format=1 commit=abc1234 home=/Users/u/.personant".
  - Cut the `dissect.fire` line (a reserved name).
  - Cut the `retire.ack` line.

**CT55 — The order of `symbols.jsonl`** (go-coder arch-alone 32)
- **Code.** `symbols.go:39`: lists are "sorted by descending recall_fires"; the file is sorted by symbol.
- **Class 1.**
- **Resolution.** `SpineRecord` comment → "…; orders each symbol's thread lists in symbols.jsonl".

**CT56 — Cross-project recall** (architect arch-with-volumes 7; go-coder arch-with-volumes 18)
- **Side A.** ARCHITECTURE path 3 "scanned each turn"; "Cross-project recall is designed (recall path 3), not emergent".
- **Side B.** SPEC §3.1/§3.2: declined.
- **Code.** `git grep -n -F 'func crossProjectDecline' -- internal/turn/lru.go` → `:133`. Nothing implements path 3 (`git grep -i crossproject` → only the decline and the seed default).
- **Class 1.**
- **Resolution.**
  - Path 3: prefix "Designed, not built; a cross-project fetch is declined (SPEC §3.1, §3.2)."
  - "Many projects": "is designed (recall path 3), not emergent" → "is designed (recall path 3), not built; cross-project engagement is declined (SPEC §3.2)".

**CT57 — When directives are read** (architect arch-with-volumes 10; go-coder arch-with-volumes 5)
- **Side A.** ARCHITECTURE "The composed request…": "(resolved by directive precedence at the start of a service run)".
- **Side B.** SPEC §8.2.4: "at parameter-read time".
- **Class 4.** `what-it-does.md`: ack modes go, so no parameter is read.
- **Resolution (cut).** Cut the parenthetical. The A1 unstated-departure item for it then lapses.

**CT58 — The session command list** (architect arch-with-volumes 15; go-coder arch-with-volumes 26)
- **Class 4.**
- **Resolution (cut).** Cut both ARCHITECTURE "Integration shape" "**Open:** U4" paragraphs and the "**Session commands.**" bullet.

**CT59 — How a thread becomes dormant** (architect arch-with-volumes 13)
- **Side A.** "Thread lifecycle" stage 3: "as engagement decays".
- **Side B.** SPEC §3.1 LRU rule.
- **Code.** `lru.go:145` "overflow demotes".
- **Class 1.**
- **Resolution.** → "**Dormant**, in the dormant set, once more recently engaged threads push it out of the active set (SPEC §3.1 LRU update rule)."

**CT60 — Project names: user-chosen vs. regex** (architect arch-with-volumes 16)
- **Code.** Matches ARCHITECTURE: `memops_symbols.go:20` `ProjectNamePattern`, enforced at `verify.go:520`.
- **Class 4** for the SPEC side: §4.5.2's "user-chosen string … `/project rename`" row is rewritten by WP9.
- **Resolution.** No C edit beyond what the strip cuts.
- **Note for strip resumption.** WP9's "directory base name" display name will fail `ProjectNamePattern` for names like `MyApp`.

**CT61 — `/topic`, ack and remote examples carried in SPEC U5 Opens** (go-coder arch-with-volumes 1, SPEC part)
- **Side A.** SPEC §5.2 and §5.3: "**Open:** U5 — how the memory service reaches the model".
- **Side B.** SPEC §8.2.1: "never calls a model"; ROADMAP U5 is checked off.
- **Class 4.**
- **Resolution (cut).** Cut both Opens, and the §1.8 "**Open:** U5 — which model runs the review." Inventory row 5 keeps a non-U5 Open for the reviewer model; keeping that is the strip's call.

**CT62 — Anchor length vs. identifier ranking** (architect arch-with-volumes 20; go-coder arch-with-volumes 13)
- **Side A.** ARCHITECTURE verify: "`anchors[i]` … 3–50 chars".
- **Side B.** "Projection" ranks high-specificity identifiers (URLs, paths) first.
- **Code.** Both hold, so the code contradicts itself.
  - `git grep -n -E '50' -- internal/verify/verify.go` → `:156 if n := len(a); n < 3 || n > 50` (Errors).
  - Nothing filters projection by length: R-read `projection.go:47–113`, `engage.go:421–430`.
  - Extraction admits hex ids up to 64: `chain.go:181`.
- **Moved last.** The verify check first appears at `b4a89e7 2026-05-09`; ranking at `9fc7347 2026-05-22`; the 64-char hex bound at `b900840 2026-11-02`. The newer code violates the store-integrity principle (history repository HEAD passes verify).
- **Class 2.**
- **ROADMAP Near-term draft:** "- [ ] **Anchor projection can emit an anchor `personant verify` rejects.** `turn.ProjectAnchors` ranks high-specificity identifiers first with no length bound, and extraction admits URLs, file paths and hex ids up to 64 characters (`internal/turn/chain.go`), while `verify` errors on an anchor outside 3–50 characters (`internal/verify/verify.go`), so a thread whose top symbol is a long path fails the day-commit's thread registry integrity check. (ARCHITECTURE "Constraints enforced by `personant verify`", "Projection".)"

**CT63 — Ownership of opencode's database** (architect arch-with-volumes 24)
- **Side A.** ARCHITECTURE "Boundary" and SPEC §1: "Outside context management, Personant uses what opencode has chosen".
- **Side B.** "opencode database cleanup" deletes rows and runs `VACUUM`.
- **Code.** Not built.
- **Record.** SPEC §1.7; ROADMAP B3.
- **Class 1 (record).**
- **Resolution.**
  - Boundary: append "; the one exception is cleaning up the rows its checkpoint compactions leave in opencode's database (SPEC §1.7; see "opencode database cleanup")."
  - SPEC §1: after "Personant uses it as it is", add "(§1.7's database cleanup is the one exception)".

**CT64 — Session titles** (go-coder arch-with-volumes 4)
- **Side A.** ARCHITECTURE "Integration shape": "the `session.title` and `session.compaction` hooks supply `result`, which skips the model call".
- **Side B.** "Boundary" and SPEC §1: outside context management, opencode's choice stands; titles are not among the things Personant owns.
- **Code.** The prototype is not in this tree. The probe results report (lines 199–200) answered titles so that `oc_uncomposed_requests` stays 0.
- **Class 1 (record; triage — the reviewer filed class 3).** SPEC §1's "Personant owns what
  enters every model request" covers a title request: it is a model request. "Uses opencode's
  choice as it is" applies outside context management, and what reaches a model is context
  management. ARCHITECTURE's "Any other model call is kept off the provider" already states it.
- **Resolution.** ARCHITECTURE "Integration shape": append to the bullet ", so no model request
  reaches the provider uncomposed (SPEC §1)". SPEC unchanged.

**CT65 — Scope of recall path 2** (go-coder arch-with-volumes 17)
- **Side A.** "match a closed thread's anchors".
- **Side B.** SPEC §3.4 match target is `anchors ∪ {h.normalized}`.
- **Code.** `scoring.go:116–127`: every spine record of the project, with target = `buildThreadSet`. `recall.go:256`: `Exclude: engagedSet`.
- **Class 1.**
- **Resolution.** → "When current-turn symbols match, above threshold, the anchors and history symbols of a thread of the active project that this turn did not engage".

**CT66 — Excerpt file count** (go-coder arch-with-volumes 22)
- **Side A.** SPEC §2.3: "The number of files … does not grow with thread length."
- **Side B.** ARCHITECTURE: an excerpt over 1 MiB "stays loose".
- **Code.** `folio.go:126`; `jsonl.go:19` (1 MiB).
- **Class 1.**
- **Resolution.** → "The number of files in a thread's directory does not grow with its count of excerpts; an excerpt too large to bundle stays a file of its own."

**CT67 — Project directive path** (go-coder arch-with-volumes 23)
- **Side A.** ARCHITECTURE "Personalization": "`directives/prj_<n>/*.md`".
- **Code.** `directives.go:18` `"project.md"`; `:36`.
- **Class 1.**
- **Resolution.** → "`directives/prj_<n>/project.md`".

## C. Vocabulary §5 and novelty items

These are counted as class 1 but carry no CT number:
- **Slot percentages** (vocab SPEC §5 item 3). Code: `memops_types.go:366–372` (E12, A18 = A1 10 + A2 8, C15, B55). In §3.1, "(12/18/15/55, …)" → "(directives 12, registry and other-projects together 18, active-threads 55, dormant-threads 15; the 18 is split between the registry (fixed, 10) and other-projects (the residue))".
- **chars/token** (vocab SPEC §5 item 7). Code: `memops_types.go:351` bytes. "~2.5 chars/token" → "~2.5 bytes/token".
- **Display-name fallback** (vocab SPEC §5 item 4). Class 4: the §3.5 offer leaf and Banding go (WP6/WP7) and the §2.2.2 chain stays. Cut "resolved by the §2.2.2 fallback chain (description → summary → projected anchors)".
- **"Anchor selection quality at close"** (vocab ARCHITECTURE §5 item 3). In "How decisions evolve", → "anchor projection quality (SPEC §2.7.4)".
- **SPEC §3.0.4 carries two concerns** (novelty "Held for later packages"). There are no title citations (`grep -n -i 'per-turn coalescing'` → only the heading and the module map). Heading → "3.0.4 Turn boundaries and per-turn engagement coalescing".

## D. Dismissed with evidence
- **Topic-tag window vs. Close-time fallback** (architect arch-with-volumes 9). R-read `streamfilter.go:106–119`: Close reruns the same bounded `PreambleScan`, and the single-line check only suppresses display. Nothing is recognized beyond the window.
- **Turn-id collision after restart** (go-coder arch-with-volumes 7, inference). `turn.go:573`: the id carries a nanosecond stamp, so it is unique across runs.
- **`model.response` at turn close** (go-coder arch-with-volumes 19). SPEC §3.0.2 allows "on stream completion"; no order relative to tool results is required.
- **S35** is not a contradiction: memory discard is decided, and the mapping is open (ROADMAP B8).
- **S27** is not a contradiction: the keep/toss stage is deferred and the batch review is a B10 requirement.
- **S36** is settled by ROADMAP A7.

## E. Resolved since, settled, or held elsewhere (not re-raised)
- **Resolved since:**
  - S26: no redactor text remains (`grep -c redact SPEC.md` → 0).
  - The SQLite reserve: arch-alone 14, go-coder arch-alone 20, architect arch-with-volumes 26, go-coder arch-with-volumes 29 (NV16/17).
  - Secret-bearing files: architect arch-with-volumes 2 (NV19/20).
  - The embedding provider: go-coder arch-with-volumes 3 (NV18).
  - Vocab ARCHITECTURE §5 item 2 (NV18) and item 4 (the heading is now "Project identity").
- **Settled by ruling 2:** the tracked-file store (arch-alone 3, go-coder arch-alone 17, architect arch-with-volumes 4, go-coder arch-with-volumes 21, 24).
- **Settled by `spec-cold-read.md` "SPEC against THESIS":** replay (architect arch-with-volumes 18).
- **Settled by ROADMAP:**
  - U9: the restart context and the turn-counter decay (go-coder arch-with-volumes 6, 7; arch-alone 21, second half). A1 holds the departure.
  - B12: whether the model mutates state (go-coder arch-alone 27).
  - A12: config location (architect arch-with-volumes 3) and the §4.1 verbs (vocab ARCHITECTURE §5 item 7).
- **Held for A1:**
  - Career memory (architect arch-with-volumes 12, go-coder arch-with-volumes 32, the S-gap).
  - The recall-cache and working-set classification (architect arch-with-volumes 17, go-coder arch-with-volumes 20).
  - User-facing outcomes in ARCHITECTURE (arch-alone 1, go-coder arch-alone 2).
  - Concurrent sessions, designed vs. open (go-coder arch-alone 30).
- **Held for A by the novelty report:** the model client and the `reaper` target (architect arch-with-volumes 1, vocab ARCHITECTURE §5 item 9).
- **Not contradictions:** vocab SPEC §5 items 1, 2, 8, 9, 10 and ARCHITECTURE items 1, 5, 6, 8 are S, M or R material.

## Count per class (73 contradictions)
- **Class 1:** 48. CT1, 3 (parallel part), 4–8, 10, 12 (ARCHITECTURE part), 13, 15–21, 23 (§3.9.3), 24–43, 45–47, 50–56, 59, 63, 64, 65–67, plus 4 from section C.
- **Class 2:** 2 (CT2, CT62).
- **Class 3:** none after triage.
- **Class 4:** 17, pure or partial. CT3 (Banding), 6 (commands), 9, 11, 12 (§3.5), 14, 22, 23 (example and `/no-revisit`), 38 (`last-active`), 40 (step 4), 44, 49, 54 (`retire.ack`), 57, 58, 60, 61, plus the section C display-name item.
- **Uncounted:** 6 items dismissed in section D.

