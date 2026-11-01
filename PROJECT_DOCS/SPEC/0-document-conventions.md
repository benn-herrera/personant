## 0. Document conventions

- **JSONL schemas** use TypeScript-style `interface` notation; it is descriptive only.
- **Field markings:**
  - `field: T` — required.
  - `field?: T` — optional.
  - `[derived]` — regenerated from canonical sources; never hand-edited.
- **Pseudocode** is descriptive and language-neutral.
- **Cross-references** use §-numbered section identifiers (e.g. "see [§2.2](2-data-model/2.2-spine-record-schema-spine-jsonl/index.md)").
- **`Open:`** marks a clause whose content depends on a decision not yet made. Two forms. (1) An
  `Open:` line alone: the text around it states only what is decided. (2) An `Open:` line followed
  by text that specifies a current mechanism: that text binds the implementation until the decision
  lands. A tag such as U3 names a pending decision; ROADMAP.md lists and sequences them.
- **Unused section numbers** (§4.3.1, §4.3.2, §4.3.4, [§4.4](4-user-surface/4.4-shell-escape.md)–§4.4.4, [§4.6](4-user-surface/4.6-rendering.md), [§6](6-tool-surface-and-permissions.md)–§6.5, §7) are never
  reassigned; code cites sections by number. Where a heading remains, its text says what replaced
  it.
- **Moved sections.** A heading marked "Unused → ARCHITECTURE.md «X»" keeps its number and states
  only the outcome; the schema or format lives in ARCHITECTURE.md section X. A §-citation of that
  heading, in this document or in code, refers to X.

---

