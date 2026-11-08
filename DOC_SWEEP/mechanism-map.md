# Mechanism map (package M1)

Reviewer: adjagent:architect, on `DOC_SWEEP/` at `f724b98`. M2 applies each item: **cut** removes
the SPEC passage where ARCHITECTURE already states it; **move** carries the mechanism to the named
ARCHITECTURE section; in both, SPEC keeps the "Kept" sentence verbatim. **[strip]** items get no
M2 edit: the strip plan cuts them.

## Dispatcher triage

Settled against the record before M2; M2 applies these and not the reviewer's open readings.

- **Undecided 1 (pipeline step list).** SPEC keeps the §3.0.2 step list and §3.0.3 as stated:
  the steps are how SPEC states "no gaps". M14's narrow cut applies.
- **Undecided 2 (§3.10.8 lifetime tag).** Stays in SPEC: it is a model-facing emission format,
  and SPEC §5 holds those (the §5.1.1 topic tag is the precedent).
- **Undecided 3 (lifecycle tag names).** Stay in SPEC, as M67 keeps them: the memory store is a
  git repository a person inspects with `git tag` and `git show`.
- **Undecided 4 (trimming order, M4).** Stays in SPEC: which symbols survive an overflow decides
  what stays findable. M4 gets no edit.
- **Undecided 5 (§4.5.8 (9)).** Stays in SPEC as an acceptance obligation; M76's cut of the
  test-method sentence applies.
