# Doc sweep: content passes and redice

**Status:** active plan; `adjagent:prompt-engineer` usability review done and `adjagent:architect`
revision applied. Ready for dispatch.

**Base commit:** `e0854c2` (`git rev-parse HEAD` when R2's citation grep was last run).

## What this plan does

The project documents describe each piece of Personant as the ordinary thing it is most like,
name what it is not, and keep each kind of content in its own document. The vocabulary of all four
documents is settled and applied (`DOC_SWEEP/spec-vocabulary.md`,
`DOC_SWEEP/architecture-vocabulary.md`). This plan takes the documents the rest of the way:

1. novelty language goes, except for what THESIS "What is new" lists;
2. contradictions inside and between the documents are resolved against the code;
3. SPEC stands alone: every term it uses is defined in it, and it sends no reader elsewhere for a
   requirement;
4. mechanism leaves SPEC for ARCHITECTURE;
5. ARCHITECTURE states each departure from a THESIS standard pattern with its reason, holds no
   future intent and no user-facing outcomes;
6. the revised monoliths are rediced over `PROJECT_DOCS/`, every citation of a leaf path follows,
   and `DOC_SWEEP/` is removed.

**Invariant for every package:** outside the THESIS, no behavior, rule or requirement is added or
dropped unless an owner ruling says so. A sentence is restated, moved to the document its content
belongs in, or cut as framing; a change to what it requires is reported, never applied. The
standing exceptions:
- strip material (next section);
- a C1 class-1 resolution settled by the record: where the code is newer than the document and
  holds the project's principles, the document is stale and moves to match the code, with the
  evidence recorded in `DOC_SWEEP/contradictions.md`.

**Strip material.** Contract text that the parked strip plan
(`ROADMAP_PLANS/strip-self-built-harness/`, "Strip the self-built harness") deletes is cut when a
package meets it, in as contained a change as possible; nobody hunts for it
(`DOC_SWEEP/process-notes.md`, "Sweeping alongside a removal plan"). What the strip deletes is
stated in `ROADMAP_PLANS/strip-self-built-harness/what-it-does.md`.

## Working copy

- `DOC_SWEEP/THESIS.md`, `SPEC.md`, `ARCHITECTURE.md`, `CONVENTIONS.md`: the monoliths every
  package edits. `PROJECT_DOCS/` is not edited until package R.
- Links in the monoliths are as `docdicer undice` wrote them. Nothing checks them before R.
- Rulings and triage the packages work from, all tracked under `DOC_SWEEP/`:
  - `spec-vocabulary.md` and `architecture-vocabulary.md` (vocabulary; section 5 of each lists
    other-kind findings held for these passes). Their line numbers resolve against the commit each
    file's header names: `spec-vocabulary.md` against `git show 022282b:DOC_SWEEP/SPEC.md`,
    `architecture-vocabulary.md` against `git show 3fb2084:DOC_SWEEP/ARCHITECTURE.md`. A package
    citing one of their findings cites the section heading it found the passage under, not the line.
  - `spec-cold-read.md` and `architecture-cold-read.md` (cold-read triage; what is settled and
    what work each finding leaves);
  - `process-notes.md` (how the sweep runs).
- Cold-read round 1, tracked under `DOC_SWEEP/cold-read-answers/`:
  - answers: `adjagent-{architect,go-coder}-{spec-alone,spec-with-thesis,arch-alone,arch-with-volumes}.md`;
  - question templates: `{spec,arch}-questions-*.md`.

  The triage files say what is settled; the answer files hold the findings. A package reads the
  answer-file section its inputs name, by heading.

## Gates

- No runner target checks a `DOC_SWEEP/` monolith. Monolith packages are exempt from the
  runner-target done-line ("Decided at planning"); each one's done-line is its stated observations
  plus the dispatcher's diff audit.
- Package R's done-line uses `just docdicer-check` and `just test`. `just test` is needed because R
  edits a Go test (below).

## Packages

Each package is one dispatch unless stated, runs alone (all of them edit the same monoliths), and
has lifetime **shipped**. "Review" packages report findings and edit nothing; the dispatcher
writes the findings file, the owner rules on what the written record does not settle, and the
following "apply" package executes the rulings.

### N: Novelty

- **N1, report** (review; `adjagent:prompt-engineer`, own model).
  - Reads THESIS (accepted), then SPEC, ARCHITECTURE, CONVENTIONS and `README.md`, each whole.
  - Flags a passage (one paragraph or list item) only when it:
    - (a) calls a piece new, unique or unlike others when the THESIS building-blocks table lists
      it as standard ("What is new" items are exempt);
    - (b) is the home section of a piece (the ARCHITECTURE section whose heading names it) and
      neither names nor cites the piece's THESIS row;
    - (c) proposes or invites extending a piece toward its "is not";
    - (d) is a heading that names a mechanism's trigger or internals instead of what it does
      (case: "Accept suppression (the intra-thread re-fire)" became "Recall skips content already
      in the request (in-context exclusion)").
  - Returns findings NV1, NV2, … with file, heading, quote, category, and a plain restatement that
    keeps every requirement the passage states.
  - **Done:** the dispatcher writes `DOC_SWEEP/novelty-report.md` and records each finding as
    settled by the record (with where) or put to the owner; the owner's rulings are recorded under
    each finding put to them.
- **N2, apply** (mechanical; `adjagent:tech-writer`, sonnet).
  - Applies each accepted or amended restatement verbatim; a restatement that would change a
    requirement is reported, not applied.
  - `README.md` findings are in scope: they are applied to `README.md` itself, which is not a
    monolith and is not rediced.
  - **Done:** every accepted finding applied, each named by its NV-number in the commit message.

### C: Contradictions

- **C1, check** (review; `adjagent:architect`, own model).
  - Inputs, each a list of contradictions already found:
    - architect spec-with-thesis S1–S36: `cold-read-answers/adjagent-architect-spec-with-thesis.md`
      "## 3. Contradictions", subsection "### SPEC against itself" (held by `spec-cold-read.md`
      "Held for the contradictions pass"; its "### SPEC against THESIS" is already triaged in
      `spec-cold-read.md` "SPEC against THESIS");
    - both readers' ARCHITECTURE contradictions (held by `architecture-cold-read.md` "Held for
      later passes"):
      - `adjagent-architect-arch-alone.md` "## 2. Self-contradictions";
      - `adjagent-go-coder-arch-alone.md` "## 2. Self-contradictions";
      - `adjagent-architect-arch-with-volumes.md` "## 2. Contradictions";
      - `adjagent-go-coder-arch-with-volumes.md` "## 2. Contradictions involving ARCH";
    - section 5 of both vocabulary files, line numbers resolved at the commits named under
      "Working copy".
  - For each contradiction: which side the code matches, with the file and line that shows it
    (`git grep`), and which side moved last (`git log -1 --format='%h %ad' -- <path>` on the code
    and on the document leaf in `PROJECT_DOCS/`).
  - Classifies each as: (1) the document is stale and the code holds the project's principles —
    the document moves; (2) the code is newer and violates a principle — the code is wrong, a
    ROADMAP "Near-term" item; (3) two documents disagree and the code settles neither — owner
    call; (4) strip material — cut.
  - **Done:** the dispatcher writes `DOC_SWEEP/contradictions.md`, each item citing its input by
    file and section heading, settled items marked with their evidence, owner calls ruled.
- **C2, apply** (expert; `adjagent:tech-writer`, own model). Applies class 1 and 4 resolutions
  and the owner's class 3 rulings to the monoliths; drafts each class 2 item as a ROADMAP line
  stating the fact and where it lives, which the dispatcher adds to `ROADMAP.md` "Near-term".
  - **Done:** every item in `contradictions.md` marked applied, ROADMAP'd or cut.

### S: SPEC stands alone

- **S1, apply** (expert; `adjagent:tech-writer`, own model), SPEC only.
  - Every term SPEC uses is defined in SPEC at first use, or is a term of art used as the field
    uses it. Inputs:
    - section 4 of `spec-vocabulary.md`;
    - the readers' undefined-term lists:
      - `adjagent-architect-spec-alone.md` "## 4. Terms the document does not define";
      - `adjagent-go-coder-spec-alone.md` "## 4. Terms the document does not define";
      - `adjagent-architect-spec-with-thesis.md` "## 5. Terms I could not understand";
      - `adjagent-go-coder-spec-with-thesis.md` "## 5. Terms I could not resolve from the two
        documents".
  - No requirement is stated only by pointer. Each "Unused → ARCHITECTURE «…»" section keeps its
    section number and, per CONVENTIONS "Moved sections", states the outcome only: what its
    consumer (the user, the model, or a person inspecting the store) observes or can rely on.
    Today the text after each marker states schema and layout instead (case: §2.2 "a single file
    sorted by id, so diffs stay line-grain: project, anchors, summary, …"). S1 writes the outcome
    sentence first after the marker; schema text that ARCHITECTURE «X» does not already hold stays
    for M. Each placeholder heading says in one line what replaced it.
  - SPEC cites THESIS for no requirement. `grep -n 'THESIS' DOC_SWEEP/SPEC.md` at the base commit
    returns one hit, line 124: "A lesson is filed under the kind of memory it changes (THESIS
    defines the kinds)". SPEC defines the kinds itself.
  - SPEC states what a day is (a UTC calendar day, "Decided at planning") in §4.5.8, where the day
    rollover is defined.
  - **Done:**
    - for each `grep -n 'Unused →' DOC_SWEEP/SPEC.md` hit, the dispatcher's audit names the
      consumer and the observable its first sentence states; a hit followed only by schema or
      layout fails;
    - each `grep -n 'THESIS' DOC_SWEEP/SPEC.md` hit is non-normative: removing it leaves every
      requirement stated;
    - no term from the input lists remains undefined;
    - the dispatcher's diff audit finds no requirement added or dropped.

### M: Mechanism out of SPEC

- **M1, map** (review; `adjagent:architect`, own model). Lists every SPEC passage that states
  mechanism: code symbols, file and field names beyond the user-visible schema, regexes, internal
  layout, step-by-step procedures, test and tool names. Known instances:
  - §4.5.8's recovery cases, trailers, rollover steps and named rules (the day definition S1 put
    there is outcome and stays);
  - §3.8.1's `archive/index.jsonl` record;
  - the regex in §5.1;
  - internal field names such as `anchors_projected_at_turn`.
  
  For each, it names the outcome SPEC keeps and the ARCHITECTURE section that does or should
  hold the mechanism.
  - **Done:** the dispatcher writes `DOC_SWEEP/mechanism-map.md`, one item per passage with the
    fields SPEC quote, outcome kept, ARCHITECTURE target section.
- **M2, apply** (expert; `adjagent:tech-writer`, own model). Moves each mechanism passage to its
  ARCHITECTURE section, or cuts it where ARCHITECTURE already states it, and leaves SPEC the
  outcome sentence.
  - **Done:** every item in `mechanism-map.md` marked applied; the dispatcher's diff audit finds no
    requirement lost in the move.

### A: ARCHITECTURE content

- **A1, review** (review; `adjagent:architect`, own model). Returns findings on:
  - each unstated departure in `architecture-cold-read.md` "Unstated departures": state it with
    its reason in the section covering the piece, or list it as a ROADMAP item where the design
    should conform instead;
  - future intent in ARCHITECTURE. N2 already removes the "Boundary" fallback sentence, the
    Code Mode pointer and "Storage evolution" (`novelty-report.md` NV16, NV21); other
    future-tense passages A1 finds move to ROADMAP if not already there;
  - user-facing outcomes in ARCHITECTURE, from `adjagent-architect-arch-alone.md` "## 4. Passages
    stating user-facing behavior or outcomes" and `adjagent-go-coder-arch-alone.md` "## 4.
    Passages stating user-facing behaviors or outcomes": each moves to SPEC if SPEC lacks it,
    otherwise is cut;
  - career memory: ARCHITECTURE gains a short section saying it is threads plus recall (THESIS
    Career memory row), or states that no mechanism is built;
  - "The model drafts, deterministic code writes": names the validation each model judgment gets,
    or states which ones have none;
  - terms ARCHITECTURE uses without defining, from `adjagent-architect-arch-alone.md` "## 3. Terms
    not understood from the document", `adjagent-go-coder-arch-alone.md` "## 3. Terms not
    understood from the document", `adjagent-architect-arch-with-volumes.md` "## 5. Terms I could
    not resolve from the documents" and `adjagent-go-coder-arch-with-volumes.md` "## 5. ARCH terms
    not understandable from the three documents": each is defined at first use, cites the SPEC or
    THESIS definition, is a term of art used as the field uses it, no longer appears, or is
    replaced by the term the vocabulary files settle (cite where).
  - **Done:** the dispatcher writes `DOC_SWEEP/architecture-content.md`, each item settled by the
    record (with where) or put to the owner; owner calls ruled.
- **A2, apply** (expert; `adjagent:tech-writer`, own model). Applies every item in
  `architecture-content.md` to the monoliths; drafts each ROADMAP item as a line stating the fact
  and where it lives, which the dispatcher adds to `ROADMAP.md`.
  - **Done:** every item in `architecture-content.md` marked applied or ROADMAP'd.

### X: Cold read of the set

- **X1** (review). Readers `adjagent:architect` and `adjagent:go-coder`, two configurations, four
  runs. Questions kept are those that do not point the reader at the "is not" column or the
  THESIS standard patterns (`process-notes.md` "Cold reads (additions)", first bullet):
  - SPEC alone: `DOC_SWEEP/cold-read-answers/spec-questions-alone.md` questions 1–3; brief
    `.claude-temp/cold-read-round2/spec-alone.md` (the kept questions, then `=== SPEC ===` and
    `DOC_SWEEP/SPEC.md` in full);
  - THESIS + SPEC + ARCHITECTURE: `DOC_SWEEP/cold-read-answers/arch-questions-with-volumes.md`
    questions 1 and 2; brief `.claude-temp/cold-read-round2/arch-with-volumes.md` (the kept
    questions, then `=== THESIS ===`, `=== SPEC ===`, `=== ARCHITECTURE ===`, each monolith in
    full).
  - Before the runs, refresh the reader's agent set: `command rm -rf ../personant-cold-read/.claude`
    then `command cp -R .claude ../personant-cold-read/`.
  - Each run: `DOC_SWEEP/cold-read.sh <agent> <brief> <output>`, output
    `DOC_SWEEP/cold-read-answers/<agent>-<configuration>-round2.md` with the agent's `:` written
    as `-` (e.g. `adjagent-architect-spec-alone-round2.md`). The output argument is required: the
    default, `.claude-temp/cold-read/<agent>-<brief name>.md`, overwrites round 1's scratch copies
    and leaves round 2 untracked.
- The dispatcher triages against the record first (`process-notes.md`). Only a reach past an
  "is not", a contradiction, or content in the wrong document counts.
- **Done:** triage written to `DOC_SWEEP/cold-read-2.md`; a finding that needs a change reopens the
  package that owns it (N, C, S, M or A) for that finding only.

### R: Redice

- **R1** (mechanical; dispatcher, runner targets).
  - For each of SPEC, ARCHITECTURE and CONVENTIONS: `git rm -r -q PROJECT_DOCS/<VOLUME>`, then
    `just docdicer dice-doc DOC_SWEEP/<VOLUME>.md PROJECT_DOCS/<VOLUME>`.
  - Then `git mv -f DOC_SWEEP/THESIS.md PROJECT_DOCS/entry-point.md`. Order against the dice runs
    does not matter: no volume links to the root (`grep -o '([./]*entry-point.md[^)]*)'
    DOC_SWEEP/SPEC.md DOC_SWEEP/ARCHITECTURE.md DOC_SWEEP/CONVENTIONS.md` returns nothing at the
    base commit), and THESIS's own links (`SPEC/entry-point.md`, `ARCHITECTURE/entry-point.md`,
    `CONVENTIONS/entry-point.md`) are already relative to `PROJECT_DOCS/`.
  - Dice in place, never into scratch: dicing elsewhere writes wrong cross-volume link depths.
  - `dice-doc`'s own reassembly and link checks must pass.
