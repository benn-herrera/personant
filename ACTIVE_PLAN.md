# Doc sweep: content passes and redice

**Status:** active plan, draft; awaiting `adjagent:prompt-engineer` usability review, then
`adjagent:architect` revision. No package is dispatched until both are done.

**Base commit:** `2167007` (`git rev-parse HEAD` when this plan was written).

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
belongs in, or cut as framing; a change to what it requires is reported, never applied. The one
standing exception is strip material (next section).

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
    other-kind findings held for these passes);
  - `spec-cold-read.md` and `architecture-cold-read.md` (cold-read triage; what is settled and
    what work each finding leaves);
  - `process-notes.md` (how the sweep runs).
- Cold-read answers in `.claude-temp/cold-read/` are scratch. A package that needs one of their
  findings cites it through the triage files above, which quote what was ruled.

## Gates

- No runner target checks a `DOC_SWEEP/` monolith. A monolith package's done-line is its stated
  greps plus the dispatcher's diff audit; this is a gap the plan accepts, not a substitute for a
  target.
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
  - Returns findings N1, N2, … with file, heading, quote, category, and a plain restatement that
    keeps every requirement the passage states.
  - **Done:** the dispatcher writes `DOC_SWEEP/novelty-report.md` and records each finding as
    settled by the record (with where) or put to the owner; the owner's rulings are recorded under
    each finding put to them.
- **N2, apply** (mechanical; `adjagent:tech-writer`, sonnet).
  - Applies each accepted or amended restatement verbatim; a restatement that would change a
    requirement is reported, not applied.
  - **Done:** every accepted finding applied, each named by its N-number in the commit message.

### C: Contradictions

