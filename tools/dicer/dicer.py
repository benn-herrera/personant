#!/usr/bin/env python3
"""dicer: split markdown documents by heading into trees of small linked files (see README.md)."""

import argparse
import os
import sys
from typing import Optional

from dicerlib.dicedoc import dice_doc, verify
from dicerlib.linkcheck import check_markdown_links
from dicerlib.manifest import dice_docs
from dicerlib.refactor import list_runins, refactor
from dicerlib.textfile import read_lines


def _keep_list() -> list[str]:
    """Titles never converted: the comma list in DICER_KEEP, default "Open"; empty keeps none."""
    keep = os.environ.get("DICER_KEEP", "Open")
    return keep.split(",") if keep else []


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="dicer.py", description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True, metavar="command")

    p = commands.add_parser("dice-doc", help="dice one document, then check it reassembles and its links resolve")
    p.add_argument("monolith")
    p.add_argument("out", help="output directory, absent or empty")
    p.add_argument("map", nargs="?", default="", help="link map TSV")

    p = commands.add_parser("dice-docs", help="dice the documents a manifest TSV lists, then verify them all")
    p.add_argument("manifest")
    p.add_argument("src", help="directory the manifest's files are read from")
    p.add_argument("work", help="directory for intermediates")

    p = commands.add_parser("verify", help="re-run the checks on trees already built")
    p.add_argument("map", help='link map TSV, or "-" for none')
    p.add_argument("pairs", nargs="+", metavar="monolith out")

    p = commands.add_parser("refactor", help="turn bold run-in titles into headings")
    p.add_argument("input", metavar="in")
    p.add_argument("output", metavar="out")
    p.add_argument("items", nargs="?", default="", help='"0" or absent: leave bold list items alone')

    p = commands.add_parser("list", help="classify every bold-led line of a document")
    p.add_argument("doc")

    p = commands.add_parser("check-links", help="check the relative links and anchors of markdown files")
    p.add_argument("roots", nargs="+", metavar="root", help="a directory, walked for every .md, or one file")
    return parser


def main(argv: Optional[list[str]] = None) -> int:
    for stream in (sys.stdout, sys.stderr):
        stream.reconfigure(encoding="utf-8", errors="surrogateescape")
    parser = _parser()
    args = parser.parse_args(argv)
    out, err = sys.stdout, sys.stderr
    if args.command == "dice-doc":
        return dice_doc(args.monolith, args.out, args.map, stdout=out, stderr=err)
    if args.command == "dice-docs":
        return dice_docs(args.manifest, args.src, args.work, keep=_keep_list(), stdout=out, stderr=err)
    if args.command == "verify":
        if len(args.pairs) % 2:
            parser.error("verify takes <map|-> followed by <monolith> <out> pairs")
        pairs = list(zip(args.pairs[::2], args.pairs[1::2]))
        return verify("" if args.map == "-" else args.map, pairs, stdout=out, stderr=err)
    if args.command == "refactor":
        items = args.items not in ("", "0")
        return refactor(args.input, args.output, items=items, keep=_keep_list(), out=out, err=err)
    if args.command == "check-links":
        missing = [root for root in args.roots if not os.path.exists(root)]
        if missing:
            parser.error(f"no such root: {', '.join(missing)}")
        return 0 if check_markdown_links(args.roots, out=out, err=err) else 1
    for row in list_runins(read_lines(args.doc), name=args.doc, keep=_keep_list()):
        print(row, file=out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
