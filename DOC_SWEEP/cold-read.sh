#!/usr/bin/env bash
# Cold read for the doc sweep: one agent reads one brief with no project instructions in context
# and no tools, so nothing outside the brief can reach it.
#
# usage: DOC_SWEEP/cold-read.sh <agent> <brief-file> [<output-file>]
#   agent        e.g. adjagent:architect, adjagent:go-coder
#   brief-file   the question plus the material, pasted in full
#   output-file  default: .claude-temp/cold-read/<agent>-<brief name>.md
#
# Runs from ../personant-cold-read/, a sibling of the repository holding only a copy of .claude/,
# so no project CLAUDE.md or AGENTS.md is loaded; the global ~/.claude/CLAUDE.md still is.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cold_dir="$(dirname "$repo")/personant-cold-read"

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 <agent> <brief-file> [<output-file>]" >&2
  exit 2
fi
agent="$1"
brief="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
out="${3:-$repo/.claude-temp/cold-read/${agent//:/-}-$(basename "$brief" .md).md}"

[[ -f "$brief" ]] || { echo "no brief: $brief" >&2; exit 1; }
[[ -d "$cold_dir/.claude" ]] || { echo "no $cold_dir/.claude: copy the repository's .claude/ there" >&2; exit 1; }
for f in CLAUDE.md AGENTS.md; do
  [[ ! -e "$cold_dir/$f" ]] || { echo "$cold_dir/$f exists; a cold read must not load it" >&2; exit 1; }
done

mkdir -p "$(dirname "$out")"
out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
cd "$cold_dir"
claude -p --tools "" --agent "$agent" < "$brief" > "$out"
echo "$out"
