### Event log format

The runtime↔harness measurement contract (`logs/YYYY-MM-DD.log`, `internal/eventlog`). SPEC §2.8
states the requirement that every decision the §9 regime measures is recorded; this is the format
and vocabulary. Plain text, append-only, one event per line; no JSON schema in v0.1:

```
<RFC3339-timestamp> <event-name> <free-form-details>
```

Event names are dot-separated category and action (`thread.engaged`, `spine.match-fire`,
`retire.prompt`). An event absent from the vocabulary cannot be measured, so a mechanism that emits
a new decision event adds its entry here. Actions ending in `-error` (and `warning`) are forensic
diagnostics; §9 measurement keys off the decision events. No event carries multi-line content.

```
2026-05-08T02:55:44-07:00 system.bootstrap version=0.1.0 frontend=0.0.2 home-format=1 commit=abc1234
2026-05-08T02:55:45-07:00 session.started active=prj_3
2026-05-08T02:55:50-07:00 thread.engaged thr_42 turn_count=1247
2026-05-08T02:55:51-07:00 spine.match-fire thr_88 score=0.62 matched=trefoil,unknot query_size=5
2026-05-08T02:55:51-07:00 spine.embed-match-fire thr_88 score=0.610 query_chars=140
2026-05-08T03:12:00-07:00 retire.prompt thr=thr_42 inactivity=12
2026-05-08T03:12:45-07:00 retire.ack thr=thr_42 resolution=resolved edited=no
2026-05-08T03:14:30-07:00 dissect.fire reason=budget-pressure clusters=4
```

