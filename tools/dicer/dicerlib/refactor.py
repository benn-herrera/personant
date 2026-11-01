"""Run-in titles to headings, and the check that every changed hunk is one of those conversions.

Classes of bold-led line:
  RUNIN  bold title opening a paragraph: "**T.**", "**T:**" or a whole-line "**T**"
  ITEM   column-0 list item "- **T.** rest" / "- **T:** rest" (converted only with items); its
         continuation lines (indented 2+) are dedented by 2
  LABEL  paragraph-start bold that is a sentence subject or has punctuation outside the bold
  LIST   any other list item or indented line led by bold
  MID    bold at line start where the previous line is not blank
  KEEP   a RUNIN/ITEM whose title is in the keep-list: never converted, copied verbatim
A RUNIN/ITEM before the first "##" is suffixed -OUTSIDE-SECTION; converting, a title of more than
eight words is SKIPLONG.
"""

import difflib
import re
from collections.abc import Iterable
from dataclasses import dataclass
from typing import TextIO

from dicerlib.textfile import FenceTracker, is_blank, join_lines, read_text, split_lines, terminated_lines, write_lines

_HEADING = re.compile(r"#+ ")
_BOLD_LED = re.compile(r"[ \t]*(?:[-*+]|[0-9]+[.)])?[ \t]*\*\*[^*]")
_BOLD_ITEM = re.compile(r"- \*\*([^*]+)\*\*")
_BOLD_RUNIN = re.compile(r"\*\*([^*]+)\*\*")
_OTHER_LIST_ITEM = re.compile(r"(?:[-+]|[0-9]+[.)]) ")
_WORD = re.compile(r"[^ \t\n]+")
_LONG_TITLE_WORDS = 8
_EXCERPT_BYTES = 70
_MERGE_GAP_LINES = 3


def _strip_mark(title: str) -> str:
    return title[:-1] if title[-1:] in (".", ":") else title


def _excerpt(line: str) -> str:
    """The first 70 bytes of a line, even if that splits a character."""
    raw = line.encode("utf-8", "surrogateescape")[:_EXCERPT_BYTES]
    return raw.decode("utf-8", "surrogateescape")


def _classify(line: str, *, prev_blank: bool) -> tuple[str, str, str]:
    """(class, title or excerpt, text after the bold) of a line outside fences; class "" for a line
    not led by bold."""
    if not _BOLD_LED.match(line):
        return "", "", ""
    m = _BOLD_ITEM.match(line)
    if m:
        if m.group(1)[-1] in ".:":
            return "ITEM", _strip_mark(m.group(1)), line[m.end():]
        return "LIST", _excerpt(line), ""
    if line[0] in " \t" or _OTHER_LIST_ITEM.match(line):
        return "LIST", _excerpt(line), ""
    if not prev_blank:
        return "MID", _excerpt(line), ""
    m = _BOLD_RUNIN.match(line)
    if m:
        rest = line[m.end():]
        if rest == "" or m.group(1)[-1] in ".:":
            return "RUNIN", _strip_mark(m.group(1)), rest
    return "LABEL", _excerpt(line), ""


def _walk(lines: list[str], *, converting: bool, items: bool, keep: Iterable[str], name: str) -> tuple[list[str], list[str]]:
    """(output lines, notes): converting, the notes are the skipped long titles; listing, one row
    per bold-led line."""
    keep = frozenset(keep)
    fence = FenceTracker()
    out: list[str] = []
    notes: list[str] = []
    level, heading = 0, ""
    prev_blank = in_item = False
    for no, line in enumerate(lines, 1):
        fenced = fence.inside(line)
        if in_item:
            if is_blank(line):
                out.append(line)
                prev_blank = True
                continue
            if line.startswith("  "):
                out.append(line[2:])
                prev_blank = False
                continue
            in_item = False
        m = _HEADING.match(line)
        if not fenced and m:
            level, heading = m.end() - 1, line[m.end():]
            prev_blank = False
            out.append(line)
            continue
        cls, title, rest = ("", "", "") if fenced else _classify(line, prev_blank=prev_blank)
        if cls in ("RUNIN", "ITEM") and level < 2:
            cls += "-OUTSIDE-SECTION"
        if cls == "ITEM" and not items and converting:
            cls = "LIST"
        if cls in ("RUNIN", "ITEM") and title in keep:
            cls = "KEEP"
        long_title = cls in ("RUNIN", "ITEM") and len(_WORD.findall(title)) > _LONG_TITLE_WORDS
        if long_title and converting:
            cls = "SKIPLONG"
            notes.append(f"skipped long: line {no}: {title}")
        if cls and not converting:
            new_level = str(level + 1) if cls in ("RUNIN", "ITEM") else ""
            notes.append(f"{name}:{no}\t{cls}{'(long)' if long_title else ''}\t{new_level}\t{title}\t{heading}")
        if cls in ("RUNIN", "ITEM"):
            out.append("#" * min(level + 1, 6) + " " + title)
            rest = rest.lstrip(" ")
            if rest:
                out += ["", rest]
            elif cls == "ITEM":
                out.append("")
            in_item = cls == "ITEM"
        else:
            out.append(line)
        prev_blank = is_blank(line)
    return out, notes


