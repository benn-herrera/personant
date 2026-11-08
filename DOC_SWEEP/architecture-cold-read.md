# ARCHITECTURE cold read 1: triage

Readers: adjagent:architect and adjagent:go-coder, each on ARCHITECTURE alone and on THESIS + SPEC
+ ARCHITECTURE, run by `DOC_SWEEP/cold-read.sh` against `5543a75`. Answers:
`DOC_SWEEP/cold-read-answers/adjagent-*-arch-*.md`.

Each finding was checked against the written record first (process notes, "Before putting a
cold-read finding to the owner"). None needed a ruling.

## "Is not" crossings

| Crossing | Settled by | Work it leaves |
|---|---|---|
| Model access: `internal/model` HTTP client, U5 provider-pool option, the in-process executor's model call, the Gemma-via-`reaper` tuning target | SPEC §8.2.1 and THESIS ruling 1 (the memory service never calls a model) | ARCHITECTURE's U5 text and provider-pool material are strip material (WP5): cut in the content pass. |
| Plugin toward an agent runtime: U3's per-protocol stream cancel-and-reissue | ROADMAP U3 (pending decision) | none until U3 lands |
| Plugin / host: "a full Go port of opencode's server" as the Boundary fallback | Future intent: ROADMAP Parked plans lists `OPENCODE_SERVER_REPLACEMENT.md`; future intent stays out of contract documents | Content pass: the fallback sentence leaves ARCHITECTURE "Boundary", which cites nothing future. |
| Memory store / career memory: tracked-file content in the history repository | SPEC cold-read ruling 2 (already marked out of date in ARCHITECTURE) | The go-coder adds that archival capture commits pin the store too: extend the ROADMAP item "Tracked-file content in the history repository". |
| Event log as telemetry: `model.inference-telemetry` | Ruling 1: with no model call in the memory service there is no inference telemetry to log | Strip material: cut in the content pass. |
| Memory service toward multi-tenant: "multi-user (v2.0, locked)" | ROADMAP: "locked, not under active consideration"; ARCHITECTURE only distinguishes concurrent sessions from it | none |
| Storage evolution: "bbolt as a derived KV index" | THESIS Indexes row ("derived index files, rebuildable"); the store of record stays text | none |
| Recall as a vector database; durability as a transaction manager | Not signal (borderline), as in the SPEC cold read | none |
| Model judgments: routine closing "takes the draft as written"; clusterer output decides thread boundaries; tag symbols "folded in directly" | THESIS Model judgments row is the rule; U3 for the tag; strip WP6 for closing | Content pass: ARCHITECTURE "The model drafts, deterministic code writes" must name the validation, or state the gap. |

## Unstated departures (content pass: state each departure and its reason, or conform)

- Excerpt bundle stores text with JSON HTML escaping; `logs/archive/*.tar.gz` is binary and in no
  storage class: against "human-readable text files" and "One inspection location".
- Derived fields inside authoritative files (`anchors`, `anchors_projected_at_turn` in
  `spine.jsonl`; `thread_count` in `meta.json`); `working-set.json` holds state that cannot be
  rebuilt; `.recall-cache/` filed operational.
- Plugin keeps sequence numbers and a fail-open count, against "stateless adapter" (reason given
  for sequencing, departure not named).
- Decay keys off a turn counter that resets on relaunch (U9), against "state machine with decay
  timers".
- Directive precedence resolved once per service run, no reason given.
- Summary-tree beam descent replaces the flat scan; the tie to SPEC §3.4's no-decay requirement is
  not drawn.
- Career memory: ARCHITECTURE has no section saying it is threads plus recall (THESIS row: "drawn
  from the record and recalled by relevance").

## Held for later passes

Both readers' internal contradictions, undefined-term lists and "SPEC content in ARCHITECTURE"
lists (alone reads, question 4), checked against the code before ruling.
