package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personant/internal/tools"
)

// §6.1.1 web.wikipedia tests. Everything runs against httptest — no live
// network anywhere, including the "real payload" case, whose body is a
// canned copy of the REST endpoint's documented shape.

// wikiPayload is the REST response shape:
// pages[].{id,key,title,excerpt,description,thumbnail}. It carries the
// two nullable fields as nulls on one entry, because a null description
// is the common case for an article without a short description and a
// decoder that trips on it would fail in production and nowhere else.
const wikiPayload = `{
  "pages": [
    {
      "id": 25039021,
      "key": "Go_(programming_language)",
      "title": "Go (programming language)",
      "excerpt": "<span class=\"searchmatch\">Go</span> is a high-level general purpose language designed at Google &amp; released in 2009. It is syntactically similar to C.",
      "matched_title": null,
      "description": "Programming language",
      "thumbnail": {
        "mimetype": "image/png",
        "width": 60,
        "height": 60,
        "duration": null,
        "url": "//upload.wikimedia.org/wikipedia/commons/thumb/0/05/Go_Logo_Blue.svg/60px-Go_Logo_Blue.svg.png"
      }
    },
    {
      "id": 1234,
      "key": "Go_(game)",
      "title": "Go (game)",
      "excerpt": "Abstract strategy board <span class=\"searchmatch\">game</span> for two players",
      "matched_title": null,
      "description": null,
      "thumbnail": null
    }
  ]
}`

// wikiServer stands in for the REST endpoint, recording what the tool
// actually sent so the request half of the contract is measured rather
// than assumed.
type wikiServer struct {
	*httptest.Server
	gotQuery     string
	gotLimit     string
	gotUserAgent string
	calls        int
}

func newWikiServer(t *testing.T, status int, body string) *wikiServer {
	t.Helper()
	ws := &wikiServer{}
	ws.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws.calls++
		ws.gotQuery = r.URL.Query().Get("q")
		ws.gotLimit = r.URL.Query().Get("limit")
		ws.gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ws.Close)
	return ws
}

func wikiHandler(t *testing.T, cfg WikipediaConfig) tools.Handler {
	t.Helper()
	tool := NewWikipediaTool(cfg)
	if tool.Spec.Name != ToolNameWikipedia {
		t.Fatalf("tool name = %q, want %q", tool.Spec.Name, ToolNameWikipedia)
	}
	if tool.Tier != tools.TierSilent || tool.Mutates {
		t.Fatalf("web.wikipedia must be tier 0 and non-mutating; got tier=%d mutates=%v", tool.Tier, tool.Mutates)
	}
	return tool.Handler
}

func wikiCall(t *testing.T, h tools.Handler, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := h(tools.ContextWithTurn(context.Background(), 1), raw)
	return string(out), err
}

// TestWikipediaHappyPath — the whole result contract in one pass: the
// request the endpoint sees, and every field the model reads back.
func TestWikipediaHappyPath(t *testing.T) {
	pinDay(t, "2026-08-05")
	ws := newWikiServer(t, http.StatusOK, wikiPayload)
	h := wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: ws.URL})

	out, err := wikiCall(t, h, map[string]any{"q": "go language"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ws.gotQuery != "go language" {
		t.Errorf("endpoint saw q=%q, want %q (the term must be URL-encoded, not mangled)", ws.gotQuery, "go language")
	}
	// Wikimedia's User-Agent policy REQUIRES contact information; the
	// exact string is pinned by TestUserAgentShape, so this asserts the
	// obligation rather than the formatting.
	for _, want := range []string{"personant/", testContact.Name, "mailto:" + testContact.Email, ToolNameWikipedia} {
		if !strings.Contains(ws.gotUserAgent, want) {
			t.Errorf("User-Agent %q is missing %q — Wikimedia's policy requires an identifying agent with contact",
				ws.gotUserAgent, want)
		}
	}

	for _, want := range []string{
		"2 Wikipedia article(s)",
		"Go (programming language)",
		// The `key` is already URL-safe and must be concatenated, NOT
		// re-encoded — percent-escaped parens would still resolve but stop
		// matching what a human or a follow-up web.fetch would write.
		"https://en.wikipedia.org/wiki/Go_(programming_language)",
		"https://en.wikipedia.org/wiki/Go_(game)",
		"Programming language", // description
		"web.fetch",            // the follow-up affordance
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q:\n%s", want, out)
		}
	}
	// A null description simply has no line; it must not render as "null".
	if strings.Contains(out, "null") {
		t.Errorf("a null field leaked into the result:\n%s", out)
	}
}

