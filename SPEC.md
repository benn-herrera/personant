# SPEC – Personant

**Audience:** anyone building or checking an implementation. This spec states consumer-facing
outcomes — on-disk schemas, observable behavior, config keys and defaults, events, user-visible
surfaces, acceptance-gate properties — such that any implementation meeting them is valid. How this
implementation meets them is `ARCHITECTURE.md`'s subject; this spec cites it rather than restating
it.

**Companion docs:**
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — orientation, principles, patterns, mechanisms,
  anti-patterns. **Read first.**
- [`CONVENTIONS.md`](CONVENTIONS.md) — house rules for agents in this repo.
- [`README.md`](README.md) — user-facing description; getting started.

---

## 0. Document conventions

- **JSONL schemas** use TypeScript-style `interface` notation; it is descriptive only.
- **Field markings:**
  - `field: T` — required.
  - `field?: T` — optional.
  - `[derived]` — regenerated from canonical sources; never hand-edited.
- **Pseudocode** is descriptive and language-neutral.
- **Cross-references** use §-numbered section identifiers (e.g. "see §2.2").

---

## 2. Data model

### 2.1 Storage layout

```
$PERSONANT_HOME/                    # default ~/.personant; configurable
  spine.jsonl                       # canonical, sorted lexically by id
  symbols.jsonl                     # [derived] inverse symbol → threads index
  threads/
    thr_<id>/                       # one directory per thread (see §2.3)
      thread.md                     # frontmatter (canonical) + title; no accumulating body
      turns/                        # serial FIFO-windowed turn-excerpt files (0000024.md)
      files.json                    # [§3.9] tracked-file sidecar
  projects/
    prj_<n>/                        # stable internal handle (see §2.5.1 / §4.5)
      meta.json                     # project metadata (canonical)
      digest.json                   # [derived] Layer A2 digest content
  directives/
    defaults.md                     # baked-in system defaults (read-only after install)
    user.md                         # user-wide accrued overrides
    prj_<n>/                        # project-scoped overrides, keyed by internal handle
      project.md                    # project-scoped directives
  logs/
    YYYY-MM-DD.log                  # plain-text, append-only, daily rotation
    archive/
      YYYY-MM.tar.gz                # rotated logs older than 90 days, gzipped
  providers.toml                    # provider pool — connectivity catalog (canonical; see §8.2.1)
  config.toml                       # configuration choices drawing from the pool (canonical; see §8.2.2)
  version.toml                      # canonical; committed. on-disk layout revision, one key: `format = N` (§9.1)
  api_keys/                         # secret-bearing — apiKeyFile targets; never read by the agent
  README.md                         # layout documentation for human inspection
  last-active                       # operational; one line: prj_<n> of most-recently-active project (§4.5.7)
  history                           # operational; REPL line-edit history (§4.3.1); newest last; capped
  turn-journal.jsonl                # operational, gitignored; in-flight-turn content journal + turn signal (§4.5.8)
  op-in-progress.json               # operational, gitignored; batch-op marker (archival|sleep|recovery|barrier|rebaseline), survives reset (§4.5.8)
  derived-watermark                 # operational, gitignored; daily HEAD hash the derived artifacts were built from (§4.5.8)
  working-set.json                  # operational, gitignored; Layer B order + dormant set, rewritten per turn (§3.1)
  .recall-cache/                    # operational, gitignored; derived recall indexes, regenerable (§3.4)
  archive/
    index.jsonl                     # canonical; deep cold archive index (§3.8); recoverable git-based archival
  recovery/
    quarantine/<stamp>/             # operational; byte-exact preserved bytes from torn-turn / verify-failure recovery (§4.5.8)
  .git/                             # primary git DB: permanent career history — one day-grain commit/day + archival anchors (§4.5.8)
  .git-daily/                       # operational, gitignored: DISPOSABLE per-turn recovery DB; nuked + reborn each day barrier; never career history (§4.5.8)
  tmp/                              # agent's drafting scratch (see §6.3); never git-committed
```

**File ownership classification:**

| Type | Examples | Drift policy |
|---|---|---|
| Canonical | `spine.jsonl` rows, `threads/*.md`, `directives/*.md`, `projects/prj_<n>/meta.json`, `logs/*.log`, `providers.toml`, `config.toml`, `version.toml`, `archive/index.jsonl` | Source of truth. Hand-editable. Other files derive from these. `logs/*.log` is canonical: the append-only turn-grain forensic record that crash recovery preserves and re-appends (§4.5.8). |
| Derived | `symbols.jsonl`, `projects/prj_<n>/digest.json` | Regenerable from canonical. A state-changing git operation on the home tree fails while any derived file is stale. Never hand-edited. |
| Operational | `.git/`, `.git-daily/`, `tmp/`, `last-active`, `history`, `turn-journal.jsonl`, `op-in-progress.json`, `derived-watermark`, `working-set.json`, `.recall-cache/`, `recovery/` | System-managed; not subject to drift checking. `.recall-cache/` is derived and regenerable; deleting it loses nothing. `tmp/`, `last-active`, `history`, `.git-daily/`, `turn-journal.jsonl`, `op-in-progress.json`, `derived-watermark`, `working-set.json`, and `.recall-cache/` are gitignored; `.git/` and `.git-daily/` are both in the managed gitignore block so neither repo sees the other as untracked (§4.5.8 INV-6). |
| Secret-bearing | `api_keys/*` (apiKeyFile targets); `providers.toml` only if it uses `apiKeyUnsafe` | Contains API keys. Never included in any LLM-context artifact, log line, ack prompt, or captured output. See §8.2.1. |

**Storage commands** (binary subcommands; see §4.1):
- `personant init` — first-run scaffold, idempotent.
- `personant index rebuild` — regenerate all derived files from canonical.
- `personant index check` — validate derived files match what rebuild would produce; non-zero exit
  on mismatch.
- `personant verify` — full structural validation (schema, ID uniqueness, anchor cardinality bounds,
  etc.).

### 2.2 Spine record schema (`spine.jsonl`)

One JSON object per line, sorted lexically by `id`, so diffs stay line-grain.

```typescript
interface SpineRecord {
  // identity
  id: string;                       // "thr_<n>"; monotonic serial; matches /^thr_\d+$/
  project: string;                  // stable project handle "prj_<n>" (see §2.5.1); display name dereferenced from project meta. "prj_default" reserved for unscoped threads.

  // recall material
  anchors: string[];                // [derived] 0..anchor.projection-max (default 8) normalized anchor symbols; a re-derived projection of active history_symbols (see §2.7.4)
  anchors_projected_at_turn: number; // [derived] owner-turn index at which the anchors projection last changed (idempotent-write guard, §3.2). Decodes 0 on old records.
  summary: string;                  // 100-150 char gist; ≤ spine.entry-max-chars (default 200); curator-drafted at retirement (§3.5)
  description: string;              // triggering utterance; set once at creation, never rewritten (§2.3). Distinct from summary. Decodes empty on old records.
  state: ThreadState;               // see §2.2.1

  // timestamps (RFC3339)
  created: string;
  last_engaged: string;             // read by closure decay against `engagement.decay-time` [§3.5], dormant-thread eviction order (oldest-`last_engaged` first) [§3.1], and cross-project digest `recent_anchors` selection [§3.7]
  state_changed: string;

  // bookkeeping
  turn_count: number;               // total turns this thread has been engaged in
  recall_fires: number;             // count of recall matches that resulted in fetch; orders the symbol index's candidate threads (most-recalled first) [§2.4] and feeds anchor-quality tuning
}
```

**Hard limits enforced by `personant verify`:**

| Field | Constraint |
|---|---|
| `id` | regex `/^thr_\d+$/`, globally unique |
| `project` | regex `/^prj_\d+$/`, references an existing project (or `prj_default`) |
| `anchors.length` | between 0 and `anchor.projection-max` (default 8) inclusive. `[derived]`: re-derived every owner turn at close from the thread's *active* `history_symbols` (§2.7.4). 0 anchors is legal (a vague-start thread). `personant verify` flags only an over-ceiling count — a substrate bug, since the model never controls this count (§5.1). The value 8 is a §9 calibration window. |
| `anchors[i]` | normalized form (see §2.7); 3-50 chars |
| `summary.length` | ≤ `spine.entry-max-chars` directive value |
| `state` | one of `ThreadState` enum values |
| timestamps | parseable as RFC3339, `created` ≤ `last_engaged` |

#### 2.2.1 Thread states

```typescript
type ThreadState =
  | "active"     // currently engaged or recently active (within engagement.decay window)
  | "paused"     // user explicitly paused (/pause)
  | "blocked"    // gated on external input; user-confirmed
  | "wip"        // long-running with progress; not yet resolved
  | "resolved"   // problem solved; work complete
  | "decided"    // architectural decision recorded; no further action expected
  | "abandoned"  // explicitly dropped without resolution
  ;
```

**State transitions** (see §3.5 for detailed flow):

| From | To | Trigger |
|---|---|---|
| (none) | `active` | thread creation |
| `active` | `paused` | `/pause` |
| `paused` | `active` | re-engagement or `/resume` |
| `active` | `blocked` | curator detects + user confirms |
| `active` | `wip` / `resolved` / `decided` / `abandoned` | closure prompt with chosen resolution |
| `wip` | `active` | re-engagement (model emits topic tag) |
| `resolved` / `decided` / `abandoned` | `active` | re-engagement; rare, but allowed |

#### 2.2.2 Display form

Human-readable spine line rendered from a `SpineRecord`:

```
thr_<id> [<anchors joined with ", ">] — <summary> [<state>]
```

Example:
```
thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict; entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]
```

This is what the LLM sees in working context; it is generated from the JSONL record, never stored.

The **human half** of a thread's display — the working NAME a user reads and types back — is its
`description`, falling back to `summary`, falling back to the id. `/topic rename` (§4.2) rewrites
that name and nothing else: the id, the anchors, `history_symbols` and everything recall matches on
are untouched. The user-facing rendering is one shape everywhere — `thr_N: display — gist` (§3.4).

### 2.3 Thread file format (`threads/thr_<id>/`)

A thread is a **directory**, `threads/thr_<id>/`, holding three things:

- **`thread.md`** — YAML frontmatter (canonical for metadata) followed by a single `# <title>` line.
  No accumulating body; rewritten in full on every engagement.
- **`turns/`** — serial turn-excerpt files, one per primary-engagement turn, named by the thread's
  turn number zero-padded to 7 digits (`turns/0000024.md`). Each holds that turn's terse operational
  excerpt.
- **`files.json`** — the §3.9 tracked-file sidecar.

Appending a turn writes one small file; per-turn write cost does not grow with thread length.

`thread.md`:

```markdown
---
id: thr_88
project: prj_3                       # internal handle; "ave-kb" is the display name in meta.json
state: wip
created: 2026-05-06T14:23:00-07:00
last_engaged: 2026-05-08T03:12:00-07:00
state_changed: 2026-05-07T19:42:00-07:00
turn_count: 24
recall_fires: 3
description: how does the trefoil's body topology constrain electron shape?
anchors:
  - trefoil
  - unknot
  - body-topology
  - electron-shape
history_symbols:
  - {raw: "trefoil", normalized: "trefoil", first_seen_turn: 142, count: 17, source: deterministic}
  - {raw: "(3,2)-torus knot", normalized: "3-2-torus-knot", first_seen_turn: 145, count: 4, source: model}
  - {raw: "Faddeev-Skyrme", normalized: "faddeev-skyrme", first_seen_turn: 148, count: 2, source: model}
---

# Body topology — trefoil vs unknot
```

One turn-excerpt file, `turns/0000024.md`:

```markdown
## Turn 24 · 2026-05-08T03:12:00-07:00 · [trefoil, unknot, body-topology]

**user:** [terse prompt excerpt]

**agent:** [terse response excerpt, topic tag stripped]
```

**Recency window (assembly) vs. retention (disk).** The working-set assembler reads at most 512
(default; calibratable) of a thread's most-recent turn-excerpts into the live body, sized so that at
`b-top-k = 3` each thread's assembled window stays within LayerB/3 bytes. The window governs
assembly into context, not on-disk retention: excerpts are **retained** past it, remain
recall-indexed, and are the durable source the §3.4 chunk-level index embeds for **intra-thread
recall** of early content. An excerpt is decision-class content (the terse `user:`/`agent:` turn
rendering); raw task-class tool output is a transient §3.0 context delta and is *never* written as
an excerpt. The assembled body is read newest-first up to a byte budget, then joined oldest→newest.

Durable turn content lives only in the retained excerpts, never the event log (§2.8 records event
metadata only: `source=` plus a byte count). Retention trimming is the deferred keep/toss layer
(§3.10.8); v0.1 keeps all decision-class excerpts.

**`description` vs `summary`.** `description` is the triggering utterance — the user prompt that
spawned the thread — set once at creation and never rewritten. `summary` is the curator's closure
gist, set at retirement (§3.5). A live thread has a `description` but no `summary`; a retired thread
has both. v0.1 sets `description` deterministically: a whitespace-collapsed copy of the spawning
prompt, truncated to the length bound the new-thread summary uses; no LLM paraphrase.

**No parent thread.** A thread has no parent-thread field and none is synthesized (the flat model,
§3.2). Origin provenance is recorded per symbol instead: a `history_symbols` entry may carry the
thread(s) it was carried in from via recall (`derived_from`, §2.7.3). Thread-level lineage remains
reserved.

**Body content:** terse and operational. Each `turns/<n>.md` holds one turn's excerpt (a turn where
this thread was the primary engagement) and carries no file-level title (the title lives in
`thread.md`). The retirement summary lives in frontmatter only, never in a turn file.

**Frontmatter `history_symbols` structure:**

```typescript
interface HistorySymbol {
  raw: string;                      // surface form as encountered
  normalized: string;               // normalized form (see §2.7)
  first_seen_turn: number;          // turn ID when first emitted
  count: number;                    // total emissions across all turns
  source: SymbolSource;             // see §2.7.2
  lifecycle?: SymbolLifecycle;      // "active" | "superseded"; canonical source of truth for supersession state (see §2.7.4). Omitted ≡ "active".
  ever_central?: boolean;           // latched true the first time the symbol enters the active anchor projection; never cleared. The retention discriminator. Omitted/false ≡ never-central.
  last_active_turn?: number;        // most recent turn the symbol was in the active projection; temporal ordering + eviction tiebreak. Omitted/0 ≡ never-active.
  derived_from?: string[];          // origin thread ID(s) this symbol was carried into this thread from, observed co-incident with a recall hit (§2.7.3); deterministic, set-valued, monotonic. Omitted/empty ≡ organic to this thread (the common case).
}

type SymbolSource = "deterministic" | "model" | "user" | "curator";
type SymbolLifecycle = "active" | "superseded";  // "evicted" is the *absence* of the entry, not a stored value
```

**Zero-value semantics (§2.7.4).** A record decoding without the lifecycle fields is well-formed:
`lifecycle == ""` is `active`, `ever_central` false ≡ never-central, `last_active_turn` 0 ≡
never-active, `derived_from` absent ≡ empty ≡ organic. `verify`/index-rebuild regenerate the derived
projection from canonical on first run.

`history_symbols` is hard-capped at `history.cap-per-thread` (default 40), a *total* cap over both
`active` and `superseded` entries. Over the cap, eviction operates over the **evictable** partition
only: a symbol is evictable **iff** it is `NOT ever_central AND NOT a §2.7.2 high-specificity
identifier`. Within the evictable set, lowest `count` goes first, ties broken by lowest
`first_seen_turn`. **Graceful degradation:** if the protected set alone exceeds the cap, the
lowest-`count` `superseded` entries are evicted first.

### 2.4 Symbol index schema (`symbols.jsonl`)

[Derived] Inverse index. Sorted lexically by `symbol`. Rebuilt from `spine.jsonl` + `threads/*.md`
frontmatter on every index refresh.

```typescript
interface SymbolRecord {
  symbol: string;                   // normalized form
  threads: string[];                // thread IDs where symbol appears anywhere (anchor or history)
  anchor_in: string[];              // subset of `threads` where symbol is an anchor
  superseded_in: string[];          // subset of `threads` where this symbol's history entry is lifecycle==superseded; the cheap-scan abandoned-premise surface (§3.4). [derived] from frontmatter lifecycle.
  source_dominant: SymbolSource;    // most common source across emissions
}
```

`threads` is sorted by descending `recall_fires` of the referenced thread (most-recalled first).
`superseded_in` is sorted the same way and is rebuilt from the frontmatter `lifecycle` field (§2.3),
the only source of truth for supersession state.

### 2.5 Project metadata schema

#### 2.5.1 Canonical: `projects/prj_<n>/meta.json`

A project has three layers of identity (see §4.5):

- **Internal handle** (`id`) — `prj_<n>`, stable forever, used as storage key.
- **External canonical identity** (`remote_urls[0]`) — normalized git remote URL when present;
  gained when the project's git tree first acquires a remote.
- **Display name** (`name`) — user-facing label; mutable via `/project rename`.

```typescript
interface ProjectMeta {
  // identity
  id: string;                       // "prj_<n>"; matches /^prj_\d+$/; assigned at creation; never changes
  name: string;                     // display label; matches /^[a-z][a-z0-9-]*$/; mutable

  // location on disk (mutable)
  current_root_path: string;        // absolute path to project root, current
  historical_root_paths?: string[]; // appended on /cd-project moves; never rewritten

  // canonical external identity (when available)
  remote_urls?: string[];           // normalized git remote URLs; primary = remote_urls[0]
  historical_remote_urls?: string[]; // de-duplication memory; appended when remote URL changes

  // bookkeeping
  created: string;                  // RFC3339
  last_active: string;              // RFC3339; updated on any engagement
  thread_count: number;             // [derived] count of threads with project == id

  // project-scoped configuration
  conventions_paths: string[];      // absolute paths to convention files (CLAUDE.md, CONVENTIONS.md, etc.) loaded into Layer E
  symbol_patterns: ProjectPattern[]; // project-specific deterministic-pass regex patterns
  ignore_symbols: string[];         // project-scoped stopword additions
}

interface ProjectPattern {
  name: string;                     // human-readable name (e.g. "ave-kb-claim-id")
  regex: string;                    // RE2-compatible
  category: SymbolCategory;         // see §2.7.1
}
```

**Remote URL normalization** (canonical form):

- Lowercase host.
- Strip trailing `.git`.
- `git@host:owner/name` and `https://host/owner/name` are treated as the same URL.

**`prj_default`** is reserved for threads created before any explicit project was active. It has no
path or remote; its `meta.json` carries empty path fields and a fixed `name: default`.

#### 2.5.2 Derived: `projects/prj_<n>/digest.json`

[Derived] Layer A2 digest content. Regenerated whenever any thread in the project is updated.

```typescript
interface ProjectDigest {
  project: string;                  // project id ("prj_<n>"), matching ProjectMeta.id
  display_name: string;             // resolved at render time; cached for the digest's consumers
  thread_count: number;
  recent_anchors: string[];         // top N anchors aggregated across N most-recently-engaged threads; the Layer A2 cross-project recognition surface [§3.1, §3.4]
  one_line_summary: string;         // ≤ 80 chars; auto-generated from recent thread summaries
  byte_size: number;                // for budget accounting; should be ≤ cross-project.digest-per-project-bytes
}
```

`one_line_summary` is a comma-joined list of `recent_anchors`, truncated to 80 chars.

### 2.6 Directive file format (`directives/*.md`)

Directive files are markdown documents with YAML frontmatter for parameter overrides.

```markdown
---
scope: user                         # one of: defaults | user | project
project: null                       # null for defaults/user; project name for project-scoped
parameters:
  closure.ack-mode: always          # override system default of auto
  recall.ack-mode: always           # override system default of banded
last_modified: 2026-05-08T19:42:00-07:00
modification_source: accrual        # one of: install | user-edit | accrual
---

# User directives

## Revisit guidelines

User has accepted 12/15 of last cross-project recall offers; threshold tightened from 0.5 to 0.45.

[Free-form prose explaining the user-facing behavior the parameters encode. Inspectable, editable.]
```

**Directive precedence** (highest wins):
1. Project-scoped (`directives/prj_<n>/project.md`)
2. User-wide (`directives/user.md`)
3. System defaults (`directives/defaults.md`)

A parameter read walks the precedence chain and returns the first match; missing parameters fall
through to defaults.

#### 2.6.1 Recognized parameters

**v0.1 honors only `closure.ack-mode` and `recall.ack-mode` from directive files.** Every other key
below is recognized and listed with its default, but is currently fixed at that default: setting it
in a directive file has no effect.

```yaml
engagement.decay-turns: 8           # turns of non-engagement before closure fires
engagement.decay-time: 7d           # wall-clock equivalent (duration string)
closure.ack-mode: auto              # auto | always (§3.5).
                                    # auto (default): routine decay closures are
                                    # auto-accepted with the curator summary;
                                    # exceptions queue for a boundary drain.
                                    # always: every decayed thread prompts
                                    # interactively in-session.
recall.symbolic-threshold: 0.4      # Jaccard threshold for opportunistic recall surfacing
recall.cross-project-threshold: 0.5 # higher bar for cross-project surface
recall.ack-mode: banded             # banded | always (§3.4).
                                    # banded (default): a candidate at or above its
                                    # tier's auto threshold is fetched with one
                                    # committed line and no prompt; the band between
                                    # the surface and auto thresholds is asked;
                                    # nothing else surfaces.
                                    # always: every surfaced candidate is asked
                                    # (decline-reason prompt included).
recall.symbolic-auto-threshold: 0.65  # Jaccard score at or above which a candidate is
                                    # fetched without asking (§3.4 auto band)
recall.cosine-auto-threshold: 0.75  # cosine (embedding AND intra-thread — one bar, one
                                    # scale) at or above which a candidate is fetched
                                    # without asking
layer.b-top-k: 3                    # max active threads in Layer B
layer.budget.percentages: {E: 12, A1: 10, A2: variable, B: 55, C: 15, live_turn: 15}
                                    # live_turn is carved from the byte total first;
                                    # E/A1/A2/B/C partition the remaining memory budget
                                    # (A1+A2 = 18% high tier; A2 is the residue)
context.token-budget: 200000        # whole-request TOKEN ceiling; the authoritative
                                    # gate. Per-layer BYTE shares are derived
                                    # (ceiling × ~2.5 conservative B/tok, TEXT-ONLY)
                                    # and drive truncation; usage.prompt_tokens is
                                    # the gate.
recall.superseded-weight: 1.0       # numerator weight for a matched symbol that is superseded in a thread (§3.4); §9 calibration window — 1.0 = no down-weight
history.cap-per-thread: 40          # max history symbols per thread
anchor.projection-max: 8            # top-N active history_symbols projected to spine anchors (§2.2/§2.7.4); §9 calibration window
spine.entry-max-chars: 200          # hard cap for SpineRecord.summary
cross-project.digest-per-project-bytes: 150
dissect.pressure-threshold: 0.90    # budget-pressure fraction triggering fallback dissection
dedup.anchor-cadence: 10            # anchor literal every K-th stored version (§3.9.1)
dedup.diff-literal-threshold: 0.7   # reverse-delta at or above this fraction of the literal
                                    # size is stored as a literal instead (§3.9.1)
dedup.chain-retention-turns: 100    # reverse-delta chain retention window, in turns (§3.9.1)
dedup.chain-retention-days: 2       # ...or in days; whichever trips first ages the chain out
                                    # once the committed blob is workspace-git reachable
dedup.live-diff-window: 3           # N most-recent diffs kept literal in the window (§3.9.2)
dedup.diff-format: unified          # global default diff format (§3.9.3)
```

