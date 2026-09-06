package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"personant/internal/clock"
	"personant/internal/tools"
	"personant/internal/version"
)

// Free-API citizenship — ONE mechanism for the whole package.
//
// CONVENTIONS.md carries this as a HARD REQUIREMENT (user ruling, 2026-08-05):
// personant complies with all etiquette and required behaviours of every
// free / volunteer / donor-funded API it consumes. The five binding
// consequences land here rather than in each tool, because five
// re-implementations of "be polite" is five places for one of them to be
// quietly wrong:
//
//  1. Each tool's doc comment records the SPECIFIC published obligations
//     of the service it talks to. That is the tool's job; this file is
//     the machinery.
//  2. A descriptive User-Agent carrying CONTACT INFORMATION, sourced from
//     config.toml `[user]` and injected at tool construction — never
//     compiled in. A service that REQUIRES contact refuses to run without
//     it (contactRequiredError), rather than falling back to anonymous.
//  3. Serial requests per host as a stated guarantee, plus any
//     service-specific minimum spacing, through the per-host gate table
//     below.
//  4. One honest retry on 429/503 carrying Retry-After, then a clean tool
//     error. Never hammer through.
//  5. Local per-tool query caps — quota.go, one counter per tool.
//
// The tools' own caps and the host gate are independent bounds: the cap
// stops a runaway loop from generating load, the gate shapes whatever
// load does get generated.

const (
	// uaProduct is the product token. It carries the SUBSTRATE version,
	// not the front-end one: this package is substrate-side, and pinning
	// the UA to the front-end line would churn the string a server
	// operator reads on every unrelated U/X patch bump.
	uaProduct = "personant/" + version.Substrate

	// uaDescriptor says what the client IS, for an operator reading their
	// access log. It survives from the pre-contact UA unchanged.
	uaDescriptor = "personal knowledge agent"

	// maxRetryAfter caps how long a Retry-After header can park a turn.
	// A service is allowed to ask for more; we are not willing to hold a
	// user's turn hostage for it, and the honest answer past this point is
	// a tool error saying so. 30s is already long enough that the model
	// will have to explain the pause.
	maxRetryAfter = 30 * time.Second

	// retryDrainBytes bounds the body we consume off a 429/503 before
	// reusing the connection. Enough to let keep-alive work, small enough
	// that a hostile error page costs nothing.
	retryDrainBytes = 4 << 10

	// apiQueryTimeout bounds one query against a credential-free JSON/XML
	// API. Shared by web.wikipedia, web.wiktionary, web.wikidata,
	// web.arxiv and web.crossref: one number, calibrated once — under
	// DefaultFetchTimeout and well under tools.DefaultTimeout, so a slow
	// endpoint surfaces as the tool's own message rather than dispatch's
	// generic one.
	apiQueryTimeout = 15 * time.Second

	// apiMaxResponseBytes caps the response read for those same tools. A
	// search API returning megabytes is a malfunction; buffering it
	// unbounded would make that malfunction ours.
	apiMaxResponseBytes = 4 << 20

	// apiErrorSnippetChars bounds the body echoed back on a non-200. See
	// apiStatusError for why a snippet is included on these endpoints when
	// the Exa provider deliberately includes none.
	apiErrorSnippetChars = 200
)

// Contact is the `[user]` identity from config.toml, injected at tool
// construction the way API keys are — internal/tools/web reads no config
// file and knows nothing about the substrate.
//
// The zero value means "not configured", which is a legitimate state for
// the services that merely PREFER contact and a refusal for the ones that
// require it.
type Contact struct {
	Name  string
	Email string
}

// Present reports whether both halves are set. A name with no address is
// not contact information — nobody can reach it — so partial counts as
// absent.
func (c Contact) Present() bool {
	return strings.TrimSpace(c.Name) != "" && strings.TrimSpace(c.Email) != ""
}

