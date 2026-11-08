### 3.1 Working-set composition

The **working set** — the memory content of the composed request — is rendered into the system
prompt as five memory slots, each truncated to a byte
budget derived from `context.token-budget` and `layer.budget.percentages` ([§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)). The token
ceiling is the authoritative whole-request check; each slot's byte budget is derived from it
conservatively, so the memory slots stay within their share of the ceiling. The live-turn reserve
(`live_turn`, 15%) is carved from the byte total first; the five memory slots partition the
remainder (directives 12, registry and other-projects together 18, active-threads 55, dormant-threads 15; the 18 is split between the registry (fixed, 10) and other-projects (the residue)). The whole request — system prompt + replayed
transcript tail + current user input + this turn's tool results + tool definitions — is bounded
against the token ceiling. Static prompt scaffolding (the orientation
preamble that opens the system prompt, the [§5.1.3](../../5-model-interaction/5.1-topic-tag-emission-format/5.1.3-prompt-template.md) topic-tag directive, the [§3.3](../3.3-symbol-extraction-two-passes/index.md) fallback re-prompt reminders, section headers) is charged to no slot.

**Slot order and contents**:

| Slot | Content |
|---|---|
| directives | the defaults, user-wide and active-project directive texts plus the project's convention files, frontmatter removed |
| registry | a display line for each thread of the active project |
| other-projects | one line per other project (never `prj_default`) with its name, id, summary and recent anchors, most recently active first, each within `cross-project.digest-per-project-bytes` |
| active-threads | up to `layer.b-top-k` threads, most recently engaged first, each with its metadata and recent excerpts; each thread's share is the slot's share ÷ `layer.b-top-k`, and an oversized thread is truncated on its own |
| dormant-threads | a display line for each thread of the dormant set |

The slot percentages are calibration starting points, earned by the simulation's parameter sweep ([§9.4](../../9-measurement-and-validation-regime/9.4-measurement-regime/index.md)).

Contents:

- [§3.1 Slots for opencode content](slots-for-opencode-content.md)
- [§3.1 Slot policies](slot-policies.md)
- [§3.1 Tool output in composed requests](tool-output-in-composed-requests.md)
- [§3.1 Truncation policy](truncation-policy.md)
- [§3.1 §5.5 mid-turn fetch](5-5-mid-turn-fetch.md)
