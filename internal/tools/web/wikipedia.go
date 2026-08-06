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

// `web.wikipedia` is a SEPARATE tool rather than a web.search backend.
//
// The two answer different questions. web.search asks the open web; this
// asks an encyclopedia. Folding Wikipedia in behind SearchProvider would
// make the choice ours (a config pin, decided once, at startup) when it
// is properly the model's, per call: "I want an encyclopedic source for
// this" is a judgement about the QUESTION, and the model is the only
// participant holding it. Two tools cost one extra spec block in the
// request prefix and buy the model an actual choice.
//
// # Published obligations (AGENTS.md free-API citizenship, consequence 1)
//
// Wikimedia's User-Agent policy REQUIRES every request to carry a
// descriptive User-Agent WITH contact information (a mail address or a
// contact page); it is enforced, and anonymous or default agents are
// rate-limited or refused outright. The REST API asks for serial
// requests rather than a request burst; it states no minimum interval.
// The service is donor-funded, which is why the contact requirement is
// implemented as a REFUSAL rather than a best effort: personant does not
// send anonymous traffic to a charity's servers.

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

// wikimediaPolicy names the obligation in the refusal the model reads.
// Shared with web.wikidata: one family, one policy, one sentence.
const wikimediaPolicy = "User-Agent policy"

const (
	// wikipediaEndpoint is the REST search-page endpoint. Query
	// parameters are attached with url.Values, never by string formatting
	// — a search term is arbitrary user/model text and hand-built query
	// strings are how it stops being a search term.
	//
	// The path is identical on every ENCYCLOPEDIA in the family, so a
	// language change is the host swap described above. It is NOT shared
	// with web.wikidata: the Wikidata wiki answers this endpoint with bare
	// QIDs and no labels (see wikidata.go), so that tool uses the Action
	// API instead.
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
)

// WikipediaConfig configures the tool. The zero value is valid and yields
// the live endpoint, a default client and the default local caps — but
// NOT a runnable tool: without Contact the handler refuses, per the
// Wikimedia policy above.
type WikipediaConfig struct {
	// Contact is the config.toml `[user]` identity. REQUIRED in practice:
	// absent, every call fails with contactRequiredError.
	Contact Contact

	// Endpoint overrides the REST endpoint. "" → wikipediaEndpoint. It is
	// a field rather than a package variable so a test can point at an
	// httptest server without mutating global state.
	Endpoint string

	// Client is the HTTP client. nil → a client with apiQueryTimeout.
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
	"short description, a matching excerpt and its canonical URL. This wins for encyclopedic PROSE context — " +
	"established topics, definitions, people, places, events — where you want an article to read. " +
	"For structured entity facts (identifiers, dates, relations) see web.wikidata; for the open web see web.search. " +
	"Use web.fetch on any result URL to read that article in full."

// NewWikipediaTool builds the `web.wikipedia` tool. It never fails at
// CONSTRUCTION: like web.fetch and unlike web.search it needs no
// credential, so it registers on every session. Missing contact
// information is an INVOCATION-time refusal instead, so the model is told
// why the encyclopedia is unavailable rather than silently never seeing
// the tool.
func NewWikipediaTool(cfg WikipediaConfig) tools.Tool {
	w := &wikipedian{
		endpoint: cfg.Endpoint,
		contact:  cfg.Contact,
		quota:    newQuota(ToolNameWikipedia, cfg.MaxPerTurn, cfg.MaxPerDay),
	}
	if w.endpoint == "" {
		w.endpoint = wikipediaEndpoint
	}
	w.polite = newPoliteness(ToolNameWikipedia, cfg.Contact, apiClient(cfg.Client))
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

// apiClient is the default client for the credential-free query tools.
func apiClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: apiQueryTimeout}
}

type wikipedian struct {
	endpoint string
	contact  Contact
	polite   *politeness
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

// wikipediaWhat names the operation in every message the model reads.
const wikipediaWhat = "the Wikipedia search"

func (w *wikipedian) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var a struct {
		Q     string `json:"q"`
		Limit int    `json:"limit"`
	}
	if err := tools.DecodeArgs(args, &a); err != nil {
		return nil, err
	}
	// The contact gate comes FIRST — before argument validation and before
	// the quota. An unconfigured contact makes the tool unusable whatever
	// the arguments are, so reporting an argument problem instead would
	// send the model off fixing the wrong thing; and spending the turn's
	// allowance on a permanent configuration condition teaches nothing.
	if !w.contact.Present() {
		return nil, contactRequiredError(ToolNameWikipedia, "Wikimedia", wikimediaPolicy)
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

	pages, err := w.query(ctx, q, clampLimit(a.Limit, DefaultWikipediaResults, MaxWikipediaResults))
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return []byte(noWikipediaResultsMessage(q)), nil
	}
	return renderWikipediaPages(q, pages), nil
}

func (w *wikipedian) query(ctx context.Context, q string, limit int) ([]wikipediaPage, error) {
	resp, err := w.polite.getAPI(ctx, w.endpoint, url.Values{
		"q":     {q},
		"limit": {strconv.Itoa(limit)},
	}, "application/json")
	if err != nil {
		return nil, transportError(wikipediaWhat, err)
	}
	if !resp.ok() {
		return nil, apiStatusError(wikipediaWhat, resp.Status, resp.Body)
	}

	var decoded wikipediaResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return nil, malformedError(wikipediaWhat, len(resp.Body))
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
	entries := make([]resultEntry, 0, len(pages))
	for _, p := range pages {
		entries = append(entries, resultEntry{
			Title: p.Title,
			URL:   wikipediaArticleBase + p.Key,
			Lines: []string{snippetLine(p.Description), snippetLine(p.Excerpt)},
		})
	}
	return renderList(
		fmt.Sprintf("%d Wikipedia article(s) for %q, most relevant first:\n", len(pages), q),
		entries,
		"\nUse web.fetch on a URL above to read the full article.\n",
	)
}
