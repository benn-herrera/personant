"""Link rewriting: section references become links to their files, links outside the document are
rebased to resolve from each leaf, and links to diced documents go through the link map.

Map rows (TSV): "<target>\t<path from the run directory>" sends a link to a diced document to that
path; "<target>\t<heading>\t<path>" sends a link whose following text quotes that heading, as
`, "The walk"`, to the heading's leaf.
"""

import re
from dataclasses import dataclass, field
from typing import Callable, Optional

from dicerlib.outline import Outline
from dicerlib.textfile import read_lines

URL_SCHEME = re.compile(r"[a-z]+:")
_SECTION_REF = re.compile(r"§[0-9]+(?:\.[0-9]+)*")
_SECTION_LINK_TEXT = re.compile(r"§[0-9]")
_NAMED_CHILD = re.compile(r" `([^`]+)`")
_OTHER_DOCUMENT = re.compile(r"(?:SPEC|ARCHITECTURE|THESIS|CONVENTIONS)(?:\.md)? $")
_BARE_TAIL = re.compile(r",? *")
_QUOTED_HEADING = re.compile(r',? *"([^"]+)"')

# (raw link, text, target, rest of the line after the link) -> replacement
OnLink = Callable[[str, str, str, str], str]
# (reference, line, offset after the reference, output so far) -> (replacement, offset to resume at)
OnRef = Callable[[str, str, int, str], tuple[str, int]]


def rel(from_dir: str, to: str) -> str:
    """Relative path from directory `from_dir` ("" is the root) to file `to`."""
    a = from_dir.split("/") if from_dir else []
    b = to.split("/") if to else []
    i = 0
    while i < len(a) and i < len(b) - 1 and a[i] == b[i]:
        i += 1
    return "../" * (len(a) - i) + "/".join(b[i:])


def parent_dir(path: str) -> str:
    return path.rpartition("/")[0]


@dataclass
class LinkMap:
    documents: dict[str, str] = field(default_factory=dict)
    headings: dict[tuple[str, str], str] = field(default_factory=dict)

    @classmethod
    def load(cls, path: str) -> "LinkMap":
        link_map = cls()
        for line in read_lines(path):
            fields = line.split("\t")
            if len(fields) == 2:
                link_map.documents[fields[0]] = fields[1]
            elif len(fields) == 3:
                link_map.headings[(fields[0], fields[1])] = fields[2]
        return link_map


@dataclass
class Tree:
    """A diced document's output directory and the absolute paths its links resolve against."""

    out: str  # as given, prefixed to the files written and the paths reported
    outabs: str  # out, absolute and with symlinks resolved
    root: str  # the run directory, absolute: links in the monolith are relative to it
    link_map: LinkMap


def _scan(s: str, on_link: OnLink, on_ref: Optional[OnRef] = None) -> str:
    """Rewrite the prose of one line: code spans pass through, each `[text](target)` is replaced by
    on_link and, given on_ref, each `§N` reference by on_ref."""
    if "](" not in s and (on_ref is None or "§" not in s):
        return s
    out = []
    p, n = 0, len(s)
    while p < n:
        c = s[p]
        if c == "`":
            q = p
            while q < n and s[q] == "`":
                q += 1
            close = s.find(s[p:q], q)
            end = q if close < 0 else close + q - p
            out.append(s[p:end])
            p = end
            continue
        if c == "[":
            bracket = s.find("]", p)
            paren = s.find(")", bracket + 2) if bracket >= 0 and s.startswith("](", bracket) else -1
            if paren >= 0:
                out.append(on_link(s[p:paren + 1], s[p + 1:bracket], s[bracket + 2:paren], s[paren + 1:]))
                p = paren + 1
                continue
        if on_ref is not None:
            m = _SECTION_REF.match(s, p)
            if m:
                text, p = on_ref(m.group(), s, m.end(), "".join(out))
                out.append(text)
                continue
        out.append(c)
        p += 1
    return "".join(out)


def _anchor_homes(outline: Outline, lines: list[str]) -> dict[str, tuple[str, str]]:
    """For each heading anchor of the monolith, the file of the diced tree that holds the heading
    and its anchor there: "" where the heading opens that file (a link to the file needs none),
    else its anchor within the file, whose repeat suffixes count that file's headings alone."""
    from dicerlib.linkcheck import heading_anchor_list  # linkcheck imports this module

    owner = {}  # monolith line -> section holding it
    for s in outline.sections:
        for no in range(s.first, s.last + 1):
            owner[no] = s
    homes: dict[str, tuple[str, str]] = {}
    per_file: dict[int, list[str]] = {}
    for no, anchor in heading_anchor_list(lines):
        s = owner.get(no)
        if s is None:
            continue
        local = per_file.setdefault(s.id, [])
        local.append(anchor.base)
        n = local.count(anchor.base) - 1
        within = f"{anchor.base}-{n}" if n else anchor.base
        own = no == s.first and s.id > 0
        homes.setdefault(anchor.name, (s.path, "" if own else within))
    return homes


