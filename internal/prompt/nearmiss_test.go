package prompt

import (
	"strings"
	"testing"
)

// TestClassifyNearMiss is the forensic near-miss taxonomy table: it locks
// in which shapes count as an attempted-but-invalid tag and how each is
// classified. The detector is a loose heuristic (not the §5.1.2 contract),
// so the table doubles as its documented behavior.
func TestClassifyNearMiss(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantOK     bool
		wantReason string
	}{
		{
			name:   "valid tag is not a near-miss",
			in:     "*topic: thr_1 [trefoil]*\nbody",
			wantOK: false,
		},
		{
			name:   "no tag at all",
			in:     "just a normal response with no tag anywhere",
			wantOK: false,
		},
		{
			name:   "prose mentioning topic is not a near-miss",
			in:     "the main topic: was knot theory this time",
			wantOK: false,
		},
		{
			name:       "bold-mangled tag",
			in:         "**topic:** thr_1 [trefoil, unknot]",
			wantOK:     true,
			wantReason: NearMissMarkdownMangled,
		},
		{
			name:       "code-fence wrapped tag",
			in:         "`*topic: thr_1 [trefoil]*`",
			wantOK:     true,
			wantReason: NearMissMarkdownMangled,
		},
		{
			name:       "missing asterisk frame",
			in:         "topic: thr_1 [trefoil]",
			wantOK:     true,
			wantReason: NearMissBadDelimiters,
		},
		{
			name:       "missing anchor brackets",
			in:         "*topic: thr_1 trefoil, unknot*",
			wantOK:     true,
			wantReason: NearMissBadDelimiters,
		},
		{
			name:       "missing trailing asterisk",
			in:         "*topic: thr_1 [trefoil]",
			wantOK:     true,
			wantReason: NearMissBadDelimiters,
		},
		{
			name:       "empty thread list",
			in:         "*topic:  [trefoil]*",
			wantOK:     true,
			wantReason: NearMissBadThreadList,
		},
		{
			name:       "invalid thread token",
			in:         "*topic: garbage [trefoil]*",
			wantOK:     true,
			wantReason: NearMissBadThreadList,
		},
		{
			name:       "near-miss on a later line after prose",
			in:         "Here you go:\n**topic:** thr_1 [trefoil]",
			wantOK:     true,
			wantReason: NearMissMarkdownMangled,
		},
		{
			// The bare new-topic alias (§5.1.2) makes this shape a VALID
			// tag — it must never be double-counted as an attempted-but-
			// invalid near-miss.
			name:   "valid bare new-topic alias is not a near-miss",
			in:     "*new-topic* [trefoil, unknot]\nbody",
			wantOK: false,
		},
		{
			// The unclosed-inner-literal tolerance (§5.1.2; 2026-07-16 live
			// evidence, 364/380 misses) makes this shape a VALID tag — it must
			// stop counting as a bad-thread-list near-miss.
			name:   "unclosed inner new-topic in canonical wrapper is not a near-miss",
			in:     "*topic: *new-topic [trefoil, unknot]*\nbody",
			wantOK: false,
		},
		{
			name:       "bare new-topic without anchor brackets",
			in:         "*new-topic*\nbody",
			wantOK:     true,
			wantReason: NearMissBareNewTopic,
		},
		{
			name:       "bare new-topic with unclosed anchor list",
			in:         "*new-topic* [trefoil\nbody",
			wantOK:     true,
			wantReason: NearMissBareNewTopic,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nm, ok := ClassifyNearMiss(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ClassifyNearMiss(%q) ok = %v, want %v (nm=%+v)", tc.in, ok, tc.wantOK, nm)
			}
			if !tc.wantOK {
				return
			}
			if nm.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", nm.Reason, tc.wantReason)
			}
			if nm.Snippet == "" {
				t.Errorf("snippet must be non-empty for a near-miss")
			}
		})
	}
}

// TestClassifyNearMissSnippetBounded verifies the snippet never carries
// full content: it is truncated to NearMissSnippetLen runes and stays on
// one line.
func TestClassifyNearMissSnippetBounded(t *testing.T) {
	long := "topic: thr_1 [" + strings.Repeat("anchor-name, ", 40) + "]"
	nm, ok := ClassifyNearMiss(long)
	if !ok {
		t.Fatalf("expected a near-miss for the long candidate")
	}
	if n := len([]rune(nm.Snippet)); n > NearMissSnippetLen {
		t.Errorf("snippet is %d runes, want <= %d", n, NearMissSnippetLen)
	}
	if strings.ContainsAny(nm.Snippet, "\n\r") {
		t.Errorf("snippet must be a single line, got %q", nm.Snippet)
	}
}
