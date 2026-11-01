# dice-doc.awk -- passes selected by -v mode=plan|write|reassemble|links
#   plan:       monolith          -> manifest (TSV) on stdout
#   write:      manifest monolith -> files under -v out=DIR
#   reassemble: manifest monolith -> verify leaves rebuild the monolith
#   links:      manifest          -> verify relative links in out resolve
# manifest columns: id parent kind(E|I|L) path first last num level title
# -v map=FILE (optional): TSV rows "<original target>\t<new path from root>" send a link to a
# document that has itself been diced to its tree; rows "<target>\t<heading>\t<path>" send a
# link whose following text quotes that heading to the heading's leaf.
# -v root=DIR and -v outabs=DIR: absolute paths of the monolith's directory and of out.
BEGIN {
  FS = "\t"
  if (map != "") {
    while ((getline mapline < map) > 0) {
      mn = split(mapline, mf, "\t")
      if (mn == 2) mapent[mf[1]] = mf[2]; else if (mn == 3) maphead[mf[1] SUBSEP mf[2]] = mf[3]
    }
    close(map)
  }
}

# 1 if the line is a fence line or inside a fenced block (state in fch/flen)
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

function slug(s) {
  s = tolower(s); gsub(/`/, "", s); gsub(/[^a-z0-9]+/, "-", s)
  sub(/^-/, "", s); sub(/-$/, "", s); return s
}

function dirof(p) { sub(/\/?[^\/]*$/, "", p); return p }

# relative path from directory 'from' ("" = root) to file 'to'
function rel(from, to,   a, b, n, m, i, k, r) {
  n = split(from, a, "/"); m = split(to, b, "/"); i = 0
  while (i < n && i < m - 1 && a[i + 1] == b[i + 1]) i++
  r = ""; for (k = i; k < n; k++) r = r "../"
  for (k = i + 1; k <= m; k++) r = r b[k] (k < m ? "/" : "")
  return r
}

# index of the ")" closing a "[text](target)" that starts s, else 0
function linkend(s,   i, r) {
  i = index(s, "]"); if (i == 0 || substr(s, i, 2) != "](") return 0
  r = index(substr(s, i + 2), ")"); return r ? i + 1 + r : 0
}

# A link target outside the monolith is relative to root (the monolith's own directory);
# return it relative to the leaf directory 'dir' of out. A target in the map points at its
# diced tree instead: at the heading leaf when the text after the link quotes that heading
# exactly (the quote may open the next line), else at the tree's entry point.
function rebase(tgt, dir, after,   t0, frag, k, q, p) {
  if (tgt == "" || tgt ~ /^[a-z]+:/ || tgt ~ /^[#\/]/) return tgt
  t0 = tgt; frag = ""
  if (k = index(t0, "#")) { frag = substr(t0, k); t0 = substr(t0, 1, k - 1) }
  p = t0
  if (t0 in mapent) {
    p = mapent[t0]; frag = ""
    if (after ~ /^,? *$/) after = after " " nextline
    if (match(after, /^,? *"[^"]+"/)) {
      q = substr(after, RSTART, RLENGTH); sub(/^,? *"/, "", q); sub(/"$/, "", q)
      if ((t0 SUBSEP q) in maphead) p = maphead[t0 SUBSEP q]
    }
  }
  return rel(outabs (dir == "" ? "" : "/" dir), root "/" p) frag
}

# One scanner for four jobs, so the rules for what counts as prose live once.
# fwd: link bare section refs and rebase outside links.  ext: rebase outside links only.
# rev: unlink links whose text starts with a section sign.
# chk: verify relative link targets exist (relative to directory 'dir' of out).
function scan(s, dir, op,   o, c, n, bt, r, j, i, ref, nm, text, q, tgt, hit) {
  if (index(s, "](") == 0 && (op != "fwd" || index(s, "§") == 0)) return s
  o = ""
  while (s != "") {
    c = substr(s, 1, 1)
    if (c == "`") {
      n = 0; while (substr(s, n + 1, 1) == "`") n++
      bt = substr(s, 1, n); r = substr(s, n + 1); j = index(r, bt)
      if (j == 0) { o = o bt; s = r } else { o = o substr(s, 1, n + j - 1 + n); s = substr(s, n + j + n) }
    } else if (c == "[" && (j = linkend(s))) {
      i = index(s, "]"); tgt = substr(s, i + 2, j - i - 2); text = substr(s, 2, i - 2)
      if (op == "chk") check(tgt, dir)
      if (op == "rev" && ours(text, tgt, dir)) o = o text
      else if (op == "fwd" || op == "ext") o = o "[" text "](" rebase(tgt, dir, substr(s, j + 1)) ")"
      else o = o substr(s, 1, j)
      s = substr(s, j + 1)
    } else if (op == "fwd" && match(s, /^§[0-9]+(\.[0-9]+)*/)) {
      ref = substr(s, 1, RLENGTH); s = substr(s, RLENGTH + 1)
      nm = ref; sub(/^§/, "", nm); text = ref
      if (o ~ /(SPEC|ARCHITECTURE|THESIS|CONVENTIONS)(\.md)? $/) o = o ref   # another document's section
      else if (nm in npath) {
        tgt = npath[nm]
        if (match(s, /^ `[^`]+`/) && (q = oppath(nm, substr(s, 3, RLENGTH - 3))) != "") {
          tgt = q; text = text substr(s, 1, RLENGTH); s = substr(s, RLENGTH + 1)
        }
        o = o "[" text "](" rel(dir, tgt) ")"; nres++
      } else { o = o ref; dang[nm]++ }
    } else { o = o c; s = substr(s, 2) }
  }
  return o
}

# path of the child of section nm whose heading holds `q`, else ""
function oppath(nm, q,   n, i, hit) {
  n = split(kids[nid[nm]], hit, " ")
  for (i = 1; i <= n; i++) if (index(ttl[hit[i]], "`" q "`")) return mpath[hit[i]]
  return ""
}

# 1 if [text](tgt) is exactly the link the forward pass would have made for text
function ours(text, tgt, dir,   nm, q, i) {
  if (text !~ /^§[0-9]/) return 0
  nm = substr(text, 1); sub(/^§/, "", nm); q = ""
  if (i = index(nm, " `")) { q = substr(nm, i + 2); sub(/`$/, "", q); nm = substr(nm, 1, i - 1) }
  if (!(nm in npath)) return 0
  return tgt == rel(dir, q != "" ? oppath(nm, q) : npath[nm])
}

# queue a link target; probed after all files are read (probing a file that is
# mid-read would share its stream)
function check(tgt, dir) {
  sub(/#.*/, "", tgt)
  if (tgt == "" || tgt ~ /^[a-z]+:/) return
  nlinks++; qf[nlinks] = out "/" (dir == "" ? "" : dir "/") tgt; qs[nlinks] = curfile ":" curln; qt[nlinks] = tgt
}

function load(   id) {                            # one manifest row into arrays
  id = $1; mkind[id] = $3; mpath[id] = $4; mfirst[id] = $5; mlast[id] = $6
  num[id] = $7; ttl[id] = $9; par[id] = $2; nrows = id
  if (id > 0) { kids[$2] = kids[$2] " " id; dep[id] = dep[$2] + 1 }
  if ($7 != "") { if ($7 in npath) printf "duplicate section number %s\n", $7 > "/dev/stderr"; npath[$7] = $4; nid[$7] = id }
}

function ltext(i,   a) {                          # contents link text: section sign, number, title
  a = i; while (a > 0 && num[a] == "") a = par[a]
  return (a > 0 ? "§" num[a] " " : "") ttl[i]
}

function contents(i, f,   n, k, h, d) {          # added block: label, blank, bullets
  print "Contents:\n" > f
  if (i == 0) { for (k = 1; k <= nrows; k++) print sprintf("%*s", 2 * (dep[k] - 1), "") "- [" ltext(k) "](" mpath[k] ")" > f; return }
  n = split(kids[i], h, " "); d = dirof(mpath[i])
  for (k = 1; k <= n; k++) print "- [" ltext(h[k]) "](" rel(d, mpath[h[k]]) ")" > f
}

# "1.2. Title" -> num "1.2", title "Title"; unnumbered -> num ""
function numsplit(t) {
  sub(/^ +/, "", t); pnum = ""
  if (match(t, /^[0-9]+(\.[0-9]+)*\.? /)) { pnum = substr(t, 1, RLENGTH - 1); sub(/\.$/, "", pnum); t = substr(t, RLENGTH + 1) }
  return t
}

mode == "plan" {
  infz = infence($0)
  if (!infz && match($0, /^#+ /) && RLENGTH <= 7) {
    lv = RLENGTH - 1
    if (lv == 1 && !h1) h1 = 1
    else if (lv == 1) printf "extra level-1 heading at line %d kept as text\n", NR > "/dev/stderr"
    else if (lv <= 4) { n++; lvl[n] = lv; ln[n] = NR; tx[n] = substr($0, RLENGTH + 1); sub(/ +$/, "", tx[n]) }
  }
  next
}

mode != "plan" && NR == FNR { load(); next }
{ mono[FNR] = $0; fz[FNR] = infence($0); nmono = FNR }

END {
  if (mode == "plan") {
    for (i = 1; i <= n; i++) {
      while (sp > 0 && lvl[stk[sp]] >= lvl[i]) sp--
      p[i] = sp ? stk[sp] : 0; stk[++sp] = i; hasc[p[i]] = 1
    }
    printf "0\t0\tE\tentry-point.md\t1\t%d\t\t1\t\n", (n ? ln[1] - 1 : NR)
    for (i = 1; i <= n; i++) {
      t = numsplit(tx[i]); nu = pnum; s = slug(t)
      if (s == "" && nu == "") { printf "empty slug for heading at line %d\n", ln[i] > "/dev/stderr"; exit 1 }
      nm = (nu != "" && s != "") ? nu "-" s : nu s
      dp[i] = (p[i] ? dp[p[i]] "/" : "") nm
      pth = hasc[i] ? dp[i] "/index.md" : dp[i] ".md"
      if (pth in seen) { printf "path collision: %s (lines %d and %d)\n", pth, ln[seen[pth]], ln[i] > "/dev/stderr"; bad = 1 }
      seen[pth] = i
      last = (i < n) ? ln[i + 1] - 1 : NR
      printf "%d\t%d\t%s\t%s\t%d\t%d\t%s\t%d\t%s\n", i, p[i], (hasc[i] ? "I" : "L"), pth, ln[i], last, nu, lvl[i], t
    }
    exit bad
  }
  if (mode == "write") {
    for (i = 0; i <= nrows; i++) {
      f = out "/" mpath[i]; d = dirof(mpath[i])
      for (k = mfirst[i]; k <= mlast[i]; k++) { nextline = mono[k + 1]; print (fz[k] ? mono[k] : scan(mono[k], d, "fwd")) > f }
      if (mkind[i] != "L") contents(i, f)
      close(f)
    }
    for (x in dang) { nd++; dl = dl " §" x "(" dang[x] ")" }
    printf "refs linked: %d; dangling: %d distinct:%s\n", nres, nd, dl > "/dev/stderr"
    exit 0
  }
  if (mode == "reassemble") {
    k = 0
    for (i = 0; i <= nrows; i++) {
      f = out "/" mpath[i]; nl = 0; fch = ""
      while ((r = (getline l < f)) > 0) ln2[++nl] = l
      if (r < 0) { printf "reassemble: cannot read %s\n", f > "/dev/stderr"; exit 1 }
      close(f)
      if (mkind[i] != "L") {
        while (nl > 0 && ln2[nl] != "Contents:") nl--
        if (nl == 0) { printf "reassemble: no Contents block in %s\n", f > "/dev/stderr"; exit 1 }
        nl--
      }
      for (q = 1; q <= nl; q++) {
        got = infence(ln2[q]) ? ln2[q] : scan(ln2[q], dirof(mpath[i]), "rev"); k++
        nextline = mono[k + 1]                      # outside links keep their rebased form: expect that
        want = fz[k] ? mono[k] : scan(mono[k], dirof(mpath[i]), "ext")
        if (got != want) { printf "reassemble: first difference at monolith line %d (%s)\n  want: %s\n  got:  %s\n", k, f, want, got > "/dev/stderr"; exit 1 }
      }
    }
    if (k != nmono) { printf "reassemble: leaves hold %d lines, monolith %d\n", k, nmono > "/dev/stderr"; exit 1 }
    printf "reassemble ok: %d lines\n", k
    exit 0
  }
  if (mode == "links") {
    for (i = 0; i <= nrows; i++) {
      f = out "/" mpath[i]; d = dirof(mpath[i]); curfile = f; curln = 0; fch = ""
      while ((r = (getline l < f)) > 0) { curln++; if (!infence(l)) scan(l, d, "chk") }
      close(f)
    }
    for (i = 1; i <= nlinks; i++) {
      if ((getline junk < qf[i]) < 0) { printf "dead link: %s -> %s\n", qs[i], qt[i] > "/dev/stderr"; bad++ }
      else close(qf[i])
    }
    printf "links checked: %d, dead: %d\n", nlinks, bad
    exit bad > 0
  }
}
