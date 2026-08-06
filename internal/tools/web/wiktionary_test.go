package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"personant/internal/tools"
)

// §6.1.9 web.wiktionary tests. httptest throughout — no live network,
// including the "real payload" case, whose body is a canned copy of the
// shape a LIVE call returned on 2026-08-05.
//
// The fixture carries what the live check found: the encyclopedia's
// payload shape verbatim (`pages[].{id,key,title,excerpt,…}`), searchmatch
// spans inside the excerpt, escaped prose around them, a null description
// (the norm for a dictionary entry), an etymology sentence riding in the
// excerpt — which is the finding that made the shared REST path the right
// endpoint here — and one entry with no key, which has no URL and must be
// dropped.
const wiktionaryPayload = `{
  "pages": [
    {
      "id": 1109384,
      "key": "emulsion",
      "title": "emulsion",
      "excerpt": "<span class=\"searchmatch\">emulsion</span> (plural <span class=\"searchmatch\">emulsions</span>) Borrowed from New Latin ēmulsiō, from Latin ēmulgeō (&quot;I milk out&quot;) &amp; -ion. A stable suspension of one liquid dispersed in another.",
      "matched_title": null,
      "description": null,
      "thumbnail": null
    },
    {
      "id": 1109385,
      "key": "emulsions",
      "title": "emulsions",
      "excerpt": "plural of <span class=\"searchmatch\">emulsion</span>",
      "matched_title": null,
      "description": "English word",
      "thumbnail": null
    },
    {
      "id": 999,
      "key": "",
      "title": "keyless",
      "excerpt": "nothing to link to",
      "description": null
    }
  ]
}`

// TestWiktionaryHappyPath — the whole result contract in one pass: the
// request the endpoint sees, and every field the model reads back.
func TestWiktionaryHappyPath(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, wiktionaryPayload)
	h := assertToolShape(t, NewWiktionaryTool(WiktionaryConfig{Contact: testContact, Endpoint: s.URL}), ToolNameWiktionary)

	out, err := call(t, h, map[string]any{"q": "emulsion"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := s.param("q"); got != "emulsion" {
		t.Errorf("endpoint saw q=%q, want %q (the term must be URL-encoded, not mangled)", got, "emulsion")
	}
	if !strings.Contains(s.userAgent(), "mailto:"+testContact.Email) {
		t.Errorf("Wikimedia requires contact in the User-Agent; got %q", s.userAgent())
	}
	if !strings.Contains(s.userAgent(), ToolNameWiktionary) {
		t.Errorf("the User-Agent does not name the tool: %q", s.userAgent())
	}

	for _, want := range []string{
		"2 Wiktionary entr",
		"emulsion",
		"https://en.wiktionary.org/wiki/emulsion",
		"https://en.wiktionary.org/wiki/emulsions",
		// The dictionary payload the live check found in the excerpt — the
		// reason this endpoint is worth a tool at all.
		"Borrowed from New Latin",
		"English word", // the one non-null description
		"web.fetch",    // the follow-up affordance
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}
	// searchmatch markup out, entities resolved — one tokenizer pass, the
	// §6.1.5 rule unchanged.
	for _, forbidden := range []string{"<span", "searchmatch", "</span>", "&quot;", "&amp;", "null"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("%q survived into the tool result:\n%s", forbidden, out)
		}
	}
	if !strings.Contains(out, `("I milk out") & -ion`) {
		t.Errorf("HTML entities were not unescaped:\n%s", out)
	}
	// A page with no key has no entry URL and is not actionable.
	if strings.Contains(out, "keyless") {
		t.Errorf("a page with no key was rendered:\n%s", out)
	}
}

// TestWiktionaryLimit — default and clamping at both ends, measured on the
// wire because the wire is where the limit has an effect. Out of range is
// clamped SILENTLY: erroring on a preference spends a tool round to learn
// what the clamp already decided.
func TestWiktionaryLimit(t *testing.T) {
	pinDay(t, "2026-08-05")
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"absent → default", map[string]any{"q": "emulsion"}, "5"},
		{"explicit zero → default", map[string]any{"q": "emulsion", "limit": 0}, "5"},
		{"negative → default", map[string]any{"q": "emulsion", "limit": -7}, "5"},
		{"in range is honoured", map[string]any{"q": "emulsion", "limit": 3}, "3"},
		{"the floor", map[string]any{"q": "emulsion", "limit": 1}, "1"},
		{"at the ceiling", map[string]any{"q": "emulsion", "limit": 10}, "10"},
		{"above the ceiling clamps", map[string]any{"q": "emulsion", "limit": 500}, "10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAPIServer(t, http.StatusOK, wiktionaryPayload)
			h := NewWiktionaryTool(WiktionaryConfig{Contact: testContact, Endpoint: s.URL}).Handler
			if _, err := call(t, h, tc.args); err != nil {
				t.Fatalf("args %v were rejected rather than clamped: %v", tc.args, err)
			}
			if got := s.param("limit"); got != tc.want {
				t.Errorf("endpoint saw limit=%q, want %q", got, tc.want)
			}
		})
	}
}

