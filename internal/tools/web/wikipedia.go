package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"personant/internal/model"
	"personant/internal/tools"
)

// `web.wikipedia` is a SEPARATE tool rather than a web.search backend.
//
// The two answer different questions. web.search asks the open web; this
// asks an encyclopedia. Folding Wikipedia in behind SearchProvider would
// make the choice ours (a config pin, decided once, at startup) when it
// is properly the model's, per call: "I want an encyclopedic source for
// this" is a judgement about the QUESTION, and the model is the only
// participant holding it. Two tools cost one extra spec block in the
// request prefix and buy the model an actual choice.

// ToolNameWikipedia is the registry key for the §6.1.1 Wikipedia search
// tool.
const ToolNameWikipedia = "web.wikipedia"

// WikipediaHost is the language edition queried.
//
// Another language is a HOST SWAP and nothing else — the REST path,
// the response shape and the `/wiki/<key>` article form are identical on
// `de.wikipedia.org`, `ja.wikipedia.org` and every other edition. When a
// language choice arrives it belongs here (one config value → one host
// string), not in a second endpoint or a second tool.
const WikipediaHost = "en.wikipedia.org"

const (
	// wikipediaEndpoint is the REST search-page endpoint. Query
	// parameters (`q`, `limit`) are attached with url.Values, never by
	// string formatting — a search term is arbitrary user/model text and
	// hand-built query strings are how it stops being a search term.
	wikipediaEndpoint = "https://" + WikipediaHost + "/w/rest.php/v1/search/page"

	// wikipediaArticleBase + a page's `key` is the canonical article URL.
	// The key arrives already URL-safe (`Go_(programming_language)`), so
	// it is concatenated, NOT re-encoded — escaping it again would turn
	// every parenthesis and apostrophe into a percent triple and produce
	// a URL that still resolves but no longer matches what a human, or a
	// later `web.fetch`, would write.
	wikipediaArticleBase = "https://" + WikipediaHost + "/wiki/"

	// DefaultWikipediaResults is the `limit` used when the model asks for
	// no particular count.
	DefaultWikipediaResults = 5

	// MaxWikipediaResults caps `limit`. Above this the list stops being a
	// shortlist to choose a web.fetch from and starts being the §6.5
	// budget's problem.
	MaxWikipediaResults = 10

	// wikipediaTimeout bounds one query, matching exaTimeout: under
	// DefaultFetchTimeout and well under tools.DefaultTimeout, so a slow
	// endpoint surfaces as this tool's own message rather than dispatch's
	// generic one.
	wikipediaTimeout = 15 * time.Second

	// wikipediaMaxResponseBytes caps the response read. A search API
	// returning megabytes is a malfunction; buffering it unbounded would
	// make that malfunction ours.
	wikipediaMaxResponseBytes = 4 << 20

	// wikipediaErrorSnippetChars bounds the body echoed back on a non-200.
	// See the comment at wikipediaStatusError for why a snippet is
	// included here when the Exa provider deliberately includes none.
	wikipediaErrorSnippetChars = 200
)

// WikipediaConfig configures the tool. The zero value is valid and yields
// the live endpoint, http.DefaultClient and the default local caps.
type WikipediaConfig struct {
	// Endpoint overrides the REST endpoint. "" → wikipediaEndpoint. It is
	// a field rather than a package variable so a test can point at an
	// httptest server without mutating global state.
	Endpoint string

	// Client is the HTTP client. nil → a client with wikipediaTimeout.
	Client *http.Client

	// MaxPerTurn / MaxPerDay are the LOCAL query caps. <= 0 → the
	// Default* constants in quota.go — the same allowance web.search
	// gets. Wikipedia needs no credential and meters nothing, but an
	// unbounded tool still invites a retry loop, and the loop is what the
	// cap is for.
	MaxPerTurn int
	MaxPerDay  int
}

var wikipediaParams = json.RawMessage(`{
  "type": "object",
  "properties": {
    "q": {
      "type": "string",
      "description": "Search term. Article titles and topic words work best; this is an encyclopedia index, not a web search engine."
    },
    "limit": {
      "type": "integer",
      "description": "How many results to return. Default 5, maximum 10; out-of-range values are clamped, never rejected."
    }
  },
  "required": ["q"],
  "additionalProperties": false
}`)

