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

	"personant/internal/log"
)

// sseMaxLineBytes is the buffer cap for a single SSE line. The bufio.Scanner
// default of 64KB is too tight: SSE payloads occasionally embed multi-KB
// tool-call argument blobs on one line. 1MB is generous for well-behaved
// providers and clamps a runaway server before OOM.
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

	// toolCallGuardLogged tracks whether the streamed-tool-call guard has
	// already logged for this reader, so a multi-chunk tool-call stream
	// logs once rather than per delta.
	toolCallGuardLogged bool

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
		if chunk.Usage.TotalTokens != 0 || chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 {
			r.usage = chunk.Usage
		}
		// Streamed tool-call deltas are NOT merged into Final(). OpenAI-style
		// providers fragment a single tool call across chunks (id/name once,
		// then argument-string fragments keyed by index); naively appending
		// the per-chunk deltas would yield corrupt, half-parsed arguments in
		// Final().ToolCalls. The runtime does not stream tool calls today
		// (no ConsultStream caller passes Tools), so rather than ship an
		// un-exercisable merge path we guard loudly: the fragments are
		// dropped from the accumulated Final() and the condition is logged.
		// Whoever wires tool-call streaming will hit this log and must
		// implement index-keyed merge against a real provider. The per-chunk
		// Chunk.ToolCalls is still surfaced below (documented best-effort).
		if len(chunk.ToolCalls) > 0 && !r.toolCallGuardLogged {
			r.toolCallGuardLogged = true
			log.Error("model: streamed tool-call deltas observed but not merged; Final().ToolCalls will be empty — use Consult for tool-calling")
		}
		return chunk, nil
	}
}

// Final returns the accumulated Response after iteration. Safe to call
// before EOF, though the resulting Content/Usage/FinishReason will only
// reflect what has been observed so far.
//
// ToolCalls is always nil: streamed tool-call deltas are not merged (see
// the guard in Next). Callers that need tool calls must use Consult.
func (r *httpStreamReader) Final() Response {
	return Response{
		Content:      r.content.String(),
		ToolCalls:    nil,
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
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	ToolCalls []wireToolCall `json:"tool_calls,omitempty"`
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
	out := Chunk{}
	if len(w.Choices) > 0 {
		c := w.Choices[0]
		out.Content = c.Delta.Content
		out.FinishReason = c.FinishReason
		for _, tc := range c.Delta.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:       tc.ID,
				Function: tc.Function.Name,
				Args:     tc.Function.Arguments,
			})
		}
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
