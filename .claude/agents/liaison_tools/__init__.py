#
# !INSTALLED! from the adjagent repo — do not edit in place; edit the source repo and re-install.
# !BODY-SHA256! 75d6555a1e38dd84af8cab193593a16390102bb22012213404818acf49cc210d
#
"""liaison_tools — transport/session helpers shared by the liaison agents.

The tools are invoked by command line, and each is hyphenated so that no tool
admits an import. One module does: ``openai_chat``, the chat-completions
request the transport command is built on, for a caller that wants it in-process.
This package marker also lets the test suite under ``tests/`` collect with a
stable module path alongside the ``kb_tools`` suite.
"""
