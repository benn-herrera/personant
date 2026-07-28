package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"personant/internal/log"
)

// sseMaxLineBytes is the buffer cap for a single SSE line. The bufio.Scanner
// default of 64KB is too tight: SSE payloads occasionally embed multi-KB
// tool-call argument blobs on one line. 1MB is generous for well-behaved
// providers and clamps a runaway server before OOM.
const sseMaxLineBytes = 1 << 20

// toolCallAccum accumulates the fragments of ONE streamed tool call.
// Providers fragment a tool call across chunks: `id` and `name` typically
// arrive once (often in the first fragment for that index) while
// `arguments` arrives as a string sliced across many later fragments.
// Only the concatenation is valid JSON.
type toolCallAccum struct {
	id   string
	name strings.Builder
	args strings.Builder
}

// httpStreamReader is the production StreamReader, backed by an HTTP
// response body delivering SSE-encoded chunks.
type httpStreamReader struct {
	ctx     context.Context
	body    io.ReadCloser
	scanner *bufio.Scanner

	// done is set once the stream has been fully drained (either [DONE]
	// observed, EOF on the body, or a hard error). Subsequent Next()
	// calls return io.EOF.
	done bool

	// content is the accumulator for the final Response. Each chunk's
	// Content is appended in order.
	content      strings.Builder
	finishReason string
	usage        Usage

	// toolCalls accumulates streamed tool-call fragments keyed by the
	// provider's tool-call index. curToolIdx is the index that fragments
	// arriving WITHOUT an explicit `index` field attach to.
	toolCalls  map[int]*toolCallAccum
	curToolIdx int

	// finalToolCalls / toolCallErr are produced exactly once by
	// finalizeToolCalls, guarded by toolsFinalized.
	finalToolCalls []ToolCall
	toolCallErr    error
	toolsFinalized bool

	closeOnce sync.Once
	closeErr  error
}

func newHTTPStreamReader(ctx context.Context, body io.ReadCloser) *httpStreamReader {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), sseMaxLineBytes)
	return &httpStreamReader{
		ctx:       ctx,
		body:      body,
		scanner:   sc,
		toolCalls: make(map[int]*toolCallAccum),
	}
}

// Next returns the next chunk in the stream, or io.EOF when the stream
// has been drained. ctx cancellation surfaces as the ctx error.
//
// When the stream ends with a tool call that never completed (torn
// connection mid-fragment, or arguments that are not valid JSON after
// concatenation), Next returns that error INSTEAD of io.EOF and the
// offending call is dropped from Final(). A half-parsed tool call reaching
// an execution loop is worse than no tool call, so the failure is loud.
func (r *httpStreamReader) Next() (Chunk, error) {
	if r.done {
		return Chunk{}, io.EOF
	}
	for {
		// Honor context first so a slow server can't outlast a deadline.
		if err := r.ctx.Err(); err != nil {
			r.done = true
			return Chunk{}, err
		}
		if !r.scanner.Scan() {
			r.done = true
			r.finalizeToolCalls()
			if err := r.scanner.Err(); err != nil {
				return Chunk{}, fmt.Errorf("stream read: %w", err)
			}
			if r.toolCallErr != nil {
				return Chunk{}, r.toolCallErr
			}
			return Chunk{}, io.EOF
		}
		line := r.scanner.Bytes()
		// SSE separators are blank lines; ignore them.
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		// Only `data:` lines carry payload; some servers also emit
		// comment lines starting with `:` and event/id headers — skip
		// anything that isn't a data line.
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 {
			continue
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			r.done = true
			r.finalizeToolCalls()
			if r.toolCallErr != nil {
				return Chunk{}, r.toolCallErr
			}
			return Chunk{}, io.EOF
		}
		sc, err := parseSSEChunk(payload)
		if err != nil {
			r.done = true
			return Chunk{}, fmt.Errorf("decode stream chunk: %w", err)
		}
		chunk := sc.Chunk
		// Accumulate for Final().
		if chunk.Content != "" {
			r.content.WriteString(chunk.Content)
		}
		if chunk.FinishReason != "" {
			r.finishReason = chunk.FinishReason
		}
		if chunk.Usage.TotalTokens != 0 || chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
			r.usage = chunk.Usage
		}
		// Merge tool-call fragments into the index-keyed accumulators and
		// surface the identity-only per-chunk view (see Chunk.ToolCalls).
		chunk.ToolCalls = r.mergeToolDeltas(sc.toolDeltas)
		return chunk, nil
	}
}

