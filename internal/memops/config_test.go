package memops_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

// projectRoot walks up from the test working directory to the directory
// containing go.mod, so fixture files under test/ resolve regardless of
// which package's test binary is running.
func projectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("project root (containing go.mod) not found from %s", dir)
		}
		dir = parent
	}
}

func TestParseModelRef(t *testing.T) {
	cases := []struct {
		in              string
		provider, model string
		ok              bool
	}{
		{"reaper/nomicai-embed", "reaper", "nomicai-embed", true},
		{"reaper/gemma-main", "reaper", "gemma-main", true},
		// Split on the FIRST slash — the model part may contain slashes.
		{"openrouter/google/gemma-4-31b-it", "openrouter", "google/gemma-4-31b-it", true},
		{"  reaper/nomicai-embed  ", "reaper", "nomicai-embed", true}, // trimmed
		{"", "", "", false},
		{"noslash", "", "", false},
		{"/leading", "", "", false},
		{"trailing/", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			p, m, ok := memops.ParseModelRef(c.in)
			if ok != c.ok || p != c.provider || m != c.model {
				t.Errorf("ParseModelRef(%q) = (%q,%q,%v), want (%q,%q,%v)",
					c.in, p, m, ok, c.provider, c.model, c.ok)
			}
		})
	}
}

// TestValidateConfig cross-checks config.toml against the provider pool.
// The two fixture cases (valid config.toml, invalid broken-config.toml)
// are the required core; inline sub-cases cover the simpler edges.
//
// LoadProviders / LoadConfig are substrate I/O and stay in internal/store;
// this test is in the external memops_test package so it can call them
// without a dependency cycle.
func TestValidateConfig(t *testing.T) {
	root := projectRoot(t)
	providers, _, err := store.LoadProviders(filepath.Join(root, "test", "providers.toml"))
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}

	t.Run("valid fixture", func(t *testing.T) {
		cfg, err := store.LoadConfig(filepath.Join(root, "test", "config.toml"))
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if issues := memops.ValidateConfig(cfg, providers); len(issues) != 0 {
			t.Errorf("expected no issues for valid config, got %+v", issues)
		}
	})

	t.Run("broken fixture", func(t *testing.T) {
		cfg, err := store.LoadConfig(filepath.Join(root, "test", "broken-config.toml"))
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		issues := memops.ValidateConfig(cfg, providers)
		if len(issues) != 2 {
			t.Fatalf("expected exactly 2 issues, got %d: %+v", len(issues), issues)
		}
		var chat, emb *memops.ConfigIssue
		for i := range issues {
			switch issues[i].Section {
			case "chat":
				chat = &issues[i]
			case "embedding":
				emb = &issues[i]
			}
		}
		if chat == nil {
			t.Errorf("missing chat issue in %+v", issues)
		} else if !strings.Contains(chat.Message, "no-such-provider") {
			t.Errorf("chat issue message = %q, want it to name no-such-provider", chat.Message)
		}
		if emb == nil {
			t.Errorf("missing embedding issue in %+v", issues)
		} else if !strings.Contains(emb.Message, "not in the provider pool") {
			t.Errorf("embedding issue message = %q, want an unknown-provider report", emb.Message)
		}
	})

	t.Run("empty config", func(t *testing.T) {
		if issues := memops.ValidateConfig(memops.Config{}, providers); len(issues) != 0 {
			t.Errorf("empty config should yield no issues, got %+v", issues)
		}
	})

	t.Run("malformed chat ref", func(t *testing.T) {
		cfg := memops.Config{Chat: memops.ChatConfig{DefaultModel: "noslash"}}
		issues := memops.ValidateConfig(cfg, providers)
		if len(issues) != 1 || issues[0].Section != "chat" {
			t.Fatalf("expected 1 chat issue, got %+v", issues)
		}
		if !strings.Contains(issues[0].Message, "is not a") {
			t.Errorf("issue message = %q, want a malformed-reference report", issues[0].Message)
		}
	})
}

// TestValidateConfig_ProviderModelSplit drives ValidateConfig directly
// against inline provider pools (no fixture I/O), covering the
// single-provider / dedicated-provider / unknown / malformed shapes.
//
// The key regression: an embedding model id that differs from the
// provider's defaultModel is legitimate — defaultModel is the chat
// fallback, not an embedding pin — so a single provider serving both a
// chat model and a distinct embedding model must validate clean.
func TestValidateConfig_ProviderModelSplit(t *testing.T) {
	// singleProvider mirrors the real dogfood shape: one provider whose
	// defaultModel is a chat model, referenced by both [chat] and
	// [embedding] with distinct model ids.
	singleProvider := memops.Providers{
		"reaper": {Name: "reaper", DefaultModel: "gemma-4-main"},
	}
	// twoProviders adds a dedicated embedding provider.
	twoProviders := memops.Providers{
		"reaper": {Name: "reaper", DefaultModel: "gemma-4-main"},
		"embco":  {Name: "embco", DefaultModel: "embco-default"},
	}

	cases := []struct {
		name       string
		cfg        memops.Config
		providers  memops.Providers
		wantIssues int
		wantSect   string // section of the single expected issue (when wantIssues == 1)
		wantMsg    string // substring the single expected issue must contain
	}{
		{
			// (a) REGRESSION — the exact user shape that was failing.
			name: "single provider, distinct chat and embedding models",
			cfg: memops.Config{
				Chat:      memops.ChatConfig{DefaultModel: "reaper/gemma-4-main"},
				Embedding: memops.EmbeddingConfig{Model: "reaper/nomicai-embed"},
			},
			providers:  singleProvider,
			wantIssues: 0,
		},
		{
			// (b) dedicated embedding provider.
			name: "dedicated embedding provider",
			cfg: memops.Config{
				Chat:      memops.ChatConfig{DefaultModel: "reaper/gemma-4-main"},
				Embedding: memops.EmbeddingConfig{Model: "embco/some-embed-model"},
			},
			providers:  twoProviders,
			wantIssues: 0,
		},
		{
			// (c) unknown provider in the embedding reference.
			name: "embedding references unknown provider",
			cfg: memops.Config{
				Embedding: memops.EmbeddingConfig{Model: "ghost/embed"},
			},
			providers:  singleProvider,
			wantIssues: 1,
			wantSect:   "embedding",
			wantMsg:    "not in the provider pool",
		},
		{
			// (c) malformed embedding reference (not "provider/model").
			name: "embedding malformed ref",
			cfg: memops.Config{
				Embedding: memops.EmbeddingConfig{Model: "noslash"},
			},
			providers:  singleProvider,
			wantIssues: 1,
			wantSect:   "embedding",
			wantMsg:    "is not a",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := memops.ValidateConfig(c.cfg, c.providers)
			if len(issues) != c.wantIssues {
				t.Fatalf("got %d issues, want %d: %+v", len(issues), c.wantIssues, issues)
			}
			if c.wantIssues == 1 {
				if issues[0].Section != c.wantSect {
					t.Errorf("issue section = %q, want %q", issues[0].Section, c.wantSect)
				}
				if !strings.Contains(issues[0].Message, c.wantMsg) {
					t.Errorf("issue message = %q, want it to contain %q", issues[0].Message, c.wantMsg)
				}
			}
		})
	}
}
