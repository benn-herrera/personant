package autogit

import (
	"sort"
	"testing"
	"time"
)

// TestTagStamp_RoundTrip: TagStamp → ParseTagStamp recovers the same
// instant, UTC-normalized, and matches the documented example shape.
func TestTagStamp_RoundTrip(t *testing.T) {
	orig := time.Date(2026, 5, 8, 3, 12, 0, 0, time.FixedZone("PDT", -7*3600))
	got := TagStamp(orig)
	if want := "2026-05-08T10-12-00Z"; got != want {
		t.Fatalf("TagStamp = %q, want %q (UTC of 03:12 -07:00)", got, want)
	}
	back, err := ParseTagStamp(got)
	if err != nil {
		t.Fatalf("ParseTagStamp: %v", err)
	}
	if !back.Equal(orig) {
		t.Errorf("round-trip = %v, want %v", back, orig)
	}
}

// TestTagStamp_LexicalOrderIsChronological is the load-bearing property:
// sorting stamps as strings equals sorting the instants chronologically,
// across a DST boundary and mixed original UTC offsets. The Mar-8 trio
// carries the DST/offset stress (wall-clock 01:30 PST and 03:30 PDT are
// on the same local date but distinct UTC instants; 12:00 CET normalizes
// between them).
func TestTagStamp_LexicalOrderIsChronological(t *testing.T) {
	pst := time.FixedZone("PST", -8*3600)
	pdt := time.FixedZone("PDT", -7*3600)
	cet := time.FixedZone("CET", +2*3600)
	times := []time.Time{
		time.Date(2026, 3, 8, 1, 30, 0, 0, pst),  // 09:30Z — pre spring-forward
		time.Date(2026, 3, 8, 3, 30, 0, 0, pdt),  // 10:30Z — post spring-forward
		time.Date(2026, 3, 8, 12, 0, 0, 0, cet),  // 10:00Z
		time.Date(2026, 11, 1, 1, 30, 0, 0, pdt), // 08:30Z — much later date
	}

	chrono := append([]time.Time(nil), times...)
	sort.Slice(chrono, func(i, j int) bool { return chrono[i].Before(chrono[j]) })

	stamps := make([]string, len(times))
	for i, tm := range times {
		stamps[i] = TagStamp(tm)
	}
	sort.Strings(stamps)

	for i := range chrono {
		if want := TagStamp(chrono[i]); stamps[i] != want {
			t.Errorf("lexical order[%d] = %q, chronological order = %q — stamps do not sort chronologically", i, stamps[i], want)
		}
	}
}