// TestWikipediaExcerptStripping — the API wraps matches in
// <span class="searchmatch"> and escapes the prose around them. Both must
// be undone, and undone in the order that does not resurrect a tag.
func TestWikipediaExcerptStripping(t *testing.T) {
	pinDay(t, "2026-08-05")
	ws := newWikiServer(t, http.StatusOK, wikiPayload)
	h := wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: ws.URL})

	out, err := wikiCall(t, h, map[string]any{"q": "go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "<span") || strings.Contains(out, "searchmatch") || strings.Contains(out, "</span>") {
		t.Errorf("HTML markup survived into the tool result:\n%s", out)
	}
	if !strings.Contains(out, "Go is a high-level general purpose language") {
		t.Errorf("the excerpt text did not survive tag stripping:\n%s", out)
	}
	if !strings.Contains(out, "at Google & released in 2009") {
		t.Errorf("HTML entities were not unescaped (&amp; should read as &):\n%s", out)
	}

	// And the unit directly, including the order-of-operations trap: an
	// escaped tag must stay text rather than becoming a tag.
	for _, tc := range []struct{ in, want string }{
		{"plain text", "plain text"},
		{`<span class="searchmatch">Go</span> is fast`, "Go is fast"},
		{"tea &amp; biscuits &lt;3", "tea & biscuits <3"},
		{"&lt;script&gt;alert(1)&lt;/script&gt;", "<script>alert(1)</script>"},
		{"<b>unclosed", "unclosed"},
		{"", ""},
	} {
		if got := stripHTML(tc.in); got != tc.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWikipediaLimit — default and clamping at both ends, measured on the
// wire because the wire is where the limit has an effect. Out-of-range is
// clamped SILENTLY: a limit is a preference, and erroring on one spends a
// tool round to learn what the clamp already decided.
func TestWikipediaLimit(t *testing.T) {
	pinDay(t, "2026-08-05")
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"absent → default", map[string]any{"q": "go"}, "5"},
		{"explicit zero → default", map[string]any{"q": "go", "limit": 0}, "5"},
		{"negative → default", map[string]any{"q": "go", "limit": -7}, "5"},
		{"in range is honoured", map[string]any{"q": "go", "limit": 3}, "3"},
		{"at the ceiling", map[string]any{"q": "go", "limit": 10}, "10"},
		{"above the ceiling clamps", map[string]any{"q": "go", "limit": 500}, "10"},
		{"the floor", map[string]any{"q": "go", "limit": 1}, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := newWikiServer(t, http.StatusOK, wikiPayload)
			h := wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: ws.URL})
			if _, err := wikiCall(t, h, tc.args); err != nil {
				t.Fatalf("args %v were rejected rather than clamped: %v", tc.args, err)
			}
			if ws.gotLimit != tc.want {
				t.Errorf("endpoint saw limit=%q, want %q", ws.gotLimit, tc.want)
			}
		})
	}
}

// TestWikipediaEmptyResults — an empty index is a SUCCESS that says so,
// never an error. web.search's trichotomy, held to here for the same
// reason: a model told the lookup failed goes looking elsewhere, a model
// told the topic does not exist says so confidently.
func TestWikipediaEmptyResults(t *testing.T) {
	pinDay(t, "2026-08-05")
	ws := newWikiServer(t, http.StatusOK, `{"pages":[]}`)
	h := wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: ws.URL})

	out, err := wikiCall(t, h, map[string]any{"q": "qwertyuiop asdf"})
	if err != nil {
		t.Fatalf("an empty result is a SUCCESS, not an error: %v", err)
	}
	for _, want := range []string{"0 articles", "successfully", "EMPTY RESULT"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty result missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "could not be performed") {
		t.Errorf("the empty result claims a failure:\n%s", out)
	}
}

// TestWikipediaNonOK — status AND a bounded body snippet, and the whole
// thing stays one line however the endpoint formats its error.
func TestWikipediaNonOK(t *testing.T) {
	pinDay(t, "2026-08-05")
	body := "{\n  \"httpCode\": 400,\n  \"messageTranslations\": { \"en\": \"limit must be an integer\" }\n}"
	ws := newWikiServer(t, http.StatusBadRequest, body)
	h := wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: ws.URL})

	out, err := wikiCall(t, h, map[string]any{"q": "go"})
	if err == nil {
		t.Fatalf("a non-200 must be an error, not content; got:\n%s", out)
	}
	text := err.Error()
	if !strings.Contains(text, "400") {
		t.Errorf("error does not name the status: %s", text)
	}
	if !strings.Contains(text, "NO search happened") {
		t.Errorf("error does not say the search did not run: %s", text)
	}
	if !strings.Contains(text, "limit must be an integer") {
		t.Errorf("error dropped the actionable body detail: %s", text)
	}
	if strings.Contains(text, "\n") {
		t.Errorf("the body snippet was not collapsed to one line: %q", text)
	}

	// A huge error page cannot become the tool result.
	big := newWikiServer(t, http.StatusInternalServerError, "<html>"+strings.Repeat("boom ", 4000)+"</html>")
	if _, err := wikiCall(t, wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: big.URL}), map[string]any{"q": "go"}); err == nil {
		t.Fatal("a 500 was not reported as an error")
	} else if len(err.Error()) > 600 {
		t.Errorf("the error snippet is unbounded (%d bytes)", len(err.Error()))
	}
}

