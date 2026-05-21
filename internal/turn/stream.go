package turn

import (
	"errors"
	"io"

	"personant/internal/model"
	"personant/internal/prompt"
)

// streamThroughFilter pumps every chunk's Content through filter until
// the StreamReader signals EOF. Errors from filter.Write are surfaced
// immediately — a sink that fails to accept bytes is not something the
// runtime can recover from per-chunk.
func streamThroughFilter(sr model.StreamReader, filter io.Writer) error {
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
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
// surfaced verbatim.
func readPreamble(sr model.StreamReader) (preambleResult, error) {
	var buf []byte
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			return classifyPreamble(buf, true), nil
		}
		if err != nil {
			return preambleResult{}, err
		}
		if chunk.Content == "" {
			continue
		}
		buf = append(buf, chunk.Content...)
		if _, _, _, done := prompt.PreambleScan(buf); done {
			return classifyPreamble(append([]byte(nil), buf...), false), nil
		}
	}
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
