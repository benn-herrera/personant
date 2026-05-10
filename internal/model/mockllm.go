package model

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// MockClient implements Client without crossing the network. Two backing
// modes — scripted and generated — share one type so callers (tests,
// scenario harnesses) can switch modes without re-typing.
//
// All public methods are safe for concurrent use; tests sometimes share
// one MockClient across goroutines that pump turns from a scenario.
type MockClient struct {
	mu sync.Mutex

	// Scripted mode: pop from queue. nil → not scripted.
	queue []Response

	// Generated mode: rng + opts. rng is nil → not generated.
	rng     *rand.Rand
	genOpts GeneratedMockOpts
	counter int

	calls []MockCall
}

// MockCall records one Consult invocation for test assertions.
type MockCall struct {
	Request  Request
	Response Response
	Err      error
	At       time.Time
}

// GeneratedMockOpts controls the synthesized-response shape produced by
// NewGeneratedMock.
type GeneratedMockOpts struct {
	// ThreadPool is the rotation pool for the topic tag's thread list.
	// Entries should be either "thr_<n>" or the literal "*new-topic*"
	// (per spec §5.1).
	ThreadPool []string

	// AnchorPool is the rotation pool for the topic tag's anchor list.
	AnchorPool []string

	// AnchorsPerTag is how many anchors each emitted tag carries.
	// 0 → defaultAnchorsPerTag. Must be ≤ len(AnchorPool).
	AnchorsPerTag int

	// BodyWords is the lorem-ipsum-ish word count of the generated
	// response body. 0 → defaultBodyWords.
	BodyWords int
}

const (
	defaultAnchorsPerTag = 4
	defaultBodyWords     = 50
)

// NewScriptedMock returns a MockClient that pops responses from the given
// queue, in order. Calling Consult past the end returns ErrMockExhausted.
func NewScriptedMock(responses []Response) *MockClient {
	q := make([]Response, len(responses))
	copy(q, responses)
	return &MockClient{queue: q}
}

// NewGeneratedMock returns a MockClient that synthesizes responses from a
// seeded RNG. Same seed → same sequence, byte-for-byte. Each generated
// body carries a counter suffix that guarantees no two responses dedup
// across a long run.
func NewGeneratedMock(seed int64, opts GeneratedMockOpts) *MockClient {
	if opts.AnchorsPerTag == 0 {
		opts.AnchorsPerTag = defaultAnchorsPerTag
	}
	if opts.BodyWords == 0 {
		opts.BodyWords = defaultBodyWords
	}
	if opts.AnchorsPerTag > len(opts.AnchorPool) {
		opts.AnchorsPerTag = len(opts.AnchorPool)
	}
	return &MockClient{
		rng:     rand.New(rand.NewSource(seed)),
		genOpts: opts,
	}
}

// Consult returns the next response from this mock. Behavior depends on
// the mock's mode (scripted vs generated).
func (m *MockClient) Consult(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var resp Response
	var err error
	switch {
	case m.rng != nil:
		resp = m.synthesize()
	case m.queue != nil:
		if len(m.queue) == 0 {
			err = ErrMockExhausted
		} else {
			resp = m.queue[0]
			m.queue = m.queue[1:]
		}
	default:
		err = fmt.Errorf("model: MockClient constructed without a mode")
	}

	m.calls = append(m.calls, MockCall{
		Request:  req,
		Response: resp,
		Err:      err,
		At:       time.Now(),
	})
	return resp, err
}

// Calls returns a snapshot of every Consult invocation observed so far.
// Returned slice is a defensive copy; safe to retain across further calls.
func (m *MockClient) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// synthesize builds one generated response. The mu lock is already held
// by the caller (Consult).
func (m *MockClient) synthesize() Response {
	m.counter++
	tag := m.makeTopicTag()
	body := m.makeBody(m.counter)
	return Response{
		Content:      tag + "\n" + body,
		FinishReason: "stop",
		Usage: Usage{
			PromptTokens:     8,
			CompletionTokens: m.genOpts.BodyWords,
			TotalTokens:      8 + m.genOpts.BodyWords,
		},
	}
}

// makeTopicTag emits one §5.1-format tag.
//
// One thread is chosen from ThreadPool; AnchorsPerTag anchors are
// selected without replacement from AnchorPool. Both pools may be
// empty — in that case the tag uses a stub thread / no anchors so the
// generated content remains regex-parseable but doesn't pretend to
// reference real state.
func (m *MockClient) makeTopicTag() string {
	thread := "*new-topic*"
	if len(m.genOpts.ThreadPool) > 0 {
		thread = m.genOpts.ThreadPool[m.rng.Intn(len(m.genOpts.ThreadPool))]
	}

	var anchors []string
	if n := m.genOpts.AnchorsPerTag; n > 0 && len(m.genOpts.AnchorPool) > 0 {
		// Sample without replacement: shuffle a copy and take the first n.
		pool := make([]string, len(m.genOpts.AnchorPool))
		copy(pool, m.genOpts.AnchorPool)
		m.rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		anchors = pool[:n]
	}

	return fmt.Sprintf("*topic: %s [%s]*", thread, strings.Join(anchors, ", "))
}

// loremWords is a small word bag drawn on for body filler. Deliberately
// short — entropy comes from the counter suffix, which guarantees
// uniqueness regardless of bag size.
var loremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing",
	"elit", "sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore",
	"et", "dolore", "magna", "aliqua", "veniam", "quis", "nostrud",
	"exercitation", "ullamco", "laboris", "nisi", "aliquip", "commodo",
	"consequat", "duis", "aute", "irure", "in", "reprehenderit",
}

func (m *MockClient) makeBody(counter int) string {
	parts := make([]string, 0, m.genOpts.BodyWords+1)
	for i := 0; i < m.genOpts.BodyWords; i++ {
		parts = append(parts, loremWords[m.rng.Intn(len(loremWords))])
	}
	// Counter suffix: guarantees no two generated bodies share content.
	parts = append(parts, fmt.Sprintf("[#%d]", counter))
	return strings.Join(parts, " ")
}
