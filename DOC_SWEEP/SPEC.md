# SPEC – Personant

Consumer-facing behaviors and outcomes

Must-see:

- Any implementation that satisfies this SPEC is valid
- No project mechanisms or code structure are put forth, that belongs to ARCHITECTURE
- No project-specific process is dictated, that belongs to CONVENTIONS

## 1. Operating model

Personant runs under unmodified opencode as a plugin plus the **memory service**, a local process
that holds all memory state. Personant owns what enters every model request: composition
(assembling each request, the **composed request**, from memory, §3.1), compaction (shrinking a
long conversation to fit the context window) and summarization (condensing older content), and
recall (surfacing older **threads** — each the durable record of one problem being worked on —
relevant to the current work, §3.4). opencode
owns everything else: the terminal UI, session storage, the message data model, the event stream,
agents, permissions, tools, providers and configuration. Where opencode has made a choice outside
context management, Personant uses it as it is (§1.7's database cleanup is the one exception). Personant does not define interaction modes, a
workflow, or how much the user takes part. opencode's plugins and Code Mode (opencode's way of
letting the model call tools by writing code) work unchanged. The
components and their wiring: ARCHITECTURE "Plugin route".

An **Open:** line marks a decision not yet made; a tag such as U7, B6a or A12a names it.

**Open:** U7 — plugin failure policy: fail open (opencode's own composition reaches the model;
the memory service misses the step) or fail closed (the request is refused).

### 1.1 Sessions and service runs

Two terms. An **opencode session** is opencode's conversation container. A **service run** is one
memory-service process lifetime. A session is a window into one continuous memory; a service run
spans any number of sessions, and a session can span several service runs.

- Each **turn** — the span from a user message (or a resumed request) to the close of the response
  (§3.0.4) — is assigned to a thread by the problem it concerns (§3.2), never by session.
- Deleting a session leaves memory untouched.
- A session never requires `/clear` or `/compact`, and neither command discards memory.

**Open:** what `/compact` and `/clear` eventually map to.

**Open:** fork and revert — how a branch's work is incorporated or discarded, decided together with
sub-mind split and re-merge (handing work to a child agent session that runs apart from the main
one, and folding its result back).

**Open:** two concurrent opencode sessions against one memory.

### 1.2 MCP

opencode delivers all MCP content: server instructions, tool definitions, tool and resource
results, and prompts (templates a server offers for the user to invoke). Every kind reaches
composition (§3.1). An MCP prompt expansion is handled as user input.

**Open:** a way to tell an MCP prompt expansion apart from a typed user message. The budget
and overflow form (what stands in for content a **slot** — a named, budgeted part of the composed
request, §3.1 — drops) of tool definitions are Open in §3.1.

### 1.3 opencode content in composed requests

- Instructions reach the model current, never as a stale baseline plus diffs. A nested `AGENTS.md`
  that opencode injects once stays available for as long as it applies.
- opencode's expectations — the todo list, the agent mode, an open question or permission request,
  reminders still in force, child session IDs, background-job results — are always present as
  current state, not as history.
- Content already accounted for is not counted twice when opencode re-injects it.

### 1.4 Evicted content

Content is **evicted** from a request when it is dropped from that request to meet a budget. File
content evicted from a request (and any content dropped from a slot whose overflow form is a
stand-in line) is named in that request by a **stand-in line** (a one-line placeholder that names
the content; for a file, its content-id line, §3.9.1), at the version it had, and can be brought
back on demand, except a file at or above the share threshold (`dedup.literal-share-threshold`,
§2.6.1), for which no mechanism yet exists (§3.9.2). Where replay (re-reading the file or
re-running the call) cannot reproduce the content, the stored bytes are what the stand-in line
points to. The model can expand
the stand-in line (ARCHITECTURE "Display lines are how the model recognizes threads").

### 1.5 Determinism

Memory state does not depend on the order in which concurrent tool calls complete. A response's
tool results are applied in tool-call order (§3.0.4).

### 1.6 Resume

- Every request of a turn that opencode resumes is composed by the memory service, including a resumed turn
  that arrives with no new prompt (§3.0.4).
- At service-run start, memory is reconciled against opencode's stored messages: turns opencode
  kept that the memory service rolled back are re-ingested (§4.5.8 (10)).

**Open:** U9 — whether in-run history and the turn counter survive a service restart (today only
the working set reloads).

### 1.7 opencode storage

opencode's checkpointing (after a checkpoint, opencode never reads a session's earlier rows again,
but they stay on disk) does not make opencode's database grow without bound. Cleanup of opencode's database
never breaks a kept session's (one the user has not deleted) scrollback (the history the user can
scroll back through), fork or revert.

### 1.8 Memory vetting

Memory comes in three kinds: **behavior memory** (how the agent thinks and conducts itself, the
same for every project), **process memory** (how work is done: practices for every project, plus
heuristics scoped to a category of project or to a role in the work) and **career memory**
(techniques and solutions developed on specific projects that may serve others).

Personant can review behavior- and process-memory text a model will later read before it is
committed. Whether the review runs, what is accepted and where feedback directive tuning (proposing
directive changes from the lessons the work produces) files a lesson are the user's policy:
Personant ships defaults and imposes none.

- **Reviewer.** A model applying a fixed rubric of the kind used for prompt-engineer review:
  clarity, scope, absolutes that will be over-applied, conflicts with existing entries, and wording
  another model would misread.
- **Behavior and process memory have one write path.** Every proposed change, whatever proposes
  it, goes through it, and the review runs there when the policy enables it. The outcome is
  recorded with provenance: what proposed it, what the review found, and what accepted it.
- **The policy is user-configurable:** whether the review runs, and acceptance (for example manual
  acceptance, auto-accept on a clean review, or auto-accept everything). A pending proposal never
  blocks a turn and never prompts the user unasked.
- **Direct edits** to the **memory store** (the directory holding all of Personant's memory, with
  its git history, §2.1) are not prevented; its git history records them.
- **Feedback directive tuning** files a lesson by scope by default: a behavior-memory change is proposed
  only from patterns aggregated across projects, and a pattern observed in one project becomes a
  process-memory proposal (a way of working) or career memory (a technique), not a change to
  `directives/user.md` or the defaults. The user's policy can change this routing. A tuning
  proposal takes the same write path as any other.
- **Process memory** is directive text (§2.6): practices for every project, and heuristics
  scoped to a project category or to a role. A change to it may be proposed from a single project
  and takes the same write path as any other.
- **Threads**, the turn-to-turn record every kind of memory is drawn from, are written every turn
  and are not reviewed per write. They are protected by
  budgeted slots (§3.1), provenance on every entry, reversibility through the memory store's git
  history (§3.11), mechanical checks on write (schema, length, provenance, dedup), and batch content
  review in consolidation (the offline cycle that reviews and packs memory; the review is not yet built, §3.10.8).
- **Standing behavior and process memory** is re-reviewed periodically when the policy enables
  it, because text that was sound for one model can mislead the next.

A lesson is filed under the kind of memory it changes, and the
project's own state and history stay in its workspace. A failed approach is kept only when it is
a tempting wrong approach: something that will look like a good idea again. Beyond the write path and
the policy above, no approval gate, confirmation prompt or other enforcement mechanism is built.

**Open:** the default policy: whether the review runs, the acceptance mode, and the re-review
setting.

**Open:** the review rubric's exact content.

**Open:** the re-review cadence.

**Open:** how patterns are aggregated across projects for feedback directive tuning (what counts as a
pattern, how many projects, what threshold).

**Open:** how process memory is stored and selected: its directive-file layout and scope values,
its place in directive precedence, and how the categories that apply to a project and the active
role are determined.

---

## 2. Data model

This section states what a compliant implementation must keep in memory and the properties of that
storage. The storage layout and the record, symbol and log formats this implementation uses are in
ARCHITECTURE "Storage and record formats" and "Event log format"; directive files (§2.6) stay
here because users edit them.

### 2.1 Storage layout

