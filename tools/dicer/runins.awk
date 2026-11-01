# runins.awk -v mode=list|convert -v name=FILE [-v dest=OUT] [-v items=1] <doc.md>
# list:    name:line <TAB> class <TAB> new heading level <TAB> bold text <TAB> enclosing heading
# convert: write to dest a copy with every RUNIN line (and, with items=1, every ITEM) turned
#          into a heading + blank + remaining text
# classes: RUNIN  bold title opening a paragraph ("**T.**" / "**T:**" / whole-line "**T**")
#          ITEM   column-0 list item "- **T.** rest" / "- **T:** rest" (convert only with items=1);
#                 its continuation lines (indented 2+) are dedented by 2
#          LABEL  paragraph-start bold that is a sentence subject or has punctuation outside the bold
#          LIST   other list item or indented line led by bold
#          MID    bold at line start, but the previous line is not blank
# a title of more than 8 words is skipped (SKIPLONG) in convert mode
function infence(l,   t, c, n) {
  t = l; sub(/^ +/, "", t); c = substr(t, 1, 1)
  if (c != "`" && c != "~") return fch != ""
  n = 0; while (substr(t, n + 1, 1) == c) n++
  if (fch == "") {
    if (n < 3 || (c == "`" && index(substr(t, n + 1), "`"))) return 0
    fch = c; flen = n; return 1
  }
  if (c == fch && n >= flen && substr(t, n + 1) ~ /^ *$/) fch = ""
  return 1
}
function emit(s) { if (mode == "convert") print s > dest }
{
  fz = infence($0)
  if (init) {                                   # inside a converted list item
    if ($0 ~ /^[ \t]*$/) { emit($0); pb = 1; next }
    if ($0 ~ /^  /) { emit(substr($0, 3)); pb = 0; next }
    init = 0
  }
  if (!fz && match($0, /^#+ /)) { hl = RLENGTH - 1; hd = substr($0, RLENGTH + 1); pb = 0; emit($0); next }
  cls = ""; t = ""; rest = ""
  if (!fz && $0 ~ /^[ \t]*([-*+]|[0-9]+[.)])?[ \t]*\*\*[^*]/) {
    if (match($0, /^- \*\*[^*]+\*\*/)) {
      n = RLENGTH; t = substr($0, 5, n - 6); rest = substr($0, n + 1)
      if (t ~ /[.:]$/) { cls = "ITEM"; sub(/[.:]$/, "", t) } else { cls = "LIST"; t = substr($0, 1, 70) }
    } else if ($0 ~ /^[ \t]/ || $0 ~ /^([-+]|[0-9]+[.)]) /) { cls = "LIST"; t = substr($0, 1, 70) }
    else if (!pb) { cls = "MID"; t = substr($0, 1, 70) }
    else if (match($0, /^\*\*[^*]+\*\*/)) {
      n = RLENGTH; t = substr($0, 3, n - 4); rest = substr($0, n + 1)
      if (rest == "" || t ~ /[.:]$/) { cls = "RUNIN"; sub(/[.:]$/, "", t) } else { cls = "LABEL"; t = substr($0, 1, 70) }
    } else { cls = "LABEL"; t = substr($0, 1, 70) }
  }
  if ((cls == "RUNIN" || cls == "ITEM") && hl < 2) cls = cls "-OUTSIDE-SECTION"
  if (cls == "ITEM" && !items && mode == "convert") cls = "LIST"
  w = split(t, ww, " ")
  if ((cls == "RUNIN" || cls == "ITEM") && w > 8 && mode == "convert") { cls = "SKIPLONG"; printf "skipped long: line %d: %s\n", NR, t > "/dev/stderr" }
  if (cls != "" && mode == "list") {
    printf "%s:%d\t%s%s\t%s\t%s\t%s\n", name, NR, cls, ((cls == "RUNIN" || cls == "ITEM") && w > 8 ? "(long)" : ""), ((cls == "RUNIN" || cls == "ITEM") ? hl + 1 : ""), t, hd
  }
  if (cls == "RUNIN" || cls == "ITEM") {
    emit(sprintf("%s %s", substr("######", 1, hl + 1), t))
    sub(/^ +/, "", rest)
    if (rest != "") { emit(""); emit(rest) } else if (cls == "ITEM") emit("")
    if (cls == "ITEM") init = 1
  } else emit($0)
  pb = ($0 ~ /^[ \t]*$/)
}
