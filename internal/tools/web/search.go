package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"personant/internal/model"
	"personant/internal/tools"
)

// ToolNameSearch is the registry key for the §6.1.1 search tool.
const ToolNameSearch = "web.search"

// DefaultSearchResults is how many ranked results one query returns.
// Enough to choose from, small enough that the §6.5 byte cap is never
// the thing doing the choosing.
const DefaultSearchResults = 6

// snippetMaxChars bounds one result's snippet. The snippet exists to let
// the model decide WHICH result to fetch; a longer one is the fetch it
// has not decided to make yet.
const snippetMaxChars = 400

// SearchResult is one ranked hit. Published is a free-form date string
// exactly as the backend reported it (formats vary by provider and a
// half-parsed date is worse than the original text).
type SearchResult struct {
	Title     string
	URL       string
	Snippet   string
	Published string
}

// SearchProvider is the backend seam: implement one method and a
// different search engine drops in without the tool changing at all.
//
// It is deliberately ONE method. The tool needs a ranked list for a
// query and nothing else — no capability negotiation, no provider name
// (the model does not care which engine answered, and the event log
// already records the tool name), no result-count contract beyond "at
// most limit".
//
// The error/empty distinction is the interface's most important term:
//
//   - A non-nil error means the search DID NOT HAPPEN (unreachable
//     backend, refused credential, malformed response). It must never be
//     used to report "nothing matched".
//   - A nil error with an empty slice means the search RAN and the index
//     had no match.
//
// Collapsing the two is the single failure this tool is designed
// against: an empty-on-failure result teaches the model that the web has
// no answer, and it then says so with confidence.
type SearchProvider interface {
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// SearchConfig configures the search tool.
type SearchConfig struct {
	// Provider is the backend. Required.
	Provider SearchProvider

	// MaxResults caps one query's results. <= 0 → DefaultSearchResults.
	MaxResults int

	// MaxPerTurn / MaxPerDay are the LOCAL query caps. <= 0 → the
	// Default* constants in quota.go.
	MaxPerTurn int
	MaxPerDay  int
}

var searchParams = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "The search query, in natural language or as keywords."
    }
  },
  "required": ["query"],
  "additionalProperties": false
}`)

const searchDescription = "Search the web and return ranked results with title, URL and a short snippet. " +
	"Use web.fetch on a result URL to read the full page. " +
	"A successful search that matches nothing says so explicitly; a search that could not be performed " +
	"reports an error — the two are never conflated, so an error never means 'no information exists'."

// NewSearchTool builds the `web.search` tool. It fails only on a nil
// provider, which is a wiring bug rather than a user-configuration
// problem: a session with no search credential does not build this tool
// at all (see the registry construction in internal/chat), and §6.1.4's
// empty-registry semantics already handle an absent tool correctly.
func NewSearchTool(cfg SearchConfig) (tools.Tool, error) {
	if cfg.Provider == nil {
		return tools.Tool{}, errors.New("web: search tool needs a SearchProvider")
	}
	s := &searcher{
		provider:   cfg.Provider,
		maxResults: cfg.MaxResults,
		quota:      newQuota(ToolNameSearch, cfg.MaxPerTurn, cfg.MaxPerDay),
	}
	if s.maxResults <= 0 {
		s.maxResults = DefaultSearchResults
	}
	return tools.Tool{
		Spec: model.ToolSpec{
			Name:        ToolNameSearch,
			Description: searchDescription,
			Parameters:  searchParams,
		},
		Handler: s.handle,
		Tier:    tools.TierSilent,
		Mutates: false,
	}, nil
}

type searcher struct {
	provider   SearchProvider
	maxResults int
	quota      *quota
}

func (s *searcher) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := tools.DecodeArgs(args, &a); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return nil, fmt.Errorf("no query given: call %s with {\"query\": \"…\"}", ToolNameSearch)
	}

	// The cap is taken BEFORE the call, so it bounds ATTEMPTS rather than
	// successes. A model looping against a failing backend is exactly the
	// case the cap exists for, and a cap that only counted successes
	// would not stop it.
	if err := s.quota.take(ctx); err != nil {
		return nil, err
	}

	results, err := s.provider.Search(ctx, query, s.maxResults)
	if err != nil {
		return nil, fmt.Errorf("the search could not be performed (%w) — NO search happened, so this says "+
			"nothing about whether results exist. Do not report that nothing was found. "+
			"Retry once if the cause looks transient, otherwise answer from what you have "+
			"and say the search was unavailable", err)
	}
	if len(results) == 0 {
		return []byte(noResultsMessage(query)), nil
	}
	return renderResults(query, results), nil
}

// noResultsMessage is the EMPTY half of the trichotomy. It is a
// successful tool result, and it states the success explicitly — the
// model must be able to tell this apart from the failure message above
// without inferring anything.
func noResultsMessage(query string) string {
	return fmt.Sprintf("The search ran successfully and matched 0 results for %q.\n\n"+
		"This is an EMPTY RESULT, not a failure: the query reached the search backend and the index "+
		"returned nothing for it. Try broader or differently-worded terms, or answer from what you have.\n", query)
}

func renderResults(query string, results []SearchResult) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "%d result(s) for %q, most relevant first:\n", len(results), query)
	for i, r := range results {
		fmt.Fprintf(&b, "\n%d. %s\n   %s\n", i+1, tools.OneLine(fallback(r.Title, "(untitled)")), r.URL)
		if r.Published != "" {
			fmt.Fprintf(&b, "   published: %s\n", tools.OneLine(r.Published))
		}
		if snip := clip(tools.OneLine(r.Snippet), snippetMaxChars); snip != "" {
			fmt.Fprintf(&b, "   %s\n", snip)
		}
	}
	b.WriteString("\nUse web.fetch on a URL above to read the full page.\n")
	return []byte(b.String())
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// clip truncates on a rune boundary with an honest ellipsis.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}