// userAgent builds this tool's User-Agent:
//
//	personant/<substrate> (personal knowledge agent; <name>; mailto:<email>) <tool>
//
// and, with no contact configured, the identity-only form:
//
//	personant/<substrate> (personal knowledge agent) <tool>
//
// The config-sourced halves go through tools.OneLine, which collapses all
// whitespace: a name carrying a newline would otherwise be header
// injection sourced from a hand-edited config file.
func (c Contact) userAgent(tool string) string {
	inside := uaDescriptor
	if c.Present() {
		inside += "; " + tools.OneLine(c.Name) + "; mailto:" + tools.OneLine(c.Email)
	}
	return uaProduct + " (" + inside + ") " + tool
}

// contactRequiredError is the refusal for a service whose policy REQUIRES
// contact information. It fires at invocation rather than at construction:
// the tool still exists and still describes itself, so the model learns
// why it cannot be used instead of silently never seeing it.
func contactRequiredError(tool, service, policy string) error {
	return fmt.Errorf("%s did not run: %s's %s REQUIRES contact information in every request, "+
		"and personant will not send anonymous requests to a donor-funded service. NO request was made — "+
		"this says nothing about whether the information exists. "+
		"Fix: set `name` and `email` under `[user]` in $PERSONANT_HOME/config.toml, or set git's global "+
		"user.name/user.email and re-run `personant init`, which populates the section from them",
		tool, service, policy)
}

// ---------- per-host politeness gate ----------

// hostSpacing is the per-host minimum-spacing table: the SERVICE-SPECIFIC
// half of consequence 3. Every host the package talks to is listed, and
// an entry of 0 is a decision rather than an omission — serial-only, no
// extra spacing, which is what the Wikimedia and Crossref policies ask
// for. politenessTableCoversEveryHost (in the tests) is the mechanical
// gate that a new host cannot be added without a ruling here.
//
// The serial-only guarantee is NOT in this table; it is unconditional and
// applies to every host, listed or not (an unlisted host gets a gate with
// zero spacing).
var hostSpacing = map[string]time.Duration{
	ArxivHost:      arxivMinSpacing, // arXiv's API manual asks ~3s between requests
	WikipediaHost:  0,               // Wikimedia: serial + contact, no stated spacing
	WiktionaryHost: 0,               // same family, same policy, its own host
	WikidataHost:   0,               // same family, same policy
	CrossrefHost:   0,               // polite pool: mailto in the UA, no stated spacing
	ExaHost:        0,               // commercial + metered; the local cap is the real bound
}

var (
	gatesMu sync.Mutex
	gates   = map[string]*hostGate{}
)

// gateFor returns the process-wide gate for a host, creating it on first
// use. Package-level state is the point: two tools reaching the same host
// must share one gate, or "serial per host" is a per-tool claim rather
// than a guarantee.
func gateFor(host string) *hostGate {
	host = strings.ToLower(host)
	gatesMu.Lock()
	defer gatesMu.Unlock()
	g, ok := gates[host]
	if !ok {
		g = newHostGate(hostSpacing[host])
		gates[host] = g
	}
	return g
}

// hostGate serializes requests to one host and enforces the minimum
// spacing between the COMPLETION of one and the start of the next.
// Spacing measured from completion, not from start, is the conservative
// reading: a slow response never lets the next request arrive early.
type hostGate struct {
	slot    chan struct{} // capacity 1: the in-flight token
	spacing time.Duration

	mu   sync.Mutex
	last clock.ProfilingTime // when the previous request finished
}

func newHostGate(spacing time.Duration) *hostGate {
	return &hostGate{slot: make(chan struct{}, 1), spacing: spacing}
}

// enter claims the host's single in-flight slot, then waits out whatever
// remains of the spacing interval. A cancelled context releases the slot
// and returns without having sent anything.
//
// The zero value of last reads as "very long ago" (clock.Since of a zero
// time is enormous), so the first request through a gate never waits.
func (g *hostGate) enter(ctx context.Context, sleep sleeper) error {
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	g.mu.Lock()
	wait := g.spacing - clock.Since(g.last)
	g.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	if err := sleep(ctx, wait); err != nil {
		g.release()
		return err
	}
	return nil
}

