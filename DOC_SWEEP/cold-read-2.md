# Round-2 cold read: triage (package X1)

Readers `adjagent:architect` (AS = SPEC alone, AV = THESIS+SPEC+ARCHITECTURE) and
`adjagent:go-coder` (GS, GV), on `DOC_SWEEP/` at `c71f2b6`. Answers:
`cold-read-answers/*-round2.md`; items are cited by reader and number, AV by its heading. Only a
reach past an "is not", a contradiction or wrong-document content counts.

Question 1 and 2 answers (AS, GS, AV, GV) describe the system as the THESIS building blocks do; no
reader named a part as a novel mechanism. No reach past an "is not" was reported.

## Settled by the record (no change)

- **Strip material** (the strip plan cuts it): ack modes and user involvement (AS 27, GS 24);
  routine close-out, exception queue and its `turns=N` reason (AS 4, 6; GS 9, 16; AV "Exception
  criterion"; GV 4); `blocked` "user confirms" (AS 3, GS 10); project resolution §4.5.3 vs §4.5.7
  (AS 10, GS 11; strip WP9); `/project`-keyed directive wording rides WP9 except as R2-C18 below.
- **Recorded as ROADMAP items or stated departures** (ARCHITECTURE flags them, and the record
  holds the fix): tracked-file store in the history repository (AS 9, GS 25, AV, GV 22; ROADMAP
  Near-term); closing-summary and anchor length (AV, GV 3, 30; ROADMAP Near-term); turn counter
  across a relaunch (AV, GV 7, 8; ROADMAP U9); `.recall-cache/` and excerpt-bundle legibility
  (AV, GV 19, 20; stated departures, A1-1, A1-4); config location and `providers.toml` (AV, GV
  23; ROADMAP A12); missing CLI verbs (GV 24; A12); the in-process model client (AV, GV 1; strip
  plan contract row 14, A1-10); the prototype's formatted `read` output (AV; stated); budget
  demotion not performed in v0.1 (AS 15, GS 7; SPEC flags it); mid-turn prompt (AS 18; Open).
- **Session titles are a second exception to "uses opencode's choice"** (GV 2): settled by CT64 —
  a title request is a model request, inside context management.
- **Judgment calls dismissed:** THESIS "not a network server" vs loopback HTTP, and the shell
  reaching the store (AV "weaker tensions"; ROADMAP B12); "composed, not replayed" vs the
  transcript tail (GV 25; the tail is composed into the request, not opencode's replay).

## Reopened: package C (contradictions), for these findings only

Each needs the C1 treatment — which side the code matches, with `git grep` evidence — and a
resolution; C2 applies them.

| Id | Finding | Readers |
|---|---|---|
| R2-C1 | Standalone archival (`op=archival` outside a rollover) vs SPEC §3.8 "only at the day rollover" and ARCHITECTURE's own "fires only at the rollover's archival step" | AV, GV 11 |
| R2-C2 | SPEC §3.11 checkpoint after a mid-run structural change, full tree, vs ARCHITECTURE "structural changes ride the per-turn commit" (scoped); SPEC §4.5.8 (2) hand-edit timing | AS 5, GS 2, AV, GV 9 |
| R2-C3 | SPEC §4.5.8 (4) debris quarantined before removal vs ARCHITECTURE case 4 (tracked bytes only) and "Start order" | AV, GV 12 |
| R2-C4 | Derived freshness at the day-commit (SPEC §3.11, §8.3) vs ARCHITECTURE step order (commit, then rebuild, then assert) and the capture commit's checks; whether derived files are in the history repository | GV 10 |
| R2-C5 | `.git/` filed operational vs the history repository being irreplaceable and holding archived threads | AV, GV 18 |
| R2-C6 | ARCHITECTURE `IngestModelResponse` "tag strip" vs U3 Open and SPEC §5.1.1 "no mechanism" | AV |
| R2-C7 | SPEC §5.1.2 accepts unclosed `*new-topic` vs ARCHITECTURE validation pattern | GV 6 |
| R2-C8 | SPEC §2.8 opening-line contract vs `BeginTurn` work logged before `user.prompt` | GV 14 |
| R2-C9 | SPEC §9.4 one declared scoring exception vs the archive-index read in recall fidelity | GV 15 |
| R2-C10 | `defaults.md` read-only (ARCHITECTURE layout) vs hand-editable (storage table, SPEC §2.6) | GV 16 |
| R2-C11 | ARCHITECTURE "Key abstractions" pure dominance order vs "Symbol source" most-frequent with tie-break | GV 17 |
| R2-C12 | ARCHITECTURE "`anchors` is not a separately stored set" vs stored in the record (A1-3); `[derived]` label vs not regenerated | AV, GV 31 |
| R2-C13 | Registry record "changes only when its anchor set does" (SPEC §3.2, ARCHITECTURE) vs per-turn `turn_count`, `last_engaged`, `last_engaged_turn` | GS 6, GV 28 |
| R2-C14 | Projection ranking: class vs count primary | GV 29 |
| R2-C15 | ARCHITECTURE tense: "Cold storage archival (implemented…)", the baselines cited as present vs "ROADMAP A2" | GV 32, 33 |
| R2-C16 | "Mechanism names": the loose-object check "runs gc" vs "Memory consolidation": it runs `Consolidate` | GV 34 |
| R2-C17 | Restore fallback "first parent" vs "Archive index": first parent picks the wrong lineage on a merge | GV 35 |
| R2-C18 | SPEC §2.6 directive files keyed by "project name" vs `directives/prj_<n>/project.md` | AS 26 |
| R2-C19 | Examples: `(3,2)-torus knot` normalization, `[WIP]` display vs lowercase states, bootstrap `home-format=1` | GV 37 |
| R2-C20 | SPEC §3.0.3 "Budget check runs last" vs step 5 logging | AS 1 |
| R2-C21 | SPEC §2.2.1 "engaged (referenced by a turn's symbols or by its topic tag)" vs §3.0.2 step 2 (topic tag) | AS 2 |
| R2-C22 | Hand-edit "never reverted / never reset" vs quarantine-then-reset (SPEC §2.1, §4.5.8 (2), (4)) | AS 7, GS 1 |
| R2-C23 | Torn-turn bytes "never replayed into memory" (§4.5.8 (3)) vs re-ingesting turns opencode kept (§1.6, §4.5.8 (10)) | AS 8, GS 4 |
| R2-C24 | Durability bar "at most one turn" vs §4.5.8 (7) history-repository loss and journal-write failure | GS 5 |
| R2-C25 | Diff threshold of "the new content's size" (§2.6.1, §3.9.1) vs "the file's current size" (§3.9.2) | AS 12, GS 19 |
| R2-C26 | §3.1 "within its slot budgets is within the ceiling" vs scaffolding charged to no slot and no tool-definition slot | AS 11 |
| R2-C27 | Slot count: "five memory slots and the three above" vs "live turn" as a dynamic slot; "project conventions" as a fixed slot inside directives | AS 17, GS 21 |
| R2-C28 | §3.5 decay "`last_engaged` exceeds `engagement.decay-turns`" vs `last_engaged` a timestamp | AS 20 |
| R2-C29 | §3.3 heading "three passes" vs pass 3 "contributes no symbols" | AS 21, GS 22 |
| R2-C30 | §3.3 binding-required "(buffered §3.9 file edits)" vs a read turn being binding-required; ARCHITECTURE calls it open calibration | AS 22, GV 5 |
| R2-C31 | §2.3 "Appending a turn writes one small file" vs file count not growing | AS 24, GS 14, AV, GV 21 |
| R2-C32 | §3.9 "opencode's stored transcript is never rewritten" vs §1.7 database cleanup | AS 28 |
| R2-C33 | §3.4 auto-fetch through the same fetch path vs cross-project fetch declined (§3.1); `recall.cross-project-threshold`, `wrong-project` | AS 19, GS 12 |
| R2-C34 | §3.1 tool-output cap "configurable" vs §2.6.1 only ack-mode parameters honored | AS 16 |
| R2-C35 | SPEC §4.5.8 (5) a lifecycle tag for every event vs gap-day events untagged (M67's outcome sentence dropped the gap-day exception) | AV, GV 13 |
| R2-C36 | SPEC §4.3.3 "as if the turn never happened" vs the event log (authoritative) already holding the `user.prompt` delta's lines | AV |
| R2-C37 | ARCHITECTURE "no model request reaches the provider uncomposed" vs the prototype's fail-open (U7 open) | AV |
| R2-C38 | SPEC §3.4 embedding similarity "the primary recall scan" vs ARCHITECTURE "Recall paths" naming no inter-thread embedding path | GV 26 |
| R2-C39 | SPEC §1.8 threads protected by batch content review in consolidation vs that review not built | GV 27 |
| R2-C40 | Minor wording: §3.9.4 heading "content-id-only is insufficient" vs its body; content-id line "content id" vs "content-id or commit-hash prefix"; ~365 commits plus archive commits; digest "current" vs derived lag between rollovers; history hard cap vs flat history as a sim obligation; refuse-to-open vs an entry that "stays refused"; "daily-rotated" event log vs rotation not built | AS 23, GS 13, 15, 17, 18, 20, 23, GV 36 |

No item reopens N, S, M or A on its own; R2-C35 corrects an M2 outcome sentence and is handled
here with the rest.
