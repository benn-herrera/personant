package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"personant/internal/tools"
)

// §6.1.7 web.crossref tests. httptest throughout. The payload is a canned
// copy of the /works envelope carrying the shapes that break a naive
// decoder: array-valued `title` and `container-title`, an organizational
// author with `name` instead of given/family, a work with no date at all,
// and one with no DOI (which is not citable and must be dropped).

const crossrefPayload = `{
  "status": "ok",
  "message-type": "work-list",
  "message": {
    "total-results": 2,
    "items": [
      {
        "DOI": "10.1038/s41586-021-03819-2",
        "title": ["Highly accurate protein structure prediction with AlphaFold"],
        "container-title": ["Nature"],
        "author": [
          {"given": "John", "family": "Jumper"},
          {"given": "Richard", "family": "Evans"},
          {"given": "Alexander", "family": "Pritzel"},
          {"given": "Tim", "family": "Green"}
        ],
        "issued": {"date-parts": [[2021, 7, 15]]}
      },
      {
        "DOI": "10.5555/no-date",
        "title": ["A Report With No Date"],
        "container-title": [],
        "author": [{"name": "Some Standards Body"}],
        "issued": {"date-parts": [[null]]}
      },
      {
        "title": ["Unregistered"],
        "issued": {"date-parts": [[2020]]}
      }
    ]
  }
}`

func TestCrossrefHappyPath(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, crossrefPayload)
	h := assertToolShape(t, NewCrossrefTool(CrossrefConfig{Contact: testContact, Endpoint: s.URL}), ToolNameCrossref)

	out, err := call(t, h, map[string]any{"q": "alphafold", "limit": 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := s.param("query"); got != "alphafold" {
		t.Errorf("endpoint saw query=%q, want the term unmangled", got)
	}
	if got := s.param("rows"); got != "3" {
		t.Errorf("endpoint saw rows=%q, want 3", got)
	}
	// Crossref's polite pool is entered by carrying a mail address; the UA
	// is where we put it.
	if !strings.Contains(s.userAgent(), "mailto:"+testContact.Email) {
		t.Errorf("the polite-pool mailto is missing from the User-Agent: %q", s.userAgent())
	}

	for _, want := range []string{
		"2 Crossref work(s)",
		"Highly accurate protein structure prediction with AlphaFold",
		"DOI: 10.1038/s41586-021-03819-2",
		"https://doi.org/10.1038/s41586-021-03819-2",
		"John Jumper, Richard Evans, Alexander Pritzel, et al.",
		"Nature (2021)",
		// The organizational author's `name` field, and a work whose only
		// citation detail is that it has none.
		"Some Standards Body",
		"A Report With No Date",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}
	// A DOI-less record is not citable; rendering it would produce a
	// https://doi.org/ URL that resolves to nothing.
	if strings.Contains(out, "Unregistered") {
		t.Errorf("a work with no DOI was rendered:\n%s", out)
	}
	// A missing venue and a missing year must simply have no line, never a
	// literal "null" or an empty parenthesis.
	for _, forbidden := range []string{"null", "()"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("an absent field leaked %q into the result:\n%s", forbidden, out)
		}
	}
}

// TestCrossrefRunsWithoutContact — Crossref PREFERS a mailto (it routes to
// the polite pool); it does not require one. No contact means the
// anonymous pool, not a refusal.
func TestCrossrefRunsWithoutContact(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, crossrefPayload)
	h := NewCrossrefTool(CrossrefConfig{Endpoint: s.URL}).Handler

	if _, err := call(t, h, map[string]any{"q": "alphafold"}); err != nil {
		t.Fatalf("Crossref refused to run without contact; its policy only prefers: %v", err)
	}
	if strings.Contains(s.userAgent(), "mailto:") {
		t.Errorf("an unconfigured contact produced a mailto anyway: %q", s.userAgent())
	}
}

func TestCrossrefLimit(t *testing.T) {
	pinDay(t, "2026-08-05")
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"absent → default", map[string]any{"q": "x"}, "5"},
		{"negative → default", map[string]any{"q": "x", "limit": -1}, "5"},
		{"above the ceiling clamps", map[string]any{"q": "x", "limit": 400}, "10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAPIServer(t, http.StatusOK, crossrefPayload)
			h := NewCrossrefTool(CrossrefConfig{Contact: testContact, Endpoint: s.URL}).Handler
			if _, err := call(t, h, tc.args); err != nil {
				t.Fatalf("args %v were rejected rather than clamped: %v", tc.args, err)
			}
			if got := s.param("rows"); got != tc.want {
				t.Errorf("endpoint saw rows=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestCrossrefEmptyAndFailure(t *testing.T) {
	pinDay(t, "2026-08-05")

	empty := newAPIServer(t, http.StatusOK, `{"message":{"items":[]}}`)
	out, err := call(t, NewCrossrefTool(CrossrefConfig{Contact: testContact, Endpoint: empty.URL}).Handler,
		map[string]any{"q": "qwertyuiop"})
	if err != nil {
		t.Fatalf("an empty item list is a SUCCESS, not an error: %v", err)
	}
	for _, want := range []string{"0 results", "successfully", "EMPTY RESULT", "web.arxiv"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty result missing %q:\n%s", want, out)
		}
	}

	bad := newAPIServer(t, http.StatusBadRequest, `{"message":"Invalid rows value"}`)
	_, err = call(t, NewCrossrefTool(CrossrefConfig{Contact: testContact, Endpoint: bad.URL}).Handler,
		map[string]any{"q": "x"})
	if err == nil {
		t.Fatal("a non-200 must be an error, not content")
	}
	if !strings.Contains(err.Error(), "Invalid rows value") {
		t.Errorf("the error dropped the actionable body detail: %v", err)
	}
}

func TestCrossrefQuotaIsItsOwn(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, crossrefPayload)
	feed := newAPIServer(t, http.StatusOK, arxivPayload)
	crossref := NewCrossrefTool(CrossrefConfig{Contact: testContact, Endpoint: s.URL, MaxPerTurn: 1}).Handler
	arxiv := NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: feed.URL, MaxPerTurn: 1}).Handler

	ctx := tools.ContextWithTurn(context.Background(), 1)
	if _, err := crossref(ctx, json.RawMessage(`{"q":"x"}`)); err != nil {
		t.Fatalf("crossref: %v", err)
	}
	if _, err := arxiv(ctx, json.RawMessage(`{"q":"x"}`)); err != nil {
		t.Fatalf("web.arxiv was capped by web.crossref's usage: %v", err)
	}
	if _, err := crossref(ctx, json.RawMessage(`{"q":"x"}`)); err == nil {
		t.Fatal("crossref's own cap did not bind")
	}
}

func TestCrossrefRejectsEmptyTerm(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, crossrefPayload)
	h := NewCrossrefTool(CrossrefConfig{Contact: testContact, Endpoint: s.URL}).Handler

	for _, args := range []string{`{}`, `{"q":"  "}`} {
		if _, err := h(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %s: an empty term was accepted", args)
		}
	}
	if s.count() != 0 {
		t.Errorf("an empty term reached the endpoint %d time(s)", s.count())
	}
}
