package turn

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"personant/internal/model"
)

// chunkReader replays a scripted chunk sequence as a model.StreamReader.
// Deliberately hand-rolled rather than driven through the mock client:
// the shape under test — reasoning-only chunks interleaved with content —
// is exactly what a scripted mock built from a response body cannot
// produce.
type chunkReader struct {
	chunks []model.Chunk
	i      int
}

func (r *chunkReader) Next() (model.Chunk, error) {
	if r.i >= len(r.chunks) {
		return model.Chunk{}, io.EOF
	}
	c := r.chunks[r.i]
	r.i++
	return c, nil
}

func (r *chunkReader) Close() error          { return nil }
func (r *chunkReader) Final() model.Response { return model.Response{} }

// reasoningSink collects the deltas handed to State.OnReasoning.
type reasoningSink struct {
	deltas []string
}

func (s *reasoningSink) emit(d string) { s.deltas = append(s.deltas, d) }

// The load-bearing separation: reasoning reaches the hook per-chunk and
// NEVER reaches the body sink. A reasoning-only chunk carries empty
// Content, so the emit must precede the empty-Content skip — this is the
// regression that would silently disable the whole feature.
func TestStreamThroughFilter_ReasoningGoesToHookNotBody(t *testing.T) {
	sink := &reasoningSink{}
	state := &State{OnReasoning: sink.emit}
	sr := &chunkReader{chunks: []model.Chunk{
		{Reasoning: "let me "},
		{Reasoning: "think"},
		{Content: "answer "},
		{Reasoning: "wait,"}, // interleaved after content starts
		{Content: "here."},
	}}
	var body bytes.Buffer
	if err := streamThroughFilter(state, sr, &body); err != nil {
		t.Fatalf("streamThroughFilter: %v", err)
	}
	if got, want := body.String(), "answer here."; got != want {
		t.Errorf("body = %q, want %q — reasoning must never join the body", got, want)
	}
	if got, want := strings.Join(sink.deltas, "|"), "let me |think|wait,"; got != want {
		t.Errorf("reasoning deltas = %q, want %q", got, want)
	}
}

// The default path: no hook installed (the scenario harness, every
// non-interactive caller). Reasoning is dropped at the chunk loop without
// a nil-deref and without leaking into the body.
func TestStreamThroughFilter_NilHookDropsReasoning(t *testing.T) {
	state := &State{}
	sr := &chunkReader{chunks: []model.Chunk{
		{Reasoning: "scratch"},
		{Content: "visible"},
	}}
	var body bytes.Buffer
	if err := streamThroughFilter(state, sr, &body); err != nil {
		t.Fatalf("streamThroughFilter: %v", err)
	}
	if got := body.String(); got != "visible" {
		t.Errorf("body = %q, want %q", got, "visible")
	}
}

// The preamble pump is the other chunk loop, and on a thinking model it
// is where most reasoning arrives (it runs before the first content byte
// resolves the scan). Reasoning must reach the hook there too, and must
// not enter the accumulated head — the §5.1.2 tag scan runs over head, so
// a reasoning leak would corrupt topic-tag detection.
func TestReadPreamble_EmitsReasoningAndKeepsHeadClean(t *testing.T) {
	sink := &reasoningSink{}
	state := &State{OnReasoning: sink.emit}
	sr := &chunkReader{chunks: []model.Chunk{
		{Reasoning: "*topic: thr_9 [x]*"}, // a tag-shaped decoy inside reasoning
		{Content: "*topic: thr_1 [alpha, beta]*\n"},
		{Content: "body"},
	}}
	pre, err := readPreamble(state, sr)
	if err != nil {
		t.Fatalf("readPreamble: %v", err)
	}
	if strings.Contains(string(pre.head), "thr_9") {
		t.Errorf("reasoning leaked into preamble head: %q", pre.head)
	}
	if pre.tag == nil || len(pre.tag.Threads) != 1 || pre.tag.Threads[0] != "thr_1" {
		t.Errorf("tag = %+v, want the content tag thr_1", pre.tag)
	}
	if len(sink.deltas) != 1 || sink.deltas[0] != "*topic: thr_9 [x]*" {
		t.Errorf("reasoning deltas = %v, want the one reasoning chunk", sink.deltas)
	}
}
