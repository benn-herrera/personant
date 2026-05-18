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

## 1. System overview

Personant is a single-user, single-agent runtime for managing an AI assistant's working memory across arbitrary projects with continuity. The agent has one continuous career — unified persistent memory, no session boundaries, no compaction-driven information loss, cross-project recognition. Role: **research assistant**, not general-capability agent.

For orientation (the architectural thesis, recurring patterns, layer model, tool surface, permission tiers, etc.), see `ARCHITECTURE.md`. This spec assumes that orientation and goes directly to operational detail.

**Operationalized acceptance (v0.1):** the system must pass a **six-month simulated workload** (§9.1) — sustained continuity of memory quality, zero out-of-context-space events, measured runtime costs for recall / retirement / archival / resurrection within bounds. After six simulated months the system must be in a state demonstrating it could run another six months without degradation. This is what makes "months-long continuity" a proven property rather than an aspiration.

---

## 2. Data model

### 2.1 Storage layout

```
$PERSONANT_HOME/                    # default ~/.personant; configurable
  spine.jsonl                       # canonical, sorted lexically by id
  symbols.jsonl                     # [derived] inverse symbol → threads index
  threads/
    thr_<id>.md                     # one file per thread; markdown + YAML frontmatter
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
    index.jsonl                     # canonical; deep cold archive index (§3.8); empty until v0.2
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
  anchors: string[];                // 4-8 normalized anchor symbols (see §2.7)
  summary: string;                  // 100-150 char gist; ≤ spine.entry-max-chars (default 200)
  state: ThreadState;               // see §2.2.1

  // timestamps (RFC3339)
  created: string;
  last_engaged: string;
  state_changed: string;

  // bookkeeping
  turn_count: number;               // total turns this thread has been engaged in
  recall_fires: number;             // count of recall matches that resulted in fetch (for anchor quality tuning)
}
```

**Hard limits enforced by `personant verify`:**

| Field | Constraint |
|---|---|
| `id` | regex `/^thr_\d+$/`, globally unique |
| `project` | regex `/^prj_\d+$/`, references an existing project (or `prj_default`) |
| `anchors.length` | between 4 and 8 inclusive |
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

### 2.3 Thread file format (`threads/thr_<id>.md`)

Markdown body with YAML frontmatter. Frontmatter is canonical for metadata; body is operational content (turn excerpts, decisions, working notes — not pedagogy).

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

[Operational body content. Terse, accumulated as thread progressed.]
```

**Body content guidelines:**
- This is operational, not pedagogical. Terse; machine-friendly.
- Typical structure: chronological excerpts of turns where this thread was primary engagement, plus curator-summarized milestones at retirement.
- No required heading structure beyond a top-level title matching the thread's working name.
- The retirement summary (the same text that becomes the spine `summary`) is not duplicated in the body — the body holds detail; the summary is in frontmatter only.

**Frontmatter `history_symbols` structure:**

```typescript
interface HistorySymbol {
  raw: string;                      // surface form as encountered
  normalized: string;               // normalized form (see §2.7)
  first_seen_turn: number;          // turn ID when first emitted
  count: number;                    // total emissions across all turns
  source: SymbolSource;             // see §2.7.2
}

