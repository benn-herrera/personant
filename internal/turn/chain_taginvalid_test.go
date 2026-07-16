package turn

import (
	"context"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/prompt"
)

// TestChainTagInvalidLogEmission drives a model.response carrying a
// near-miss (bold-mangled) tag through the §3.0 chain and asserts the
// close-time parse site emits BOTH topic.tag-missing (unchanged) and the
// additive topic.tag-invalid forensic with a reason and snippet. This pins
// the chain call site's wiring of prompt.ClassifyNearMiss.
func TestChainTagInvalidLogEmission(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "model.response",
		Content: "**topic:** thr_5 [trefoil, unknot]\nHere is the body.",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	logs := readChainLogs(t, paths)
	// The missing-tag measure must still fire (additive contract).
	if !strings.Contains(logs, "topic.tag-missing ") {
		t.Errorf("expected topic.tag-missing to still fire; logs:\n%s", logs)
	}
	if !strings.Contains(logs, "topic.tag-invalid ") {
		t.Fatalf("expected topic.tag-invalid forensic line; logs:\n%s", logs)
	}
	if !strings.Contains(logs, "reason="+prompt.NearMissMarkdownMangled) {
		t.Errorf("expected reason=%s in tag-invalid line; logs:\n%s", prompt.NearMissMarkdownMangled, logs)
	}
	if !strings.Contains(logs, "snippet=") {
		t.Errorf("expected a snippet= field in the tag-invalid line; logs:\n%s", logs)
	}
}

// TestChainNoTagInvalidWhenNeverAttempted asserts the near-miss forensic is
// truly additive-and-conditional: a tag-less response with NO near-miss
// candidate emits topic.tag-missing but NOT topic.tag-invalid, so the two
// bins ("never attempted" vs "attempted but malformed") stay disjoint.
func TestChainNoTagInvalidWhenNeverAttempted(t *testing.T) {
	paths, meta := newChainHome(t)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, nil)

	if err := onContextDelta(context.Background(), state, Delta{
		Source:  "model.response",
		Content: "Sure, here's a plain conversational answer with no tag.",
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}

	logs := readChainLogs(t, paths)
	if !strings.Contains(logs, "topic.tag-missing ") {
		t.Errorf("expected topic.tag-missing; logs:\n%s", logs)
	}
	if strings.Contains(logs, "topic.tag-invalid ") {
		t.Errorf("did not expect topic.tag-invalid for a never-attempted response; logs:\n%s", logs)
	}
}
