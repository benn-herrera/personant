# ACTIVE DESIGN — Working-set content dedup (SPEC §3.9) in Personant's turn loop

Scope: the memory engine's own turn loop (`internal/turn` → `MemoryOps` → `fileadapter` →
`store` / `workset` / `dedup`). This file is the design a plan is cut from. It is not a contract
document; [§9](9-proposed-spec-text-replaces-the-leaves-under-project-docs-spec-3-core-algorithms-3-9-working-set-content-dedup.md) and [§10](10-architecture-changes-same-change.md) below are proposed contract text, to be landed by the plan, not edited in place
by this design.

**SPEC §3.9 covers the memory-composed layers only** — the working set Personant composes in its
own turn loop (SPEC §3.1). opencode's stored transcript is never rewritten and Personant never
edits its tool-result rows: history rows are dropped from the request once memory has captured them
(ARCHITECTURE "Injection handling", drop on re-injection), so repeated file copies there never reach
the model and need no dedup of their own. Growth of opencode's own storage is ROADMAP B3's cleanup,
not part of this work.

---

Contents:

- [§1 Established facts](1-established-facts/index.md)
  - [§1 E1. Where file content gets into the model's context](1-established-facts/e1-where-file-content-gets-into-the-model-s-context.md)
  - [§1 E2. What identifiers can attach to](1-established-facts/e2-what-identifiers-can-attach-to.md)
  - [§1 E3. What persistent storage the behavior needs](1-established-facts/e3-what-persistent-storage-the-behavior-needs.md)
- [§2 Design in one paragraph](2-design-in-one-paragraph.md)
- [§3 Invariants](3-invariants.md)
- [§4 Module skeleton](4-module-skeleton.md)
- [§5 Store format and retention](5-store-format-and-retention/index.md)
  - [§5.1 Layout](5-store-format-and-retention/5.1-layout.md)
  - [§5.2 Write at turn close (`ApplyFileEvents`)](5-store-format-and-retention/5.2-write-at-turn-close-applyfileevents.md)
  - [§5.3 Retention (B1c, §8)](5-store-format-and-retention/5.3-retention-b1c-8.md)
- [§6 Composition](6-composition.md)
- [§7 Fate of current mechanisms](7-fate-of-current-mechanisms.md)
- [§8 Barrier step B1c and crash safety](8-barrier-step-b1c-and-crash-safety/index.md)
  - [§8 Migration (format 2 → 3)](8-barrier-step-b1c-and-crash-safety/migration-format-2-3.md)
- [§9 Proposed SPEC text (replaces the leaves under `PROJECT_DOCS/SPEC/3-core-algorithms/3.9-working-set-content-dedup/`)](9-proposed-spec-text-replaces-the-leaves-under-project-docs-spec-3-core-algorithms-3-9-working-set-content-dedup.md)
- [§10 ARCHITECTURE changes (same change)](10-architecture-changes-same-change.md)
- [§11 Sequencing constraint for the plan](11-sequencing-constraint-for-the-plan.md)
- [§12 Acceptance criteria](12-acceptance-criteria.md)
- [§13 Measurement (the A11a method)](13-measurement-the-a11a-method.md)
- [§14 Deliberately unspecified (coder's choice)](14-deliberately-unspecified-coder-s-choice.md)
