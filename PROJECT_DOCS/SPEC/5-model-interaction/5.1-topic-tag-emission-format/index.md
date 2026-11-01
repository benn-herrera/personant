### 5.1 Topic tag emission format

The model emits a topic tag at the **start of every response** identifying which threads the current
turn engages and which symbols attach to that engagement. The runtime parses the tag before any
other response processing.

**Open:** U3 — whether the in-band tag is kept (which needs a stream abort-and-re-issue, built per
provider protocol, and a way to strip the tag from the text opencode stores and shows) or replaced
by a recall tool (which changes [§5.1](index.md), [§5.5](../5.5-mid-turn-thread-fetch-system-injected/index.md) and the tag-fidelity measures of [§9.1](../../9-measurement-and-validation-regime/9.1-the-four-month-simulation-v0-1-acceptance-gate/index.md)). This section
([§5.1.1](5.1.1-format.md)–[§5.1.3](5.1.3-prompt-template.md)), [§5.5](../5.5-mid-turn-thread-fetch-system-injected/index.md) and the recovery re-prompts of [§3.3](../../3-core-algorithms/3.3-symbol-extraction-three-passes/index.md) specify the in-band tag and bind until
the decision lands.

Contents:

- [§5.1.1 Format](5.1.1-format.md)
- [§5.1.2 Parser](5.1.2-parser.md)
- [§5.1.3 Prompt template](5.1.3-prompt-template.md)
