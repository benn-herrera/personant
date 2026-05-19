package model

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"time"

	"personant/internal/clock"
)

// MockClient implements Client without crossing the network. Two backing
// modes — scripted and generated — share one type so callers (tests,
// scenario harnesses) can switch modes without re-typing.
//
// All public methods are safe for concurrent use; tests sometimes share
// one MockClient across goroutines that pump turns from a scenario.
type MockClient struct {
	mu sync.Mutex

	// Scripted mode has two sub-modes, selected at construction.
	//
	// Single-slot mode (NewScriptedMock, perConsult false): the mock
	// holds one current Response. Every Consult re-serves `current`,
	// including a §5.5 mid-turn re-prompt — a legitimate second consult
	// within one turn — which is correct: the re-prompt re-issues the
	// same request, and once the missing thread is fetched the
	// re-evaluated topic tag drains cleanly. The harness (or a
	// multi-turn unit test) installs each turn's response with
	// SetResponse before driving the turn. If no response was ever
	// installed, Consult returns ErrMockExhausted.
	//
	// Per-consult queue mode (NewScriptedMockPerConsult, perConsult
	// true): the mock walks `queue` one entry per Consult — queue[0],
	// queue[1], … This is for runtime unit tests that drive a single
	// turn and must observe two *distinct* consults, e.g. testing the
	// §5.5 re-prompt cap. A Consult past the queue end returns
	// ErrMockExhausted. This is NOT the scenario-harness path.
	scripted   bool     // true once the mock is in scripted mode
	current    Response // single-slot mode: the response every Consult serves
	hasCurrent bool     // single-slot mode: SetResponse (or ctor) installed a response
	queue      []Response
	step       int
	perConsult bool

	// Generated mode: rng + opts. rng is nil → not generated.
	rng     *rand.Rand
	genOpts GeneratedMockOpts
	counter int

	// Models is returned verbatim from ListModels. Settable at
	// construction (NewScriptedMock) or directly on the returned
	// *MockClient. nil → ListModels returns an empty slice.
	Models []ModelInfo

	calls []MockCall

	// chunks is the per-response chunk count for ConsultStream. 0 →
	// DefaultMockChunks. Set via SetMockChunks.
	chunks int
}

// DefaultMockChunks is the per-response chunk count used by ConsultStream
// when SetMockChunks has not been called.
const DefaultMockChunks = 8

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

// NewScriptedMock returns a single-slot scripted MockClient. The mock
// holds one current Response that every Consult re-serves — including a
// §5.5 mid-turn re-prompt. The current response is changed per turn via
// SetResponse.
//
// `responses` seeds the initial slot: if non-empty, responses[0] is
// installed as the current response, so a single-turn caller can pass a
// one-element slice and never call SetResponse. Any further elements
// are ignored — a multi-turn caller installs each subsequent turn's
// response with SetResponse. A nil/empty `responses` leaves the slot
// empty; the first Consult before any SetResponse yields ErrMockExhausted.
//
// models is returned verbatim from ListModels; nil yields an empty list.
func NewScriptedMock(responses []Response, models []ModelInfo) *MockClient {
	m := &MockClient{scripted: true}
	if len(responses) > 0 {
		m.current = responses[0]
		m.hasCurrent = true
	}
	if models != nil {
		m.Models = make([]ModelInfo, len(models))
		copy(m.Models, models)
	}
	return m
}

// NewScriptedMockPerConsult returns a scripted MockClient that advances
// through the queue one entry per Consult: queue[0], queue[1], … This
// is for runtime unit tests that drive a single turn and must observe
// two distinct consults — e.g. exercising the §5.5 mid-turn re-prompt
// cap, where the re-prompt must see a different response than the
// aborted first stream. The scenario harness uses NewScriptedMock
// (step-indexed) instead. A Consult past the end yields ErrMockExhausted.
func NewScriptedMockPerConsult(responses []Response) *MockClient {
	q := make([]Response, len(responses))
	copy(q, responses)
	return &MockClient{scripted: true, queue: q, perConsult: true}
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
	case m.scripted && m.perConsult:
		if m.step >= 0 && m.step < len(m.queue) {
			resp = m.queue[m.step]
		} else {
			err = ErrMockExhausted
		}
		m.step++
	case m.scripted:
		if m.hasCurrent {
			resp = m.current
		} else {
			err = ErrMockExhausted
		}
	default:
		err = fmt.Errorf("model: MockClient constructed without a mode")
	}

	m.calls = append(m.calls, MockCall{
		Request:  req,
		Response: resp,
		Err:      err,
		At:       clock.Timeline(),
	})
	return resp, err
}

