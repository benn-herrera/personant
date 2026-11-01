### Personalization through directive accrual

Behavior tuning lives in inspectable directive files that accrue from feedback signals:

- `directives/defaults.md` — system defaults, baked in.
- `directives/user.md` — user-wide overrides. Accrual may propose a change here only from patterns
  aggregated across projects (explicit instructions, decline categorizations and accept/decline
  statistics are inputs to the aggregate, not single-project writers).
- `directives/prj_<n>/*.md` — project-specific tuning. A pattern seen in one project stays
  here or in career memory; it never reaches `user.md` or the defaults.

**The user can read what the agent learned and correct it.** Every accrual proposal goes through
"Memory vetting", the same review and acceptance path as any other. (Spec §1.8, §2.6.)

**Open:** how cross-project aggregation is computed (what counts as a pattern, how many projects,
what threshold); no aggregator exists, and the per-project statistics the recall loop collects
(`directive.accrual-update`, reserved) feed none of these files yet.

