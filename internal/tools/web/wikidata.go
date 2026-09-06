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

// `web.wikidata` — the structured-facts half of the Wikimedia pair.
//
// # Why the Action API and not the REST search/page endpoint
//
// The first implementation used `/w/rest.php/v1/search/page`, the same
// path web.wikipedia uses, on the assumption that the Wikidata wiki would
// return a human label in `title` the way the other wikis in the family
// do. A LIVE CHECK disproved it: `q="general relativity"` comes back with
// `title` = `"Q11452"`, and the label appears NOWHERE in the payload (the
// excerpt carries description text instead). A shortlist of bare QIDs is
// not something a model or a human can choose from, which defeats the
// entire receipts purpose of the tool.
//
// `action=wbsearchentities` is Wikidata's own entity search and returns
// `id`, `label` and `description` as first-class fields. The cost is that
// this is the Action API rather than the REST one, so the two Wikimedia
// tools no longer share a decoder; the purchase is a result that says
// what the entity IS.
//
// # Published obligations (CONVENTIONS.md free-API citizenship, consequence 1)
//
// Wikidata is Wikimedia infrastructure and falls under the SAME
// User-Agent policy as Wikipedia: every request must carry a descriptive
// User-Agent WITH contact information, it is enforced, and anonymous or
// default agents are rate-limited or refused. Serial requests, no stated
// minimum interval. Donor-funded, so the contact requirement is a
// REFUSAL here exactly as in web.wikipedia — one family, one policy, one
// implementation.
//
// The Action API additionally offers `maxlag`, which asks the server to
// refuse a request outright while replication lag is high. NOTED AND
// DELIBERATELY NOT SENT: `maxlag` is the courtesy owed by BOTS running
// bulk or write traffic, and sending it converts a lagging cluster into a
// failed tool call for a human waiting on one read. This tool is
// read-only, interactive, and already bounded at five queries a turn —
// the load `maxlag` exists to shed is not load we generate.

// ToolNameWikidata is the registry key for the §6.1.8 Wikidata tool.
const ToolNameWikidata = "web.wikidata"

// WikidataHost is the API host.
const WikidataHost = "www.wikidata.org"

const (
	// wikidataEndpoint is the Action API. See the note above for why this
	// rather than the REST search/page path the Wikipedia tool uses.
	wikidataEndpoint = "https://" + WikidataHost + "/w/api.php"

	// wikidataEntityBase + a QID is the canonical entity URL. Built from
	// the `id` rather than taken from the response's own fields: `url` is
	// PROTOCOL-RELATIVE (`//www.wikidata.org/wiki/Q42`), which would reach
	// the model as something web.fetch cannot resolve, and `concepturi` is
	// the RDF entity IRI (`http://www.wikidata.org/entity/Q42`), which is
	// not the page a human would open.
	wikidataEntityBase = "https://" + WikidataHost + "/wiki/"

	// wikidataLanguage is the language the search term is interpreted in
	// and the labels come back in. One string, like WikipediaHost: another
	// language is a parameter swap and nothing else.
	wikidataLanguage = "en"

	DefaultWikidataResults = 5
	MaxWikidataResults     = 10
)

const wikidataWhat = "the Wikidata search"

// WikidataConfig configures the tool. The zero value is valid but not
// runnable: without Contact the handler refuses, per the Wikimedia policy
// above.
type WikidataConfig struct {
	// Contact is the config.toml `[user]` identity. REQUIRED in practice.
	Contact Contact

	// Endpoint overrides the API endpoint. "" → wikidataEndpoint.
	Endpoint string

	// Client is the HTTP client. nil → a client with apiQueryTimeout.
	Client *http.Client

	// MaxPerTurn / MaxPerDay are the LOCAL query caps. <= 0 → the
	// Default* constants in quota.go.
	MaxPerTurn int
	MaxPerDay  int
}

var wikidataParams = json.RawMessage(`{
  "type": "object",
  "properties": {
    "q": {
      "type": "string",
      "description": "The entity to find: a person, place, organization, work, species, chemical — any name or label. A QID (Q42) also resolves."
    },
    "limit": {
      "type": "integer",
      "description": "How many entities to return. Default 5, maximum 10; out-of-range values are clamped, never rejected."
    }
  },
  "required": ["q"],
  "additionalProperties": false
}`)

const wikidataDescription = "Search Wikidata for entities, returning each entity's label, one-line description, " +
	"QID and canonical URL. This wins for STRUCTURED ENTITY IDENTITY — pinning down which of several same-named " +
	"people, places or works is meant, and getting the stable QID that other datasets key on. For prose context " +
	"about a topic see web.wikipedia. Use web.fetch on a result URL to read the entity's full statement list."

