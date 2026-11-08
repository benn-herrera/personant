### Event log format

Standard pattern: THESIS "The building blocks", row "The event log".

The memory service↔harness measurement contract (`logs/YYYY-MM-DD.log`, `internal/eventlog`). SPEC §2.8
states the requirement that every decision the §9 regime measures is recorded; this is the format
and vocabulary. Plain text, append-only, one event per line; no JSON schema:

```
<RFC3339-timestamp> <event-name> <free-form-details>
```

Event names are dot-separated category and action (`thread.engaged`, `spine.match-fire`,
`retire.prompt`). An event absent from the vocabulary cannot be measured, so a mechanism that emits
a new decision event adds its entry here. Actions ending in `-error` (and `warning`) are forensic
diagnostics; §9 measurement keys off the decision events. No event carries multi-line content.

```
2026-05-08T02:55:44-07:00 system.bootstrap version=0.1.0 home-format=1 commit=abc1234 home=/Users/u/.personant
2026-05-08T02:55:45-07:00 session.started active=prj_3
2026-05-08T02:55:50-07:00 thread.engaged thr_42 turn_count=1247
2026-05-08T02:55:51-07:00 spine.match-fire thr_88 score=0.62 matched=trefoil,unknot query_size=5
2026-05-08T02:55:51-07:00 spine.embed-match-fire thr_88 score=0.610 query_chars=140
2026-05-08T03:12:00-07:00 retire.prompt thr=thr_42 inactivity=12
```

