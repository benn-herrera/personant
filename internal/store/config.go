package store

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the personant settings file (config.toml) — the choices
// that draw from the providers.toml pool. providers.toml lists what is
// available; config.toml says which to use. config.toml is the home
// for all future personant-level configuration.
type Config struct {
	Chat      ChatConfig      `toml:"chat"`
	Embedding EmbeddingConfig `toml:"embedding"`
}

// ChatConfig is the [chat] section.
type ChatConfig struct {
	// DefaultModel is a "<provider>/<model>" reference into the
	// providers.toml pool — the default chat provider and model. The
	// --provider / --model CLI flags override it.
	DefaultModel string `toml:"defaultModel"`
}

// EmbeddingConfig is the [embedding] section — the §3.4 layer-2
// embedding choice.
type EmbeddingConfig struct {
	// Model is a "<provider>/<model>" reference — the pinned embedding
	// provider and model. The embedding model defines the vector
	// space, so this pin is deliberate and stable: it must not drift
	// with the chat provider, and changing it invalidates any
	// persisted embedding index.
	Model string `toml:"model"`

	// VectorLength, when > 0, requests a Matryoshka-truncated
	// embedding of that dimensionality. 0 → the model's native
	// dimension.
	VectorLength int `toml:"vectorLength"`
}

// LoadConfig reads config.toml at path. A nonexistent file yields a
// zero Config and a nil error — a fresh home may have no config.toml,
// in which case all choices fall back to their defaults.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}

// ParseModelRef splits a "<provider>/<model>" config reference. The
// split is on the FIRST '/', so the model part may itself contain
// slashes (e.g. "openrouter/google/gemma-4-31b-it"). ok is false when
// the reference is empty or lacks a non-empty provider and model.
func ParseModelRef(ref string) (provider, model string, ok bool) {
	ref = strings.TrimSpace(ref)
	i := strings.IndexByte(ref, '/')
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}
