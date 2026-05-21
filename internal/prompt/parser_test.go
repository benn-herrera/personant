package prompt

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"personant/internal/memops"
)

func TestParseHappyPath(t *testing.T) {
	in := "*topic: thr_42 [trefoil, unknot, body-topology, electron-shape]*\nresponse body"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	wantThreads := []string{"thr_42"}
	wantAnchors := []string{"trefoil", "unknot", "body-topology", "electron-shape"}
	if !reflect.DeepEqual(got.Tag.Threads, wantThreads) {
		t.Errorf("Threads: got %v, want %v", got.Tag.Threads, wantThreads)
	}
	if !reflect.DeepEqual(got.Tag.Anchors, wantAnchors) {
		t.Errorf("Anchors: got %v, want %v", got.Tag.Anchors, wantAnchors)
	}
	if got.Body != "response body" {
		t.Errorf("Body: got %q, want %q", got.Body, "response body")
	}
	if len(got.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", got.Warnings)
	}
}

func TestParseMultipleThreads(t *testing.T) {
	in := "*topic: thr_42, thr_88 [a, b, c, d]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"thr_42", "thr_88"}
	if !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("Threads: got %v, want %v", got.Tag.Threads, want)
	}
}

func TestParseNewTopicLiteral(t *testing.T) {
	in := "*topic: *new-topic* [a, b, c, d]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"*new-topic*"}
	if !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("Threads: got %v, want %v", got.Tag.Threads, want)
	}
}

func TestParseMixedExplicitAndNewTopic(t *testing.T) {
	in := "*topic: thr_42, *new-topic* [a, b, c, d]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"thr_42", "*new-topic*"}
	if !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("Threads: got %v, want %v", got.Tag.Threads, want)
	}
}

func TestParseAnchorNormalization(t *testing.T) {
	in := "*topic: thr_1 [Cosserat Sector, the trefoil, body-topology, electron]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"cosserat-sector", "trefoil", "body-topology", "electron"}
	if !reflect.DeepEqual(got.Tag.Anchors, want) {
		t.Errorf("Anchors: got %v, want %v", got.Tag.Anchors, want)
	}
}

func TestParseAnchorCountWarningTooFew(t *testing.T) {
	// One anchor below the hard minimum — boundary case for the §2.2 range.
	want := memops.MinAnchorsPerThread - 1
	in := makeAnchorTagInput(want)
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Tag.Anchors) != want {
		t.Errorf("Anchors len: got %d, want %d", len(got.Tag.Anchors), want)
	}
	if !hasWarningContaining(got.Warnings, fmt.Sprintf("anchor count %d", want)) {
		t.Errorf("expected warning naming count %d, got %v", want, got.Warnings)
	}
}

func TestParseAnchorCountWarningTooMany(t *testing.T) {
	// One anchor above the hard maximum — boundary case for the §2.2 range.
	want := memops.MaxAnchorsPerThread + 1
	in := makeAnchorTagInput(want)
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Tag.Anchors) != want {
		t.Errorf("Anchors len: got %d, want %d", len(got.Tag.Anchors), want)
	}
	if !hasWarningContaining(got.Warnings, fmt.Sprintf("anchor count %d", want)) {
		t.Errorf("expected warning naming count %d, got %v", want, got.Warnings)
	}
}

// makeAnchorTagInput synthesizes a topic-tag line carrying n synthetic
// anchor symbols ("a1, a2, ..."). Used by the anchor-cardinality
// boundary tests so the inputs track memops.MinAnchorsPerThread /
// MaxAnchorsPerThread rather than baking the bounds into the fixture.
func makeAnchorTagInput(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("a%d", i+1)
	}
	return "*topic: thr_1 [" + strings.Join(parts, ", ") + "]*\nbody"
}

func TestParseMultipleValidTagsFirstWins(t *testing.T) {
	in := "*topic: thr_1 [a, b, c, d]*\nbody one\n" +
		"*topic: thr_2 [e, f, g, h]*\nbody two\n" +
		"*topic: thr_3 [i, j, k, l]*\nbody three"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Tag.Threads[0] != "thr_1" {
		t.Errorf("first wins: got %v", got.Tag.Threads)
	}
	if !hasWarningContaining(got.Warnings, "additional topic tag(s) ignored: 2") {
		t.Errorf("expected extras=2 warning, got %v", got.Warnings)
	}
	// Subsequent tags retained as-is in the body.
	if !strings.Contains(got.Body, "*topic: thr_2 [e, f, g, h]*") {
		t.Errorf("expected later tag retained in body, body=%q", got.Body)
	}
}

func TestParseTagMidDocument(t *testing.T) {
	in := "line one\n" +
		"line two\n" +
		"line three\n" +
		"line four\n" +
		"*topic: thr_5 [a, b, c, d]*\n" +
		"line six\n" +
		"line seven\n"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Tag.Threads[0] != "thr_5" {
		t.Errorf("Threads: got %v", got.Tag.Threads)
	}
	wantBody := "line one\nline two\nline three\nline four\nline six\nline seven\n"
	if got.Body != wantBody {
		t.Errorf("Body:\n got: %q\nwant: %q", got.Body, wantBody)
	}
}

func TestParseLeadingWhitespace(t *testing.T) {
	in := "   \t*topic: thr_1 [a, b, c, d]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Tag.Threads[0] != "thr_1" {
		t.Errorf("Threads: got %v", got.Tag.Threads)
	}
	if got.Body != "body" {
		t.Errorf("Body: got %q, want %q", got.Body, "body")
	}
}

func TestParseNoTagAtAll(t *testing.T) {
	in := "just a plain response\nwith multiple lines\nand no topic tag"
	got, err := Parse(in)
	if !errors.Is(err, ErrNoTopicTag) {
		t.Fatalf("expected ErrNoTopicTag, got %v", err)
	}
	if got.Body != in {
		t.Errorf("Body must be preserved on no-tag, got %q", got.Body)
	}
}

