package store

import (
	"os"
	"path/filepath"
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
	if cfg.Embedding.Model != "dummy-emb-provider/dummy-emb-model" {
		t.Errorf("Embedding.Model = %q, want dummy-emb-provider/dummy-emb-model", cfg.Embedding.Model)
	}
	if cfg.Chat.DefaultModel != "reaper/gemma-main" {
		t.Errorf("Chat.DefaultModel = %q, want reaper/gemma-main", cfg.Chat.DefaultModel)
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
