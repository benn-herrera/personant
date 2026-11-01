"""Dicing one document, and re-checking trees that already exist."""

import io
import os
import posixpath
from typing import TextIO

from dicerlib.linkcheck import check_links
from dicerlib.linkrewrite import LinkMap, Tree
from dicerlib.outline import plan_sections
from dicerlib.reassemble import check_reassembly
from dicerlib.split import write_tree
from dicerlib.textfile import read_lines, split_lines


def _tree(out: str, map_path: str) -> Tree:
    """Links in a monolith are relative to the run directory, so it anchors their rebasing."""
    link_map = LinkMap.load(map_path) if map_path and os.path.isfile(map_path) else LinkMap()
    return Tree(out=out, outabs=os.path.realpath(out), root=os.getcwd(), link_map=link_map)


def dice_doc(monolith: str, out: str, map_path: str, *, stdout: TextIO, stderr: TextIO) -> int:
    """Split a monolith into a tree under out (empty or absent), then check that the tree
    reassembles the monolith and that its links resolve. 0 ok, 1 a failed step, 2 bad arguments."""
    if not os.path.isfile(monolith):
        print(f"not a file: {monolith}", file=stderr)
        return 2
    if map_path and not os.path.isfile(map_path):
        print(f"not a file: {map_path}", file=stderr)
        return 2
    if os.path.isdir(out) and os.listdir(out):
        print(f"out-dir not empty: {out}", file=stderr)
        return 2
    os.makedirs(out, exist_ok=True)
    tree = _tree(out, map_path)
    lines = read_lines(monolith)
    sections, ok = plan_sections(lines, err=stderr)
    if not ok:
        return 1
    for s in sections:
        os.makedirs(os.path.join(out, posixpath.dirname(s.path)), exist_ok=True)
    write_tree(sections, lines, tree, err=stderr)
    if not check_reassembly(sections, lines, tree, out=stdout, err=stderr):
        return 1
    return 0 if check_links(sections, tree, out=stdout, err=stderr) else 1


def _leaf_count(out: str) -> int:
    count = 0
    for _, dirs, files in os.walk(out):
        names = dirs + files
        count += sum(n.endswith(".md") and n not in ("index.md", "entry-point.md") for n in names)
    return count


def verify(map_path: str, pairs: list[tuple[str, str]], *, stdout: TextIO, stderr: TextIO) -> int:
    """Re-run the reassembly and link checks on each (monolith, out) tree, once every tree a link
    points into exists. 0 when every check passes, else 1."""
    rc = 0
    for monolith, out in pairs:
        if not os.path.isdir(out):
            print(f"not a directory: {out}", file=stderr)
            rc = 1
            continue
        tree = _tree(out, map_path)
        print(f"=== {monolith} -> {out}", file=stdout)
        try:
            lines = read_lines(monolith)
        except OSError:
            print(f"cannot read: {monolith}", file=stderr)
            rc = 1
            continue
        sections, ok = plan_sections(lines, err=stderr)
        if not ok:
            rc = 1
        if not check_reassembly(sections, lines, tree, out=stdout, err=stdout):
            rc = 1
        log = io.StringIO()
        if not check_links(sections, tree, out=log, err=log):
            rc = 1
        for line in split_lines(log.getvalue()):
            print(f"  {line}", file=stdout)
        print(f"  leaves: {_leaf_count(out)}", file=stdout)
    return rc
