package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"personant/internal/tools"
	"personant/internal/version"
)

// The free-API citizenship layer. Everything runs against httptest and a
// FAKE sleeper — no live network, and no test in this package ever spends
// a real second waiting: a 3s sleep in a unit test is a 3s sleep in every
// future run of the suite.

// testContact is the configured `[user]` identity every test in the
// package uses. It is a documented reserved-for-documentation domain, so
// a stray live request could not reach a real mailbox.
var testContact = Contact{Name: "Test Person", Email: "test@example.com"}

// fakeSleeper records what it was asked to wait for and returns
// immediately.
type fakeSleeper struct {
	mu     sync.Mutex
	slept  []time.Duration
	cancel error // returned instead of nil, for the cancellation cases
}

func (f *fakeSleeper) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slept = append(f.slept, d)
	return f.cancel
}

func (f *fakeSleeper) waits() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.slept...)
}

// apiServer stands in for a credential-free query API, recording what the
// tool actually sent so the request half of each contract is measured
// rather than assumed. Shared by the wikipedia / wikidata / arxiv /
// crossref suites — one fake, one set of accessors.
type apiServer struct {
	*httptest.Server
	mu     sync.Mutex
	query  url.Values
	agent  string
	calls  int
	status int
	body   string
}

func newAPIServer(t *testing.T, status int, body string) *apiServer {
	t.Helper()
	s := &apiServer{status: status, body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls++
		s.query = r.URL.Query()
		s.agent = r.Header.Get("User-Agent")
		s.mu.Unlock()
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *apiServer) param(k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query.Get(k)
}

func (s *apiServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *apiServer) userAgent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agent
}

// call invokes a handler on turn 1 with the given arguments.
func call(t *testing.T, h tools.Handler, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := h(tools.ContextWithTurn(context.Background(), 1), raw)
	return string(out), err
}

// h2err discards a handler's bytes and keeps its error.
func h2err(_ []byte, err error) error { return err }

// assertToolShape holds a tool to the §6.2.7 ruling: tier 0, never
// mutating (tools.Registry mechanically refuses a mutating tool, but the
// declaration is the thing under test here).
func assertToolShape(t *testing.T, tool tools.Tool, name string) tools.Handler {
	t.Helper()
	if tool.Spec.Name != name {
		t.Fatalf("tool name = %q, want %q", tool.Spec.Name, name)
	}
	if tool.Tier != tools.TierSilent || tool.Mutates {
		t.Fatalf("%s must be tier 0 and non-mutating; got tier=%d mutates=%v", name, tool.Tier, tool.Mutates)
	}
	return tool.Handler
}

// TestWikimediaContactRequired — consequence 2, on the two tools whose
// service REQUIRES contact. The refusal must name the policy, say no
// request was made, and name the fix; and the endpoint must see nothing.
func TestWikimediaContactRequired(t *testing.T) {
	pinDay(t, "2026-08-05")
	for _, tc := range []struct {
		name     string
		build    func(endpoint string, c Contact) tools.Tool
		toolName string
	}{
		{"wikipedia", func(e string, c Contact) tools.Tool {
			return NewWikipediaTool(WikipediaConfig{Contact: c, Endpoint: e})
		}, ToolNameWikipedia},
		{"wikidata", func(e string, c Contact) tools.Tool {
			return NewWikidataTool(WikidataConfig{Contact: c, Endpoint: e})
		}, ToolNameWikidata},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAPIServer(t, http.StatusOK, `{"pages":[]}`)
			// No contact at all, and each half on its own: a name nobody can
			// write to is not contact information.
			for _, c := range []Contact{{}, {Name: testContact.Name}, {Email: testContact.Email}} {
				_, err := call(t, tc.build(s.URL, c).Handler, map[string]any{"q": "anything"})
				if err == nil {
					t.Fatal("an anonymous request to a Wikimedia service was permitted")
				}
				for _, want := range []string{
					tc.toolName, "User-Agent policy", "REQUIRES contact",
					"NO request was made", "[user]", "config.toml", "personant init",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal is missing %q: %v", want, err)
					}
				}
			}
			if s.count() != 0 {
				t.Errorf("a contact-less call reached the endpoint %d time(s)", s.count())
			}
		})
	}
}

