package model

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// topicTagRe is the §5.1 parser regex, lifted verbatim. Tests reuse it
// to ensure generated tags are parseable by the same regex the runtime
// will eventually use.
var topicTagRe = regexp.MustCompile(`^\s*\*topic:\s*([^\[]+?)\s*\[([^\]]*)\]\s*\*\s*$`)

func TestMockScriptedDeliversInOrder(t *testing.T) {
	scripted := []Response{
		{Content: "first", FinishReason: "stop"},
		{Content: "second", FinishReason: "stop"},
		{Content: "third", FinishReason: "stop"},
	}
	m := NewScriptedMock(scripted, nil)

	for i, want := range scripted {
		got, err := m.Consult(context.Background(), Request{})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got.Content != want.Content {
			t.Errorf("call %d: got %q, want %q", i, got.Content, want.Content)
		}
	}
}

func TestMockScriptedExhausted(t *testing.T) {
	m := NewScriptedMock([]Response{{Content: "only one"}}, nil)
	if _, err := m.Consult(context.Background(), Request{}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := m.Consult(context.Background(), Request{})
	if !errors.Is(err, ErrMockExhausted) {
		t.Fatalf("expected ErrMockExhausted, got %v", err)
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

// TestMockCallsAccumulates: every Consult invocation is recorded.
func TestMockCallsAccumulates(t *testing.T) {
	m := NewScriptedMock([]Response{
		{Content: "a"},
		{Content: "b"},
		{Content: "c"},
	}, nil)
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
