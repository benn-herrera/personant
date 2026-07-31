package store

import (
	"os"
	"path/filepath"
	"testing"

	"personant/internal/memops"
)

func TestLoadConfigFixture(t *testing.T) {
	path := filepath.Join(projectRoot(t), "test", "config.toml")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Embedding.Model != "dummy-embedding/dummy-creator/dummy-emb-model" {
		t.Errorf("Embedding.Model = %q, want dummy-embedding/dummy-creator/dummy-emb-model", cfg.Embedding.Model)
	}
	if cfg.Chat.DefaultModel != "dummy-router/gemma-main" {
		t.Errorf("Chat.DefaultModel = %q, want dummy-router/gemma-main", cfg.Chat.DefaultModel)
	}
	// [search] names a pool entry and carries no key of its own — the
	// backend's apiKeyFile lives on its providers.toml entry, which is
	// what keeps config.toml non-secret-bearing.
	if cfg.Search.Provider != "dummy-search" {
		t.Errorf("Search.Provider = %q, want dummy-search", cfg.Search.Provider)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "no-config.toml"))
	if err != nil {
		t.Fatalf("LoadConfig missing: %v", err)
	}
	if cfg != (memops.Config{}) {
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
