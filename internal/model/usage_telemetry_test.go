package model

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// mlxUsageBlock is the `usage` payload a live streamed request to the
// reaper provider (MLX-backed, via litellm) returned, VERBATIM off the
// wire. It is kept byte-for-byte rather than hand-simplified so a field
// name that drifts — or one this decoder spells wrong — fails here instead
// of silently decoding to zero.
//
// Note what is genuinely ABSENT: `completion_tokens_details`. That is a
// provider gap (ReasoningTokens stays 0), not a parsing bug.
const mlxUsageBlock = `{"prompt_tokens":962,"completion_tokens":900,"total_tokens":1862,
"input_tokens":962,"output_tokens":900,
"prompt_tokens_details":{"cached_tokens":0},
"time_to_first_token":0.61,"total_time":6.89,
"prompt_eval_duration":0.61,"generation_duration":6.28,
"prompt_tokens_per_second":1567.45,"generation_tokens_per_second":143.35}`

// wantMLXUsage is mlxUsageBlock as the caller-facing type: token counts
// straight through, cached_tokens from the prompt-token details, and the
// four wire durations (FRACTIONAL SECONDS) converted to time.Duration.
var wantMLXUsage = Usage{
	PromptTokens:       962,
	CompletionTokens:   900,
	TotalTokens:        1862,
	ReasoningTokens:    0, // completion_tokens_details absent on this provider
	CachedPromptTokens: 0,
	Telemetry: InferenceTelemetry{
		TimeToFirstToken:          610 * time.Millisecond,
		PrefillDuration:           610 * time.Millisecond,
		GenerationDuration:        6280 * time.Millisecond,
		TotalDuration:             6890 * time.Millisecond,
		PrefillTokensPerSecond:    1567.45,
		GenerationTokensPerSecond: 143.35,
	},
}

// TestUsageTelemetryDecode_MLX pins the extended block's decode on BOTH
// paths off the one shared projection: the blocking response and the
// streamed include_usage chunk.
func TestUsageTelemetryDecode_MLX(t *testing.T) {
	t.Run("blocking", func(t *testing.T) {
		resp, err := decodeResponse([]byte(
			`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],
			"usage":` + mlxUsageBlock + `}`))
		if err != nil {
			t.Fatalf("decodeResponse: %v", err)
		}
		if resp.Usage != wantMLXUsage {
			t.Errorf("blocking Usage =\n %+v\nwant\n %+v", resp.Usage, wantMLXUsage)
		}
	})

	t.Run("streamed", func(t *testing.T) {
		// SSE frames one event per line, so the capture's line wrapping —
		// and only that — is removed here. The field names and values stay
		// verbatim, which is what the fixture exists to pin.
		oneLine := strings.ReplaceAll(mlxUsageBlock, "\n", "")
		sse := `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":` + oneLine + `}

data: [DONE]

`
		_, final, err := runStream(t, sse)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("stream did not end cleanly: %v", err)
		}
		if final.Usage != wantMLXUsage {
			t.Errorf("streamed Usage =\n %+v\nwant\n %+v", final.Usage, wantMLXUsage)
		}
	})

	t.Run("predicate-and-log-detail", func(t *testing.T) {
		if !wantMLXUsage.HasTelemetry() {
			t.Fatal("HasTelemetry = false on a block that carries telemetry")
		}
		const want = "ttft_ms=610.0 prefill_ms=610.0 gen_ms=6280.0 total_ms=6890.0 " +
			"prefill_tps=1567.5 gen_tps=143.3 prompt_tokens=962 completion_tokens=900 cached_tokens=0"
		if got := wantMLXUsage.TelemetryLogDetail(); got != want {
			t.Errorf("TelemetryLogDetail =\n %q\nwant\n %q", got, want)
		}
	})
}

// TestUsageTelemetryAbsent: a standard OpenAI usage block carries none of
// the extension fields. That must decode with no error, leave every
// telemetry field zero, and report HasTelemetry false — absence is the
// normal case on most providers, never a fault.
func TestUsageTelemetryAbsent(t *testing.T) {
	const openAIUsage = `{"prompt_tokens":41,"completion_tokens":9,"total_tokens":50,
"completion_tokens_details":{"reasoning_tokens":4}}`

	resp, err := decodeResponse([]byte(
		`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}],"usage":` + openAIUsage + `}`))
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	want := Usage{PromptTokens: 41, CompletionTokens: 9, TotalTokens: 50, ReasoningTokens: 4}
	if resp.Usage != want {
		t.Errorf("Usage = %+v; want %+v (every extension field zero)", resp.Usage, want)
	}
	if resp.Usage.HasTelemetry() {
		t.Errorf("HasTelemetry = true on a standard OpenAI usage block: %+v", resp.Usage.Telemetry)
	}
	if resp.Usage.Telemetry != (InferenceTelemetry{}) {
		t.Errorf("Telemetry = %+v; want zero", resp.Usage.Telemetry)
	}

	// A usage block with nothing in it at all stays fully zero, and IsZero
	// still reports "the provider supplied nothing".
	resp, err = decodeResponse([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{}}`))
	if err != nil {
		t.Fatalf("decodeResponse (empty usage): %v", err)
	}
	if !resp.Usage.IsZero() || resp.Usage.HasTelemetry() {
		t.Errorf("empty usage block: IsZero=%v HasTelemetry=%v; want true/false",
			resp.Usage.IsZero(), resp.Usage.HasTelemetry())
	}
}

// TestUsageIsZeroWithTelemetryOnly: IsZero is the WHOLE-struct zero test —
// "did this block carry anything?" — so a block carrying telemetry but no
// token counts is NOT zero. That is what its callers assume: the stream
// reader latches the last non-empty usage block, and a telemetry-only block
// is a block worth latching.
//
// The #127 token-ceiling guard is unaffected either way: it branches on
// Usage.PromptTokens directly, never on IsZero, so a telemetry-only block
// still reads as `prompt_tokens=0` → `context-ceiling-unenforceable`, which
// is exactly right — telemetry is not a token count.
func TestUsageIsZeroWithTelemetryOnly(t *testing.T) {
	const telemetryOnly = `{"time_to_first_token":0.5,"total_time":2.0,
"prompt_eval_duration":0.5,"generation_duration":1.5,
"prompt_tokens_per_second":900.0,"generation_tokens_per_second":120.0}`

	resp, err := decodeResponse([]byte(
		`{"choices":[{"message":{"content":"ok"}}],"usage":` + telemetryOnly + `}`))
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	if resp.Usage.IsZero() {
		t.Errorf("IsZero = true on a telemetry-bearing block: %+v", resp.Usage)
	}
	if !resp.Usage.HasTelemetry() {
		t.Errorf("HasTelemetry = false on a telemetry-bearing block: %+v", resp.Usage)
	}
	if resp.Usage.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d; want 0 — the #127 guard must still see it as unmeasured",
			resp.Usage.PromptTokens)
	}
	if got := resp.Usage.Telemetry.GenerationDuration; got != 1500*time.Millisecond {
		t.Errorf("GenerationDuration = %v; want 1.5s", got)
	}
}
