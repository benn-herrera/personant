#
# !INSTALLED! from the adjagent repo — do not edit in place; edit the source repo and re-install.
# !BODY-SHA256! 64f57384e3dac2ba2a96447f6b98bfbbae4daa81f7cc2fb266735711234b3d77
#
"""Runtime query interface over the KB derived index (Phase 3 consumer).

The query side reads the index streams under the KB's ``.index/`` directory
through ``kb_load`` — the KB's format first, an older KB converted in memory —
and exposes question-shaped lookups via :class:`Index`.

``kb_cmd`` is a normal importable package. The CLI entry point is ``python -m
kb_tools.kb_cmd``; for programmatic access import the loader directly::

    from kb_tools.kb_cmd import load
    idx = load()
    idx.depends_on("0ktpcn")
"""

from .index import (
    CitationEdge,
    Claim,
    DependsOnEdge,
    ExperimentNode,
    FrameworkNode,
    GraphNode,
    Index,
    StrengthenByItem,
    SubtreeAggregate,
    SupportNode,
    load,
)

__all__ = [
    "Claim",
    "CitationEdge",
    "DependsOnEdge",
    "ExperimentNode",
    "FrameworkNode",
    "GraphNode",
    "Index",
    "StrengthenByItem",
    "SubtreeAggregate",
    "SupportNode",
    "load",
]
