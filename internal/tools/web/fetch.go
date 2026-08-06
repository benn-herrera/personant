// Package web implements the §6.1.1 network tools: `web.fetch` (retrieve
// a URL as readable Markdown), `web.search` (query → ranked results from
// the open web), and the receipt-bearing query tools `web.wikipedia`,
// `web.wikidata`, `web.arxiv` and `web.crossref`. All are read-only, all
// register at tools.TierSilent, and none touches the substrate — they
// take their configuration as plain values and hand back a tools.Tool the
// caller registers.
//
// # Free-API citizenship
//
// Every outbound request in this package goes through politeness.go: the
// contact-bearing User-Agent, the per-host serial gate and spacing table,
// and the one-retry Retry-After rule. That file documents the mechanism;
// each tool's own doc comment records the SPECIFIC published obligations
// of the service it talks to, per the AGENTS.md house rule.
//
// # Tier ruling (SPEC §6.2 gap, closed here)
//
// §6.2's tier table grades filesystem operations and says nothing about
// the web tools. The ruling recorded in §6.2 and implemented here is
// TIER 0 (silent), with the SCHEME ALLOWLIST as the boundary instead of
// an acknowledgement. An ack is only worth its interruption when the user
// can actually adjudicate the question being asked, and "is this URL an
// internal-network probe?" is not a question a human can answer from a
// prompt at typing speed — they would learn to press `y`, which is worse
// than no gate at all. A mechanical boundary that always holds beats a
// prompt that gets reflexively cleared.
//
// # The allowlist is `http` and `https`, and nothing else
//
// It blocks `file://`, `data:`, `ftp://`, and every other scheme, on both
// the initial URL and every redirect hop. It deliberately does NOT block
// loopback, RFC1918, link-local, or any other IP range — that is a USER
// RULING, recorded verbatim: "block file:// urls, do not block localhost
// urls (if I'm deliberately exposing a web server, it's fair game)."
// Do not "harden" this into SSRF filtering; a personal agent on the
// user's own machine reaching the user's own dev server is the intended
// case, not the threat.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"personant/internal/model"
	"personant/internal/tools"
)

// ToolNameFetch is the registry key and the name the model calls back
// with. It is the §6.1.1 name, dotted, not a Go identifier.
const ToolNameFetch = "web.fetch"

// Fetch defaults. Each is overridable through FetchConfig; the values
// here are the ones a caller that supplies nothing gets.
const (
	// DefaultFetchMaxBytes caps the response body. It is enforced WHILE
	// READING (io.LimitReader), not after, so a hostile or merely huge
	// body can never balloon memory before the cap applies. 5 MiB is far
	// above any article page and far below a payload worth buffering.
	DefaultFetchMaxBytes = 5 << 20

	// DefaultFetchTimeout bounds the whole fetch — connect, headers, and
	// body read. It sits deliberately UNDER tools.DefaultTimeout so the
	// tool's own message ("the fetch timed out after 20s") is what the
	// model reads, rather than dispatch's generic timeout result.
	DefaultFetchTimeout = 20 * time.Second

	// DefaultFetchMaxRedirects caps the redirect chain. Enough for the
	// http→https→www→canonical shuffle a real site performs; short of a
	// loop.
	DefaultFetchMaxRedirects = 5
)

