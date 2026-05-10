package turn

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"personant/internal/eventlog"
	"personant/internal/prompt"
	"personant/internal/store"
)

// Delta is one context-modification event (spec §3.0.1). Source is the
// dotted event name from the §3.0.1 vocabulary (e.g. "user.prompt",
// "model.response", "tool.result"). Content is the raw delta text.
// Meta carries source-specific metadata; nil-safe.
type Delta struct {
	Source  string
	Content string
	Meta    map[string]string
}

// userTagRE matches user-emitted hash-tags in a prompt. The character
// class deliberately excludes uppercase letters — the §2.7.2
// normalization downcase happens via store.Normalize after extraction,
// but the surface form for tag-class symbols is conventionally lower
// already, so the regex stays narrow.
//
// Pattern matches: "#alpha-beta", "#feat_2", "#a"; rejects:
// "#-leading-dash", "trailing#" without a leading word boundary.
var userTagRE = regexp.MustCompile(`(?:^|[^a-z0-9_-])#([a-z0-9][a-z0-9_-]*)`)

// onContextDelta runs the spec §3.0 hook chain on one delta. The order
// of the steps is fixed; see §3.0.3.
//
// The chain mutates state in two places:
//   - state.coalesce — symbol and thread accumulator (step 1).
//   - on-disk event log — via eventlog.LogContextModified (step 5).
//
// Persistent engagement updates (spine writes) are deferred to turn
// close — see §3.0.4.
func onContextDelta(state *State, delta Delta) error {
	// Step 1: symbol extraction.
	if err := extractSymbols(state, delta); err != nil {
		return fmt.Errorf("turn: extract symbols: %w", err)
	}
	// Step 2: engagement signal (DEFERRED per §3.0.4 — enqueue only).
	//   noteEngagementOwed(state, delta) is implicit: extractSymbols put
	//   the source's threads into state.coalesce.threads.
	// Step 3: dedup decision — no-op in v0.1.
	//   TODO(v0.2-dedup): per §3.9, replace literals with content-addressed
	//   identifiers in the live window once a second-or-later occurrence is
	//   observed; persistent storage records the diff chain.
	// Step 4: budget check — no-op in v0.1.
	//   TODO(phase-2-budget): track byte counts when working-set composition
	//   lands more layers (§2.e); evict to honor layer caps.
	// Step 5: logging.
	if err := eventlog.LogContextModified(state.Paths, delta.Source, len(delta.Content)); err != nil {
		return fmt.Errorf("turn: log context.modified: %w", err)
	}
	return nil
}

// extractSymbols applies the §3.3 three-pass extraction policy. The
// deterministic pass (URLs, file paths, hex IDs) runs over every
// delta source; source-specific passes layer on top:
//   - user.prompt: hash-tag regex → SourceUser
//   - model.response: topic-tag parser → SourceModel
//
// Other sources (tool.result, thread.fetched, slash.injected,
// digest.refresh, directive.reloaded, user.shell-capture) currently
// contribute through the deterministic pass only.
func extractSymbols(state *State, delta Delta) error {
	deterministicExtract(state, delta.Content)

	switch delta.Source {
	case "user.prompt":
		for _, tag := range userTagRE.FindAllStringSubmatch(delta.Content, -1) {
			raw := tag[1]
			normalized := store.Normalize(raw, store.SymbolTag)
			state.coalesce.addSymbol(raw, normalized, store.SourceUser)
		}
		return nil

	case "model.response":
		result, err := prompt.Parse(delta.Content)
		if err != nil {
			if errors.Is(err, prompt.ErrNoTopicTag) {
				// §5.1.2: missing tag is a warning, not a fatal — log and continue.
				_ = eventlog.Log(state.Paths, "topic", "tag-missing",
					"source=model.response bytes="+itoa(len(delta.Content)))
				return nil
			}
			return err
		}
		for _, a := range result.Tag.Anchors {
			// prompt.Parse already normalizes anchors (§2.7.2 entity rules),
			// so raw == normalized here. A future surface-form pass would
			// thread the original through.
			state.coalesce.addSymbol(a, a, store.SourceModel)
		}
		for _, t := range result.Tag.Threads {
			state.coalesce.addThread(t)
		}
		// Each emitted topic tag's anchors are also engagement-relevant
		// symbols; warning lines are surfaced so a calibration pass can
		// catch malformed-tag drift.
		for _, w := range result.Warnings {
			_ = eventlog.Log(state.Paths, "topic", "warning", "source=model.response detail="+sanitizeDetail(w))
		}
		return nil

	default:
		// Deterministic pass already ran above; no source-specific
		// extractor for tool.result, thread.fetched, slash.injected,
		// digest.refresh, directive.reloaded, user.shell-capture.
		return nil
	}
}

// sanitizeDetail trims newlines and control chars from a free-form detail
// string before it lands in a log line. Log lines are one per event;
// embedded newlines would split them.
func sanitizeDetail(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

// itoa is a tiny helper to avoid pulling fmt for one int format on a hot
// path that runs once per delta; matches the pattern used in eventlog
// tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
