### Serialized data is debuggable

A human or agent chasing a problem
reads the file raw. JSON is the format to resist — quote noise, no comments, nesting tracked by
brace-counting. A new file, record stream or command output takes:

| Shape | Format |
|---|---|
| flat records, one per line, sorted or append-only | JSONL — the line is the record: `grep` returns whole records, `git diff` isolates the changed one, id order stays stable |
| nested, or read as a document (command output, configuration, frontmatter) | YAML when tree-shaped (deep nesting, nulls, top-level lists); TOML when flat, hand-edited configuration |

Machine-emitted YAML decodes into typed structs and quotes string values on emit. A format
outside this mapping states its reason in the change that introduces it.
