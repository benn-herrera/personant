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
  api_keys/                         # secret-bearing — apiKeyFile targets; never read by the agent
  README.md                         # layout documentation for human inspection
  last-active                       # operational; one line: prj_<n> of most-recently-active project (§4.5.7)
  history                           # operational; REPL line-edit history (§4.3.1); newest last; capped
  archive/
    index.jsonl                     # canonical; deep cold archive index (§3.8); recoverable git-based archival (active)
  .git/                             # git-init'd at first run
  tmp/                              # agent's drafting scratch (see §6.3); never git-committed
```

**File ownership classification:**

| Type | Examples | Drift policy |
|---|---|---|
| Canonical | `spine.jsonl` rows, `threads/*.md`, `directives/*.md`, `projects/prj_<n>/meta.json`, `logs/*.log`, `providers.toml`, `config.toml`, `archive/index.jsonl` | Source of truth. Hand-editable. Other files derive from these. |
| Derived | `symbols.jsonl`, `projects/prj_<n>/digest.json` | Regenerable from canonical. `autogit.CheckDerivedFresh` fails any state-changing git op on stale. Never hand-edited. |
| Operational | `logs/*.log`, `.git/`, `tmp/`, `last-active`, `history` | System-managed; not subject to drift checking. `tmp/`, `last-active`, and `history` are gitignored. |
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
engagement.decay-turns: 8           # turns of non-engagement before closure prompt fires
engagement.decay-time: 7d           # wall-clock equivalent (Go duration string)
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
2026-05-08T02:55:44-07:00 system.bootstrap version=0.1.0 home=/home/user/.personant
2026-05-08T02:55:50-07:00 thread.engaged thr_42 [trefoil, unknot] turn=1247
2026-05-08T02:55:51-07:00 spine.match-fire thr_88 type=symbolic score=0.62
2026-05-08T03:12:00-07:00 retire.prompt thr_42 inactivity=12 ack=yes resolution=resolved
2026-05-08T03:14:30-07:00 dissect.fire reason=budget-pressure clusters=4
2026-05-08T03:14:35-07:00 dissect.complete clusters=4 acks=4 spine_added=4
```

No JSON schema in v0.1. Promote individual event types to structured form when query patterns become repetitive enough that grep/awk friction matters.

**Initial event vocabulary** (will grow during implementation). The event vocabulary is the minimum required to satisfy §9 measurement contracts and the directive-accrual feedback loop. Events absent from this vocabulary cannot be measured; any new mechanism that emits a decision event must add a corresponding entry here before the mechanism is considered measurable.

| Category | Events |
|---|---|
| `system` | `bootstrap`, `shutdown`, `error`, `config-reload` |
| `thread` | `engaged`, `created`, `state-change`, `summary-generated` |
| `spine` | `match-fire`, `match-miss`, `entry-updated` |
| `recall` | `offer` (with `count=N`), `accept` (with `thr=`, `layers=`), `decline` (with `thr=`, `reason=not-relevant\|wrong-project\|already-known`), `embed-error`, `cross-project-fire` |
| `retire` | `prompt`, `ack`, `defer`, `complete`, `curator-error`, `load-error` |
| `dissect` | `fire`, `cluster-proposed`, `complete` |
| `directive` | `accrual-update`, `parameter-read` (sampled) |
| `index` | `rebuild-start`, `rebuild-complete`, `verify-fail` |
| `user` | `shell` (with `$`/`#` discriminant), `slash` (see §4.2 / §4.4) |
| `project` | `created`, `switched`, `renamed`, `cd-changed`, `remote-adopted`, `remote-updated`, `remote-collision-prompt`, `meta-updated` |

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
real-model gate (see §6.5).

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
would be. In every case the turn's excerpt is never dropped.

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

### 3.5 Closure flow

Trigger detection (engagement decay or `/done`) → curator-drafted summary
→ user ack with resolution choice → frontmatter + spine update → Layer
B/C eviction. Deep cold archival (§3.8) is the next stage past closure
when spine cardinality pressure builds.

Engagement decay fires when `last_engaged` exceeds `engagement.decay-turns` OR `engagement.decay-time` (§2.6.1). OR semantics is deliberate: `decay-time` catches extended user absence (no turns at all); `decay-turns` catches low-frequency engagement in a busy session. AND semantics would let threads outlive their usefulness in either pattern.

The curator-drafted summary targets 100–150 chars. This balances two constraints: summary + anchors must fit within the Layer A1 budget at realistic spine cardinality (~400 threads × ~200 chars ≈ 80 KB, within the 8% A1 share at 64 KB context); the gist must also be sufficient for the model to recognize prior engagement without fetching the thread body.

**Ack-quality instrumentation (front-end v0.1 requirement, spec'd
2026-07-13; instrumentation landed 2026-07-13, rate-evaluation still
front-end-phase).** The closure ack is a load-bearing integrity gate only
while the human actually reads the draft; a rubber-stamped ack is worse
than none, because it launders an unread summary as human-verified. The
canary is **ack-edit-rate**: the REPL ack UI now offers an `[e]dit` choice
(§4.2 `/done`, §3.5 decay flow), and the `retire.ack` event detail (§2.8)
records `edited=yes|no` for EVERY acked outcome (retire and wip) —
`yes` only when the user submits a summary that differs from the curator
draft, so a rubber-stamp resubmission stays `edited=no`. What remains a
front-end-phase deliverable is the *evaluation* of the rate — a user who
edits 0% of drafts over months is either being served perfection or has
stopped reading, and the distinction must be probed, not assumed. That
evaluation needs a real human, so it is a §9.1 category-3 known-unknown
until then.

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
2. A **capture commit** pins the to-be-archived directories into a
   committed tree — this commit becomes the *parent* where the thread
   bytes remain reachable after removal. (Per-turn *content* writes do
   not commit; the substrate commits at `init`, on structural change,
   at archival, and at session close — §3.11 — so a capture commit here
   guarantees the bytes are in a committed tree before deletion
   regardless of what the cadence committed earlier.)
3. Stage the recursive removal of each `threads/thr_<n>/` directory
   (`os.RemoveAll` + `autogit.Add(".")`, which stages tracked-file
   deletions), remove all spine records in **one** filtered rewrite
   (`RemoveSpineRecords`), regenerate derived state (`symbols.jsonl`,
   digests) **once** for the batch, and append one `archive/index.jsonl`
   entry per thread. Commit this **deletion commit** as a unit (gated by
   `CheckSpineIntegrity` on the pre-state and `CheckDerivedFresh` on the
   post-state); capture its hash `H`.
4. The index entry references the deletion commit `H` and the parent's
   tree hash; because `H` is only known after step 3, a small
   **stamp commit** records the now-known `commit_hash` into the index
   entries. The archive index entry:
   ```jsonl
   {"thr_id":"thr_42","commit_hash":"<H>","tree_hash":"<tree sha of threads/thr_42/ at H's parent>","archived_at":"<RFC3339>","original_path":"threads/thr_42/","spine_summary":"<summary at archival time>","anchors":["..."],"project":"prj_3","recovered_at":""}
   ```
   `spine_summary` and `anchors` are preserved verbatim from the spine
   record at archival time so a search over archive entries can match
   without recovering the full thread. `recovered_at` is empty until the
   thread is recovered (§3.8.3).

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
— so it is a *conditional* guarantee, gated by the `CommitReachable` check
that also refuses to age an unreachable hash. The asymmetry is intentional:
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
the commit hash is reachable in the workspace repo** (a read-only
reachability check, §6.1.3, via the `CommitReachable` predicate). If the
hash is not reachable — the user amended, rebased, gc'd, or moved the
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

The substrate is a git repo; every commit buys a recovery point at the
cost of `.git` growth and commit overhead. The cadence policy is
**commit-on-structural-change**: the substrate commits at `init`, on
each **structural change**, at **archival** (§3.8, its own
capture/deletion/stamp batch), and at **session close**. Per-turn
*content* writes (turn excerpts, `history_symbols` accretion, anchor
re-projection, file edits) do **not** themselves commit.

**Structural change** = a turn that **creates** a thread (incl. a
synthesis thread, §2.7.3) or **closes/retires** one (§3.5 closure), or
the creation of a project. Archival is structural but commits via its
own §3.8 batch.

**The check is performed once at turn close**, not at each mutation
site: a turn that produced ≥1 structural change yields **exactly one**
commit. A close+create switch, or a vacation closure-storm (§9 T1-4)
that retires many threads in one turn, is therefore a single commit, not
one per event — fewer commits than a per-mutation rule, and the turn's
accumulated content rides along in the same commit.

**Session-close commit.** At session end the runtime commits any pending
working-tree changes — the safety net that flushes content-only turns
accumulated since the last structural commit.

**Why this cadence.** Per-turn commits at multi-month scale produce tens
of thousands of commits and corresponding `.git` growth; per-session
commits lose up to a session's work on a crash. Commit-on-structural-
change is the empirically-chosen middle. The §9 sim (15 sim-days) measures
**≈130 commits/sim-day, ≈1 commit per 2.8 turns** — closures dominate
(≈1648 vs ≈399 creates), so the rate is set mostly by thread retirement,
not creation. Per-commit cost is **linear** (P50 ≈38 ms; no `Add(".")`
rescan blowup, since thread bodies are FIFO-windowed), so the cadence does
not bog down even at this rate — it stays well below per-turn. The cost it
*does* carry is loose-object accumulation in `.git` (the sim never packs:
≈159 MB unpacked over 15 sim-days); packing is delegated to the offline
**sleep/consolidation cycle** (`git gc`/repack — ARCHITECTURE.md), not a
working-hours operation. `MemoryOps.Consolidate` is the sleep-cycle entry
point — today it runs go-git gc (`RepackObjects` + `Prune`); the §9 sim
fires it on the day-off idle window. It is a §9 calibration choice, not a frozen
constant — adjustable (e.g. commit only on terminal retirement, not
wip-pause) if `.git` growth or overhead ever warrant, but the measured
linear cost did not.

**Durability gap (by design).** Content-only turns between structural
commits are uncommitted; a hard crash loses at most that window, bounded
by the session-close commit. Finer-grain per-turn recovery is the
separate concern of the forced-shutdown/crash-resilience work (a per-turn
transaction boundary + reconstruct-on-open), which layers *beneath* this
cadence rather than replacing it — the cadence governs git recovery
points; the journal governs mid-session crash replay.

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
| `/done` | request closure ack on the active thread (§3.5) |
| `/pause` | mark active thread as `paused` (§2.2.1) |
| `/resume` | resume a paused thread |
| `/back-to <thr_id>` | re-engage a retired thread |
| `/no-revisit` | tighten threshold on the most recent recall surface (§3.4) |
| `/project` | print active project (id, name, root path, remote URL if any) |
| `/project switch <name-or-id>` | switch active project to a known one |
| `/project rename <new-name>` | rename the active project; updates `name` only (id, path, remote URL unchanged) — see §4.5 |
| `/cd-project <path>` | set active project root to `<path>` (see §4.5) |
| `/model <id>` | switch active LLM |
| `/stats` | runtime stats (active threads, layer fill, recent recall events, etc.) |

### 4.3 Decline categorization UI

When a recall offer is declined, the user picks a reason from a fixed
enum: `not-relevant` / `wrong-project` / `already-known`. v0.1
deliberately favors reasons that yield useful recall-tuning signal
(why a candidate missed) over U/X-oriented actions like defer or
suppress-offers; the latter return when the recall accrual loop is
actually built. The enum is expected to be tuned then.

### 4.3.1 REPL line editing and history

The chat REPL must support:

- `←` / `→` cursor movement within the current line.
- `↑` / `↓` command history; persistent across sessions in
  `<Home>/history` (operational, not git-tracked).
- Standard readline shortcuts: Ctrl-A / Ctrl-E (line start/end),
  Ctrl-W (kill word), Ctrl-U (kill line), Ctrl-K (kill to end),
  Alt-B / Alt-F (word-back / word-forward).
- History deduplication (no consecutive duplicate entries).
- History size cap (configurable; default 1000 entries).

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

#### 4.4.2 Shell process

The runtime spawns one long-lived `$SHELL -i` (fall back to `/bin/sh -i`)
subprocess at personant startup. `$`/`#` lines are written to its stdin;
output is streamed to the user's terminal and (for `#`) tee'd into a
capture buffer for context inclusion.

Shell state — cwd, environment, aliases, functions, history — persists
naturally across the personant session because the same process services
all `$`/`#` invocations. The shell process is destroyed cleanly on
personant exit.

#### 4.4.3 Interactive applications

Interactive terminal applications (`vim`, `nano`, `less`, `top`, etc.) are
**not supported**. Owning an interactive shell process is not the same
as wiring the user's terminal to that shell's PTY for arbitrary
sub-applications, which requires terminal-mode handoff (raw mode +
signal forwarding + state restoration). If the user wants `vim`, they
suspend personant or open another terminal.

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
| Shell cwd | `$ cd <path>` within the interactive shell (§4.4) | path resolution for `$` and `#` shell commands only |

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
files, logs). On startup the runtime must **reconcile derived state
against canonical** — detect that a prior session did not shut down
cleanly, and rebuild/repair the stale derived artifacts (cf. `personant
index rebuild`, §2.1) rather than trusting them. The normal
shutdown→resume cycle must also be exercised, not only the crash path.
Status: queued for v0.1; not yet implemented. (The honest-coverage rule
of §9.1 applies — this is a known substrate obligation, tracked, not
forgotten.)

**Design direction (decided 2026-05-22; not yet coded):**

**No signal/interrupt handlers as a flush mechanism.** A handler doing real work during teardown is a footgun (partial flush, re-entrancy, races, can itself be SIGKILL'd) and may produce a *more* confused post-termination state. No signal handlers for canonical writes.

**Per-turn transaction marker.** A file marker (`turn-in-progress` with the current turn id) is written before any canonical writes for a turn and cleared after they all complete. On open: marker absent → clean shutdown (rebuild derived); marker present → the prior turn was interrupted mid-write → reconcile/roll-back the partial turn, then rebuild derived. This handles clean shutdown and SIGKILL uniformly with no shutdown event to catch, and bounds possible inconsistency to exactly one in-flight turn.

**Turn-level (cross-file) atomicity is the real gap.** Current writes use `store.WriteFileAtomic` (deterministic sibling temp file `tmp-<target>` + `os.Rename`) — atomic per file, but a turn rewrites two canonical files that must stay mutually consistent: `spine.jsonl` and the owner thread's `.md`. An interrupt *between* those two renames leaves canonical internally inconsistent (spine `turn_count` ahead of the `.md`); rebuilding derived state does not fix this. The chosen solution: **git commit per turn as the atomic boundary** — the substrate is already a git repo, each commit preserves the prior state, and crash recovery can `reset --hard HEAD` to discard a partial in-flight turn. (Note: the §3.11 commit-on-structural-change cadence commits only on structural changes; the per-turn transaction boundary is a finer-grain commit that fires on *every* turn for durability. Both coexist: the §3.11 commit may absorb the per-turn commit on structural turns.)

**Durability bar: at most one turn may ever be lost**, no matter how pathologically timed the termination. Even that one turn must be made as rare as practical — the in-flight turn carries user prompt + model response: real, expensive-to-recreate in-task-flow state. Protect the raw `(user-prompt, model-response)` bytes by writing them to a **durable append log as the first action of the turn** — before any multi-file canonical writes — so a kill during the canonical commit can recover turn content on restart even when spine/.md writes were partially applied. Structural/derived state is always reconstructable; the content bytes are the genuinely irreplaceable part.

**Temp file naming.** Use deterministic sibling names (`tmp-spine.jsonl`, `tmp-thr_42.md`) rather than random-suffix temps. Rationale: (a) a leftover temp after a crash maps clearly to its target and is visible as untracked; (b) `O_TRUNC` handles a stale leftover from a prior crashed write; (c) deterministic temp→target mapping is what a roll-forward redo-log recovery needs.

**Testing methodology (must be built):** deliberate fault injection — partial file-set writes (apply only some of a turn's renames, in every ordering), torn/truncated writes — followed by recovery and verification that the substrate ends in a runnable state that lost ≤1 turn. Fold these as sim/recovery scenarios, not one-off manual checks.

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
`MinAnchorsPerThread`, the 4-floor, and the over-8 abort. **Retained
fail-loud:** `ErrFileEditWithoutTopicTag` (ownership binding) and
synthetic-stub / malformed-tag rejection remain the only topic-tag
fail-loud paths (§3.0/§3.3) — a file edit with no owning topic tag still
fails the turn loud.

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

#### 5.1.3 Prompt template

The system prompt instructs the model to emit a topic tag at response
start. The template lives in `internal/prompt/template.go` and is
hot-reloadable for empirical tuning.

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

Re-prompt is capped at 1 per turn. Latency cost on a turn that
triggers a fetch is up to 2× the no-fetch case (one aborted stream +
one full stream); turns that don't trigger a fetch pay nothing.
Speculative pre-fetch is rejected as an explicit non-goal (every
loaded thread is loaded because the model said it was needed; see
ARCHITECTURE.md "No speculative prefetch").

---

## 6. Tool surface and permissions

See ARCHITECTURE.md §"Tool surface (bounded, role-shaped)" for the design rationale; this section specifies the contract.

### 6.1 External tool surface

The LLM's external tool inventory is bounded at twelve tools, split between
read/think and draft/mutate. *Internal* tools (operations on personant's own
state — `personant search`, mid-turn thread fetch, `/topic`, `/done`, etc.)
are out of scope for this section; see §4 (user surface) and §5.5 (model
invocation protocol).

#### 6.1.1 Read/think tools (6)

| Tool | Signature (informal) | Purpose |
|---|---|---|
| `fs.read` | `(path) → bytes` | read a workspace file |
| `fs.list` | `(path) → entries[]` | list a directory |
| `fs.grep` | `(pattern, path, opts) → matches[]` | regex/literal search across a tree |
| `web.fetch` | `(url) → bytes + meta` | retrieve a URL |
| `web.search` | `(query) → results[]` | search query → URLs |
| `model.consult` | `(prompt, model_id) → response` | ask a guest model |

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

### 6.2 Permission tiers and accrual

Acknowledgement on every workspace mutation defeats the architectural thesis
("human acks at high-leverage moments only"). Permission tiers grade ops by
risk and reversibility; accrued grants in directive files let friction trend
toward zero as the system learns the user's working pattern.

#### 6.2.1 Three-tier default policy

| Tier | Default | Covers |
|---|---|---|
| 0 (silent) | never ack | all reads; all writes inside `.personant/**`; all `fs.tmp_*` |
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
| `tool` | `call`, `result`, `error`, `denied`, `truncated` |
| `permissions` | `prompt`, `grant`, `deny`, `revoke`, `accrual-update` |

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

LLM provider connectivity lives in `$PERSONANT_HOME/providers.toml`. This file is **the pool of available providers** — a connectivity catalog only. It pins nothing and expresses no preference; the choices that *draw from* the pool live in `config.toml` (§8.2.2).

Format:

```toml
[provider-name]
baseUrl      = "https://api.example.com/v1"
apiKeyFile   = "api_keys/example.key"
defaultModel = "gpt-5.4-2026-03-05"
```

One TOML table per provider. The provider name is the lookup key used by config references and by the runtime's outbound LLM-client wiring. `defaultModel` is the provider's own fallback model, used when no model is named elsewhere.

**API-key forms.** A provider supplies its key one of two ways:

- `apiKeyFile` — **recommended.** A path (relative to the `providers.toml` directory) to a file containing only the key. The key material lives outside `providers.toml`, so the catalog itself carries no secret.
- `apiKeyUnsafe` — an inline key string. **Discouraged**, named to make the hazard visible: it embeds a secret directly in `providers.toml`.

**Security boundary (load-bearing):**

- Because keys are referenced by file rather than embedded, `providers.toml` using `apiKeyFile` throughout is **safely scannable** — agents and tooling may read and edit it. The **secret-bearing artifacts are the `apiKeyFile` targets**, not the catalog.
- The runtime resolves a provider name → `baseUrl`/key/`defaultModel` at the moment of an outbound API call; the resolved key material is **never** included in any LLM context, log line, ack prompt, or captured shell output.
- The refuse-vs-redact policy below applies to the key files (and to any `providers.toml` that still uses `apiKeyUnsafe`), not to an `apiKeyFile`-only `providers.toml`.

**Hybrid redaction policy** (refuse vs. redact, by initiator) — for the secret-bearing artifacts (`apiKeyFile` targets; `providers.toml` only when it carries `apiKeyUnsafe`):

- **Agent-initiated reads** (`fs.read`, `fs.grep`, `fs.list` listing the file's parent dir) targeting a key file (after symlink resolution): the runtime **refuses outright** — the tool returns a deny-error to the model like `permission denied: API-key file is secret-bearing and cannot be read by the agent`. Logged as `permissions.suspicious-access`.
- **User-initiated captures** (`#`-prefixed shell commands per §4.4 whose output contains key material): the runtime **redacts before reaching context** — captured output is replaced with `[redacted: API-key content]`. The user's terminal still sees the unredacted output; only the path-into-LLM-context is filtered. Logged as `permissions.redaction-fire`.

The split reflects intent: an agent reaching for secrets is suspicious and warrants refusal; a user `#`-grepping their own home dir for context inclusion is reasonable but accidental and just gets quietly cleaned up.

**Load-time key resolution.** Every provider's key is resolved when the pool is loaded — an `apiKeyFile` is read and trimmed. A provider whose `apiKeyFile` cannot be read (missing file, permission error) is **dropped from the pool and reported as a fault**: it does not abort the load, and the remaining providers stay usable. The runtime surfaces each fault as a startup warning. A `providers.toml` that is itself unreadable or malformed is a hard error. (Resolution and fault strings carry only the file path, never key content — see the security boundary above.)

#### 8.2.2 Configuration choices: `config.toml`

`$PERSONANT_HOME/config.toml` holds the choices that draw from the provider pool. It is canonical, hand-editable, and **not secret-bearing** — it names providers and models, never keys.

Format:

```toml
[chat]
defaultModel = "provider/model"

[embedding]
model        = "provider/model"
vectorLength = 768
```

- `[chat] defaultModel` — the default chat provider/model.
- `[embedding] model` — the embedding provider/model. This pin is **mandatory for embedding recall** and must be explicit: the embedding model defines the vector space, and an inferred or drifting model would silently invalidate the existing embedding cache.
- `[embedding] vectorLength` — optional; for matryoshka-capable embedding models, requests this truncated dimensionality (passed as the `dimensions` parameter on the embeddings call).
- `[recall] model` — (future, not yet built) the recall layer-3 judge model (`provider/model`). A separate reference from `[chat]` so the judge's **context ceiling can be tuned independently** of the main chat model's large window; a small dedicated window (~8K) is intentional (the judge's input is: a query + N ≤150-char candidate summaries). Default: a small-tier model (E2B). The empirical discipline: measure E2B → E4B → 26B against the embedding-only precision baseline; pick the smallest passing tier. See ARCHITECTURE.md §"Minimize infrastructural prompts" and §"Recall mechanisms" (layer-3 judgment).

Model references are `"provider/model"`, split on the **first** `/` (the model portion may itself contain slashes, e.g. `openrouter/google/gemma-4-31b-it`).

**Cross-file validation.** At bootstrap the runtime validates `config.toml` against the loaded pool:

- Each non-empty `[chat]`/`[embedding]` reference must be a well-formed `provider/model` string, and the named provider must exist in `providers.toml`.
- The `[embedding]` model id must equal that provider's `defaultModel` **verbatim**. The embedding model is a hard static pin — it defines the vector space — so it is effectively double-declared (the pool entry and the config reference) and cross-checked, catchable at bootstrap with no network call.
- The `[chat]` model id is **not** statically checked. A chat model is resolved at use time against the provider's `/models` endpoint; a no-such-model condition (or an absent/invalid provider `defaultModel`) surfaces as a runtime error then, with the provider's `defaultModel` used as the fallback. This is checked only when the provider is actually used — the runtime does not probe every provider's `/models` at startup.

Any cross-file validation failure is fatal at bootstrap. (A faulted provider per §8.2.1 is *not* itself fatal — but a `config.toml` reference to a provider that faulted out of the pool fails this check, since it is no longer in the pool.)

**Precedence** (chat model resolution): CLI flag > `config.toml` `[chat]` > the resolved provider's own `defaultModel`.

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

**Two version lines.** The substrate (this spec's runtime + the
`MemoryOps` API) and the front end (U/X + feature logic atop it) are
versioned independently. The four-month gate converges the *substrate*;
it says nothing about the front end, which is earned by a separate human
U/X phase. Current state: **substrate v0.1.0** (earned through the test
rigor to date), **front end v0.0.1** (REPL loop closed, untested by any
direct human means, missing much minimally-required interactive
behavior). Substrate **v0.5.0 is the realism-convergence milestone**, at
which work switches gears to front-end logic; the front end reaches
**v0.1.0** once a minimum interactive feature set exists in any form.

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

**Invariant validators** (`internal/scenarios/invariants.go`) run after every operation in churn sequences and at key checkpoints in scenario tests: `VerifySpineIntegrity`, `VerifyIndexFresh`, `VerifyEngagementConsistency`, `VerifyArchiveResolvable`, `VerifyProjectReferences`, `VerifyLastActiveValid`, `VerifyNoBudgetOverflow`, `VerifyDedupConsistency`.

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