def convert_runins(lines: list[str], *, items: bool, keep: Iterable[str]) -> tuple[list[str], list[str]]:
    """(converted lines, a "skipped long" note per title left alone for its length)."""
    return _walk(lines, converting=True, items=items, keep=keep, name="")


def list_runins(lines: list[str], *, name: str, keep: Iterable[str]) -> list[str]:
    """One row per bold-led line: name:line, class, new heading level, title, enclosing heading."""
    return _walk(lines, converting=False, items=False, keep=keep, name=name)[1]


@dataclass
class _Hunk:
    start: int  # first old line, 1-based (for a pure insertion, the line after it)
    end: int  # old lines consumed so far, 0-based exclusive
    old: list[str]
    new: list[str]


def _expected(old: list[str]) -> list[tuple[bool, str]]:
    """The conversion of a hunk's old side, as (is a heading, text) per new line."""
    expected = []
    i = 0
    while i < len(old):
        item = _BOLD_ITEM.match(old[i])
        runin = None if item else _BOLD_RUNIN.match(old[i])
        if not item and not runin:
            expected.append((False, old[i]))
            i += 1
            continue
        m = item or runin
        rest = old[i][m.end():].lstrip(" ")
        expected.append((True, _strip_mark(m.group(1))))
        if item:
            expected.append((False, ""))
            if rest:
                expected.append((False, rest))
            i += 1
            while i < len(old) and (is_blank(old[i]) or old[i].startswith("  ")):
                expected.append((False, old[i][2:] if old[i].startswith("  ") else old[i]))
                i += 1
        else:
            if rest:
                expected += [(False, ""), (False, rest)]
            i += 1
    return expected


def _is_conversion(hunk: _Hunk) -> bool:
    expected = _expected(hunk.old)
    if len(expected) != len(hunk.new):
        return False
    for (is_heading, text), got in zip(expected, hunk.new):
        if is_heading:
            m = _HEADING.match(got)
            if not m or got[m.end():] != text:
                return False
        elif got != text:
            return False
    return True


def check_hunks(old_text: str, new_text: str) -> tuple[int, list[str]]:
    """(number of merged hunks, a BAD line for each merged hunk that is not a conversion).

    Hunks separated only by up to three unchanged blank lines are merged first, the blank lines
    joining both sides.
    """
    old_keys, new_keys = terminated_lines(old_text), terminated_lines(new_text)
    old, new = split_lines(old_text), split_lines(new_text)
    hunks: list[_Hunk] = []
    matcher = difflib.SequenceMatcher(None, old_keys, new_keys, autojunk=False)
    for tag, i1, i2, j1, j2 in matcher.get_opcodes():
        if tag == "equal":
            continue
        if hunks:
            last = hunks[-1]
            gap = old[last.end:i1]
            if len(gap) <= _MERGE_GAP_LINES and all(map(is_blank, gap)):
                last.old += gap + old[i1:i2]
                last.new += gap + new[j1:j2]
                last.end = i2
                continue
        hunks.append(_Hunk(start=i1 + 1, end=i2, old=old[i1:i2], new=new[j1:j2]))
    bad = [f"BAD merged hunk at old line {h.start}: {h.old[0] if h.old else ''}" for h in hunks if not _is_conversion(h)]
    return len(hunks), bad


def refactor(in_path: str, out_path: str, *, items: bool, keep: Iterable[str], out: TextIO, err: TextIO) -> int:
    """Write in_path with run-ins converted to out_path, and its skipped long titles to
    out_path.skipped; 1 if a changed hunk is not a conversion."""
    try:
        old_text = read_text(in_path)
    except OSError:
        print(f"not a file: {in_path}", file=err)
        return 2
    new_lines, skipped = convert_runins(split_lines(old_text), items=items, keep=keep)
    write_lines(out_path, new_lines)
    write_lines(out_path + ".skipped", skipped)
    print(f"{in_path}: {len(skipped)} long titles skipped; ", end="", file=out)
    count, bad = check_hunks(old_text, join_lines(new_lines))
    for line in bad:
        print(line, file=out)
    # With no hunks the count prints empty, as the summary always has.
    print(f"merged hunks {count or ''} bad {len(bad)}", file=out)
    return 1 if bad else 0
