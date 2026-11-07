# Process notes (rough)

Personant's capture for the same fold-in as `/Users/agent-user/projects/adjagent/DOC_SWEEP/process-notes.md`,
which is the reference for how this sweep runs. Only what this run adds is here: new principles,
and new cases for principles stated there. Rough on purpose: principle, why, the case that taught
it, open questions.

## Principles

### Describe each piece as what it is most like (additions)

- An "is not" must not name the "is" column's own pattern. Where a piece is a small, scoped version
  of a larger system, the fence names what makes the larger system larger (external consumers,
  further media), and the pattern side carries the scoping word (case: Archival read "tiered
  storage" on both sides; it became "a local, internal tiered storage and retrieval scheme" against
  "a backup system or external tiered storage").
- Case for "an is not must not rule out something the system really does": the memory store's
  "not an archive of the user's work" read as "user work is referenced by path and hash, never
  copied", which forbids the tracked-file store that renders file content. Became "not a mirror of
  the user's repository: the project's files and history stay in its own version control".
- A kind of thing THESIS names needs a building-block row, or a line saying it has none yet (case:
  career memory was defined under "Three kinds of memory" with no row; the architect's cold read
  flagged it).
- A rule shared across projects that use the table goes to the shared agent set, not into a
  THESIS or a project's CONVENTIONS (case: "a design that departs from the standard pattern states
  why, in ARCHITECTURE" is process in a THESIS; suggested for the architect definition in
  `/Users/agent-user/projects/adjagent/ARCHITECT_RULE_SUGGESTION.md`).

### Vocabulary first (additions)

- The volumes below THESIS are part of the problem being fixed: a THESIS piece is never named from
  their current lexicon. Naming cascades forward from THESIS; a volume may later coin a narrower
  term, tied to the THESIS term, and coinages change when a more specific need appears (case:
  SPEC's "deep cold archival" was rejected as the THESIS name in favour of "Cold storage").
- Why: THESIS is the first thing an agent reads; it should feel at home in the implementation
  space before it meets any project-specific word.

### Each volume's job (additions)

- Motivation written for a human reader ("an assistant without a past cannot be a partner") goes
  to README, not to THESIS. Test for keeping a THESIS passage: does it change what a building
  agent decides?
- A project's ethos about how the agent's own instructions may change is not product behavior.
  The product ships defaults and imposes none; the user chooses by tuning their agent
  instructions (case: THESIS "behavior and process change only deliberately" was cut, and SPEC
  §1.8's single write path became a review whose running, acceptance and accrual routing are user
  policy).
- Retired volume text that THESIS's "does not" block now covers goes (case: CONVENTIONS
  "Anti-reaccretion doctrine").

### Cold reads (additions)

- Do not point the brief at the "is not" column: the test is whether the document holds the reader
  by itself, not whether the reader follows an instruction to watch it.
- Case for contamination: the architect cited the old THESIS's "behavior and process change only
  deliberately", which the draft had cut; the coder used topic tags, which the draft never names
  (SPEC's contents list was preloaded).
- "Disregard what is preloaded" does not work: a model cannot unsee its context. Pasting the
  document into the brief and asking the reader to mark every claim resting on anything else made
  leaks visible; it did not remove them.
- A truly cold read, under the scratch-space rule: run from a sibling of the repository
  (`../personant-cold-read/`) holding only a copy of `.claude/`, so the agent set loads and no
  project `CLAUDE.md` or `AGENTS.md` does; `claude -p --tools "" --agent <agent>` with the brief on
  stdin, so the reader cannot open repository files by absolute path either. `DOC_SWEEP/cold-read.sh`
  runs it. A world-writable directory (`/var/tmp`) is unsafe: any local user can plant a
  `CLAUDE.md` there or above it.

## Open questions

- adjagent's "How a truly cold read is run under the scratch-space rule" has a candidate answer
  above (*Cold reads*); not yet exercised end to end.

- This run asked the cold readers "the first three things you would design / build", at the
  owner's direction, with the signal filtered to "is not" crossings. adjagent's notes say never to
  ask that. Both runs filtered the same way; which question the guide prescribes is open.
