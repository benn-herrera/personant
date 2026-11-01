# dicer

Splits a markdown document by heading into a tree of small linked files, and keeps documents that
cite each other linked across their trees. Python 3.9 or later, standard library only; the tools
take every input as an argument and name nothing specific to a repository. Run them from the
directory the documents' relative links are written against.

## Tools

One CLI, `python3 dicer.py <command> ...`:

- `dice-doc <monolith.md> <out-dir> [map.tsv]` dices one document and runs the checks below.
  `out-dir` must be absent or empty. Exit 1 when a step fails, 2 on bad arguments.
- `refactor <in.md> <out.md> [items]` turns bold run-in titles into headings (see Transforms);
  any `items` value other than empty or `0` converts bold list items too. Exit 1 on a bad hunk.
- `dice-docs <manifest.tsv> <src-dir> <work-dir>` drives several documents: refactor where
  flagged, link map, dice each, group indexes and entry point, verify. Intermediates (refactored
  copies, their `.skipped` lists, the link map `links.tsv`) go to `<work-dir>`; output
  directories not nested in another output are removed first. See Worked example for a manifest.
- `verify <map|-> <monolith> <out-dir> ...` re-runs the checks on built trees, once every tree a
  link points into exists. Exit 1 if any check fails.
- `list <doc.md>` classifies every bold-led line of a document, one TSV row each: `doc:line`,
  class, new heading level, title, enclosing heading.

The passes behind them live in `dicerlib/`, one module each: `outline` (headings to files),
`split` (writing the tree), `linkrewrite` (section links, rebasing, the map), `reassemble` and
`linkcheck` (the checks), `refactor` (run-ins and the hunk check), `dicedoc` and `manifest` (the
drivers). `just test-dicer` runs the golden cases under `testdata/`.

## Heading convention

`#` title, then `##` sections, `###` subsections, `####` sub-subsections; deeper headings and
headings inside fenced code are text. A heading starting `N.` or `N.M` is numbered: its file is
`<number>-<slug>`, and `§N`, `§N.M` in prose link to it. Any other heading is named by its slug.
A heading with children becomes a directory with an `index.md`; otherwise one leaf file. Leaves
hold exactly the original lines, heading included. Sibling headings with the same slug would write
the same file, so they are an error and the run fails. Text before the first `##` is `entry-point.md`
with a `Contents:` list.

## Transforms (`refactor`)

A paragraph opening with `**Title.**`, `**Title:**` or a whole-line `**Title**` becomes a heading one
level below its enclosing heading, then a blank line and the rest. With `items`, a column-0 list item
`- **Title.** rest` does the same and its continuation lines are dedented by 2. Skipped, and listed
in `<out.md>.skipped`: titles over 8 words; bold followed by more sentence (`**Note** that ...`);
indented items; bold at the start of a wrapped line; bold outside any section. Every changed hunk is
checked to be one of these conversions.

Titles in the keep-list are never converted: the paragraph or item stays verbatim inside its section's
leaf and produces no hunk. The list is the comma-separated environment variable `DICER_KEEP` (read by
`refactor`, `dice-docs` and `list`), default `Open`; set it empty to keep none. Matching is exact and
case-sensitive on the title without its trailing `.` or `:`. `list` shows kept lines as `KEEP`.

## Link rewriting and the map

`§N` links to its file, and `§N.M` followed by a backticked name goes to the child whose heading
holds that name. A `§N` right after `SPEC `, `ARCHITECTURE `, `THESIS ` or `CONVENTIONS ` is left as
text (another document's section). Relative links to files outside the document are rewritten to
resolve from each leaf. The map is TSV: `<target>\t<new path from the run directory>` points a link
to a diced document at its entry point; `<target>\t<heading>\t<path>` points a link whose
following text quotes that heading, as `, "The walk"`, at the heading's leaf.

## Manifest (`dice-docs`)

Tab separated: `file`, `outdir`, `flags`, `label`; blank lines and lines starting `#` are skipped.
Flags (comma list, may be empty): `refactor`, `items`, `entry` (the file is copied verbatim to
`<outdir>/entry-point.md` with a `Volumes:` list of the volumes directly under `<outdir>`, each with
the first sentence of its opening paragraph; it is not diced). A volume nested as
`<parent>/<group>/<name>` takes the group's title in `label`; the group gets an `index.md` and the
parent's entry point gains a line for it. Parents precede children. Documents link to each other by
the base name of `file`.

## Checks

- Reassembly: concatenating the leaves in order, with the added `Contents:` blocks removed and
  section links reverted, reproduces the monolith's lines exactly (outside links are compared in
  their rewritten form). Proves nothing was lost, moved or edited.
- Links: every relative link in the output resolves to an existing file. Proves the tree and the
  cross-document map are consistent; it does not check `#anchor` fragments.

## Worked example

kbase's `PROJECT_DOCS/` was first produced from its root documents with this manifest (tab
separated; `tools/dicer/kbase-docs.tsv` in the commit that introduced it), the sources copied from
`HEAD` into a scratch directory:

| file | outdir | flags | label |
|---|---|---|---|
| THESIS.md | PROJECT_DOCS | entry | |
| SPEC.md | PROJECT_DOCS/SPEC | refactor | |
| ARCHITECTURE.md | PROJECT_DOCS/ARCHITECTURE | refactor | |
| CONVENTIONS.md | PROJECT_DOCS/CONVENTIONS | refactor,items | |
| RIGOR_AND_BEARING_SCORING_SCHEME.md | PROJECT_DOCS/SPEC/scoring/rigor-and-bearing | | Scoring schemes |
| STRENGTH_SCORING_SCHEME.md | PROJECT_DOCS/SPEC/scoring/strength | | |

    just dice-docs kbase-docs.tsv <src-dir> <work-dir>

The leaves under `PROJECT_DOCS/` are now the source; the root `SPEC.md`, `ARCHITECTURE.md`,
`CONVENTIONS.md` and `THESIS.md` are pointers to them.
