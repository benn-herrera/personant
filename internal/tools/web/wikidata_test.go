package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"personant/internal/tools"
)

// §6.1.8 web.wikidata tests. httptest throughout.
//
// The payload is a canned copy of the `action=wbsearchentities` envelope,
// carrying the shapes that matter: the LABEL as a first-class field (the
// whole reason this endpoint replaced the REST search/page one, which
// answers on the Wikidata wiki with bare QIDs and no label anywhere in
// the payload), the protocol-relative `url` and the RDF `concepturi` that
// must both be ignored in favour of a built
// `https://www.wikidata.org/wiki/<QID>`, an entity with no description,
// and one with no id at all.

const wikidataPayload = `{
  "searchinfo": {"search": "general relativity"},
  "search": [
    {
      "id": "Q11452",
      "title": "Q11452",
      "pageid": 13253,
      "display": {
        "label": {"value": "general relativity", "language": "en"},
        "description": {"value": "theory of gravitation developed by Albert Einstein", "language": "en"}
      },
      "repository": "wikidata",
      "url": "//www.wikidata.org/wiki/Q11452",
      "concepturi": "http://www.wikidata.org/entity/Q11452",
      "label": "general relativity",
      "description": "theory of gravitation developed by Albert Einstein",
      "match": {"type": "label", "language": "en", "text": "general relativity"}
    },
    {
      "id": "Q3183606",
      "title": "Q3183606",
      "url": "//www.wikidata.org/wiki/Q3183606",
      "concepturi": "http://www.wikidata.org/entity/Q3183606",
      "label": "General Relativity",
      "match": {"type": "label", "language": "en", "text": "General Relativity"}
    },
    {
      "title": "no id at all",
      "label": "Unaddressable",
      "description": "nothing to link to"
    }
  ],
  "search-continue": 7,
  "success": 1
}`