### 2.7 Symbol normalization and categories

#### 2.7.1 Categories

```typescript
type SymbolCategory =
  | "identifier"   // pattern-extractable: file paths, URLs, code symbols, claim IDs, commit SHAs
  | "entity"       // model-emitted: named concepts, proper nouns
  | "tag"          // user-emitted: #hash-style
  ;
```

#### 2.7.2 Normalization rules

| Category | Case | Whitespace | Punctuation | Notes |
|---|---|---|---|---|
| identifier | preserved | preserved | preserved | exact match required for collision |
| entity | lowercase | hyphenated (`"Cosserat sector"` → `"cosserat-sector"`) | stripped except `-`, `.`, `_` | stop words dropped from multi-word forms |
| tag | lowercase | hyphenated | stripped except `-` | leading `#` stripped |

**Multi-word entity stop word list** (initial; per-project additions in `meta.json`):
```
the, a, an, of, in, on, at, by, for, to, with, from, as, is, are, was, were, be, been, being
```

Stop words are dropped only *from* multi-word entities; a single-token entity that matches a stop
word (e.g. the bare entity `"the"`) is preserved as-is.

**Storage form:** `{raw: original surface, normalized: per-rules-above}`. Matching uses
`normalized`; display uses `raw`.

#### 2.7.3 Source

```typescript
type SymbolSource =
  | "deterministic"  // regex-extracted from turn content
  | "model"          // emitted in topic-tag prompt response
  | "user"           // emitted via #tag
  | "curator"        // selected at retirement as anchor
  ;
```

When a symbol is emitted by multiple sources, `source_dominant` in `SymbolRecord` is the
most-frequent source across all its history entries; ties broken by `curator > user > model >
deterministic`.

**Origin provenance (`derived_from`) vs source (`source`).** `source` records *how* a symbol was
emitted; `derived_from` records *which thread it came from*. They are orthogonal: a symbol can be
`source: model` and `derived_from: [thr_X]` simultaneously.

**Deterministic population — no LLM, no human.** At each turn's history merge, for every symbol
emitted into the engaged thread's `history_symbols`: if that normalized symbol also belongs to a
thread **currently resident in the recall-surfaced working set** — promoted into Layer B by recall,
via §3.4 recall-acceptance at turn close or the §5.5 mid-turn fetch, and not yet evicted — that
thread's id is unioned into the symbol's `derived_from`. A turn-close acceptance therefore
attributes from the next turn, a mid-turn fetch within the same turn. Eligibility **ends when the
parent is evicted from the working set**. The per-symbol set is monotonic: an origin, once
attributed, is never cleared. Recall does not write symbols into a thread.

**The synthesis case.** When a thread is started by recalling and combining ≥2 prior threads, the
symbols it carries forward from each acquire that parent's id — multi-parent provenance carried per
symbol, not as a thread-level edge.

**Recall is provenance-agnostic.** `derived_from` does **not** participate in the §3.4 recall match;
it is provenance metadata only.

**Open (deferred).** Synthesis introduced *without* a recall hit — a prior thread's idea brought
forward by paraphrase, so no normalized symbol matches — leaves `derived_from` empty.

#### 2.7.4 Symbol lifecycle and anchor projection

A thread's evolving identity lives in `history_symbols` (§2.3). The spine `anchors` field (§2.2) is
**not** a separate storage tier — it is a deterministic re-derived **projection** of that canonical
set. Every state transition here is deterministic; **no LLM prompt is involved.**

**Lifecycle states** (`HistorySymbol.lifecycle`):

- `active` — currently occupying a top-`anchor.projection-max` projection slot. The zero value `""`
  decodes as `active`.
- `superseded` — a once-central symbol that has fallen out of the top-`anchor.projection-max`
  projection (rank-dropout). A descriptive / eviction-priority label, **not a candidacy gate**: a
  superseded symbol competes for the projection on equal footing every turn. **Retained, not
  evicted** — it stays in `history_symbols`, so an abandoned premise remains a findable recall
  handle.
- *evicted* is **not** a stored value — it is the *absence* of the entry (capacity eviction per
  §2.3).

**The projection function.** A pure function of **current salience** over **all** retained
history_symbols — active and superseded alike; lifecycle is never a candidacy filter. Rank by:

1. **class** — a §2.7.2 high-specificity identifier (URL, file path, git SHA) ranks above an
   ordinary symbol of equal count;
2. **count** descending;
3. **recency** — `last_active_turn` then `first_seen_turn`, newest first;
4. **normalized** ascending (stable final tiebreak).

The top-`anchor.projection-max` is the active slice → `SpineRecord.anchors` (in rank order). Zero
active symbols project to an empty anchor list. **`ever_central` is not a ranking tier** — it is the
eviction-retention discriminator only; a once-central premise must remain outrankable.

**State transitions** are labels read off the re-ranked top-`anchor.projection-max` each turn —
deterministic, with **no TTL**. Exactly one entry and one exit transition:

- *entry* — a symbol that occupies a projection slot this turn latches `ever_central = true` (never
  cleared), sets `last_active_turn = turn`, and is `active`. This is the same transition whether the
  symbol is newly central or was `superseded` and has re-ranked back in; there is no separate
  re-entry pathway.
- *exit* — a symbol that *was* `ever_central` but no longer holds a top-`anchor.projection-max` slot
  flips to `superseded`.

**Ever-central = projection-entry latch.** A symbol is historically central iff it has *ever* made
the headline projection. This latched bool plus `last_active_turn` is the whole representation; no
peak rank or weight is stored.

**Capacity eviction** follows the §2.3 predicate. A symbol is never evicted while `ever_central` so
long as the protected set fits `history.cap-per-thread`.

**Ordering invariant** (per turn, before the spine/frontmatter write): **merge → project → evict.**
Projection runs on the full merged set before eviction, so a symbol entering the projection this
turn has `ever_central` latched before the eviction predicate reads it.

**Steady-state bound.** `history.cap-per-thread` (40) is the hard total cap (active + superseded);
the projected slice is hard-bounded at `anchor.projection-max` (8). Flat
`history_len`/`ever_central_count` over a run is a §9 sim obligation, not a static guarantee.

### 2.8 Log event format (`logs/YYYY-MM-DD.log`)

Plain-text, append-only, one event per line. Free-form details after a fixed prefix.

**Line format:**
```
<RFC3339-timestamp> <event-name> <free-form-details>
```

**Event-name convention:** dot-separated category and action (`thread.engaged`, `spine.match-fire`,
`retire.prompt`).

**Examples:**
```
2026-05-08T02:55:44-07:00 system.bootstrap version=0.1.0 frontend=0.0.2 home-format=1 commit=abc1234 home=/home/user/.personant
2026-05-08T02:55:45-07:00 session.started active=prj_3 provider=local
2026-05-08T02:55:50-07:00 thread.engaged thr_42 turn_count=1247
2026-05-08T02:55:51-07:00 spine.match-fire thr_88 score=0.62 matched=trefoil,unknot query_size=5
2026-05-08T02:55:51-07:00 spine.embed-match-fire thr_88 score=0.610 query_chars=140
2026-05-08T03:12:00-07:00 retire.prompt thr=thr_42 inactivity=12
2026-05-08T03:12:45-07:00 retire.ack thr=thr_42 resolution=resolved edited=no
2026-05-08T03:14:30-07:00 dissect.fire reason=budget-pressure clusters=4
2026-05-08T03:14:35-07:00 dissect.complete clusters=4 acks=4 spine_added=4
```

No JSON schema in v0.1.

**Event vocabulary.** The minimum required by §9 measurement and the directive-accrual loop: an
event absent from it cannot be measured, so a mechanism that emits a new decision event adds its
entry here. Actions ending in `-error` (and `warning`) are forensic diagnostics, listed for
completeness; §9 measurement keys off the decision events. An event line is single-line free-form,
so no event carries multi-line content.

