package web

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// The Wikimedia family — what the REST-endpoint tools share.
//
// `web.wikipedia` and `web.wiktionary` are two wikis of one family
// answering ONE endpoint with ONE payload shape, so the path, the decoder
// and the query live here once rather than twice. Only the nouns differ,
// and the nouns belong to the tools.
//
// `web.wikidata` is family by POLICY but not by endpoint: the Wikidata
// wiki answers this same REST path with bare QIDs and no label anywhere
// in the payload (the finding is recorded in wikidata.go), so that tool
// uses the Action API and shares nothing here but wikimediaPolicy.

// wikimediaPolicy names the obligation in the refusal the model reads.
// Shared by every Wikimedia tool: one family, one policy, one sentence.
const wikimediaPolicy = "User-Agent policy"

// wikimediaSearchPagePath is the REST search-page path, identical on
// every wiki in the family — `en.wikipedia.org`, `en.wiktionary.org`,
// `de.wikipedia.org` and the rest. A language edition is a HOST SWAP and
// nothing else; a second wiki is a host swap plus its own vocabulary.
//
// It is NOT shared with web.wikidata, for the reason above.
const wikimediaSearchPagePath = "/w/rest.php/v1/search/page"

// wikimediaPage is one entry of the REST response's `pages` array. The
// fields not read here (`id`, `matched_title`, `thumbnail`) are omitted
// deliberately: an unused field is a field a reader has to check the
// purpose of.
//
// `description` and `excerpt` are nullable in the API. A JSON `null`
// unmarshals into a string field as a no-op, leaving "", which is exactly
// the rendering decision ("skip the line") already wanted.
type wikimediaPage struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Excerpt     string `json:"excerpt"`
	Description string `json:"description"`
}

type wikimediaSearchResponse struct {
	Pages []wikimediaPage `json:"pages"`
}

// searchPages performs one REST search/page query and returns the hits a
// result URL can be built from.
//
// what names the operation in the model's terms ("the Wiktionary
// search"), so the family's three failure messages read as the tool's own.
func searchPages(ctx context.Context, p *politeness, endpoint, what, q string, limit int) ([]wikimediaPage, error) {
	resp, err := p.getAPI(ctx, endpoint, url.Values{
		"q":     {q},
		"limit": {strconv.Itoa(limit)},
	}, "application/json")
	if err != nil {
		return nil, transportError(what, err)
	}
	if !resp.ok() {
		return nil, apiStatusError(what, resp.Status, resp.Body)
	}

	var decoded wikimediaSearchResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		return nil, malformedError(what, len(resp.Body))
	}
	out := make([]wikimediaPage, 0, len(decoded.Pages))
	for _, page := range decoded.Pages {
		if strings.TrimSpace(page.Key) == "" {
			continue // no key means no page URL, which means not actionable
		}
		out = append(out, page)
	}
	return out, nil
}

// renderPages lays out a search/page shortlist. Both wikis project the
// same four fields into the package's shared list shape; the header and
// footer wording is the caller's, because "article" and "entry" are not
// interchangeable.
//
// The page `key` is concatenated onto pageBase, NOT re-encoded: the API
// returns it already URL-safe (`Go_(programming_language)`), and escaping
// it again yields a URL that still resolves but no longer matches what a
// human — or a follow-up web.fetch — would write.
func renderPages(pages []wikimediaPage, pageBase, header, footer string) []byte {
	entries := make([]resultEntry, 0, len(pages))
	for _, p := range pages {
		entries = append(entries, resultEntry{
			Title: p.Title,
			URL:   pageBase + p.Key,
			Lines: []string{snippetLine(p.Description), snippetLine(p.Excerpt)},
		})
	}
	return renderList(header, entries, footer)
}
