# Personant — v0.1 Specification

**Status:** in progress. §0–§2, §4, §6, §8.2 substantively drafted; §3, §5, §7, §8.{1,3,4}, §9–§11 stubbed.
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
  .git/                             # git-init'd at first run
  tmp/                              # agent's drafting scratch (see §6.3); never git-committed
```

**File ownership classification:**

| Type | Examples | Drift policy |
|---|---|---|
| Canonical | `spine.jsonl` rows, `threads/*.md`, `directives/*.md`, `projects/prj_<n>/meta.json`, `logs/*.log`, `providers.toml` | Source of truth. Hand-editable. Other files derive from these. |
| Derived | `symbols.jsonl`, `projects/prj_<n>/digest.json` | Regenerable from canonical. Pre-commit hook fails if stale. Never hand-edited. |
| Operational | `logs/*.log`, `.git/`, `tmp/` | System-managed; not subject to drift checking. `tmp/` is gitignored (agent drafts, not history). |
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

[STUB — to be drafted next.]

Sections planned:

- 3.1 Working-set composition (per-turn): given current thread state, layer budgets, and active project, produce the actual context window contents for the upcoming turn.
- 3.2 Topic tagging and engagement update: prompt the model for topic tags + per-thread symbol lists, parse, validate, normalize, update thread engagement state.
- 3.3 Symbol extraction (three passes): deterministic regex pass over turn content; model emission integration; curator anchor selection at retirement.
- 3.4 Recall matching: symbolic Jaccard match (cheap pre-filter), embedding similarity (mid-cost), model judgment (expensive last resort).
- 3.5 Closure flow: trigger detection (engagement decay or `/done`), curator-drafted summary, user ack with resolution choice, frontmatter+spine update, Layer B/C eviction.
- 3.6 Fallback dissection: budget-pressure trigger, dissector LLM clustering of oldest content, batch retirement, ack flow.
- 3.7 Cross-project digest maintenance: regeneration triggers, per-project byte budget enforcement, recent-anchor selection.

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

---

## 5. Model interaction

[STUB.]

Sections planned:

- 5.1 Topic-tagging prompt fragment and parser regex
- 5.2 Curator prompt for retirement summary
- 5.3 Dissector prompt for fallback clustering
- 5.4 Recognition-context construction (spine + recent + active threads + project conventions)
- 5.5 Tool-call surface for mid-turn thread fetch

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
- `#`-prefixed shell commands (§4.4) that target this file path (or any path matching it after symlink resolution) have their captured output **redacted before reaching context** — replaced with a `[redacted: providers.toml]` placeholder. Logged as `permissions.redaction-fire`.
- The same redaction applies to `fs.read`/`fs.grep` results targeting `providers.toml` from the agent's tool surface, but the agent should not need to read this file at all under normal operation; an attempt is itself loggable as `permissions.suspicious-access`.
- The file is not git-committed inside `~/.personant/`'s git tree by default — `.gitignore` in the home tree excludes it. (Optional opt-in for users who want their own private repo containing it; out of scope for v0.1.)

[OPEN: redaction strategy under partial-read or grep — should the runtime refuse the operation outright, or pass through with redacted content? See §13.]

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

### 10.1 Beyond v0.1 (tracking, not committed scope)

Capabilities that expand the role beyond v0.1's read-and-think research
assistant. Noted so v0.1 implementation does not preclude them; not on the
near-term dev list.

- **v1.0: Computational tool surface for math/physics projects.** Python
  authoring (via the existing `fs.propose_promote` flow) plus *execution* of
  scripts, simulations, and formatter/linter tooling (`black`, `isort`,
  `flake8`). Requires a sandboxed subprocess execution surface, stdout/stderr
  capture into the engaged thread (subject to the §6.5 budget policy),
  resource limits (wall-clock, memory, output bytes), and lifecycle
  management (cancellation, orphan reaping). The shell-execution exclusion in
  §6.1.3 lifts only for this targeted capability when it lands — not as a
  general "agent can now run anything." Held until the v0.1 memory-context
  mechanism is proven.

---

## 11. Testing approach

[STUB.]

Sections planned:

- 11.1 Unit testing (deterministic state operations, JSONL round-trips, schema validation)
- 11.2 Integration testing (turn loop with mock model, recall flow end-to-end)
- 11.3 Live instrumentation as testing (the log itself is the test fixture; replay-based regression)
- 11.4 Property-based tests for layer-budget invariants

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

---

## 13. Open questions

Compiled from inline `[OPEN: ...]` markers and design-pass uncertainties.

1. **§2.3 — `history_symbols` eviction weight formula.** Defer concrete formula to v0.1.1 with empirical data; v0.1 uses count alone.
2. **§2.5.2 — `one_line_summary` generation.** Frequency-weighted symbol concatenation vs. LLM-curator. Start with the former; may upgrade if quality is poor.
3. **§2.6.1 — Full parameter namespace.** Initial seed exists; will grow during implementation.
4. **§3.5 — Closure prompt phrasing variants.** Calibrate empirically; expose via directive file once tuning becomes useful.
5. **§3.6 — Fallback dissection batch size.** How many clusters max per dissection event? Likely 3-5 to keep ack burden manageable.
6. **§5.1 — Topic-tagging prompt format.** Need to settle exact syntax (single-line tag vs structured prefix vs JSON envelope) — affects parser robustness.
7. **§5.5 — Tool-call surface.** Whether mid-turn thread fetch is a tool call or a system-injected context augmentation.
8. **§7 — Package boundaries.** Concrete Go layout — to be drafted with package skeletons before Phase 1 implementation.
9. **`/back-to` semantics.** Resume thread or just reload context? Probably both as separate commands.
10. **Multi-project digest budget.** How much total budget for Layer A2 when many projects exist? Flat per-project cap (current spec) vs. dynamic allocation.
11. **§6.1.1 — Web search provider.** Brave / DuckDuckGo / no-search-in-v0.1. Suggest deferring `web.search` until empirical pressure proves `web.fetch` alone is insufficient.
12. **§6.5 — Tool-output cap value and head/tail ratio.** 8 KB starting guess; calibrate with use.
13. **§6.1 — PDF support.** Research consumes papers; `web.fetch` returns bytes. Whether to ship a thin PDF extractor in v0.1 or defer until empirical pressure.
14. **§6.2.5 — Batch threshold.** 5 files is an initial guess. Smaller (3?) might be safer; larger (10?) less friction. Calibrate.
15. **§4.5 — Project marker file.** A `.personant-project` file at the project root would make rebinding bulletproof when there is no git remote. Defaults to no marker file in v0.1 (don't pollute the user's workspace), with the user-prompt fallback handling missed rebinds. Reconsider in v0.1.1 if local-only projects empirically thrash on `/cd-project` resolution.
16. **§4.5.4 — Polling cadence for git remote detection.** Per-turn vs. on-engagement vs. on-explicit-trigger. Default to on-engagement to keep cost down; revisit if remote changes are common enough that lag becomes noticeable.
17. **§4.4.3 — Interactive-app handoff.** Whether to support `vim`/`nano`/`less` via terminal-mode handoff, or keep deferring to "open another terminal." Likely held until empirical pressure makes the friction onerous.
18. **§8.2.1 — `providers.toml` redaction strategy.** When `#`-prefixed shell capture or `fs.read` would expose `providers.toml` content: redact-and-pass-through (replace content with placeholder), refuse-outright (deny the op with an explicit error), or hybrid (refuse on agent-initiated reads, redact on user-initiated captures)? Hybrid feels right but warrants explicit calibration during implementation.

---

## Document changelog

- 2026-05-08 — initial draft (§0–§2 substantive; §3+ stubs; watch list / open questions carried from outline / inline markers).
- 2026-05-09 — §6 "Tool surface and permissions" drafted; subsequent sections renumbered §6→§7 through §12→§13. §1 mentions research-assistant role. Watch-list items 8–11 and open questions 11–14 added from the same design pass. §6.1.3 clarified: exclusion is at the LLM-tool layer; runtime performs git operations autonomically on `~/.personant/` and read-only git queries on the workspace.
- 2026-05-09 — §10.1 "Beyond v0.1" added to track v1.0 computational tool surface (Python execution + black/isort/flake8) as a planned but uncommitted post-v0.1 role expansion. Outline mirrors the deferral.
- 2026-05-09 — §4 expanded substantively: §4.1 CLI command list seeded; §4.2 slash command catalog (incl. `/cd-project`, `/project switch|rename`, `/model`, `/stats`); new §4.4 "Shell escape (`$` and `#`)" with long-lived interactive shell subprocess (interactive apps deferred); new §4.5 "Project identity, project root, and shell cwd" with dual-cwd model. §6.2 gains §6.2.6 "Active-project boundary as a tier axis." §2.8 vocabulary adds `user.*` and `project.*` categories.
- 2026-05-09 — Project identity model revised to use stable internal handle (`prj_<n>`) + canonical external identity (normalized git remote URL) + mutable display name. §2.1 storage layout uses `projects/prj_<n>/` (and parallel for project-scoped directives). §2.2 spine `project` field is now the prj id. §2.5.1 ProjectMeta gets `id`, `current_root_path`, `historical_root_paths`, `remote_urls`, `historical_remote_urls`. §4.5 expanded to specify three identity layers, `/cd-project` resolution flow, local→remote promotion, file-move detection, and `/project rename`. §2.8 vocabulary extends `project.*` with `renamed`, `remote-adopted`, `remote-updated`, `remote-collision-prompt`. Watch-list items 12–14 and open questions 15–17 added.
- 2026-05-09 — `providers.toml` added to §2.1 storage layout (canonical, secret-bearing) and `tmp/` added (operational, gitignored). New "Secret-bearing" file ownership tier introduced. §8.2 substantively drafted: §8.2.1 covers `providers.toml` format and the security boundary (no inclusion in any LLM-context artifact; redaction rules for `#` capture and `fs.read`/`fs.grep`); §8.2.2 / §8.2.3 stubs for env-var and directive-precedence (latter cross-references §2.6). §8.3 stub clarified for pre-commit hook scope. Watch-list items 15–16 and open question 18 added.