type SymbolSource = "deterministic" | "model" | "user" | "curator";
```

`history_symbols` is hard-capped at `history.cap-per-thread` directive value (default 40). When the cap is exceeded, eviction policy is lowest cumulative weight: lowest `count` first, ties broken by lowest `first_seen_turn`.

### 2.4 Symbol index schema (`symbols.jsonl`)

[Derived] Inverse index. Sorted lexically by `symbol`. Rebuilt from `spine.jsonl` + `threads/*.md` frontmatter on every index refresh.

```typescript
interface SymbolRecord {
  symbol: string;                   // normalized form
  threads: string[];                // thread IDs where symbol appears anywhere (anchor or history)
  anchor_in: string[];              // subset of `threads` where symbol is an anchor
  source_dominant: SymbolSource;    // most common source across emissions
}
```

`threads` is sorted by descending `recall_fires` of the referenced thread (most-recalled first) so that opportunistic-match operations naturally surface high-recall threads first when ties exist.

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
  recent_anchors: string[];         // top N anchors aggregated across N most-recently-engaged threads
  one_line_summary: string;         // ≤ 80 chars; auto-generated from recent thread summaries
  byte_size: number;                // for budget accounting; should be ≤ cross-project.digest-per-project-bytes
}
```

`one_line_summary` is generated by frequency-weighted concatenation of symbols across the project's recent threads.

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
layer.budget.percentages: {E: 8, A1: 8, A2: variable, B: 50, C: 15, current_turn: 15}
context.byte-budget: 65536          # total system-prompt byte budget; v0.1
                                    # uses bytes as a token proxy (see §6.5)
anchors.cap-per-thread: 6           # max anchor symbols per thread (within [4,8] hard range)
history.cap-per-thread: 40          # max history symbols per thread
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

**Initial event vocabulary** (will grow during implementation):

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
| `digest.refresh` | cross-project Layer A2 digest regenerated | digest content |
| `slash.injected` | `/cd-project`, `/back-to`, `/project switch`, etc. inject content | varies |
| `directive.reloaded` | a directive file changed and the runtime re-read it | merged parameter set |

Future capabilities (deep-cold recovery, dedup-promotion of identifiers
back to literal) add more event types via the same chain.

#### 3.0.2 The hook chain

Each context-modification event runs the chain, in this order:

1. **Symbol extraction** (§3.3) — deterministic regex pass over the
   delta's content extracts identifiers (file paths, URLs, claim IDs,
   user `#`-tags). Model-emitted symbols (in `topic` blocks) are also
   parsed here.
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

`last_engaged` updates to the *latest* delta's timestamp; `turn_count`
increments by 1 per turn per affected thread, regardless of how many
deltas in the turn touched its anchors.

#### 3.0.5 Implementation contract

The runtime exposes a single internal entry point — roughly
`onContextDelta(source, content, metadata)` — that every content-emitting
code path calls. The chain is not optional; there is no path by which
content reaches the working window without passing through it. Code
review treats any direct working-window mutation that bypasses this
entry point as a bug.

### 3.1 Working-set composition

The working set is rendered into the system prompt as five layers, each
truncated to a byte budget derived from `context.byte-budget` and the
`layer.budget.percentages` table (§2.6.1). Bytes serve as the token
proxy (see §6.5).

**Layer order and contents** (§2.1 maps these to on-disk sources):

| Layer | Source | Render |
|---|---|---|
| E    | `directives/defaults.md`, `directives/user.md`, `directives/<active>/project.md`, plus `ProjectMeta.ConventionsPaths` | Section-divided concatenation; YAML frontmatter is stripped from each directive file. |
| A1   | `spine.jsonl` filtered to `project == active` | One §2.2.2 display line per record. |
| A2   | `projects/<id>/digest.json` for every other project (excluding `prj_default`) | One line per project: `<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>`, sorted by `last_active` desc. Each line capped at `cross-project.digest-per-project-bytes`. |
| B    | `threads/<id>.md` for each id in the runtime's `ActiveThreads` LRU | Up to `layer.b-top-k` threads, most-recently-engaged first; each rendered as a heading + frontmatter line + body. Per-thread share = `LayerB / k`; oversized threads are individually truncated. |
| C    | `spine.jsonl` records for each id in the runtime's `DormantThreads` LRU | One §2.2.2 display line per record. |

**LRU update rule** (driven by the §3.0.4 turn-close path):

After per-turn engagements commit, for each engaged thread id:
- Remove from both `ActiveThreads` and `DormantThreads` if present.
- Prepend to `ActiveThreads`.
- If `len(ActiveThreads) > layer.b-top-k`, demote the tail to the head
  of `DormantThreads`.

`DormantThreads` is bounded by a count cap (default 20).

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
The re-prompt is capped at 1 per turn.

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

### 3.3 Symbol extraction (three passes)

Three passes per §3.0.2 step 1, ordered cheapest first:

1. **Deterministic** — regex over the delta (file paths, URLs, claim IDs, `#`-tags).
2. **Model-emitted** — anchors from the topic tag (§5.1) are folded in directly.
3. **Curator** — at retirement, the curator selects the final anchor set (§3.5) from the accumulated `history_symbols`.

### 3.4 Recall matching

Three layers, run as **parallel signals**, not a strict cost cascade:

1. **Symbolic Jaccard** over the symbol index — high precision, low
   recall. Cheap.
2. **Embedding cosine** over an in-memory thread-embedding index — the
   primary recall scan. Drift-robust. Cheap to match (cosine over a
   few hundred in-memory vectors); the cost is one embedding call per
   turn to vectorize the query.
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
hard failure. The thread-embedding index is
session-scoped and rebuilt at session start; persistence (a derived
KV store) is a later increment if rebuild cost warrants it.

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

### 3.5 Closure flow

Trigger detection (engagement decay or `/done`) → curator-drafted summary
→ user ack with resolution choice → frontmatter + spine update → Layer
B/C eviction. Deep cold archival (§3.8) is the next stage past closure
when spine cardinality pressure builds.

### 3.6 Fallback dissection

Budget-pressure trigger → dissector LLM clusters the oldest content →
batch retirement → ack flow.

### 3.7 Cross-project digest maintenance

`projects/<id>/digest.json` regenerates whenever any thread in the
project is updated. Each project's digest is capped at
`cross-project.digest-per-project-bytes`; recent anchors are selected
from the N most-recently-engaged threads.

### 3.8 Deep cold archival

Per §3.5, threads can be archived "off-spine" when spine cardinality
pressure builds, with recovery via explicit fetch. The mechanism leverages
the autonomic git layer (§8.3) rather than a bespoke archive format.

#### 3.8.1 Mechanism

To archive thread `thr_<n>`:

1. The runtime confirms `threads/thr_<n>.md` is in git's working tree at
   HEAD.
2. `git rm threads/thr_<n>.md`.
3. `git commit -m "archive thr_<n>"` — the commit captures the deletion.
4. Append an archive index entry to `archive/index.jsonl`:
   ```jsonl
   {"thr_id":"thr_42","commit_hash":"<sha>","blob_hash":"<sha>","archived_at":"<RFC3339>","original_path":"threads/thr_42.md","spine_summary":"<summary at archival time>","anchors":["..."],"project":"prj_3"}
   ```
   The `spine_summary` and `anchors` are preserved verbatim from the
   spine record at archival time so a `personant search` over archive
   entries can match without recovering the full thread.
5. Remove the spine record from `spine.jsonl` (the thread is no longer
   active recall material).
6. Commit the spine and archive-index changes together (one commit, so
   the archived state is atomic).

The archive index is **canonical** (per §2.1 ownership table); sorted by
`thr_id` for line-grain git diffs.

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

To recover archived thread `thr_<n>`:

1. Look up the archive index entry by `thr_id`.
2. `git show <commit_hash>:<original_path>` → write to working tree.
3. Compute `git hash-object` on the recovered file; verify against the
   stored `blob_hash`. Mismatch indicates index corruption — abort and
   surface the discrepancy as an error.
4. Append a fresh spine record (using current parameters; `state` is
   typically `wip` after recovery, with `last_engaged` updated).
5. Commit the recovery to the personant home git tree.

The archive index entry is **kept** as a forensic breadcrumb.
`personant archive list` / `personant archive recover` surface
deep-cold history.

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
project's git repo, its committed state is recoverable by commit hash, so
Personant's reverse-delta chain of the *pre-commit* edit history is pure
duplication of what git already holds. After a retention window
(`FileChainRetentionTurns` turns OR `FileChainRetentionDays` days,
whichever trips first), the chain is clock-aged out and only the commit
hash is retained as a recovery pointer. The forensic history of
*committed* states is therefore bounded by what the project git repo
already holds; uncommitted edit history is unaffected.

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
rest. Empty thread list or empty anchor list → not a valid topic tag;
warn-log and continue without engagement update for this delta.

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
   - Read `threads/thr_<n>.md` (frontmatter + body).
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

Personant is a **research-assistant runtime**, not a general-capability
agent. A research assistant **reads and thinks** rather than **builds
and runs** (see ARCHITECTURE.md for the full role framing); the
external tool surface follows from that role.

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

## 7. Go package layout

Current package layout is documented in `AGENTS.md` (Repo state); the
internal-package map there is the authoritative current shape.

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
recovery: if six months of accumulated usage reveals the storage
strategy or memory mechanics are structurally wrong, the choice is
between a complex, risky refactor of accumulated agent memory state or
losing all of it. The user's accumulated continuity is the property the
system exists to deliver in the first place — sacrificing it to
validate the architecture would defeat the purpose. The simulation
regime exists because the proof must come **ahead of time**, not after
six months of real use have built up the very state we'd be putting at
risk.

This section is therefore framed not as a quality gate bolted on after
features land, but as the **measurement instrument** by which we
validate the thesis and *evolve techniques iteratively until they
deliver*. Personant studies its own behavior; the test fabric is its
lab bench.

### 9.1 The six-month simulation (v0.1 acceptance gate)

The v0.1 acceptance criterion:

- **Simulate six continuous months** of realistic usage via a synthetic
  workload (§9.8) running against the real runtime with a mock LLM
  (§9.2) supplying canned responses.
- **Memory quality maintained** throughout, measured via the metrics
  emitted at every event (§9.6): recall hit rate, engagement accuracy,
  retirement timing, round-trip information-preservation through
  retire→archive→recover.
- **Zero out-of-context-space events.** The layered budget (E/A1/A2/B/C)
  must never overflow; bumpable layers must always free enough room
  before the next event lands. Any overflow is a fail.
- **Operation runtime costs measured and within bounds.** Wall-clock
  P50/P95/P99 latency for: engagement update, spine match, thread
  fetch, retirement, archival, recovery, index rebuild, index check.
  Bounds are calibrated empirically; "within bounds" means stable
  across the simulation, not exceeding a threshold that grows with
  accumulated state.
- **Steady-state demonstrated.** After six simulated months, the
  trajectory of working-set size, spine size, and per-operation
  latency should be **flat** — not creeping upward. The acceptance
  criterion is "the system is in a state from which it could run
  another six months without degradation," not "the system has
  survived six months."

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
six-month sim).

### 9.3 Unit testing

Standard Go `testing` patterns over `internal/store/`,
`internal/index/`, `internal/verify/`, etc. Targets:
- Pure-function correctness (JSONL round-trips, schema validation,
  symbol normalization, ID parsing).
- Atomic-write semantics, idempotency.
- No mock LLM needed at this layer; tests don't exercise the turn
  loop.

### 9.4 Scenario testing

Named lifecycle flows demonstrated end-to-end through the real
`onContextDelta` chain (§3.0). Each test is a story:
"thread A is created → engaged for 5 turns → retired → archived →
recovered → re-retired."

Scenarios drive the synthetic turn driver (`internal/testing/turn/`)
which pumps user-prompt + mock-LLM-response + faked tool-results
through production code paths. After each turn (or operation): assert
invariants from §9.5; emit metrics from §9.6.

Scenarios worth covering explicitly (initial set):

- **Single-thread lifecycle.** Create → engage 5 turns → retire →
  archive → recover → engage → re-retire → re-archive. Verify content
  survives the round-trip.
- **Multi-thread interleaving.** 5 threads alive simultaneously,
  alternating engagement; verify per-thread `turn_count` and
  `last_engaged` match the operation log.
- **Project switching.** Thread in project A → `/cd-project` to B →
  engage in B → switch back to A → re-engage. Project tags stay
  consistent.
- **Heavy retirement.** 50 threads, 30 retired, 20 archived, 10
  recovered. Verify spine cardinality, archive integrity, no orphan
  files.
- **Same-anchor collision.** Two threads with overlapping anchors;
  one retires; the other engages later via the shared anchor.
  Verify recall semantics.
- **Transient shell-capture content.** Several normal turns interleaved
  with `user.shell-capture` events carrying 1–2 pages of unique
  transient content (simulated `# cat ...`-style captures). Verify the
  transient content does not appear in any spine record's anchors /
  summary, does not bloat any thread's history_symbols (transient ≠
  anchored), and evicts cleanly from working-window layers under
  budget pressure. Same shape covers `tool.result` deltas with large
  payloads (`fs.read` of a long file, `web.fetch` of a long page).
- **Cross-boundary recovery.** Project remote URL added → identity
  promoted → original local-only project state preserved.

### 9.5 Invariant validators

Standalone validators (`internal/testing/invariants/`) callable from
any test:

- `VerifySpineIntegrity` — wraps `verify`; asserts exit 0.
- `VerifyIndexFresh` — wraps `index check`; asserts exit 0 (no drift
  between canonical and derived).
- `VerifyEngagementConsistency` — replay the operation log;
  reconstruct expected `turn_count` and `last_engaged`; compare.
- `VerifyArchiveResolvable` — every `archive/index.jsonl` entry's
  `commit_hash` resolves via `git show`, and the recovered blob
  matches the recorded `blob_hash`.
- `VerifyProjectReferences` — every `spine.project` resolves to a
  known `prj_<n>` or `prj_default`.
- `VerifyLastActiveValid` — `last-active` points to a known project.
- `VerifyNoBudgetOverflow` — replay turn-by-turn; assert no layer
  exceeded its allocation cap.
- `VerifyDedupConsistency` — content reachable via identifier
  references reconstructs to its canonical form.

Invariants are non-optional after every operation in churn tests
(§9.7); selectively after key checkpoints in scenario tests (§9.4).

### 9.6 Metrics emission

Every test (scenario, churn, calibration, simulation) emits a
machine-readable metrics blob (`internal/testing/metrics/`). Stable
JSON schema so cross-version comparison works.

Metrics worth capturing:

- **Recall fidelity.** Of N expected matches, how many fired?
  Precision/recall/F1 per measured step.
  - Ground truth: `Step.ExpectedRecallMatches []string` (scenarios
    harness); `nil` → step unmeasured.
  - Counters: `recall_fidelity_measured_steps`,
    `recall_fidelity_unmeasured_steps` (every step contributes
    exactly one).
  - Histograms (one observation per measured step):
    `recall_fidelity_precision`, `recall_fidelity_recall`,
    `recall_fidelity_f1`. Empty-set conventions: (E=∅, A=∅) → 1/1/1;
    (E=∅, A≠∅) → 0/1/0; (E≠∅, A=∅) → 1/0/0.
  - Mode: `Step.RecallMode` selects enforcement.
    - `RecallStrict` (default) — clean ground truth. Strict-set
      comparison; on mismatch the harness calls `t.Errorf` with the
      false-positive and false-negative sets so both precision and
      recall regressions surface in test logs. Feeds the
      `recall_fidelity_*` series above.
    - `RecallMeasureOnly` — adversarial probes (vocabulary drift,
      stop-word leak, false-friend pairs) whose under- or
      mis-firing is the measurement, not a defect. Records
      `recall_fidelity_adversarial_{precision,recall,f1}` histograms
      and the `recall_fidelity_adversarial_steps` counter; never
      fails the test. Regressions in these numbers surface through
      §9.9 baseline comparison, not a red unit test. Kept in a
      separate series so adversarial scores never dilute the clean
      aggregate.
- **Engagement accuracy.** Were tagged-engaged threads the actual
  active threads? (Compared against canonical-by-construction ground
  truth in synthetic scenarios.)
- **Retirement timing.** Lag between "thread effectively complete"
  (scenario marker) and "retirement prompt fired."
- **Budget pressure profile.** Peak fill, eviction count, layer
  displacement events.
- **Round-trip fidelity.** Archive → recover → diff against original.
  Information-preservation rate.
- **Dedup compression ratio.** Literal bytes vs. encoded bytes.
- **Symbol-extraction yield.** Deterministic-pass hits vs.
  model-emitted vs. curator-selected.
- **Operation latency** (per type): P50, P95, P99 wall-clock.

Metrics emit from every test run from the outset — they are
infrastructure, not instrumentation bolted on afterwards.

### 9.7 Churn testing

Randomized but seeded operation sequences
(`internal/testing/churn/`). A driver picks valid operations from a
weighted distribution (e.g., 80% engage existing, 10% create new, 5%
retire, 5% project switch); after each operation, the invariant suite
(§9.5) runs. Failures dump the operation log + seed for replay.

Churn drives generate sequences that exercise retirement, archival,
recovery, project switching, and cross-project recall as those
mechanisms come online.

### 9.8 Calibration testing

Same scenario, different parameter values
(`internal/testing/calibration/`). The harness sweeps a directive-file
parameter across a configured range and emits a metrics matrix:

```
calibrate recall.symbolic-threshold ∈ [0.3, 0.4, 0.5, 0.6]
          on scenario "cross-project-recall-100-turns"
→ metrics matrix (one row per threshold value)
→ best operating point identified by the metric we're optimizing for
```

This is how the bootstrap-default values in §2.6.1
(`recall.symbolic-threshold: 0.4`, `engagement.decay-turns: 8`, etc.)
earn their numbers empirically rather than by guess. The
directive-accrual mechanism (§2.6) and calibration scenarios are the
two ends of the same feedback loop: directives let the running system
learn from one user; calibration scenarios let the *project* learn
from canonical workloads.

### 9.9 Cross-run baseline comparison

`personant test report` (or equivalent CLI tool):

- Reads metrics output from a test run.
- Compares against a stored baseline (`testdata/baselines/<scenario>.json`).
- Highlights regressions and improvements.
- Optionally fails CI when key metrics regress beyond a threshold.

Baselines are git-tracked so the project's improvement trajectory is
itself version-controlled. This is what makes "iterating on the
techniques" actually work: every change is measured against the prior
baseline.

### 9.10 Six-month simulation harness

The capstone test (§9.1). Implementation
(`internal/testing/sixmonth/`):

- **Synthetic workload generator.** Realistic patterns of turn arrival
  (bursty with quiet periods), thread creation/engagement/retirement
  rates, cross-project workflow, configurable workload "shape"
  (researcher, software-engineer, mixed).
- **Logical-clock acceleration.** Six months of wall-clock time can't
  run in six months of test time. The simulation uses a logical clock
  that advances at a configurable rate (e.g., one simulated hour per
  100ms of real time). All time-based logic
  (`engagement.decay-time`, retirement triggers) consults the logical
  clock. Tests run in minutes.
- **Steady-state assertions.** Working-set size trajectory plateaus,
  not climbs. Spine cardinality grows but plateaus as retirement →
  archival keeps pace. Per-operation latency stays stable as
  accumulated state grows. The full-test pass criterion is in §9.1.
- **Operation-cost profiling.** Per-operation wall-clock latency
  histogram across the full simulation. Particularly: archival cost
  (git ops), recovery cost (git fetch + spine update), index rebuild
  cost as state grows.

Output: a single comprehensive metrics JSON + a human-readable
summary (`make six-month-sim` or similar). Pass/fail per §9.1
criteria.


