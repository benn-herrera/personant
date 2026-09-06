package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"personant/internal/model"
	"personant/internal/tools"
)

// `web.crossref` — the published-record half of the scholarly receipt
// trio.
//
// # Published obligations (CONVENTIONS.md free-API citizenship, consequence 1)
//
// Crossref's REST API is free and open with no key. Its etiquette asks
// callers to identify themselves with a mail address — either as the
// `mailto` query parameter or inside the User-Agent — which routes the
// request to the POLITE POOL, a set of machines with better and more
// predictable service than the anonymous pool. Crossref publishes no
// fixed rate limit; it asks callers to honour the `X-Rate-Limit-*` and
// `Retry-After` headers it may send and to keep requests serial rather
// than parallel. Contact is strongly preferred but not required, so an
// unconfigured `[user]` still runs — in the anonymous pool, with the
// identity-only User-Agent, which is the honest consequence rather than a
// fabricated address.

// ToolNameCrossref is the registry key for the §6.1.7 Crossref tool.
const ToolNameCrossref = "web.crossref"

// CrossrefHost is the REST API host.
const CrossrefHost = "api.crossref.org"

const (
	crossrefEndpoint = "https://" + CrossrefHost + "/works"

	// doiResolver + a DOI is the canonical resolvable URL for a work.
	doiResolver = "https://doi.org/"

	// DefaultCrossrefResults / MaxCrossrefResults bound `rows`.
	DefaultCrossrefResults = 5
	MaxCrossrefResults     = 10

	// crossrefMaxAuthors matches arXiv's: enough to recognize the work.
	crossrefMaxAuthors = 3
)

const crossrefWhat = "the Crossref search"

// CrossrefConfig configures the tool. The zero value is valid and yields
// the live endpoint, a default client, the default caps, and the
// identity-only (anonymous-pool) User-Agent.
type CrossrefConfig struct {
	// Contact is the config.toml `[user]` identity. Optional, but its
	// presence is what puts the request in Crossref's polite pool.
	Contact Contact

	// Endpoint overrides the works endpoint. "" → crossrefEndpoint.
	Endpoint string

	// Client is the HTTP client. nil → a client with apiQueryTimeout.
	Client *http.Client

	// MaxPerTurn / MaxPerDay are the LOCAL query caps. <= 0 → the
	// Default* constants in quota.go.
	MaxPerTurn int
	MaxPerDay  int
}

var crossrefParams = json.RawMessage(`{
  "type": "object",
  "properties": {
    "q": {
      "type": "string",
      "description": "What to look up: a title, an author plus a few title words, or a partial citation. Searching a bare DOI also works."
    },
    "limit": {
      "type": "integer",
      "description": "How many works to return. Default 5, maximum 10; out-of-range values are clamped, never rejected."
    }
  },
  "required": ["q"],
  "additionalProperties": false
}`)

const crossrefDescription = "Search Crossref's registry of scholarly works, returning each work's title, first " +
	"authors, journal or venue, year, DOI and resolvable doi.org URL. This wins for RESOLVING AND VERIFYING " +
	"citations — turning a half-remembered reference into a DOI, confirming a paper exists and was published " +
	"where you think, or finding the published version of a preprint. For preprints and work too recent to be " +
	"registered see web.arxiv. Use web.fetch on a doi.org URL to reach the publisher's page."

// NewCrossrefTool builds the `web.crossref` tool. No credential, so it
// never fails at construction and registers on every session.
func NewCrossrefTool(cfg CrossrefConfig) tools.Tool {
	c := &crossrefSearcher{
		endpoint: cfg.Endpoint,
		polite:   newPoliteness(ToolNameCrossref, cfg.Contact, apiClient(cfg.Client)),
		quota:    newQuota(ToolNameCrossref, cfg.MaxPerTurn, cfg.MaxPerDay),
	}
	if c.endpoint == "" {
		c.endpoint = crossrefEndpoint
	}
	return tools.Tool{
		Spec: model.ToolSpec{
			Name:        ToolNameCrossref,
			Description: crossrefDescription,
			Parameters:  crossrefParams,
		},
		Handler: c.handle,
		Tier:    tools.TierSilent,
		Mutates: false,
	}
}

type crossrefSearcher struct {
	endpoint string
	polite   *politeness
	quota    *quota
}

// The Crossref envelope. Only the fields that reach the model are
// declared.
//
// `title` and `container-title` are ARRAYS in Crossref's schema (a work
// can carry several), and both are routinely empty — a dataset or a
// component record has no title at all. First element or nothing.
type crossrefResponse struct {
	Message struct {
		Items []crossrefWork `json:"items"`
	} `json:"message"`
}

