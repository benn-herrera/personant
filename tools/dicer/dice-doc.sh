#!/bin/sh
# dice-doc.sh <monolith.md> <out-dir> [map.tsv]
# Splits a markdown monolith by heading level into an entry point, section
# indexes and leaves under <out-dir>, rewriting §N references as relative
# links, then checks that the leaves reassemble the monolith and that every
# relative link resolves. Relative links in the monolith are taken relative to
# the current directory and rewritten to resolve from each leaf; map.tsv (see
# dice-doc.awk) redirects links to documents that have themselves been diced.
set -eu
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || { printf 'usage: %s <monolith.md> <out-dir> [map.tsv]\n' "${0}" >&2; exit 2; }
mono=${1}
out=${2}
map=${3:-}
here=$(cd "$(dirname "${0}")" && pwd)
[ -f "${mono}" ] || { printf 'not a file: %s\n' "${mono}" >&2; exit 2; }
[ -z "${map}" ] || [ -f "${map}" ] || { printf 'not a file: %s\n' "${map}" >&2; exit 2; }
command -v awk >/dev/null || { printf 'awk not on PATH\n' >&2; exit 2; }
if [ -d "${out}" ] && [ -n "$(ls -A "${out}")" ]; then
  printf 'out-dir not empty: %s\n' "${out}" >&2; exit 2
fi
mkdir -p "${out}"
root=$(pwd -P)
outabs=$(cd "${out}" && pwd -P)
# The manifest sits beside the output, never in a system-wide temp directory.
man=${out%/}.manifest
trap 'rm -f "${man}"' EXIT

# C locale: this awk's regex is multibyte-aware but substr is not; a split character aborts it
run() { LC_ALL=C awk -f "${here}/dice-doc.awk" -v out="${out}" -v map="${map}" -v root="${root}" -v outabs="${outabs}" "$@"; }

run -v mode=plan "${mono}" >"${man}"
cut -f4 "${man}" | while IFS= read -r p; do
  mkdir -p "${out}/$(dirname "${p}")"
done
run -v mode=write "${man}" "${mono}"
run -v mode=reassemble "${man}" "${mono}"
run -v mode=links "${man}"
