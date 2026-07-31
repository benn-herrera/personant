package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// DecodeArgs unmarshals a provider-sent tool-call argument blob into dst.
//
// It exists because the wire representation is DOUBLE-ENCODED and every
// handler would otherwise repeat the unwrapping: OpenAI-compatible
// providers send `arguments` as a JSON *string* whose contents are the
// argument object, so model.ToolCall.Args typically reads
// `"{\"url\":\"https://…\"}"` rather than `{"url":"https://…"}`. Some
// providers (and every hand-written test) send the object directly.
// DecodeArgs accepts BOTH, so a handler declares its argument struct and
// nothing else.
//
// An empty or whitespace-only blob decodes as an EMPTY object rather than
// an error: a zero-argument tool is legitimate, and a model that emits
// `""` for one is not making a mistake worth spending a tool round on.
// A handler that requires a field validates that field itself — the
// resulting error names the field, which is far more actionable to the
// model than "invalid JSON".
func DecodeArgs(raw json.RawMessage, dst any) error {
	if dst == nil {
		return errors.New("tools: decode args: nil destination")
	}
	trimmed := bytes.TrimSpace(raw)
	// Unwrap the JSON-string envelope, once. A nested string ("\"{}\"")
	// is not a shape any provider produces; treating one level as the
	// contract keeps this from becoming a guessing loop.
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err != nil {
			return fmt.Errorf("tools: decode args: malformed argument string: %w", err)
		}
		trimmed = bytes.TrimSpace([]byte(inner))
	}
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if err := json.Unmarshal(trimmed, dst); err != nil {
		return fmt.Errorf("tools: decode args: %w", err)
	}
	return nil
}