// fetchParams is the model-facing JSON Schema. One required string. The
// scheme constraint is stated in the description because a model that
// reads it before calling saves a wasted round.
var fetchParams = json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {
      "type": "string",
      "description": "Absolute URL to retrieve. Must be http:// or https:// — no other scheme is permitted."
    }
  },
  "required": ["url"],
  "additionalProperties": false
}`)

const fetchDescription = "Retrieve a web page over plain HTTP and return its main article content as Markdown, " +
	"with the page's title, byline, publication date and language when the page declares them. " +
	"Boilerplate (navigation, ads, footers) is stripped. There is NO JavaScript execution: a page that renders " +
	"its content client-side returns an explicit 'requires JavaScript rendering' notice rather than an empty body. " +
	"Only http:// and https:// URLs are permitted."

// FetchConfig configures the fetch tool. The zero value is valid and
// yields the Default* constants above.
//
// # Published obligations (AGENTS.md free-API citizenship, consequence 1)
//
// web.fetch has no single service and therefore no single policy: it
// retrieves whatever URL the model names. What it owes every operator it
// reaches is the generic courtesy — an identifying User-Agent with
// contact information when configured, one request at a time per host,
// and Retry-After honoured. A site with a stated crawl-delay is not
// covered here; this is a user-directed single-page retrieval, not a
// crawler, and a per-site policy fetch is out of scope.
type FetchConfig struct {
	// Contact is the config.toml `[user]` identity, injected into the
	// User-Agent. Optional: an ordinary web server requires nothing.
	Contact Contact

	// Client is the HTTP client. nil → http.DefaultClient. The client is
	// COPIED before use, so installing the redirect policy never mutates
	// a client the caller shares with anything else.
	Client *http.Client

	// Timeout bounds one fetch end-to-end. <= 0 → DefaultFetchTimeout.
	Timeout time.Duration

	// MaxBytes caps the response body, enforced mid-read. <= 0 →
	// DefaultFetchMaxBytes.
	MaxBytes int64

	// MaxRedirects caps the redirect chain. <= 0 →
	// DefaultFetchMaxRedirects. There is deliberately no "follow none"
	// setting: the zero value of this struct is the SHIPPING
	// configuration, and a zero that meant "refuse every redirect" would
	// silently break the http→https and trailing-slash hops that most of
	// the real web performs.
	MaxRedirects int
}

// NewFetchTool builds the `web.fetch` tool. It never fails: the tool
// needs no credential and no backend, which is why §6.1.1's fetch
// registers unconditionally while search does not.
func NewFetchTool(cfg FetchConfig) tools.Tool {
	f := newFetcher(cfg)
	return tools.Tool{
		Spec: model.ToolSpec{
			Name:        ToolNameFetch,
			Description: fetchDescription,
			Parameters:  fetchParams,
		},
		Handler: f.handle,
		Tier:    tools.TierSilent,
		Mutates: false,
	}
}

type fetcher struct {
	polite       *politeness
	timeout      time.Duration
	maxBytes     int64
	maxRedirects int
}

func newFetcher(cfg FetchConfig) *fetcher {
	f := &fetcher{
		timeout:      cfg.Timeout,
		maxBytes:     cfg.MaxBytes,
		maxRedirects: cfg.MaxRedirects,
	}
	if f.timeout <= 0 {
		f.timeout = DefaultFetchTimeout
	}
	if f.maxBytes <= 0 {
		f.maxBytes = DefaultFetchMaxBytes
	}
	if f.maxRedirects <= 0 {
		f.maxRedirects = DefaultFetchMaxRedirects
	}

	base := cfg.Client
	if base == nil {
		base = http.DefaultClient
	}
	// Copy: the redirect policy is ours, and a caller's client (or
	// http.DefaultClient) must not acquire it as a side effect.
	client := *base
	client.CheckRedirect = f.checkRedirect
	f.polite = newPoliteness(ToolNameFetch, cfg.Contact, &client)
	return f
}

// checkRedirect enforces both the hop cap and — the load-bearing half —
// the scheme allowlist on every hop. A server that 302s to `file:///etc/
// passwd` must be refused by us; relying on the transport to reject an
// unknown scheme would leave the guarantee to somebody else's default.
func (f *fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > f.maxRedirects {
		return fmt.Errorf("stopped after %d redirects", f.maxRedirects)
	}
	return checkScheme(req.URL)
}

func (f *fetcher) handle(ctx context.Context, args json.RawMessage) ([]byte, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := tools.DecodeArgs(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.URL) == "" {
		return nil, fmt.Errorf("no url given: call %s with {\"url\": \"https://…\"}", ToolNameFetch)
	}
	return f.fetch(ctx, a.URL)
}

// ErrSchemeNotAllowed-shaped message. Stated once, used by both the
// initial check and the redirect check, so the model reads the same
// sentence either way.
func checkScheme(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	case "":
		return fmt.Errorf("url %q has no scheme; %s accepts absolute http:// and https:// URLs only", u.String(), ToolNameFetch)
	default:
		return fmt.Errorf("scheme %q is not permitted; %s accepts http:// and https:// only "+
			"(this blocks file://, data: and every other scheme, and is not negotiable)", u.Scheme, ToolNameFetch)
	}
}

