#!/bin/sh
# verify.sh <map.tsv|-> <work-dir> <monolith> <out-dir> [<monolith> <out-dir> ...]
# Re-run the reassembly and link checks of dice-doc.sh on trees that already exist, so links
# between trees can be checked once all of them are built. Run from the same directory as
# dice-doc.sh (links in the monoliths are relative to it). Exit 1 if any check fails.
set -u
[ "$#" -ge 4 ] && [ $(($# % 2)) -eq 0 ] || { printf 'usage: %s <map.tsv|-> <work-dir> <monolith> <out-dir> ...\n' "${0}" >&2; exit 2; }
map=${1}
work=${2}
shift 2
[ "${map}" != "-" ] || map=
here=$(cd "$(dirname "${0}")" && pwd)
root=$(pwd -P)
rc=0
n=0
while [ "$#" -gt 0 ]; do
  mono=${1}
  out=${2}
  shift 2
  n=$((n + 1))
  man=${work}/verify-${n}.manifest
  outabs=$(cd "${out}" && pwd -P) || { rc=1; continue; }
  run() { LC_ALL=C awk -f "${here}/dice-doc.awk" -v out="${out}" -v map="${map}" -v root="${root}" -v outabs="${outabs}" "$@"; }
  echo "=== ${mono} -> ${out}"
  run -v mode=plan "${mono}" >"${man}" || rc=1
  run -v mode=reassemble "${man}" "${mono}" 2>&1 || rc=1
  run -v mode=links "${man}" >"${man}.log" 2>&1 || rc=1
  sed 's/^/  /' "${man}.log"
  rm -f "${man}" "${man}.log"
  echo "  leaves: $(find "${out}" -name '*.md' ! -name index.md ! -name entry-point.md | wc -l | tr -d ' ')"
done
exit "${rc}"
