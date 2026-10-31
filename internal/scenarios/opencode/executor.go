package opencode

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/opencodehook"
	"personant/internal/scenarios"
	"personant/internal/turn"
)

const turnTimeout = 2 * time.Minute

// TraceFilename is the per-turn trace Finish writes in the harness run home.
const TraceFilename = "oc_trace.jsonl"

// TraceExact is the part of a trace line two same-seed runs must match
// exactly. Every list is in plugin-sequence order except Applied and Tools,
// which opencode produces concurrently within a response and so are sorted.
type TraceExact struct {
	SystemHashes []string                  `json:"system_hashes"`
	Contexts     []TraceContext            `json:"contexts"`
	Applied      []TraceDelta              `json:"applied"` // sorted by (source, path)
	Tools        []opencodehook.ToolSeen   `json:"tools"`   // sorted by (tool, path)
	Events       []string                  `json:"events"`
	Tokens       []opencodehook.StepTokens `json:"tokens"`
}

// TraceContext is one session.context call's hook-input measures.
type TraceContext struct {
	Messages     int `json:"messages"`
	TurnMessages int `json:"turn_messages"`
	Bytes        int `json:"bytes"`
}

// TraceDelta is one applied delta: its source and the SHA-256 of its content.
type TraceDelta struct {
	Source string `json:"source"`
	SHA    string `json:"sha"`
}

// TraceLine is one turn of oc_trace.jsonl: the exact fields plus timing.
// RTTMs holds the turn's booked RTTs by hook name.
type TraceLine struct {
	Exact      TraceExact           `json:"exact"`
	OverheadMs float64              `json:"overhead_ms"`
	RTTMs      map[string][]float64 `json:"rtt_ms"`
}

// turnRecord is what Exec knows of a turn; RTTs and event order complete it
// only at Finish, because the plugin reports them with later RPCs.
type turnRecord struct {
	ord      int
	wallMs   float64
	servedMs float64
	exact    TraceExact
}

// Executor drives each harness step's turn through one isolated opencode
// server whose plugin calls the in-process memory service. Setup is the
// scenario's Setup, Exec its TurnExecutor, and Finish runs after
// RunScenario returns.
type Executor struct {
	cadence    int
	dropSource string

	// service and wrap are set only by the resume probe: run opencode as a
	// managed server, and wrap the memory service's handler (nil: as is).
	service bool
	wrap    func(http.Handler) http.Handler

	svc       *opencodehook.Service
	mock      *model.MockServer
	mockURL   string
	svcURL    string
	drv       *Driver
	sessionID string
	runHome   string
	steps     int
	fallbacks int64
	order     []opencodehook.OrderEntry
	rtts      map[int][]opencodehook.RTT
	turns     []turnRecord
	day       int
	loopStart clock.ProfilingTime
	loopWall  time.Duration
}

// NewExecutor returns an executor that checkpoints every cadence turns and
// has the service drop the first delta of dropSource ("" drops nothing).
func NewExecutor(cadence int, dropSource string) *Executor {
	return &Executor{cadence: cadence, dropSource: dropSource, rtts: map[int][]opencodehook.RTT{}}
}

