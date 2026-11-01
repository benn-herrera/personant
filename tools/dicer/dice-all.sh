#!/bin/sh
# dice-all.sh <manifest.tsv> <src-dir> <work-dir>
# Dice a set of related markdown documents into one tree of volumes whose cross-document links
# point at each other: refactor (where flagged) -> link map -> dice each -> group indexes and
# entry point -> verify. Run from the directory the documents' relative links are written
# against. Sources are read from <src-dir>/<file>; intermediates go to <work-dir>. Output
# directories that are not nested inside another output are removed first.
#
# manifest.tsv columns (tab separated; blank lines and lines starting with # are skipped):
#   file    source file name, relative to <src-dir>; documents link to it by this base name
#   outdir  output directory
#   flags   comma list, may be empty: refactor (run-ins to headings), items (also bold list items),
#           entry (the file's text, verbatim, becomes <outdir>/entry-point.md followed by a list
#           of the volumes directly under <outdir>; the file itself is not diced)
#   label   for a volume nested as <parent volume>/<group>/<name>: the group's title, used for
#           <parent>/<group>/index.md and the entry added to <parent>/entry-point.md
set -eu
[ "$#" -eq 3 ] || { printf 'usage: %s <manifest.tsv> <src-dir> <work-dir>\n' "${0}" >&2; exit 2; }
manifest=${1}
src=${2}
work=${3}
here=$(cd "$(dirname "${0}")" && pwd)
[ -f "${manifest}" ] || { printf 'not a file: %s\n' "${manifest}" >&2; exit 2; }
[ -d "${src}" ] || { printf 'not a directory: %s\n' "${src}" >&2; exit 2; }
mkdir -p "${work}"
map=${work}/links.tsv
rows=${work}/rows
vols=${work}/volumes

# normalise to '|' separated so empty fields survive `read`
awk -F'\t' '!/^#/ && NF { print $1 "|" $2 "|" $3 "|" $4 }' "${manifest}" >"${rows}"
: >"${vols}"
: >"${map}"
entry_out=
entry_file=

# 1. refactor where flagged; record each volume as mono|out|label|file
while IFS='|' read -r file out flags label; do
  [ -f "${src}/${file}" ] || { printf 'missing source: %s/%s\n' "${src}" "${file}" >&2; exit 2; }
  case ",${flags}," in
    *,entry,*) entry_out=${out}; entry_file=${src}/${file}; continue ;;
  esac
  mono=${src}/${file}
  case ",${flags}," in
    *,refactor,*) mono=${work}/${file}
      case ",${flags}," in *,items,*) items=1 ;; *) items=0 ;; esac
      "${here}/refactor.sh" "${src}/${file}" "${mono}" "${items}" ;;
  esac
  printf '%s|%s|%s|%s\n' "${mono}" "${out}" "${label}" "${file}" >>"${vols}"
done <"${rows}"

# 2. link map: each document's entry point, and each heading's leaf
if [ -n "${entry_out}" ]; then
  printf '%s\t%s/entry-point.md\n' "$(basename "${entry_file}")" "${entry_out}" >>"${map}"
fi
while IFS='|' read -r mono out label file; do
  printf '%s\t%s/entry-point.md\n' "${file}" "${out}" >>"${map}"
  LC_ALL=C awk -f "${here}/dice-doc.awk" -v mode=plan "${mono}" |
    awk -F'\t' -v o="${file}" -v d="${out}" '$9 != "" && !seen[$9]++ { printf "%s\t%s\t%s/%s\n", o, $9, d, $4 }' >>"${map}"
done <"${vols}"

# nested: is directory $1 inside another volume's output?
nested() {
  while IFS='|' read -r _ other _ _; do
    case "${1}" in "${other}"/*) return 0 ;; esac
  done <"${vols}"
  return 1
}

# 3. clear generated output, dice each volume (parents precede children in the manifest)
[ -z "${entry_out}" ] || { mkdir -p "${entry_out}"; rm -f "${entry_out}/entry-point.md"; }
while IFS='|' read -r mono out label file; do
  nested "${out}" || rm -rf "${out}"
done <"${vols}"
while IFS='|' read -r mono out label file; do
  echo "=== ${file}"
  # a tree whose links point at trees not built yet reports them dead here; verify below decides
  "${here}/dice-doc.sh" "${mono}" "${out}" "${map}" 2>&1 | grep -v '^dead link' || true
done <"${vols}"

# 4. group indexes for nested volumes, and the entry point
: >"${work}/generated"
title() { awk '/^# / { sub(/^# /, ""); print; exit }' "${1}"; }
seen_groups=
while IFS='|' read -r mono out label file; do
  group=$(dirname "${out}")
  parent=$(dirname "${group}")
  nested "${out}" || continue
  case " ${seen_groups} " in
    *" ${group} "*) ;;
    *) printf '# %s\n\nContents:\n\n' "${label}" >"${group}/index.md"
       echo "${group}/index.md" >>"${work}/generated"
       printf -- '- [%s](%s/index.md)\n' "${label}" "$(basename "${group}")" >>"${parent}/entry-point.md"
       seen_groups="${seen_groups} ${group}" ;;
  esac
  printf -- '- [%s](%s/entry-point.md)\n' "$(title "${mono}")" "$(basename "${out}")" >>"${group}/index.md"
done <"${vols}"

# first sentence of a document's opening paragraph, links reduced to their text
purpose() {
  awk '
    !started { if ($0 ~ /^# /) started = 1; next }
    para == "" && $0 == "" { next }
    $0 == "" { exit }
    { para = para (para == "" ? "" : " ") $0 }
    END {
      s = para; out = ""
      while (match(s, /\[[^]]*\]\([^)]*\)/)) {
        t = substr(s, RSTART + 1, index(substr(s, RSTART), "]") - 2)
        out = out substr(s, 1, RSTART - 1) t; s = substr(s, RSTART + RLENGTH)
      }
      s = out s
      if (match(s, /\. /)) s = substr(s, 1, RSTART)
      print s
    }' "${1}"
}
if [ -n "${entry_out}" ]; then
  {
    cat "${entry_file}"
    printf '\nVolumes:\n\n'
    while IFS='|' read -r mono out label file; do
      [ "$(dirname "${out}")" = "${entry_out}" ] || continue
      printf -- '- [%s](%s/entry-point.md): %s\n' "$(basename "${out}")" "$(basename "${out}")" "$(purpose "${mono}")"
    done <"${vols}"
  } >"${entry_out}/entry-point.md"
  echo "${entry_out}/entry-point.md" >>"${work}/generated"
fi

# 5. verify every tree, then the files written above
set --
while IFS='|' read -r mono out label file; do set -- "$@" "${mono}" "${out}"; done <"${vols}"
rc=0
"${here}/verify.sh" "${map}" "${work}" "$@" || rc=1
echo "=== files written by this script: link targets exist"
while IFS= read -r f; do
  d=$(dirname "${f}")
  grep -o '](\([^)#]*\.md\))' "${f}" | sed 's/^](//; s/)$//' >"${work}/targets" || true
  while IFS= read -r t; do
    [ -f "${d}/${t}" ] || { echo "  DEAD ${f} -> ${t}"; rc=1; }
  done <"${work}/targets"
  echo "  checked ${f}"
done <"${work}/generated"
exit "${rc}"
