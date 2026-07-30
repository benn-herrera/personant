package turn

import (
	"bytes"
	"errors"
	"io"

	"personant/internal/model"
	"personant/internal/prompt"
)

// streamThroughFilter pumps every chunk's Content through filter until
// the StreamReader signals EOF. Errors from filter.Write are surfaced
// immediately — a sink that fails to accept bytes is not something the
// runtime can recover from per-chunk.
//
// A chunk's Reasoning is handed to State.OnReasoning and then dropped —
// it never joins the body. On a thinking model most chunks are
// reasoning-only (empty Content), so the emit precedes the empty-Content
// skip.
func streamThroughFilter(state *State, sr model.StreamReader, filter io.Writer) error {
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		emitReasoning(state, chunk.Reasoning)
		if chunk.Content == "" {
			continue
		}
		if _, werr := io.WriteString(filter, chunk.Content); werr != nil {
			return werr
		}
	}
}

// preambleResult bundles the bytes accumulated from a streaming response
// up through the point the topic tag was located (or the bounded preamble
// scan gave up). head holds the accumulated preamble bytes — the caller
// re-writes them through the stream filter, which independently locates
// and suppresses the tag line within the same bounded region. tag is the parsed §5.1.2 tag if one was located
// anywhere in the scanned preamble. ended==true signals EOF arrived before
// the scan resolved — head holds whatever bytes did arrive.
//
// readPreamble + classifyPreamble are split so the unit tests can
// exercise the classification in isolation from the StreamReader pump.
type preambleResult struct {
	head  []byte           // accumulated preamble bytes (re-fed to the filter)
	tag   *prompt.TopicTag // non-nil iff a §5.1.2 topic tag was located in head
	ended bool             // true when EOF arrived before the scan resolved
	// tools holds the IDENTITY-ONLY tool calls (model.Chunk.ToolCalls:
	// ID + Function, Args always nil) observed while reading the preamble.
	//
	// It is what tells a TOOL-CALL-ONLY response apart from an empty one.
	// A response that is nothing but tool calls carries zero visible
	// content, so the bounded scan never resolves and the stream reaches
	// EOF with an empty head — byte-for-byte the D6 empty-response
	// signature. Without this, the §6.1 tool round would be misread as a
	// protocol failure, burn the empty-response re-prompt, and then render
	// nothing. It is the identity channel doing exactly the job it was
	// added for.
	tools []model.ToolCall
}

// readPreamble accumulates chunks from sr until the bounded preamble scan
// (prompt.PreambleScan) locates a topic tag, rules one out (scan bound
// reached), or the stream ends — whichever comes first. This generalizes
// the prior "read to the first newline" behavior so a tag that follows a
// `<think>…</think>` reasoning block or conversational filler (real-model
// shapes the mock never produced) is still detected, driving the §5.5
// mid-turn fetch decision. The scan is bounded (prompt.PreambleScanLineCap
// / PreambleScanByteCap) so streaming latency stays bounded: the worst-case
// hold before bytes flow downstream is one of those caps.
//
// Returned head points into a freshly allocated buffer — the caller owns
// it across subsequent sr.Next() calls. Errors other than io.EOF are
// surfaced verbatim. Reasoning deltas are emitted to State.OnReasoning
// and dropped; they never enter head, so the preamble scan sees only
// committed content.
func readPreamble(state *State, sr model.StreamReader) (preambleResult, error) {
	var buf []byte
	var seen []model.ToolCall
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			res := classifyPreamble(buf, true)
			res.tools = seen
			return res, nil
		}
		if err != nil {
			return preambleResult{}, err
		}
		emitReasoning(state, chunk.Reasoning)
		seen = appendToolIdentities(seen, chunk.ToolCalls)
		if chunk.Content == "" {
			continue
		}
		buf = append(buf, chunk.Content...)
		if _, _, _, done := prompt.PreambleScan(buf); done {
			res := classifyPreamble(append([]byte(nil), buf...), false)
			res.tools = seen
			return res, nil
		}
	}
}

// appendToolIdentities merges one chunk's identity-only tool-call view
// into the running set, keyed by call ID. A chunk repeats the identity of
// every call it carried a fragment for, so the same ID arrives many times
// over a stream; only the first occurrence (and any later one that finally
// supplies the function name) is kept.
func appendToolIdentities(seen []model.ToolCall, chunkCalls []model.ToolCall) []model.ToolCall {
	for _, c := range chunkCalls {
		found := false
		for i := range seen {
			if seen[i].ID != c.ID {
				continue
			}
			found = true
			if seen[i].Function == "" {
				seen[i].Function = c.Function
			}
			break
		}
		if !found {
			seen = append(seen, model.ToolCall{ID: c.ID, Function: c.Function})
		}
	}
	return seen
}

