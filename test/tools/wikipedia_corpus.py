#!/usr/bin/env python3
"""Wikipedia recall-fidelity corpus miner (Phase C.4).

Reads a hand-curated list of Wikipedia articles, fetches each one's
plain-text extract, splits it into section-level fragments, labels each
fragment by source article + section + domain, and vendors the result
as a single ``corpus.json`` under the Go test tree.

Design context — this is a *mining* operation, not a reproducible
generative step. Wikipedia is a rich but moving source: an extract
fetched today differs from one fetched next year. The output corpus is
therefore committed to git as a deliberate snapshot (unlike the
recall-madlibs ``queries.json``, which is derived and regenerable). The
canonical-reproducible part is *this script plus the article list* — if
the corpus must be refreshed or extended, the inputs are mechanical.

The corpus is the lexically-fixed *stored* side of the recall-fidelity
measurement (spec §9.6): each article is a topic cluster, each fragment
a unit of stored thread content with a deterministic ground-truth
label. The drifty *query* side is authored separately (C.5, the
LLM-assisted mad-libs templates).

Stdlib only (AGENTS.md auxiliary-script constraint): urllib for HTTP,
no requests, no venv. Polite by construction — descriptive User-Agent,
inter-request delay, redirect following, missing-page skip.

Usage:
    python3 test/tools/wikipedia_corpus.py            # full fetch
    python3 test/tools/wikipedia_corpus.py --limit 5  # smoke test
"""

import argparse
import json
import pathlib
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

SCRIPT_DIR = pathlib.Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent.parent
DEFAULT_LIST = SCRIPT_DIR / "wikipedia_corpus_articles.txt"
DEFAULT_OUT = REPO_ROOT / "internal" / "scenarios" / "testdata" / "corpus" / "corpus.json"

API_ENDPOINT = "https://en.wikipedia.org/w/api.php"
# Wikipedia asks API clients to identify themselves; an anonymous or
# generic UA risks being throttled or blocked.
USER_AGENT = (
    "personant-recall-corpus/0.1 "
    "(https://github.com/personant; recall-fidelity test corpus miner)"
)

# Section headings that carry no topical content — dropped during
# chunking so they cannot become spurious thread fragments.
SKIP_SECTIONS = {
    "references", "external links", "see also", "further reading",
    "notes", "bibliography", "citations", "sources", "footnotes",
    "notes and references", "explanatory notes",
}

# A plain-text section heading line from exsectionformat=wiki, e.g.
# "== History ==" or "=== Early work ===".
HEADING_RE = re.compile(r"^(=+)\s*(.+?)\s*\1$")


def parse_article_list(path):
    """Parse the curated seed list into (domain, title) pairs.

    File format: ``## domain: <name>`` starts a domain section; every
    article line until the next domain line inherits it. Article lines
    are a full ``/wiki/<title>`` URL or a bare title. Blank lines and
    ``#`` comments are ignored.
    """
    entries = []
    domain = "uncategorized"
    for lineno, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line:
            continue
        if line.lower().startswith("## domain:"):
            domain = line.split(":", 1)[1].strip()
            continue
        if line.startswith("#"):
            continue
        title = url_to_title(line)
        if not title:
            print(f"  warning: line {lineno}: cannot parse {line!r}", file=sys.stderr)
            continue
        entries.append((domain, title))
    return entries


def url_to_title(line):
    """Extract a Wikipedia article title from a URL or bare title."""
    if "/wiki/" in line:
        slug = line.split("/wiki/", 1)[1]
        slug = slug.split("#", 1)[0].split("?", 1)[0]
        return urllib.parse.unquote(slug).replace("_", " ").strip()
    return line.strip()