- **R2, citations** (mechanical; `adjagent:tech-writer` for prose files, `adjagent:go-coder` for the
  test).
  - Every path that cites a leaf resolves after the redice. At `e0854c2`,
    `git grep -l 'PROJECT_DOCS/' -- ':!PROJECT_DOCS' ':!DOC_SWEEP'` returns:
    - `ACTIVE_PLAN.md` (out of scope: its paths describe the pre-redice tree, and it is deleted at
      Landing);
    - the parked strip plan, `ROADMAP_PLANS/strip-self-built-harness/`: `2-inventory-contract.md`,
      `4-work-packages/wp10-closing-sweep-mechanical.md`,
      `4-work-packages/wp2-retire-the-front-end-version-line-mechanical.md`, `5-sequencing.md`,
      `entry-point.md`, `outcome-acceptance-criteria.md`;
    - `AGENTS.md`, `ARCHITECTURE.md`, `CONVENTIONS.md`, `SPEC.md`, `THESIS.md`, `ROADMAP.md`;
    - `ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md`, `ROADMAP_PLANS/folio-deferred-shrink.md`;
    - `internal/turn/chain_taskclass_test.go`.
  - **The test.** `TestEventTaxonomyHasNoUnroutedEvents` reads
    `PROJECT_DOCS/SPEC/3-core-algorithms/3.0-context-modification-events-the-architectural-primitive/3.0.1-the-event-taxonomy.md`
    (line 122). The §3.0 and §3.0.1 headings are now "Deltas (the architectural primitive)" and
    "The delta taxonomy", so the leaf path changes; the new path is the one R1's dice output wrote
    for §3.0.1. Updating the path is in scope; the test's assertions (backticked event names as
    table rows) are not, and a failure on them is reported. Edit gate after the edit:
    `just test-run ./internal/turn TestEventTaxonomyHasNoUnroutedEvents`.
  - The strip plan's citations are re-mapped when it resumes ("Landing"), not here. R2 lists each
    strip-plan citation that no longer resolves in a "Citations to re-map at resumption" list at
    the end of `ROADMAP_PLANS/strip-self-built-harness/2-inventory-contract.md`.
  - `AGENTS.md`'s `@PROJECT_DOCS/…/entry-point.md` include lines (lines 11–17) still name each
    volume's entry point.
