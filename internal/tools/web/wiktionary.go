package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"personant/internal/model"
	"personant/internal/tools"
)

// `web.wiktionary` — the DICTIONARY half of the REST-endpoint pair.
//
// It is a separate tool from web.wikipedia for the same reason
// web.wikipedia is separate from web.search: "I want the word, not the
// topic" is a judgement about the QUESTION, held only by the model at the
// moment it asks. The two wikis share a family, an endpoint and a
// decoder (wikimedia.go); they do not share a territory.
//
// # Endpoint verification (2026-08-05)
//
// `GET https://en.wiktionary.org/w/rest.php/v1/search/page?q=…&limit=…`
// was checked LIVE on this date. The payload is IDENTICAL in shape to the
// encyclopedia's — `pages[].{id,key,title,excerpt,…}`, with matched terms
// wrapped in `<span class="searchmatch">` and the surrounding prose
// escaped — and the excerpts carry real dictionary payload: the
// `emulsion` hit returns its etymology inline. So unlike web.wikidata,
// where the same REST path answered with bare QIDs and forced the Action
// API, the shared REST path is the CORRECT choice here and the shared
// decoder returns something usable.

// ToolNameWiktionary is the registry key for the §6.1.9 Wiktionary search
// tool.
const ToolNameWiktionary = "web.wiktionary"

// WiktionaryHost is the wiki queried. `en.wiktionary.org` is the ENGLISH
// EDITION, not the English language: it defines words from every language
// in English. Another edition is a host swap and nothing else.
//
// It is its OWN host entry in the politeness table — serial, no fixed
// spacing — rather than sharing Wikipedia's: the gate keys on the host a
// request actually reaches, and two wikis are two hosts.
const WiktionaryHost = "en.wiktionary.org"

const (
	// wiktionaryEndpoint is this wiki's REST search-page endpoint. The path
	// itself is the family's (wikimedia.go), shared with web.wikipedia.
	wiktionaryEndpoint = "https://" + WiktionaryHost + wikimediaSearchPagePath

	// wiktionaryEntryBase + a page's `key` is the canonical entry URL,
	// concatenated and not re-encoded — see renderPages.
	wiktionaryEntryBase = "https://" + WiktionaryHost + "/wiki/"

	// DefaultWiktionaryResults is the `limit` used when the model asks for
	// no particular count.
	DefaultWiktionaryResults = 5

	// MaxWiktionaryResults caps `limit`, on §6.1.5's terms.
	MaxWiktionaryResults = 10
)

// wiktionaryWhat names the operation in every message the model reads.
const wiktionaryWhat = "the Wiktionary search"

// WiktionaryConfig configures the tool. The zero value is valid and
// yields the live endpoint, a default client and the default local caps —
// but NOT a runnable tool: without Contact the handler refuses, per the
// Wikimedia policy recorded in wikipedia.go.
type WiktionaryConfig struct {
	// Contact is the config.toml `[user]` identity. REQUIRED in practice:
	// absent, every call fails with contactRequiredError.
	Contact Contact

	// Endpoint overrides the REST endpoint. "" → wiktionaryEndpoint.
	Endpoint string

	// Client is the HTTP client. nil → a client with apiQueryTimeout.
	Client *http.Client

	// MaxPerTurn / MaxPerDay are the LOCAL query caps, on this tool's OWN
	// counter. <= 0 → the Default* constants in quota.go.
	MaxPerTurn int
	MaxPerDay  int
}

var wiktionaryParams = json.RawMessage(`{
  "type": "object",
  "properties": {
    "q": {
      "type": "string",
      "description": "The word or phrase to look up. The base form (lemma) matches best; an inflected form usually resolves to its lemma's entry."
    },
    "limit": {
      "type": "integer",
      "description": "How many entries to return. Default 5, maximum 10; out-of-range values are clamped, never rejected."
    }
  },
  "required": ["q"],
  "additionalProperties": false
}`)

// The description LEADS with the literal words a request will use.
// Selection is a small model's job as much as a large one's, and
// "Wiktionary" does not tell a gemma-class model that this is where a
// dictionary lookup goes — so "Dictionary search", "define" and "look up
// the word" appear verbatim, as the anchors the routing hooks on.
const wiktionaryDescription = "Dictionary search (Wiktionary). Wins for word definitions, etymology, usage, " +
	"pronunciation and translations — \"define X\", \"look up the word X\", \"dictionary search\", \"what does X mean\". " +
	"For encyclopedic or conceptual context about a topic see web.wikipedia. Results include each entry's canonical " +
	"URL, which web.fetch can retrieve in full."

// NewWiktionaryTool builds the `web.wiktionary` tool. Like web.wikipedia
// it never fails at CONSTRUCTION — no credential — and refuses at
// INVOCATION without contact information, so the model is told why the
// dictionary is unavailable rather than silently never seeing it.
func NewWiktionaryTool(cfg WiktionaryConfig) tools.Tool {
	w := &wiktionarian{
		endpoint: cfg.Endpoint,
		contact:  cfg.Contact,
		polite:   newPoliteness(ToolNameWiktionary, cfg.Contact, apiClient(cfg.Client)),
		quota:    newQuota(ToolNameWiktionary, cfg.MaxPerTurn, cfg.MaxPerDay),
	}
	if w.endpoint == "" {
		w.endpoint = wiktionaryEndpoint
	}
	return tools.Tool{
		Spec: model.ToolSpec{
			Name:        ToolNameWiktionary,
			Description: wiktionaryDescription,
			Parameters:  wiktionaryParams,
		},
		Handler: w.handle,
		Tier:    tools.TierSilent,
		Mutates: false,
	}
}

type wiktionarian struct {
	endpoint string
	contact  Contact
	polite   *politeness
	quota    *quota
}

func (w *wiktionarian) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var a struct {
		Q     string `json:"q"`
		Limit int    `json:"limit"`
	}
	if err := tools.DecodeArgs(args, &a); err != nil {
		return nil, err
	}
	// Contact first, for the reason spelled out in wikipedia.go's handler.
	if !w.contact.Present() {
		return nil, contactRequiredError(ToolNameWiktionary, "Wikimedia", wikimediaPolicy)
	}
	q := strings.TrimSpace(a.Q)
	if q == "" {
		return nil, fmt.Errorf("no search term given: call %s with {\"q\": \"…\"}", ToolNameWiktionary)
	}
	// Taken BEFORE the call, so it bounds ATTEMPTS rather than successes.
	if err := w.quota.take(ctx); err != nil {
		return nil, err
	}

	pages, err := searchPages(ctx, w.polite, w.endpoint, wiktionaryWhat, q,
		clampLimit(a.Limit, DefaultWiktionaryResults, MaxWiktionaryResults))
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return []byte(noResultsFor("The Wiktionary search", q,
			"Try the word's base form, or a different spelling; for a topic rather than a word, use web.wikipedia.")), nil
	}
	return renderPages(pages, wiktionaryEntryBase,
		fmt.Sprintf("%d Wiktionary entr(y/ies) for %q, most relevant first:\n", len(pages), q),
		"\nUse web.fetch on a URL above to read the full dictionary entry.\n"), nil
}