def link_targets(line: str) -> list[str]:
    """The target of every link in the prose of a line, in order."""
    found = []

    def collect(raw: str, text: str, target: str, after: str) -> str:
        found.append(target)
        return raw

    _scan(line, collect)
    return found


class Linker:
    """Rewrites lines of one diced document; counts the section references it resolved and the
    ones it could not."""

    def __init__(self, outline: Outline, tree: Tree, lines: Optional[list[str]] = None) -> None:
        self.outline = outline
        self.tree = tree
        self.resolved = 0
        self.dangling: dict[str, int] = {}
        self.anchors = _anchor_homes(outline, lines or [])

    def forward(self, line: str, leaf_dir: str, next_line: str) -> str:
        """Link section references and rebase outside links, for a line written under leaf_dir."""
        return _scan(line, self._rebaser(leaf_dir, next_line), self._ref_linker(leaf_dir))

    def rebase_only(self, line: str, leaf_dir: str, next_line: str) -> str:
        return _scan(line, self._rebaser(leaf_dir, next_line))

    def revert(self, line: str, leaf_dir: str) -> str:
        """Undo the section links `forward` made; every other link stays."""

        def unlink(raw: str, text: str, target: str, after: str) -> str:
            return text if self._made_by_forward(text, target, leaf_dir) else raw

        return _scan(line, unlink)

    def _rebaser(self, leaf_dir: str, next_line: str) -> OnLink:
        def rebase(raw: str, text: str, target: str, after: str) -> str:
            return f"[{text}]({self._rebase(target, leaf_dir, after, next_line)})"

        return rebase

    def _ref_linker(self, leaf_dir: str) -> OnRef:
        def link_ref(ref: str, s: str, p: int, before: str) -> tuple[str, int]:
            number = ref[1:]
            if _OTHER_DOCUMENT.search(before):
                return ref, p
            if number not in self.outline.by_number:
                self.dangling[number] = self.dangling.get(number, 0) + 1
                return ref, p
            target, text = self.outline.path_of_number(number), ref
            m = _NAMED_CHILD.match(s, p)
            if m:
                child = self.outline.child_path_naming(number, m.group(1))
                if child:
                    target, text, p = child, ref + m.group(), m.end()
            self.resolved += 1
            return f"[{text}]({rel(leaf_dir, target)})", p

        return link_ref

    def _made_by_forward(self, text: str, target: str, leaf_dir: str) -> bool:
        if not _SECTION_LINK_TEXT.match(text):
            return False
        number, name = text[1:], ""
        i = number.find(" `")
        if i >= 0:
            number, name = number[:i], number[i + 2:].removesuffix("`")
        if number not in self.outline.by_number:
            return False
        if name:
            dest = self.outline.child_path_naming(number, name)
        else:
            dest = self.outline.path_of_number(number)
        return target == rel(leaf_dir, dest)

    def _rebase(self, target: str, leaf_dir: str, after: str, next_line: str) -> str:
        """An outside link target, relative to the run directory, made relative to leaf_dir; a
        target in the link map goes to its diced tree instead (the heading's leaf when the text
        after the link quotes a mapped heading, which may open the next line)."""
        if target.startswith("#") and target[1:] in self.anchors:
            path, anchor = self.anchors[target[1:]]
            return rel(leaf_dir, path) + (f"#{anchor}" if anchor else "")
        if target == "" or URL_SCHEME.match(target) or target[0] in "#/":
            return target
        path, hash_, fragment = target.partition("#")
        fragment = hash_ + fragment
        dest = path
        link_map = self.tree.link_map
        if path in link_map.documents:
            dest, fragment = link_map.documents[path], ""
            if _BARE_TAIL.fullmatch(after):
                after = f"{after} {next_line}"
            m = _QUOTED_HEADING.match(after)
            if m:
                dest = link_map.headings.get((path, m.group(1)), dest)
        here = self.tree.outabs + (f"/{leaf_dir}" if leaf_dir else "")
        return rel(here, f"{self.tree.root}/{dest}") + fragment
