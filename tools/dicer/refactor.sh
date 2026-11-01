#!/bin/sh
# refactor.sh <in.md> <out.md> [items]
# Turn a document's bold run-in paragraphs (and, with items=1, its column-0 bold list items) into
# real headings, writing out.md, then check that every changed hunk is one of those conversions.
# The titles it skipped as too long are listed in <out.md>.skipped. Exit 1 on a bad hunk.
set -eu
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || { printf 'usage: %s <in.md> <out.md> [items]\n' "${0}" >&2; exit 2; }
in=${1}
out=${2}
items=${3:-0}
here=$(cd "$(dirname "${0}")" && pwd)
[ -f "${in}" ] || { printf 'not a file: %s\n' "${in}" >&2; exit 2; }
LC_ALL=C awk -f "${here}/runins.awk" -v mode=convert -v items="${items}" -v dest="${out}" "${in}" 2>"${out}.skipped"
printf '%s: %s long titles skipped; ' "${in}" "$(wc -l <"${out}.skipped" | tr -d ' ')"
diff -U0 "${in}" "${out}" | LC_ALL=C awk -f "${here}/hunkcheck.awk" -v orig="${in}"
