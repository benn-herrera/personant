# Novelty report (package N1)

Reviewer: adjagent:prompt-engineer, on `DOC_SWEEP/` at `47d0067`. Each finding was checked against
the record before any went to the owner (`process-notes.md`); none needed a ruling. N2 applies
every finding marked **apply**, verbatim from the restatement given here.

## (a) Standard pieces framed as new

- **NV1 — apply.** ARCHITECTURE "The per-delta pipeline (§3.0)". Replace "Personant treats the
  working window as a sequence of **deltas** … the load-bearing primitive is the per-delta
  pipeline" with: "The memory service takes content in as **deltas** (SPEC §3.0: each is one
  content addition to the window); a turn groups deltas. Every delta runs the per-delta pipeline,
  a fixed sequence of steps, so no content entering the window skips a step (**no gaps**)."
- **NV2 — apply.** SPEC §9 intro. "The thesis — months-long continuity, … — is a behavioral
  claim…" becomes "Personant's behavioral claims — months-long continuity, drift-resistant memory,
  opportunistic recall, close-and-restore cycles that preserve meaning — must be proven by
  simulation **before** real use accumulates the state a structural defect would put at risk."
  The rest of the paragraph is unchanged.

## (b) Home sections that do not cite their THESIS row — apply all

N2 adds one line directly under each heading: `Standard pattern: THESIS "The building blocks",
row "<row>".`

| NV | ARCHITECTURE heading | Row |
|---|---|---|
| NV3 | Plugin route | The opencode plugin; and The memory service (no home section of its own) |
| NV4 | Storage non-negotiables | The memory store |
| NV5 | Crash recovery and the turn and history repositories | Durability and crash recovery |
| NV6 | Symbol extraction (three passes ordered cheapest-first) | Symbol extraction |
| NV7 | Recall paths (four, ordered by cost) | Recall |
| NV8 | The composed request and its slots | Context composition |
| NV9 | Thread lifecycle | Thread lifecycle |
| NV10 | Personalization through feedback directive tuning | Directives |
| NV11 | Memory vetting | Memory vetting |
| NV12 | Model access and inference telemetry | Model access |
| NV13 | Event log format | The event log |
| NV14 | The model drafts, deterministic code writes | Model judgments |

Six pieces have no home section: the memory service (covered by NV3), indexes, content dedup,
cold storage, the CLI and career memory. Career memory's section is A1's. The rest are A1
findings if A1 judges a home section is missing.

## (c) Passages that invite growth toward "It is not"

- **NV15 — apply.** ARCHITECTURE "Concurrent sessions": drop "Distinguished from multi-user
  (v2.0, locked):", keeping "Concurrent sessions interleave on *shared* authoritative state." The
  multi-user status lives in ROADMAP ("Multi-user").
- **NV16, NV17 — apply as future intent.** ARCHITECTURE "Storage evolution: if/when scale forces
  it" describes what may be built if scale forces it. That is future intent, which ARCHITECTURE
  does not hold (plan, package A1). The section leaves ARCHITECTURE: N2 drafts it as one ROADMAP
  Far-horizon line, "Derived index if scale forces it: bbolt first; SQLite only if queries become
  SQL-shaped (from ARCHITECTURE "Storage evolution")", which the dispatcher adds. The anti-pattern
  row "Use SQLite for the thread registry" then reads: "Text + git; a derived index only, if
  scale forces one (ROADMAP Far-horizon)." This settles the reviewer's NV16/NV17 owner question
  and the bbolt-vs-SQLite disagreement ("Seen" 3).
- **NV18 — apply (strip material).** ARCHITECTURE "Model access and inference telemetry": the U5
  Open with its provider-pool option (b) becomes the reviewer's restatement — the memory
  service's judgments go through the host (SPEC §8.2.1); text generation for the summarizer,
  clusterer and recall judgment through the plugin's `experimental.generate` over opencode's
  providers; the embedder names an opencode provider in `[embedding]` (SPEC §8.2.2); how
  embeddings are requested through opencode is Open A12a. Settled by ROADMAP U5 (checked off) and
  SPEC §8.2.1.
