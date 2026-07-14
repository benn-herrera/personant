package model

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
)

// topicTagRe is the §5.1 parser regex, lifted verbatim. Tests reuse it
// to ensure generated tags are parseable by the same regex the runtime
// will eventually use.
var topicTagRe = regexp.MustCompile(`^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$`)

// TestMockScriptedServesCurrentResponse: a single-slot scripted mock
// serves whatever response SetResponse last installed.
func TestMockScriptedServesCurrentResponse(t *testing.T) {
	scripted := []Response{
		{Content: "first", FinishReason: "stop"},
		{Content: "second", FinishReason: "stop"},
		{Content: "third", FinishReason: "stop"},
	}
	m := NewScriptedMock(nil, nil)

	for i, want := range scripted {
		m.SetResponse(want)
		got, err := m.Consult(context.Background(), Request{})
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if got.Content != want.Content {
			t.Errorf("step %d: got %q, want %q", i, got.Content, want.Content)
		}
	}
}

// TestMockScriptedReServesWithinStep: every consult after one
// SetResponse — the §5.5 mid-turn re-prompt case — serves that one
// installed response.
func TestMockScriptedReServesWithinStep(t *testing.T) {
	m := NewScriptedMock(nil, nil)
	m.SetResponse(Response{Content: "step1"})
	for c := 0; c < 3; c++ {
		got, err := m.Consult(context.Background(), Request{})
		if err != nil {
			t.Fatalf("consult %d: %v", c, err)
		}
		if got.Content != "step1" {
			t.Errorf("consult %d: got %q, want step1", c, got.Content)
		}
	}
}

// TestMockScriptedExhausted: a single-slot scripted mock with no
// response installed (neither via the constructor nor SetResponse)
// yields ErrMockExhausted on Consult.
func TestMockScriptedExhausted(t *testing.T) {
	m := NewScriptedMock(nil, nil)
	_, err := m.Consult(context.Background(), Request{})
	if !errors.Is(err, ErrMockExhausted) {
		t.Fatalf("expected ErrMockExhausted with no response installed, got %v", err)
	}
}

func TestMockGeneratedDeterministicAcrossSeed(t *testing.T) {
	opts := GeneratedMockOpts{
		ThreadPool:    []string{"thr_1", "thr_2", "thr_3"},
		AnchorPool:    []string{"alpha", "beta", "gamma", "delta", "epsilon"},
		AnchorsPerTag: 3,
		BodyWords:     20,
	}
	m1 := NewGeneratedMock(42, opts)
	m2 := NewGeneratedMock(42, opts)

	for i := 0; i < 5; i++ {
		r1, err := m1.Consult(context.Background(), Request{})
		if err != nil {
			t.Fatalf("m1[%d]: %v", i, err)
		}
		r2, err := m2.Consult(context.Background(), Request{})
		if err != nil {
			t.Fatalf("m2[%d]: %v", i, err)
		}
		if r1.Content != r2.Content {
			t.Errorf("seed determinism broken at i=%d:\n m1: %q\n m2: %q", i, r1.Content, r2.Content)
		}
	}
}

func TestMockGeneratedDifferentSeedsDiffer(t *testing.T) {
	opts := GeneratedMockOpts{
		ThreadPool: []string{"thr_1"},
		AnchorPool: []string{"x", "y"},
		BodyWords:  10,
	}
	m1 := NewGeneratedMock(1, opts)
	m2 := NewGeneratedMock(99, opts)

	r1, _ := m1.Consult(context.Background(), Request{})
	r2, _ := m2.Consult(context.Background(), Request{})
	if r1.Content == r2.Content {
		t.Errorf("different seeds produced identical content: %q", r1.Content)
	}
}

// TestMockGeneratedBodiesUnique: across a long sequence, no two bodies
// collide. The counter suffix is the load-bearing mechanism here — even
// a small word pool can't accidentally produce identical content.
func TestMockGeneratedBodiesUnique(t *testing.T) {
	opts := GeneratedMockOpts{
		ThreadPool: []string{"thr_1"},
		AnchorPool: []string{"a"},
		BodyWords:  3,
	}
	m := NewGeneratedMock(7, opts)
	seen := map[string]struct{}{}
	for i := 0; i < 200; i++ {
		r, err := m.Consult(context.Background(), Request{})
		if err != nil {
			t.Fatalf("Consult: %v", err)
		}
		if _, dup := seen[r.Content]; dup {
			t.Fatalf("duplicate generated content at i=%d: %q", i, r.Content)
		}
		seen[r.Content] = struct{}{}
	}
}

