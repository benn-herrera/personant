Everything below comes from the three documents you pasted. I opened no files. Quotes are taken from your text.

## 1. What the system is made of

The system has two parts that run alongside unmodified opencode:
- a **TypeScript plugin** that holds no memory state, and
- a **Go memory service**, which is the only writer of memory.

The plugin forwards opencode's hooks and events to the service over loopback HTTP + JSON and applies what the service answers. Through the `session.context` hook, the service builds every model request from memory. The request is a set of budgeted slots: directives, the thread registry, other projects, active threads and dormant threads, plus slots for content opencode supplies. Every piece of incoming content passes through one per-delta pipeline: symbol extraction → engagement → dedup → budget check → logging.

Memory is plain text in a store directory: `spine.jsonl` (the thread registry), one directory per thread with excerpts and tracked files, project metadata, directive files and a daily event log. Two git repositories track it: a turn repository committed every turn and deleted daily, and a permanent history repository committed once a day. On top of that sit derived indexes and a crash-recovery state machine that needs only what is on disk.

Recall combines symbolic Jaccard scoring with an embedding index that is built in the background. Model judgments, such as closing summaries, are requested through opencode. A simulation harness with a mock LLM is the acceptance instrument. ARCHITECTURE says the production plugin and service are not built yet; only a prototype on branch `probe-path` exists.

## 2. Contradictions

"(ack.)" means ARCHITECTURE itself flags the conflict.

**Archival outside the day rollover**
- SPEC §3.8 "Runs only at day rollover": "Archival runs only at the day rollover, so the history repository is written only then, apart from crash recovery."
- ARCH "Day rollover → Archival crash completion": "Archival fires only at the rollover's archival step". The same paragraph then says: "A standalone batch runs under its own `op=archival` marker, outside any rollover". "Closing a thread" adds: "a standalone `ArchiveThreads` is crash-recoverable through the same roll-forward." This conflicts with SPEC and with ARCH's own sentence.

**Structural changes don't trigger a full-tree checkpoint**
- SPEC §3.11: "A Personant checkpoint is a commit made at a clean service-run stop or after a mid-run structural change (creating or closing a thread, creating a project). Every checkpoint commits the whole store working tree, hand-edits included".
- ARCH "Turn transaction and journal": "Structural changes (creating or closing a thread, creating a project) ride the per-turn commit … A hand-edit is therefore absorbed at the next full staging pass: a clean-stop checkpoint, or the archival and day-commit steps". The per-turn commit is described as scoped ("through `autogit.AddPaths`"), not a full-tree commit.

**Tracked-file store in the history repository (ack.)**
- SPEC §3.9.1: "the history repository never holds it."
- ARCH "Day rollover" step 5: "commit the store working tree to the history repository … staging everything". Archival: "The capture commit is `Add(".")` of the full store working tree into the history repository". "Mechanism names": "restore both [`files.jsonl` and `files/`] from history repository HEAD". ARCH marks this "Out of date — SPEC §3.9.1 governs".
- That label also breaks ARCH's own "Tense" rule: "present tense states the production design."

**Closing summaries and anchors are not checked before they are written**
- SPEC §2.2: the summary is "bounded by `spine.entry-max-chars`".
- THESIS, row "Model judgments": text "that deterministic code validates before writing".
- ARCH "The model drafts, deterministic code writes": "Length is not checked before the write: `spine.entry-max-chars` is enforced only by `personant verify` at the history-repository write". The same table says "Anchor length is not checked", while the verify table requires "`anchors[i]` … 3–50 chars".
- My inference: an over-length value would only fail at the day commit.

**Recency signals reset across a relaunch**
- SPEC §3.2: the owner falls back to "the most recently engaged **valid** referenced thread".
- ARCH "Active-set and dormant-set order": that fallback "ranks tagged ids by `last_engaged_turn` … the counter inverts recency across a relaunch."
- Same root cause for decay. SPEC §3.5: decay fires when `last_engaged` exceeds `decay-turns` OR `decay-time`. ARCH "Closing a thread": "Across a relaunch, only the wall-clock threshold is a durable decay timer." (SPEC U9 is open but leaves both rules standing.)