func TestParseEmptyThreadList(t *testing.T) {
	in := "*topic:  [a, b, c, d]*\nbody"
	_, err := Parse(in)
	if !errors.Is(err, ErrNoTopicTag) {
		t.Fatalf("expected ErrNoTopicTag for empty thread list, got %v", err)
	}
}

func TestParseEmptyAnchorList(t *testing.T) {
	in := "*topic: thr_42 []*\nbody"
	_, err := Parse(in)
	if !errors.Is(err, ErrNoTopicTag) {
		t.Fatalf("expected ErrNoTopicTag for empty anchor list, got %v", err)
	}
}

func TestParseMalformedThreadID(t *testing.T) {
	in := "*topic: foo [a, b, c, d]*\nbody"
	_, err := Parse(in)
	if !errors.Is(err, ErrNoTopicTag) {
		t.Fatalf("expected ErrNoTopicTag for bad thread id, got %v", err)
	}
}

// A malformed tag should not gate a later valid tag from winning.
func TestParseMalformedThreadIDFollowedByValid(t *testing.T) {
	in := "*topic: foo [a, b, c, d]*\n" +
		"*topic: thr_7 [w, x, y, z]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Tag.Threads[0] != "thr_7" {
		t.Errorf("Threads: got %v, want [thr_7]", got.Tag.Threads)
	}
}

func TestParseTrailingCommaInAnchors(t *testing.T) {
	in := "*topic: thr_1 [a, b, c, d,]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(got.Tag.Anchors, want) {
		t.Errorf("Anchors: got %v, want %v", got.Tag.Anchors, want)
	}
	// Four anchors → no out-of-range warning.
	if len(got.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", got.Warnings)
	}
}

func TestParseWhitespaceInAnchorList(t *testing.T) {
	in := "*topic: thr_1 [ a , b , c , d ]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(got.Tag.Anchors, want) {
		t.Errorf("Anchors: got %v, want %v", got.Tag.Anchors, want)
	}
}

func TestParseTrailingCommaInThreads(t *testing.T) {
	// A trailing comma in the thread list yields an empty entry which fails
	// the §5.1.2 alternation. The whole tag is rejected.
	in := "*topic: thr_1, [a, b, c, d]*\nbody"
	_, err := Parse(in)
	if !errors.Is(err, ErrNoTopicTag) {
		t.Fatalf("expected ErrNoTopicTag, got %v", err)
	}
}

func TestParseTagNotOnOwnLine(t *testing.T) {
	// The §5.1.2 regex anchors with ^...$ in multi-line mode; anything other
	// than whitespace on the same line invalidates the match.
	in := "prefix *topic: thr_1 [a, b, c, d]* suffix\n"
	_, err := Parse(in)
	if !errors.Is(err, ErrNoTopicTag) {
		t.Fatalf("expected ErrNoTopicTag for embedded tag, got %v", err)
	}
}

func TestParseBodyPreservesContent(t *testing.T) {
	// The parser strips only the chosen tag's line + its trailing newline.
	// All other whitespace (interior blank lines, indentation) is preserved.
	in := "*topic: thr_1 [a, b, c, d]*\n\n  indented line\n\nlast"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := "\n  indented line\n\nlast"
	if got.Body != want {
		t.Errorf("Body:\n got: %q\nwant: %q", got.Body, want)
	}
}

func TestPreambleScanFirstLineTag(t *testing.T) {
	buf := []byte("*topic: thr_1 [a, b, c, d]*\nbody")
	start, end, found, done := PreambleScan(buf)
	if !found || !done {
		t.Fatalf("found=%v done=%v, want both true", found, done)
	}
	if got := string(buf[start:end]); got != "*topic: thr_1 [a, b, c, d]*" {
		t.Errorf("span: got %q", got)
	}
}

func TestPreambleScanTagAfterThinkBlock(t *testing.T) {
	buf := []byte("<think>\nreasoning about thr_5\n</think>\n*topic: thr_5 [a, b, c, d]*\nbody")
	start, end, found, done := PreambleScan(buf)
	if !found || !done {
		t.Fatalf("found=%v done=%v, want both true", found, done)
	}
	if got := string(buf[start:end]); got != "*topic: thr_5 [a, b, c, d]*" {
		t.Errorf("span: got %q", got)
	}
}

func TestPreambleScanUnterminatedTagUndecided(t *testing.T) {
	// A tag line whose terminating newline has not yet streamed must not
	// resolve — more bytes might extend the line.
	buf := []byte("filler\n*topic: thr_5 [a, b, c, d]*")
	_, _, found, done := PreambleScan(buf)
	if found || done {
		t.Fatalf("found=%v done=%v, want both false (unterminated tag)", found, done)
	}
}

func TestPreambleScanPartialUndecided(t *testing.T) {
	buf := []byte("<think>\nstill reasoning")
	_, _, found, done := PreambleScan(buf)
	if found || done {
		t.Fatalf("found=%v done=%v, want both false (more may arrive)", found, done)
	}
}

func TestPreambleScanLineCapExhausted(t *testing.T) {
	var b strings.Builder
	for i := 0; i < PreambleScanLineCap+1; i++ {
		b.WriteString("noise\n")
	}
	b.WriteString("*topic: thr_5 [a, b, c, d]*\n")
	_, _, found, done := PreambleScan([]byte(b.String()))
	if found {
		t.Errorf("tag past the line cap should not be found")
	}
	if !done {
		t.Errorf("scan past the line cap must be done")
	}
}

// hasWarningContaining is a test helper that returns true if any of the
// strings in ws contains the substring sub.
func hasWarningContaining(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
