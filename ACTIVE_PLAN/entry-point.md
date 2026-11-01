# ACTIVE_PLAN – Excerpt folios: two-tier turn-excerpt storage

Fixes the tracked-file growth behind DEFECTS.md D1.

**Every dispatch carries this plan's "Design" section verbatim**, plus its own work package. Packages cite Design subsections by title ("Folio format", "Settle step
and crash safety", …) instead of restating them.

Pre-task reading for every package (the contract documents live under `PROJECT_DOCS/`; the root
`*.md` files are pointers only):
- THESIS: `PROJECT_DOCS/entry-point.md`.
- CONVENTIONS: `PROJECT_DOCS/CONVENTIONS/repo-state/project-documents.md`,
  `PROJECT_DOCS/CONVENTIONS/build-test/two-gates-do-not-conflate-them.md`,
  `PROJECT_DOCS/CONVENTIONS/build-test/docs-stay-current.md`,
  `PROJECT_DOCS/CONVENTIONS/house-rules-for-agents/serialized-data-is-debuggable.md`.
- ARCHITECTURE: `PROJECT_DOCS/ARCHITECTURE/module-map.md`,
  `PROJECT_DOCS/ARCHITECTURE/substrate-non-negotiables/port-abstraction-policy.md`,
  `PROJECT_DOCS/ARCHITECTURE/storage-and-record-formats/home-layout.md`,
  `PROJECT_DOCS/ARCHITECTURE/storage-and-record-formats/thread-directory-threads-thr-id.md`,
  `PROJECT_DOCS/ARCHITECTURE/mechanisms/crash-stability-and-the-dual-repo-substrate.md`.
- SPEC: §2.3 `PROJECT_DOCS/SPEC/2-data-model/2.3-thread-file-format-threads-thr-id.md`; §4.5.8
  `PROJECT_DOCS/SPEC/4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md`.
- DEFECTS.md D1 (repo root).
- Each package names any further leaves it reads or edits. Edit the leaf that holds a section; a new
  section is a new leaf, added to the `Contents:` list of its parent `index.md` and of that
  document's `entry-point.md`. No package in this plan needs a new section.

Contents:

- [Status](status.md)
- [Design](design/index.md)
  - [Outcome](design/outcome.md)
  - [Terms](design/terms.md)
  - [Folio format](design/folio-format.md)
  - [Invariants](design/invariants.md)
  - [Placement rule: what "cold" means](design/placement-rule-what-cold-means.md)
  - [Read path, per reader](design/read-path-per-reader.md)
  - [Settle step (barrier B1b) and crash safety](design/settle-step-barrier-b1b-and-crash-safety.md)
  - [Effects](design/effects.md)
  - [Module and symbol placement](design/module-and-symbol-placement.md)
- [Work packages](work-packages/index.md)
  - [WP0 — Instrumentation and the "before" run (mechanical)](work-packages/wp0-instrumentation-and-the-before-run-mechanical.md)
  - [WP1 — Store: folio format, tier-transparent reads, settle (expert)](work-packages/wp1-store-folio-format-tier-transparent-reads-settle-expert.md)
  - [WP2 — Home format 2 and the migration registry (mechanical)](work-packages/wp2-home-format-2-and-the-migration-registry-mechanical.md)
  - [WP3 — `personant verify` folio checks (mechanical)](work-packages/wp3-personant-verify-folio-checks-mechanical.md)
  - [WP4 — Barrier step B1b, crash matrix, contract documents (expert)](work-packages/wp4-barrier-step-b1b-crash-matrix-contract-documents-expert.md)
  - [WP5 — Measurement, DEFECTS.md D1, ROADMAP.md A11 text (expert)](work-packages/wp5-measurement-defects-md-d1-roadmap-md-a11-text-expert.md)
- [Post-sprint review](post-sprint-review.md)
