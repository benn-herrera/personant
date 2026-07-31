package chat

import (
	"context"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/testsupport"
)

// ONE live probe for web.search, and it lives here rather than in
// internal/tools/web because THIS is the path the user actually gets:
// the real providers.toml pool → selectSearchProvider → one query. A
// probe that hand-built an ExaProvider would validate the HTTP call and
// skip the selection logic that decides whether the tool exists at all.
//
// Exactly ONE query. The Exa free tier is a finite budget and the search
// is metered, so this is deliberately not a sweep: one query proves the
// credential resolves, the wire shape is right, and results come back
// ranked. Everything else — the found/empty/failed trichotomy, the caps,
// the selection failures — is measured against fakes in search_test.go
// and tools_test.go, for free and more strictly.
const liveSearchQuery = "sourdough hydration crumb structure"

func TestWebSearch_Live(t *testing.T) {
	testsupport.RequireLive(t)

	// Read the REAL home's pool. A missing or absent key is a SKIP, not
	// a failure: an unconfigured search backend is a legitimate state
	// (web.search simply is not offered), and failing the suite over one
	// would punish every developer who has no Exa account.
	paths, err := store.ResolvePaths()
	if err != nil {
		t.Skipf("cannot resolve the personant home: %v", err)
	}
	providers, _, err := store.LoadProviders(paths.Providers)
	if err != nil {
		t.Skipf("cannot read %s: %v", paths.Providers, err)
	}
	cfg, err := store.LoadConfig(paths.Config)
	if err != nil {
		t.Skipf("cannot read %s: %v", paths.Config, err)
	}
	if len(providers.OfKind(memops.ProviderTypeSearch)) == 0 {
		t.Skip("no `type = \"search\"` provider configured — web.search is not offered in this home")
	}

	provider, err := selectSearchProvider(cfg.Search, providers)
	if err != nil {
		t.Skipf("search backend not usable in this home: %v", err)
	}
	if provider == nil {
		t.Skip("no search provider selected in this home")
	}

	results, err := provider.Search(context.Background(), liveSearchQuery, 3)
	testsupport.FailOnErr(t, "web.search "+liveSearchQuery, err)

	if len(results) == 0 {
		t.Fatalf("live search returned no results for %q — a well-known query matching nothing "+
			"means the request shape or the account is wrong", liveSearchQuery)
	}
	for i, r := range results {
		t.Logf("%d. %s\n   %s\n   %.120s", i+1, r.Title, r.URL, r.Snippet)
		if !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d has a non-http URL %q — web.fetch could not follow it", i, r.URL)
		}
	}
}
