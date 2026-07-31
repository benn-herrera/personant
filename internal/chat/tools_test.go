package chat

import (
	"bytes"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/tools/web"
)

// §6.1 registry construction. The property under test throughout: a
// missing or broken search configuration costs the SEARCH TOOL and
// nothing else — never the session, never web.fetch, never a line of
// noise on a fresh install.

// inferenceProvider is a complete, valid inference pool entry — `type`
// and `api` are REQUIRED fields, so a bare literal is not a provider the
// runtime would ever see.
func inferenceProvider(name string) memops.Provider {
	return memops.Provider{
		Name: name, BaseURL: "http://" + name + ".local/v1", APIKey: "k",
		Type: memops.ProviderTypeInference, API: memops.ProviderAPIOpenAI,
	}
}

func hasTool(t *testing.T, cfg memops.Config, providers memops.Providers, name string) (bool, string) {
	t.Helper()
	var warn bytes.Buffer
	reg, err := buildToolRegistry(cfg, providers, &warn)
	if err != nil {
		t.Fatalf("buildToolRegistry: %v", err)
	}
	_, ok := reg.Lookup(name)
	return ok, warn.String()
}

func TestToolRegistryWithoutSearchProvider(t *testing.T) {
	// The fresh-install state: inference providers only, no [search].
	providers := memops.Providers{
		"reaper": inferenceProvider("reaper"),
	}

	hasSearch, warn := hasTool(t, memops.Config{}, providers, web.ToolNameSearch)
	if hasSearch {
		t.Error("web.search registered with no search provider in the pool")
	}
	if warn != "" {
		t.Errorf("an unconfigured search backend must be SILENT, not warned about; got: %q", warn)
	}

	// web.fetch needs no credential and must be there regardless.
	hasFetch, _ := hasTool(t, memops.Config{}, providers, web.ToolNameFetch)
	if !hasFetch {
		t.Error("web.fetch must register regardless of search configuration")
	}
}

func TestToolRegistrySearchFromPool(t *testing.T) {
	providers := memops.Providers{
		"reaper": inferenceProvider("reaper"),
		"exa": {
			Name: "exa", BaseURL: web.ExaEndpoint, APIKey: "search-key",
			Type: memops.ProviderTypeSearch, API: memops.ProviderAPIExa,
		},
	}
	cfg := memops.Config{Search: memops.SearchConfig{Provider: "exa"}}

	hasSearch, warn := hasTool(t, cfg, providers, web.ToolNameSearch)
	if !hasSearch {
		t.Fatalf("web.search did not register from a complete pool entry; warnings: %q", warn)
	}
	if warn != "" {
		t.Errorf("a working configuration produced warnings: %q", warn)
	}

	// The pin is optional when the pool holds exactly one search entry.
	hasSearch, _ = hasTool(t, memops.Config{}, providers, web.ToolNameSearch)
	if !hasSearch {
		t.Error("a sole search provider must be selected without an explicit [search] provider pin")
	}
}

// TestToolRegistryDegradesLoudly — each broken shape leaves the session
// alive, drops only web.search, and says why.
func TestToolRegistryDegradesLoudly(t *testing.T) {
	base := inferenceProvider("reaper")

	for _, tc := range []struct {
		name      string
		providers memops.Providers
		cfg       memops.Config
		wantWarn  string
	}{
		{
			name:      "pinned provider absent from the pool",
			providers: memops.Providers{"reaper": base},
			cfg:       memops.Config{Search: memops.SearchConfig{Provider: "exa"}},
			wantWarn:  "no `type = \"search\"` entry",
		},
		{
			name: "pinned name is not a search provider",
			providers: memops.Providers{
				"reaper": base,
				"other":  {Name: "other", APIKey: "k", Type: memops.ProviderTypeSearch, API: memops.ProviderAPIExa},
			},
			cfg:      memops.Config{Search: memops.SearchConfig{Provider: "nosuch"}},
			wantWarn: "no search provider named",
		},
		{
			name: "search provider with no key",
			providers: memops.Providers{
				"reaper": base,
				"exa":    {Name: "exa", Type: memops.ProviderTypeSearch, API: memops.ProviderAPIExa},
			},
			wantWarn: "no apiKeyFile",
		},
		{
			name: "search provider with an unknown api",
			providers: memops.Providers{
				"reaper": base,
				"weird":  {Name: "weird", APIKey: "k", Type: memops.ProviderTypeSearch, API: "brand-new-engine"},
			},
			wantWarn: "does not speak",
		},
		{
			name: "search provider declaring no api",
			providers: memops.Providers{
				"reaper": base,
				"bare":   {Name: "bare", APIKey: "k", Type: memops.ProviderTypeSearch},
			},
			wantWarn: "declares no `api`",
		},
		{
			name: "two search providers and no pin",
			providers: memops.Providers{
				"reaper": base,
				"exa":    {Name: "exa", APIKey: "k", Type: memops.ProviderTypeSearch, API: memops.ProviderAPIExa},
				"exa2":   {Name: "exa2", APIKey: "k", Type: memops.ProviderTypeSearch, API: memops.ProviderAPIExa},
			},
			wantWarn: "set [search] provider",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hasSearch, warn := hasTool(t, tc.cfg, tc.providers, web.ToolNameSearch)
			if hasSearch {
				t.Error("web.search registered against a broken configuration")
			}
			if !strings.Contains(warn, tc.wantWarn) {
				t.Errorf("warning %q does not contain %q", warn, tc.wantWarn)
			}
			// And the session still has its fetch tool.
			if hasFetch, _ := hasTool(t, tc.cfg, tc.providers, web.ToolNameFetch); !hasFetch {
				t.Error("a broken search configuration took web.fetch down with it")
			}
		})
	}
}

// TestSearchConfigNeverRefusesTheConfig — ValidateConfig gates the
// session, so an over-strict check there blocks a launch entirely. It
// must have nothing to say about [search], whatever is in it.
func TestSearchConfigNeverRefusesTheConfig(t *testing.T) {
	providers := memops.Providers{"reaper": inferenceProvider("reaper")}
	for _, search := range []memops.SearchConfig{
		{},
		{Provider: "exa"},
		{Provider: "a-backend-that-does-not-exist"},
		{Provider: "reaper"}, // an inference provider named as the search one
		{MaxPerTurn: -4, MaxPerDay: -1},
	} {
		cfg := memops.Config{
			Chat:   memops.ChatConfig{DefaultModel: "reaper/some-model"},
			Search: search,
		}
		if issues := memops.ValidateConfig(cfg, providers); len(issues) != 0 {
			t.Errorf("[search] %+v produced config issues %+v; it must never refuse a session", search, issues)
		}
	}
}