| Category | Events |
|---|---|
| `system` | `bootstrap` (identity line, once per process open, after the §9.1 format gate resolves: `version=` substrate, `frontend=`, `home-format=` the EFFECTIVE on-disk revision, `commit=` (`unknown` on an unstamped binary), `home=`; distinct from `session.started`), `home-format-override` (a `--allow-newer-home` open waved a newer home through the §9.1 refusal — `on-disk=`, `binary=`; always paired with a stderr warning), `context-ceiling-breach`, `context-ceiling-unenforceable` (the post-flight token-ceiling gate could not be evaluated: no `usage.prompt_tokens` for a streamed turn while a ceiling was configured — `reason=usage-unavailable ceiling= turn=`; one line per affected turn; the count is never estimated), `empty-response` (forensic; the final drained response carried zero visible content — `reprompted=yes\|no`, `turn=`; §3.3), `turn-aborted` (Esc retraction — `turn=` the turn's transaction id, `phase=` the labelled stage, `bytes=` the retracted input's size; §4.3.3; **never the text**), `turn-abort-release-error` (forensic; releasing an aborted turn's recovery scope failed) |
| `thread` | `engaged`, `engaged-non-owner`, `engaged-cross-project`, `engaged-miss`, `created`, `created-meta-only`, `state-change`, `renamed` (`/topic rename` changed a §2.2.2 display name — `thr=`, `old=`, `new=`; same shape as `project.renamed`; unmeasured, recorded so every display name is reconstructable from the log), `fetch-miss`, `fetch-cross-project`, `anchor-projection-overflow`, `tag-defaulted` (`thr=` the bound owner, `cause=missing-tag\|empty-response`, `reprompted=yes\|no`; §3.3) |
| `spine` | `match-fire`, `embed-match-fire`, `intra-match-fire` |
| `recall` | `offer` (`count=N`), `accept` (`thr=`, `layers=`, `ack=human\|auto` — `auto` is an auto-band candidate fetched without asking; any accept-rate measure counts `ack=human` lines only), `decline` (`thr=`, `reason=not-relevant\|wrong-project\|already-known` — `already-known` is also the runtime's own verdict on a candidate already resident in Layer B under `recall.ack-mode: banded`), `net-cap-hit`, `flush-backlog`, `W1-diag`, `fire-error`, `error`, `index-error`, `embed-error`, `debt-window-error`, `tree-error` |
| `retire` | `prompt` (`thr=` and `inactivity=`\|`trigger=manual`\|`trigger=queued`, the last for a §3.5 boundary drain), `ack` (every applied closure — retire or WIP — `resolution=`, `edited=yes\|no`, `ack=human\|auto`; the ack-edit-rate canary counts `ack=human` lines only), `pending` (a decayed thread queued as an exception for the boundary drain — `thr=`, `inactivity=`, `reason=anchors=N\|turns=N`; once per idle episode, not per scan), `defer`, `complete` (`resolution=`), `curator-error`, `load-error`, `resolver-error`, `apply-error`, `error` |
| `archive` | `archived`, `recovered`, `recovered-record`, `skip`, `under-drain`, `error` |
| `recovery` | (§4.5.8 startup recovery; forensic) `begin`, `complete` (`cells=`, `reset=`, `stamped=`, `unrepairable=`), `pending` (`op=`, `day=` — a barrier/archival completion left pending), `rollback` (torn-turn reset — `turn=`, `reverted=`, `debris=`), `journal-recovered` (preserved in-flight bytes — `turn=`, `records=`), `morning-init` (`baseline=`, `rebuilt=`), `adopt` (cell-12 greenfield/legacy), `stamp-repaired` (cell-9), `unrepairable` (archive entry stays refused — `thr=`, `reason=`), `quarantined` (byte-exact preserved path), `log-tail-repaired` |
| `barrier` | (§4.5.8 day barrier B0–B6; forensic) `begin` (`day=`), `archived` (B1 — `day=`, `count=`), `day-committed` (B2 — `day=`, optional `repaired=true`), `reborn` (B5 daily re-init — `day=`, `baseline=`), `complete` (`day=`), `re-mint` (defensive B2 re-mint on a non-day-shape HEAD), `quarantined` (§4.5.8 quarantine-and-proceed — `paths=`, `dir=`, `restored=`), `tag-error` (forensic; a lifecycle-tag derivation read/parse/tag fault — never fatal to the seal) |
| `consolidate` | `sleep-cycle` |
| `dedup` | `chain-aged`, `chain-age-refused`, `error` |
| `fs` | `edit-no-path`, `write-error`, `commit-untracked` |
| `staging` | `promoted`, `evicted` (window-close GC, §3.10) |
| `topic` | `re-prompt` (`cause=missing-thread` + `fetched=` for the §5.5 fetch, `cause=missing-tag` for the §3.3 tag recovery, or `cause=empty-response` for the §3.3 empty-response recovery), `tag-missing`, `tag-invalid` (forensic; a near-miss tag candidate that failed strict validation — `reason=markdown-mangled\|bad-delimiters\|bad-thread-list\|bare-new-topic` + a short sanitized `snippet=`; additive to `tag-missing`), `warning` |
| `session` | `started` (`active=`, `provider=`; distinct from `system.bootstrap`), `ended`, `working-set-save-error`, `checkpoint-error` |
| `project` | `created`, `switched`, `renamed` |
| `model` | `switched` (`/model` changed the session's chat model or provider — `from=`, `to=`, each a `provider/model` reference; the only record of the session-scoped switch, §4.2), `stream-close-warn`, `inference-telemetry` (the provider's extended inference-timing block, one line per turn: `ttft_ms=`, `prefill_ms=`, `gen_ms=`, `total_ms=` in MILLISECONDS, plus `prefill_tps=`, `gen_tps=` (the provider's own tokens/second), `prompt_tokens=`, `completion_tokens=`, `cached_tokens=` (prompt-cache hits), `turn=`; a de-facto extension, not OpenAI schema — when the provider sends none the line is not emitted, and absence is never an error or warning) |
| `workset` | `warning` (layer render failure, §3.1) |
| `context` | `modified` (`source=`, `bytes=`; §3.0.1). **Per-turn boundary contract:** exactly one `modified source=user.prompt` line per turn, and every other line a turn produces follows it before the next turn's boundary; measurement segments turns on it, so it is never suppressed, reordered, or duplicated. |
| `user` | `shell` (one line per §4.4 invocation: `kind=` the `$`/`#` discriminant, `exit=` the command's status (negative when killed by a signal), `dir=` the shell cwd after the command, `cmd=` the invocation, quoted onto one line and passed through the §8.2.1 redactor; for `#` also `bytes=` the captured byte count, plus `truncated=yes produced=` when the in-memory capture bound clipped the output; **never the captured content**), `shell-capture-dropped` (a session ended with `#` captures still buffered — `count=`; the captures are discarded, §4.4.1) |

**Reserved names** (part of the vocabulary; a mechanism that emits one follows the shape of its
category): `system.shutdown`, `system.error`, `system.config-reload`, `spine.match-miss`,
`spine.entry-updated`, `recall.cross-project-fire`, `project.cd-changed`, `project.remote-adopted`,
`project.remote-updated`, `project.remote-collision-prompt`, `project.meta-updated`, `dissect.fire`,
`dissect.cluster-proposed`, `dissect.complete`, `directive.accrual-update`,
`directive.parameter-read` (sampled), `index.rebuild-start`, `index.rebuild-complete`,
`index.verify-fail`, `user.slash` (§4.2).

---

## 3. Core algorithms

### 3.0 Context-modification events (the architectural primitive)

The working window is a sequence of **context-modification events**; a "turn" only groups events
into human-readable spans. **No gaps:** every path by which content enters the working window runs
the per-event hook chain, so no content escapes extraction, dedup, or the budget check.

#### 3.0.1 The event taxonomy

| Event | Source | Content shape |
|---|---|---|
| `user.prompt` | user types or pastes | message text |
| `user.shell-capture` | `# <cmd>` stdout/stderr (§4.4.1) | captured bytes (subject to §6.5 byte cap) |
| `model.response` | LLM emits final reply (or response segment) | response text + optional tool-call envelope |
| `tool.result` | each tool dispatch returns | tool output bytes |
| `thread.fetched` | topic tag triggered a thread load into Layer B | thread body + frontmatter |

These are all the deltas the runtime emits through the §3.0 chain. Layer A2 digest content,
slash-command effects (`/cd-project`, `/back-to`, etc.) and directive-file reloads are not chain
deltas: they change *which* content the composer (§3.1) assembles, and what they surface arrives
through the `user.prompt` / `thread.fetched` / Layer-A2 paths. New capabilities (deep-cold recovery,
dedup-promotion of identifiers back to literal) add event types to the same chain.

#### 3.0.2 The hook chain

Each context-modification event runs the chain, in this order:

1. **Symbol extraction** (§3.3) — deterministic regex pass over the delta's content extracts
   identifiers (file paths, URLs, claim IDs, user `#`-tags); model-emitted symbols (in `topic`
   blocks) are parsed here too. Routing by the delta's retention class (§3.10.1) follows
   immediately: decision-class extractions go to the per-turn coalesce buffer; task-class
   extractions enter the staging buffer (§3.10.2).
2. **Engagement signal** (§3.2) — if the delta's symbol set overlaps any active thread's anchors
   above threshold, fire engagement for those threads, coalesced per turn (§3.0.4).
3. **Dedup decision** (§3.9) — a duplicate or near-duplicate of content already in window gets
   identifier-replacement or diff-encoding per §3.9.2; persistent storage (§3.9.1) is updated here.
4. **Budget check** (§3.1) — a delta that pushes the window over its layer caps triggers
   bump-eviction before the next event lands.

   > **v0.1 non-goal (turn-time budget enforcement).** This step performs no eviction in v0.1. The
   > only budget protections are (a) the per-thread turn-excerpt assembly window (§2.3) and (b)
   > render-time byte truncation of each layer (§3.1); neither acts at delta time, so an LLM whose
   > per-turn deltas outrun the layer caps can silently starve Layer B/C. Enabling enforcement here
   > is a separate decision, not an incremental fill-in.
5. **Logging** (§2.8) — emit the source-specific event (`tool.result`, `user.shell-capture`, etc.)
   plus a `context.modified` event if the delta materially changed window contents.

The chain runs at **semantic boundaries** — complete response segment, complete tool result,
complete capture — not per token. A streamed response fires hooks on stream completion (or on
logical sub-segment boundaries).

#### 3.0.3 Hook-firing order matters

- Symbol extraction runs before engagement (engagement depends on the extracted set).
- Dedup runs after extraction (extracted identifiers are what dedup matches against).
- Budget check runs last. Eviction is itself a context modification, but recursive hook firing is
  **capped at depth 1** — eviction emits a log line and is done.

#### 3.0.4 Per-turn coalescing for engagement

A turn is bracketed by consecutive `user.prompt` events. Within a turn the symbol set is
union-accumulated across all deltas, and engagement fires once per affected thread with the union as
input, so an N-tool-call turn does not inflate `turn_count` for every thread a tool result mentions.

`last_engaged` updates to the *latest* delta's timestamp. `turn_count` increments by 1 only for the
turn's **owner** thread (§3.2), however many deltas touched its anchors; an engaged-but-not-owner
thread's `turn_count` is unchanged.

#### 3.0.5 Implementation contract

Content reaches the working window only through the §3.0 chain; there is no bypass.

### 3.1 Working-set composition

The working set is rendered into the system prompt as five layers, each truncated to a byte budget
derived from `context.token-budget` and `layer.budget.percentages` (§2.6.1). The token ceiling is
the authoritative whole-request gate; the per-layer **byte** shares are derived from it (ceiling × a
conservative ~2.5 chars/token, text-only) and drive truncation. The live-turn reserve (`live_turn`,
15%) is carved from the byte total first; E/A1/A2/B/C partition the remainder (12/18/15/55, the 18%
high tier split between A1 fixed and A2 residue). The whole request — system prompt + replayed
history tail + current user input — is bounded against the token ceiling: bytes pre-flight (this
composition plus the live-turn sub-policy), `usage.prompt_tokens` post-flight as the real-model gate
(§6.5). Static prompt scaffolding (orientation preamble, §5.1.3 topic-tag directive, recovery
reminders, section headers) is charged to no layer; the conservative bytes/token ratio covers it.

**Layer order and contents** (§2.1 maps these to on-disk sources):

| Layer | Source | Render |
|---|---|---|
| E    | `directives/defaults.md`, `directives/user.md`, `directives/<active>/project.md`, plus the project's `conventions_paths` (§2.5.1) | Section-divided concatenation; YAML frontmatter is stripped from each directive file. |
| A1   | `spine.jsonl` filtered to `project == active` | One §2.2.2 display line per record. |
| A2   | `projects/<id>/digest.json` for every other project (excluding `prj_default`) | One line per project: `<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>`, sorted by `last_active` desc. Each line capped at `cross-project.digest-per-project-bytes`. |
| B    | `threads/thr_<id>/` (§2.3) for each id in the Layer B active set | Up to `layer.b-top-k` threads, most-recently-engaged first; each rendered as a heading + frontmatter line + body. Per-thread share = `LayerB / k`; oversized threads are individually truncated. |
| C    | `spine.jsonl` records for each id in the dormant set | One §2.2.2 display line per record. |

The layer percentages are calibration starting points, earned by the simulation sweep (§9.4).

**LRU update rule** (driven by the §3.0.4 turn-close path). After per-turn engagements commit, for
each engaged thread id:
- Remove it from both the Layer B active set and the dormant set if present.
- Place it at the front of the Layer B active set. **Layer B order** is that sequence, most recently
  engaged first.
- If the active set exceeds `layer.b-top-k`, demote the tail to the front of the dormant set.

Layer B order and the dormant set persist across a relaunch (`working-set.json`, §2.1); Layer B
order is the recency signal §3.3 owner-default uses. The dormant set is **not count-capped**: Layer
C's byte budget truncates it at composition time, and its truncation marker is the overflow
indicator.

**Truncation policy:** each layer's rendered string is truncated to its byte budget at a UTF-8 rune
boundary, with a marker `... [Layer X truncated; budget=N bytes] ...` appended. A layer that fails
to render (e.g. a project's `digest.json` is missing) emits a `workset.warning` log line and renders
empty; neighbour layers proceed.

**§5.5 mid-turn fetch:** when the model's topic tag at response start references a `thr_<n>` not in
the Layer B active set, the runtime aborts the in-flight stream, loads the thread, fires a
`thread.fetched` context delta (§3.0.1), promotes the thread into the Layer B active set (at the
front, capped at `layer.b-top-k` — overflow demotes the tail to the dormant set), and re-issues the
request with the recomposed system prompt. The user-visible response is the second stream's body.
The re-prompt is capped at 1 per turn. A fetched thread belonging to another project is declined at
the fetch seam (`thread.fetch-cross-project`) — the §3.2 cross-project policy — and is neither
promoted nor re-prompted for.

The close-time LRU update still picks up any thread the mid-turn fetch could not service (an
unloadable thread file logs `thread.fetch-miss` and is skipped) or did not trigger (e.g. the
second-stream tag introduces another missing `thr_<n>`; with the cap reached, that thread enters the
Layer B active set at close).

### 3.2 Topic tagging and engagement update

Topic-tag parsing is §5.1.2; engagement update is the §3.0.2 step-2 hook fired with the per-turn
coalesced symbol set (§3.0.4).

**Turn-single-owner invariant.** A **turn belongs to exactly one thread** — its *owner*. A turn may
*engage* several threads; only the owner receives the turn's excerpt. A second excerpt-write for the
same turn is a structural error and is refused. The runtime assigns ownership; the topic tag
(§5.1.1) is advisory input.

**Owner vs engaged.** For each turn:
- the **owner** gets the turn excerpt, `turn_count++`, recency (`last_engaged`), `history_symbols`
  merge, and state→active resurrection (§2.2.1). A new-thread owner is created with the excerpt,
  `turn_count = 1`, and its `description` (§2.3).
- an **engaged-but-not-owner** thread gets recency, `history_symbols` merge, and state→active
  resurrection — but **no excerpt** and **no `turn_count++`**.

The Layer B/C recency order (§3.1) and recall surfacing operate over all engaged threads; the §3.9
file-edit application binds to the owner.

**Phantom resolution and owner fallback.** Every referenced `thr_<n>` is resolved against the spine
at turn close. An id that does not resolve is a *phantom*: not in the spine (`thread.engaged-miss`)
or belonging to another project (`thread.engaged-cross-project` — cross-project engagement is not
built in v0.1 and is declined). A phantom is logged and dropped: it never enters the engaged set,
the Layer B active set, or the persisted working set; never owns the turn or receives the §3.9
file-edit binding; and a declined cross-project record is never mutated. When the tag's would-be
owner (the lowest-id existing referenced thread) is a phantom, ownership falls back to the most
recently engaged **valid** referenced thread, ties broken by lowest `thr_<n>` id. When every
referenced thread is a phantom: a tagged `*new-topic*` thread owns the turn; with no `*new-topic*`
either, a new thread is created from the turn as for a pure `*new-topic*` tag. The turn's excerpt is
never dropped. A turn with **no tag at all** whose deltas require owner binding follows the §3.3
missing-tag recovery (re-prompt once → owner-default by Layer B order).

**Anchor projection at owner-turn close.** After the owner's `history_symbols` merge, the runtime
re-derives the thread's `anchors` projection (§2.7.4) in the order **merge → project → evict**.
**Idempotent-write guard:** the spine line is changed (and `anchors_projected_at_turn` bumped to the
owner-turn index) **only when the projected anchor set differs** from the prior set.
**Engaged-but-not-owner threads do not re-project.** Closure (§3.5) runs a final authoritative
projection from `history_symbols`.

### 3.3 Symbol extraction (three passes)

Three passes per §3.0.2 step 1, cheapest first:

1. **Deterministic** — regex over the delta (file paths, URLs, claim IDs, `#`-tags).
2. **Model-emitted** — anchors from the topic tag (§5.1) are folded in directly.
3. **Curator** — at retirement the curator drafts the closure summary (§3.5). It does **not** select
   anchors: the anchor set is the deterministic projection of active `history_symbols` (§2.7.4).

Symbols from task-class deltas (§3.10.1) enter the staging buffer (§3.10.2) instead of the coalesce
buffer; they reach `symbols.jsonl` only on citation-window promotion (§3.10.4).

**Missing-tag protocol recovery (violation → recovery, never abort).** A response with no valid
topic tag never aborts the turn. Two regimes, split by whether the turn's deltas require owner
binding (buffered §3.9 file edits):

- **No binding required** (plain conversational turn): the turn proceeds tag-less — no engagement
  update, no excerpt, no owner, no re-prompt; `topic.tag-missing` is logged for calibration.
- **Binding required**: two recovery stages.
  1. *Single re-prompt* (`topic.re-prompt cause=missing-tag`): the in-flight stream is aborted and
     the request re-issued with a terse system-side reminder appended (§5.1.3), via the §5.5
     re-issue path. Capped at 1 per turn for this cause (combined bound: §5.5).
  2. *Owner-default* (`thread.tag-defaulted`): if the re-prompted response still lacks a tag, the
     turn binds to the most recently engaged valid Layer-B thread, by Layer B order (§3.1; the first
     resolving candidate wins) — the same choice after a restart, since Layer B order survives
     relaunch. When no Layer-B candidate resolves, a new thread is created from the turn. The
     excerpt and the §3.9 edits are never dropped, and recovered content still flows through the
     §3.0 chain (§3.0.5). The forensic `thread.tag-defaulted` line carries `thr=`,
     `cause=missing-tag`, `reprompted=yes|no`.

  `fs.read` deltas buffer a write entry exactly like `fs.write` (§3.9), so a tag-less read-only turn
  is binding-required.

**Empty-response recovery (any turn).** A response with **zero visible content** (no tag and no body
— e.g. a model that spent its completion budget on hidden reasoning and returned `finish=stop`) is a
protocol failure whether or not binding is required (an all-whitespace response longer than the
§5.1.2 scan window is tag-less, not empty — §5.5):

1. *Single re-prompt* (`topic.re-prompt cause=empty-response`): the ended stream is discarded and
   the request re-issued with the empty-response reminder (§5.1.3) appended — its own per-cause cap
   of 1 under the combined bound (§5.5). Fires on ANY turn, conversational or binding.
2. *Accept the empty*: if the re-prompted response is also empty, the turn proceeds with it. On a
   **binding-required** turn it falls through to owner-default above; the recorded excerpt's agent
   section is **empty** (never synthesized placeholder content), and `thread.tag-defaulted` carries
   `cause=empty-response` (with `reprompted=` reading the empty cause's flag). On a
   **conversational** turn the empty body surfaces to the caller unchanged. Either way the runtime
   emits a `system.empty-response` forensic line (`reprompted=`, `turn=`).

   The empty cause **subsumes** the missing-tag cause: a persistent-empty response never also spends
   the missing-tag re-prompt.

### 3.4 Recall matching

> **Recall-completeness invariant (no dead zones).** All **non-transient** turn content MUST be
> locatable by recall at every stage of its lifecycle — creation, assembly-window residency,
> scroll-out and durable storage, archival (§3.8), and later summarization/retirement (§3.5). **The
> ONLY content that may be unfindable is transient data deliberately discarded** per §3.10 (uncited
> task-class symbols / raw task-bytes evicted at window close, §3.10.5–§3.10.6).
>
> "Locatable" is **mechanism-agnostic**: findable by *some* recall path — symbolic, embedding
> coarse/fine tier, summary tree, or the exact lexical / flat-scan tier.
>
> Completeness holds **continuously, not eventually**: durable content momentarily absent from one
> index — e.g. scrolled out of the assembly window but pending its *asynchronous* fine-tier
> embedding flush — must be covered by another mechanism in the interim (the completeness floor
> below), so there is no lag-window dead zone.
>
> **Enforcement.** A measurement harness must assert this completeness directly and may **NOT**
> model any "by-design blind spot" or acceptable lag in which durable content is unfindable — such a
> window is a runtime correctness defect, never a behavior to certify.
>
> **Validation scope.** A run without an embedder exercises neither the fine tier nor its lexical
> completeness floor, so it proves this claim for the symbolic path only. The embedding-flush-lag
> half is **accepted only when the embedder-enabled completeness rung runs green** (§9.1 open gate
> requirement).

Three layers, run as **parallel signals**, not a cost cascade:

1. **Symbolic Jaccard** over the symbol index — high precision, low recall. Cheap.
2. **Embedding cosine** over an in-memory, **hierarchical** embedding index — the primary recall
   scan, drift-robust. A coarse tier (one vector per thread body) picks threads; a fine tier
   (per-turn-excerpt chunk vectors) picks content within a surfaced thread. The per-turn cost is one
   embedding call to vectorize the query.
3. **Model judgment** on surfaced candidates — expensive confirmation that cuts false positives
   before a recall is offered.

Symbolic Jaccard is never a pre-filter for the embedding scan (under realistic vocabulary drift it
would discard most genuine matches).

Embedding recall (layer 2) is **opt-in**: enabled when `config.toml` pins an `[embedding]`
provider/model (§8.2.2). With no embedding model configured, recall runs symbolic-only — never a
hard failure.

**Embedding index — granularity, maintenance, persistence.** One uniform mechanism serves all
not-in-context recall; there is no separate intra-thread path. Every thread is an *indexed body*
(coarse thread vector + per-excerpt chunk vectors); an *active* thread additionally carries a live
FIFO window (§2.3) that is in the working set and is *not* embedded. Turns scrolling out of the FIFO
become index material. Recall runs over all indexed bodies; including the **engaged thread's own**
indexed body gives intra-thread recall of its scrolled-out early content. The live FIFO window is
never a candidate.

*Maintenance.* Incremental and **asynchronous**, off the turn's critical path: recall reads never
block on indexing, and a stale in-flight embed never overwrites a fresher vector. Two flush triggers
feed the one index: an **embedding-debt cap** (flush after N turns of accumulated scrolled-out
content; N is a §9 calibration window, default 16) and the **dormancy transition** (flush a thread's
remaining debt when it decays out of the working set, so its full body is indexed before it becomes
a pure recall target).

*Persistence (required).* The vectors are a **derived, persisted cache** (§2.1 `.recall-cache/`)
keyed by a content-staleness marker (body/chunk hash): gitignored, regenerable (rebuild-on-miss),
written through by the indexer, and loaded at startup so a session re-embeds only what changed —
**O(changed), not O(all)**.

*Long threads.* Intra-thread recall on very long single threads must work with **no performance
decay**, validated in the §9 acceptance simulation by a long-running-thread workload with explicit
decay measurement (index size, per-query cost, retrieval latency) as thread count *and* per-thread
length grow into the thousands.

*Completeness floor.* After the fine-tier scan, the automatic intra-thread path searches the engaged
thread's not-yet-flushed tail lexically against the query symbols, over a fixed bound that covers
in-flight and queued flushes, and unions the hits into the intra-thread result — no per-query
embedding, no unbounded scan. Accepted residual: a single transient embed failure leaves a gap for
that batch until the next successful flush republishes it (healed by the dormancy-demotion flush or
the next session's startup recovery); the guarantee is against async-flush *lag*, not failed
flushes. Summary-tree descent over a long thread is an approximate recall heuristic; exact recall is
guaranteed by the separate exact lexical / flat-scan tier.

**Recall surface.** At turn close the merged candidates are logged per layer (`spine.match-fire` /
`spine.embed-match-fire` / `spine.intra-match-fire`) and, when an interactive front end is attached,
the top 3 are taken forward; three is the surfaced cap across the auto and ask bands TOGETHER. With
no interactive front end attached, recall stays log-only, including the auto band.

**Banding.** Each surfaced candidate falls in exactly one band, by its score against its OWN tier's
threshold (layer 1 is a Jaccard set overlap, layers 2/3 are cosines):

- **AUTO** — score ≥ `recall.symbolic-auto-threshold` (0.65) for a symbolic-scored candidate, or ≥
  `recall.cosine-auto-threshold` (0.75) for an embedding- or intra-thread-scored one. The thread is
  promoted through the SAME fetch path an accepted candidate takes, with no prompt and ONE committed
  line, `recalled thr_N: <display name> — <gist>` (the standing `thr_N: display — gist` topic
  reference, so the fetch is verifiable against `/topics`). Logged `recall.accept ... ack=auto`; the
  `recall_fires` increment (§2.2) applies as for any accept.
- **ASK** — surfaced but below the auto bar. ONE offer block per turn; per candidate: the thread's
  §2.2.2 display name (never a bare `thr_N`), a gist (the spine summary, else the projected anchors
  — never blank, never a raw id), WHY it matched (the matched symbols, or for an intra hit the
  matched turn numbers), and a short tier label with the score. The question states what accepting
  does: one candidate → `pull into context? [y]es / [n]o`; several → a numbers/all/none pick-list
  over the enriched lines. No decline-reason question in this mode.
- **Below the surface threshold** — never surfaces.

Under `banded`, a candidate already resident in Layer B is answered by the runtime itself:
`recall.decline reason=already-known`, no prompt, no line. An intra-thread hit is exempt (its
subject is the engaged thread's early content).

**Accept suppression (the intra-thread re-fire).** An accepted intra candidate is not offered again
while it is resident: the runtime records the accepted turn-excerpts as in-window for the engaged
thread, and the recall stack drops them from BOTH the fine tier and the lexical completeness floor.
This is not a completeness exception — the content is in the working window — and the mark ends when
the thread is demoted out of Layer B. Session-scoped; nothing is persisted.

**Escape hatch.** `recall.ack-mode: banded | always` (§2.6.1), default `banded` — the behavior
above. `always` asks about every surfaced candidate: one offer (a numbered pick-list, `a`=all,
`n`=none) followed by a decline-reason question, and nothing is fetched without an ack.

Every candidate the runtime touches is logged: `recall.accept` (with `ack=human|auto`, §2.8) or
`recall.decline` (with a §4.3 reason).

**Lifecycle-aware symbolic scorer.** The match target is `T(thr) = anchors ∪ {h.normalized}`, so a
`superseded` symbol (retained in `history_symbols`, §2.7.4) stays matchable. The Jaccard *numerator*
is a **weighted sum**: each matched symbol contributes `1.0` if it is `active` in that thread,
`recall.superseded-weight` (default **1.0**) if it is `superseded`. A symbol that is also a
projected anchor counts as active. The superseded **label is canonical** — read from the thread's
frontmatter `lifecycle` (§2.3), not the derived index. The denominator (union size) is unchanged; at
weight 1.0 the score is byte-identical to the plain-count scorer. `recall.superseded-weight` is a §9
calibration window, lowered below 1.0 only if the sim's `superseded_precision` /
`abandoned_premise_recall` show abandoned threads crowding out focused ones.
`symbols.jsonl.superseded_in` (§2.4) is the cheap-scan candidate-filter surface ("which threads
abandoned premise X", O(1) per query symbol); the scorer does not consume it.

### 3.5 Closure flow

Trigger detection (engagement decay or `/done`) → curator-drafted summary → resolution →
frontmatter + spine update → Layer B/C eviction. Deep cold archival (§3.8) follows closure under
spine cardinality pressure.

Engagement decay fires when `last_engaged` exceeds `engagement.decay-turns` OR
`engagement.decay-time` (§2.6.1): `decay-time` catches extended user absence, `decay-turns` catches
low-frequency engagement in a busy session.

The curator-drafted summary targets 100–150 chars: summary + anchors must fit the Layer A1 budget at
realistic spine cardinality while letting the model recognize prior engagement without fetching the
body.

**Routine closures are auto-accepted; exceptions are batched at boundaries.** At the decay-detection
scan each candidate is classified:

- **EXCEPTION** — the curator draft is empty or failed, OR the thread is anchor-rich (≥ 6 anchors),
  OR long-engaged (≥ 15 engaged turns, `turn_count`). These criteria are a calibration
  window pending real-use data.
- **ROUTINE** — everything else.

A **routine** closure is applied without asking: the curator's summary, `resolution=resolved`, the
same frontmatter + spine write and Layer B/C eviction an accepted ack performs, and ONE committed
line, `closed thr_N: <display name> — <summary>` (the id leads because `/back-to <thr_id>` is the
revision path). It rides the turn transaction already open around turn close (§4.5.8) and adds no
commit path of its own. **The revision path for a wrongly-summarized routine closure is
`/back-to`**: re-engaging returns the thread to `active`, and its next close re-drafts the summary.

An **exception** closure is **queued** silently: no curator call, no prompt, one `retire.pending`
event. The queue is **derived, never stored** — a pending closure is a decay-eligible thread that
classified as an exception — so it is crash-safe and correct across restarts. It is drained with the
full interactive resolver at **boundaries only**: at clean session exit (before the session-close
checkpoint), and on demand via `/closures` (§4.2). At session **start** a non-empty queue is
ANNOUNCED in one committed line (`N closure(s) pending review — /closures`) and never prompted.

**The offer names the topic.** Wherever the interactive resolver runs — the boundary drain, `/done`,
or `always` mode — each closure offer shows `idle topic thr_N: <display name> — <gist>` above the
curator's draft summary, resolved by the §2.2.2 fallback chain (description → summary → projected
anchors).

**`/done` is always fully interactive** in both modes. A draft that **fails or comes back empty is
never auto-accepted**: it logs `retire.curator-error` and is retried at the next scan. (A
persistently failing curator therefore leaves its thread un-closed rather than queued.)

**Escape hatch.** `closure.ack-mode: auto | always` (§2.6.1), default `auto` — the behavior above.
`always` prompts interactively in-session for every decayed thread; nothing queues under it.

**Ack-quality instrumentation.** The canary is **ack-edit-rate**: the
REPL ack UI offers an `[e]dit` choice (§4.2 `/done`, the `/closures` drain), and `retire.ack` (§2.8)
records `edited=yes|no` for EVERY acked outcome (retire and wip) — `yes` only when the submitted
summary differs from the curator draft. `retire.ack` also carries `ack=human|auto`, and the rate is
computed over `ack=human` lines only (an auto-accept is unedited by construction and would drive the
rate toward zero). Auto-accepts are still recorded (`retire.ack ... ack=auto` plus
`retire.complete`). *Evaluating* the rate — distinguishing "drafts are perfect" from "the user
stopped reading", and judging the auto/exception split (a miscalibration shows as a zero human
edit-rate while auto-accepts are corrected by hand with `/back-to`) — needs a real human and is a
§9.1 category-3 known-unknown until then.

### 3.6 Fallback dissection

Budget-pressure trigger → dissector LLM clusters the oldest content → batch retirement → ack flow.

### 3.7 Cross-project digest maintenance

`projects/<id>/digest.json` regenerates whenever any thread in the project is updated. Each
project's digest is capped at `cross-project.digest-per-project-bytes`; recent anchors are selected
from the N most-recently-engaged threads.

### 3.8 Deep cold archival

Threads are archived off-spine under spine cardinality pressure and recovered by explicit fetch,
using the autonomic git layer (§8.3) rather than a bespoke archive format.

**Barrier-homed.** The archival *drain* fires **only at the day barrier** (§4.5.8 B1), against the
**primary** git DB, so primary stays barrier-exclusive (§4.5.8 INV-5). Standalone archival is
crash-recoverable via the same roll-forward completion (§4.5.8 (6)).

#### 3.8.1 Mechanism

A thread is a **directory** (`threads/thr_<n>/` = `thread.md` + `turns/` + `files.json`, §2.3).
Archival drains a **batch** of the coldest retired threads at once; a batch archive must achieve
three results:

1. **Membership recorded before deletion.** One `archive/index.jsonl` entry per thread is durably
   appended — with empty `commit_hash` but full `parent_commit_hash`/`tree_hash`/`original_path`/
   spine snapshot — before any thread directory is removed. This entry set is the deterministic
   worklist a crash-completion consumes (§4.5.8 (6)).
2. **Bytes reachable after.** A capture commit in **primary** pins the to-be-archived directories
   (and lands the day's cumulative content in primary, which makes the barrier's daily-DB nuke safe,
   §4.5.8 INV-3) and becomes the *parent* of the deletion commit, regardless of the per-turn daily
   cadence (§3.11). Each directory's git **tree hash** is captured there; recovery verifies against
   it. The deletion removes all spine records in **one** rewrite, regenerates derived state **once**
   for the batch, and is validated for spine integrity (pre-state) and derived freshness
   (post-state).
3. **Bounded commits per batch.** The drain produces a bounded number of commits per batch, not one
   per thread; a final stamp commit records the deletion commit's hash `H` as `commit_hash` in the
   membership entries.

The completed archive index entry:
```jsonl
{"thr_id":"thr_42","commit_hash":"<H>","parent_commit_hash":"<capture commit; H's parent>","tree_hash":"<tree sha of threads/thr_42/ at the capture commit>","archived_at":"<RFC3339>","original_path":"threads/thr_42/","spine_summary":"<summary at archival time>","anchors":["..."],"project":"prj_3","recovered_at":""}
```
`spine_summary` and `anchors` are copied verbatim from the spine record at archival time.
`parent_commit_hash` is the capture commit whose tree still holds the thread bytes; recovery
restores from it directly rather than walking `commit_hash`'s first parent (which picks the wrong
lineage on a merge commit). An entry lacking `parent_commit_hash` is recovered via the deletion
commit's first parent. `recovered_at` is empty until the thread is recovered (§3.8.3).

The archive index is **canonical** (§2.1), sorted by `thr_id`. Losing it loses no data: the thread
bytes stay reachable from the capture commit's tree.

#### 3.8.2 Storage properties

- Git's content-addressed, delta-compressed object store holds multiple versions of a thread file
  compactly.
- `git gc` is safe to run autonomically: archived blobs remain reachable via the deletion commit's
  parent. Personant never rewrites history; deep-cold reachability depends on this.
- `git gc --aggressive` may run on a schedule when pack-file size matters.

#### 3.8.3 Recovery

To recover archived thread `thr_<n>` (`personant archive recover <thr_id>`):

1. Look up the archive index entry by `thr_id` (refused if absent).
2. Resolve the deletion commit's **parent** and restore the full `threads/thr_<n>/` **subtree** from
   it — every blob under the directory.
3. Recompute the recovered directory's git **tree hash** and verify it against the stored
   `tree_hash`. Mismatch ⇒ delete the partial restore and abort with an integrity error; a
   partially-recovered thread is never written onto the spine.
4. Append a fresh spine record reusing the **same** `thr_<n>` id (recovery never mints a new id);
   `state = wip`, `last_engaged` updated. Anchors/summary come from the **recovered frontmatter**,
   not the index snapshot.
5. Stamp `recovered_at` on the index entry and commit the recovery (worktree restore + spine +
   index) to the home git tree.

The index entry is **kept** as a forensic breadcrumb (`recovered_at` marks it historical).
`personant archive list` / `personant archive recover <thr_id>` surface deep-cold history.

**Two recovery surfaces, different strength.** Archived *threads* recover from Personant's **own**
home git, which it never rewrites — an *unconditional* guarantee. Aged-out *file versions* recover
(§3.9.1) from the **user's** workspace git — a *conditional* guarantee, gated by a reachability
check of the blob at `hash:path` that also refuses to age an unreachable hash.

#### 3.8.4 Recall treatment of archived threads (v0.1: off-recall)

Archived threads are **off the live recall surface** in v0.1: the §3.4 scan covers the spine +
per-thread frontmatter, and an archived thread has neither. The `archive/index.jsonl`
anchors/summary are a forensic + `personant archive list/recover` surface, not a live match surface;
deep cold is recovered by **explicit fetch**. Archive recall is out of scope for v0.1 (the index
already carries `anchors` + `spine_summary`).

**Measurement consequence (§9).** Recall-fidelity metrics exclude an archived-recoverable expected
match from the *live*-recall score rather than counting it a miss (`recall_archived_recoverable`).
`recall_unexplained_absence` — an expected thread off-spine **and** absent from the archive index —
is an integrity signal that must stay 0, since archival is the only legitimate way off the spine.

### 3.9 Working-set content dedup

When file content (or another large blob) appears in the working context multiple times — read,
modified, re-read — the runtime de-redundifies it. **Persistent storage and live context composition
are separate concerns** sharing the same primitives: content-addressed identifiers, diffs, anchor
literals.

#### 3.9.1 Persistent storage (thread file body, log)

The full chain is preserved, **anchored on the current state**:

- The current content is stored as a **literal**.
- Each prior version is stored as a **reverse-delta**: the unified diff that transforms the newer
  state back into it. When a new version arrives, the outgoing literal is re-encoded as a
  reverse-delta and the new content becomes the literal.
- An anchor literal every K-th version back through history (default K = 10, directive
  `dedup.anchor-cadence`) bounds reverse-delta chain length.
- A reverse-delta ≥ `dedup.diff-literal-threshold` (default 0.7) of the literal size is stored as a
  literal instead.

**Git-minimization bound.** Once a tracked file is committed to the project's git repo, the
pre-commit reverse-delta chain duplicates what git holds **for as long as the commit stays
reachable**. After a retention window (`dedup.chain-retention-turns` turns OR
`dedup.chain-retention-days` days, whichever trips first) the chain is aged out — **only after the
runtime confirms the committed blob is reachable in the workspace repo** (read-only check of the
blob at `hash:path`, §6.1.3). If it is not reachable (amended, rebased, gc'd, moved workspace),
**aging is refused and the chain retained**, logged as `dedup.chain-age-refused` (thread, path,
hash, reason). Recovery of an aged-out committed state goes through the file-version recovery op,
which reads the blob read-only from the workspace tree (a still-retained chain recovers with no git
access). Uncommitted edit history lives only in the retained chain and is unaffected.

**External-git dependency.** The workspace repo is the user's (§6.1.3 — Personant never mutates
workspace git or monitors its history), so recovering an aged-out committed state depends on the
user not having orphaned the commit. Personant's contract: it never *drops* a chain while the commit
is unreachable, so no content is lost *by aging*. If `git` is unavailable, every chain is retained
and the condition is logged once. The same reachability predicate gates both surfaces: recovery
succeeds exactly when aging would have been permitted.

**Symbol lifecycle for file-edit events.** `fs.read`, `fs.write`, and `fs.commit` deltas are
task-class (§3.10.1): their symbols enter the staging buffer and reach `symbols.jsonl` only if a
decision delta cites them within the citation window (K = 3, §3.10.3).

#### 3.9.2 Live context composition (working window)

- **Literal current state** at the most-recent position.
- **N most-recent diffs** in literal form (default N = 3, `dedup.live-diff-window`).
- **Older states** replaced with content-addressed identifiers: `[content #<hash> — see most-recent
  position]`, preserving causal/temporal ordering.

When N is exceeded, the oldest diff in window collapses to its identifier; its content remains
recoverable from persistent storage.

#### 3.9.3 Diff format

**Default: unified diff.** **Fallback: natural-language descriptive diff** ("added a line `foo`
after line 12; removed lines 20–22"), which the runtime may switch to per-diff where unified diff is
hard for the model to apply (complex multi-region changes, very long files). The directive
`dedup.diff-format` sets the global default; per-thread override is possible.

#### 3.9.4 Identifier-only references

Identifier-only references (no nearby literal) are used when cognition is about *meta* (this file
was mentioned or modified) rather than content, or when the content has left the live window and
forensic recovery is the user's path.

**For active reasoning over content, identifier-only is insufficient.** A file recently the subject
of `fs.read` or `fs.propose_promote` in the engaged thread's tool-call history keeps its literal in
the active layers (B / C), regardless of how many references precede it.

### 3.10 Transient-data lifecycle

Task-class context (tool outputs, shell captures, file reads) is valuable for a span of turns and
then worthless. This two-stage mechanism keeps task-class content discardable while preserving the
symbols it produces when — and only when — a decision delta cites them.

#### 3.10.1 Provisional classification at delta time

Each delta entering the §3.0.2 chain is assigned a **retention class** before any hook fires,
mechanically from its event source (§3.0.1):

| Source | Provisional class |
|---|---|
| `tool.result`, `user.shell-capture`, `fs.read`, `fs.write`, `fs.commit` | `task` (provisional-transient) |
| `user.prompt`, `model.response`, `thread.fetched` | `decision` (provisional-persistent) |
| unknown source | `decision` (safe default — unknown content is never silently dropped) |

The class governs where extracted symbols land (§3.10.2); the citation window (§3.10.3–§3.10.5)
confirms or discards them.

One user override: a `task`-source delta prefixed with the reference-material marker (`##` /
`/keep`) is reclassified to `decision` at emit time (e.g. a paper ingested for ongoing reference).
The agent has no parallel override; agent-produced retention flows through decision-delta authorship
(§3.10.4).

#### 3.10.2 Staging buffer

Symbol extraction (§3.3) runs on every delta regardless of class. Where symbols land:

- **Decision-class delta:** the per-turn coalesce buffer; at turn close they commit to the thread's
  `history_symbols` and to `symbols.jsonl`.
- **Task-class delta:** the **staging buffer** — a map keyed by normalized symbol form (§2.7.2).
  They do not enter `history_symbols` or `symbols.jsonl` yet.

The staging buffer persists **across turns** (the coalesce buffer resets each turn). Each entry
holds the normalized form (also the key), the raw surface form (for display on promotion), the
symbol's source (§2.7.3), and the turn at which it was staged. A later task-class delta carrying the
same normalized form overwrites the entry and refreshes its staged turn, extending the window.

#### 3.10.3 Citation window K

A staged symbol is eligible for promotion from its staged turn S through S + K − 1 inclusive.

**K = 3** (same-turn citation, next-turn citation, and one more turn for an intervening tool call or
clarification). K is a **calibration target** earned by the §9 recall-fidelity simulation; it is
currently fixed at 3, and its future directive key is `staging.window-turns`.

#### 3.10.4 Promotion

When a decision-class delta is processed, each §3.3 extraction candidate with a matching staging
entry (normalized-form match only — no fuzzy matching):

1. is **promoted**: moved from the staging buffer into the coalesce buffer;
2. commits at turn close to `history_symbols` and `symbols.jsonl` normally, anchored to the decision
   delta that cited it;
3. carries only the symbol across: the raw bytes of the task-class delta that produced it are
   **not** promoted.

A promotion is logged as `staging.promoted` with `normalized`, `staged_at`, `cited_at`, and
`turn_span` fields.

#### 3.10.5 Window-close eviction

At the top of each turn (before any delta in the new turn), the runtime drops staging entries staged
before turn (currentTurn − K + 1). A nonzero eviction count is logged as `staging.evicted`. Eviction
is deterministic; no LLM is involved.

#### 3.10.6 What stays transient

**Never** written to persistent substrate:

- Raw bytes of task-class deltas (file content from `fs.read`/`fs.write`, tool output from
  `tool.result`, captured output from `user.shell-capture`, and the `fs.commit` pointer event). The
  event log records a short metadata summary instead (source, path, byte count). The §3.0.2 step-5
  logging step summarizes every source whose provisional class is `task`, so a newly-added
  task-class source inherits the minimization automatically.
- Symbols from task-class deltas not cited by a decision delta within K turns.

File content has a separate persistent home in the per-thread tracked-file store (§3.9.1,
`files.json`), not governed by this section.

#### 3.10.7 Why this matters

Without this lifecycle, every path or URL in a `tool.result` would inflate `history_symbols`, raise
Jaccard's denominator, and degrade §3.4 precision, compounding over a §9.1 simulation. It is a
prerequisite for a meaningful recall-fidelity measurement (§9).

#### 3.10.8 Content retention (keep/toss) — deferred precision layer

§3.10.1–.7 govern *symbol* promotion. **Content retention** — which retained turn-excerpts (§2.3)
survive on disk for §3.4 intra-thread recall — is the adjacent keep/toss decision. v0.1
**over-retains**: all decision-class excerpts are kept, and raw task-class tool output is never an
excerpt. The **precision layer** that trims the retained set is deferred to the sleep-cycle
keep/toss (ARCHITECTURE.md, Memory consolidation — the "sleep" cycle), with this settled mechanism:

- **Raw tool output → always transient**, deterministically, with no LLM involvement.
- **The agent's *re-presentation* of tool data → the durable candidate**, marked inline by the agent
  with a pinned closed-set marker — exactly one of `lifetime:transient` | `lifetime:durable`, at a
  fixed stripped position (metadata, never shown to the user). This is the one LLM judgment in the
  mechanism.
- **Fail-safe = durable.** A missing/malformed marker resolves to *preserve*. Compliance is measured
  in the inference-in-loop sim by the omission rate.
- The marker's primary job is **downgrading** a routine tool-data presentation to
  `lifetime:transient`: `durable = decision content − agent-marked-transient`.

The in-turn agent marker and the offline sleep-cycle keep/toss (outside v0.1 scope) are the **same
decision at two cadences**.

### 3.11 Substrate commit cadence

The substrate is backed by **two** git DBs (§4.5.8): a **daily** DB (`.git-daily/`, disposable
per-turn) and a **primary** DB (`.git/`, permanent day-grain career history). Policy: **per-turn
commit to daily, day-grain commit to primary**.

**Per-turn (daily).** Every turn commits its **scoped** write set to **daily** at turn close
(`Personant-Turn: <id>` trailer) — the ≤1-turn durability boundary of §4.5.8. Staging is scoped to
the turn's recorded write set (O(write-set), no tree walk); daily commits carry **no integrity
validation**, so per-turn cost stays flat. Structural changes (create/close a thread, create a
project) ride the per-turn daily commit and are folded into the day at the barrier.

**Per-day (primary).** The **day barrier** (§4.5.8 B0–B6, fired on new-day detection) writes exactly
**one** day-grain commit to primary (`Personant-Day: <N>`; an empty day still yields one) plus the
day's archival anchors (§3.8, B1). Primary is bounded to ~365 commits/year, and integrity gating
runs **once a day**: spine-integrity validation of the staged day-commit (B2) and a
derived-freshness assertion after the post-rebuild (B6). The daily DB is nuked and reborn at each
barrier (B4/B5).

**Checkpoint (session-close + mid-session structural) → daily.** Every checkpoint targets **daily**
with a full-worktree staging sweep that absorbs hand-edits and any content not yet scoped-committed
(normally a no-op). Primary is **barrier-exclusive** (§4.5.8 INV-5).

**gc cadence.** Packing is **count-triggered** by the daily DB's loose-object count, plus the
offline sleep cycle, which packs both repos (on the §9 day-off idle window). A default-off mid-day
re-baseline knob (§4.5.8 (5)) handles a long single session in which no barrier fires.

**Durability.** Content is durable to ≤1 turn at all times: the journal (§4.5.8) protects the
in-flight turn's raw bytes before any canonical write, and the scoped daily commit lands the turn at
close. §4.5.8 governs the crash protocol and mid-session recovery.

---

## 4. User surface

Three channels feed user input to the runtime: CLI commands (outside a conversation),
in-conversation slash commands, and shell escapes. The agent's tool surface is §6.

### 4.1 CLI command surface

Binary subcommands (see also §2.1):

- `personant init` — first-run scaffold; idempotent (see §8.1).
- `personant index rebuild` — regenerate derived files from canonical (§2.1).
- `personant index check` — validate derived files match canonical; non-zero on mismatch.
- `personant verify` — full structural validation.
- `personant ping` — send a single prompt to a configured provider and print the response
  (`--provider`, `--prompt`, `--model`, `--timeout` flags). Connectivity smoke test.
- `personant models` — list models available from a provider (`--provider` flag).
- `personant version` — print version identity (both semver lines, the home format this binary
  writes, the build stamp) plus the resolved home path and the format stamp found there. **Ungated**
  (§9.1): never runs the format gate, never runs startup recovery, never fails on a broken home.
  `personant --version` prints the version lines in one-line short form only.
- `personant search <query>` — symbol/text search across spine and threads.
- `personant deps <thr_id>` — show threads referenced by anchors of the given thread.
- `personant permissions list` — show accrued grants from directive files (§6.2.3).
- `personant log <date|range>` — query the event log.

Persistent flags on every subcommand: `--home` (override `$PERSONANT_HOME` for the invocation) and
`--allow-newer-home` (open a home whose on-disk format exceeds this binary's — §9.1; leaves a stderr
warning and a `system.home-format-override` event).

### 4.2 In-conversation slash commands

Slash commands begin with `/`, are intercepted by the runtime before any prompt construction, and
bypass the LLM's tool surface.

**Vocabulary bridge.** User-facing surfaces say **topic**; a topic is stored as a **thread**
(`thr_N`). "Thread" remains the term below the user surface — data model, algorithms, the §2.8 event
log, and the id scheme, which is rendered to the user unchanged because they type it back. `/help`
opens with the one sentence that names both. There is no `/threads` alias.

| Command | Purpose |
|---|---|
| `/topic <name>` | force a new topic with the given working name. The first word is read as a subcommand (see `/topic rename`), mirroring `/project`'s parse; a topic cannot be CREATED with a name whose first word is `rename` |
| `/topic rename <new-name>` | rename the ACTIVE topic's §2.2.2 display name. **Display-cosmetic**: the id, the anchors, `history_symbols`, the §2.8 `thr=` details, recall matching and every stored turn excerpt are unchanged. Writes `description` (and `summary` when it duplicates `description` — the shape `/topic` creation leaves) to both the canonical §2.3 frontmatter and the derived spine record. Old names are NOT aliases: `/back-to <old-name>` stops resolving. Duplicate display names are permitted; the ambiguity surfaces in the name resolver. Not a §3.11 structural change, so it rides the session-close commit; logged `thread.renamed` |
| `/topics [all]` | list the active project's topics. Grouped ENGAGED / PAUSED / DORMANT / RECENTLY CLOSED, recency-sorted within each group, one line per topic in the §3.4 topic-reference shape: `thr_N`, display name, gist, relative age. Recently-closed topics are in the default view, with the `/back-to` hint, because an auto-accepted §3.5 closure is revised that way. The default view is bounded (20 topics, then a count of what it withheld); `all` lifts the bound and the closed-window cutoff |
| `/done` | request closure ack on the active topic (§3.5); always fully interactive |
| `/closures` | drain the §3.5 pending-review queue now. The same drain runs at clean session exit. An empty queue says so |
| `/pause` | mark active topic as `paused` (§2.2.1) |
| `/resume` | resume a paused topic |
| `/back-to <thr_id>` | re-engage a closed topic — the revision path for an auto-accepted closure; see `/topics` |
| `/no-revisit` | tighten threshold on the most recent recall surface (§3.4) |
| `/project` | print active project (id, name, root path, remote URL if any) |
| `/project switch <name-or-id>` | switch active project to a known one |
| `/project rename <new-name>` | rename the active project; updates `name` only (id, path, remote URL unchanged) — see §4.5 |
| `/cd-project <path>` | set active project root to `<path>` (see §4.5) |
| `/model [<model-id>\|<provider>/<model-id>]` | switch the session's LLM. The **bare form reports** the active `provider/model` plus a usage hint and mutates nothing. A bare `<model-id>` switches model within the current provider; `<provider>/<model-id>` switches provider AND model, and the provider must be an `inference` pool entry — a `search` entry is refused by kind, as at session open. **Ambiguity rule** (model ids may contain `/`, e.g. `org/name`): the text before the FIRST `/` is a provider name **only when it names a provider in `providers.toml`**; otherwise the whole argument is a model id on the current provider — consistent with §8.2.2's split-on-first-`/`. **Verification mirrors session open**: a model absent from a non-empty `/models` list **REFUSES** the switch, naming what the provider offers, and leaves model, provider and client untouched; an unreachable or model-less `/models` warns and switches **UNVERIFIED**. **Session-scoped, never serialized:** `[chat] defaultModel` remains the startup default and no home state records a "last model used" — the printed confirmation says so. Takes effect on the next turn; recorded as `model.switched` (§2.8) |
| `/thinking [on\|off]` | show/hide the model's live reasoning, dimmed; bare form reports the state. Session-scoped override of `[chat] showThinking` (§8.2.2) — never written back to `config.toml` |
| `/terminal-setup` | configure the hosting terminal for Shift+Enter multi-line input (§4.3.1). Detects the host from `$TERM_PROGRAM`, falling back to `$LC_TERMINAL`, reports what it found, and acts: **Zed** is the one recipe that is a file write, and it is offered — `~/.config/zed/keymap.json` gains the `shift-enter` entry, textually, never by parse-and-rewrite. Every other known terminal PRINTS its recipe and writes nothing; an unrecognized terminal gets the whole list. Because the write lands outside `$PERSONANT_HOME`, it is confirmed EVERY time (never `ack=auto`), the prior bytes are copied to `keymap.json.personant-bak-<timestamp>` first, a keymap that already binds `shift-enter` in a `Terminal` context is left untouched (idempotent), a file whose structure defeats byte-safe insertion is REFUSED with the snippet printed for manual paste, and a non-interactive session prints and never writes |
| `/stats` | runtime stats (active topics, layer fill, recent recall events, etc.) |
| `/version` | version identity plus the on-disk format of the home this session opened (§9.1) |

### 4.3 Decline categorization UI

Every decline is recorded with a reason from a fixed enum: `not-relevant` / `wrong-project` /
`already-known`. The enum favors recall-tuning signal (why a candidate missed); it may be retuned
when the recall accrual loop is built.

**Who supplies the reason.** Under the default `recall.ack-mode: banded` the user is NOT asked to
categorize a decline. A banded decline logs `not-relevant`, except that a candidate already resident
in Layer B is declined `already-known` by the runtime itself. Under `recall.ack-mode: always` the
reason prompt is restored.

### 4.3.1 REPL line editing and history

Line editing is provided by the runtime itself, not a third-party line editor: a mid-turn key must
be observable (§4.3.3) and the editing path must be testable without a live terminal (§9).

Required behavior:

- `←` / `→` cursor movement within the current line.
- `↑` / `↓` command history; persistent across sessions in `<Home>/history` (operational, not
  git-tracked). Newline-delimited text.
- Standard readline shortcuts: Ctrl-A / Ctrl-E (line start/end), Ctrl-B / Ctrl-F (char
  back/forward), Ctrl-W (kill word), Ctrl-U (kill line), Ctrl-K (kill to end), Home / End, Delete.
  An Alt-modified rune is inserted as its base character.
- History deduplication (no consecutive duplicate entries).
- History size cap (configurable; default 1000 entries).
- **Multi-row rendering.** A line longer than the terminal width wraps and stays editable, repainted
  as a block rather than scrolled horizontally. Rendered rows are **capped** (default `min(10,
  rows−2)`) with the excess collapsed to a placeholder and the **whole buffer retained**. Submitting
  sends the full buffer regardless of what is drawn.
- **Multi-line input.** The buffer may hold line breaks. **Alt/Option+Enter inserts one**
  (universal); **Shift+Enter inserts one where the terminal can say so** (below); **bare Enter
  submits**, returning the whole buffer with its newlines intact. An embedded newline is a **hard
  row break** in the rendered block, counted by the row cap. `←`/`→` cross a newline as one rune and
  Backspace/Delete remove it as one rune. **Home / End / Ctrl-A / Ctrl-E operate on the logical line
  the cursor is on**; the kills (Ctrl-U / Ctrl-K) stay **buffer-scoped**. History carries multi-line
  entries: the on-disk file is one entry per LINE, so a newline inside an entry is written `\n` and
  a literal backslash `\\` (injective encoding), and `↑` recall renders the whole block.

  **Terminal reality.** Legacy terminal input encodes Enter and Shift+Enter as the SAME byte
  (`0x0D`); the modifier is visible only under an enhanced keyboard protocol (kitty CSI-u: `ESC [ 13
  ; 2 u`). Personant DECODES that sequence but does not REQUEST the protocol, so Shift+Enter works
  only in a terminal configured to send a distinguishable sequence. `/terminal-setup` (§4.2)
  performs the recipe below that is a file write (Zed's) or prints the others; for an unrecognized
  terminal it prints this list verbatim.

  - **iTerm2** — nothing to configure: **3.5 and newer sends `ESC[13;2u` natively**, which is the
    sequence personant decodes. On an older build, map it by hand — Settings → Profiles → Keys → Key
    Mappings → `+`, press Shift+Enter, action **Send Escape Sequence**, escape `[13;2u`.
    (Equivalently, action **Send Text with "vim" Special Chars** with text `\e\r`, which sends the
    universal Alt+Enter form.)
  - **Zed** — in `keymap.json`: `{"context": "Terminal", "bindings": {"shift-enter":
    ["terminal::SendText", "\u001b\r"]}}` — that is `ESC CR`, the universal Alt+Enter form, in JSON
    escapes. `/terminal-setup` installs exactly this entry, with a backup and a confirmation.
  - **VS Code** — in `keybindings.json`: `{"key": "shift+enter", "command":
    "workbench.action.terminal.sendSequence", "args": {"text": "\u001b\r"}, "when":
    "terminalFocus"}`
  - **WezTerm** — in `~/.wezterm.lua`, inside the keys table: `{key="Enter", mods="SHIFT",
    action=wezterm.action.SendString("\x1b\r")}` (WezTerm speaks the kitty protocol, but only when
    the application REQUESTS it, and personant deliberately does not — so the binding is needed
    there like anywhere else.)
  - **Anything else** — Alt/Option+Enter already works unconfigured. On macOS, Terminal.app needs
    *Use Option as Meta key* for the Option form.

- **Editable pre-filled default.** A question may open with text already in the buffer, cursor at
  end — the §4.3.3 retraction re-offer and the `/done` summary edit both use it.
- **A question is part of the read**, so the editor's repaint never erases it. A single-key answer
  returns without Enter; a first keystroke outside the set becomes the first character of a normally
  edited line.
- **Non-interactive sessions** (piped stdin, the harness, tests) install no terminal mode, run no
  key reader, and read lines from the stream — output is byte-identical to a stream-only run.

### 4.3.2 Turn progress indicator

A turn has two output-silent windows — before the model's first token, and between the last token
and the next prompt (§3.4 recall, the §3.5 closure scan, the turn commit, working-set save). The
REPL renders a **phase-labeled** indicator across both. The phase vocabulary is fixed:

| Phase | Window |
|---|---|
| composing context | §3.0 delta chain + working-set composition |
| waiting on the model | the LLM round-trip (first-token latency) |
| re-issuing request | a §5.5 fetch or a §3.3 recovery re-prompt aborted the stream |
| running `<tool>` | §6.1 tool execution between two model streams |
| searching memory | the §3.4 recall stack (the embedding round-trip) |
| closing turn | response journal, engagement, closure scan, commit, save |

`running <tool>` is a FAMILY: it carries the tool's name (never its arguments). A round with several
calls summarizes (`running web.search +2 more`) so the line cannot wrap. The family is abortable per
§4.3.3.

Requirements:

- Phases are a presentation signal only — never journaled, logged, or committed, and nothing
  branches on them.
- Rendering is **terminal-gated**. On a non-terminal stdout the indicator emits zero bytes. On a
  no-ANSI terminal (`TERM=dumb` or unset) it degrades to **one committed line per occupancy**: the
  first draw after a release prints, further draws are dropped until the indicator is released
  again. That line carries the animation glyph, and the heartbeat keeps ticking (accepted).
- **Deferred reveal.** Nothing is drawn until the wait passes a short reveal threshold. A phase
  announced during the delay stays owed: the first heartbeat past the threshold draws it, once, only
  onto a line nothing else has taken. After that, heartbeats are redraw-only.
- **A heartbeat may only redraw a frame that is still on screen.** An announcement (new phase, or
  the abort hint appearing) claims the slot unconditionally; the animation tick must not.
- Content always wins the terminal: the first response byte — and any interactive offer prompt (§3.4
  recall, §3.5 closure) — retires the indicator and clears its line before writing. The indicator is
  restored for the close window afterwards (turn close re-announces its phase). While a **read** is
  in flight the indicator writes nothing.
- The line is cleared on every exit: normal completion, per-turn error, and both interrupt paths
  (§4.3.3 clean shutdown and the forced quit). The forced path clears first.
- While the turn is abortable (§4.3.3) the label advertises the abort key, appearing and
  disappearing **with** the abort window. The rendered line is truncated to the terminal width less
  one column.

### 4.3.3 Interrupt and abort keys

Two keys, two meanings: the user can abandon a turn without losing the session.

The session terminal mode delivers `Ctrl-C` as a **decoded key, not a signal** wherever the front
end owns the terminal, so its disposition depends on the editor's buffer. There is no single-press
exit.

| Key | At the prompt, line non-empty | At the prompt, line empty | During a turn |
|---|---|---|---|
| `Ctrl-C` | **clears the line**; the session survives and the cleared text does not enter history | first press **hints**; a second CONSECUTIVE press ends the session, cleanly and silently | abandons the turn, then the same clean shutdown, with a one-line notice |
| `Ctrl-D` | delete-forward | ends the session (end of input) | n/a — nothing is reading the line editor |
| `Esc` | nothing — swallowed | nothing — swallowed | aborts the turn and returns to the prompt; the session continues |
| `Ctrl-Z` | suspends cleanly; `fg` returns with the typed line intact | as at left | suspends cleanly; `fg` returns to the still-streaming turn |

- **The `Ctrl-C` double press must be consecutive.** Any other key spends the pending press and
  takes the hint down, and a press that cleared a line resets the count.
- A **second `Ctrl-C` during shutdown** forces an immediate exit, restoring the terminal first.
- The clean-shutdown path (the final recall-index flush, the §3.11 session-close checkpoint, the
  session-end log) completes even when entered by `Ctrl-C`, so a `Ctrl-C` exit loses no state.
- The shutdown notice is a warning, printed only when a turn was actually in flight; an ordinary
  quit prints none.
- **An Esc abort leaves the substrate exactly as a turn that never happened**: no open §4.5.8 turn
  scope, so the next launch opens quiet.
- **Esc means "retract my input", not "stop generating".** No partial response is kept, at any point
  relative to the model's first token.

**The retraction rule (normative).** A retracted prompt does **not enter memory**; only a deliberate
re-submission puts its text there. Consequences:

1. **Session rollback.** The `user.prompt` delta has already run (§3.0 chain step 1) by the time Esc
   is pressed. A pre-canonical abort restores the session-volatile runtime state — turn counter, the
   §3.10 cross-turn staging buffer, Layer B/C membership, history, recall-surfaced marks, embedding
   debt — to its pre-turn value (otherwise, e.g., a staged task-class symbol cited by the retracted
   prompt would be permanently consumed). The rollback is in-memory and must not perturb §4.5.8
   write ordering. Post-canonical failures do **not** roll back; recovery owns them.
2. **Getting the text back.** The aborted input is re-offered at the next prompt as an **editable
   default** (cursor at end), and is in `↑` history. Neither is memory.
3. **No durable full-text artifact.** The abort record is one §2.8 event line —
   `system.turn-aborted` with turn id, phase and byte count. Not the text.

**On-screen honesty.** If the abort lands mid-stream, the REPL prints one terse, visually distinct
marker showing that the partial response on screen is not in memory.

**Abort window.** Esc cancels the turn's context, and a cancelled substrate op fails the turn — safe
only while the turn is **pre-canonical** (§4.5.8): before the first canonical write, the abort
releases the recovery scope; after it, a failure leaves the scope open for recovery. The abort
window closes at the first non-pre-canonical phase (`closing turn`), and the front end gates on an
**allow-list** of abortable phases, so a phase added later is non-abortable by default. The §6.1
`running <tool>` family is on the list (a tool round writes nothing canonical); cancelling the turn
cancels the running tool.

**Terminal mode and the single reader.** The terminal is held in one session mode for the whole
session, with one key reader from open to close:

- **`Ctrl-Z` suspends cleanly**: restore the entry mode, suspend, re-install the session mode on
  `SIGCONT`.
- **`SIGTERM`/`SIGHUP` restore the entry mode** before the process dies, with the conventional
  128+signal status. They are not swallowed.
- **Output newlines render correctly while input is raw:** a bare `\n` still implies a carriage
  return.
- **No competing reader during a child window** (§4.4.2): nothing in-process reads the terminal
  while a child owns it; reading resumes after the child is reaped.
- **Type-ahead survives every boundary.** An `Esc` typed in the gap after Enter, or buried in a
  burst mid-turn, reaches the abort window; text typed in the gap lands in the next prompt. A
  mid-turn prompt (§3.4 recall, §3.5 closure) takes keys without retiring the turn's reader.
- **Non-terminal sessions** (piped stdin, the harness, tests) install no mode, run no reader loop,
  and produce byte-identical output to a stream-only run. `Esc` is unavailable there.

### 4.3.4 Tool receipts

**Every §6.1 tool call leaves ONE committed line in scrollback**, so tool activity is verifiable on
screen rather than by asking the model. (The §4.3.2 `running <tool>` label is ephemeral and often
never revealed.)

- One receipt per call, fired the moment the result returns, interleaved with the answer in event
  order. A call the user ABORTED (§4.3.3) produces no receipt.
- The line carries the tool NAME, a rune-bounded **gist of the decoded arguments** (`q="general
  relativity"`), textual arguments before scalar options, the OUTCOME, and the elapsed time.
- **Success reports SIZE, not an item count.**
- **An error receipt carries the error's first line**, so a refusal (e.g. `web.wikipedia` lacking a
  §6.1 contact) is visible on screen.
- It is **dimmed, persistent, and zero bytes off a terminal**. On a no-ANSI terminal it degrades
  (words instead of dimming) rather than disappearing.
- It is **NOT gated by `/thinking`**.
- **No new §2.8 event**: `tool.call` / `tool.result` / `tool.error` already record every call.

### 4.4 Shell escape (`$` and `#`)

The user can run shell commands from the personant prompt by prefixing their input. This is
**user-initiated** and does not extend the LLM's tool surface (§6.1.3 — the LLM has no shell tool).

#### 4.4.1 Prefix semantics

| Prefix | Meaning | Output → next-turn context | Logged |
|---|---|---|---|
| `$` | run in user's shell, fire-and-forget | no — streams to user's terminal only | yes (event line, no captured output) |
| `#` | run in user's shell, capture output | yes (subject to §6.5 byte cap) | yes (with captured output) |

`#` captured output flows through §3.3's deterministic symbol extraction like turn content.

**Delivery.** A `#` capture is buffered in the REPL and delivered as a **pre-prompt delta** (§3.0,
source `user.shell-capture`) on the NEXT turn, firing before that turn's `user.prompt` delta and
sharing its turn number. Multiple `#` captures before one prompt are all delivered, in order. The
buffer is **not durable**: a session that ends with captures unconsumed drops them (logged as
`user.shell-capture-dropped`), and a crash between capture and prompt loses them. An Esc-retracted
turn (§4.3.3) returns its captures to the buffer along with the input.

The in-memory capture buffer is bounded (so a runaway command cannot balloon the process), and the
§6.5 byte cap separately bounds what reaches the working window. Key material in a capture is
redacted on the context path only — see §8.2.1.

#### 4.4.2 Shell process — per-command execution

Each `$`/`#` line runs a fresh `$SHELL -c <command>` (falling back to `/bin/sh`). There is no
long-lived shell subprocess; only the cwd persists between lines (§4.5.1's shell cwd), and every
command has an exit code.

**cwd persistence — adopted from the command, not parsed from `cd`.** After each command the runtime
adopts the directory the command ended in as the shell cwd for subsequent invocations (passed as the
child's working directory). `cd` is **not** intercepted or parsed, so `cd /x && ls`, `pushd`, a cd
inside a function or subshell, and rc-driven changes are all honored. The reported path is carried
verbatim (newline, NUL and space are safe). The process exit status is the command's own. A reported
directory that no longer exists is refused, leaving the last known-good cwd in place.

**Accepted loss.** Aliases, shell functions, and exported environment defined *mid-session* do not
persist between lines. Measured rc behavior (macOS zsh 5.9 / bash-3.2-as-`sh`):

- `zsh -c` sources `/etc/zshenv` and `~/.zshenv` **only**; `~/.zshrc` is not sourced, so its aliases
  are unavailable. Aliases in `.zshenv` are available.
- `sh -c` sources **nothing**: not `~/.profile`, and not `$ENV`/`$BASH_ENV`.

**Ctrl-C.** The child runs in its **own process group**. Ctrl-C during a `$`/`#` command is
forwarded to that group and does **not** end the session: the command dies and the prompt returns.
The session's interrupt count is untouched.

Invariant:

> From the moment personant commits to a `$`/`#` command until that child is reaped, a SIGINT
> must **never** reach the session-cancel path.

From the commit point personant owns the signal; an interrupt arriving before the child can be
signaled is **queued** and delivered the instant it can be. A claimed interrupt is never silently
dropped, except for a child that failed to start, where the command's own error is the report.

**Terminal state.** The startup terminal mode is restored for the duration of every child and
re-installed after. During that window `Ctrl-C` is a real signal rather than a decoded key, so the
kernel generates `SIGINT` for personant to forward. Neither personant nor a pager the child spawns
competes for stdin.

#### 4.4.3 Interactive applications

Interactive terminal applications (`vim`, `nano`, `less`, `top`, etc.) are **not supported**: a
`$`/`#` child gets the terminal's *mode*, not its *input* (no PTY wiring).

The child's **stdin is the null device**, so an interactive program reads EOF and exits instead of
wedging the REPL. A non-zero exit is announced on the terminal (`[exit N]`, or `[interrupted]` for a
signalled command).

#### 4.4.4 Permission tiers do not apply to `$`/`#`

The §6.2 permission tiers govern *agent-initiated* operations. `$`/`#` are user-initiated and bypass
the tier check entirely, with full shell privileges. The invocation (and for `#`, the captured
output) is logged — `user.shell` is a first-class §2.8 event category.

### 4.5 Project identity, project root, and shell cwd

#### 4.5.1 Two cwds, deliberately distinguished

The runtime tracks two cwds:

| Concept | Set by | Used for |
|---|---|---|
| Active project root | `/cd-project <path>` or `/project switch <name-or-id>` | path resolution for agent tools (`fs.read`/`fs.list`/`fs.grep`/`propose_*`); permission-tier classification (§6.2.6); project-scoped directives (§2.5, §2.6) |
| Shell cwd | any directory change a `$`/`#` command performs, learned from the §4.4.2 cwd epilogue rather than by parsing `cd` | path resolution for `$` and `#` shell commands only |

They diverge freely: `$ cd` into `/etc` does not change the active project or grant the agent any
permission.

#### 4.5.2 Three layers of project identity

A project's identity has three layers (schema: §2.5.1):

| Layer | Form | Stable? |
|---|---|---|
| Internal handle | `prj_<n>` | yes — assigned at creation, never changes; storage layout key, spine reference key |
| External canonical identity | normalized git remote URL (`remote_urls[0]`) | mutable but rare; gained when the project's git tree first acquires a remote; survives file moves |
| Display name | user-chosen string | mutable via `/project rename` |

The internal handle is always present. External canonical identity is *adopted* when a git remote
appears (§4.5.4). Renames touch only the display name; no spine entries are rewritten.

#### 4.5.3 `/cd-project <path>` resolution flow

1. Resolve `<path>`; query its git config for the canonical remote URL (`origin` by default;
   otherwise none).
2. **If a remote URL is found:** look up known projects by `remote_urls`.
   - Match → switch (silent); update `current_root_path` if it has drifted from the matched
     project's stored value (and append the old path to `historical_root_paths`).
3. **Else (no remote at this path):** look up by `current_root_path`, then `historical_root_paths`.
   - Match → switch.
4. **No match anywhere:** prompt:
   ```
   Path '<path>' is not associated with a known project.
   [s] switch an existing project to this path
   [c] create a new project here
   [n] cancel
   ```
   On `[s]`, list known projects (id + name) and let user pick. On `[c]`, ask for a display name;
   allocate a new `prj_<n>`; record path; auto-detect remote URL.

The runtime does **not** auto-switch the active project when `$ cd` lands the shell on a known
project's directory. Project switching is always explicit.

#### 4.5.4 Local→remote promotion (auto-detect)

The runtime polls the active project's git config cheaply (e.g. once per turn or at engagement) for
its remote URL:

- **None → present.** Prompt: `Project '<name>' just gained a git remote (<url>). Adopt as canonical
  identity?` Default `[y]`. Appends to `remote_urls`. Logged as `project.remote-adopted`.
- **Present → updated.** If it normalizes to the same URL, silent. Otherwise prompt: `Remote URL
  changed (<old> → <new>). Update canonical identity? Keep the old one in history?` Default both.
  Appends new URL to `remote_urls`; old URL goes to `historical_remote_urls`. Logged as
  `project.remote-updated`.
- **Present → none.** Keep the historical entry; no demotion.

If a *different* known project already carries the just-gained remote URL in its `remote_urls` (or
`historical_remote_urls`), prompt:

```
Project '<name>' just gained a remote (<url>) that already belongs to '<other>'.
[m] merge into '<other>' (move threads + spine entries)
[k] keep separate (leave both projects intact)
[c] cancel — do not adopt the remote
```

Logged as `project.remote-collision-prompt`.

#### 4.5.5 File-move detection

When the agent tries to access the active project's root and the path is gone (ENOENT on
`current_root_path`), the runtime prompts:

```
Project '<name>' files at <path> are missing.
[r] relocate (provide new path)
[s] search (let the runtime scan a parent for the directory)
[w] wait (defer; assume temporary unavailability)
```

`[r]` updates `current_root_path` and appends old path to `historical_root_paths`. If a git remote
is preserved at the new path, the identity is unchanged. Logged as `project.cd-changed`.

#### 4.5.6 `/project rename`

`/project rename <new-name>` updates the `name` field in `meta.json`. The internal handle (`id`),
the storage path (`projects/prj_<n>/`), the canonical remote URL, and every spine entry's `project`
field are unaffected. Logged as `project.renamed`.

#### 4.5.7 Project bootstrap at startup

At session start the active project is resolved by an explicit override, else a three-step
waterfall, else a final fallback.

**Explicit override:** `--project <name-or-id>` is used directly, skipping the heuristics. Error if
the project isn't known.

**Heuristic waterfall** (in order; first hit wins):

1. **Git remote match.** If CWD (or any ancestor) is a git working tree, read its `origin` remote
   URL (or the first remote if no `origin`). Normalize per §2.5.1. Look up known projects by
   `remote_urls` and `historical_remote_urls`. On match: silent switch; update `current_root_path`
   if it has drifted (and append the old path to `historical_root_paths`).
2. **CWD path match.** If step 1 produced no match: check whether CWD (or any ancestor) is
   `current_root_path` or appears in `historical_root_paths` of any known project. On match: silent
   switch; updates as in step 1.
3. **Last-active confirmation.** Otherwise read `<Home>/last-active` (operational; one line:
   `prj_<n>` of the most recently active project) and prompt:
   ```
   Resume work on '<display_name>' (last active <RFC3339>)? [y/n/<other-name-or-id>]
   ```
   On `y`: silent switch. On `<other>`: treat as `/project switch` target. On `n`: fall through to
   final fallback.

**Final fallback** — fresh install, never-used home, or all heuristics declined:

```
No active project resolved. Choose:
  [c]  create a new project rooted at this directory
  [s]  switch to a known project (lists ids/names)
  [n]  no project (use prj_default)
```

`prj_default` is always available as a no-project escape hatch.

**`last-active` file maintenance.** The runtime writes the active project's `prj_<n>` to
`<Home>/last-active` whenever the active project changes — bootstrap, `/project switch`,
`/cd-project`. The file is operational (§2.1), single-line, plain text.

#### 4.5.8 Startup recovery after unclean shutdown

The runtime may be hard-terminated (crash, SIGKILL, power loss) mid-turn, leaving derived state (the
symbol index, working-set membership, the `last-active` file) inconsistent with canonical
(`spine.jsonl`, thread files, logs). (This is distinct from §3.8's archival recovery.) On startup
the runtime **reconciles derived state against canonical** and **repairs a torn in-flight turn**
before opening the session. Startup order: **init → startup recovery → (day barrier if new-day) →
session load**. The runtime **refuses to open** on an unreconciled or unrepairable substrate.

**Durability bar: at most one turn may ever be lost**, under any termination timing; journaled
content bytes are recovered from prompt-time onward. The raw `(prompt, response)` bytes are
protected by a durable append journal written *before* any canonical write.

**No signal/interrupt handlers as a flush mechanism.** Recovery is entirely a **cold-start
reconciliation** — no shutdown event to catch, no LLM in the loop, no user acknowledgement; the
on-disk state alone determines the outcome deterministically. The worktree is the source of truth;
git is a recovery-point index over it (see (5)).

##### (1) Detection observables

Recovery reads the following on-disk signals and classifies into exactly one cell (see (4)). No
signal is trusted transitively; each is read against the correct repo (daily vs primary).

- **Journal-as-turn-signal.** A **non-empty** `turn-journal.jsonl` (§2.1) *is* the in-flight-turn
  signal — there is no separate `op=turn` marker file. The journal's first record carries the turn
  id; the first append's fsync makes the signal durable. (An `op=turn` marker file left by an older
  binary is still read and reconciled.)
- **Batch marker file** `op-in-progress.json` (`$PERSONANT_HOME`, gitignored, survives `reset
  --hard`): batch ops **only** — `{op: archival|sleep|recovery|barrier|rebaseline, day?, orig?}`.
  Written before a batch operation's canonical mutations, cleared as its last step. Its presence is
  the batch-in-flight signal; `op=barrier` carries the target `day`.
- **Watermark = daily HEAD.** `derived-watermark` (gitignored) holds the **daily** git HEAD hash the
  derived artifacts were last built from. `daily HEAD == watermark ∧ daily WT clean ⇒ derived fresh`
  — the O(1) clean-open certificate.
- **HEADTURN / HEADDAY trailers.** Every daily commit carries `Personant-Turn: <id>` (read as
  HEADTURN); every primary commit carries `Personant-Day: <N>` (read as HEADDAY). HEADTURN
  discriminates the torn-turn cells (4/5/6); HEADDAY drives new-day detection and the barrier
  idempotence guard. HEADDAY is ⊥ only for a bare (never-committed) primary.
- **DAILY ∈ {present, missing, half-created}**, evaluated **defensively and FIRST**, before any
  daily-repo read: a failure to open a corrupt/partial `.git-daily` classifies as `half-created`,
  **never a propagated error**, so a corrupt daily (disposable by construction) is recreated from
  the worktree and never wedges an open.

##### (2) Turn transaction protocol

A turn is written to the **daily** repo in three ordered steps:

1. Append the prompt record to the journal (fsync) **before the model call**. The first append arms
   the in-flight signal.
2. Append the response record (fsync) **before any canonical write**. A journal-append failure
   aborts the turn *pre-canonical*.
3. At turn close, commit the turn's **scoped** write set to daily with the `Personant-Turn` trailer,
   then **truncate the journal** (the truncate *is* the scope release). A commit failure leaves the
   journal non-empty and fails loudly into recovery.

Per-turn staging is scoped to the turn's recorded write set (§3.11) — `spine.jsonl`, touched thread
dirs, the day's event log, recorded at each write site, never guessed. A hand-edit is therefore
absorbed at the next FULL sweep (session-close checkpoint or day barrier B1/B2), not the next turn.

The §5.5 re-prompt caps live entirely *inside* a turn, ahead of step 3; recovery operates only on
the committed/journaled result and never re-drives the model.

##### (3) The journal

`turn-journal.jsonl` (`$PERSONANT_HOME`, flat, operational/gitignored):

- **JSONL contract:** one record per line, `{turn, kind: prompt|response, at, bytes}`; **fsync per
  append**.
- **Truncate-no-fsync at turn commit.** The truncate-to-empty carries **no fsync**: a power-loss
  resurrection of a stale-but-truncated journal is the cell-5 redundant-journal case (`turn ==
  HEADTURN`), and the next turn's first append fsync makes the truncate durable before any new
  canonical dirt can exist. **Converse residual:** a torn *append* is bounded to that one turn's
  content — the ≤1-turn bar.
- **Torn tail.** A torn final line is **skipped** by the scan, never parsed.
- **Preserve + surface, never replay.** On a torn turn the journal bytes are written to a recovery
  artifact and **surfaced** in the REPL banner. They are **never auto-replayed** into canonical (a
  turn's derivation — tag parse, symbol extraction, engagement — is not replayable). The user
  re-issues if they choose.

##### (4) The recovery state machine

Recovery classifies the observables into one cell. Turn cells (4/5/6) evaluate against **daily**;
cell 12 against **primary**. For barrier/archival cells recovery only **detects and reports a
pending completion**, leaving the marker in place; the substrate then completes it.

| Cell | Trigger | Action |
|---|---|---|
| 1 clean | no marker, DAILY present + WT clean vs daily HEAD, daily HEAD == watermark, journal empty | no-op — **O(1)** open |
| 2 derived-stale | no marker, clean, derived stale | rebuild derived; stamp watermark = daily HEAD |
| 3 hand-edit | markerless-**dirty** vs daily HEAD, watermark present | **NEVER reset** — a legitimate hand-edit; absorbed at the next FULL sweep (see (2)) |
| 4 torn turn | journal non-empty, dirty, `HEADTURN ≠ turn` | preserve+surface journal → reset the **daily** worktree to its HEAD (≤1 turn) → sweep untracked canonical-namespace debris (excluding `logs/`) → re-append log bytes → truncate journal |
| 5 turn committed | journal non-empty, clean, `HEADTURN == turn` | redundant journal (turn already committed) — truncate, zero loss |
| 6 turn no-writes | journal non-empty, clean, `HEADTURN ≠ turn` | nothing landed structurally — preserve+surface journal, truncate |
| 7 (composite) | cell 4 coinciding with an unstamped ARCH entry | cell 4, then the cell-9 stamp pass |
| 8 archival | `op=archival` marker | **DETECT ONLY** → reported as a pending completion; the substrate's roll-forward **completes** the batch (no reset, no re-archive-as-drift); marker left in place until completion |
| 9 stamp-repair | unstamped ARCH entry (any cell, post-normalization) | locate the deletion commit (the child of the entry's `parent_commit_hash`, by tree comparison), stamp; unlocatable ⇒ `recovery.unrepairable`, entry stays **refused, never guessed** |
| 10 sleep | `op=sleep` marker | clear marker; substrate self-heals (gc/rebuild idempotent) |
| 11 recovery-reentry | `op=recovery` (crash during recovery) | re-run under the original marker; every phase idempotent → converges |
| 12 legacy-adopt | dirty, **no watermark ever** | greenfield/legacy: verify-gated adopt-commit to **primary** (`Personant-Day` trailer), stamp-repair, full rebuild, then morning-init |

Dual-repo op rows (see (5)):

| Op | Action |
|---|---|
| `op=barrier` | **DETECT ONLY** → reported as a pending completion; the substrate runs the idempotent barrier completion of (5). The DAILY observable is **irrelevant** to classification. |
| `op=rebaseline` | `rm -rf .git-daily` + morning-init **unconditionally**; no primary touch (see the re-baseline knob in (5)). |
| morning-init rows (no marker, daily missing/half-created) | greenfield (no watermark) → [adopt if primary-dirty] + morning-init + rebuild; benign morning (watermark) → morning-init, rebuild iff derived stale; interrupted-init/corrupt-dir → `rm -rf`; morning-init. **Daily-missing is NORMAL, never a data-loss signature.** |

Orthogonal heals run on **every** path, before classification: additive event-log tail-heal (last
byte ≠ `\n` ⇒ append one `\n`, never truncate) and `tmp/` sweep.

**Startup recovery reports once**: the repairs made, any paths quarantined-and-restored (empty on
the normal path), and any completion left pending.

**Quarantine-never-delete.** Recovery never deletes canonical bytes it cannot re-derive. The
torn-turn debris sweep and the persistent-verify-failure posture of (5) both **quarantine byte-exact
under `recovery/quarantine/<stamp>/`** before any removal or scoped restore, and report the moved
paths. An offending path absent from the restore source is quarantined and removed.

##### (5) The dual-repo architecture and the day barrier

**The worktree is the truth; the daily DB is disposable.** Every canonical byte is a file under
`$PERSONANT_HOME`; git is a recovery-point index over those files. Two git DBs track the one
worktree at two cadences:

| DB | git-dir | cadence | role |
|---|---|---|---|
| **daily** | `.git-daily/` | per-turn | today's microscope — fine-grained recovery points; **nuked + reborn each day barrier**; never career history |
| **primary** | `.git/` | per-day | career history — one day-grain commit per day + permanent archival anchors (~365 commits/year); irreplaceable |

Invariants:

- **INV-1** archival and the barrier NEVER reset the worktree (their recovery is *re-drive /
  repair*); only turn recovery resets, and only ever to **daily** HEAD (≤1 turn).
- **INV-2** the day-commit is guarded by *primary HEAD is a day-commit-shape commit AND its
  `Personant-Day == N`* (not merely "trailered") — a re-drive after a crash never mints a second
  day-commit.
- **INV-3** `.git-daily` is nuked only after the worktree is captured to primary.
- **INV-4** the watermark tracks **daily** HEAD, re-stamped by derived rebuild and by
  barrier/morning-init re-init.
- **INV-5** primary is **barrier-exclusive** with one recovery-repair exemption; the exhaustive
  primary-writer set is: barrier B1 archival, barrier B2 day-commit, cell-9 stamp-repair, cell-12
  adopt, archival roll-forward completion — **nothing else** (the session checkpoint targets daily).
  Every primary writer — day-commit and archival alike — validates spine integrity before minting,
  so primary HEAD is always spine-good.
- **INV-6** both `.git/` and `.git-daily/` are in the managed `.gitignore` block, so neither repo
  sees the other's git-dir as untracked debris.

**Day barrier B0–B6** (fired on new-day detection — single-clock day index exceeds HEADDAY — checked
at session open after startup recovery and before each turn; runs under an `op=barrier {day: N}`
marker):

- **B0** write the `op=barrier` marker (owns the whole sequence, including B1 — B1 opens no nested
  `op=archival` scope).
- **B1** *archival-if-pressure* against **primary** via the batch archive (§3.8.1; no nested
  marker). Batch membership is recorded durably before any deletion, and its capture commit stages
  the full worktree into primary (making B4's nuke safe, INV-3). Skipped under the low-water mark;
  then B2 is the sole worktree-capture.
- **B2** *day-commit* worktree → **primary**, `Personant-Day: N`, staging **everything including
  overnight hand-edits**. The staged tree is spine-integrity-validated **before the commit is
  minted** (INV-5). Guarded by INV-2; an empty day still produces
  exactly one trailered day-commit (HEADDAY always defined; no tag path).
- **B3** *rebuild derived* from the now-committed worktree, re-stamping the watermark against the
  pre-nuke daily HEAD.
- **B4** `rm -rf .git-daily` (idempotent).
- **B5** *re-init daily* + baseline commit of the current worktree → baseline hash H_d.
- **B6** stamp watermark = H_d (INV-4); **assert derived files are fresh**; clear marker.

Only B1/B2 mutate primary; B4/B5 are pure daily lifecycle; B3/B6 touch derived + watermark. The
canonical worktree is untouched by B2–B6 (only *reduced* by B1's archival removals, whose bytes are
preserved in primary).

**Lifecycle tags.** After the B2 day-commit lands (inside the `op=barrier` scope, before the marker
is cleared), the barrier derives **git tags on primary** from the sealed day's §2.8 event log:
`thread.created` → `thread/<id>/created`, `retire.complete` → `thread/<id>/retired`,
`project.created` → `project/<id>/created` — all targeting **that day's day-commit** — and
`archive.archived` → `thread/<id>/archived`, targeting the **archival commit** recorded in the
thread's archive-index entry. Project open/close/switch are NOT mapped. The ref name is
`<kind>/<id>/<event>_<stamp>`, e.g. `thread/thr_42/created_2026-05-08T03-12-00Z`; the stamp is the
event's UTC-normalized RFC3339 instant with time colons rewritten to hyphens (git refs forbid `:`),
so names are unique and sort **lexically = chronologically**. Derivation is **idempotent**
(skip-if-exists by exact name) and **best-effort**: a read/parse/tag fault logs `barrier.tag-error`
and is skipped, never failing the seal. `git show <tag>:spine.jsonl` (or any path) reads the
substrate as it stood at that commit. *Day-scoping:* the barrier for day N fires the next day, so
created/retired/project lines are those with event day index N, while `archived` (emitted by the
barrier itself) is not day-filtered; life events on earlier **idle gap-days** (< N) go untagged.

**Roll-forward completion (crash inside the barrier).** Every barrier crash window recovers by
**roll-forward, never a reset** (INV-1). The `op=barrier` marker (with its `day: N`) is the
idempotent re-entry token; every sub-action is guarded/idempotent, so re-entry converges. The B1
completion re-derives the durable batch-membership worklist: ensure-removed → ensure-spine-removed →
regen → (deletion commit iff none has landed, else stamp the located one) → stamp. A deletion commit
that already landed is stamped, not duplicated (this is where roll-forward meets cell-9
stamp-repair), and completion must **not** infer state from worktree-clean-vs-HEAD (an overnight
hand-edit would be misread as uncommitted removals). Completion also **re-runs the B1 pressure
drain**, so a barrier killed before archival does not defer the day's archival.

**Day-commit-shape guard, at-most-once per day.** New-day *detection* reads HEADDAY off any
trailered primary commit; the INV-2 *idempotence guard* fires only on a day-commit-shape HEAD for
the target day. A trailered-but-non-day-shape HEAD (a roll-forward stamp, an adopt) advances
detection but does NOT satisfy the guard, so the real day-commit is still minted. Recovery is
**fork-free** across the morning-N+1 completion and adopt-then-barrier kill timings. Only
**completed** days (strictly before the current clock day) are sealed, each exactly once (gap-day
at-most-once).

**Quarantine-and-proceed (persistent verify failure).** If B2's spine gate or B6's derived assert
fails and still fails across one idempotent re-drive: (1) identify the offending paths; (2)
quarantine their bytes byte-exact under `recovery/quarantine/<stamp>/`; (3) scoped-restore exactly
those paths from primary HEAD (path-scoped — **never** `reset --hard`, never a whole-worktree
restore; derived paths, absent from primary, are regenerated-and-verified instead); an offending
path absent from HEAD is quarantined and **removed**; (4) re-run the failing step once; (5) still
failing ⇒ **refuse-to-open**, marker LEFT IN PLACE (the retry token), report names the quarantine
dir. Safe because the restore is scoped and quarantined-first (INV-1) and primary HEAD is always
spine-good (INV-5).

**Morning-init** (`git init .git-daily` + baseline-commit the worktree + stamp watermark = baseline
hash) is exactly B5+B6 in isolation. It is **O(active working-set bytes)**, independent of
career-history length.

**Mid-day re-baseline knob**, **default-off**: nuke + recreate daily at a commit boundary when daily
loose objects cross a threshold, under `op=rebaseline` (never touches primary, never advances the
day). Recovery routes an `op=rebaseline` crash straight to morning-init.

##### (6) Archival: barrier-only + roll-forward completion

Deep-cold archival (§3.8) fires **only at the barrier (B1)** so primary stays barrier-exclusive
(INV-5); standalone archival remains available outside the barrier. Crash-completion is
deterministic because:

- **Membership-before-deletion.** Archive-index entries are appended (with
  `parent_commit_hash`/`tree_hash`/`original_path`/spine snapshot, empty `commit_hash`) *before* any
  thread directory is removed, so a crash mid-batch leaves a durable worklist.
- **Locating the deletion commit.** An already-landed deletion commit is resolved as the child of
  the entry's `parent_commit_hash` by **tree comparison** (robust to overnight hand-edit dirt), so
  roll-forward stamps the existing commit rather than minting a duplicate.

Standalone archival (cell 8) is likewise **detect-then-complete**: recovery reports it pending on
marker presence alone and the substrate runs the same roll-forward tail. Turn/structural commits
land in daily and are folded into the day at the barrier, so archival (primary) never races a turn.

##### (7) Honest-coverage tiers

- **SIGKILL — full guarantee.** ≤1 turn structural loss under any kill timing; journaled content
  bytes recovered from prompt-time onward. Every crash point has an asserting scenario ((9)).
- **Daily-DB loss — never content loss.** Nuked, lost, corrupted, or absent, the daily DB is
  recreated from the worktree.
- **Primary-DB loss — unrecoverable.** A missing/corrupt **primary** is **refuse-to-open +
  surface**, never auto-recreated.
- **Power-loss — qualified.** The ≤1-turn bar holds under fsync semantics, with two named residuals:
  (a) the **truncate-converse** — a truncate-no-fsync journal resurrection is the benign cell-5
  redundant case (see (3)); (b) a **journal-write-failure** aborts the turn pre-canonical, losing
  that one in-flight turn's content (still ≤1).
- **Intra-day derived staleness — by design.** The turn path **never stamps the watermark**; the
  **barrier owns freshness** (B3 rebuild + B6 assert). Between barriers, derived tracks daily
  per-turn state via the watermark; a markerless hand-edit stays deferred (cell 3) until the next
  full sweep.

##### (8) Greenfield / legacy compatibility

- **Greenfield** (no watermark, no daily): cell-12 verify-gated adopt-commit to primary
  (`Personant-Day: <current day>` — the sole bootstrap carve-out), stamp-repair, full rebuild, then
  a fresh daily baseline. An `op=turn` marker file left by an older binary is honored and cleared.
- **A missing daily on an existing home is the legacy-upgrade shape** and stays absent through init
  (a daily is born only alongside a fresh primary), so cell-12's adopt gate and the benign-morning
  stamp-only path discriminate correctly.
- Legacy unstamped archive entries take the **general adopt-forward repair path** (no special case).

##### (9) Crash-injection methodology

Every point at which a crash can strike the turn, barrier, archival, recovery and morning-init
sequences is exercised. After each crash: the full invariant suite holds; loss is ≤1 turn versus a
pre-crash oracle; journal bytes are recovered exactly; barrier and archival paths do not reset the
worktree; exactly one day-commit exists per day; and a second startup recovery run is a no-op. A
crash point that is not exercised fails the suite. The matrix is unit-grade (synthetic homes) and is
backed by a 2-day barrier-crossing simulation rung.

### 4.6 Math rendering (binding constraints; delivery deferred)

Personant does not render mathematics today (no inline images, no LaTeX handling). Binding now: any
future backend↔front-end boundary carries *semantic* content — markdown with LaTeX source — never
pre-rendered bytes. Rendering decisions (protocol choice, styling, wrapping) belong wholly to the
front end; the backend never bakes rendered output into stored or transmitted turn content, so
memory and the piped path always see source text.

---

## 5. Model interaction

### 5.1 Topic tag emission format

The model emits a topic tag at the **start of every response** identifying which threads the current
turn engages and which symbols attach to that engagement. The runtime parses the tag before any
other response processing.

#### 5.1.1 Format

Single-line, asterisk-bracketed, parsed deterministically:

```
*topic: <thread-list> [<anchor-list>]*
```

Where:

- `<thread-list>` is a comma-separated list of `thr_<n>` identifiers, optionally including the
  literal `*new-topic*` to indicate a new thread should be created.
- `<anchor-list>` is a comma-separated list of normalized anchor symbols (per §2.7.2) attached to
  this turn's engagement.

Examples:

```
*topic: thr_42 [trefoil, unknot, body-topology]*
*topic: thr_42, thr_88 [trefoil, neutrino, helical-screw]*
*topic: *new-topic* [neutrino, oscillation]*
```

The topic tag is **advisory**: it declares which threads the turn engages, and the runtime decides
*ownership* (§3.2). Owner selection from the thread-list:
- **multi-existing** (`[thr_N, thr_M, ...]`): the owner is the **lowest `thr_N` id** among
  referenced existing threads; the rest are engaged-but-not-owner.
- **pure `*new-topic*`** (no existing thread referenced): the new thread owns the turn (genesis
  turn).
- **mixed** (`[thr_N, *new-topic*]`): the existing `thr_N` owns; the new thread is created
  **metadata-only** (`turn_count = 0`, no excerpt, with its `description` set per §2.3).

The anchor-list is the model's advisory per-turn symbol contribution (`source = model`), folded into
`history_symbols` — not a cardinality contract. **0 anchors is legal** (a vague start), and an
emission over `anchor.projection-max` **folds**: the deterministic projection (§2.7.4) keeps the
strongest and files the rest. The `anchor.projection-max` bound is a runtime self-assertion checked
by `personant verify`, not a model contract. No topic-tag failure aborts a turn: a file edit with no
owning topic tag, or a missing or malformed tag, is at worst a re-prompt plus a defaulted binding
(§3.3).

The runtime strips the tag from the user-visible output before display.

#### 5.1.2 Parser

Regex (RE2 syntax):

```
^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$
```

Group 1: thread list (comma-split, trim whitespace, validate against `thr_\d+|\*new-topic\*`). Group
2: anchor list (comma-split, trim whitespace, run §2.7.2 normalization).

The tag is searched for only within the response's **leading scan window** — its first 64 lines
or 8 KiB, whichever ends first; a tag-shaped line beyond the window is not recognized. If the
response contains multiple matches, take the first; warn-log the rest. An **empty thread
list** is not a valid topic tag; warn-log and continue without engagement update for this delta. An
**empty anchor list is valid** (§2.7.4): an anchor list that normalizes to empty does not invalidate
an otherwise well-formed tag.

**Bare new-topic alias.** The parser also accepts a line of the exact shape

```
*new-topic* [<anchor-list>]
```

as equivalent to `*topic: *new-topic* [<anchor-list>]*`. The alias is strict: line-anchored (regex
`^\s*\*new-topic\*\s*\[([^\]]*)\]\s*$`), identical anchor-list grammar (empty-in-brackets valid),
and a bare `*new-topic*` with **no** `[...]` bracket pair remains invalid (forensically classified
`bare-new-topic`). The alias participates in the same first-valid-match rule and extras count
everywhere the tag is recognized.

**Unclosed inner literal.** Within an otherwise-valid `*topic: … [ … ]*` wrapper, the thread-list
token `*new-topic` (closing asterisk dropped) is accepted as the new-topic sentinel and normalized
to the canonical closed form (it would otherwise classify `bad-thread-list`). Thread-list position
only, token-exact with a single leading asterisk (`new-topic` and `**new-topic` remain invalid); a
mixed thread list (`thr_3, *new-topic`) follows the same per-entry rule as the strict form.

#### 5.1.3 Prompt template

The system prompt instructs the model to emit a topic tag at response start; v0.1 changes the
template only by rebuild.

The directive carries an **ambiguity clause**, at most two sentences: the tag is required even when
the response is a clarifying question or the thread routing is uncertain — tag the best-guess
thread, or `*new-topic*` if the work is genuinely new; the tag is a routing signal, not a
commitment.

Each recovery re-prompt appends a terse system-side reminder. The tag-missing reminder (§3.3) names
the discard reason, restates the tag form, and demands the tag as the first line — even for a
clarifying question. The empty-response reminder (§3.3) names the discard reason (no visible text),
restates the tag form, and asks for brief hidden reasoning. Once appended, each reminder survives
any later same-turn recomposition (e.g. a subsequent §5.5 fetch re-prompt).

### 5.2 Curator prompt for retirement summary

The curator LLM is invoked at closure (§3.5) to draft the spine `summary` from the thread's
accumulated body. The prompt instructs the model to produce a 100–150 char gist faithful to the
thread's operational content.

### 5.3 Dissector prompt for fallback clustering

The dissector LLM is invoked under budget pressure (§3.6) to cluster the oldest in-window content
into proposed retirement groups. The prompt presents the candidate content and asks for a small
number of clusters with brief rationale.

### 5.4 Recognition-context construction

The system prompt assembles the spine display lines, active-thread bodies, and project conventions
into the layered working set rendered by §3.1.

### 5.5 Mid-turn thread fetch (system-injected)

When a topic tag references one or more `thr_<n>` not currently in Layer B, the runtime fetches
those threads' contents and injects them into context **before the model generates its response
body**. The model does not invoke a tool to request the fetch.

Mechanism:

1. Model emits topic tag at response start.
2. Runtime parses tag (per §5.1.2) before consuming any response body.
3. For each `thr_<n>` in the tag not currently in Layer B:
   - Read `threads/thr_<n>/` (frontmatter from `thread.md`, body assembled from the recency-windowed
     `turns/` excerpts; see §2.3).
   - Truncate to budget allowance (subject to §3.1 layer caps).
   - Inject as a `thread.fetched` context delta (per §3.0).
4. Re-prompt the model with the augmented context; the response body is generated against it.

**Combined mid-turn re-prompt bound (with §3.3 recovery).** Re-prompts are capped at **1 per
cause**, and there are exactly three causes — missing-thread (this section), missing-tag (§3.3), and
empty-response (§3.3) — so a turn issues at most 3 re-prompts (≤ 4 model streams). A second miss for
the same cause falls through to that cause's deterministic close-time fallback (LRU pickup here;
owner-default for missing-tag; accept-the-empty for empty-response). The empty-response cause
subsumes missing-tag for a persistent-empty response (§3.3): the two reminders never both fire
against emptiness, with one exception — an all-whitespace response longer than the leading scan
window (§5.1.2) counts as tag-less rather than empty, so it fires the missing-tag reminder instead
of the empty-response one. A fetch costs at most one aborted stream
plus one full stream; turns that trigger no cause pay nothing. Speculative pre-fetch is a non-goal
(ARCHITECTURE.md "No speculative prefetch").

---

## 6. Tool surface and permissions

See ARCHITECTURE.md §"Tool surface (bounded, role-shaped)" for the design rationale; this section
specifies the contract.

### 6.1 External tool surface

The LLM's external tool inventory is bounded at seventeen tools, split between read/think and
draft/mutate. *Internal* tools (operations on personant's own state — `personant search`, mid-turn
thread fetch, `/topic`, `/done`, etc.) are out of scope for this section; see §4 and §5.5.

**Free-API citizenship is a hard requirement** (CONVENTIONS.md house rule). Every `web.*` tool:

- Sends a **descriptive User-Agent with contact information**, sourced from `config.toml [user]`
  (§8.2.2) — never compiled-in personal data. A tool whose service REQUIRES contact **refuses at
  invocation** without it, naming the config key; it still registers, so the model is told why.
  There is no silent anonymous fallback.
- Makes **serial requests per host**, with any service-specific minimum spacing applied from one
  shared per-host table (e.g. arXiv ~3s).
- On a `429`/`503` carrying `Retry-After`, makes **one retry** after the stated delay (capped at
  30s), then returns a clean tool error. No header → no retry.
- On every query tool (not `web.fetch`), enforces a **local per-turn / per-day cap** on its own
  counter, so an agent loop cannot convert one user request into unbounded upstream load.

#### 6.1.1 Read/think tools (11)

| Tool | Signature (informal) | Purpose |
|---|---|---|
| `fs.read` | `(path) → bytes` | read a workspace file |
| `fs.list` | `(path) → entries[]` | list a directory |
| `fs.grep` | `(pattern, path, opts) → matches[]` | regex/literal search across a tree |
| `web.fetch` | `(url) → bytes + meta` | retrieve a URL |
| `web.search` | `(query) → results[]` | search query → URLs |
| `web.wikipedia` | `(q, limit?) → articles[]` | search Wikipedia → articles (§6.1.5) |
| `web.wiktionary` | `(q, limit?) → entries[]` | dictionary search Wiktionary → entries (§6.1.9) |
| `web.wikidata` | `(q, limit?) → entities[]` | search Wikidata → entities (§6.1.8) |
| `web.arxiv` | `(q, limit?) → papers[]` | search arXiv → preprints (§6.1.6) |
| `web.crossref` | `(q, limit?) → works[]` | search Crossref → DOIs (§6.1.7) |
| `model.consult` | `(prompt, model_id) → response` | ask a guest model |

All seven `web.*` tools are tier 0 / non-mutating (§6.2). `web.fetch` and `web.search` are specified
below, the rest in §6.1.5–§6.1.9.

**The query tools are disjoint by territory, not by exclusivity.** Each model-facing description
says what that tool wins for and names its neighbours' ground. It does NOT say "use only for":
fanning one question out across several sources is an expected pattern.

**`web.fetch`** is a plain HTTP GET → readability extraction → Markdown, with no headless browser
and no JavaScript execution.

- **Scheme allowlist: `http` and `https` ONLY** (§6.2.7: blocks `file://`, `data:`, `ftp://` and
  every other scheme; does NOT block loopback or any IP range). Enforced on the initial URL AND on
  every redirect hop.
- **Bounded**: a timeout, a redirect cap, and a response size cap enforced while reading. Over the
  cap is a §6.5-style refusal asking for a narrower fetch.
- **Content-type branch**: HTML through the extraction pipeline; Markdown, plain text and JSON/XML
  passed through verbatim; binary and unknown types REFUSED with a reason.
- **Empty-shell detection.** When the extracted text is tiny relative to the document (a
  client-rendered SPA), the tool returns an explicit *"this page requires JavaScript rendering;
  content unavailable"* signal, never the empty shell as content. The notice names both possible
  causes (client-side rendering, or a genuinely text-less page).
- **Metadata is surfaced with the content** — title, byline, published date, site, language.

**`web.search`** returns ranked results (title, URL, snippet) from a pluggable backend (shipped
backend: Exa, `api = "exa"`, §8.2.1).

- **"Search failed" and "search found nothing" are distinguishable.** A backend failure is an ERROR
  result stating that NO search happened and that this says nothing about whether results exist; an
  empty index is a SUCCESS result stating that the search ran and matched zero. A silent empty on
  failure is forbidden.
- **A local query cap, per turn and per day, enforced in personant's own code** (§8.2.2 `[search]
  maxPerTurn` / `maxPerDay`; defaults 5 and 100). The cap bounds ATTEMPTS, not successes; hitting it
  produces a logged result telling the model to stop rather than retry. The counters are in-process:
  the per-turn cap is exact and the per-day cap resets on restart.

#### 6.1.2 Draft/mutate tools (6)

The LLM never names a workspace path on a write call. All workspace mutations flow through
`propose_*` tools, which are ack-gated (see §6.2).

| Tool | Signature | Purpose |
|---|---|---|
| `fs.tmp_write` | `(name, content) → tmp_id` | write to `.personant/tmp/<name>` |
| `fs.tmp_read` | `(name) → content` | read back own draft (multi-turn iteration) |
| `fs.tmp_list` | `() → drafts[]` | list current drafts |
| `fs.propose_promote` | `(tmp_id, target_path) → ack_event` | promote tmp → workspace; ack-gated |
| `fs.propose_rename` | `(from_path, to_path) → ack_event` | rename/move workspace path; ack-gated |
| `fs.propose_delete` | `(path) → ack_event` | delete workspace path; ack-gated |

`fs.propose_*` tools are **workspace-only**. Paths under `.personant/tmp/` are the agent's scratch
space (lifecycle in §6.3); paths under `.personant/` proper are not the agent's to mutate.

#### 6.1.3 Out of v0.1 scope (intentional exclusion)

Deliberately not in scope (not deferred):

- Shell command execution, subprocess spawning
- Git operations (commit, branch, push, etc.)
- Package management, build, test
- IDE / editor integration
- Deployment, service, running-system control
- Direct filesystem writes outside the `propose_*` channel

The exclusion is at the **LLM tool inventory** layer. The deterministic runtime may perform
analogous operations *autonomically* when required to maintain the canonical KB:

- **Autonomic git on `~/.personant/`.** The runtime owns its home's git tree: `git init` on first
  run and `git add`/`commit` of canonical mutations with structured messages, all in-process (no git
  binary required on `$PATH`). Validation runs inline — no git pre-commit hook; no user ack, no LLM
  involvement. See §8.3.
- **Read-only git queries on the workspace.** The runtime issues `git ls-files`, `git status`, and
  `git diff` against the workspace tree to classify tracked-vs-untracked status (input to §6.2) and
  to render diffs in ack prompts. The runtime never runs *mutating* git on a workspace tree.

#### 6.1.4 Registry and execution loop

The registry is substrate-free and unaware of the turn loop.

- **An empty registry sends no `tools` field.** An OPTIONAL tool with no configuration is absent
  from the inventory: `web.search` with no configured backend is not offered.
- **Registration is per-session and config-driven.** The registry is built once at session open and
  immutable thereafter. `web.fetch` and `web.wikipedia` register unconditionally; `web.search`
  registers only when the pool holds a usable `type = "search"` provider. Any fault in the optional
  half degrades to a warning and a smaller registry — never a refused session.
- **Spec order is deterministic** (sorted by name).
- **Dispatch never fails.** Every call yields a result safe to return as a tool message: an unknown
  tool, a handler error, and a handler timeout each become an error result naming what went wrong
  and what to do next. The one exception is the caller's context ending (§4.3.3 Esc, shutdown),
  reported to the caller and never to the model. Every tool call receives exactly one tool reply.
- **Handlers are bounded** by a deadline derived from the turn context.

After a stream completes carrying tool calls, the turn runs them sequentially in emission order,
appends the assistant message that carried the calls followed by one tool message per result, fires
each result through the §3.0 chain as a `tool.result` delta, and re-issues.

- A **tool-call-only response** (zero visible content, byte-identical to the §3.3 empty-response
  signature) never burns the empty-response re-prompt; the presence of tool calls separates them.
- **Tool rounds are bounded per turn**, by a counter DISTINCT from the §5.5/§3.3 re-prompt caps.
  Exhausting the tool budget is surfaced to the user on screen AND logged (`tool.truncated`) — never
  a silent truncation.
- **The user is never left with silence.** A turn that renders no text — tool calls only, a spent
  round budget, a persistently empty reply — emits one bracketed runtime notice.
- **Every executed call is observable.** One receipt per executed call (§4.3.4); the durable record
  is the `tool.call` / `tool.result` / `tool.error` triple.

**Crash stability (§4.5.8): tool calls and results are NOT journaled, and execution is
AT-LEAST-ONCE.** The turn journal holds only the (prompt, response) byte pair; replay re-elicits the
calls and re-runs the tools. Tool rounds add no canonical write. Every §6.1.1 tool is read-only;
**no mutating tool is registered until this decision is revisited.**

#### 6.1.5 `web.wikipedia`

A search tool scoped to English Wikipedia: ranked articles, each with its title, short description,
a matching excerpt and its canonical URL. Tier 0 / non-mutating. No credential, no configuration, no
pool entry — it registers on every session. It is a SEPARATE tool, not a `web.search` backend.

- **Arguments: `q` (required) and `limit` (optional, default 5, maximum 10).** An out-of-range
  `limit` is **clamped silently, never refused**. A non-positive `limit` reads as "unspecified".
- **The §6.1.1 `web.search` trichotomy holds.** A transport or non-200 failure is an ERROR stating
  that NO search happened; an empty result list is a SUCCESS stating that the search ran and matched
  nothing.
- **A local per-turn / per-day cap, on its OWN counter**, with the same mechanism and defaults (5
  / 100) as `web.search`.
- **Excerpts are stripped of the service's markup** without re-activating escaped content (an
  escaped `&lt;script&gt;` never becomes a tag). Each rendered field is clipped to the §6.5
  shortlist bound, and the response read is capped.
- **The result URL is the canonical `https://en.wikipedia.org/wiki/<key>` article URL.** The
  model-facing description states that `web.fetch` retrieves any result's full article.
- **Contact is REQUIRED** (Wikimedia's enforced User-Agent policy). With `[user]` unset (§8.2.2) the
  tool REFUSES at invocation with a message naming the policy, stating that no request was made, and
  giving both fixes. It still registers.

#### 6.1.6 `web.arxiv`

A search over arXiv, rendered as a shortlist of papers: title, arXiv id, submission date, the first
three authors with "et al.", the abstract, and the `https://arxiv.org/abs/<id>` page. Tier 0 /
non-mutating, no credential, registers on every session.

**Territory:** preprints and recent scholarship. The description points at `web.crossref` for a
*published* paper's DOI and venue.

- **Published obligations.** At most one request every **three seconds**, serially; an identifying
  User-Agent. Contact is asked for, not required: an unconfigured `[user]` runs with the
  identity-only agent.
- **`limit` default 5, maximum 10, clamped silently** — likewise for §6.1.7/§6.1.8 below.
- **The §6.1.1 trichotomy holds**: transport or non-200 is an ERROR saying NO search happened; an
  empty result is a SUCCESS saying the search ran and matched nothing.
- **An entry without a resolvable arXiv identifier is DROPPED**, not rendered. The abstract is
  clipped to a longer bound than a search snippet.
- **A local per-turn / per-day cap on its OWN counter**, 5 / 100.

#### 6.1.7 `web.crossref`

A search over Crossref's registry of scholarly works: title, first authors, container-title
(journal/venue), year, DOI, and the resolvable `https://doi.org/<DOI>` URL. Tier 0 / non-mutating,
no credential, registers on every session.

**Territory:** resolving and verifying citations. The description points at `web.arxiv` for work too
recent or unpublished to be registered.

- **Published obligations.** No key. The User-Agent carries the contact mail address, routing the
  request to Crossref's **polite pool**. Requests are serial; `X-Rate-Limit-*` and `Retry-After` are
  honoured. Contact is not required: with `[user]` unset the tool runs in the anonymous pool.
- **A record with no DOI is DROPPED.**
- **Absent fields are omitted.** Each absent title, venue or year yields no line — never a literal
  `null` or an empty parenthesis.

#### 6.1.8 `web.wikidata`

An entity search over Wikidata: each result's label, QID, canonical
`https://www.wikidata.org/wiki/<QID>` URL and description. Tier 0 / non-mutating, no credential,
registers on every session.

**Territory:** structured entity identity — disambiguating same-named entities and getting the
stable QID. The description points at `web.wikipedia` for prose context.

- **Contact is REQUIRED**, on exactly the §6.1.5 terms, with the same refusal.
- **The tool does not send `maxlag`.**
- **Results are English-only**: the search is run with `language=en`, so labels and descriptions
  are English and entities matching only in other languages are not returned.
- **A failure the upstream reports inside a successful (HTTP 200) response is raised as an ERROR**,
  never rendered as an empty success.
- **The canonical URL is BUILT from the QID**, not taken from the response. A result with no `id` is
  dropped; a result with no label is rendered, headed by its QID.

#### 6.1.9 `web.wiktionary`

A dictionary search over English Wiktionary: ranked entries, each with its headword, a matching
excerpt and its canonical `https://en.wiktionary.org/wiki/<key>` URL. Tier 0 / non-mutating, no
credential, registers on every session.

**Territory:** the word itself — definitions, etymology, usage, pronunciation, translations — across
every language Wiktionary covers. The description points at `web.wikipedia` for encyclopedic
context.

- **The model-facing description LEADS with the literal anchors** *"Dictionary search (Wiktionary)"*
  and the words `define`, `look up`, `dictionary`, and names `web.wikipedia`'s ground without saying
  "use only for".
- **Contact is REQUIRED**, on exactly the §6.1.5 terms, with the same refusal text.
- **Serial requests on its own host**, distinct from Wikipedia's, with no fixed spacing.
- **Its OWN per-turn / per-day counter**, 5 / 100.
- **`limit` default 5, maximum 10, clamped silently**; excerpt markup stripped as in §6.1.5; the
  result URL is the canonical page URL, not re-encoded; the §6.1.1 trichotomy holds, with an empty
  result list rendered by the same no-results message as `web.wikipedia`.

### 6.2 Permission tiers and accrual

Permission tiers grade ops by risk and reversibility; accrued grants in directive files reduce
acknowledgement friction over time.

#### 6.2.1 Three-tier default policy

| Tier | Default | Covers |
|---|---|---|
| 0 (silent) | never ack | all reads; **all `web.*` tools**; all writes inside `.personant/**`; all `fs.tmp_*` |
| 1 (first-time ack with scope-grant offer) | ack once, then auto-allow within granted scope | `propose_promote` (new and overwrite), `propose_rename` to workspace |
| 2 (always individual ack) | every time | `propose_delete` on tracked files; `propose_rename` overwriting tracked files; ops matching always-confirm patterns; batches over a threshold |

#### 6.2.2 Ack prompt as accrual UI

The single ack prompt offers grant scopes via single-keystroke choice:

```
Promote tmp/foo.md → ave-kb/notes/topology.md (new file, 2.3 KB)

  [y] yes, this once
  [d] yes, allow promote-new under ave-kb/notes/ permanently
  [p] yes, allow promote-new anywhere in project ave-kb permanently
  [n] no
  [e] edit
```

`d` and `p` write a line into `directives/prj_<n>/permissions.md`.

#### 6.2.3 Permission directive schema

`directives/prj_<n>/permissions.md` accumulates standing grants:

```markdown
---
scope: project
project: prj_3                        # internal handle; display name resolved from meta.json
parameters:
  fs.permissions:
    - {action: promote-new,        path: "knowledge-base/**"}
    - {action: promote-overwrite,  path: "knowledge-base/**/*.md"}
    - {action: propose_rename,     path: "knowledge-base/scratch/**"}
last_modified: 2026-05-09T02:21:00-07:00
modification_source: accrual
---

# Project permissions

Standing grants accrued from ack prompts. Edit by hand to revoke.
```

Granularity dimensions:

- `action`: one of `promote-new`, `promote-overwrite`, `propose_rename`, `propose_delete`
- `path`: glob pattern relative to project root

All standing grants are permanent until the directive is edited.

#### 6.2.4 Default-shipped permissions

`directives/defaults.md` ships with seed grants:

- Auto-allow `promote-new` to any path under the active project's CWD that is *not* already tracked
  by git, *and* does not match the sensitive-pattern list (`.env`, `secrets/**`, `*.key`, `*.pem`;
  configurable).
- Always require ack for `promote-overwrite` of git-tracked files.
- Never auto-promote into a directory containing `.git/HEAD` not on the trusted-projects list.

#### 6.2.5 Always-loud (no scope expansion offered)

Some ops always offer only `[y]/[n]/[e]` — never a scope-grant option:

- `propose_delete` on git-tracked files
- Batches over a threshold (suggest 5+ files in one promotion). Single ack for the batch, file list
  shown, but no permanent grant from this ack.
- Ops flagged by an `always-confirm` pattern in the user's directive

#### 6.2.6 Active-project boundary as a tier axis

Agent-tool access to a path *outside the active project root* (§4.5) bumps tier:

| Op | In active project | Outside active project |
|---|---|---|
| `fs.read` / `fs.list` / `fs.grep` | tier 0 (silent) | tier 1 (first-time ack with scope-grant offer) |
| `propose_promote` (new) on git-untracked | tier 0 (covered by default-shipped grant, §6.2.4) | tier 1 (always; the default-shipped grant does not extend cross-boundary) |
| `propose_promote` (overwrite) on git-tracked | tier 1 | tier 2 (always individual ack, no scope expansion offered) |
| `propose_delete` / `propose_rename` on tracked | tier 2 | tier 2 with explicit cross-boundary warning in the prompt |

Sensitive-pattern policy (§6.2.4) applies regardless of in-vs-out classification, but a
cross-boundary access matching a sensitive pattern escalates to tier 2 even on a read.

**Path classification is mechanical:** after path resolution (resolving symlinks; collapsing `..`),
a result that is not a descendant of the active project root is out-of-project. `..` traversal above
the project root counts as cross-boundary. Symbolic links cannot be used as a permission bypass.

`$`/`#` user shell commands (§4.4) are not subject to this.

#### 6.2.7 Network tools: tier 0, bounded by an allowlist

The network tools (`web.*`) are **tier 0 (silent), with a SCHEME ALLOWLIST as the boundary** instead
of an acknowledgement — a user cannot adjudicate "is this URL an internal-network probe?" from a
prompt.

The allowlist is **`http` and `https`, and nothing else**, applied to the initial URL and to every
redirect hop:

- It **blocks** `file://`, `data:`, `ftp://`, and every other scheme.
- It **does NOT block** loopback, RFC1918, link-local, or any other IP range: reaching the user's
  own dev server is an intended case. It must not be extended into SSRF filtering.

All `web.*` tools are non-mutating, so they meet the §6.1.4 at-least-once bar.

### 6.3 `.personant/tmp/` lifecycle

- Tmps live for the active thread's life. On thread retirement, unpromoted tmps are surfaced at ack
  time (`thread retiring with N unpromoted drafts — promote / discard / keep?`).
- A periodic GC sweeps tmps with no thread reference older than 30 days.
- The agent picks names; collisions overwrite. Deterministic code prefixes by thread to avoid
  cross-thread collisions: `tmp/thr_42/foo.md`.
- Tmp paths are *not* extracted as identifier symbols. Promoted target paths *are* (§2.7.1,
  "identifier" category) and feed the symbol extractor on the promotion event.

### 6.4 Tool calls in logs and the spine

Tool-call events are first-class in `logs/YYYY-MM-DD.log`, extending the §2.8 table:

| Category | Events |
|---|---|
| `tool` | `call` (a §6.1.4 dispatch is starting — `name=`, `id=` the provider's call id, `round=`; never the arguments), `result` (the call returned — `name= id= round= bytes=`, the byte count BEFORE the §6.5 cap), `error` (unknown tool, handler failure, or handler timeout — `name= id= round= detail=`; the model got a recoverable error result and the turn continued), `truncated` (the turn's tool-round budget was exhausted with the model still asking — `reason=round-cap rounds= pending= turn=`; paired with an on-screen notice, §6.1.4) |
| `permissions` | `redaction-fire` (§8.2.1 user-initiated redaction fired on a `#` capture — `source=` the delta source, `occurrences=`, `bytes=` how much key material was removed; **never what matched** — no field may carry the secret) |

Reserved names (see §2.8): `tool.denied`, `permissions.prompt`, `permissions.grant`,
`permissions.deny`, `permissions.revoke`, `permissions.accrual-update`,
`permissions.suspicious-access`.

The §3.3 deterministic pass runs over tool result content as over turn content: file paths and URLs
encountered through `fs.read`, `fs.grep`, `web.fetch` are identifier-category symbols.

### 6.5 Tool output budget policy

Tool output is bounded so it cannot exhaust the 15% live-turn budget (§3.1):

- Hard byte cap on tool result inclusion in the model's working window (default 8 KB; configurable).
  Above the cap: head + tail with ellipsis; full body written to the engaged thread's file under a
  tool-result section.
- The cap is a **task-class** bound: it applies to `tool.result` and to a §4.4 `#` shell capture
  (`user.shell-capture`). It does **not** apply to the §3.9 `fs.read`/`fs.write`/`fs.commit` deltas,
  whose content is the file body written to the per-thread tracked-file store.
- The cap is applied to the **delta**, and the bounded content is what builds the tool message: the
  working-window copy and the wire copy are bounded identically, and a truncation always carries its
  marker.
- Agent can re-fetch with a narrower query or a byte range if it needs more.
- Pathological cases (a 50 MB file the agent asked for) reject at the tool layer with a message
  asking for a narrower fetch.

---

## 8. Bootstrap and lifecycle

### 8.1 First-run scaffolding (`personant init`)

Idempotent. Creates the directory layout from §2.1; runs `git init`; writes seed
`directives/defaults.md`, `README.md`, and template `providers.toml` and `config.toml` files, each
with a commented-out example block.

**The `[user]` step.** After `config.toml` exists and before the initial commit, init adds whatever
`[user]` (§8.2.2) is missing, seeded from the git binary's global identity. It reports one line, in
init's `[INFO] init:` style, **to stdout as well as the log**:

```
init: created [user] name="Ada Lovelace" email="ada@example.com" (from git config)
init: existing [user] name="Ada Lovelace" email="ada@example.com"
```

When git is absent from `$PATH`, or its global identity is unset, the section is still created with
empty fields, and the report names the cause, states that `web.wikipedia` and every other
Wikimedia-backed tool WILL BE UNAVAILABLE until `name` and `email` are populated, and gives both
fixes (edit `config.toml` directly, or set the git global config and re-run `personant init`).

### 8.2 Configuration sources and precedence

The runtime reads configuration from four sources, each with distinct purpose and security posture:

#### 8.2.1 Provider pool: `providers.toml`

`$PERSONANT_HOME/providers.toml` is **the pool of available providers** — a connectivity catalog
only. It pins nothing and expresses no preference; the choices that draw from the pool live in
`config.toml` (§8.2.2).

Format:

```toml
[provider-name]
baseUrl    = "https://api.example.com/v1"
apiKeyFile = "api_keys/example.key"
type       = "inference"          # or "search"
api        = "openai"             # or "exa"
```

One TOML table per provider. The provider name is the lookup key used by config references and by
the runtime's outbound client wiring.

**Kind and protocol.** `type` says what the endpoint IS and `api` says how to talk to it. A §6.1.1
`web.search` backend is a pool entry like any other, with its own `apiKeyFile`.

| `type` | Meaning | `api` values |
|---|---|---|
| `inference` | LLM endpoint: chat, embeddings, `/models` | `openai` |
| `search` | `web.search` backend (§6.1.1) | `exa` |

**Both fields are REQUIRED, and they must agree.** Neither has a default. An entry that declares
neither, declares an unknown kind, or pairs a kind with a protocol it does not speak is **dropped at
pool load and reported as a named fault**.

**Kind is a selection boundary.** Chat, embedding and `/models` selection — including the
alphabetical default-provider fallback — run over the `inference` subset only.

**A provider names no models.** There is no `defaultModel` field. The model choice lives in
`config.toml` (or `--model`), and `personant models --provider <name>` lists what an endpoint
currently serves.

**API-key forms.** A provider supplies its key one of two ways:

- `apiKeyFile` — **recommended.** A path (relative to the `providers.toml` directory) to a file
  containing only the key.
- `apiKeyUnsafe` — an inline key string. **Discouraged**: it embeds a secret in `providers.toml`.

**Security boundary (load-bearing):**

- A `providers.toml` using `apiKeyFile` throughout is **safely scannable** — agents and tooling may
  read and edit it. The **secret-bearing artifacts are the `apiKeyFile` targets**.
- The runtime resolves a provider name → `baseUrl`/key at the moment of an outbound API call; the
  resolved key material is **never** included in any LLM context, log line, ack prompt, or captured
  shell output — for a `search` provider exactly as for an inference one (no error string, event
  line, or tool result carries it).
- The refuse-vs-redact policy below applies to the key files (and to any `providers.toml` that still
  uses `apiKeyUnsafe`), not to an `apiKeyFile`-only `providers.toml`.

**Hybrid redaction policy** (refuse vs. redact, by initiator) — for the secret-bearing artifacts:

- **Agent-initiated reads** (`fs.read`, `fs.grep`, `fs.list` listing the file's parent dir)
  targeting a key file (after symlink resolution): the runtime **refuses outright** with a
  deny-error to the model like `permission denied: API-key file is secret-bearing and cannot be read
  by the agent`. Logged as `permissions.suspicious-access`.
- **User-initiated captures** (`#`-prefixed shell commands per §4.4 whose output contains key
  material): the runtime **redacts before reaching context** — key material is replaced with
  `[redacted: API-key content]`. The user's terminal still sees the unredacted output. Logged as
  `permissions.redaction-fire`.
  - The replacement is **per occurrence**, not whole-buffer; the surrounding output survives. The
    key set is the pool's already-resolved keys; the redactor reads no key files of its own. Keys
    under 8 bytes are not scanned for. The same redactor is applied to the *invocation* before it is
    logged, so `$ export API_KEY=…` cannot leak through the `user.shell` event line.

**Load-time validation.** Shape is checked before secrets: an entry whose `type`/`api` pair is
missing or unroutable is faulted out before its key file is read.

**Load-time key resolution.** Every provider's key is resolved when the pool is loaded — an
`apiKeyFile` is read and trimmed. A provider whose `apiKeyFile` cannot be read (missing file,
permission error) is **dropped from the pool and reported as a fault** (a startup warning); the
remaining providers stay usable. A `providers.toml` that is itself unreadable or malformed is a hard
error. Resolution and fault strings carry only the file path, never key content.

#### 8.2.2 Configuration choices: `config.toml`

`$PERSONANT_HOME/config.toml` holds the choices that draw from the provider pool. It is canonical,
hand-editable, and **not secret-bearing** — it names providers and models, never keys.

Format:

```toml
[user]
name  = "Ada Lovelace"
email = "ada@example.com"

[chat]
defaultModel = "provider/model"
showThinking = false

[embedding]
model        = "provider/model"
vectorLength = 768

[search]
provider   = "exa"                # a `type = "search"` pool entry
maxPerTurn = 5
maxPerDay  = 100
```

- `[user] name` / `[user] email` — the §6.1 free-API citizenship contact, placed in the outbound
  `User-Agent`; the Wikimedia-family tools (`web.wikipedia`, `web.wikidata`, `web.wiktionary`)
  REFUSE to run without both. Not secret. Like `[search]`, the section is **unvalidated**: an absent
  or half-filled `[user]` costs only the tools that need contact, never the session.
  - **`personant init` populates it** (§8.1) from the installed git's global `user.name` /
    `user.email`, as git itself resolves them — including `include`/`includeIf`.
  - **Insertion is textual and field-level**, never a TOML round-trip: a section with `name` but no
    `email` gains only `email`; a complete section is not rewritten.
- `[chat] defaultModel` — the default chat provider/model.
- `[chat] showThinking` — optional: stream a thinking model's reasoning deltas to the terminal,
  dimmed. **Absent → off**. Display-only — reasoning is never journaled, never feeds §3.3 symbol
  extraction, and is never replayed in history — and outside cross-file validation: an absent or
  unrecognized value never refuses a config. `/thinking` (§4.2) overrides it for the session.
- `[embedding] model` — the embedding provider/model. **Mandatory for embedding recall** and must be
  explicit: the embedding model defines the vector space, and changing it invalidates the embedding
  cache.
- `[embedding] vectorLength` — optional; for matryoshka-capable embedding models, requests this
  truncated dimensionality (passed as the `dimensions` parameter on the embeddings call).
- `[search] provider` — which `type = "search"` pool entry backs §6.1.1 `web.search`. Needed only to
  disambiguate a pool holding more than one; with exactly one, that one is used. The section is
  optional: with no search provider in the pool, `web.search` is not registered. **No key appears
  here.**
- `[search] maxPerTurn` / `[search] maxPerDay` — the local query caps (§6.1.1). Defaults 5 and 100.

Model references are `"provider/model"`, split on the **first** `/` (the model portion may itself
contain slashes, e.g. `openrouter/google/gemma-4-31b-it`).

**Cross-file validation.** At bootstrap the runtime validates `config.toml` against the loaded pool:

- Each non-empty `[chat]`/`[embedding]` reference must be a well-formed `provider/model` string, and
  the named provider must exist in `providers.toml`. That is the full bootstrap cross-check.
- Neither model id is statically checked against the pool.
- The chat model IS verified at session open against the resolved provider's `/models`. An
  unreachable or model-less `/models` degrades to a warning and an unverified model; a `/models`
  list that does not contain the configured model is a hard startup error naming what the provider
  does offer.
- **`[search]` is unvalidated.** Absent, partial, or naming a backend this binary does not know
  never refuses a config: the tool is left unregistered, with a warning.

Any cross-file validation failure is fatal at bootstrap. A faulted provider (§8.2.1) is not itself
fatal, but a `config.toml` reference to it fails this check.

**Precedence** (chat model resolution): CLI flag > `config.toml` `[chat]`. There is no third
fallback. A session with neither refuses to open, naming both fixes.

#### 8.2.3 Environment variables

`PERSONANT_HOME` overrides the home directory (resolved in §2.1's `$PERSONANT_HOME`).

#### 8.2.4 Directive precedence

Detailed in §2.6. Briefly: `directives/defaults.md` < `directives/user.md` <
`directives/prj_<n>/project.md`. The runtime walks the precedence chain at parameter-read time and
returns the first match.

### 8.3 Substrate validation in autonomic git operations

Before and after each state-changing git operation on `$PERSONANT_HOME/.git/`, the runtime runs
systemic validation inside its own logic: derived files are fresh (what `personant index check`
verifies — `symbols.jsonl` and per-project `digest.json` match their canonical sources) and the
substrate is structurally sound (what `personant verify` performs).

Operation-specific verifications (e.g. that a particular archive entry resolves after a recovery
checkout) live inside the operation that needs them.

**No git pre-commit hook is installed.** A human who runs `git commit` manually inside
`$PERSONANT_HOME` is operating outside the runtime and owns the consequences.

---

## 9. Measurement and validation regime

The thesis — months-long continuity, drift-resistant memory, opportunistic recall,
retire-and-recover cycles that preserve meaning — is a behavioral claim, and it must be proven by
simulation **before** real use accumulates the state a structural defect would put at risk. This
section defines that proof: the acceptance gate (§9.1) and the instruments that measure it
(§9.2–§9.4).

### 9.1 The four-month simulation (v0.1 acceptance gate)

The v0.1 acceptance criterion:

- **Simulate four continuous months (120 calendar days)** of realistic usage via a synthetic
  workload (§9.4) running against the real runtime with a mock LLM (§9.2) supplying canned
  responses.
- **Memory quality maintained** throughout, measured via the metrics emitted at every event (§9.4):
  recall hit rate, engagement accuracy, retirement timing, round-trip information-preservation
  through retire→archive→recover.
- **Zero out-of-context-space events.** The normative criterion is the whole-request token ceiling
  (`context.token-budget`): the fully-assembled request (system prompt + replayed history tail +
  current user input + injected fetches) never exceeds it, checked post-flight against
  `usage.prompt_tokens`. Any overflow is a fail. On mock rungs a secondary check asserts each
  rendered layer stayed within its derived byte allocation (§3.1). Turn-time budget enforcement is
  deferred past v0.1 (§3.0.2 step 4).
- **Operation runtime costs measured and within bounds.** Wall-clock P50/P95/P99 latency for:
  engagement update, spine match, thread fetch, retirement, archival, recovery, index rebuild, index
  check. "Within bounds" means stable across the simulation, not a threshold that grows with
  accumulated state.
- **Steady-state demonstrated.** After 120 simulated days the trajectories of working-set size,
  spine size, and per-operation latency are **flat**: the system is in a state from which it could
  run another four months without degradation.

**The acceptance ladder.** Rungs are spans of simulated calendar time in **days** (most of a day is
not spent working):

- **Smoke rungs — `1d` / `7d`.** Regression catch; the default test run exercises `1d`. Not analysis
  rungs.
- **Real ladder — `15d → 30d → 60d → 120d`.** Exact doublings. The top rung is 120 days.

**Long-rung symbolic precision is a corpus-saturation floor, not a regression.** On long rungs
symbolic-layer precision/F1 declines and the unresolved-episode rate rises while recall stays flat:
the fixed sim vocabulary lets dormant non-sibling threads clear the Jaccard threshold alongside the
true match. The decline is bounded; F1 settles. Rungs past ~30d partly re-measure this floor.

**Recall-layer scope.** The standard acceptance run uses the mock LLM (§9.2) with no embedder, so it
exercises only the §3.4 symbolic Jaccard layer — not the embedding layer that is primary in
production, nor model judgment. Consequences:

- Symbolic-only metrics are not reported as system recall. A within-thread-drift miss on the
  symbolic layer is expected, not a substrate defect.
- Inference-in-loop and embedding-in-loop rungs, each individually configurable, are required for
  convergence. They run as periodic deep validation; the mock regime stays the default iteration
  loop.

**The gate is realism convergence, not a single run.** The criterion is the full top-rung span (120
days) passed with every identified element of realism accounted for, including elements surfaced by
prior runs. Each run that exposes a missing realism behavior (or a defect previous incoherence
masked) is followed by addressing it; the gate is met at the run that surfaces **no new realism
gap**.

A realism element is accounted for by one of, in descending preference:

1. **Simulated** — exercised end-to-end in the workload.
2. **Modeled and attempted** — a written model of its expected effect, a solution implemented
   against that model, and unit tests over its decomposable parts.
3. **Known unknown, deferred to empirical human-use data** — parked explicitly; the front-end U/X
   phase supplies the data and a later substrate iteration closes the item.

**Honesty clause:** the acceptance claim documents its own coverage (simulated vs. modeled vs.
parked), so a green check never overclaims. A forgotten realism element is a failure; a documented
deferral is not.

**Accounted for:**

- **Evolving anchors** — *simulated.* Threads drift from their creation topic without a jump sharp
  enough to cut a new thread, premises invert, and an abandoned premise stays a findable handle. The
  evolving-anchor model (§2.2/§2.3/§2.4/§2.7.4/§3.2/§3.4/§5.1) is exercised end-to-end by a coupled
  vague-start, drift, premise-inversion and interleave workload.
- **New thread as synthesis of prior threads** — *accounted for at the symbol level.* Multi-parent
  provenance is carried as per-symbol `derived_from` (§2.3/§2.7.3), populated deterministically on
  recall; the non-recall residual is an explicit deferred decision (§2.7.3). The sim models
  synthesis threads that borrow symbols from ≥2 prior threads; recall stays provenance-agnostic.

**Open gate requirements.** Each must be accounted for before the gate is met. The list grows as
runs surface new gaps.

- **Workload interleaving.** The workload must interleave excerpts and queries from several
  loosely-related or unrelated topics within a session, with abandon-and-resume and non-sequiturs.
  Until it does, recall/precision numbers describe a too-coherent stream.
- **Topic clustering / deep dives, metronomic edit cadence, cold start.**
- **Transient-data lifecycle (§3.10) fidelity** — a prerequisite to an honest recall-fidelity
  measurement.
- **Topic-tag fidelity under real inference.** The mock scripts topic tags, so tag quality is
  otherwise unmeasured. The inference-in-loop rung reports, as first-class metrics: re-engagement
  miss rate (turn engages a known thread, tag omits it), spurious `*new-topic*` rate (tag cuts a new
  thread where the oracle expects re-engagement), and anchor-emission overlap vs. the deterministic
  extraction pass.
- **Embedder-enabled completeness rung.** A live embedder with mock inference over several
  sim-days, asserting that every flush-lag dead-zone probe is surfaced and that at least one was
  observed. The §3.4 flush-lag completeness claim is accepted only when this rung runs green.
- **A1-saturation stress.** At spine-display line lengths and the current partition, Layer A1
  saturates in the low hundreds of threads; a multi-year career accrues thousands, beyond what the
  120-day rung reaches. A stress rung with a synthetic pre-seeded multi-thousand-thread spine
  measures (a) recognition quality across the A1 truncation boundary, (b) whether opportunistic
  recall picks up recognition for threads that fell off the rendered surface, and (c) §3.8 archival
  onset under genuine cardinality pressure.

**Two version lines.** The substrate (the runtime and memory model this spec defines) and the front
end (U/X and feature logic atop it) are versioned independently; `personant version` prints the
current values, `personant --version` prints the same version lines in one-line form, and
`system.bootstrap` records them once per session (§2.8). CONVENTIONS.md, "Versioning &
acceptance", carries the bump contract. The
four-month gate converges the substrate only. Milestone gates:

- Substrate **v0.5.0 — realism convergence.** The gate above, walked to the top rung with every
  identified realism element accounted for. At v0.5.0 work switches to front-end logic.
- Front end **v0.1.0 — minimum interactive set.** Earned once a minimum interactive feature set
  exists, validated by direct human use rather than by simulation.

**A third line, not semver: the home's on-disk `format`.** `<home>/version.toml` (§2.1) carries one
integer, `format = N`, recording the layout revision the home was written in. It advances
independently of both semver lines.

**Gating** runs at the process boundary, **before** startup recovery (§4.5.8): recovery resets the
worktree to a recovery point and must never run against a layout this binary cannot interpret. The
open sequence is format gate → startup recovery → session load.

- **Equal** → proceed.
- **Newer on disk → refuse.** `--allow-newer-home` overrides the refusal for one invocation; a
  waved-through open emits a stderr warning and a `system.home-format-override` event (§2.8).
- **Older on disk → refuse**, pending a registered migration.
- **No stamp → adopt forward.** A home predating versioning is taken to be format 1 and stamped as
  such — always 1, even after the current format advances, so it takes its migration.

`personant version` is **ungated**: it reports the binary's identity, the resolved home path, and
the home's stamp (a concrete revision, *absent*, or *unreadable*) and exits 0 either way. It never
runs the gate or recovery and never fails on a home it cannot read.

### 9.2 Mock LLM client

Tests exercise the full runtime path except the LLM round-trip, using a mock with two modes:

- **Scripted**: a preloaded queue of canned responses; each call returns the next. Deterministic,
  replay-friendly.
- **Generated**: on demand from a seeded RNG. Body text carries a counter suffix or seed-derived
  UUID so no two responses dedup accidentally. The caller specifies topic-tag content, anchors
  emitted, and response length. Format-correct per §5.1.

The same mock serves every test layer (scenario, churn, calibration, the acceptance sim).

### 9.3 Unit testing

Unit testing is layer 1 of the measurement stack (ARCHITECTURE.md, "Testing as the lab bench"); no
mock LLM is needed at this layer.

### 9.4 Measurement regime

Four test layers — scenario, churn, calibration, and the acceptance simulation — run against the
mock LLM client (§9.2) with logical-clock acceleration, so the full simulation completes in minutes.
Every layer emits a machine-readable metrics blob with a stable JSON schema for cross-version
comparison.

**v0.1 acceptance metrics** (normative; must hold through the four-month simulation):

- **Recall fidelity:** precision/recall/F1 per scenario step against ground-truth expected matches.
  Strict mode fails the test on mismatch; measure-only mode records adversarial probes without
  failing. Regressions are tracked against stored per-scenario baselines. Series:
  `recall_fidelity_{precision,recall,f1}` and `recall_fidelity_adversarial_{precision,recall,f1}`.
- **Engagement accuracy:** tagged-engaged threads match canonical-by-construction ground truth.
- **Heap bounded / zero overflow:** the token-ceiling criterion of §9.1, backed on mock rungs by a
  per-turn check that each rendered layer stayed within its byte allocation; the byte check is
  skipped on live-inference rungs.
- **Steady-state trajectory:** working-set size, spine cardinality, and per-operation latency
  (P50/P95/P99) flat after 120 simulated days.
- **Round-trip fidelity:** archive → recover → diff against original; information-preservation rate
  measured.
- **Operation latency within bounds:** engagement update, spine match, thread fetch, retirement,
  archival, recovery, index rebuild, index check — stable as accumulated state grows.
- **Oracle agreement with the runtime's Layer B:** the workload generator's own model of Layer B
  membership, from which it derives expected recall matches, agrees exactly with the runtime's Layer
  B after every turn, in both directions (`layerb_shadow_divergence`,
  `layerb_shadow_reverse_divergence`). Zero divergence is a gate on mock rungs; under live
  inference, where the real model engages off-plan, it is reported only.
- **Intra-thread descent divergence** (`recall_intra_descent_divergence`): how often the approximate
  summary-tree descent (§3.4) substitutes a different leaf than the exhaustive scan. A reported
  quality measure, **not** a gate.

**Invariant validators** run after every operation in churn sequences and at key checkpoints in
scenario tests. They cover spine integrity, index freshness, project references, last-active
validity, thread-metadata/spine agreement, engagement consistency, thread accounting, closed-thread
consistency, archive resolvability, dedup consistency, and per-layer budget. Cheap checks run every
step; substrate-scale checks run once per sim day and always once at end of run.

**Named scenario set** (initial): single-thread lifecycle, multi-thread interleaving, project
switching, heavy retirement (50 threads), same-anchor collision, transient shell-capture content,
cross-boundary recovery. Churn sequences are randomized but seeded (80% engage / 10% create / 5%
retire / 5% switch); a failure dumps the operation log and seed for replay. Calibration sweeps a
directive parameter across a range and emits a metrics matrix — the instrument by which §2.6.1
defaults earn their values.

**Acceptance simulation harness**: a deterministic seeded workload generator with logical-clock
acceleration, steady-state assertions, and per-operation cost profiling, run at a chosen span in
calendar days (ladder: §9.1). Output: metrics JSON plus a human-readable summary; pass/fail per
§9.1.

The harness measures only through the event log (§2.8), never by introspecting runtime state, and
tests pin the scraped event names so a rename fails a test rather than silently zeroing a series.

#### 9.4.1 Workload cadence model

The workload's simulated clock is a **single UTC time value**, anchored at **2026-05-04 00:00 UTC**
(a Monday at midnight). Every other time-shaped value — current date, day index, day-off,
termination — is derived from it, never tracked in parallel. The harness clock is set from each
step's emitted time, never integrated separately, so there is exactly one clock.

- **Day grid.** Days are midnight-to-midnight buckets. Day *N*'s work starts at anchor + N·24h + 8h
  ± 15min (08:00 ± 15min, jitter drawn deterministically from the seed). At end of day the clock
  jumps to day *N+1*'s start; it never carries the accumulated day forward, so the work pattern
  never precesses. A run spans `Duration / 24h` days (a 240h run = 10 days); the day index
  `int((clock − anchor) / 24h)` ticks once per midnight and drives the day-close and the heavy
  invariant cadence.
- **Two 6h sessions + ~1h break.** A work day emits two sessions of ~6h turn-active time each,
  separated by a fuzzed ~1h break (~08:00 → ~21:30). Within a session the clock advances per turn by
  a fuzzed span centered at ~72 s (turn plus pause); a session emits turns until its cumulative span
  crosses 6h. A work day is therefore ~600 natural turns; injected engagements and probes raise the
  observed count.
- **Emergent overnight.** The ~11h overnight idle arises from the next day's re-anchor, never from
  an additive constant.
- **Weekly day-off from the calendar.** A day is a day-off iff its un-jittered grid date is a Sunday
  (day index 6, 13, …); there is no modular day counter. A day-off emits no turns, only a
  consolidation marker step that drives the sleep cycle. Its idle span crosses the §3.5 wall-clock
  decay threshold.
- **Each daily record is dated the day it represents.** The day-close for day *d* is stamped with
  day *d*'s own grid date (anchor + d·24h), so the 0-turn Sunday record lands on the Sunday.
- **Day-off keeps phase.** The day-off occupies its own day-index slot and re-anchors like a work
  day that emits no turns; work resumes at the same time of day (±15min) as before it, with no
  systematic phase offset. A run of at least 9 days asserts that work boundaries after the day-off
  stay on grid, the day-off advances exactly one 24h slot on each side, exactly one 0-turn day-off
  record appears with contiguous `sim_date`, and the day count equals `int(Duration/24h)`.
- **Determinism.** `(Seed, Duration)` → output is byte-exact: all fuzz (start jitter, per-turn
  spans, break, session boundary) draws from the single seeded RNG in fixed order, and the day count
  is a function of `Duration` alone. Same seed and duration → identical step stream.
- **Clock frozen within a turn.** The simulated clock advances once per step; every read inside one
  turn returns the same instant. This is benign because the logical clock is the turn number:
  closure/decay, staging, file-chain aging and history ordering key off turns, and the simulated
  clock is consumed only at coarse grain (§3.5 wall-clock decay, provenance/display timestamps).
  Revisit if sub-turn logic orders events by timestamp or metrics become timestamp-grained.