func (f *fetcher) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("url %q is not parseable: %w", rawURL, err)
	}
	if err := checkScheme(u); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", u.Redacted(), err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/markdown,text/plain;q=0.9,*/*;q=0.1")

	// The per-host gate keys on the INITIAL host. A redirect chain that
	// crosses hosts is followed by the transport inside this one call, so
	// the destination host's gate is not consulted — accepted: a single
	// user-directed fetch of one page is not load worth shaping, and the
	// alternative is reimplementing redirect following by hand.
	resp, err := f.polite.do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", u.Redacted(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s returned HTTP %s", u.Redacted(), resp.Status)
	}

	// Fast reject on a declared oversize body, before a single byte of it
	// is read. The mid-read cap below is the guarantee; this is only the
	// courtesy of failing before spending the bandwidth.
	if resp.ContentLength > f.maxBytes {
		return nil, f.tooLargeError(resp.ContentLength)
	}

	// The cap is enforced HERE, while reading: at most maxBytes+1 bytes
	// ever exist in memory, and the +1 is what distinguishes "exactly at
	// the cap" from "over it".
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", u.Redacted(), err)
	}
	if int64(len(body)) > f.maxBytes {
		return nil, f.tooLargeError(-1)
	}

	final := resp.Request.URL // after redirects
	switch kind, mediaType := classifyContent(resp.Header.Get("Content-Type"), body); kind {
	case contentHTML:
		return renderArticle(ctx, body, final)
	case contentText:
		return renderPlain(body, final, mediaType), nil
	default:
		return nil, fmt.Errorf("%s served content type %q, which %s cannot read. "+
			"It handles HTML, Markdown and plain text; it does not decode binary formats. "+
			"Ask the user to supply the content if you need it", u.Redacted(), mediaType, ToolNameFetch)
	}
}

func (f *fetcher) tooLargeError(declared int64) error {
	size := "the response"
	if declared > 0 {
		size = fmt.Sprintf("the response (%d bytes)", declared)
	}
	return fmt.Errorf("%s exceeds the %d-byte fetch limit and was not retrieved. "+
		"Request a narrower resource — a specific page or section rather than a whole archive", size, f.maxBytes)
}

// contentKind is the three-way content-type branch: parse it, pass it
// through, or refuse it. Refusing is the point — feeding a JPEG to an
// HTML parser produces plausible-looking garbage, and garbage in model
// context is worse than an honest refusal.
type contentKind int

const (
	contentUnsupported contentKind = iota
	contentHTML
	contentText
)

func classifyContent(header string, body []byte) (contentKind, string) {
	mediaType := ""
	if header != "" {
		if mt, _, err := mime.ParseMediaType(header); err == nil {
			mediaType = strings.ToLower(strings.TrimSpace(mt))
		}
	}
	if mediaType == "" {
		// No usable header: sniff. http.DetectContentType reads at most
		// 512 bytes and always returns something.
		if mt, _, err := mime.ParseMediaType(http.DetectContentType(body)); err == nil {
			mediaType = strings.ToLower(mt)
		}
	}

	switch {
	case mediaType == "text/html", mediaType == "application/xhtml+xml":
		return contentHTML, mediaType
	case strings.HasPrefix(mediaType, "text/"),
		mediaType == "application/json",
		mediaType == "application/xml",
		strings.HasSuffix(mediaType, "+json"),
		strings.HasSuffix(mediaType, "+xml"):
		return contentText, mediaType
	default:
		return contentUnsupported, mediaType
	}
}

// renderPlain passes non-HTML text through verbatim under the same
// metadata header the HTML path uses. Markdown and plain text are
// already what the model wants to read; running them through an HTML
// parser would only damage them. (The user's own first test case was a
// `.md` URL.)
func renderPlain(body []byte, final *url.URL, mediaType string) []byte {
	var b bytes.Buffer
	writeMeta(&b, map[string]string{
		"url":          final.Redacted(),
		"content-type": mediaType,
	}, "url", "content-type")
	b.WriteString("\n")
	b.Write(bytes.TrimSpace(body))
	b.WriteString("\n")
	return b.Bytes()
}

// writeMeta emits the `key: value` header block, in the given key order,
// skipping empty values. Fixed order matters: a stable prefix is one
// fewer thing that differs between two otherwise identical fetches.
func writeMeta(b *bytes.Buffer, meta map[string]string, order ...string) {
	for _, k := range order {
		v := strings.TrimSpace(meta[k])
		if v == "" {
			continue
		}
		// Collapse to one line — a byline with an embedded newline would
		// break the block's shape.
		fmt.Fprintf(b, "%s: %s\n", k, tools.OneLine(v))
	}
}
