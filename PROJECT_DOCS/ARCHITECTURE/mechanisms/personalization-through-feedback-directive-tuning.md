### Personalization through feedback directive tuning

Standard pattern: THESIS "The building blocks", row "Directives".

Directives are inspectable files, and feedback signals tune them:

- `directives/defaults.md` — system defaults, baked in.
- `directives/user.md` — user-wide overrides. By default, feedback directive tuning proposes a
  change here only from patterns aggregated across projects (explicit instructions, decline
  categorizations and accept/decline statistics are inputs to the aggregate, not single-project
  writers; SPEC §1.8).
- `directives/prj_<n>/project.md` — project-specific directives.

Process-memory directive files do not exist yet (SPEC §1.8
`Open:`).

Every feedback-tuning proposal goes
through "Memory vetting", the same review and acceptance path as any other. (SPEC §1.8, §2.6.)

**Open:** how cross-project aggregation is computed (what counts as a pattern, how many projects,
what threshold); no aggregator exists, and the per-project statistics the recall loop collects
(`directive.accrual-update`, reserved) feed none of these files yet.