**The "curator" symbol source**
- SPEC §2.7.3: each symbol records how it was emitted, "(deterministic extraction, model or user)".
- SPEC §3.3: the summarizer "contributes no symbols".
- ARCH "Symbol source": "a thread's anchor counts as `curator`-sourced … not summarizer output". `curator` also ranks highest in the dominance tie-break.

**`.git/` filed as operational**
- SPEC §2.1: operational files are "runtime state … neither authoritative nor derived".
- SPEC §4.5.8 (5): the history repository is "irreplaceable", and an archived thread's bytes are "in the history repository only".
- ARCH storage table: "Operational | `.git/`, `.git-daily/` … | System-managed, not drift-checked".

**`.recall-cache/` filed as operational (ack.)**
- SPEC §3.4 *Persistence*: "The vectors are derived and regenerable … (§2.1 `.recall-cache/`)".
- ARCH: "filed operational … cannot be kept fresh at every history-repository write as derived files are."

**Config location and `providers.toml` (ack.)**
- SPEC §2.1 puts settings under `~/.config/personant/`.
- SPEC §8.2.1: "Personant holds no endpoint, provider configuration".
- ARCH "One inspection location": "`internal/store/paths.go` still places `config.toml` and `providers.toml` under the store directory".

**The memory service calls a model directly**
- THESIS, row "Model access", and SPEC §8.2.1: "the memory service never calls a model".
- ARCH "Model access…": "Only the in-process turn emits `model.inference-telemetry` today … from its own provider response". "Module map": `internal/model` has an "OpenAI-compatible `HTTPClient`". `runwithinfo` is the default executor.
- ARCH never limits the in-process turn to tests.

**Uncomposed requests under fail-open**
- ARCH "Integration shape": "no model request reaches the provider uncomposed (SPEC §1)".
- ARCH "Failure handling": the prototype "fails open: … leaves the hook input as opencode built it". SPEC §1 U7 is still open.

**Stripping the topic tag**
- ARCH "Hook-driven turn phases": `IngestModelResponse` (on `session.execution.succeeded`) does "tag strip (remove the topic tag from the stored and shown response)".
- ARCH "Topic-tag mechanics": "Open: U3 … including stripping it from the stored transcript".
- SPEC §5.1.1: "under opencode this has no mechanism".

**Lifecycle git tags skipped on gap days**
- SPEC §4.5.8 (5): "a tag for each thread created, closed or archived and each project created".
- ARCH "Lifecycle git tags": "life events on earlier idle gap-days … go untagged."

**Untracked debris removed without quarantine**
- SPEC §4.5.8 (4): torn-turn debris removal must "quarantine byte-exact … before any removal".
- ARCH case 4: it quarantines only "dirty tracked bytes", then will "remove untracked debris".
- ARCH "Start order": "Crash recovery quarantines untracked files". This conflicts with case 4.

**Anchors: stored or not**
- ARCH "Anchors and symbol history": "`anchors` is **not** a separately stored set".
- ARCH "Thread registry record": anchors are "stored in the authoritative record … `personant index rebuild` does not regenerate them, and no drift check covers them".

**Excerpt bundles (ack.)**
- SPEC §2.1: "plain text … inspectable with ordinary tools".
- ARCH bundle format: "an excerpt cannot be read in place … accepted".
- Related: SPEC §2.3 says file count "does not grow with its count of excerpts", but ARCH says excerpts over 1 MiB "stays loose … permanently".

**`read` output stored formatted (ack., prototype only)**
- SPEC §3.9.1: "never a tool's formatted rendering".
- ARCH "Ordering and correlation": "the prototype stores that output as returned".

**Exception criterion (minor, inferred)**
- SPEC §3.5: exceptions are only "anchor-rich (≥ 6 anchors)".
- ARCH `retire.pending`: "`reason=anchors=N|turns=N`".

**Retraction versus the authoritative log (inferred)**
- SPEC §4.3.3: before the first authoritative write, an interrupt leaves memory "as if the turn never happened".
- ARCH classes `logs/*.log` as Authoritative, and says the `user.prompt` delta "has already run … when a retraction arrives". That delta's logging step writes to the log first.

**Two weaker tensions, which are judgment calls**
- THESIS says the memory service is not "a network server", while ARCH serves it over loopback HTTP.
- THESIS says "deterministic code writes everything that is kept", while ARCH admits the agent's shell "can reach [the store], and nothing blocks it".
