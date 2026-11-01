### Plan work rating and dispatch

- Every plan rates each dispatch **mechanical** or **expert**.
  - The rating decides the model: a mechanical dispatch runs on a stepped-down model, as the
    operator's global instructions specify (`~/.claude/CLAUDE.md` "Step-down dispatches"); an
    expert dispatch runs on the seat's pinned model (`~/.claude/CLAUDE.md` "Subagent handling",
    "Coder dispatches").
  - Rate what the dispatch must decide, not how much it types.
    - mechanical: a package whose work is settled by the plan's own text — a rename, a
      transcription, a test that mirrors a stated rule.
    - expert: one that must choose between defensible options, diagnose behaviour, or write
      model-facing prose.
- Coder packages that share a build run one at a time in the shared working tree; every package
  that touches Go code shares `just build`. No dispatch uses a git worktree — neither
  `git worktree add` nor a worktree-isolated agent. For a baseline, read the committed side with
  `git show HEAD:<path>`.

