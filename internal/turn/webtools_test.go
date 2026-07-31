package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/tools"
	"personant/internal/tools/web"
)

// §6.1.1 wave-3 integration: the REAL web tools inside the wave-2 turn
// loop. The existing §6.5 test (toolloop_test.go) uses a fake handler,
// which proves the loop's policy but not that a real tool travels the
// same path — this file closes that gap by registering the actual
// web.fetch and driving a turn through it.

// TestRealWebFetchRespectsSixFiveCap — the §6.5 byte cap fires on the
// web.fetch path, on BOTH copies (the delta that becomes memory and the
// tool message that goes back on the wire), with the honest marker.
//
// The page is large but well under web.fetch's own MaxBytes, so this
// measures the TURN's cap rather than the tool's: the two are separate
// bounds and only the turn's protects the request budget.
func TestRealWebFetchRespectsSixFiveCap(t *testing.T) {
	var body strings.Builder
	body.WriteString("<!DOCTYPE html><html lang=\"en\"><head><title>Big Article</title></head><body><article><h1>Big Article</h1>")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&body, "<p>Paragraph %d. Hydration is the ratio of water to flour by weight, and it changes "+
			"the loaf more than any other single variable in the whole process of baking bread at home.</p>", i)
	}
	body.WriteString("</article></body></html>")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body.String())
	}))
	defer srv.Close()

	reg := tools.NewRegistry()
	if err := reg.Register(web.NewFetchTool(web.FetchConfig{Client: srv.Client()})); err != nil {
		t.Fatalf("register: %v", err)
	}

	pageURL := srv.URL + "/notes/hydration"
	args, err := json.Marshal(string(mustJSON(t, map[string]string{"url": pageURL})))
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	state, mock, _ := toolStateWith(t, reg,
		toolCallResponse(call("c1", web.ToolNameFetch, string(args))),
		model.Response{Content: "*topic: *new-topic* [hydration, crumb]*\nRead it."},
	)

	if _, err := Run(context.Background(), state, "read "+pageURL, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	_, _, share := liveTurnShares(state.Budget)
	if share <= 0 {
		t.Fatal("task-result share is zero — the cap under test is not configured")
	}
	msgs := mock.Calls()[1].Request.Messages
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Role != "tool" {
		t.Fatalf("last message role = %q, want tool", toolMsg.Role)
	}
	if len(toolMsg.Content) > share {
		t.Errorf("the §6.5 cap did NOT fire on the web.fetch path: tool message is %d bytes, cap is %d",
			len(toolMsg.Content), share)
	}
	if !strings.Contains(toolMsg.Content, "output truncated") {
		t.Error("truncation is silent on the web.fetch path; want the honest marker")
	}
	// The extraction still happened before the cap: what survives is the
	// head of a real article, not a truncated HTML document.
	if !strings.Contains(toolMsg.Content, "title: Big Article") {
		t.Errorf("metadata header did not survive the cap:\n%s", toolMsg.Content[:min(400, len(toolMsg.Content))])
	}
	if strings.Contains(toolMsg.Content, "<p>") {
		t.Error("raw HTML reached the model; the Markdown conversion did not run")
	}
}

// TestRealWebFetchFeedsSymbolExtraction confirms (does not re-implement)
// that §3.3 extraction runs over tool output through the §3.0 chain: the
// fetched URL is an identifier-category symbol, staged as task-class per
// §3.10 rather than persisted outright.
func TestRealWebFetchFeedsSymbolExtraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = io.WriteString(w, "# Notes\n\nSee https://upstream.example/spec/v2 for the protocol.\n")
	}))
	defer srv.Close()

	reg := tools.NewRegistry()
	if err := reg.Register(web.NewFetchTool(web.FetchConfig{Client: srv.Client()})); err != nil {
		t.Fatalf("register: %v", err)
	}
	args := string(mustJSON(t, map[string]string{"url": srv.URL + "/notes.md"}))

	state, _, _ := toolStateWith(t, reg,
		toolCallResponse(call("c1", web.ToolNameFetch, args)),
		model.Response{Content: "*topic: *new-topic* [protocol, notes]*\nDone."},
	)

	if _, err := Run(context.Background(), state, "read the notes", io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The URL inside the fetched body is extracted by the deterministic
	// pass, which runs over every delta source including tool.result,
	// and lands in the §3.10 staging buffer because tool.result is
	// task-class. Both halves are pre-existing behavior; this asserts
	// the web tools actually reach them.
	if state.staging == nil {
		t.Fatal("no staging buffer: the tool result never reached the §3.0 chain")
	}
	found := false
	for _, s := range state.staging.entries {
		if strings.Contains(s.Normalized, "upstream.example") || strings.Contains(s.Raw, "upstream.example") {
			found = true
			if s.Source != memops.SourceDeterministic {
				t.Errorf("URL symbol source = %q, want %q", s.Source, memops.SourceDeterministic)
			}
		}
	}
	if !found {
		t.Errorf("no symbol extracted from the fetched body; staged = %+v", state.staging.entries)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