- **NV19 — apply (strip material).** ARCHITECTURE "One inspection location": "… moving them is
  ROADMAP.md work …" becomes "… ROADMAP.md work moves `config.toml` and removes `providers.toml`
  (U5)"; the U5 Open line on `providers.toml` and `api_keys/` is deleted. Settled as NV18, plus
  SPEC §2.1 (Personant reads no API key).
- **NV20 — apply (strip material).** ARCHITECTURE "Storage non-negotiables", row
  "Configuration": becomes "TOML (`config.toml` under `~/.config/personant/`) + markdown
  directives | hand-editable; no secret-bearing file (SPEC §2.1)".
- **NV21 — apply as future intent.** ARCHITECTURE "Plugin route" → "Boundary": delete "The
  alternative route, if this one cannot give the memory system the control it needs, is a full Go
  port of opencode's server (`ROADMAP_PLANS/OPENCODE_SERVER_REPLACEMENT.md`)." "Integration
  shape", Code Mode item: delete "`ROADMAP_PLANS/code-mode-support.md` applies only to the
  alternative route." ROADMAP "Parked plans" already lists both plans. (This lands A1's planned
  "Boundary" item early; A1 skips it.)
- **NV22 — apply as future intent.** SPEC §1, ARCHITECTURE "What this is" and "Event
  observation": "the sub-mind split and re-merge" is ROADMAP B6 ("Fork and revert with
  sub-minds"), not built. Remove it from the list of what Personant owns; leave the rest of each
  sentence. This also retires the "sub-mind split" undefined-term item (`spec-vocabulary.md`
  "Open for the owner").

## (d) Headings that name a trigger or internals

The reviewer found none of these titles cited by title anywhere in `DOC_SWEEP/*.md`.

- **NV23 — apply.** SPEC "3.0 Deltas (the architectural primitive)" → "3.0 All content enters
  through one pipeline (deltas)".
- **NV24 — apply.** SPEC "Missing-tag protocol fallback (violation → fallback, never abort)" → "A
  response without a topic tag never aborts the turn (missing-tag fallback)".
- **NV25 — apply.** SPEC "Lifecycle-aware symbolic scorer" → "Superseded symbols still match in
  recall (lifecycle-aware symbolic scorer)".
- **NV26–NV29 — not applied.** "Empty-response fallback (any turn)", "3.10.5 Window-close
  expiry", "§5.5 mid-turn fetch" and "5.5 Mid-turn thread fetch (system-injected)" already name
  what the section does; criterion (d) covers only a heading that names a trigger or internals
  *instead of* what it does. The reviewer rated these lowest.

## Also applied in N2 (references, README in scope)

- README's title citations of renamed ARCHITECTURE headings: "Single spelunking location" (L64)
  → "One inspection location"; "Home layout" (L160) → "Store directory layout"; "Crash stability
  and the dual-repo substrate" (L165) → "Crash recovery and the turn and history repositories";
  "Substrate non-negotiables" (L180) → "Storage non-negotiables".

## Held for later packages

- C: README L34 against ARCHITECTURE "Pinned release" (whether the opencode pin is stated);
  SPEC §3.0.4 carries two concerns under one heading.
- A: Module map `internal/model/` `HTTPClient` and the `reaper` tuning target against SPEC
  §8.2.1 (strip material); the tracked-file out-of-date marker (already ruled); "Memory
  consolidation"'s "fuller offline-reorganization vision" (future intent); "architectural
  thesis" undefined.
- S: SPEC §4.5.8's excerpt-storage step cites a "warm window (§2.3)" §2.3 no longer defines;
  §3.10.7 "Why this matters" is rationale only.