type crossrefWork struct {
	DOI            string           `json:"DOI"`
	Title          []string         `json:"title"`
	ContainerTitle []string         `json:"container-title"`
	Author         []crossrefAuthor `json:"author"`
	Issued         crossrefDate     `json:"issued"`
}

type crossrefAuthor struct {
	Given  string `json:"given"`
	Family string `json:"family"`
	Name   string `json:"name"` // organizations carry `name` instead
}

// crossrefDate is the `date-parts` shape: [[year, month, day]], with
// month and day optional and the whole thing occasionally [[null]] for a
// work with no known date.
type crossrefDate struct {
	DateParts [][]*int `json:"date-parts"`
}

// year returns the four-digit year, or "" when the record has none.
func (d crossrefDate) year() string {
	if len(d.DateParts) == 0 || len(d.DateParts[0]) == 0 || d.DateParts[0][0] == nil {
		return ""
	}
	return strconv.Itoa(*d.DateParts[0][0])
}

func (a crossrefAuthor) display() string {
	if n := tools.OneLine(a.Name); n != "" {
		return n
	}
	return tools.OneLine(strings.TrimSpace(a.Given + " " + a.Family))
}

func (a crossrefAuthor) empty() bool { return a.display() == "" }

func (c *crossrefSearcher) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var in struct {
		Q     string `json:"q"`
		Limit int    `json:"limit"`
	}
	if err := tools.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(in.Q)
	if q == "" {
		return nil, fmt.Errorf("no search term given: call %s with {\"q\": \"…\"}", ToolNameCrossref)
	}
	if err := c.quota.take(ctx); err != nil {
		return nil, err
	}

	works, err := c.query(ctx, q, clampLimit(in.Limit, DefaultCrossrefResults, MaxCrossrefResults))
	if err != nil {
		return nil, err
	}
	if len(works) == 0 {
		return []byte(noResultsFor("The Crossref search", q,
			"Try the exact title, or an author surname with two or three title words. "+
				"A work with no registered DOI will not be here — try web.arxiv for preprints.")), nil
	}
	return renderCrossrefWorks(q, works), nil
}

func (c *crossrefSearcher) query(ctx context.Context, q string, limit int) ([]crossrefWork, error) {
	resp, err := c.polite.getAPI(ctx, c.endpoint, url.Values{
		"query": {q},
		"rows":  {strconv.Itoa(limit)},
	}, "application/json")
	if err != nil {
		return nil, transportError(crossrefWhat, err)
	}
	if !resp.ok() {
		return nil, apiStatusError(crossrefWhat, resp.Status, resp.Body)
	}

	var decoded crossrefResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return nil, malformedError(crossrefWhat, len(resp.Body))
	}
	out := make([]crossrefWork, 0, len(decoded.Message.Items))
	for _, w := range decoded.Message.Items {
		if strings.TrimSpace(w.DOI) == "" {
			continue // the DOI is the whole point; without one there is nothing to cite
		}
		out = append(out, w)
	}
	return out, nil
}

// firstOf returns the first non-blank element, or "".
func firstOf(ss []string) string {
	for _, s := range ss {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}

func crossrefAuthorList(authors []crossrefAuthor) string {
	names := make([]string, 0, crossrefMaxAuthors)
	shown := 0
	total := 0
	for _, a := range authors {
		if a.empty() {
			continue
		}
		total++
		if shown < crossrefMaxAuthors {
			names = append(names, a.display())
			shown++
		}
	}
	if shown == 0 {
		return ""
	}
	joined := strings.Join(names, ", ")
	if total > shown {
		joined += ", et al."
	}
	return joined
}

// crossrefCitation is the venue-and-year line: the part of a citation a
// reader uses to tell two same-titled works apart.
func crossrefCitation(w crossrefWork) string {
	venue, year := firstOf(w.ContainerTitle), w.Issued.year()
	switch {
	case venue != "" && year != "":
		return tools.OneLine(venue) + " (" + year + ")"
	case venue != "":
		return tools.OneLine(venue)
	case year != "":
		return year
	default:
		return ""
	}
}

func renderCrossrefWorks(q string, works []crossrefWork) []byte {
	out := make([]resultEntry, 0, len(works))
	for _, w := range works {
		out = append(out, resultEntry{
			Title: firstOf(w.Title),
			URL:   doiResolver + w.DOI,
			Lines: []string{
				"DOI: " + w.DOI,
				crossrefAuthorList(w.Author),
				crossrefCitation(w),
			},
		})
	}
	return renderList(
		fmt.Sprintf("%d Crossref work(s) for %q, most relevant first:\n", len(works), q),
		out,
		"\nUse web.fetch on a doi.org URL above to reach the publisher's page.\n",
	)
}