- **C1, check** (review; `adjagent:architect`, own model).
  - Inputs, each a list of contradictions already found:
    - `spec-cold-read.md` "Held for the contradictions pass" (the architect's S1–S36);
    - `architecture-cold-read.md` "Held for later passes";
    - section 5 of both vocabulary files.
  - For each contradiction: which side the code matches, with the file and line that shows it
    (`git grep` and the command in the finding), and which side moved last (`git log -1
    --format=%h %ad -- <path>` on the code and on the document leaf in `PROJECT_DOCS/`).
  - Classifies each as: (1) the document is stale and the code holds the project's principles —
    the document moves; (2) the code is newer and violates a principle — the code is wrong, a
    ROADMAP known-defect item; (3) two documents disagree and the code settles neither — owner
    call; (4) strip material — cut.
  - **Done:** the dispatcher writes `DOC_SWEEP/contradictions.md`, settled items marked with their
    evidence, owner calls ruled.
- **C2, apply** (expert; `adjagent:tech-writer`, own model). Applies class 1 and 4 resolutions
  and the owner's class 3 rulings to the monoliths; drafts each class 2 item as a ROADMAP line
  stating the fact and where it lives, which the dispatcher adds to `ROADMAP.md`.
  - **Done:** every item in `contradictions.md` marked applied, ROADMAP'd or cut.

### S: SPEC stands alone

- **S1, apply** (expert; `adjagent:tech-writer`, own model), SPEC only.
  - Every term SPEC uses is defined in SPEC at first use, or is a term of art used as the field
    uses it. Inputs: section 4 of `spec-vocabulary.md` and the undefined-term lists the cold reads
    produced (through `spec-cold-read.md`).
  - No requirement is stated only by pointer. Each "Unused → ARCHITECTURE «…»" stub keeps its
    section number and states the outcome its consumer observes, with the ARCHITECTURE pointer
    kept for the mechanism; each placeholder heading says in one line what replaced it.
  - SPEC cites THESIS for no requirement.
  - SPEC states what a day is (a UTC calendar day, "Decided at planning") where the day rollover
    is first defined.
  - **Done:** `grep -n 'Unused →' DOC_SWEEP/SPEC.md` lines each followed by an outcome sentence;
    no term from the undefined-term lists remains undefined; the dispatcher's diff audit finds no
    requirement added or dropped.

### M: Mechanism out of SPEC

- **M1, map** (review; `adjagent:architect`, own model). Lists every SPEC passage that states
  mechanism: code symbols, file and field names beyond the user-visible schema, regexes, internal
  layout, step-by-step procedures, test and tool names. Known instances:
  - §4.5.8's recovery cases, trailers, rollover steps and named rules;
  - §3.8.1's `archive/index.jsonl` record;
  - the regex in §5.1;
  - internal field names such as `anchors_projected_at_turn`.
  
  For each, it names the outcome SPEC keeps and the ARCHITECTURE section that does or should
  hold the mechanism.
- **M2, apply** (expert; `adjagent:tech-writer`, own model). Moves each mechanism passage to its
  ARCHITECTURE section, or cuts it where ARCHITECTURE already states it, and leaves SPEC the
  outcome sentence.
  - **Done:** every M1 item applied; the dispatcher's diff audit finds no requirement lost in the
    move.

### A: ARCHITECTURE content

- **A1, apply** (expert; `adjagent:architect` drafts, `adjagent:tech-writer` applies, each own
  model).
  - Each unstated departure in `architecture-cold-read.md` "Unstated departures" is either stated
    with its reason in the section covering the piece, or listed as a ROADMAP item where the
    design should conform instead (owner call where the record does not say which).
  - ARCHITECTURE holds no future intent: the "Boundary" fallback sentence (the Go port of
    opencode's server) leaves; other future-tense passages A1 finds move to ROADMAP if not
    already there.
  - User-facing outcomes in ARCHITECTURE (the alone-reads' question 4, through
    `architecture-cold-read.md`) move to SPEC if SPEC lacks them, otherwise are cut.
  - ARCHITECTURE gains a short section saying career memory is threads plus recall (THESIS
    Career memory row), or states that no mechanism is built.
  - "The model drafts, deterministic code writes" names the validation each model judgment gets,
    or states which ones have none.
  - **Done:** every item in "Unstated departures" and every A1 finding marked applied or
    ROADMAP'd.

### X: Cold read of the set

- **X1** (review). `DOC_SWEEP/cold-read.sh`, `adjagent:architect` and `adjagent:go-coder`:
  - SPEC alone;
  - THESIS + SPEC + ARCHITECTURE.
  
  Orientation questions only, as in `.claude-temp/cold-read/*-questions-*.md`.
- The dispatcher triages against the record first (`process-notes.md`). Only a reach past an
  "is not", a contradiction, or content in the wrong document counts.
- **Done:** triage written to `DOC_SWEEP/cold-read-2.md`; a finding that needs a change reopens the
  package that owns it (N, C, S, M or A) for that finding only.

### R: Redice

- **R1** (mechanical; dispatcher, runner targets).
  - For each of SPEC, ARCHITECTURE and CONVENTIONS: `git rm -r -q PROJECT_DOCS/<VOLUME>`, then
    `just docdicer dice-doc DOC_SWEEP/<VOLUME>.md PROJECT_DOCS/<VOLUME>`.
  - `DOC_SWEEP/THESIS.md` replaces `PROJECT_DOCS/entry-point.md`.
  - Dice in place, never into scratch: dicing elsewhere writes wrong cross-volume link depths.
  - `dice-doc`'s own reassembly and link checks must pass.
- **R2, citations** (mechanical; `adjagent:tech-writer` for prose files, `adjagent:go-coder` for the
  test).
  - Every path that cites a leaf resolves after the redice. As of the base commit,
    `git grep -l 'PROJECT_DOCS/' -- ':!PROJECT_DOCS' ':!DOC_SWEEP'` lists:
    - the parked strip plan, `ROADMAP_PLANS/strip-self-built-harness/`: `2-inventory-contract.md`,
      `4-work-packages/wp10-closing-sweep-mechanical.md`,
      `4-work-packages/wp2-retire-the-front-end-version-line-mechanical.md`, `5-sequencing.md`,
      `outcome-acceptance-criteria.md`;
    - `AGENTS.md`, `ARCHITECTURE.md`, `CONVENTIONS.md`, `SPEC.md`, `THESIS.md`, `ROADMAP.md`;
    - `ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md`, `ROADMAP_PLANS/folio-deferred-shrink.md`;
    - `internal/turn/chain_taskclass_test.go`.
  - **The test.** `TestEventTaxonomyHasNoUnroutedEvents` reads
    `PROJECT_DOCS/SPEC/3-core-algorithms/3.0-context-modification-events-the-architectural-primitive/3.0.1-the-event-taxonomy.md`
    (line 122). The §3.0 and §3.0.1 headings are now "Deltas (the architectural primitive)" and
    "The delta taxonomy", so the leaf path changes. Updating the path is in scope; the test's
    assertions (backticked event names as table rows) are not, and a failure on them is reported.
  - The strip plan's citations are re-mapped when it resumes ("Landing"), not here; R2 lists them
    for that step.
  - `AGENTS.md`'s `@PROJECT_DOCS/…/entry-point.md` include lines (lines 11–17) still name each
    volume's entry point.
- **Done:** `just docdicer-check` and `just test` pass; R2's grep resolves every listed citation
  or lists it for the strip resumption; `DOC_SWEEP/` deleted except `DOC_SWEEP/process-notes.md`
  ("Decided at planning").

### P: Post-plan review

- **P1** (review). Reviewers: `adjagent:tech-writer` (documents) and `adjagent:prompt-engineer`
  (model-facing text: `AGENTS.md`, the documents as agents read them). They get the contract
  documents and `git diff 2167007..HEAD -- PROJECT_DOCS README.md AGENTS.md ROADMAP.md internal`,
  not this plan; they check that no requirement moved except by a recorded ruling, and that the
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

Each package commits on completion. The commit message names the rulings it applied.

| Package | State |
|---|---|
| N1, N2 | not started |
| C1, C2 | not started |
| S1 | not started |
| M1, M2 | not started |
| A1 | not started |
| X1 | not started |
| R1, R2 | not started |
| P1 | not started |

## Landing

When P1's burndown is done, this plan is deleted and the strip plan
(`ROADMAP_PLANS/strip-self-built-harness/`) becomes the active plan again, in the same change,
with its section-2 rows re-mapped onto the rediced tree before WP3 is dispatched.

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
  it where the day rollover is first defined (package S1).

## Questions for the owner

None.
