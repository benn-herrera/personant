# Doc sweep: post-plan review burndown

Findings of the P1 review (`adjagent:tech-writer` and `adjagent:prompt-engineer`, at `e369983`),
triaged against the record. Execute each item as written: locate the quote in the named file,
apply the fix.

- Paths are under `PROJECT_DOCS/` unless they start with another top-level name. A file named by
  heading only is found with `grep -rl -F '<heading>' PROJECT_DOCS`.
- Quotes may wrap across a line break in the source: search the first few words.
- "ROADMAP Bn" is a ROADMAP.md item; a bare "Bn" is an item of this list.
- A retitle renames the leaf file to the new heading's slug (`git mv`) where the heading is a
  leaf's own, and updates its Contents lines and every link and title citation to it.
- Settled vocabulary: close/closing; thread registry; turn repository, history repository; day
  rollover; summarizer; clusterer; cold storage; in-context exclusion; recent-excerpt window;
  feedback directive tuning; authoritative (not canonical); memory store or store directory (not
  home, substrate); automatic (not autonomic). Backticked code identifiers keep their spelling.
- Out of scope: rendered user-facing strings, and strip material
  (`ROADMAP_PLANS/strip-self-built-harness/what-it-does.md`).
- No item adds or drops a requirement. A fix that would is reported, not applied.
- After the last item: `just docdicer-check PROJECT_DOCS "PROJECT_DOCS *.md ROADMAP_PLANS/*.md"`
  passes.

## Fix: documents

**B1. CONVENTIONS "Before a change" blocks the routing SPEC §1.8 leaves to user policy.**
`CONVENTIONS/before-a-change.md`: replace the whole bullet that contains "promotes a single-project
pattern into behavior memory" with: "- a change that writes behavior or process memory other than
through SPEC §1.8's single write path, or that hard-codes a routing SPEC §1.8 leaves to the user's
policy (SPEC §1.8 "Feedback directive tuning")."

**B2. Plugin-route tense is lost per leaf.**
- `ARCHITECTURE/plugin-route/index.md`: add, as a new paragraph directly after the "Standard
  pattern" line: "In every leaf of Plugin route, present tense states the production design,
  which is not built yet (see "Tense")."
- `ARCHITECTURE/plugin-route/tense.md`: "In this section and the ones that follow it" → "In every
  Plugin route leaf".
- `ARCHITECTURE/mechanisms/model-access-and-inference-telemetry.md`: prefix the paragraph that
  opens "The memory service's judgments go through the host" with "Production design, not built: ".

**B3. The "Unused →" marker reads as retired text.** CONVENTIONS defines it as a section that keeps
its number and states only the outcome, its format in ARCHITECTURE X.
- `CONVENTIONS/document-conventions.md`, "Moved sections" bullet: "A SPEC heading marked "Unused →
  ARCHITECTURE «X»"" → "A SPEC section marked "Format → ARCHITECTURE «X»""; "of that heading" →
  "of that section".
- Every SPEC leaf (`grep -rln 'Unused → ARCHITECTURE' PROJECT_DOCS/SPEC`): "Unused → ARCHITECTURE"
  → "Format → ARCHITECTURE".
- Plain "Unused" for empty section numbers (§0, §4.3.1, …) is unchanged.

**B4. "v0.1" is an undefined scope label.** Apply exactly these (example values such as
`version=0.1.0` stay):
- SPEC §3.2, `unknown-thread-id` passage: "is not built in v0.1 and is declined" → "is declined".
- SPEC §3.0.2: "**v0.1 non-goal (…)**" → "**Non-goal (…).**"; "This step performs no budget
  demotion in v0.1." → "This step performs no budget demotion."
- SPEC §3.8.4 body: drop " in v0.1". Heading "3.8.4 Recall treatment of archived threads (v0.1:
  off-recall)" → "3.8.4 Recall treatment of archived threads (off-recall)" (a retitle; header
  rule).
- SPEC §3.10.8: "v0.1 **over-retains**" → "Retention **over-retains**"; cut "(outside v0.1 scope)".
- SPEC §5.1.3: drop " in v0.1".
- SPEC §2.3 and ARCHITECTURE "Thread directory": "v0.1 keeps all excerpts" → "all excerpts are
  kept".
- SPEC §2.6.1: "**v0.1 honors only**" → "**Directive files set only**".
- SPEC §9.1 index: "deferred past v0.1" → "not required".
- ARCHITECTURE "Recall paths" index, "Event log format", "The per-delta pipeline" index: drop the
  word "v0.1" and any "in" before it.
