# Personant — v0.1 Specification

**Audience:** implementation work. Specifies field-level schemas, algorithms, and surface APIs.
**Companion docs:**
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — orientation, principles, patterns, mechanisms, anti-patterns. **Read first.**
- [`AGENTS.md`](AGENTS.md) — house rules for agents in this repo.
- [`README.md`](README.md) — user-facing description; getting started.

This spec is the *what and how*: schemas, algorithms, surface APIs. Tables and diagrams that already live in `ARCHITECTURE.md` are not reproduced here unless the spec needs them at higher resolution.

---

## 0. Document conventions

- **JSONL schemas** are documented in TypeScript-style `interface` notation for clarity. The runtime implementation is Go; the TypeScript notation is purely descriptive.
- **Field markings:**
  - `field: T` — required.
  - `field?: T` — optional.
  - `[derived]` — regenerated from canonical sources; never hand-edited.
- **Pseudocode** is given in Go-flavored syntax where appropriate.
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
  archive/
    index.jsonl                     # canonical; deep cold archive index (§3.8); recoverable git-based archival (active)
  recovery/
    quarantine/<stamp>/             # operational; byte-exact preserved bytes from torn-turn / verify-failure recovery (§4.5.8)
  .git/                             # primary git DB: permanent career history — one day-grain commit/day + archival anchors (§4.5.8)
  .git-daily/                       # operational, gitignored: DISPOSABLE per-turn recovery DB; nuked + reborn each day barrier; never career history (§4.5.8)
  tmp/                              # agent's drafting scratch (see §6.3); never git-committed