// ConsultStream produces the same Response Consult would have produced,
// then splits it across chunks for incremental delivery. The number of
// chunks is set by SetMockChunks (default DefaultMockChunks).
//
// All non-content fields (FinishReason, Usage, ToolCalls) attach to the
// final chunk so iteration order matches a real provider.
func (m *MockClient) ConsultStream(ctx context.Context, req Request) (StreamReader, error) {
	resp, err := m.Consult(ctx, req)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	chunkCount := m.chunks
	m.mu.Unlock()
	if chunkCount <= 0 {
		chunkCount = DefaultMockChunks
	}
	return newMockStreamReader(ctx, resp, chunkCount), nil
}

// SetMockChunks overrides the per-response chunk count for streamed mock
// responses. Subsequent ConsultStream calls use the new value. n ≤ 0 is
// equivalent to DefaultMockChunks.
func (m *MockClient) SetMockChunks(n int) {
	m.mu.Lock()
	m.chunks = n
	m.mu.Unlock()
}

// SetResponse installs the current response a single-slot scripted mock
// serves. The harness calls this before each turn; every Consult during
// the turn — including a §5.5 mid-turn re-prompt — then returns resp.
// Has no effect on a generated-mode or per-consult-queue mock.
func (m *MockClient) SetResponse(resp Response) {
	m.mu.Lock()
	if m.scripted && !m.perConsult {
		m.current = resp
		m.hasCurrent = true
	}
	m.mu.Unlock()
}

// ListModels returns a copy of the Models field, or an empty slice if
// none are configured. ctx is honored for cancellation.
func (m *MockClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Models == nil {
		return []ModelInfo{}, nil
	}
	out := make([]ModelInfo, len(m.Models))
	copy(out, m.Models)
	return out, nil
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

// mockStreamReader splits a Response.Content into N near-equal chunks and
// hands them out one Next() call at a time. Tool-calls, finish_reason,
// and usage attach to the final chunk so iteration matches a real
// provider's emission order.
type mockStreamReader struct {
	ctx     context.Context
	pieces  []string
	idx     int
	full    Response
	closed  bool
	closeMu sync.Mutex
}

func newMockStreamReader(ctx context.Context, resp Response, chunks int) *mockStreamReader {
	if chunks < 1 {
		chunks = 1
	}
	return &mockStreamReader{
		ctx:    ctx,
		pieces: splitForChunks(resp.Content, chunks),
		full:   resp,
	}
}

func (r *mockStreamReader) Next() (Chunk, error) {
	if r.closed {
		return Chunk{}, io.EOF
	}
	if err := r.ctx.Err(); err != nil {
		return Chunk{}, err
	}
	if r.idx >= len(r.pieces) {
		return Chunk{}, io.EOF
	}
	chunk := Chunk{Content: r.pieces[r.idx]}
	r.idx++
	if r.idx == len(r.pieces) {
		// Final chunk carries the non-content fields.
		chunk.FinishReason = r.full.FinishReason
		chunk.Usage = r.full.Usage
		chunk.ToolCalls = r.full.ToolCalls
	}
	return chunk, nil
}

func (r *mockStreamReader) Final() Response {
	// The mock knows the full response from the start; expose it
	// directly. (Real providers compute Final() from observed deltas;
	// the mock cheats but the contract is the same.)
	if r.idx == 0 {
		return Response{}
	}
	consumed := strings.Join(r.pieces[:r.idx], "")
	out := Response{Content: consumed}
	if r.idx == len(r.pieces) {
		out.FinishReason = r.full.FinishReason
		out.Usage = r.full.Usage
		out.ToolCalls = r.full.ToolCalls
	}
	return out
}

func (r *mockStreamReader) Close() error {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	r.closed = true
	return nil
}

// splitForChunks slices s into n near-equal pieces by rune count. The
// last piece absorbs any remainder so concat(pieces) == s exactly.
// If s is empty, the result is a single empty piece (so Next() still
// emits one chunk carrying the trailing finish_reason/usage).
func splitForChunks(s string, n int) []string {
	if n <= 1 {
		return []string{s}
	}
	if s == "" {
		return []string{""}
	}
	runes := []rune(s)
	total := len(runes)
	if total < n {
		// One rune per piece; trailing pieces empty.
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			if i < total {
				out = append(out, string(runes[i:i+1]))
			} else {
				out = append(out, "")
			}
		}
		return out
	}
	per := total / n
	out := make([]string, 0, n)
	cursor := 0
	for i := 0; i < n; i++ {
		end := cursor + per
		if i == n-1 {
			end = total
		}
		out = append(out, string(runes[cursor:end]))
		cursor = end
	}
	return out
}