Memory lives under one store directory (default `~/.personant`; the environment variable that
overrides it is in §8.2.3). The **memory store** is that directory with its git history.
Authoritative files are plain text, versioned in git, and inspectable with ordinary tools. A user's
direct edit to an authoritative file is never destroyed by crash recovery: it is absorbed (§4.5.8), or, where a torn turn's reset would revert it, preserved byte-exact in
quarantine (a directory of recovered bytes, §4.5.8) first. Files derived from authoritative files are regenerable and never hand-edited; a
day rollover or archival batch, which writes the **history repository** (the memory store's permanent day-grain git repository, §3.11),
fails if it leaves a derived file stale (§8.3); between **day rollovers** (the once-a-day
maintenance sequence, §4.5.8 (5)) derived files may lag by design (§4.5.8 (7)). **Operational**
files are the gitignored runtime state the memory service manages to run (the turn journal, the
last-active record, the working-set record), neither authoritative nor derived. The file
tree, the authoritative/derived/operational classification and the storage commands: ARCHITECTURE
"Storage and record formats"; the commands are listed in §4.1.

#### Secret-bearing content

Personant reads no API key and keeps no secret-bearing file: opencode holds every provider
credential (§8.2.1).

#### Outside the store directory

- Personant-only settings live under `~/.config/personant/` (`config.toml`, §8.2.2).
- opencode's data directory is never under the store directory.

### 2.2 Thread registry record schema (`spine.jsonl`)

Unused → ARCHITECTURE «Storage and record formats» (the record layout and field names). A person
inspecting the store finds every thread as one record in the **thread registry**, a single file
sorted by id, so a change to one thread shows as a one-line diff. A record carries the thread's
project, **anchors** (the few **symbols** that currently identify it), summary, description, state,
timestamps, turn count and recall-fire count (the number of accepted recalls of the thread, §3.4).
Names beginning `spine` (`spine.jsonl`, `spine.entry-max-chars`, the `spine.*`
events) are the thread registry's. The `description` is the triggering utterance, set once at creation and never
rewritten; the `summary` is set from the thread's first response at creation and replaced when the thread closes (§3.5) by the **summarizer**'s draft (the model call that
drafts a thread's closing summary, §5.2) and bounded by `spine.entry-max-chars`. The anchors are 0 to
`anchor.projection-max` (default 8, a provisional value calibrated in §9) normalized symbols
(the keys recall matches on: file paths, URLs, named concepts, hashtags, §2.7), re-derived at the close of every owner turn (a turn the thread owns, §3.2)
from the thread's retained symbol history (§2.7.4); the model never controls their count (§5.1).
`personant verify` enforces the structural constraints and flags an over-ceiling anchor count.

#### 2.2.1 Thread states

A thread is in exactly one of these states:

- `active` — currently **engaged** (named by a turn's **topic tag**, the
  line that opens each model response and names the threads the turn engages, §5.1, or bound as its owner by the §3.3 fallback; §3.2) or recently
  active (within the engagement decay window, `engagement.decay-turns` / `engagement.decay-time`,
  §2.6.1)
- `paused` — the user explicitly paused it
- `blocked` — gated on external input; user-confirmed
- `wip` — long-running with progress; not yet resolved
- `resolved` — problem solved; work complete
- `decided` — architectural decision recorded; no further action expected
- `abandoned` — explicitly dropped without resolution

**State transitions** (see §3.5 for detailed flow):

| From | To | Trigger |
|---|---|---|
| (none) | `active` | thread creation |
| `active` | `paused` | `/pause` |
| `paused` | `active` | re-engagement or `/resume` |
| `active` | `blocked` | summarizer detects + user confirms |
| `active` | `wip` / `resolved` / `decided` / `abandoned` | closing question with chosen outcome |
| `wip` | `active` | re-engagement (model emits topic tag) |
| `resolved` / `decided` / `abandoned` | `active` | re-engagement; rare, but allowed |

#### 2.2.2 Display form

Unused → ARCHITECTURE «Storage and record formats» (the display-line layout). The model sees each
thread in its composed request as one **display line** of id, anchors, summary and state — enough
to **recognize** the thread (tell that earlier work bears on the current work) without loading it.
The line carries enough decision state that the model can answer most questions about a thread's outcome from the line alone.
The line is generated from the thread registry record each time and never stored.

The **human half** of a thread's display — the working name a user reads and types back — is its
`description`, falling back to `summary`, falling back to the id.

### 2.3 Thread file format (`threads/thr_<id>/`)

Unused → ARCHITECTURE «Storage and record formats» (the directory layout). A person inspecting the
store finds each thread as one directory holding its metadata (YAML frontmatter, authoritative),
one terse **excerpt** per owner turn and the §3.9
tracked-file store (per-thread storage of the current content and recent diffs of files the
assistant read or wrote). Appending a turn writes one small file, so per-turn write cost does not
grow with thread length.

- Excerpts hold **decision-class** content only (what the user and the assistant said and
  decided): raw **task-class** output (tool output, shell captures, file contents) is never written
  as one (§3.10.1, §3.10.6); a file delta (one content addition to the context, §3.0) appears in an
  excerpt only as its **content-id line** (one line naming the operation, the file's content id and
  its path, §3.9.1). They are retained past the thread's recent-excerpt window (its newest excerpts, up to a
  bounded count, which are read into the composed request), remain recall-indexed, and are the
  durable source of intra-thread recall (§3.4). Retention trimming is the deferred keep/toss step
  (the decision which retained excerpts to keep or discard, §3.10.8); v0.1 keeps all excerpts.
- The number of files in a thread's directory does not grow with its count of excerpts: the day rollover moves older excerpts out of individual files, and only an excerpt too large to move stays a file of its own. Excerpts are
  returned to readers byte-exact, whichever way they are stored; what a reader gets never depends on
  how an excerpt is stored.
- Durable turn content lives only in excerpts, never the event log (§2.8).
- `description` is set deterministically at creation (a whitespace-collapsed copy of the spawning
  prompt, truncated; no LLM paraphrase) and never rewritten; until the thread closes, its `summary` is the one set at creation (its first response, whitespace-collapsed and truncated).
- A thread has no parent-thread field and none is synthesized (the flat thread structure, §3.2);
  origin provenance is recorded per symbol (`derived_from`, §2.7.3).
- A thread's symbol history is hard-capped at `history.cap-per-thread` (default 40) over active and
  superseded entries (a symbol is superseded when it has dropped out of the anchor projection and
  is kept as a recall handle, §2.7.4). Capacity trimming never removes a symbol that was once
  central (projected as an anchor at some time) or that is a high-specificity identifier symbol (a
  URL, file path or git SHA) while these symbols, the **protected set**, fit the cap; if the protected set alone
  exceeds it, the least-counted superseded entries go first.

### 2.4 Symbol index schema (`symbols.jsonl`)

Unused → ARCHITECTURE «Storage and record formats» (the index layout). For any symbol, recall can
find which threads hold it as an anchor and which have superseded it (§3.4). `symbols.jsonl` is
derived: regenerable from the authoritative files and never hand-edited.

### 2.5 Project metadata schema

#### 2.5.1 Authoritative: `projects/prj_<n>/meta.json`

Unused → ARCHITECTURE «Storage and record formats» (the metadata layout). The store records, for
each project, three levels of identity (see §4.5): a stable internal handle (`prj_<n>`, never changes, the storage key), a remote identity
(the normalized git remote URL, gained when the project's tree first acquires a remote)
and a mutable display name (`/project rename`). Project metadata also holds the project's root
paths (current, `current_root_path`, and historical), remote URLs (current and historical), and project-scoped
configuration: convention files loaded into the directives slot (§3.1), project-specific symbol patterns
and project-scoped stopword additions.

**Open:** whether `conventions_paths` overlaps the instructions slot (§3.1), which carries the
instruction files opencode loads; decided with the per-slot policies.

##### Remote URL normalization

Remote URLs compare in a normalized form: lowercase host, trailing
`.git` stripped, and `git@host:owner/name` equal to `https://host/owner/name`.

**`prj_default`** is reserved for threads created before any explicit project was active. It has no
path or remote.

#### 2.5.2 Derived: `projects/prj_<n>/digest.json`

Unused → ARCHITECTURE «Storage and record formats» (the digest layout). The model sees every
other project through its digest: the project's recent anchors and a one-line summary, derived from
its threads; between day rollovers it may lag them (§2.1).

### 2.6 Directive file format (`directives/*.md`)

Directive files are markdown documents with YAML frontmatter for parameter overrides.

```markdown
---
scope: user                         # one of: defaults | user | project
project: null                       # null for defaults/user; the project's id (prj_<n>) for project-scoped
parameters:
  closure.ack-mode: always          # override system default of auto
  recall.ack-mode: always           # override system default of banded
last_modified: 2026-05-08T19:42:00-07:00
modification_source: accrual        # one of: install | user-edit | accrual (accrual = feedback directive tuning, §1.8: cross-project by default)
---

# User directives

## Revisit guidelines

[Free-form prose explaining the user-facing behavior the parameters encode. Inspectable, editable.]
```

A change proposed to the defaults or user-wide directive file, by feedback directive tuning or any
other proposer, is a behavior-memory change and follows §1.8; a user's direct edit is not
prevented. By default (§1.8), feedback directive tuning proposes such a change only from patterns
aggregated across projects, and a
pattern observed in one project does not reach those files. Process memory is also directive text; a change
proposed to it follows §1.8.

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
engagement.decay-turns: 8           # turns of non-engagement before a thread closes
engagement.decay-time: 7d           # wall-clock equivalent (duration string)
closure.ack-mode: auto              # auto | always (§3.5).
                                    # auto (default): routine close-outs (closings
                                    # of decayed threads) are
                                    # auto-accepted with the summarizer's summary;
                                    # exceptions queue for a drain at a drain point.
                                    # always: every decayed thread is put to the
                                    # user for an ack (the user's answer to a
                                    # question confirming a step) as it decays.
recall.symbolic-threshold: 0.4      # Jaccard threshold (overlap = shared symbols ÷ symbols in
                                    # either set) for opportunistic recall surfacing
recall.cross-project-threshold: 0.5 # higher bar for cross-project surfacing; cross-project recall is not built (§3.2)
recall.ack-mode: banded             # banded | always (§3.4).
                                    # banded (default): a candidate at or above its
                                    # signal's auto-fetch threshold is fetched with one
                                    # user-visible line and no question; the band between
                                    # the surface and auto-fetch thresholds is asked;
                                    # nothing else surfaces.
                                    # always: every surfaced candidate is asked
                                    # (decline-reason question included).
recall.symbolic-auto-threshold: 0.65  # Jaccard score at or above which a candidate is
                                    # fetched without asking (§3.4 auto-fetch band)
recall.cosine-auto-threshold: 0.75  # cosine similarity of embedding vectors (embedding AND
                                    # intra-thread — one bar, one
                                    # scale) at or above which a candidate is fetched
                                    # without asking
layer.b-top-k: 3                    # max threads in the active set (loaded in full, §3.1);
                                    # `layer.` keys are slot parameters
layer.budget.percentages: {E: 12, A1: 10, A2: variable, B: 55, C: 15, live_turn: 15}
                                    # keys E, A1, A2, B, C name the directives,
                                    # registry, other-projects, active-threads and
                                    # dormant-threads slots (§3.1)
                                    # live_turn is carved from the byte total first;
                                    # the five memory slots partition the remaining memory
                                    # budget (A1+A2 = 18% share; A2 is the residue)
context.token-budget: 200000        # whole-request TOKEN ceiling; the authoritative check
recall.superseded-weight: 1.0       # numerator weight for a matched symbol that is superseded in a thread (§3.4); provisional value calibrated in §9 — 1.0 = no down-weight
history.cap-per-thread: 40          # max history symbols per thread
anchor.projection-max: 8            # top-N of the thread's retained symbol history (`history_symbols`) projected to thread registry anchors (§2.2/§2.7.4); provisional value calibrated in §9
spine.entry-max-chars: 200          # hard cap for a thread registry record's `summary`
cross-project.digest-per-project-bytes: 150
dissect.pressure-threshold: 0.90    # budget-pressure fraction triggering fallback dissection (§3.6)
dedup.diff-literal-threshold: 0.7   # a change's diff at or above this fraction of the new
                                    # content's size is not kept (§3.9.1); a kept diff at or above
                                    # it of the file's current size does not render (§3.9.2)
dedup.live-diff-window: 3           # N most-recent changes per file kept (§3.9.1) and rendered as diffs (§3.9.2)
dedup.literal-share-threshold: 0.25 # a file whose current content is at or above this fraction
                                    # of its thread's active-threads share is never expanded;
                                    # content id only (§3.9.2)
dedup.diff-format: unified          # global default diff format (§3.9.3)
```

### 2.7 Symbol normalization and categories

#### 2.7.1 Categories

Unused → ARCHITECTURE «Symbol normalization» (the category definitions). Recall matches on three kinds of symbol: identifier symbols
(file paths, URLs, code symbols, commit SHAs), entities (model-emitted named concepts)
and hashtags (user `#`-tags).

#### 2.7.2 Normalization rules

Unused → ARCHITECTURE «Symbol normalization» (the per-category rules). Recall matches entities and hashtags across
case, spacing and punctuation variants, and matches identifier symbols exactly; a symbol is
displayed as it was written.

#### 2.7.3 Source

Unused → ARCHITECTURE «Symbol source» (the source and provenance fields). Each symbol records how it was emitted (deterministic
extraction, model or user) and, separately, which threads it was carried in from via recall
(`derived_from`). Provenance never takes part in the §3.4 recall match. A thread started by recalling and
combining two or more threads carries each parent's id per symbol, not as a thread-level edge.

##### Open (deferred)

Synthesis introduced *without* a recall hit — a prior thread's idea brought
forward by paraphrase, so no normalized symbol matches — leaves `derived_from` empty.

#### 2.7.4 Symbol lifecycle and anchor projection

Unused → ARCHITECTURE «Symbol lifecycle and anchor projection» (the projection rules). A thread's anchors are a
deterministic projection of its retained symbol history, re-derived every owner turn; no model call
is involved. A symbol that falls out of the projection is marked superseded and retained, so an
abandoned premise remains a findable recall handle; a symbol that was ever central is protected from
capacity trimming (§2.3) while the protected set fits the cap. The history is
bounded by `history.cap-per-thread` (40) and the projection by `anchor.projection-max` (8); flat
history never exceeds its cap; that it stays below the cap with a flat once-central count over a run, so protection never has to yield, is a §9 sim obligation, not a static guarantee.

### 2.8 Log event format (`logs/YYYY-MM-DD.log`)

Unused → ARCHITECTURE «Event log format» (line format, examples, event vocabulary and reserved
names). A person inspecting the log, and the measurement regime, rely on this: every decision the §9 measurement regime and the feedback-directive-tuning loop need is recorded as
a plain-text, append-only, single-line event; no event carries multi-line content, and an event
absent from the vocabulary cannot be measured. Durable turn content is never in the log (§2.3).

#### Per-turn opening-line contract

Exactly one `context.modified source=user.prompt` line per prompted
turn. Lines from the turn's preparation — the day rollover, staged-symbol expiry and deltas that arrive ahead of the prompt — precede it; every other line the turn produces follows it before the next turn's opening line.
Measurement segments turns on it, so it is never suppressed, reordered, or duplicated. A resumed
turn (§3.0.4) has no user message and so no such line. **Open:** the opening line of a resumed turn
(resume design).


---

## 3. Core algorithms

### 3.0 All content enters through one pipeline (deltas)

The **working window** — everything the next model request will carry — is built up as a sequence
of **deltas**, each one content addition to the window, with a
source (§3.0.1); a turn groups deltas, from the user message (or resumed request) that opens it to its close (§3.0.4). **No gaps:** every delta — every content addition from the session (§3.0.1) — runs the **per-delta pipeline** (§3.0.2: extraction,
engagement, dedup, budget check, logging), so no content escapes extraction, dedup, or the budget
check.

#### 3.0.1 The delta taxonomy

| Delta | Source | Content shape |
|---|---|---|
| `user.prompt` | user types or pastes | message text |
| `user.shell-capture` | stdout/stderr of a command the user runs through opencode's shell escape (`!cmd`) | captured bytes (subject to the §3.1 tool-output bound) |
| `model.response` | LLM emits final reply (or response segment) | response text + optional tool-call envelope |
| `tool.result` | each tool call returns | tool output bytes |
| `fs.read` | the assistant reads a file (opencode's `read`) | path + file bytes |
| `fs.write` | the assistant writes a file (opencode's `write`) or edits one (opencode's `edit`) | path + the file bytes after the write or edit |
| `fs.commit` | a tracked file is committed to the user's workspace git | path + commit hash; no file bytes |
| `thread.fetched` | a thread is loaded into the active set (**Open:** U3 — how a fetch is triggered under opencode) | thread body + frontmatter |

These are all the deltas the memory service emits through the §3.0 pipeline. Other-projects digest
content and directive-file reloads are not pipeline
deltas: they change *which* content the composer (§3.1) assembles, and what they surface arrives
through the `user.prompt` / `thread.fetched` / other-projects paths. New capabilities (cold-storage
restore, restoring a file's full content in place of its content id) add delta types to the same
pipeline.

#### 3.0.2 The per-delta pipeline

Each delta runs the pipeline, in this order:

1. **Symbol extraction** (§3.3) — identifier symbols and hashtags are extracted from the delta and
   the model's topic-tag symbols are read; where they land depends on the delta's retention class
   (§3.10).
2. **Engagement signal** (§3.2) — fire engagement for the threads the turn engages (§3.2: those
   its topic tag names), coalesced per turn (§3.0.4).
3. **Dedup decision** (§3.9) — the delta's file content is recorded once for the turn; which
   positions render content is decided at composition (§3.9.2).
4. **Budget check** (§3.1) — a delta that pushes the window over its slot caps triggers
   budget demotion before the next delta lands.

   > **v0.1 non-goal (turn-time budget enforcement).** This step performs no budget demotion in v0.1. The
   > only budget protections are (a) the per-thread recent-excerpt window (§2.3) and (b)
   > render-time byte truncation of each slot (§3.1); neither acts at delta time, so an LLM whose
   > per-turn deltas outrun the slot caps can silently starve the active-threads and
   > dormant-threads slots. Enabling enforcement here is a separate decision, not an incremental
   > fill-in.
5. **Logging** (§2.8) — emit the source-specific event (`tool.result`, `user.shell-capture`, etc.)
   plus a `context.modified` event if the delta materially changed window contents.

The pipeline runs at **semantic boundaries** — complete response segment, complete tool result,
complete capture — not per token. A streamed response runs the pipeline on stream completion (or on
logical sub-segment boundaries).

#### 3.0.3 Pipeline order matters

- Symbol extraction runs before engagement (engagement depends on the extracted set).
- Dedup runs after extraction; it keys file content by content id (§3.9.1).
- Budget check runs after dedup; logging runs last.

#### 3.0.4 Turn boundaries and per-turn engagement coalescing

A turn opens on a `user.prompt` event, or on a model-request composition (§3.1) with no preceding
user message — a **resumed turn**, which opencode starts when it resumes an interrupted turn
(§1.6). A user message can arrive in the middle of a turn, after tool results (opencode's `steer`
delivery: a user message sent while a turn is still running). **Open:** mid-turn user-message handling — whether it opens a new turn or joins the turn in progress. A response's
tool results are applied in tool-call order, whatever order the calls complete in; this is what
makes memory state independent of completion order (§1.5).

Within a turn, engagement counts once per thread, over the symbols of all the turn's deltas, so an
N-tool-call turn does not inflate `turn_count` (a thread's count of the turns it owns) for every
thread a tool result mentions.

`last_engaged` (a thread's recency timestamp) updates to the *latest* delta's timestamp. `turn_count` increments by 1 only for the
turn's **owner** thread (§3.2), however many deltas touched its anchors; an engaged-but-not-owner
thread's `turn_count` is unchanged.

#### 3.0.5 Implementation contract

Every delta reaches the working window only through the §3.0 pipeline; there is no bypass. Content the composer assembles from memory already held (§3.0.1) and the opencode-content slots, rebuilt from opencode's input on every request (§1.3), are not deltas.

### 3.1 Working-set composition

The **working set** — the memory content of the composed request — is rendered into the system
prompt as five memory slots, each truncated to a byte
budget derived from `context.token-budget` and `layer.budget.percentages` (§2.6.1). The token
ceiling is the authoritative whole-request check; each slot's byte budget is derived from it
conservatively, so the memory slots stay within their share of the ceiling. The live-turn reserve
(`live_turn`, 15%) is carved from the byte total first; the five memory slots partition the
remainder (directives 12, registry and other-projects together 18, active-threads 55, dormant-threads 15; the 18 is split between the registry (fixed, 10) and other-projects (the residue)). The whole request — system prompt + replayed
transcript tail + current user input + this turn's tool results + tool definitions — is bounded
against the token ceiling. Static prompt scaffolding (the orientation
preamble that opens the system prompt, the §5.1.3 topic-tag directive, the §3.3 fallback re-prompt reminders, section headers) is charged to no slot.

**Slot order and contents**:

| Slot | Content |
|---|---|
| directives | the defaults, user-wide and active-project directive texts plus the project's convention files, frontmatter removed |
| registry | a display line for each thread of the active project |
| other-projects | one line per other project (never `prj_default`) with its name, id, summary and recent anchors, most recently active first, each within `cross-project.digest-per-project-bytes` |
| active-threads | up to `layer.b-top-k` threads, most recently engaged first, each with its metadata and recent excerpts; each thread's share is the slot's share ÷ `layer.b-top-k`, and an oversized thread is truncated on its own |
| dormant-threads | a display line for each thread of the dormant set |

The slot percentages are calibration starting points, earned by the simulation's parameter sweep (§9.4).

#### Slots for opencode content

Three further slots carry content opencode supplies (§1.2, §1.3):

| Slot | Content |
|---|---|
| Instructions | The full text of each instruction source opencode loads (for example `AGENTS.md`), per source, including a nested `AGENTS.md` captured when opencode injects it. A source whose content is unchanged does not change the slot. |
| opencode expectations | The todo list, the agent mode, an open question or permission request, reminders still in force, child session IDs, background-job results — rendered as current state. |
| Tool definitions | The built-in and MCP tool definitions sent with each request. They count against the token ceiling. |

#### Slot policies

Every slot — the five memory slots, the live-turn reserve and the three above — defines four things before it
ships: (1) its **budget**; (2) its **internal priority**, what is dropped first when its content
exceeds its share; (3) its **overflow form**, what stands in for dropped content — the honest
truncation notice, or a stand-in line that names the content and can reproduce it (§1.4); (4) its
**unused-share rule**, whether it gives up unused share to other slots or takes unused share from
them.

**Unused-share reallocation** (a slot's unused share made available to other slots) **flows from
fixed slots to dynamic ones; the slot giving up share reclaims it first.** A fixed slot —
directives (project convention files included), instructions, the registry — changes only on a visible event at
a turn boundary, so its unused share is idle and is given up. A dynamic slot — live turn,
active-threads, dormant-threads, and the opencode-expectations slot, whose todo and background-job
updates arrive mid-turn — varies turn to turn and does not give up share. When a slot that gave up
share grows it takes its share back at the next composition, and the slot that received it gives
way through the existing demotion order (oldest dormant thread first, then oldest active thread).
Fixed slots are composed first, so the request keeps a stable prefix across turns.

**Open:** per-slot policies — all four for the instructions and tool-definition slots (including
whether the tool-definition slot gives up unused share); budget, internal priority and overflow form for the
opencode-expectations slot. Candidate overflow forms for tool definitions: omit rarely used tools from a request, or list them
by name with definitions on demand.
Without a tool-definition slot the whole-request ceiling above cannot be enforced.

#### Tool output in composed requests

Tool output is bounded so it cannot exhaust the 15% live-turn
budget:

- A hard byte cap on tool-result inclusion in the composed request: 15% of the live-turn reserve (`live_turn`, §2.6.1).
  Above the cap: head + tail with an ellipsis notice. A truncation always carries its notice.
- The cap is a **task-class** bound: it applies to `tool.result` and to `user.shell-capture`. It
  does **not** apply to the `fs.read`/`fs.write`/`fs.commit` deltas, whose content is the file body
  written to the per-thread tracked-file store (§3.9).
- The cap is applied to the **delta**: the bounded content is what the composed request carries and
  what the working window holds. The full body is not persisted (§3.10.6).
- The assistant can re-fetch with a narrower query or a byte range if it needs more.

**LRU update rule** (driven by the §3.0.4 turn-close path). After a turn's engagements commit, each
thread it engaged moves to the front of the active set, most recently engaged first
(**active-set order**). A thread pushed past `layer.b-top-k` moves to the front of the dormant set.

Active-set order and the dormant set persist across a relaunch (`working-set.json`, §2.1);
active-set order is the recency signal §3.3 owner-default uses. The dormant set is **not
count-capped**: the dormant-threads slot's byte budget truncates it at composition time, and its
truncation notice is the overflow indicator.

#### Truncation policy

each slot's rendered string is truncated to its byte budget at a UTF-8 rune
boundary, with a notice `... [Layer X truncated; budget=N bytes] ...` appended (X is the slot's
key in `layer.budget.percentages`, §2.6.1). A slot that fails
to render (e.g. a project's `digest.json` is missing) emits a `workset.warning` log line and renders
empty; neighbour slots proceed.

#### §5.5 mid-turn fetch

**Open:** U3 — whether the in-band tag and its stream abort-and-re-issue
are built under opencode or replaced by a recall tool. Until decided, the text below binds: when
the model's topic tag at response start names a `thr_<n>` not in the active set, that thread joins
the front of the active set and the response is regenerated with it in context; the user sees only
the regenerated response. At most one such re-prompt per turn (§5.5). A thread of another project
is declined (`thread.fetch-cross-project`) and neither loaded nor re-prompted for.

The close-time LRU update still picks up any thread the mid-turn fetch could not service (an
unloadable thread file logs `thread.fetch-miss` and is skipped) or did not trigger (e.g. the
second-stream tag introduces another missing `thr_<n>`; with the cap reached, that thread enters the
active set at close).

### 3.2 Topic tagging and engagement update

Topic-tag parsing is §5.1.2; engagement update is the §3.0.2 step-2 pipeline step fired with the
per-turn coalesced symbol set (§3.0.4).

#### Turn-single-owner invariant

A **turn belongs to exactly one thread** — its *owner*. A turn may
*engage* several threads; only the owner receives the turn's excerpt. The memory service assigns ownership; the topic tag
(§5.1.1) is advisory input.

#### Owner vs engaged

For each turn:
- the **owner** gets the turn excerpt, `turn_count++`, recency (`last_engaged`), `history_symbols`
  merge, and state→active resurrection (§2.2.1). A new-thread owner is created with the excerpt,
  `turn_count = 1`, and its `description` (§2.3).
- an **engaged-but-not-owner** thread gets recency, `history_symbols` merge, and state→active
  resurrection — but **no excerpt** and **no `turn_count++`**.

The active-set and dormant-set recency order (§3.1) and recall surfacing operate over all engaged
threads; the §3.9 file-edit application binds to the owner.

#### Unknown thread id and owner fallback

Every referenced `thr_<n>` is resolved against the thread registry
at turn close. An id that does not resolve is an **unknown thread id**: not in the thread registry
(`thread.engaged-miss`) or belonging to another project (`thread.engaged-cross-project` —
cross-project engagement is not built in v0.1 and is declined). An unknown thread id is logged and
dropped: it never enters the engaged set, the active set, or the persisted working set; never owns
the turn or receives the §3.9 file-edit binding; and a declined cross-project record is never
mutated. When the tag's would-be owner (the lowest-id existing referenced thread) is an unknown
thread id, ownership falls back to the most recently engaged **valid** referenced thread, ties
broken by lowest `thr_<n>` id. When every referenced thread id is unknown: a tagged `*new-topic*`
thread owns the turn; with no `*new-topic*` either, a new thread is created from the turn as for a
pure `*new-topic*` tag. The turn's excerpt is never dropped. A turn with **no tag at all** whose
deltas require owner binding follows the §3.3 missing-tag fallback (re-prompt once → owner-default
by active-set order).

#### Anchor projection at owner-turn close

After the owner's symbol history is merged, the memory service
re-derives the thread's anchors (§2.7.4) and records them in its registry record. Engaged-but-not-owner threads do not re-project. Closing a thread runs a final projection
from its symbol history.

### 3.3 Symbol extraction (two passes)

Two passes run per §3.0.2 step 1, cheapest first:

1. **Deterministic** — extraction from the delta (file paths, URLs, commit SHAs, hashtags).
2. **Model-emitted** — tag symbols from the topic tag (§5.1) are folded in directly.

The summarizer, which drafts the closing summary when a thread closes (§3.5), does **not** select anchors and contributes no symbols: the anchor set is the deterministic projection of retained `history_symbols` (§2.7.4).

Symbols from task-class deltas (§3.10.1) enter the staging buffer (§3.10.2); they reach `symbols.jsonl` only on citation-window promotion (§3.10.4).

#### A response without a topic tag never aborts the turn (missing-tag fallback)

A response with no valid
topic tag never aborts the turn. Two regimes, split by whether the turn's deltas require owner
binding (buffered §3.9 file reads or writes):

- **No binding required** (plain conversational turn): no
  re-prompt; the turn's content is kept like any other turn's (§3.4); `topic.tag-missing` is
  logged for calibration.
- **Binding required**: two fallback stages.
  1. *Single re-prompt* (`topic.re-prompt cause=missing-tag`): the in-flight stream is aborted and
     the request re-issued with a terse system-side reminder appended (§5.1.3). Capped at 1 per
     turn for this cause (combined bound: §5.5).
  2. *Owner-default* (`thread.tag-defaulted`): if the re-prompted response still lacks a tag, the
     turn binds to the most recently engaged valid active-set thread, by active-set order (§3.1;
     the first resolving candidate wins) — the same choice after a restart, since active-set order
     survives relaunch. When no active-set candidate resolves, a new thread is created from the turn.
     The excerpt and the §3.9 edits are never dropped, and content handled by the fallback still
     flows through the §3.0 pipeline (§3.0.5).

  A turn that read a file is binding-required, like one that wrote one.

#### Empty-response fallback (any turn)

A response with **zero visible content** (no tag and no body
— e.g. a model that spent its completion budget on hidden reasoning and returned `finish=stop`) is a
protocol failure whether or not binding is required (an all-whitespace response longer than the
§5.1.2 scan window is missing its topic tag, not empty — §5.5):

A response that carries tool calls is not empty even when it has no visible text; it never spends
the empty-response re-prompt.

1. *Single re-prompt* (`topic.re-prompt cause=empty-response`): the ended stream is discarded and
   the request re-issued with the empty-response reminder (§5.1.3) appended — its own per-cause cap
   of 1 under the combined bound (§5.5). Fires on ANY turn, conversational or binding.
2. *Accept the empty*: if the re-prompted response is also empty, the turn proceeds with it. On a
   **binding-required** turn it falls through to owner-default above; the recorded excerpt's agent
   section is **empty** (never synthesized placeholder content), and is logged `thread.tag-defaulted`. On a
   **conversational** turn the empty body surfaces to the caller unchanged. Either way the memory service
   emits a `system.empty-response` forensic line.

   The empty cause **subsumes** the missing-tag cause: a persistent-empty response never also spends
   the missing-tag re-prompt.

### 3.4 Recall matching

> **Recall-completeness invariant (no gaps in findability).** All **non-transient** turn content
> MUST be locatable by recall at every stage of its lifecycle — creation, recent-excerpt-window
> residency, scroll-out and durable storage, and later summarization and closing
> (§3.5). **The ONLY content that may be unfindable is transient data deliberately discarded** per
> §3.10 (uncited task-class symbols / raw task-bytes expired at window close, §3.10.5–§3.10.6), and two stated limits: archived threads, which are off live recall in v0.1 and restored by explicit fetch (§3.8.4), and the accepted embed-failure residual (below).
>
> "Locatable" is **mechanism-agnostic**: findable by *some* recall path — symbolic, embedding
> similarity, summary tree (a hierarchy of summaries over a long thread's excerpts), or the
> exact lexical / flat-scan level (a plain text search of every excerpt).
>
> Completeness holds **continuously, not eventually**: durable content momentarily absent from one
> index — e.g. scrolled out of the recent-excerpt window but pending its *asynchronous* embedding
> flush — must be covered by another mechanism in the interim (the completeness floor
> below), so there is no lag-window gap in findability.
>
> **Enforcement.** A measurement harness must assert this completeness directly and may **NOT**
> model any "by-design blind spot" or acceptable lag in which durable content is unfindable — such a
> window is a correctness defect in the memory service, never a behavior to certify.
>
> **Validation scope.** A run without an embedder exercises neither intra-thread embedding recall
> nor its lexical completeness floor, so it proves this claim for the symbolic path only. The embedding-flush-lag
> half is **accepted only when the embedder-enabled completeness run configuration runs green**
> (§9.1 open gate requirement).

Recall guarantees findability; it does not volunteer a connection to past work that shares no symbols or embedding similarity with the current work.

Three signals; signals 1 and 2 run in parallel, not as a cost cascade, and signal 3 runs on the candidates they surface:

1. **Symbolic Jaccard** over the symbol index — high precision, low recall. Cheap.
2. **Embedding cosine** — embedding similarity over every thread's content is the primary recall
   scan and is robust to drift: it finds threads, and content within a thread, by meaning, at one
   embedding call per turn. Intra-thread recall searches the engaged thread's own content that has
   left its recent-excerpt window; the recent-excerpt window, already in the request, is never a
   candidate.
3. **Model judgment** on surfaced candidates — expensive confirmation that cuts false positives
   before a recall is offered.

Symbolic Jaccard is never a pre-filter for the embedding scan (under realistic vocabulary drift it
would discard most genuine matches).

Embedding recall (signal 2) is **opt-in**: enabled when `config.toml` pins an `[embedding]`
provider/model (§8.2.2). With no embedding model configured, recall runs symbolic-only — never a
hard failure.

#### Embedding index — granularity, maintenance, persistence

One embedding index serves all
not-in-context recall. Intra-thread recall (recall of a thread's own earlier content) searches the
engaged thread's (the current turn's owner, §3.2) own content in it directly.

*Maintenance.* Indexing is off the turn's critical path: recall reads never wait on it. A thread's
whole content is indexed by the time it leaves the working set.

*Persistence (required).* The vectors are derived and regenerable, and are kept across service runs
(§2.1 `.recall-cache/`), so a service run re-embeds only content that changed.

*Long threads.* Intra-thread recall on very long single threads must work with **no performance
decay**, validated in the §9 acceptance simulation by a long-running-thread workload with explicit
decay measurement (index size, per-query cost, retrieval latency) as thread count *and* per-thread
length grow into the thousands.

*Completeness floor.* Content not yet embedded is still found: intra-thread recall also matches the
engaged thread's not-yet-indexed content against the query's symbols, at a bounded cost per query.
Accepted residual: a single transient embed failure leaves a gap for that batch until the next
successful indexing; the guarantee is against indexing *lag*, not failed indexing. Summary-tree descent over a long thread is an approximate recall heuristic; exact recall is
guaranteed by the separate exact lexical / flat-scan level.

#### Recall candidates

At turn close the merged candidates are logged per signal (`spine.match-fire` /
`spine.embed-match-fire` / `spine.intra-match-fire`) and, when a channel to the user exists, the
top 3 are taken forward; three is the surfaced cap across the auto-fetch and ask bands TOGETHER.
With no such channel, recall stays log-only, including the auto-fetch band.

**Open:** U4 — the channel for the auto-fetch line and the ask offer below. Under opencode a plugin
has a yes/no question and no pick-list, so a multi-candidate offer cannot be a numbers/all/none
list.

#### Banding

Each surfaced candidate falls in exactly one band, by its score against its OWN signal's
threshold:

- **Auto-fetch band** — score ≥ `recall.symbolic-auto-threshold` (0.65) for a symbolic-scored
  candidate, or ≥ `recall.cosine-auto-threshold` (0.75) for an embedding- or intra-thread-scored
  one. The thread is loaded through the SAME fetch path an accepted candidate takes, with no
  question and ONE user-visible line, `recalled thr_N: <display name> — <gist>` (the standing
  `thr_N: display — gist` thread reference, so the fetch is verifiable against `/topics`; the
  **gist** is the thread's registry summary, else its projected anchors). Logged
  `recall.accept ... ack=auto`; the `recall_fires` increment (§2.2) applies as for any accept.
- **Ask band** — surfaced but below the auto-fetch bar. ONE offer block per turn; per candidate: the
  thread's §2.2.2 display name (never a bare `thr_N`), a gist (the thread registry summary, else the
  projected anchors — never blank, never a raw id), WHY it matched (the matched symbols, or for an
  intra hit the matched turn numbers), and a short signal label with the score. The question states
  what accepting does: one candidate → `pull into context? [y]es / [n]o`; several → a selection
  among the enriched lines (**Open:** U4 — its form). No decline-reason question in this mode.
- **Below the surface threshold** — never surfaces. (The surface threshold is each signal's lowest
  bar: `recall.symbolic-threshold` for symbolic scores, `recall.cross-project-threshold` across
  projects; the cosine signals' floor is not a directive key.)

Under `banded`, a candidate already resident in the active set is answered by the memory service
itself: `recall.decline reason=already-known`, no question, no line. An intra-thread hit is exempt
(its subject is the engaged thread's early content).

#### Recall skips content already in the request (in-context exclusion)

**In-context exclusion:** an accepted intra-thread candidate is not offered again while it is in
the request; it can be offered again once its thread leaves the active set. This is not a
completeness exception — the content is in the working window. The exclusion is scoped to the
composed request: content already in it is not added a second time, whatever session or service
run the request belongs to.

#### Mode parameter

`recall.ack-mode: banded | always` (§2.6.1), default `banded` — the behavior
above. `always` asks about every surfaced candidate: one offer followed by a decline-reason
question, and nothing is fetched without an ack.

Every candidate the memory service touches is logged: `recall.accept` (with `ack=human|auto`, §2.8) or
`recall.decline` (with a §4.3 reason).

#### Superseded symbols still match in recall (lifecycle-aware symbolic scorer)

Symbolic recall matches a thread on its anchors and on every symbol retained in its history, so a
superseded symbol stays matchable. A matched symbol counts 1 if it is active in that thread and
`recall.superseded-weight` (default 1.0, no down-weight) if superseded; a symbol that is also an
anchor counts as active; the union the score divides by is unchanged. The default is lowered below
1.0 only if the simulation shows abandoned threads crowding out focused ones (§9).

### 3.5 Closing a thread

Trigger detection (engagement decay or an explicit close request, `/done`) → summarizer-drafted
summary → outcome → frontmatter + thread registry update → demotion out of the active and dormant
sets. Cold storage (§3.8) follows a close under thread registry size pressure (the registry
holding more records than archival allows, §3.8).

**Open:** U4 — the channel for every interactive step below (the exception drain, `/done`, the
`[e]dit` choice, `always` mode, the pending-queue announcement) and the command that triggers each.
The rules for decay, classification, the derived queue and the summarizer draft are decided.

Engagement decay fires when a thread has gone unengaged for at least `engagement.decay-turns` turns OR at least
`engagement.decay-time` since its `last_engaged` (§2.6.1): `decay-time` catches extended user absence, `decay-turns` catches
low-frequency engagement in a busy session.

The summarizer-drafted summary targets 100–150 chars: summary + anchors must fit the registry
slot's budget at realistic thread registry size while letting the model recognize prior engagement
without fetching the body.

**Routine close-outs are auto-accepted; exceptions are batched at drain points.** A **drain point**
is a moment at which the queued exception close-outs are put to the user. At the decay-detection
scan (run at turn close, finding the threads whose engagement has decayed) each candidate is
classified:

- **Exception** — the thread is anchor-rich (≥ 6
  anchors). These criteria are provisional
  values pending real-use data.
- **Routine** — everything else.

A **routine** close-out is applied without asking: the summarizer's summary, `resolution=resolved`,
the same frontmatter + thread registry write and demotion out of the active and dormant sets an
accepted ack performs, and ONE user-visible
line, `closed thr_N: <display name> — <summary>` (the id leads because `/back-to <thr_id>` is the
revision path). It rides the turn transaction already open around turn close (§4.5.8) and adds no
commit path of its own. **The revision path for a wrongly-summarized routine close-out is
re-engagement** (`/back-to`, §4.2): it returns the thread to `active`, and its next close re-drafts
the summary.

An **exception** close-out is **queued** silently: no summarizer call, no question, one
`retire.pending` event (the `retire.*` events record closings). The queue is **derived, never stored** — a pending close-out is a
decay-eligible thread that classified as an exception — so it is crash-safe and correct across
restarts. It is drained with the full interactive closing dialog at **drain points only**, and on
demand via `/closures` (§4.2). An opencode session never exits, so there is no clean-exit drain
point; **Open:** U4 — the drain point that takes its place. A non-empty queue is ANNOUNCED in one
user-visible line (`N closure(s) pending review — /closures`) when an opencode session starts and
never put to the user as a question.

#### The offer names the thread

Wherever the interactive closing dialog runs — a drain point, `/done`,
or `always` mode — each offer shows `idle topic thr_N: <display name> — <gist>` above the
summarizer's draft summary.

**`/done` is always fully interactive** in both modes. A draft that **fails or comes back empty is
never auto-accepted**: it logs `retire.curator-error` (the summarizer-failure event) and is retried at the next scan. (A
persistently failing summarizer therefore leaves its thread un-closed rather than queued.)

#### Mode parameter

`closure.ack-mode: auto | always` (§2.6.1), default `auto` — the behavior above.
`always` asks interactively in-session for every decayed thread; nothing queues under it.

#### Ack-quality instrumentation

The indicator metric is **ack-edit-rate**: the
ack question offers an `[e]dit` choice (§4.2 `/done`, the `/closures` drain), and `retire.ack` (§2.8)
records `edited=yes|no` for EVERY acked outcome (close and wip) — `yes` only when the submitted
summary differs from the summarizer draft. `retire.ack` also carries `ack=human|auto`, and the rate is
computed over `ack=human` lines only (an auto-accept is unedited by construction and would drive the
rate toward zero). Auto-accepts are still recorded (`retire.ack ... ack=auto` plus
`retire.complete`). *Evaluating* the rate — distinguishing "drafts are perfect" from "the user
stopped reading", and judging the auto/exception split (a miscalibration shows as a zero human
edit-rate while auto-accepts are corrected by hand with `/back-to`) — needs a real human and is a
§9.1 known unknown (accounting level 3: deferred to empirical human-use data) until then.

### 3.6 Fallback dissection

Budget-pressure trigger (`dissect.pressure-threshold`, §2.6.1) → the **clusterer** (the model call that groups the oldest content into
threads to close, §5.3) clusters the oldest content → batch close → ack flow.

The grouping is best effort: under stress, thread quality degrades gracefully; content is not lost.

### 3.7 Cross-project digest maintenance

Each project's digest is brought current with its threads at each day rollover and archival, and stays within
`cross-project.digest-per-project-bytes`.

### 3.8 Cold storage

Closed threads are moved to **cold storage** — archived out of the thread registry under thread
registry size pressure and restored by explicit fetch; an archived thread's bytes are kept in the
history repository. **Size pressure** is the registry holding more records than
an upper bound; archival then drains the coldest closed threads until the registry is down to a
lower bound, the **low-water mark**.

#### Runs only at day rollover

Archival runs only at the day rollover, so the history repository is written only then, apart from
crash recovery. An archival interrupted by a crash is completed at the next open.

#### 3.8.1 Mechanism

Archival drains a batch of the coldest closed threads at once. The batch's membership is recorded
durably before any thread directory is removed, so a crash mid-batch can be completed. Each
archived thread's bytes stay reachable in the history repository and verifiable against a hash
recorded at archival. The batch leaves the registry in one change and adds a bounded number of
commits, not one per thread. `archive/index.jsonl` (authoritative, sorted by `thr_id`) lists each
archived thread with its summary and anchors as they stood at archival, its project, its archive
time and, once restored, its restore time. Losing the index loses no thread bytes.

#### 3.8.2 Storage properties

Personant never rewrites the history repository's commits, so archived threads stay restorable
however the repositories are packed.

#### 3.8.3 Restore

`personant archive recover <thr_id>` restores an archived thread under its original id (a restore
never mints a new id), in state `wip`, taking its anchors and summary from its own restored files
rather than the archive snapshot. The restored bytes must match the hash recorded at archival; on a
mismatch nothing is restored and the command fails with an integrity error, so a partially restored
thread is never written onto the thread registry. A thread with no archive entry is refused. The
entry is kept, marked with its restore time, and the restore is committed to the memory store's git
history.

`personant archive list` / `personant archive recover <thr_id>` show cold-storage history.

##### Two recovery surfaces, different strength

Archived *threads* are restored from the history repository, whose commits Personant never
rewrites: an unconditional guarantee. File content a thread no longer keeps (§3.9.1) is not
Personant's to recover; its content id names it in the user's version control.

#### 3.8.4 Recall treatment of archived threads (v0.1: off-recall)

Archived threads are **never candidates for live recall** in v0.1; the archive index's anchors and
summary serve `personant archive list` and forensics.

##### Measurement consequence (§9)

An expected match that is archived and restorable is excluded from the live-recall score rather
than counted a miss; an expected thread in neither the registry nor the archive index is an
integrity failure and must never occur.

### 3.9 Working-set content dedup

This section covers the slots Personant composes into the working set (§3.1). Dedup never
rewrites opencode's stored transcript or edits its tool-result rows (the §1.7 database cleanup is separate): a transcript row is
dropped from the request once memory has captured it, so repeated file copies there never reach
the model.

File content the assistant reads or writes appears in the composed context at most once as full
content, at its most recent position. Every other place it appeared carries a content-addressed
content id in place, so the context still shows that the file was read or modified there, and in
what order. Per-turn composition cost and context size scale with what the context holds, never
with how many times a file has been read or edited; this is what lets long-running work fit a
fixed budget. Persistent storage (§3.9.1) holds only what composition can render; composition
(§3.9.2) decides each turn which positions render content.

#### 3.9.1 Persistent storage and retention

What is stored is the file's bytes, never a tool's formatted rendering of them.

**Content id.** A file content's id is computed from its bytes alone, in the form a git
object id for those bytes takes, so a version control system that holds that version resolves it.

**Positions.** Each `fs.read`, `fs.write` and `fs.commit` of a turn is recorded in the owning
thread's excerpt for that turn as one content-id line — operation, a prefix of the content id (of the commit hash for
`fs.commit`) and path — in delta order. A content-id line is written once and never rewritten.
Personant never writes file bytes into an excerpt.

**What is kept**, per thread and file: the current content (the bytes of the file's most recent
read or write) and its `dedup.live-diff-window` most recent changes, each as a diff from the prior
content to the new. A change whose diff is at or above `dedup.diff-literal-threshold` of the new
content's size keeps no diff. Nothing else is kept: superseded content, and files whose most
recent read or write has left the thread's recent-excerpt window
(§2.3), leave storage at the next
day rollover.

**Bounds are Personant's own.** Storage per thread is bounded by the files read or written within
its recent-excerpt window times (current size + the diff window), whatever the user does with version
control — commit, rebase, or no repository at all. Storage, retention and composition never
consult the workspace repository.

File content is kept only as long as composition needs it. The tracked-file store is held in the
store working tree and the turn repository, which is deleted at each day rollover; the history
repository never holds it. **Open:** the implementation still commits the tracked-file store to the
history repository at each day commit (ROADMAP "Tracked-file content in the history repository").

**Earlier versions.** A superseded version of a file is not content evicted for budget in the
§1.4 sense, and Personant does not keep file
history. An earlier version is recovered, where it can be, through the user's own version control
or opencode's snapshots; its content id is a git blob id, so it resolves there. Personant
guarantees nothing about it.

##### Symbol lifecycle for file-edit deltas

`fs.read`, `fs.write`, and `fs.commit` deltas are
task-class (§3.10.1): their symbols enter the staging buffer and reach `symbols.jsonl` only if a
decision delta cites them within the citation window (K = 3, §3.10.3).
#### 3.9.2 Live context composition (working window)

A file delta's position is the excerpt that holds its content-id line
(§2.3). Content for a position
renders within that excerpt, after the excerpt's text; the content-id line itself stays as
written. This happens only for positions the context holds:

- **Full current content** at each file's most recent read or write position, when the context
  holds that position and the content fits there. One file, and one content, render as full
  content at most once across the whole composed context, active-set threads included. Full
  content is never moved to an earlier position.
- **The N most recent changes** of each file (`dedup.live-diff-window`, default N = 3) render as
  diffs in full at their positions, when the context holds them. A change whose diff was not kept, or is at or above
  `dedup.diff-literal-threshold` of the file's current size, does not render, and no older change
  renders in its place.
- **Every other position** renders as its content-id line alone.
- **A file too large for its thread's share is never rendered as content.** When a file's current
  content is at or above `dedup.literal-share-threshold` (default 0.25) of its thread's share of
  the active-threads slot (§3.1), no full content and no diff of it renders anywhere in the
  composed context; every position of it is its content-id line alone.

Positions are never rewritten in storage; replacement is a composition decision each turn. Content
rendered at a position counts against the budget of the slot holding it, together with its
excerpt (§3.1). Content under the share threshold that does not fit renders as its content id with its size,
except where the excerpt holding its position leaves no room even for that size note; there
its content-id line alone names it. Either way it is content evicted for budget in the
§1.4 sense, named at the version it had, and
the store keeps those bytes (§3.9.1).

**Open:** B6a — bringing back on demand the content of a file at or above
`dedup.literal-share-threshold`. Such a file is shown only by its content-id lines; no mechanism yet
brings its content back.
#### 3.9.3 Diff format

##### Default: unified diff, from the prior content to the new

**Fallback: natural-language descriptive diff** ("added a line `foo`
after line 12; removed lines 20–22"), which the memory service may switch to per-diff where unified diff is
hard for the model to apply (complex multi-region changes, very long files). The directive
`dedup.diff-format` (§2.6.1) names the global default.

#### 3.9.4 Content-id-only references

A content-id line alone is the normal form of every earlier read or write of a file, and of every
commit: it says *that* the file was touched there, and in what order. When cognition needs the
content of a version no longer kept, recovery is the user's path, through their version control.

##### Active reasoning keeps full content at the most recent position

A file read or written in an engaged thread keeps its full content at its most recent position in
the active-threads slot, when the context holds that position, however many positions precede it,
provided it is under `dedup.literal-share-threshold` (§3.9.2). A file at or above it is shown by
content id only.
### 3.10 Transient-data lifecycle

Task-class context (tool outputs, shell captures, file reads) is valuable for a span of turns and
then worthless. This two-stage mechanism keeps task-class content discardable while preserving the
symbols it produces when — and only when — a decision delta cites them.

#### 3.10.1 Provisional classification at delta time

Each delta entering the §3.0.2 pipeline is assigned a **retention class** before any pipeline step
runs, mechanically from its source (§3.0.1):

| Source | Provisional class |
|---|---|
| `tool.result`, `user.shell-capture`, `fs.read`, `fs.write`, `fs.commit` | `task` (provisional-transient) |
| `user.prompt`, `model.response`, `thread.fetched` | `decision` (provisional-persistent) |
| unknown source | `decision` (safe default — unknown content is never silently dropped) |

The class governs where extracted symbols land (§3.10.2); the citation window (§3.10.3–§3.10.5)
confirms or discards them.

One user override: a `task`-source delta the user flags as reference material is reclassified to
`decision` at emit time (e.g. a paper ingested for ongoing reference). **Open:** U4 — how the user
flags it (opencode offers no mechanism for a prefix or command). The assistant has no
parallel override; assistant-produced retention flows through decision-delta authorship (§3.10.4).

#### 3.10.2 Staging buffer

Symbol extraction (§3.3) runs on every delta regardless of class. Where symbols land:

- **Decision-class delta:** its symbols commit at turn close to the thread's `history_symbols` and
  to `symbols.jsonl`.
- **Task-class delta:** its symbols are staged and do not enter `history_symbols` or `symbols.jsonl`
  yet.

Staged symbols persist across turns. A symbol carried again by a later task-class delta is staged
afresh, so its window restarts.

#### 3.10.3 Citation window K

A staged symbol is eligible for promotion from its staged turn S through S + K − 1 inclusive.

**K = 3** (same-turn citation, next-turn citation, and one more turn for an intervening tool call or
clarification). K is a **calibration target** earned by the §9 recall-fidelity simulation; it is
currently fixed at 3, and its future directive key is `staging.window-turns`.

#### 3.10.4 Promotion

When a decision-class delta is processed, each §3.3 extraction candidate with a matching staging
entry (normalized-form match only — no fuzzy matching):

1. is **promoted** out of staging;
2. commits at turn close to `history_symbols` and `symbols.jsonl` normally, attributed to the
   decision delta that cited it;
3. carries only the symbol across: the raw bytes of the task-class delta that produced it are
   **not** promoted.

A promotion is logged as `staging.promoted`.

#### 3.10.5 Window-close expiry

A staged symbol not cited within its window is dropped before the next turn's first delta. A
nonzero expiry count is logged as `staging.evicted`. Expiry is deterministic; no model is involved.

#### 3.10.6 What stays transient

**Never** written to the memory store:

- Raw bytes of task-class deltas (file content from `fs.read`/`fs.write`, tool output from
  `tool.result`, captured output from `user.shell-capture`, and the `fs.commit` pointer delta). The
  event log records a short metadata summary instead (source, path, byte count). The one exception is file bytes from `fs.read`/`fs.write`, which the tracked-file store keeps only as long as composition needs them (§3.9.1).
- Carve-out: each `fs.read`, `fs.write` and `fs.commit` delta leaves one content-id line in its
  turn's excerpt: operation, a content-id or commit-hash prefix, and path
  (§3.9.1). That
  line is persisted; the delta's raw bytes are not.
- Symbols from task-class deltas not cited by a decision delta within K turns.

#### 3.10.7 Why this matters

Without this lifecycle, every path or URL in a `tool.result` would inflate `history_symbols`, raise
Jaccard's denominator, and degrade §3.4 precision, compounding over a §9.1 simulation. It is a
prerequisite for a meaningful recall-fidelity measurement (§9).

#### 3.10.8 Content retention (keep/toss) — deferred precision stage

§3.10.1–.7 govern *symbol* promotion. **Content retention** — which retained turn-excerpts (§2.3)
survive on disk for §3.4 intra-thread recall — is the adjacent keep/toss decision. v0.1
**over-retains**: all decision-class excerpts are kept, and raw task-class tool output is never an
excerpt. The **precision stage** that trims the retained set is deferred to consolidation's
keep/toss (ARCHITECTURE, "Memory consolidation"), with this settled mechanism:

- **Raw tool output → always transient**, deterministically, with no LLM involvement.
- **The assistant's *re-presentation* of tool data → the durable candidate**, marked inline by the
  assistant with a pinned closed-set lifetime tag — exactly one of `lifetime:transient` |
  `lifetime:durable`, at a fixed stripped position (metadata, never shown to the user). This is the
  one LLM judgment in the mechanism.
- **Fail-safe = durable.** A missing/malformed lifetime tag resolves to *preserve*. Compliance is
  measured in the inference-in-loop sim by the omission rate.
- The lifetime tag's primary job is **downgrading** a routine tool-data presentation to
  `lifetime:transient`: `durable = decision content − assistant-marked-transient`.

The in-turn assistant lifetime tag and the offline consolidation keep/toss (outside v0.1 scope) are
the **same decision at two cadences**.

### 3.11 Memory store commit cadence

The memory store is backed by **two** git repositories (§4.5.8): the **turn repository**
(`.git-daily/`, disposable, per-turn) and the **history repository** (`.git/`, permanent, day-grain
career history). Policy: **per-turn commit to the turn repository, day-grain commit to the history
repository**.

#### Per-turn (turn repository)

Every turn commits its writes to the turn repository at turn close (the ≤1-turn durability
boundary of §4.5.8), at a cost that does not grow with the store.

#### Per-day (history repository)

The day rollover writes exactly one day-grain commit to the history repository (an empty day still
yields one) plus the day's archive commits (§3.8). The history repository grows by at most one day-commit a day (~365 a year) plus archival's bounded commits,
and integrity checks run once a day: the day's commit passes thread registry
integrity validation, and the rollover leaves derived files fresh. The turn repository is deleted and recreated
at each rollover.

**Checkpoint (clean service-run stop + project creation) → turn repository.** A Personant
**checkpoint** is a commit of the whole store working tree, hand-edits included, to the turn
repository, made at a clean service-run stop and after a project is created. Creating or closing a
thread lands in its turn's own commit. The history
repository is written only at day rollover, apart from crash recovery (§4.5.8).

#### gc cadence

The store's git repositories are packed automatically as they grow.

#### Durability

Content is durable to ≤1 turn at all times; the in-flight turn's prompt and response are
recoverable from the moment the prompt is journaled; a failed journal write aborts the turn (§4.5.8 (7)). §4.5.8 governs crash recovery.

---

## 4. User surface

Personant's user interface has two parts: the `personant` command line, which runs outside
opencode, and **session commands** — slash commands typed inside an opencode session — where U4 keeps them. Everything else the user touches —
the terminal UI, line editing, shell escape, permissions, tools, model and provider selection — is
opencode's.

### 4.1 CLI command surface

Binary subcommands (see also §2.1):

- `personant init` — first-run scaffold; idempotent (see §8.1).
- `personant index rebuild` — regenerate derived files from authoritative files (§2.1).
- `personant index check` — validate derived files match authoritative files; non-zero on mismatch.
- `personant verify` — full structural validation.
- `personant version` — print version identity (the memory service's semver line, the store format
  revision this binary writes, the build id) plus the resolved store directory and the format
  revision found there. **Exempt from the format check** (§9.1): never runs the format check, never
  runs startup recovery, never fails on a broken memory store. `personant --version` prints the
  version lines in one-line short form only.
- `personant archive list` / `personant archive recover <thr_id>` — cold-storage archive (§3.8.3).
- `personant search <query>` — symbol/text search across the thread registry and threads.
- `personant deps <thr_id>` — show threads referenced by anchors of the given thread.
- `personant log <date|range>` — query the event log.

Persistent flags on every subcommand: `--home` (override the store directory for the invocation,
§8.2.3) and
`--allow-newer-home` (open a store directory whose on-disk format revision exceeds this binary's — §9.1; leaves a stderr
warning and a `system.home-format-override` event).

### 4.2 Session commands

Commands that alias behavior opencode already provides are not Personant's: model and provider
selection, reasoning display, API keys, terminal setup, help, version and quit are opencode's.
`/compact` and `/clear` are opencode's, and neither discards memory (§1.1).

**Open:** U4, per command — each session command below is kept as a plugin-registered command, made autonomic, or dropped. Unverified:
how opencode's UI shows a command whose handler sends no prompt, and how the handler reports a
result to the user. The purposes below are what each memory function does, independent of its
channel.

#### Vocabulary bridge

Commands and their output say **thread**, the term used throughout this SPEC; there is no separate
user-facing word. Where a command acts on "the active thread", that is the thread at the front of
the active set (the most recently engaged). The command names `/topic` and `/topics` keep their spelling. A thread's id
(`thr_N`) is rendered to the user unchanged because they type it back.

| Command | Purpose |
|---|---|
| `/topics [all]` | list the active project's threads. Grouped ENGAGED / PAUSED / DORMANT / RECENTLY CLOSED, recency-sorted within each group, one line per thread in the §3.4 thread-reference shape: `thr_N`, display name, gist, relative age. Recently-closed threads are in the default view, with the `/back-to` hint, because an auto-accepted §3.5 close-out is revised that way. The default view is bounded (20 threads, then a count of what it withheld); `all` lifts the bound and the closed-window cutoff |
| `/done` | request the closing ack on the active thread (§3.5); always fully interactive |
| `/closures` | drain the §3.5 queue of exception close-outs now. The same drain runs at the §3.5 drain points. An empty queue says so |
| `/pause` | mark active thread as `paused` (§2.2.1) |
| `/resume` | resume a paused thread |
| `/back-to <thr_id>` | re-engage a closed thread — the revision path for an auto-accepted close-out; see `/topics` |
| `/project` | print active project (id, name, the project's directory, remote URL if any) |
| `/project switch <name-or-id>` | switch active project to a known one |
| `/project rename <new-name>` | rename the active project; updates `name` only (id, path, remote URL unchanged) — see §4.5 |
| `/stats` | memory service stats (active threads, slot fill, recent recall events, etc.) |

### 4.3 Decline categorization

Every decline is recorded with a reason from a fixed enum: `not-relevant` / `wrong-project` /
`already-known`. The enum favors recall-tuning signal (why a candidate missed); it may be retuned
when feedback directive tuning of recall (§1.8) is built.

#### Who supplies the reason

Under the default `recall.ack-mode: banded` the user is NOT asked to
categorize a decline. A banded decline logs `not-relevant`, except that a candidate already resident
in the active set is declined `already-known` by the memory service itself. Under `recall.ack-mode: always` the
reason is asked. **Open:** U4 — the channel that carries the question.

§4.3.1 (REPL line editing and history) and §4.3.2 (turn progress indicator) are unused: opencode
renders the terminal UI. §4.3.4 (tool receipts) is unused: opencode shows tool calls.

### 4.3.3 Interrupted turns

Interrupt keys and terminal handling are opencode's. This section keeps the memory half: what an
interrupted turn leaves in memory.

**Open:** U8 — whether memory forgets what opencode's transcript still shows, or conforms to
opencode and ingests the prompt and the partial turn. Until decided, the rule below binds.

#### The retraction rule

A retracted prompt does **not enter memory**; only a deliberate
re-submission puts its text there. No partial response is kept, at any point relative to the
model's first token. Before the first authoritative write, an interrupt leaves memory exactly as if the
turn never happened — turn count, staged symbols, active and dormant sets, symbol history and recall
marks included — except that the event log keeps the lines the turn already wrote (sources and byte counts, never text) and gains its `system.turn-aborted` line; the next start finds nothing to recover. After it, recovery
owns the turn (§4.5.8). Consequences:

1. **Rollback.** An abort before any authoritative write restores that state to its pre-turn value
   (otherwise, e.g., a staged task-class symbol cited by the retracted prompt would be permanently
   consumed). Failures after an authoritative write do **not** roll back; recovery owns them.
2. **No durable full-text artifact.** The abort record is one §2.8 event line —
   `system.turn-aborted` with turn id, phase and byte count. Not the text.

An interrupt during a tool round counts as before the first authoritative write.

### 4.4 Shell escape

Unused (§4.4.1–§4.4.4 likewise). opencode provides the user shell and permissions. The output of a
command the user runs through opencode's shell escape enters memory as the `user.shell-capture`
delta (§3.0.1).

### 4.5 Project identity and the project's directory

#### 4.5.1 Active project directory

The active project is resolved from opencode's session directory (§4.5.7) unless the user selects
one explicitly. The active project's directory is that project's `current_root_path` (§2.5.1): after an
explicit switch it is the selected project's directory, not the session directory. The active project
selects the project-scoped directives (§2.5, §2.6). A directory change inside a command
the agent or user runs does not change the active project.

#### 4.5.2 Three levels of project identity

A project's identity has three levels (schema: §2.5.1):

| Level | Form | Stable? |
|---|---|---|
| Internal handle | `prj_<n>` | yes — assigned at creation, never changes; storage layout key, thread registry reference key |
| Remote identity | normalized git remote URL (`remote_urls[0]`) | mutable but rare; gained when the project's git tree first acquires a remote; survives file moves |
| Display name | user-chosen string | mutable via `/project rename` |

The internal handle is always present. Remote identity is *adopted* when a git remote
appears (§4.5.4). Renames touch only the display name; no thread registry records are rewritten.

#### 4.5.3 Resolving a directory to a project

Given a directory (opencode's session directory, §4.5.1):

1. Resolve the directory; query its git config for the remote URL that identifies the project (`origin` by default;
   otherwise none).
2. **If a remote URL is found:** look up known projects by `remote_urls`.
   - Match → switch (silent); update `current_root_path` if it has drifted from the matched
     project's stored value (and append the old path to `historical_root_paths`).
3. **Else (no remote at this path):** look up by `current_root_path`, then `historical_root_paths`.
   - Match → switch.
4. **No match anywhere:** ask the user:
   ```
   Path '<path>' is not associated with a known project.
   [s] switch an existing project to this path
   [c] create a new project here
   [n] cancel
   ```
   On `[s]`, list known projects (id + name) and let user pick. On `[c]`, ask for a display name;
   allocate a new `prj_<n>`; record path; auto-detect remote URL.

**Open:** U4 — the channel for the question above and for every interactive step in §4.5.4,
§4.5.5 and §4.5.7.

The active project does not change when a command inside the session changes its own working
directory.

#### 4.5.4 Local→remote adoption (auto-detect)

The memory service polls the active project's git config cheaply (e.g. once per turn or at engagement) for
its remote URL:

- **None → present.** Ask: `Project '<name>' just gained a git remote (<url>). Adopt as canonical
  identity?` Default `[y]`. Appends to `remote_urls`. Logged as `project.remote-adopted`.
- **Present → updated.** If it normalizes to the same URL, silent. Otherwise ask: `Remote URL
  changed (<old> → <new>). Update canonical identity? Keep the old one in history?` Default both.
  Appends new URL to `remote_urls`; old URL goes to `historical_remote_urls`. Logged as
  `project.remote-updated`.
- **Present → none.** Keep the historical entry; no demotion.

If a *different* known project already carries the just-gained remote URL in its `remote_urls` (or
`historical_remote_urls`), ask:

```
Project '<name>' just gained a remote (<url>) that already belongs to '<other>'.
[m] merge into '<other>' (move threads + spine entries)
[k] keep separate (leave both projects intact)
[c] cancel — do not adopt the remote
```

Logged as `project.remote-collision-prompt`.

#### 4.5.5 File-move detection

When the active project's directory is gone (ENOENT on `current_root_path`), the memory service asks:

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
the storage path (`projects/prj_<n>/`), the remote identity, and every thread registry record's `project`
field are unaffected. Logged as `project.renamed`.

#### 4.5.7 Project resolution at startup

When an opencode session starts, the active project is resolved from its session directory
(§4.5.1) by a three-step waterfall, else a final fallback.

##### Explicit selection

the user can name a project directly, skipping the heuristics. **Open:**
U4 — the command (`/project switch` is the only one defined, §4.2).

**Heuristic waterfall** (in order; first hit wins):

1. **Git remote match.** If the session directory (or any ancestor) is a git working tree, read its
   `origin` remote URL (or the first remote if no `origin`). Normalize per §2.5.1. Look up known
   projects by `remote_urls` and `historical_remote_urls`. On match: silent switch; update
   `current_root_path` if it has drifted (and append the old path to `historical_root_paths`).
2. **Directory path match.** If step 1 produced no match: check whether the session directory (or
   any ancestor) is `current_root_path` or appears in `historical_root_paths` of any known project.
   On match: silent switch; updates as in step 1.
3. **Last-active confirmation.** Otherwise read `<store-dir>/last-active` (operational; one line:
   `prj_<n>` of the most recently active project) and ask:
   ```
   Resume work on '<display_name>' (last active <RFC3339>)? [y/n/<other-name-or-id>]
   ```
   On `y`: silent switch. On `<other>`: treat as `/project switch` target. On `n`: fall through to
   final fallback.

**Final fallback** — fresh install, never-used memory store, or all heuristics declined:

```
No active project resolved. Choose:
  [c]  create a new project rooted at this directory
  [s]  switch to a known project (lists ids/names)
  [n]  no project (use prj_default)
```

`prj_default` is always available as the no-project default.

##### `last-active` file maintenance

The memory service writes the active project's `prj_<n>` to
`<store-dir>/last-active` whenever the active project changes — startup resolution or an explicit switch. The
file is operational (§2.1), single-line, plain text.

#### 4.5.8 Startup recovery after unclean shutdown

The memory service may be hard-terminated (crash, SIGKILL, power loss) mid-turn, leaving derived state (the
symbol index) inconsistent with the authoritative
files (`spine.jsonl`, thread files, logs). (This is distinct from the restore of an archived thread,
§3.8.3.) On startup the memory service **reconciles derived state against the authoritative files**
and **repairs a torn in-flight turn** before the service run opens. Startup order: **format check (§9.1) → startup
recovery → (day rollover if new-day) → service-run load**. The memory service **refuses to open** on
an unreconciled or unrepairable memory store. The **day rollover** is the once-a-day sequence defined in (5).

**Durability bar: at most one turn may ever be lost**, under any termination timing; the in-flight turn's
prompt and response are recoverable from the moment the prompt is journaled; a failed journal write aborts the turn ((7)).

##### Recovery needs no clean shutdown

Recovery needs no shutdown event, no model and no user acknowledgement; the on-disk state alone
determines its outcome.

##### (1) Detection observables

The on-disk signals recovery reads and how it classifies them: ARCHITECTURE "Crash recovery and the
turn and history repositories".

##### (2) Turn transaction protocol

A hand-edit to an authoritative file is committed at the next checkpoint (§3.11) or day
rollover, not with the next turn. Recovery never reverts it, except where a torn turn's reset covers its file; its bytes are then preserved in quarantine first ((4), §2.1). Recovery never re-drives the model.

##### (3) The journal

On a torn turn, the turn's prompt and response bytes are preserved and surfaced to the user
(**Open:** U4, the channel); the journal bytes are never replayed into memory. A torn turn that opencode kept is re-ingested from opencode's stored messages ((10)); otherwise the user re-issues if they choose.

##### (4) The recovery state machine

Startup recovery classifies the on-disk state and repairs it idempotently: a hand-edit is never
reset outside a torn turn; a torn turn is reset to the last committed turn after its dirty bytes, a hand-edit's included, are quarantined; an
interrupted day rollover or archival is completed forward; a missing turn repository is normal,
never a sign of data loss; a crash during recovery converges on re-run; an archive entry that
cannot be repaired stays refused for restore, never guessed (`recovery.unrepairable`), and does not stop the open; a torn final line of the
event log is completed, never truncated.

**Startup recovery reports once**: the repairs made, any paths quarantined-and-restored (empty on
the normal path), and any completion left pending.

###### Quarantine-never-delete

Recovery never deletes authoritative bytes it cannot re-derive. The
torn-turn debris removal and the persistent-verify-failure posture of (5) both **quarantine byte-exact
under `recovery/quarantine/<stamp>/`** before any removal or scoped restore, and report the moved
paths. An offending path absent from the restore source is quarantined and removed.

##### (5) The two repositories and the day rollover

**The store working tree is the truth; the turn repository is disposable.** Every authoritative byte of a live thread
is a file under the store directory; an archived thread's bytes are in the history repository only (§3.8.1); git is a recovery-point index over those files. Two git
repositories track the one store working tree at two cadences:

| Repository | git-dir | cadence | role |
|---|---|---|---|
| **turn repository** | `.git-daily/` | per-turn | today's fine-grained recovery points; **deleted and recreated at each day rollover**; never career history |
| **history repository** | `.git/` | per-day | career history — one day-grain commit per day + permanent archive commits (~365 day-commits/year); irreplaceable |

Archival and the day rollover never reset the store working tree; only torn-turn recovery does, and
only to the last committed turn. A day is committed to the history repository at most once, however
often a crashed rollover re-runs. The turn repository is removed only after the day's content is in
the history repository. History repository HEAD always passes thread registry integrity validation:
every write to it is validated first. Neither repository sees the other's git directory as
untracked content.

**Day rollover.** A **day** is a UTC calendar day; the **day index** numbers days by whole UTC
days since the Unix epoch, so it is the same in every process and unaffected by the local time zone
or daylight saving. The rollover runs at the first service-run open or turn of a new day, after
startup recovery. It archives under size pressure (§3.8), settles excerpt storage (§2.3), drops
tracked-file content composition no longer needs (§3.9.1), commits the day (overnight hand-edits
included) to the history repository, rebuilds derived files and recreates the turn repository. It
loses no authoritative content except what archival moves to the history repository and what file
retention deletes on purpose; excerpt moves are lossless and every excerpt stays readable
throughout.

###### Lifecycle git tags

After the day's commit, the history repository gains a tag for each thread created, closed or
archived and each project created, named `<kind>/<id>/<event>_<stamp>` (e.g.
`thread/thr_42/created_2026-05-08T03-12-00Z`), so names sort chronologically;
`git show <tag>:spine.jsonl` reads the store as it stood then. A life event on an earlier day on which no rollover ran is not tagged. A tagging fault never fails the
day's commit.

###### Roll-forward completion (crash inside the day rollover)

A crash inside the day rollover is completed forward at the next open, never by resetting the store
working tree. The completion does everything an uninterrupted rollover would, archival included,
and never duplicates an archival commit.

###### Day-commit-shape guard, at-most-once per day

Each completed day is committed to the history repository at most once, whatever the crash and
re-run timing.

###### Quarantine-and-proceed (persistent verify failure)

If the day's integrity checks still fail after one re-run, the offending paths are preserved
byte-exact under `recovery/quarantine/<stamp>/` and restored from the history repository, never by
resetting the whole store working tree. If the checks still fail, the memory service refuses to
open, and its report names the quarantine directory.

**Turn-repository init.** Recreating the turn repository costs in proportion to the live store, not
to the length of career history.

##### (6) Archival: day-rollover-only + roll-forward completion

When archival runs and how a crash during it is completed: §3.8.

##### (7) Honest-coverage levels

- **SIGKILL — full guarantee.** ≤1 turn structural loss under any kill timing; journaled content
  bytes recovered from prompt-time onward. Every crash point has an asserting scenario ((9)).
- **Turn-repository loss — never content loss.** Deleted, lost, corrupted, or absent, the turn
  repository is recreated from the store working tree, and a corrupt turn repository never
  prevents an open.
- **History-repository loss — unrecoverable.** A missing/corrupt **history repository** is
  **refuse-to-open + surface**, never auto-recreated.
- **Power-loss — qualified.** The ≤1-turn bar holds under fsync semantics, with two named residuals:
  (a) a journal emptied at turn commit can reappear after power loss; recovery recognizes it as
  already committed, with no loss; (b) a **journal-write-failure** aborts the turn before any
  authoritative write, losing that one in-flight turn's content (still ≤1).
- **Intra-day derived staleness — by design.** Derived files may lag between day rollovers; the
  rollover leaves them fresh. A hand-edit reaches them after the next full commit of the store
  working tree.

##### (8) Greenfield / legacy compatibility

A store with no recovery history (new, or written before the turn repository existed) is adopted
into the history repository on first open, after structural validation.

##### (9) Crash-injection methodology

Every point at which a crash can strike the turn, day rollover, archival, recovery and
turn-repository init sequences is exercised. After each crash: the full invariant suite holds; loss
is ≤1 turn versus a pre-crash oracle; journal bytes are recovered exactly; day-rollover and archival
paths do not reset the store working tree; exactly one day-commit exists per day; and a second
startup recovery run is a no-op. A crash point that is not exercised fails the suite.

##### (10) Resume under opencode

- **Reconcile at service start.** After startup recovery, memory is reconciled against opencode's
  stored messages. A turn that opencode kept and memory rolled back in crash recovery is
  re-ingested, so recovery's rollback does not leave the transcript the user sees ahead of memory.
  A turn rolled back by retraction (§4.3.3) is not re-ingested while the retraction rule stands.
  **Open:** U8 — reconcile's treatment of retracted turns follows the interrupted-prompt decision.
- **Resumed turn.** When opencode resumes an interrupted turn on start, the resumed request
  carries no new prompt. Memory opens a resumed turn on that request (§3.0.4), composes it, and
  ingests the resumed reply.
- **Every delta is attributed to the turn it belongs to**, including a resumed turn's deltas, not to
  whichever turn is current when it arrives.
- **The ≤1-turn bar is judged against turns the user saw complete.** A turn opencode recorded as
  terminal that memory rolled back is a lost turn for this bar until reconcile re-ingests it.

**Open:** U9 — restart persistence of in-run history and the turn counter (§1.6).

**Open:** resume cases not yet exercised — a graceful stop with a turn in flight, a standalone-kill
claim (the in-flight marker opencode leaves when a process run without its managed server is
killed) later swept by a managed server (opencode's background server, which resumes interrupted
executions at start), and recovery of the resume budget (the limit on resume attempts), background
jobs, subagents and shell.

### 4.6 Rendering

Unused. opencode renders; memory stores and composes source text.

---

## 5. Model interaction

### 5.1 Topic tag emission format

The model emits a topic tag at the **start of every response** identifying which threads the current
turn engages and which symbols attach to that engagement. The memory service parses the tag before any
other response processing.

**Open:** U3 — whether the in-band tag is kept (which needs a stream abort-and-re-issue, built per
provider protocol, and a way to strip the tag from the text opencode stores and shows) or replaced
by a recall tool (which changes §5.1, §5.5 and the tag-fidelity measures of §9.1). This section
(§5.1.1–§5.1.3), §5.5 and the fallback re-prompts of §3.3 specify the in-band tag and bind until
the decision lands.

#### 5.1.1 Format

Single-line, asterisk-bracketed, parsed deterministically:

```
*topic: <thread-list> [<anchor-list>]*
```

Where:

- `<thread-list>` is a comma-separated list of `thr_<n>` thread ids, optionally including the
  literal `*new-topic*` to indicate a new thread should be created.
- `<anchor-list>` is a comma-separated list of normalized tag symbols (per §2.7.2) attached to
  this turn's engagement.

Examples:

```
*topic: thr_42 [trefoil, unknot, body-topology]*
*topic: thr_42, thr_88 [trefoil, neutrino, helical-screw]*
*topic: *new-topic* [neutrino, oscillation]*
```

The topic tag is **advisory**: it declares which threads the turn engages, and the memory service decides
*ownership* (§3.2). Owner selection from the thread-list:
- **multi-existing** (`[thr_N, thr_M, ...]`): the owner is the **lowest `thr_N` id** among
  referenced existing threads; the rest are engaged-but-not-owner.
- **pure `*new-topic*`** (no existing thread referenced): the new thread owns the turn (genesis
  turn).
- **mixed** (`[thr_N, *new-topic*]`): the existing `thr_N` owns; the new thread is created
  **metadata-only** (`turn_count = 0`, no excerpt, with its `description` set per §2.3).

The tag symbols are the model's advisory per-turn symbol contribution (`source = model`), folded into
`history_symbols` — not a cardinality contract. **0 tag symbols is legal** (a vague start), and an
emission over `anchor.projection-max` **folds**: the deterministic projection (§2.7.4) keeps the
strongest and files the rest. The `anchor.projection-max` bound is a memory-service self-assertion checked
by `personant verify`, not a model contract. No topic-tag failure aborts a turn: a file edit with no
owning topic tag, or a missing or malformed tag, is at worst a re-prompt plus a defaulted binding
(§3.3).

The memory service strips the tag from the user-visible output before display. **Open:** U3 — under
opencode this has no mechanism; opencode's transcript keeps the tag unless the plugin strips it.

#### 5.1.2 Parser

A topic tag is recognized only as a whole line of the §5.1.1 form: a comma-separated thread list of
`thr_<n>` ids and `*new-topic*`, then a bracketed comma-separated anchor list, with whitespace
around entries ignored; anchor entries are normalized (§2.7.2).

The tag is searched for only within the response's **leading scan window** — its first 64 lines
or 8 KiB, whichever ends first; a tag-shaped line beyond the window is not recognized. If the
response contains multiple matches, take the first; warn-log the rest. An **empty thread
list** is not a valid topic tag; warn-log and continue without engagement update for this delta. An
**empty `<anchor-list>` is valid** (§2.7.4): a list of tag symbols that normalizes to empty does not invalidate
an otherwise well-formed tag.

##### Bare new-topic alias

The parser also accepts a line of the exact shape

```
*new-topic* [<anchor-list>]
```

as equivalent to `*topic: *new-topic* [<anchor-list>]*`. The alias is strict: line-anchored,
identical `<anchor-list>` grammar (empty-in-brackets valid), and a bare `*new-topic*` with **no**
`[...]` bracket pair remains invalid. The alias participates in the same first-valid-match rule and extras count
everywhere the tag is recognized.

##### Unclosed inner literal

Within an otherwise-valid `*topic: … [ … ]*` wrapper, the thread-list
token `*new-topic` (closing asterisk dropped) is accepted as the new-topic sentinel and normalized
to the closed form. Thread-list position
only, token-exact with a single leading asterisk (`new-topic` and `**new-topic` remain invalid); a
mixed thread list (`thr_3, *new-topic`) follows the same per-entry rule as the strict form.

#### 5.1.3 Prompt template

The system prompt instructs the model to emit a topic tag at response start. The topic-tag
instruction and its reminders are not user-configurable in v0.1.

The template carries an **ambiguity clause**, at most two sentences: the tag is required even when
the response is a clarifying question or the thread assignment is uncertain — tag the best-guess
thread, or `*new-topic*` if the work is genuinely new; the tag is an assignment signal, not a
commitment.

Each fallback re-prompt appends a terse system-side reminder. The tag-missing reminder (§3.3) names
the discard reason, restates the tag form, and demands the tag as the first line — even for a
clarifying question. The empty-response reminder (§3.3) names the discard reason (no visible text),
restates the tag form, and asks for brief hidden reasoning. Once appended, each reminder survives
any later same-turn recomposition (e.g. a subsequent §5.5 fetch re-prompt).

### 5.2 Summarizer prompt for closing summary

The summarizer is invoked when a thread closes (§3.5) to draft the thread registry `summary` from the thread's
accumulated body. The prompt instructs the model to produce a 100–150 char gist faithful to the
thread's operational content.

### 5.3 Clusterer prompt for fallback clustering

The clusterer is invoked under budget pressure (§3.6) to cluster the oldest in-window content
into proposed groups of threads to close. The prompt presents the candidate content and asks for a small
number of clusters with brief rationale.

### 5.4 Recognition-context construction

The system prompt assembles the thread registry display lines, active-thread bodies, and project
conventions into the working set, in the slots rendered by §3.1.

### 5.5 Mid-turn thread fetch (system-injected)

**Open:** U3 — under opencode the mechanism below does not fire; a recall tool the model invokes is
the alternative. Until decided, the text below binds.

When a topic tag references one or more `thr_<n>` not currently in the active set, the memory service fetches
those threads' contents and injects them into context **before the model generates its response
body**. The model does not invoke a tool to request the fetch.

The fetched thread enters as a `thread.fetched` delta (§3.0), within its active-threads slot budget
(§3.1).

#### Combined mid-turn re-prompt bound (with §3.3 fallback)

Re-prompts are capped at **1 per
cause**, and there are exactly three causes — missing-thread (this section), missing-tag (§3.3), and
empty-response (§3.3) — so a turn issues at most 3 re-prompts (≤ 4 model streams). A second miss for
the same cause falls through to that cause's deterministic close-time fallback (LRU pickup here;
owner-default for missing-tag; accept-the-empty for empty-response). The empty-response cause
subsumes missing-tag for a persistent-empty response (§3.3): the two reminders never both fire
against emptiness, with one exception — an all-whitespace response longer than the leading scan
window (§5.1.2) counts as missing its topic tag rather than empty, so it fires the missing-tag reminder instead
of the empty-response one. A fetch costs at most one aborted stream
plus one full stream; turns that trigger no cause pay nothing. Speculative pre-fetch is a non-goal
(ARCHITECTURE "No speculative prefetch").

---

## 6. Tool surface and permissions

Unused (§6.1–§6.5 likewise). Tools, permissions and the shell are opencode's. Tool output in
composed requests: §3.1. Tool-result events: §2.8. User workspace file content: §3.9.

---

## 8. Bootstrap and lifecycle

### 8.1 First-run scaffolding (`personant init`)

Idempotent. Creates the directory layout from §2.1; runs `git init`; writes seed
`directives/defaults.md` and `README.md`; writes Personant's configuration file under
`~/.config/personant/` (§8.2.2) as a template with a commented-out example block.

#### opencode settings

Init writes the settings opencode must hold for the plugin integration — automatic
compaction off (`compaction.auto: false`) and the plugin entry — into opencode's own configuration,
in opencode's own format. It adds nothing else to opencode's configuration.

### 8.2 Configuration sources and precedence

Personant reads three kinds of configuration: its own settings (§8.2.2), directives (§8.2.4) and
the environment (§8.2.3). opencode's configuration is read by opencode as it is; Personant writes
only the entries named in §8.1.

#### 8.2.1 Model access through the host

The memory service never calls a model. It requests each judgment it needs — the summarizer's
draft (§5.2), the clusterer's grouping (§5.3), model judgment on surfaced recall candidates (§3.4)
and embeddings for recall (§3.4) — from opencode, which runs the model with its own providers and
returns the result. Personant holds no endpoint, provider configuration or API key.

**Open:** A12a — how embeddings are requested through opencode.

#### 8.2.2 Personant settings: `~/.config/personant/config.toml`

Holds Personant-only settings. It is authoritative, hand-editable, and **not secret-bearing** — it names
providers and models, never keys. It never contains a setting that opencode's configuration also
holds.

```toml
[embedding]
model        = "provider/model"
vectorLength = 768
```

- `[embedding] model` — the embedding provider/model. **Mandatory for embedding recall** and must be
  explicit: the embedding model defines the vector space, and changing it invalidates the embedding
  cache.
- `[embedding] vectorLength` — optional; for embedding models whose vectors can be cut short,
  requests vectors of this length.

A model reference is `"provider/model"`, split on the **first** `/` (the model portion may itself
contain slashes, e.g. `openrouter/google/gemma-4-31b-it`).

The reference names one of opencode's providers and a model it serves. **Open:** A12a — the
startup check that it exists.

#### 8.2.3 Environment variables

`PERSONANT_HOME` overrides the store directory (default `~/.personant`; §2.1).

#### 8.2.4 Directive precedence

Detailed in §2.6. Briefly: `directives/defaults.md` < `directives/user.md` <
`directives/prj_<n>/project.md`.

### 8.3 Memory store validation in automatic git operations

The memory service owns the store directory's git repositories: it initializes them on first run
and commits authoritative changes itself, with no user ack and no model. Every day-commit and
archival write to `<store-dir>/.git/` leaves the memory store structurally sound (what
`personant verify` checks), and every day rollover and archival batch leaves its derived files fresh (what `personant index check` checks).
Turn-repository commits are checked by neither.

#### No git pre-commit hook is installed

A human who runs `git commit` manually inside
the store directory is operating outside the memory service and owns the consequences.

---

## 9. Measurement and validation regime

Personant's behavioral claims — months-long continuity, drift-resistant memory, opportunistic recall,
close-and-restore cycles that preserve meaning — must be proven by
simulation **before** real use accumulates the state a structural defect would put at risk. This
section defines that proof: the acceptance criterion (§9.1) and the instruments that measure it
(§9.2–§9.4).

### 9.1 The four-month simulation (the v0.5.0 acceptance criterion)

The acceptance criterion (memory service v0.5.0, Version lines):

- **Simulate four continuous months (120 calendar days)** of realistic usage via a synthetic
  workload (§9.4) running against the real memory service with a mock LLM (§9.2) supplying canned
  responses.
- **Memory quality maintained** throughout, measured via the metrics emitted at every event (§9.4):
  recall hit rate, engagement accuracy, closing timing, round-trip information-preservation
  through close→archive→restore.
- **Zero out-of-context-space events.** The normative criterion is the whole-request token ceiling
  (`context.token-budget`): the fully-assembled request (every §3.1 slot, including tool definitions
  and this turn's tool results, plus injected fetches) never exceeds it, checked against the token count
  reported for the request. Any overflow is a fail. On mock run configurations a secondary check asserts each
  rendered slot stayed within its derived byte allocation (§3.1). Turn-time budget enforcement is
  deferred past v0.1 (§3.0.2 step 4).
- **Operation runtime costs measured and within bounds.** Wall-clock P50/P95/P99 latency for:
  engagement update, recall match, thread fetch, thread close, archival, recovery, index rebuild, index
  check. "Within bounds" means stable across the simulation, not a threshold that grows with
  accumulated state.
- **Steady-state demonstrated.** After 120 simulated days the trajectories of working-set size,
  thread registry size, and per-operation latency are **flat**: the system is in a state from which it could
  run another four months without degradation.

#### The acceptance ladder

A run's **span** is the simulated calendar time it covers, in **days** (most of a day is
not spent working). The ladder is the set of spans run:

- **Smoke spans — `1d` / `7d`.** Regression catch; the default test run exercises `1d`. Not analysis
  spans.
- **Real ladder — `15d → 30d → 60d → 120d`.** Exact doublings. The top span is 120 days.

**Long-span symbolic precision is a corpus-saturation floor, not a regression.** On long spans
symbolic-signal precision/F1 declines and the unresolved-episode rate rises while recall stays flat:
the fixed sim vocabulary lets dormant non-sibling threads (threads outside the true match's simulated topic family) clear the Jaccard threshold alongside the
true match. The decline is bounded; F1 settles. Spans past ~30d partly re-measure this floor.

#### Recall-signal scope

The standard acceptance run uses the mock LLM (§9.2) with no embedder, so it
exercises only the §3.4 symbolic Jaccard signal — not the embedding signal that is primary in
production, nor model judgment. Consequences:

- Symbolic-only metrics are not reported as system recall. A within-thread-drift miss on the
  symbolic signal is expected, not a memory-service defect.
- Inference-in-loop and embedding-in-loop run configurations, each individually configurable, are required for
  convergence. They run as periodic deep validation; the mock regime stays the default iteration
  loop.

**The acceptance criterion is realism convergence, not a single run.** The criterion is the full top
span (120 days) passed with every identified **realism element** (a behavior of real use the
synthetic workload must reproduce for the simulation to be representative) accounted for, including elements
surfaced by prior runs. Each run that exposes a missing realism behavior (or a defect previous
incoherence masked) is followed by addressing it; the criterion is met at the run that surfaces **no
new realism gap**.

A realism element is accounted for by one of, in descending preference:

1. **Simulated** — exercised end-to-end in the workload.
2. **Modeled and attempted** — a written model of its expected effect, a solution implemented
   against that model, and unit tests over its decomposable parts.
3. **Known unknown, deferred to empirical human-use data** — parked explicitly; real use supplies
   the data and a later iteration of the memory service closes the item.

#### Honesty clause

the acceptance claim documents its own coverage (simulated vs. modeled vs.
parked), so a green check never overclaims. A forgotten realism element is a failure; a documented
deferral is not.

#### Accounted for

- **Evolving anchors** — *simulated.* Threads drift from their creation topic without a jump sharp
  enough to cut a new thread, premises invert, and an abandoned premise stays a findable handle. The
  evolving-anchor model (§2.2/§2.3/§2.4/§2.7.4/§3.2/§3.4/§5.1) is exercised end-to-end by a coupled
  vague-start, drift, premise-inversion and interleave workload.
- **New thread as synthesis of prior threads** — *accounted for at the symbol level.* Multi-parent
  provenance is carried as per-symbol `derived_from` (§2.3/§2.7.3), populated deterministically on
  recall; the non-recall residual is an explicit deferred decision (§2.7.3). The sim models
  synthesis threads that borrow symbols from ≥2 prior threads; recall stays provenance-agnostic.

#### Open acceptance requirements

Each must be accounted for before the acceptance criterion is met. The list grows as
runs surface new gaps.

- **Workload interleaving.** The workload must interleave excerpts and queries from several
  loosely-related or unrelated topics within a work block (§9.4.1), with abandon-and-resume and non-sequiturs.
  Until it does, recall/precision numbers describe a too-coherent stream.
- **Topic clustering / deep dives, metronomic edit cadence (edits to the same files at a fixed
  rhythm), cold start.**
- **Transient-data lifecycle (§3.10) fidelity** — a prerequisite to an honest recall-fidelity
  measurement.
- **Topic-tag fidelity under real inference.** The mock scripts topic tags, so tag quality is
  otherwise unmeasured. The inference-in-loop run configuration reports, as first-class metrics: re-engagement
  miss rate (turn engages a known thread, tag omits it), spurious `*new-topic*` rate (tag cuts a new
  thread where the oracle expects re-engagement), and tag-symbol overlap vs. the deterministic
  extraction pass. **Open:** U3 — these measures change if the in-band tag is replaced by a recall
  tool.
- **Embedder-enabled completeness run configuration.** A live embedder with mock inference over several
  sim-days, asserting that every flush-lag findability-gap probe (a recall query aimed at content that has left
  the recent-excerpt window but whose embedding flush has not yet landed) is surfaced and that at least one was
  observed. The §3.4 flush-lag completeness claim is accepted only when this run configuration runs green.
- **Registry-slot saturation stress.** At display-line lengths and the current partition, the
  registry slot saturates in the low hundreds of threads; a multi-year career accrues thousands,
  beyond what the 120-day span reaches. A stress run configuration with a synthetic pre-seeded
  multi-thousand-thread thread registry measures (a) recognition quality across the registry slot's
  truncation boundary, (b) whether opportunistic recall picks up recognition for threads that fell
  out of the rendered registry slot, and (c) §3.8 archival
  onset under genuine cardinality pressure.

#### Version lines

The memory service this spec defines carries a semver
line; `personant version` prints the current value, `personant --version` prints it in one-line
form, and `system.bootstrap` records it once per process open (§2.8). CONVENTIONS, "Versioning &
acceptance", carries the bump contract. The four-month acceptance criterion converges the memory
service. Milestone:

- Memory service **v0.5.0 — realism convergence.** The acceptance criterion above, walked to the top
  span with every identified realism element accounted for.

**A further line, not semver: the store's on-disk format revision.** The **format revision** is the
store's on-disk layout number. `<store-dir>/version.toml` (§2.1) carries it as one integer,
`format = N`, recording the layout the store was written in; this binary writes one revision and
may find another in a store. It advances independently of the semver line.

The **format check** runs at the process boundary, **before** startup recovery (§4.5.8): recovery
resets the store working tree to a recovery point and must never run against a layout this binary
cannot interpret. The open sequence is format check → startup recovery → service-run load.

- **Equal** → proceed.
- **Newer on disk → refuse.** `--allow-newer-home` overrides the refusal for one invocation; a
  waved-through open emits a stderr warning and a `system.home-format-override` event (§2.8).
- **Older on disk → migrate forward** when this binary can migrate from
  that format revision to the current one: the store is brought to the current format revision and
  its `version.toml` rewritten, and the open emits a `system.home-format-migrated` event (§2.8)
  naming both revisions. An older revision this binary cannot migrate from is refused. `personant init`
  runs the format check and writes `version.toml` at the current format revision only when the store has none; it never rewrites one, so it leaves an older store's revision for
  the next open to migrate.
- **No revision → treated as revision 1.** A store predating versioning is taken
  to be format revision 1 — always 1, even after the current revision advances — and then takes the
  older-on-disk path, so it is migrated and its revision written at the current one.

`personant version` is **exempt from the format check**: it reports the binary's identity, the
resolved store directory, and the store's format revision (a concrete number, *absent*, or
*unreadable*) and exits 0 either way. It never runs the format check or startup recovery and never
fails on a store it cannot read.

### 9.2 Mock LLM client

Tests exercise the full memory-service path except the LLM round-trip, using a mock with two modes:

- **Scripted**: a preloaded queue of canned responses; each call returns the next. Deterministic,
  replay-friendly.
- **Generated**: on demand from a seeded RNG; no two generated responses are identical. The caller specifies topic-tag content, tag symbols
  emitted, and response length. Format-correct per §5.1.

The same mock serves every test level (scenario, churn, calibration, the acceptance sim; §9.4).

**Open:** whether code written to probe the opencode route stays in the product: the mock served as
an OpenAI-compatible endpoint, which a run with opencode in the loop requires.

### 9.3 Unit testing

Unit testing is level 1 of the measurement stack (ARCHITECTURE, "Testing as the measurement instrument"); no
mock LLM is needed at this level.

### 9.4 Measurement regime

The scenario, churn, calibration and acceptance-simulation levels run against the
mock LLM client (§9.2) with logical-clock acceleration, so the full simulation completes in minutes.
Every level emits a machine-readable metrics blob with a stable JSON schema for cross-version
comparison.

**Acceptance metrics** (normative; must hold through the four-month simulation):

- **Recall fidelity:** precision/recall/F1 per scenario step against ground-truth expected matches.
  Strict mode fails the test on mismatch; measure-only mode records adversarial probes (injected recall queries built to be hard to match)
  without failing. Regressions are tracked against stored per-scenario baselines.
- **Engagement accuracy:** tagged-engaged threads match authoritative-by-construction ground truth.
- **Size bounded / zero overflow:** the token-ceiling criterion of §9.1, backed on mock run
  configurations by a per-turn check that each rendered slot stayed within its byte allocation; the
  byte check is skipped on live-inference run configurations.
- **Steady-state trajectory:** working-set size, thread registry cardinality, and per-operation
  latency (P50/P95/P99) flat after 120 simulated days.
- **Round-trip fidelity:** archive → restore → diff against original; information-preservation rate
  measured.
- **Operation latency within bounds:** engagement update, recall match, thread fetch, thread close,
  archival, recovery, index rebuild, index check — stable as accumulated state grows.
- **Oracle agreement with the memory service's active set:** the workload generator's own model of
  active-set membership, from which it derives expected recall matches, agrees exactly with the
  memory service's active set after every turn, in both directions. Zero divergence is an acceptance criterion on mock run
  configurations; under live inference, where the real model engages off-plan, it is reported only.
- **Intra-thread descent divergence**: how often the approximate
  summary-tree descent (§3.4) substitutes a different leaf than the exhaustive scan. A reported
  quality measure, **not** an acceptance criterion.

**Invariant validators** run after every operation in churn sequences and at key checkpoints in
scenario tests. They cover thread registry integrity, index freshness, project references,
last-active validity, thread-metadata/thread-registry agreement, engagement consistency, thread
accounting, closed-thread consistency, archive resolvability, dedup consistency, and per-slot
budget.

**Named scenario set** (initial): single-thread lifecycle, multi-thread interleaving, project
switching, heavy closing (50 threads), same-anchor collision, transient shell-capture content,
cross-boundary recovery (crash recovery across a day rollover). Churn sequences are randomized but seeded; a failure is replayable from its seed. Calibration sweeps a
directive parameter across a range and emits a metrics matrix — the instrument by which §2.6.1
defaults earn their values.

**Acceptance simulation harness**: a deterministic seeded workload generator with logical-clock
acceleration, steady-state assertions, and per-operation cost profiling, run at a chosen span in
calendar days (ladder: §9.1). Output: metrics JSON plus a human-readable summary; pass/fail per
§9.1.

Recall-fidelity scoring reads the event log (§2.8), not memory-service state, so a renamed event
fails the measurement rather than silently zeroing a series. Two reads are declared
exceptions: the active-set oracle-agreement check above, which compares the generator's model with
the memory service's active set, and the set of archived threads, read from the archive index (§3.8.1) so that an archived expected match is excluded rather than counted a miss (§3.8.4).

#### 9.4.1 Workload cadence model

The workload's simulated clock is a **single UTC time value**, anchored at **2026-05-04 00:00 UTC**
(a Monday at midnight). Every other time-shaped value — current date, day index, day-off,
termination — is derived from it, never tracked in parallel.

- **Day grid.** Days are midnight-to-midnight buckets. Day *N*'s work starts at anchor + N·24h + 8h
  ± 15min (08:00 ± 15min, jitter drawn deterministically from the seed). At end of day the clock
  jumps to day *N+1*'s start; it never carries the accumulated day forward, so the work pattern
  never precesses. A run spans `Duration / 24h` days (a 240h run = 10 days); the day index
  ticks once per midnight and drives the day rollover and the heavy invariant cadence.
- **Two 6h work blocks + ~1h break.** A work day emits two **work blocks** (simulated working
  sessions) of ~6h turn-active time each, separated by a fuzzed ~1h break (~08:00 → ~21:30). Within
  a work block the clock advances per turn by a fuzzed span centered at ~72 s (turn plus pause); a
  work block emits turns until its cumulative span crosses 6h. A work day is therefore ~600 natural turns; injected engagements and probes raise the
  observed count.
- **Emergent overnight.** The ~11h overnight idle arises from the next day's re-anchor, never from
  an additive constant.
- **Weekly day-off from the calendar.** A day is a day-off iff its un-jittered grid date is a Sunday
  (day index 6, 13, …); there is no modular day counter. A day-off emits no turns, only a
  consolidation marker step that drives consolidation. Its idle span (~34.5 h) stays under the §3.5 wall-clock decay threshold (`engagement.decay-time`, 7d), so decay in the simulation is turn-driven.
- **Each daily record is dated the day it represents.** The daily record (the run's per-day entry
  in its metrics output) for day *d*, written at the day's close, is stamped with
  day *d*'s own grid date (anchor + d·24h), so the 0-turn Sunday record lands on the Sunday.
- **Day-off keeps phase.** The day-off occupies its own day-index slot and re-anchors like a work
  day that emits no turns; work resumes at the same time of day (±15min) as before it, with no
  systematic phase offset.
- **Determinism.** `(Seed, Duration)` → output is byte-exact: the day count
  is a function of `Duration` alone. Same seed and duration → identical step stream.
- **Clock frozen within a turn.** The simulated clock advances once per step; every read inside one
  turn returns the same instant. This is benign because the logical clock is the turn number:
  closing/decay, staging, file-chain aging and history ordering key off turns, and the simulated
  clock is consumed only at coarse grain (§3.5 wall-clock decay, provenance/display timestamps).
  Revisit if sub-turn logic orders events by timestamp or metrics become timestamp-grained.
