# SPEC cold read 1: triage and rulings

Readers: adjagent:architect and adjagent:go-coder, each on SPEC alone and on THESIS + SPEC, run by
`DOC_SWEEP/cold-read.sh` against `DOC_SWEEP/SPEC.md` at `cf0b53a`. Answers:
`DOC_SWEEP/cold-read-answers/adjagent-*-spec-*.md` (this file holds what was ruled).

## "Is not" crossings (both readers)

| # | Crossing | Ruling |
|---|---|---|
| 1 | Model access: SPEC §8.2.1 "The memory service calls models itself", embedding provider config, §5.1 per-provider stream re-issue | THESIS stands. The memory service never calls a model: it requests judgments from the host, which runs the model. SPEC §8.2.1 states the U5 outcome. Going purely deterministic (no memory-side model judgments) is future intent for ROADMAP. |
| 2 | Memory store vs "a mirror of the user's repository": §3.9.1 "The history repository keeps each day-commit's tracked-file store" | THESIS stands. File content is kept only as long as composition needs it: the tracked-file store lives in the turn repository (deleted at each day rollover) and never in the history repository. Behavior change in code. |
| 3 | Thread lifecycle vs "a task tracker": `blocked`/`wip`/`decided` states, exception queue, `/closures`, confirmation dialogs | Settled by the strip plan: flow control goes (`ROADMAP_PLANS/strip-self-built-harness/what-it-does.md`, WP6–WP8). |
| 4 | Model judgments vs "free-form generation that decides what is stored": model tag symbols folded into `history_symbols` with normalization only; clusterer's open-ended answers decide retirement | Settled: the topic tag's fate is ROADMAP U3; THESIS's Model judgments row is the rule SPEC is held to when U3 lands. |
| 5 | Durability vs "a transaction manager": §4.5.8's 12-case recovery machine, batch-op markers, two-repository roll-forward | Settled: SPEC holds no mechanism; it leaves in the mechanism pass. |
| 6 | Recall vs "a vector database"; memory vetting vs "an approval workflow" | Not signal: borderline, no proposal builds past an "is not". |
| 7 | Event log vs "a telemetry or metrics backend" | Not signal: measuring from a log is ordinary. |

## SPEC against THESIS

All three are v0.1 scope limits already tracked:
- §3.1 "replayed transcript tail" against "composed, not replayed": ROADMAP U3 and the plugin route.
- §3.2/§3.1 cross-project engagement declined in v0.1 against "across projects as if there had
  never been a boundary": ROADMAP.
- §3.8.4 archived threads off-recall in v0.1 against "stays findable afterwards": ROADMAP
  "Archive recall".

## Done for ruling 1

THESIS Model access row and plain-terms line; SPEC §8.2.1 rewritten to the U5 outcome, with §2.1
"Secret-bearing content", the §2.1, §8.1 and §8.2.2 U5 `Open:` lines and the "Key material"
subsection following; ROADMAP Mid-term "Deterministic-only memory".

## Done for ruling 2

SPEC §3.9.1 states the outcome with an `Open:` line naming the gap; ARCHITECTURE "Tracked-file
store" carries an out-of-date marker deferring to SPEC; ROADMAP Near-term "Tracked-file content
in the history repository" records the defect.

## Held for the contradictions pass

The architect's S1–S36 (internal contradictions) and both readers' undefined-term lists, checked
against the code before ruling.

## Open

- Sweep vs strip plan: strip packages edit `PROJECT_DOCS/` leaves the redice will overwrite.
  (a) strip contract edits go to the `DOC_SWEEP/` monoliths while the sweep is open; (b) finish
  the sweep and redice before resuming the strip at WP3.
