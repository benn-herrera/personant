package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Exa is the first SearchProvider implementation (user ruling). It is
// one HTTP POST behind the interface, so a different backend is a new
// file rather than a change to web.search.

const (
	// ExaEndpoint is Exa's search API.
	ExaEndpoint = "https://api.exa.ai/search"

	// exaTimeout bounds one query. Under DefaultFetchTimeout and well
	// under tools.DefaultTimeout, so a slow backend surfaces as this
	// tool's own message.
	exaTimeout = 15 * time.Second

	// exaTextChars is how much page text Exa returns per result, which
	// becomes the snippet. `text` (a plain extract) is deliberate over
	// `highlights` (LLM-generated): it is the cheaper product on a
	// metered account, and the snippet only has to be good enough to
	// choose a URL to fetch.
	exaTextChars = 500

	// exaMaxResponseBytes caps the response read. A search API returning
	// megabytes is a malfunction; buffering it unbounded would make that
	// malfunction ours.
	exaMaxResponseBytes = 8 << 20
)

// ExaProvider queries Exa. The API key is held here and NOWHERE else:
// it is never logged, never formatted into an error, and never part of
// any value that reaches model context (SPEC §6.4 / §8.2.1).
type ExaProvider struct {
	key      string
	endpoint string
	client   *http.Client
}

// NewExaProvider builds the provider. endpoint may be empty for
// ExaEndpoint; it is a parameter rather than a package variable so a test
// can point at an httptest server without mutating global state. A
// missing key is an error — a keyless Exa provider can only produce
// 401s, and the correct handling of "no key" is to not register the tool
// at all.
func NewExaProvider(apiKey, endpoint string, client *http.Client) (*ExaProvider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("web: exa provider needs an API key")
	}
	if endpoint == "" {
		endpoint = ExaEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: exaTimeout}
	}
	return &ExaProvider{key: strings.TrimSpace(apiKey), endpoint: endpoint, client: client}, nil
}

type exaRequest struct {
	Query      string      `json:"query"`
	NumResults int         `json:"numResults"`
	Contents   exaContents `json:"contents"`
}

type exaContents struct {
	Text exaTextOptions `json:"text"`
}

type exaTextOptions struct {
	MaxCharacters int `json:"maxCharacters"`
}

type exaResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Text          string `json:"text"`
		PublishedDate string `json:"publishedDate"`
	} `json:"results"`
}

// Search implements SearchProvider. Every return path honours the
// interface's error/empty contract: a transport, status or decode
// failure is an error (the search did not happen), and a well-formed
// response with no results is (nil, nil) (the search happened and
// matched nothing).
func (p *ExaProvider) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = DefaultSearchResults
	}
	body, err := json.Marshal(exaRequest{
		Query:      query,
		NumResults: limit,
		Contents:   exaContents{Text: exaTextOptions{MaxCharacters: exaTextChars}},
	})
	if err != nil {
		return nil, fmt.Errorf("exa: encode request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, exaTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("exa: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", p.key)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exa: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Status only, never the response body: an error echo could
		// contain the submitted credential, and this string travels into
		// the event log and model context.
		return nil, fmt.Errorf("exa: search backend returned HTTP %s", resp.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, exaMaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("exa: read response: %w", err)
	}
	var decoded exaResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("exa: malformed response body (%d bytes)", len(raw))
	}

	out := make([]SearchResult, 0, len(decoded.Results))
	for _, r := range decoded.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue // a result with no URL is not actionable
		}
		out = append(out, SearchResult{
			Title:     r.Title,
			URL:       r.URL,
			Snippet:   r.Text,
			Published: r.PublishedDate,
		})
	}
	return out, nil
}
