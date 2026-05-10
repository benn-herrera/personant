# Personant — v0.1 Specification

**Status:** in progress. §0–§2, §3.0, §3.8, §3.9, §4, §5.{1,5}, §6, §8.2, §11 substantively drafted; §3.{1–7}, §5.{2–4}, §7, §8.{1,3,4}, §9, §10 stubbed.
**Derives from:** [`outline.md`](outline.md).
**Audience:** implementation work. Specifies field-level schemas, algorithms, and surface APIs.
**Relationship to outline:** outline is the *why*; spec is the *what and how*. Where the outline says "the model emits a topic tag at turn start," the spec specifies the prompt fragment, the parser regex, and the parser's normalization rules.

---

## 0. Document conventions

- **JSONL schemas** are documented in TypeScript-style `interface` notation for clarity. The runtime implementation is Go; the TypeScript notation is purely descriptive.
- **Field markings:**
  - `field: T` — required.
  - `field?: T` — optional.
  - `[derived]` — regenerated from canonical sources; never hand-edited.
- **Pseudocode** is given in Go-flavored syntax where appropriate.
- **Open questions** are flagged inline with `[OPEN: ...]` and aggregated in §13.
- **Cross-references** use §-numbered section identifiers (e.g. "see §2.2").

---

## 1. System overview (one-page recap)

Personant is a single-user, single-agent runtime for managing an AI assistant's working memory across arbitrary projects with continuity. The agent has one continuous career: a unified persistent memory, no session boundaries, no compaction-driven information loss, cross-project recognition.

**Role: research assistant**, not general-capability agent. The constraint is load-bearing — see §6 for the bounded external tool surface and the rationale.

The architectural thesis: **deterministic state as canonical, LLM in narrow judgment roles, human acks at high-leverage moments only.**

| Tier | Role |
|---|---|
| Deterministic code (Go) | canonical state holder, integrity enforcer, build/query/index operations |
| LLM | narrow generative/judgment roles (topic tagging, summary drafting, anchor selection, recognition, dissection clustering) |
| Human | final ack at three moments (closure, opportunistic recall surfacing, fallback dissection trigger) |

Substrate: text files committed to git; JSONL for structured records; markdown for thread bodies and directive files; sub-millisecond in-memory query layer in Go; pre-commit hook enforces derived-index freshness.

Working set is layered (E + A1 + A2 + B + C + current turn) with explicit budget caps and decay-driven layer transitions. Recognition is model-native (spine in window); recall is opportunistic (deterministic match → user-acked surface prompt → explicit fetch). Closure is event-driven (engagement decay + ack). Fallback dissection handles overflow when normal retirement falls behind.

Success criterion: months-long seamless continuity. Externally a research partner picking up where work was left off; internally a substrate doing constant work to make that appearance honest.

**Operationalized acceptance (v0.1):** the system must pass a **six-month simulated workload** (§11.1) — sustained continuity of memory quality, zero out-of-context-space events, measured runtime costs for recall / retirement / archival / resurrection within bounds. After six simulated months the system must be in a state demonstrating it could run another six months without degradation. This is what makes "months-long continuity" a proven property rather than an aspiration.

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
  providers.toml                    # LLM provider config (canonical; secret-bearing — see §8.2)
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
| Canonical | `spine.jsonl` rows, `threads/*.md`, `directives/*.md`, `projects/prj_<n>/meta.json`, `logs/*.log`, `providers.toml`, `archive/index.jsonl` | Source of truth. Hand-editable. Other files derive from these. |
| Derived | `symbols.jsonl`, `projects/prj_<n>/digest.json` | Regenerable from canonical. Pre-commit hook fails if stale. Never hand-edited. |
| Operational | `logs/*.log`, `.git/`, `tmp/`, `last-active`, `history` | System-managed; not subject to drift checking. `tmp/`, `last-active`, and `history` are gitignored. |
| Secret-bearing | `providers.toml` | Contains API keys. Treated specially by the runtime: never included in any LLM-context artifact, log line, ack prompt, or captured output. See §8.2. |

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

`history_symbols` is hard-capped at `history.cap-per-thread` directive value (default 40). When the cap is exceeded, eviction policy is lowest cumulative weight (lowest `count` × recency factor) [OPEN: precise weight formula — defer to v0.1.1 once we have empirical data].

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

`one_line_summary` generation [OPEN: heuristic vs LLM-curated; v0.1 starts with frequency-weighted symbol concatenation, may move to LLM-curator if quality is poor].

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

[OPEN: full parameter table. Initial seed from outline §"Bootstrap defaults". Will expand as implementation progresses.]

