#### Truncation policy

each layer's rendered string is truncated to its byte budget at a UTF-8 rune
boundary, with a marker `... [Layer X truncated; budget=N bytes] ...` appended. A layer that fails
to render (e.g. a project's `digest.json` is missing) emits a `workset.warning` log line and renders
empty; neighbour layers proceed.

