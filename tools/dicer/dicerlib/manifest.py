"""The manifest driver: dice a set of documents into one tree of volumes that link to each other.

Manifest rows are tab separated (blank lines and lines starting with # skipped):
  file    source file name, relative to the source directory; documents link to it by this name
  outdir  output directory
  flags   comma list, may be empty: refactor (run-ins to headings), items (bold list items too),
          entry (the file, verbatim, becomes <outdir>/entry-point.md followed by a list of the
          volumes directly under <outdir>; the file itself is not diced)
  label   for a volume nested as <parent volume>/<group>/<name>: the group's title, used for
          <parent>/<group>/index.md and the line added to <parent>/entry-point.md
"""

import io
import os
import posixpath
import re
import shutil
from collections.abc import Iterable
from dataclasses import dataclass
from typing import Optional, TextIO

from dicerlib.dicedoc import dice_doc, verify
from dicerlib.outline import plan_sections
from dicerlib.refactor import refactor
from dicerlib.textfile import append_text, read_lines, read_text, terminated_lines, write_lines, write_text

_LINK = re.compile(r"\[([^\]]*)\]\([^)]*\)")
_MD_LINK_TARGET = re.compile(r"\]\(([^)#]*\.md)\)")


@dataclass
class _Volume:
    monolith: str  # the file diced: the source, or its refactored copy in the work directory
    out: str
    label: str
    file: str


@dataclass
class _Entry:
    out: str
    source: str


def _dirname(path: str) -> str:
    return posixpath.dirname(path.rstrip("/")) or "."


def _basename(path: str) -> str:
    return posixpath.basename(path.rstrip("/"))


def _read_manifest(path: str) -> list[list[str]]:
    rows = []
    for line in read_lines(path):
        if line and not line.startswith("#"):
            rows.append((line.split("\t") + ["", "", ""])[:4])
    return rows


def _nested(out: str, volumes: list[_Volume]) -> bool:
    return any(out.startswith(v.out + "/") for v in volumes)


def _remove(path: str) -> None:
    if os.path.isdir(path) and not os.path.islink(path):
        shutil.rmtree(path)
    elif os.path.lexists(path):
        os.remove(path)


def _title(monolith: str) -> str:
    for line in read_lines(monolith):
        if line.startswith("# "):
            return line[2:]
    return ""


def _purpose(monolith: str) -> str:
    """First sentence of the paragraph after the title, links reduced to their text."""
    paragraph: list[str] = []
    started = False
    for line in read_lines(monolith):
        if not started:
            started = line.startswith("# ")
        elif line:
            paragraph.append(line)
        elif paragraph:
            break
    text = _LINK.sub(r"\1", " ".join(paragraph))
    end = text.find(". ")
    return text[:end + 1] if end >= 0 else text


def _link_map_rows(volumes: list[_Volume], entry: Optional[_Entry], *, err: TextIO) -> list[str]:
    """Each document's entry point, and the leaf of each of its headings (first of a title wins)."""
    rows = []
    if entry:
        rows.append(f"{_basename(entry.source)}\t{entry.out}/entry-point.md")
    for v in volumes:
        rows.append(f"{v.file}\t{v.out}/entry-point.md")
        sections, _ = plan_sections(read_lines(v.monolith), err=err)
        seen = set()
        for s in sections:
            if s.title and s.title not in seen:
                seen.add(s.title)
                rows.append(f"{v.file}\t{s.title}\t{v.out}/{s.path}")
    return rows


def _write_group_indexes(volumes: list[_Volume]) -> list[str]:
    """For volumes nested as <parent>/<group>/<name>: the group's index, and its line in the
    parent's entry point. Returns the indexes written."""
    written = []
    for v in volumes:
        if not _nested(v.out, volumes):
            continue
        group = _dirname(v.out)
        index = f"{group}/index.md"
        if index not in written:
            write_text(index, f"# {v.label}\n\nContents:\n\n")
            written.append(index)
            append_text(f"{_dirname(group)}/entry-point.md", f"- [{v.label}]({_basename(group)}/index.md)\n")
        append_text(index, f"- [{_title(v.monolith)}]({_basename(v.out)}/entry-point.md)\n")
    return written


def _write_entry_point(entry: _Entry, volumes: list[_Volume]) -> str:
    path = f"{entry.out}/entry-point.md"
    listing = "".join(
        f"- [{_basename(v.out)}]({_basename(v.out)}/entry-point.md): {_purpose(v.monolith)}\n"
        for v in volumes
        if _dirname(v.out) == entry.out
    )
    write_text(path, read_text(entry.source) + "\nVolumes:\n\n" + listing)
    return path


def _check_written(files: list[str], *, out: TextIO) -> bool:
    ok = True
    for f in files:
        here = _dirname(f)
        for line in read_lines(f):
            for target in _MD_LINK_TARGET.findall(line):
                if not os.path.isfile(f"{here}/{target}"):
                    print(f"  DEAD {f} -> {target}", file=out)
                    ok = False
        print(f"  checked {f}", file=out)
    return ok


def dice_docs(manifest: str, src: str, work: str, *, keep: Iterable[str], stdout: TextIO, stderr: TextIO) -> int:
    """Refactor where flagged, build the link map, dice each volume, write group indexes and the
    entry point, then verify every tree. Output directories not nested in another output are
    removed first; parents must precede children in the manifest."""
    if not os.path.isfile(manifest):
        print(f"not a file: {manifest}", file=stderr)
        return 2
    if not os.path.isdir(src):
        print(f"not a directory: {src}", file=stderr)
        return 2
    os.makedirs(work, exist_ok=True)

    volumes: list[_Volume] = []
    entry: Optional[_Entry] = None
    for file, out, flags, label in _read_manifest(manifest):
        source = f"{src}/{file}"
        if not os.path.isfile(source):
            print(f"missing source: {source}", file=stderr)
            return 2
        flag_set = flags.split(",")
        if "entry" in flag_set:
            entry = _Entry(out, source)
            continue
        monolith = source
        if "refactor" in flag_set:
            monolith = f"{work}/{file}"
            rc = refactor(source, monolith, items="items" in flag_set, keep=keep, out=stdout, err=stderr)
            if rc:
                return rc
        volumes.append(_Volume(monolith, out, label, file))

    map_path = f"{work}/links.tsv"
    write_lines(map_path, _link_map_rows(volumes, entry, err=stderr))

    if entry:
        os.makedirs(entry.out, exist_ok=True)
        _remove(f"{entry.out}/entry-point.md")
    for v in volumes:
        if not _nested(v.out, volumes):
            _remove(v.out)
    for v in volumes:
        print(f"=== {v.file}", file=stdout)
        # A tree linking into trees not built yet reports those links dead; verify decides below.
        log = io.StringIO()
        dice_doc(v.monolith, v.out, map_path, stdout=log, stderr=log)
        stdout.write("".join(line for line in terminated_lines(log.getvalue()) if not line.startswith("dead link")))

    written = _write_group_indexes(volumes)
    if entry:
        written.append(_write_entry_point(entry, volumes))

    rc = verify(map_path, [(v.monolith, v.out) for v in volumes], stdout=stdout, stderr=stderr)
    print("=== files written by this script: link targets exist", file=stdout)
    if not _check_written(written, out=stdout):
        rc = 1
    return 1 if rc else 0
