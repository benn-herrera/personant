package modellist

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

// writeProviders writes a providers.toml file at home/providers.toml with
// a single [test] table pointing at baseURL, and returns the resolved
// PersonantPaths.
func writeProviders(t *testing.T, baseURL string) store.PersonantPaths {
	t.Helper()
	home := t.TempDir()
	body := fmt.Sprintf(`[test]
baseUrl = %q
apiKey = "dummy"
type = "inference"
api = "openai"
`, baseURL)
	path := filepath.Join(home, "providers.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write providers.toml: %v", err)
	}
	return store.PathsForHome(home)
}

func TestRunSortedOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path: got %q, want /models", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method: got %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		// Intentionally unsorted.
		_, _ = w.Write([]byte(`{
			"object": "list",
			"data": [
				{"id": "zeta", "object": "model", "created": 1, "owned_by": "x"},
				{"id": "alpha", "object": "model", "created": 2, "owned_by": "x"},
				{"id": "mu", "object": "model", "created": 3, "owned_by": "x"}
			]
		}`))
	}))
	defer srv.Close()

	paths := writeProviders(t, srv.URL)
	var stdout, stderr bytes.Buffer
	err := Run(fileadapter.NewFileAdapter(paths), Options{
		Provider: "test",
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantStdout := "alpha\nmu\nzeta\n"
	if stdout.String() != wantStdout {
		t.Errorf("stdout: got %q, want %q", stdout.String(), wantStdout)
	}

	summary := stderr.String()
	if !strings.HasPrefix(summary, "models ok: test count=3 elapsed=") {
		t.Errorf("stderr summary: got %q, want prefix %q", summary, "models ok: test count=3 elapsed=")
	}
}

func TestRunEmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srv.Close()

	paths := writeProviders(t, srv.URL)
	var stdout, stderr bytes.Buffer
	if err := Run(fileadapter.NewFileAdapter(paths), Options{Provider: "test", Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty for empty list, got %q", stdout.String())
	}
	if !strings.HasPrefix(stderr.String(), "models ok: test count=0 elapsed=") {
		t.Errorf("stderr summary: got %q", stderr.String())
	}
}

func TestRunProviderMissing(t *testing.T) {
	paths := writeProviders(t, "http://unused.example")
	var stdout, stderr bytes.Buffer
	err := Run(fileadapter.NewFileAdapter(paths), Options{Provider: "no-such-provider", Stdout: &stdout, Stderr: &stderr})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), `provider "no-such-provider" not found`) {
		t.Errorf("error text: got %v, want substring %q", err, `provider "no-such-provider" not found`)
	}
}

// writeProvidersWithFault writes a providers.toml with a healthy provider
// (good, pointing at baseURL) and a faulted one (bad, referencing an
// apiKeyFile that does not exist so it drops from the pool). Returns the
// paths.
func writeProvidersWithFault(t *testing.T, baseURL string) store.PersonantPaths {
	t.Helper()
	home := t.TempDir()
	body := fmt.Sprintf(`[good]
baseUrl = %q
apiKey = "dummy"
type = "inference"
api = "openai"

[bad]
baseUrl = "http://unused.example"
apiKeyFile = "does-not-exist.key"
type = "inference"
api = "openai"
`, baseURL)
	if err := os.WriteFile(filepath.Join(home, "providers.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write providers.toml: %v", err)
	}
	return store.PathsForHome(home)
}

// TestRunSurfacesLoadFaultsAsWarnings: a provider dropped for an unreadable
// apiKeyFile must appear on stderr as a warning naming the provider — not
// vanish silently while a healthy provider still resolves.
func TestRunSurfacesLoadFaultsAsWarnings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1","object":"model"}]}`))
	}))
	defer srv.Close()

	paths := writeProvidersWithFault(t, srv.URL)
	var stdout, stderr bytes.Buffer
	if err := Run(fileadapter.NewFileAdapter(paths), Options{Provider: "good", Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr.String(), `provider "bad" unavailable`) {
		t.Errorf("stderr should warn about the faulted provider; got %q", stderr.String())
	}
	// The healthy provider still resolved and listed.
	if stdout.String() != "m1\n" {
		t.Errorf("stdout: got %q, want %q", stdout.String(), "m1\n")
	}
}

// TestRunFaultedProviderRequestedNamesFault: requesting the faulted
// provider must return an error that says it failed to load — distinct from
// the "not found" message a typo would produce.
func TestRunFaultedProviderRequestedNamesFault(t *testing.T) {
	paths := writeProvidersWithFault(t, "http://unused.example")
	var stdout, stderr bytes.Buffer
	err := Run(fileadapter.NewFileAdapter(paths), Options{Provider: "bad", Stdout: &stdout, Stderr: &stderr})
	if err == nil {
		t.Fatal("expected error for faulted provider, got nil")
	}
	if !strings.Contains(err.Error(), `provider "bad" failed to load`) {
		t.Errorf("error should name the load fault; got %v", err)
	}
}

func TestRunDefaultsToLocal(t *testing.T) {
	// Provider name is "local" — Options.Provider left empty, defaults to "local".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"only","object":"model"}]}`))
	}))
	defer srv.Close()

	home := t.TempDir()
	body := fmt.Sprintf(`[local]
baseUrl = %q
apiKey = ""
type = "inference"
api = "openai"
`, srv.URL)
	if err := os.WriteFile(filepath.Join(home, "providers.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(fileadapter.NewFileAdapter(store.PathsForHome(home)), Options{Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.String() != "only\n" {
		t.Errorf("stdout: got %q, want %q", stdout.String(), "only\n")
	}
	if !strings.HasPrefix(stderr.String(), "models ok: local count=1 elapsed=") {
		t.Errorf("stderr: got %q", stderr.String())
	}
}

func TestRunTimeoutHonored(t *testing.T) {
	// Server hangs until stop is closed (or its context fires); the
	// caller's tight timeout should fire well before that.
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer func() {
		close(stop)
		srv.Close()
	}()

	paths := writeProviders(t, srv.URL)
	var stdout, stderr bytes.Buffer
	err := Run(fileadapter.NewFileAdapter(paths), Options{
		Provider: "test",
		Timeout:  50 * time.Millisecond,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// http.Client wraps the ctx error; we want to still be able to detect
	// it via errors.Is.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestRunHTTPErrorScrubsKey(t *testing.T) {
	const apiKey = "sk-LEAKY-MUST-NOT-APPEAR-1234"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// Worst case: the server reflects the bearer token into the body.
		_, _ = w.Write([]byte("forbidden: token=" + r.Header.Get("Authorization")))
	}))
	defer srv.Close()

	home := t.TempDir()
	body := fmt.Sprintf(`[test]
baseUrl = %q
apiKey = %q
type = "inference"
api = "openai"
`, srv.URL, apiKey)
	if err := os.WriteFile(filepath.Join(home, "providers.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(fileadapter.NewFileAdapter(store.PathsForHome(home)), Options{Provider: "test", Stdout: &stdout, Stderr: &stderr})
	if err == nil {
		t.Fatal("expected 403 error, got nil")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("API key leaked into error: %v", err)
	}
}
