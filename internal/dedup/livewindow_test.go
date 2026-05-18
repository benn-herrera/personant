package dedup

import (
	"fmt"
	"strings"
	"testing"
)

// TestLiveWindowClassification builds an 8-version chain and checks that
// LiveWindow(3) yields 1 current + 3 diff + 4 identifier entries in
// oldest-first order with the §3.9.2 representations.
func TestLiveWindowClassification(t *testing.T) {
	const n = 8
	c := New()
	want := make([]string, n)
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("title\nrevision %d\n", i)
		for j := 0; j <= i; j++ {
			body += fmt.Sprintf("body line %d\n", j)
		}
		want[i] = body
		c.Append(body)
	}

	entries, err := c.LiveWindow(3)
	if err != nil {
		t.Fatalf("LiveWindow(3): %v", err)
	}
	if len(entries) != n {
		t.Fatalf("got %d entries, want %d", len(entries), n)
	}

	// Oldest-first: versions 0..3 identifier, 4..6 diff, 7 current.
	wantKinds := []windowKind{
		kindIdentifier, kindIdentifier, kindIdentifier, kindIdentifier,
		kindDiff, kindDiff, kindDiff,
		kindCurrent,
	}
	for i, e := range entries {
		if e.Version != i {
			t.Errorf("entry %d: Version = %d, want %d", i, e.Version, i)
		}
		if e.Kind != wantKinds[i] {
			t.Errorf("entry %d: Kind = %q, want %q", i, e.Kind, wantKinds[i])
		}
	}

	// Current entry text equals Current().
	cur := entries[n-1]
	if cur.Text != c.Current() {
		t.Errorf("current entry text mismatch\n got: %q\nwant: %q", cur.Text, c.Current())
	}

	// Each diff entry, applied to the next-newer version, reconstructs
	// the older version.
	for _, e := range entries {
		if e.Kind != kindDiff {
			continue
		}
		newer, err := c.Reconstruct(e.Version + 1)
		if err != nil {
			t.Fatalf("Reconstruct(%d): %v", e.Version+1, err)
		}
		got, err := applyDiff(newer, e.Text)
		if err != nil {
			t.Fatalf("applyDiff for version %d: %v", e.Version, err)
		}
		if got != want[e.Version] {
			t.Errorf("diff entry %d applied mismatch\n got: %q\nwant: %q",
				e.Version, got, want[e.Version])
		}
	}

	// Identifier text matches the exact bracketed shape.
	for _, e := range entries {
		if e.Kind != kindIdentifier {
			continue
		}
		if !strings.HasPrefix(e.Text, "[content #") ||
			!strings.HasSuffix(e.Text, " — see most-recent position]") {
			t.Errorf("identifier entry %d: unexpected shape %q", e.Version, e.Text)
		}
		want := identifierText(want[e.Version])
		if e.Text != want {
			t.Errorf("identifier entry %d:\n got: %q\nwant: %q", e.Version, e.Text, want)
		}
	}
}

// TestLiveWindowZeroDiffs: recentDiffs=0 → current + identifiers only.
func TestLiveWindowZeroDiffs(t *testing.T) {
	c := New()
	for i := 0; i < 5; i++ {
		c.Append(fmt.Sprintf("version %d\n", i))
	}
	entries, err := c.LiveWindow(0)
	if err != nil {
		t.Fatalf("LiveWindow(0): %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5", len(entries))
	}
	for i, e := range entries {
		want := kindIdentifier
		if i == 4 {
			want = kindCurrent
		}
		if e.Kind != want {
			t.Errorf("entry %d: Kind = %q, want %q", i, e.Kind, want)
		}
	}
}

// TestLiveWindowFewerVersionsThanWindow: when the chain is shorter than
// recentDiffs+1, every non-current version is a diff and there are no
// identifiers.
func TestLiveWindowFewerVersionsThanWindow(t *testing.T) {
	c := New()
	for i := 0; i < 3; i++ {
		c.Append(fmt.Sprintf("v%d\n", i))
	}
	entries, err := c.LiveWindow(LiveDiffWindow)
	if err != nil {
		t.Fatalf("LiveWindow: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	wantKinds := []windowKind{kindDiff, kindDiff, kindCurrent}
	for i, e := range entries {
		if e.Kind != wantKinds[i] {
			t.Errorf("entry %d: Kind = %q, want %q", i, e.Kind, wantKinds[i])
		}
	}
}

func TestLiveWindowEdgeCases(t *testing.T) {
	t.Run("empty chain", func(t *testing.T) {
		c := New()
		entries, err := c.LiveWindow(LiveDiffWindow)
		if err != nil {
			t.Fatalf("LiveWindow on empty chain: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("empty chain should yield 0 entries, got %d", len(entries))
		}
	})

	t.Run("negative window", func(t *testing.T) {
		c := New()
		c.Append("v0\n")
		if _, err := c.LiveWindow(-1); err == nil {
			t.Fatal("LiveWindow(-1) should error")
		}
	})

	t.Run("single version is current", func(t *testing.T) {
		c := New()
		c.Append("only\n")
		entries, err := c.LiveWindow(LiveDiffWindow)
		if err != nil {
			t.Fatalf("LiveWindow: %v", err)
		}
		if len(entries) != 1 || entries[0].Kind != kindCurrent {
			t.Fatalf("single version should yield one current entry, got %+v", entries)
		}
		if entries[0].Text != "only\n" {
			t.Errorf("current text = %q, want %q", entries[0].Text, "only\n")
		}
	})
}
