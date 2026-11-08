### Store directory layout

```
$PERSONANT_HOME/                    # default ~/.personant; configurable
  spine.jsonl                       # thread registry; authoritative, sorted lexically by id
  symbols.jsonl                     # [derived] inverse symbol → threads index
  threads/thr_<id>/                 # one directory per thread
    thread.md                       # frontmatter (authoritative) + title; no accumulating body
    turns/                          # loose tier: turn excerpts, one file each (0000024.md)
    folio.jsonl                     # excerpt bundle: bundled-tier turn excerpts, one line each
    files.jsonl                     # tracked-file index (SPEC §3.9)
    files/                          # current blobs and kept diffs
  projects/prj_<n>/
    meta.json                       # project metadata (authoritative)
    digest.json                     # [derived] the project's other-projects slot entry
  directives/
    defaults.md                     # baked-in defaults; rewritten from the binary by every `personant init`
    user.md                         # user-wide overrides, including feedback directive tuning
    prj_<n>/project.md              # project-scoped directives
  logs/YYYY-MM-DD.log               # plain-text, append-only, one file per day
  logs/archive/                     # created empty by init; nothing writes it (moving old day files there is not built)
  version.toml                      # authoritative, committed; format revision `format = N` (SPEC §9.1)
  README.md                         # layout documentation for human inspection
  last-active                       # operational; prj_<n> of the most recently active project
  turn-journal.jsonl                # operational, gitignored; in-flight-turn journal (SPEC §4.5.8)
  op-in-progress.json               # operational, gitignored; operation marker, survives reset
  derived-watermark                 # operational, gitignored; turn-repository HEAD the derived files match
  working-set.json                  # operational, gitignored; active-set order + dormant set
  .recall-cache/                    # operational, gitignored; derived recall indexes
  archive/index.jsonl               # authoritative; cold storage index (SPEC §3.8)
  recovery/quarantine/<stamp>/      # operational, gitignored; byte-exact bytes preserved by recovery
  .git/                             # history repository: permanent; one day-commit per day plus archival commits (SPEC §3.11)
  .git-daily/                       # operational, gitignored: turn repository, per-turn, deleted and recreated each day
```

| Type | Examples | Drift policy |
|---|---|---|
| Authoritative | `spine.jsonl`, `threads/*`, `directives/*.md`, `projects/*/meta.json`, `logs/*.log`, `version.toml`, `archive/index.jsonl` | Source of truth; hand-editable (`directives/defaults.md` is rewritten by every `personant init`). `logs/*.log` is the append-only turn-grain forensic record that crash recovery preserves and re-appends. |
| Derived | `symbols.jsonl`, `projects/*/digest.json` | Regenerable from the authoritative files; a day rollover or archival batch fails if it leaves a derived file stale; turn-repository commits are not checked; never hand-edited. |
| Operational | `.git-daily/`, `last-active`, `turn-journal.jsonl`, `op-in-progress.json`, `derived-watermark`, `working-set.json`, `.recall-cache/`, `recovery/` | System-managed, not drift-checked, and gitignored: the managed gitignore block (the part of the store's `.gitignore` that Personant writes and keeps current) lists each, `.git/` and `.git-daily/` included, so neither repository sees the other as untracked (the ignore-block rule; see "Crash recovery and the turn and history repositories"). |

`.git/`, the history repository, is in no row: system-managed and gitignored like the operational files, it is the only home of archived threads (SPEC §3.8.1) and so irreplaceable; a missing one is refuse-to-open.

`.recall-cache/` and `working-set.json` are filed operational for different reasons. `.recall-cache/` holds derived vectors and summary trees (SPEC §3.4 *Persistence*), but regenerating it costs embedding calls, so it cannot be kept fresh at every day rollover and archival batch as derived files are. Instead each sidecar carries a content-staleness key, a stale or missing sidecar is rebuilt on load, and deleting the directory costs only re-embedding. `working-set.json` holds the active-set order and the dormant set, which no authoritative file records and nothing rebuilds. Losing it costs only recency: the next service run starts with both sets empty (`turn.LoadSession`); a malformed file fails the load.

