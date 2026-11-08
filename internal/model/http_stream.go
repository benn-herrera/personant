package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// sseMaxLineBytes is the buffer cap for a single SSE line. The bufio.Scanner
// default of 64KB is too tight for a large content or reasoning delta on
// one line. 1MB is generous for well-behaved providers and clamps a runaway
// server before OOM.
const sseMaxLineBytes = 1 << 20

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

	closeOnce sync.Once
	closeErr  error
}

func newHTTPStreamReader(ctx context.Context, body io.ReadCloser) *httpStreamReader {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), sseMaxLineBytes)
	return &httpStreamReader{
		ctx:     ctx,
		body:    body,
		scanner: sc,
	}
}

// Next returns the next chunk in the stream, or io.EOF when the stream
// has been drained. ctx cancellation surfaces as the ctx error.
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
			if err := r.scanner.Err(); err != nil {
				return Chunk{}, fmt.Errorf("stream read: %w", err)
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
			return Chunk{}, io.EOF
		}
		chunk, err := parseSSEChunk(payload)
		if err != nil {
			r.done = true
			return Chunk{}, fmt.Errorf("decode stream chunk: %w", err)
		}
		// Accumulate for Final().
		if chunk.Content != "" {
			r.content.WriteString(chunk.Content)
		}
		if chunk.FinishReason != "" {
			r.finishReason = chunk.FinishReason
		}
		// Last non-empty usage block wins. The include_usage final chunk is
		// choices-less, so it lands here with empty Content and updates
		// nothing but Usage.
		if !chunk.Usage.IsZero() {
			r.usage = chunk.Usage
		}
		// Reasoning is deliberately NOT accumulated — see Final().
		return chunk, nil
	}
}

// Final returns the accumulated Response after iteration. Safe to call
// before EOF, though the resulting Content/Usage/FinishReason will only
// reflect what has been observed so far.
//
// Reasoning is NOT part of the accumulated Response and Response has no
// field for it: reasoning is delivered per-Chunk only. Two reasons, in
// order of weight. (1) Reasoning is model scratch, not its committed
// answer — it must never reach the response body, the turn journal, symbol
// extraction, or replayed history, and the cheapest guarantee of that is a
// type in which it cannot be represented. (2) It is large: on a live probe
// 48 of 50 payloads were reasoning against 1 byte of content, so
// unconditional accumulation would buy every turn a second full-size
// buffer for data nearly every caller discards. A caller that wants the
// whole text (a renderer) is already iterating chunks and can
// accumulate what it chooses to keep.
func (r *httpStreamReader) Final() Response {
	return Response{
		Content:      r.content.String(),
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
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`

	// ReasoningContent / Reasoning are the same field under two spellings.
	// litellm normalizes most vendors to `reasoning_content`; `reasoning`
	// appears in the wild (and is what some native endpoints emit). Both
	// are decoded and collapsed by parseSSEChunk; neither ever joins
	// Content — reasoning is model scratch, not its committed answer.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Reasoning        string `json:"reasoning,omitempty"`
}

type wireStreamResponse struct {
	Choices []wireStreamChoice `json:"choices"`
	Usage   *wireUsage         `json:"usage,omitempty"`
}

func parseSSEChunk(payload []byte) (Chunk, error) {
	var w wireStreamResponse
	if err := json.Unmarshal(payload, &w); err != nil {
		return Chunk{}, err
	}
	var out Chunk
	// A choices-less payload is not malformed: with
	// stream_options.include_usage the provider emits a final chunk whose
	// only content is `usage`. Everything below is independently guarded so
	// that chunk decodes to a Usage-only Chunk.
	if len(w.Choices) > 0 {
		c := w.Choices[0]
		out.Content = c.Delta.Content
		out.Reasoning = c.Delta.ReasoningContent
		if out.Reasoning == "" {
			out.Reasoning = c.Delta.Reasoning
		}
		out.FinishReason = c.FinishReason
	}
	if w.Usage != nil {
		out.Usage = w.Usage.usage()
	}
	return out, nil
}
