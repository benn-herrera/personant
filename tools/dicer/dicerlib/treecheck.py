"""The tree check: in a diced tree, every document is linked from its parent, and every document is
reachable by links from the root's entry point.

A directory's head is its `entry-point.md` where it has one (the root, a diced volume), else its
`index.md`. A document's parent is its directory's head; a head's parent is its parent directory's
head; the root's entry point has none.
"""

import os
from collections import deque
from typing import Optional, TextIO

from dicerlib.linkcheck import markdown_files, prose_links
from dicerlib.linkrewrite import URL_SCHEME
from dicerlib.textfile import read_lines

HEADS = ("entry-point.md", "index.md")


def _head(directory: str) -> Optional[str]:
    for name in HEADS:
        path = os.path.join(directory, name)
        if os.path.isfile(path):
            return path
    return None


def _links_to(path: str) -> set[str]:
    """The real paths of the files a document's relative links name, fragments dropped."""
    found = set()
    for _, target in prose_links(read_lines(path)):
        ref = target.partition("#")[0]
        if ref == "" or ref.startswith("/") or URL_SCHEME.match(ref):
            continue
        found.add(os.path.realpath(os.path.join(os.path.dirname(path), ref)))
    return found


def check_tree(root: str, *, err: TextIO) -> tuple[int, int]:
    """(documents checked, problems) for one tree, each problem reported on err."""
    entry = os.path.join(root, "entry-point.md")
    if not os.path.isfile(entry):
        print(f"{root}: no entry-point.md", file=err)
        return 0, 1
    files = markdown_files([root])
    real = {os.path.realpath(f): f for f in files}
    links = {f: _links_to(f) for f in files}
    problems = 0
    root_real = os.path.realpath(root)
    for f in files:
        directory = os.path.dirname(f)
        if os.path.realpath(f) == os.path.realpath(entry):
            continue
        own_head = _head(directory)
        is_head = own_head is not None and os.path.realpath(own_head) == os.path.realpath(f)
        parent = _head(os.path.dirname(directory)) if is_head else own_head
        if parent is None or not os.path.realpath(os.path.dirname(parent)).startswith(root_real):
            print(f"{f}: no parent document (an index.md or entry-point.md beside it or above it)", file=err)
            problems += 1
            continue
        if os.path.realpath(f) not in links[parent]:
            print(f"{f}: not linked from its parent {parent}", file=err)
            problems += 1
    seen = {os.path.realpath(entry)}
    queue = deque([entry])
    while queue:
        for dest in links[queue.popleft()]:
            if dest in real and dest not in seen:
                seen.add(dest)
                queue.append(real[dest])
    for f in files:
        if os.path.realpath(f) not in seen:
            print(f"{f}: not reachable from {entry}", file=err)
            problems += 1
    return len(files), problems


def check_trees(roots: list[str], *, out: TextIO, err: TextIO) -> bool:
    checked = problems = 0
    for root in roots:
        n, p = check_tree(root, err=err)
        checked += n
        problems += p
    print(f"documents checked: {checked}, problems: {problems}", file=out)
    return problems == 0