```

**File ownership classification:**

| Type | Examples | Drift policy |
|---|---|---|
| Canonical | `spine.jsonl` rows, `threads/*.md`, `directives/*.md`, `projects/prj_<n>/meta.json`, `logs/*.log`, `providers.toml`, `config.toml`, `version.toml`, `archive/index.jsonl` | Source of truth. Hand-editable. Other files derive from these. (`logs/*.log` is canonical, not operational: it is the append-only turn-grain forensic record that crash recovery preserves and re-appends — §4.5.8, §9 non-goals.) |
| Derived | `symbols.jsonl`, `projects/prj_<n>/digest.json` | Regenerable from canonical. `autogit.CheckDerivedFresh` fails any state-changing git op on stale. Never hand-edited. |
| Operational | `.git/`, `.git-daily/`, `tmp/`, `last-active`, `history`, `turn-journal.jsonl`, `op-in-progress.json`, `derived-watermark`, `recovery/` | System-managed; not subject to drift checking. `tmp/`, `last-active`, `history`, `.git-daily/`, `turn-journal.jsonl`, `op-in-progress.json`, and `derived-watermark` are gitignored; `.git/` and `.git-daily/` are both in the managed gitignore block so neither repo sees the other as untracked (§4.5.8 INV-6). |
| Secret-bearing | `api_keys/*` (apiKeyFile targets); `providers.toml` only if it uses `apiKeyUnsafe` | Contains API keys. Treated specially by the runtime: never included in any LLM-context artifact, log line, ack prompt, or captured output. See §8.2.1. |

**Storage commands** (Go binary subcommands; see §4.1):
- `personant init` — first-run scaffold, idempotent.
- `personant index rebuild` — regenerate all derived files from canonical.
- `personant index check` — validate derived files match what rebuild would produce; non-zero exit on mismatch.
- `personant verify` — full structural validation (schema, ID uniqueness, anchor cardinality bounds, etc.).

### 2.2 Spine record schema (`spine.jsonl`)

One JSON object per line, sorted lexically by `id`. Line-grain diffs preserved by deterministic ordering.

```typescript
interface SpineRecord {
  // identity
  id: string;                       // "thr_<n>"; monotonic serial; matches /^thr_\d+$/
  project: string;                  // stable project handle "prj_<n>" (see §2.5.1); display name dereferenced from project meta. "prj_default" reserved for unscoped threads.

  // recall material
  anchors: string[];                // [derived] 0..AnchorProjectionMax (default 8) normalized anchor symbols; a re-derived projection of active history_symbols (see §2.7.x), not a frozen birth certificate
  anchors_projected_at_turn: number; // [derived] staleness watermark: owner-turn index at which the anchors projection last changed (idempotent-write guard, §3.2). Decodes 0 on old records.
  summary: string;                  // 100-150 char gist; ≤ spine.entry-max-chars (default 200); curator-drafted at retirement (§3.5)
  description: string;              // triggering utterance; set once at creation, never rewritten (§2.3). Distinct from summary. Decodes empty on old records.
  state: ThreadState;               // see §2.2.1

  // timestamps (RFC3339)
  created: string;
  last_engaged: string;             // required because (a) closure auto-prompt compares it against `engagement.decay-time` [§3.5], (b) dormant-thread eviction order is oldest-`last_engaged` first [§3.1], and (c) cross-project digest `recent_anchors` selection uses it [§3.7]
  state_changed: string;

  // bookkeeping
  turn_count: number;               // total turns this thread has been engaged in
  recall_fires: number;             // count of recall matches that resulted in fetch; required because (a) the symbol index sorts candidate threads by this count so high-recall threads naturally surface first [§2.4], and (b) the anchor-quality tuning loop uses the distribution to decide when anchor replacement is warranted
}
```

**Hard limits enforced by `personant verify`:**

| Field | Constraint |
|---|---|
| `id` | regex `/^thr_\d+$/`, globally unique |
| `project` | regex `/^prj_\d+$/`, references an existing project (or `prj_default`) |
| `anchors.length` | between 0 and `AnchorProjectionMax` (default 8) inclusive. **Anchors are `[derived]`, not a frozen birth certificate:** the field is a deterministic re-derived projection of the thread's *active* `history_symbols` (the lifecycle state machine in §2.7.x), recomputed every owner turn at close. 0 anchors is legal — a vague-start thread that has not yet accreted a headline topic. The 4-minimum floor is **deleted**. The upper bound is a runtime self-assertion the projection owns (it takes the top-`AnchorProjectionMax`); `personant verify` flags only an over-ceiling count (a substrate bug — the model never controls this count, per §5.1). `AnchorProjectionMax = 8` is a §9 calibration window (inherits the Phase C.6 Jaccard dilution ceiling; re-confirmed under drift in the sim — not final). |
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

Spec for human-readable spine line rendered from a `SpineRecord`:

```
thr_<id> [<anchors joined with ", ">] — <summary> [<state>]
```

Example:
```
thr_88 [trefoil, unknot, body-topology, electron-shape] — electron body-topology conflict; entries trf3bd / unk0bd added; awaiting Grant resolution [WIP]
```

The display form is what the LLM sees in working context; it is generated from the JSONL record, not stored.

### 2.3 Thread file format (`threads/thr_<id>/`)

A thread is a **directory**, `threads/thr_<id>/`, holding three things:

- **`thread.md`** — YAML frontmatter (canonical for metadata) followed by a single `# <title>` line. Small and bounded; no accumulating body. Rewritten in full on every engagement — that is cheap precisely because it carries no body.
- **`turns/`** — a directory of serial turn-excerpt files, one per primary-engagement turn, named by the thread's turn number zero-padded to 7 digits (`turns/0000024.md`). Each file holds that turn's terse operational excerpt.
- **`files.json`** — the §3.9 tracked-file sidecar (see §3.9).

The single growing markdown file of earlier drafts is gone: appending a turn was O(thread length) per engagement and O(K²) over a thread's life, which the §11.1 simulation surfaced as a severe superlinear slowdown. Splitting the body into one file per turn makes appending O(1) — write one small file, FIFO-evict one — and decouples per-turn write cost from thread length.

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

**Recency window (assembly) vs. retention (disk).** The working-set assembler reads at most `ThreadTurnWindow` (currently 512; a calibratable count) of a thread's most-recent turn-excerpts into the live body — calibrated against the Layer B budget: at `b-top-k = 3`, each thread's assembled window stays within LayerB/3 bytes. **This window governs *assembly into context*, not on-disk retention.** Turn-excerpts are **retained** on disk past the window: an excerpt is decision-class content (the terse `user:`/`agent:` turn rendering — raw task-class tool output is a transient §3.0 context delta and is *never* written as an excerpt), and the retained excerpts are the durable source the §3.4 chunk-level index embeds for **intra-thread recall** of early content. Earlier turns scroll out of the *assembled context* but remain on disk and recall-indexed; `history_symbols` additionally distills symbol memory. The body assembled for a prompt is read newest-first up to a byte budget, then joined oldest→newest so it reads naturally top-to-bottom.

> **Correction (the durable-content source).** An earlier draft said scrolled-out detail "remains recoverable from the §2.8 event log." It does **not** — the event log records only event *metadata* (`source=` + byte count), never content (§2.8). Durable turn content lives in the **retained excerpts**, which is why the window must not delete them. **Retention trimming is the deferred keep/toss precision layer (§3.10):** routine/fluff turns and the agent's transient tool-data re-presentations can be evicted from retention by the sleep-cycle keep/toss; the v0.1 first cut **over-retains** (decision-class excerpts are kept — safe; losing crucial content is the dangerous error) and trims later.

**`description` vs `summary`.** `description` is the *triggering utterance* — the user prompt that spawned the thread — set once at creation and never rewritten. `summary` is the curator's closure *gist*, set at retirement (§3.5). A live thread has a `description` but no `summary`; a retired thread has both. v0.1 sets `description` deterministically (a whitespace-collapsed copy of the spawning prompt, truncated to the same length bound the new-thread summary uses); no LLM paraphrase is involved.

**No parent thread; symbol-level origin provenance.** A thread belongs to no parent thread — the model stays flat (§3.2): there is no thread-level parent field and none is synthesized. *Symbol*-level origin provenance, however, **is** built: a `history_symbols` entry may record the thread(s) it was carried into this thread from via recall (`derived_from`, §2.7.3). This is provenance on symbols, not edges between threads — **the flat-thread invariant is unchanged.** Thread-level lineage remains reserved.

**Body content guidelines:**
- This is operational, not pedagogical. Terse; machine-friendly.
- Each `turns/<n>.md` file is one turn's excerpt — a turn where this thread was the primary engagement. Files carry no file-level title (the title lives in `thread.md`).
- The retirement summary (the same text that becomes the spine `summary`) is not duplicated in any turn file — the turn files hold detail; the summary is in frontmatter only.

**Frontmatter `history_symbols` structure:**

```typescript
interface HistorySymbol {
  raw: string;                      // surface form as encountered
  normalized: string;               // normalized form (see §2.7)
  first_seen_turn: number;          // turn ID when first emitted
  count: number;                    // total emissions across all turns
  source: SymbolSource;             // see §2.7.2
  lifecycle?: SymbolLifecycle;      // "active" | "superseded"; canonical source of truth for supersession state (see §2.7.x). Omitted ≡ "active".
  ever_central?: boolean;           // latched true the first time the symbol enters the active anchor projection; never cleared. The retention discriminator. Omitted/false ≡ never-central.
  last_active_turn?: number;        // most recent turn the symbol was in the active projection; temporal ordering + eviction tiebreak. Omitted/0 ≡ never-active.
  derived_from?: string[];          // origin thread ID(s) this symbol was carried into this thread from, observed co-incident with a recall hit (§2.7.3); deterministic, set-valued, monotonic. Omitted/empty ≡ organic to this thread (the common case).
}

type SymbolSource = "deterministic" | "model" | "user" | "curator";
type SymbolLifecycle = "active" | "superseded";  // "evicted" is the *absence* of the entry, not a stored value
```

**Zero-value semantics (greenfield, no migration — §2.7.x).** The three lifecycle fields are added plainly; an old record decoding without them is well-formed: `lifecycle == ""` is treated as `active`, `ever_central` false ≡ never-central, `last_active_turn` 0 ≡ never-active, `derived_from` absent ≡ empty ≡ organic. `verify`/index-rebuild regenerate the derived projection from canonical on first run.

`history_symbols` is hard-capped at `history.cap-per-thread` directive value (default 40) — a hard *total* cap over both `active` and `superseded` entries. When the cap is exceeded, eviction operates over the **evictable** partition only: a symbol is evictable **iff** it is `NOT ever_central AND NOT a §2.7.2 high-specificity (B11) identifier` — the *union* of two protections (once-central premises stay a findable abandoned-premise handle; intrinsically discriminative identifiers stay matchable). Within the evictable set, lowest cumulative weight: lowest `count` first, ties broken by lowest `first_seen_turn`. **Graceful degradation:** if the protected set alone exceeds the cap, evict the lowest-`count` `superseded` entries first (an abandoned premise yields before a still-protected active one). Symbols with low cumulative count and early `first_seen_turn` represent brief noise rather than persistent signal; retaining them dilutes the Jaccard matching set and degrades recall precision [§3.4].

### 2.4 Symbol index schema (`symbols.jsonl`)

[Derived] Inverse index. Sorted lexically by `symbol`. Rebuilt from `spine.jsonl` + `threads/*.md` frontmatter on every index refresh.

```typescript
interface SymbolRecord {
  symbol: string;                   // normalized form
  threads: string[];                // thread IDs where symbol appears anywhere (anchor or history)
  anchor_in: string[];              // subset of `threads` where symbol is an anchor
  superseded_in: string[];          // subset of `threads` where this symbol's history entry is lifecycle==superseded; the cheap-scan abandoned-premise surface (§3.4). [derived] from frontmatter lifecycle.
  source_dominant: SymbolSource;    // most common source across emissions
}
```

`threads` is sorted by descending `recall_fires` of the referenced thread (most-recalled first) so that opportunistic-match operations naturally surface high-recall threads first when ties exist. `superseded_in` is sorted the same way and is rebuilt from canonical (`BuildSymbols`, gated on `lifecycle == "superseded"`) — there is no second source of truth for supersession state, which is the frontmatter `lifecycle` field (§2.3).

### 2.5 Project metadata schema

#### 2.5.1 Canonical: `projects/prj_<n>/meta.json`

A project has three layers of identity (see §4.5):

- **Internal handle** (`id`) — `prj_<n>`, stable forever, used as storage key.
- **External canonical identity** (`remote_urls[0]`) — normalized git remote URL when present; gained when the project's git tree first acquires a remote.
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
  conventions_paths: string[];      // absolute paths to convention files (CLAUDE.md, AGENTS.md, etc.) loaded into Layer E
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
- Edge cases (Gerrit, custom SSH hosts, IDN hostnames) handled as they appear in practice.

**`prj_default`** is reserved for threads created before any explicit project was active. It has no path or remote; `meta.json` for it carries empty path fields and a fixed `name: default`.

#### 2.5.2 Derived: `projects/prj_<n>/digest.json`

[Derived] Layer A2 digest content. Regenerated whenever any thread in the project is updated.

```typescript
interface ProjectDigest {
  project: string;                  // project id ("prj_<n>"), matching ProjectMeta.id
  display_name: string;             // resolved at render time; cached for the digest's consumers
  thread_count: number;
  recent_anchors: string[];         // top N anchors aggregated across N most-recently-engaged threads; this is the Layer A2 cross-project recognition surface — the mechanism by which the model recognizes a prior cross-project topic without needing to load any thread body [§3.1, §3.4]
  one_line_summary: string;         // ≤ 80 chars; auto-generated from recent thread summaries
  byte_size: number;                // for budget accounting; should be ≤ cross-project.digest-per-project-bytes
}
```

`one_line_summary` is a comma-joined list of `recent_anchors`, truncated to the field's 80-char length budget. Reserved for revisit if v0.1 sim metrics show this simple form is inadequate for the cross-project recognition signal.

### 2.6 Directive file format (`directives/*.md`)

Directive files are markdown documents with YAML frontmatter for parameter overrides.

```markdown
---
scope: user                         # one of: defaults | user | project
project: null                       # null for defaults/user; project name for project-scoped
parameters:
  engagement.decay-turns: 10        # override system default of 8
  recall.symbolic-threshold: 0.35   # override system default of 0.4
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

When a parameter is read, the runtime walks the precedence chain and returns the first match. Missing parameters fall through to defaults.

**Parameter namespace:** see §2.6.1 for full list of recognized parameters.

#### 2.6.1 Recognized parameters

```yaml
engagement.decay-turns: 8           # turns of non-engagement before closure fires
engagement.decay-time: 7d           # wall-clock equivalent (Go duration string)
closure.ack-mode: auto              # auto | always (AMENDED 2026-08-04, §3.5).
                                    # auto (default): routine decay closures are
                                    # auto-accepted with the curator summary and
                                    # exceptions queue for a boundary drain.
                                    # always: every decayed thread prompts
                                    # interactively in-session (the superseded flow).
                                    # The FIRST parameter served by the directive
                                    # layer at runtime — the rest of this namespace
                                    # is still compiled-in constants.
recall.symbolic-threshold: 0.4      # Jaccard threshold for opportunistic recall surfacing
recall.cross-project-threshold: 0.5 # higher bar for cross-project surface
layer.b-top-k: 3                    # max active threads in Layer B
layer.budget.percentages: {E: 12, A1: 10, A2: variable, B: 55, C: 15, live_turn: 15}
                                    # live_turn is carved from the byte total first;
                                    # E/A1/A2/B/C partition the remaining memory budget
                                    # (A1+A2 = 18% high tier; A2 is the residue)
context.token-budget: 200000        # whole-request TOKEN ceiling (#127); the
                                    # authoritative gate. Per-layer BYTE shares are
                                    # derived (ceiling × ~2.5 conservative B/tok, TEXT-ONLY)
                                    # and drive truncation; usage.prompt_tokens is the gate.
                                    # v0.1 reads this from a const + constructor override
                                    # (directive-file plumbing lands later).
recall.superseded-weight: 1.0       # numerator weight for a matched symbol that is superseded in a thread (§3.4); §9 calibration window — 1.0 = no down-weight (today's behavior)
history.cap-per-thread: 40          # max history symbols per thread
anchor.projection-max: 8            # AnchorProjectionMax: top-N active history_symbols projected to spine anchors (§2.2/§2.7.4); §9 calibration window
spine.entry-max-chars: 200          # hard cap for SpineRecord.summary
cross-project.digest-per-project-bytes: 150
dissect.pressure-threshold: 0.90    # budget-pressure fraction triggering fallback dissection
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

The stop-word filter applies only to multi-word entities. A single-token entity that happens to match a stop word (e.g. the bare entity `"the"`) is preserved as-is — the rule is "drop stop words *from* multi-word phrases," not "reject any input matching a stop word."

**Storage form:** `{raw: original surface, normalized: per-rules-above}`. Matching uses `normalized`; display uses `raw`.

#### 2.7.3 Source

```typescript
type SymbolSource =
  | "deterministic"  // regex-extracted from turn content
  | "model"          // emitted in topic-tag prompt response
  | "user"           // emitted via #tag
  | "curator"        // selected at retirement as anchor
  ;
```

When a symbol is emitted by multiple sources, `source_dominant` in `SymbolRecord` is the most-frequent source across all its history entries; ties broken by `curator > user > model > deterministic`.

**Origin provenance (`derived_from`) vs source (`source`).** `source` records *how* a symbol was emitted (the agent). `derived_from` records *where it came from* (origin thread). The two are orthogonal: a symbol can be `source: model` (the model re-emitted it this turn) and `derived_from: [thr_X]` (it surfaced via a recall hit on `thr_X` that same turn) simultaneously.

**Deterministic population — no LLM, no human.** A parent is surfaced into the Layer-B working set by recall through two paths: §3.4 recall-acceptance at turn-close (which primes the parent for *subsequent* turns' responses) and the §5.5 mid-turn fetch (which surfaces it within the current turn, before that turn's emission). Both promote the parent into Layer B, and a single attribution rule covers both: at each turn's history merge, for every symbol emitted into the engaged thread's `history_symbols`, if that normalized symbol also belongs to a thread **currently resident in the recall-surfaced working set** — promoted by recall and not yet evicted from Layer B — that thread's id is unioned into the symbol's `derived_from`. Timing falls out of residency: a turn-close acceptance attributes one turn later, a mid-turn fetch attributes same-turn — both are just "resident at merge time." The origin is *known* because the recall promotion is known. Eligibility **ends when the parent is evicted from the working set**, so a symbol re-emitted long after its source left context is not falsely attributed (provenance stays honest, not merely ever-recalled). The per-symbol set is monotonic — once attributed, an origin is never cleared. Recall does not *write* symbols into a thread; it surfaces a parent whose symbol a turn re-emits — that coincidence is the deterministic attribution hook.

**The synthesis case.** This is the symbol-level realization of "a new thread synthesizes prior threads": when a thread is started by recalling and combining ≥2 prior threads, the symbols it carries forward from each acquire that parent's id — multi-parent provenance distributed across the synthesized symbols, rather than a coarse thread-level edge.

**Recall is provenance-agnostic.** `derived_from` does **not** participate in the §3.4 recall match — recall scores on the symbols themselves; origin is honest provenance metadata (load-bearing for "where did this come from?" queries and submind merge), not a recall input. Keeping recall provenance-agnostic is deliberate.

**Open (deferred).** Synthesis introduced *without* a recall hit — a human or the model brings forward a prior thread's idea by paraphrase, so no normalized symbol matches — leaves `derived_from` empty (honest: the origin is not deterministically known). Attributing that residual is a fuzzy judgment reserved for an LLM-inference or human-declaration path; it is an explicit open decision, **not built in v0.1**.

#### 2.7.4 Symbol lifecycle and anchor projection

A thread's evolving identity lives in `history_symbols` (§2.3): it accretes per turn, is weighted, is evictable with high-specificity (B11) protection, and is part of the recall match set. The spine `anchors` field (§2.2) is **not** a separate storage tier — it is a deterministic re-derived **projection** of that canonical set. Every state transition here is deterministic; **no LLM prompt is involved.**

**Lifecycle states** (`HistorySymbol.lifecycle`):

- `active` — currently occupying a top-`AnchorProjectionMax` projection slot. The zero value `""` decodes as `active`.
- `superseded` — a once-central symbol that has fallen out of the top-`AnchorProjectionMax` projection (rank-dropout). A **descriptive / eviction-priority label, not a candidacy gate** — a superseded symbol still competes for the projection on equal footing every turn (see "The projection function": candidacy is current salience, not lifecycle). **Retained, not evicted** — it stays in `history_symbols`, so an abandoned premise remains a findable recall handle.
- *evicted* is **not** a stored value — it is the *absence* of the entry (capacity eviction per §2.3).

**The projection function** (`ProjectAnchors`). The projection is a pure function of **current salience** over **all** retained history_symbols — active and superseded alike compete; lifecycle is never a candidacy filter. Rank by:

1. **class** — a §2.7.2 high-specificity (B11) identifier (URL, file path, git SHA) ranks above an ordinary symbol of equal count;
2. **count** descending;
3. **recency** — `last_active_turn` then `first_seen_turn`, newest first;
4. **normalized** ascending (stable final tiebreak).

Take the top-`AnchorProjectionMax` as the active slice → `SpineRecord.anchors` (in rank order). Zero active symbols project to an empty anchor list (the legal vague start). **`ever_central` is deliberately not a ranking tier** — it is the eviction-retention discriminator only (below). If it boosted rank, a once-central premise could never be outranked and supersession (the inversion case) would be impossible.

**State transitions** (deterministic; **no TTL** — a timer would wrongly demote still-valid anchors in a quiescent thread and would miss inversion). These transitions are **emergent labels read off the re-ranked top-`AnchorProjectionMax` each turn**, not a state machine that gates candidacy. There is exactly **one entry transition and one exit transition**:

- *entry* — a symbol that occupies a projection slot this turn latches `ever_central = true` (never cleared), sets `last_active_turn = turn`, and is `active`. This is the SAME transition whether the symbol is newly central for the first time OR was previously `superseded` and has now re-ranked back in. **Reappearance of a superseded symbol is not a distinct "return to active" pathway** — its rank simply rose (e.g. its `count` climbed) above the top-`AnchorProjectionMax` cut, so the ordinary entry transition fires. Implementers must NOT build re-entry machinery; there is nothing to build beyond ranking all symbols and re-labelling the result.
- *exit* — a symbol that *was* `ever_central` but no longer holds a top-`AnchorProjectionMax` slot flips to `superseded` (rank-dropout supersession — captures a still-mentioned-but-outranked premise demoting).

**Ever-central = projection-entry latch.** A symbol is "historically central" iff it has *ever* made the headline projection — not merely been mentioned often. This single latched bool (plus `last_active_turn`) is the whole representation; no peak-rank/peak-weight magnitude is stored (no algorithm consumes it; the eviction tiebreak uses `count`).

**Capacity eviction predicate** (§2.3, restated): evictable **iff** `NOT ever_central AND NOT B11-high-specificity` — the union of protections. Within the evictable set, lowest `count`, ties by `first_seen_turn`. **Graceful degradation** when the protected set alone exceeds `history.cap-per-thread`: evict lowest-`count` `superseded` entries first. A symbol is never evicted while `ever_central` so long as the protected set fits the cap.

**Ordering invariant** (per turn, before the spine/frontmatter write): **merge → project → evict.** Projection must run on the full merged set before eviction, so a symbol entering the projection this turn has its `ever_central` latch set *before* the eviction predicate reads it — otherwise a freshly-central symbol would be unprotected and could be wrongly evicted the same turn.

**Steady-state bound.** `history.cap-per-thread` (40) remains the hard total cap (active + superseded); the projected slice is hard-bounded at `AnchorProjectionMax` (8). Ever-central count is bounded by the number of genuine central topic-shifts (tens, not hundreds); under pathological pressure graceful degradation evicts superseded-first. The empirical flatness of `history_len`/`ever_central_count` is the §9 sim's steady-state obligation, not a static guarantee.

### 2.8 Log event format (`logs/YYYY-MM-DD.log`)

Plain-text, append-only, one event per line. Free-form details after a fixed prefix.

**Line format:**
```
<RFC3339-timestamp> <event-name> <free-form-details>
```

**Event-name convention:** dot-separated category and action (`thread.engaged`, `spine.match-fire`, `retire.prompt`).

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

No JSON schema in v0.1. Promote individual event types to structured form when query patterns become repetitive enough that grep/awk friction matters.

**Initial event vocabulary** (will grow during implementation). The event vocabulary is the minimum required to satisfy §9 measurement contracts and the directive-accrual feedback loop. Events absent from this vocabulary cannot be measured; any new mechanism that emits a decision event must add a corresponding entry here before the mechanism is considered measurable.

Actions ending in `-error` (and `warning`) are forensic diagnostics, not measured decision events; they are listed so the vocabulary is complete, but §9 measurement keys off the decision events. Rows marked *(vocabulary; not yet emitted)* are reserved for planned mechanisms.

| Category | Events |
|---|---|
| `system` | `bootstrap` (the VERSION/IDENTITY line — one per process open, emitted at the process boundary after the §9.1 format gate resolves: `version=` substrate, `frontend=`, `home-format=` the EFFECTIVE on-disk revision this session ran against, `commit=` (`unknown` on an unstamped binary), `home=`. It is not the session/project event — that is `session.started`), `home-format-override` (a `--allow-newer-home` open waved a newer home through the §9.1 refusal — `on-disk=`, `binary=`; knowingly-unsafe, always paired with a stderr warning), `context-ceiling-breach`, `context-ceiling-unenforceable` (the post-flight token-ceiling gate could not be EVALUATED — the provider reported no `usage.prompt_tokens` for a streamed turn while a ceiling was configured, with `reason=usage-unavailable ceiling= turn=`. A guard that cannot read its input must say so rather than passing silently; the count is deliberately never estimated, since a guessed number would make the ceiling look enforced when it is not. One line per affected turn — the count of unenforced turns is the measurement), `empty-response` (forensic; the final drained response carried zero visible content — with `reprompted=yes\|no`, `turn=`; §3.3 empty-response recovery), `turn-aborted` (the user retracted a turn with Esc — `turn=` the #94 transaction id, `phase=` the labelled stage they gave up in, `bytes=` the retracted input's size; §4.3.3. **Never the text**: an event line is single-line free-form and a multi-line prompt would break the format — and the text is deliberately not durable anywhere, per the retraction rule), `turn-abort-release-error` (forensic; releasing an aborted turn's recovery scope failed); *(vocabulary; not yet emitted)* `shutdown`, `error`, `config-reload` |
| `thread` | `engaged`, `engaged-non-owner`, `engaged-cross-project`, `engaged-miss`, `created`, `created-meta-only`, `state-change`, `fetch-miss`, `fetch-cross-project`, `anchor-projection-overflow`, `tag-defaulted` (with `thr=` — the bound owner, `cause=missing-tag\|empty-response`, `reprompted=yes\|no`; §3.3 owner-default) |
| `spine` | `match-fire`, `embed-match-fire`, `intra-match-fire`; *(vocabulary)* `match-miss`, `entry-updated` |
| `recall` | `offer` (with `count=N`), `accept` (with `thr=`, `layers=`), `decline` (with `thr=`, `reason=not-relevant\|wrong-project\|already-known`), `net-cap-hit`, `flush-backlog`, `W1-diag`, `fire-error`, `error`, `index-error`, `embed-error`, `debt-window-error`, `tree-error`; *(vocabulary)* `cross-project-fire` |
| `retire` | `prompt` (with `thr=` and `inactivity=`\|`trigger=manual`\|`trigger=queued` — the last for a §3.5 boundary drain), `ack` (EVERY applied closure — retire or WIP — with `resolution=`, `edited=yes\|no`, and **`ack=human\|auto`** (AMENDED 2026-08-04, §3.5): `auto` is a routine closure the runtime applied without asking, and the ack-edit-rate canary is a rate over `ack=human` lines ONLY, since an auto-accept is unedited by construction and would dilute the rate to zero), `pending` (a decayed thread classified as an EXCEPTION and queued for the boundary drain rather than auto-accepted — `thr=`, `inactivity=`, `reason=anchors=N\|turns=N`; emitted ONCE per idle episode, not on every scan the thread keeps waiting — the queue is derived, so a per-scan line would be a heartbeat rather than a decision event), `defer`, `complete` (with `resolution=`), `curator-error`, `load-error`, `resolver-error`, `apply-error`, `error` |
| `archive` | `archived`, `recovered`, `recovered-record`, `skip`, `under-drain`, `error` |
| `recovery` | (§4.5.8 startup reconciliation; forensic) `begin`, `complete` (with `cells=`, `reset=`, `stamped=`, `unrepairable=`), `pending` (with `op=`, `day=` — a barrier/archival completion was typed to the adapter), `rollback` (torn-turn reset — `turn=`, `reverted=`, `debris=`), `journal-recovered` (preserved in-flight bytes — `turn=`, `records=`), `morning-init` (`baseline=`, `rebuilt=`), `adopt` (cell-12 greenfield/legacy), `stamp-repaired` (cell-9), `unrepairable` (archive entry stays refused — `thr=`, `reason=`), `quarantined` (byte-exact preserved path), `log-tail-repaired` |
| `barrier` | (§4.5.8 day barrier B0–B6; forensic) `begin` (`day=`), `archived` (B1 — `day=`, `count=`), `day-committed` (B2 — `day=`, optional `repaired=true`), `reborn` (B5 daily re-init — `day=`, `baseline=`), `complete` (`day=`), `re-mint` (defensive B2 re-mint on a non-day-shape HEAD — currently unreachable), `quarantined` (§4.5.8 quarantine-and-proceed — `paths=`, `dir=`, `restored=`), `tag-error` (forensic; a lifecycle-tag derivation read/parse/tag fault — never fatal to the seal) |
| `consolidate` | `sleep-cycle` |
| `dedup` | `chain-aged`, `chain-age-refused`, `error` |
| `fs` | `edit-no-path`, `write-error`, `commit-untracked` (`unsynced-no-topic-tag` is **retracted** with the missing-tag abort — §3.3 recovery means edits always bind) |
| `staging` | `promoted`, `evicted` (window-close GC, §3.10) |
| `topic` | `re-prompt` (with `cause=missing-thread` + `fetched=` for the §5.5 fetch, `cause=missing-tag` for the §3.3 tag recovery, or `cause=empty-response` for the §3.3 empty-response recovery), `tag-missing`, `tag-invalid` (forensic; a near-miss tag candidate that failed strict validation — `reason=markdown-mangled\|bad-delimiters\|bad-thread-list\|bare-new-topic` + a short sanitized `snippet=`; ADDITIVE to `tag-missing`, not a measured decision event), `warning` |
| `session` | `started` (with `active=`, `provider=` — the session/project event, distinct from the `system.bootstrap` identity line), `ended`, `working-set-save-error`, `checkpoint-error` |
| `project` | `created`, `switched`, `renamed`; *(vocabulary)* `cd-changed`, `remote-adopted`, `remote-updated`, `remote-collision-prompt`, `meta-updated` |
| `model` | `switched` (ADDED 2026-08-05; the user changed the session's chat model or provider with `/model` — `from=` and `to=`, each a `provider/model` reference. Model identity changes how the replayed history reads, so the switch must be reconstructable from the log; it is deliberately the ONLY record of the change, since the switch is session-scoped and never serialized to `config.toml` or home state, §4.2), `stream-close-warn`, `inference-telemetry` (the provider's extended inference-timing block, one line per turn: `ttft_ms=`, `prefill_ms=`, `gen_ms=`, `total_ms=` — MILLISECONDS, converted from the wire's fractional seconds — plus `prefill_tps=`, `gen_tps=` (the provider's own tokens/second figures), `prompt_tokens=`, `completion_tokens=`, `cached_tokens=` (prompt-cache hits), `turn=`. The prefill-vs-generation split is what separates a PREFILL-bound slow turn — the reassembled context is too large — from a GENERATION-bound one, which is the primary slow-turn diagnostic for a design that rebuilds a large context every turn; `cached_tokens` gives prompt-cache visibility over the same surface. It is a **de-facto extension**, not OpenAI schema: MLX-backed servers (oMLX via litellm) send it, most providers do not. Absence is normal and is **never an error or a warning** — the line is simply NOT emitted, rather than emitted all-zero, because an all-zero line every turn would train the reader to skip the event) |
| `workset` | `warning` (layer render failure, §3.1) |
| `context` | `modified` (with `source=`, `bytes=`; the §3.0 chain step, §3.0.1). **Per-turn boundary contract:** exactly one `modified source=user.prompt` line is emitted per turn, and every other line a turn produces follows it before the next turn's boundary — the log is a concatenation of per-turn segments. This is load-bearing: the A2 tag-fidelity grader segments observed turns on it (same-second turns would otherwise smear into one group), so a change that suppresses, reorders, or duplicates the user.prompt delta line must revisit that grader. |
| `dissect` | *(vocabulary; not yet emitted)* `fire`, `cluster-proposed`, `complete` |
| `directive` | *(vocabulary; not yet emitted)* `accrual-update`, `parameter-read` (sampled) |
| `index` | *(vocabulary; not yet emitted)* `rebuild-start`, `rebuild-complete`, `verify-fail` |
| `user` | `shell` (one line per §4.4 invocation: `kind=` the `$`/`#` discriminant, `exit=` the command's status (NEGATIVE when the shell was killed by a signal — the Ctrl-C path), `dir=` the shell cwd after the command, `cmd=` the invocation, quoted so a multi-word or newline-bearing command stays on one line. For `#` additionally `bytes=` the CAPTURED byte count, plus `truncated=yes produced=` when the in-memory capture bound clipped the output. **Never the captured content** — an event line is single-line free-form and a capture is large and multi-line; `cmd=` is passed through the §8.2.1 redactor first, because the never-in-a-log-line rule for key material does not care which end typed the bytes), `shell-capture-dropped` (a session ended with `#` captures still buffered for a prompt that never came — `count=`; the captures are discarded, §4.4.1); *(vocabulary; not yet emitted)* `slash` (see §4.2) |

---

## 3. Core algorithms

### 3.0 Context-modification events (the architectural primitive)

Personant treats the working window as a sequence of **context-modification
events**, not as user-agent turns. A "turn" is a logging convenience —
useful for grouping events into human-readable spans — but the
load-bearing primitive is the per-event hook chain. The principle: **no
gaps**. Any path by which content enters the working window is a path
through this chain, so the runtime never loses an opportunity to
extract, dedup, or budget-check.

#### 3.0.1 The event taxonomy

| Event | Source | Content shape |
|---|---|---|
| `user.prompt` | user types or pastes | message text |
| `user.shell-capture` | `# <cmd>` stdout/stderr (§4.4.1) | captured bytes (subject to §6.5 byte cap) |
| `model.response` | LLM emits final reply (or response segment) | response text + optional tool-call envelope |
| `tool.result` | each tool dispatch returns | tool output bytes |
| `thread.fetched` | topic tag triggered a thread load into Layer B | thread body + frontmatter |

This taxonomy is exhaustive of the events the runtime actually emits as
context-modification deltas through the §3.0 chain. It deliberately
**excludes** content that enters the working window by other documented
paths and is therefore not a chain delta:

- **Cross-project digest content** reaches Layer A2 through
  `workset.Compose` (§3.1), which renders the digest directly into the
  composed context — it is not announced as a `digest.refresh` delta.
- Slash-command effects (`/cd-project`, `/back-to`, etc.) and
  directive-file reloads change *which* content `workset.Compose`
  assembles; the content they surface arrives through the existing
  `user.prompt` / `thread.fetched` / Layer-A2 paths, not through a
  dedicated chain delta.

Earlier drafts listed `digest.refresh`, `slash.injected`, and
`directive.reloaded` here, but the runtime never emits them as deltas;
they were pruned (m2) so the taxonomy matches the emitted event set —
the §3.0 "no gaps" principle requires the table to enumerate the actual
chain inputs, not aspirational ones.

Future capabilities (deep-cold recovery, dedup-promotion of identifiers
back to literal) add more event types via the same chain.

#### 3.0.2 The hook chain

Each context-modification event runs the chain, in this order:

1. **Symbol extraction** (§3.3) — deterministic regex pass over the
   delta's content extracts identifiers (file paths, URLs, claim IDs,
   user `#`-tags). Model-emitted symbols (in `topic` blocks) are also
   parsed here. Class-conditional routing per the delta's retention
   class (§3.10.1) follows immediately: decision-class extractions go
   to the per-turn coalesce buffer; task-class extractions enter the
   staging buffer (§3.10.2).
2. **Engagement signal** (§3.2) — does the delta's symbol set overlap
   any active thread's anchors above threshold? If so, fire engagement
   for those threads. Engagement updates are coalesced per-turn (see
   §3.0.4) so 5 deltas hitting the same thread don't multiply
   `turn_count`.
3. **Dedup decision** (§3.9) — is the delta a duplicate or near-duplicate
   of content already in window? Apply identifier-replacement or
   diff-encoding per §3.9.2. Persistent storage (§3.9.1) is also updated
   at this step.
4. **Budget check** (§3.1) — did the delta push the working window over
   its layer budget caps? Bump-eviction fires immediately so the budget
   is honored before the next event lands.

   > **v0.1 non-goal (turn-time budget enforcement is deliberately
   > deferred past v0.1).** This step is a documented no-op in v0.1
   > (`internal/turn/chain.go`, `TODO(phase-2-budget)`); no eviction
   > fires here. The only v0.1 budget protections are (a) the FIFO
   > turn-pair cap on each thread's `turns/` window (§2.3,
   > `ThreadTurnWindow`) and (b) blind, render-time byte truncation in
   > `workset.Compose` (§3.1). Neither acts at delta time. Consequence
   > (sim-vs-reality MAD finding B7/T0-2): attaching a *verbose* real
   > LLM — one whose per-turn deltas can outrun the layer caps — risks
   > silently starving Layer B/C, because nothing reclaims budget
   > between deltas; the mock's bounded responses hide this. Enabling
   > enforcement here is a separate, deliberate decision (deferred
   > T3-3), not an incremental fill-in. A regression guard
   > (`TestBudgetCheckIsExplicitNoOpV01`) trips if the no-op is half-
   > enabled without that decision.
5. **Logging** (§2.8) — emit the source-specific event (`tool.result`,
   `user.shell-capture`, etc.) plus a `context.modified` event if the
   delta materially changed window contents.

The chain runs at **semantic boundaries** — complete response segment,
complete tool result, complete capture — not per token. If the LLM
streams, hooks fire on stream-completion (or on logical sub-segment
boundaries, TBD per provider).

#### 3.0.3 Hook-firing order matters

Within the chain:

- Symbol extraction must run before engagement (engagement depends on
  the extracted set).
- Dedup runs after extraction (extracted identifiers are the things we
  may dedup against).
- Budget check runs last (eviction may displace content; that
  displacement is itself a context modification, but recursive hook
  firing is **capped at depth 1** — eviction emits a log line and is
  done).

#### 3.0.4 Per-turn coalescing for engagement

A "turn" is bracketed by consecutive `user.prompt` events: every
context-modification event between two user prompts belongs to the same
turn for engagement-coalescing purposes. Within a turn, the symbol set
is union-accumulated across all deltas; engagement updates fire once
per affected thread with the union as input. This prevents
N-tool-call turns from inflating `turn_count` for every thread mentioned
in any tool result.

`last_engaged` updates to the *latest* delta's timestamp. `turn_count`
increments by 1 only for the turn's **owner** thread (§3.2) — the single
thread that received the turn's excerpt — regardless of how many deltas
touched its anchors. An engaged-but-not-owner thread had no turn added to
it, so its `turn_count` is unchanged.

#### 3.0.5 Implementation contract

The runtime exposes a single internal entry point — roughly
`onContextDelta(source, content, metadata)` — that every content-emitting
code path calls. The chain is not optional; there is no path by which
content reaches the working window without passing through it. Code
review treats any direct working-window mutation that bypasses this
entry point as a bug.

### 3.1 Working-set composition

The working set is rendered into the system prompt as five layers, each
truncated to a byte budget derived from `context.token-budget` and the
`layer.budget.percentages` table (§2.6.1). The token ceiling is the
authoritative whole-request gate (#127); the per-layer **byte** shares
are derived from it (ceiling × a conservative ~2.5 chars/token ratio,
text-only) and serve as the truncation driver. The live-turn reserve
(`live_turn`, 15%) is carved from the byte total first; the memory layers
E/A1/A2/B/C partition the remainder (12/18/15/55, with the 18% high tier
split between A1 fixed and A2 residue). The whole assembled request — system
prompt + replayed history tail + current user input — is bounded against
the token ceiling: bytes pre-flight (this composition + the live-turn
sub-policy in `internal/turn`), `usage.prompt_tokens` post-flight as the
real-model gate (see §6.5). *Known accounting gap (#127 follow-up):* the
partition allocates 100% of the byte total to the layers + live-turn
reserve; the **static prompt scaffolding** (orientation preamble, §5.1.3
topic-tag directive, D6 reminders, section headers — ~1.7 KB) has no
share and rides on top of the byte proxy. Harmless against the
authoritative token gate (the 2.5 B/token ratio is deliberately
conservative), and the A4 acceptance test bounds it explicitly
(`Total + measured scaffolding`), but a partition that charges it a real
share is deferred — a fixed deduction would zero out the tiny ceilings
the acceptance tests exercise.

**Layer order and contents** (§2.1 maps these to on-disk sources):

| Layer | Source | Render |
|---|---|---|
| E    | `directives/defaults.md`, `directives/user.md`, `directives/<active>/project.md`, plus `ProjectMeta.ConventionsPaths` | Section-divided concatenation; YAML frontmatter is stripped from each directive file. |
| A1   | `spine.jsonl` filtered to `project == active` | One §2.2.2 display line per record. |
| A2   | `projects/<id>/digest.json` for every other project (excluding `prj_default`) | One line per project: `<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>`, sorted by `last_active` desc. Each line capped at `cross-project.digest-per-project-bytes`. |
| B    | `threads/<id>.md` for each id in the runtime's `ActiveThreads` LRU | Up to `layer.b-top-k` threads, most-recently-engaged first; each rendered as a heading + frontmatter line + body. Per-thread share = `LayerB / k`; oversized threads are individually truncated. |
| C    | `spine.jsonl` records for each id in the runtime's `DormantThreads` LRU | One §2.2.2 display line per record. |

The layer percentages (`layer.budget.percentages` in §2.6.1) are calibration starting points, not constants: A1+A2+E percentages reflect the recognition-surface minimum required every turn; B=50% reflects the empirical finding that active thread bodies dominate prompt value; C=15% is the dormant-summary buffer that enables re-engagement recognition. The simulation sweep (§9.4) is how these numbers earn their values.

**LRU update rule** (driven by the §3.0.4 turn-close path):

After per-turn engagements commit, for each engaged thread id:
- Remove from both `ActiveThreads` and `DormantThreads` if present.
- Prepend to `ActiveThreads`.
- If `len(ActiveThreads) > layer.b-top-k`, demote the tail to the head
  of `DormantThreads`.

`DormantThreads` is **not count-capped** (#127): Layer C's byte budget truncates the rendered dormant set at composition time, which is the real bound. The v0.1 count cap (default 20) was dropped — at the token-denominated budget (`context.token-budget` in §2.6.1) the byte share is large enough that a 20-thread count cap would leave it mostly empty (dead budget), and dropping the cap removes the silent-drop-oldest behavior the count cap caused. The byte-truncation marker on Layer C is the visible, honest overflow indicator.

**Truncation policy:** each layer's rendered string is truncated to its
byte budget at a UTF-8 rune boundary, with a marker
`... [Layer X truncated; budget=N bytes] ...` appended. A failure to
render one layer (e.g. a project's `digest.json` is missing) emits a
`workset.warning` log line and that layer renders empty; neighbour
layers proceed.

**§5.5 mid-turn fetch:** when the model's topic tag at response start
references a `thr_<n>` not currently in `ActiveThreads`, the runtime
aborts the in-flight stream, loads the missing thread, fires a
`thread.fetched` context delta (per §3.0.1), promotes the thread into
`ActiveThreads` (front, BTopK-capped — overflow demotes the tail to
`DormantThreads`), and re-issues the request with the recomposed
system prompt. The user-visible response is the second stream's body.
The re-prompt is capped at 1 per turn. A fetched thread that belongs to
another project is declined at the fetch seam
(`thread.fetch-cross-project`) — the same §3.2 cross-project policy the
turn-close engagement resolver applies — and is neither promoted nor
re-prompted for.

The close-time LRU update still picks up any thread that the mid-turn
fetch couldn't service (an unloadable thread file logs
`thread.fetch-miss` and is skipped) or didn't trigger (e.g. the
second-stream tag introduces yet another missing `thr_<n>`; with the
cap reached, that thread enters `ActiveThreads` at close, not via
§5.5).

### 3.2 Topic tagging and engagement update

Topic-tag parsing is detailed in §5.1.2; the engagement-update model is
the §3.0.2 step-2 hook fired with the per-turn coalesced symbol set
(§3.0.4).

**Turn-single-owner invariant.** A thread belongs to no parent, but a
**turn belongs to exactly one thread** — its *owner*. A turn may *engage*
several threads; only the owner receives the turn's excerpt. The
excerpt-bearing write fires for exactly one thread per turn; a second
excerpt-write for the same turn is a structural error
(`ErrTurnAlreadyOwned`). Ownership *assignment* is a runtime decision —
the topic tag (§5.1.1) is advisory input, not a directive.

**Owner vs engaged.** For each turn:
- the **owner** gets the turn excerpt, `turn_count++`, recency
  (`last_engaged` / `last_engaged_turn`), `history_symbols` merge, and
  state→active resurrection (§2.2.1). A new-thread owner is created with
  the excerpt, `turn_count = 1`, and its `description` (§2.3).
- an **engaged-but-not-owner** thread gets recency, `history_symbols`
  merge, and state→active resurrection — but **no excerpt** and **no
  `turn_count++`** (no turn was added to it).

The Layer B/C LRU (§3.1) and recall surfacing operate over all engaged
threads (owner + non-owner); only excerpt and `turn_count` ownership is
restricted. The §3.9 file-edit application binds to the owner.

**Phantom resolution and owner fallback (D2).** Every referenced
`thr_<n>` is resolved against the spine at turn close — the tag is
advisory input; the runtime owns the decision. An id that does not
resolve is a *phantom*: not in the spine (`thread.engaged-miss`) or
belonging to another project (`thread.engaged-cross-project` —
cross-project engagement is reserved for Phase 5; v0.1 declines). A
phantom is logged for forensics and dropped: it never enters the
engaged set, `ActiveThreads`, or the persisted working set; never owns
the turn or receives the §3.9 file-edit binding; and a declined
cross-project record is never mutated. When the tag's would-be owner
(the lowest-id existing referenced thread) is a phantom, ownership
falls back to the most recently engaged **valid** referenced thread —
highest `last_engaged_turn`, ties broken by lowest `thr_<n>` id for
determinism. When every referenced thread is a phantom: a tagged
`*new-topic*` thread owns the turn; with no `*new-topic*` either, a new
thread is created from the turn exactly as a pure `*new-topic*` tag
would be. In every case the turn's excerpt is never dropped. A turn with
**no tag at all** whose deltas require owner binding takes the same
never-drop stance via the §3.3 missing-tag recovery (re-prompt once →
owner-default) — but ranks its candidates by the persisted Layer-B
recency order, NOT this fallback's `last_engaged_turn` ordering (§3.3;
`last_engaged_turn` is session-scoped and inverts recency across a
relaunch). This fallback deliberately keeps `last_engaged_turn`: its
candidates are the turn's own tagged ids — all engaged this turn either
way, so ownership placement among model-named threads is a bounded
misbind — and Layer-B order cannot rank them (a tagged id may sit
outside Layer B, and a same-turn §5.5 fetch reorders the list by fetch
order, not engagement recency).

**Anchor projection at owner-turn close.** After the owner's
`history_symbols` merge, the runtime re-derives the thread's `anchors`
projection (§2.7.4) over the merged set, in the order **merge → project
→ evict** (the eviction predicate must read the freshly-latched
`ever_central` flags). The projection piggybacks the full-file spine
rewrite that already happens on engagement (§2.3) — no separate spine
pass. **Idempotent-write guard:** the spine line is treated as changed
(and `anchors_projected_at_turn` bumped to the owner-turn index) **only
when the projected anchor set actually differs** from the prior set.
This keeps git diffs clean under any per-turn commit cadence without the
staleness a TTL would carry. **Engaged-but-not-owner threads do not
re-project** (no excerpt, no headline recompute). Closure (§3.5) runs a
final authoritative projection from `history_symbols` (consistent with
the same canonical source the curator anchor selection ranks).

### 3.3 Symbol extraction (three passes)

Three passes per §3.0.2 step 1, ordered cheapest first:

1. **Deterministic** — regex over the delta (file paths, URLs, claim IDs, `#`-tags).
2. **Model-emitted** — anchors from the topic tag (§5.1) are folded in directly.
3. **Curator** — at retirement, the curator drafts the closure summary (§3.5). It does **not** select anchors: the anchor set is the deterministic re-derived projection of active `history_symbols` (§2.7.4), recomputed every owner turn with no LLM. (Pass 3 is named for the curator's *summary* role at the same lifecycle point, not an anchor-selection step.)

For task-class deltas (§3.10.1), extracted symbols enter the staging buffer (§3.10.2) instead of feeding the coalesce buffer directly; they reach `symbols.jsonl` only on citation-window promotion (§3.10.4).

**Missing-tag protocol recovery (D6 — violation → recovery, never abort).**
A model response with no valid topic tag is a protocol violation only in
the advisory sense; the runtime recovers, it does not refuse the turn
(the former `ErrFileEditWithoutTopicTag` abort is **retracted** — live
data showed a real model omits the tag intermittently, and aborting
discards a coherent turn). Two regimes, split by whether the turn's
deltas require owner binding (buffered §3.9 file edits):

- **No binding required** (plain conversational turn): the turn proceeds
  tag-less — no engagement update, no excerpt, no owner; `topic.tag-missing`
  is logged for calibration. This is the pre-D6 behavior, kept
  deliberately: nothing on such a turn needs an owner, so a re-prompt or
  a defaulted engagement would spend cost to recover advisory metadata.
- **Binding required**: two recovery stages.
  1. *Single re-prompt* (`topic.re-prompt cause=missing-tag`): the
     in-flight stream is aborted and the request re-issued with a terse
     system-side reminder appended (§5.1.3 `TopicTagReminder`), via the
     same §5.5 re-issue machinery. Capped at 1 per turn for this cause
     (combined bound: §5.5).
  2. *Owner-default* (`thread.tag-defaulted`): if the re-prompted
     response still lacks a tag, the turn binds to the thread that would
     own it absent any tag — the most recently engaged valid Layer-B
     thread, ranked by the **persisted `ActiveThreads` order** (index 0 =
     most recently engaged; the first resolving candidate wins). The list
     order is the honest recency signal: it survives the working-set
     save/reload across a relaunch, whereas `last_engaged_turn` stores
     the session-scoped turn counter (resets on session load) and ranked
     ANTI-recency after a restart — a stale prior-session thread's high
     count outranked the thread actively worked this session. (The §3.2
     D2 fallback-(i) ordering keeps `last_engaged_turn` for its own,
     tagged-ids-only scope — see §3.2.) When no Layer-B candidate
     resolves, a new thread is created from the turn
     (the D2 fallback-(ii) terminal). The excerpt and the §3.9 edits are
     never dropped, and all recovered content still flows through the
     §3.0.5 `onContextDelta` chain. The binding-integrity concern the old
     abort answered is answered instead by determinism + the forensic
     `thread.tag-defaulted` line (`thr=`, `cause=missing-tag`,
     `reprompted=yes|no`).

  *Classification note — `fs.read` (open calibration question).* The
  "binding required" predicate is "any buffered §3.9 file-edit event",
  and `fs.read` deltas buffer a write entry exactly like `fs.write`
  (the read content enters the tracked-file version chain — pre-existing
  §3.9 classification). A tag-less **read-only** turn therefore pays the
  full recovery cost (re-prompt → owner-default) even though it mutated
  nothing outside the sidecar. Whether a pure-`fs.read` turn should
  instead be conversational-class for this predicate is flagged as a
  calibration/classification question awaiting live data; no behavior
  change is specified here.

**Empty-response recovery (D6 extension — any turn).** A response with
**zero visible content** is a protocol failure regardless of whether
binding is required: there is no tag AND no body. Observed live (the
2026-07-15 elicitation probe, round 3, signature (c)): on work-switch
turns the model intermittently burns its whole completion budget on
hidden reasoning and returns `finish=stop` with empty content. Recovery:

1. *Single re-prompt* (`topic.re-prompt cause=empty-response`): the
   ended stream is discarded and the request re-issued with the terse
   `EmptyResponseReminder` (§5.1.3) appended — its own per-cause cap of
   1 under the combined bound (§5.5). Fires on ANY turn, conversational
   or binding.
2. *Accept the empty*: if the re-prompted response is also empty, the
   turn proceeds with it. On a **binding-required** turn it falls through
   to the owner-default above (an empty response has no tag, vacuously);
   the recorded excerpt's agent section is deliberately **empty** — the
   user prompt and the §3.9 edits are real and need an owner, and an
   empty agent section is the honest record of what the model produced
   (synthesizing placeholder content would forge the thread history).
   The `thread.tag-defaulted` line then carries `cause=empty-response`
   (with `reprompted=` reading the empty cause's flag). On a
   **conversational** turn the empty body surfaces to the caller
   unchanged. Either way the runtime emits a `system.empty-response`
   forensic line (`reprompted=`, `turn=`) — the REPL prints nothing for
   an empty body, so without it a blank turn would be invisible in the
   log.

   The empty cause **subsumes** the missing-tag cause: a persistent-empty
   response never additionally spends the missing-tag re-prompt — a tag
   reminder to a model that twice produced no content re-runs the
   intervention class that just failed, buying latency and nothing else.
   Reasoning-burn empties are additionally tracked as a serving-level
   hazard (max-token budget vs. hidden-reasoning interplay), not only a
   prompt-protocol one.

### 3.4 Recall matching

> **Recall-completeness invariant (load-bearing — no dead zones).**
> All **non-transient** turn content MUST be locatable by recall at every stage
> of its lifecycle — from creation, through assembly-window residency,
> scroll-out and durable storage, archival (§3.8), and any later
> summarization/retirement (§3.5). **The ONLY content that may be unfindable by
> contract is transient data that has been deliberately discarded** per the
> §3.10 transient-data lifecycle (uncited task-class symbols / raw task-bytes
> evicted at window close, §3.10.5–§3.10.6) — that data was never promised to
> persist, so its absence is the contract. Every other classification is durable
> and must remain locatable.
>
> "Locatable" is **mechanism-agnostic**: findable by *some* recall path —
> symbolic, embedding coarse/fine tier, summary tree, or the exact lexical /
> flat-scan tier (#117). The consumer neither knows nor cares which; the recall
> mechanism is an implementation detail, completeness is the contract.
>
> **There must be no window in which durable content is hidden.** A piece of
> content that is retained on disk but momentarily absent from one index — e.g.
> scrolled out of the assembly window but still pending its *asynchronous*
> fine-tier embedding flush — is NOT permitted to be unfindable in the interim.
> Completeness holds **continuously, not eventually**: the recall path must cover
> such content by another mechanism (e.g. an exact pass over the bounded
> not-yet-flushed set, which is on disk) so there is no lag-window dead zone.
>
> **Enforcement.** A measurement harness must assert this completeness directly
> and may **NOT** model any "by-design blind spot" or acceptable-lag in which
> durable content is unfindable — such a window is a correctness defect to fix in
> the runtime, never a behavior to certify. (This invariant was previously
> unstated; its absence let the embedding-flush debt window become an unflagged
> recall dead zone the sim oracle accommodated instead of catching.)
>
> **Interim claim caveat (B1).** The default acceptance gate (`make test` / `make
> sim`) runs with a **nil embedder**, so the §3.4 fine tier and its bounded
> lexical completeness floor (#123) **do not execute** there — the floor's effect
> is unobserved on the mock gate. Until the **embedder-enabled completeness rung**
> (`make sim-completeness-rung`: live embedder + mock inference over several
> sim-days, asserting that every flush-lag dead-zone probe is surfaced and at
> least one was observed) is wired and **green**, the recall-completeness claim
> above is proven only for the **symbolic** path; the embedding-flush-lag half is
> **symbolic-only-validated until that rung is green**. The rung is the direct
> proof of this invariant's continuous-completeness clause.

Three layers, run as **parallel signals**, not a strict cost cascade:

1. **Symbolic Jaccard** over the symbol index — high precision, low
   recall. Cheap.
2. **Embedding cosine** over an in-memory, **hierarchical** embedding
   index — the primary recall scan, drift-robust. A coarse tier (one
   vector per thread body) is the first pass (which threads); a fine
   tier (per-turn-excerpt chunk vectors) is the second pass (which
   content within a surfaced thread). Coarse→fine keeps matching cheap
   and bounded as both thread count and per-thread length grow; the
   per-turn cost is one embedding call to vectorize the query. Index
   granularity / maintenance / persistence is detailed below.
3. **Model judgment** on surfaced candidates — expensive confirmation,
   used to cut false positives before a recall is offered to the user.

> **Why parallel, not a cascade.** An earlier draft of this spec ran
> Jaccard as a *pre-filter* and embedding only on its candidates. The
> Phase C.6 calibration disproved the assumption that made that
> ordering safe: symbolic Jaccard recall collapses to ~10% under
> realistic vocabulary drift. A Jaccard pre-filter would discard ~90%
> of genuine matches before embedding ever saw them. Embedding
> similarity must therefore be the primary scan; symbolic Jaccard is a
> parallel high-precision signal, not a gate.

Embedding recall (layer 2) is **opt-in**: enabled when `config.toml`
pins an `[embedding]` provider/model (§8.2.2). With no embedding model
configured, recall runs symbolic-only — graceful degradation, never a
hard failure.

**Embedding index — granularity, maintenance, persistence (build target;
unifies #102 thread-level freshness and #109 intra-thread recall).** The
index is chunk-level and hierarchical (above), and **one uniform
mechanism serves all "not-immediately-in-context" recall — there is no
separate intra-thread path.** The organizing distinction is **live FIFO
window vs. indexed body**, not active vs. inactive thread: every thread
is an *indexed body* (coarse thread vector + per-excerpt chunk vectors);
an *active* thread additionally carries a live FIFO window (§2.3,
`ThreadTurnWindow`) that is already in the working set and so is
deliberately *not* embedded. As turns scroll out of the FIFO into the
stored body they become index material. Recall runs uniformly over all
indexed bodies; whether the **engaged thread's own** indexed body is a
candidate is a filter choice — include it → intra-thread recall of its
scrolled-out early content; the live FIFO window is never a candidate
because it is in-context. This is how §3.4 answers "what did we settle
on earlier *in this thread*" for a long-running thread: the early content
is in the index like everything else.

*Maintenance.* Incremental and **asynchronous** — a single background
indexer embeds scrolled-out chunks off the turn's critical path, then
swaps an `atomic`-pointer index so recall reads never block; a
**per-thread turncount watermark** guards ordering, so a stale in-flight
embed never overwrites a fresher vector when a thread re-activates and
re-decays mid-job. Two flush triggers feed the one index: an
**embedding-debt cap** (flush after N turns of accumulated scrolled-out
content — N a §9 calibration window; the scrolled-out-but-not-yet-flushed
content this lags is NOT a recall blind spot — the automatic intra path
covers it with the bounded lexical completeness floor above, #123) and the
**dormancy transition** (flush a thread's remaining debt when it decays
out of the working set, so its full body is indexed before it becomes a
pure recall target). Mid-session threads and mid-thread content thus
become recallable without a full rebuild.

*Persistence (required, not deferred).* The vectors are a **derived,
persisted cache** keyed by a content-staleness marker (body/chunk hash):
gitignored and regenerable (never canonical — rebuild-on-miss), written
through by the indexer, and loaded at startup so a session re-embeds only
what changed since last run — **O(changed), not O(all)**. A multi-year
instance accrues thousands of threads × turns; re-embedding everything
every startup is untenable, so the cache is a requirement of the design,
not a later increment. (Shares the staleness-keyed rebuild-on-open logic;
cf. #44.)

*Non-negotiable + simulation.* Intra-thread recall on long-running
threads is load-bearing — through usage or topic there will be very long
single threads (a multi-year project managed on one thread; a main
default thread where work is discussed and synthesized before kickoff).
It must work reliably and efficiently with **no performance decay**, and
must be **validated in the §9 acceptance simulation** with a
long-running-thread workload and explicit decay measurement (index size,
per-query cost, retrieval latency) as both thread count *and* per-thread
length grow into the thousands.

*Implementation — shipped.* Tasks #102 and #109 were designed and built
as one: chunk unit = turn-excerpt (`internal/recall/scoring/chunk.go`);
coarse→fine Kc/Kf cutoffs implemented; debt-window N=16 (embedding-debt
cap) implemented — and, per the recall-completeness invariant above, the
automatic intra-thread path now closes the former debt-window dead zone
(#123): after the fine-tier scan it adds a **bounded lexical pass** over
exactly the engaged thread's debt-window excerpts (the recent scrolled-out
tail awaiting its async flush, loaded via
`MemoryOps.LoadDebtWindowExcerpts`, matched against the query symbols by
`recall/exact.MatchExcerptsBySymbols`) and unions the hits into the
intra-thread result. This is the on-disk completeness floor: durable content
stays findable continuously through the async-flush lag, with no per-query
embedding and no unbounded scan. The bound is the debt **cap** (not the live
debt depth — a cap-flush resets the live counter while its embed is still in
flight) **widened by the recall service's per-thread count of enqueued-but-
unpublished flush jobs**: the scan covers `(1 + pending) × cap` excerpts,
which spans the whole unflushed tail because at most cap excerpts accrue
between consecutive flush triggers and everything older was published by the
last completed flush (BD-8; the cap alone left the in-flight batch's oldest
excerpts transiently unfindable once scroll-outs continued past the flush).
`pending` is 0 in the steady state — the scan is exactly one cap — and under
backlog is bounded by the indexer's queue depth plus one job in process plus
one blocked enqueue (the sender counts itself before the send): 258 at the
v0.1 queue depth of 256. The guarantee thus holds continuously across
in-flight *and queued* cap-flush windows while the scan stays fixed-bound.
A flush arriving at an *equal* dispatch watermark (the same-turn cap-flush +
dormancy-flush double-enqueue) always publishes: the single-FIFO indexer
loaded it later, so it is fresher — ties are never dropped (BD-8), leaving a
genuinely *failed* flush as the only path that retires a pending job without
publishing. (Residual, accepted: a **single transient** embed/load failure
opens a gap for that job's batch lasting until the next **successful** flush
for the thread republishes the retained set — for an engaged-but-idle thread
that can span the rest of the session (the next cap-flush needs more
scroll-out), healed by the dormancy-demotion flush or the next session's
startup reconcile. The floor guarantees against async-flush *lag*, not
against failed flushes.) Persisted
cache = `.vec` + `.tree` sidecars under `.recall-cache/`
(`internal/recall/measure/veccache.go`, `treecache.go`); shadow-chunk
oracle implemented in the sim. On top of this, the #111 within-thread
summary hierarchy shipped: an O(log n) beam descent over a per-thread
summary tree (v0.1 node keys = deterministic exemplar sets per
`ExemplarSummarizer`; an LLM-summary key is the deferred F-A enrichment
at the same seam) built offline by the sleep cycle, replacing the O(n)
flat fine-pass on the engaged thread. The W1 recall-preservation signal
(`recall_intra_descent_divergence`) is a reported **quality measure** — an
approximation-drift canary — **not a build-blocking gate** (#119): the
summary tree is an approximate O(log n) heuristic, so a nonzero divergence
means it substituted a within-top-Kf leaf, not necessarily that recall was
lost. Exact/exhaustive recall is the separate exact tier (#117 grep +
flat-scan), where it is guaranteed; it is not the approximate tree's
contract. (It was green on a live 14-day run; a later 1-month rung showed a
within-Kf substitution at 1.000 embedding recall, which motivated the
gate→measure reframe.) Algorithm detail and acceptance criteria are in
`design/within-thread-summary-hierarchy.md` and
`design/intra-thread-recall-design.md`.

**Recall surface.** At turn close the merged candidates are logged
per-layer (`spine.match-fire` / `spine.embed-match-fire`) and, when an
experience-layer resolver is installed, the top 3 are surfaced as an
*offer*. C.6 measured top-1 recall ~73% vs. top-3 ~92%; surfacing
three and letting the user pick beats forcing a single-candidate
guess. The resolver is the seam between the recall stack and the
experience layer — the chat REPL resolves it interactively, the
scenario harness from a scripted decision — so recall internals stay
insulated from caller code. Accepted candidates are promoted into
Layer B; every offered candidate is logged `recall.accept` or
`recall.decline` (the latter with a §4.3 reason). With no resolver
installed, recall stays log-only.

**Lifecycle-aware symbolic scorer.** The match target is unchanged —
`T(thr) = anchors ∪ {h.normalized}` — so a `superseded` symbol, which
stays in `history_symbols` (§2.7.4), remains matchable with no change to
the match-set construction (abandoned-premise recall works for free).
The Jaccard *numerator* is a **weighted sum** rather than a plain count:
each matched symbol contributes `1.0` if it is `active` in that thread,
`recall.superseded-weight` (default **1.0**) if it is `superseded`. A
symbol that is also a projected anchor always counts as active (the
headline takes precedence over a stale history entry). The
superseded **label is canonical** — sourced from the thread's
frontmatter `lifecycle` (§2.3), not the derived index. The denominator
(union size) is unchanged in cardinality; at weight 1.0 the score is
byte-identical to the plain-count scorer. `recall.superseded-weight` is
a §9 calibration window — lowered below 1.0 only if the sim's
`superseded_precision`/`abandoned_premise_recall` show abandoned threads
crowding out focused ones. Separately, `symbols.jsonl.superseded_in`
(§2.4) is the cheap-scan candidate-filter surface — answering "which
threads abandoned premise X" in O(1) per query symbol without loading
every thread's frontmatter; the candidate-filter rework that consumes it
is a tracked follow-on, not part of the scorer.

### 3.5 Closure flow (AMENDED)

Trigger detection (engagement decay or `/done`) → curator-drafted summary
→ resolution → frontmatter + spine update → Layer B/C eviction. Deep cold
archival (§3.8) is the next stage past closure when spine cardinality
pressure builds.

Engagement decay fires when `last_engaged` exceeds `engagement.decay-turns` OR `engagement.decay-time` (§2.6.1). OR semantics is deliberate: `decay-time` catches extended user absence (no turns at all); `decay-turns` catches low-frequency engagement in a busy session. AND semantics would let threads outlive their usefulness in either pattern.

The curator-drafted summary targets 100–150 chars. This balances two constraints: summary + anchors must fit within the Layer A1 budget at realistic spine cardinality (~400 threads × ~200 chars ≈ 80 KB, within the 8% A1 share at 64 KB context); the gist must also be sufficient for the model to recognize prior engagement without fetching the thread body.

**Original specification (superseded).** Every decayed thread prompts the
user interactively, in-session, at the turn close that detects the decay.

**As built (amendment, user ruling 2026-08-04): auto-accept routine
closures, batch the exceptions at boundaries.** Living-with found the
per-thread interactive ack an unreasonable burden. At the default
`engagement.decay-turns: 8` every side topic decays mid-session, so the
`[r/d/a/w/e/s]` resolver interrupts several times an hour — which is
exactly the rubber-stamp failure the ack-quality paragraph below
predicted, arriving through frequency rather than through inattention.

At the decay-detection scan each candidate is classified:

- **EXCEPTION** — the curator draft is empty or failed, OR the thread is
  anchor-rich (≥ 6 anchors), OR long-engaged (≥ 15 engaged turns,
  `SpineRecord.turn_count`). These are the closures whose summary is worth
  a human's eyes. The v1 criteria are deliberately simple (two integers
  read straight off the spine record, no new persistent field, no scoring
  model) and are a calibration window pending living-with data.
- **ROUTINE** — everything else.

A **routine** closure is applied without asking: the curator's summary,
`resolution=resolved`, the same frontmatter + spine write and Layer B/C
eviction an accepted ack performs, and ONE committed line to the terminal
(`closed: <thread> — <summary>`). It rides the turn transaction already
open around turn close (§4.5.8) — auto-accept adds no commit path of its
own. `resolved` is the auto-accept resolution because it is where a
decayed-and-answered side topic lands when a human picks. **The revision
path for a wrongly-summarized routine closure is `/back-to`**: re-engaging
the thread returns it to `active`, and its next close re-drafts the
summary. v1 adds no new command for this.

An **exception** closure is **queued**, silently: no curator call, no
prompt, one `retire.pending` event. The queue is **derived, never stored**
— a pending closure is just a decay-eligible thread that classified as an
exception and so was not auto-accepted, which makes it crash-safe by
construction (no queue file to tear or reconcile) and correct across
restarts. It is drained with the full interactive resolver at
**boundaries only**: at clean session exit (before the session-close
checkpoint), and on demand via `/closures` (§4.2). At session **start** a
non-empty queue is ANNOUNCED in one committed line
(`N closure(s) pending review — /closures`) and never prompted.

Two flows are unchanged by the amendment. **`/done` is always fully
interactive** in both modes — the user typed the command, so they have
opted into the conversation. And a draft that **fails or comes back empty
is never auto-accepted**: it logs `retire.curator-error` and is retried at
the next scan, because a blank gist is precisely the case a human has to
see. (Consequence, stated honestly: a persistently failing curator leaves
its thread un-closed rather than queued — the derived queue is keyed on
the two spine-readable signals, and a draft failure is not visible to it.)

**Escape hatch.** `closure.ack-mode: auto | always` (§2.6.1), default
`auto` — the behavior above. `always` restores the superseded per-decay
interactive flow verbatim; nothing queues under it, because every decayed
thread is offered in-session.

**Ack-quality instrumentation (front-end v0.1 requirement, spec'd
2026-07-13; instrumentation landed 2026-07-13, rate-evaluation still
front-end-phase; AMENDED 2026-08-04).** The closure ack is a load-bearing
integrity gate only while the human actually reads the draft; a
rubber-stamped ack is worse than none, because it launders an unread
summary as human-verified. The canary is **ack-edit-rate**: the REPL ack
UI offers an `[e]dit` choice (§4.2 `/done`, the `/closures` drain), and the
`retire.ack` event detail (§2.8) records `edited=yes|no` for EVERY acked
outcome (retire and wip) — `yes` only when the user submits a summary that
differs from the curator draft, so a rubber-stamp resubmission stays
`edited=no`.

**The canary now reads HUMAN acks only.** `retire.ack` additionally
carries `ack=human|auto`, and the rate is computed over `ack=human` lines.
This is not bookkeeping tidiness: an auto-accept is unedited by
construction, so folding the two populations together would drive the
measured rate toward zero as auto-accepts came to dominate, and the canary
would read "the user stopped reading" about closures nobody was shown —
retiring itself silently at exactly the moment it mattered. Auto-accepts
are still fully recorded (`retire.ack ... ack=auto` plus
`retire.complete`); they are simply not part of this denominator. What
remains a front-end-phase deliverable is the *evaluation* of the rate — a
user who edits 0% of the drafts they ARE shown is either being served
perfection or has stopped reading, and the distinction must be probed, not
assumed. That evaluation needs a real human, so it is a §9.1 category-3
known-unknown until then. The auto/exception SPLIT is itself now part of
what that evaluation must judge: if the exception criteria are miscalibrated,
the symptom is a human ack-edit-rate that stays at zero while auto-accepts
are being corrected by hand with `/back-to`.

### 3.6 Fallback dissection

Budget-pressure trigger → dissector LLM clusters the oldest content →
batch retirement → ack flow.

### 3.7 Cross-project digest maintenance

`projects/<id>/digest.json` regenerates whenever any thread in the
project is updated. Each project's digest is capped at
`cross-project.digest-per-project-bytes`; recent anchors are selected
from the N most-recently-engaged threads. Eager regeneration ensures Layer A2 always reflects the current cross-project recognition surface; `most-recently-engaged` selection is the heuristic that engagement-recency is a proxy for ongoing relevance, maximizing the probability that a cross-project match is actionable.

### 3.8 Deep cold archival

Per §3.5, threads can be archived "off-spine" when spine cardinality
pressure builds, with recovery via explicit fetch. The mechanism leverages
the autonomic git layer (§8.3) rather than a bespoke archive format.

**Barrier-homed (behavioral change, #94 R3b).** The archival *drain*
fires **only at the day barrier** (§4.5.8 B1), against the **primary**
git DB, so primary stays barrier-exclusive (§4.5.8 INV-5). This
supersedes the R3-shipped mid-turn/between-turns firing. The pressure
computation is unchanged; only the drain's timing and target repo move.
The public `RecoverThread`/`ArchiveThreads` port ops still exist (and
standalone `ArchiveThreads` stays crash-recoverable via the same
roll-forward completion, §4.5.8 §6), but under normal operation the
barrier owns the drain.

#### 3.8.1 Mechanism

A thread is a **directory** (`threads/thr_<n>/` = `thread.md` + `turns/` +
`files.json`, §2.3), not a single file. Cardinality-pressure archival
drains a **batch** of the coldest retired threads at once (§3.8 trigger);
the mechanism below is per-batch, and all git goes through the
`internal/autogit` (go-git) seam — never shell git, never a pre-commit
hook (§8.3).

To archive a batch of threads:

1. The runtime confirms each `threads/thr_<n>/` is tracked at HEAD and
   captures the directory's git **tree hash** (the value recovery will
   verify against).
2. A **capture commit** `Add(".")`s the full current worktree into
   **primary**, pinning the to-be-archived directories into a committed
   tree — this commit becomes the *parent* where the thread bytes remain
   reachable after removal, and it also lands the day's cumulative
   content in primary (what makes the barrier's daily-DB nuke safe,
   §4.5.8 INV-3). A capture commit here guarantees the bytes are in a
   committed tree before deletion regardless of the per-turn daily
   cadence (§3.11).
3. **Record batch membership durably BEFORE any deletion** (F1,
   membership-before-deletion): append one `archive/index.jsonl` entry
   per thread with **empty `commit_hash`** but full
   `parent_commit_hash`/`tree_hash`/`original_path`/spine snapshot — all
   available immediately after the capture commit. This entry set is the
   deterministic worklist a crash-completion consumes (§4.5.8 §6), so no
   half-applied deletion is ever ambiguous.
4. Stage the recursive removal of each `threads/thr_<n>/` directory
   (`os.RemoveAll` + `autogit.Add(".")`, which stages tracked-file
   deletions), remove all spine records in **one** filtered rewrite
   (`RemoveSpineRecords`), regenerate derived state (`symbols.jsonl`,
   digests) **once** for the batch, and commit this **deletion commit**
   as a unit (gated by `CheckSpineIntegrity` on the pre-state and
   `CheckDerivedFresh` on the post-state); capture its hash `H`.
5. Because `H` is only known after step 4, a small **stamp commit**
   records the now-known `commit_hash` into the membership entries from
   step 3. The completed archive index entry:
   ```jsonl
   {"thr_id":"thr_42","commit_hash":"<H>","parent_commit_hash":"<capture commit; H's parent>","tree_hash":"<tree sha of threads/thr_42/ at the capture commit>","archived_at":"<RFC3339>","original_path":"threads/thr_42/","spine_summary":"<summary at archival time>","anchors":["..."],"project":"prj_3","recovered_at":""}
   ```
   `spine_summary` and `anchors` are preserved verbatim from the spine
   record at archival time so a search over archive entries can match
   without recovering the full thread. `parent_commit_hash` is the capture
   commit whose tree still holds the thread bytes; recovery restores the
   subtree from it directly rather than walking `commit_hash`'s first
   parent (an older entry written before this field existed omits it —
   `omitempty` — and recovery falls back to the walk-parent path). It is
   stored explicitly because a future merge commit (v2.0 submind-via-clone)
   has multiple parents, where a first-parent walk would pick the wrong
   lineage. `recovered_at` is empty until the thread is recovered (§3.8.3).

The drain thus produces a **bounded** number of commits per batch (not
one per thread), and the spine rewrite + derived regen happen **once**.
The archive index is **canonical** (per §2.1 ownership table); sorted by
`thr_id` for line-grain git diffs. Losing the index never loses data: the
thread bytes are reachable from the capture commit's tree regardless
(`git log`).

#### 3.8.2 Storage properties

- Git's object store is content-addressed and zlib-deflated (loose
  objects) or delta-compressed (packfiles). Multiple historical
  versions of the same thread file are stored compactly via git's
  delta-compression — better than independent `tar.gz` archives, which
  would not compress across entries.
- `git gc` is safe to run autonomically: archived blobs remain
  reachable via the deletion commit's parent commit. Personant never
  rewrites history; deep-cold blobs depend on this invariant for
  reachability.
- Periodic `git gc --aggressive` can be invoked on a schedule when
  pack-file size starts mattering.

#### 3.8.3 Recovery

To recover archived thread `thr_<n>` (via the `RecoverThread` port op):

1. Look up the archive index entry by `thr_id`
   (`ErrArchiveEntryNotFound` if absent).
2. Resolve the deletion commit's **parent** (the deletion commit's own
   tree no longer contains the directory; the bytes live in its parent)
   and restore the full `threads/thr_<n>/` **subtree** from it —
   `autogit.CheckoutTree` walks the directory tree and writes every blob,
   not a single `git show`.
3. Recompute the recovered directory's git **tree hash** and verify it
   against the stored `tree_hash`. Mismatch ⇒ delete the partial restore
   and abort with `ErrArchiveIntegrity` — a partially-recovered thread is
   never written onto the spine.
4. Append a fresh spine record reusing the **same** `thr_<n>` id (ids are
   stable forever — recovery never mints a new one); `state = wip`,
   `last_engaged` updated. Anchors/summary come from the **recovered
   frontmatter** (canonical), not the index snapshot (which may be stale).
5. Stamp `recovered_at` on the index entry and commit the recovery
   (worktree restore + spine + index) to the home git tree.

The archive index entry is **kept** as a forensic breadcrumb (the
`recovered_at` stamp marks it historical, not a current archived claim).
`personant archive list` / `personant archive recover <thr_id>` surface
deep-cold history.

**Two recovery surfaces, deliberately different strength.** Archived
*threads* recover via `RecoverThread` from Personant's **own** home git —
which Personant owns and never rewrites — so it is a *strong, unconditional*
guarantee. Aged-out *file versions* recover via `GetFileVersion` (§3.9.1)
from the **workspace** git — which the **user** owns and may amend/rebase/gc
— so it is a *conditional* guarantee, gated by the `BlobReachable` check
(the blob at `hash:path`, BD-11) that also refuses to age an unreachable
hash. The asymmetry is intentional:
the strength of each recovery surface follows the ownership of the git repo
it reads from.

#### 3.8.4 Recall treatment of archived threads (v0.1: off-recall)

Archived-recoverable threads are **off the live recall surface** in v0.1.
The §3.4 recall scan operates over the spine + per-thread frontmatter;
an archived thread is off-spine with no frontmatter loaded, so it is not
a recall candidate. The `archive/index.jsonl` anchors/summary are a
forensic + `personant archive list/recover` surface, **not** a live
recall match surface — deep cold is recoverable by **explicit fetch**,
not ambient surfacing. (Re-surfacing the coldest threads ambiently would
re-introduce the cardinality pressure archival exists to relieve.) A
future "archive recall" — matching the index anchors and offering a
recovery-fetch — is a clean v0.2 addendum the index schema already
supports (it carries `anchors` + `spine_summary`); it is out of scope
for v0.1.

**Measurement consequence (§9).** Because archived threads are off the
live recall surface, the §9 recall-fidelity metrics exclude an
archived-recoverable expected match from a *live*-recall score rather
than counting it a miss (`recall_archived_recoverable`) — the runtime's
§3.4 algorithm is correctly not surfacing an off-spine thread, not
failing. This is distinct from `recall_unexplained_absence` (an expected
thread that is off-spine **and** absent from the archive index — a
genuine integrity signal that must stay 0). With recoverable archival,
the only legitimate reason a thread leaves the spine is archival, so the
distinction is meaningful: a recoverable thread is preserved and
fetch-recoverable, not lost.

### 3.9 Working-set content dedup

When file content (or other large content blobs) appears in the working
context multiple times — a file is read, then modified, then re-read —
the runtime de-redundifies content to keep the working window's
information density high.

The key observation: **persistent storage and live context composition
are separate concerns** sharing the same primitives — content-addressed
identifiers, diffs, anchor literals.

#### 3.9.1 Persistent storage (thread file body, log)

The full chain is preserved, **anchored on the current state**:

- The current content is stored as a **literal** — fully formed and
  hot, since it is what active cognition works on.
- Each prior version is stored as a **reverse-delta**: the unified diff
  that transforms the newer state back into it. History reconstructs
  *backward* from the live literal; old states stay recoverable and
  their differences are explicitly enumerated. (When a new version
  arrives, the outgoing literal is re-encoded as a reverse-delta and
  the new content becomes the literal.)
- Periodic anchor literals every K-th version back through history
  (default K = 10) — a full literal that bounds reverse-delta chain
  length and prevents cumulative drift. Same trick I-frames in video
  compression use. Configurable via the directive `dedup.anchor-cadence`.
- A reverse-delta that is ≥ `dedup.diff-literal-threshold` (default
  0.7) of the literal size is stored as a literal instead — the delta
  isn't earning its keep. Same heuristic as lzma's literal-vs-match
  decision.

This keeps the current state fully formed and hot while giving
forensic-quality history at bounded storage cost.

**Git-minimization bound.** Once a tracked file is committed to the
project's git repo, its committed state is recoverable from that repo by
commit hash. Personant's reverse-delta chain of the *pre-commit* edit
history is then duplicative of what git holds, **for as long as the commit
stays reachable in the workspace repo**. After a retention window
(`FileChainRetentionTurns` turns OR `FileChainRetentionDays` days, whichever
trips first), the chain is aged out — **but only after the runtime confirms
the committed blob is reachable in the workspace repo** (a read-only
reachability check, §6.1.3, via the `BlobReachable` predicate — the blob at
`hash:path`, the same lookup `GetFileVersion` recovery uses, BD-11). If the
blob is not reachable — the user amended, rebased, gc'd, or moved the
workspace — **aging is refused and the chain is retained**, and the refusal
is logged (`dedup/chain-age-refused`, carrying thread, path, hash, reason).
Recovery of an aged-out committed state goes through the `GetFileVersion`
recovery op, which reads the blob read-only from the workspace tree (chain
fast path first: a still-retained chain recovers with no git access).
Uncommitted edit history lives only in the retained chain and is unaffected
by minimization.

**External-git dependency (honest statement).** The workspace repo is the
**user's**, not Personant's (§6.1.3 — Personant never mutates workspace git
and does not monitor its history). The recoverability of an aged-out
committed state therefore depends on the user not having orphaned the
commit. Personant's contract is narrower and keepable: it will not *drop* a
chain while the commit is unreachable, so no content is lost *by aging* — at
worst a chain is retained longer than the window. If `git` is unavailable at
all there is no second copy to minimize against, so every chain is retained
and the condition is logged once. The same reachability predicate gates both
surfaces: recovery succeeds exactly when aging would have been permitted to
drop.

**Symbol lifecycle for file-edit events.** `fs.read`, `fs.write`, and
`fs.commit` deltas are task-class events (§3.10.1). Symbols extracted
from them land in the staging buffer rather than the symbol index
directly; they enter `symbols.jsonl` only if a subsequent decision delta
cites them within the citation window K. `stagingWindowTurns` default 3
reflects the empirical claim that a task-class symbol cited by a decision
delta within 3 turns is likely signal; beyond that window, uncited
symbols are noise. The window is a calibration target (§3.10.3).

#### 3.9.2 Live context composition (working window)

The working window's representation is more aggressive:

- **Literal current state** at the most-recent position (where active
  cognition is happening).
- **N most-recent diffs** in literal form (default N = 3, configurable
  via `dedup.live-diff-window`) for forensic queries that look back a
  few steps.
- **Older states** replaced with content-addressed identifiers:
  `[content #<hash> — see most-recent position]`. The identifier
  preserves causal/temporal ordering without the storage cost of
  redundant copies.

When N is exceeded, the oldest diff in window is collapsed to its
identifier; the displaced literal content is unrecoverable from window
alone but still recoverable from persistent storage.

#### 3.9.3 Diff format

**Default: unified diff.** The format LLMs encounter most often (git,
patch, code review) — parsing burden is familiar. Start here.

**Fallback: natural-language descriptive diff** ("added a line `foo`
after line 12; removed lines 20–22"). When unified diff produces output
that the model has trouble applying — empirically: complex
multi-region changes, very long files — the runtime can switch
formats per-diff. The directive `dedup.diff-format` controls the
global default; per-thread override is possible.

#### 3.9.4 Identifier-only references

Pure identifier references (no nearby literal) are appropriate when:

- Cognition is about *meta* (this file has been mentioned, that file
  was modified) rather than content.
- The content has been displaced from the live window and forensic
  recovery is the user's path.

**For active reasoning over content, identifier-only is insufficient.**
The LLM must have the literal in attention range to reason about it
directly; resolving an identifier to remote content costs attention
bandwidth and exhibits "lost-in-the-middle" effects when the original
is far from where it's needed. The runtime tracks active engagement
via the engaged thread's tool-call history: a file recently the
subject of `fs.read` or `fs.propose_promote` keeps its literal in the
active layers (B / C), regardless of how many references precede it.

### 3.10 Transient-data lifecycle

Task-class context (tool outputs, shell captures, file reads) has value
for one span of turns and then drops to zero. Treating it the same as
decision-class context (user prompts, model responses) would pollute the
symbol index with transient noise, bloat persistent storage with
discardable bytes, and degrade recall precision in §3.4. This section
defines the two-stage mechanism that keeps task-class content discardable
while preserving the symbols it produces when — and only when — a
decision delta cites them.

#### 3.10.1 Provisional classification at delta time

Each delta entering the §3.0.2 chain is assigned a **retention class**
before any hook fires. The class derives mechanically from the event
source (§3.0.1); no content inspection is required.

| Source | Provisional class |
|---|---|
| `tool.result`, `user.shell-capture`, `fs.read`, `fs.write`, `fs.commit` | `task` (provisional-transient) |
| `user.prompt`, `model.response`, `thread.fetched` | `decision` (provisional-persistent) |
| unknown source | `decision` (safe default — unknown content is never silently dropped) |

The classification is **provisional**: it governs where extracted symbols
land (§3.10.2), not whether content ultimately persists — that is
confirmed or discarded by the citation window (§3.10.3–§3.10.5).

One user-controlled override: a `task`-source delta prefixed with the
reference-material marker (`##` / `/keep`) is reclassified to `decision`
at emit time. This is the path for explicitly retaining task-class
content (e.g. a paper ingested for ongoing reference). The agent has no
parallel override; agent-produced retention flows through decision-delta
authorship as described in §3.10.4.

#### 3.10.2 Staging buffer

Symbol extraction (§3.3) runs on every delta regardless of retention
class. Where extracted symbols land depends on the delta's class:

- **Decision-class delta:** symbols enter the per-turn coalesce buffer
  immediately. At turn close they are committed to the thread's
  `history_symbols` and to `symbols.jsonl`.
- **Task-class delta:** symbols are placed in the **staging buffer** —
  a per-`State` map keyed by normalized symbol form (§2.7.2). They do
  not enter `history_symbols` or `symbols.jsonl` at this point.

The staging buffer persists **across turns**, unlike the coalesce buffer
which resets at each turn boundary. Each entry holds:

| Field | Description |
|---|---|
| `Normalized` | Canonical normalized form (§2.7.2); also the map key. |
| `Raw` | Original surface form; preserved for user-facing display on promotion. |
| `Source` | Symbol provenance (§2.7.3). |
| `StagedAt` | `TurnNumber` when the entry was added. |

When the same normalized form arrives from a later task-class delta,
the existing entry is overwritten and `StagedAt` refreshed to the newer
turn number — a recurring observation extends the citation window rather
than accumulating duplicate entries.

#### 3.10.3 Citation window K

A staged symbol is eligible for promotion during the K turns following
the turn it was staged: turns `StagedAt` through `StagedAt + K - 1`
inclusive.

**K = 3** (constant `stagingWindowTurns`). Rationale: a task-class
symbol cited by a decision delta almost always appears within the very
next model response (the model interprets the tool result immediately).
K = 3 provides a generous window that covers same-turn citation,
next-turn citation, and one additional turn for cases where a second
tool call or clarification prompt intervenes. Task-class symbols that go
uncited beyond three turns are treated as noise. K is a **calibration
target**, not a hard constant: the recall-fidelity simulation (§9)
is the instrument by which K = 3 earns its value. Future directive
plumbing will expose it as `staging.window-turns`.

#### 3.10.4 Promotion

When a decision-class delta is processed, the §3.3 symbol extraction
pass produces a set of candidates. For each candidate, the runtime
checks whether a matching entry exists in the staging buffer (normalized
form match). If it does:

1. The staged symbol is **promoted**: moved from the staging buffer into
   the coalesce buffer, where it joins the turn's decision-class symbols.
2. At turn close the promoted symbol commits to `history_symbols` and
   `symbols.jsonl` normally — anchored to the decision delta that cited
   it.
3. The raw bytes of the task-class delta that produced the symbol are
   **not** promoted. Only the symbol crosses over; the task content
   remains discardable.

The cross-reference is normalized-form match only — no fuzzy matching.
A promotion event is logged as `staging.promoted` with `normalized`,
`staged_at`, `cited_at`, and `turn_span` fields.

#### 3.10.5 Window-close eviction

At the top of each turn (before any delta in the new turn is processed),
`pruneStaging` evicts staging entries whose citation window has closed:
any entry with `StagedAt < (currentTurn - K + 1)` is dropped. These
symbols were never cited by a decision delta within the window; they are
treated as noise and discarded. A nonzero eviction count is logged as
`staging.evicted`.

This eviction is deterministic and requires no LLM involvement.

#### 3.10.6 What stays transient

The following content is **never** written to persistent substrate:

- Raw bytes of task-class deltas (file content from `fs.read`/`fs.write`,
  tool output from `tool.result`, captured output from
  `user.shell-capture`, and the `fs.commit` pointer event). These exist
  only in the turn-local context window during their relevant turns; the
  event log records a short metadata summary in their place (source,
  path, byte count). This minimization is gated on the **retention class**
  (§3.10.1): the §3.0 chain (`internal/turn/chain.go`, step 5) summarizes
  every source whose provisional class is `task` — i.e. the full
  task-class set above, not a hand-picked subset — so no raw task-class
  body ever reaches the persistent event log, and a newly-added
  task-class source inherits the minimization automatically.
- Symbols extracted from task-class deltas that are not cited by any
  decision delta within K turns. These are evicted from staging without
  entering any persistent index.

File content has a separate persistent home in the per-thread
tracked-file store (§3.9.1, `files.json`); that mechanism is distinct
from the symbol lifecycle and is not governed by this section.

#### 3.10.7 Why this matters

This mechanism allows personant to pull large task-class context into
a turn — multi-MB file reads, verbose tool outputs, shell captures —
without that content accumulating in the persistent symbol index or
the event log. Without it, every `tool.result` line that happened to
contain a file path or URL would inflate `history_symbols`, raise
Jaccard's denominator, and degrade recall precision in §3.4. At
simulation scale (§9.1), this pollution would compound turn-by-turn
until recall measurements reflected noise level as much as signal. The
transient-data lifecycle is therefore a prerequisite for meaningful
execution of the recall-fidelity measurement regime (§9).

#### 3.10.8 Content retention (keep/toss) — deferred precision layer

§3.10.1–.7 govern *symbol* promotion. **Content retention** — which retained
turn-excerpts (§2.3) survive on disk for §3.4 intra-thread recall — is the
adjacent keep/toss decision. The v0.1 **first cut over-retains**: decision-class
excerpts are all kept (safe — losing crucial content is the dangerous error;
keeping fluff is the cheap one), raw task-class tool output is never an excerpt
(already transient). The **precision layer** that trims the retained set is
deferred to the sleep-cycle keep/toss (§-sleep-cycle, ARCHITECTURE), and its
mechanism is settled:

- **Raw tool output → always transient. Deterministic, no judgment.** The bulk is
  disposed by the substrate with zero LLM involvement.
- **The agent's *re-presentation* of tool data → the durable candidate**, marked
  inline by the agent via a pinned closed-set marker — exactly one of
  `lifetime:transient` | `lifetime:durable`, at a fixed stripped position (the
  topic-tag #81 emit-then-parse pattern; metadata, never shown to the user). This
  is the one inherently-fuzzy call (is this stat worth remembering?) and is
  reserved for the LLM per the founding division of labor; everything else is
  deterministic.
- **Fail-safe = durable.** A missing/malformed marker resolves to *preserve* — a
  non-compliant model over-retains safely. Compliance is measured in the
  inference-in-loop sim (rides the #81/#98 tag-discipline harness); the metric to
  watch is the omission rate.
- The marker's primary job is **downgrading**: decision content is preserve-by-
  default, so a genuinely-important re-presented stat is kept automatically; the
  marker lets the agent flag a *routine* tool-data presentation `lifetime:transient`
  to trim noise. So `durable = decision content − agent-marked-transient`.

The in-turn agent marker and the offline sleep-cycle keep/toss are the **same
decision at two cadences**. Not built in v0.1; the first-cut over-retention is the
correctness floor it later trims.

### 3.11 Substrate commit cadence

The substrate is backed by **two** git DBs (§4.5.8): a **daily** DB
(`.git-daily/`, disposable per-turn microscope) and a **primary** DB
(`.git/`, permanent day-grain career history). The cadence policy is
**per-turn commit to daily, day-grain commit to primary**, superseding
the earlier commit-on-structural-change scheme (#94, waves R1–R5).

**Per-turn (daily).** Every turn commits its **scoped** write set to
**daily** at turn close (`CommitTurn`, `Personant-Turn: <id>` trailer)
— the ≤1-turn durability boundary of §4.5.8. Staging is scoped to the
turn's recorded write set (`autogit.AddPaths`), O(write-set) with no
tree walk; daily commits carry **no integrity flags** (`0,0`) so
per-turn cost stays flat. Structural changes (create/close a thread,
create a project) no longer drive a *separate* commit — they ride the
per-turn daily commit and are folded into the day at the barrier.

**Per-day (primary).** The **day barrier** (§4.5.8 B0–B6, fired on
new-day detection) writes exactly **one** day-grain commit to primary
(`Personant-Day: <N>`, `AllowEmptyCommits`) plus the day's archival
anchors (§3.8, barrier-homed at B1). This bounds primary to ~365
commits/year forever — a single small pack — and moves integrity gating
off the per-turn path to **once a day**: `CheckSpineIntegrity` on the
staged day-commit (B2) and `CheckDerivedFresh` on the post-rebuild
assert (B6). The daily DB is nuked and reborn at each barrier (B4/B5),
so its loose objects do not accrue across days.

**Checkpoint (session-close + mid-session structural) → daily.** All
`Checkpoint` calls target **daily** with a full `Add(".")` sweep — the
belt-and-braces backstop that absorbs hand-edits and any content not yet
scoped-committed. It is normally a no-op (daily is already current at
close). Primary is **barrier-exclusive** (§4.5.8 INV-5).

**gc cadence.** Packing is **count-triggered** plus the offline
sleep-cycle. Count-triggered: after every `gcCheckEveryCommits` (64)
per-turn daily commits the adapter reads daily's loose-object count and
runs gc when it crosses `gcLooseObjectThreshold` (5000). The offline
sleep/consolidation cycle (`MemoryOps.Consolidate` → go-git
`RepackObjects` + `Prune`, fired on the §9 day-off idle window) packs
both repos. A default-off mid-day re-baseline knob (§4.5.8, §6.2)
handles the pathological long-single-session case where no barrier fires
to bound daily loose objects.

**Durability.** Content is durable to ≤1 turn at all times: the journal
(§4.5.8) protects the in-flight turn's raw bytes before any canonical
write, and the scoped daily commit lands the turn structurally at close.
This section governs git recovery points; §4.5.8 governs the crash
protocol and mid-session recovery.

---

## 4. User surface

The user surface is what the human types or invokes. Three channels feed
input to the runtime: CLI commands (used outside an active conversation),
in-conversation slash commands, and shell escapes. The agent itself does
not appear in this section — its tool surface is §6.

### 4.1 CLI command surface

Go binary subcommands (see also §2.1):

- `personant init` — first-run scaffold; idempotent (see §8.1).
- `personant index rebuild` — regenerate derived files from canonical (§2.1).
- `personant index check` — validate derived files match canonical; non-zero on mismatch.
- `personant verify` — full structural validation.
- `personant ping` — send a single prompt to a configured provider and print the response (`--provider`, `--prompt`, `--model`, `--timeout` flags). Connectivity smoke test.
- `personant models` — list models available from a provider (`--provider` flag).
- `personant version` — print version identity (both semver lines, the home format this binary writes, the build stamp) plus the resolved home path and the format stamp found there. **Ungated by construction** (§9.1): never runs the format gate, never reconciles, never fails on a broken home. `personant --version` prints the one-line short form only.

Persistent flags on every subcommand: `--home` (override `$PERSONANT_HOME` for the invocation) and `--allow-newer-home` (open a home whose on-disk format exceeds this binary's — §9.1; leaves a stderr warning and a `system.home-format-override` event).
- `personant search <query>` — symbol/text search across spine and threads.
- `personant deps <thr_id>` — show threads referenced by anchors of the given thread.
- `personant permissions list` — show accrued grants from directive files (§6.2.3).
- `personant log <date|range>` — query the event log.

### 4.2 In-conversation slash commands

Slash commands begin with `/` and are intercepted by the runtime before any
prompt construction. They are user-initiated and bypass the LLM's tool
surface entirely.

| Command | Purpose |
|---|---|
| `/topic <name>` | force a new thread with the given working name |
| `/done` | request closure ack on the active thread (§3.5); always fully interactive |
| `/closures` | drain the §3.5 pending-review queue now — the exception closures awaiting an ack (AMENDED 2026-08-04). The same drain runs at clean session exit; this is the user choosing the moment. An empty queue says so |
| `/pause` | mark active thread as `paused` (§2.2.1) |
| `/resume` | resume a paused thread |
| `/back-to <thr_id>` | re-engage a retired thread |
| `/no-revisit` | tighten threshold on the most recent recall surface (§3.4) |
| `/project` | print active project (id, name, root path, remote URL if any) |
| `/project switch <name-or-id>` | switch active project to a known one |
| `/project rename <new-name>` | rename the active project; updates `name` only (id, path, remote URL unchanged) — see §4.5 |
| `/cd-project <path>` | set active project root to `<path>` (see §4.5) |
| `/model [<model-id>\|<provider>/<model-id>]` | switch the session's LLM (AMENDED 2026-08-05 — IMPLEMENTED, and the argument form is wider than the original `<id>`). The **bare form reports** the active `provider/model` plus a usage hint and mutates nothing (the `/thinking` convention). A bare `<model-id>` switches model within the current provider; a `<provider>/<model-id>` reference switches provider AND model, and the named provider must be an `inference` entry in the pool — a `search` entry is refused by kind, exactly as at session open. **Ambiguity rule** (model ids may themselves contain `/`, e.g. a HuggingFace-style `org/name` on a local server): the text before the FIRST `/` is read as a provider name **only when it names a provider in `providers.toml`**; otherwise the whole argument is a model id on the current provider. The decision is a pool lookup, not a heuristic, and it agrees with §8.2.2's split-on-first-`/` convention wherever a provider is genuinely named. **Verification mirrors session open** (same probe, same messages): a model absent from a non-empty `/models` list **REFUSES** the switch, naming what the provider does offer, and leaves model, provider and client untouched; an unreachable or model-less `/models` warns and switches **UNVERIFIED**. **Session-scoped and deliberately never serialized:** `[chat] defaultModel` remains the startup default, and no home state records a "last model used" — the printed confirmation says so. Takes effect on the next turn (commands and turns are alternating branches of one loop, so no in-flight turn exists to touch), and is recorded as `model.switched` (§2.8) |
| `/thinking [on\|off]` | show/hide the model's live reasoning, dimmed; bare form reports the state. Session-scoped override of `[chat] showThinking` (§8.2.2) — never written back to `config.toml` |
| `/stats` | runtime stats (active threads, layer fill, recent recall events, etc.) |
| `/version` | version identity plus the on-disk format of the home this session opened (§9.1) |

### 4.3 Decline categorization UI

When a recall offer is declined, the user picks a reason from a fixed
enum: `not-relevant` / `wrong-project` / `already-known`. v0.1
deliberately favors reasons that yield useful recall-tuning signal
(why a candidate missed) over U/X-oriented actions like defer or
suppress-offers; the latter return when the recall accrual loop is
actually built. The enum is expected to be tuned then.

### 4.3.1 REPL line editing and history (AMENDED)

**Original specification (superseded).** The requirement list below,
satisfied by a readline-class library over the process's stdin.

**As built (amendment, terminal-layer design 2026-08-04).** The editor is
**personant's own**, inside the terminal arbiter, and it is not a
requirement list any more — it is a consequence of who owns the file
descriptor. A library that hardcodes the process fds applies its own
terminal mode, installs its own signal handlers, and holds its own byte
reservoirs, which makes it a second owner of stdin: type-ahead stranded in
those reservoirs is invisible to anyone else reading the same fd, and no
test can drive the editing path at all. Both are disqualifying under §4.3.3
(a mid-turn key must be observable) and §9 (a behavior only a human at a
terminal can check is not verifiable). The arbiter therefore reads stdin
itself, decodes keys, and renders the line; see the ARCHITECTURE.md
"Terminal ownership" mechanism for the shape.

The behavior required, unchanged in substance:

- `←` / `→` cursor movement within the current line.
- `↑` / `↓` command history; persistent across sessions in
  `<Home>/history` (operational, not git-tracked). Newline-delimited text.
- Standard readline shortcuts: Ctrl-A / Ctrl-E (line start/end),
  Ctrl-B / Ctrl-F (char back/forward), Ctrl-W (kill word), Ctrl-U (kill
  line), Ctrl-K (kill to end), Home / End, Delete.
  **Alt-B / Alt-F (word-back / word-forward) are specified but NOT
  IMPLEMENTED** — the decoder reports the Meta form, the editor does not
  yet bind it, and an Alt-modified rune is currently inserted as its base
  character. Open item, not a ruling.
- History deduplication (no consecutive duplicate entries).
- History size cap (configurable; default 1000 entries).
- **Multi-row rendering.** A line longer than the terminal width wraps and
  stays editable, repainted as a block rather than scrolled horizontally.
  Rendered rows are **capped** (default `min(10, rows−2)`, user ruling
  2026-07-31 — arbitration item 6) with the excess collapsed to a
  placeholder and the **whole buffer retained**: content taller than the
  terminal cannot be erased, so unbounded wrapping is not a nicety
  question. Submitting sends the full buffer regardless of what is drawn.
- **Multi-line input (amendment, 2026-08-05).** The buffer may hold line
  breaks. **Alt/Option+Enter inserts one** and is the universal key;
  **Shift+Enter inserts one where the terminal can say so** (see the
  terminal-reality note below); **bare Enter submits**, and the whole buffer
  is returned with its newlines intact. An embedded newline is a **hard row
  break** in the rendered block — the second cause of a row break beside
  width wrapping — and the row cap counts those rows like any other. `←`/`→`
  cross a newline as one rune and Backspace/Delete remove it as one rune.
  **Home / End / Ctrl-A / Ctrl-E operate on the logical line the cursor is
  on** (the run between two newlines); the kills (Ctrl-U / Ctrl-K) stay
  **buffer-scoped**, because clearing a recalled multi-line draft must not be
  a repeated keystroke. History carries multi-line entries: the on-disk file
  is one entry per LINE, so a newline inside an entry is written `\n` and a
  literal backslash `\\` — escaping the escape makes the encoding injective,
  so it cannot collide with content — and `↑` recall renders the whole block.

  **Terminal reality — why there are two keys.** Legacy terminal input
  encodes Enter and Shift+Enter as the SAME byte (`0x0D`); the modifier is
  visible only under an enhanced keyboard protocol (kitty CSI-u:
  `ESC [ 13 ; 2 u`). Personant DECODES that sequence but does not REQUEST
  the protocol — a protocol push/pop is new terminal-mode-ownership surface,
  and holding mode ownership in one place is what this section's arbiter is
  for; requesting it under that one owner is a possible future item, not a
  gap. Shift+Enter therefore works only in a terminal configured to send a
  distinguishable sequence, which is a one-line keybinding — the same method
  Claude Code's `/terminal-setup` uses:

  - **iTerm2** — Settings → Profiles → Keys → Key Mappings → `+`, press
    Shift+Enter, action **Send Escape Sequence**, escape `[13;2u`.
    (Equivalently, action **Send Text with "vim" Special Chars** with text
    `\e\r`, which sends the universal Alt+Enter form.)
  - **Zed** — in `keymap.json`:
    `{"context": "Terminal", "bindings": {"shift-enter": ["terminal::SendText", "\u001b\r"]}}`
    — that is `ESC CR`, the universal Alt+Enter form, in JSON escapes.
  - **Anything else** — Alt/Option+Enter already works unconfigured. On
    macOS, Terminal.app needs *Use Option as Meta key* for the Option form.

- **Editable pre-filled default.** A question may open with text already in
  the buffer, cursor at end — the §4.3.3 retraction re-offer and the
  `/done` summary edit both use it.
- **A question is part of the read, never a write before it.** There is one
  read path and it takes the prompt (and any single-keystroke answer set)
  as an argument, so no call site can print a question that the editor's
  first repaint then erases. A single-key answer returns without Enter; a
  first keystroke outside the set becomes the first character of a normally
  edited line.
- **Non-interactive sessions** (piped stdin, the harness, tests) install no
  mode, run no reader loop, and read lines from the buffered stream —
  byte-identical output to the pre-arbiter runtime, which is what the
  committed piped-session golden asserts.

### 4.3.2 Turn progress indicator (AMENDED)

A turn has two windows in which the user gets no output: between pressing
enter and the model's first token, and between the last token and the next
prompt (§3.4 recall, the §3.5 closure scan, `CommitTurn`, working-set save).
With a live embedding provider either can run for seconds.

The REPL renders a **phase-labeled** indicator across both — the label
names what the runtime is doing, not merely that it is busy. The phase
vocabulary is fixed and small:

| Phase | Window |
|---|---|
| composing context | §3.0 delta chain + working-set composition |
| waiting on the model | the LLM round-trip (first-token latency) |
| re-issuing request | a §5.5 fetch or D6 re-prompt aborted the stream |
| running `<tool>` | §6.1 tool execution between two model streams |
| searching memory | the §3.4 recall stack (the embedding round-trip) |
| closing turn | response journal, engagement, closure scan, commit, save |

`running <tool>` is the one label that is a FAMILY rather than a constant:
it carries the tool's name from the identity-only `Chunk.ToolCalls` view
(ID + function, never arguments), because "waiting on the model" left up
for the whole of a multi-round tool turn names the wrong thing. A round
with several calls summarizes (`running web.search +2 more`) so the line
cannot wrap. The family shares the `running ` prefix, and
`turn.IsToolPhase` is its single membership test — the front end's §4.3.3
abort allow-list admits the family through that predicate rather than by
listing labels it cannot know in advance.

Requirements:

- The turn pipeline **announces** phases through an optional hook; it does
  not render. Phases are a presentation signal only — never journaled,
  logged, or committed, and nothing branches on them.
- The indicator is a **formatter, not a writer**. It decides what the wait
  line says and hands the finished string to the terminal arbiter's single
  **ephemeral slot** — a token-less, one-line region, replaced whole. It
  keeps no belief about the cursor or about who owns the current line;
  those are the arbiter's, held once. Two projections of "is this line
  mine?" is the defect this split removes.
- Rendering is **terminal-gated**, on the arbiter's single TTY predicate
  rather than a second opinion. On a non-terminal stdout (piped runs, the
  scenario harness, tests) the indicator emits zero bytes. On a no-ANSI
  terminal (`TERM=dumb` or unset) an erasable region cannot exist, so it
  degrades to **one committed line per occupancy**: the first draw after a
  release prints, further draws are dropped until the slot is released
  again. **Accepted degradation:** that line carries the animation glyph,
  and the heartbeat keeps ticking where it used to be suppressed —
  suppressing it would require the formatter to know whether the terminal
  can erase, which is exactly the knowledge that moved to the arbiter.
- **Deferred reveal (amendment, 2026-08-04).** *Original rule: nothing is
  drawn until the wait passes a short reveal threshold, so ordinary short
  turns stay visually silent.* That rule alone made a **single-phase wait
  invisible for its whole duration**: the phase is announced at t≈0, inside
  the delay, and every later tick is a heartbeat — which may not claim an
  unoccupied slot (next bullet). The two rules deadlocked. As built, the
  threshold **defers** an announcement rather than discarding it: a
  swallowed announcement stays owed and the first heartbeat past the
  threshold delivers it, once, only onto a line nothing else has taken.
  After that, heartbeats are redraw-only again.
- **A heartbeat may only redraw a frame that is still on screen.** An
  announcement (new phase, or the abort hint appearing) claims the slot
  unconditionally; the animation tick must not. Otherwise a frame lands in
  the middle of a streaming response — the arbiter's own record of what
  occupies the line is what tells the two cases apart.
- Content always wins the terminal: the first response byte — and any
  interactive offer prompt (§3.4 recall, §3.5 closure) — retires the
  indicator and clears its line before writing. The indicator is restored
  for the close window afterwards, which is why turn close re-announces its
  phase. While a **read** is in flight the slot is suspended outright: it
  writes nothing and records nothing, so the heartbeat rule keeps the
  indicator quiet without the formatter knowing a prompt is up.
- The line is cleared on every exit: normal completion, per-turn error,
  and both interrupt paths (§4.3.3 clean shutdown and the forced quit).
  The forced path runs no deferred cleanup, so the clear happens first.
- While the turn is still abortable (§4.3.3) the label advertises the
  abort key, appearing and disappearing **with** the abort window rather
  than one frame later: the hint and the arbiter's abort-window state are
  set by the same two calls and are one fact, not two. The arbiter
  truncates the rendered line to the terminal width less one column — a
  wrapped line turns the in-place redraw into a scroll, and writing the
  final column puts xterm-family terminals in pending-wrap.

### 4.3.3 Interrupt and abort keys (AMENDED)

Two keys, two meanings. Conflating them is the U/X defect this section
exists to prevent — the user needs a way to abandon a turn that does
**not** cost them the session.

**Original specification (superseded).** A single `Ctrl-C` at the prompt
ended the session, identical to `/exit` and silent. `Esc` was the line
editor's key at the prompt. `Ctrl-Z` was unspecified.

**As built (amendment, user rulings 2026-07-31 — arbitration items 2–6).**
The table below. The hinge is item 2: the session terminal mode clears
`ISIG`, so wherever the arbiter owns the file descriptor a `Ctrl-C` is a
**decoded key, not a signal**. That is not a side effect — it is the only
way a disposition can be keyed on what is in the editor's buffer, which an
async signal handler cannot read without racing the editor. Single-press
exit was reversed on the grounds that it is destructive on a typo (item 3).

| Key | At the prompt, line non-empty | At the prompt, line empty | During a turn |
|---|---|---|---|
| `Ctrl-C` | **clears the line**; the session survives and the cleared text does not enter history — it was never submitted | first press **hints**; a second CONSECUTIVE press ends the session, cleanly and silently | abandons the turn, then the same clean shutdown, with a one-line notice (item 4) |
| `Ctrl-D` | delete-forward | ends the session (end of input) | n/a — nothing is reading the line editor |
| `Esc` | nothing — swallowed | nothing — swallowed | aborts the turn and returns to the prompt; the session continues |
| `Ctrl-Z` | suspends cleanly; `fg` returns with the typed line intact | as at left | suspends cleanly; `fg` returns to the still-streaming turn (item 5) |

- **The `Ctrl-C` double press must be consecutive.** Any other key spends
  the pending press and takes the hint back down, and a press that cleared
  a line resets the count. Two presses far apart, with typing between them,
  must NOT exit: the second press is a first press.
- **`Esc` at the prompt is deliberately inert.** Retraction is a property
  of a turn in flight; a line being typed has nothing to retract, and the
  editor already has `Ctrl-C` for "discard this line".
- A **second `Ctrl-C` during shutdown** forces an immediate exit. This is
  the safety valve for a shutdown that itself wedges, and it restores the
  terminal before exiting.
- The clean-shutdown path (recaller close, the §3.11 session-close
  checkpoint, the session-end log) runs on the **uncancelled parent
  context**, which is why a `Ctrl-C` exit loses no state.
- **Honest wording.** The shutdown notice is a warning; an ordinary quit
  earns no warning. It is printed only when a turn was actually in flight.
- **Esc is a routine action, not a fault.** An abort must leave the
  substrate exactly as a turn that never happened: no open §4.5.8 turn
  scope, so the next launch opens quiet. This constrains *when* Esc may
  fire — see below.
- **Esc means "retract my input", not "stop generating".** The user is
  declaring the input a mistake or incomplete, so any response to it is
  irrelevant by construction. Timing relative to the model's first token
  is therefore irrelevant too: there is no partial-response-keeping
  behavior, at any point.

**The retraction rule (normative).** A retracted prompt does **not enter
memory**. The only way its text ever enters memory is the user
deliberately re-submitting it. Three consequences:

1. **Session rollback.** The `user.prompt` delta fires at step 1 of the
   §3.0 chain, long before the model call, so by the time the user
   reaches for Esc it has already run. A pre-canonical abort must
   therefore restore the session-volatile runtime state — turn counter,
   the §3.10 cross-turn staging buffer, Layer B/C membership, history,
   recall-surfaced marks, embedding debt — to its pre-turn value.
   Otherwise the retracted prompt still shapes the next turn: citing a
   staged task-class symbol PROMOTES it out of staging, and the promotion
   then evaporates with the turn-scoped coalesce buffer, permanently
   consuming a symbol a later real prompt could have cited. The rollback
   is pure in-memory work and must not perturb the §4.5.8 write ordering
   it runs alongside. Post-canonical failures do **not** roll back —
   canonical writes exist and recovery owns them.
2. **Getting the text back.** The aborted input is re-offered at the next
   prompt as an **editable default** (cursor at end), so the user can fix
   and resubmit or clear it. It is also already in `↑` history. Neither is
   memory; both require a deliberate re-submission.
3. **No durable full-text artifact.** The abort record is one §2.8 event
   line — `system.turn-aborted` with turn id, phase and byte count. Not
   the text.

**On-screen honesty.** If the abort lands mid-stream, partial response
tokens are already on the terminal and cannot be reliably erased (the
output may have scrolled and may span many rows). The REPL prints one
terse, visually distinct marker so the transcript does not assert
something false — the user must be able to see that what is on screen is
not in memory and never was.
- **Abort window.** Esc cancels the turn's context, and a cancelled
  substrate op fails the turn. That is only safe while the turn is
  **pre-canonical** (§4.5.8): before the first canonical write, the turn's
  abort handler releases the recovery scope, so nothing is left behind.
  After it, a failure deliberately leaves the scope open for recovery.
  The abort window therefore closes at the first non-pre-canonical phase
  (`closing turn`) and the front end must gate on an **allow-list** of
  abortable phases, so a phase added later is non-abortable by default.
  The §6.1 `running <tool>` family is ON that list: a tool round runs
  entirely between model streams and writes nothing canonical, and a slow
  fetch is exactly the wait a user reaches for Esc during. Cancelling the
  turn cancels the running tool — handlers receive the turn's context.
**Terminal mode and the single reader (AMENDED).**

*Original specification (superseded): the mid-turn cbreak mode is entered
only between prompts and restored before the next one, byte-for-byte as the
line editor left it; `OPOST` and `ISIG` both stay on; the reader watching
for the key is retired before any mid-turn prompt asks a question.*

*As built:* the arbiter installs **one mode for the whole session** and
runs **one reader** from open to close.

- **Two mode installations per session** — capture the entry mode, install
  the session mode; restore at exit — plus one restore/re-install pair per
  child window (§4.4.2). **Zero prompt/turn transitions.** The only thing
  that ever varied between prompt and turn was the read-blocking strategy
  (`VMIN`/`VTIME`); with the arbiter owning the reader, readiness comes
  from `poll(2)` as a **call argument** instead. A parameter cannot be
  observed, adopted, or left stale, so the "mode went stale" defect class
  ceases to exist rather than being disciplined.
- **The session mode clears `ICANON`, `ECHO`, `IEXTEN`, `IXON` and
  `ISIG`.** `OPOST` is untouched — clearing it would stop a bare `\n`
  implying a carriage return and staircase both the response body and the
  indicator. Clearing `ISIG` is the item-2 ruling: `Ctrl-C`, `Ctrl-Z` and
  `Ctrl-\` arrive as bytes, which is what makes the buffer-keyed
  dispositions above expressible. It does not *add* a reimplementation
  burden — the runtime already hand-rolled these semantics.
- **`Ctrl-Z` is handled in band and by the arbiter alone**, because only
  the owner of the file descriptor can suspend correctly: restore the entry
  mode, raise `SIGTSTP` at its default disposition, re-install the session
  mode on `SIGCONT`. There is no exported API and no client veto. The prior
  behavior — suspending with the mode still installed, so the shell
  inherited a terminal nobody re-established — was a latent defect.
- **`SIGTERM`/`SIGHUP` restore the entry mode** before the process dies,
  with the conventional 128+signal status. They are not caught and returned
  from: that would turn `kill` into a no-op, a worse bug than the one being
  fixed. Mode ownership and exit-restore are the same responsibility, so
  they live together.
- **One reader, always** — except inside the §4.4.2 child window, where the
  reader is joined **before** the child starts and restarted after it is
  reaped, so nothing in-process competes for bytes with the child or with a
  pager it spawns. Consumers register on a **LIFO stack** and receive
  **decoded events**; they never name the file descriptor. This is not
  tidiness: disambiguating a bare `Esc` from an `Esc`-prefixed sequence
  requires holding bytes read-but-not-yet-interpreted **across a timeout**,
  and a lookahead that straddles a handover is silently lost. A mid-turn
  prompt (§3.4 recall, §3.5 closure) therefore does not retire the reader —
  it pushes a nested entry **over** the turn, which is why the stack is LIFO
  rather than a single owner slot.
- **Type-ahead survives every boundary.** An `Esc` typed in the gap after
  Enter, or buried in a burst mid-turn, reaches the abort window; text typed
  in the gap lands in the next prompt. Under the previous multi-reader
  arrangement those bytes could be stranded in a private buffer and never
  seen.
- **Non-terminal sessions** (piped stdin, the harness, tests) install no
  mode, run no reader loop, and behave exactly as before — byte-identical,
  asserted by the committed piped-session golden. `Esc` is unavailable
  there; everything else stands.

### 4.4 Shell escape (`$` and `#`)

The user can run arbitrary shell commands without leaving the personant
prompt by prefixing their input. This is **user-initiated** — it does not
extend the LLM's tool surface in any way (see §6.1.3 — the LLM has no
shell tool).

#### 4.4.1 Prefix semantics

| Prefix | Meaning | Output → next-turn context | Logged |
|---|---|---|---|
| `$` | run in user's shell, fire-and-forget | no — streams to user's terminal only | yes (event line, no captured output) |
| `#` | run in user's shell, capture output | yes (subject to §6.5 byte cap) | yes (with captured output) |

The captured output for `#` flows through §3.3's deterministic symbol
extraction the same way turn content does — file paths, identifiers, and
URLs in the output are extracted automatically and feed the symbol
extractor.

**Delivery.** A `#` capture is buffered in the REPL and delivered as a
**pre-prompt delta** (§3.0, source `user.shell-capture`) on the NEXT turn,
firing before that turn's `user.prompt` delta and sharing its turn number.
Multiple `#` commands taken before one prompt are all delivered, in order.
The buffer is **not durable and deliberately so**: a capture is a
convenience for the next thing the user types, so a session that ends with
captures unconsumed simply drops them (logged as
`user.shell-capture-dropped`), and a crash between capture and prompt loses
them. Journaling arbitrary command output on a path with no recovery
contract would be inventing durability nothing asked for. An Esc-retracted
turn (§4.3.3) returns its captures to the buffer along with the input,
since they were taken before the turn began.

Two bounds apply, at different layers and for different reasons: the
in-memory capture buffer is bounded so a runaway command cannot balloon the
process before anything else runs, and the §6.5 byte cap bounds what
actually reaches the model's working window. Key material in a capture is
redacted on the context path only — see §8.2.1.

#### 4.4.2 Shell process — per-command execution (AMENDED)

**Original specification (superseded).** The runtime spawns one long-lived
`$SHELL -i` (fall back to `/bin/sh -i`) subprocess at personant startup;
`$`/`#` lines are written to its stdin, and shell state — cwd, environment,
aliases, functions, history — persists naturally because the same process
services every invocation.

**As built (amendment, front end 0.0.7).** Each `$`/`#` line runs a **fresh
`$SHELL -c <command>`** (falling back to `/bin/sh`). There is no long-lived
shell subprocess.

Rationale. The persistent shell exists mainly to preserve cwd/env/aliases
across invocations, but §4.4.3 already excludes the interactive applications
a persistent PTY would principally serve, and a long-lived shell on pipes
needs a sentinel-marker protocol with a timeout and hang recovery for
commands that consume the sentinel from stdin. Per-command execution gets
exit codes for free, cannot hang the session, and — with the cwd epilogue
below — satisfies §4.5.1's "shell cwd" concept, which is the load-bearing
part. The REPL-facing seam is identical, so a persistent shell can replace
the implementation later without touching the surface.

**cwd persistence — epilogue, not `cd`-parsing.** A reporting epilogue is
appended to every command and the runtime adopts the reported directory as
the shell cwd for subsequent invocations (passed as the child's working
directory). `cd` is deliberately **not** intercepted or parsed: naive
parsing misses `cd /x && ls`, `pushd`, a cd inside a function or subshell,
and any rc-driven change. The report goes out on a **dedicated file
descriptor (fd 3)**, not delimited inside stdout — a separate channel cannot
collide with command output at all, so the trailer never has to be stripped
back out of the live terminal stream or the capture buffer, and the reported
path is carried verbatim (a path containing a newline, a NUL, or a space is
safe). The exit status is **not** in the trailer: the epilogue re-exits with
the user command's status, so the process exit code is the single source of
truth. A reported directory that no longer exists is refused, leaving the
last known-good cwd in place.

**Accepted loss.** Aliases, shell functions, and exported environment
defined *mid-session* do not persist from one `$`/`#` line to the next. Only
the cwd does. What rc files contribute was measured, not assumed (macOS
zsh 5.9 / bash-3.2-as-`sh`):

- `zsh -c` sources `/etc/zshenv` and `~/.zshenv` **only**. `~/.zshrc` is
  **not** sourced — it is interactive-only — so aliases defined there are
  unavailable. Aliases in `.zshenv` are available.
- `sh -c` sources **nothing**: not `~/.profile`, and not `$ENV`/`$BASH_ENV`
  (both are interactive-only for `sh`).

**Ctrl-C.** The child runs in its **own process group**. Ctrl-C during a
`$`/`#` command is forwarded to that group and does **not** end the session
(which is what Ctrl-C means at the prompt, §4.3.3) — standard shell
semantics: the running command dies and the prompt returns. The session's
interrupt count is untouched, so a later Ctrl-C at the prompt still behaves
as the first one.

This is an **invariant, not a best effort**:

> From the moment the runner commits to a `$`/`#` command until that
> child is reaped, a SIGINT must **never** reach the session-cancel path.

The stronger statement is required because the REPL asks the runner first
and ends the session whenever the runner declines, so *any* instant in
which a command is running and the runner says otherwise is an instant in
which Ctrl-C quits personant instead of killing the command. Publishing
the child's process group as early as possible and treating "have a pgid"
as "have a child" makes that window small rather than empty, and a small
window is what a loaded machine finds. **Interrupt ownership is therefore
tracked separately from deliverability**: the runner claims the signal
from the commit point, and an interrupt claimed before the process group
exists is **queued** and delivered the instant it does. A claimed
interrupt is never silently dropped; the one case where it cannot be
delivered is a child that failed to start at all, where the command's
own error is the report and there is nothing left to kill. The parent
re-asserts `setpgid` after `fork` so the group provably exists before it
is published, closing the fork/exec race in which `kill(-pid)` would find
no group.

**Terminal state — the handoff window.** The session mode has `ICANON` and
`ECHO` off for the whole session (§4.3.1/§4.3.3), so a naively-spawned child
would inherit a terminal with no echo and no line discipline. The mode
personant found at startup is captured at open and **restored for the
duration of every child**, then put back after the child exits. The
in-process reader is joined before the child starts and restarted after it
is reaped, so neither personant nor a pager the child spawns competes for
stdin. Spawning a child and handing over the terminal compose in exactly one
function, so the window cannot be leaked.

Restoring the entry mode restores **`ISIG`** with it — shells leave it on —
and that is the other half of the §4.3.3 ruling rather than an accident.
This is the one mid-session window in which a `Ctrl-C` is a real signal
instead of a decoded key, which is precisely what the invariant above needs:
the child is in its own process group, so the kernel must still generate
`SIGINT` for personant for the runner to have anything to forward.

#### 4.4.3 Interactive applications

Interactive terminal applications (`vim`, `nano`, `less`, `top`, etc.) are
**not supported**. Terminal-mode handoff itself is no longer the obstacle —
`term.Handoff` restores the entry mode, joins the reader, and puts the
session mode back afterwards, which is exactly what a `$`/`#` child gets.
What is missing is the other half: wiring the user's terminal to the child's
PTY, so an application that draws a full screen has one to draw on and a
reader to take its keystrokes. Under §4.4 the child gets the terminal's
*mode*, not its *input*. If the user wants `vim`, they suspend personant or
open another terminal.

The child's **stdin is the null device**. That is the enforcement, not a
detail: an interactive program reads EOF and exits with its own complaint
instead of wedging the REPL forever on input that will never arrive. A
non-zero exit is announced on the terminal (`[exit N]`, or `[interrupted]`
for a signalled command) so the failure is legible rather than looking like
the command silently doing nothing.

#### 4.4.4 Permission tiers do not apply to `$`/`#`

The permission tier model in §6.2 governs *agent-initiated* operations.
`$`/`#` are user-initiated; they bypass the tier check entirely. The user
runs git, deletes files, etc., with full shell privileges. Logging captures
the invocation (and for `#`, the captured output) so the runtime can
incorporate the action into memory — `user.shell` is a first-class event
category in §2.8.

### 4.5 Project identity, project root, and shell cwd

#### 4.5.1 Two cwds, deliberately distinguished

The runtime tracks two cwds:

| Concept | Set by | Used for |
|---|---|---|
| Active project root | `/cd-project <path>` or `/project switch <name-or-id>` | path resolution for agent tools (`fs.read`/`fs.list`/`fs.grep`/`propose_*`); permission-tier classification (§6.2.6); project-scoped directives (§2.5, §2.6) |
| Shell cwd | any directory change a `$`/`#` command performs, learned from the §4.4.2 cwd epilogue rather than by parsing `cd` | path resolution for `$` and `#` shell commands only |

They diverge freely. The user `$ cd`-ing the shell into `/etc` for grep
convenience does not change the active project or grant the agent any new
permissions.

#### 4.5.2 Three layers of project identity

A project's identity has three distinct layers (see §2.5.1 for the schema):

| Layer | Form | Stable? |
|---|---|---|
| Internal handle | `prj_<n>` | yes — assigned at creation, never changes; storage layout key, spine reference key |
| External canonical identity | normalized git remote URL (`remote_urls[0]`) | mutable but rare; gained when the project's git tree first acquires a remote; survives file moves |
| Display name | user-chosen string | mutable via `/project rename` |

The internal handle is always present. External canonical identity is
*adopted* when a git remote appears (see §4.5.4). Renames touch only the
display name — id, path, and remote URL are unaffected, and no spine entries
are rewritten.

#### 4.5.3 `/cd-project <path>` resolution flow

1. Resolve `<path>`; query its git config for the canonical remote URL
   (`origin` by default; otherwise none).
2. **If a remote URL is found:** look up known projects by `remote_urls`.
   - Match → switch (silent); update `current_root_path` if it has drifted from the matched project's stored value (and append the old path to `historical_root_paths`).
3. **Else (no remote at this path):** look up by `current_root_path`, then `historical_root_paths`.
   - Match → switch.
4. **No match anywhere:** prompt:
   ```
   Path '<path>' is not associated with a known project.
   [s] switch an existing project to this path
   [c] create a new project here
   [n] cancel
   ```
   On `[s]`, list known projects (id + name) and let user pick. On `[c]`, ask for a display name; allocate a new `prj_<n>`; record path; auto-detect remote URL.

The runtime does **not** auto-switch the active project when `$ cd` lands
the shell on a known project's directory. Project switching is always
explicit, to avoid surprise behavior where a shell command silently changes
which spine the agent is anchored to.

#### 4.5.4 Local→remote promotion (auto-detect)

The runtime polls the active project's git config cheaply (e.g. once per
turn or at engagement) for its remote URL. State machine:

- **None → present.** Prompt: `Project '<name>' just gained a git remote (<url>). Adopt as canonical identity?` Default `[y]`. Appends to `remote_urls`. Logged as `project.remote-adopted`.
- **Present → updated.** Rare. If it normalizes to the same URL, silent. Otherwise: prompt: `Remote URL changed (<old> → <new>). Update canonical identity? Keep the old one in history?` Default both. Appends new URL to `remote_urls`; old URL goes to `historical_remote_urls`. Logged as `project.remote-updated`.
- **Present → none.** Keep the historical entry; no demotion (the project might just be temporarily disconnected).

If a *different* known project already carries the just-gained remote URL
in its `remote_urls` (or `historical_remote_urls`), prompt:

```
Project '<name>' just gained a remote (<url>) that already belongs to '<other>'.
[m] merge into '<other>' (move threads + spine entries)
[k] keep separate (leave both projects intact)
[c] cancel — do not adopt the remote
```

This handles "two stub projects converged on the same upstream" cleanly.
Logged as `project.remote-collision-prompt`.

#### 4.5.5 File-move detection

When the agent tries to access the active project's root and the path is
gone (ENOENT on `current_root_path`), the runtime prompts:

```
Project '<name>' files at <path> are missing.
[r] relocate (provide new path)
[s] search (let the runtime scan a parent for the directory)
[w] wait (defer; assume temporary unavailability)
```

`[r]` updates `current_root_path` and appends old path to
`historical_root_paths`. If a git remote is preserved at the new path, the
identity is unchanged. Logged as `project.cd-changed`.

#### 4.5.6 `/project rename`

`/project rename <new-name>` updates the `name` field in `meta.json`. The
internal handle (`id`), the storage path (`projects/prj_<n>/`), the
canonical remote URL, and every spine entry's `project` field are
unaffected. Logged as `project.renamed`.

#### 4.5.7 Project bootstrap at startup

When the personant binary starts a new session, it resolves the active
project via a three-step waterfall, with an explicit-override rail above
and a final-fallback rail below.

**Explicit override:** if `--project <name-or-id>` is given on the command
line, use it directly; skip the heuristics. Error if the project isn't
known. This is the deterministic path for scripts and explicit
invocations.

**Heuristic waterfall** (in order; first hit wins):

1. **Git remote match.** If CWD (or any ancestor) is a git working tree,
   read its `origin` remote URL (or the first remote if no `origin`).
   Normalize per §2.5.1. Look up known projects by `remote_urls` and
   `historical_remote_urls`. On match: silent switch. Update
   `current_root_path` if it has drifted from the matched project's
   stored value (and append the old path to `historical_root_paths`).
   This is the strongest signal — remote URL is canonical identity.

2. **CWD path match.** If step 1 produced no match (no remote, or remote
   doesn't resolve to any known project): check whether CWD (or any
   ancestor) is `current_root_path` or appears in `historical_root_paths`
   of any known project. On match: silent switch. Updates as in step 1.
   This handles local-only projects (no git remote yet) and recovery
   from earlier file moves.

3. **Last-active confirmation.** If neither step 1 nor step 2 matched:
   read `<Home>/last-active` (operational; one line: `prj_<n>` of the
   most recently active project). Surface a prompt:
   ```
   Resume work on '<display_name>' (last active <RFC3339>)? [y/n/<other-name-or-id>]
   ```
   On `y`: silent switch. On `<other>`: treat as `/project switch`
   target. On `n`: fall through to final fallback.

**Final fallback** — fresh install, never-used home, or all heuristics
declined:

```
No active project resolved. Choose:
  [c]  create a new project rooted at this directory
  [s]  switch to a known project (lists ids/names)
  [n]  no project (use prj_default)
```

`prj_default` is always available as a no-project escape hatch.

**`last-active` file maintenance.** The runtime writes the active
project's `prj_<n>` to `<Home>/last-active` whenever the active project
changes — bootstrap, `/project switch`, `/cd-project`. The file is
operational (per §2.1: runtime-managed, not git-tracked, not
canonical), single-line, plain text, fast to read. Rewriting on every
active-project change is fine; the write volume is trivial.

#### 4.5.8 Startup recovery after unclean shutdown

A v0.1 substrate requirement, distinct from the deep-cold *archival*
recovery of §3.8 (which recovers retired threads via `git show`): the
runtime may be hard-terminated (crash, SIGKILL, power loss) mid-turn,
leaving derived state (the symbol index, working-set membership, the
`last-active` file) inconsistent with canonical (`spine.jsonl`, thread
files, logs). On startup the runtime **reconciles derived state against
canonical** and **repairs a torn in-flight turn** before opening the
session. Implemented (#94, waves R1–R5). Startup ordering is
load-bearing: **Init → Reconcile → (day barrier if new-day) →
LoadSession**; the runtime **refuses to open** on an unreconciled or
unrepairable substrate rather than trusting stale state.

**Durability bar: at most one turn may ever be lost**, no matter how
pathologically timed the termination; journaled content bytes are
recovered from prompt-time onward. The in-flight turn carries user
prompt + model response — real, expensive-to-recreate in-task-flow
state — so the raw `(prompt, response)` bytes are protected by a
durable append journal written *before* any canonical write.

**No signal/interrupt handlers as a flush mechanism.** A teardown
handler doing real work is a footgun (partial flush, re-entrancy,
races, can itself be SIGKILL'd) and may produce a *more* confused
post-termination state. Recovery is entirely a **cold-start
reconciliation** — no shutdown event to catch, no LLM in the loop, no
user acknowledgement; the on-disk state alone determines the outcome
deterministically. The worktree is the source of truth; git is a
recovery-point index over it (see the dual-repo model below).

##### (1) Detection observables

Recovery reads the following on-disk signals and classifies into exactly
one cell (§4). No signal is trusted transitively; each is read against
the correct repo (daily vs primary — see §5).

- **Journal-as-turn-signal.** A **non-empty** `turn-journal.jsonl`
  (§2.1) *is* the in-flight-turn signal — there is no separate
  `op=turn` marker file. The journal's first record carries the turn id
  (`store.JournalOwner`); the first append's existing fsync makes the
  signal durable for free. (`store.WriteMarker` refuses `op=turn`; a
  legacy `op=turn` marker file from pre-#94 code is still read and
  reconciled.)
- **Batch marker file** `op-in-progress.json` (`$PERSONANT_HOME`,
  gitignored, survives `reset --hard`): batch ops **only** —
  `{op: archival|sleep|recovery|barrier|rebaseline, day?, orig?}`.
  Written before a batch operation's canonical mutations, cleared as
  its last step. Its presence is the batch-in-flight signal;
  `op=barrier` carries the target `day`.
- **Watermark = daily HEAD.** `derived-watermark` (gitignored) holds
  the **daily** git HEAD hash the derived artifacts were last built
  from. `daily HEAD == watermark ∧ daily WT clean ⇒ derived fresh` —
  the O(1) clean-open certificate.
- **HEADTURN / HEADDAY trailers.** Every daily commit carries
  `Personant-Turn: <id>` (read as HEADTURN); every primary commit
  carries `Personant-Day: <N>` (read as HEADDAY). HEADTURN discriminates
  the torn-turn cells (4/5/6); HEADDAY drives new-day detection and the
  barrier idempotence guard. HEADDAY is ⊥ only for a bare (never-
  committed) primary.
- **DAILY ∈ {present, missing, half-created}**, evaluated **defensively
  and FIRST**, before any daily-repo read: a go-git open failure on a
  corrupt/partial `.git-daily` classifies as `half-created`, **never a
  propagated error** — a corrupt or half-nuked daily is always a
  recreate-from-worktree candidate (the daily is disposable by
  construction), so it can never wedge an open.

##### (2) Turn transaction protocol

A turn is written to the **daily** repo through a three-step port
protocol (`internal/turn`, `memops` port ops):

1. **`JournalTurn(ctx, turnID, "prompt", bytes)`** — append the prompt
   record (fsync) **before the model call**. The first append is what
   arms the in-flight signal.
2. **`JournalTurn(ctx, turnID, "response", bytes)`** — append the
   response record (fsync) **before any canonical write**. A journal-
   append failure aborts the turn *pre-canonical* (no torn state
   possible).
3. **`CommitTurn(ctx, turnID, reason)`** at turn close — commit the
   turn's **scoped** write set to daily with the `Personant-Turn`
   trailer, then **truncate the journal** (the truncate *is* the scope
   release — no separate marker clear). A `CommitTurn` failure leaves
   the journal non-empty and fails loudly into recovery.

Per-turn staging is **scoped**: `CommitTurn` stages only the turn's
recorded write set (`spine.jsonl`, touched thread dirs, the day's event
log — recorded at each adapter write site, never guessed) via
`autogit.AddPaths`, O(write-set) with no tree walk. The full `Add(".")`
sweep is the session-close/backstop `Checkpoint`'s job and a day-barrier
responsibility (§5 B1/B2), so a hand-edit is absorbed at the next FULL
sweep, not the next turn.

The per-cause re-prompt caps of §5.5 (missing-tag / empty-response
recovery) live entirely *inside* a turn, ahead of step 3; recovery
operates only on the committed/journaled result and never re-drives the
model.

##### (3) The journal

`turn-journal.jsonl` (`$PERSONANT_HOME`, flat, operational/gitignored):

- **JSONL contract:** one record per line,
  `{turn, kind: prompt|response, at, bytes}`; **fsync per append**.
- **Truncate-no-fsync on `CommitTurn`.** The truncate-to-empty carries
  **no fsync**. Proof it is safe (`store.TruncateJournal` comment +
  `TestCell5_ResurrectedTruncatedJournal`): a power-loss resurrection
  of a stale-but-truncated journal is the cell-5 redundant-journal case
  (`turn == HEADTURN`, already committed), and the next turn's first
  append fsync makes the truncate durable before any new canonical dirt
  can exist. **Converse residual:** a torn *append* (fsync not yet
  flushed on the failing turn) is bounded to that one turn's content —
  the ≤1-turn bar (§7).
- **Torn tail.** A torn final line (a partial record from a kill
  mid-append) is **skipped** by the scan, never parsed.
- **Preserve + surface, never replay.** On a torn turn the journal
  bytes are written to a recovery artifact and **surfaced** in the REPL
  banner. They are **never auto-replayed** into canonical: a turn is
  bytes *plus* non-replayable derivation (tag parse, symbol extraction,
  engagement). The user re-issues if they choose.

##### (4) The recovery state machine

Core recovery (`internal/recovery`) reads the observables and classifies
into one cell. Turn cells (4/5/6) evaluate against **daily**; cell 12
against **primary**. Barrier/archival cells **DETECT ONLY** — core
recovery returns a typed `RecoveryReport.Pending` with a nil error and
the marker left in place, and the **adapter** (`fileadapter.Reconcile`,
which owns `ArchiveThreads` + both repo handles) completes them (§6, the
F4 seam). This avoids the `recovery → adapter` import cycle.

| Cell | Trigger | Action |
|---|---|---|
| 1 clean | no marker, DAILY present + WT clean vs daily HEAD, daily HEAD == watermark, journal empty | no-op — **O(1)** open |
| 2 derived-stale | no marker, clean, derived stale | rebuild derived; stamp watermark = daily HEAD |
| 3 hand-edit | markerless-**dirty** vs daily HEAD, watermark present | **NEVER reset** — a legitimate hand-edit; absorbed at the next FULL sweep (§2) |
| 4 torn turn | journal non-empty, dirty, `HEADTURN ≠ turn` | preserve+surface journal → `ResetHard` **daily** (≤1 turn) → sweep untracked canonical-namespace debris (excluding `logs/`) → re-append log bytes → truncate journal |
| 5 turn committed | journal non-empty, clean, `HEADTURN == turn` | redundant journal (turn already committed) — truncate, zero loss |
| 6 turn no-writes | journal non-empty, clean, `HEADTURN ≠ turn` | nothing landed structurally — preserve+surface journal, truncate |
| 7 (composite) | cell 4 coinciding with an unstamped ARCH entry | cell 4, then the cell-9 stamp pass — no distinct constant |
| 8 archival | `op=archival` marker | **DETECT ONLY** → typed `Pending`; adapter roll-forward **completes** the batch (no reset, no re-archive-as-drift); marker left in place until completion (supersedes the R2 reset — see §7) |
| 9 stamp-repair | unstamped ARCH entry (any cell, post-normalization) | `locateDeletionCommit` (child-of-`ParentCommitHash` tree test), stamp; unlocatable ⇒ `recovery.unrepairable`, entry stays **refused, never guessed** |
| 10 sleep | `op=sleep` marker | clear marker; substrate self-heals (gc/rebuild idempotent) |
| 11 recovery-reentry | `op=recovery` (crash during recovery) | re-run under the original marker; every phase idempotent → converges |
| 12 legacy-adopt | dirty, **no watermark ever** | greenfield/legacy: verify-gated adopt-commit to **primary** (`Personant-Day` trailer), stamp-repair, full rebuild, then morning-init |

Dual-repo op rows (§5):

| Op | Action |
|---|---|
| `op=barrier` | **DETECT ONLY** → typed `Pending`; adapter runs the idempotent §5 B-recovery routine. The DAILY observable is **irrelevant** to classification — the barrier owns daily's lifecycle regardless of its state. |
| `op=rebaseline` | `rm -rf .git-daily` + morning-init **unconditionally**; no primary touch (§6.2 knob). |
| morning-init rows (no marker, daily missing/half-created) | greenfield (no watermark) → [adopt if primary-dirty] + morning-init + rebuild; benign morning (watermark) → morning-init, rebuild iff derived stale; interrupted-init/corrupt-dir → `rm -rf`; morning-init. **Daily-missing is NORMAL, never a data-loss signature.** |

Orthogonal heals run on **every** path, before classification: additive
event-log tail-heal (last byte ≠ `\n` ⇒ append one `\n`, never truncate)
and `tmp/` sweep.

The **Pending seam** is a typed opaque "finish this" request
(`PendingCompletion{Kind: PendingBarrier|PendingArchival, Day}`) plus
`ScopedRestored []string` (the §5 quarantine-and-proceed paths; empty on
the normal path). The chat/cmd caller is unchanged — it calls the port's
`Reconcile` and reads one report.

**Quarantine-never-delete.** Recovery never deletes canonical bytes it
cannot re-derive. The torn-turn debris sweep and the §5 persistent-
verify-failure posture both **quarantine byte-exact under
`recovery/quarantine/<stamp>/`** before any removal or scoped restore
(the **quarantine-and-proceed** posture, §5), and record the moved paths
in `ScopedRestored`. An offending path absent from the restore source is
quarantined and removed (nothing valid to restore to).

##### (5) The dual-repo architecture and the day barrier

**THE WORKTREE IS THE TRUTH; the daily DB is disposable scaffolding.**
No canonical byte lives *only* in git — every canonical byte is a file
under `$PERSONANT_HOME`, and git is a recovery-point index over those
files. Two git DBs track the one worktree at two cadences:

| DB | git-dir | cadence | role |
|---|---|---|---|
| **daily** | `.git-daily/` | per-turn | today's microscope — fine-grained recovery points; **nuked + reborn each day barrier**; never career history |
| **primary** | `.git/` | per-day | career history — one day-grain commit per day + permanent archival anchors (~365 commits/year); irreplaceable |

Invariants (the design is correct iff these hold): **INV-1** archival
and the barrier NEVER reset the worktree (their recovery is *re-drive /
repair*); only op=turn recovery resets, and only ever to **daily** HEAD
(≤1 turn). **INV-2** the day-commit is guarded by *primary HEAD is a
day-commit-shape commit AND its `Personant-Day == N`* (not merely
"trailered") — a re-drive after a crash never mints a second day-commit.
**INV-3** `.git-daily` is nuked only after the worktree is captured to
primary. **INV-4** the watermark tracks **daily** HEAD, re-stamped by
derived rebuild and by barrier/morning-init re-init. **INV-5** primary
is **barrier-exclusive** with one recovery-repair exemption; the
exhaustive primary-writer set is: barrier B1 archival, barrier B2
day-commit, cell-9 stamp-repair, cell-12 adopt, F1 roll-forward
completion — **nothing else** (Checkpoint now targets daily). **INV-6**
both `.git/` and `.git-daily/` are in the managed `.gitignore` block, so
neither repo sees the other's git-dir as untracked debris.

**Day barrier B0–B6** (a normal operation fired on new-day detection —
single-clock day index exceeds HEADDAY — checked at session open after
Reconcile and before each turn; homed in the adapter under an
`op=barrier {day: N}` marker):

- **B0** write the `op=barrier` marker (owns the whole sequence,
  including B1 — B1 opens no nested `op=archival` scope).
- **B1** *archival-if-pressure* against **primary** via internal
  `archiveBatch` (no nested marker). It **records batch membership
  durably before any deletion** (archive-index entries with empty
  `CommitHash` appended *before* the first `RemoveAll` — the F1
  membership-before-deletion record), and its capture commit `Add(".")`s
  the full worktree into primary (making B4's nuke safe, INV-3). Skipped
  under the low-water mark; then B2 is the sole worktree-capture.
- **B2** *day-commit* worktree → **primary**, `Personant-Day: N`, staging
  **everything including overnight hand-edits**. The `CheckSpineIntegrity`
  gate runs on the **STAGED tree BEFORE the mint** (post-review
  amendment 1), so a spine-broken day-commit can never land — making
  "primary HEAD is always spine-good" universal. Guarded by INV-2
  (idempotent re-drive); minted with `AllowEmptyCommits` so an empty day
  still produces exactly one trailered day-commit (HEADDAY always
  defined; no tag path anywhere).
- **B3** *rebuild derived* from the now-committed worktree (absorbs
  overnight hand-edits and B1 removals), re-stamping the watermark
  against the pre-nuke daily HEAD.
- **B4** `rm -rf .git-daily` (idempotent).
- **B5** *re-init daily* + baseline commit of the current worktree →
  baseline hash H_d.
- **B6** stamp watermark = H_d (INV-4); **assert `CheckDerivedFresh`**;
  clear marker.

Only B1/B2 mutate primary; B4/B5 are pure daily lifecycle; B3/B6 touch
derived + watermark. The canonical worktree is untouched by B2–B6 (only
*reduced*, never lost, by B1's archival removals — bytes preserved in
primary).

**Lifecycle tags (one derivation site).** After the B2 day-commit lands
(inside the `op=barrier` scope, immediately before `ClearMarker`), the
barrier derives autonomic **git tags on primary** marking thread/project
life events. It parses the sealed day's §2.8 event log (the reuse of the
tolerant `eventlog` reader) and maps: `thread.created` →
`thread/<id>/created`, `retire.complete` → `thread/<id>/retired`,
`project.created` → `project/<id>/created` — all targeting **that day's
day-commit** — and `archive.archived` → `thread/<id>/archived`, targeting
the **archival commit** recorded in the thread's archive-index entry.
Ambiguous project *open/close/switch* semantics are deliberately NOT
mapped (still deferred, ARCHITECTURE.md). The ref namespace is
`<kind>/<id>/<event>_<stamp>`, e.g.
`thread/thr_42/created_2026-05-08T03-12-00Z`; the stamp is the event's own
UTC-normalized instant in RFC3339 with time colons rewritten to hyphens
(git refs forbid `:`), so names are timestamp-unique by construction and
sort **lexically = chronologically** across DST boundaries and mixed
original offsets (`autogit.TagStamp` / `ParseTagStamp` are the single
source; nothing else hand-formats). Derivation is **idempotent** —
skip-if-exists by exact name (`ErrTagExists` tolerated) — so a barrier
completion re-drive re-derives safely, and **best-effort**: a
read/parse/tag fault logs `barrier.tag-error` and is skipped, never
failing the seal. The tag→log→pathspec workflow it enables: locate a life
event's tag, then `git show <tag>:spine.jsonl` (or any path) reads the
substrate exactly as it stood at that commit — the tag narrows the
pathspec lookup to O(1) without a bespoke temporal index. *Day-scoping
note:* the barrier for day N fires the next day, so created/retired/project
lines are scoped to `DayIndexOf(ts) == N` while `archived` (emitted by the
barrier itself) is not; life events on earlier **idle gap-days** (< N) go
untagged — the barrier honestly derives only from the sealed day's log.

**Roll-forward completion (crash inside the barrier).** Every barrier
crash window recovers by **roll-forward, never a reset** (INV-1). The
`op=barrier` marker (with its `day: N`) is the idempotent re-entry
token; every sub-action is guarded/idempotent, so re-entry converges.
The B1 sub-window completion consumes the durable batch-membership
worklist: ensure-removed → ensure-spine-removed → regen → (deletion
commit iff `locateDeletionCommit` returns not-found, else stamp the
located one) → stamp. The `postDeletionCommit.preStamp` window is the
exact join where roll-forward degenerates into cell-9 stamp-repair —
and it must **not** discriminate on worktree-clean-vs-HEAD (an overnight
hand-edit would be misread as uncommitted removals and mint a duplicate
deletion commit). The completion routine also **re-runs the B1 pressure
drain** (post-review amendment 2) so a barrier killed at
`barrier.preArchival` does not silently defer the day's archival.

**Day-commit-shape guard, at-most-once per day.** New-day *detection*
reads HEADDAY off any trailered primary commit; the INV-2 *idempotence
guard* fires only on a `DayCommitMessage`-shape HEAD for the target day.
A trailered-but-non-day-shape HEAD (a roll-forward stamp, an adopt)
advances detection but does NOT satisfy the guard, so the real
day-commit is still minted — proven fork-free across the morning-N+1
completion and adopt-then-barrier kill timings. The poll seals only
**completed** days (strictly before the current clock day), so each day
is sealed exactly once (gap-day at-most-once).

**Quarantine-and-proceed (persistent verify failure).** If B2's spine
gate or B6's derived assert fails and still fails across one idempotent
re-drive: (1) identify the offending paths; (2) quarantine their bytes
byte-exact under `recovery/quarantine/<stamp>/`; (3) scoped-restore
exactly those paths from primary HEAD (a path-scoped checkout — **never**
`reset --hard`, never a whole-worktree restore; derived paths, absent
from primary, are regenerated-and-verified instead — amendment 5); an
offending path absent from HEAD is quarantined and **removed**; (4)
re-run the failing step once; (5) still failing ⇒ **refuse-to-open**,
marker LEFT IN PLACE (the retry token), report names the quarantine dir.
Safe by INV-1 (scoped, quarantined-first) and by "primary HEAD is always
spine-good" (every primary writer carries the pre-mint gate).

**Morning-init** (`git init .git-daily` + baseline-commit the worktree +
stamp watermark = baseline hash) is exactly B5+B6 in isolation, reused
as the recovery/first-open primitive so no code path ever observes a
daily-less substrate. It is **O(active working-set bytes)**, independent
of career-history length (archival caps the working set) — a 5-year-old
home morning-inits in the same time as a 5-day-old one.

**Mid-day re-baseline knob** (§6.2), **default-off**: nuke + recreate
daily at a commit boundary when daily loose objects cross a threshold,
under `op=rebaseline` (never touches primary, never advances the day).
Recovery routes an `op=rebaseline` crash straight to morning-init. In
v0.1 the recovery-side handling and marker plumbing are present; the
mid-day *arming* trigger is not yet wired (see §6.2 carry-forward).

##### (6) Archival: barrier-only + roll-forward completion

Deep-cold archival (§3.8) is re-sequenced to fire **only at the barrier
(B1)** so primary stays barrier-exclusive (INV-5) — a **behavioral
change** from the R3-shipped mid-turn/between-turn firing (public
`ArchiveThreads` still exists and stays specified for standalone use,
but the drain is barrier-homed). Two properties make crash-completion
deterministic:

- **Membership-before-deletion.** Archive-index entries are appended
  (with `ParentCommitHash`/`TreeHash`/`OriginalPath`/spine snapshot,
  empty `CommitHash`) *before* the first `os.RemoveAll`, so a crash
  mid-batch leaves a durable worklist and no half-applied deletion is
  ever ambiguous.
- **`locateDeletionCommit`** resolves an already-landed deletion commit
  by a child-of-`ParentCommitHash` **tree test** (robust to overnight
  hand-edit dirt), so roll-forward stamps the existing commit rather
  than minting a duplicate.

Standalone archival (cell 8) is likewise **detect-then-complete**:
recovery types `Pending` on marker presence alone and the adapter runs
the same roll-forward tail. The daily-absorb rationale: turn/structural
commits land in daily and are folded into the day at the barrier, so
archival (primary) never races a turn.

##### (7) Honest-coverage tiers

- **SIGKILL — full guarantee.** ≤1 turn structural loss under any kill
  timing; journaled content bytes recovered from prompt-time onward.
  Every registered crashpoint (§9) has an asserting scenario.
- **Daily-DB loss — never content loss.** The daily DB is disposable by
  construction: nuked, lost, corrupted, or absent, recovery recreates
  it from the worktree. Daily-missing is never a data-loss signature.
- **Primary-DB loss — unrecoverable.** A missing/corrupt **primary** is
  **refuse-to-open + surface**, never auto-recreated (career history is
  irreplaceable).
- **Power-loss — qualified.** The ≤1-turn bar holds under fsync
  semantics, with two named residuals: (a) the **truncate-converse** —
  a truncate-no-fsync journal resurrection is the benign cell-5
  redundant case (§3); (b) a **journal-write-failure** aborts the turn
  pre-canonical, losing that one in-flight turn's content (still ≤1).
- **Intra-day derived staleness — STATED design behavior.** The turn
  path **never stamps the watermark**; the **barrier owns freshness**
  (B3 rebuild + B6 assert). Between barriers, derived tracks daily
  per-turn state via the watermark; a markerless hand-edit stays
  deferred (cell 3) until the next full sweep. This is intentional, not
  a gap (see §6.2 carry-forward for the revisit question).

##### (8) Greenfield / legacy compatibility

- **Greenfield** (no watermark, no daily): cell-12 verify-gated
  adopt-commit to primary (`Personant-Day: <current day>` — the sole
  bootstrap carve-out), stamp-repair, full rebuild, morning-init. A
  legacy `op=turn` marker file is honored and cleared.
- **`store.Init` births `.git-daily` only alongside a fresh primary**
  (true greenfield); on an existing home a missing daily is the
  legacy-upgrade shape and stays absent through Init, so cell-12's
  adopt gate and the benign-morning stamp-only path discriminate
  correctly (amendment 3).
- **Arbitration rulings (both closed):** legacy unstamped archive cells
  take the **general adopt-forward repair path** (no special case) —
  population **expected-empty** under release sequencing; the journal is
  **JSONL**, flat top-level, operational/gitignored.
- **R2→R3b watermark migration** is codeless: the stale R2 primary-hash
  watermark never equals a daily HEAD, so the first R3b open reads it as
  stale via the benign-morning path and rebuilds once.

##### (9) Crash-injection methodology (R4)

- **Crashpoint seam** (`internal/crashpoint`): named deterministic
  hooks (`crashpoint.At(name)`), test-armed, **production no-op**,
  registered at every point the matrix kills.
- **Mechanical coverage gate** (`TestCrashPointCoverageGate`): the suite
  **fails** if any registered crashpoint lacks a scenario in the
  greppable `crashScenarioCoverage` registry.
- **`RestartWithCrash`** (`internal/scenarios/restart.go`): sibling of
  `RestartSession` — arms a crashpoint, drives the turn/op, discards
  in-memory state, and drives `Reconcile` against the on-disk result.
- **The matrix** kills at: every W-TURN ordering
  (`turn.postJournalPreCanonical`, `turn.betweenCanonicalRenames`,
  `fileadapter.CommitTurn.*`, `store.WriteFileAtomic.{preRename,tornWrite}`,
  `fileadapter.JournalTurn.preAppend`); every barrier step
  (`barrier.preArchival` … `barrier.postWatermark.preMarkerClear`);
  every B1 archival sub-window (`archiveBatch.postCapture.preMembership`
  … `archiveBatch.postStamp`); standalone archival
  (`archival.{preMarker,postMarker}`); recovery internals
  (`recovery.{adopt.preCommit,stampRepair.preCommit,resetDone.preSweep,
  logsRestored.preCleanup,done.preMarkerClear}`); and morning-init
  (`morninginit.{preBaseline,postBaseline.preWatermark}`). Every
  fixture asserts the full invariant suite plus the strengthened
  `VerifyThreadMetaMatchesSpine`, ≤1-turn-loss vs a pre-crash oracle,
  exact journal-byte recovery, no-reset on barrier/archival paths
  (`RevertedPaths` empty), exactly-one day-commit, and idempotent second
  `Reconcile`. The matrix is **unit-grade** (synthetic homes, inside the
  30-min checkpoint budget); a 2-day barrier-crossing sim rung
  (`TestSimBarrier2Day`) runs in the default suite.

### 4.6 Math rendering (deferred; non-optional)

**Requirement (recorded 2026-08-01).** As a research assistant, personant
must eventually render mathematics in a conveniently visible form —
inline images in image-capable terminals, not raw LaTeX or lossy unicode
approximation. Deferred, but **non-optional**: it shapes several nearer
decisions, recorded here so they are honored cheaply now rather than
retrofitted.

**Ecosystem finding (empirical, decides the shape).** The well-trodden
third-party path for LaTeX→terminal-image rendering (renderer, kitty /
OSC 1337 / sixel emission, Windows parity) exists in **Rust** and not in
Go — measured directly during the `laterm` project's development, where
Go was explored first and rejected on this ground. Consequently the
rendering and protocol-encoding work stays on the Rust side in every
option below; personant does not grow Go rendering or sixel-encoding
dependencies.

**Delivery ladder** (each rung independently shippable):

1. **Sidecar** — personant pushes committed turns to `laterm`'s local
   socket (the hook shape laterm already ingests from Claude Code).
   Math renders in a second window; no personant architecture change.
2. **Inline, Rust renders / Go splices** — a laterm-derived tool takes
   LaTeX + a target protocol and emits finished protocol bytes;
   `internal/term` splices them as an opaque committed block in the
   `(tty-only, scrollback)` cell, with LaTeX source text on the piped
   path. Cost on our side: term's cursor/erase bookkeeping and the
   `screentest` model must learn N-row committed blocks.
3. **Rust front end** — the far-horizon front/back split (two
   executables; Go substrate, Rust terminal front end) unifying laterm's
   renderer with the `internal/term` architecture, which is
   language-portable by design.

**Protocol principle, binding now:** any future backend↔frontend
boundary carries *semantic* content — markdown with LaTeX source — never
pre-rendered bytes. Rendering decisions (protocol choice, styling,
wrapping) belong wholly to the front end. Corollary: nothing on the Go
side may bake rendered output into stored or transmitted turn content;
memory and the piped path always see source text.

**Model-output posture (open):** rendering requires the model to emit
LaTeX; the prompt/template stance (LaTeX vs unicode math in responses)
is decided when the first rung ships.

---

## 5. Model interaction

### 5.1 Topic tag emission format

The model emits a topic tag at the **start of every response** identifying
which threads the current turn engages and which symbols attach to that
engagement. The runtime parses the tag before any other response
processing.

#### 5.1.1 Format

Single-line, asterisk-bracketed, parsed deterministically:

```
*topic: <thread-list> [<anchor-list>]*
```

Where:

- `<thread-list>` is a comma-separated list of `thr_<n>` identifiers,
  optionally including the literal `*new-topic*` to indicate a new
  thread should be created.
- `<anchor-list>` is a comma-separated list of normalized anchor symbols
  (per §2.7.2) attached to this turn's engagement.

Examples:

```
*topic: thr_42 [trefoil, unknot, body-topology]*
*topic: thr_42, thr_88 [trefoil, neutrino, helical-screw]*
*topic: *new-topic* [neutrino, oscillation]*
```

The topic tag is **advisory** input to the runtime, not a directive: it
declares which threads the turn engages, but the runtime decides
*ownership* (§3.2). Owner selection from the thread-list:
- **multi-existing** (`[thr_N, thr_M, ...]`): the first-listed existing
  thread owns; the rest are engaged-but-not-owner. The coalesce buffer
  (§3.0.4) is a map and does not preserve tag order, so v0.1 uses the
  **lowest `thr_N` id** among referenced existing threads as the
  deterministic stand-in for "first-listed". The requirement is
  determinism + single ownership, not literal type order.
- **pure `*new-topic*`** (no existing thread referenced): the new thread
  owns the turn (genesis turn — the only available owner).
- **mixed** (`[thr_N, *new-topic*]`): the existing `thr_N` owns; the new
  thread is created **metadata-only** (`turn_count = 0`, no excerpt, with
  its `description` set per §2.3).

The anchor-list is the model's **advisory per-turn symbol contribution**
(`source = model`), folded into `history_symbols` — *not* a cardinality
contract. There is no anchor count to satisfy: **0 anchors is legal** (a
vague start), and an over-`AnchorProjectionMax` emission **folds** — the
deterministic projection (§2.7.4) keeps the strongest `AnchorProjectionMax`
and files the rest; the turn never aborts on count. The headline anchor
count is owned by the runtime's projection, not the emission. The
`≤ AnchorProjectionMax` bound is a runtime self-assertion (the projection
enforces it), surfaced by `personant verify` as a substrate-bug check, not
a model contract. **Deleted:** `ErrAnchorCardinalityViolation`,
`MinAnchorsPerThread`, the 4-floor, and the over-8 abort. **Also deleted
(D6):** `ErrFileEditWithoutTopicTag` — a file edit with no owning topic
tag no longer fails the turn; the runtime recovers per §3.3 (single
re-prompt with the `TopicTagReminder` appended, then deterministic
owner-default with a `thread.tag-defaulted` forensic line). There are no
remaining topic-tag fail-loud paths: a missing or malformed tag is at
worst a re-prompt plus a defaulted binding, never an abort.

The single-line form is deliberate: no multi-line state for the parser
to track, no envelope syntax that varies across providers, no JSON
escaping. The asterisks are a low-collision sentinel that survives
markdown rendering (rendering as italic text doesn't break parsing —
the runtime strips the tag from the user-visible output before
display).

#### 5.1.2 Parser

Regex (Go `regexp` syntax):

```
^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$
```

Group 1: thread list (comma-split, trim whitespace, validate against
`thr_\d+|\*new-topic\*`).
Group 2: anchor list (comma-split, trim whitespace, run §2.7.2
normalization).

If the response contains multiple matches, take the first; warn-log the
rest. An **empty thread list** → not a valid topic tag (the tag's job is
thread binding); warn-log and continue without engagement update for this
delta. An **empty anchor list is valid** — a 0-anchor vague-start emission
(§2.7.4); anchors are advisory, so an anchor list that normalizes to empty
does not invalidate an otherwise well-formed tag.

**Bare new-topic alias (deterministic-tier absorption).** The parser
additionally accepts a line of the exact shape

```
*new-topic* [<anchor-list>]
```

as equivalent to `*topic: *new-topic* [<anchor-list>]*`. Evidence: the
2026-07-15 live elicitation probe (round 3, 600 calls total across the
probe series; miss signature (b)) caught the model intermittently
"unwrapping" the nested-asterisk new-topic syntax into exactly this form
on work-switch turns. Absorbing a demonstrably-confusable wire shape at
the deterministic tier is cheaper than paying recurring prompt tokens to
forbid it (minimize-infrastructural-prompts). The alias is **strict, not a
general loosening**: line-anchored (regex
`^\s*\*new-topic\*\s*\[([^\]]*)\]\s*$`), identical anchor-list grammar
(empty-in-brackets valid), and a bare `*new-topic*` with **no** `[...]`
bracket pair remains invalid (forensically classified `bare-new-topic` by
the near-miss taxonomy). The alias participates in the same
first-valid-match rule and extras count, in the bounded preamble scan
(so the §5.5 fetch timing, the D6 missing-tag trigger, and the
stream-filter suppression all accept it), and in the stream filter's
Close-time trailing-line fallback.

**Unclosed inner literal (deterministic-tier absorption, third
nested-asterisk confusion variant).** Within an otherwise-valid
`*topic: … [ … ]*` wrapper, the thread-list token `*new-topic` — leading
asterisk present, closing asterisk dropped — is accepted as the new-topic
sentinel and normalized to the canonical closed form. Evidence: the
2026-07-16 1d live-inference run, where **364 of the 380 captured tag
misses** were exactly `*topic: *new-topic [anchors]*` (classified
`bad-thread-list`); replay of the capture directory recovers 363/380 with
this tolerance, leaving ~16 true omissions. This is the third confusion
variant of the nested-asterisk syntax (after the markdown mangle and the
unwrapped bare alias above), absorbed at the deterministic tier per
minimize-infrastructural-prompts. The tolerance is **strict**: thread-list
position only, token-exact with a single leading asterisk (`new-topic`
and `**new-topic` remain invalid), and a mixed thread list
(`thr_3, *new-topic`) follows the same per-entry rule as the strict form.

#### 5.1.3 Prompt template

The system prompt instructs the model to emit a topic tag at response
start. The template lives in `internal/prompt/template.go` as a compiled
constant (`TopicTagDirective`, exposed with a stable identifier). Runtime
hot-reload for empirical tuning is deferred (future work) — v0.1 requires
a rebuild to change the template.

The directive carries an **ambiguity clause** (evidence-directed, probe
round 3 signature (a): clarify-question-without-tag on uncertain routing):
the tag is required even when the response is a clarifying question or the
thread routing is uncertain — tag the best-guess thread, or `*new-topic*`
if the work is genuinely new; the tag is a routing signal, not a
commitment (the next turn can correct it). Kept to two sentences —
directive tokens are paid on every turn.

`TopicTagReminder` (same file) is the terse system-side reminder appended
to the system prompt for the §3.3 missing-tag re-prompt: it names the
discard reason, restates the tag form, and demands the tag as the first
line — even for a clarifying question. `EmptyResponseReminder` (same
file) is the counterpart for the §3.3 empty-response re-prompt: it names
the discard reason (no visible text), restates the tag form, and asks for
brief hidden reasoning. Once appended, each reminder survives any later
same-turn recomposition (e.g. a subsequent §5.5 fetch re-prompt).

### 5.2 Curator prompt for retirement summary

The curator LLM is invoked at closure (§3.5) to draft the spine
`summary` from the thread's accumulated body. The prompt instructs the
model to produce a 100–150 char gist faithful to the thread's
operational content.

### 5.3 Dissector prompt for fallback clustering

The dissector LLM is invoked under budget pressure (§3.6) to cluster
the oldest in-window content into proposed retirement groups. The
prompt presents the candidate content and asks for a small number of
clusters with brief rationale.

### 5.4 Recognition-context construction

The system prompt assembles the spine display lines, active-thread
bodies, and project conventions into the layered working set rendered
by §3.1.

### 5.5 Mid-turn thread fetch (system-injected)

When a topic tag references one or more `thr_<n>` not currently in
Layer B (i.e., not in the model's working window), the runtime fetches
those threads' contents and injects them into context **before the
model generates its response body**. This is system-injected; the model
does not invoke a tool to request fetch.

Mechanism:

1. Model emits topic tag at response start.
2. Runtime parses tag (per §5.1.2) before consuming any response body.
3. For each `thr_<n>` in the tag not currently in Layer B:
   - Read `threads/thr_<n>/` (frontmatter from `thread.md`, body assembled from the recency-windowed `turns/` excerpts; see §2.3).
   - Truncate to budget allowance (subject to §3.1 layer caps).
   - Inject as a `thread.fetched` context delta (per §3.0).
4. Re-prompt the model with the augmented context; the model's actual
   response body is generated against that augmented context.

**Rationale:** mechanical arbitration is straightforward and does the
job. Reserving tool-call overhead for things the model genuinely needs
to *decide* (workspace reads, web fetch, model.consult) keeps the
model's decision-making narrow. Topic tag is the request; system
injection is the fulfillment.

**Combined mid-turn re-prompt bound (with §3.3 recovery).**
Re-prompts are capped at **1 per cause**, and there are exactly three
causes — missing-thread (this section), missing-tag (§3.3), and
empty-response (§3.3) — so a turn issues at most 3 re-prompts (≤ 4 model
streams). One per cause because the causes are independent failure modes
with independent interventions (context augmentation vs. two distinct
protocol reminders); several firing in one turn requires as many distinct
model failures, so the worst case is bounded and rare. Never two for the
same cause: a repeat would re-run an intervention that just demonstrably
failed, so the second miss falls through to the cause's deterministic
close-time fallback (LRU pickup here; owner-default for §3.3 missing-tag;
accept-the-empty for §3.3 empty-response). The empty-response cause
subsumes missing-tag for a persistent-empty response (§3.3): the two
reminders never both fire against emptiness — with one pathological
exception, an all-whitespace response longer than the bounded preamble
scan resolves as tag-less rather than empty, so it fires the missing-tag
reminder instead of the empty-response one. Latency cost on a turn that
triggers a fetch is up to 2× the no-fetch case (one aborted stream + one
full stream); turns that trigger no cause pay nothing.
Speculative pre-fetch is rejected as an explicit non-goal (every
loaded thread is loaded because the model said it was needed; see
ARCHITECTURE.md "No speculative prefetch").

---

## 6. Tool surface and permissions

See ARCHITECTURE.md §"Tool surface (bounded, role-shaped)" for the design rationale; this section specifies the contract.

### 6.1 External tool surface

The LLM's external tool inventory is bounded at thirteen tools, split
between read/think and draft/mutate (**AMENDED 2026-08-05**: twelve → thirteen,
`web.wikipedia`, §6.1.5). *Internal* tools (operations on personant's own
state — `personant search`, mid-turn thread fetch, `/topic`, `/done`, etc.)
are out of scope for this section; see §4 (user surface) and §5.5 (model
invocation protocol).

#### 6.1.1 Read/think tools (7)

| Tool | Signature (informal) | Purpose |
|---|---|---|
| `fs.read` | `(path) → bytes` | read a workspace file |
| `fs.list` | `(path) → entries[]` | list a directory |
| `fs.grep` | `(pattern, path, opts) → matches[]` | regex/literal search across a tree |
| `web.fetch` | `(url) → bytes + meta` | retrieve a URL — **BUILT** (wave 3) |
| `web.search` | `(query) → results[]` | search query → URLs — **BUILT** (wave 3) |
| `web.wikipedia` | `(q, limit?) → articles[]` | search Wikipedia → articles — **BUILT** (§6.1.5, AMENDED 2026-08-05) |
| `model.consult` | `(prompt, model_id) → response` | ask a guest model |

**Status:** the `web.*` trio is built and registered; the `fs.*` tools are
not yet. All three web tools are tier 0 / non-mutating (§6.2), and each is
described at the level of the behaviour that is load-bearing rather than
as an API listing — `web.fetch` and `web.search` below, `web.wikipedia` in
§6.1.5.

**`web.fetch`** is a plain HTTP GET → readability extraction → Markdown.
**There is no headless browser and no JavaScript execution**, which is a
scope decision, not a gap. Load-bearing properties:

- **Scheme allowlist: `http` and `https` ONLY** — see §6.2 for the ruling
  and its precise extent (it blocks `file://`, `data:`, `ftp://` and
  everything else; it deliberately does NOT block loopback or any IP
  range). Enforced on the initial URL AND on every redirect hop.
- **Bounded**: a timeout, a redirect cap, and a response size cap
  enforced WHILE READING, so an oversize body cannot balloon memory
  before the cap applies. Over the cap is a §6.5-style refusal asking for
  a narrower fetch.
- **Content-type branch**: HTML through the extraction pipeline;
  Markdown, plain text and JSON/XML passed through verbatim; binary and
  unknown types REFUSED with a reason, never fed to the parser as
  garbage.
- **Empty-shell detection.** A client-rendered SPA answers a plain GET
  with a near-empty document. When the extracted text is tiny relative to
  the document, the tool returns an explicit *"this page requires
  JavaScript rendering; content unavailable"* signal instead of the
  shell. **Returning an empty shell as if it were content is the failure
  mode this exists to prevent**: a model told the page is blank concludes
  the information does not exist and says so confidently, while a model
  told the fetch was incomplete goes and looks elsewhere. The notice
  names both possible causes (client-side rendering, or a genuinely
  text-less page), so the heuristic's false-positive case is still an
  honest message.
- **Metadata is surfaced with the content** — title, byline, published
  date, site, language. For a memory system whose whole business is
  information over a long career, "when was this written?" is not
  decoration; it is how staleness gets judged.

**`web.search`** returns ranked results (title, URL, snippet) from a
pluggable backend behind a one-method `SearchProvider` interface, so a
different engine drops in without the tool changing. Exa is the first
implementation. Two properties are load-bearing:

- **"Search failed" and "search found nothing" are distinguishable, and
  the model is told which.** A backend failure is an ERROR result stating
  that NO search happened and that this says nothing about whether
  results exist; an empty index is a SUCCESS result stating that the
  search ran and matched zero. Collapsing them — a silent empty on
  failure — teaches the model that the web has no answer, which is the
  single worst outcome this tool can produce.
- **A local query cap, per turn and per day, enforced in personant's own
  code** (§8.2.2 `[search] maxPerTurn` / `maxPerDay`; defaults 5 and
  100). A metered API plus a model in a retry loop is a real failure
  mode, and a provider dashboard reports it only after the budget is
  gone. The cap bounds ATTEMPTS, not successes — a loop against a
  *failing* backend is the one most likely to run away — and hitting it
  produces an honest, logged result telling the model to stop rather
  than retry. Scope, stated honestly: the counters live in the process,
  so the per-turn cap is exact and the per-day cap resets on restart;
  the runaway-loop case is intra-session, which is where it is exact.

#### 6.1.2 Draft/mutate tools (6)

The LLM never names a workspace path on a write call. All workspace mutations
flow through `propose_*` tools, which are ack-gated (see §6.2). The
draft-and-promote pattern keeps mechanical mutation chores out of the LLM and
narrows the surface area for LLM failure.

| Tool | Signature | Purpose |
|---|---|---|
| `fs.tmp_write` | `(name, content) → tmp_id` | write to `.personant/tmp/<name>` |
| `fs.tmp_read` | `(name) → content` | read back own draft (multi-turn iteration) |
| `fs.tmp_list` | `() → drafts[]` | list current drafts |
| `fs.propose_promote` | `(tmp_id, target_path) → ack_event` | promote tmp → workspace; ack-gated |
| `fs.propose_rename` | `(from_path, to_path) → ack_event` | rename/move workspace path; ack-gated |
| `fs.propose_delete` | `(path) → ack_event` | delete workspace path; ack-gated |

`fs.propose_*` tools are **workspace-only**. Paths under `.personant/tmp/` are
the agent's scratch space (lifecycle in §6.3); paths under `.personant/`
proper are not the agent's to mutate.

#### 6.1.3 Out of v0.1 scope (intentional exclusion)

Not "deferred to a later version" — *deliberately not in scope*, because
including them slides toward general-agent territory:

- Shell command execution, subprocess spawning
- Git operations (commit, branch, push, etc.)
- Package management, build, test
- IDE / editor integration
- Deployment, service, running-system control
- Direct filesystem writes outside the `propose_*` channel

The exclusion is at the **LLM tool inventory** layer — these operations live
in no tool the LLM can call. The deterministic runtime may perform analogous
operations *autonomically* (without LLM or user direction at the moment of
invocation) when they are required to maintain the canonical KB. The two
notable cases:

- **Autonomic git on `~/.personant/`.** The runtime owns its own home's git
  tree: `git init` on first run and `git add`/`commit` of canonical mutations
  with structured messages, all in-process via `internal/autogit` (no git
  binary required on `$PATH`). Systemic validation runs inline via
  `autogit.GitCheckFlags` bitmask policy declared on each state-changing
  operation — no git pre-commit hook. Same lifecycle status as writing to
  `spine.jsonl` itself — no user ack, no LLM involvement. See §8.3 for
  details.
- **Read-only git queries on the workspace.** The runtime issues `git
  ls-files`, `git status`, and `git diff` against the workspace tree to
  classify tracked-vs-untracked status (input to the permission tiers in
  §6.2) and to render diffs in ack prompts. The runtime never runs *mutating*
  git on a workspace tree — the user owns workspace git.

These do not enter scope by accident.

#### 6.1.4 Registry and execution loop

The inventory and the machinery that runs it are separate concerns.
`internal/tools` holds the registry: name → (`model.ToolSpec`, handler,
tier, mutates). It is substrate-free and unaware of the turn loop; a
handler is `(context, raw JSON args) → (bytes, error)`.

- **An empty registry sends no `tools` field.** `Registry.Specs()` returns
  nil when nothing is registered, and the wire encoder omits an empty
  field — the model is never invited to call what cannot be serviced.
  This is also how an OPTIONAL tool is handled: `web.search` with no
  configured backend is simply absent from the inventory, so there is no
  "configured but broken" state for the model to trip over.
- **Registration is per-session and config-driven.** The registry is
  built once at session open and immutable thereafter. `web.fetch` and
  `web.wikipedia` register unconditionally (neither needs a credential);
  `web.search` registers only when the pool holds a usable
  `type = "search"` provider.
  Everything that can go wrong with the optional half degrades to a
  warning and a smaller registry — never to a refused session.
- **Spec order is deterministic** (sorted by name). Map order would make
  otherwise-identical requests differ in their prefix, which is both
  unreproducible and a prompt-cache miss on a block that did not change.
- **`Registry.Dispatch` never fails.** Every call yields a result whose
  content is safe to return as a tool message: an unknown tool, a handler
  error, and a handler timeout each become an error result naming what
  went wrong and what to do next. The one exception is the caller's
  context ending (§4.3.3 Esc, shutdown), which is reported to the caller
  and never to the model. A provider that saw N tool calls requires N tool
  replies; dropping one wedges the conversation.
- **Handlers are bounded.** Dispatch derives a per-call deadline from the
  turn context, so a wedged tool costs a bounded wait rather than the
  session.

The execution loop lives in `internal/turn` and reuses the §5.5 / D6
re-issue machinery rather than opening a parallel request path. After a
stream completes carrying tool calls: run them in emission order
(sequentially — a deterministic result order is worth more than a shorter
multi-fetch round until a tool exists whose latency is the complaint),
append the assistant message that carried the calls followed by one tool
message per result, fire each result through the §3.0 chain as a
`tool.result` delta, and re-issue.

- A **tool-call-only response carries zero visible content**, which is
  byte-identical to the §3.3 empty-response signature. The identity-only
  `Chunk.ToolCalls` view is what separates them, so a tool round never
  burns the empty-response re-prompt.
- **Tool rounds are bounded per turn**, by a counter DISTINCT from the
  §5.5/§3.3 re-prompt caps. Those bound recovery from a protocol failure,
  where repeating a just-failed intervention is known-useless; a tool
  round is the model working, and each round carries new information.
  Folding them would let one tool-using turn consume the budget that
  exists to recover a malformed topic tag. Exhausting the tool budget is
  surfaced to the user on screen AND logged (`tool.truncated`) — never a
  silent truncation.
- **The user is never left with silence.** A turn that renders no text —
  tool calls only, a spent round budget, a persistently empty reply —
  emits one bracketed runtime notice, so a working system is never
  indistinguishable from a broken one.

**Crash stability (§4.5.8): tool calls and results are NOT journaled, and
execution is AT-LEAST-ONCE.** The turn journal holds the (prompt,
response) byte pair — the authored content that must survive a crash.
Tool results are derived: replay re-issues the prompt, which re-elicits
the calls and re-runs the tools, so journaling buys back no content, and a
third record kind would perturb the recovery state machine's vocabulary
and ordering. Tool rounds add no canonical write, so they need no recovery
point of their own. Re-running a **read-only** tool is harmless, and every
§6.1.1 tool is read-only. It would not be harmless for a mutating one.
**This decision must be revisited before any mutating tool lands** — the
trip-wire is mechanical: `tools.Registry.Register` REFUSES a tool
declaring `Mutates`, citing this decision.

#### 6.1.5 `web.wikipedia` (AMENDED 2026-08-05)

A search tool scoped to one encyclopedia: `GET
https://en.wikipedia.org/w/rest.php/v1/search/page?q=<term>&limit=<n>` →
ranked articles, each with its title, short description, a matching
excerpt and its canonical URL. Tier 0 / non-mutating, like the rest of
§6.1.1. No credential, no configuration, no pool entry — it registers on
every session, as `web.fetch` does and `web.search` does not.

**It is a SEPARATE tool, not a `web.search` backend.** Folding Wikipedia
in behind `SearchProvider` would make the encyclopedia-vs-open-web choice
*ours* — a config pin, decided once, at startup — when it is properly the
model's, decided per call. "Is an encyclopedic source what I want here?"
is a judgement about the question being asked, and the model is the only
participant holding it. The cost is one extra spec block in the request
prefix; the purchase is an actual choice.

Load-bearing properties:

- **Arguments: `q` (required) and `limit` (optional, default 5, maximum
  10).** An out-of-range `limit` is **CLAMPED SILENTLY, never refused** —
  a limit is a preference, and erroring on one spends a tool round to
  learn what the clamp already decided. A non-positive `limit` reads as
  "unspecified" (JSON omission and an explicit `0` are indistinguishable,
  and the surrounding code already reads a non-positive bound as "use the
  default").
- **The trichotomy of §6.1.1 `web.search` holds unchanged.** A transport
  or non-200 failure is an ERROR stating that NO search happened; an
  empty `pages` array is a SUCCESS stating that the search ran and matched
  nothing. The reason is the same one and it is the whole point: a model
  told the topic does not exist says so confidently.
- **A local per-turn / per-day cap, on its OWN counter**, using the same
  mechanism and the same defaults (5 / 100) as `web.search`. Wikipedia is
  unmetered, so the *cost* argument for a cap does not apply — but the
  *loop* argument does, unchanged, and an unbounded tool invites the
  retry loop. Separate counters, so a Wikipedia loop cannot silently
  spend the metered backend's allowance.
- **Excerpts are stripped of the API's markup.** The endpoint wraps
  matched terms in `<span class="searchmatch">…</span>` and escapes the
  prose around them; tags are removed and entities resolved in ONE
  tokenizer pass, because doing it in two steps in the wrong order turns
  an escaped `&lt;script&gt;` back into a tag. Each rendered field is
  clipped to the §6.5 shortlist bound, and the response read is capped.
- **The canonical URL is `https://en.wikipedia.org/wiki/<key>`, with the
  `key` concatenated and NOT re-encoded.** The API returns it already
  URL-safe (`Go_(programming_language)`); escaping it again yields a URL
  that still resolves but no longer matches what a human — or a follow-up
  `web.fetch` — would write. The tool's model-facing description states
  that `web.fetch` retrieves any result's full article.
- **English is a host swap, not a design.** The REST path, the response
  shape and the `/wiki/<key>` article form are identical on every language
  edition, so a language choice, when one is wanted, is one host string.

### 6.2 Permission tiers and accrual

Acknowledgement on every workspace mutation defeats the architectural thesis
("human acks at high-leverage moments only"). Permission tiers grade ops by
risk and reversibility; accrued grants in directive files let friction trend
toward zero as the system learns the user's working pattern.

#### 6.2.1 Three-tier default policy

| Tier | Default | Covers |
|---|---|---|
| 0 (silent) | never ack | all reads; **both `web.*` tools**; all writes inside `.personant/**`; all `fs.tmp_*` |
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

`d` and `p` write a line into `directives/prj_<n>/permissions.md`. After a
few hours of working in a project, the typical state is: tier 0 covers all
reads, tier 1 has accrued "promote anywhere in this project" for one or two
scopes, tier 2 fires only on deletes — which are rare in research work and
*should* be loud.

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

- Auto-allow `promote-new` to any path under the active project's CWD that is
  *not* already tracked by git, *and* does not match the sensitive-pattern
  list (`.env`, `secrets/**`, `*.key`, `*.pem`; configurable).
- Always require ack for `promote-overwrite` of git-tracked files. Git
  trackedness is a free risk filter — recoverable via `git diff` only if not
  yet `git add`'d.
- Never auto-promote into a directory containing `.git/HEAD` not on the
  trusted-projects list (don't accidentally write into a foreign repo).

#### 6.2.5 Always-loud (no scope expansion offered)

Some ops always offer only `[y]/[n]/[e]` — never a scope-grant option:

- `propose_delete` on git-tracked files
- Batches over a threshold (suggest 5+ files in one promotion). Single ack for
  the batch, file list shown, but no permanent grant from this ack.
- Ops flagged by an `always-confirm` pattern in the user's directive

Loud by design; their frequency is low and the regret cost on a mistaken
auto-allow is high.

#### 6.2.6 Active-project boundary as a tier axis

In addition to operation type and git-trackedness, agent-tool access to a
path *outside the active project root* (§4.5) bumps tier:

| Op | In active project | Outside active project |
|---|---|---|
| `fs.read` / `fs.list` / `fs.grep` | tier 0 (silent) | tier 1 (first-time ack with scope-grant offer) |
| `propose_promote` (new) on git-untracked | tier 0 (covered by default-shipped grant, §6.2.4) | tier 1 (always; the default-shipped grant does not extend cross-boundary) |
| `propose_promote` (overwrite) on git-tracked | tier 1 | tier 2 (always individual ack, no scope expansion offered) |
| `propose_delete` / `propose_rename` on tracked | tier 2 | tier 2 with explicit cross-boundary warning in the prompt |

Sensitive-pattern policy (§6.2.4 — `.env`, `secrets/**`, `*.key`, `*.pem`)
applies regardless of in-vs-out classification, but cross-boundary +
sensitive-match escalates to tier 2 even on a read.

**Path classification is mechanical:** after path resolution (resolving
symlinks; collapsing `..`), if the result is not a descendant of the active
project root, it is out-of-project. `..` traversal that climbs above the
project root counts as cross-boundary. Symbolic links cannot be used as a
permission bypass.

`$`/`#` user shell commands (§4.4) are not subject to this — the user is
initiating, not the agent.

#### 6.2.7 Network tools: tier 0, bounded by an allowlist

The tier table above grades filesystem operations; it assigned no tier to
`web.fetch` / `web.search`, which was a genuine gap. **The ruling: tier 0
(silent), with a SCHEME ALLOWLIST as the boundary instead of an
acknowledgement.**

An ack is worth its interruption only when the user can adjudicate the
question being asked, and *"is this URL an internal-network probe?"* is
not a question a human can answer from a prompt at typing speed. They
would learn to press `y`, which is worse than no gate at all — it
manufactures consent and trains the reflex that clears the *next* prompt
too. A mechanical boundary that always holds beats a prompt that gets
reflexively cleared.

The allowlist is **`http` and `https`, and nothing else**, applied to the
initial URL and to every redirect hop:

- It **blocks** `file://`, `data:`, `ftp://`, and every other scheme.
  This is the boundary that matters: it is the difference between "fetch
  a web page" and "read the filesystem through a tool that was not
  supposed to".
- It **deliberately does NOT block** loopback, RFC1918, link-local, or
  any other IP range. This is a user ruling, recorded verbatim: *"block
  file:// urls, do not block localhost urls (if I'm deliberately exposing
  a web server, it's fair game)."* A personal agent on the user's own
  machine reaching the user's own dev server is the intended case, not
  the threat. Do not "harden" this into SSRF filtering.

Both tools are read-only (`Mutates: false`), so they also clear the
§6.1.4 at-least-once bar without further argument.

### 6.3 `.personant/tmp/` lifecycle

- Tmps live for the active thread's life. On thread retirement, unpromoted
  tmps are surfaced at ack time (`thread retiring with N unpromoted drafts —
  promote / discard / keep?`).
- A periodic GC sweeps tmps with no thread reference older than 30 days as a
  safety net.
- The agent picks names; collisions overwrite (tmp is scratch, not canonical
  state). Deterministic code prefixes by thread to avoid cross-thread
  collisions: `tmp/thr_42/foo.md`.
- Tmp paths are *not* extracted as identifier symbols. Promoted target paths
  *are* (see §2.7.1, "identifier" category) and feed the symbol extractor on
  the promotion event.

### 6.4 Tool calls in logs and the spine

Tool-call events are first-class in `logs/YYYY-MM-DD.log`. Initial vocabulary
additions (extends §2.8 table):

| Category | Events |
|---|---|
| `tool` | `call` (a §6.1.4 dispatch is starting — `name=`, `id=` the provider's call id, `round=`. Deliberately NOT the arguments: an argument blob is multi-line and free-form, and an event line is neither), `result` (the call returned — `name= id= round= bytes=`, the byte count BEFORE the §6.5 cap), `error` (unknown tool, handler failure, or handler timeout — `name= id= round= detail=`; the model got a recoverable error result and the turn continued), `truncated` (the turn's tool-round budget was exhausted with the model still asking — `reason=round-cap rounds= pending= turn=`; paired with an on-screen notice, §6.1.4); *(vocabulary; not yet emitted)* `denied` |
| `permissions` | `redaction-fire` (§8.2.1 user-initiated redaction fired on a `#` capture — `source=` the delta source, `occurrences=`, `bytes=` how much key material was removed. **Never what matched**: §8.2.1 forbids resolved key material in any log line, so the event records that it fired and how much, and there is deliberately no field that could carry the secret); *(vocabulary; not yet emitted)* `prompt`, `grant`, `deny`, `revoke`, `accrual-update`, `suspicious-access` |

Symbol extraction over tool I/O is unchanged from §3.3: the deterministic pass
runs over tool result content the same way it runs over turn content. File
paths and URLs encountered through `fs.read`, `fs.grep`, `web.fetch` are
identifier-category symbols and feed the extractor naturally.

### 6.5 Tool output budget policy

Long file reads, search results, and fetched pages can blow the 15%
current-turn budget. Default policy:

- Hard byte cap on tool result inclusion in the model's working window
  (default 8 KB; configurable). Above the cap: head + tail with ellipsis;
  full body written to the engaged thread's file under a tool-result section.
- The cap is a **task-class** bound, not a `tool.result`-only one: a §4.4 `#`
  shell capture (`user.shell-capture`) is machine output the user merely
  asked for, and §2.8 marks it subject to this cap for that reason. It does
  **not** extend to the §3.9 `fs.read`/`fs.write`/`fs.commit` deltas, which
  are task-class but whose content is the file body written to the
  per-thread tracked-file store — truncating one would not bound a budget,
  it would corrupt a file.
- The cap is applied to the **delta**, and the bounded content is what
  builds the tool message too. Bounding only the memory copy would leave
  the wire copy unbounded, which is the budget the cap exists to protect —
  so one implementation (`boundTaskResultDeltas`) covers both, and a
  truncation always carries its honest marker.
- Agent can re-fetch with a narrower query or a byte range if it needs more.
- Pathological cases (a 50 MB file the agent asked for) reject at the tool
  layer with a message asking for a narrower fetch.

---

## 8. Bootstrap and lifecycle

### 8.1 First-run scaffolding (`personant init`)

Idempotent. Creates the directory layout from §2.1; runs `git init`;
writes seed `directives/defaults.md`, `README.md`, and template
`providers.toml` and `config.toml` files, each with a commented-out
example block.

### 8.2 Configuration sources and precedence

The runtime reads configuration from four sources, each with distinct purpose and security posture:

#### 8.2.1 Provider pool: `providers.toml`

Endpoint connectivity lives in `$PERSONANT_HOME/providers.toml`. This file is **the pool of available providers** — a connectivity catalog only. It pins nothing and expresses no preference; the choices that *draw from* the pool live in `config.toml` (§8.2.2).

Format:

```toml
[provider-name]
baseUrl    = "https://api.example.com/v1"
apiKeyFile = "api_keys/example.key"
type       = "inference"          # or "search"
api        = "openai"             # or "exa"
```

One TOML table per provider. The provider name is the lookup key used by config references and by the runtime's outbound client wiring.

**Kind and protocol.** `type` says what the endpoint IS and `api` says how to talk to it. The pool is not LLM-only: a §6.1.1 `web.search` backend is a pool entry like any other, with its own `apiKeyFile` under the same discipline.

| `type` | Meaning | `api` values |
|---|---|---|
| `inference` | LLM endpoint: chat, embeddings, `/models` | `openai` |
| `search` | `web.search` backend (§6.1.1) | `exa` |

**Both fields are REQUIRED, and they must agree.** There is deliberately no default for either: the only plausible default is "inference over openai", which is a silent guess about an endpoint the runtime is about to send a credential to, and the failure it produces (a search backend selected as a chat provider) is far more confusing than the missing field it papered over. An entry that declares neither, declares an unknown kind, or pairs a kind with a protocol it does not speak is **dropped at pool load and reported as a named `ProviderFault`** — never silently absent from the selection lists it would not have matched.

**Kind is a selection boundary, not a label.** Chat, embedding and `/models` selection run over the `inference` subset only. Without that filter the documented alphabetical default-provider fallback would hand the chat client a search API the moment one sorts ahead of the LLM endpoints.

**A provider names no models.** There is deliberately no `defaultModel` field (removed by user ruling): providers rotate their catalogues constantly, so a model id pinned on a connectivity entry is a field that goes stale — and a stale fallback fires exactly when the configured model is unavailable, which is when it is least likely to still be right. The model choice lives in `config.toml` (or `--model`), and `personant models --provider <name>` lists what an endpoint currently serves.

**API-key forms.** A provider supplies its key one of two ways:

- `apiKeyFile` — **recommended.** A path (relative to the `providers.toml` directory) to a file containing only the key. The key material lives outside `providers.toml`, so the catalog itself carries no secret.
- `apiKeyUnsafe` — an inline key string. **Discouraged**, named to make the hazard visible: it embeds a secret directly in `providers.toml`.

**Security boundary (load-bearing):**

- Because keys are referenced by file rather than embedded, `providers.toml` using `apiKeyFile` throughout is **safely scannable** — agents and tooling may read and edit it. The **secret-bearing artifacts are the `apiKeyFile` targets**, not the catalog.
- The runtime resolves a provider name → `baseUrl`/key at the moment of an outbound API call; the resolved key material is **never** included in any LLM context, log line, ack prompt, or captured shell output. This holds for a `search` provider exactly as for an inference one: the §6.1.1 search backend's key is held by the provider object and appears in no error string, event line, or tool result.
- The refuse-vs-redact policy below applies to the key files (and to any `providers.toml` that still uses `apiKeyUnsafe`), not to an `apiKeyFile`-only `providers.toml`.

**Hybrid redaction policy** (refuse vs. redact, by initiator) — for the secret-bearing artifacts (`apiKeyFile` targets; `providers.toml` only when it carries `apiKeyUnsafe`):

- **Agent-initiated reads** (`fs.read`, `fs.grep`, `fs.list` listing the file's parent dir) targeting a key file (after symlink resolution): the runtime **refuses outright** — the tool returns a deny-error to the model like `permission denied: API-key file is secret-bearing and cannot be read by the agent`. Logged as `permissions.suspicious-access`.
- **User-initiated captures** (`#`-prefixed shell commands per §4.4 whose output contains key material): the runtime **redacts before reaching context** — captured output is replaced with `[redacted: API-key content]`. The user's terminal still sees the unredacted output; only the path-into-LLM-context is filtered. Logged as `permissions.redaction-fire`.
  - *Resolved reading (front end 0.0.7):* the replacement is **per occurrence**, not whole-buffer — each run of key material inside the capture is substituted with the placeholder, and the surrounding output survives. The key bytes never reach context either way, so per-occurrence substitution is no less protective, and it keeps the rest of a `# env` or `# grep -r` capture useful. The key set is the pool's already-resolved keys; the redactor reads no key files of its own. Degenerately short "keys" (under 8 bytes) are not scanned for: such a value protects nothing, and a redactor that shreds every capture is a worse failure than one that misses a two-byte secret. The same redactor is applied to the *invocation* before it is logged, so `$ export API_KEY=…` cannot leak through the `user.shell` event line.

The split reflects intent: an agent reaching for secrets is suspicious and warrants refusal; a user `#`-grepping their own home dir for context inclusion is reasonable but accidental and just gets quietly cleaned up.

**Load-time validation.** Shape is checked before secrets: an entry whose `type`/`api` pair is missing or unroutable is faulted out before its key file is even read.

**Load-time key resolution.** Every provider's key is resolved when the pool is loaded — an `apiKeyFile` is read and trimmed. A provider whose `apiKeyFile` cannot be read (missing file, permission error) is **dropped from the pool and reported as a fault**: it does not abort the load, and the remaining providers stay usable. The runtime surfaces each fault as a startup warning. A `providers.toml` that is itself unreadable or malformed is a hard error. (Resolution and fault strings carry only the file path, never key content — see the security boundary above.)

#### 8.2.2 Configuration choices: `config.toml`

`$PERSONANT_HOME/config.toml` holds the choices that draw from the provider pool. It is canonical, hand-editable, and **not secret-bearing** — it names providers and models, never keys.

Format:

```toml
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

- `[chat] defaultModel` — the default chat provider/model.
- `[chat] showThinking` — optional display preference: stream a thinking model's reasoning deltas to the terminal, dimmed, as they arrive. **Absent → off**; a fresh install does not start showing scratch unasked. Display-only — reasoning is never journaled, never feeds §3.3 symbol extraction, and is never replayed in history — so it is deliberately outside cross-file validation: an absent or unrecognized display preference must never refuse a config. `/thinking` (§4.2) overrides it for the session.
- `[embedding] model` — the embedding provider/model. This pin is **mandatory for embedding recall** and must be explicit: the embedding model defines the vector space, and an inferred or drifting model would silently invalidate the existing embedding cache.
- `[embedding] vectorLength` — optional; for matryoshka-capable embedding models, requests this truncated dimensionality (passed as the `dimensions` parameter on the embeddings call).
- `[search] provider` — which `type = "search"` pool entry backs §6.1.1 `web.search`. Needed only to disambiguate a pool holding more than one; with exactly one, that one is used. The section is optional in full: with no search provider in the pool, `web.search` is simply not registered and the model is never offered it. **No key appears here** — the backend's `apiKeyFile` lives on its pool entry, so `config.toml` stays a choices-only, non-secret-bearing file.
- `[search] maxPerTurn` / `[search] maxPerDay` — the LOCAL query caps (§6.1.1), enforced by personant itself rather than by watching a provider dashboard. Defaults 5 and 100.
- `[recall] model` — (future, not yet built) the recall layer-3 judge model (`provider/model`). A separate reference from `[chat]` so the judge's **context ceiling can be tuned independently** of the main chat model's large window; a small dedicated window (~8K) is intentional (the judge's input is: a query + N ≤150-char candidate summaries). Default: a small-tier model (E2B). The empirical discipline: measure E2B → E4B → 26B against the embedding-only precision baseline; pick the smallest passing tier. See ARCHITECTURE.md §"Minimize infrastructural prompts" and §"Recall mechanisms" (layer-3 judgment).

Model references are `"provider/model"`, split on the **first** `/` (the model portion may itself contain slashes, e.g. `openrouter/google/gemma-4-31b-it`).

**Cross-file validation.** At bootstrap the runtime validates `config.toml` against the loaded pool:

- Each non-empty `[chat]`/`[embedding]` reference must be a well-formed `provider/model` string, and the named provider must exist in `providers.toml`. This is the full extent of the bootstrap cross-check: shape plus provider-existence.
- Neither model id is statically checked against the pool: a provider names no models at all (§8.2.1), and a single provider legitimately serves both a chat model and a distinct embedding model. The embedding model still matters (it defines the vector space, so changing it invalidates the persisted embedding cache), but that is a lifecycle concern, not a bootstrap cross-file check.
- The chat model IS verified at session open against the resolved provider's `/models` — the one provider actually being used, never the whole pool. An unreachable or model-less `/models` degrades to a warning and an unverified model; a `/models` list that does not contain the configured model is a hard startup error naming what the provider does offer, because the alternative is a 404 at the first turn.
- **`[search]` is deliberately unvalidated.** Absent, partial, or naming a backend this binary does not know must never refuse a config: the section gates ONE optional tool, and the correct handling of every bad value is to leave that tool unregistered (with a warning), not to block the session. This validator's failure mode is total — an over-strict check here has blocked a launch before.

Any cross-file validation failure is fatal at bootstrap. (A faulted provider per §8.2.1 is *not* itself fatal — but a `config.toml` reference to a provider that faulted out of the pool fails this check, since it is no longer in the pool.)

**Precedence** (chat model resolution): CLI flag > `config.toml` `[chat]`. There is no third fallback — see §8.2.1 on why a provider names no models. A session with neither refuses to open, naming both fixes.

#### 8.2.3 Environment variables

`PERSONANT_HOME` overrides the home directory (resolved in §2.1's `$PERSONANT_HOME`).

#### 8.2.4 Directive precedence

Detailed in §2.6. Briefly: `directives/defaults.md` < `directives/user.md` < `directives/prj_<n>/project.md`. The runtime walks the precedence chain at parameter-read time and returns the first match.

### 8.3 Substrate validation in autonomic git operations

Personant's autonomic git operations on `$PERSONANT_HOME/.git/` run
in-process via the `go-git` library, routed through a domain-specific
wrapper (`internal/autogit`) that applies systemic validation checks
before and after each state-changing operation. Checks are declared as
a bitmask on each call (`PreFlags`, `PostFlags`). The check vocabulary
is small and bounded; 64 flag slots cover any plausible growth.

Built-in systemic checks:

- `CheckDerivedFresh` — wraps `personant index check`; verifies
  `symbols.jsonl` and per-project `digest.json` are up to date with
  canonical sources.
- `CheckSpineIntegrity` — wraps `personant verify`; full structural
  validation.

**Systemic vs specific validation.** The `GitCheckFlags` bitmask is
for *systemic* validation — substrate-wide consistency checks
parameterized only by `paths`. Operation-tied *specific* verifications
(e.g., verifying a particular archive entry resolves after a recovery
checkout) are not expressed through flags — they live inside
purpose-specific wrapper functions that compose `autogit.Checkout` (or
similar) with their own internal verification. Flags carry kind, not
arguments.

**No git pre-commit hook is installed.** Validation is part of
personant's own logic, invoked at the event points where it's required;
routing through git's hook mechanism would be unnecessary indirection
for a function call we make from our own code. A human who runs
`git commit` manually inside `$PERSONANT_HOME` is operating outside
the runtime and owns the consequences — same posture as hand-editing
`spine.jsonl`.

---

## 9. Measurement and validation regime

The architectural thesis cannot be validated by inspection. Personant's
value proposition — months-long seamless continuity, drift-resistant
memory, opportunistic recall, retire-and-recover cycles that preserve
meaning — is a behavioral claim about a complex, time-evolving system.
Either the runtime delivers these properties under realistic load, or
it doesn't. **The measurement regime is what proves it.**

**The cost of getting this wrong without proof ahead of time is
asymmetric and severe.** The "use it and find out" path has no graceful
recovery: if extended accumulated usage reveals the storage
strategy or memory mechanics are structurally wrong, the choice is
between a complex, risky refactor of accumulated agent memory state or
losing all of it. The user's accumulated continuity is the property the
system exists to deliver in the first place — sacrificing it to
validate the architecture would defeat the purpose. The simulation
regime exists because the proof must come **ahead of time**, not after
extended real use has built up the very state we'd be putting at
risk.

This section is therefore framed not as a quality gate bolted on after
features land, but as the **measurement instrument** by which we
validate the thesis and *evolve techniques iteratively until they
deliver*. Personant studies its own behavior; the test fabric is its
lab bench.

### 9.1 The four-month simulation (v0.1 acceptance gate)

The v0.1 acceptance criterion:

- **Simulate four continuous months (120 calendar days)** of realistic usage via a synthetic
  workload (§9.4) running against the real runtime with a mock LLM
  (§9.2) supplying canned responses.
- **Memory quality maintained** throughout, measured via the metrics
  emitted at every event (§9.4): recall hit rate, engagement accuracy,
  retirement timing, round-trip information-preservation through
  retire→archive→recover.
- **Zero out-of-context-space events.** The **normative** criterion is the
  X4 whole-request **token** ceiling (`context.token-budget`, #127): the
  fully-assembled request (system prompt + replayed history tail + current
  user input + injected fetches) must never exceed it, checked post-flight
  against `usage.prompt_tokens` (the only modality-agnostic authoritative
  count). Any overflow is a fail. The per-layer **byte** budget
  (E/A1/A2/B/C) is a derived, deliberately-conservative truncation *driver*
  (§3.1): on mock rungs `VerifyNoBudgetOverflow` asserts each rendered layer
  stayed within its byte allocation as a cheap **belt-and-suspenders** check
  on that truncation contract, catching a future render-time bypass before it
  can inflate the token count. Turn-time (delta-time) budget enforcement
  (T3-3) stays **deferred** past v0.1 — see §3.0.2 step 4.
- **Operation runtime costs measured and within bounds.** Wall-clock
  P50/P95/P99 latency for: engagement update, spine match, thread
  fetch, retirement, archival, recovery, index rebuild, index check.
  Bounds are calibrated empirically; "within bounds" means stable
  across the simulation, not exceeding a threshold that grows with
  accumulated state.
- **Steady-state demonstrated.** After four simulated months (120 days), the
  trajectory of working-set size, spine size, and per-operation
  latency should be **flat** — not creeping upward. The acceptance
  criterion is "the system is in a state from which it could run
  another four months without degradation," not "the system has
  survived four months."

**The acceptance ladder.** The gate is walked as a geometric ladder of
increasing *simulated calendar span*, expressed in **days** (not hours)
on purpose: a span like `30d` is 30 days of calendar time — most of it
*not* spent working — which is the realistic, harder case; "hours" would
invite the wrong reading of active-use time. Two tiers:
- **Smoke rungs — `1d` / `7d`.** Fast regression catch; `make test` runs
  the default `1d` rung. Too short to distinguish steady-state from
  drift, so they are not analysis rungs.
- **Real ladder — `15d → 30d → 60d → 120d`.** Exact ×2 doublings
  (anchored on the 30-day month) for clean log-scale comparison of
  per-rung deltas. The top rung is **120 days (4 months)** — the gate was
  initially gut-framed as six months, but 120d is empirically sufficient
  to demonstrate steady-state and distinguish *bounded* from *runaway*;
  longer rungs only re-measure the bounded corpus-saturation floor
  (below) at greater wall-clock cost. Per-turn wall-clock has been shown flat across the
  whole ladder (≈32 ms/turn, 1d→120d), so the ladder cost is linear in
  span with no per-turn growth term.

**Long-rung recall precision is corpus-saturation-bounded — read it as a
measurement floor, not a regression.** On long rungs the symbolic-layer
precision/F1 declines (and the unresolved-episode rate rises) while
*recall stays flat* — the true match is found just as reliably at 120d
as at 7d. The cause is the fixed sim corpus (§9.4): a bounded symbol
vocabulary means that as thread population grows, dormant non-sibling
threads accumulate enough symbolic-Jaccard overlap to clear the match
threshold, so the correct thread fires *plus* topically-legitimate
extras — which the FamilySize-bounded ground-truth oracle scores as
precision misses though they are correct symbolic behavior. The
asymptote is bounded (tag signatures are near-unique), so F1 settles
rather than diverging. In production the §3.4 layer-2/3 embedding +
model-judgment stages disambiguate these, so the sim's symbolic-only
precision is a *floor*, not the system's. Consequence: rungs past ~30d
partly re-measure this fixed collision floor rather than memory scaling;
making them measure memory scaling again requires the corpus vocabulary
to scale with thread population (per-thread symbol salting) — a queued
sim improvement, not a production recall defect.

**Recall-layer scope: the acceptance sim is transitional, currently
symbolic-only.** The standard acceptance run uses the mock LLM (§9.2)
with a *nil embedder*, so it exercises only the §3.4 **symbolic Jaccard**
layer — not the embedding-primary scan that §3.4 makes the *primary*
recall mechanism in production, nor the model-judgment confirmation
layer. This was originally a hard constraint (no unmetered embedding or
inference available); it is being lifted. With unmetered local inference
+ embedding now available (`reaper.local`: gemma-4 family for inference,
`nomicai-modernbert-embed-base-bf16` for embeddings), the planned next
increment wires **inference-in-loop and embedding-in-loop into the
acceptance sim, each individually configurable**, to close the coverage
gap — most importantly to measure embedding-primary recall under the
same realistic workload, rather than inferring it from the separate C.6
head-to-head. Two consequences the gate must hold honestly:

- **Until that lands, symbolic-only metrics do not validate the recall
  path users actually get.** A within-thread-drift recall miss on the
  symbolic layer (e.g. the hop-graded abandoned-recall decay the
  within-thread-interleaving work measures) is *expected* — it is the
  quantified motivation for the embedding layer, not a substrate defect.
  Reporting symbolic-only recall as system recall would be exactly the
  form-vs-function overclaim the convergence honesty clause forbids.
- **Service-in-loop is a cost/coverage tradeoff, not a free upgrade.**
  Real inference/embedding raises per-turn cost from ~10s of milliseconds
  to single-digit seconds, trading away the ~20-minute wall-clock for a
  120-day run that makes the mock regime such a fast iteration
  instrument. The increment therefore includes an **empirical
  sweet-spot hunt**: maximize coverage/rigor added per unit of per-turn
  overhead (e.g. inference/embedding sampled on a fraction of turns, or
  only on recall-bearing turns), keeping the fast mock regime as the
  default iteration loop and reserving full service-in-loop for
  periodic deep validation. Tracked, gating v0.5.0 convergence.

**The gate is realism CONVERGENCE, not a single run.** "Survived the
full span in whatever shape the workload happens to be in" is form, not
function. The criterion is the full top-rung span (120 days) passed with *every identified
element of realism accounted for* — including elements surfaced by prior
runs. It is a converging loop: each run exposes a missing realism
behavior (or a latent defect the previous incoherence masked), which is
addressed before the next run; the gate is met only at the fixed point
where a run surfaces **no new realism gap**.

"Accounted for" does not require "simulated." A realism element is
accounted for by one of, in descending preference:

1. **Simulated** in the workload — exercised end-to-end (gold standard).
2. **Modeled and attempted** — a written model of its expected effect, a
   solution implemented against that model, and unit tests over whatever
   sub-parts decompose, when faithful simulation costs more than
   predicting the effect.
3. **Known unknown, deferred to empirical human-use data** — when the
   honest answer requires real human use. Labeling it a parked
   known-unknown is correct; implementing on guesswork wastes effort and
   bakes in a wrong assumption. The data arrives from the front-end U/X
   phase (below), which closes these items in a later substrate
   iteration — iterative, not circular-blocking.

The standard is *as much diligence as is honestly practical* — not
perfect knowledge, which is unattainable. **Honesty clause:** the
acceptance claim must document its own coverage (what is simulated vs.
modeled vs. parked-as-known-unknown), so the green check never
overclaims. A forgotten realism element is the failure; a documented
deferral is not.

**Accounted-for** (moved off the open backlog):

- **Evolving anchors / anchor-lifecycle redesign** — *simulated.* Threads
  drift far from their creation topic without any single jump sharp enough
  to cut a new thread, and premises invert; an abandoned premise must stay
  a findable handle. The evolving-anchor model (anchors as a re-derived
  projection of active `history_symbols`, symbol lifecycle with
  retained-not-evicted supersession, lifecycle-aware recall) is
  implemented (§2.2/§2.3/§2.4/§2.7.4/§3.2/§3.4/§5.1) and exercised
  end-to-end by the coupled `vague-new`/`drift`/`invert`/interleave
  workload and its metrics (`drift_recall_origin`/`drift_recall_dest`,
  `abandoned_premise_recall`, `superseded_precision`,
  `ever_central_count`/`history_len`, `projection_churn`), which land in
  the build's sim-workload increment.

**Open realism backlog** (the convergence checklist as of 2026-05-22 —
each item must be accounted-for before the gate is met; this list grows
as runs surface new gaps):

- **Workload incoherence / interleaving.** The current single-topic-per-
  thread workload is unrealistically tidy; humans jump between loosely-
  related threads, abandon and resume, and emit non-sequiturs. Remedy:
  interleave excerpts + queries from several unrelated topics within a
  session. Until done, recall/precision numbers describe a too-coherent
  stream.
- **New thread as synthesis** of multiple prior threads — *accounted-for
  (built at the symbol level)*: multi-parent provenance is carried as
  per-symbol `derived_from` (§2.3/§2.7.3), populated deterministically on
  recall-incorporation; the non-recall-triggered residual is an explicit
  deferred decision (§2.7.3). The sim models synthesis threads (borrowing
  symbols from ≥2 prior threads); recall stays provenance-agnostic.
- **Topic clustering / deep dives, metronomic edit cadence, cold start**
  (the sim-vs-reality review's Lens-B gaps).
- **Transient-data lifecycle (§3.10)** fidelity — a prerequisite to an
  *honest* recall-fidelity measurement.
- **Topic-tag fidelity under real inference** (added 2026-07-13, design
  assessment). Topic-tag emission is the single LLM function the whole
  recognition architecture leans on — the mock regime scripts it, so tag
  *quality* is entirely unmeasured. The inference-in-loop increment must
  treat tag-fidelity metrics as a first-class deliverable, not a
  side-effect: re-engagement miss rate (turn engages a known thread, tag
  omits it), spurious `*new-topic*` rate (tag cuts a new thread where the
  oracle expects re-engagement), and anchor-emission overlap vs. the
  deterministic extraction pass. A live rung without these instruments
  exercises real tags while measuring nothing about them.
- **Spine-cardinality stress / A1-saturation handoff** (added 2026-07-13,
  design assessment). Layer-1 model-native recognition depends on the
  active project's spine lines fitting Layer A1's byte budget; at
  spine-display line lengths and the current partition, A1 saturates in
  the low hundreds of threads, while a multi-year career accrues
  thousands. The 120-day top rung cannot reach this region organically.
  Required: a stress rung with a synthetic pre-seeded multi-thousand-
  thread spine measuring (a) recognition quality across the A1
  truncation boundary, (b) whether opportunistic recall (layers 2/3)
  picks up the recognition load for threads that fell off the rendered
  surface, and (c) §3.8 archival onset behavior under genuine
  cardinality pressure. This is the least-validated transition in the
  long-career design; the mechanisms exist (§3.1 truncation, §3.8
  archival) but no instrument is pointed at the handoff.

**Two version lines — the values live in code, not here.** The substrate
(this spec's runtime + the `MemoryOps` API) and the front end (U/X +
feature logic atop it) are versioned independently. The four-month gate
converges the *substrate*; it says nothing about the front end, which is
earned by a separate human U/X phase.

The current values are **`version.Substrate` and `version.FrontEnd` in
`internal/version`** — the single source of truth. They are deliberately
NOT restated here: a prose copy is a second definition that goes stale on
the next bump. `personant version` (and `--version`, and the REPL's
`/version`) prints them; `system.bootstrap` records them once per session
(§2.8).

The **milestone gates** are normative and stay here:

- Substrate **v0.5.0 — realism convergence.** The four-month simulation
  gate above, walked to the top rung with every identified realism
  element accounted for (simulated, modeled-and-attempted, or honestly
  parked). At v0.5.0 work switches gears to front-end logic.
- Front end **v0.1.0 — minimum interactive set.** Earned once a minimum
  interactive feature set exists in any form, validated by direct human
  use rather than by simulation.

**Front-end bump cadence.** During the living-with phase every front-end
(CLI/REPL/UX) feature change bumps `version.FrontEnd`'s PATCH digit
unless the developer specifies otherwise; MINOR bumps are developer fiat
at the milestone gates above.

**A third line, not semver: the home's on-disk `format`.**
`<home>/version.toml` (§2.1) carries a single integer — `format = N` —
recording the canonical layout revision the home's bytes were written in.
`version.CurrentHomeFormat` is what this binary writes and understands.
It is deliberately **not semver**: an on-disk layout is either one a
binary can read or it is not, so there is nothing for a three-part
version to express. It advances independently of both semver lines — most
releases do not change the layout at all.

**Gating** is `memops.GateHomeFormat`, a pure decision run at the process
boundary. **Ordering invariant (load-bearing): the gate runs BEFORE
Reconcile.** Reconcile is crash recovery (§4.5.8) and resets the worktree
to a recovery point; running it against a layout this binary cannot
interpret would rearrange bytes it does not understand. The open sequence
is *gate → Reconcile → LoadSession*.

- **Equal** → proceed.
- **Newer on disk → REFUSE.** A home written by a newer personant may
  carry fields, files, or invariants this binary does not know; operating
  on it can corrupt it, and the honest move is to say so and stop.
  `--allow-newer-home` overrides the refusal for one invocation. Because
  that is knowingly unsafe, a waved-through open emits a loud stderr
  warning AND a `system.home-format-override` event (§2.8) — the record
  someone reads afterward must show that it happened.
- **Older on disk → refuse**, pending a registered migration.
  Deliberately unreachable today (there is exactly one format); the shape
  exists so the first real migration changes a constant and registers a
  migration rather than restructuring the gate.
- **No stamp → adopt forward.** A home predating versioning is taken to
  be `version.UnversionedHomeFormat` and stamped as such.
  `UnversionedHomeFormat` is a separate constant pinned at 1 forever,
  never an alias of `CurrentHomeFormat`: when `CurrentHomeFormat` later
  advances, an unmigrated legacy home must resolve to 1 and take its
  migration, not be silently declared modern.

`personant version` is the deliberately **ungated** diagnostic — it
reports the binary's identity, the resolved home path, and the home's
stamp (a concrete revision, *absent*, or *unreadable*) and exits 0 either
way. It is what you run precisely when the home is missing, broken, or
newer than the binary, so it never gates, never reconciles, and never
fails on a home it cannot read.

This is the load-bearing test. Phase-1 unit tests, Phase-2 scenario
tests, and Phase-3 churn tests all build toward enabling this
simulation.

### 9.2 Mock LLM client

Tests exercise the full runtime path except the actual LLM round-trip.
The mock client lives in `internal/model/` alongside the production
`HTTPClient` and implements the same `Client` interface — production
callers and tests use the same import; only the constructor differs.

Two modes:

- **Scripted**: a queue of canned responses is preloaded; each call
  returns the next response. Deterministic, replay-friendly.
- **Generated**: on-demand from a seeded RNG. Lorem-ipsum-style body
  text with a counter-suffix or seed-derived UUID for guaranteed
  uniqueness (no accidental dedup). Programmable: caller specifies
  topic-tag content, anchors emitted, response length. Format-correct
  per §5.1.

The same mock satisfies all test layers (scenario, churn, calibration,
the acceptance sim).

### 9.3 Unit testing

Standard Go `testing` patterns over `internal/store/`,
`internal/index/`, `internal/verify/`, etc. Targets:
- Pure-function correctness (JSONL round-trips, schema validation,
  symbol normalization, ID parsing).
- Atomic-write semantics, idempotency.
- No mock LLM needed at this layer; tests don't exercise the turn
  loop.

### 9.4 Measurement regime (v0.1 in progress)

The measurement regime drives four test layers — scenario, churn, calibration, and the acceptance simulation — all running against the mock LLM client (§9.2) with logical-clock acceleration so the full simulation completes in minutes. Every test layer emits a machine-readable metrics blob (`internal/metrics/`) with a stable JSON schema for cross-version comparison. Working artifacts live in `internal/scenarios/` and the `mem.jsonl` telemetry; the detailed mechanics evolve with the implementation.

**v0.1 acceptance metrics** (normative; must hold through the four-month simulation):

- **Recall fidelity:** precision/recall/F1 measured per scenario step against `Step.ExpectedRecallMatches` ground truth. `RecallStrict` mode fails the test on mismatch; `RecallMeasureOnly` mode records adversarial probes without failing. Regressions tracked via baseline comparison against `testdata/baselines/<scenario>.json`. Metric series: `recall_fidelity_{precision,recall,f1}` and `recall_fidelity_adversarial_{precision,recall,f1}`.
- **Engagement accuracy:** tagged-engaged threads match canonical-by-construction ground truth in synthetic scenarios.
- **Heap bounded / zero overflow:** the normative gate is the X4 whole-request **token** ceiling checked against `usage.prompt_tokens` (§9.1, #127). `VerifyNoBudgetOverflow` is the **mock-rung belt-and-suspenders**: after composition it asserts each rendered layer stayed within its derived per-layer **byte** allocation (the §3.1 truncation contract held) across every turn. It is skipped on live-inference rungs, where the token ceiling governs. Turn-time (delta-time) enforcement (T3-3) is deferred — §3.0.2 step 4.
- **Steady-state trajectory:** working-set size, spine cardinality, and per-operation latency (P50/P95/P99) are flat after four simulated months (120 days) — not creeping upward.
- **Round-trip fidelity:** archive → recover → diff against original; information-preservation rate measured.
- **Operation latency within bounds:** engagement update, spine match, thread fetch, retirement, archival, recovery, index rebuild, index check — all stable as accumulated state grows.

**Invariant validators** (`internal/scenarios/invariants.go`) run after every operation in churn sequences and at key checkpoints in scenario tests. The full set is `VerifySpineIntegrity`, `VerifyIndexFresh`, `VerifyProjectReferences`, `VerifyLastActiveValid`, `VerifyThreadMetaMatchesSpine`, `VerifyEngagementConsistency`, `VerifyThreadAccounting`, `VerifyClosedThreadConsistency`, `VerifyArchiveResolvable`, `VerifyDedupConsistency`, and `VerifyNoBudgetOverflow`. `DefaultInvariants` is a cheap/heavy split: the cheap per-step tier (`VerifyLastActiveValid`, `VerifyNoBudgetOverflow`) fires every step; the heavy substrate-scale tier (`VerifySpineIntegrity`, `VerifyIndexFresh`, `VerifyProjectReferences`, `VerifyThreadMetaMatchesSpine`, `VerifyThreadAccounting`, `VerifyArchiveResolvable`, `VerifyDedupConsistency`) fires day-cadenced on the sim and always once at end-of-run. `VerifyEngagementConsistency` and `VerifyClosedThreadConsistency` are opt-in (closure scenarios) and not part of `DefaultInvariants`.

**Named scenario set** (initial): single-thread lifecycle, multi-thread interleaving, project switching, heavy retirement (50 threads), same-anchor collision, transient shell-capture content, cross-boundary recovery. Churn sequences are randomized-but-seeded (80% engage / 10% create / 5% retire / 5% switch); failures dump operation log + seed for replay. Calibration sweeps a directive parameter across a range and emit a metrics matrix — this is how §2.6.1 bootstrap defaults earn their numbers.

**Acceptance simulation harness** (`internal/scenarios/sim/`, `TestSim`): a deterministic seeded workload generator (the recall-madlibs corpus-slot model, §9.4) with logical-clock acceleration, steady-state assertions, and per-operation cost profiling. Run at a chosen span via `make sim DURATION=<N>d` (calendar days; named aliases `1d|1w` and Go-duration forms like `168h` also accepted). The acceptance ladder (§9.1) is `1d`/`7d` smoke + `15d→30d→60d→120d` real; `make test` runs the default `1d` rung. Output: metrics JSON + human-readable summary. Pass/fail per §9.1 criteria.

#### 9.4.1 Workload cadence model

The generated workload's simulated clock is a **single UTC `time.Time` primitive**, anchored at a Monday midnight; every other time-shaped value (current date, the day index, the day-off, termination) is **derived** from it, never tracked in parallel. This is the contract the day generator (`workload.go` `runWorkDay`/`runSession`/`runDayOff`) and the harness day-close share; its absence — two day-trackers in two coordinate systems (a jittered generator counter vs. a rigid harness `nextHeavyAt` grid) — let a day-off's 24h jump skip a day-close, collapsing the weekly day-off (#121, and the dual-source defect sim-time-single-clock.md retired). The harness's pinned clock is a **slaved copy** of the generator's emitted `Step.At` (it *sets* `pinnedClock = Step.At`, it does not integrate a delta), so there is genuinely one clock.

- **Monday-midnight anchor, 8h work-start, exactly-24h day.** The clock anchors at `clockStart = 2026-05-04 00:00 UTC` — a **Monday at midnight** — and buckets are midnight-to-midnight. Day *N* re-anchors to `dayStart(N) = clockStart + N·24h + 8h ± 15min` (the work day starts at **08:00 ± 15min**, 8h into its bucket — a fuzzy start drawn deterministically from the seed). Mid-bucket placement keeps the ±15min jitter 8h from either midnight edge, so a day can never spill into an adjacent bucket. The clock does **not** carry the accumulated day forward; at end of day it jumps to `dayStart(N+1)`. Every day therefore advances the simulated clock by exactly 24h on average and the work pattern never precesses. `Duration / 24h` is the number of days a run spans (a 240h run = 10 days); the derived day index `int((clock − clockStart) / 24h)` ticks exactly once per midnight boundary, and that tick — not a rival grid — drives the day-close + heavy-invariant cadence.
- **Two 6h sessions + ~1h break.** A work day emits two sessions, each covering ~6h of turn-active time, split by a fuzzed ~1h inter-session break — ~13h of work (08:00 → ~21:30, ending ~2.5h before midnight, comfortably inside the bucket). Within a session the clock advances per turn by a fuzzed **per-turn span centered at ~72 s** (turn duration + interstitial pause combined); the session emits turns until its cumulative span crosses 6h, the last turn giving natural end-time variation. At ~72 s/turn a 6h session is ~300 turns, so a work day is **~600 natural turns** (a deliberately brutal 12h-at-72s/turn upper bound; injected intra-thread engagements/probes raise the *observed* count further).
- **Emergent overnight.** The remaining ~11h overnight idle (24h − ~13h work span) is **emergent** from the next day's re-anchor — surfaced as the first turn's clock advance — never an additive constant.
- **Weekly day-off** (#108), **derived from the real calendar.** A day re-anchors as a day-off iff `dayStart(N).Weekday() == time.Sunday` — a pure calendar check on the un-jittered grid date, **no `%7` modular counter**. The Monday-midnight anchor puts day index 6, 13, … on Sundays. A day-off emits **no turns** — only the `SleepCycle` marker step the harness intercepts to drive consolidation. Its long emergent idle (last work-day end → next work-day start, skipping a full day) crosses the §3.5 wall-clock decay threshold without any additive day-off constant.
- **Each daily record is dated the day it represents.** The harness day-close for day *d* reports day *d*'s just-completed work and is stamped with day *d*'s own grid date `dayCloseDate(d) = clockStart + d·24h`, **not** `d+1`. So the 0-turn weekly day-off (Sunday, day index 6) lands **ON** the Sunday record carrying its own 0 turns, not on the Monday after it.
- **Day-off is a clean 24h-preserving grid slot (no phase shift).** The day-off occupies its **own** day-index slot and re-anchors to `dayStart(N)` exactly like a work day that simply emits no turns. `dayStart(N)` therefore stays strictly on the `clockStart + N·24h + 8h ± 15min` grid **through** the off-day, so work resumes the next work-day at the **same time of day** (±15min start fuzz only) as the work-day before it. The day-off must introduce **no systematic** phase offset; the ±15min start jitter is zero-mean and stays. (Residual of #121: the day-off must not advance by an emergent/additive idle gap that knocks `dayStart` off the grid — that shifted every post-day-off work day's anchor ~11h. `TestCadence_DayStartsNoPrecession` spans ≥9 days so it asserts the work boundaries **after** the day-off stay on grid, `TestCadence_DayOffPreservesPhase` asserts the day-off advances exactly one clean 24h slot on each side — phase in == phase out — and `TestDayOffThroughHarness` drives a ≥9-day run through the real harness and asserts exactly one 0-turn day-off record, contiguous `sim_date`, and the day count == `int(Duration/24h)`.)
- **Determinism.** The `(Seed, Duration)` → output contract is byte-exact: all fuzz (the ±15min start, the ~72 s per-turn spans, the ~1h break, the 6h session boundary) draws from the single seeded RNG in a fixed order, and the day count is a pure function of `Duration` alone (the termination check keys off the un-jittered day position, drawing no RNG). Same seed + duration → identical step stream.
- **Staccato clock; logical time is `TurnNumber` (realism ledger: accounted-for / not-simulated / benign).** The simulated clock advances **once per step** and is **frozen within a turn** — every `clock.Timeline()` read inside one `turn.Run` returns the same instant; it jumps at the next step. So intra-turn elapsed time is zero (a turn's own processing takes no simulated time; provenance timestamps within a turn collide). This is **not simulated and is benign** because the system's *logical* clock is `TurnNumber` (a strictly-increasing integer): closure/decay (`TurnNumber − LastEngagedTurn ≥ decayTurns`), staging, file-chain aging, and history ordering all key off `TurnNumber`, never wall-clock. `clock.Timeline()` is consumed only at coarse granularity — §3.5 wall-clock decay (days/hours; the inter-turn advance still moves it) and provenance/display timestamps — so zero intra-turn time changes no logic, no ordering, no measurement. **Revisit only if** sub-turn logic is added that orders events by timestamp, or metrics become timestamp-grained (which could then produce odd same-instant entries, knowingly discountable). Cheap fidelity fix if ever wanted: split the per-turn advance into intra-turn-processing + inter-turn-pause — buys log realism only, no correctness.

#### 9.4.2 Harness state-ownership and generator ↔ harness protocol

See ARCHITECTURE.md §"Simulation-harness structure" for the file-layout map and state-ownership table. This subsection specifies the protocol contracts that the structural map depends on.

**Log-marker contract.** The harness derives its measurement signal by scraping specific strings from the runtime's event log (§2.8) — not by introspecting internal runtime state. The scraped markers are:

| Log event string | Used for |
|---|---|
| `spine.match-fire` | Symbolic recall hit — feeds `RecallMatchFireIDs` in `StepFeedback`. |
| `spine.embed-match-fire` | Embedding recall hit — feeds `EmbedMatchFireIDs`. |
| `spine.intra-match-fire` | Intra-thread (fine-tier) recall hit — feeds `IntraMatchFireIDs`. |
| `thread.created` | Folds into `Harness.createdThreadIDs` (thread accounting + archival-forgiveness). |
| `archive.archived` | Folds into `Harness.archivedThreadIDs` (archival-forgiveness predicate). |

These strings are pinned by contract tests so a rename in the runtime's log emission fails a test rather than silently zeroing a measurement series.

**`StepFeedback.RuntimeLayerB` cross-check contract.** After each turn the harness captures the runtime's authoritative `ActiveThreads` slice and populates `RuntimeLayerB`. The generator's shadow Layer-B LRU is cross-checked against it with a hard divergence gate (==0 difference required). A divergence means the generator's recall oracle — which computes expected-match sets from the shadow LRU — has drifted from the runtime's actual working set; any tolerated divergence would silently produce wrong expected-sets and corrupt recall metrics.

**Archival-forgiveness contract.** An expected-match thread that the runtime has archived off the spine (present in `archivedThreadIDs`) can never produce a `spine.match-fire`; counting it as a miss would penalise the oracle for naming a correctly-archived thread. The forgiveness predicate (`TargetRecoverable`) and the forgiven count (`RecallExpectedForgiven`) are carried in `StepFeedback` so the generator's per-step scoring uses the same forgiveness logic the harness's `recordRecallFidelity` applies. The `recall_unexplained_absence` counter tracks threads that are off-spine AND absent from the archive index — a non-zero value is always a substrate integrity failure, never an expected archival outcome (see ARCHITECTURE.md §"Unexplained-absence = zero tolerance").


