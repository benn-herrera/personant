# CONVENTIONS – Personant

House rules for agents working in this repository. Purpose: THESIS.md. Outcomes: SPEC.md.
Mechanisms, the module map and design anti-patterns: ARCHITECTURE.md. `Open:` lines take the form
SPEC.md §0 defines.

Contents:

- [Before a change](before-a-change.md)
- [Plan & Execute Process](plan-execute-process/index.md)
  - [Post-sprint review](plan-execute-process/post-sprint-review.md)
  - [Probe work](plan-execute-process/probe-work.md)
  - [Plan work rating and dispatch](plan-execute-process/plan-work-rating-and-dispatch.md)
  - [Plan dispatchability](plan-execute-process/plan-dispatchability.md)
- [Language constraints](language-constraints/index.md)
  - [Memory service: Go](language-constraints/memory-service-go.md)
  - [Plugin: TypeScript, in `opencode-plugin/` and nowhere else](language-constraints/plugin-typescript-in-opencode-plugin-and-nowhere-else.md)
  - [Auxiliary scripts: Python, stdlib only](language-constraints/auxiliary-scripts-python-stdlib-only.md)
- [Repo state](repo-state/index.md)
  - [Anti-reaccretion doctrine](repo-state/anti-reaccretion-doctrine.md)
  - [Project documents live in `PROJECT_DOCS/`](repo-state/project-documents.md)
  - [Versioning & acceptance (read before judging "done")](repo-state/versioning-acceptance-read-before-judging-done.md)
- [Build / test](build-test/index.md)
  - [Two gates — do not conflate them](build-test/two-gates-do-not-conflate-them.md)
  - [Docs stay current](build-test/docs-stay-current.md)
- [House rules for agents](house-rules-for-agents/index.md)
  - [Substrate constraints are non-negotiable and live in ARCHITECTURE.md](house-rules-for-agents/substrate-constraints-are-non-negotiable-and-live-in-architecture-md.md)
  - [Dependencies](house-rules-for-agents/dependencies.md)
  - [All clock reads go through `internal/clock`](house-rules-for-agents/all-clock-reads-go-through-internal-clock.md)
  - [Match the data model](house-rules-for-agents/match-the-data-model.md)
  - [Serialization format: legible to whoever is debugging](house-rules-for-agents/serialization-format-legible-to-whoever-is-debugging.md)
  - [The opencode source clone (`../opencode`) is read-only](house-rules-for-agents/the-opencode-source-clone-opencode-is-read-only.md)
