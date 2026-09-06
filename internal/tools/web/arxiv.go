package web

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"personant/internal/model"
	"personant/internal/tools"
)

// `web.arxiv` — the preprint half of the scholarly receipt trio.
//
// # Published obligations (CONVENTIONS.md free-API citizenship, consequence 1)
//
// arXiv's API manual asks callers to make no more than one request every
// THREE SECONDS and to make requests serially rather than in parallel.
// That is the only hard rate statement; the terms of use additionally ask
// for a User-Agent identifying the client so the operators can contact a
// misbehaving one. arXiv is a Cornell-hosted, donation-funded service —
// hence the 3s entry in the politeness table's hostSpacing, which is the
// only per-service spacing in the package. Contact is asked for, not
// required, so an unconfigured `[user]` gets the identity-only UA and the
// tool still runs.

// ToolNameArxiv is the registry key for the §6.1.6 arXiv search tool.
const ToolNameArxiv = "web.arxiv"

// ArxivHost is the API host. The export mirror is the documented API
// endpoint (arxiv.org itself serves the web site).
const ArxivHost = "export.arxiv.org"

const (
	// arxivEndpoint is the Atom query API. It is HTTP rather than HTTPS
	// because that is the endpoint arXiv documents and serves; the payload
	// is public bibliographic metadata and carries no credential.
	arxivEndpoint = "http://" + ArxivHost + "/api/query"

	// arxivMinSpacing is arXiv's stated etiquette: ~3 seconds between
	// requests. Enforced by the per-host gate, not here.
	arxivMinSpacing = 3 * time.Second

	// DefaultArxivResults / MaxArxivResults bound `max_results`, matching
	// web.wikipedia's shortlist reasoning.
	DefaultArxivResults = 5
	MaxArxivResults     = 10

	// arxivMaxAuthors is how many author names are listed before the
	// "et al." A citation needs enough to be recognizable, not the whole
	// collaboration — a particle-physics paper carries three thousand.
	arxivMaxAuthors = 3

	// arxivAbstractChars bounds the rendered abstract. Longer than the
	// generic snippet: an abstract IS the deciding artifact for a paper,
	// where a search snippet is only a hint toward one.
	arxivAbstractChars = 700
)

const arxivWhat = "the arXiv search"

// ArxivConfig configures the tool. The zero value is valid and yields the
// live endpoint, a default client, the default caps, and the
// identity-only User-Agent.
type ArxivConfig struct {
	// Contact is the config.toml `[user]` identity. Optional here —
	// arXiv asks for contact, it does not require it — but supplied on
	// every real session.
	Contact Contact

	// Endpoint overrides the query API. "" → arxivEndpoint.
	Endpoint string

	// Client is the HTTP client. nil → a client with apiQueryTimeout.
	Client *http.Client

	// MaxPerTurn / MaxPerDay are the LOCAL query caps. <= 0 → the
	// Default* constants in quota.go.
	MaxPerTurn int
	MaxPerDay  int
}

var arxivParams = json.RawMessage(`{
  "type": "object",
  "properties": {
    "q": {
      "type": "string",
      "description": "Search terms. Plain words search title, abstract and authors; arXiv's field prefixes (ti:, au:, cat:, abs:) and AND/OR/ANDNOT also work."
    },
    "limit": {
      "type": "integer",
      "description": "How many papers to return. Default 5, maximum 10; out-of-range values are clamped, never rejected."
    }
  },
  "required": ["q"],
  "additionalProperties": false
}`)

const arxivDescription = "Search arXiv for papers, returning each paper's title, authors, abstract, arXiv id, " +
	"submission date and abstract-page URL. This wins for PREPRINTS and recent scholarship — physics, mathematics, " +
	"computer science, quantitative biology and finance, statistics — including work posted in the last days that " +
	"has not been published anywhere yet. For resolving a published paper's DOI, venue and citation record see " +
	"web.crossref. Use web.fetch on a result URL to read the abstract page."

// NewArxivTool builds the `web.arxiv` tool. Like web.fetch and
// web.wikipedia it needs no credential and never fails at construction.
func NewArxivTool(cfg ArxivConfig) tools.Tool {
	a := &arxivSearcher{
		endpoint: cfg.Endpoint,
		polite:   newPoliteness(ToolNameArxiv, cfg.Contact, apiClient(cfg.Client)),
		quota:    newQuota(ToolNameArxiv, cfg.MaxPerTurn, cfg.MaxPerDay),
	}
	if a.endpoint == "" {
		a.endpoint = arxivEndpoint
	}
	return tools.Tool{
		Spec: model.ToolSpec{
			Name:        ToolNameArxiv,
			Description: arxivDescription,
			Parameters:  arxivParams,
		},
		Handler: a.handle,
		Tier:    tools.TierSilent,
		Mutates: false,
	}
}

