## 2. Sequence and status

| # | Package | Rating | Depends on | Checkpoint | State |
|---|---|---|---|---|---|
| P1 | `dedup` primitives | expert | — | — | done |
| P2 | Tracked-file store format, excerpt reader, verify check | expert | P1 | **C1** after P2 | done; C1 passed |
| P3 | Turn-close write switch | expert | C1 | — | done |
| P4 | Composition switch (evidence step) | expert | P3 | — | done; evidence `dedup-expansions: literal=1697 diff=2355` (1d sim) |
| P5 | Barrier step B1c, format 2 → 3 | expert | P4 evidence | — | done |
| P6 | Remove the old mechanism | mechanical | P4 evidence, P5 | **C2** after P6 | done; C2 passed |
| P7 | Measurement (A11a) | expert | C2 | **C3** after P7 | done; C3 passed |
| — | Post-sprint review ([§6](6-post-sprint-review.md)) | — | C3 | — | done (R1–R6) |

P3 → P6 form one checkpoint (C2). The contract text P3–P6 land is consistent only as a whole: for
example, the new SPEC §3.9.1 (P5) states the day-barrier retention that B1c implements and that the
removal of aging (P6) makes true. So no commit lands between C1 and C2.

**Every dispatch reads first:** `THESIS.md`, `SPEC.md`, `ARCHITECTURE.md`, `CONVENTIONS.md` (all via
`PROJECT_DOCS/`), this plan's [§1](1-outcomes-this-plan-carries.md) and its own package section, and the design sections the package
names.

**Checkpoints are run by the dispatching session**, in the background, after the package's coder
reports its done-line: `just test` outlasts a coder's foreground shell limit. A coder never runs
`just test`.

**Test naming.** Every new test's name starts with one of the prefixes in its package's done-line
regexp, so the done-line runs it.

---