func TestWikidataHappyPath(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, wikidataPayload)
	h := assertToolShape(t, NewWikidataTool(WikidataConfig{Contact: testContact, Endpoint: s.URL}), ToolNameWikidata)

	out, err := call(t, h, map[string]any{"q": "general relativity"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The Action API is parameter-driven; every one of these is
	// load-bearing, and a wrong one yields an error or the wrong language.
	for _, kv := range [][2]string{
		{"action", "wbsearchentities"},
		{"search", "general relativity"},
		{"language", "en"},
		{"uselang", "en"},
		{"format", "json"},
	} {
		if got := s.param(kv[0]); got != kv[1] {
			t.Errorf("endpoint saw %s=%q, want %q", kv[0], got, kv[1])
		}
	}
	if !strings.Contains(s.userAgent(), "mailto:"+testContact.Email) {
		t.Errorf("Wikimedia requires contact in the User-Agent; got %q", s.userAgent())
	}

	for _, want := range []string{
		"2 Wikidata entit",
		// The LABEL — the finding that drove this endpoint choice.
		"general relativity",
		"QID: Q11452",
		"https://www.wikidata.org/wiki/Q11452",
		"theory of gravitation developed by Albert Einstein",
		// The second entity has a label but no description, and must still
		// render.
		"General Relativity",
		"https://www.wikidata.org/wiki/Q3183606",
		"web.fetch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}

	// The response's own URL fields are unusable and must not leak: `url`
	// is protocol-relative and `concepturi` is the RDF IRI, neither of
	// which web.fetch can follow as written.
	for _, forbidden := range []string{"   //www.wikidata.org", "wikidata.org/entity/"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("an unusable URL form %q reached the result:\n%s", forbidden, out)
		}
	}
	// An entry with no id has no entity URL.
	if strings.Contains(out, "Unaddressable") {
		t.Errorf("an entity with no QID was rendered:\n%s", out)
	}
}

func TestWikidataLimit(t *testing.T) {
	pinDay(t, "2026-08-05")
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"absent → default", map[string]any{"q": "x"}, "5"},
		{"explicit zero → default", map[string]any{"q": "x", "limit": 0}, "5"},
		{"in range is honoured", map[string]any{"q": "x", "limit": 7}, "7"},
		{"above the ceiling clamps", map[string]any{"q": "x", "limit": 99}, "10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAPIServer(t, http.StatusOK, wikidataPayload)
			h := NewWikidataTool(WikidataConfig{Contact: testContact, Endpoint: s.URL}).Handler
			if _, err := call(t, h, tc.args); err != nil {
				t.Fatalf("args %v were rejected rather than clamped: %v", tc.args, err)
			}
			if got := s.param("limit"); got != tc.want {
				t.Errorf("endpoint saw limit=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestWikidataEmptyAndFailure(t *testing.T) {
	pinDay(t, "2026-08-05")

	empty := newAPIServer(t, http.StatusOK, `{"searchinfo":{"search":"qwertyuiop"},"search":[],"success":1}`)
	out, err := call(t, NewWikidataTool(WikidataConfig{Contact: testContact, Endpoint: empty.URL}).Handler,
		map[string]any{"q": "qwertyuiop"})
	if err != nil {
		t.Fatalf("an empty search array is a SUCCESS, not an error: %v", err)
	}
	for _, want := range []string{"0 results", "successfully", "EMPTY RESULT", "web.wikipedia"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty result missing %q:\n%s", want, out)
		}
	}

	// The Action API's IN-BAND error: HTTP 200, an `error` object, and no
	// `search` key at all. It must NOT read as an empty result — that is
	// exactly the conflation the trichotomy exists to prevent.
	inBand := newAPIServer(t, http.StatusOK,
		`{"error":{"code":"param-missing","info":"The required parameter \"search\" was missing."},"servedby":"mw-api-int"}`)
	_, err = call(t, NewWikidataTool(WikidataConfig{Contact: testContact, Endpoint: inBand.URL}).Handler,
		map[string]any{"q": "x"})
	if err == nil {
		t.Fatal("an in-band API error was reported as an empty result")
	}
	for _, want := range []string{"NO search happened", "param-missing", "was missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("in-band error missing %q: %v", want, err)
		}
	}

	// And the ordinary transport-level failure.
	bad := newAPIServer(t, http.StatusServiceUnavailable, "Wikimedia is down")
	_, err = call(t, NewWikidataTool(WikidataConfig{Contact: testContact, Endpoint: bad.URL}).Handler,
		map[string]any{"q": "x"})
	if err == nil {
		t.Fatal("a non-200 must be an error, not content")
	}
	for _, want := range []string{"503", "NO search happened"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// TestWikidataQuotaIsItsOwn — a Wikidata loop must not spend the
// encyclopedia's allowance, even though both hit Wikimedia.
func TestWikidataQuotaIsItsOwn(t *testing.T) {
	pinDay(t, "2026-08-05")
	entities := newAPIServer(t, http.StatusOK, wikidataPayload)
	pages := newAPIServer(t, http.StatusOK, wikiPayload)
	wikidata := NewWikidataTool(WikidataConfig{Contact: testContact, Endpoint: entities.URL, MaxPerTurn: 1}).Handler
	wiki := NewWikipediaTool(WikipediaConfig{Contact: testContact, Endpoint: pages.URL, MaxPerTurn: 1}).Handler

	ctx := tools.ContextWithTurn(context.Background(), 1)
	if _, err := wikidata(ctx, json.RawMessage(`{"q":"x"}`)); err != nil {
		t.Fatalf("wikidata: %v", err)
	}
	if _, err := wiki(ctx, json.RawMessage(`{"q":"x"}`)); err != nil {
		t.Fatalf("web.wikipedia was capped by web.wikidata's usage: %v", err)
	}
	if _, err := wikidata(ctx, json.RawMessage(`{"q":"x"}`)); err == nil {
		t.Fatal("wikidata's own cap did not bind")
	}
}

func TestWikidataRejectsEmptyTerm(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, wikidataPayload)
	h := NewWikidataTool(WikidataConfig{Contact: testContact, Endpoint: s.URL}).Handler

	for _, args := range []string{`{}`, `{"q":"  "}`} {
		if _, err := h(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %s: an empty term was accepted", args)
		}
	}
	if s.count() != 0 {
		t.Errorf("an empty term reached the endpoint %d time(s)", s.count())
	}
}
