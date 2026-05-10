package prompt

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptEmptyLayers(t *testing.T) {
	got := BuildSystemPrompt(SystemPromptParams{})

	// Topic-tag directive is always present.
	if !strings.Contains(got, TopicTagDirective) {
		t.Errorf("topic-tag directive missing from prompt:\n%s", got)
	}

	// No layer sections should appear when all layers are empty.
	for _, header := range layerHeaders() {
		if strings.Contains(got, header) {
			t.Errorf("unexpected section header %q in empty-layer prompt", header)
		}
	}
}

func TestBuildSystemPromptAllLayers(t *testing.T) {
	got := BuildSystemPrompt(SystemPromptParams{
		LayerE:  "directive-E-content",
		LayerA1: "spine-A1-content",
		LayerA2: "digest-A2-content",
		LayerB:  "thread-B-content",
		LayerC:  "summary-C-content",
	})

	// All section headers and content present.
	wantSubstrs := []string{
		TopicTagDirective,
		"PROJECT CONVENTIONS AND DIRECTIVES (Layer E)",
		"directive-E-content",
		"CURRENT PROJECT SPINE (Layer A1)",
		"spine-A1-content",
		"OTHER PROJECTS (Layer A2",
		"digest-A2-content",
		"ACTIVELY ENGAGED THREADS (Layer B)",
		"thread-B-content",
		"RECENTLY DORMANT THREADS (Layer C",
		"summary-C-content",
	}
	for _, w := range wantSubstrs {
		if !strings.Contains(got, w) {
			t.Errorf("missing substring %q in prompt:\n%s", w, got)
		}
	}

	// Order check: each header must come before the next.
	headers := layerHeaders()
	last := -1
	for _, h := range headers {
		idx := strings.Index(got, h)
		if idx < 0 {
			t.Errorf("header %q missing", h)
			continue
		}
		if idx <= last {
			t.Errorf("header %q out of order at idx %d (previous %d)", h, idx, last)
		}
		last = idx
	}
}

func TestBuildSystemPromptPartialLayers(t *testing.T) {
	got := BuildSystemPrompt(SystemPromptParams{
		LayerA1: "spine-content-only",
		LayerB:  "active-thread-content",
	})

	mustContain := []string{
		"CURRENT PROJECT SPINE (Layer A1)",
		"spine-content-only",
		"ACTIVELY ENGAGED THREADS (Layer B)",
		"active-thread-content",
	}
	for _, w := range mustContain {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q", w)
		}
	}

	mustNotContain := []string{
		"PROJECT CONVENTIONS AND DIRECTIVES (Layer E)",
		"OTHER PROJECTS (Layer A2",
		"RECENTLY DORMANT THREADS (Layer C",
	}
	for _, w := range mustNotContain {
		if strings.Contains(got, w) {
			t.Errorf("unexpected section %q in partial-layer prompt", w)
		}
	}
}

func TestBuildSystemPromptStartsWithPreamble(t *testing.T) {
	got := BuildSystemPrompt(SystemPromptParams{})
	if !strings.HasPrefix(got, "You are personant") {
		t.Errorf("prompt should start with orientation preamble, got prefix: %q", firstN(got, 60))
	}
}

// layerHeaders is the ordered list of section headers BuildSystemPrompt
// emits. Kept in lockstep with the slice in BuildSystemPrompt itself.
func layerHeaders() []string {
	return []string{
		"PROJECT CONVENTIONS AND DIRECTIVES (Layer E)",
		"CURRENT PROJECT SPINE (Layer A1)",
		"OTHER PROJECTS (Layer A2",
		"ACTIVELY ENGAGED THREADS (Layer B)",
		"RECENTLY DORMANT THREADS (Layer C",
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
