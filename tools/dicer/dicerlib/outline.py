"""The heading pass: which monolith lines become which file of the diced tree."""

import re
from dataclasses import dataclass
from typing import TextIO

from dicerlib.textfile import FenceTracker

_HEADING = re.compile(r"#+ ")
_NUMBER = re.compile(r"[0-9]+(?:\.[0-9]+)*\.? ")
_NOT_SLUG = re.compile(r"[^a-z0-9]+")
_ASCII_LOWER = str.maketrans("ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz")
_DEEPEST_SPLIT_LEVEL = 4


@dataclass
class Section:
    id: int
    parent: int
    kind: str  # "E" the entry point, "I" a section index (has children), "L" a leaf
    path: str  # relative to the output directory
    first: int  # first and last monolith line, 1-based and inclusive
    last: int
    number: str  # "1.2" for a heading numbered "1.2." or "1.2", else ""
    level: int
    title: str  # heading text without its number


def slug(text: str) -> str:
    text = text.translate(_ASCII_LOWER).replace("`", "")
    return _NOT_SLUG.sub("-", text).strip("-")


def split_number(text: str) -> tuple[str, str]:
    """("1.2", "Title") for "1.2. Title"; ("", text) for an unnumbered heading."""
    text = text.lstrip(" ")
    m = _NUMBER.match(text)
    if not m:
        return "", text
    return m.group()[:-1].removesuffix("."), text[m.end():]


def plan_sections(lines: list[str], *, err: TextIO) -> tuple[list[Section], bool]:
    """The sections of a monolith, entry point first, and whether planning succeeded.

    An empty slug stops planning, so the sections returned stop before it; same-path siblings
    are reported and planning goes on.
    """
    headings = []  # (level, line number, text)
    fence = FenceTracker()
    seen_title = False
    for no, line in enumerate(lines, 1):
        fenced = fence.inside(line)
        m = _HEADING.match(line)
        if fenced or not m or m.end() > 7:
            continue
        level = m.end() - 1
        if level == 1:
            if seen_title:
                print(f"extra level-1 heading at line {no} kept as text", file=err)
            seen_title = True
        elif level <= _DEEPEST_SPLIT_LEVEL:
            headings.append((level, no, line[m.end():].rstrip(" ")))

    parents = []
    open_headings: list[int] = []
    has_children = set()
    for i, (level, _, _) in enumerate(headings, 1):
        while open_headings and headings[open_headings[-1] - 1][0] >= level:
            open_headings.pop()
        parent = open_headings[-1] if open_headings else 0
        parents.append(parent)
        open_headings.append(i)
        has_children.add(parent)

    entry_last = headings[0][1] - 1 if headings else len(lines)
    sections = [Section(0, 0, "E", "entry-point.md", 1, entry_last, "", 1, "")]
    dirs = {0: ""}
    path_line: dict[str, int] = {}
    ok = True
    for i, (level, no, text) in enumerate(headings, 1):
        number, title = split_number(text)
        name_slug = slug(title)
        if not name_slug and not number:
            print(f"empty slug for heading at line {no}", file=err)
            return sections, False
        name = f"{number}-{name_slug}" if number and name_slug else number + name_slug
        parent = parents[i - 1]
        dirs[i] = f"{dirs[parent]}/{name}" if parent else name
        path = f"{dirs[i]}/index.md" if i in has_children else f"{dirs[i]}.md"
        if path in path_line:
            print(f"path collision: {path} (lines {path_line[path]} and {no})", file=err)
            ok = False
        path_line[path] = no
        last = headings[i][1] - 1 if i < len(headings) else len(lines)
        kind = "I" if i in has_children else "L"
        sections.append(Section(i, parent, kind, path, no, last, number, level, title))
    return sections, ok


class Outline:
    """Lookups over planned sections; `sections[i].id == i`."""

    def __init__(self, sections: list[Section]) -> None:
        self.sections = sections
        self.children: dict[int, list[int]] = {}
        self.depth = {0: 0}
        self.by_number: dict[str, int] = {}
        self.duplicate_numbers: list[str] = []
        for s in sections:
            if s.id > 0:
                self.children.setdefault(s.parent, []).append(s.id)
                self.depth[s.id] = self.depth.get(s.parent, 0) + 1
            if s.number:
                if s.number in self.by_number:
                    self.duplicate_numbers.append(s.number)
                self.by_number[s.number] = s.id

    def path_of_number(self, number: str) -> str:
        return self.sections[self.by_number[number]].path

    def child_path_naming(self, number: str, name: str) -> str:
        """Path of the first child of section `number` whose heading holds `name` in backticks."""
        for kid in self.children.get(self.by_number[number], []):
            if f"`{name}`" in self.sections[kid].title:
                return self.sections[kid].path
        return ""

    def link_text(self, sid: int) -> str:
        """A section's title, after the section sign and number of its nearest numbered ancestor."""
        numbered = sid
        while numbered > 0 and not self.sections[numbered].number:
            numbered = self.sections[numbered].parent
        prefix = f"§{self.sections[numbered].number} " if numbered > 0 else ""
        return prefix + self.sections[sid].title


def load_outline(sections: list[Section], *, err: TextIO) -> Outline:
    """An Outline, reporting section numbers that more than one heading carries."""
    outline = Outline(sections)
    for number in outline.duplicate_numbers:
        print(f"duplicate section number {number}", file=err)
    return outline