// TestMockGeneratedTopicTagFormat: the first line of every generated
// response matches the §5.1 regex; the thread list contains entries
// drawn from the configured pool; the anchor list cardinality equals
// AnchorsPerTag and contains entries from the anchor pool.
func TestMockGeneratedTopicTagFormat(t *testing.T) {
	opts := GeneratedMockOpts{
		ThreadPool:    []string{"thr_1", "thr_2", "*new-topic*"},
		AnchorPool:    []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"},
		AnchorsPerTag: 4,
		BodyWords:     10,
	}
	m := NewGeneratedMock(13, opts)

	threadSet := map[string]struct{}{}
	for _, t := range opts.ThreadPool {
		threadSet[t] = struct{}{}
	}
	anchorSet := map[string]struct{}{}
	for _, a := range opts.AnchorPool {
		anchorSet[a] = struct{}{}
	}

	for i := 0; i < 30; i++ {
		r, err := m.Consult(context.Background(), Request{})
		if err != nil {
			t.Fatalf("Consult: %v", err)
		}
		// Tag is the first line.
		nl := strings.IndexByte(r.Content, '\n')
		if nl < 0 {
			t.Fatalf("response has no newline; tag missing? content=%q", r.Content)
		}
		tag := r.Content[:nl]

		match := topicTagRe.FindStringSubmatch(tag)
		if match == nil {
			t.Fatalf("topic tag did not match §5.1 regex: %q", tag)
		}

		// Validate threads.
		threads := splitTrim(match[1])
		if len(threads) == 0 {
			t.Fatalf("empty thread list: %q", tag)
		}
		for _, th := range threads {
			if _, ok := threadSet[th]; !ok {
				t.Errorf("thread %q not in pool %v", th, opts.ThreadPool)
			}
		}

		// Validate anchors.
		anchors := splitTrim(match[2])
		if len(anchors) != opts.AnchorsPerTag {
			t.Errorf("anchors count: got %d, want %d (tag: %q)", len(anchors), opts.AnchorsPerTag, tag)
		}
		seen := map[string]struct{}{}
		for _, a := range anchors {
			if _, ok := anchorSet[a]; !ok {
				t.Errorf("anchor %q not in pool %v", a, opts.AnchorPool)
			}
			if _, dup := seen[a]; dup {
				t.Errorf("anchor %q duplicated within one tag: %q", a, tag)
			}
			seen[a] = struct{}{}
		}
	}
}

// TestMockCallsAccumulates: every Consult invocation is recorded,
// including a call past the queue end that yields ErrMockExhausted. A
// per-consult-queue mock still has queue-exhaustion semantics, so it is
// the natural vehicle for this assertion.
func TestMockCallsAccumulates(t *testing.T) {
	m := NewScriptedMockPerConsult([]Response{
		{Content: "a"},
		{Content: "b"},
		{Content: "c"},
	})
	m.RecordCalls = true
	for i := 0; i < 4; i++ {
		_, _ = m.Consult(context.Background(), Request{Model: "test"})
	}
	calls := m.Calls()
	if len(calls) != 4 {
		t.Fatalf("Calls count: got %d, want 4 (3 successful + 1 exhausted)", len(calls))
	}
	if calls[0].Response.Content != "a" {
		t.Errorf("first call response: got %q, want a", calls[0].Response.Content)
	}
	if !errors.Is(calls[3].Err, ErrMockExhausted) {
		t.Errorf("fourth call err: got %v, want ErrMockExhausted", calls[3].Err)
	}
}

