package chat

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"personant/internal/memops"
	"personant/internal/tools"
	"personant/internal/tools/web"
)

// §6.1 tool-registry construction — the one place configuration becomes
// an inventory.
//
// The registry is built ONCE at session open and treated as immutable
// afterwards (tools.Registry documents that contract). Everything that
// can go wrong with the OPTIONAL half of the inventory degrades to a
// warning and a smaller registry: no search provider in the pool, an
// unreadable key file, an api this binary does not speak — each leaves
// `web.search` unregistered, which §6.1.4's empty-registry semantics
// already handle correctly. The model is never told about a tool that
// cannot be serviced, so it never calls one.
//
// The mandatory half is `web.fetch`: it needs no credential, so it
// registers on every session regardless of configuration.

// buildToolRegistry assembles the §6.1.1 inventory.
//
// providers is the ALREADY-LOADED pool, keys resolved — this function
// reads no file and resolves no secret of its own. The error return is
// reserved for a REGISTRY-CONSTRUCTION fault (duplicate name, nil
// handler, a tool declaring Mutates): those are programming errors in
// this function, not user misconfiguration, and failing the session
// loudly on one is correct. Every user-facing problem is a line on warn.
func buildToolRegistry(cfg memops.Config, providers memops.Providers, warn io.Writer) (*tools.Registry, error) {
	reg := tools.NewRegistry()

	if err := reg.Register(web.NewFetchTool(web.FetchConfig{})); err != nil {
		return nil, fmt.Errorf("chat: register %s: %w", web.ToolNameFetch, err)
	}

	provider, err := selectSearchProvider(cfg.Search, providers)
	if err != nil {
		fmt.Fprintf(warn, "warn: %s unavailable: %v\n", web.ToolNameSearch, err)
		return reg, nil
	}
	if provider == nil {
		return reg, nil // no search provider in the pool; silence is correct
	}

	tool, err := web.NewSearchTool(web.SearchConfig{
		Provider:   provider,
		MaxPerTurn: cfg.Search.MaxPerTurn,
		MaxPerDay:  cfg.Search.MaxPerDay,
	})
	if err != nil {
		return nil, fmt.Errorf("chat: build %s: %w", web.ToolNameSearch, err)
	}
	if err := reg.Register(tool); err != nil {
		return nil, fmt.Errorf("chat: register %s: %w", web.ToolNameSearch, err)
	}
	return reg, nil
}

// selectSearchProvider resolves the pool's `type = "search"` entries and
// config.toml's [search] choice into a backend.
//
// It returns (nil, nil) for "no search provider configured" — the
// default state, which must produce NO output at all. It returns an
// error only when the user evidently INTENDED search and something
// about it is wrong; that distinction is what keeps the warning
// meaningful rather than a line everyone learns to ignore.
func selectSearchProvider(cfg memops.SearchConfig, providers memops.Providers) (web.SearchProvider, error) {
	pool := providers.OfKind(memops.ProviderTypeSearch)
	pinned := strings.TrimSpace(cfg.Provider)

	switch {
	case len(pool) == 0 && pinned == "":
		return nil, nil
	case len(pool) == 0:
		return nil, fmt.Errorf("config.toml [search] names provider %q, but providers.toml has no "+
			"`type = \"search\"` entry (a provider whose apiKeyFile is unreadable is dropped from the pool "+
			"with its own warning)", pinned)
	}

	name := pinned
	if name == "" {
		if len(pool) > 1 {
			return nil, fmt.Errorf("providers.toml declares %d search providers (%s); "+
				"set [search] provider in config.toml to choose one", len(pool), strings.Join(sortedNames(pool), ", "))
		}
		name = sortedNames(pool)[0]
	}

	provider, ok := pool[name]
	if !ok {
		return nil, fmt.Errorf("no search provider named %q in providers.toml (search providers: %s)",
			name, strings.Join(sortedNames(pool), ", "))
	}
	if provider.APIKey == "" {
		return nil, fmt.Errorf("search provider %q has no apiKeyFile", name)
	}

	switch provider.Protocol() {
	case memops.ProviderAPIExa:
		return web.NewExaProvider(provider.APIKey, provider.BaseURL, nil)
	case "":
		return nil, fmt.Errorf("search provider %q declares no `api` (known: %s)", name, memops.ProviderAPIExa)
	default:
		return nil, fmt.Errorf("search provider %q declares api %q, which this binary does not speak (known: %s)",
			name, provider.API, memops.ProviderAPIExa)
	}
}

// sortedNames returns a pool's names in deterministic order — for a
// message the user reads, and for the single-entry pick above (map
// iteration order would make "the one provider" a coin flip the day a
// second one is added).
func sortedNames(pool memops.Providers) []string {
	out := make([]string, 0, len(pool))
	for name := range pool {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