// mergeToolDeltas folds one chunk's tool-call fragments into the
// index-keyed accumulators and returns the identity-only per-chunk view of
// the calls this chunk touched (ID + Function as known so far, never
// Args — a fragment's arguments are not valid JSON on their own).
func (r *httpStreamReader) mergeToolDeltas(deltas []wireStreamToolCall) []ToolCall {
	if len(deltas) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(deltas))
	for _, d := range deltas {
		idx := r.resolveToolIndex(d)
		a := r.toolCalls[idx]
		if a == nil {
			a = &toolCallAccum{}
			r.toolCalls[idx] = a
		}
		// First non-empty id wins: providers send it once per call.
		if a.id == "" {
			a.id = d.ID
		}
		if d.Function.Name != "" {
			// Providers vary: canonical OpenAI sends the function name
			// once, some backends split it across fragments, and some
			// repeat it whole in every fragment. Append only what is
			// new — an exact repeat of what we already hold is a repeat,
			// not the next slice of a split name.
			if a.name.String() != d.Function.Name {
				a.name.WriteString(d.Function.Name)
			}
		}
		a.args.WriteString(argFragment(d.Function.Arguments))
		out = append(out, ToolCall{ID: a.id, Function: a.name.String()})
	}
	return out
}

// resolveToolIndex determines which accumulator a fragment belongs to.
//
// An explicit `index` is authoritative. When it is absent — litellm and
// some backends omit it for a lone tool call — the fragment attaches to
// the current index, which advances only when a fragment carries a new,
// different, non-empty `id`. That covers both "one unindexed call" and
// "several unindexed calls emitted back to back".
func (r *httpStreamReader) resolveToolIndex(d wireStreamToolCall) int {
	if d.Index != nil {
		r.curToolIdx = *d.Index
		return r.curToolIdx
	}
	if d.ID != "" {
		if a, ok := r.toolCalls[r.curToolIdx]; ok && a.id != "" && a.id != d.ID {
			next := r.curToolIdx
			for i := range r.toolCalls {
				if i > next {
					next = i
				}
			}
			r.curToolIdx = next + 1
		}
	}
	return r.curToolIdx
}

// finalizeToolCalls converts the accumulators into merged ToolCalls.
//
// A call whose concatenated arguments are not valid JSON, or that never
// received a function name, is DROPPED and recorded in toolCallErr:
// truncated arguments must never reach a caller looking like a complete
// call. Well-formed sibling calls in the same stream are still returned.
//
// Before the stream has ended, a Final() caller gets a fresh snapshot on
// every call (a call still in flight is not a failure, so nothing is
// latched and nothing is logged). Once the stream is done the result
// latches, so Next's error is stable and the drop logs exactly once.
func (r *httpStreamReader) finalizeToolCalls() {
	if r.toolsFinalized {
		return
	}
	r.finalToolCalls = nil
	r.toolCallErr = nil
	if len(r.toolCalls) == 0 {
		r.toolsFinalized = r.done
		return
	}
	idxs := make([]int, 0, len(r.toolCalls))
	for i := range r.toolCalls {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)

	var bad []string
	for _, i := range idxs {
		a := r.toolCalls[i]
		name := a.name.String()
		args := a.args.String()
		if strings.TrimSpace(args) == "" {
			// Zero-arg tool: streaming no `arguments` fragments at all is
			// legal, but the empty string is not valid JSON. An empty
			// object is the honest equivalent and keeps every emitted
			// call's Args parseable by the execution loop.
			args = "{}"
		}
		switch {
		case name == "":
			bad = append(bad, fmt.Sprintf("index %d: no function name", i))
			continue
		case !json.Valid([]byte(args)):
			bad = append(bad, fmt.Sprintf("index %d (%s): arguments are not valid JSON after merge (%d bytes)", i, name, len(args)))
			continue
		}
		// Args carries the raw JSON value of the wire `arguments` field,
		// matching the non-streaming path (decodeResponse), where OpenAI
		// sends the arguments as a JSON-encoded STRING. Re-encoding the
		// concatenation keeps both paths byte-compatible so a caller —
		// and encodeRequest, replaying the call back to the provider —
		// handles exactly one representation.
		enc, err := json.Marshal(args)
		if err != nil {
			bad = append(bad, fmt.Sprintf("index %d (%s): %v", i, name, err))
			continue
		}
		r.finalToolCalls = append(r.finalToolCalls, ToolCall{
			ID:       a.id,
			Function: name,
			Args:     enc,
		})
	}
	if len(bad) > 0 {
		r.toolCallErr = fmt.Errorf("model: incomplete streamed tool call(s) dropped: %s", strings.Join(bad, "; "))
	}
	if !r.done {
		return
	}
	r.toolsFinalized = true
	if r.toolCallErr != nil {
		log.Error("%v", r.toolCallErr)
	}
}

