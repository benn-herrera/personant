# CONVENTIONS – Personant

Project-specific process and house rules

Must-see:

- No user-facing behaviors or outcomes are designed here, that belongs to SPEC
- No project mechanisms or code structure are put forth, that belongs to ARCHITECTURE
- Always read [Two gates](build-test/two-gates-do-not-conflate-them.md) before choosing a gate check

Contents:

- [Before a change](before-a-change.md)
- [Plan & Execute Process](plan-execute-process/index.md)
  - [Sim harness gates](plan-execute-process/sim-harness-gates.md)
  - [Probe work](plan-execute-process/probe-work.md)
- [Language constraints](language-constraints/index.md)
  - [Memory service: Go](language-constraints/memory-service-go.md)
  - [TypeScript usage limited to plugin](language-constraints/typescript-usage-limited-to-plugin.md)
  - [Auxiliary scripts: Python, stdlib only](language-constraints/auxiliary-scripts-python-stdlib-only.md)
- [Repo state](repo-state/index.md)
  - [Anti-reaccretion doctrine](repo-state/anti-reaccretion-doctrine.md)
  - [Pre-production: no transition scaffolding](repo-state/pre-production-no-transition-scaffolding.md)
  - [Versioning and acceptance](repo-state/versioning-and-acceptance.md)
- [Build / test](build-test/index.md)
  - [Two gates — do not conflate them](build-test/two-gates-do-not-conflate-them.md)
  - [Docs stay current](build-test/docs-stay-current.md)
- [House rules for agents](house-rules-for-agents/index.md)
  - [Dependencies](house-rules-for-agents/dependencies.md)
  - [All clock reads use `internal/clock`](house-rules-for-agents/all-clock-reads-use-internal-clock.md)
  - [Match the data model](house-rules-for-agents/match-the-data-model.md)
  - [Serialized data is debuggable](house-rules-for-agents/serialized-data-is-debuggable.md)
  - [opencode clone: referent only](house-rules-for-agents/opencode-clone-referent-only.md)
- [Document conventions](document-conventions.md)
