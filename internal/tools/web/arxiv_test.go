package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"personant/internal/tools"
)

// §6.1.6 web.arxiv tests. httptest throughout — the "real payload" case
// is a canned copy of the Atom feed the query API documents, including
// the shapes that trip a naive decoder: a title with the newline+indent
// arXiv actually emits, an author list longer than the render cap, and an
// entry with no usable id.

const arxivPayload = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title type="html">ArXiv Query: search_query=attention</title>
  <entry>
    <id>http://arxiv.org/abs/1706.03762v7</id>
    <updated>2023-08-02T00:41:18Z</updated>
    <published>2017-06-12T17:57:34Z</published>
    <title>Attention Is All You
  Need</title>
    <summary>  The dominant sequence transduction models are based on complex recurrent or
convolutional neural networks.
</summary>
    <author><name>Ashish Vaswani</name></author>
    <author><name>Noam Shazeer</name></author>
    <author><name>Niki Parmar</name></author>
    <author><name>Jakob Uszkoreit</name></author>
    <author><name>Llion Jones</name></author>
    <link href="http://arxiv.org/abs/1706.03762v7" rel="alternate" type="text/html"/>
    <category term="cs.CL" scheme="http://arxiv.org/schemas/atom"/>
  </entry>
  <entry>
    <id>http://arxiv.org/abs/2401.00001v1</id>
    <published>2024-01-01T00:00:00Z</published>
    <title>A Single-Author Paper</title>
    <summary>Short abstract.</summary>
    <author><name>Solo Researcher</name></author>
  </entry>
  <entry>
    <id>malformed-identifier</id>
    <title>Unaddressable</title>
    <summary>No abs URL, so nothing to cite.</summary>
  </entry>
</feed>`

// TestArxivHappyPath — the request the endpoint sees and every field the
// model reads back.
func TestArxivHappyPath(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, arxivPayload)
	h := assertToolShape(t, NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: s.URL}), ToolNameArxiv)

	out, err := call(t, h, map[string]any{"q": "attention is all you need"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := s.param("search_query"); got != "attention is all you need" {
		t.Errorf("endpoint saw search_query=%q, want the term unmangled", got)
	}
	if !strings.Contains(s.userAgent(), "mailto:"+testContact.Email) {
		t.Errorf("arXiv asks for an identifying agent; got %q", s.userAgent())
	}

	for _, want := range []string{
		"2 arXiv paper(s)",
		// The multi-line Atom title must collapse to one line.
		"Attention Is All You Need",
		"arXiv:1706.03762v7",
		"https://arxiv.org/abs/1706.03762v7",
		"submitted 2017-06-12",
		"Ashish Vaswani, Noam Shazeer, Niki Parmar, et al.",
		"Solo Researcher",
		"dominant sequence transduction models",
		"web.fetch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}
	// A single-author paper must not claim collaborators.
	if strings.Contains(out, "Solo Researcher, et al.") {
		t.Errorf("a lone author gained an 'et al.':\n%s", out)
	}
	// The entry with no /abs/ id is not citable and must be dropped, not
	// rendered with a broken URL.
	if strings.Contains(out, "Unaddressable") {
		t.Errorf("an entry with no arXiv id was rendered:\n%s", out)
	}
}

// TestArxivLimit — default and clamping, measured on the wire.
func TestArxivLimit(t *testing.T) {
	pinDay(t, "2026-08-05")
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"absent → default", map[string]any{"q": "x"}, "5"},
		{"explicit zero → default", map[string]any{"q": "x", "limit": 0}, "5"},
		{"negative → default", map[string]any{"q": "x", "limit": -3}, "5"},
		{"in range is honoured", map[string]any{"q": "x", "limit": 2}, "2"},
		{"above the ceiling clamps", map[string]any{"q": "x", "limit": 900}, "10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAPIServer(t, http.StatusOK, arxivPayload)
			h := NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: s.URL}).Handler
			if _, err := call(t, h, tc.args); err != nil {
				t.Fatalf("args %v were rejected rather than clamped: %v", tc.args, err)
			}
			if got := s.param("max_results"); got != tc.want {
				t.Errorf("endpoint saw max_results=%q, want %q", got, tc.want)
			}
		})
	}
}

// TestArxivRunsWithoutContact — arXiv ASKS for contact, it does not
// require it. An unconfigured [user] must still get results, with the
// identity-only agent. (Contrast TestWikimediaContactRequired.)
func TestArxivRunsWithoutContact(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, arxivPayload)
	h := NewArxivTool(ArxivConfig{Endpoint: s.URL}).Handler

	out, err := call(t, h, map[string]any{"q": "attention"})
	if err != nil {
		t.Fatalf("arXiv refused to run without contact; its policy only asks: %v", err)
	}
	if !strings.Contains(out, "arXiv:1706.03762v7") {
		t.Errorf("no results without contact:\n%s", out)
	}
	if strings.Contains(s.userAgent(), "mailto:") {
		t.Errorf("an unconfigured contact produced a mailto anyway: %q", s.userAgent())
	}
	if !strings.Contains(s.userAgent(), "personant/") {
		t.Errorf("the anonymous agent still has to identify the client: %q", s.userAgent())
	}
}

// TestArxivEmptyAndFailure — the trichotomy: 0 entries is a SUCCESS that
// says so, a non-200 is an error that says NO search happened.
func TestArxivEmptyAndFailure(t *testing.T) {
	pinDay(t, "2026-08-05")

	empty := newAPIServer(t, http.StatusOK, `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"></feed>`)
	out, err := call(t, NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: empty.URL}).Handler,
		map[string]any{"q": "qwertyuiop"})
	if err != nil {
		t.Fatalf("an empty feed is a SUCCESS, not an error: %v", err)
	}
	for _, want := range []string{"0 results", "successfully", "EMPTY RESULT"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty result missing %q:\n%s", want, out)
		}
	}

	bad := newAPIServer(t, http.StatusBadRequest, "sortBy is not a valid field")
	_, err = call(t, NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: bad.URL}).Handler,
		map[string]any{"q": "x"})
	if err == nil {
		t.Fatal("a non-200 must be an error, not content")
	}
	for _, want := range []string{"400", "NO search happened", "sortBy is not a valid field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}

	garbage := newAPIServer(t, http.StatusOK, "<<<not xml")
	if _, err := call(t, NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: garbage.URL}).Handler,
		map[string]any{"q": "x"}); err == nil {
		t.Error("a malformed feed was accepted as content")
	}
}

