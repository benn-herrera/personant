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

// TestParseHighAnchorCountNoWarning — anchor-lifecycle Inc 1 deletes the
// out-of-range cardinality warning. An over-AnchorProjectionMax anchor
// list parses cleanly with no warning: the projection owns the ceiling
// deterministically (the strongest fold in), so the parser does not gate
// or warn on count.
func TestParseHighAnchorCountNoWarning(t *testing.T) {
	n := memops.AnchorProjectionMax + 1
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("a%d", i+1)
	}
	in := "*topic: thr_1 [" + strings.Join(parts, ", ") + "]*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Tag.Anchors) != n {
		t.Errorf("Anchors len: got %d, want %d (parser does not truncate)", len(got.Tag.Anchors), n)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("expected no cardinality warning, got %v", got.Warnings)
	}
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

// TestParseEmptyAnchorListIsValid — anchor-lifecycle Inc 1 / Risk R1: a
// tag whose anchor list normalizes to empty is now a VALID tag, not
// ErrNoTopicTag. The tag's job is thread binding (the valid thr_42 list
// satisfies it); anchors are an advisory per-turn contribution, and 0
// anchors is legal (a vague-start emission, spec §5.1 / §2.7.x). This
// reverses the prior tag-validity contract deliberately.
func TestParseEmptyAnchorListIsValid(t *testing.T) {
	in := "*topic: thr_42 []*\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("0-anchor tag with a valid thread list must parse, got %v", err)
	}
	if want := []string{"thr_42"}; !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("Threads: got %v, want %v", got.Tag.Threads, want)
	}
	if len(got.Tag.Anchors) != 0 {
		t.Errorf("Anchors: got %v, want empty (advisory, 0 legal)", got.Tag.Anchors)
	}
	if got.Body != "body" {
		t.Errorf("Body: got %q, want %q", got.Body, "body")
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

// --- Bare new-topic alias (§5.1.2 deterministic-tier absorption of the
// probe-observed unwrapped wire shape — 2026-07-15 elicitation probe
// round 3, signature (b); design burndown-2026-07b "RESOLVED"). The alias
// is strict: line-anchored, bracket pair required, same anchor-list
// grammar. ---

func TestParseBareNewTopicAlias(t *testing.T) {
	in := "*new-topic* [Trefoil, body-topology]\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []string{NewTopicLiteral}; !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("Threads: got %v, want %v", got.Tag.Threads, want)
	}
	// The alias shares the canonical anchor-list grammar, including §2.7.2
	// normalization.
	if want := []string{"trefoil", "body-topology"}; !reflect.DeepEqual(got.Tag.Anchors, want) {
		t.Errorf("Anchors: got %v, want %v", got.Tag.Anchors, want)
	}
	if got.Body != "body" {
		t.Errorf("Body: got %q, want %q (alias line must be stripped)", got.Body, "body")
	}
	if len(got.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", got.Warnings)
	}
}

func TestParseBareNewTopicAliasEmptyAnchorList(t *testing.T) {
	// Grammar parity with the canonical form: an empty-in-brackets anchor
	// list is a valid 0-anchor (vague-start) emission.
	in := "*new-topic* []\nbody"
	got, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []string{NewTopicLiteral}; !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("Threads: got %v, want %v", got.Tag.Threads, want)
	}
	if len(got.Tag.Anchors) != 0 {
		t.Errorf("Anchors: got %v, want empty", got.Tag.Anchors)
	}
}

func TestParseBareNewTopicWithoutAnchorListInvalid(t *testing.T) {
	// The alias is NOT a general loosening: without the `[...]` bracket
	// pair, a bare *new-topic* line stays invalid.
	for _, in := range []string{
		"*new-topic*\nbody",
		"*new-topic* trefoil, unknot\nbody",
		"*new-topic* [trefoil\nbody", // unclosed bracket
	} {
		if _, err := Parse(in); !errors.Is(err, ErrNoTopicTag) {
			t.Errorf("Parse(%q): expected ErrNoTopicTag, got %v", in, err)
		}
	}
}

func TestParseBareNewTopicMidLineInvalid(t *testing.T) {
	// Line-anchored: any non-whitespace before or after the alias on the
	// same line invalidates it (same discipline as the canonical form).
	for _, in := range []string{
		"see *new-topic* [trefoil]\nbody",
		"*new-topic* [trefoil] and more\nbody",
	} {
		if _, err := Parse(in); !errors.Is(err, ErrNoTopicTag) {
			t.Errorf("Parse(%q): expected ErrNoTopicTag, got %v", in, err)
		}
	}
}

func TestParseAliasAndCanonicalShareFirstValidMatchRule(t *testing.T) {
	// The alias participates in the same first-valid-match rule and the
	// same extras count as the canonical form, in document order.
	aliasFirst := "*new-topic* [a, b]\nbody\n*topic: thr_2 [c, d]*\nmore"
	got, err := Parse(aliasFirst)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []string{NewTopicLiteral}; !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("alias-first Threads: got %v, want %v", got.Tag.Threads, want)
	}
	if !hasWarningContaining(got.Warnings, "additional topic tag(s) ignored: 1") {
		t.Errorf("alias-first: expected extras=1 warning, got %v", got.Warnings)
	}

	canonicalFirst := "*topic: thr_2 [c, d]*\nbody\n*new-topic* [a, b]\nmore"
	got, err = Parse(canonicalFirst)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []string{"thr_2"}; !reflect.DeepEqual(got.Tag.Threads, want) {
		t.Errorf("canonical-first Threads: got %v, want %v", got.Tag.Threads, want)
	}
	if !hasWarningContaining(got.Warnings, "additional topic tag(s) ignored: 1") {
		t.Errorf("canonical-first: expected extras=1 warning, got %v", got.Warnings)
	}
}

func TestPreambleScanBareNewTopicAlias(t *testing.T) {
	// The streaming-side authority (stream-filter suppression, §5.5 timing,
	// D6 missing-tag trigger) must accept the alias exactly like Parse.
	buf := []byte("*new-topic* [a, b]\nbody")
	start, end, found, done := PreambleScan(buf)
	if !found || !done {
		t.Fatalf("found=%v done=%v, want both true", found, done)
	}
	if got := string(buf[start:end]); got != "*new-topic* [a, b]" {
		t.Errorf("span: got %q", got)
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
