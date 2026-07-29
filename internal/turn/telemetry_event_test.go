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

// The provider's extended inference-telemetry block is a NON-STANDARD
// extension: present on MLX-backed providers, absent on most others. Both
// halves of that are pinned here — the line is written when the block
// arrives, and NOTHING is written when it does not. The second half is the
// load-bearing one: an all-zero telemetry line every turn on a provider
// that never reports it is noise that trains the reader to skip the event.
func TestInferenceTelemetryEvent(t *testing.T) {
	const event = "model.inference-telemetry"

	telemetry := model.InferenceTelemetry{
		TimeToFirstToken:          610 * time.Millisecond,
		PrefillDuration:           610 * time.Millisecond,
		GenerationDuration:        6280 * time.Millisecond,
		TotalDuration:             6890 * time.Millisecond,
		PrefillTokensPerSecond:    1567.45,
		GenerationTokensPerSecond: 143.35,
	}

	for _, tc := range []struct {
		name  string
		usage model.Usage
		want  []string // substrings that must appear; empty = event must be absent
	}{
		{
			name: "telemetry-present-is-logged",
			usage: model.Usage{
				PromptTokens: 962, CompletionTokens: 900, TotalTokens: 1862,
				CachedPromptTokens: 12,
				Telemetry:          telemetry,
			},
			want: []string{
				event,
				"ttft_ms=610.0", "prefill_ms=610.0", "gen_ms=6280.0", "total_ms=6890.0",
				"prefill_tps=1567.5", "gen_tps=143.3",
				"prompt_tokens=962", "completion_tokens=900", "cached_tokens=12",
				"turn=",
			},
		},
		{
			// Token counts but no extension block — the standard-provider
			// shape. Usage is reported; telemetry is not; no line.
			name:  "telemetry-absent-is-silent",
			usage: model.Usage{PromptTokens: 41, CompletionTokens: 9, TotalTokens: 50},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, meta := newTestHome(t)
			pinClock(t, time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC))

			mock := model.NewScriptedMock([]model.Response{{
				Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world.",
				Usage:   tc.usage,
			}}, nil)
			state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)

			_, info, err := RunWithInfo(context.Background(), state, nil, "hi", &bytes.Buffer{})
			if err != nil {
				t.Fatalf("RunWithInfo: %v", err)
			}
			if info.Telemetry != tc.usage.Telemetry {
				t.Errorf("TurnInfo.Telemetry = %+v; want %+v", info.Telemetry, tc.usage.Telemetry)
			}

			logged := readArchivalEventLog(t, paths)
			if len(tc.want) == 0 {
				if strings.Contains(logged, event) {
					t.Errorf("%s emitted with no telemetry on the wire:\n%s", event, logged)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(logged, w) {
					t.Errorf("missing %q from the event log:\n%s", w, logged)
				}
			}
		})
	}
}
