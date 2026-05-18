package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
			p, m, ok := ParseModelRef(c.in)
			if ok != c.ok || p != c.provider || m != c.model {
				t.Errorf("ParseModelRef(%q) = (%q,%q,%v), want (%q,%q,%v)",
					c.in, p, m, ok, c.provider, c.model, c.ok)
			}
		})
	}
}

func TestLoadConfigFixture(t *testing.T) {
	path := filepath.Join(projectRoot(t), "test", "config.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Embedding.Model != "dummy-emb-provider/dummy-creator/dummy-emb-model" {
		t.Errorf("Embedding.Model = %q, want dummy-emb-provider/dummy-creator/dummy-emb-model", cfg.Embedding.Model)
	}
	if cfg.Chat.DefaultModel != "dummyrouter/gemma-main" {
		t.Errorf("Chat.DefaultModel = %q, want dummyrouter/gemma-main", cfg.Chat.DefaultModel)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "no-config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig missing: %v", err)
	}
	if cfg != (Config{}) {
		t.Errorf("missing config should yield zero Config, got %+v", cfg)
	}
}

func TestLoadConfigVectorLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `[embedding]
model = "reaper/nomicai-embed"
vectorLength = 256
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Embedding.VectorLength != 256 {
		t.Errorf("VectorLength = %d, want 256", cfg.Embedding.VectorLength)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[embedding\nbroken"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error from malformed config.toml, got nil")
	}
}

// TestValidateConfig cross-checks config.toml against the provider pool.
// The two fixture cases (valid config.toml, invalid broken-config.toml)
// are the required core; inline sub-cases cover the simpler edges.
func TestValidateConfig(t *testing.T) {
	root := projectRoot(t)
	providers, _, err := LoadProviders(filepath.Join(root, "test", "providers.toml"))
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}

	t.Run("valid fixture", func(t *testing.T) {
		cfg, err := LoadConfig(filepath.Join(root, "test", "config.toml"))
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if issues := ValidateConfig(cfg, providers); len(issues) != 0 {
			t.Errorf("expected no issues for valid config, got %+v", issues)
		}
	})

	t.Run("broken fixture", func(t *testing.T) {
		cfg, err := LoadConfig(filepath.Join(root, "test", "broken-config.toml"))
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		issues := ValidateConfig(cfg, providers)
		if len(issues) != 2 {
			t.Fatalf("expected exactly 2 issues, got %d: %+v", len(issues), issues)
		}
		var chat, emb *ConfigIssue
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
		} else if !strings.Contains(emb.Message, "does not match") {
			t.Errorf("embedding issue message = %q, want a model-mismatch report", emb.Message)
		}
	})

	t.Run("empty config", func(t *testing.T) {
		if issues := ValidateConfig(Config{}, providers); len(issues) != 0 {
			t.Errorf("empty config should yield no issues, got %+v", issues)
		}
	})

	t.Run("malformed chat ref", func(t *testing.T) {
		cfg := Config{Chat: ChatConfig{DefaultModel: "noslash"}}
		issues := ValidateConfig(cfg, providers)
		if len(issues) != 1 || issues[0].Section != "chat" {
			t.Fatalf("expected 1 chat issue, got %+v", issues)
		}
		if !strings.Contains(issues[0].Message, "is not a") {
			t.Errorf("issue message = %q, want a malformed-reference report", issues[0].Message)
		}
	})
}
