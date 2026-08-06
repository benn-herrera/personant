package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exa provider tests: the wire shape, and the contract terms the tool
// depends on (error means "did not happen", empty means "happened and
// matched nothing"). The key must not appear in anything the provider
// hands back.

const exaTestKey = "exa-test-key-DO-NOT-LEAK"

func TestExaSearchRequestAndResponse(t *testing.T) {
	var gotKey, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"results":[
		  {"title":"A","url":"https://example.com/a","text":"first snippet","publishedDate":"2026-01-02T00:00:00Z"},
		  {"title":"B","url":"https://example.com/b","text":"second snippet"},
		  {"title":"no url","url":"","text":"dropped"}
		]}`))
	}))
	defer srv.Close()

	p, err := NewExaProvider(exaTestKey, srv.URL+"/search", testContact, srv.Client())
	if err != nil {
		t.Fatalf("NewExaProvider: %v", err)
	}
	results, err := p.Search(context.Background(), "hydration", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if gotKey != exaTestKey {
		t.Errorf("x-api-key header = %q", gotKey)
	}
	if gotPath != "/search" {
		t.Errorf("path = %q, want /search", gotPath)
	}
	if gotBody["query"] != "hydration" {
		t.Errorf("query = %v", gotBody["query"])
	}
	if n, _ := gotBody["numResults"].(float64); int(n) != 3 {
		t.Errorf("numResults = %v, want 3", gotBody["numResults"])
	}

	// A result with no URL is dropped: the model cannot act on it, and a
	// snippet with nowhere to go is context spent for nothing.
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (the URL-less one dropped): %+v", len(results), results)
	}
	if results[0].Title != "A" || results[0].URL != "https://example.com/a" ||
		results[0].Snippet != "first snippet" || results[0].Published == "" {
		t.Errorf("first result mis-decoded: %+v", results[0])
	}
}

// TestExaZeroResultsIsNotAnError — the contract's empty half, at the
// provider boundary where it originates.
func TestExaZeroResultsIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	p, err := NewExaProvider(exaTestKey, srv.URL, testContact, srv.Client())
	if err != nil {
		t.Fatalf("NewExaProvider: %v", err)
	}
	results, err := p.Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("an empty index must not be an error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

// TestExaFailuresAreErrorsAndLeakNoKey — every failure path is an error
// (the search did not happen), and none of them carries key material,
// which is load-bearing because these strings reach the event log and
// model context (SPEC §6.4 / §8.2.1).
func TestExaFailuresAreErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"unauthorized", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			// An error echo that quotes the submitted credential back is
			// exactly why the body is never included in our error.
			_, _ = w.Write([]byte(`{"error":"invalid key ` + exaTestKey + `"}`))
		}},
		{"server error", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`upstream on fire`))
		}},
		{"malformed body", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"results": not json`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			p, err := NewExaProvider(exaTestKey, srv.URL, testContact, srv.Client())
			if err != nil {
				t.Fatalf("NewExaProvider: %v", err)
			}
			results, err := p.Search(context.Background(), "q", 5)
			if err == nil {
				t.Fatalf("failure returned no error (results=%+v) — an empty-on-failure "+
					"result is the exact behavior this tool is designed against", results)
			}
			if results != nil {
				t.Errorf("failure returned results: %+v", results)
			}
			if strings.Contains(err.Error(), exaTestKey) {
				t.Errorf("error text carries key material: %v", err)
			}
		})
	}
}

func TestExaRequiresKey(t *testing.T) {
	if _, err := NewExaProvider("   ", "", testContact, nil); err == nil {
		t.Error("a keyless exa provider was built; the correct handling of no key is no tool")
	}
}