// TestArxivQuota — its OWN counter, at the shared defaults.
func TestArxivQuota(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, arxivPayload)
	h := NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: s.URL, MaxPerTurn: 2}).Handler

	ctx := tools.ContextWithTurn(context.Background(), 1)
	args := json.RawMessage(`{"q":"x"}`)
	for i := 1; i <= 2; i++ {
		if _, err := h(ctx, args); err != nil {
			t.Fatalf("query %d under the cap failed: %v", i, err)
		}
	}
	err := h2err(h(ctx, args))
	if err == nil {
		t.Fatal("the third query in one turn was not capped")
	}
	if !strings.Contains(err.Error(), ToolNameArxiv) {
		t.Errorf("the refusal does not name the tool it bounds: %v", err)
	}
	if s.count() != 2 {
		t.Errorf("endpoint was called %d times under a per-turn cap of 2", s.count())
	}
}

// TestArxivRejectsEmptyTerm — no term, no request.
func TestArxivRejectsEmptyTerm(t *testing.T) {
	pinDay(t, "2026-08-05")
	s := newAPIServer(t, http.StatusOK, arxivPayload)
	h := NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: s.URL}).Handler

	for _, args := range []string{`{}`, `{"q":"   "}`} {
		if _, err := h(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %s: an empty term was accepted", args)
		}
	}
	if s.count() != 0 {
		t.Errorf("an empty term reached the endpoint %d time(s)", s.count())
	}
}

// TestArxivAbstractByteCap — §6.5. A pathological abstract is clipped.
func TestArxivAbstractByteCap(t *testing.T) {
	pinDay(t, "2026-08-05")
	long := strings.Repeat("scholarly prose ", 800) // ~12 KB
	payload := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><entry>` +
		`<id>http://arxiv.org/abs/2401.99999v1</id><title>Long</title><summary>` + long + `</summary>` +
		`</entry></feed>`
	s := newAPIServer(t, http.StatusOK, payload)
	h := NewArxivTool(ArxivConfig{Contact: testContact, Endpoint: s.URL}).Handler

	out, err := call(t, h, map[string]any{"q": "long"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("an over-long abstract was not clipped:\n%s", out)
	}
	if len(out) > arxivAbstractChars+512 {
		t.Errorf("rendered result is %d bytes; the per-field clip did not bound it", len(out))
	}
}
