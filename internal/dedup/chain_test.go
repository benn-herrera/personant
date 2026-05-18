package dedup

import (
	"encoding/json"
	"fmt"
	"testing"
)

// assertReconstructAll checks that every version in c byte-matches the
// corresponding entry in want.
func assertReconstructAll(t *testing.T, c *Chain, want []string) {
	t.Helper()
	if c.Len() != len(want) {
		t.Fatalf("Len() = %d, want %d", c.Len(), len(want))
	}
	for i, w := range want {
		got, err := c.Reconstruct(i)
		if err != nil {
			t.Fatalf("Reconstruct(%d) error: %v", i, err)
		}
		if got != w {
			t.Fatalf("Reconstruct(%d) mismatch\n got: %q\nwant: %q", i, got, w)
		}
	}
}

// TestRoundTrip builds a chain crossing several anchor boundaries and
// reconstructs every version. This is the load-bearing correctness test.
func TestRoundTrip(t *testing.T) {
	const n = 35 // crosses anchor indices 0,10,20,30
	c := New()
	want := make([]string, n)
	for i := 0; i < n; i++ {
		// Incremental edits: a growing body with a per-version mutation,
		// so most deltas are small (delta-encoded) but content evolves.
		body := fmt.Sprintf("header line\nversion marker %d\n", i)
		for j := 0; j <= i; j++ {
			body += fmt.Sprintf("stable line %d\n", j)
		}
		want[i] = body
		c.Append(body)
	}
	assertReconstructAll(t, c, want)

	if c.Current() != want[n-1] {
		t.Fatalf("Current() != last version")
	}
}

// TestAnchorCadence confirms that demoted versions on K-th boundaries
// are kept as literals.
func TestAnchorCadence(t *testing.T) {
	c := New()
	// A large stable body with one small per-version mutation: the
	// reverse-delta stays well under the diff-literal threshold, so
	// non-anchor demoted versions are stored as deltas and the only
	// literals are the current version and anchor boundaries.
	body := func(i int) string {
		s := fmt.Sprintf("mutable header for version %d\n", i)
		for j := 0; j < 40; j++ {
			s += fmt.Sprintf("stable body line %d, unchanging filler text\n", j)
		}
		return s
	}
	for i := 0; i < 25; i++ {
		c.Append(body(i))
	}
	for i := 0; i < c.Len(); i++ {
		isCurrent := i == c.Len()-1
		isAnchor := i%AnchorCadence == 0
		switch {
		case isCurrent:
			if c.Versions[i].Kind != kindLiteral {
				t.Fatalf("version %d (current) must be literal", i)
			}
		case isAnchor:
			if c.Versions[i].Kind != kindLiteral {
				t.Fatalf("version %d on anchor boundary must be literal, got %s", i, c.Versions[i].Kind)
			}
		default:
			if c.Versions[i].Kind != kindDelta {
				t.Fatalf("version %d (small edit, non-anchor) expected delta, got %s", i, c.Versions[i].Kind)
			}
		}
	}
	// Reconstruction still byte-exact.
	for i := 0; i < c.Len(); i++ {
		got, err := c.Reconstruct(i)
		if err != nil {
			t.Fatalf("Reconstruct(%d): %v", i, err)
		}
		if got != body(i) {
			t.Fatalf("Reconstruct(%d) = %q, want %q", i, got, body(i))
		}
	}
}