// classifyPreamble locates a §5.1.2 topic tag anywhere within the bounded
// preamble head and, if found, parses it (requiring a non-empty thread
// list per §5.1.2). ended is passed through unchanged. The located tag
// line is fed to prompt.Parse so the parsed TopicTag (threads + anchors)
// matches what the full-body parse would yield, keeping the §5.5 fetch
// decision and the engagement parse consistent.
func classifyPreamble(head []byte, ended bool) preambleResult {
	res := preambleResult{head: head, ended: ended}
	if len(head) == 0 {
		return res
	}
	start, end, found, _ := prompt.PreambleScan(head)
	if !found {
		return res
	}
	// Feed the located tag line (plus a synthetic newline so the
	// multi-line-anchored regex matches) to Parse for full validation.
	line := head[start:end]
	candidate := make([]byte, 0, len(line)+1)
	candidate = append(candidate, line...)
	candidate = append(candidate, '\n')
	pr, err := prompt.Parse(string(candidate))
	if err != nil {
		return res
	}
	if len(pr.Tag.Threads) == 0 {
		return res
	}
	tag := pr.Tag
	res.tag = &tag
	return res
}

// preambleLacksTag reports whether a streamed response conclusively lacks
// a leading §5.1.2 topic tag — the D6 missing-tag re-prompt trigger. The
// bounded preamble scan is the runtime's operational definition of
// "response start" (the same authority behind the §5.5 fetch decision and
// the stream-filter tag suppression), so a tag buried past the scan bound
// (PreambleScanLineCap lines / PreambleScanByteCap bytes) counts as
// missing here.
//
// That bound is an ASYMMETRY against the close-time parser, not a shared
// convention: prompt.Parse at turn close binds a valid tag found ANYWHERE
// in the body, unbounded. So a response whose only tag sits past the scan
// bound is treated as tag-less by this trigger even though the close-time
// parse would have bound it — the stream is discarded and re-prompted.
// The cost is one wasted model stream on that (rare, deep-tag) shape; the
// failure mode is graceful: the re-issued response's tag binds normally,
// and a still-deep second tag binds at close (the coalesce is non-empty,
// so the owner-default never fires). Accepted as the price of a bounded
// streaming hold — closing the asymmetry would mean buffering the whole
// stream before the re-prompt decision.
//
// One refinement keeps the trigger honest at stream end: when EOF arrived
// before the scan resolved (pre.ended), the ENTIRE response is already in
// pre.head, so the complete-body Parse is exact and free. That prevents a
// wasted model round-trip for a tag on an unterminated final line (which
// PreambleScan cannot classify but the close-time parse would bind
// normally).
func preambleLacksTag(pre preambleResult) bool {
	if pre.tag != nil {
		return false
	}
	if pre.ended {
		_, err := prompt.Parse(string(pre.head))
		return err != nil
	}
	return true
}

// preambleIsEmpty reports whether a streamed response conclusively carries
// ZERO visible content — the D6 empty-response re-prompt trigger (spec
// §3.3, cause=empty-response; the reasoning-burn signature from the
// 2026-07-15 probe: finish=stop with the whole budget spent on hidden
// reasoning). Conclusive means the stream already ended (pre.ended): an
// empty or all-whitespace response never resolves the bounded preamble
// scan, so it always reaches EOF with the entire response in pre.head —
// the check is exact, not a truncated view. A response whose scan resolved
// early (!pre.ended) necessarily carried content and is never empty here.
//
// Bound note: an all-whitespace response LONGER than the scan bound
// (PreambleScanLineCap newlines / PreambleScanByteCap bytes of whitespace)
// resolves the scan as tag-less and drains without triggering this — a
// pathological shape no live run has produced; the close-time
// system.empty-response forensic line (turn.go) still records it.
func preambleIsEmpty(pre preambleResult) bool {
	return pre.ended && len(bytes.TrimSpace(pre.head)) == 0
}

// missingFromActiveB returns thread ids from threads that are not
// present in active. The literal "*new-topic*" sentinel never needs
// fetching and is filtered out unconditionally.
func missingFromActiveB(threads, active []string) []string {
	if len(threads) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(active))
	for _, id := range active {
		have[id] = struct{}{}
	}
	var out []string
	for _, t := range threads {
		if t == prompt.NewTopicLiteral {
			continue
		}
		if _, ok := have[t]; ok {
			continue
		}
		out = append(out, t)
	}
	return out
}