- **Done:** `just docdicer-check PROJECT_DOCS "PROJECT_DOCS *.md ROADMAP_PLANS/*.md"` and
  `just test` pass (the `ROADMAP_PLANS/*.md` glob takes the top-level roadmap plans and not the
  strip plan's subdirectory, whose citations wait for resumption); every citation in R2's list
  resolves or is in the strip plan's re-map list; `DOC_SWEEP/` deleted except
  `DOC_SWEEP/process-notes.md` ("Decided at planning").

### P: Post-plan review

- **P1** (review). Reviewers: `adjagent:tech-writer` (documents) and `adjagent:prompt-engineer`
  (model-facing text: `AGENTS.md`, the documents as agents read them). They get:
  - the contract documents;
  - `git diff 2167007..HEAD -- PROJECT_DOCS README.md AGENTS.md ROADMAP.md internal` (from
    `2167007`, before the base commit, so the planning commit's SPEC §3.4 and ROADMAP edits are
    reviewed too);
  - the ruling files R deletes, as of the parent of the R1 commit (`git show <R1>^:DOC_SWEEP/<file>`):
    `novelty-report.md`, `contradictions.md`, `mechanism-map.md`, `architecture-content.md`,
    `cold-read-2.md`, `spec-vocabulary.md`, `architecture-vocabulary.md`, `spec-cold-read.md`,
    `architecture-cold-read.md`.

  Not this plan. They check that no requirement moved except by a recorded ruling, and that the
  plain framing holds. They write nothing.
- The dispatcher writes their findings to the burndown list, which gets one
  `adjagent:prompt-engineer` usability review and the dispatcher's revision before any fix is
  dispatched.
- **Done:** the burndown committed, and every item applied or ROADMAP'd.

## Sequencing

N → C → S → M → A → X → R → P, serial.
- C before S and M: a contradiction resolved first is not moved twice.
- S before M: SPEC's outcome sentences exist before mechanism leaves around them.
- X after A: the set is read whole once every content pass has landed.
- R after X: nothing is rediced that a cold-read finding reopens.

Each package commits on completion. The commit message names the rulings it applied. The R1
commit's hash is recorded in the table below; P1 reads the ruling files at its parent.

| Package | State |
|---|---|
| N1, N2 | done (`af53921`) |
| C1, C2 | done |
| S1 | done |
| M1, M2 | done |
| A1, A2 | done |
| X1 | done (`DOC_SWEEP/cold-read-2.md`; reopened C applied, `contradictions-2.md`) |
| R1, R2 | done |
| P1 | done (burndown B1–B24 applied) |

## Landing

When P1's burndown is done, this plan is deleted and the strip plan
(`ROADMAP_PLANS/strip-self-built-harness/`) becomes the active plan again, in the same change,
with its section-2 rows re-mapped onto the rediced tree, and the "Citations to re-map at
resumption" list in `2-inventory-contract.md` worked off, before WP3 is dispatched.

## Decided at planning

- **Active plan.** This plan is the one active plan; the strip plan is parked in
  `ROADMAP_PLANS/strip-self-built-harness/` until this plan lands (above).
- **`process-notes.md`** survives R: it goes to adjagent for the `dev-process` guide. R deletes
  the rest of `DOC_SWEEP/` and leaves `DOC_SWEEP/process-notes.md` until the owner says it has
  been handed over.
- **Rendered strings and labels** (ENGAGED / PAUSED / DORMANT / RECENTLY CLOSED, the truncation
  string, user-facing dialog text) keep their current wording; the strip removes most of the flow
  control that shows them, and settles the rest.
- **In-context exclusion** (SPEC §3.4) is scoped to the composed request: content already in the
  request is not added a second time, whatever session or service run the request belongs to.

- **A day is a UTC calendar day**, as the code defines it (`internal/autogit/day.go`
  `DayIndexOf`: whole UTC days since the Unix epoch; no DST, stable across processes). SPEC states
  it in §4.5.8, where the day rollover is defined (package S1).
- **Monolith done-lines.** Monolith packages (N2, C2, S1, M2, A2) are exempt from the
  runner-target done-line. Requirement preservation cannot be checked by a machine; the dispatcher
  audits each package's diff for it, and P1 reviews the whole. Link and tree integrity are checked
  by `just docdicer-check` at R.

## Questions for the owner

None.
