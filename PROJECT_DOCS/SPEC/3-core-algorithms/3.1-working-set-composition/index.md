### 3.1 Working-set composition

The working set is rendered into the system prompt as five layers, each truncated to a byte budget
derived from `context.token-budget` and `layer.budget.percentages` ([§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)). The token ceiling is
the authoritative whole-request gate; the per-layer **byte** shares are derived from it (ceiling × a
conservative ~2.5 chars/token, text-only) and drive truncation. The live-turn reserve (`live_turn`,
15%) is carved from the byte total first; E/A1/A2/B/C partition the remainder (12/18/15/55, the 18%
high tier split between A1 fixed and A2 residue). The whole request — system prompt + replayed
history tail + current user input + this turn's tool results + tool definitions — is bounded
against the token ceiling: bytes pre-flight (this composition plus the live-turn sub-policy),
`usage.prompt_tokens` post-flight as the real-model gate. Static prompt scaffolding (orientation
preamble, [§5.1.3](../../5-model-interaction/5.1-topic-tag-emission-format/5.1.3-prompt-template.md) topic-tag directive, recovery reminders, section headers) is charged to no layer;
the conservative bytes/token ratio covers it.

**Layer order and contents** ([§2.1](../../2-data-model/2.1-storage-layout/index.md) maps these to on-disk sources):

| Layer | Source | Render |
|---|---|---|
| E    | `directives/defaults.md`, `directives/user.md`, `directives/<active>/project.md`, plus the project's `conventions_paths` ([§2.5.1](../../2-data-model/2.5-project-metadata-schema/2.5.1-canonical-projects-prj-n-meta-json.md)) | Section-divided concatenation; YAML frontmatter is stripped from each directive file. |
| A1   | `spine.jsonl` filtered to `project == active` | One [§2.2.2](../../2-data-model/2.2-spine-record-schema-spine-jsonl/2.2.2-display-form.md) display line per record. |
| A2   | `projects/<id>/digest.json` for every other project (excluding `prj_default`) | One line per project: `<name> (<id>): <one_line_summary> :: <recent_anchors[:5]>`, sorted by `last_active` desc. Each line capped at `cross-project.digest-per-project-bytes`. |
| B    | `threads/thr_<id>/` ([§2.3](../../2-data-model/2.3-thread-file-format-threads-thr-id.md)) for each id in the Layer B active set | Up to `layer.b-top-k` threads, most-recently-engaged first; each rendered as a heading + frontmatter line + body. Per-thread share = `LayerB / k`; oversized threads are individually truncated. |
| C    | `spine.jsonl` records for each id in the dormant set | One [§2.2.2](../../2-data-model/2.2-spine-record-schema-spine-jsonl/2.2.2-display-form.md) display line per record. |

The layer percentages are calibration starting points, earned by the simulation sweep ([§9.4](../../9-measurement-and-validation-regime/9.4-measurement-regime/index.md)).

Contents:

- [§3.1 Slots for opencode content](slots-for-opencode-content.md)
- [§3.1 Slot answers](slot-answers.md)
- [§3.1 Tool output in composed requests](tool-output-in-composed-requests.md)
- [§3.1 Truncation policy](truncation-policy.md)
- [§3.1 §5.5 mid-turn fetch](5-5-mid-turn-fetch.md)
