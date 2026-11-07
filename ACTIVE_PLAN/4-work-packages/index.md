## 4. Work packages

Shared rules:

- Every package touches Go, shares `just build` and runs alone in the shared tree, in the order
  of section 5.
- Coders never run `just test` and never commit. The dispatching session runs the checkpoint
  (`just test`), runs `just sim` where the done-line names it, and writes the commit message from
  the coder's report.
- Each package re-runs its evidence commands at dispatch, and lands its section-2 rows.
- Ratings follow the `adjagent:dev-process` skill's *Dispatch* section.
- A done-line grep must return nothing, or only the hits it names. Use `-w`, never `\b`.
- Quoted current text ("X" → "Y") is the file's text with its line breaks removed; match it as
  it wraps in the file, and wrap the replacement to the file's width.
- References inside a package resolve to leaves of this tree: "section 1" and its tables are
  [`1-inventory-code/`](../1-inventory-code/index.md), "section 2" and "contract rows marked N"
  are [`2-inventory-contract.md`](../2-inventory-contract.md), `E1`–`E16` are
  [`evidence.md`](../evidence.md), invariants are
  [`invariants-the-failure-modes-they-prevent.md`](../invariants-the-failure-modes-they-prevent.md),
  and "section 5" is [`5-sequencing.md`](../5-sequencing.md). A dispatch hands the coder its
  package leaf plus the leaves it cites.

Contents:

- [§4 WP1: Remove the REPL, terminal, shell and the `ping`/`models` verbs (mechanical)](wp1-remove-the-repl-terminal-shell-and-the-ping-models-verbs-mechanical.md)
- [§4 WP2: Retire the front-end version line (mechanical)](wp2-retire-the-front-end-version-line-mechanical.md)
- [§4 WP3: Remove the in-process tool loop and the tool surface (expert)](wp3-remove-the-in-process-tool-loop-and-the-tool-surface-expert.md)
- [§4 WP4: Drop the dead provider parameter (mechanical)](wp4-drop-the-dead-provider-parameter-mechanical.md)
- [§4 WP5: Remove the provider pool and home configuration (expert)](wp5-remove-the-provider-pool-and-home-configuration-expert.md)
- [§4 WP6: Closure without a resolver (expert)](wp6-closure-without-a-resolver-expert.md)
- [§4 WP7: Recall without a resolver (expert)](wp7-recall-without-a-resolver-expert.md)
- [§4 WP8: Presentation hooks, memory-native commands, directive parameters (mechanical)](wp8-presentation-hooks-memory-native-commands-directive-parameters-mechanical.md)
- [§4 WP9: Project identity from the directory alone (expert)](wp9-project-identity-from-the-directory-alone-expert.md)
- [§4 WP10: Closing sweep (mechanical)](wp10-closing-sweep-mechanical.md)
