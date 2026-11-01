"""The link checks: every relative link in a diced tree names a readable file (fragments ignored);
every relative link in a set of markdown files names an existing path and, given a `#fragment`, a
heading of the target whose GitHub anchor equals it."""

import os
import re
from dataclasses import dataclass
from typing import Iterator, TextIO

from dicerlib.linkrewrite import URL_SCHEME, Tree, link_targets, parent_dir
from dicerlib.outline import Section, load_outline
from dicerlib.textfile import FenceTracker, read_lines

_ATX_HEADING = re.compile(r" {0,3}#{1,6}(?:[ \t]|$)")
_CLOSING_HASHES = re.compile(r"(?:^|[ \t]+)#+[ \t]*$")


def prose_links(lines: list[str]) -> Iterator[tuple[int, str]]:
    """(1-based line number, target as written) of every link outside fenced code and code spans."""
    fence = FenceTracker()
    for no, line in enumerate(lines, 1):
        if not fence.inside(line):
            for target in link_targets(line):
                yield no, target


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
        for no, target in prose_links(leaf):
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


def github_anchor(heading: str) -> str:
    """GitHub's anchor for a heading's text: lowercased, every character but a letter, digit, space,
    hyphen or underscore dropped, spaces made hyphens. Not `outline.slug`, which names files."""
    kept = "".join(c for c in heading.lower() if c.isalnum() or c in " -_")
    return kept.replace(" ", "-")


@dataclass
class Anchor:
    base: str  # github_anchor of the heading's text
    name: str  # base, or base-N for the document's N-th repeat of it


def heading_anchor_list(lines: list[str]) -> list[tuple[int, Anchor]]:
    """(1-based line, anchor) of a document's ATX headings outside fenced code, in order, a repeat
    taking -1, -2, ..."""
    found = []
    repeats: dict[str, int] = {}
    fence = FenceTracker()
    for no, line in enumerate(lines, 1):
        m = _ATX_HEADING.match(line)
        if fence.inside(line) or not m:
            continue
        base = github_anchor(_CLOSING_HASHES.sub("", line[m.end():].strip(" \t")))
        n = repeats.get(base, 0)
        found.append((no, Anchor(base, f"{base}-{n}" if n else base)))
        repeats[base] = n + 1
    return found


def heading_anchors(lines: list[str]) -> set[str]:
    """The anchors of a document's ATX headings outside fenced code."""
    return {anchor.name for _, anchor in heading_anchor_list(lines)}


def markdown_files(roots: list[str]) -> list[str]:
    """Every `.md` under each directory root, walked in sorted order, and each file root as given."""
    files = []
    for root in roots:
        if not os.path.isdir(root):
            files.append(root)
            continue
        for directory, subdirs, names in os.walk(root):
            subdirs.sort()
            files.extend(os.path.join(directory, n) for n in sorted(names) if n.endswith(".md"))
    return files


def check_markdown_links(roots: list[str], *, out: TextIO, err: TextIO) -> bool:
    """Report each broken relative link in the markdown files under roots, then the count."""
    anchors: dict[str, set[str]] = {}

    def anchors_of(path: str) -> set[str]:
        key = os.path.realpath(path)
        if key not in anchors:
            is_markdown = key.endswith(".md") and os.path.isfile(key)
            anchors[key] = heading_anchors(read_lines(key)) if is_markdown else set()
        return anchors[key]

    checked = broken = 0
    done = set()
    for path in markdown_files(roots):
        if os.path.realpath(path) in done:
            continue
        done.add(os.path.realpath(path))
        for no, target in prose_links(read_lines(path)):
            if target == "" or target.startswith("/") or URL_SCHEME.match(target):
                continue
            checked += 1
            ref, _, fragment = target.partition("#")
            dest = os.path.join(os.path.dirname(path), ref) if ref else path
            if not os.path.exists(dest):
                problem = "missing file"
            elif fragment and fragment not in anchors_of(dest):
                problem = "missing anchor"
            else:
                continue
            print(f"{path}:{no}: {target}: {problem}", file=err)
            broken += 1
    print(f"links checked: {checked}, broken: {broken}", file=out)
    return broken == 0