// Setup starts the mock model endpoint, the memory service and opencode,
// and opens the session every step prompts.
func (e *Executor) Setup(h *scenarios.Harness) error {
	if e.runHome == "" {
		e.runHome = filepath.Join(h.RunHome, "opencode")
	}
	e.mock = model.NewMockServer(h.Mock)
	mockURL, err := serveLoopback(h, e.mock)
	if err != nil {
		return err
	}
	e.mockURL = mockURL + "/v1"
	if err := e.startService(h); err != nil {
		return err
	}
	if err := e.startOpencode(h); err != nil {
		return err
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	body := map[string]any{}
	if e.service { // a managed server's cwd is its home, not the workspace
		body["location"] = map[string]string{"directory": e.drv.Workspace}
	}
	if err := e.drv.Do(context.Background(), http.MethodPost, "/api/session", body, &created); err != nil {
		return err
	}
	if created.Data.ID == "" {
		return errors.New("opencode: session create returned no id")
	}
	e.sessionID = created.Data.ID
	return nil
}

// startService serves a fresh memory service over h.State on a new port.
func (e *Executor) startService(h *scenarios.Harness) error {
	e.svc = opencodehook.NewService(func() *turn.State { return h.State }, e.cadence, e.dropSource)
	var handler http.Handler = e.svc
	if e.wrap != nil {
		handler = e.wrap(handler)
	}
	var err error
	e.svcURL, err = serveLoopback(h, handler)
	return err
}

// startOpencode starts opencode on the run home, its plugin pointed at the
// current memory service.
func (e *Executor) startOpencode(h *scenarios.Harness) error {
	_, here, _, _ := runtime.Caller(0)
	plugin := filepath.Join(filepath.Dir(here), "..", "..", "..", "opencode-plugin")
	var err error
	e.drv, err = Start(h.T, Config{RunHome: e.runHome, MockURL: e.mockURL, PluginDir: plugin,
		ServiceURL: e.svcURL, Service: e.service})
	return err
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
	day := scenarios.SimDayIndex(h.PinnedClock())
	if e.steps == 0 {
		e.loopStart = clock.Profiling()
	} else if day != e.day {
		if err := e.sampleDay(h, day); err != nil {
			return "", turn.TurnInfo{}, err
		}
	}
	e.day = day
	e.steps++

	// Commit dates follow the sim clock so commit hashes repeat across runs.
	gitDate := fmt.Sprintf("'%d +0000'", h.PinnedClock().Unix())
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
			name, args = "shell", map[string]string{"command": "git add " + path +
				" && GIT_AUTHOR_DATE=" + gitDate + " GIT_COMMITTER_DATE=" + gitDate +
				" git commit -q -m probe && git rev-parse HEAD"}
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
	start := clock.Profiling()
	if err := e.drv.Do(wait, http.MethodPost, "/api/session/"+e.sessionID+"/prompt",
		map[string]string{"text": step.UserInput}, nil); err != nil {
		return "", turn.TurnInfo{}, err
	}
	rep, err := e.svc.WaitClose(wait)
	if err != nil {
		return "", turn.TurnInfo{}, err
	}
	wall := clock.Since(start)
	e.loopWall = clock.Since(e.loopStart)
	reqs := e.mock.TakeRequests()
	e.book(h, e.svc.TakeStats())
	slices.SortFunc(rep.Contexts, func(a, b opencodehook.ContextCall) int { return cmp.Compare(a.Seq, b.Seq) })

	h.Metrics.Counter(scenarios.MetricOCMissedDeltas, int64(missedDeltas(step, rep, reqs)))
	h.Metrics.Counter(scenarios.MetricOCUncomposedRequests, int64(uncomposedRequests(rep, reqs)))
	for _, c := range rep.Contexts {
		h.Metrics.Record(scenarios.MetricOCHistoryMessages, float64(c.Messages))
	}
	if n := len(rep.Contexts); n > 0 {
		h.Metrics.Record(scenarios.MetricOCTurnMessages, float64(rep.Contexts[n-1].TurnMessages))
	}
	var served time.Duration
	for _, r := range reqs {
		served += r.Duration
	}
	e.turns = append(e.turns, turnRecord{ord: rep.Ord, wallMs: ms(wall), servedMs: ms(served), exact: traceExact(rep)})
	return rep.Body, rep.Info, rep.Err
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// traceExact builds a turn's exact trace fields, less the event order.
func traceExact(rep opencodehook.TurnReport) TraceExact {
	var x TraceExact
	for _, c := range rep.Contexts {
		x.SystemHashes = append(x.SystemHashes, c.Hash)
		x.Contexts = append(x.Contexts, TraceContext{Messages: c.Messages, TurnMessages: c.TurnMessages, Bytes: c.Bytes})
	}
	applied := slices.Clone(rep.Applied)
	slices.SortFunc(applied, func(a, b opencodehook.AppliedDelta) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Path, b.Path), cmp.Compare(a.SHA, b.SHA))
	})
	for _, a := range applied {
		x.Applied = append(x.Applied, TraceDelta{Source: a.Source, SHA: a.SHA})
	}
	x.Tools = slices.Clone(rep.Tools)
	slices.SortFunc(x.Tools, func(a, b opencodehook.ToolSeen) int {
		return cmp.Or(cmp.Compare(a.Tool, b.Tool), cmp.Compare(a.Path, b.Path))
	})
	x.Tokens = rep.Steps
	return x
}