const wikipediaDescription = "Search English Wikipedia for articles matching a term, returning each article's title, " +
	"short description, a matching excerpt and its canonical URL. Use this when an encyclopedic source is what you " +
	"want — established topics, definitions, people, places, events — and web.search when you want the open web. " +
	"Use web.fetch on any result URL to read that article in full."

// NewWikipediaTool builds the `web.wikipedia` tool. It never fails: like
// web.fetch and unlike web.search, it needs no credential and no
// configured backend, so it registers on every session.
func NewWikipediaTool(cfg WikipediaConfig) tools.Tool {
	w := &wikipedian{
		endpoint: cfg.Endpoint,
		client:   cfg.Client,
		quota:    newQuota(ToolNameWikipedia, cfg.MaxPerTurn, cfg.MaxPerDay),
	}
	if w.endpoint == "" {
		w.endpoint = wikipediaEndpoint
	}
	if w.client == nil {
		w.client = &http.Client{Timeout: wikipediaTimeout}
	}
	return tools.Tool{
		Spec: model.ToolSpec{
			Name:        ToolNameWikipedia,
			Description: wikipediaDescription,
			Parameters:  wikipediaParams,
		},
		Handler: w.handle,
		Tier:    tools.TierSilent,
		Mutates: false,
	}
}

type wikipedian struct {
	endpoint string
	client   *http.Client
	quota    *quota
}

// wikipediaPage is one entry of the REST response's `pages` array. The
// fields not read here (`id`, `matched_title`, `thumbnail`) are omitted
// deliberately: an unused field is a field a reader has to check the
// purpose of.
//
// `description` and `excerpt` are nullable in the API. A JSON `null`
// unmarshals into a string field as a no-op, leaving "", which is exactly
// the rendering decision ("skip the line") already wanted.
type wikipediaPage struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Excerpt     string `json:"excerpt"`
	Description string `json:"description"`
}

type wikipediaResponse struct {
	Pages []wikipediaPage `json:"pages"`
}

func (w *wikipedian) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var a struct {
		Q     string `json:"q"`
		Limit int    `json:"limit"`
	}
	if err := tools.DecodeArgs(args, &a); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(a.Q)
	if q == "" {
		return nil, fmt.Errorf("no search term given: call %s with {\"q\": \"…\"}", ToolNameWikipedia)
	}

	// The cap is taken BEFORE the call, so it bounds ATTEMPTS rather than
	// successes — same reasoning as web.search, and the same mechanism.
	if err := w.quota.take(ctx); err != nil {
		return nil, err
	}

	pages, err := w.query(ctx, q, wikipediaLimit(a.Limit))
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return []byte(noWikipediaResultsMessage(q)), nil
	}
	return renderWikipediaPages(q, pages), nil
}

// wikipediaLimit resolves the requested count. Out-of-range is CLAMPED,
// never an error: a limit is a preference, and failing a call over one
// spends a tool round to learn something the clamp already decided.
//
// Non-positive means "unspecified". JSON omission and an explicit `0` are
// indistinguishable in a plain int, and the rest of this package already
// reads a non-positive bound as "use the default" — one convention beats
// a *int that exists only to tell two identical intentions apart.
func wikipediaLimit(n int) int {
	if n <= 0 {
		return DefaultWikipediaResults
	}
	return min(n, MaxWikipediaResults)
}

