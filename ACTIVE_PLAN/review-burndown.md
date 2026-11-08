# Strip plan: post-sprint review burndown

Findings of the post-sprint review (`adjagent:go-coder` code, `adjagent:architect` structure,
`adjagent:tech-writer` documents) over the strip commits `488de08`…`1be480c`, triaged against the
record and revised on its usability review. Order: part K (one go-coder dispatch), then the
dispatcher's checkpoint and part R (ROADMAP lines), then part D (one tech-writer dispatch).

Execution rules:
- Locate targets by the names given; line numbers are hints. Quotes may wrap: search the first
  few words.
- An item that changes behaviour names its test; a test failing on behaviour no item changes is a
  finding, not edited to pass.
- No shims or dual paths. Comments state what the code cannot (an invariant, an external
  contract, why not the obvious way); they cite no decision, review id or document.
- Document vocabulary: close/closing; thread registry; store directory, memory store; turn and
  history repository; day rollover; summarizer; automatic; active set; in-context exclusion;
  auto-fetch band. Code identifiers keep their spelling. "Not built" is stated as "Not built
  (ROADMAP "<title>")"; `**Open:**` marks only a pending decision (CONVENTIONS "Document
  conventions").
- Deleted or retitled leaves: `git rm` / `git mv`, update Contents lines and links.

## K: code

**K1. Closing skips demotion's flush.** `internal/turn/closure.go` `closeThreadResolved` removes the
thread from the active and dormant sets with `removeString` but never calls `flushOnDormancy`,
which `touchActiveLRU` (`internal/turn/lru.go`) calls when a thread leaves the active set. Add
`evictThread(state, id)` in `internal/turn`: remove the id from both sets; call `flushOnDormancy`
if it was in the active set. `closeThreadResolved` and the day-barrier archival loop in `turn.go`
(which removes archived ids with `removeString`) call it. `touchActiveLRU` keeps its
active-to-dormant move. Test `TestClosure_FlushesOnClose`: an active thread with unflushed debt
closes; its flush was requested. (SPEC §3.4: a thread's content is indexed by the time it leaves
the working set.)

**K2. An automatic intra-thread fetch delivers nothing and hides content.** An intra-thread result
(`IntraThread != nil`) targets the engaged thread, already in the active set; the fetch re-sends
its recent body, so the matched early excerpts never reach the model, while
`markRecallWindowTurns` hides them from the fine level and the lexical floor. Change: the
auto-fetch in `internal/turn/recall.go` skips intra-thread results — no `acceptRecallCandidate`,
no `recall.accept`, no `recall_fires` bump; their match-fire line stays, and a skipped intra result
still occupies its top-3 slot. Delete `markRecallWindowTurns`, `State.recallWindowTurns`,
`measure.Request.EngagedInWindow`, `dropInWindow`, `intraThread`'s `inWindow` parameter and their
cases in `intra_recall_test.go`; delete the `recallWindowTurns` entry removal in `lru.go`; delete
the "HONEST LIMIT" comment block and `measure.go`'s "the excluded content is in the window"
comment. Rewrite `TestRecall_FetchedIntraHitIsNotFetchedAgain` as `TestRecall_IntraHitIsNotFetched`:
an intra-thread result above its bar is not fetched and leaves its match-fire line.

**K3. The saved working set crosses projects.** `turn.LoadSession` loads the global
`working-set.json` into any project, and composition renders the active set unfiltered. Add
exported `turn.ScopeWorkingSet(ctx, state)`: drop every active and dormant id whose registry record
belongs to another project than `state`'s, each dropped active id through `evictThread` (K1); it
writes nothing (the next working-set save persists the result). Call it at the end of
`LoadSession` and from `internal/scenarios/harness.go` `SwitchProject` (the only in-run project
change once `internal/turn/commands.go` is deleted). Add a per-step harness invariant: every
active-set id belongs to the active project. `TestScenario_ProjectSwitching` runs it after each
`SwitchProject`. Delete `internal/turn/commands.go` (`SwitchProject`, `persistWorkingSet` have no
caller). (SPEC §3.1 mid-turn fetch budget; §3.2.)

