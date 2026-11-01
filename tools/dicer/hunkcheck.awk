# hunkcheck.awk -v orig=FILE  < diff -U0 orig new
# Merge hunks separated only by unchanged blank lines, then require each merged hunk's new
# side to equal the conversion of its old side: a run-in paragraph or a column-0 bold list item
# becomes "#.. Title", blank, rest; an item's indented continuation lines are dedented by 2.
BEGIN { while ((getline l < orig) > 0) O[++no_] = l }
function conv(   i, j, k, t, r, h, nb) {      # old lines o[1..no] -> expected new lines e[1..ne]
  ne = 0; i = 1
  while (i <= no) {
    if (o[i] ~ /^- \*\*[^*]+\*\*/ ) {
      match(o[i], /^- \*\*[^*]+\*\*/); t = substr(o[i], 5, RLENGTH - 6); r = substr(o[i], RLENGTH + 1)
      sub(/[.:]$/, "", t); sub(/^ +/, "", r)
      e[++ne] = "H " t; e[++ne] = ""; if (r != "") e[++ne] = r
      for (j = i + 1; j <= no && (o[j] ~ /^[ \t]*$/ || o[j] ~ /^  /); j++) e[++ne] = (o[j] ~ /^  / ? substr(o[j], 3) : o[j])
      i = j
    } else if (o[i] ~ /^\*\*[^*]+\*\*/) {
      match(o[i], /^\*\*[^*]+\*\*/); t = substr(o[i], 3, RLENGTH - 4); r = substr(o[i], RLENGTH + 1)
      sub(/[.:]$/, "", t); sub(/^ +/, "", r)
      e[++ne] = "H " t; if (r != "") { e[++ne] = ""; e[++ne] = r }
      i++
    } else { e[++ne] = o[i]; i++ }
  }
}
function finish(   i, ok, h) {
  if (no == 0 && na == 0) return
  nh++; conv(); ok = (ne == na)
  for (i = 1; ok && i <= ne; i++) {
    if (e[i] ~ /^H /) { h = a[i]; ok = (h ~ /^#+ / && substr(e[i], 3) == substr(h, index(h, " ") + 1)) }
    else ok = (e[i] == a[i])
  }
  if (!ok) { bad++; printf "BAD merged hunk at old line %d: %s\n", ostart, o[1] }
  no = na = 0
}
/^@@/ {
  split($2, p, ","); s = substr(p[1], 2) + 0; c = (p[2] == "" ? 1 : p[2] + 0); if (c == 0) s++   # old start (first line after, for pure adds)
  if (no + na > 0) {          # extend across blank gap lines, else finish
    gap = s - oend - 1; g = 1
    if (gap < 0 || gap > 3) g = 0
    for (i = 1; g && i <= gap; i++) if (O[oend + i] !~ /^[ \t]*$/) g = 0
    if (g) { for (i = 1; i <= gap; i++) { o[++no] = O[oend + i]; a[++na] = O[oend + i] } } else finish()
  }
  if (no + na == 0) ostart = s
  oend = s + (p[2] == "" ? 1 : p[2] + 0) - 1; if (p[2] != "" && p[2] + 0 == 0) oend = s - 1
  next
}
/^(---|\+\+\+)/ { next }
/^-/ { o[++no] = substr($0, 2); next }
/^\+/ { a[++na] = substr($0, 2); next }
END { finish(); print "merged hunks", nh, "bad", bad + 0; exit bad > 0 }
