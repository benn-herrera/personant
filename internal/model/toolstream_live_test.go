// Live probe for streamed tool-call merging. It ALWAYS COMPILES (no build
// tag) and gates EXECUTION at runtime on testsupport.LiveTestsEnv, per the
// house live-test convention: bare `make test` compiles and skips it,
// `make integration-test` runs it against the `reaper` provider. Under the
// opt-in an unreachable endpoint is a FAILURE, not a skip.
//
// It issues exactly ONE request. The comment this wave replaced demanded
// validation "against a real provider"; this is that validation, and it
// doubles as the observation of how litellm actually fragments a tool call
// (logged, so `-v` shows the real wire shape).

package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"personant/internal/testsupport"
)

const (
	// liveToolChatModel is the reaper-served chat model the probe offers a
	// tool to — the same model the live sim uses (test/rundata's
	// defaultModel = "reaper/gemma-4-main").
	liveToolChatModel = "gemma-4-main"

	// liveToolTimeout bounds the single round-trip. Thinking-mode prefill
	// plus generation on the reaper host is minutes, not seconds.
	liveToolTimeout = 5 * time.Minute
)

// TestStreamToolCallMerge_Live offers one tool to a real provider over the
// streaming path and asserts the merged Final().ToolCalls. The response
// bytes are captured first so the fragmentation shape can be reported —
// the merge itself then runs over those bytes through the production
// reader, the same object ConsultStream hands back.
func TestStreamToolCallMerge_Live(t *testing.T) {
	testsupport.RequireLive(t)

	c := NewHTTPClient(testsupport.ReaperProvider()).(*HTTPClient)
	req := DefaultRequest(liveToolChatModel, []Message{{
		Role:    "user",
		Content: "What is the current weather in San Francisco? Call the get_weather tool to find out; do not guess.",
	}})
	req.Tools = []ToolSpec{{
		Name:        "get_weather",
		Description: "Get the current weather for a named location.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"location": {"type": "string", "description": "City name, e.g. San Francisco"}
			},
			"required": ["location"]
		}`),
	}}

	body, err := encodeRequest(req, true)
	testsupport.FailOnErr(t, "encode request", err)

	ctx, cancel := context.WithTimeout(context.Background(), liveToolTimeout)
	defer cancel()

	// Exactly the call ConsultStream makes; the only difference is that
	// the body is captured here before the reader consumes it.
	resp, err := c.doRequest(ctx, http.MethodPost, "chat/completions", "text/event-stream", body)
	testsupport.FailOnErr(t, "chat/completions (stream)", err)
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	testsupport.FailOnErr(t, "read stream body", err)

	t.Log(describeFragmentation(t, raw))

	sr := newHTTPStreamReader(ctx, io.NopCloser(bytes.NewReader(raw)))
	defer sr.Close()
	for {
		if _, err := sr.Next(); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("stream did not end cleanly (merge failed): %v", err)
			}
			break
		}
	}
	final := sr.Final()
	t.Logf("finish_reason=%q content=%dB usage=%+v tool_calls=%d",
		final.FinishReason, len(final.Content), final.Usage, len(final.ToolCalls))

	// Usage on a streamed response exists only because the request carried
	// stream_options.include_usage (encodeRequest, streaming path). Without
	// it the provider sends no usage payload at all and every count is 0 —
	// which is what silently disabled the #127 token-ceiling guard for the
	// life of the project. An all-zero usage here means the field stopped
	// being sent or the provider stopped honoring it.
	if final.Usage.PromptTokens <= 0 || final.Usage.TotalTokens <= 0 {
		t.Errorf("streamed usage not reported (usage=%+v) — stream_options.include_usage "+
			"is missing from the request or unsupported by the provider", final.Usage)
	}

	// The extended inference-telemetry block. It is a non-standard MLX/oMLX
	// extension — absence is NEVER an error in production — but this probe
	// runs against reaper, which does send it, so a zero block here means
	// the provider stopped sending it or renamed a field. The values are
	// logged so a run puts the real prefill-vs-generation split on the
	// record.
	t.Logf("inference telemetry: %s", final.Usage.TelemetryLogDetail())
	if !final.Usage.HasTelemetry() {
		t.Errorf("no inference telemetry decoded from the reaper provider (usage=%+v) — "+
			"the extended block is gone from the wire or its field names changed", final.Usage)
	} else if tel := final.Usage.Telemetry; tel.TimeToFirstToken <= 0 ||
		tel.GenerationDuration <= 0 || tel.GenerationTokensPerSecond <= 0 {
		t.Errorf("inference telemetry decoded but partially zero: %+v", tel)
	}

	if len(final.ToolCalls) == 0 {
		t.Fatalf("provider returned no merged tool calls; finish_reason=%q content=%q",
			final.FinishReason, final.Content)
	}
	for i, tc := range final.ToolCalls {
		if tc.Function == "" {
			t.Errorf("tool call %d: empty function name", i)
		}
		// Args is the raw JSON value of the wire `arguments` field: a
		// JSON-encoded string whose contents are the argument JSON.
		var argJSON string
		if err := json.Unmarshal(tc.Args, &argJSON); err != nil {
			t.Errorf("tool call %d: Args %s is not a JSON string: %v", i, tc.Args, err)
			continue
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(argJSON), &args); err != nil {
			t.Errorf("tool call %d: merged arguments are not a JSON object: %q (%v)", i, argJSON, err)
			continue
		}
		t.Logf("tool call %d: id=%q function=%q args=%s", i, tc.ID, tc.Function, argJSON)
	}
}

// describeFragmentation summarizes how the provider sliced the response:
// how many SSE payloads carried tool-call deltas, whether each carried an
// explicit index / id / name, and how long each argument fragment was.
// This is the probe's other deliverable — it says whether litellm conforms
// to the canonical OpenAI shape.
//
// It also reports the reasoning shape: how many payloads carried a
// thinking delta, how many bytes of it, and the LITERAL delta field names
// the provider used (read from the raw JSON keys, not the decoded struct —
// the field name is the conformance datum for whoever wires the UI, and
// both `reasoning_content` and `reasoning` are in the wild). Payloads that
// carry nothing the runtime decodes are counted too: a large "inert" count
// means the wire carries something this decoder still drops.
func describeFragmentation(t *testing.T, raw []byte) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("provider fragmentation:\n")
	var payloads, contentDeltas, toolDeltas int
	var reasoningDeltas, reasoningBytes, inertPayloads int
	deltaFields := map[string]int{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		p := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if p == "" || p == "[DONE]" {
			continue
		}
		payloads++
		for _, f := range rawDeltaFields([]byte(p)) {
			deltaFields[f]++
		}
		sc, err := parseSSEChunk([]byte(p))
		if err != nil {
			fmt.Fprintf(&b, "  payload %d: DECODE ERROR: %v\n", payloads, err)
			continue
		}
		if sc.Reasoning != "" {
			reasoningDeltas++
			reasoningBytes += len(sc.Reasoning)
		}
		if sc.Content == "" && sc.Reasoning == "" && len(sc.toolDeltas) == 0 &&
			sc.FinishReason == "" && sc.Usage.IsZero() {
			inertPayloads++
		}
		if sc.Content != "" {
			contentDeltas++
		}
		for _, d := range sc.toolDeltas {
			toolDeltas++
			idx := "absent"
			if d.Index != nil {
				idx = strconv.Itoa(*d.Index)
			}
			frag := argFragment(d.Function.Arguments)
			fmt.Fprintf(&b, "  payload %d: index=%s id=%q type=%q name=%q args=%q (%dB)\n",
				payloads, idx, d.ID, d.Type, d.Function.Name, frag, len(frag))
		}
		if sc.FinishReason != "" {
			fmt.Fprintf(&b, "  payload %d: finish_reason=%q\n", payloads, sc.FinishReason)
		}
	}
	fmt.Fprintf(&b, "  totals: payloads=%d content_deltas=%d tool_deltas=%d "+
		"reasoning_deltas=%d reasoning_bytes=%d inert_payloads=%d\n",
		payloads, contentDeltas, toolDeltas, reasoningDeltas, reasoningBytes, inertPayloads)
	names := make([]string, 0, len(deltaFields))
	for k := range deltaFields {
		names = append(names, fmt.Sprintf("%s=%d", k, deltaFields[k]))
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "  literal delta fields observed: %s\n", strings.Join(names, " "))
	return b.String()
}

// rawDeltaFields returns the literal JSON key names present in this
// payload's first choice `delta` object. Decoding into a map keeps the
// answer honest: a struct would report the names this code already knows
// about and say nothing about the ones it drops.
func rawDeltaFields(payload []byte) []string {
	var w struct {
		Choices []struct {
			Delta map[string]json.RawMessage `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &w); err != nil || len(w.Choices) == 0 {
		return nil
	}
	out := make([]string, 0, len(w.Choices[0].Delta))
	for k, v := range w.Choices[0].Delta {
		if len(v) == 0 || bytes.Equal(v, []byte("null")) || bytes.Equal(v, []byte(`""`)) {
			continue
		}
		out = append(out, k)
	}
	return out
}
