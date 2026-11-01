"""The write pass: each section's lines, links rewritten, into its file under the output directory;
the entry point and each section index end with a Contents block listing what is below them."""

from typing import TextIO

from dicerlib.linkrewrite import Linker, Tree, parent_dir, rel
from dicerlib.outline import Outline, Section, load_outline
from dicerlib.textfile import fence_flags, write_lines


def contents_block(outline: Outline, section: Section) -> list[str]:
    """The entry point lists every section, indented by depth; an index lists its children."""
    block = ["Contents:", ""]
    if section.id == 0:
        for s in outline.sections[1:]:
            indent = "  " * (outline.depth[s.id] - 1)
            block.append(f"{indent}- [{outline.link_text(s.id)}]({s.path})")
        return block
    here = parent_dir(section.path)
    for kid in outline.children.get(section.id, []):
        block.append(f"- [{outline.link_text(kid)}]({rel(here, outline.sections[kid].path)})")
    return block


def write_tree(sections: list[Section], lines: list[str], tree: Tree, *, err: TextIO) -> None:
    outline = load_outline(sections, err=err)
    linker = Linker(outline, tree)
    fenced = fence_flags(lines)
    for s in sections:
        leaf_dir = parent_dir(s.path)
        body = []
        for k in range(s.first - 1, s.last):
            next_line = lines[k + 1] if k + 1 < len(lines) else ""
            body.append(lines[k] if fenced[k] else linker.forward(lines[k], leaf_dir, next_line))
        if s.kind != "L":
            body += contents_block(outline, s)
        write_lines(f"{tree.out}/{s.path}", body)
    distinct = "".join(f" §{number}({count})" for number, count in linker.dangling.items())
    print(f"refs linked: {linker.resolved}; dangling: {len(linker.dangling)} distinct:{distinct}", file=err)