// TestUserAgentShape pins both forms of the User-Agent. The contact-
// bearing one is what the Wikimedia policy demands; the identity-only one
// is what an unconfigured install sends to the services that merely
// prefer contact.
func TestUserAgentShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		contact Contact
		want    string
	}{
		{
			"with contact",
			testContact,
			"personant/" + version.Substrate + " (personal knowledge agent; Test Person; mailto:test@example.com) web.arxiv",
		},
		{
			"no contact at all",
			Contact{},
			"personant/" + version.Substrate + " (personal knowledge agent) web.arxiv",
		},
		{
			// Half an identity is not contact information: nobody can write
			// to a name. It must degrade to the anonymous form rather than
			// send a half-truth.
			"name only",
			Contact{Name: "Test Person"},
			"personant/" + version.Substrate + " (personal knowledge agent) web.arxiv",
		},
		{
			// A hand-edited config could carry a newline; a User-Agent
			// carrying one is header injection.
			"whitespace is collapsed",
			Contact{Name: "Test\nPerson", Email: " test@example.com "},
			"personant/" + version.Substrate + " (personal knowledge agent; Test Person; mailto:test@example.com) web.arxiv",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.contact.userAgent(ToolNameArxiv); got != tc.want {
				t.Errorf("userAgent = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHostSpacingTableCoversEveryHost is the mechanical gate on
// consequence 3: a new host cannot be added to the package without a
// spacing ruling. An entry of 0 is a legitimate ruling (serial-only); an
// ABSENT entry is an oversight.
func TestHostSpacingTableCoversEveryHost(t *testing.T) {
	for _, host := range []string{WikipediaHost, WikidataHost, ArxivHost, CrossrefHost, ExaHost} {
		if _, ok := hostSpacing[host]; !ok {
			t.Errorf("host %q has no entry in hostSpacing — every host this package reaches needs a ruling, "+
				"even if the ruling is 0 (serial-only)", host)
		}
	}
	if hostSpacing[ArxivHost] != 3*time.Second {
		t.Errorf("arXiv spacing = %v, want 3s — their API manual asks for one request every three seconds",
			hostSpacing[ArxivHost])
	}
	// And the endpoints must actually point at the hosts the table keys on,
	// or the gate silently applies to nothing.
	for _, tc := range []struct{ endpoint, host string }{
		{wikipediaEndpoint, WikipediaHost},
		{wikidataEndpoint, WikidataHost},
		{arxivEndpoint, ArxivHost},
		{crossrefEndpoint, CrossrefHost},
		{ExaEndpoint, ExaHost},
	} {
		u, err := url.Parse(tc.endpoint)
		if err != nil {
			t.Fatalf("endpoint %q: %v", tc.endpoint, err)
		}
		if u.Hostname() != tc.host {
			t.Errorf("endpoint %q resolves to host %q, but the table keys on %q", tc.endpoint, u.Hostname(), tc.host)
		}
	}
}

// TestHostGateSpacing — the second request through a gate waits out the
// remainder of the interval, measured from the previous COMPLETION.
func TestHostGateSpacing(t *testing.T) {
	fake := &fakeSleeper{}
	g := newHostGate(3 * time.Second)
	ctx := context.Background()

	if err := g.enter(ctx, fake.sleep); err != nil {
		t.Fatalf("first enter: %v", err)
	}
	if got := fake.waits(); len(got) != 0 {
		t.Errorf("the first request through a gate waited %v; it must not wait at all", got)
	}
	g.release()

	if err := g.enter(ctx, fake.sleep); err != nil {
		t.Fatalf("second enter: %v", err)
	}
	g.release()

	waits := fake.waits()
	if len(waits) != 1 {
		t.Fatalf("second enter produced %d waits, want 1: %v", len(waits), waits)
	}
	// Some real time elapsed between release and enter, so the remainder is
	// slightly under the full interval. The property is "close to 3s", not
	// "exactly 3s" — a test that demanded exactness would measure the
	// machine, not the gate.
	if waits[0] > 3*time.Second || waits[0] < 2*time.Second {
		t.Errorf("waited %v, want the remainder of a 3s interval", waits[0])
	}
}

// TestHostGateSerializes — one in flight per host, as a stated guarantee.
// A zero-spacing gate still serializes: spacing and serialization are
// independent halves of consequence 3.
func TestHostGateSerializes(t *testing.T) {
	g := newHostGate(0)
	ctx := context.Background()
	fake := &fakeSleeper{}

	if err := g.enter(ctx, fake.sleep); err != nil {
		t.Fatalf("enter: %v", err)
	}

	entered := make(chan struct{})
	go func() {
		_ = g.enter(ctx, fake.sleep)
		close(entered)
		g.release()
	}()

	select {
	case <-entered:
		t.Fatal("a second request entered the gate while the first was in flight")
	case <-time.After(20 * time.Millisecond):
	}

	g.release()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the second request never entered after the first released")
	}
}

// TestHostGateCancellation — a cancelled context does not leave the slot
// claimed. A leaked slot wedges the host for the rest of the process.
func TestHostGateCancellation(t *testing.T) {
	g := newHostGate(3 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := g.enter(ctx, (&fakeSleeper{}).sleep); err != nil {
		t.Fatalf("first enter: %v", err)
	}
	g.release()

	// The spacing wait is where a cancellation lands, so make the sleeper
	// report one.
	refusing := &fakeSleeper{cancel: context.Canceled}
	if err := g.enter(ctx, refusing.sleep); err == nil {
		t.Fatal("a cancelled wait entered the gate anyway")
	}

	// The slot must be free: a third attempt (with the spacing satisfied by
	// a fake that returns nil) proceeds.
	done := make(chan error, 1)
	go func() { done <- g.enter(ctx, (&fakeSleeper{}).sleep) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("enter after a cancelled wait: %v", err)
		}
		g.release()
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled wait leaked the in-flight slot")
	}
}

// retryServer counts attempts and answers the first N with a status plus
// an optional Retry-After.
type retryServer struct {
	*httptest.Server
	mu         sync.Mutex
	attempts   int
	failFirst  int
	failStatus int
	retryAfter string
	userAgents []string
}

func newRetryServer(t *testing.T, failFirst, failStatus int, retryAfter string) *retryServer {
	t.Helper()
	rs := &retryServer{failFirst: failFirst, failStatus: failStatus, retryAfter: retryAfter}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.attempts++
		n := rs.attempts
		rs.userAgents = append(rs.userAgents, r.Header.Get("User-Agent"))
		rs.mu.Unlock()
		if n <= rs.failFirst {
			if rs.retryAfter != "" {
				w.Header().Set("Retry-After", rs.retryAfter)
			}
			w.WriteHeader(rs.failStatus)
			_, _ = w.Write([]byte("slow down"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *retryServer) count() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.attempts
}

// TestRetryAfter — the whole of consequence 4 in one table: exactly ONE
// retry when the service says when, no retry when it does not, and the
// cap on how long a service may park a turn.
func TestRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		retryAfter  string
		wantAttempt int
		wantSlept   []time.Duration
		wantStatus  int
	}{
		{"429 with seconds retries once", http.StatusTooManyRequests, "2", 2, []time.Duration{2 * time.Second}, http.StatusOK},
		{"503 with seconds retries once", http.StatusServiceUnavailable, "5", 2, []time.Duration{5 * time.Second}, http.StatusOK},
		{"no header means no retry", http.StatusTooManyRequests, "", 1, nil, http.StatusTooManyRequests},
		{"an unparseable header means no retry", http.StatusTooManyRequests, "soon", 1, nil, http.StatusTooManyRequests},
		{"a huge delay is capped", http.StatusTooManyRequests, "86400", 2, []time.Duration{maxRetryAfter}, http.StatusOK},
		{"a negative delay clamps to zero", http.StatusTooManyRequests, "-5", 2, []time.Duration{0}, http.StatusOK},
		{"a 404 is not retried at all", http.StatusNotFound, "2", 1, nil, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// failFirst=1 so a single retry is enough to succeed; the
			// no-retry cases simply never get there.
			rs := newRetryServer(t, 1, tc.status, tc.retryAfter)
			fake := &fakeSleeper{}
			p := newPoliteness(ToolNameFetch, testContact, rs.Client())
			p.sleep = fake.sleep

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rs.URL, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := p.do(context.Background(), req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			_ = resp.Body.Close()

			if got := rs.count(); got != tc.wantAttempt {
				t.Errorf("server saw %d attempt(s), want %d", got, tc.wantAttempt)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("final status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			got := fake.waits()
			if len(got) != len(tc.wantSlept) {
				t.Fatalf("slept %v, want %v", got, tc.wantSlept)
			}
			for i := range got {
				if got[i] != tc.wantSlept[i] {
					t.Errorf("wait %d = %v, want %v", i, got[i], tc.wantSlept[i])
				}
			}
		})
	}
}

// TestRetryAfterStopsAtOne — a service that keeps saying 429 gets exactly
// two requests and then a clean tool error. "Never hammer through" is the
// rule; a retry LOOP would be the violation the rule names.
func TestRetryAfterStopsAtOne(t *testing.T) {
	rs := newRetryServer(t, 99, http.StatusTooManyRequests, "1")
	fake := &fakeSleeper{}
	p := newPoliteness(ToolNameFetch, testContact, rs.Client())
	p.sleep = fake.sleep

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, rs.URL, nil)
	resp, err := p.do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()

	if rs.count() != 2 {
		t.Errorf("server saw %d attempts; the rule is ONE retry, then a clean error", rs.count())
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d; the second 429 must be handed back, not retried again", resp.StatusCode)
	}
}

// TestRetryAfterHTTPDate — RFC 9110 allows an HTTP-date, and a service
// that uses it must be honoured rather than silently not retried.
func TestRetryAfterHTTPDate(t *testing.T) {
	when := time.Now().UTC().Add(4 * time.Second).Format(http.TimeFormat)
	d, ok := retryAfterDelay(when)
	if !ok {
		t.Fatalf("an HTTP-date Retry-After was not parsed: %q", when)
	}
	// Second-granularity formatting plus the elapsed test time means the
	// value lands in a small band, not on a point.
	if d < 2*time.Second || d > 5*time.Second {
		t.Errorf("delay = %v, want roughly 4s", d)
	}
	if _, ok := retryAfterDelay("  "); ok {
		t.Error("a blank Retry-After was treated as a delay")
	}
}

// TestPolitenessSetsUserAgent — every request through the layer carries
// the agent, including the retry.
func TestPolitenessSetsUserAgent(t *testing.T) {
	rs := newRetryServer(t, 1, http.StatusServiceUnavailable, "1")
	p := newPoliteness(ToolNameCrossref, testContact, rs.Client())
	p.sleep = (&fakeSleeper{}).sleep

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, rs.URL, nil)
	resp, err := p.do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	_ = resp.Body.Close()

	rs.mu.Lock()
	agents := append([]string(nil), rs.userAgents...)
	rs.mu.Unlock()
	if len(agents) != 2 {
		t.Fatalf("saw %d requests, want 2", len(agents))
	}
	for i, ua := range agents {
		if !strings.Contains(ua, "mailto:"+testContact.Email) || !strings.Contains(ua, ToolNameCrossref) {
			t.Errorf("request %d carried User-Agent %q, missing the contact or the tool name", i+1, ua)
		}
	}
}

// TestGateReleasedOnBodyClose — the gate is held until the body closes,
// and a second Close does not free it twice. A double release would panic
// on the channel receive or, worse, let two requests in flight.
func TestGateReleasedOnBodyClose(t *testing.T) {
	rs := newRetryServer(t, 0, http.StatusOK, "")
	p := newPoliteness(ToolNameFetch, testContact, rs.Client())
	p.sleep = (&fakeSleeper{}).sleep

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, rs.URL, nil)
	resp, err := p.do(context.Background(), req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	// The host must be usable again, promptly.
	done := make(chan error, 1)
	go func() {
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, rs.URL, nil)
		resp2, err := p.do(context.Background(), r)
		if err == nil {
			_ = resp2.Body.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second request: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the host gate was never released")
	}
}
