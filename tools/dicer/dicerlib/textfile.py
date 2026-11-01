"""Text files as every pass sees them: utf-8, lines split on newline only, bytes preserved.

Bytes that are not valid utf-8 survive a read/write round trip (surrogateescape), and newline=""
keeps a document's line endings exactly as written.
"""

import re

_TERMINATED_LINE = re.compile(r"[^\n]*\n|[^\n]+$")


def read_text(path: str) -> str:
    with open(path, encoding="utf-8", errors="surrogateescape", newline="") as f:
        return f.read()


def write_text(path: str, text: str) -> None:
    with open(path, "w", encoding="utf-8", errors="surrogateescape", newline="") as f:
        f.write(text)


def append_text(path: str, text: str) -> None:
    with open(path, "a", encoding="utf-8", errors="surrogateescape", newline="") as f:
        f.write(text)


def split_lines(text: str) -> list[str]:
    """Lines without their newline; a final unterminated line counts, a trailing newline adds none."""
    lines = text.split("\n")
    if lines[-1] == "":
        lines.pop()
    return lines


def terminated_lines(text: str) -> list[str]:
    """Lines with their newline kept, so an unterminated last line differs from a terminated one."""
    return _TERMINATED_LINE.findall(text)


def read_lines(path: str) -> list[str]:
    return split_lines(read_text(path))


def join_lines(lines: list[str]) -> str:
    return "".join(line + "\n" for line in lines)


def write_lines(path: str, lines: list[str]) -> None:
    write_text(path, join_lines(lines))


def is_blank(line: str) -> bool:
    return line.strip(" \t") == ""


class FenceTracker:
    """Fed a document's lines in order, tells which belong to fenced code blocks."""

    def __init__(self) -> None:
        self._char = ""
        self._length = 0

    def inside(self, line: str) -> bool:
        """True for a fence line or a line inside a fenced block."""
        text = line.lstrip(" ")
        char = text[:1]
        if char not in ("`", "~"):
            return self._char != ""
        run = len(text) - len(text.lstrip(char))
        if not self._char:
            if run < 3 or (char == "`" and "`" in text[run:]):
                return False
            self._char, self._length = char, run
            return True
        if char == self._char and run >= self._length and text[run:].strip(" ") == "":
            self._char = ""
        return True


def fence_flags(lines: list[str]) -> list[bool]:
    tracker = FenceTracker()
    return [tracker.inside(line) for line in lines]
