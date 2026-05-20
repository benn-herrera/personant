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
// up through the first newline (head), bytes that arrived in the same
// chunk after that newline (tail), and a parsed topic tag if head was
// recognized as one (per §5.1.2). ended==true signals the stream EOF'd
// before any newline was seen — head holds whatever bytes did arrive,
// tail is empty.
//
// readPreamble + classifyPreamble are split so the unit tests can
// exercise the classification in isolation from the StreamReader pump.
type preambleResult struct {
	head  []byte           // first line up to and including its trailing \n
	tail  []byte           // bytes after that \n in the chunk that contained it
	tag   *prompt.TopicTag // non-nil iff head parsed as a §5.1.2 topic tag
	ended bool             // true when EOF arrived before any \n
}

// readPreamble loops over chunks from sr until either the first newline
// arrives or the stream ends. Returned head/tail point into freshly
// allocated buffers — the caller owns them across subsequent sr.Next()
// calls. Errors other than io.EOF are surfaced verbatim.
func readPreamble(sr model.StreamReader) (preambleResult, error) {
	var buf []byte
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			return classifyPreamble(buf, nil, true), nil
		}
		if err != nil {
			return preambleResult{}, err
		}
		if chunk.Content == "" {
			continue
		}
		buf = append(buf, chunk.Content...)
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			head := append([]byte(nil), buf[:i+1]...)
			tail := append([]byte(nil), buf[i+1:]...)
			return classifyPreamble(head, tail, false), nil
		}
	}
}

// classifyPreamble inspects head and reports a parsed topic tag if it
// matches §5.1.2 with a non-empty thread list. tail and ended are
// passed through unchanged. head may or may not include a trailing
// newline (it does when readPreamble found one; it doesn't when the
// stream ended early); prompt.Parse is multi-line anchored so we feed
// it head as-is plus a synthetic newline only if absent.
func classifyPreamble(head, tail []byte, ended bool) preambleResult {
	res := preambleResult{head: head, tail: tail, ended: ended}
	if len(head) == 0 {
		return res
	}
	candidate := head
	if candidate[len(candidate)-1] != '\n' {
		c := make([]byte, len(candidate)+1)
		copy(c, candidate)
		c[len(c)-1] = '\n'
		candidate = c
	}
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