// TestDiffLiteralThreshold confirms that a near-total rewrite between
// two non-anchor versions is stored as a literal, not a delta.
func TestDiffLiteralThreshold(t *testing.T) {
	c := New()
	// v0: anchor literal regardless.
	c.Append("alpha line one\nalpha line two\nalpha line three\n")
	// v1: complete rewrite — the reverse-delta would restate the whole
	// old body plus diff framing, exceeding the 0.7 threshold.
	c.Append("totally different content here\nnothing in common at all\n")
	// v2: current literal.
	c.Append("a third unrelated body\n")

	if c.Versions[1].Kind != kindLiteral {
		t.Fatalf("version 1 (near-total rewrite) should be stored as literal, got %s", c.Versions[1].Kind)
	}
	// And it must still reconstruct.
	got, err := c.Reconstruct(1)
	if err != nil {
		t.Fatalf("Reconstruct(1): %v", err)
	}
	if want := "totally different content here\nnothing in common at all\n"; got != want {
		t.Fatalf("Reconstruct(1) = %q, want %q", got, want)
	}
}

func TestEdgeCases(t *testing.T) {
	t.Run("empty chain", func(t *testing.T) {
		c := New()
		if c.Len() != 0 {
			t.Fatalf("Len() = %d, want 0", c.Len())
		}
		if c.Current() != "" {
			t.Fatalf("Current() = %q, want empty", c.Current())
		}
		if _, err := c.Reconstruct(0); err == nil {
			t.Fatal("Reconstruct(0) on empty chain should error")
		}
	})

	t.Run("zero value usable", func(t *testing.T) {
		var c Chain
		c.Append("only version\n")
		assertReconstructAll(t, &c, []string{"only version\n"})
	})

	t.Run("single version", func(t *testing.T) {
		c := New()
		c.Append("hello\n")
		assertReconstructAll(t, c, []string{"hello\n"})
	})

	t.Run("identical consecutive versions", func(t *testing.T) {
		c := New()
		same := "unchanging content\n"
		want := make([]string, 5)
		for i := range want {
			want[i] = same
			c.Append(same)
		}
		assertReconstructAll(t, c, want)
	})

	t.Run("empty content versions", func(t *testing.T) {
		c := New()
		want := []string{"", "non-empty\n", "", "back to content\n", ""}
		for _, w := range want {
			c.Append(w)
		}
		assertReconstructAll(t, c, want)
	})

	t.Run("trailing newline toggles", func(t *testing.T) {
		c := New()
		want := []string{"line\n", "line", "line\n", "line\nmore", "line\nmore\n"}
		for _, w := range want {
			c.Append(w)
		}
		assertReconstructAll(t, c, want)
	})

	t.Run("multi-region changes", func(t *testing.T) {
		c := New()
		want := []string{
			"a\nb\nc\nd\ne\nf\n",
			"A\nb\nc\nd\ne\nf\n",
			"A\nb\nC\nd\ne\nf\n",
			"A\nb\nC\nd\nE\nf\n",
			"A\nb\nC\nd\nE\nf\nG\n",
		}
		for _, w := range want {
			c.Append(w)
		}
		assertReconstructAll(t, c, want)
	})

	t.Run("out of range", func(t *testing.T) {
		c := New()
		c.Append("v0\n")
		c.Append("v1\n")
		if _, err := c.Reconstruct(-1); err == nil {
			t.Fatal("Reconstruct(-1) should error")
		}
		if _, err := c.Reconstruct(2); err == nil {
			t.Fatal("Reconstruct(2) should error")
		}
	})
}

// TestJSONRoundTrip marshals a chain, unmarshals it, and confirms every
// reconstructed version still byte-matches. This guards the persistence
// contract: a separate layer will marshal Chain to disk.
func TestJSONRoundTrip(t *testing.T) {
	c := New()
	const n = 30
	want := make([]string, n)
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("doc title\nrevision %d\n", i)
		for j := 0; j <= i; j++ {
			body += fmt.Sprintf("paragraph %d body text\n", j)
		}
		// Inject a full rewrite mid-stream to also exercise a
		// threshold-induced literal through serialization.
		if i == 17 {
			body = "an entirely unrelated short note\n"
		}
		want[i] = body
		c.Append(body)
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Chain
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	assertReconstructAll(t, &got, want)
	if got.Current() != want[n-1] {
		t.Fatalf("post-unmarshal Current() mismatch")
	}
}
