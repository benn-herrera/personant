package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/opencodehook"
	"personant/internal/scenarios"
	"personant/internal/turn"
)

const turnTimeout = 2 * time.Minute

// Executor drives each harness step's turn through one isolated opencode
// server whose plugin calls the in-process memory service. Setup is the
// scenario's Setup, Exec its TurnExecutor, and Finish runs after
// RunScenario returns.
type Executor struct {
	cadence    int
	dropSource string

	svc       *opencodehook.Service
	mock      *model.MockServer
	drv       *Driver
	sessionID string
	runHome   string
	steps     int
	fallbacks int64
	order     []opencodehook.OrderEntry
}

// NewExecutor returns an executor that checkpoints every cadence turns and
// has the service drop the first delta of dropSource ("" drops nothing).
func NewExecutor(cadence int, dropSource string) *Executor {
	return &Executor{cadence: cadence, dropSource: dropSource}
}

// Setup starts the mock model endpoint, the memory service and opencode,
// and opens the session every step prompts.
func (e *Executor) Setup(h *scenarios.Harness) error {
	e.runHome = filepath.Join(h.RunHome, "opencode")
	e.mock = model.NewMockServer(h.Mock)
	e.svc = opencodehook.NewService(func() *turn.State { return h.State }, e.cadence, e.dropSource)
	mockURL, err := serveLoopback(h, e.mock)
	if err != nil {
		return err
	}
	svcURL, err := serveLoopback(h, e.svc)
	if err != nil {
		return err
	}
	_, here, _, _ := runtime.Caller(0)
	plugin := filepath.Join(filepath.Dir(here), "..", "..", "..", "opencode-plugin")
	e.drv, err = Start(h.T, Config{RunHome: e.runHome, MockURL: mockURL + "/v1", PluginDir: plugin, ServiceURL: svcURL})
	if err != nil {
		return err
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := e.drv.Do(context.Background(), http.MethodPost, "/api/session", map[string]any{}, &created); err != nil {
		return err
	}
	if created.Data.ID == "" {
		return errors.New("opencode: session create returned no id")
	}
	e.sessionID = created.Data.ID
	return nil
}

// serveLoopback serves handler on an ephemeral 127.0.0.1 port until the
// test ends and returns its base URL.
func serveLoopback(h *scenarios.Harness, handler http.Handler) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }() // returns ErrServerClosed at Close
	h.T.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String(), nil
}

// Exec is the scenarios.TurnExecutor: it scripts one tool call per PreEvent
// ahead of the step's MockResponse, prompts opencode, waits for the
// service's close, and books the turn's probe metrics.
func (e *Executor) Exec(ctx context.Context, h *scenarios.Harness, step scenarios.Step) (string, turn.TurnInfo, error) {
	e.steps++
	var calls []model.ToolCall
	for i, d := range step.PreEvents {
		path := d.Meta["path"]
		var name string
		var args map[string]string
		switch d.Source {
		case memops.SourceFSRead:
			if err := writeWorkspaceFile(e.drv.Workspace, path, d.Content); err != nil {
				return "", turn.TurnInfo{}, err
			}
			name, args = "read", map[string]string{"path": path}
		case memops.SourceFSWrite:
			name, args = "write", map[string]string{"path": path, "content": d.Content}
		case memops.SourceFSCommit:
			name, args = "shell", map[string]string{"command": "git add " + path + " && git commit -q -m probe"}
		default:
			return "", turn.TurnInfo{}, fmt.Errorf("opencode executor: no tool for PreEvent source %q", d.Source)
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return "", turn.TurnInfo{}, err
		}
		calls = append(calls, model.ToolCall{ID: fmt.Sprintf("call_%d_%d", e.steps, i), Function: name, Args: raw})
	}
	if len(calls) > 0 {
		e.mock.Enqueue(model.Response{ToolCalls: calls})
	}

	wait, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	if err := e.drv.Do(wait, http.MethodPost, "/api/session/"+e.sessionID+"/prompt",
		map[string]string{"text": step.UserInput}, nil); err != nil {
		return "", turn.TurnInfo{}, err
	}
	rep, err := e.svc.WaitClose(wait)
	if err != nil {
		return "", turn.TurnInfo{}, err
	}
	reqs := e.mock.TakeRequests()
	e.book(h, e.svc.TakeStats())
	h.T.Logf("opencode turn %d: %d model requests at the mock, %d session.context calls, %d idles",
		rep.Turn, len(reqs), len(rep.ComposedHashes), rep.Idles)

	h.Metrics.Counter(scenarios.MetricOCMissedDeltas, int64(missedDeltas(step, rep, reqs)))
	h.Metrics.Counter(scenarios.MetricOCUncomposedRequests, int64(uncomposedRequests(rep, reqs)))
	for _, n := range rep.HistoryMessages {
		h.Metrics.Record(scenarios.MetricOCHistoryMessages, float64(n))
	}
	h.Metrics.Record(scenarios.MetricOCTurnMessages, float64(rep.TurnMessages))
	return rep.Body, rep.Info, rep.Err
}

