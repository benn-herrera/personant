#
# !INSTALLED! from the adjagent repo — do not edit in place; edit the source repo and re-install.
# !BODY-SHA256! a628e90e5d2a4c2bab3a81afc11398924874d2b8e533c074ee6c31e4303a21c1
#
"""kb_driver — the KB build sequencer.

A mechanical driver for the build pipeline whose state machine lives in
``kb_tools.kb_pipeline``: it decides what happens next, spawns the inference
that does the work, relays the toolchain's own renders, and stops at a barrier
rather than asking a human anything.

Entry point is ``python3 -m kb_tools.kb_driver``; prompt templates ship beside
the code in ``prompt-templates/`` and are anchored by ``__file__`` (the same
package-resource exception ``kb_pipeline.installed_template`` already makes),
while repo and KB paths stay cwd-anchored.

Stdlib only.
"""