```yaml
engagement.decay-turns: 8           # turns of non-engagement before closure prompt fires
engagement.decay-time: 7d           # wall-clock equivalent (Go duration string)
recall.symbolic-threshold: 0.4      # Jaccard threshold for opportunistic recall surfacing
recall.cross-project-threshold: 0.5 # higher bar for cross-project surface
layer.b-top-k: 3                    # max active threads in Layer B
layer.budget.percentages: {E: 8, A1: 8, A2: variable, B: 50, C: 15, current_turn: 15}
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
  // | "phrasal"   // [DEFERRED to v0.2]
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
| `recall` | `surface-prompt`, `surface-ack`, `surface-decline-not-relevant`, `surface-decline-not-now`, `surface-decline-stop-offering`, `cross-project-fire` |
| `retire` | `prompt`, `ack`, `defer`, `complete` |
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
review on Phase 2 should treat any direct working-window mutation that
bypasses this entry point as a bug.

### 3.1 Working-set composition

[STUB — see §3.0.2 step 4 for the budget-check hook; full composition
algorithm draft lands with Phase 2 implementation.]

### 3.2 Topic tagging and engagement update

[STUB — see §3.0.2 step 2 and §3.0.4 for the engagement model; topic-tag
parsing detail lands with Phase 2.]

### 3.3 Symbol extraction (three passes)

[STUB — see §3.0.2 step 1 for the per-event hook context; full
three-pass algorithm (deterministic / model-emitted / curator) lands as
implementation requires.]

### 3.4 Recall matching

[STUB — symbolic Jaccard match (cheap pre-filter), embedding similarity (mid-cost), model judgment (expensive last resort).]

### 3.5 Closure flow

[STUB — trigger detection (engagement decay or `/done`), curator-drafted summary, user ack with resolution choice, frontmatter+spine update, Layer B/C eviction. Deep cold archival (§3.8) is the next stage past closure when spine cardinality pressure builds.]

### 3.6 Fallback dissection

[STUB — budget-pressure trigger, dissector LLM clustering of oldest content, batch retirement, ack flow.]

### 3.7 Cross-project digest maintenance

[STUB — regeneration triggers, per-project byte budget enforcement, recent-anchor selection.]

§3.0, §3.8, and §3.9 are substantively drafted; §3.1–§3.7 are stub.

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
  rewrites history (see watch-list §12 entry on this invariant).
- Periodic `git gc --aggressive` can be invoked on a schedule when
  pack-file size starts mattering. Storage pressure isn't a v0.1
  concern.

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

The archive index entry is **kept** as a forensic breadcrumb. A future
`personant archive list` / `personant archive recover` command surfaces
deep-cold history.

#### 3.8.4 Relationship to other deferrals

This obsoletes the outline's "Archives off-spine" handwave: the
mechanism is now concrete. The outline's "Symbol decay over time"
deferral remains separate — that's about evicting *symbols* from the
inverse index when they go cold, not about archiving threads.

[OPEN: archival trigger threshold ("spine cardinality pressure" is
hand-wavy). Calibrate empirically; defer to v0.2.]

### 3.9 Working-set content dedup

When file content (or other large content blobs) appears in the working
context multiple times — a file is read, then modified, then re-read —
the runtime de-redundifies content to keep the working window's
information density high.

The key observation: **persistent storage and live context composition
are separate concerns** sharing the same primitives — content-addressed
identifiers, diffs, anchor literals.

#### 3.9.1 Persistent storage (thread file body, log)

The full chain is preserved:

- Initial content read → stored as literal.
- Subsequent change → stored as a unified diff against the previous
  state.
- Periodic anchor literals every K-th change (default K = 10) — full
  literal stored to prevent cumulative drift through long diff chains.
  Same trick I-frames in video compression use. Configurable via the
  directive `dedup.anchor-cadence`.
- A diff that is ≥ `dedup.diff-literal-threshold` (default 0.7) of the
  literal size is stored as a literal instead — the diff isn't earning
  its keep. Same heuristic as lzma's literal-vs-match decision.

This gives forensic-quality history with bounded storage cost.

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

The empirical-calibration plan: ship unified-diff-only; instrument
"diff misapplication" signals (cases where the model's post-state
guess diverges from the runtime's deterministic apply); switch to
natural-language fallback when frequency warrants. v0.2 work.

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

[STUB — initial command list; flag/option detail TBD.]

Initial commands (Go binary subcommands; see also §2.1):

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

The catalog grows during v0.1 implementation. `/back-to` semantics remain
an open question (§13).

### 4.3 Decline categorization UI

[STUB — three-button surface: not-relevant / not-now / stop-offering. See §3.4 for the accrual semantics.]

### 4.3.1 REPL line editing and history

**Currently absent.** The chat REPL reads input via stdlib cooked-mode
line-buffered I/O, which gets the user backspace and Ctrl-U/W
line-clearing for free but **no** in-line cursor movement, history
recall, or tab completion.

**Required for v0.1 polish** (lands before the six-month simulation
acceptance gate at §11.1):

- `←` / `→` cursor movement within the current line.
- `↑` / `↓` command history; persistent across sessions in
  `<Home>/history` (operational, not git-tracked).
- Standard readline shortcuts: Ctrl-A / Ctrl-E (line start/end),
  Ctrl-W (kill word), Ctrl-U (kill line), Ctrl-K (kill to end),
  Alt-B / Alt-F (word-back / word-forward).
- History deduplication (no consecutive duplicate entries).
- History size cap (configurable; default 1000 entries).

Tab completion deferred to v0.2 — slash-command names and active
project IDs are obvious candidates but the surface needs settling.

[OPEN: implementation strategy. Options: (a) `golang.org/x/term` +
custom minimal readline (~500–800 LoC; full control; one std-adjacent
dep); (b) `github.com/peterh/liner` (small, BSD-2, well-maintained,
~1k LoC dep); (c) `github.com/chzyer/readline` (bigger, more features,
MIT). All compatible with the substrate-memory "approved-when-earned"
framing; the consumer (chat REPL) is real. Decide before
implementation lands.]

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
**not supported in v0.1**. Owning an interactive shell process is not the
same as wiring the user's terminal to that shell's PTY for arbitrary
sub-applications, which requires terminal-mode handoff (raw mode + signal
forwarding + state restoration). If the user wants `vim`, they suspend
personant or open another terminal.

[OPEN: revisit if empirical pressure makes this onerous; see §13.]

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

[OPEN: collisions in step 1 — if a CWD's remote URL matches
`historical_remote_urls` of multiple projects (rare; only if remote
URLs were merged or a fork was registered as a separate project),
prompt for disambiguation. Defer the prompt format to v0.2.]

---

## 5. Model interaction

§5.1 and §5.5 are substantively drafted; §5.2–§5.4 are stub.

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
start. The exact template lives in `internal/prompt/template.go` (Phase
2 deliverable); the template is hot-reloadable for empirical tuning.

[OPEN: format reliability calibration. Smaller models may fail to emit
tags consistently. Fallback: runtime detects missing tag, retries with
a more directive system prompt; last-resort, infers engagement from
symbol-anchor overlap with the response body. v0.2 work if needed.]

### 5.2 Curator prompt for retirement summary

[STUB — Phase 4 retirement work.]

### 5.3 Dissector prompt for fallback clustering

[STUB — Phase 5 dissection work.]

### 5.4 Recognition-context construction

[STUB — Phase 2 prompt-template work; covers spine + active threads + project conventions assembly into the system prompt.]

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

[OPEN: re-prompt cost. Each fetch implies a re-roundtrip to the LLM.
Speculative pre-fetch was rejected in the outline as an explicit
non-goal (every loaded thread is loaded because the model said it was
needed), so re-prompt cost is the path; quantify the latency penalty
during Phase 2 instrumentation. The six-month simulation (§11.1) will
expose the steady-state impact.]

---

## 6. Tool surface and permissions

Personant is a **research-assistant runtime**, not a general-capability agent.
The constraint is load-bearing — not a v0.1 limitation that lifts later, but a
deliberate boundary for two reasons:

1. **Focus.** The load-bearing innovation is the context-management mechanism
   (continuity across projects, drift-resistant memory, opportunistic recall).
   Bounding the action surface lets v0.1 actually exercise that mechanism
   instead of being absorbed into re-implementing the broader agent-coding
   surface area.
2. **Anti-folly.** Reproducing a fully-featured agent-coding system (a
   multi-year effort) is unnecessary at this scope, infeasible at this scale,
   and *undesirable* — that class of system represents an engineering failure
   mode worth not reproducing.

A research assistant **reads and thinks** rather than **builds and runs**. The
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
| `web.search` | `(query) → results[]` | search query → URLs (provider TBD; see §13) |
| `model.consult` | `(prompt, model_id) → response` | ask a guest model (formalizes the existing `guest-liaison` pattern) |

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
  tree: `git init` on first run, `git add`/`commit` of canonical mutations
  with structured messages, and pre-commit hook installation/invocation. Same
  lifecycle status as writing to `spine.jsonl` itself — no user ack, no LLM
  involvement. See §8.3 for hook details.
- **Read-only git queries on the workspace.** The runtime issues `git
  ls-files`, `git status`, and `git diff` against the workspace tree to
  classify tracked-vs-untracked status (input to the permission tiers in
  §6.2) and to render diffs in ack prompts. The runtime never runs *mutating*
  git on a workspace tree — the user owns workspace git.

If empirical pressure shifts the role definition these may be reconsidered.
They do not enter scope by accident.

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

Granularity dimensions in v0.1:

- `action`: one of `promote-new`, `promote-overwrite`, `propose_rename`, `propose_delete`
- `path`: glob pattern relative to project root

Per-thread, per-session, and time-bounded grants are out of scope for v0.1;
all standing grants are permanent until the directive is edited.

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

[OPEN: cap value, head/tail ratio, pagination semantics — calibrate with use; see §13.]

---

## 7. Go package layout

[STUB.]

Sections planned:

- 7.1 Top-level structure (`cmd/personant`, `internal/...`, `pkg/...`)
- 7.2 Package responsibilities (storage, index, working-set, prompt, model, curator, dissector, cli, log, directive, tool)
- 7.3 Inter-package interfaces

---

## 8. Bootstrap and lifecycle

### 8.1 First-run scaffolding (`personant init`)

[STUB — semantics TBD with implementation. Idempotent; creates the directory layout from §2.1; runs `git init`; writes seed `directives/defaults.md`, `README.md`, and an empty `providers.toml` with a commented-out example block.]

### 8.2 Configuration sources and precedence

The runtime reads configuration from three layers, each with distinct purpose and security posture:

#### 8.2.1 Provider configuration: `providers.toml`

LLM provider connectivity lives in `$PERSONANT_HOME/providers.toml`. This file is **canonical, hand-editable, and secret-bearing** — it carries API keys.

Format:

```toml
[provider-name]
baseUrl      = "https://api.example.com/v1"
apiKey       = "sk-..."
defaultModel = "gpt-5.4-2026-03-05"
```

One TOML table per provider. The provider name is the lookup key used by `/model <provider>:<model>` (or `/model <provider>` to use that provider's `defaultModel`) and by the runtime's outbound LLM-client wiring.

**Security boundary (load-bearing):**

- `providers.toml` content is **never** included in any LLM context, log line, ack prompt, or captured shell output. The runtime is the only consumer; it resolves a provider name → `baseUrl`/`apiKey`/`defaultModel` at the moment of an outbound API call and that's where the key material lifecycle ends.
- The file is not git-committed inside `~/.personant/`'s git tree by default — `.gitignore` in the home tree excludes it. (Optional opt-in for users who want their own private repo containing it; out of scope for v0.1.)

**Hybrid redaction policy** (refuse vs. redact, by initiator):

- **Agent-initiated reads** (`fs.read`, `fs.grep`, `fs.list` listing the file's parent dir) targeting `providers.toml` (after symlink resolution): the runtime **refuses outright** — the tool returns a deny-error to the model with a message like `permission denied: providers.toml is secret-bearing and cannot be read by the agent`. Logged as `permissions.suspicious-access` (the agent should not be reaching for this file under normal operation; reaching for it at all is signal worth surfacing).
- **User-initiated captures** (`#`-prefixed shell commands per §4.4 whose output contains content from `providers.toml`): the runtime **redacts before reaching context** — captured output is replaced with `[redacted: providers.toml content]`. The user's terminal still sees the unredacted output (the `$`-prefixed equivalent is unaffected; `$` doesn't capture into context); only the path-into-LLM-context is filtered. Logged as `permissions.redaction-fire`.

