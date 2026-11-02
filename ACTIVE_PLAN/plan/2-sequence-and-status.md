## 2. Sequence and status

| # | Package | Rating | Depends on | Checkpoint | State |
|---|---|---|---|---|---|
| P1 | `dedup` primitives | expert | — | — | in progress |
| P2 | Tracked-file store format, excerpt reader, verify check | expert | P1 | **C1** after P2 | not started |
| P3 | Turn-close write switch | expert | C1 | — | not started |
| P4 | Composition switch (evidence step) | expert | P3 | — | not started |
| P5 | Barrier step B1c, format 2 → 3 | expert | P4 evidence | — | not started |
| P6 | Remove the old mechanism | mechanical | P4 evidence, P5 | **C2** after P6 | not started |
| P7 | Measurement (A11a) | expert | C2 | **C3** after P7 | not started |
| — | Post-sprint review ([§6](6-post-sprint-review.md)) | — | C3 | — | not started |

P3 → P6 form one checkpoint (C2). The contract text P3–P6 land is consistent only as a whole: for
example, the new SPEC §3.9.1 (P5) states the day-barrier retention that B1c implements and that the
removal of aging (P6) makes true. So no commit lands between C1 and C2.

**Every dispatch reads first:** `THESIS.md`, `SPEC.md`, `ARCHITECTURE.md`, `CONVENTIONS.md` (all via
`PROJECT_DOCS/`), this plan's [§1](1-outcomes-this-plan-carries.md) and its own package section, and the design sections the package
names.

**Test naming.** Every new test's name starts with one of the prefixes in its package's done-line
regexp, so the done-line runs it.

---

