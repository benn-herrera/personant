"""The link check: every relative link in a diced tree names a readable file (fragments ignored)."""

import os
from typing import TextIO

from dicerlib.linkrewrite import URL_SCHEME, Tree, link_targets, parent_dir
from dicerlib.outline import Section, load_outline
from dicerlib.textfile import FenceTracker, read_lines


def check_links(sections: list[Section], tree: Tree, *, out: TextIO, err: TextIO) -> bool:
    load_outline(sections, err=err)
    links = []  # (path to probe, "file:line" of the link, target as written)
    for s in sections:
        path = f"{tree.out}/{s.path}"
        leaf_dir = parent_dir(s.path)
        try:
            leaf = read_lines(path)
        except OSError:
            continue
        fence = FenceTracker()
        for no, line in enumerate(leaf, 1):
            if fence.inside(line):
                continue
            for target in link_targets(line):
                target = target.partition("#")[0]
                if target == "" or URL_SCHEME.match(target):
                    continue
                probe = f"{tree.out}/{leaf_dir}/{target}" if leaf_dir else f"{tree.out}/{target}"
                links.append((probe, f"{path}:{no}", target))
    dead = 0
    for probe, where, target in links:
        if not (os.path.isfile(probe) and os.access(probe, os.R_OK)):
            print(f"dead link: {where} -> {target}", file=err)
            dead += 1
    print(f"links checked: {len(links)}, dead: {dead}", file=out)
    return dead == 0