type arxivSearcher struct {
	endpoint string
	polite   *politeness
	quota    *quota
}

// The Atom 1.0 feed arXiv returns. Only the fields that reach the model
// are declared; encoding/xml ignores the rest, which is the right default
// for a schema somebody else owns.
type arxivFeed struct {
	XMLName xml.Name     `xml:"feed"`
	Entries []arxivEntry `xml:"entry"`
}

type arxivEntry struct {
	ID        string        `xml:"id"`
	Title     string        `xml:"title"`
	Summary   string        `xml:"summary"`
	Published string        `xml:"published"`
	Authors   []arxivAuthor `xml:"author"`
}

type arxivAuthor struct {
	Name string `xml:"name"`
}

func (a *arxivSearcher) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var in struct {
		Q     string `json:"q"`
		Limit int    `json:"limit"`
	}
	if err := tools.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(in.Q)
	if q == "" {
		return nil, fmt.Errorf("no search term given: call %s with {\"q\": \"…\"}", ToolNameArxiv)
	}
	if err := a.quota.take(ctx); err != nil {
		return nil, err
	}

	entries, err := a.query(ctx, q, clampLimit(in.Limit, DefaultArxivResults, MaxArxivResults))
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return []byte(noResultsFor("The arXiv search", q,
			"Try broader terms, an author surname, or web.crossref for published work that never appeared as a preprint.")), nil
	}
	return renderArxivEntries(q, entries), nil
}

func (a *arxivSearcher) query(ctx context.Context, q string, limit int) ([]arxivEntry, error) {
	resp, err := a.polite.getAPI(ctx, a.endpoint, url.Values{
		"search_query": {q},
		"max_results":  {strconv.Itoa(limit)},
	}, "application/atom+xml")
	if err != nil {
		return nil, transportError(arxivWhat, err)
	}
	if !resp.ok() {
		return nil, apiStatusError(arxivWhat, resp.Status, resp.Body)
	}

	var feed arxivFeed
	if err := xml.Unmarshal(resp.Body, &feed); err != nil {
		return nil, malformedError(arxivWhat, len(resp.Body))
	}
	out := make([]arxivEntry, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		if arxivID(e.ID) == "" {
			continue // no id means no citable reference, which means not actionable
		}
		out = append(out, e)
	}
	return out, nil
}

// arxivID reduces the Atom `id` (an absolute abs-page URL) to the bare
// versioned identifier, e.g. `2401.01234v2`. A shape we do not recognize
// yields "", which drops the entry rather than rendering a half-parsed
// citation.
func arxivID(raw string) string {
	s := strings.TrimSpace(raw)
	i := strings.LastIndex(s, "/abs/")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(s[i+len("/abs/"):])
}

// arxivAbsURL is the canonical abstract page for an id. Concatenated, not
// re-encoded: an arXiv id is already URL-safe by construction.
func arxivAbsURL(id string) string { return "https://arxiv.org/abs/" + id }

// arxivAuthorList renders the first few authors with an honest "et al.".
func arxivAuthorList(authors []arxivAuthor) string {
	names := make([]string, 0, arxivMaxAuthors)
	for _, a := range authors {
		if n := tools.OneLine(a.Name); n != "" {
			names = append(names, n)
		}
		if len(names) == arxivMaxAuthors {
			break
		}
	}
	if len(names) == 0 {
		return ""
	}
	joined := strings.Join(names, ", ")
	if len(authors) > len(names) {
		joined += ", et al."
	}
	return joined
}

// arxivDate keeps the calendar date and drops the time. A preprint's
// submission timestamp to the second is precision nobody asked for; the
// day is what "how recent is this?" needs.
func arxivDate(published string) string {
	s := strings.TrimSpace(published)
	if len(s) >= len("2006-01-02") {
		return s[:len("2006-01-02")]
	}
	return s
}

func renderArxivEntries(q string, entries []arxivEntry) []byte {
	out := make([]resultEntry, 0, len(entries))
	for _, e := range entries {
		id := arxivID(e.ID)
		meta := "arXiv:" + id
		if d := arxivDate(e.Published); d != "" {
			meta += " · submitted " + d
		}
		out = append(out, resultEntry{
			Title: e.Title,
			URL:   arxivAbsURL(id),
			Lines: []string{
				meta,
				arxivAuthorList(e.Authors),
				clip(tools.OneLine(e.Summary), arxivAbstractChars),
			},
		})
	}
	return renderList(
		fmt.Sprintf("%d arXiv paper(s) for %q, most relevant first:\n", len(entries), q),
		out,
		"\nUse web.fetch on a URL above to read the abstract page.\n",
	)
}