// NewWikidataTool builds the `web.wikidata` tool. It never fails at
// CONSTRUCTION — no credential — but refuses at invocation without
// contact information, so the model is told why rather than never seeing
// the tool.
func NewWikidataTool(cfg WikidataConfig) tools.Tool {
	w := &wikidataSearcher{
		endpoint: cfg.Endpoint,
		contact:  cfg.Contact,
		polite:   newPoliteness(ToolNameWikidata, cfg.Contact, apiClient(cfg.Client)),
		quota:    newQuota(ToolNameWikidata, cfg.MaxPerTurn, cfg.MaxPerDay),
	}
	if w.endpoint == "" {
		w.endpoint = wikidataEndpoint
	}
	return tools.Tool{
		Spec: model.ToolSpec{
			Name:        ToolNameWikidata,
			Description: wikidataDescription,
			Parameters:  wikidataParams,
		},
		Handler: w.handle,
		Tier:    tools.TierSilent,
		Mutates: false,
	}
}

type wikidataSearcher struct {
	endpoint string
	contact  Contact
	polite   *politeness
	quota    *quota
}

// The wbsearchentities envelope. The fields not read here (`pageid`,
// `repository`, `match`, `display`, `title`, `url`, `concepturi`,
// `search-continue`) are omitted deliberately: an unused field is a field
// a reader has to check the purpose of.
type wikidataResponse struct {
	Search []wikidataEntity `json:"search"`

	// Error is the Action API's IN-BAND failure, and it arrives with HTTP
	// 200 — which is the whole reason it has to be decoded. An unhandled
	// one would present as an empty `search` array, and reporting "the
	// search ran and matched nothing" for a query that never ran is
	// precisely the failed-vs-empty conflation this package exists to
	// prevent.
	Error *wikidataError `json:"error"`
}

type wikidataError struct {
	Code string `json:"code"`
	Info string `json:"info"`
}

type wikidataEntity struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (w *wikidataSearcher) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var in struct {
		Q     string `json:"q"`
		Limit int    `json:"limit"`
	}
	if err := tools.DecodeArgs(args, &in); err != nil {
		return nil, err
	}
	// Contact first, for the reason spelled out in wikipedia.go's handler.
	if !w.contact.Present() {
		return nil, contactRequiredError(ToolNameWikidata, "Wikimedia", wikimediaPolicy)
	}
	q := strings.TrimSpace(in.Q)
	if q == "" {
		return nil, fmt.Errorf("no search term given: call %s with {\"q\": \"…\"}", ToolNameWikidata)
	}
	if err := w.quota.take(ctx); err != nil {
		return nil, err
	}

	entities, err := w.query(ctx, q, clampLimit(in.Limit, DefaultWikidataResults, MaxWikidataResults))
	if err != nil {
		return nil, err
	}
	if len(entities) == 0 {
		return []byte(noResultsFor("The Wikidata search", q,
			"Try the entity's common label, or web.wikipedia for a topic that may not be a distinct entity.")), nil
	}
	return renderWikidataEntities(q, entities), nil
}

func (w *wikidataSearcher) query(ctx context.Context, q string, limit int) ([]wikidataEntity, error) {
	resp, err := w.polite.getAPI(ctx, w.endpoint, url.Values{
		"action":   {"wbsearchentities"},
		"search":   {q},
		"language": {wikidataLanguage},
		"uselang":  {wikidataLanguage},
		"format":   {"json"},
		"limit":    {strconv.Itoa(limit)},
	}, "application/json")
	if err != nil {
		return nil, transportError(wikidataWhat, err)
	}
	if !resp.ok() {
		return nil, apiStatusError(wikidataWhat, resp.Status, resp.Body)
	}

	var decoded wikidataResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return nil, malformedError(wikidataWhat, len(resp.Body))
	}
	if e := decoded.Error; e != nil {
		return nil, fmt.Errorf("%s returned an API error and NO search happened: %s (code %s)", wikidataWhat,
			clip(tools.OneLine(e.Info), apiErrorSnippetChars), tools.OneLine(e.Code))
	}
	out := make([]wikidataEntity, 0, len(decoded.Search))
	for _, e := range decoded.Search {
		if strings.TrimSpace(e.ID) == "" {
			continue // no QID means no entity URL, which means not actionable
		}
		out = append(out, e)
	}
	return out, nil
}

// renderWikidataEntities lays out label / QID / URL / description — the
// three fields that make an entity identifiable, in the package's shared
// shortlist shape.
//
// An entity with no label is still rendered, headed by its QID: dropping
// a real hit over a cosmetic gap loses more than it saves.
func renderWikidataEntities(q string, entities []wikidataEntity) []byte {
	out := make([]resultEntry, 0, len(entities))
	for _, e := range entities {
		out = append(out, resultEntry{
			Title: fallback(e.Label, e.ID),
			URL:   wikidataEntityBase + e.ID,
			Lines: []string{"QID: " + e.ID, snippetLine(e.Description)},
		})
	}
	return renderList(
		fmt.Sprintf("%d Wikidata entit(y/ies) for %q, most relevant first:\n", len(entities), q),
		out,
		"\nUse web.fetch on a URL above to read the entity's full statement list.\n",
	)
}