// Final returns the accumulated Response after iteration. Safe to call
// before EOF, though the resulting Content/Usage/FinishReason will only
// reflect what has been observed so far.
//
// ToolCalls holds the fully merged tool calls: fragments are accumulated
// by index across chunks and each call's `arguments` string is
// concatenated in arrival order, so Args is complete, parseable JSON in
// the same representation the non-streaming path produces. Calls that did
// not complete (torn stream, invalid JSON after merge) are dropped rather
// than returned truncated — the drop is reported as an error from Next and
// logged. Calling Final before the stream has ended returns a snapshot of
// what has merged so far without freezing it; a later Final sees the rest.
func (r *httpStreamReader) Final() Response {
	r.finalizeToolCalls()
	return Response{
		Content:      r.content.String(),
		ToolCalls:    r.finalToolCalls,
		FinishReason: r.finishReason,
		Usage:        r.usage,
	}
}

// Close releases the underlying body. Idempotent.
func (r *httpStreamReader) Close() error {
	r.closeOnce.Do(func() {
		r.done = true
		r.closeErr = r.body.Close()
	})
	return r.closeErr
}

// --- SSE wire decoding ---

type wireStreamChoice struct {
	Index        int             `json:"index"`
	Delta        wireStreamDelta `json:"delta"`
	FinishReason string          `json:"finish_reason"`
}

type wireStreamDelta struct {
	Role      string               `json:"role,omitempty"`
	Content   string               `json:"content,omitempty"`
	ToolCalls []wireStreamToolCall `json:"tool_calls,omitempty"`
}

// wireStreamToolCall is one tool-call FRAGMENT. It is deliberately not
// wireToolCall: Index is a pointer so an absent field is distinguishable
// from an explicit 0, and Arguments is a raw fragment (typically a JSON
// string holding a slice of the eventual argument JSON) rather than a
// complete value.
type wireStreamToolCall struct {
	Index    *int                   `json:"index"`
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function wireStreamToolFunction `json:"function"`
}

type wireStreamToolFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type wireStreamResponse struct {
	Choices []wireStreamChoice `json:"choices"`
	Usage   *wireUsage         `json:"usage,omitempty"`
}

// sseChunk is one decoded SSE payload: the caller-facing Chunk plus the
// raw tool-call fragments, which only mean anything after index-keyed
// merge across the whole stream.
type sseChunk struct {
	Chunk
	toolDeltas []wireStreamToolCall
}

// argFragment extracts the text of one `arguments` fragment. Canonically
// it is a JSON string (a slice of the eventual argument JSON). Some
// litellm-normalized backends instead emit the whole argument object
// unfragmented; taking the raw bytes in that case keeps the concatenation
// correct rather than failing the decode of the entire chunk.
func argFragment(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

func parseSSEChunk(payload []byte) (sseChunk, error) {
	var w wireStreamResponse
	if err := json.Unmarshal(payload, &w); err != nil {
		return sseChunk{}, err
	}
	var out sseChunk
	if len(w.Choices) > 0 {
		c := w.Choices[0]
		out.Content = c.Delta.Content
		out.FinishReason = c.FinishReason
		out.toolDeltas = c.Delta.ToolCalls
	}
	if w.Usage != nil {
		out.Usage = Usage{
			PromptTokens:     w.Usage.PromptTokens,
			CompletionTokens: w.Usage.CompletionTokens,
			TotalTokens:      w.Usage.TotalTokens,
		}
	}
	return out, nil
}
