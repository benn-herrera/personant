package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// §6.1.1 web.fetch tests. Everything mechanical runs against httptest —
// the extraction pipeline, the caps, and the refusals are all
// deterministic given bytes, so the network buys nothing here. The one
// live probe lives in fetch_live_test.go.

// articleFixture is a realistic page: site chrome (nav, ad rail, cookie
// banner, footer, related-links block) wrapped around a real article
// with a subheading, a list, and a RELATIVE link. Extraction quality is
// measured against it in both directions — the article survives, the
// chrome does not.
const articleFixture = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sourdough Hydration and Crumb Structure — Baker's Notebook</title>
  <meta name="author" content="Ida Fermentova">
  <meta property="article:published_time" content="2026-03-14T09:30:00Z">
  <meta property="og:site_name" content="Baker's Notebook">
</head>
<body>
  <nav id="site-nav"><a href="/">Home</a> <a href="/archive">Archive</a> <a href="/about">About</a></nav>
  <div class="cookie-banner">We use cookies to improve your experience. Accept all cookies?</div>
  <aside class="advertisement"><p>SPONSORED: Buy the Proofing Basket Pro today and save 40 percent on your first order.</p></aside>
  <article>
    <h1>Sourdough Hydration and Crumb Structure</h1>
    <p>Hydration is the ratio of water to flour by weight, and it is the single variable that
    changes a sourdough loaf more than any other. A dough at sixty-five percent hydration behaves
    like clay: it holds a shape, it takes a score cleanly, and it produces a tight, even crumb that
    slices well for sandwiches. Push the same flour to eighty-five percent and the dough becomes a
    slack, sticky mass that must be handled with wet hands and coaxed rather than shaped.</p>
    <p>The crumb that results from high hydration is not simply more open; it is differently
    organized. Large irregular alveoli form where the gluten network is thin enough to stretch
    around expanding gas but strong enough not to tear. That balance is what fermentation time and
    folding schedules are actually managing, and it is why two bakers working at identical
    hydration can produce loaves that look nothing alike.</p>
    <h2>Reading the dough rather than the clock</h2>
    <p>Time-based schedules fail because flour, room temperature, and starter vigor all move
    independently. A bulk ferment that takes four hours in a cold kitchen in February may finish in
    two and a half hours in August, and a dough left to the clock in the second case will be
    over-proofed before it reaches the bench. The reliable signals are physical: a domed and jiggly
    surface, a twenty to fifty percent rise in volume, and a bubble structure visible along the side
    of a straight-walled container.</p>
    <ul>
      <li>Sixty-five percent: tight crumb, forgiving handling, good for beginners.</li>
      <li>Seventy-five percent: open crumb with structure, the common working range.</li>
      <li>Eighty-five percent: very open crumb, demands strong flour and confident handling.</li>
    </ul>
    <p>See the companion piece on <a href="/notes/starter-maintenance">starter maintenance</a> for
    the feeding ratios that keep a culture predictable enough for any of this to be repeatable at
    all. A sluggish starter turns every hydration number in this article into a guess.</p>
  </article>
  <div class="related"><h3>Related posts</h3><a href="/notes/bagels">Bagels at home</a></div>
  <footer id="site-footer"><p>Copyright 2026 Baker's Notebook. All rights reserved. Privacy policy. Terms of service.</p></footer>
</body>
</html>`

// spaShellFixture is a client-rendered application: a real document, a
// real title, several kilobytes of inline script, and no article text.
var spaShellFixture = `<!DOCTYPE html>
<html lang="en">
<head><title>Dashboard — Metrics App</title></head>
<body>
  <div id="root"></div>
  <script>
  var CONFIG = {"feature":"flag","padding":"` + strings.Repeat("x", 4096) + `"};
  window.__APP__ = function(){ return CONFIG; };
  </script>
