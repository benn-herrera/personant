package shell

import (
	"strings"
	"testing"
)

// A synthetic key — key-SHAPED, not a real credential. Nothing in this
// package ever reads a real key file.
const fakeKey = "sk-test-0123456789abcdefghijklmnop"

func TestRedactorReplacesKeyMaterial(t *testing.T) {
	r := NewRedactor([]string{fakeKey})
	in := "PROVIDER_KEY=" + fakeKey + "\nOTHER=fine\nAGAIN=" + fakeKey + "\n"

	out, hits, removed := r.Redact(in)

	if strings.Contains(out, fakeKey) {
		t.Fatalf("key material survived redaction")
	}
	if hits != 2 {
		t.Errorf("occurrences: got %d want 2", hits)
	}
	if removed != 2*len(fakeKey) {
		t.Errorf("bytesRemoved: got %d want %d", removed, 2*len(fakeKey))
	}
	if strings.Count(out, RedactionPlaceholder) != 2 {
		t.Errorf("placeholder not substituted per occurrence: %q", out)
	}
	// Surrounding context survives — the point of per-occurrence
	// substitution over whole-buffer replacement.
	if !strings.Contains(out, "OTHER=fine") {
		t.Errorf("unrelated capture content was destroyed: %q", out)
	}
}

func TestRedactorInertCases(t *testing.T) {
	tests := []struct {
		name string
		keys []string
	}{
		{"no keys", nil},
		{"empty key", []string{""}},
		{"degenerate short key", []string{"a"}},
		{"below minimum length", []string{strings.Repeat("k", minRedactableKey-1)}},
	}
	const text = "aaa bbb ka kkkkkkk\n"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, hits, removed := NewRedactor(tt.keys).Redact(text)
			if out != text || hits != 0 || removed != 0 {
				t.Errorf("a key too short to protect anything must not shred output: got %q hits=%d", out, hits)
			}
		})
	}

	// A nil Redactor is a valid inert redactor, so callers need no branch.
	var nilR *Redactor
	if out, hits, _ := nilR.Redact(text); out != text || hits != 0 {
		t.Errorf("nil Redactor must pass through")
	}
}

func TestRedactorDeduplicatesKeys(t *testing.T) {
	r := NewRedactor([]string{fakeKey, fakeKey, ""})
	if len(r.keys) != 1 {
		t.Fatalf("duplicate/empty keys not dropped: %d entries", len(r.keys))
	}
	_, hits, _ := r.Redact("x=" + fakeKey)
	if hits != 1 {
		t.Errorf("a duplicated key must be counted once: got %d", hits)
	}
}