func (w *wikipedian) query(ctx context.Context, q string, limit int) ([]wikipediaPage, error) {
	u, err := url.Parse(w.endpoint)
	if err != nil {
		return nil, fmt.Errorf("%s: endpoint %q is not parseable: %w", ToolNameWikipedia, w.endpoint, err)
	}
	u.RawQuery = url.Values{
		"q":     {q},
		"limit": {strconv.Itoa(limit)},
	}.Encode()

	ctx, cancel := context.WithTimeout(ctx, wikipediaTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", ToolNameWikipedia, err)
	}
	// Wikimedia's API etiquette asks for a descriptive User-Agent that
	// identifies the client; an anonymous or default one is rate-limited
	// or refused outright.
	req.Header.Set("User-Agent", wikipediaUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the Wikipedia search could not be performed (%w) — NO search happened, so this says "+
			"nothing about whether an article exists. Answer from what you have and say the lookup was unavailable", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, wikipediaMaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("%s: read response: %w", ToolNameWikipedia, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, wikipediaStatusError(resp.Status, raw)
	}

	var decoded wikipediaResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: malformed response body (%d bytes) — the search may not have run",
			ToolNameWikipedia, len(raw))
	}
	out := make([]wikipediaPage, 0, len(decoded.Pages))
	for _, p := range decoded.Pages {
		if strings.TrimSpace(p.Key) == "" {
			continue // no key means no article URL, which means not actionable
		}
		out = append(out, p)
	}
	return out, nil
}

// wikipediaStatusError reports a non-200 with the status AND a bounded
// snippet of the body.
//
// The Exa provider deliberately echoes NO body, and the reason is
// §8.2.1: its request carries an API key, and an error echo is a
// documented way for a submitted credential to come back out and land in
// model context and the event log. This request carries no credential —
// no key, no cookie, no auth header is ever sent to this endpoint — so
// that hazard does not exist here, while the body ("invalid limit",
// "unknown parameter") is genuinely the actionable half of the error. The
// snippet is collapsed to one line and clipped, so a long HTML error page
// cannot become the tool result.
func wikipediaStatusError(status string, body []byte) error {
	detail := clip(tools.OneLine(string(body)), wikipediaErrorSnippetChars)
	if detail == "" {
		return fmt.Errorf("the Wikipedia search returned HTTP %s and NO search happened", status)
	}
	return fmt.Errorf("the Wikipedia search returned HTTP %s and NO search happened: %s", status, detail)
}

// noWikipediaResultsMessage is the EMPTY half of web.search's trichotomy,
// held to here for the same reason: an empty result and a failed lookup
// must be distinguishable by the model from the text alone, or it learns
// that the topic does not exist and says so with confidence.
func noWikipediaResultsMessage(q string) string {
	return fmt.Sprintf("The Wikipedia search ran successfully and matched 0 articles for %q.\n\n"+
		"This is an EMPTY RESULT, not a failure: the query reached Wikipedia and its index returned nothing. "+
		"Try the topic's common name or a broader term, or use web.search for non-encyclopedic sources.\n", q)
}

func renderWikipediaPages(q string, pages []wikipediaPage) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%d Wikipedia article(s) for %q, most relevant first:\n", len(pages), q)
	for i, p := range pages {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1,
			tools.OneLine(fallback(p.Title, "(untitled)")), wikipediaArticleBase+p.Key)
		if desc := clip(tools.OneLine(stripHTML(p.Description)), snippetMaxChars); desc != "" {
			fmt.Fprintf(&b, "   %s\n", desc)
		}
		if excerpt := clip(tools.OneLine(stripHTML(p.Excerpt)), snippetMaxChars); excerpt != "" {
			fmt.Fprintf(&b, "   %s\n", excerpt)
		}
	}
	b.WriteString("\nUse web.fetch on a URL above to read the full article.\n")
	return []byte(b.String())
}

// stripHTML reduces an HTML fragment to its text.
//
// The REST API wraps matched terms in `<span class="searchmatch">…</span>`
// and escapes the surrounding text, so an excerpt reaches us as markup
// even though it is prose. Tokenizing is the boring correct way to undo
// both at once: the tokenizer's text tokens are already entity-decoded,
// so tag removal and unescaping are one pass rather than a regex plus a
// separate html.UnescapeString (which, run in the wrong order, happily
// turns `&lt;script&gt;` back into a tag).
func stripHTML(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			// The only terminal token: io.EOF for a complete fragment, and
			// for a malformed one the text seen so far is still the best
			// answer available.
			return b.String()
		case html.TextToken:
			b.Write(z.Text())
		}
	}
}
