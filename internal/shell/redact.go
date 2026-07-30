package shell

import "strings"

// SPEC §8.2.1 / §6.4 hybrid redaction policy, user-initiated half.
//
// An agent-initiated read of a key file is REFUSED outright (that half
// belongs to the future fs.* tool layer). A user-initiated `#` capture is
// REDACTED: the user grepping their own home directory is reasonable but
// accidental, so the key material is stripped on the path into context
// while the user's terminal keeps showing exactly what they ran.
//
// Two rules from §8.2.1 shape this file:
//
//  1. Resolved key material must never appear in ANY log line. The
//     redaction event therefore records that it fired and how many bytes
//     went, never what matched.
//  2. Only the path-into-LLM-context is filtered. Redact is applied to the
//     capture buffer AFTER it has already streamed to the terminal, so
//     the ordering is correct by construction rather than by discipline.

// RedactionPlaceholder is the §8.2.1 substitution text.
const RedactionPlaceholder = "[redacted: API-key content]"

// minRedactableKey is the shortest key string the redactor will match on.
//
// A degenerate provider entry — an empty or one-character apiKeyUnsafe, a
// key file that holds a single letter — would otherwise match nearly
// every capture and silently shred it. Refusing to scan for a "key" that
// short is the safe direction: such a value is not protecting anything,
// and a redactor that destroys all context is a worse failure than one
// that misses a two-byte secret.
const minRedactableKey = 8

// Redactor removes resolved provider API keys from `#` capture text.
//
// It holds key material in memory, which is unavoidable — the runtime
// already does, to make API calls. What it must never do is emit that
// material: Redact returns only the cleaned text and a byte count, and
// there is deliberately no method that reports what matched.
type Redactor struct {
	keys []string
}

// NewRedactor builds a redactor over the resolved keys of the loaded
// provider pool. Keys shorter than minRedactableKey and duplicates are
// dropped. A redactor with no usable keys is inert, not nil-hostile —
// Redact is a pass-through, so callers need no special case.
func NewRedactor(keys []string) *Redactor {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if len(k) < minRedactableKey {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return &Redactor{keys: out}
}

// Redact replaces every occurrence of a known key with
// RedactionPlaceholder and reports how many occurrences fired and how
// many bytes of key material were removed.
//
// Substitution is per-occurrence rather than whole-buffer: §8.2.1's
// "captured output is replaced with [redacted: API-key content]" is read
// as an inline replacement of the key material, which keeps the rest of
// a `# env` or `# grep -r` capture useful. It is no less protective — the
// key bytes never reach context either way.
//
// A nil Redactor is a valid inert redactor.
func (r *Redactor) Redact(s string) (out string, occurrences, bytesRemoved int) {
	if r == nil || len(r.keys) == 0 || s == "" {
		return s, 0, 0
	}
	for _, k := range r.keys {
		n := strings.Count(s, k)
		if n == 0 {
			continue
		}
		occurrences += n
		bytesRemoved += n * len(k)
		s = strings.ReplaceAll(s, k, RedactionPlaceholder)
	}
	return s, occurrences, bytesRemoved
}
