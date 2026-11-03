## Project documents live in `PROJECT_DOCS/`

THESIS, SPEC, ARCHITECTURE and CONVENTIONS are trees of small files under `PROJECT_DOCS/`, each
section one file. The root `THESIS.md`, `SPEC.md`, `ARCHITECTURE.md` and `CONVENTIONS.md` only
point there.

- Edit the leaf that holds the section. Never edit a root pointer file, and never regenerate the
  trees from the root files: the leaves are the source.
- A new section is a new leaf, added to the `Contents:` list of its parent `index.md` and of the
  document's `entry-point.md`.
- Read the leaves a task needs, starting from the document's `entry-point.md`; a whole document
  is read only when the task is about the whole document.
- Citations keep their form: SPEC by `§N`, the others by section title. A leaf's file name is its
  section number or its title's slug.