| Category | Events |
|---|---|
| `system` | `bootstrap` (identity line, once per process open, after the §9.1 format check resolves: `version=` the memory service semver, `home-format=` the EFFECTIVE on-disk revision, `commit=` (`unknown` on a binary built without a build id), `home=`; distinct from `session.started`), `home-format-override` (a `--allow-newer-home` open waved a newer store directory through the §9.1 refusal — `on-disk=`, `binary=`; always paired with a stderr warning), `home-format-migrated` (an older store directory was migrated forward at open by the §9.1 format check — `from=`, `to=`), `context-ceiling-breach`, `context-ceiling-unenforceable` (the post-flight token-ceiling check could not be evaluated: no `usage.prompt_tokens` for a turn while a ceiling was configured — `reason=usage-unavailable ceiling= turn=`; one line per affected turn; the count is never estimated), `empty-response` (forensic; the final drained response carried zero visible content — `reprompted=yes\|no`, `turn=`; SPEC §3.3), `turn-aborted` (a retracted turn — `turn=` the turn's transaction id, `phase=` the labelled stage, `bytes=` the retracted input's size; SPEC §4.3.3; **never the text**), `turn-abort-release-error` (forensic; releasing a retracted turn's recovery scope failed) |
| `thread` | `engaged`, `engaged-non-owner`, `engaged-cross-project`, `engaged-miss`, `created`, `created-meta-only`, `state-change`, `renamed` (a working name changed — `thr=`, `old=`, `new=`; unmeasured, recorded so every working name is reconstructable from the log), `fetch-miss`, `fetch-cross-project`, `anchor-projection-overflow`, `tag-defaulted` (`thr=` the bound owner, `cause=missing-tag\|empty-response`, `reprompted=yes\|no`; SPEC §3.3) |
| `spine` | `match-fire`, `embed-match-fire`, `intra-match-fire` |
| `recall` | `accept` (`thr=`, `layers=` — an auto-fetch band candidate fetched automatically, one line per thread in promotion order), `net-cap-hit` (the intra-thread descent dropped related leaves at `scoring.NetCap`), `flush-backlog` (pending flush jobs passed the warning threshold), `W1-diag` (per-probe line for a descent-versus-flat-scan divergence: class, flat, descent, missed, substituted; `measure.go:1689`), `fire-error`, `error`, `index-error`, `embed-error`, `debt-window-error`, `tree-error` |
| `retire` | `prompt` (`thr=`, `inactivity=`), `complete` (`resolution=`), `curator-error`, `load-error`, `apply-error`, `error` |
| `archive` | `archived`, `recovered`, `recovered-record`, `skip`, `under-drain` (archival at a rollover could not reach the registry low-water mark), `error` |
| `recovery` | (startup recovery, see "Crash recovery and the turn and history repositories"; forensic) `begin`, `complete` (`cells=` lists the recovery cases hit; `reset=`, `stamped=`, `unrepairable=`), `pending` (`op=`, `day=` — a rollover or archival completion left pending), `rollback` (torn-turn reset — `turn=`, `reverted=`, `debris=`), `journal-recovered` (preserved in-flight bytes — `turn=`, `records=`), `morning-init` (`baseline=`, `rebuilt=`), `adopt` (case 12 greenfield/legacy), `stamp-repaired` (case 9), `unrepairable` (archive entry stays refused — `thr=`, `reason=`), `quarantined` (byte-exact preserved path), `log-tail-repaired` |
| `barrier` | (day rollover steps, see "Day rollover"; forensic) `begin` (`day=`), `archived` (archival step — `day=`, `count=`), `folio-settled` (excerpt-storage step, once per rollover — `day=`, `threads=`, `bound=`, `unbound=`: excerpts moved into / out of bundles), `folio-skip` (excerpt-storage step left an excerpt where it was — `thr=`, `name=`, `reason=too-large\|conflict\|unreadable`; for `unreadable` the name is `folio.jsonl` and nothing in the thread moved), `day-committed` (day-commit step — `day=`, optional `repaired=true`), `reborn` (turn-repository re-init step — `day=`, `baseline=`), `complete` (`day=`), `re-mint` (defensive day-commit step re-mint on a non-day-shape HEAD), `quarantined` (quarantine-and-proceed — `paths=`, `dir=`, `restored=`), `tag-error` (forensic; a lifecycle-tag derivation read/parse/tag fault — never fatal to the seal) |
| `consolidate` | `sleep-cycle` |
| `dedup` | `pruned`, `prune-skip`, `error` |
| `fs` | `edit-no-path`, `write-error` |
| `staging` | `promoted` (`normalized=`, `staged_at=`, `cited_at=`, `turn_span=`), `evicted` (expiry at window close, SPEC §3.10) |
| `topic` | `re-prompt` (`cause=missing-thread` + `fetched=` for the §5.5 fetch, `cause=missing-tag` for the §3.3 missing-tag fallback, or `cause=empty-response` for the §3.3 empty-response fallback; **Open:** U3 — `cause=missing-thread` has no producer under opencode), `tag-missing`, `tag-invalid` (forensic; a near-miss tag candidate that failed strict validation — `reason=markdown-mangled\|bad-delimiters\|bad-thread-list\|bare-new-topic` + a short sanitized `snippet=`; additive to `tag-missing`), `warning` |
| `session` | `started` (a service run started, SPEC §1.1 — `active=`; distinct from `system.bootstrap`), `ended` (a service run ended), `working-set-save-error`, `checkpoint-error` |
| `project` | `created` (a project was created for a directory not seen before — `id=` the `prj_<n>` handle, `name=` the directory's base name, last on the line; SPEC §4.5.7; the day rollover tags it `project/<id>/created`) |
| `model` | `stream-close-warn`, `inference-telemetry` (the provider's extended inference-timing block, one line per turn: `ttft_ms=`, `prefill_ms=`, `gen_ms=`, `total_ms=` in MILLISECONDS, plus `prefill_tps=`, `gen_tps=` (the provider's own tokens/second), `prompt_tokens=`, `completion_tokens=`, `cached_tokens=` (prompt-cache hits), `turn=`; a de-facto extension, not OpenAI schema — when the provider sends none the line is not emitted, and absence is never an error or warning; **Open:** the source of the block under opencode, which makes the provider call; today only the in-process turn emits it (see "Model access and inference telemetry")) |
| `workset` | `warning` (slot render failure, SPEC §3.1) |
| `context` | `modified` (`source=`, `bytes=`; SPEC §3.0.1). The opening line of each turn is defined in SPEC §2.8. |

Some category and code names predate the settled vocabulary: `spine` is the thread registry, `retire` closing, `dissect` the clusterer, `accrual` feedback directive tuning, `barrier` the day rollover and `sleep` consolidation.

**Reserved names** (a mechanism that emits one follows the shape of its category):
`system.shutdown`, `system.error`, `system.config-reload`, `spine.match-miss`,
`spine.entry-updated`, `recall.cross-project-fire`, `project.meta-updated`, `dissect.fire`,
`dissect.cluster-proposed`, `dissect.complete`, `directive.accrual-update`,
`directive.parameter-read` (sampled), `index.rebuild-start`, `index.rebuild-complete`,
`index.verify-fail`.