**K4. A rolled-back turn logs nothing.** `internal/turn/turn.go` `abortPreCanonical` rolls back
silently; the only producer of `system.turn-aborted` was the deleted `internal/chat`. Emit
`system.turn-aborted` with `turn=` and `bytes=` (the prompt's byte count) from
`abortPreCanonical`, on every pre-canonical abort (retraction and provider error alike). No
`phase=` field. Test in `rollback_test.go`. (SPEC §4.3.3; D6 aligns the docs.)

**K5. (removed)** SPEC §4.3.3's recall marks are `recallSurfaced`, which `rollback.go` already
snapshots.

**K6.** `internal/turn/recall.go` `acceptRecallCandidate`: drop the unread `bool` return.

**K7.** `internal/turn/stream.go` `streamThroughFilter` and `readPreamble`: drop the unused
`state` parameter; update callers and tests.

**K8.** `internal/curator/curator.go`: delete `ClosureDraft`, `SelectAnchors` and
`closureAnchorMax`; `DraftClosure` returns `(string, error)` (the summary). Update callers:
`internal/turn/closure.go`, `internal/scenarios/harness_setup.go` (its stub draft),
`internal/turn/closure_test.go`. Delete `TestSelectAnchors`.

**K9.** `internal/model/client.go`: drop `ListModels` from `Client`; `NewHTTPClient` returns
`*HTTPClient`. Delete `MockClient.ListModels`, `MockClient.Models` and `NewScriptedMock`'s `models`
parameter (re-grep callers), and the `ListModels` methods on the test doubles `stubClient`
(`internal/curator/curator_test.go`) and `captureClient` (`internal/turn/budget_acceptance_test.go`).
Live callers use the concrete method: `internal/prompt/*_probe_test.go`,
`internal/turn/embed_recall_integration_test.go`, `internal/scenarios/sim/sim_live_test.go`,
`internal/testsupport/livetests.go`. Remove the type assertions made dead (`embed.go`,
`sim_live_test.go`).

**K10.** `internal/memops/event_log.go`: delete `LogCategoryPermissions` and `LogCategoryUser`.

**K11.** `internal/store/init.go`: remove the seeded `.gitignore` `history` entry and its REPL
comment, and update `internal/store/init_test.go`'s expectation; reword the `seedUserMD` body and
the nearby comment so neither mentions ack-prompt scope grants or decline categorizations.

**K12. Stale comments and names.**
- Comments naming removed things: `git grep -n -E '\bREPL\b|chat\.|re-offer|offered|closure-defer|/project|DefaultModel|provider pool|turn/archival\.go|sleep cycle' -- '*.go'`;
  reword or delete each.
- Decision and document ids in comments:
  `git grep -n -E 'design §|Inc [0-9]|burndown #|BD-[0-9]|C\.[0-9]|#[0-9]+\b|\(S[0-9]\)|INV-[0-9]|user ruling' -- '*.go'`;
  drop the id, keep the invariant.
- `internal/turn/projection.go`: the comments saying ever-central symbols rank in the high class
  and that class breaks ties of equal count; `rankClass` tests only `IsHighSpecificity`, and class
  outranks count. `internal/memops/fileadapter/fileadapter_barrier.go`: the comment saying derived
  files never exist in history-repository HEAD (`digest.json` does). Correct both.
- `Checkpoint`: `memops.go`'s doc says turn close and session close; `turn.go`'s Step 5c comment
  and `checkpoint_test.go` name `chat.go`. State that nothing in production calls it (the clean-stop
  checkpoint belongs to the plugin route's service).
- `git mv internal/scenarios/harness_resolvers.go internal/scenarios/harness_invariants.go`;
  update `harness.go`'s reference.
- `recallOfferK` → `recallFetchK` (`internal/turn/recall.go`), including the comments in
  `internal/recall/measure/measure.go` and `ranking_internal_test.go`.
- `internal/scenarios/sim/workload.go`: the comment saying `Next` cross-checks the shadow Layer-B
  model (the check reads `rtActive`).

**K13. Untested recall bounds.** Add `TestRecall_AutoFetchBarPerSignal` (a table over
`autoAccepts`: symbolic at and below 0.65, cosine at and below 0.75),
`TestRecall_FetchesAtMostThree` (four candidates clear the bar, three are fetched) and
`TestRecall_SkipsResident` (a non-intra candidate already in the active set is not fetched). Add a
unit test for the `recallFetchedIDs` scraper in `internal/scenarios`.

**K14. Opportunities emptied by the mirror filter.** Count a recall opportunity whose expected set
the runtime-mirror filter empties under `recall_fidelity_opportunities_resident`, and the embedding
twin under `embed_recall_fidelity_opportunities_resident`, separate from the `…_unmeasured_steps`
counters; add both as constants in `internal/scenarios/metric_keys.go` (they are reported series).

**K15.** `cmd/main.go`: drop `openHome`'s unused `paths` return.

**K16.** `internal/verify/verify.go`: the name error message becomes `name %q is not a valid
project name`.

**K17. Port methods with no intended consumer.** Remove `CreateProject`, `NextProjectID`,
`LoadProject`, `SaveProject`, `FindProjectByPath` and `ArchiveThread` from both `memops.MemoryOps`
and `FileAdapter`; tests and the harness use the `store` helpers, `ResolveActiveProject` or
`ArchiveThreads`. Keep `LoadSession`, `ResolveActiveProject`, `IsInputExceedsBudget`,
`ErrMarkerRetained`, `NewHTTPCurator`. Re-grep callers.

**K18–K19. (moved to R5; no change)** The harness's project seeding stays (R5). The top-3 bound
stays before the resident skip, as SPEC §3.4 "Recall candidates" states.

**K20. Done-line.** `just build`; `just test-run` for `./internal/turn`, `./internal/scenarios`,
`./internal/scenarios/sim`, `./internal/curator`, `./internal/model`, `./internal/prompt`,
`./internal/testsupport`, `./internal/store`, `./internal/memops/...`, `./internal/recall/measure`,
`./internal/verify`, `./cmd`; both K12 greps return only lines kept on purpose (listed in the
report).

**K21.** The dispatcher runs `just test`, then `just sim`, and diffs the metrics JSON against
`test/rundata/sim-workload-seed24095-dur168h0m0s.261108061052/sim-workload-seed24095-dur168h0m0s.261108061052.metrics.json`.
K2 is expected to drop intra-thread `recall.accept` lines; a failing gate is diagnosed, not
loosened.

## R: ROADMAP lines (dispatcher, after K)

- **R1.** Near-term: "- [ ] **`last_active` is never refreshed.** `meta.json` `last_active`
  (ARCHITECTURE "Project metadata": updated on any engagement) has no writer, so the
  other-projects slot's most-recently-active order (`internal/workset/workset.go`, SPEC §3.1) is
  the creation order."
- **R2.** Mid-term: "- [ ] **No metric measures recall fetches.** Recall fidelity scores
  match-fires, and the sim oracle copies the runtime's own fetches, so the auto-fetch bar, the
  top-3 bound and the resident skip have unit tests but no acceptance series (SPEC §9.4)."
- **R3.** Near-term: "- [ ] **Intra-thread recall finds but does not deliver.** An intra-thread
  match is logged as a match-fire and not fetched: nothing puts a thread's earlier excerpts into
  the composed request (SPEC §3.4 in-context exclusion; `internal/turn/recall.go`)."
- **R4.** Mid-term: "- [ ] **Recall's model-judgment signal is not built.** SPEC §3.4 signal 3;
  candidates are fetched on the symbolic and embedding signals alone (ARCHITECTURE "The model
  drafts, deterministic code writes")."
- **R5.** Near-term: "- [ ] **The sim never resolves a project.** The harness seeds projects with
  `store.SaveProjectMeta` (`internal/scenarios/harness_setup.go`), so `ResolveActiveProject`
  (SPEC §4.5.7) runs only in unit tests."
- **R6.** Near-term, if no item says it: "- [ ] **Named §9.4 scenarios and churn tests are
  missing.** SPEC §9.4's heavy-closing and same-anchor-collision scenarios and the churn level
  have no test in `internal/scenarios`."
- **R7.** Delete the Near-term item "Code comments cite retired document names" (K12 fixes it).

## D: documents (after K and R)

**D1. SPEC §2.6 index contradicts §2.6.1.** "YAML frontmatter for parameter overrides" → "YAML
frontmatter. The `parameters` field is reserved: no §2.6.1 key is read from it (§2.6.1)"; the
template comment `# §2.6.1 key: value overrides` → `# reserved; not read (§2.6.1)`; the sentence
"A parameter read walks the precedence chain and returns the first match; missing parameters fall
through to defaults." → "The precedence list orders directive text only."

**D2. `decay-time`.** SPEC §3.5 index: "`decay-time` catches extended user absence" →
"`decay-time` measures a thread's neglect against its project's most recent engagement, so an
absence from the project closes nothing; `decay-turns` catches low-frequency engagement in a busy
session"; in the same paragraph state that closing covers the active project's threads.
ARCHITECTURE "Closing a thread", Trigger: add that idle time is measured from `systemReference`,
the newest `last_engaged` across the project's threads.

**D3. Recall's third signal.** SPEC §3.4 index: "before a recall is offered" → "before a
candidate is fetched"; after the signal list add "Signal 3 is not built (ROADMAP "Recall's
model-judgment signal is not built"); candidates are fetched on signals 1 and 2 alone."

**D4. Intra-thread recall after K2.** SPEC §3.4 in-context exclusion leaf, first sentence →
"An intra-thread match is recorded as a match-fire and not fetched: nothing yet delivers a
thread's earlier excerpts into the composed request (not built, ROADMAP "Intra-thread recall finds
but does not deliver"). Content already in the composed request is not added a second time."
Also: `recall-candidates.md` "for an embedding- or intra-thread-scored one" → "for an
embedding-scored one"; `recall-candidates.md` "An intra-thread hit is exempt … the in-context
exclusion below governs it" → "An intra-thread match is not fetched (in-context exclusion,
below)"; §2.6.1 `recall.cosine-auto-threshold` comment "(embedding AND intra-thread …)" → drop the
intra-thread half. ARCHITECTURE "Recall paths" item 4: state that intra-thread matches are logged
and not fetched, and drop the recall-mark mechanism.

**D5. SPEC §4.5.7 order.** "first hit wins: 1. Directory path match. 2. Create. 3. No directory.
When no directory is given, `prj_default`." → "No directory given: `prj_default`. Otherwise:
1. the project whose directory it is; 2. else a new project for it."

**D6. Rolled-back turns.**
- SPEC §4.5.8 (10): "is never re-ingested." → "is never re-ingested; the record that tells a
  retraction rollback from a crash rollback is not built (ROADMAP A8)." (keep its §4.3.3 link).
- ARCHITECTURE "Start order and resume": "Reconcile never re-ingests a turn rolled back by
  retraction; no record yet distinguishes a retraction rollback from a crash rollback (ROADMAP A8)."
  → "Reconcile must never re-ingest a turn rolled back by retraction; the record that distinguishes
  it from a crash rollback is not built (ROADMAP A8)."
- ARCHITECTURE "Event log format", `system` row: `turn-aborted` is "a turn rolled back before its
  first authoritative write (retraction or provider error): `turn=`, `bytes=`" (K4). SPEC §4.3.3:
  if it states other fields for that line, align them to `turn=`, `bytes=`.

**D7. Recovery cases rows 4 and 6.** ARCHITECTURE "Recovery cases": "preserve and surface the
journal" → "preserve the journal and report it (`recovery.journal-recovered`)".

**D8. Unbuilt CLI verbs and init behaviour.** SPEC §4.1: after the `search`, `deps` and `log`
entries add "(not built, ROADMAP A12)". SPEC §8.1 index, the config-template sentence, and
`8.1-…/opencode-settings.md`: add "(not built, ROADMAP A12)". README: "(`init`, `index`, `verify`,
`archive`, `version`; [`SPEC.md`](SPEC.md) §4.1)" → "(`init`, `index`, `verify`, `archive`,
`version`; the full command surface: [`SPEC.md`](SPEC.md) §4.1)".

**D9. README.** "Go 1.26.1+ (`go.mod`)" → "Go 1.27.3+ (`go.mod`)". Quick Start: between `just
build` and `./bin/personant version`, add `./bin/personant init      # creates the memory store at
~/.personant/`. "the memory home" → "the store directory".

**D10. SPEC §3.4 "Recall candidates".** "the fetch path every Layer-B fetch takes" → "the fetch
path every fetch into the active set (§3.1) takes".

**D11. Format 4 and leftovers.** ARCHITECTURE "Mechanism names": the restamp clause becomes "1→2,
2→3 and 3→4 change only the format revision: … revision 4 drops `remote_urls`,
`historical_remote_urls` and `historical_root_paths` from `meta.json`". "Store directory layout",
after the type table: "A `last-active` or `history` file left by an older store is not managed; it
is untracked content."

**D12. Vocabulary.**
- SPEC §2.2: "recall-fire count (the number of accepted recalls" → "(the number of automatic
  fetches".
- ARCHITECTURE "Symbol source": both "turn-close acceptance" phrases → "automatic turn-close
  fetch".
- ARCHITECTURE "Closing a thread": "the single closure write" → "the single close write".
- ARCHITECTURE "Invariant for the production phase split": "recall-surfaced marks" → "recall
  marks".
- ARCHITECTURE "Recall paths": path 2 "Opportunistic surfacing" → "Automatic fetch". Elsewhere
  "opportunistic" for recall (SPEC §2.6.1, the §9 index, "Open gate requirements", ROADMAP) →
  "automatic".
- SPEC §2.2.1: "no longer entered" → "never entered".
- Event-log example `home-format=1` → `home-format=4`.

**D13. SPEC §3.5 mechanism.** Move "rides the turn transaction already open … adds no commit path
of its own" to ARCHITECTURE "Closing a thread"; SPEC keeps "A close is part of its turn: both land
or neither (§4.5.8)."

**D14. ARCHITECTURE "Integration shape".** "Any memory tool registers through `tool.transform`" →
"If U3 adds a recall tool, it registers through `tool.transform`".

**D15. Checkpoints and events.**
- ARCHITECTURE "Turn transaction and journal": "creating a project takes a checkpoint" → "the
  plugin route's service takes a checkpoint after creating a project (not built)".
- ARCHITECTURE "Start order and resume": add "At a clean service-run stop the service takes a
  checkpoint (SPEC §3.11)."
- ARCHITECTURE "Event log format": `session.started`, `session.ended`, `session.checkpoint-error`
  → append "(the plugin route's service; not built)". `thread.renamed`: delete (nothing renames a
  thread). `thread.state-change`: `git grep` its producer; delete it if none. Reserved names:
  delete `project.meta-updated`. "Concurrent sessions": "`session.started active=` is logged once
  per service run" → "the service will log `session.started active=` once per service run (not
  built)".
- ARCHITECTURE `mechanisms/memory-vetting.md`: "(`DirectiveParam` only reads)" → "(no port
  operation reads or writes directive memory)".
- ARCHITECTURE "File layout": `harness_resolvers.go` → `harness_invariants.go` (K12).

**D16. ROADMAP.**
- U12: append "THESIS "What is new" item 2 and SPEC §1.1 ("by the problem it concerns, never by
  session") change with A10."
- U3: the sentence saying the topic tag's thread list has no job → "Under U12, sessions will set
  thread boundaries, so the topic tag's thread list will have no job."
- Delete the Near-term "Banded/auto-mode sim rung" item (superseded: the mock gate runs automatic
  closing and recall).
- B1: keep it checked; replace "Plan: `ACTIVE_PLAN/entry-point.md` (WP1 and WP2 done)." with
  "Done."
- U4: keep its title; body → "Personant registers no command in opencode and asks the user
  nothing; memory work runs automatically (SPEC §4)." U5 and U6: drop their removal inventories
  and any "Follow-up pass" line, keeping the decision sentence. U8: drop its "Follow-up pass" line.
- "Directive-file plumbing": drop `staging.window-turns` from the key list.
- B12: the quoted "the LLM never touches the home's git tree" → "the service is the only writer of
  the store directory (ARCHITECTURE "Automatic vs model-controlled")".
- A10: add SPEC §3.0.4 and §3.3 to its SPEC list.
- Vocabulary: "offer a recovery-fetch" → "fetch on restore"; "harness home", "test home",
  "existing home", "creates a home" → "…store directory"; "primary HEAD" → "history repository
  HEAD". "data home" (opencode's) stays.

**D17. Done-line.** `just docdicer-check PROJECT_DOCS "PROJECT_DOCS *.md ROADMAP_PLANS/*.md"`
passes; `git grep -n -E 'Layer-B fetch|before a recall is offered|accepted recalls|Opportunistic surfacing|DirectiveParam|harness_resolvers' -- PROJECT_DOCS README.md ROADMAP.md`
→ empty.

## Not applied (settled by the record)

- **U12 against THESIS "organized by problem, not by session"**: U12 is the strip plan's stated
  outcome for its follow-on (A10); THESIS and SPEC §1.1 change with A10's code (D16 records it).
- **README "Why" and section order**: placed there on owner direction during the doc sweep.
- **SPEC §1.8 manual acceptance**: a user-configurable policy for an unbuilt review (ROADMAP B10),
  outside a session; not an in-session prompt.
- **Project creation checkpoint in the resolver**: WP9 puts it in the plugin route's service; D15
  states that.
- **The cosine surface floor's value** (SPEC §3.4): stating 0.55 adds a requirement.
