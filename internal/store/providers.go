package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Provider describes one LLM provider's connectivity (spec §8.2.1).
// providers.toml is the provider *pool* — connectivity only; it does
// not pin or prefer anything. The chat/embedding choices that draw
// from this pool live in config.toml (see Config).
//
// The API key is secret-bearing. The recommended form is APIKeyFile (a
// path to a key file kept out of the scannable config); APIKeyUnsafe
// is the legacy inline form. Either way the resolved key lands in
// APIKey, which the runtime is the sole consumer of — it must never
// appear in a log line, error message, or LLM-bound context.
type Provider struct {
	Name         string `toml:"-"` // table header from TOML; populated post-decode
	BaseURL      string `toml:"baseUrl"`
	DefaultModel string `toml:"defaultModel"`

	// APIKeyUnsafe is an inline API key. Discouraged — it places a
	// secret directly in providers.toml, making the file unsafe to
	// scan. Prefer APIKeyFile.
	APIKeyUnsafe string `toml:"apiKeyUnsafe"`

	// APIKeyFile is a path to a file holding the API key. Relative
	// paths resolve against the providers.toml directory. This is the
	// recommended form: the secret stays out of providers.toml, so the
	// config file itself is safe to scan and edit.
	APIKeyFile string `toml:"apiKeyFile"`

	// APIKey is the resolved key — populated by LoadProviders from
	// APIKeyFile (preferred) or APIKeyUnsafe. Not a TOML field.
	APIKey string `toml:"-"`
}

// Providers is a name-keyed set of providers loaded from providers.toml.
type Providers map[string]Provider

// Get returns the provider by name. The second return is false when the
// name is unknown (i.e. not declared in providers.toml).
func (p Providers) Get(name string) (Provider, bool) {
	v, ok := p[name]
	return v, ok
}

// LoadProviders reads providers.toml at path and returns a name-keyed
// map. Each provider's API key is resolved: from APIKeyFile when set
// (read relative to the providers.toml directory), otherwise from
// APIKeyUnsafe.
//
// A nonexistent or empty file yields an empty Providers and a nil
// error — a fresh home may not have a populated providers.toml yet.
//
// Errors are wrapped without TOML value content or key material — a
// parse error references line/column, and a key-file read error
// references the path, so no secret leaks through an error string.
func LoadProviders(path string) (Providers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Providers{}, nil
		}
		return nil, fmt.Errorf("providers: read %s: %w", path, err)
	}

	raw := map[string]Provider{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, fmt.Errorf("providers: parse %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	out := make(Providers, len(raw))
	for name, p := range raw {
		p.Name = name
		key, err := resolveAPIKey(p, dir)
		if err != nil {
			return nil, fmt.Errorf("providers: %s: %w", name, err)
		}
		p.APIKey = key
		out[name] = p
	}
	return out, nil
}

// resolveAPIKey returns the provider's API key from APIKeyFile
// (preferred) or APIKeyUnsafe. The key file's content is trimmed of
// surrounding whitespace. The returned error never carries key
// content — only the file path, on an I/O failure.
func resolveAPIKey(p Provider, dir string) (string, error) {
	if p.APIKeyFile != "" {
		keyPath := p.APIKeyFile
		if !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(dir, keyPath)
		}
		b, err := os.ReadFile(keyPath)
		if err != nil {
			return "", fmt.Errorf("read apiKeyFile: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return p.APIKeyUnsafe, nil
}
