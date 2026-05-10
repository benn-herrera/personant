package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projectRoot walks up from the test's working directory until it finds a
// go.mod file, returning that directory. The test fixture providers.toml
// lives at <root>/test/providers.toml.
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

func TestLoadProvidersFixture(t *testing.T) {
	path := filepath.Join(projectRoot(t), "test", "providers.toml")
	got, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}

	want := map[string]Provider{
		"local": {
			Name:         "local",
			BaseURL:      "http://localhost:11117",
			APIKey:       "dummy",
			DefaultModel: "gemma-4-26B-A4B-it-MXFP4_MOE",
		},
		"dummyrouter": {
			Name:         "dummyrouter",
			BaseURL:      "https://dummyrouter.ai/api/v1",
			APIKey:       "sk-or-v1-0123456789abcdefghijklmnopqrstuvwxyz0123457890abcdefghijklmnopqr",
			DefaultModel: "google/gemma-4-31b-it",
		},
		"superinf": {
			Name:         "superinf",
			BaseURL:      "https://api.superinf.ai/v1",
			APIKey:       "supe_01234_56789abcdefghijklmnopqrstuvwxyzABCDEF",
			DefaultModel: "gpt-5.4-2026-03-05",
		},
	}

	if len(got) != len(want) {
		t.Fatalf("provider count: got %d, want %d (got keys: %v)", len(got), len(want), keysOf(got))
	}
	for name, w := range want {
		g, ok := got.Get(name)
		if !ok {
			t.Errorf("missing provider %q", name)
			continue
		}
		if g != w {
			t.Errorf("provider %q mismatch:\n got: %+v\nwant: %+v", name, g, w)
		}
	}
}

func TestLoadProvidersGetUnknown(t *testing.T) {
	path := filepath.Join(projectRoot(t), "test", "providers.toml")
	got, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if _, ok := got.Get("nope"); ok {
		t.Errorf("Get(\"nope\") returned ok=true on unknown provider")
	}
}

func TestLoadProvidersEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.toml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	got, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty Providers, got %d entries: %v", len(got), keysOf(got))
	}
}

// TestLoadProvidersTemplateOnlyFile mirrors what `personant init` writes:
// a comment-only TOML file with no live tables. Must yield an empty map,
// not an error.
func TestLoadProvidersTemplateOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.toml")
	body := `# personant LLM provider configuration
#
# [openai]
# baseUrl      = "https://api.openai.com/v1"
# apiKey       = "sk-..."
# defaultModel = "gpt-5.4-2026-03-05"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	got, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders template: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty Providers from comment-only file, got %d entries", len(got))
	}
}

func TestLoadProvidersMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "definitely-not-here.toml")
	got, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders missing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty Providers on missing file, got %d entries", len(got))
	}
}

func TestLoadProvidersMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.toml")
	if err := os.WriteFile(path, []byte("[bad toml content\n"), 0o644); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	_, err := LoadProviders(path)
	if err == nil {
		t.Fatal("expected error from malformed TOML, got nil")
	}
	if !strings.Contains(err.Error(), "providers:") {
		t.Errorf("error not wrapped with providers prefix: %v", err)
	}
}

// TestLoadProvidersAPIKeyAbsentFromParseError: when one table is malformed,
// the wrapped parse error must not contain an apiKey value from a sibling
// table that did parse, nor any apiKey-shaped string at all.
func TestLoadProvidersAPIKeyAbsentFromParseError(t *testing.T) {
	const sentinel = "sk-SECRET-MUST-NOT-LEAK-9f3a0b"
	body := `[clean]
baseUrl = "https://api.example.com/v1"
apiKey = "` + sentinel + `"
defaultModel = "m1"

[broken
this is not valid toml
`
	path := filepath.Join(t.TempDir(), "providers.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadProviders(path)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("API key leaked into parse error: %v", err)
	}
}

func keysOf(p Providers) []string {
	out := make([]string, 0, len(p))
	for k := range p {
		out = append(out, k)
	}
	return out
}
