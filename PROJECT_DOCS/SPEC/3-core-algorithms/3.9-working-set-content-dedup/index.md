### 3.9 Working-set content dedup

When file content (or another large blob) appears in the working context multiple times — read,
modified, re-read — the runtime de-redundifies it. **Persistent storage and live context composition
are separate concerns** sharing the same primitives: content-addressed identifiers, diffs, anchor
literals.

Contents:

- [§3.9.1 Persistent storage (thread file body, log)](3.9.1-persistent-storage-thread-file-body-log.md)
- [§3.9.2 Live context composition (working window)](3.9.2-live-context-composition-working-window.md)
- [§3.9.3 Diff format](3.9.3-diff-format.md)
- [§3.9.4 Identifier-only references](3.9.4-identifier-only-references.md)
