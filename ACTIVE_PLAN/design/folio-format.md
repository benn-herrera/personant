### Folio format

JSONL. One line per excerpt, two keys in this order:
`{"name":"0000024.md","content":"<the file's bytes as a JSON string>"}`. Lines sort ascending by
(turn number, name), with no repeated name. Every line ends in `\n`. A thread with no bound
excerpts has no folio file. Folios are written and read through the existing generic helpers
`store.WriteJSONL` and `store.ReadJSONL` (`internal/store/jsonl.go:22, 63`). `WriteJSONL` already
sorts by a key, refuses duplicate keys and writes atomically. The sort key is the turn number
zero-padded to ten digits, a space, then the name, so the lexical sort is the (number, name) order.
Like every JSONL file in the home, folio lines keep `encoding/json`'s default HTML escaping (`<`
is written as the six-character escape backslash-u-003c). That is a legibility cost, accepted so
that a single JSONL writer stays in use. The encoder escapes every control character, so a line
never contains a raw newline. A line is malformed if it fails to decode, if `name` is missing or
not a name the excerpt listing accepts, or if `content` is absent (decoded through a `*string`).
Unknown keys are ignored, as in every other JSONL file in the home; `ReadJSONL` skips blank lines.
`ReadJSONL` caps a line at 1 MiB (`jsonl.go:33`: `scanner.Buffer(make([]byte, 0, 64*1024),
1024*1024)`). Bind therefore keeps loose any excerpt whose encoded line would exceed that cap
(`reason=too-large`). WP1 turns the literal into a named constant that both sides use.

