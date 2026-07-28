package turn

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// The #127 post-flight token-ceiling guard, both of its outcomes.
//
// This guard had never been seen to fire: every real turn streams, and a
// streamed response reported no usage at all until the request started
// asking for it (model's stream_options.include_usage), so
// `full.Usage.PromptTokens > ceiling` was `0 > ceiling` on every
// production turn. The mock reports canned usage, which hid the gap. Two
// behaviors are pinned here: the breach fires when the provider's count
// really does exceed the ceiling, and the ABSENCE of a count is reported
// as unenforceable rather than passing silently.
func TestTokenCeilingGuard(t *testing.T) {
	const ceiling = 1000

	const (
		breach        = "context-ceiling-breach"
		unenforceable = "context-ceiling-unenforceable"
	)

	for _, tc := range []struct {
		name         string
		promptTokens int
		want         string
		absent       []string
	}{
		{
			name:         "breach-fires",
			promptTokens: ceiling + 1,
			want:         breach,
			absent:       []string{unenforceable},
		},
		{
			name:         "usage-unavailable-is-visible",
			promptTokens: 0,
			want:         unenforceable,
			absent:       []string{breach},
		},
		{
			name:         "within-ceiling-is-silent",
			promptTokens: ceiling - 1,
			absent:       []string{breach, unenforceable},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, meta := newTestHome(t)
			pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

			mock := model.NewScriptedMock([]model.Response{{
				Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world.",
				Usage:   model.Usage{PromptTokens: tc.promptTokens},
			}}, nil)
			state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock,
				WithTokenCeiling(ceiling))

			if _, _, err := RunWithInfo(context.Background(), state, nil, "hi", &bytes.Buffer{}); err != nil {
				t.Fatalf("RunWithInfo: %v", err)
			}

			logged := readArchivalEventLog(t, paths)
			if tc.want != "" && !strings.Contains(logged, tc.want) {
				t.Errorf("expected %s in the event log; prompt_tokens=%d ceiling=%d\nlog:\n%s",
					tc.want, tc.promptTokens, ceiling, logged)
			}
			for _, a := range tc.absent {
				if strings.Contains(logged, a) {
					t.Errorf("did not expect %s in the event log; prompt_tokens=%d ceiling=%d\nlog:\n%s",
						a, tc.promptTokens, ceiling, logged)
				}
			}
		})
	}
}

// TestTokenCeilingUnsetIsSilent: with no ceiling configured there is
// nothing to enforce, so an absent usage count is not a finding.
func TestTokenCeilingUnsetIsSilent(t *testing.T) {
	paths, meta := newTestHome(t)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	mock := model.NewScriptedMock([]model.Response{{
		Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world.",
	}}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	state.Budget.TokenCeiling = 0

	if _, _, err := RunWithInfo(context.Background(), state, nil, "hi", &bytes.Buffer{}); err != nil {
		t.Fatalf("RunWithInfo: %v", err)
	}
	if logged := readArchivalEventLog(t, paths); strings.Contains(logged, "context-ceiling-") {
		t.Errorf("no ceiling configured must log nothing:\n%s", logged)
	}
}