</body>
</html>`

// fetchTool builds a fetch handler pointed at whatever the caller's
// server does, with the caller's overrides applied.
func fetchTool(t *testing.T, cfg FetchConfig) func(ctx context.Context, url string) (string, error) {
	t.Helper()
	tool := NewFetchTool(cfg)
	if tool.Spec.Name != ToolNameFetch {
		t.Fatalf("tool name = %q, want %q", tool.Spec.Name, ToolNameFetch)
	}
	if tool.Tier != 0 || tool.Mutates {
		t.Fatalf("web.fetch must be tier 0 and non-mutating; got tier=%d mutates=%v", tool.Tier, tool.Mutates)
	}
	return func(ctx context.Context, url string) (string, error) {
		args, err := json.Marshal(map[string]string{"url": url})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		// Wrapped in a JSON string — the shape an OpenAI-compatible
		// provider actually sends.
		wrapped, err := json.Marshal(string(args))
		if err != nil {
			t.Fatalf("wrap args: %v", err)
		}
		out, err := tool.Handler(ctx, wrapped)
		return string(out), err
	}
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func htmlHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

// TestFetchExtractsArticleAndDropsChrome is the extraction-quality
// measurement: what a model reads after a fetch is the article, with its
// structure and its links, and none of the site furniture.
func TestFetchExtractsArticleAndDropsChrome(t *testing.T) {
	srv := serve(t, htmlHandler(articleFixture))
	fetch := fetchTool(t, FetchConfig{Client: srv.Client()})

	out, err := fetch(context.Background(), srv.URL+"/notes/hydration")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// Metadata readability lifted from the page head. This is what a
	// long-career memory system uses to judge staleness, so its absence
	// is a real regression, not a cosmetic one.
	for _, want := range []string{
		"title: Sourdough Hydration and Crumb Structure",
		"byline: Ida Fermentova",
		"published: 2026-03-14",
		"language: en",
		"site: Baker's Notebook",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metadata line %q missing from result:\n%s", want, out)
		}
	}

	// The article body survived, with its structure.
	for _, want := range []string{
		"Hydration is the ratio of water to flour",
		"Reading the dough rather than the clock",
		"Seventy-five percent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("article content %q missing from result:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "## Reading the dough") {
		t.Errorf("subheading did not survive as Markdown:\n%s", out)
	}
	// The relative link became an absolute, followable one — the reason
	// Markdown conversion is worth doing at all.
	if !strings.Contains(out, "("+srv.URL+"/notes/starter-maintenance)") {
		t.Errorf("relative link was not resolved against the page URL:\n%s", out)
	}

	// The chrome did not.
	for _, unwanted := range []string{
		"SPONSORED",
		"We use cookies",
		"All rights reserved",
		"Bagels at home",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("boilerplate %q leaked into the extracted article:\n%s", unwanted, out)
		}
	}
	if strings.Contains(out, "status: no-content") {
		t.Error("a real article was misdetected as an empty shell")
	}
}

// TestFetchDetectsEmptyShell — the failure mode the detector exists for.
// A model told "the page is blank" answers confidently and wrongly; a
// model told the fetch was incomplete goes and looks elsewhere.
func TestFetchDetectsEmptyShell(t *testing.T) {
	srv := serve(t, htmlHandler(spaShellFixture))
	fetch := fetchTool(t, FetchConfig{Client: srv.Client()})

	out, err := fetch(context.Background(), srv.URL+"/dashboard")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(out, "status: no-content") {
		t.Errorf("empty shell not flagged in the metadata block:\n%s", out)
	}
	if !strings.Contains(out, "requires JavaScript rendering") {
		t.Errorf("empty shell notice missing:\n%s", out)
	}
	if !strings.Contains(out, "Do NOT report that the page is empty") {
		t.Errorf("notice does not tell the model what NOT to conclude:\n%s", out)
	}
	// The metadata that IS available still comes back — knowing the title
	// of the page it could not read is useful.
	if !strings.Contains(out, "Dashboard") {
		t.Errorf("title lost on the empty-shell path:\n%s", out)
	}
}

// TestEmptyShellHeuristic exercises the predicate directly at its
// boundaries, including the documented false-positive corner.
func TestEmptyShellHeuristic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		textLen int
		htmlLen int
		want    bool
	}{
		{"tiny document is not a shell", 10, 500, false},
		{"tiny document at the size floor", 10, shellMinHTMLBytes - 1, false},
		{"no text in a substantial document", 0, shellMinHTMLBytes, true},
		{"text just under the floor", shellMinTextChars - 1, 10000, true},
		{"text at the floor", shellMinTextChars, 10000, false},
		{"paragraph of text in a huge bundle", 400, 400_000, true},
		{"long article in a huge page", 40_000, 400_000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksEmptyShell(tc.textLen, tc.htmlLen); got != tc.want {
				t.Errorf("looksEmptyShell(%d, %d) = %v, want %v", tc.textLen, tc.htmlLen, got, tc.want)
			}
		})
	}
}

// TestFetchRefusesNonHTTPSchemes — the §6.2 boundary. It is precise: it
// blocks every non-http(s) scheme and it does NOT block loopback (the
// tests above fetch 127.0.0.1 freely, which is the ruling working).
func TestFetchRefusesNonHTTPSchemes(t *testing.T) {
	fetch := fetchTool(t, FetchConfig{})
	for _, target := range []string{
		"file:///etc/passwd",
		"file://localhost/etc/passwd",
		"data:text/html,<h1>hi</h1>",
		"ftp://ftp.example.com/pub/file.txt",
		"javascript:alert(1)",
		"gopher://example.com/",
		"/etc/passwd",
		"example.com/no-scheme",
	} {
		t.Run(target, func(t *testing.T) {
			out, err := fetch(context.Background(), target)
			if err == nil {
				t.Fatalf("scheme was not refused; got output:\n%s", out)
			}
			if !strings.Contains(err.Error(), "http") {
				t.Errorf("refusal does not name the permitted schemes: %v", err)
			}
		})
	}
}

// TestFetchAllowsLoopback pins the other half of the ruling: loopback is
// deliberately NOT blocked. A test that only proved the refusals would
// pass just as well against an over-broad SSRF filter.
func TestFetchAllowsLoopback(t *testing.T) {
	srv := serve(t, htmlHandler(articleFixture))
	fetch := fetchTool(t, FetchConfig{Client: srv.Client()})
	if !strings.Contains(srv.URL, "127.0.0.1") {
		t.Skipf("httptest did not bind loopback (%s)", srv.URL)
	}
	if _, err := fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("loopback fetch must be permitted: %v", err)
	}
}

// TestFetchRefusesRedirectToBlockedScheme — the allowlist must hold on
// every hop, not just the one the model typed.
func TestFetchRefusesRedirectToBlockedScheme(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "file:///etc/passwd", http.StatusFound)
	})
	fetch := fetchTool(t, FetchConfig{Client: srv.Client()})
	if out, err := fetch(context.Background(), srv.URL); err == nil {
		t.Fatalf("redirect to file:// was followed; got:\n%s", out)
	}
}

// TestFetchFollowsRedirectsByDefault pins the ZERO-VALUE configuration —
// the one the session actually ships with. Most of the real web performs
// at least one hop (http→https, trailing slash, canonical host), so a
// default that followed none would break the common case while every
// explicitly-configured test still passed.
func TestFetchFollowsRedirectsByDefault(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/final" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		htmlHandler(articleFixture)(w, r)
	})
	fetch := fetchTool(t, FetchConfig{Client: srv.Client()}) // zero config

	out, err := fetch(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatalf("default configuration did not follow a single redirect: %v", err)
	}
	if !strings.Contains(out, "Hydration is the ratio") {
		t.Errorf("redirect target was not fetched:\n%s", out)
	}
	if !strings.Contains(out, "url: "+srv.URL+"/final") {
		t.Errorf("result reports the pre-redirect URL; the model needs the final one:\n%s", out)
	}
}

// TestFetchCapsRedirects — a redirect loop costs a bounded number of
// round trips, not the turn.
func TestFetchCapsRedirects(t *testing.T) {
	var hops int
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, fmt.Sprintf("/hop%d", hops), http.StatusFound)
	})
	fetch := fetchTool(t, FetchConfig{Client: srv.Client(), MaxRedirects: 3})

	_, err := fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("redirect chain was followed without limit")
	}
	if hops > 5 { // the cap plus the initial request, with slack
		t.Errorf("followed %d hops under a cap of 3", hops)
	}
}

// TestFetchEnforcesSizeCapMidRead is the load-bearing memory property:
// the cap must bound what is READ, so a body that lies about its length
// (or declares none) cannot be buffered whole before the check happens.
func TestFetchEnforcesSizeCapMidRead(t *testing.T) {
	const cap = 64 << 10
	var served int
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// No Content-Length: chunked, so the fast-reject path cannot fire
		// and the mid-read cap is the only thing that can stop this.
		chunk := strings.Repeat("y", 8<<10)
		for i := 0; i < 512; i++ { // 4 MiB if fully drained
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
			served += len(chunk)
			w.(http.Flusher).Flush()
		}
	})
	fetch := fetchTool(t, FetchConfig{Client: srv.Client(), MaxBytes: cap})

	out, err := fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("oversize body was accepted (%d bytes returned)", len(out))
	}
	if !strings.Contains(err.Error(), "narrower") {
		t.Errorf("rejection does not ask for a narrower fetch: %v", err)
	}
}

// TestFetchRejectsDeclaredOversize — the Content-Length fast path.
func TestFetchRejectsDeclaredOversize(t *testing.T) {
	body := strings.Repeat("z", 200<<10)
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write([]byte(body))
	})
	fetch := fetchTool(t, FetchConfig{Client: srv.Client(), MaxBytes: 8 << 10})
	if _, err := fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("declared-oversize response was accepted")
	}
}

// TestFetchContentTypeBranches — HTML through the pipeline, text through
// verbatim, binary refused rather than parsed as garbage.
func TestFetchContentTypeBranches(t *testing.T) {
	const markdownBody = "# Release notes\n\n- fixed the thing\n- broke another thing\n"
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		wantErr     string
		wantContain string
	}{
		{"markdown passes through", "text/markdown; charset=utf-8", markdownBody, "", "- fixed the thing"},
		{"plain text passes through", "text/plain", "just some notes about the build", "", "just some notes"},
		{"json passes through", "application/json", `{"ok":true}`, "", `{"ok":true}`},
		{"html goes through the pipeline", "text/html", articleFixture, "", "Hydration is the ratio"},
		{"png is refused", "image/png", "\x89PNG\r\n\x1a\n binary bytes here", "cannot read", ""},
		{"pdf is refused", "application/pdf", "%PDF-1.7 binary", "cannot read", ""},
		{"octet-stream is refused", "application/octet-stream", "\x00\x01\x02", "cannot read", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			})
			fetch := fetchTool(t, FetchConfig{Client: srv.Client()})
			out, err := fetch(context.Background(), srv.URL+"/doc")

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected refusal, got output:\n%s", out)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("refusal %v does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if !strings.Contains(out, tc.wantContain) {
				t.Errorf("result missing %q:\n%s", tc.wantContain, out)
			}
			if !strings.Contains(out, "url: "+srv.URL+"/doc") {
				t.Errorf("result does not carry the fetched url:\n%s", out)
			}
		})
	}
}

// TestFetchHonoursTimeout — a wedged server costs a bounded wait, and
// the message says what happened.
func TestFetchHonoursTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })

	fetch := fetchTool(t, FetchConfig{Client: srv.Client(), Timeout: 150 * time.Millisecond})
	start := time.Now()
	_, err := fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("a hanging server did not produce an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout took %s; the configured bound is 150ms", elapsed)
	}
}

// TestFetchSurfacesHTTPStatus — a 404 is a fact the model can act on,
// not a mystery.
func TestFetchSurfacesHTTPStatus(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	fetch := fetchTool(t, FetchConfig{Client: srv.Client()})
	_, err := fetch(context.Background(), srv.URL+"/missing")
	if err == nil {
		t.Fatal("404 was not reported")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error does not name the status: %v", err)
	}
}

// TestFetchRejectsMissingURL — an argument-shaped mistake gets a message
// naming the field, which is what makes it recoverable in one round.
func TestFetchRejectsMissingURL(t *testing.T) {
	tool := NewFetchTool(FetchConfig{})
	for _, args := range []string{`{}`, `"{}"`, `{"url":"  "}`, ``} {
		if _, err := tool.Handler(context.Background(), json.RawMessage(args)); err == nil {
			t.Errorf("args %q: expected an error naming the url field", args)
		} else if !strings.Contains(err.Error(), "url") {
			t.Errorf("args %q: error does not name the field: %v", args, err)
		}
	}
}

// TestFetchSendsDescriptiveUserAgent — the courtesy owed to an operator
// reading their access log.
func TestFetchSendsDescriptiveUserAgent(t *testing.T) {
	got := make(chan string, 1)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("User-Agent")
		htmlHandler(articleFixture)(w, r)
	})
	fetch := fetchTool(t, FetchConfig{Client: srv.Client()})
	if _, err := fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	ua := <-got
	if !strings.Contains(ua, "personant/") {
		t.Errorf("User-Agent %q does not identify personant", ua)
	}
}
