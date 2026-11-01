## Storage and record formats

SPEC §2 states the outcomes: memory lives under one home, is plain text and is versioned in git.
This section holds the layout and record formats this implementation uses. `personant verify`
enforces the constraints marked below; `personant index rebuild` regenerates derived files.

Contents:

- [Home layout](home-layout.md)
- [Spine record (`spine.jsonl`)](spine-record-spine-jsonl/index.md)
- [Thread directory (`threads/thr_<id>/`)](thread-directory-threads-thr-id.md)
- [Symbol index (`symbols.jsonl`)](symbol-index-symbols-jsonl.md)
- [Project metadata](project-metadata.md)
- [Symbol normalization](symbol-normalization.md)
- [Symbol source](symbol-source.md)
- [Symbol lifecycle and anchor projection](symbol-lifecycle-and-anchor-projection/index.md)
