---
#
# !GENERATED! from templates/commands/kb-build.tmpl.md and templates/shared-chunks.toml — edit those. DO NOT HAND EDIT THIS FILE.
# !TUNING! family=templates/family/claude.toml seat=none member=none tier=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku pin=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku stock=highest,high,medium,low,lowest harness=opencode
# !BODY-SHA256! 691de97f2c05b3d419f3a19e36f15c6b17093570106630a5ef0648efbf615cc0
#
---
Print the shell command that runs a KB build over the named LaTeX sources. This session prints it
and stops: it runs nothing, writes nothing, dispatches nothing, and waits for nothing.

## Arguments

```
/kb-build <source>[,source...]
```

The first token is the LaTeX source root, or a comma-separated list of volume paths. Each path
becomes one `--source` on the line below, in the order given and spelled relative to the repository
root. Nothing else is an argument: the KB root is always `kb-root/`, a name the tools hard-code and
take no override for. Anything the user typed after the source list is not an input — not a further
source, and nothing this command carries into the build; say so rather than folding it into the
line.

## What you print

```
PYTHONPATH=.opencode/agents python3 -m kb_tools.kb_driver run --source <path> [--source <path> ...]
```

Print it filled in — one `--source` per source — with what the user needs to run it and nothing
else:

- run it from the repository root;
- it stops at every decision the build reaches, printing there what to answer and the line that
  carries the answer back;
- running the line a stop printed is how the build goes on.

That is the whole of your work. Everything from there is the driver's: it opens the build, seeds the
spine, sequences the stages, and holds the user through its own output, in a process this session
never sees.