// release stamps the completion time and frees the slot.
func (g *hostGate) release() {
	g.mu.Lock()
	g.last = clock.Profiling()
	g.mu.Unlock()
	<-g.slot
}

// sleeper is the package's only wait seam. It exists so the tests can
// measure the spacing and Retry-After behaviour without spending the wall
// clock on it — a 3s sleep in a unit test is a 3s sleep in every future
// run of the suite.
type sleeper func(ctx context.Context, d time.Duration) error

// realSleep is the production sleeper: cancellable, because a user
// pressing Esc mid-turn must not have to wait out a 30s Retry-After.
func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------- the outbound path ----------

// politeness is what a tool holds instead of a bare *http.Client: the
// User-Agent it presents, the client, and the wait seam.
type politeness struct {
	ua     string
	client *http.Client
	sleep  sleeper
}

func newPoliteness(tool string, contact Contact, client *http.Client) *politeness {
	return &politeness{ua: contact.userAgent(tool), client: client, sleep: realSleep}
}

// do sends req under the host gate, with the package User-Agent and the
// one-retry Retry-After rule.
//
// OWNERSHIP: the gate is held until the caller CLOSES the response body —
// a request is not finished while its body is still being read. Every
// caller in this package defers a Close, which is required of an
// http.Response regardless; forgetting one wedges that host.
func (p *politeness) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	gate := gateFor(req.URL.Hostname())
	if err := gate.enter(ctx, p.sleep); err != nil {
		return nil, err
	}
	resp, err := p.attempt(ctx, req)
	if err != nil {
		gate.release()
		return nil, err
	}
	resp.Body = &gateBody{ReadCloser: resp.Body, release: gate.release}
	return resp, nil
}

// attempt performs the request and, on a 429/503 that states a
// Retry-After, ONE honest retry after the stated delay. No header means
// no retry: an unqualified 429 is the service declining to say when, and
// guessing an interval is exactly the hammering the rule forbids.
func (p *politeness) attempt(ctx context.Context, req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", p.ua)

	resp, err := p.client.Do(req)
	if err != nil || !retryableStatus(resp.StatusCode) {
		return resp, err
	}
	delay, ok := retryAfterDelay(resp.Header.Get("Retry-After"))
	if !ok {
		return resp, nil
	}
	retry, err := replayRequest(ctx, req)
	if err != nil {
		return resp, nil // a body we cannot replay: hand back the 429 as-is
	}
	drainClose(resp.Body)
	if err := p.sleep(ctx, delay); err != nil {
		return nil, err
	}
	return p.client.Do(retry)
}

func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable
}

// retryAfterDelay parses the header in both RFC 9110 forms — delta
// seconds and an HTTP-date — and clamps the result to [0, maxRetryAfter].
// An unparseable or absent value reports !ok, which means "do not retry".
func retryAfterDelay(header string) (time.Duration, bool) {
	h := strings.TrimSpace(header)
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return clampDelay(time.Duration(secs) * time.Second), true
	}
	when, err := http.ParseTime(h)
	if err != nil {
		return 0, false
	}
	// Real wall time, routed through internal/clock so this package keeps
	// the no-direct-clock-reads invariant.
	now := time.Unix(0, clock.Profiling().UnixNano())
	return clampDelay(when.Sub(now)), true
}

func clampDelay(d time.Duration) time.Duration {
	return min(max(d, 0), maxRetryAfter)
}

// replayRequest rebuilds a request for the single retry. A GET (every
// citizenship-relevant call in this package) clones trivially; a body-
// bearing request needs GetBody, which http.NewRequest installs for the
// in-memory reader types.
func replayRequest(ctx context.Context, req *http.Request) (*http.Request, error) {
	clone := req.Clone(ctx)
	if req.Body == nil || req.Body == http.NoBody {
		return clone, nil
	}
	if req.GetBody == nil {
		return nil, fmt.Errorf("request body cannot be replayed")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	clone.Body = body
	return clone, nil
}

func drainClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, retryDrainBytes))
	_ = body.Close()
}