// book records the service's cross-turn accumulators.
func (e *Executor) book(h *scenarios.Harness, st opencodehook.Stats) {
	for _, r := range st.RTTs {
		h.Metrics.Record(scenarios.MetricOCHookRTTMs, r.Ms)
	}
	h.Metrics.Counter(scenarios.MetricCheckpointsPerformed, int64(st.Checkpoints))
	h.Metrics.Counter(scenarios.MetricOCHookFallbacks, st.Fallbacks-e.fallbacks)
	e.fallbacks = st.Fallbacks
	e.order = append(e.order, st.Order...)
}

// Finish stops opencode, books what the service received after the last
// close, sets the user-DB guard gauge, writes the captured context sample
// and the observed hook/event order beside opencode's run home, and
// rewrites the metrics blob RunScenario already wrote.
func (e *Executor) Finish(h *scenarios.Harness) error {
	if err := e.drv.Stop(); err != nil {
		return err
	}
	e.book(h, e.svc.TakeStats())
	touched, err := e.drv.UserDBTouched()
	if err != nil {
		return err
	}
	gauge := 0.0
	if touched {
		gauge = 1
	}
	h.Metrics.Set(scenarios.MetricOCUserDBTouched, gauge)
	if err := os.WriteFile(filepath.Join(e.runHome, "context-sample.json"), e.svc.Sample(), 0o644); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(e.runHome, "oc_order.jsonl"))
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, o := range e.order {
		if err := enc.Encode(o); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return h.Metrics.WriteJSON(h.MetricsPath)
}

// missedDeltas counts the step's expected deltas absent from the service's
// applied ledger, plus one if the turn's usage differs from the last served
// request's. user.prompt and model.response match on content hash, so a
// content mismatch counts as absent; fs.* match on (source, path).
func missedDeltas(step scenarios.Step, rep opencodehook.TurnReport, reqs []model.ServedRequest) int {
	expected := []opencodehook.AppliedDelta{{Source: memops.SourceUserPrompt, SHA: model.SystemPromptHash(step.UserInput)}}
	for _, d := range step.PreEvents {
		expected = append(expected, opencodehook.AppliedDelta{Source: d.Source, Path: d.Meta["path"]})
	}
	expected = append(expected, opencodehook.AppliedDelta{
		Source: memops.SourceModelResponse, SHA: model.SystemPromptHash(step.MockResponse.Content)})

	applied := append([]opencodehook.AppliedDelta(nil), rep.Applied...)
	missed := 0
	for _, want := range expected {
		i := 0
		for i < len(applied) && !deltaMatches(want, applied[i]) {
			i++
		}
		if i == len(applied) {
			missed++
			continue
		}
		applied = append(applied[:i], applied[i+1:]...)
	}
	if len(reqs) == 0 {
		return missed + 1
	}
	last := reqs[len(reqs)-1].Usage
	if rep.Usage.PromptTokens != last.PromptTokens || rep.Usage.CompletionTokens != last.CompletionTokens {
		missed++
	}
	return missed
}

func deltaMatches(want, got opencodehook.AppliedDelta) bool {
	if want.Source != got.Source {
		return false
	}
	if want.Path != "" {
		return want.Path == got.Path
	}
	return want.SHA == got.SHA
}

// uncomposedRequests counts model calls whose system message is missing,
// repeated, or not one the service composed this turn.
func uncomposedRequests(rep opencodehook.TurnReport, reqs []model.ServedRequest) int {
	n := 0
	for _, r := range reqs {
		composed := false
		for _, h := range rep.ComposedHashes {
			composed = composed || h == r.SystemHash
		}
		if r.SystemMessages != 1 || !composed {
			n++
		}
	}
	return n
}

func writeWorkspaceFile(ws, rel, content string) error {
	p := filepath.Join(ws, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}