- **M16, M85 (`usage.prompt_tokens`).** The "Kept" wording ("the token count reported for the
  request") already settles it: it names no field, and ARCHITECTURE «Model access and inference
  telemetry» states where the count comes from. The ratio moves; the field name does not.
- **M30 (in-context exclusion across a restart).** Not a disagreement. The exclusion covers
  content in the composed request; after a restart the accepted excerpts are no longer in it
  (the marks and the window are rebuilt together), so offering them again is what the ruling
  requires. The "Kept" text applies as written.
- **M46.** Move the exact formula (entries staged before turn currentTurn − K + 1 expire) into
  ARCHITECTURE «Transient-data lifecycle» in place of "older than".
- **M55.** Strip material; no M2 edit.
- **M82 (embedder `dimensions`).** The code's direct embeddings call is the provider path the
  strip removes; how embeddings are requested through opencode is ROADMAP A12a. Move the
  parenthetical to ARCHITECTURE «Model access and inference telemetry» without restating the
  endpoint.

## Map

I read `DOC_SWEEP/SPEC.md` (2532 lines) whole and `DOC_SWEEP/ARCHITECTURE.md` whole, both at f724b98. Each ARCHITECTURE heading is given as «…». "→ cut" means ARCHITECTURE already states the mechanism (quoted). "→ move" means it does not, or only in part. Code evidence comes from `git grep` and from reading the cited lines. Nine passages disagree with code or with ARCHITECTURE; they are flagged inline as **Disagreement** and left unresolved.

## §2 Data model

**M1** · §2.1 "Outside the store directory": "crash recovery quarantines untracked files (§4.5.8), which would include opencode's database."
- **Kept:** "opencode's data directory is never under the store directory."
- **ARCH** «Start order and resume», already stated ("Crash recovery quarantines untracked files there, which would include opencode's database") → **cut**

**M2** · §2.2: "Names beginning `spine` (`spine.jsonl`, `spine.entry-max-chars`, `SpineRecord`, the `spine.*` events)"
- **Kept:** the same sentence without `SpineRecord`.
- **ARCH** «Thread registry record»: `interface SpineRecord {` → **cut**

**M3** · §2.3 second bullet: "older excerpts are bundled into one per-thread file (`folio.jsonl`) while recent ones stay individual files (`turns/`) — the two excerpt storage **tiers** — and an excerpt too large to bundle stays a file of its own."
- **Kept:** "The number of files in a thread's directory does not grow with its count of excerpts. Excerpts are returned to readers byte-exact, whichever way they are stored; what a reader gets never depends on how an excerpt is stored."
- **ARCH** «Thread directory», "Excerpt storage tiers": "split between loose files (`turns/`…) and its excerpt bundle (`folio.jsonl`…)"; "An excerpt whose bundle line would exceed `ReadJSONL`'s 1 MiB line cap stays loose" → **cut**
- The term "tiers" leaves with the §4.5.8 step that uses it (M66).

**M4** · §2.3 symbol-history bullet: "if the protected set alone exceeds it, the least-counted superseded entries go first."
- **Kept:** "A thread's symbol history is hard-capped at `history.cap-per-thread` (default 40). Capacity trimming never removes a once-central symbol, or a URL, file path or git SHA, while those symbols fit the cap."
- **ARCH** «Thread directory»: "If the protected set alone exceeds the cap, the lowest-`count` `superseded` entries go first." → **cut**
- See undecided item 4.

**M5** · §2.4: "from a derived inverse index of symbol to threads, rebuilt from authoritative files on every refresh of derived files, ordered most-recalled thread first; supersession state is read only from the thread frontmatter."
- **Kept:** "For any symbol, recall can find which threads hold it as an anchor and which have superseded it (§3.4). `symbols.jsonl` is derived: regenerable from the authoritative files and never hand-edited."
- **ARCH** «Symbol index»: "rebuilt from `spine.jsonl` and `threads/*/thread.md` frontmatter on every index refresh"; "`threads` and `superseded_in` are sorted by descending `recall_fires`"; "`superseded_in` is rebuilt from the frontmatter `lifecycle`, the only source of supersession state." → **cut**

**M6** · §2.5.1 "Remote URL normalization": "lowercase host, trailing `.git` stripped, and `git@host:owner/name` equal to `https://host/owner/name`." **[strip]**
- **Kept:** "Two spellings of one git remote compare as the same remote."
- **ARCH** «Project metadata»: "Remote URL normalization: lowercase host; strip trailing `.git`; …" → **cut**

**M7** · §2.5.2: "(recent anchors across the project's most recently engaged threads, a one-line summary, a byte size); the digest is derived and regenerated whenever any thread in the project is updated."
- **Kept:** "The model sees every other project through its digest: the project's recent anchors and a one-line summary, derived from its threads and current with them."
- **ARCH** «Project metadata»: "[derived], regenerated whenever any thread in the project updates"; "`recent_anchors` … top N anchors across the N most recently engaged threads"; "`byte_size`" → **cut**

**M8** · §2.6.1, the `context.token-budget` comment: "Per-slot BYTE shares are derived (ceiling × ~2.5 conservative bytes per token, counting text bytes only) and drive truncation; usage.prompt_tokens is the check."
- **Kept:** "# whole-request TOKEN ceiling; the authoritative check"
- **ARCH** «The composed request and its slots», Derivation, states the check and pre-flight truncation; the ~2.5 ratio is stated nowhere in ARCHITECTURE → **move** (the ratio, once, together with M16).
- **Disagreement:** see M16.

**M9** · §2.6.1: "`spine.entry-max-chars: 200 # hard cap for SpineRecord.summary`"
- **Kept:** "# hard cap for a thread registry record's `summary`"
- **ARCH** «Thread registry record»: "`summary` … ≤ spine.entry-max-chars" → **cut**

**M10** · §2.7.2: "entities and hashtags compare lowercased, hyphenated and punctuation-stripped (stop words dropped from multi-word entities), while identifier symbols compare exactly. A stored symbol keeps its surface form for display and its normalized form for matching."
- **Kept:** "Recall matches entities and hashtags across case, spacing and punctuation variants, and matches identifier symbols exactly; a symbol is displayed as it was written."
- **ARCH** «Symbol normalization» (the table, and "Storage is `{raw, normalized}`: matching uses `normalized`, display uses `raw`.") → **cut**

**M11** · §2.7.3: "in the symbol index a thread's anchor counts as `curator`-sourced … the most frequent source dominates, ties breaking `curator`, user, model, deterministic" and "Provenance is populated deterministically — no model, no human — is monotonic"
- **Kept:** "Each symbol records how it was emitted (deterministic extraction, model or user) and, separately, which threads it was carried in from via recall (`derived_from`). Provenance never takes part in the §3.4 recall match. A thread started by recalling and combining two or more threads carries each parent's id per symbol, not as a thread-level edge."
- **ARCH** «Symbol source» already states the tie order ("`curator > user > model > deterministic`") and the population rule ("deterministic (no model, no human) … The set is monotonic") → **cut** those.
- The rule that an anchor counts as `curator` is not in ARCHITECTURE; the code has it at `internal/index/symbols.go:83` ("Anchors → SourceCurator") → **move** that rule.

**M12** · §2.7.4: "Per turn the order is merge, then project, then trim." and "flat history length and once-central count over a run is a §9 sim obligation, not a static guarantee."
- **Kept:** the rest of the paragraph.
- **ARCH** «Projection»: "**Ordering invariant** … merge → project → trim" and "`history_len` and `ever_central_count` staying flat over a run is a SPEC §9 sim obligation." → **cut**

## §3 Core algorithms

**M13** · §3.0.1, `fs.write` row: "for `write`, taken from the tool's input"
- **Kept:** "path + the file bytes after the write or edit"
- **ARCH** «Ordering and correlation»: "`fs.write` content comes from the `write` tool's input; its output is only a confirmation line" → **cut**

**M14** · §3.0.2 steps 1 and 3, and the last bullet of §3.0.3.
- Step 1: "deterministic regex pass … decision-class extractions go to the **turn buffer** (…); task-class extractions enter the **staging buffer**"
- Step 3: "a file-edit delta is buffered for the turn; at turn close it becomes a content-id line … and updates the tracked-file store"
- §3.0.3: "Budget demotion adds no delta: it emits a log line and is done."
- **Kept** for step 1: "Symbol extraction (§3.3): identifier symbols and hashtags are extracted from the delta and the model's topic-tag symbols are read; where they land depends on the delta's retention class (§3.10)."
- **Kept** for step 3: "Dedup (§3.9): the delta's file content is recorded once for the turn; which positions render content is decided at composition (§3.9.2)."
- **ARCH** «The per-delta pipeline (§3.0)»: "deterministic regex pass + parse model emissions; routing by retention class"; "buffered; identifier lines and the tracked-file store update at turn close; replacement at composition." → **cut**
- See undecided item 1.

**M15** · §3.0.4: "Within a turn the symbol set is union-accumulated across all deltas, and engagement fires once per affected thread with the union as input"
- **Kept:** "Within a turn, engagement counts once per thread, over the symbols of all the turn's deltas, so an N-tool-call turn does not inflate `turn_count` …"
- **ARCH** «The per-delta pipeline», step 2: "coalesced per turn in the turn buffer (multi-tool-call turns don't multiply `turn_count`)" → **cut**

**M16** · §3.1 first paragraph: "(ceiling × a conservative ~2.5 bytes/token, text-only)", "bytes pre-flight (this composition plus the live-turn sub-policy), `usage.prompt_tokens` post-flight as the real-model check", "the conservative bytes/token ratio covers it."
- **Kept:** "The token ceiling is the authoritative whole-request check; each slot's byte budget is derived from it conservatively, so a request within its slot budgets is within the ceiling. Static prompt scaffolding (…) is charged to no slot."
- **ARCH** «The composed request and its slots», Derivation and Scaffolding ("rides on top of the byte proxy") → **move** the ratio; cut the rest.
- **Disagreement:** `usage.prompt_tokens` is a field of the provider's response. ARCH «Model access and inference telemetry» says "opencode makes the provider call, so the memory service sees no provider response … Token counts reach the memory service through opencode's step and usage events (`session.step.ended`)". The same field is named in §2.6.1 and §9.1 (M85).

**M17** · §3.1 slot table, Source and Render columns:
- "`directives/defaults.md`, … `directives/<active>/project.md`"
- "`spine.jsonl` filtered to `project == active`"
- "`projects/<id>/digest.json` for every other project"
- "One line per project: `<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>`, sorted by `last_active` desc"
- "heading + frontmatter line + body"
- the lead-in "(§2.1 maps these to on-disk sources)"

**Kept**, as a single Content column:
- directives: the defaults, user-wide and active-project directive texts plus the project's convention files, frontmatter removed;
- registry: a display line for each thread of the active project;
- other-projects: one line per other project (never `prj_default`) with its name, id, summary and recent anchors, most recently active first, each within `cross-project.digest-per-project-bytes`;
- active-threads: up to `layer.b-top-k` threads, most recently engaged first, each with its metadata and recent excerpts; each thread's share is the slot's share ÷ `layer.b-top-k`, and an oversized thread is truncated on its own;
- dormant-threads: a display line for each thread of the dormant set.

**ARCH** «The composed request and its slots» does not state the sources or the line format; the format exists only in a code comment (`internal/workset/workset.go:292`) → **move**. The pointer "(§2.1 maps these…)" is dead: §2.1 maps no slot to a source. Cut it.

**M18** · §3.1, Open under "Slot policies": "(MCP tools are recognizable by the `<server>_` name prefix)"
- **ARCH** «Injection handling»: "MCP tools are recognizable by the `<server>_` name prefix." → **cut**

**M19** · §3.1 "LRU update rule (driven by the §3.0.4 turn-close path)", the three-step procedure.
- **Kept:** "After a turn's engagements commit, each thread it engaged moves to the front of the active set, most recently engaged first (**active-set order**). A thread pushed past `layer.b-top-k` moves to the front of the dormant set."
- The persistence and "not count-capped" paragraph stays.
- **ARCH** «Active-set and dormant-set order» states persistence but not the update procedure → **move**

**M20** · §3.1 "§5.5 mid-turn fetch": "the memory service aborts the in-flight stream, loads the thread, fires a `thread.fetched` delta (§3.0.1), loads the thread into the active set (…), and re-issues the request with the recomposed system prompt."
- **Kept:** "Until decided, the text below binds: when the model's topic tag at response start names a `thr_<n>` not in the active set, that thread joins the front of the active set and the response is regenerated with it in context; the user sees only the regenerated response. At most one such re-prompt per turn (§5.5). A thread of another project is declined (`thread.fetch-cross-project`) and neither loaded nor re-prompted for."
- **ARCH** «Topic-tag mechanics» does not state the abort and re-issue sequence; «No speculative prefetch» states only the opencode spike → **move**, once, shared with M81.

**M21** · §3.2 "Turn-single-owner invariant": "A second excerpt-write for the same turn is a structural error and is refused."
- **Kept:** "A turn belongs to exactly one thread, its owner; only the owner receives the turn's excerpt."
- **ARCH** «Active-set and dormant-set order»: "A second excerpt write for one turn is refused with `ErrTurnAlreadyOwned`." → **cut**

**M22** · §3.2 "Anchor projection at owner-turn close": "in the order **merge → project → trim**. **Idempotent-write guard:** … (and `anchors_projected_at_turn`, the field recording …) **only when the projected anchor set differs**"
- **Kept:** "After the owner's symbol history is merged, the memory service re-derives the thread's anchors (§2.7.4); the thread's registry record changes only when its anchor set does. Engaged-but-not-owner threads do not re-project. Closing a thread runs a final projection from its symbol history."
- **ARCH** «Thread registry record» has the field ("owner turn of last projection change (SPEC §3.2)") but not the guard → **move** the guard. The merge → project → trim order is already in «Projection» → cut.

**M23** · §3.3 pass 1: "regex over the delta"; and "instead of the turn buffer"
- **Kept:** "Deterministic extraction from the delta (file paths, URLs, commit SHAs, hashtags)."
- **ARCH** «Symbol extraction», pass 1: "regex/parser scan" → **cut**

**M24** · §3.3 missing-tag fallback:
- "via the §5.5 re-issue path"
- "The forensic `thread.tag-defaulted` line carries `thr=`, `cause=missing-tag`, `reprompted=yes|no`."
- "`fs.read` deltas buffer a write entry exactly like `fs.write` (§3.9), so…"

**Kept:** "…re-issued with a terse system-side reminder appended (§5.1.3)…"; "logged `thread.tag-defaulted`"; "A turn that read a file is binding-required, like one that wrote one."

**ARCH** «Event log format», `thread` row: "`tag-defaulted` (`thr=` …, `cause=…`, `reprompted=…`)"; «Topic-tag mechanics»: "`fs.read` deltas buffer a write entry like `fs.write`" → **cut**

**M25** · §3.3 empty-response: "`thread.tag-defaulted` carries `cause=empty-response` (with `reprompted=` …)"; "`system.empty-response` forensic line (`reprompted=`, `turn=`)"
- **Kept:** the event names without their field lists.
- **ARCH** «Event log format», `system` row: "`empty-response` (… `reprompted=yes|no`, `turn=`)" → **cut**

**M26** · §3.4 signal 2: "in-memory, **hierarchical** embedding index … coarse level (one vector per thread body) … fine level (per-turn-excerpt chunk vectors)"; and the granularity paragraph: "Every thread is an *indexed body* (coarse thread vector + per-excerpt chunk vectors) … Turns scrolling out … become index material."
- **Kept:** "Embedding similarity over every thread's content is the primary recall scan and is robust to drift: it finds threads, and content within a thread, by meaning, at one embedding call per turn. Intra-thread recall searches the engaged thread's own content that has left its recent-excerpt window; the recent-excerpt window, already in the request, is never a candidate."
- **ARCH** «Recall paths», item 4, has the fine level and the coarse-level bypass but not the per-body coarse vector → **move**
- The completeness invariant's list "embedding coarse/fine level" then needs plain wording.

**M27** · §3.4 *Maintenance*: "a stale in-flight embed never overwrites a fresher vector. Two flush triggers … unembedded backlog limit (… default 16) and the dormancy transition"
- **Kept:** "Indexing is off the turn's critical path: recall reads never wait on it. A thread's whole content is indexed by the time it leaves the working set."
- **ARCH** «Recall paths» item 4 has the `atomic.Pointer` snapshot, the "embed sequence guard" and "the unembedded backlog limit (16)"; the dormancy trigger appears only in passing → **move** the two triggers; cut the rest.
- Code agrees: `internal/memops/memops_recall.go:22` `EmbeddingDebtCap = 16`.

**M28** · §3.4 *Persistence*: "keyed by a content-staleness marker (body/chunk hash): gitignored, regenerable (rebuild-on-miss), written through by the indexer, and loaded at startup … O(changed), not O(all)."
- **Kept:** "The vectors are derived and regenerable, and are kept across service runs (§2.1 `.recall-cache/`), so a service run re-embeds only content that changed."
- **ARCH** «Recall paths» has only "`.vec`/`.tree` sidecars under `.recall-cache/`" → **move** (hash keying, write-through, rebuild-on-miss).

**M29** · §3.4 *Completeness floor*: "searches the engaged thread's not-yet-flushed tail lexically against the query symbols, over a fixed bound that covers in-flight and queued flushes, and unions the hits"
- **Kept:** "Content not yet embedded is still found: intra-thread recall also matches the engaged thread's not-yet-indexed content against the query's symbols, at a bounded cost per query." The accepted residual stays, reworded to "until the next successful indexing".
- **ARCH** «Recall paths»: "a lexical pass over exactly that backlog (`exact.MatchExcerptsBySymbols` … (1 + pending) × the limit…)" → **cut**

**M30** · §3.4 in-context exclusion: "the memory service records the accepted turn-excerpts as in-window for the engaged thread, and the recall stack drops them from BOTH the fine level and the lexical completeness floor" and "Nothing is persisted."
- **Kept:** "An accepted intra-thread candidate is not offered again while it is in the request; it can be offered again once its thread leaves the active set. This is not a completeness exception — the content is in the working window. The exclusion is scoped to the composed request: content already in it is not added a second time, whatever session or service run the request belongs to." The last sentence is the planning ruling, kept verbatim.
- **ARCH** does not state it → **move** to «Recall paths».
- **Disagreement** with the "whatever … service run" ruling. The mark lives only in memory, as `state.recallWindowTurns` (`internal/turn/recall.go:274`, cleared at `internal/turn/lru.go:257`). `working-set.json` persists only `active_threads` and `dormant_threads` (`internal/store/workingset_ops.go:15-16`). So after a restart the active set comes back but the exclusion marks do not.

**M31** · §3.4 lifecycle-aware scorer:
- "`T(thr) = anchors ∪ {h.normalized}` … **weighted sum** … byte-identical to the plain-count scorer"
- "read from the thread's frontmatter `lifecycle` field …, not the derived index"
- "`symbols.jsonl.superseded_in` … O(1) per query symbol; the scorer does not consume it"
- the metric names `superseded_precision` / `abandoned_premise_recall`

**Kept:** "Symbolic recall matches a thread on its anchors and on every symbol retained in its history, so a superseded symbol stays matchable. A matched symbol counts 1 if it is active in that thread and `recall.superseded-weight` (default 1.0, no down-weight) if superseded; a symbol that is also an anchor counts as active; the union the score divides by is unchanged. The default is lowered below 1.0 only if the simulation shows abandoned threads crowding out focused ones (§9)."

**ARCH** has no scorer section → **move** to «Recall paths» item 2. The metric names are already in «Acceptance-run mechanics» → cut those.

**M32** · §3.5: "It rides the turn transaction already open around turn close (§4.5.8) and adds no commit path of its own." **[strip]**
- **Kept:** none. Routine close-out is ack-mode material, and the strip deletes it.
- **ARCH** «Closing a thread» does not state it → **move** only if the strip has not landed.

**M33** · §3.5: "The queue is **derived, never stored** … so it is crash-safe and correct across restarts." **[strip]**
- **ARCH** «Closing a thread»: "The queue is derived, never stored" → **cut**

**M34** · §3.5 "Ack-quality instrumentation": "`retire.ack` (§2.8) records `edited=yes|no` … also carries `ack=human|auto`" **[strip]**
- **ARCH** «Event log format», `retire` row: "`ack` (… `edited=yes|no`, `ack=human|auto` …)" → **cut**

**M35** · §3.7 (whole): "`projects/<id>/digest.json` regenerates whenever any thread … recent anchors are selected from the N most-recently-engaged threads."
- **Kept:** "Each project's digest stays current with its threads and within `cross-project.digest-per-project-bytes`."
- **ARCH** «Project metadata», as quoted in M7 → **cut**

**M36** · §3.8 intro: "using the autonomic git operations (§8.3) rather than a bespoke archive format"
- **Kept:** "…restored by explicit fetch; an archived thread's bytes are kept in the history repository."
- **ARCH** «Closing a thread»: "by removing their directories, committing the removal and recording an `archive/index.jsonl` entry" → **cut**

**M37** · §3.8 "Runs only at day rollover": "(the once-a-day sequence of steps in §4.5.8, … this is its archival step)" and "Standalone archival (a batch run under its own `op=archival` marker…) is crash-recoverable via the same roll-forward completion (§4.5.8 (6))."
- **Kept:** "Archival runs only at the day rollover, so the history repository is written only then, apart from crash recovery. An archival interrupted by a crash is completed at the next open."
- **ARCH** «Closing a thread»: "a standalone `ArchiveThreads` is crash-recoverable through the same roll-forward" → cut. The marker name → **move** to «Crash recovery and the turn and history repositories».

**M38** · §3.8.1 (whole): the three results, the `jsonl` record, the `parent_commit_hash` and first-parent rule, back-fill.
- **Kept:** "Archival drains a batch of the coldest closed threads at once. The batch's membership is recorded durably before any thread directory is removed, so a crash mid-batch can be completed. Each archived thread's bytes stay reachable in the history repository and verifiable against a hash recorded at archival. The batch leaves the registry in one change and adds a bounded number of commits, not one per thread. `archive/index.jsonl` (authoritative, sorted by `thr_id`) lists each archived thread with its summary and anchors as they stood at archival, its project, its archive time and, once restored, its restore time. Losing the index loses no thread bytes."
- **ARCH** «Closing a thread» states how a batch meets the three results ("the capture commit is `Add(".")` … `RemoveSpineRecords` performs the single thread registry rewrite") but not the record schema, the back-fill or the parent-commit lookup → **move**: the record to a new archive-index entry under «Storage and record formats», the procedure to «Closing a thread».
- Code agrees: fields at `internal/memops/memops_archive.go:32-84`; the first-parent fallback is `RecoverThread` calling `autogit.ParentCommitHash`.

**M39** · §3.8.2 (whole): "content-addressed, delta-compressed object store …", "`git gc` is safe to run autonomically …", "`git gc --aggressive` may run on a schedule".
- **Kept:** "Personant never rewrites the history repository's commits, so archived threads stay restorable however the repositories are packed."
- **ARCH** «Crash recovery…» has `Consolidate` (go-git `RepackObjects` + `Prune`); `--aggressive` is not stated → **move**

**M40** · §3.8.3 steps 1–5.
- **Kept:** "`personant archive recover <thr_id>` restores an archived thread under its original id (a restore never mints a new id), in state `wip`, taking its anchors and summary from its own restored files rather than the archive snapshot. The restored bytes must match the hash recorded at archival; on a mismatch nothing is restored and the command fails with an integrity error, so a partially restored thread is never written onto the thread registry. A thread with no archive entry is refused. The entry is kept, marked with its restore time, and the restore is committed to the memory store's git history."
- **ARCH** «Closing a thread»: "Restore (`RecoverThread`, `autogit.CheckoutTree`) refuses with `ErrArchiveEntryNotFound` or `ErrArchiveIntegrity` and never writes a partial thread." → **move** the steps.
- Code: the restore commits to the turn repository (`fileadapter_archive.go`, `RecoverThread`: `autogit.Commit(… autogit.Daily, "archive: recover "…)`), which agrees with the history-writer rule. Code also refuses an entry with an empty `commit_hash`; that is mechanism and goes with the move.

**M41** · §3.8.4: "the §3.4 scan covers the thread registry + per-thread frontmatter, and an archived thread has neither"; "(the index already carries `anchors` + `spine_summary`)"; and the metric names `recall_archived_recoverable` and `recall_unexplained_absence`.
- **Kept:** "Archived threads are never candidates for live recall in v0.1; the archive index's anchors and summary serve `personant archive list` and forensics. Measurement: an expected match that is archived and restorable is excluded from the live-recall score rather than counted a miss; an expected thread in neither the registry nor the archive index is an integrity failure and must never occur."
- **ARCH** «Acceptance-run mechanics» has `recall_unexplained_absence` → cut. `recall_archived_recoverable` is absent → **move**

**M42** · §3.9.1: "opencode's `read` returns formatted output …; the tracked-file store holds the file's bytes, not that output. The content of a write comes from the `write` tool's input, because the tool's output is only a confirmation line."
- **Kept:** "What is stored is the file's bytes, never a tool's formatted rendering of them."
- **ARCH** «Ordering and correlation», File content, states both → **cut**

**M43** · §3.9.2: "Composition reads only the content it renders, plus one record per file kept for the thread."
- **Kept:** none. §3.9's intro already says composition cost scales with what the context holds.
- **ARCH** «The composed request and its slots»: "Only content that renders is read." → **cut**

**M44** · §3.10.2: "a map keyed by normalized symbol form"; "Each entry holds the normalized form (also the key), the raw surface form …, the symbol's source …, and the turn at which it was staged. A later task-class delta carrying the same normalized form overwrites the entry and refreshes its staged turn"; and "the turn buffer".
- **Kept:** "Task-class delta: its symbols are staged and do not enter `history_symbols` or `symbols.jsonl` yet. Staged symbols persist across turns. A symbol carried again by a later task-class delta is staged afresh, so its window restarts."
- **ARCH** «Transient-data lifecycle» has the key and the entry fields → cut. The overwrite-and-refresh rule is absent → **move**

**M45** · §3.10.4: "moved from the staging buffer into the turn buffer"; "`staging.promoted` with `normalized`, `staged_at`, `cited_at`, and `turn_span` fields."
- **Kept:** "is promoted out of staging"; "logged as `staging.promoted`."
- **ARCH** «Event log format» lists `promoted` without fields → **move** the fields there. The turn-buffer wording is in «Transient-data lifecycle» → cut.

**M46** · §3.10.5: "drops staging entries staged before turn (currentTurn − K + 1)"
- **Kept:** "A staged symbol not cited within its window is dropped before the next turn's first delta. A nonzero expiry count is logged as `staging.evicted`. Expiry is deterministic; no model is involved."
- **ARCH** «Transient-data lifecycle»: "staged entries older than `stagingWindowTurns` (default 3) expire (`pruneStaging`)" → **cut**
- Code agrees: `internal/turn/staging.go:115` `cutoff := state.TurnNumber - stagingWindowTurns + 1`. ARCHITECTURE's "older than" is looser than this formula, so M2 should carry the formula across.

**M47** · §3.10.6: "The §3.0.2 step-5 logging step summarizes every source whose provisional class is `task`, so a newly-added task-class source inherits the minimization automatically."
- **Kept:** none. The preceding sentence ("a short metadata summary … (source, path, byte count)") states the outcome.
- **ARCH** «The per-delta pipeline»: "Step 5 summarizes every task-class source … so a newly added task-class source inherits the minimization." → **cut**

**M48** · §3.11 Per-turn: "(`Personant-Turn: <id>` trailer)", "Staging is scoped to the turn's recorded write set (O(write-set), no tree walk); turn-repository commits carry **no integrity validation** … Structural changes … ride the per-turn commit"
- **Kept:** "Every turn commits its writes to the turn repository at turn close (the ≤1-turn durability boundary of §4.5.8), at a cost that does not grow with the store."
- **ARCH** «Crash recovery…» has "`CommitTurn` … `autogit.AddPaths` with no integrity flags"; the trailer is absent → **move** the trailer, cut the rest.

**M49** · §3.11 Per-day: "(`Personant-Day: <N>`; …)", "(day-commit step)", "(freshness-assert step)", "(turn-repository removal and re-init steps)".
- **Kept:** "The day rollover writes exactly one day-grain commit to the history repository (an empty day still yields one) plus the day's archive commits (§3.8). The history repository is bounded to ~365 commits a year, and integrity checks run once a day: the day's commit passes thread registry integrity validation and leaves derived files fresh. The turn repository is deleted and recreated at each rollover."
- **ARCH** has neither the trailer nor the step names → **move**

**M50** · §3.11 Checkpoint: "with a full staging pass over the store working tree that absorbs hand-edits and any content not yet scoped-committed (normally a no-op)"
- **Kept:** "Every checkpoint commits the whole store working tree, hand-edits included, to the turn repository."
- **ARCH** «Crash recovery…»: "Session checkpoint commits target the turn repository with a full `Add(".")` staging pass." → **cut**

**M51** · §3.11 "gc cadence" (whole).
- **Kept:** none. There is no consumer-facing requirement; if one is wanted: "The store's git repositories are packed automatically as they grow."
- **ARCH** «Crash recovery…»: "after every `gcCheckEveryCommits` (64) … runs gc at `gcLooseObjectThreshold` (5000); `MemoryOps.Consolidate` … packs both repositories"; "the mid-day arming trigger is not wired" → **cut**
- Code agrees: `internal/memops/fileadapter/fileadapter_recovery.go:260-286`.

**M52** · §3.11 Durability: "the journal (…) protects the in-flight turn's raw bytes before any authoritative write, and the scoped turn-repository commit lands the turn at close."
- **Kept:** "Content is durable to ≤1 turn at all times; the in-flight turn's prompt and response are recoverable from the moment the prompt is submitted. §4.5.8 governs crash recovery."
- **ARCH** «Crash recovery…»: "raw `(prompt, response)` bytes are protected by a durable append journal written *before* any authoritative write" → **cut**

## §4 User surface

**M53** · §4.3.3 retraction rule:
- "no open §4.5.8 turn scope (the span in which the turn's journal is non-empty …), so the next start opens quiet"
- "(§3.0 per-delta pipeline step 1)"
- "restores the memory service's run-volatile state — turn counter, … embedding debt — … and releases the turn's journal scope … must not perturb §4.5.8 write ordering"
- "A tool round writes nothing authoritative, so…"

**Kept:** "Before the first authoritative write, an interrupt leaves memory exactly as if the turn never happened: turn count, staged symbols, active and dormant sets, symbol history and recall marks included, and the next start finds nothing to recover. An interrupt during a tool round counts as before the first authoritative write."

**ARCH** «Invariant for the production phase split» ("restores run-volatile state (`internal/turn/rollback.go`)") and «Hook-driven turn phases», `AbortTurn` → **move** the journal-scope detail.

**M54** · §4.5.2 table: "storage layout key, thread registry reference key"; "(`remote_urls[0]`)" **[strip]**
- **ARCH** «Project identity» table → **cut**

**M55** · §4.5.3 steps 1–4 (git config query, lookup order) **[strip]**
- **Kept:** none (strip, `what-it-does.md:24-26`).
- **Disagreement** with code. Step 1 says "`origin` by default; otherwise none", but `internal/store/gitworkspace.go:72` falls back to "first remote listed by `git remote`". Step 2 says "look up … by `remote_urls`", but `internal/store/project_ops.go:133-153` (`FindProjectByRemote`) also matches `historical_remote_urls`. In both cases the code matches §4.5.7 and ARCH «Project identity».

**M56** · §4.5.4: "polls the active project's git config cheaply (e.g. once per turn or at engagement)" **[strip]**

**M57** · §4.5.5: "(ENOENT on `current_root_path`)" **[strip]**

**M58** · §4.5.7 step 3: "read `<store-dir>/last-active` (operational; one line: `prj_<n>` …)" and "`last-active` file maintenance" **[strip]**
- **ARCH** «Store directory layout»: "`last-active` # operational; prj_<n> of the most recently active project" → **cut**

**M59** · §4.5.8 intro: "The raw `(prompt, response)` bytes are protected by a durable append journal written *before* any authoritative write."
- **Kept:** "…journaled content bytes" becomes "the in-flight turn's prompt and response are recoverable from the moment the prompt is submitted."
- **ARCH** as in M52 → **cut**

**M60** · §4.5.8 heading "No signal/interrupt handlers as a flush mechanism" and "The store working tree is the source of truth; git is a recovery-point index over it"
- **Kept:** heading "Recovery needs no clean shutdown"; body "Recovery needs no shutdown event, no model and no user acknowledgement; the on-disk state alone determines its outcome."
- **ARCH** «Crash recovery…» opening paragraph, nearly verbatim → **cut**

**M61** · §4.5.8 (1) Detection observables (whole): the journal signal, `op-in-progress.json` and its op values, the watermark, trailers, turn-repository state.
- **Kept:** none. Its one requirement moves to (7) as "a corrupt turn repository never prevents an open."
- **ARCH** «Crash recovery…» names the observables in one clause and `store.JournalOwner` → **move** the detail.
- Code agrees: `internal/store/marker.go:26-31`.

**M62** · §4.5.8 (2) Turn transaction protocol (whole).
- **Kept:** "A hand-edit to an authoritative file is committed at the next clean-stop checkpoint or day rollover, not with the next turn, and is never reverted. Recovery never re-drives the model."
- **ARCH** «Crash recovery…» has "`JournalTurn`, kinds `prompt` and `response`) followed by `CommitTurn`" but not the step ordering → **move**

**M63** · §4.5.8 (3) The journal (whole).
- **Kept:** "On a torn turn, the turn's prompt and response bytes are preserved and surfaced to the user (**Open:** U4, the channel); they are never replayed into memory. The user re-issues if they choose."
- **ARCH** has the file and the `store.TruncateJournal` proof, not the record → **move**
- Code agrees: `internal/store/journal.go:36-39` (`turn`, `kind`, `at`, `bytes`).

**M64** · §4.5.8 (4): the recovery case table, the dual-repository op rows and the event-log tail-heal. The case references "case 4/6 of (4)" in (10) follow it.
- **Kept:** "Startup recovery classifies the on-disk state and repairs it idempotently: a hand-edit is never reset; a torn turn is reset to the last committed turn after its dirty bytes are quarantined; an interrupted day rollover or archival is completed forward; a missing turn repository is normal, never a sign of data loss; a crash during recovery converges on re-run; an archive entry that cannot be repaired stays refused, never guessed (`recovery.unrepairable`); a torn final line of the event log is completed, never truncated."
- "Startup recovery reports once…" stays. (10) becomes "a turn … that memory rolled back in crash recovery".
- **ARCH** «Crash recovery…» has only "a small state machine … classifies into one case and repairs idempotently" → **move**

**M65** · §4.5.8 (5) "Invariants": the named rules (no-reset, day-commit guard, capture-before-removal, watermark, history-writer, ignore-block).
- **Kept:** "Archival and the day rollover never reset the store working tree; only torn-turn recovery does, and only to the last committed turn. A day is committed to the history repository at most once, however often a crashed rollover re-runs. The turn repository is removed only after the day's content is in the history repository. History repository HEAD always passes thread registry integrity validation: every write to it is validated first. Neither repository sees the other's git directory as untracked content."
- **ARCH** has the outcomes only in part → **move**
- ARCH «Store directory layout» cites "(the ignore-block rule, SPEC §4.5.8)", so that citation must change with the move.

**M66** · §4.5.8 "Day rollover": "fired on new-day detection — the day index exceeds the head day number … runs under an `op=barrier {day: N}` marker", the nine-step list, and the "Only the archival and day-commit steps mutate…" paragraph.
- **Kept:** the UTC day definition, verbatim (judgment note), then: "The rollover runs at the first service-run open or turn of a new day, after startup recovery. It archives under size pressure (§3.8), settles excerpt storage (§2.3), drops tracked-file content composition no longer needs (§3.9.1), commits the day (overnight hand-edits included) to the history repository, rebuilds derived files and recreates the turn repository. It loses no authoritative content except what archival moves to the history repository and what file retention deletes on purpose; excerpt moves are lossless and every excerpt stays readable throughout."
- **ARCH** «Crash recovery…» names `settleExcerptTiers` and `pruneTrackedFiles` but has no step list → **move**

**M67** · §4.5.8 "Lifecycle git tags": the event-to-tag mapping, targets, stamp encoding, idempotence, `barrier.tag-error`, day-scoping, gap-days.
- **Kept:** "After the day's commit, the history repository gains a tag for each thread created, closed or archived and each project created, named `<kind>/<id>/<event>_<stamp>` (e.g. `thread/thr_42/created_2026-05-08T03-12-00Z`), so names sort chronologically; `git show <tag>:spine.jsonl` reads the store as it stood then. A tagging fault never fails the day's commit."
- **ARCH** «Crash recovery…» has `autogit.TagStamp`/`ParseTagStamp` and `ErrTagExists` → **move** the rest.
- Code agrees: `internal/autogit/tag.go:15-30`. See undecided item 3.

**M68** · §4.5.8 "Roll-forward completion" (the ensure-removed … back-fill sequence and the clean-vs-HEAD warning).
- **Kept:** "A crash inside the day rollover is completed forward at the next open, never by resetting the store working tree. The completion does everything an uninterrupted rollover would, archival included, and never duplicates an archival commit."
- **ARCH** has it only in part → **move**

**M69** · §4.5.8 "Day-commit-shape guard, at-most-once per day" (whole).
- **Kept:** "Each completed day is committed to the history repository at most once, whatever the crash and re-run timing."
- **ARCH** absent → **move**

**M70** · §4.5.8 "Quarantine-and-proceed", steps (1)–(5).
- **Kept:** "If the day's integrity checks still fail after one re-run, the offending paths are preserved byte-exact under `recovery/quarantine/<stamp>/` and restored from the history repository, never by resetting the whole store working tree. If the checks still fail, the memory service refuses to open, and its report names the quarantine directory."
- **ARCH** «Crash recovery…» has `spineQuarantineRepair` per case, not the procedure → **move**

**M71** · §4.5.8 "Turn-repository init (`git init .git-daily` + baseline-commit … + set watermark …)"
- **Kept:** "Recreating the turn repository costs in proportion to the live store, not to the length of career history."
- **ARCH** absent → **move**

**M72** · §4.5.8 "Mid-day re-baseline knob" (whole).
- **Kept:** none. The knob is default-off, has no directive key and nothing reaches the user.
- **ARCH** «Crash recovery…»: "plumbs the `op=rebaseline` operation marker, but the mid-day arming trigger is not wired" → **cut**

**M73** · §4.5.8 (6) Archival (whole).
- **Kept:** none beyond M37.
- **ARCH** has `recovery.LocateDeletionCommit` (tree test) → cut that; "membership-before-deletion" and "archival never races a turn" → **move**

**M74** · §4.5.8 (7): "(a) the truncate-converse — … case-5 …" and "Intra-day derived staleness … The turn path never sets the watermark; … via the watermark; … (case 3)"
- **Kept:** "(a) a journal emptied at turn commit can reappear after power loss; recovery recognizes it as already committed, with no loss." And: "Derived files may lag between day rollovers; the rollover leaves them fresh. A hand-edit reaches them after the next full commit of the store working tree."
- **ARCH** absent → **move**

**M75** · §4.5.8 (8) Greenfield / legacy (whole).
- **Kept:** "A store with no recovery history (new, or written before the turn repository existed) is adopted into the history repository on first open, after structural validation."
- **ARCH** has `store.Init` and the legacy-upgrade shape; case 12, the trailer carve-out and `op=turn` → **move**

**M76** · §4.5.8 (9): "The matrix is unit-grade (synthetic memory stores) and is backed by a 2-day simulation span that crosses a day rollover."
- **Kept:** the rest of (9).
- **ARCH** «Acceptance-run mechanics», Crash injection: "unit-grade (synthetic store directories…); `TestSimBarrier2Day` adds a 2-day run configuration" → **cut**
- See undecided item 5.

## §5 Model interaction

**M77** · §5.1.2: the regex block, and "Group 1: … validate against `thr_\d+|\*new-topic\*`. Group 2: … run §2.7.2 normalization."
- **Kept:** "A topic tag is recognized only as a whole line of the §5.1.1 form: a comma-separated thread list of `thr_<n>` ids and `*new-topic*`, then a bracketed comma-separated anchor list, with whitespace around entries ignored; anchor entries are normalized (§2.7.2)." The scan-window, first-match and empty-list sentences stay.
- **ARCH** «Topic-tag mechanics» has no regex → **move**
- Code agrees: `internal/prompt/parser.go:29` (same pattern plus `(?m)`); window at `:60` `PreambleScanLineCap = 64` and `:66` `PreambleScanByteCap = 8 << 10`.

**M78** · §5.1.2 bare alias: "(regex `^\s*\*new-topic\*\s*\[([^\]]*)\]\s*$`)" and "(forensically classified `bare-new-topic`)"
- **Kept:** the alias rule without the regex or the classification.
- **ARCH** «Event log format» has `bare-new-topic` → cut. The regex → **move**
- Code: `parser.go:46`.

**M79** · §5.1.2 unclosed inner literal: "(it would otherwise classify `bad-thread-list`)"
- **ARCH** «Event log format», `tag-invalid` reasons → **cut**

**M80** · §5.1.3: "v0.1 changes the template only by rebuild."
- **Kept:** "The topic-tag instruction and its reminders are not user-configurable in v0.1."
- **ARCH** «Topic-tag mechanics»: "a compiled constant in `internal/prompt/template.go` … changing any of them is a rebuild" → **cut**

**M81** · §5.5 "Mechanism", steps 1–4, including "Read `threads/thr_<n>/` (frontmatter from `thread.md`, body … from `folio.jsonl`…)".
- **Kept:** §5.5's first paragraph, plus "The fetched thread enters as a `thread.fetched` delta (§3.0), within its active-threads slot budget (§3.1)."
- **ARCH**: the file detail is in «Thread directory» (tier-transparent readers) → cut. The abort and re-issue sequence → **move** with M20, to one destination (it duplicates §3.1 inside SPEC).

## §8 Bootstrap and lifecycle

**M82** · §8.2.2: "(passed as the `dimensions` parameter on the embeddings call)"
- **Kept:** "`[embedding] vectorLength`: optional; for embedding models whose vectors can be cut short, requests vectors of this length."
- **ARCH** absent → **move** to «Model access and inference telemetry».
- **Disagreement** with §8.2.1 ("The memory service never calls a model … embeddings … from opencode", Open A12a). The code calls an embeddings endpoint directly: `internal/model/embed.go:28` `NewHTTPEmbedder`, and `:61` `Dimensions int json:"dimensions,omitempty"`.

**M83** · §8.2.4: "The memory service walks the precedence chain at parameter-read time and returns the first match."
- **Kept:** "Detailed in §2.6. Briefly: `defaults.md` < `user.md` < `prj_<n>/project.md` (highest wins)."
- It duplicates §2.6 → **cut**

**M84** · §8.3: "with structured messages, in-process"; "inside its own logic"; "Operation-specific verifications (…) live inside the operation that needs them."
- **Kept:** "The memory service owns the store directory's git repositories: it initializes them on first run and commits authoritative changes itself, with no user ack and no model. Every day-commit and archival write to `<store-dir>/.git/` leaves the memory store structurally sound (what `personant verify` checks) and its derived files fresh (what `personant index check` checks). Turn-repository commits are checked by neither (§3.11)."
- **ARCH** «Automatic vs model-controlled»: "`CheckDerivedFresh` (wraps `personant index check`) and `CheckSpineIntegrity` (wraps `personant verify`) … Operation-tied verifications live in purpose-specific wrappers" → **cut**

## §9 Measurement and validation regime

**M85** · §9.1: "checked post-flight against `usage.prompt_tokens`"
- **Kept:** "…checked against the token count reported for the request."
- **ARCH** «The composed request and its slots», Derivation → **cut**
- **Disagreement:** same as M16.

**M86** · §9.1 Version lines: "when this binary registers a chain of migration steps from that format revision"
- **Kept:** "when this binary can migrate from that format revision to the current one"
- **ARCH** «Crash recovery…»: "walking the table `homeFormatMigrations`" → **cut**

**M87** · §9.2: "(`model.MockServer`)"; "Body text carries a counter suffix or seed-derived UUID so no two responses dedup accidentally."
- **Kept:** "Generated: on demand from a seeded RNG; no two generated responses are identical."
- **ARCH** «opencode-in-loop harness» has `model.MockServer` → cut. The suffix technique → **move** to «Testing as the measurement instrument».

**M88** · §9.4 metric keys: "`recall_fidelity_{precision,recall,f1}` and `recall_fidelity_adversarial_{…}`"; "(`layerb_shadow_divergence`, `layerb_shadow_reverse_divergence`; `layerb` names the active-threads slot)"; "(`recall_intra_descent_divergence`)"
- **Kept:** the same sentences without the key names.
- **ARCH** «State-ownership map» has the layerb keys → cut. The others → **move** to «Acceptance-run mechanics».

**M89** · §9.4: "Cheap checks run every step; store-wide checks run once per sim day and always once at end of run."
- **Kept:** the validator coverage list.
- **ARCH** «File layout» has `cheapDefaultInvariants` (per-step) and `heavyDefaultInvariants` (day-cadenced); "end of run" is absent → **move**

**M90** · §9.4: "(80% engage / 10% create / 5% close / 5% switch); a failure dumps the operation log and seed for replay."
- **Kept:** "Churn sequences are randomized but seeded; a failure is replayable from its seed."
- **ARCH** absent → **move**

**M91** · §9.4: "tests pin the scraped event names so a rename fails a test rather than silently zeroing a series."
- **Kept:** "…a renamed event fails the measurement rather than silently zeroing a series."
- **ARCH** «Generator ↔ harness protocol» item 3 → **cut**

**M92** · §9.4.1:
- "The harness clock is set from each step's emitted time, never integrated separately"
- "`int((clock − anchor) / 24h)`"
- "A run of at least 9 days asserts … contiguous `sim_date`, … `int(Duration/24h)`"
- "draws from the single seeded RNG in fixed order"

**Kept:** the workload shape: the anchor, the day grid, the work blocks, the day-off, phase-keeping, "(Seed, Duration) → byte-exact output", and the frozen in-turn clock.

**ARCH** «State-ownership map» (`pinnedClock = Step.At`, `SimDayIndex`) and «Acceptance-run mechanics» (`TestCadence_DayStartsNoPrecession` (at least 9 days)) → cut. The RNG ordering → **move**

"Revisit if sub-turn logic orders events by timestamp…" is future intent. That is package A's material, not mechanism.

## Counts

- **Move: 45** (M8, M11, M16, M17, M19, M20, M22, M26–M28, M30, M31, M37–M41, M44, M45, M48, M49, M53, M61–M71, M73–M75, M77, M78, M81, M82, M87–M90, M92)
- **Cut: 38** (all other unstripped items)
- **Strip: 9** (M6, M32–M34, M54–M58)

Total 92. M11, M16, M37, M41, M45 and others are partial: they cut what ARCHITECTURE already states and move the rest. They are counted under move.

## Could not decide: outcome or mechanism

1. **§3.0.2 step list and §3.0.3 "Pipeline order matters" as a whole.**
   - Outcome reading: the fixed order (extract → engage → dedup → budget → log) is SPEC's guarantee that no content escapes a step, which the old heading called "the architectural primitive".
   - Mechanism reading: only "no delta escapes any step" is consumer-facing, and the order and reasons are ARCHITECTURE «The per-delta pipeline», which already restates them.
   - M14 takes the narrow cut either way.
2. **§3.10.8 lifetime tag** (`lifetime:transient | lifetime:durable`, at "a fixed stripped position", omission rate measured in the sim).
   - Outcome reading: it is a model-facing emission contract, like the §5.1.1 topic tag.
   - Mechanism reading: it is a deferred, "settled mechanism" of consolidation, and the outcome is only "raw tool output is never retained; an unmarked re-presentation is preserved".
3. **Lifecycle tag names** (M67), `<kind>/<id>/<event>_<stamp>`.
   - Outcome reading: user-visible schema, since a person inspecting the store runs `git tag` / `git show <tag>:…`.
   - Mechanism reading: git refs are not files a person hand-edits, so they are not in the documented schema.
   - M67 currently keeps the names in SPEC.
4. **Trimming order when the protected set overflows** (M4).
   - Outcome reading: it decides which symbols stay findable, so the recall guarantee depends on it.
   - Mechanism reading: it is a tiebreak inside an edge case.
5. **§4.5.8 (9) crash-injection obligations** (M76).
   - Outcome reading: an acceptance obligation on a par with §9.
   - Mechanism reading: a test method. ARCHITECTURE «Acceptance-run mechanics» already states all of it, so the whole of (9) could be cut.

