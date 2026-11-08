# ARCHITECTURE content (package A1)

Reviewer: adjagent:architect, on `DOC_SWEEP/` at `22e3ead`. A2 applies every item's disposition
text verbatim to the monoliths; the dispatcher adds the drafted ROADMAP lines.

## Dispatcher triage

- **A1-26.** Accepted: `model.inference-telemetry` stays (`internal/turn/turn.go` emits it, and
  the strip keeps the in-process runtime). It supersedes `architecture-cold-read.md`'s "Is not
  crossings" row that said cut.
- **A1-32 (added; the reviewer's "Outside A1's brief").** Process instructions in ARCHITECTURE,
  which its Must-see bars, go as CT34's did:
  - "Applying the thesis": the "When making a design decision, ask:" list moves to the end of
    CONVENTIONS "Before a change", merged with the list CT34 put there (no question twice).
  - "Testing as the measurement instrument": "Bake metrics emission into new code from day one…"
    moves to CONVENTIONS "Before a change" as one bullet; "the question is always 'what does the
    simulation say?'" is cut.
- **ROADMAP lines** (A1-2, 3, 6, 9, 12, 18, 19, 20) are added by the dispatcher from the text
  below; A2 does not edit ROADMAP.
- No owner calls.

## Review

Nothing in this review needs an owner call: the written record or the code settles every item. Three findings need a ROADMAP Near-term line because the code breaks a stated rule:
- the closing summary is written without a length check;
- `meta.json` `thread_count` is never written;
- nothing outside the simulation builds summary trees.

One triage instruction does not hold against the code: the cut of `model.inference-telemetry` (A1-26).

**One departure from a literal reading of the brief.** The reader lists in "user-facing outcomes" include purpose clauses that tie a mechanism to a SPEC outcome and cite it, for example "(SPEC §1.5)". Linking mechanism to SPEC is what ARCHITECTURE is for ("How this project's implementation meets requirements designated by SPEC"). I mark those **keep**, not cut. Only clauses that state an outcome on their own move or go.

Headings below are current headings in `DOC_SWEEP/ARCHITECTURE.md`.

---

## Unstated departures

**A1-1 — departure. "Thread directory" → "Excerpt storage tiers" → *Bundle format*.**
- Quote: "(atomic write, default HTML escaping as in every JSONL file in the store directory)".
- Disposition: state it in that bullet. Replace the parenthetical with: "(atomic write). Like every JSONL file in the store directory, bundle lines keep `encoding/json`'s default escaping: `<`, `>` and `&` are written as `\u003c`, `\u003e` and `\u0026`, and a newline as `\n`, so an excerpt cannot be read in place; any JSON reader (`jq -r .content`) returns its exact bytes. That legibility cost against SPEC §2.1's plain-text inspection is accepted so that one JSONL writer serves the whole store."
- Settled by:
  - The retired folio plan: `git show d5e3899:ACTIVE_PLAN/design/folio-format.md` says "a legibility cost, accepted so that a single JSONL writer stays in use".
  - Code: `internal/store/jsonl.go:90` uses `json.NewEncoder` with no `SetEscapeHTML(false)`.

**A1-2 — departure / future intent. "Store directory layout".**
- Quote: "`logs/archive/YYYY-MM.tar.gz       # rotated logs older than 90 days, gzipped`".
- Nothing rotates logs. `init` creates the directory (`internal/store/init.go:97`), and recovery skips it (`internal/recovery/logs.go:130`). `git grep -n -i 'gzip\|tar.gz' -- '*.go'` finds only those comments and `paths.go:22`.
- Disposition: change the layout line to `logs/archive/                     # created empty by init; nothing writes it (log rotation is not built)`, and add a ROADMAP line.
- ROADMAP, **Mid-term**: "- [ ] **Event-log rotation.** `personant init` creates `logs/archive/` (`internal/store/init.go`) and startup recovery leaves it alone (`internal/recovery/logs.go`), but nothing rotates logs into it. The layout ARCHITECTURE carried was monthly gzipped tarballs of logs older than 90 days (`logs/archive/YYYY-MM.tar.gz`), which would be the store's only non-text file and needs a storage class."

**A1-3 — departure. "Thread registry record (`spine.jsonl`)" and "Project metadata".**
- Quotes: "`anchors: string[]; // [derived]`", "`anchors_projected_at_turn: number; // [derived]`", "`thread_count: number; // [derived] threads with project == id`".
- Disposition, part 1: after "`anchors_projected_at_turn` and `description` decode as 0 and empty on old records.", add: "`anchors` and `anchors_projected_at_turn` are derived (the anchor projection computes them from `history_symbols`) but stored in the authoritative record. The display line and the registry slot are generated from the record alone (SPEC §2.2, §2.2.2); without the stored copy, composing a request would read every `thread.md`. They are written at owner-turn close and at closing (`internal/turn/engage.go`, `internal/turn/closure.go`). `personant index rebuild` does not regenerate them, and no drift check covers them."
- Part 2: in "Symbol lifecycle", line 766 reads "`verify`/index rebuild regenerate the derived projection from the authoritative files on first run". Change it to: "`personant index rebuild` regenerates the symbol index from them; the anchor projection is recomputed at the thread's next owner turn."
- Part 3: change the `ProjectMeta` comment to `// not maintained: nothing writes it, so it reads 0; a project's thread count is digest.json's` and add a ROADMAP line.
- Settled by:
  - `git grep -n -i 'ProjectAnchors' -- 'internal/*.go'` finds callers only in `internal/turn`, none in `internal/index`.
  - `git grep -n 'ThreadCount' -- '*.go' | grep -v _test` shows `ProjectMeta.ThreadCount` is decoded (`memops_model.go:268`) and checked ≥0 (`verify.go:543`), and assigned nowhere. Only `ProjectDigest.ThreadCount` is set (`index/digests.go:67`).
- ROADMAP, **Near-term**: "- [ ] **`meta.json` `thread_count` is never written.** `memops.ProjectMeta.ThreadCount` is decoded and `personant verify` checks it is non-negative (`internal/verify/verify.go`), but no code assigns it, so every project reads 0; the live count is `digest.json`'s (`internal/index/digests.go`)."

**A1-4 — departure + held item (recall-cache and working-set classification). "Store directory layout" table.**
- Quote: Operational row "`working-set.json`, `.recall-cache/` … System-managed, not drift-checked".
- Disposition: state it. Add after the table: "`.recall-cache/` and `working-set.json` are filed operational for different reasons. `.recall-cache/` holds derived vectors and summary trees (SPEC §3.4 *Persistence*), but regenerating it costs embedding calls, so it cannot be kept fresh at every history-repository write as derived files are. Instead each sidecar carries a content-staleness key, a stale or missing sidecar is rebuilt on load, and deleting the directory costs only re-embedding. `working-set.json` holds the active-set order and the dormant set, which no authoritative file records and nothing rebuilds. Losing it costs only recency: the next service run starts with both sets empty (`turn.LoadSession`); a malformed file fails the load."
- SPEC also contradicts itself here. In §4.5.8 change "derived state (the symbol index, working-set membership)" to "derived state (the symbol index)". SPEC §2.1 already names "the working-set record" as operational.
- Settled by:
  - `internal/recall/measure/veccache.go` header ("operational, never canonical… Deleting .recall-cache/ costs only CPU").
  - `internal/store/workingset_ops.go:47–60`.
  - `internal/turn/turn.go:512–523`.
  - Recovery never touches the working set: `git grep -n -i 'workingset\|working-set' -- internal/recovery` matches only a cost comment.

**A1-5 — departure. "Plugin route" → **Plugin** bullet.**
- Quote: "An adapter that holds no memory state".
- Disposition: state it. After "…so all memory logic is in Go (integration-options report §2.1).", add: "It keeps only transport counters, none of them memory: the sequence number it stamps on each forwarded event, because RPCs can reach the service out of order (see "Ordering and correlation"), and a fail-open count the opencode-in-loop harness reads (`oc_*` hook fail-open counts). The prototype also carries round-trip timings for the harness. All of it lives in the plugin process and starts at zero on a reload."
- Settled by: `git show probe-path:opencode-plugin/index.ts`, lines 31–36 (`let seq`, `fallbacks`, `rtts` inside `setup`).

**A1-6 — departure + held item U9. "Closing a thread" → Triggers; "Active-set and dormant-set order"; thread registry record interface.**
- Quotes: "Engagement decay: no engagement for `engagement.decay-turns` OR `engagement.decay-time`", and "ranks tagged ids by `last_engaged_turn`, a service-run-scoped counter that is not in the thread registry record".
- The second quote is false against the code: `memops.SpineRecord.LastEngagedTurn` is `json:"last_engaged_turn"` (`internal/memops/memops_model.go:116–120`) and is written to `spine.jsonl`.
- Disposition, part 1: append to the decay trigger: "The turn count is `TurnNumber − last_engaged_turn`, both in the service run's turn counter, which starts again at 0 on every relaunch (see "Start order and resume", U9). The stored `last_engaged_turn` therefore means nothing to a later run. A stored value above the current counter skips the turn threshold; a lower one is compared as if it came from the same run (`decayEligible`, `internal/turn/closure.go`). Across a relaunch, only the wall-clock threshold is a durable decay timer."
- Part 2: in "Active-set…", replace the quoted clause with "ranks tagged ids by `last_engaged_turn`, which is stored in the thread registry record but counted in the service run's turn counter".
- Part 3: add to the `SpineRecord` interface: `last_engaged_turn: number;          // service-run turn counter at last engagement; restarts each relaunch (U9); read by turn-based decay and the unknown-thread-id owner fallback`.
- Part 4: in "Restart persistence", "the turn counter, which drives checkpoint-compaction cadence" becomes "the turn counter, which drives checkpoint-compaction cadence and turn-based decay".
- Settled by: `internal/turn/closure.go:199–237`, ROADMAP U9 (open) and `contradictions.md` E ("A1 holds the departure").
- ROADMAP: extend **U9** ("Pending decisions") with: "The same counter drives turn-based decay and the unknown-thread-id owner fallback through the stored `last_engaged_turn` (`internal/turn/closure.go` `decayEligible`)."

**A1-7 — departure, gone.** Directive precedence resolved once per service run. The parenthetical was cut by CT57 ("The A1 unstated-departure item for it then lapses", `contradictions.md`). `grep -n 'start of a service run' DOC_SWEEP/ARCHITECTURE.md` returns nothing.

**A1-8 — departure. "Recall paths" item 4.**
- Quote: "an O(log n) beam descent (`scoring.DescendChunks`) replaces the flat O(C_main) scan".
- Disposition: state it. After "…with no correctness loss.", add: "The summary tree serves SPEC §3.4 *Long threads* (no performance decay as a thread's length grows into the thousands): a flat scan's per-query cost grows with the thread's chunk count (`C_main`). The descent is approximate; how often it picks a different leaf than the flat scan is measured (`recall_intra_descent_divergence`, SPEC §9.4), and exact recall stays with the lexical level (SPEC §3.4). Summary trees are built only by consolidation (see "Memory consolidation")."
- Settled by: SPEC §3.4 (lines 811–821); `internal/recall/measure/measure.go:1508,1782`.

**A1-9 — career memory (held: architect arch-with-volumes 12, go-coder 32, departure).**
- Disposition: add a section after "Personalization through feedback directive tuning":
  "### Career memory

  Standard pattern: THESIS "The building blocks", row "Career memory".

  No career-memory mechanism is built beyond threads and recall. No store holds technique notes. Nothing files a lesson as career memory: SPEC §1.8 routes a pattern seen in one project there, but feedback directive tuning has no aggregator. Cross-project recall, the path by which a past project's threads would surface, is not built (recall path 3)."
- In "Memory vetting", "Career-memory protection uses the slots, …" becomes "Threads are protected (SPEC §1.8) by the slots, …".
- Settled by:
  - THESIS row ("Notes … drawn from the record and recalled by relevance") and SPEC §1.8 ("**Threads**, the turn-to-turn record every kind of memory is drawn from") both make career memory something drawn from threads, not the threads themselves.
  - Code: no career store exists (`git grep -n -i career -- '*.go'`: comments only).
  - The text above is true under either reading of the THESIS row, so it needs no ruling.
- ROADMAP, **Part B**, after B11: "- [ ] **Career memory has no store or write path.** SPEC §1.8 routes a pattern seen in one project to career memory (a technique); nothing writes one and no layout exists, so B10's career-memory write checks and batch review have no subject yet (ARCHITECTURE "Career memory")."

**A1-10 — held item, strip material. "Module map" `internal/model/` row; go-coder departure 9 (the in-process turn's own model call).**
- Disposition: tag only, no A2 edit. The strip plan replaces this row rather than cutting it: `ROADMAP_PLANS/strip-self-built-harness/2-inventory-contract.md` row 14 gives the text, WP1/WP5 carry it, and invariant 4 says the client stays for the in-process runtime and live tests and `./cmd` does not link it.
- Code today: `git grep -l 'personant/internal/model"' -- 'cmd/*.go'` returns nothing. A transitive link is unchecked here; it is strip criterion 3.

## Model judgments

**A1-11 — model judgments. "The model drafts, deterministic code writes".**
- Disposition: replace everything after the "Standard pattern" line. The ack paragraph ("A confirmation the user clears reflexively … actually answered.") is cut as strip material: ack modes go (`what-it-does.md`; contract row 26, WP6/WP7).

  "The model never mutates authoritative state directly. It proposes content; deterministic code decides what of it is kept, and writes it.

  | Judgment | Deterministic check before it is kept |
  |---|---|
  | Topic tag: which threads a turn engages, plus an anchor list (SPEC §5.1) | `prompt.Parse` accepts only a line matching the tag pattern within the preamble scan; each thread entry must match `thr_\d+` or `*new-topic*`; extra tags are ignored with a `topic.warning`. Anchors are normalized (SPEC §2.7.2) and enter the symbol history as `model`-sourced symbols, never directly as anchors: the projection picks the anchors and caps their count. An unknown or other-project thread id is resolved or declined (SPEC §3.2). Anchor length is not checked (ROADMAP Near-term, "Anchor projection can emit an anchor `personant verify` rejects"). |
  | Closing summary (the summarizer, SPEC §5.2) | Trimmed; an empty draft is not applied. The anchors written at close come from the projection, not the draft. Length is not checked before the write: `spine.entry-max-chars` is enforced only by `personant verify` at the history-repository write (ROADMAP Near-term, "Closing summaries are not length-bounded before they are written"). |
  | The clusterer's grouping (SPEC §5.3) | Not built. |
  | Model judgment on recall candidates (SPEC §3.4) | Not built: recall composes the symbolic and embedding signals only. |
  | Memory-vetting review (SPEC §1.8) | Not built. |
  | Lifetime tag (SPEC §3.10.8) | Not built (the keep/toss stage is deferred); SPEC fixes the fail-safe: a missing or malformed tag keeps the content. |

  Recall scoring and loading a thread into the active set are deterministic (SPEC §3.4). The behavior- and process-memory write path is "Memory vetting"'s, which is not built."
- Settled by:
  - Parser: `internal/prompt/parser.go:246–254` (anchor count never gated), `:303` (extra tags ignored).
  - Model-sourced symbols: `internal/turn/chain.go:257–263`.
  - Summary trim: `internal/curator/curator.go:79–82`.
  - Empty-draft refusal: `internal/turn/closure.go:558`.
  - Close-time projection: `closure.go:645`.
  - Recall signal 3 absent: `internal/recall/measure/measure.go:311–314` ("Layer 3 (model judgment) will slot in here later").
  - Lifetime tag only in the simulation: `git grep -n 'lifetime:' -- '*.go'` (sim only).

**A1-12 — model judgments, a gap (ROADMAP).**
- The closing summary is never bounded before it is written. `git grep -n -i 'EntryMaxChars\|entry-max-chars' -- '*.go' | grep -v _test` shows only `verify.go` reads the bound.
- `applyClosureResolution` writes `draft.Summary` as is (`closure.go:653–660`).
- ROADMAP, **Near-term**: "- [ ] **Closing summaries are not length-bounded before they are written.** `curator.DraftClosure` (`internal/curator/curator.go`) only trims the summarizer's draft, and `applyClosureResolution` (`internal/turn/closure.go`) writes it to `spine.jsonl` as is. The only check is `personant verify`'s `spine.entry-max-chars` bound (`internal/verify/verify.go`), which `CheckSpineIntegrity` runs at the day-commit, so an over-long draft fails the day rollover's thread registry integrity check (SPEC §2.2)."
- The consequence beyond the failed check (quarantine-and-proceed) is taken from ARCHITECTURE's description and was not traced in code.

## Future intent

**A1-13 — "Failure handling".**
- Disposition: replace the U7 paragraph with: "**Open:** U7 — the production failure policy, fail open or fail closed (SPEC §1; ROADMAP U7). What opencode does when the `context` hook throws or the service refuses is unestablished (resume probe §6)."
- Settled by: SPEC §1 and ROADMAP U7, which hold the options.

**A1-14 — "Event observation".**
- Disposition: replace the agents-and-skills Open with "**Open:** agents and skills — whether and how opencode agent definitions and skills are supported (ROADMAP B7)."
- Settled by: ROADMAP B7, which carries the candidate mapping.

**A1-15 — "Injection handling".**
- Disposition: cut "Candidate overflow forms for tool definitions: omit rarely used tools from a request, or list them by name with definitions on demand." and end the preceding sentence with "(ROADMAP A7)".
- Settled by: ROADMAP A7 lines 139–140.

**A1-16 — "Start order and resume".**
- Disposition: the bullet "**Resume cases not exercised** (resume probe §6): …" becomes "- **Resume cases not exercised** (resume probe §6) are listed in ROADMAP B9."
- Settled by: ROADMAP B9 has the identical list. This also removes the undefined terms "standalone-kill claim" and "resume budget".

**A1-17 — "No speculative prefetch".**
- Disposition: replace the U3 paragraph with "**Open:** U3 — how a fetch is triggered under opencode: the in-band topic tag or a recall tool (ROADMAP U3). The mid-turn fetch does not fire under opencode today (probe results §5). Until decided, the mechanism below binds:"
- Settled by: ROADMAP U3, which holds both options. This also removes `session.http.response` and "tag-fidelity measures".

**A1-18 — "Recall paths" item 3; "Many projects, one career".**
- Disposition:
  - Item 3 becomes: "3. **Cross-project recall.** Not built: a cross-project fetch and cross-project engagement are declined (SPEC §3.1, §3.2; ROADMAP "Cross-project recall")." This cuts "Per-project anchor digests … slightly higher threshold."
  - In "Many projects", "is designed (recall path 3), not built" becomes "is not built (recall path 3)".
- Settled by: `contradictions.md` CT56 (nothing implements path 3; `internal/turn/lru.go:133` `crossProjectDecline`). No ROADMAP item exists (`grep -n -i cross-project ROADMAP.md` finds only B11's digest).
- ROADMAP, **Mid-term**: "- [ ] **Cross-project recall.** Not built: the other-projects slot carries each project's digest, but no recall path scans it, and a cross-project fetch or engagement is declined (`internal/turn/lru.go` `crossProjectDecline`; SPEC §3.1, §3.2), though SPEC §2.6.1 names `recall.cross-project-threshold`. The design ARCHITECTURE carried: the digests scanned each turn by the opportunistic-surfacing path, at a higher threshold."

**A1-19 — "Fallback dissection (worst-case path)".**
- Disposition: the section body becomes "Not built: no package implements the clusterer. SPEC §3.6 states the outcome; the work is ROADMAP B11."
- Move to SPEC §3.6, after its first paragraph: "The grouping is best effort: under stress, thread quality degrades gracefully; content is not lost."
- Settled by: SPEC §3.6 lacks the best-effort outcome; ROADMAP B11 names fallback dissection.
- ROADMAP: append to **B11**: "Fallback dissection as ARCHITECTURE designed it: it fires when budget pressure exceeds `dissect.pressure-threshold` and normal closing is not keeping up; the clusterer groups the oldest poorly threaded content into related clusters; each cluster becomes a thread directory and registry record shaped like a normally closed thread and closes through the closing flow; one mechanism covers the wandering user, one thread overrunning its share, and content never cleanly threaded."

**A1-20 — held item. "Memory consolidation" ("fuller offline-reorganization vision").**
- Disposition: the body becomes: "Built: `MemoryOps.Consolidate` packs both git repositories (git gc and repack, SPEC §3.11), and `measure.RebuildTrees` rebuilds the within-thread summary trees. Only the simulation's day off (the harness's `SleepCycle` step) runs both. Outside the simulation, `Consolidate` runs only from the turn-repository loose-object check (see "Mechanism names"), and nothing runs `RebuildTrees` (ROADMAP "No production trigger for consolidation"). SPEC §3.10.8's content-retention keep/toss stage, which cites this section by name, is not built (ROADMAP Far-horizon, "Memory consolidation")."
- In "Mechanism names", "packs both repositories on the day-off idle window" becomes "packs both repositories on the simulation's day off (the harness's `SleepCycle` step)".
- Settled by: `git grep -n '\.Consolidate(\|RebuildTrees(' -- '*.go' | grep -v _test` returns `fileadapter_recovery.go:286`, `scenarios/harness_run.go:864,891` and the definition only.
- ROADMAP, **Mid-term**: "- [ ] **No production trigger for consolidation.** `MemoryOps.Consolidate` runs only from the turn-repository loose-object check (`internal/memops/fileadapter/fileadapter_recovery.go`) and the scenario harness's day-off step, and `measure.RebuildTrees` only from that step (`internal/scenarios/harness_run.go`), so outside the simulation no summary tree is built and intra-thread recall always takes the flat scan (SPEC §3.4 *Long threads*)."

**A1-21 — "Mechanism names".**
- Disposition: "An aggressive repack (`git gc --aggressive`) may be scheduled when pack size matters; none is scheduled today." becomes "No aggressive repack (`git gc --aggressive`) is scheduled."
- Settled by: the plan (no future intent in ARCHITECTURE).

**A1-22 — "Port-and-adapter at the memory store boundary"; "Key abstractions" `MemoryOps` row.**
- Disposition:
  - "One adapter ships today (…); future adapters implement the same interface without rewriting callers or tests." becomes "One adapter ships (…); callers and tests depend only on the interface."
  - The row's "One adapter today (…); future adapters swap in without rewriting callers." becomes "One adapter (…); callers depend only on the interface."
- Settled by: the plan, and "Port abstraction policy" rule 2.

**A1-23 — "Thread directory", **No parent thread**.**
- Disposition: "thread-level provenance is reserved." becomes "no thread-level provenance field exists."

**A1-24 — "Model-family as platform".**
- Disposition: the U10 paragraph becomes: "**Open:** U10 — whether one model family is tuned for the memory prompts (the prompts that ask the model for narrow memory judgments: topic tagging, summary drafting, clustering, recognition) or every model opencode runs is supported and measured per family (ROADMAP U10). Until decided, the Gemma family, served locally via the `reaper` provider, is the tuning target for the memory prompts."
- The last sentence is kept verbatim: the `reaper` clause is strip material, tagged and not resolved (novelty-report "Held", A line).
- Settled by: ROADMAP U10, which holds the options.

**A1-25 — held item. "Concurrent sessions", designed vs open.**
- Disposition: the body becomes: "Not built. Two live opencode sessions against one memory is Open (SPEC §1.1; ROADMAP B5). The memory service is the single writer either way. One consequence of today's design: `session.started active=` is logged once per service run, while the active project is per opencode session."
- Settled by: SPEC §1.1 Open, and ROADMAP B5 ("`ARCHITECTURE.md` keeps a short now-stub").

**A1-26 — future intent. "Model access and inference telemetry"; event table `model` row.**
- Disposition:
  - The Open becomes "**Open:** the source of the inference-timing block under opencode. Only the in-process turn emits `model.inference-telemetry` today (`internal/turn/turn.go`), from its own provider response."
  - In the `model` row, "— the event is retired if there is none, see …" becomes "; today only the in-process turn emits it (see "Model access and inference telemetry")".
- Settled by: code `internal/turn/turn.go:1282–1291`, and strip invariant 4, which keeps the in-process runtime.
- **This departs from** `architecture-cold-read.md`'s "Is not crossings" row ("Strip material: cut in the content pass"). That row's premise does not hold: the strip plan's `what-it-does.md` does not remove the in-process runtime. Cutting the entry would leave an event the code emits missing from the vocabulary.

**A1-27 — "Tense".**
- Disposition: "ROADMAP A2 brings the baseline comparator across; A3 and A5 rebuild the rest." becomes "Carrying the baseline comparator (the `sim-baseline` and `sim-fidelity` recipes and the committed baselines) to the build branch is ROADMAP A2; rebuilding the rest is A3 and A5."
- Then add: "An **Open:** line marks a decision not yet made, and a tag such as U7 names it (CONVENTIONS "Document conventions"); ROADMAP "Pending decisions" lists the U-tags, and ids such as A2 or B9 are ROADMAP items."

## User-facing outcomes (A1-28)

The rule: a clause that states an outcome moves to SPEC if SPEC lacks it, or is cut if SPEC has it. A clause that ties a mechanism to a SPEC outcome is kept.

| # | Heading: quote | Disposition | Settled by |
|---|---|---|---|
| a | What this is: "persistent working memory … cross-project recognition (SPEC §1)" | Restate: "…owns what enters every model request — composition, compaction and summarization, and recall — to meet SPEC §1 (the effect: THESIS "What is new")." | SPEC §1; THESIS |
| b | Failure handling: fail open/closed | Covered by A1-13 | SPEC §1 |
| c | Integration shape: "A user `/compact` cannot be refused" | Keep (a fact about opencode) | context-injections §4 |
| d | Integration shape: `/clear` discards nothing; deleting a session (SPEC §1.1) | Keep (links to SPEC §1.1) | SPEC §1.1 |
| e | Integration shape: `init` writes opencode settings; never overlap | Keep (where it writes is mechanism) | SPEC §8.1, §8.2.2 |
| f | "An ack question asks…" | Gone (CT58 cut) | `grep -c 'An ack question'` → 0 |
| g | Ordering: "independent of completion order" | Keep (cites SPEC §1.5) | SPEC §1.5 |
| h | Injection: "nothing is stale and nothing is counted twice" | Keep (cites SPEC §1.3) | SPEC §1.3 |
| i | Start order: "so after a restart the TUI shows the conversation while the model does not" | Cut the clause | SPEC §1.6 U9 Open |
| j | Model drafts: "apply without asking" | Cut (A1-11, strip) | WP6/WP7 |
| k | No speculative prefetch: "Every loaded thread … never guessed" | Keep: SPEC defers this rule to ARCHITECTURE ("Speculative pre-fetch is a non-goal (ARCHITECTURE …)") | SPEC line 1842 |
| l | One inspection location: "Forensic inspection is `cd ~/.personant && grep -r ...`" | Cut | SPEC §2.1 "inspectable with ordinary tools" |
| m | Storage non-negotiables: "hand-readable; standard tooling"; "hand-editable" | Keep (the reasons for a format choice) | — |
| n | Module map `cmd/`: "and never fails on a broken store directory" | Cut the clause | SPEC §4.1 line 1294 |
| o | Display form: "The human half … falling back to the id." | Cut (duplicates SPEC §2.2.2 verbatim) | SPEC §2.2.2 |
| p | "Display lines…": "answer most 'what was the outcome of X' questions from the line alone" | Move to SPEC §2.2.2, after "…without loading it.": "The line carries enough decision state that the model can answer most questions about a thread's outcome from the line alone." In ARCHITECTURE, the quote block becomes "The display line must carry the decision state SPEC §2.2.2 requires." | SPEC §2.2.2 says only "recognize" |
| q | Thread lifecycle: "restorable by explicit fetch" | Keep (stage description) | SPEC §3.8 |
| r | Recall paths 1: "Handles synonyms and paraphrases for free" | Keep (the reason path 1 is the default) | — |
| s | Recall paths 2: the visible line, the "We talked about X…" ask, "nothing below surfaces", log-only, and the U4 Open | Cut, ending the band sentence "…brought forward by band (SPEC §3.4)." Strip WP7 | SPEC §3.4 Banding; strip row 29 |
| t | Guarantee boundary: "does not promise spontaneous cross-domain reminiscence … State this boundary when setting user expectations" | Move to SPEC §3.4, after the completeness invariant: "Recall guarantees findability; it does not volunteer a connection to past work that shares no symbols or embedding similarity with the current work." ARCHITECTURE keeps the mechanism and cuts "State this boundary when setting user expectations; do not "fix" it." | SPEC §3.4 lacks it |
| u | Closing a thread: "applied without asking, with one visible line … queued silently" and the U4 drain paragraph | Cut (strip WP6) | SPEC §3.5 lines 904, 923; strip row 26 |
| v | Restore step 3: "so a partial thread is never written onto the thread registry" | Keep (cite SPEC §3.8.3) | SPEC §3.8.3 |
| w | Fallback dissection: "best-effort by design" | Move to SPEC §3.6 (A1-19) | — |
| x | Personalization: "The user can read what the agent learned and correct it." | Cut | SPEC §1.8, §2.6 |
| y | Personalization: "By default a pattern seen in one project … can change this routing" | Cut; keep "(SPEC §1.8)" on the bullet above | SPEC §1.8 |
| z | Many projects: "The architectural commitment: one entity, many projects, one continuous career." | Cut | THESIS "What is new" |
| aa | Crash recovery: no user acknowledgement; ≤1 turn; quarantined byte-exact; turns the user saw complete; at-risk turn; refuse-to-open | Keep (the section cites SPEC §4.5.8) | SPEC §4.5.8, §2.1 |
| ab | Model-family: "with a warning on others" | Cut (A1-24) | ROADMAP U10 |
| ac | Model-family: "fail silently and statistically" | Keep (design reason) | — |
| ad | Concurrent sessions: "multitasking one career" | Cut (A1-25) | — |
| ae | Project identity: survives moves; `/project rename`; adopts remote | Held for strip WP9 (contract row 33 rewrites this section) | strip `2-inventory-contract.md` |
| af | Event log `home-format-override`: "always paired with a stderr warning" | Keep (event description) | SPEC §4.1 line 1303 |
| ag | Applying the thesis: "Personant defines no interaction mode" | Keep (cites SPEC §1) | SPEC §1 |
| ah | Anti-patterns: "destroys user work" | Keep (reason) | SPEC §2.1 |
| ai | Active-set order: "so recency survives a relaunch" | Keep | SPEC §3.1 line 627 |
| aj | Automatic vs model-controlled: "exposes no git tool" | Keep (mechanism; B12 open) | ROADMAP B12 |
| ak | Closing: "crash-safe and correct across restarts" | Gone | `grep -c crash-safe ARCHITECTURE.md` → 0 |

## Undefined terms (A1-29)

Every term from the four reader lists, checked against the current monolith with `grep -c -F`.

**Gone** (0 hits, or removed by an item above):
- `CommandDomain`, opencode-expectations slot, `permission.assert`, `[e]dit`, `always` mode and drain point (A1-28u): CT58 and the strip cut.
- v2.0/locked, provider pool, warm window, "Layer X", B6a, shipped files, the "debt window" phrase, "crash-safe".
- directives-slot: removed by A1-14.
- standalone-kill claim and resume budget: removed by A1-16.
- `session.http.response` and tag-fidelity measures: removed by A1-17.
- accept-rate/edit-rate: removed by A1-11.

**Cite SPEC/CONVENTIONS/THESIS at first use:**
- owner turn: SPEC §3.2.
- `b-top-k`: SPEC §2.6.1 `layer.b-top-k`.
- Code Mode: SPEC §1.
- sub-mind: SPEC §1.1.
- retraction: SPEC §4.3.3.
- auto-fetch band and surface threshold: SPEC §3.4 Banding.
- honest truncation notice: SPEC §3.1 truncation policy.
- corpus-saturation floor, rung and acceptance ladder: SPEC §9.1.
- behavior, process and career memory: SPEC §1.8.
- task-class: SPEC §3.0.1, §3.10.1 (first use in "Recent-excerpt window").
- working set: SPEC §3.1.
- U-tags and ROADMAP ids: A1-27.
- A12a: SPEC §8.2.1.
- "the SQLite choice" becomes "which SQLite driver a Go SQLite need takes": CONVENTIONS "Dependencies".

**Replaced by settled vocabulary.**
- `spine`, `curator`, `retire`, `dissect`, `accrual`, `barrier`, `sleep`: code names stay (`architecture-vocabulary.md` header; SPEC §2.2 "Names beginning `spine` … are the thread registry's").
- Add under the event table: "Some category and code names predate the settled vocabulary: `spine` is the thread registry, `retire` closing, `dissect` the clusterer, `accrual` feedback directive tuning, `barrier` the day rollover and `sleep` consolidation."

**Define at first use (text to add):**
- Effect defect / Promise-API hook: "(a rejected Promise-API hook surfaces as an unhandled failure in opencode's Effect runtime, integration-options report §2.2)". This rests on the report; not re-verified.
- primary step: "(a step of the main agent's model loop, not opencode's title or compaction call)".
- transcript tail: "(the newest messages of the service run's in-run history, bounded to 35% of the live-turn reserve; `boundHistoryTail`, `internal/turn/livebudget.go`)".
- live-turn reject: "(refuse a single user prompt that exceeds its live-turn share; `internal/turn/livebudget.go`)".
- closing scan: "(the decay check for threads due to close)".
- tag strip: "(remove the topic tag from the stored and shown response)".
- pre-event deltas: "(file deltas a step carries ahead of its prompt, `Step.PreEvents`)".
- pre-authoritative rollback: "(before any authoritative write has landed)".
- `steer` prompt: "(a prompt the user sends while a turn is running)".
- coarse level: "(one vector per thread body)".
- `C_main`: "(the engaged thread's chunk count)".
- embedding debt / `EmbeddingDebtCap` / `LoadDebtWindowExcerpts`: "(excerpts scrolled out of the recent-excerpt window and not yet embedded; the cap is the unembedded backlog limit, 16)".
- managed gitignore block: move its definition from the ignore-block rule to its first use in the layout table.
- `rebaseline`, at the batch-marker list: "(the mid-day turn-repository re-baseline, see "Mechanism names")".
- lifecycle tags, first use: "(see "Lifecycle git tags")".
- "orphan add" becomes "a path absent from history repository HEAD".
- morning-init / benign morning: "(a service-run open that finds the turn repository removed by the last day rollover: the normal first open of a day)".
- cells / `TestCell5_…`: "(`cells=` lists the recovery cases hit; Cell N is case N)".
- day-off idle window / `SleepCycle`: "(the simulated workload's day off, SPEC §9.4.1)". See A1-20.
- `bound=`/`unbound=`: "(excerpts moved into / out of bundles)".
- `W1-diag`: "(per-probe line for a descent-versus-flat-scan divergence: class, flat, descent, missed, substituted; `measure.go:1689`)".
- `net-cap-hit`: "(the intra-thread descent dropped related leaves at `scoring.NetCap`)".
- `flush-backlog`: "(pending flush jobs passed the warning threshold)".
- `under-drain`: "(archival at a rollover could not reach the registry low-water mark)".
- measurement probe / measurement carrier / `rtActive` / execution-time refinement:
  - "a measurement probe is a scripted step with `ExpectedRecallMatches` ground truth";
  - "a measurement carrier is a synthetic thread created to host a recall-measurement query (`sim/workload.go`)";
  - "`rtActive` is the harness's mirror of the service's active set";
  - "a refinement turn is one the generator injects after a recall step misses".
- "the 30-minute check interval" becomes "(synthetic store directories; it runs in `just test`)". `internal/scenarios/crash_matrix_test.go` has no build tag; the justfile names no 30-minute interval (`GOTESTTIMEOUT := "60m"`).
- `GitCheckFlags` "64 slots" / bitflag: "(a `uint64` bitmask of check functions; `internal/autogit/autogit.go:60`)".
- "Commits are signed from the memory store's git config, falling back to a fixed identity" becomes "Commits take their author and committer identity from the memory store's git config, falling back to `personant <personant@localhost>`; they are not cryptographically signed" (`internal/store/init.go:525`).
- "new-thread summary length bound" becomes "120 bytes (`descriptionFromNewThread`)".
- `personant-mock`: "(the mock provider in the harness's isolated opencode config)".
- `experimental.idle`, `experimental.generate`: "(opencode plugin hooks, integration-options report §2.3)".
- `reaper` / `reaper.local` / gemma-4 / oMLX, at "Acceptance-run mechanics": "(`reaper.local` is the live-run host, oMLX serving the gemma-4 family, reached through `internal/testsupport`, override `PERSONANT_REAPER_URL`)". The SPEC §8.2.1 conflict stays held (A1-10, A1-24).

**Defined in place already (no change):**
- "managed server": "Start order and resume", first bullet.
- run-volatile state: U8 paragraph.
- `LayerB`, `HotExcerptTail`, scoped-restore, freshness-assert, `reborn`, `re-mint`, day-commit.
- beam descent: a term of art, glossed in the Module map.

**No change:** "Grant", `trf3bd`/`unk0bd` are example content.

**A1-30 — undefined term. "Asymmetric cost: prove ahead of time"; "Testing as the measurement instrument".**
- `grep -n -i 'architectural thesis' DOC_SWEEP/*.md` finds only lines 460 and 1819; THESIS defines no such term.
- Disposition:
  - Line 460 becomes "Whether this design meets SPEC over months of accumulated memory cannot be judged by inspection."
  - Line 1819 becomes "SPEC §9 is the measurement instrument that tests, by simulation, whether the design meets SPEC over a long career, and the means by which the techniques evolve iteratively."

**A1-31 — held item. "Tracked-file store" Out-of-date marker.**
- It matches the ruling (SPEC §3.9.1 governs; ROADMAP Near-term holds the defect). One gap: the ROADMAP item also names archival's capture commit, and the marker does not.
- Disposition: inside the parenthetical, "(the day-commit step, and restoring …" becomes "(the day-commit step, the archival capture commit, and restoring …". Nothing else changes.

---

## Counts by disposition

Counted by A1-number; an item with two outputs is counted under each.

| Disposition | Items |
|---|---|
| State in section (departure or fact) | 10: A1-1, 3, 4, 5, 6, 8, 9, 11, 20, 31 |
| ROADMAP line (new or extended) | 9: A1-2, 3, 6 (U9), 9, 12, 18, 19 (B11), 20 |
| Restate / cut future intent | 15: A1-13 to A1-27 |
| User-facing (A1-28): move to SPEC | 3 (p, t, w); also SPEC §4.5.8 wording (A1-4) |
| User-facing: cut | 12 (a restated, i, j, l, n, o, s, u, x, y, z, ab, ad) |
| User-facing: keep (links to SPEC) | 17 |
| User-facing: gone / held for strip | 2 gone (f, ak) / 1 held (ae) |
| Terms: define / cite / settled vocabulary / gone | about 40 / 16 / 7 / about 20 (A1-29), plus A1-30 |
| Gone (A1-7) | 1 |
| Held, strip material, tag only | 2: A1-10, the A1-24 `reaper` clause |

## Owner calls

None. Two items came closest; the record settles both:
- **Career memory (A1-9):** threads-plus-recall versus a separate notes store. The proposed text is true under both readings, and the design question waits for B11.
- **`model.inference-telemetry` (A1-26):** the cold-read triage said cut. The code (`internal/turn/turn.go:1291`) and strip invariant 4, which keeps the in-process runtime, settle it as keep. The dispatcher should note the triage row is superseded.

**Outside A1's brief, for X1:** process instructions in ARCHITECTURE, which its Must-see bars:
- "Applying the thesis": "When making a design decision, ask:";
- "Testing as the measurement instrument": "Bake metrics emission into new code from day one…" and "the question is always 'what does the simulation say?'".
