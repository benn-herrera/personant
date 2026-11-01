# OPENCODE ROUTE OPEN ISSUES – Personant

The still-open issues of the opencode plugin route. Each is tracked in `ROADMAP.md` (U-numbers are its
"Pending decisions"; A and B numbers are its sequence items); the reference follows the item. Detail lives in
the contract documents under `PROJECT_DOCS/` and the reports in `OPENCODE_RESEARCH/`.

---

## Open

- **Topic tag:** keep the in-band tag or replace it with a recall tool (ROADMAP U3, A10).
- **Missing lineage under opencode:** no thread carries `derived_from` lineage through opencode, while in-process
  runs do. Cause unverified (ROADMAP A10; `OPENCODE_RESEARCH/OPENCODE_PROBE_RESULTS.md` §11).
- **Plugin failure policy:** fail open or fail closed (ROADMAP U7, A9).
- **Restart persistence:** whether in-session history and the turn counter survive a restart (ROADMAP U9, A8).
- **Resume cases not exercised** (ROADMAP B9; `OPENCODE_RESEARCH/OPENCODE_RESUME_PROBE.md` §6).
- **Memory vetting details:** default acceptance policy, rubric content, reviewing model, re-review cadence
  (ROADMAP B10, U5; SPEC §1.8).
- **Memory-native commands:** which survive, which become autonomic, which are dropped (ROADMAP U4, A12).
- **Ack channel:** how the recall offer, closure review queue and startup project prompts ask the user
  (ROADMAP U4, A12; `OPENCODE_RESEARCH/OPENCODE_INTEGRATION_OPTIONS.md` §2.3).
- **Fork, revert and sub-minds:** how a branch's work is incorporated or discarded (ROADMAP B6).
- **Agents and skills:** whether and how opencode agent definitions and skills are supported. Candidate mapping:
  agent definitions as Layer E directives scoped per agent; skill content as transient task-class (ROADMAP B7).
- **`/compact` and `/clear`:** what they eventually map to; whether a TUI plugin can replace or disable them
  (ROADMAP B8; `OPENCODE_RESEARCH/OPENCODE_CONTEXT_INJECTIONS.md` §6).
- **Concurrent sessions:** two opencode sessions against one memory (ROADMAP B5).
- **Per-slot answers**, including the tool-definition slot (ROADMAP A7).
- **MCP prompt expansions** cannot be told apart from typed prompts; **injection unknowns** — hook order after a
  plugin reload, system-role lowering on routes other than openai-chat (ROADMAP B4).
- **Per-turn cost growth past sim-day 9** after D1 is fixed (ROADMAP A11; `DEFECTS.md` D1).
- **Contradictions surfaced by the SPEC rewrite:**
  - Reconcile skips turns rolled back by retraction, but no record distinguishes a retraction rollback from a
    crash rollback (ROADMAP A8, U8).
  - SPEC §4.3.3's rollback restores run-volatile state while restart persistence of that state is open (ROADMAP U9).
  - `session.started active=` is logged per service run, but the active project is per opencode session
    (ROADMAP B5).
  - The stable-prefix claim for fixed slots no longer covers the opencode-expectations slot, now dynamic
    (ROADMAP A7).
  - SPEC §3.0.1 requires `edit` to yield post-edit file bytes as an `fs.write` delta; no probe established that
    opencode's `edit` exposes them (ROADMAP A5).
- **Gaps surfaced by the ARCHITECTURE rewrite:**
  - Production source of tool-call order: the probe took it from ids its mock assigned; real provider ids carry no
    index (ROADMAP A5).
  - The probe service maps only `read`, `write` and a shell `git commit`; `edit` and generic `tool.result` are
    unmapped, and `read`'s formatted output was stored where SPEC §3.9.1 requires file bytes (ROADMAP A5).
  - SPEC §4.1 keeps `search`, `deps` and `log` CLI verbs that `cmd/` does not implement (ROADMAP A12).
  - `internal/store/paths.go` still places `config.toml` and `providers.toml` under the home, against SPEC §2.1
    (ROADMAP A12).
  - "The LLM never touches the home's git tree" cannot be enforced under opencode, whose shell reaches it
    (ROADMAP B12).
- **Probe code worth keeping on the main line:** the sim baseline comparator (`sim-baseline`, `sim-fidelity`,
  committed baselines) and `model.MockServer` (ROADMAP A2).
- **CONVENTIONS.md rule proposed, not adopted:** never place external state inside `~/.personant/` or a test home
  (ROADMAP U11).