- SPEC §3.4 index: see B23 (joint result).
- Then `grep -rn 'v0\.1' PROJECT_DOCS` lists only example values; report any other hit.

**B5. SPEC §1 says Personant owns compaction.** `SPEC/1-operating-model/index.md`: "composition
(…), compaction (…) and summarization, and recall (…)" → "composition (…, §3.1), which takes the
place of opencode's compaction; summarization (condensing older content, §5.2); and recall (…,
§3.4)", keeping each existing parenthetical gloss for composition and recall.

**B6. "Checkpoint" names two things.** SPEC §1.7 (`1.7-opencode-storage.md`): "checkpointing" and
any "checkpoint" there that means opencode's compaction → "checkpoint compaction(s)". ROADMAP B3:
"Checkpoints must not grow" → "Checkpoint compactions must not grow". SPEC §3.11's checkpoint
(a whole-tree commit) is unchanged.

**B7. One name for the composed request.** "working window", "working-window" and "composed
context" → "composed request", at:
- SPEC §3.0 `index.md`: cut the definition "The **working window** — …" (SPEC §1 defines the
  composed request), keeping the sentence's statement about deltas with "composed request";
- SPEC §3.0.5; SPEC §3.4 in-context exclusion; SPEC §3.9 `index.md` ("composed context"); SPEC
  §3.9.2; SPEC §3.1 `tool-output-in-composed-requests.md`;
- ARCHITECTURE "The per-delta pipeline" index (including "one content addition to the window" →
  "one content addition to the composed request"); ARCHITECTURE
  `the-composed-request-and-its-slots/index.md`; ARCHITECTURE `mental-model-summary.md`
  ("working-window mutation" → "composed-request mutation"; B8 also edits this file).
- Retitle SPEC "3.9.2 Live context composition (working window)" → "3.9.2 Live composition".
- Then `grep -rn -i -E 'working[- ]window|composed context' PROJECT_DOCS ROADMAP.md README.md`
  returns nothing.

**B8. Process rules in ARCHITECTURE.**
- CONVENTIONS "Dependencies" (`CONVENTIONS/house-rules-for-agents/dependencies.md`): its first two
  lines become "- **A dependency lands with its first consumer, never before**; no hand-built
  complex substitute for one that would just work. `go.mod` is the catalog." It stops deferring
  to the ARCHITECTURE leaf.
- ARCHITECTURE "Approved-when-earned dependencies": body becomes "The design is stdlib-first; every
  module in `go.mod` has a consumer in the tree." The leaf and its title stay.
- ARCHITECTURE "Port abstraction policy", rule 4: "Concessions are explicit, case-by-case,
  user-approved" → "Concessions are explicit and case-by-case". CONVENTIONS "Before a change": add
  to its report-don't-write list "- a backend concept added at the `memops` port without the
  user's approval".
- ARCHITECTURE "Anti-patterns to push back on": delete the "Add this dep speculatively" row and the
  "Tests can come later" row. CONVENTIONS "Before a change", "Also:" list: add "- Tests land with
  the feature, not after." The dependency rule is already in "Dependencies" (above).
- ARCHITECTURE "Mental model summary": cut "Every dependency earns its keep with a real consumer."
- ARCHITECTURE "Asymmetric cost: prove ahead of time": "If it's high, default to the standard
  path." → "The design takes the standard path wherever migration cost is high."
- ARCHITECTURE "How decisions evolve": no edit.

**B9. Four THESIS rows lack a "Standard pattern" line.** Add `Standard pattern: THESIS "The
building blocks", row "<row>".`:
- row "Cold storage": directly under the heading of ARCHITECTURE "Archive index
  (`archive/index.jsonl`)";
- row "Indexes": directly under the heading of ARCHITECTURE "Storage and record formats" index;
- row "Content dedup": at the start of the bullet "**Tracked-file store.**" in ARCHITECTURE
  "Thread directory", as its first sentence;
- row "The CLI": as the first sentence of the `cmd/` row's cell in ARCHITECTURE "Module map".

**B10. Memory vetting leaf contradicts itself.** `ARCHITECTURE/mechanisms/memory-vetting.md`:
replace the sentence(s) from "writes pass through the `MemoryOps` port" through "The port has no
directive-write operation today" with: "Mechanism: behavior- and process-memory writes go through
one `MemoryOps` operation, where the review stage sits; no such operation exists yet
(`DirectiveParam` only reads)."

**B11. THESIS gaps.** `PROJECT_DOCS/entry-point.md`:
- after the building-blocks table, before the paragraph opening "Where a piece is in this table",
  add: "Behavior memory has no row of its own; process memory is directive text (row
  "Directives")."