// gateBody ties the host gate's lifetime to the response body's. Close is
// idempotent through the once, because a double Close on a response body
// is legal and must not free the slot twice.
type gateBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *gateBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

// ---------- the shared credential-free API GET ----------

// apiResponse is a fully-read, bounded reply. The five credential-free
// query tools (wikipedia, wiktionary, wikidata, arxiv, crossref) all want
// exactly this: a status to branch on and a capped body to parse.
type apiResponse struct {
	Status     string
	StatusCode int
	Body       []byte
}

func (r apiResponse) ok() bool { return r.StatusCode >= 200 && r.StatusCode <= 299 }

// getAPI performs one GET against a credential-free endpoint and reads a
// bounded body.
//
// Query parameters are attached with url.Values, never by string
// formatting — a search term is arbitrary model text and hand-built query
// strings are how it stops being a search term.
//
// It is deliberately NOT used by the Exa provider: that request carries a
// credential, which changes both the error-echo rules (§8.2.1) and the
// method.
func (p *politeness) getAPI(ctx context.Context, endpoint string, q url.Values, accept string) (apiResponse, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return apiResponse{}, fmt.Errorf("endpoint %q is not parseable: %w", endpoint, err)
	}
	u.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(ctx, apiQueryTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return apiResponse{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", accept)

	resp, err := p.do(ctx, req)
	if err != nil {
		return apiResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, apiMaxResponseBytes))
	if err != nil {
		return apiResponse{}, fmt.Errorf("read response: %w", err)
	}
	return apiResponse{Status: resp.Status, StatusCode: resp.StatusCode, Body: raw}, nil
}

// apiStatusError reports a non-200 with the status AND a bounded snippet
// of the body.
//
// The Exa provider deliberately echoes NO body, and the reason is §8.2.1:
// its request carries an API key, and an error echo is a documented way
// for a submitted credential to come back out and land in model context
// and the event log. These requests carry no credential — no key, no
// cookie, no auth header ever reaches these endpoints — so that hazard
// does not exist, while the body ("invalid limit", "unknown parameter")
// is genuinely the actionable half of the error. The snippet is collapsed
// to one line and clipped, so a long HTML error page cannot become the
// tool result.
//
// what names the operation in the model's terms ("the Wikipedia search"),
// so one sentence shape serves every tool.
func apiStatusError(what, status string, body []byte) error {
	detail := clip(tools.OneLine(stripHTML(string(body))), apiErrorSnippetChars)
	if detail == "" {
		return fmt.Errorf("%s returned HTTP %s and NO search happened", what, status)
	}
	return fmt.Errorf("%s returned HTTP %s and NO search happened: %s", what, status, detail)
}

// transportError is the "the request never completed" half of the
// trichotomy, worded so the model cannot read it as "nothing exists".
func transportError(what string, err error) error {
	return fmt.Errorf("%s could not be performed (%w) — NO search happened, so this says nothing about "+
		"whether a result exists. Answer from what you have and say the lookup was unavailable", what, err)
}

// malformedError is the third failure: a 200 whose body does not parse.
func malformedError(what string, n int) error {
	return fmt.Errorf("%s returned a malformed response body (%d bytes) — the search may not have run", what, n)
}

// noResultsFor is the EMPTY half of the trichotomy, shared by every
// query tool in the package. It is a SUCCESSFUL result that states its
// success explicitly: a model told the lookup failed goes looking
// elsewhere, a model told the topic does not exist says so confidently,
// and the two must be distinguishable from the text alone.
func noResultsFor(what, q, advice string) string {
	return fmt.Sprintf("%s ran successfully and matched 0 results for %q.\n\n"+
		"This is an EMPTY RESULT, not a failure: the query reached the service and its index returned nothing. "+
		"%s\n", what, q, advice)
}
