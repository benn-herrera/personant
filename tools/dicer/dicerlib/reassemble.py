"""The reassembly check: the leaves, read in order with their Contents blocks dropped and section
links reverted, reproduce the monolith line for line (outside links compared in rebased form)."""

from typing import TextIO

from dicerlib.linkrewrite import Linker, Tree, parent_dir
from dicerlib.outline import Section, load_outline
from dicerlib.textfile import FenceTracker, fence_flags, read_lines


def check_reassembly(sections: list[Section], lines: list[str], tree: Tree, *, out: TextIO, err: TextIO) -> bool:
    linker = Linker(load_outline(sections, err=err), tree)
    fenced = fence_flags(lines)
    k = 0
    for s in sections:
        path = f"{tree.out}/{s.path}"
        try:
            leaf = read_lines(path)
        except OSError:
            print(f"reassemble: cannot read {path}", file=err)
            return False
        if s.kind != "L":
            cut = len(leaf)
            while cut > 0 and leaf[cut - 1] != "Contents:":
                cut -= 1
            if cut == 0:
                print(f"reassemble: no Contents block in {path}", file=err)
                return False
            leaf = leaf[:cut - 1]
        leaf_dir = parent_dir(s.path)
        fence = FenceTracker()
        for line in leaf:
            got = line if fence.inside(line) else linker.revert(line, leaf_dir)
            want = ""
            if k < len(lines):
                next_line = lines[k + 1] if k + 1 < len(lines) else ""
                want = lines[k] if fenced[k] else linker.rebase_only(lines[k], leaf_dir, next_line)
            k += 1
            if got != want:
                print(f"reassemble: first difference at monolith line {k} ({path})\n  want: {want}\n  got:  {got}", file=err)
                return False
    if k != len(lines):
        print(f"reassemble: leaves hold {k} lines, monolith {len(lines)}", file=err)
        return False
    print(f"reassemble ok: {k} lines", file=out)
    return True