def fetch_extract(title, delay, retries=1):
    """Fetch one article's plain-text extract with wiki section markers.

    Returns the extract string, or None when the page is missing or the
    request fails after retries.
    """
    params = {
        "action": "query",
        "format": "json",
        "formatversion": "2",
        "prop": "extracts",
        "explaintext": "1",
        "exsectionformat": "wiki",
        "redirects": "1",
        "titles": title,
    }
    url = API_ENDPOINT + "?" + urllib.parse.urlencode(params)
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})

    for attempt in range(retries + 1):
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                payload = json.loads(resp.read().decode("utf-8"))
            break
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as exc:
            if attempt < retries:
                time.sleep(delay * 2)
                continue
            print(f"  warning: {title!r}: request failed: {exc}", file=sys.stderr)
            return None

    pages = payload.get("query", {}).get("pages", [])
    if not pages:
        print(f"  warning: {title!r}: no page in response", file=sys.stderr)
        return None
    page = pages[0]
    if page.get("missing"):
        print(f"  warning: {title!r}: page does not exist — skipped", file=sys.stderr)
        return None
    return page.get("extract", "")


def chunk_into_fragments(extract, min_words):
    """Split a plain-text extract into labelled section fragments.

    Text before the first heading becomes the "Introduction" fragment.
    Boilerplate sections (references etc.) and fragments below
    min_words are dropped. Subsection text is folded into its parent
    section so a fragment is a meaningful unit of stored content.
    """
    fragments = []
    current = "Introduction"
    buf = []

    def flush():
        text = "\n".join(buf).strip()
        if text and current.lower() not in SKIP_SECTIONS and len(text.split()) >= min_words:
            fragments.append({"section": current, "text": text})

    for line in extract.splitlines():
        m = HEADING_RE.match(line.strip())
        if m and len(m.group(1)) == 2:
            # Top-level heading → new fragment boundary. Deeper
            # headings (===, ====) fold into the current fragment.
            flush()
            current = m.group(2).strip()
            buf = []
        elif m:
            # Keep the subsection heading text as an inline cue.
            buf.append(m.group(2).strip())
        else:
            buf.append(line)
    flush()
    return fragments


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--list", type=pathlib.Path, default=DEFAULT_LIST)
    parser.add_argument("--out", type=pathlib.Path, default=DEFAULT_OUT)
    parser.add_argument(
        "--limit", type=int, default=0,
        help="fetch only the first N articles (0 = all); for smoke tests",
    )
    parser.add_argument(
        "--delay", type=float, default=0.5,
        help="seconds between requests — be polite to the API",
    )
    parser.add_argument(
        "--min-words", type=int, default=30,
        help="drop section fragments shorter than this",
    )
    args = parser.parse_args(argv)

    if not args.list.exists():
        print(f"article list not found: {args.list}", file=sys.stderr)
        return 1
    entries = parse_article_list(args.list)
    if args.limit > 0:
        entries = entries[: args.limit]
    if not entries:
        print("no articles to fetch", file=sys.stderr)
        return 1

    print(f"fetching {len(entries)} articles from {API_ENDPOINT}")
    articles = []
    total_fragments = 0
    skipped = 0
    for i, (domain, title) in enumerate(entries, 1):
        print(f"  [{i}/{len(entries)}] {domain} / {title}")
        extract = fetch_extract(title, args.delay)
        time.sleep(args.delay)
        if extract is None:
            skipped += 1
            continue
        fragments = chunk_into_fragments(extract, args.min_words)
        if not fragments:
            print(f"  warning: {title!r}: no usable fragments — skipped", file=sys.stderr)
            skipped += 1
            continue
        articles.append(
            {
                "title": title,
                "url": "https://en.wikipedia.org/wiki/"
                + urllib.parse.quote(title.replace(" ", "_")),
                "domain": domain,
                "fragments": fragments,
            }
        )
        total_fragments += len(fragments)

    doc = {
        "_comment": "MINED SNAPSHOT - Wikipedia extracts fetched by "
        "test/tools/wikipedia_corpus.py. Committed deliberately: the "
        "source is non-reproducible (articles change). Refresh by "
        "editing wikipedia_corpus_articles.txt and re-running.",
        "source": "en.wikipedia.org",
        "fetched_at": time.strftime("%Y-%m-%d"),
        "article_count": len(articles),
        "fragment_count": total_fragments,
        "articles": articles,
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(
        json.dumps(doc, indent=2, sort_keys=True, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    print(
        f"wrote {args.out} - {len(articles)} articles, "
        f"{total_fragments} fragments, {skipped} skipped"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
