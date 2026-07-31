package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"personant/internal/testsupport"
)

// ONE live probe for web.fetch, following the house convention
// (internal/model/toolstream_live_test.go): it ALWAYS COMPILES and gates
// EXECUTION on PERSONANT_LIVE_TESTS, so bare `make test` compiles and
// skips it without touching the network.
//
// It issues exactly ONE request. Everything mechanical — extraction
// quality, empty-shell detection, the caps, the refusals — is measured
// against httptest fixtures in fetch_test.go, which is both faster and
// stricter. What only a live fetch can validate is that the pipeline
// survives contact with a real page: real charset headers, real
// compression, real markup a fixture author would not have thought to
// write.
//
// The target is a server-rendered, stable, high-traffic page. A page
// that changes shape breaks this probe for a reason that is not
// personant's fault, which is how a live test earns a reputation for
// crying wolf.
const liveFetchURL = "https://en.wikipedia.org/wiki/Sourdough"

func TestWebFetch_Live(t *testing.T) {
	testsupport.RequireLive(t)

	tool := NewFetchTool(FetchConfig{})
	args, err := json.Marshal(map[string]string{"url": liveFetchURL})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	out, err := tool.Handler(context.Background(), args)
	testsupport.FailOnErr(t, "web.fetch "+liveFetchURL, err)

	result := string(out)
	t.Logf("web.fetch returned %d bytes; head:\n%s", len(result), result[:min(600, len(result))])

	if strings.Contains(result, "status: no-content") {
		t.Fatalf("a server-rendered article was reported as an empty shell:\n%s", result)
	}
	if !strings.Contains(result, "url: "+liveFetchURL) {
		t.Errorf("result does not carry the fetched url")
	}
	if !strings.Contains(strings.ToLower(result), "title:") {
		t.Errorf("no title extracted from a live article")
	}
	// Real extraction, not a truncated document: the body must carry the
	// subject matter and must NOT carry raw markup.
	if !strings.Contains(strings.ToLower(result), "sourdough") {
		t.Errorf("extracted body does not mention the article's subject")
	}
	if strings.Contains(result, "<div") || strings.Contains(result, "<script") {
		t.Errorf("raw HTML survived into the Markdown output")
	}
	if len(result) < 2000 {
		t.Errorf("extracted only %d bytes from a full article — extraction is under-reaching", len(result))
	}
}
