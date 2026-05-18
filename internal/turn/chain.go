package turn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"personant/internal/memops"
	"personant/internal/prompt"
)

// Delta is one context-modification event (spec §3.0.1). Source is the
// dotted event name from the §3.0.1 vocabulary (e.g. "user.prompt",
// "model.response", "tool.result"). Content is the raw delta text.
// Meta carries source-specific metadata; nil-safe.
//
// Retention is the provisional retention class (§3.0 transient-data
// lifecycle). When empty, onContextDelta fills it in via
// provisionalRetention(Source). Callers may set it explicitly to
// override the source-driven default — the v0.1 use case is the future
// `##` / `/keep` shell-capture override that escalates a task-class
// source to RetentionDecision.
type Delta struct {
	Source    string
	Content   string
	Meta      map[string]string
	Retention memops.RetentionClass
}

// userTagRE matches user-emitted hash-tags in a prompt. The character
// class deliberately excludes uppercase letters — the §2.7.2
// normalization downcase happens via memops.Normalize after extraction,
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
//   - on-disk event log — via state.Ops.EmitDelta (step 5).
//
// Persistent engagement updates (spine writes) are deferred to turn
// close — see §3.0.4.
func onContextDelta(ctx context.Context, state *State, delta Delta) error {
	// Step 0: provisional retention class (§3.0.1 source-driven default).
	// Set only when the caller has not already provided one — preserves a
	// future user-override path (e.g. a `##`-prefix shell capture that
	// explicitly opts a task-source delta into RetentionDecision).
	if delta.Retention == "" {
		delta.Retention = provisionalRetention(delta.Source)
	}
	// Step 1: symbol extraction.
	if err := extractSymbols(ctx, state, delta); err != nil {
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
	// Step 5: logging — record the context-modification event on the
	// substrate side. Retention is the provisional class set above (or
	// the caller's explicit override). The current file adapter ignores
	// the field; a future transient-data-aware adapter will route on it.
	if err := state.Ops.EmitDelta(ctx, memops.Delta{
		Source:    delta.Source,
		Content:   delta.Content,
		Meta:      delta.Meta,
		Retention: delta.Retention,
	}); err != nil {
		return fmt.Errorf("turn: emit delta: %w", err)
	}
	return nil
}

// provisionalRetention returns the §3.0.1 source-driven default
// retention class for a delta. This is the "stage 1" classification
// (provisional, mechanical); cross-reference promotion in B.3 confirms
// or discards. An unknown source defaults to RetentionDecision — the
// conservative direction (callers that haven't been migrated do not
// silently drop content).
func provisionalRetention(source string) memops.RetentionClass {
	switch source {
	case "tool.result", "user.shell-capture":
		return memops.RetentionTask
	case "user.prompt", "model.response",
		"thread.fetched", "digest.refresh",
		"slash.injected", "directive.reloaded":
		return memops.RetentionDecision
	default:
		return memops.RetentionDecision
	}
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
func extractSymbols(ctx context.Context, state *State, delta Delta) error {
	deterministicExtract(ctx, state, delta)

	switch delta.Source {
	case "user.prompt":
		for _, tag := range userTagRE.FindAllStringSubmatch(delta.Content, -1) {
			raw := tag[1]
			normalized := memops.Normalize(raw, memops.SymbolTag)
			addExtractedSymbol(ctx, state, delta, raw, normalized, memops.SourceUser)
		}
		return nil

	case "model.response":
		result, err := prompt.Parse(delta.Content)
		if err != nil {
			if errors.Is(err, prompt.ErrNoTopicTag) {
				// §5.1.2: missing tag is a warning, not a fatal — log and continue.
				_ = state.Ops.Log(ctx, "topic", "tag-missing",
					"source=model.response bytes="+itoa(len(delta.Content)))
				return nil
			}
			return err
		}
		for _, a := range result.Tag.Anchors {
			// prompt.Parse already normalizes anchors (§2.7.2 entity rules),
			// so raw == normalized here. A future surface-form pass would
			// thread the original through.
			addExtractedSymbol(ctx, state, delta, a, a, memops.SourceModel)
		}
		for _, t := range result.Tag.Threads {
			state.coalesce.addThread(t)
		}
		// Each emitted topic tag's anchors are also engagement-relevant
		// symbols; warning lines are surfaced so a calibration pass can
		// catch malformed-tag drift.
		for _, w := range result.Warnings {
			_ = state.Ops.Log(ctx, "topic", "warning", "source=model.response detail="+sanitizeDetail(w))
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
