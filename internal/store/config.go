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

// ConfigIssue is one cross-file validation problem found by ValidateConfig.
type ConfigIssue struct {
	Section string // "chat" or "embedding"
	Message string
}

// ValidateConfig cross-checks a loaded config.toml against the provider
// pool. It returns one ConfigIssue per problem; an empty slice means the
// config is valid. An empty [chat] or [embedding] reference is NOT an
// issue — it means "not configured, fall back to defaults".
//
// The chat model id is deliberately not validated here: chat model
// correctness is resolved at runtime against the provider's /models
// endpoint. The embedding model id, by contrast, is a hard static pin —
// it defines the vector space — so it must match the provider's
// defaultModel verbatim.
func ValidateConfig(cfg Config, providers Providers) []ConfigIssue {
	var issues []ConfigIssue

	if cfg.Chat.DefaultModel != "" {
		provider, _, ok := ParseModelRef(cfg.Chat.DefaultModel)
		switch {
		case !ok:
			issues = append(issues, ConfigIssue{
				Section: "chat",
				Message: fmt.Sprintf("defaultModel %q is not a \"provider/model\" reference", cfg.Chat.DefaultModel),
			})
		default:
			if _, known := providers[provider]; !known {
				issues = append(issues, ConfigIssue{
					Section: "chat",
					Message: fmt.Sprintf("chat references provider %q, which is not in the provider pool", provider),
				})
			}
		}
	}

	if cfg.Embedding.Model != "" {
		provider, model, ok := ParseModelRef(cfg.Embedding.Model)
		switch {
		case !ok:
			issues = append(issues, ConfigIssue{
				Section: "embedding",
				Message: fmt.Sprintf("model %q is not a \"provider/model\" reference", cfg.Embedding.Model),
			})
		default:
			p, known := providers[provider]
			switch {
			case !known:
				issues = append(issues, ConfigIssue{
					Section: "embedding",
					Message: fmt.Sprintf("embedding references provider %q, which is not in the provider pool", provider),
				})
			case model != p.DefaultModel:
				issues = append(issues, ConfigIssue{
					Section: "embedding",
					Message: fmt.Sprintf("embedding model %q does not match provider %q defaultModel %q", model, provider, p.DefaultModel),
				})
			}
		}
	}

	return issues
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