The split reflects intent: an agent reaching for secrets is suspicious and warrants refusal; a user `#`-grepping their own home dir for context inclusion is reasonable but accidental in this case and just gets quietly cleaned up.

#### 8.2.2 Environment variables

[STUB.] At minimum: `PERSONANT_HOME` overrides the home directory (resolved in §2.1's `$PERSONANT_HOME`). Other env-var overrides as needed during implementation.

#### 8.2.3 Directive precedence

Detailed in §2.6. Briefly: `directives/defaults.md` < `directives/user.md` < `directives/prj_<n>/project.md`. The runtime walks the precedence chain at parameter-read time and returns the first match.

### 8.3 Pre-commit hook installation and behavior

[STUB.] Hook is installed at `.git/hooks/pre-commit` inside `$PERSONANT_HOME/` by `personant init`. On commit attempt, regenerates derived files (`symbols.jsonl`, `projects/prj_<n>/digest.json`); fails the commit if regeneration produces output different from what's currently checked in (drift detected).

### 8.4 Upgrade path placeholder

[STUB. Deferred per outline; v1.0+ concern.]

---

## 9. Logging conventions

[STUB.]

Sections planned:

- 9.1 Format conventions (already partially specced in §2.8; §6.4 adds the `tool` and `permissions` categories)
- 9.2 Event vocabulary (will grow incrementally)
- 9.3 Rotation and archival

---

## 10. Implementation milestones

[STUB.]

Anticipated phases:

- **Phase 1: Skeleton + storage.** `init`, JSONL read/write, schema validation, pre-commit hook, `verify`.
- **Phase 2: Turn loop + topic tagging.** Model prompt augmentation, response parsing, thread engagement update, basic working-set composition.
- **Phase 3: Symbol extractor + recall.** Three-pass extraction, symbol index build, opportunistic recall surface (within-project only).
- **Phase 4: Closure + retirement.** Engagement decay tracking, closure prompts, curator-drafted summaries, retirement frontmatter+spine update.
- **Phase 5: Cross-project + dissection + personalization.** Cross-project digest, fallback dissection, directive file accrual mechanism.

Each phase is independently shippable as a working subset; later phases extend rather than replace.

**v0.1 acceptance gate:** the six-month simulation (§11.1, §11.10) passes. Phase 5 isn't "feature complete and ship"; it's "feature complete, simulation green, and the runtime is in a steady state that demonstrates another six months of equivalent operation." Each phase before 5 builds toward enabling the simulation: Phase 2 brings the mock LLM, scenario harness, and metrics emission online; Phase 3+ scenarios extend to cover the full lifecycle; Phase 5 is when the simulation can run end-to-end with all mechanisms exercised.

### 10.1 Beyond v0.1 (tracking, not committed scope)

Capabilities that expand the role beyond v0.1's read-and-think research
assistant. Noted so v0.1 implementation does not preclude them; not on the
near-term dev list.

- **v0.2: Deep cold archival via git** (per §3.8). Triggered by spine
  cardinality pressure (threshold TBD); leverages the autonomic git
  layer for storage and recovery. Adds the archive index
  (`archive/index.jsonl`), the archival trigger logic, and `personant
  archive list` / `personant archive recover` CLI commands. Storage is
  effectively free because git's object store handles compression and
  deduplication across historical versions.
- **v0.2: Working-set content dedup** (per §3.9). Live context window
  uses content-addressed identifiers + recent-N diffs + literal anchors;
  persistent storage (thread file body, log) preserves the full chain
  with periodic anchors. Includes the diff-format selector (unified
  default, natural-language fallback), the directive parameters
  (`dedup.anchor-cadence`, `dedup.diff-literal-threshold`,
  `dedup.live-diff-window`, `dedup.diff-format`), and the instrumentation
  to surface "diff misapplication" signals for the unified→NL switchover
  decision.
- **v1.0: Computational research workflow for math/physics projects.**
  Confirmed need, not speculative — math and physics work requires
  Python simulation to **validate** analytical claims, **verify** known
  results, and **explore** design space. Scope is deliberately **Python-only**;
  other coding languages are explicitly out of scope (we are not building
  a polyglot agent-coding tool — see §6.1.3 framing about general-agent
  scope creep). Python source-code authoring is already supported in v0.1
  via `fs.tmp_write` + `fs.propose_promote`; v1.0 adds *execution*:
  - `python.run <script> [args...]` — execute a Python script; capture stdout/stderr.
  - `python.format <path>` — apply `black` + `isort`.
  - `python.lint <path>` — run `flake8`.
  - `python.repl` (TBD) — session-persistent REPL for interactive exploration.

  Requires a sandboxed subprocess execution surface, stdout/stderr
  capture into the engaged thread (subject to the §6.5 budget policy),
  resource limits (wall-clock, memory, output bytes), and lifecycle
  management (cancellation, orphan reaping). The Python-environment
  policy (system Python on `$PATH` vs runtime-managed per-project venv
  vs `uv` integration) is a v1.0 design question — see watch-list.

  The shell-execution exclusion in §6.1.3 lifts **only** for this
  targeted Python capability when it lands — not as a general "agent can
  now run anything." Held until the v0.1 memory-context mechanism is
  proven; the six-month simulation (§11.1) does **not** need to exercise
  Python execution to pass.

---

## 11. Measurement and validation regime

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

### 11.1 The six-month simulation (v0.1 acceptance gate)

The v0.1 acceptance criterion:

- **Simulate six continuous months** of realistic usage via a synthetic
  workload (§11.8) running against the real runtime with a mock LLM
  (§11.2) supplying canned responses.
- **Memory quality maintained** throughout, measured via the metrics
  emitted at every event (§11.6): recall hit rate, engagement accuracy,
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

### 11.2 Mock LLM client

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

### 11.3 Unit testing

Standard Go `testing` patterns. Existing pattern from Phase 1
(`internal/store/`, `internal/index/`, `internal/verify/`). Targets:
- Pure-function correctness (JSONL round-trips, schema validation,
  symbol normalization, ID parsing).
- Atomic-write semantics, idempotency.
- No mock LLM needed at this layer; tests don't exercise the turn
  loop.

### 11.4 Scenario testing

Named lifecycle flows demonstrated end-to-end through the real
`onContextDelta` chain (§3.0). Each test is a story:
"thread A is created → engaged for 5 turns → retired → archived →
recovered → re-retired."

Scenarios drive the synthetic turn driver (`internal/testing/turn/`)
which pumps user-prompt + mock-LLM-response + faked tool-results
through production code paths. After each turn (or operation): assert
invariants from §11.5; emit metrics from §11.6.

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
- **Cross-boundary recovery.** Project remote URL added → identity
  promoted → original local-only project state preserved.

Scenarios extend through Phase 2 and 3 as features land.

### 11.5 Invariant validators

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
  references reconstructs to its canonical form. (Phase 2+.)

Invariants are non-optional after every operation in churn tests
(§11.7); selectively after key checkpoints in scenario tests (§11.4).

### 11.6 Metrics emission

Every test (scenario, churn, calibration, simulation) emits a
machine-readable metrics blob (`internal/testing/metrics/`). Stable
JSON schema so cross-version comparison works.

Metrics worth capturing:

- **Recall fidelity.** Of N expected matches, how many fired?
  Precision/recall.
- **Engagement accuracy.** Were tagged-engaged threads the actual
  active threads? (Compared against canonical-by-construction ground
  truth in synthetic scenarios.)
- **Retirement timing.** Lag between "thread effectively complete"
  (scenario marker) and "retirement prompt fired."
- **Budget pressure profile.** Peak fill, eviction count, layer
  displacement events.
- **Round-trip fidelity.** Archive → recover → diff against original.
  Information-preservation rate.
- **Dedup compression ratio.** Literal bytes vs. encoded bytes
  (Phase 2+).
- **Symbol-extraction yield.** Deterministic-pass hits vs.
  model-emitted vs. curator-selected.
- **Operation latency** (per type): P50, P95, P99 wall-clock.

Metrics are emitted from day 1 of Phase 2 — bolting them on later is
much more expensive than building them in.

### 11.7 Churn testing

Randomized but seeded operation sequences
(`internal/testing/churn/`). A driver picks valid operations from a
weighted distribution (e.g., 80% engage existing, 10% create new, 5%
retire, 5% project switch); after each operation, the invariant suite
(§11.5) runs. Failures dump the operation log + seed for replay.

Churn coverage scales as features land. By Phase 3, churn drives
should generate sequences that include retirement, archival, recovery,
project switching, and cross-project recall.

### 11.8 Calibration testing

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

### 11.9 Cross-run baseline comparison

`personant test report` (or equivalent CLI tool):

- Reads metrics output from a test run.
- Compares against a stored baseline (`testdata/baselines/<scenario>.json`).
- Highlights regressions and improvements.
- Optionally fails CI when key metrics regress beyond a threshold.

Baselines are git-tracked so the project's improvement trajectory is
itself version-controlled. This is what makes "iterating on the
techniques" actually work: every change is measured against the prior
baseline.

### 11.10 Six-month simulation harness

The capstone test (§11.1). Implementation
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
  accumulated state grows. The full-test pass criterion is in §11.1.
- **Operation-cost profiling.** Per-operation wall-clock latency
  histogram across the full simulation. Particularly: archival cost
  (git ops), recovery cost (git fetch + spine update), index rebuild
  cost as state grows.

Output: a single comprehensive metrics JSON + a human-readable
summary (`make six-month-sim` or similar). Pass/fail per §11.1
criteria.

### 11.11 Live instrumentation as testing

The runtime log (`logs/YYYY-MM-DD.log`) is itself a test fixture for
real sessions. Future capability: replay-based regression — record a
real user session's event log, replay it against a candidate runtime,
verify behavior matches the recorded baseline within tolerance. Out of
scope for v0.1 but the log format (§2.8) is designed to enable it.

---

## 12. Watch list (carried from outline)

1. **Synonym fragmentation** — same concept emitted as different surface forms. Most likely v0.2 driver.
2. **Anchor selection quality at retirement** — track `recall_fires` per anchor; anchors that never fire over time are dead weight.
3. **Cross-thread symbol collision** — single-symbol matches return ambiguous threads; may force richer matching (combinations).
4. **Low-information symbol leakage** — generic words past stopword filters.
5. **Model emission drift** — silent behavior changes affecting symbol consistency.
6. **Pattern coverage in deterministic pass** — per-project regex curation.
7. **Decline-categorization accrual fidelity** — false signals if users dismiss prompts without categorizing properly.
8. **Permission over-grant via accidental keystroke.** A misfired `[d]`/`[p]` permanently widens scope. Mitigation: every grant is logged; `personant permissions list` shows accrued grants; revocation is editing the directive file.
9. **Stale permission grants.** A directory the user no longer uses still has standing permission. Probably ignorable in v0.1; the file simply isn't touched.
10. **Cross-project grant leakage.** A grant scoped to project A doesn't auto-extend to project B even if directory paths overlap. Project scoping must be explicit at the path-glob level.
11. **Tool-output budget pathology.** Repeated near-cap tool results may displace thread content; instrumentation should surface fraction-of-budget consumed by tool I/O per turn.
12. **Remote URL normalization edge cases.** Gerrit URLs, custom SSH hosts, IDN hostnames, mirrored URLs. Initial normalization handles the common cases (lowercase host, strip `.git`, `git@` ↔ `https://`); expect long-tail surprises.
13. **Remote-collision merge fidelity.** Two stubs converging onto the same upstream is the easy case (one has threads, the other doesn't, or both are nascent). Merging two projects that have *already* accumulated divergent threads + anchors is the hard case — symbol-history reconciliation and possible rename collisions in `name`. Instrument to discover the empirical frequency before designing the merge algorithm in detail.
14. **File-move detection false negatives.** A project root that was *renamed in place* but otherwise untouched will trigger ENOENT identically to a relocation; the runtime prompt covers it, but a parent-directory scan heuristic might offer better UX.
15. **API-key leakage via capture-into-context.** `providers.toml` is secret-bearing (§8.2.1); `#`-prefixed shell capture, `fs.read`, `fs.grep`, log accidental inclusion — any path that surfaces file content into the LLM working window — must redact. The redaction filter is a single chokepoint in principle, but its coverage is the watch item: every new capture/inclusion path must be audited against this rule.
16. **Sensitive-pattern drift.** The seed sensitive-pattern list (`.env`, `secrets/**`, `*.key`, `*.pem`) plus `providers.toml` will not cover everything. As empirical pressure surfaces new patterns (`.npmrc` auth tokens, `~/.aws/credentials`, `id_rsa*`, etc.), the list must grow.
17. **Deep-cold blob reachability under `git gc`.** Archived blobs stay reachable as long as personant never rewrites history in `~/.personant/.git/`. Any future tool or accidental command that runs `git rebase` / `git filter-branch` / `git reflog expire` against the personant home could prune archived content. Mitigation: document the never-rewrite invariant in §8.3; have `personant verify` resolve every archive-index `commit_hash` and `blob_hash` to confirm reachability.
18. **Diff-chain cumulative drift.** Long chains of diffs reconstructed end-to-end can accumulate small errors if intermediate diffs are computed approximately or with a slightly drifted base. Mitigation: periodic literal anchors (§3.9.1, default K=10). Verification: walk the chain forward from each anchor and confirm intermediate states match expected literal at each anchor point.
19. **Python execution environment policy (v1.0).** Math/physics simulation requires arbitrary Python deps (`numpy`, `scipy`, `matplotlib`, etc.); the agent cannot impose stdlib-only on the user's research code (the stdlib-only constraint applies to personant's own auxiliary scripts, not user-written Python). Three options to weigh during v1.0 design: (a) use whatever `python` is on `$PATH` in the project's directory (simplest, least deterministic); (b) per-project venv managed by the runtime (more determinism, more lifecycle code); (c) leverage `uv` or similar modern tooling (modern, adds a hard external dependency). Decide before `python.run` lands.

---

## 13. Open questions

Compiled from inline `[OPEN: ...]` markers and design-pass uncertainties.

1. **§2.3 — `history_symbols` eviction weight formula.** Defer concrete formula to v0.1.1 with empirical data; v0.1 uses count alone.
2. **§2.5.2 — `one_line_summary` generation.** Frequency-weighted symbol concatenation vs. LLM-curator. Start with the former; may upgrade if quality is poor.
3. **§2.6.1 — Full parameter namespace.** Initial seed exists; will grow during implementation.
4. **§3.5 — Closure prompt phrasing variants.** Calibrate empirically; expose via directive file once tuning becomes useful.
5. **§3.6 — Fallback dissection batch size.** How many clusters max per dissection event? Likely 3-5 to keep ack burden manageable.
6. **§7 — Package boundaries.** Concrete Go layout — to be drafted with package skeletons as Phase 2 lands its first packages (prompt, model, workset, turn).
7. **`/back-to` semantics.** Resume thread or just reload context? Probably both as separate commands.
8. **Multi-project digest budget.** How much total budget for Layer A2 when many projects exist? Flat per-project cap (current spec) vs. dynamic allocation.
9. **§6.1.1 — Web search provider.** Brave / DuckDuckGo / no-search-in-v0.1. Suggest deferring `web.search` until empirical pressure proves `web.fetch` alone is insufficient.
10. **§6.5 — Tool-output cap value and head/tail ratio.** 8 KB starting guess; calibrate with use.
11. **§6.1 — PDF support.** Research consumes papers; `web.fetch` returns bytes. Whether to ship a thin PDF extractor in v0.1 or defer until empirical pressure.
12. **§6.2.5 — Batch threshold.** 5 files is an initial guess. Smaller (3?) might be safer; larger (10?) less friction. Calibrate.
13. **§4.5 — Project marker file.** A `.personant-project` file at the project root would make rebinding bulletproof when there is no git remote. Defaults to no marker file in v0.1 (don't pollute the user's workspace), with the user-prompt fallback handling missed rebinds. Reconsider in v0.1.1 if local-only projects empirically thrash on `/cd-project` resolution.
14. **§4.5.4 — Polling cadence for git remote detection.** Per-turn vs. on-engagement vs. on-explicit-trigger. Default to on-engagement to keep cost down; revisit if remote changes are common enough that lag becomes noticeable.
15. **§4.4.3 — Interactive-app handoff.** Whether to support `vim`/`nano`/`less` via terminal-mode handoff, or keep deferring to "open another terminal." Likely held until empirical pressure makes the friction onerous.
16. **§3.8 — Deep-cold trigger threshold.** "Spine cardinality pressure" is hand-wavy. Default likely 500–1000 active threads; calibrate empirically once enough spine accumulates to feel pressure.
17. **§3.9 — Live context dedup tuning.** `dedup.live-diff-window` (default 3), `dedup.anchor-cadence` (default 10), `dedup.diff-literal-threshold` (default 0.7) are starting guesses. All TBD; instrumentation drives calibration.
18. **§3.9.3 — Diff format empirical fallback trigger.** When does unified diff become harder for an LLM to apply correctly than a natural-language descriptive diff? Probably multi-region changes >50 lines and/or files >2000 lines, but this needs the model + content combination to confirm. Instrument and calibrate.
19. **§5.1 — Topic-tag emission reliability across models.** Smaller models may fail to emit tags consistently. Fallback strategy is sketched in §5.1.3; calibrate during Phase 2 with the local llama-server target.
20. **§5.5 — Re-prompt latency cost from system-injected fetches.** Each thread fetch implies a re-roundtrip to the LLM. Quantify steady-state impact in the six-month simulation (§11.1).
21. **§11.1 — Six-month simulation pass thresholds.** What are the concrete numeric bounds for "memory quality maintained" and "operation runtime within bounds"? Initial pass: derive thresholds from the first end-to-end simulation run; subsequent runs must not regress beyond a percentage. The first pass establishes the baseline.
22. **§4.3.1 — Readline implementation strategy.** Three viable options (custom on `golang.org/x/term`, `peterh/liner`, `chzyer/readline`). Decide before line-edit/history support lands. Required for v0.1 polish; not blocking earlier Phase 2 work.

---

## Document changelog

- 2026-05-08 — initial draft (§0–§2 substantive; §3+ stubs; watch list / open questions carried from outline / inline markers).
- 2026-05-09 — §6 "Tool surface and permissions" drafted; subsequent sections renumbered §6→§7 through §12→§13. §1 mentions research-assistant role. Watch-list items 8–11 and open questions 11–14 added from the same design pass. §6.1.3 clarified: exclusion is at the LLM-tool layer; runtime performs git operations autonomically on `~/.personant/` and read-only git queries on the workspace.
- 2026-05-09 — §10.1 "Beyond v0.1" added to track v1.0 computational tool surface (Python execution + black/isort/flake8) as a planned but uncommitted post-v0.1 role expansion. Outline mirrors the deferral.
- 2026-05-09 — §4 expanded substantively: §4.1 CLI command list seeded; §4.2 slash command catalog (incl. `/cd-project`, `/project switch|rename`, `/model`, `/stats`); new §4.4 "Shell escape (`$` and `#`)" with long-lived interactive shell subprocess (interactive apps deferred); new §4.5 "Project identity, project root, and shell cwd" with dual-cwd model. §6.2 gains §6.2.6 "Active-project boundary as a tier axis." §2.8 vocabulary adds `user.*` and `project.*` categories.
- 2026-05-09 — Project identity model revised to use stable internal handle (`prj_<n>`) + canonical external identity (normalized git remote URL) + mutable display name. §2.1 storage layout uses `projects/prj_<n>/` (and parallel for project-scoped directives). §2.2 spine `project` field is now the prj id. §2.5.1 ProjectMeta gets `id`, `current_root_path`, `historical_root_paths`, `remote_urls`, `historical_remote_urls`. §4.5 expanded to specify three identity layers, `/cd-project` resolution flow, local→remote promotion, file-move detection, and `/project rename`. §2.8 vocabulary extends `project.*` with `renamed`, `remote-adopted`, `remote-updated`, `remote-collision-prompt`. Watch-list items 12–14 and open questions 15–17 added.
- 2026-05-09 — `providers.toml` added to §2.1 storage layout (canonical, secret-bearing) and `tmp/` added (operational, gitignored). New "Secret-bearing" file ownership tier introduced. §8.2 substantively drafted: §8.2.1 covers `providers.toml` format and the security boundary (no inclusion in any LLM-context artifact; redaction rules for `#` capture and `fs.read`/`fs.grep`); §8.2.2 / §8.2.3 stubs for env-var and directive-precedence (latter cross-references §2.6). §8.3 stub clarified for pre-commit hook scope. Watch-list items 15–16 and open question 18 added.
- 2026-05-09 — §3.8 "Deep cold archival" drafted: thread archival mechanism leveraging the autonomic git layer (delete + commit + archive index entry); recovery via `git show <commit>:<path>` with blob-hash verification; storage properties rely on git's content-addressed object store and delta-compressed packfiles. §3.9 "Working-set content dedup" drafted: persistent storage preserves the full diff chain with periodic literal anchors (default K=10) and a literal-vs-diff threshold (default 0.7); live context window keeps literal current + N most-recent diffs (default N=3) + content-addressed identifiers for older states; diff format defaults to unified diff with a planned natural-language fallback for cases where the model has trouble applying. §10.1 gains two v0.2 milestones (deep cold, dedup); watch-list items 17–18 and open questions 19–21 added.
- 2026-05-09 — §3.0 "Context-modification events" drafted as the architectural primitive that §3.1–§3.4 and §3.9 hang off of. Frames the working window as a sequence of deltas (user.prompt, model.response, tool.result, user.shell-capture, thread.fetched, digest.refresh, slash.injected, directive.reloaded) rather than turns. Each delta runs a hook chain: symbol extraction → engagement → dedup → budget check → logging. Per-turn engagement coalescing prevents N-tool-call turns from inflating `turn_count`. §3.1–§3.7 stubs tightened to forward-reference §3.0 rather than restating the work. §4.5.7 "Project bootstrap at startup" drafted: explicit `--project` override; three-step heuristic waterfall (git remote match → CWD path match → last-active prompt); final fallback for fresh installs. New `last-active` operational file added to §2.1 storage layout (single line, prj_<n>, gitignored). New `archive/index.jsonl` listed in §2.1 (canonical, empty until v0.2).
- 2026-05-09 — §1 system overview gains operationalized acceptance language pointing to §11.1. §5.1 "Topic tag emission format" drafted: single-line `*topic: <thread-list> [<anchor-list>]*` form with parser regex; the deferred multi-form question is decided. §5.5 "Mid-turn thread fetch" drafted as **system-injected** (not tool-call) — runtime parses the topic tag and pre-loads referenced threads before re-prompting the model. §8.2.1 redaction policy explicitly **hybrid**: refuse on agent-initiated reads (deny error to the model + `permissions.suspicious-access` log); redact on user-initiated `#` captures (placeholder content + `permissions.redaction-fire` log). §10 acceptance gate clarified: Phase 5 isn't "feature complete"; it's "feature complete + six-month simulation green + steady state demonstrated." §11 fully restructured from stub to "Measurement and validation regime" with eleven subsections covering the test fabric: §11.1 six-month-simulation acceptance, §11.2 mock LLM, §11.3 unit, §11.4 scenario, §11.5 invariant validators, §11.6 metrics emission, §11.7 churn, §11.8 calibration, §11.9 cross-run baseline comparison, §11.10 six-month simulation harness, §11.11 live instrumentation. Open questions 6, 7, 18 resolved (closed) and renumbered; new questions 19, 20, 21 added (topic-tag reliability, system-injected re-prompt cost, six-month sim pass thresholds).
- 2026-05-09 — §11 lead strengthened with the **asymmetric cost framing**: there is no graceful recovery from "use it and find out" — if six months of accumulated state reveal the storage strategy is structurally wrong, the choice is between a complex/risky refactor of accumulated memory or losing it all. The simulation regime exists because proof must come ahead of time. §10.1 v1.0 Python entry refined: confirmed math/physics computational workflow as a real (not speculative) v1.0 commitment with concrete tool list (`python.run`, `python.format`, `python.lint`, possibly `python.repl`); explicitly **Python-only** (other coding languages out of scope — not building a polyglot agent-coding tool). New watch-list item 19: Python execution environment policy (system / venv / uv) for v1.0 design.
- 2026-05-09 — Phase 2.c.2 lands: chat REPL with project bootstrap (§4.5.7) handling all five BootstrapResult branches; turn handler with §3.0 chain skeleton and per-turn engagement coalescing (§3.0.4); event log writer (§2.8); working-set composer MVP (LayerA1 only). Default cobra command is now chat — `personant` with no subcommand drops into a session.