// TestWikipediaExcerptByteCap — §6.5. A pathological excerpt is clipped
// with an honest ellipsis rather than handed to the turn's budget whole.
func TestWikipediaExcerptByteCap(t *testing.T) {
	pinDay(t, "2026-08-05")
	long := strings.Repeat("encyclopedic prose ", 500) // ~9.5 KB, one result
	payload := fmt.Sprintf(`{"pages":[{"key":"Long","title":"Long","excerpt":%q,"description":%q}]}`, long, long)
	ws := newWikiServer(t, http.StatusOK, payload)
	h := wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: ws.URL})

	out, err := wikiCall(t, h, map[string]any{"q": "long"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("an over-long excerpt was not clipped with an ellipsis:\n%s", out)
	}
	// Two clipped fields plus the fixed scaffolding — comfortably under
	// the 8 KB §6.5 cap, which is the property that matters.
	if len(out) > 2*snippetMaxChars+512 {
		t.Errorf("rendered result is %d bytes; the per-field clip did not bound it", len(out))
	}
}

// TestWikipediaQuota — the same local-cap mechanism web.search uses, on
// its OWN counter. Unmetered does not mean unbounded: a retry loop is the
// failure this stops.
func TestWikipediaQuota(t *testing.T) {
	pinDay(t, "2026-08-05")
	ws := newWikiServer(t, http.StatusOK, wikiPayload)
	tool := NewWikipediaTool(WikipediaConfig{Contact: testContact, Endpoint: ws.URL, MaxPerTurn: 2, MaxPerDay: 3})
	h := tool.Handler

	ctx := tools.ContextWithTurn(context.Background(), 1)
	args := json.RawMessage(`{"q":"go"}`)
	for i := 1; i <= 2; i++ {
		if _, err := h(ctx, args); err != nil {
			t.Fatalf("query %d under the per-turn cap failed: %v", i, err)
		}
	}
	_, err := h(ctx, args)
	if err == nil {
		t.Fatal("the third query in one turn was not capped")
	}
	if !strings.Contains(err.Error(), ToolNameWikipedia) {
		t.Errorf("the refusal does not name the tool it bounds: %v", err)
	}
	if !strings.Contains(err.Error(), "per turn") || !strings.Contains(err.Error(), "Do not retry") {
		t.Errorf("cap refusal is not actionable: %v", err)
	}
	if ws.calls != 2 {
		t.Errorf("endpoint was called %d times under a per-turn cap of 2", ws.calls)
	}

	// The day cap binds across turns: turn 2 gets one more call, then the
	// day allowance of 3 is spent.
	if _, err := h(tools.ContextWithTurn(context.Background(), 2), args); err != nil {
		t.Fatalf("the next turn did not get a fresh per-turn allowance: %v", err)
	}
	_, err = h(tools.ContextWithTurn(context.Background(), 3), args)
	if err == nil {
		t.Fatal("the fourth query in one day was not capped")
	}
	if !strings.Contains(err.Error(), "per day") {
		t.Errorf("day-cap refusal does not name the window: %v", err)
	}
}

// TestWikipediaSeparateQuotaFromSearch — one mechanism, one counter EACH.
// A shared counter would let a Wikipedia loop silently spend the metered
// backend's allowance.
func TestWikipediaSeparateQuotaFromSearch(t *testing.T) {
	pinDay(t, "2026-08-05")
	ws := newWikiServer(t, http.StatusOK, wikiPayload)
	wiki := NewWikipediaTool(WikipediaConfig{Contact: testContact, Endpoint: ws.URL, MaxPerTurn: 1}).Handler
	provider := &fakeProvider{results: []SearchResult{{Title: "t", URL: "https://example.com"}}}
	search := searchHandler(t, SearchConfig{Provider: provider, MaxPerTurn: 1})

	ctx := tools.ContextWithTurn(context.Background(), 1)
	if _, err := wiki(ctx, json.RawMessage(`{"q":"go"}`)); err != nil {
		t.Fatalf("wikipedia: %v", err)
	}
	if _, err := search(ctx, json.RawMessage(`{"query":"go"}`)); err != nil {
		t.Fatalf("web.search was capped by web.wikipedia's usage: %v", err)
	}
}

// TestWikipediaRejectsEmptyTerm — the one argument mistake worth an
// error, answered with a message naming the field.
func TestWikipediaRejectsEmptyTerm(t *testing.T) {
	pinDay(t, "2026-08-05")
	ws := newWikiServer(t, http.StatusOK, wikiPayload)
	h := wikiHandler(t, WikipediaConfig{Contact: testContact, Endpoint: ws.URL})

	for _, args := range []string{`{}`, `{"q":"   "}`, `"{}"`} {
		out, err := h(context.Background(), json.RawMessage(args))
		if err == nil {
			t.Errorf("args %s: an empty term was accepted:\n%s", args, out)
		}
	}
	if ws.calls != 0 {
		t.Errorf("an empty term reached the endpoint %d time(s)", ws.calls)
	}
}