| Category | Events |
|---|---|
| `system` | `bootstrap` (identity line, once per process open, after the §9.1 format gate resolves: `version=` substrate, `frontend=` (**Open:** U6; the example above carries it), `home-format=` the EFFECTIVE on-disk revision, `commit=` (`unknown` on an unstamped binary), `home=`; distinct from `session.started`), `home-format-override` (a `--allow-newer-home` open waved a newer home through the §9.1 refusal — `on-disk=`, `binary=`; always paired with a stderr warning), `home-format-migrated` (an older home was migrated forward at open by the §9.1 gate — `from=`, `to=`), `context-ceiling-breach`, `context-ceiling-unenforceable` (the post-flight token-ceiling gate could not be evaluated: no `usage.prompt_tokens` for a turn while a ceiling was configured — `reason=usage-unavailable ceiling= turn=`; one line per affected turn; the count is never estimated), `empty-response` (forensic; the final drained response carried zero visible content — `reprompted=yes\|no`, `turn=`; SPEC §3.3), `turn-aborted` (a retracted turn — `turn=` the turn's transaction id, `phase=` the labelled stage, `bytes=` the retracted input's size; SPEC §4.3.3; **never the text**; **Open:** U8 — emitted only if retraction stands), `turn-abort-release-error` (forensic; releasing a retracted turn's recovery scope failed; same Open) |
| `thread` | `engaged`, `engaged-non-owner`, `engaged-cross-project`, `engaged-miss`, `created`, `created-meta-only`, `state-change`, `renamed` (a display name changed — `thr=`, `old=`, `new=`; same shape as `project.renamed`; unmeasured, recorded so every display name is reconstructable from the log), `fetch-miss`, `fetch-cross-project`, `anchor-projection-overflow`, `tag-defaulted` (`thr=` the bound owner, `cause=missing-tag\|empty-response`, `reprompted=yes\|no`; SPEC §3.3) |
| `spine` | `match-fire`, `embed-match-fire`, `intra-match-fire` |
| `recall` | `offer` (`count=N`), `accept` (`thr=`, `layers=`, `ack=human\|auto` — `auto` is an auto-band candidate fetched without asking; any accept-rate measure counts `ack=human` lines only), `decline` (`thr=`, `reason=not-relevant\|wrong-project\|already-known` — `already-known` is also the runtime's own verdict on a candidate already resident in Layer B under `recall.ack-mode: banded`), `net-cap-hit`, `flush-backlog`, `W1-diag`, `fire-error`, `error`, `index-error`, `embed-error`, `debt-window-error`, `tree-error` |
| `retire` | `prompt` (`thr=` and `inactivity=`\|`trigger=manual`\|`trigger=queued`, the last for a SPEC §3.5 boundary drain), `ack` (every applied closure — retire or WIP — `resolution=`, `edited=yes\|no`, `ack=human\|auto`; the ack-edit-rate canary counts `ack=human` lines only), `pending` (a decayed thread queued as an exception for the boundary drain — `thr=`, `inactivity=`, `reason=anchors=N\|turns=N`; once per idle episode, not per scan), `defer`, `complete` (`resolution=`), `curator-error`, `load-error`, `resolver-error`, `apply-error`, `error` |
| `archive` | `archived`, `recovered`, `recovered-record`, `skip`, `under-drain`, `error` |
| `recovery` | (SPEC §4.5.8 startup recovery; forensic) `begin`, `complete` (`cells=`, `reset=`, `stamped=`, `unrepairable=`), `pending` (`op=`, `day=` — a barrier/archival completion left pending), `rollback` (torn-turn reset — `turn=`, `reverted=`, `debris=`), `journal-recovered` (preserved in-flight bytes — `turn=`, `records=`), `morning-init` (`baseline=`, `rebuilt=`), `adopt` (cell-12 greenfield/legacy), `stamp-repaired` (cell-9), `unrepairable` (archive entry stays refused — `thr=`, `reason=`), `quarantined` (byte-exact preserved path), `log-tail-repaired` |
| `barrier` | (SPEC §4.5.8 day barrier B0–B6; forensic) `begin` (`day=`), `archived` (B1 — `day=`, `count=`), `folio-settled` (B1b, once per barrier — `day=`, `threads=`, `bound=`, `unbound=`), `folio-skip` (B1b left an excerpt where it was — `thr=`, `name=`, `reason=too-large\|conflict\|unreadable`; for `unreadable` the name is `folio.jsonl` and nothing in the thread moved), `day-committed` (B2 — `day=`, optional `repaired=true`), `reborn` (B5 daily re-init — `day=`, `baseline=`), `complete` (`day=`), `re-mint` (defensive B2 re-mint on a non-day-shape HEAD), `quarantined` (quarantine-and-proceed — `paths=`, `dir=`, `restored=`), `tag-error` (forensic; a lifecycle-tag derivation read/parse/tag fault — never fatal to the seal) |
| `consolidate` | `sleep-cycle` |
| `dedup` | `chain-aged`, `chain-age-refused`, `error` |
| `fs` | `edit-no-path`, `write-error`, `commit-untracked` |
| `tool` | `result` (a tool result applied to memory as a `tool.result` delta, SPEC §3.0.1 — `name=`, `id=` the call id, `bytes=` the byte count BEFORE the SPEC §3.1 tool-output bound; an error result is a result; never the arguments). |
| `staging` | `promoted`, `evicted` (window-close GC, SPEC §3.10) |
| `topic` | `re-prompt` (`cause=missing-thread` + `fetched=` for the §5.5 fetch, `cause=missing-tag` for the §3.3 tag recovery, or `cause=empty-response` for the §3.3 empty-response recovery; **Open:** U3 — `cause=missing-thread` has no producer under opencode), `tag-missing`, `tag-invalid` (forensic; a near-miss tag candidate that failed strict validation — `reason=markdown-mangled\|bad-delimiters\|bad-thread-list\|bare-new-topic` + a short sanitized `snippet=`; additive to `tag-missing`), `warning` |
| `session` | `started` (a service run started, SPEC §1.1 — `active=`; distinct from `system.bootstrap`), `ended` (a service run ended), `working-set-save-error`, `checkpoint-error` |
| `project` | `created`, `switched`, `renamed` |
| `model` | `stream-close-warn`, `inference-telemetry` (the provider's extended inference-timing block, one line per turn: `ttft_ms=`, `prefill_ms=`, `gen_ms=`, `total_ms=` in MILLISECONDS, plus `prefill_tps=`, `gen_tps=` (the provider's own tokens/second), `prompt_tokens=`, `completion_tokens=`, `cached_tokens=` (prompt-cache hits), `turn=`; a de-facto extension, not OpenAI schema — when the provider sends none the line is not emitted, and absence is never an error or warning; **Open:** the source of the block under opencode, which makes the provider call — the event is retired if there is none, see "Serving deployment") |
| `workset` | `warning` (layer render failure, SPEC §3.1) |
| `context` | `modified` (`source=`, `bytes=`; SPEC §3.0.1). The per-turn boundary contract is in SPEC §2.8. |

**Reserved names** (a mechanism that emits one follows the shape of its category):
`system.shutdown`, `system.error`, `system.config-reload`, `spine.match-miss`,
`spine.entry-updated`, `recall.cross-project-fire`, `project.cd-changed`, `project.remote-adopted`,
`project.remote-updated`, `project.remote-collision-prompt`, `project.meta-updated`, `dissect.fire`,
`dissect.cluster-proposed`, `dissect.complete`, `directive.accrual-update`,
`directive.parameter-read` (sampled), `index.rebuild-start`, `index.rebuild-complete`,
`index.verify-fail`.

