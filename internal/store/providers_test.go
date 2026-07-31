package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
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
// reported as exactly one memops.ProviderFault, without aborting the load.
func TestLoadProvidersFixture(t *testing.T) {
	path := filepath.Join(projectRoot(t), "test", "providers.toml")
	got, faults, err := LoadProviders(path)
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}

	for _, name := range []string{"local", "dummy-router", "dummy-inference", "dummy-embedding", "dummy-search"} {
		if _, ok := got.Get(name); !ok {
			t.Errorf("missing provider %q (got %v)", name, keysOf(got))
		}
	}

	// Kind is what keeps a search backend out of inference selection.
	if search, ok := got.Get("dummy-search"); ok {
		if search.Kind() != memops.ProviderTypeSearch {
			t.Errorf("dummy-search Kind() = %q, want %q", search.Kind(), memops.ProviderTypeSearch)
		}
		if n := len(got.OfKind(memops.ProviderTypeInference)); n != 4 {
			t.Errorf("inference subset has %d entries, want 4 (got %v)", n, keysOf(got.OfKind(memops.ProviderTypeInference)))
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
		t.Fatalf("expected exactly 1 memops.ProviderFault, got %d: %+v", len(faults), faults)
	}
	if faults[0].Name != "broken-provider" {
		t.Errorf("fault Name = %q, want broken-provider", faults[0].Name)
	}
	for _, leak := range []string{"somekey", "KEY_SECURITY_TEST_FAIL"} {
		if strings.Contains(faults[0].Reason, leak) {
			t.Errorf("memops.ProviderFault.Reason carries key material (%q)", leak)
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
	for _, name := range []string{"dummy-router", "dummy-inference", "dummy-embedding", "dummy-search"} {
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
		t.Errorf("expected empty memops.Providers, got %d entries: %v", len(got), keysOf(got))
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
		t.Errorf("expected empty memops.Providers from comment-only file, got %d entries", len(got))
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
		t.Errorf("expected empty memops.Providers on missing file, got %d entries", len(got))
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
type = "inference"
api = "openai"

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

func keysOf(p memops.Providers) []string {
	out := make([]string, 0, len(p))
	for k := range p {
		out = append(out, k)
	}
	return out
}

// TestLoadProvidersRejectsUndeclaredKind: `type` and `api` are REQUIRED
// and must agree. An entry that declares neither, or declares a pairing
// the runtime cannot route, is dropped as a NAMED fault — never silently
// absent from the selection lists, which is how a "why is my provider
// gone?" afternoon starts. The healthy entry beside it still loads.
func TestLoadProvidersRejectsUndeclaredKind(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entry    string
		wantHint string
	}{
		{"no type or api", "baseUrl = \"http://x/v1\"\napiKeyUnsafe = \"k\"\n", "no type declared"},
		{"unknown type", "baseUrl = \"http://x/v1\"\napiKeyUnsafe = \"k\"\ntype = \"telepathy\"\napi = \"openai\"\n", "unknown type"},
		{"no api", "baseUrl = \"http://x/v1\"\napiKeyUnsafe = \"k\"\ntype = \"search\"\n", "no api declared"},
		{"api does not match type", "baseUrl = \"http://x/v1\"\napiKeyUnsafe = \"k\"\ntype = \"inference\"\napi = \"exa\"\n", "not valid for a inference provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "providers.toml")
			body := "[healthy]\nbaseUrl = \"http://ok/v1\"\napiKeyUnsafe = \"k\"\ntype = \"inference\"\napi = \"openai\"\n\n[suspect]\n" + tc.entry
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatalf("write providers.toml: %v", err)
			}

			got, faults, err := LoadProviders(path)
			if err != nil {
				t.Fatalf("an invalid ENTRY must not fail the whole load: %v", err)
			}
			if _, ok := got.Get("suspect"); ok {
				t.Error("an unroutable provider was admitted to the pool")
			}
			if _, ok := got.Get("healthy"); !ok {
				t.Error("one bad entry took the rest of the pool with it")
			}
			if len(faults) != 1 || faults[0].Name != "suspect" {
				t.Fatalf("want exactly one fault naming suspect, got %+v", faults)
			}
			if !strings.Contains(faults[0].Reason, tc.wantHint) {
				t.Errorf("fault reason %q does not contain %q", faults[0].Reason, tc.wantHint)
			}
		})
	}
}
