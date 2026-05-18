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
		} else if !strings.Contains(emb.Message, "does not match") {
			t.Errorf("embedding issue message = %q, want a model-mismatch report", emb.Message)
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
