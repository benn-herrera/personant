#### Truncation policy

each slot's rendered string is truncated to its byte budget at a UTF-8 rune
boundary, with a notice `... [Layer X truncated; budget=N bytes] ...` appended (X is the slot's
key in `layer.budget.percentages`, [§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)). A slot that fails
to render (e.g. a project's `digest.json` is missing) emits a `workset.warning` log line and renders
empty; neighbour slots proceed.

