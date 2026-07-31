package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"personant/internal/memops"
)

// LoadProviders reads providers.toml at path and returns a name-keyed
// map. Each provider's API key is resolved: from APIKeyFile when set
// (read relative to the providers.toml directory), otherwise from
// APIKeyUnsafe.
//
// A nonexistent or empty file yields an empty Providers, no faults, and
// a nil error — a fresh home may not have a populated providers.toml
// yet.
//
// The error return is reserved for file-level failures only: the
// providers.toml file unreadable (non-not-exist) or malformed TOML. A
// provider that is individually unusable — an undeclared or mismatched
// `type`/`api` pair, or an apiKeyFile that cannot be read — does NOT
// abort the load: it is omitted from the returned map and appended to
// the returned []ProviderFault so the rest of the pool still loads.
//
// Errors and faults are constructed without TOML value content or key
// material — a parse error references line/column, and a key-file read
// failure references the path, so no secret leaks.
func LoadProviders(path string) (memops.Providers, []memops.ProviderFault, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return memops.Providers{}, nil, nil
		}
		return nil, nil, fmt.Errorf("providers: read %s: %w", path, err)
	}

	raw := map[string]memops.Provider{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, nil, fmt.Errorf("providers: parse %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	out := make(memops.Providers, len(raw))
	var faults []memops.ProviderFault
	for name, p := range raw {
		p.Name = name
		// Shape before secrets: an entry that declares no `type`/`api` is
		// dropped with a named reason rather than silently vanishing from
		// the selection lists it would never have matched. Checked first
		// because there is no point reading a key file for an entry
		// nothing can route to.
		if err := p.Validate(); err != nil {
			faults = append(faults, memops.ProviderFault{Name: name, Reason: err.Error()})
			continue
		}
		key, err := resolveAPIKey(p, dir)
		if err != nil {
			faults = append(faults, memops.ProviderFault{Name: name, Reason: err.Error()})
			continue
		}
		p.APIKey = key
		out[name] = p
	}
	return out, faults, nil
}

// resolveAPIKey returns the provider's API key from APIKeyFile
// (preferred) or APIKeyUnsafe. The key file's content is trimmed of
// surrounding whitespace. The returned error never carries key
// content — only the file path, on an I/O failure.
func resolveAPIKey(p memops.Provider, dir string) (string, error) {
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
