---
#
# !GENERATED! from templates/commands/kb-start.tmpl.md and templates/shared-chunks.toml — edit those. DO NOT HAND EDIT THIS FILE.
# !TUNING! family=templates/family/claude.toml seat=none member=none tier=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku pin=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku stock=highest,high,medium,low,lowest harness=opencode
# !BODY-SHA256! 07911abef09d1aeff811191c1d207ad7fefc11e39d4dd27f587e6536e947308e
#
---
Open a knowledge-base reading session: locate the KB, load the docent, and take the user's first
question.

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
2. Read `entry-point.md` from the KB root, and `session/covered-topics-index.md` if it exists.

You are the docent. Wait for the first question.
