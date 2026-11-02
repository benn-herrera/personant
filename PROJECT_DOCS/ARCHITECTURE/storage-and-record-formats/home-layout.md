### Home layout

```
$PERSONANT_HOME/                    # default ~/.personant; configurable
  spine.jsonl                       # canonical, sorted lexically by id
  symbols.jsonl                     # [derived] inverse symbol → threads index
  threads/thr_<id>/                 # one directory per thread
    thread.md                       # frontmatter (canonical) + title; no accumulating body
    turns/                          # warm turn excerpts, one file each (0000024.md)
    folio.jsonl                     # bound cold turn excerpts, one line each
    files.jsonl                     # tracked-file index (SPEC §3.9)
    files/                          # current blobs and kept diffs
  projects/prj_<n>/
    meta.json                       # project metadata (canonical)
    digest.json                     # [derived] Layer A2 digest
  directives/
    defaults.md                     # baked-in defaults (read-only after install)
    user.md                         # user-wide accrued overrides
    prj_<n>/project.md              # project-scoped directives
  logs/YYYY-MM-DD.log               # plain-text, append-only, daily rotation
  logs/archive/YYYY-MM.tar.gz       # rotated logs older than 90 days, gzipped
  version.toml                      # canonical, committed; `format = N` (SPEC §9.1)
  README.md                         # layout documentation for human inspection
  last-active                       # operational; prj_<n> of the most recently active project
  turn-journal.jsonl                # operational, gitignored; in-flight-turn journal (SPEC §4.5.8)
  op-in-progress.json               # operational, gitignored; batch-op marker, survives reset
  derived-watermark                 # operational, gitignored; daily HEAD the derived files match
  working-set.json                  # operational, gitignored; Layer B order + dormant set
  .recall-cache/                    # operational, gitignored; derived recall indexes
  archive/index.jsonl               # canonical; deep cold archive index (SPEC §3.8)
  recovery/quarantine/<stamp>/      # operational; byte-exact bytes preserved by recovery
  .git/                             # primary DB: permanent history, one day-grain commit per day
  .git-daily/                       # operational, gitignored: disposable per-turn DB, reborn daily
```

| Type | Examples | Drift policy |
|---|---|---|
| Canonical | `spine.jsonl`, `threads/*`, `directives/*.md`, `projects/*/meta.json`, `logs/*.log`, `version.toml`, `archive/index.jsonl` | Source of truth; hand-editable. `logs/*.log` is the append-only turn-grain forensic record that recovery preserves and re-appends. |
| Derived | `symbols.jsonl`, `projects/*/digest.json` | Regenerable from canonical; a state-changing git operation on the home tree fails while any derived file is stale; never hand-edited. |
| Operational | `.git/`, `.git-daily/`, `last-active`, `turn-journal.jsonl`, `op-in-progress.json`, `derived-watermark`, `working-set.json`, `.recall-cache/`, `recovery/` | System-managed, not drift-checked. All but `.git/`, `recovery/` and the shipped files are gitignored; both git DBs are in the managed gitignore block so neither repo sees the other as untracked (SPEC §4.5.8 INV-6). |