// TestMockContextCancellation: a cancelled ctx short-circuits Consult.
func TestMockContextCancellation(t *testing.T) {
	m := NewScriptedMock([]Response{{Content: "ok"}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Consult(ctx, Request{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestMockNoModeErrors: a zero MockClient (constructed without going
// through New*) errors out instead of silently returning empty responses.
func TestMockNoModeErrors(t *testing.T) {
	m := &MockClient{}
	_, err := m.Consult(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error from mode-less mock, got nil")
	}
}

func TestMockScriptedStreamSplitsResponse(t *testing.T) {
	scripted := []Response{
		{Content: "abcdefghijklmnop", FinishReason: "stop", Usage: Usage{TotalTokens: 99}},
	}
	m := NewScriptedMock(scripted, nil)
	m.SetMockChunks(4)

	sr, err := m.ConsultStream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	defer sr.Close()

	var got strings.Builder
	chunks := 0
	var lastChunk Chunk
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got.WriteString(chunk.Content)
		chunks++
		lastChunk = chunk
	}
	if chunks != 4 {
		t.Errorf("chunks: got %d, want 4", chunks)
	}
	if got.String() != scripted[0].Content {
		t.Errorf("concat: got %q, want %q", got.String(), scripted[0].Content)
	}
	// Final chunk carries the metadata.
	if lastChunk.FinishReason != "stop" {
		t.Errorf("last chunk finish_reason: got %q, want stop", lastChunk.FinishReason)
	}
	if lastChunk.Usage.TotalTokens != 99 {
		t.Errorf("last chunk usage: got %d, want 99", lastChunk.Usage.TotalTokens)
	}

	final := sr.Final()
	if final.Content != scripted[0].Content {
		t.Errorf("Final.Content: got %q, want %q", final.Content, scripted[0].Content)
	}
	if final.FinishReason != "stop" {
		t.Errorf("Final.FinishReason: got %q, want stop", final.FinishReason)
	}
}

func TestMockStreamCloseIdempotent(t *testing.T) {
	m := NewScriptedMock([]Response{{Content: "x"}}, nil)
	sr, err := m.ConsultStream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	if err := sr.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := sr.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := sr.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("Next after Close: got %v, want io.EOF", err)
	}
}

func TestMockGeneratedStreamMatchesConsult(t *testing.T) {
	// Two equally-seeded mocks: Consult on one, ConsultStream on the
	// other. The streamed concat must match the Consult body byte-for-byte.
	opts := GeneratedMockOpts{
		ThreadPool:    []string{"thr_1"},
		AnchorPool:    []string{"alpha", "beta", "gamma", "delta"},
		AnchorsPerTag: 4,
		BodyWords:     30,
	}
	a := NewGeneratedMock(123, opts)
	b := NewGeneratedMock(123, opts)

	respA, err := a.Consult(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Consult: %v", err)
	}
	sr, err := b.ConsultStream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	defer sr.Close()
	var got strings.Builder
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got.WriteString(chunk.Content)
	}
	if got.String() != respA.Content {
		t.Errorf("stream concat differs from Consult body:\n stream: %q\n consult: %q", got.String(), respA.Content)
	}
}

func TestMockStreamCtxCancelBetweenChunks(t *testing.T) {
	m := NewScriptedMock([]Response{{Content: "abcdefgh"}}, nil)
	m.SetMockChunks(4)
	ctx, cancel := context.WithCancel(context.Background())
	sr, err := m.ConsultStream(ctx, Request{})
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	defer sr.Close()
	// One chunk delivered, then cancel; the next Next must report ctx err.
	if _, err := sr.Next(); err != nil {
		t.Fatalf("first Next: %v", err)
	}
	cancel()
	_, err = sr.Next()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestMockStreamDefaultChunkCount(t *testing.T) {
	m := NewScriptedMock([]Response{{Content: strings.Repeat("a", 64)}}, nil)
	// No SetMockChunks call → DefaultMockChunks.
	sr, err := m.ConsultStream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("ConsultStream: %v", err)
	}
	defer sr.Close()
	chunks := 0
	for {
		_, err := sr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		chunks++
	}
	if chunks != DefaultMockChunks {
		t.Errorf("chunk count: got %d, want %d", chunks, DefaultMockChunks)
	}
}

func splitTrim(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// TestMockGeneratedTagOmission — the D6 tag-omission knob. With
// OmitTagEveryN=3, every third response omits the topic-tag line; every
// other response — and the omitted responses' BODIES — are byte-identical
// to a same-seed run with the knob off, proving the knob perturbs neither
// the RNG stream nor the sim's determinism contract when disabled.
func TestMockGeneratedTagOmission(t *testing.T) {
	const seed = 99
	opts := GeneratedMockOpts{
		ThreadPool: []string{"thr_1", "thr_2"},
		AnchorPool: []string{"alpha", "beta", "gamma", "delta", "epsilon"},
	}
	off := NewGeneratedMock(seed, opts)

	optsOn := opts
	optsOn.OmitTagEveryN = 3
	on := NewGeneratedMock(seed, optsOn)

	ctx := context.Background()
	for i := 1; i <= 9; i++ {
		base, err := off.Consult(ctx, Request{})
		if err != nil {
			t.Fatalf("off consult %d: %v", i, err)
		}
		got, err := on.Consult(ctx, Request{})
		if err != nil {
			t.Fatalf("on consult %d: %v", i, err)
		}

		baseFirstLine, baseBody, found := strings.Cut(base.Content, "\n")
		if !found || !topicTagRe.MatchString(baseFirstLine) {
			t.Fatalf("response %d: knob-off mock must always emit a leading tag: %q", i, base.Content)
		}

		if i%3 == 0 {
			// Omitted response: exactly the tag line (and its newline) gone.
			if got.Content != baseBody {
				t.Errorf("response %d: omitted content must equal knob-off body\n got: %q\nwant: %q",
					i, got.Content, baseBody)
			}
			if first, _, _ := strings.Cut(got.Content, "\n"); topicTagRe.MatchString(first) {
				t.Errorf("response %d: tag line present on an omission response: %q", i, first)
			}
		} else if got.Content != base.Content {
			// Non-omitted responses byte-identical to the knob-off run.
			t.Errorf("response %d: non-omitted content diverged\n got: %q\nwant: %q",
				i, got.Content, base.Content)
		}
	}
}