- "this projects' implementation" → "this project's implementation".
- Indexes row: "canonical files" → "authoritative files".

**B12. SPEC holds implementation state and future intent.** Cut, keeping the requirement around
each:
- §3.9.1: the Open saying the implementation still commits the tracked-file store to the history
  repository (ROADMAP Near-term holds it);
- §1.6: "today only the working set reloads" (ROADMAP U9 holds it);
- §1.8: "; the review is not yet built, §3.10.8" (ARCHITECTURE "Memory consolidation" holds it);
- §3.10.3: "its future directive key is `staging.window-turns`" and "currently fixed at 3" →
  "fixed at 3";
- §9.4.1: the sentence opening "Revisit if";
- §3.0.1: cut the sentence opening "New capabilities"; "These are all the deltas…" already closes
  the taxonomy.

**B13. SPEC §3.8.3 restore commit.** "the restore is committed to the memory store's git history"
→ "the restore is committed to the turn repository and reaches the history repository at the next
day rollover (§3.11)". Retitle "Two recovery surfaces, different strength" → "Two ways to get
content back, different strength" (no citations exist).

**B14. SPEC §4.2 vocabulary bridge.** "The command names `/topic` and `/topics` keep their
spelling." → "The command name `/topics` keeps its spelling."

**B15. ARCHITECTURE "Applying the thesis".** "THESIS states why Personant exists." → "THESIS
lists the standard pattern each piece follows."

**B16. SPEC §4.5.8 headings name mechanism.** Keep each number: "(1) Detection observables" →
"(1) What recovery reads"; "(2) Turn transaction protocol" → "(2) What a turn guarantees";
"Day-commit-shape guard, at-most-once per day" → "Each day committed once".

**B17. "close-out".** SPEC §2.6.1, §3.5, §4.2 and ARCHITECTURE "Event log format": "close-out(s)"
→ "closing(s)" — "routine closing", "exception closing", and in §2.6.1 "routine closings (of
decayed threads)". §3.5: "These criteria are provisional values" → "This threshold is a
provisional value".