// book records the service's cross-turn accumulators.
func (e *Executor) book(h *scenarios.Harness, st opencodehook.Stats) {
	for _, r := range st.RTTs {
		h.Metrics.Record(scenarios.MetricOCHookRTTMs, r.Ms)
		e.rtts[r.Turn] = append(e.rtts[r.Turn], r)
	}
	h.Metrics.Counter(scenarios.MetricCheckpointsPerformed, int64(st.Checkpoints))
	h.Metrics.Counter(scenarios.MetricOCHookFallbacks, st.Fallbacks-e.fallbacks)
	e.fallbacks = st.Fallbacks
	e.order = append(e.order, st.Order...)
}

// sampleDay sets the isolated DB size and opencode's RSS gauges.
func (e *Executor) sampleDay(h *scenarios.Harness, day int) error {
	db := filepath.Join(e.runHome, "home", "data", "opencode", "opencode.db")
	var size int64
	for _, p := range []string{db, db + "-wal"} {
		fi, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		size += fi.Size()
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(e.drv.PID)).Output()
	if err != nil {
		return fmt.Errorf("ps rss of opencode pid %d: %w", e.drv.PID, err)
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return fmt.Errorf("ps rss %q: %w", out, err)
	}
	h.Metrics.Set(scenarios.MetricOCDBBytes, float64(size))
	h.Metrics.Set(scenarios.MetricOCOpencodeRSSBytes, float64(kb*1024))
	h.T.Logf("opencode sim-day %d after %d turns: %s=%d %s=%d", day, e.steps,
		scenarios.MetricOCDBBytes, size, scenarios.MetricOCOpencodeRSSBytes, kb*1024)
	return nil
}

// Finish samples the run-end gauges, stops opencode, books what the service
// received after the last close, sets the user-DB guard gauge, writes the
// captured context sample and the observed hook/event order beside
// opencode's run home and oc_trace.jsonl in the harness run home, and
// rewrites the metrics blob RunScenario already wrote.
func (e *Executor) Finish(h *scenarios.Harness) error {
	if err := e.sampleDay(h, scenarios.SimDayIndex(h.PinnedClock())); err != nil {
		return err
	}
	if e.loopWall > 0 {
		h.Metrics.Set(scenarios.MetricOCTurnsPerSec, float64(e.steps)/e.loopWall.Seconds())
	}
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
	slices.SortFunc(e.order, func(a, b opencodehook.OrderEntry) int { return cmp.Compare(a.Seq, b.Seq) })
	if err := writeJSONL(filepath.Join(e.runHome, "oc_order.jsonl"), e.order); err != nil {
		return err
	}

	// Each prompt hook opens a turn; everything up to the next belongs to it.
	var events [][]string
	for _, o := range e.order {
		if o.Name == "hook.session.prompt" {
			events = append(events, nil)
		}
		if len(events) > 0 {
			events[len(events)-1] = append(events[len(events)-1], o.Name)
		}
	}
	if len(events) != len(e.turns) {
		h.T.Errorf("opencode trace: %d prompt hooks seen for %d turns", len(events), len(e.turns))
	}
	lines := make([]TraceLine, len(e.turns))
	for i, tr := range e.turns {
		l := TraceLine{Exact: tr.exact, RTTMs: map[string][]float64{}}
		if i < len(events) {
			l.Exact.Events = events[i]
		}
		hooks := 0.0
		for _, r := range e.rtts[tr.ord] {
			l.RTTMs[r.Hook] = append(l.RTTMs[r.Hook], r.Ms)
			hooks += r.Ms
		}
		l.OverheadMs = tr.wallMs - tr.servedMs - hooks
		h.Metrics.Record(scenarios.MetricOCStepOverheadMs, l.OverheadMs)
		lines[i] = l
	}
	if err := writeJSONL(filepath.Join(h.RunHome, TraceFilename), lines); err != nil {
		return err
	}
	return h.Metrics.WriteJSON(h.MetricsPath)
}

func writeJSONL[T any](path string, recs []T) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
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
		for _, c := range rep.Contexts {
			composed = composed || c.Hash == r.SystemHash
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
