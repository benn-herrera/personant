package prompt

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamFilterStripsTopicTagFollowedByBody(t *testing.T) {
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "*topic: thr_42 [foo, bar, baz, qux]*\nresponse body here"
	n, err := f.Write([]byte(in))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Errorf("Write returned %d, want %d", n, len(in))
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := buf.String()
	want := "response body here"
	if got != want {
		t.Errorf("output: got %q, want %q", got, want)
	}
}

// TestStreamFilterStripsBareNewTopicAlias — the §5.1.2 bare new-topic
// alias is a valid tag, so the filter must suppress it exactly like the
// canonical form; a shape Parse accepts but the filter forwards would leak
// wire syntax to the terminal. Covers both the terminated (mid-stream) and
// newline-less trailing (Close-time fallback) positions.
func TestStreamFilterStripsBareNewTopicAlias(t *testing.T) {
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "*new-topic* [foo, bar]\nresponse body here"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got, want := buf.String(), "response body here"; got != want {
		t.Errorf("output: got %q, want %q", got, want)
	}

	var buf2 bytes.Buffer
	f2 := NewStreamFilter(&buf2)
	// Newline-less single alias line: unresolved at Write, classified and
	// suppressed by Close's isTopicTagLine fallback.
	if _, err := f2.Write([]byte("*new-topic* [foo, bar]")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := buf2.String(); got != "" {
		t.Errorf("trailing alias line must be suppressed at Close, got %q", got)
	}
}

func TestStreamFilterPassesThroughNonTagFirstLine(t *testing.T) {
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "Just a normal first line.\nSecond line."
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := buf.String(); got != in {
		t.Errorf("output: got %q, want %q", got, in)
	}
}

func TestStreamFilterTagSplitAcrossManyWrites(t *testing.T) {
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	// Simulate streaming the tag one rune at a time.
	in := "*topic: thr_1 [a, b, c, d]*\nbody after tag"
	for _, r := range in {
		if _, err := f.Write([]byte(string(r))); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := "body after tag"
	if got := buf.String(); got != want {
		t.Errorf("output: got %q, want %q", got, want)
	}
}

func TestStreamFilterSingleLineTagNoNewlineSuppressedAtClose(t *testing.T) {
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "*topic: thr_42 [a, b, c, d]*"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := buf.String(); got != "" {
		t.Errorf("expected suppressed, got %q", got)
	}
}

func TestStreamFilterSingleLineNonTagFlushedAtClose(t *testing.T) {
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "no tag here, just text"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := buf.String(); got != in {
		t.Errorf("output: got %q, want %q", got, in)
	}
}

func TestStreamFilterCloseIdempotent(t *testing.T) {
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	if _, err := f.Write([]byte("hello\nworld")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestStreamFilterPassThroughAfterClassification(t *testing.T) {
	// Once the tag is observed and stripped, subsequent writes pass through verbatim.
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	if _, err := f.Write([]byte("*topic: thr_1 [a, b, c, d]*\nbody1")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if _, err := f.Write([]byte(" body2")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if _, err := f.Write([]byte("\nbody3")); err != nil {
		t.Fatalf("Write 3: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	want := "body1 body2\nbody3"
	if got := buf.String(); got != want {
		t.Errorf("output: got %q, want %q", got, want)
	}
}

func TestStreamFilterSuppressesTagAfterThinkBlock(t *testing.T) {
	// A real model emits a reasoning block before the tag. The block's
	// text must reach the user; the tag line must not.
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "<think>\nLet me consider thr_42.\n</think>\n*topic: thr_42 [a, b, c, d]*\nthe answer body"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "*topic:") {
		t.Errorf("tag leaked to terminal: %q", got)
	}
	if !strings.Contains(got, "<think>") || !strings.Contains(got, "the answer body") {
		t.Errorf("preamble or body dropped: %q", got)
	}
}

func TestStreamFilterSuppressesTagAfterFillerLine(t *testing.T) {
	// A conversational-filler first line precedes the tag. The filler must
	// reach the user; the tag must not.
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "Sure, let me help with that.\n*topic: thr_7 [a, b, c, d]*\nbody text"
	for _, r := range in { // one rune at a time to exercise the buffering path
		if _, err := f.Write([]byte(string(r))); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "*topic:") {
		t.Errorf("tag leaked to terminal: %q", got)
	}
	if !strings.Contains(got, "Sure, let me help") || !strings.Contains(got, "body text") {
		t.Errorf("preamble or body dropped: %q", got)
	}
}

func TestStreamFilterMultipleTagShapesOnlyFirstStripped(t *testing.T) {
	// The filter only inspects the first line. Subsequent tag-shaped content
	// (which would be unusual but possible) passes through; full-document
	// parsing of tag rejection lives in prompt.Parse, not in the filter.
	var buf bytes.Buffer
	f := NewStreamFilter(&buf)
	in := "*topic: thr_1 [a, b, c, d]*\nbody before second tag\n*topic: thr_2 [e, f, g, h]*\n"
	if _, err := f.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(buf.String(), "*topic: thr_2") {
		t.Errorf("second tag should pass through; got %q", buf.String())
	}
	if strings.Contains(buf.String(), "*topic: thr_1") {
		t.Errorf("first tag should be suppressed; got %q", buf.String())
	}
}
