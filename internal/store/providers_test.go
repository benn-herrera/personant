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

// TestLoadProvidersFixture loads test/providers.toml and verifies both
// API-key forms resolve. It deliberately never prints a resolved
// APIKey — the fixture's apiKeyFile targets hold KEY_SECURITY_TEST_FAIL
// sentinels, so an assertion message echoing one would itself be the
// leak the sentinel is there to catch.
//
// The fixture also carries `broken-provider`, whose apiKeyFile points at
// a file that does not exist: it must be omitted from the map and
// reported as exactly one ProviderFault, without aborting the load.
func TestLoadProvidersFixture(t *testing.T) {
	path := filepath.Join(projectRoot(t), "test", "providers.toml")
	got, faults, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}

	for _, name := range []string{"local", "dummyrouter", "superinf", "dummy-emb-provider"} {
		if _, ok := got.Get(name); !ok {
			t.Errorf("missing provider %q (got %v)", name, keysOf(got))
		}
	}

	// broken-provider's apiKeyFile is unreadable — it must not appear in
	// the resolved map.
	if _, ok := got.Get("broken-provider"); ok {
		t.Errorf("broken-provider should be absent from the map (got %v)", keysOf(got))
	}

	// Exactly one fault, naming broken-provider. The Reason references
	// the key-file path only — it must never carry key material.
	if len(faults) != 1 {
		t.Fatalf("expected exactly 1 ProviderFault, got %d: %+v", len(faults), faults)
	}
	if faults[0].Name != "broken-provider" {
		t.Errorf("fault Name = %q, want broken-provider", faults[0].Name)
	}
	for _, leak := range []string{"somekey", "KEY_SECURITY_TEST_FAIL"} {
		if strings.Contains(faults[0].Reason, leak) {
			t.Errorf("ProviderFault.Reason carries key material (%q)", leak)
		}
	}

	// local uses apiKeyUnsafe — the inline form. "somekey" is the
	// fixture's placeholder, not a secret.
	if local, ok := got.Get("local"); ok && local.APIKey != "somekey" {
		t.Errorf("local apiKeyUnsafe: APIKey not resolved to the inline value (len %d)", len(local.APIKey))
	}

	// The remaining providers use apiKeyFile — the loader must resolve
	// the key from the referenced file (path relative to the
	// providers.toml directory).
	for _, name := range []string{"dummyrouter", "superinf", "dummy-emb-provider"} {
		p, ok := got.Get(name)
		if !ok {
			continue
		}
		if p.APIKey == "" {
			t.Errorf("%s: apiKeyFile did not resolve to a key", name)
		}
		if p.BaseURL == "" {
			t.Errorf("%s: BaseURL empty", name)
		}
		if p.DefaultModel == "" {
			t.Errorf("%s: DefaultModel empty", name)
		}
	}
}

func TestLoadProvidersGetUnknown(t *testing.T) {
	path := filepath.Join(projectRoot(t), "test", "providers.toml")
	got, _, err := LoadProviders(path)
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
	got, faults, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty Providers, got %d entries: %v", len(got), keysOf(got))
	}
	if len(faults) != 0 {
		t.Errorf("expected no faults from empty file, got %+v", faults)
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
	got, faults, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders template: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty Providers from comment-only file, got %d entries", len(got))
	}
	if len(faults) != 0 {
		t.Errorf("expected no faults from comment-only file, got %+v", faults)
	}
}

func TestLoadProvidersMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "definitely-not-here.toml")
	got, faults, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders missing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty Providers on missing file, got %d entries", len(got))
	}
	if len(faults) != 0 {
		t.Errorf("expected no faults from missing file, got %+v", faults)
	}
}

func TestLoadProvidersMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.toml")
	if err := os.WriteFile(path, []byte("[bad toml content\n"), 0o644); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	_, _, err := LoadProviders(path)
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
apiKeyUnsafe = "` + sentinel + `"
defaultModel = "m1"

[broken
this is not valid toml
`
	path := filepath.Join(t.TempDir(), "providers.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := LoadProviders(path)
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