**B18. Vocabulary outside the swept volumes.** In `ROADMAP.md` and `README.md` prose: curator →
summarizer; dissector → clusterer; spine → thread registry; canonical → authoritative (not
"Canonical Ltd."); substrate → memory store; autonomic → automatic; Layer E → directives slot;
daily DB → turn repository; Barrier completion → day-rollover completion; deep-cold → cold
storage; directive accrual → feedback directive tuning (including ROADMAP B11's title); closure →
closing, except "closure ack-mode amendment" → "`closure.ack-mode` amendment"; folio → excerpt
bundle. ROADMAP "Memory consolidation — the "sleep" cycle." → "Memory consolidation."
Elsewhere:
- SPEC `4.2-…/index.md`: "made autonomic" → "made automatic".
- ARCHITECTURE "Recovery cases": "Dual-repository operation rows" → "Turn- and history-repository
  rows".

**B19. ROADMAP title citations the sweep renamed.** `ROADMAP.md`: SPEC §9.1 "Two version lines" →
"Version lines"; ARCHITECTURE "Runtime inversion" → "Hook-driven turn phases"; SPEC §3.1 "Slot
answers" → "Slot policies"; ARCHITECTURE "Personalization through directive accrual" (wraps) →
"Personalization through feedback directive tuning"; "Port-and-adapter" → "Port-and-adapter at the
memory store boundary"; CONVENTIONS "Versioning" → "Versioning and acceptance". Then
`grep -o 'ARCHITECTURE "[^"]*"' ROADMAP.md | sort -u`: each title must exist in
`PROJECT_DOCS/ARCHITECTURE` as a heading, except "Tracked-file store", a bold-bullet referent in
"Thread directory".

**B20. ROADMAP items carry surroundings.**
- ROADMAP B1: drop "until the doc sweep, `ACTIVE_PLAN.md`, lands"; keep "parked after WP2".
- ROADMAP "Parked plans", strip-plan line: "resumes once the doc sweep (`ACTIVE_PLAN.md`) has
  rediced `PROJECT_DOCS/`, with its contract rows re-mapped onto the rediced tree" → "parked
  after WP2 (ROADMAP B1)".
- "Event-log rotation": "The layout ARCHITECTURE carried was" → "Intended layout:".
- "Cross-project recall": "The design ARCHITECTURE carried:" → "Intended design:".
- ROADMAP B11: move the text from "Fallback dissection as ARCHITECTURE designed it:" to the end of
  that paragraph into a new item directly after ROADMAP B11: "- [ ] **B11a. Fallback
  dissection.** Intended design: …". ROADMAP B11's opening keeps listing fallback dissection.

**B21. CONVENTIONS "opencode clone: referent only".** Replace from "it moves only" through the
sentence's ")." with "it moves only in the change that states the supported release
(ARCHITECTURE "Pinned release")."

**B22. Citation fixes.**
- SPEC §3.1, §3.4 and §9.1 `version-lines.md` cite §2.1 for `working-set.json`, `.recall-cache/`
  or `version.toml`, which §2.1 does not name. Cite the file and its class separately, e.g.
  "(`working-set.json`: ARCHITECTURE "Store directory layout"; class [§2.1](…))".
- SPEC `9.1…/version-lines.md`: "Versioning & acceptance" → "Versioning and acceptance".
- CONVENTIONS "Memory service: Go": "Dependencies" "below" → "House rules for agents,
  "Dependencies"".
- ARCHITECTURE "Document map": drop every "(this document)".
- ARCHITECTURE "Project identity": drop the Open's mention of "the final fallback".
- ARCHITECTURE "Anti-patterns to push back on", prefetch row: the recognition route → "the topic
  tag, or recall's auto-fetch band (see "No speculative prefetch")".
- ARCHITECTURE: "Spec §" → "SPEC §" (`grep -rln 'Spec §' PROJECT_DOCS/ARCHITECTURE`).

**B23. Small terms.**
- ARCHITECTURE `plugin-route/start-order-and-resume.md`: "rather than from process memory" →
  "rather than from in-process state".
- SPEC §3.1 leaf "§5.5 mid-turn fetch" → retitle "Mid-turn fetch budget".
- ARCHITECTURE `key-abstractions.md` "Delta" row: definition → "one content addition to the
  composed request" (as B7 leaves the pipeline index).
- "Explicit fetch" for restoring an archived thread: ARCHITECTURE `thread-lifecycle.md` "restorable
  by explicit fetch" → "restorable with `personant archive recover`"; SPEC §3.4 index, the
  completeness-invariant exception, becomes "archived threads, which are off live recall and
  restored with `personant archive recover` ([§3.8.4](…))" (this is also B4's §3.4 edit); any
  other "explicit fetch" meaning restore → "restore (`personant archive recover`)".
- ARCHITECTURE `automatic-vs-model-controlled.md`: "That the service is the only writer of the
  store directory is the intent." → "The service is the only writer of the store directory by
  design; enforcing it is ROADMAP B12."
- README intro: "context-management and long-lived memory system" → "memory system for a coding
  agent".
- THESIS "Recall" row "Lexical and embedding" → "Symbolic (Jaccard) and embedding"; SPEC §3.4
  index "Symbolic Jaccard" → "Symbolic (Jaccard)". "exact lexical" (a different level) is
  unchanged.

## ROADMAP'd

**B24.** Add at the end of ROADMAP "Near-term": "- [ ] **Code comments cite retired document
names.** Comments in `internal/memops/fileadapter/` cite INV-1…INV-6 (no longer in ARCHITECTURE),
`fileadapter.go` names a `turn/archival.go` drain that does not exist, `internal/turn/projection.go`
says ever-central symbols rank in the high class and that class breaks ties of equal count
(`rankClass` tests only `IsHighSpecificity`, and class outranks count), `fileadapter_barrier.go`
says derived files never exist in history-repository HEAD (`digest.json` does), and several files
say "sleep cycle"; a comment-only pass fixes them."

## Not applied (settled by the record)

- **Cosine surface threshold value**: SPEC §3.4 "Banding" already says the cosine floor is not a
  directive key; stating 0.55 adds a requirement.
- **SPEC §1.8 "one write path", "never blocks a turn"; ARCHITECTURE "No delta reaches the composed
  request without going through it"**: SPEC states required outcomes whatever is built; build
  state is ARCHITECTURE's and ROADMAP's.
- **U7 and `conventions_paths` defaults**: an `Open:` with no binding default is CONVENTIONS
  "Document conventions" form 1, and both name their decision.
- **CONVENTIONS "ask:" questions, "Bake metrics emission"**: process guidance, CONVENTIONS' own
  content.
- **`opencode-database-cleanup.md` holds an unbuilt design**: Plugin route states the production
  design (B2).
- **THESIS Must-see**: AGENTS.md's rule covers subtree entry points; THESIS is the root.
- **Anti-reaccretion doctrine**: removed on owner direction in this sweep.
- **Mode-parameter leaf titles, the `internal/model` client, SPEC §4.5.3 vs §4.5.7, `/done`**:
  strip material (header).