// TestWiktionaryEmptyAndFailure — the trichotomy. An empty index is a
// SUCCESS that says so; a non-200 is an ERROR that says NO search
// happened.
func TestWiktionaryEmptyAndFailure(t *testing.T) {
	pinDay(t, "2026-08-05")

	empty := newAPIServer(t, http.StatusOK, `{"pages":[]}`)
	out, err := call(t, NewWiktionaryTool(WiktionaryConfig{Contact: testContact, Endpoint: empty.URL}).Handler,
		map[string]any{"q": "qwertyuiop"})
	if err != nil {
		t.Fatalf("an empty result is a SUCCESS, not an error: %v", err)
	}
	for _, want := range []string{"0 results", "successfully", "EMPTY RESULT", "web.wikipedia"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty result missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "could not be performed") {
		t.Errorf("the empty result claims a failure:\n%s", out)
	}

	bad := newAPIServer(t, http.StatusBadRequest,
		"{\n  \"httpCode\": 400,\n  \"messageTranslations\": { \"en\": \"limit must be an integer\" }\n}")
	_, err = call(t, NewWiktionaryTool(WiktionaryConfig{Contact: testContact, Endpoint: bad.URL}).Handler,
		map[string]any{"q": "emulsion"})
	if err == nil {
		t.Fatal("a non-200 must be an error, not content")
	}
	for _, want := range []string{"400", "NO search happened", "limit must be an integer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("the body snippet was not collapsed to one line: %q", err.Error())
	}
}

// TestWiktionaryQuotaIsItsOwn — one mechanism, one counter EACH. Two
// Wikimedia tools on one policy still get independent allowances, or a
// dictionary loop silently spends the encyclopedia's.
func TestWiktionaryQuotaIsItsOwn(t *testing.T) {
	pinDay(t, "2026-08-05")
	words := newAPIServer(t, http.StatusOK, wiktionaryPayload)
	pages := newAPIServer(t, http.StatusOK, wikiPayload)
	wikt := NewWiktionaryTool(WiktionaryConfig{Contact: testContact, Endpoint: words.URL, MaxPerTurn: 1}).Handler
	wiki := NewWikipediaTool(WikipediaConfig{Contact: testContact, Endpoint: pages.URL, MaxPerTurn: 1}).Handler

	ctx := tools.ContextWithTurn(context.Background(), 1)
	args := json.RawMessage(`{"q":"emulsion"}`)
	if _, err := wikt(ctx, args); err != nil {
		t.Fatalf("wiktionary: %v", err)
	}
	if _, err := wiki(ctx, args); err != nil {
		t.Fatalf("web.wikipedia was capped by web.wiktionary's usage: %v", err)
	}
	err := h2err(wikt(ctx, args))
	if err == nil {
		t.Fatal("wiktionary's own cap did not bind")
	}
	if !strings.Contains(err.Error(), ToolNameWiktionary) || !strings.Contains(err.Error(), "Do not retry") {
		t.Errorf("the cap refusal is not actionable and tool-named: %v", err)
	}
	if words.count() != 1 {
		t.Errorf("endpoint was called %d times under a per-turn cap of 1", words.count())
	}
}

func TestWiktionaryRejectsEmptyTerm(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, wiktionaryPayload)
	h := NewWiktionaryTool(WiktionaryConfig{Contact: testContact, Endpoint: s.URL}).Handler

	for _, args := range []string{`{}`, `{"q":"  "}`, `"{}"`} {
		if _, err := h(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %s: an empty term was accepted", args)
		}
	}
	if s.count() != 0 {
		t.Errorf("an empty term reached the endpoint %d time(s)", s.count())
	}
}

// TestWiktionaryDescriptionAnchors — the description is the ONLY thing
// routing a "define X" request to this tool, and a gemma-class model
// cannot be assumed to know that Wiktionary IS a dictionary. The literal
// phrasings a user reaches for must appear in it (user ruling
// 2026-08-05).
func TestWiktionaryDescriptionAnchors(t *testing.T) {
	desc := strings.ToLower(NewWiktionaryTool(WiktionaryConfig{}).Spec.Description)
	for _, anchor := range []string{"dictionary", "define", "look up", "etymology", "pronunciation", "web.wikipedia"} {
		if !strings.Contains(desc, anchor) {
			t.Errorf("the model-facing description is missing the selection anchor %q: %s", anchor, desc)
		}
	}
}
