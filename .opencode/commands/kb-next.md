---
#
# !GENERATED! from templates/commands/kb-next.tmpl.md and templates/shared-chunks.toml — edit those. DO NOT HAND EDIT THIS FILE.
# !TUNING! family=templates/family/claude.toml seat=none member=none tier=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku pin=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku stock=highest,high,medium,low,lowest harness=opencode
# !BODY-SHA256! b3d6ac9ec11d18799a00810d5a3e269a00a8fe1d839103de3a34f093bdd89976
#
---
Continue a knowledge-base reading session on a new topic: locate the KB, load the docent, and answer
the question left in the handoff.

## Locate the KB

Before reading anything, establish where the KB is. Probe in order:

1. `./kb-root/entry-point.md` **and** `./kb-root/.index/` both exist → **KB root** `./kb-root`, **project root** `.`
2. otherwise `./entry-point.md` **and** `./.index/` both exist → **KB root** `.`, **project root** `..`
3. otherwise **stop and ask.** Name both probes you ran, and say that this command runs from the project root (the directory holding `kb-root/`) or from `kb-root/` itself. Do not search the tree for a KB — section directories carry their own `entry-point.md`, and a search finds the wrong one.

The `.index/` sibling is what the probes turn on: it is what makes a directory a viable KB root rather than a section inside one.

From here, every file path resolves against the located **KB root**, and every tool invocation anchors to the located **project root** — `PYTHONPATH=<project-root>/.opencode/agents`.

Then, in order:

1. Read `<project-root>/.opencode/agents/kb-docent.md` — that definition is who
   you are for this session.
2. Read `session/new-topic.md` from the KB root.

You are the docent. Respond to the question at the end.
