## Document conventions

How the project documents (THESIS, SPEC, ARCHITECTURE, CONVENTIONS) are written and read.

- **JSONL schemas** use TypeScript-style `interface` notation; it is descriptive only.
- **Field markings:**
  - `field: T` — required.
  - `field?: T` — optional.
  - `[derived]` — regenerated from authoritative files; never hand-edited.
- **Pseudocode** is descriptive and language-neutral.
- **Cross-references** cite SPEC by §-numbered section identifier (e.g. "see SPEC §2.2") and the
  other documents by section title.
- **`Open:`** marks a clause whose content depends on a decision not yet made. Two forms. (1) An
  `Open:` line alone: the text around it states only what is decided. (2) An `Open:` line followed
  by text that specifies a current mechanism: that text binds the implementation until the decision
  lands. A tag such as U3 names a pending decision; ROADMAP.md lists and sequences them.
- **Unused SPEC section numbers** (§0, §4.3.1, §4.3.2, §4.3.4, §4.4–§4.4.4, §4.6, §6–§6.5, §7) are
  never reassigned; code cites sections by number. Where a heading remains, its text says what
  replaced it.
- **Moved sections.** A SPEC section marked "Format → ARCHITECTURE «X»" keeps its number and
  states only the outcome; the schema or format lives in ARCHITECTURE section X. A §-citation of
  that section, in SPEC or in code, refers to X.
