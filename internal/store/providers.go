package store

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Provider describes one LLM provider's connectivity (spec §8.2.1).
//
// APIKey is secret-bearing. The runtime is the only consumer; per the spec's
// security boundary, the field must not appear in any log line, error
// message, or LLM-bound context. Helpers in this package and in
// internal/model use the literal string "<redacted>" when an error message
// might otherwise reveal it.
type Provider struct {
	Name         string `toml:"-"`            // table header from TOML; populated post-decode
	BaseURL      string `toml:"baseUrl"`
	APIKey       string `toml:"apiKey"`
	DefaultModel string `toml:"defaultModel"`
}

// Providers is a name-keyed set of providers loaded from providers.toml.
type Providers map[string]Provider

// Get returns the provider by name. The second return is false when the
// name is unknown (i.e. not declared in providers.toml).
func (p Providers) Get(name string) (Provider, bool) {
	v, ok := p[name]
	return v, ok
}

// LoadProviders reads providers.toml at path and returns a name-keyed map.
//
// A nonexistent file or an empty file yields an empty Providers and a nil
// error — `personant init` writes a template-only providers.toml with all
// example tables commented out, and a fresh home may not have written one
// yet at all. Callers that need a specific provider should use Get and
// surface a useful error themselves.
//
// Malformed TOML or any I/O error other than "not exist" is returned
// wrapped. The wrapped error never carries TOML content from a successfully
// parsed table, so an APIKey from one table cannot leak through a parse
// error in a different table.
func LoadProviders(path string) (Providers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Providers{}, nil
		}
		return nil, fmt.Errorf("providers: read %s: %w", path, err)
	}

	// Decode into a map keyed by table header. BurntSushi/toml errors
	// reference line/column, not value content, so a parse error in one
	// table cannot leak an APIKey from a sibling table that parsed cleanly.
	raw := map[string]Provider{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("providers: parse %s: %w", path, err)
	}

	out := make(Providers, len(raw))
	for name, p := range raw {
		p.Name = name
		out[name] = p
	}
	return out, nil
}
