package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/scenarios"
	"personant/internal/turn"
)

// breakRPC names, per break point, the plugin RPC the dying memory service
// never answers; "stop" is a clean stop between turns instead.
var breakRPC = map[string]string{
	"stop":      "",
	"idle":      "hook.session.experimental.idle", // response stored, execution still claimed
	"succeeded": "session.execution.succeeded",    // opencode has released the claim
	"tool":      "hook.tool.execute.after",        // first tool done, a second still running
}

const breakStep = 2 // 0-based; the next step restarts

// TestProbeResume stops or kills opencode and the memory service on the third
// turn, restarts the service from its on-disk state and opencode on the same
// data home, and continues the same session. Each subtest is one opencode
// mode (stdio: the standalone server; service: the managed server the default
// TUI uses) and one break point.
func TestProbeResume(t *testing.T) {
	if os.Getenv("PERSONANT_OPENCODE_PROBE") != "1" {
		t.Skip("set PERSONANT_OPENCODE_PROBE=1 (just probe-resume) to run against the installed opencode")
	}
	for _, mode := range []string{"stdio", "service"} {
		for _, point := range []string{"stop", "idle", "succeeded", "tool"} {
			t.Run(mode+"-"+point, func(t *testing.T) { runResume(t, mode == "service", point) })
		}
	}
}

func runResume(t *testing.T, service bool, point string) {
	const path = "internal/probe/resume.go"
	read := "package probe\n\n// resume fixture #probe-resume\n"
	written := read + "\n// edit 1: probe resume — #probe-resume\n"
	fsRead := turn.Delta{Source: memops.SourceFSRead, Content: written, Meta: map[string]string{"path": path}}
	respond := func(anchor, body string) model.Response {
		return scenarios.NewMockResponseWithTag([]string{"*new-topic*"}, []string{"probe-resume", anchor}, body)
	}
	steps := []scenarios.Step{
		{UserInput: "Edit the resume fixture. #probe-resume", PreEvents: []turn.Delta{
			{Source: memops.SourceFSRead, Content: read, Meta: map[string]string{"path": path}},
			{Source: memops.SourceFSWrite, Content: written, Meta: map[string]string{"path": path}},
			{Source: memops.SourceFSCommit, Meta: map[string]string{"path": path}},
		}, MockResponse: respond("edit", "Edited the fixture.")},
		{UserInput: "What does the fixture say? #probe-resume", MockResponse: respond("explain", "It names the probe.")},
		{UserInput: "Check the fixture once more. #probe-resume", MockResponse: respond("break", "Checked; unchanged.")},
		{UserInput: "After the restart: read the fixture. #probe-resume", RestartSession: true,
			PreEvents: []turn.Delta{fsRead}, MockResponse: respond("after", "Read it after the restart.")},
		{UserInput: "One more turn after the restart. #probe-resume", MockResponse: respond("later", "Still here.")},
	}

	ex := NewExecutor(2, "")
	ex.service = service
	r := &resumeRun{ex: ex, point: point}
	if name := breakRPC[point]; name != "" {
		r.gate = &breakGate{name: name, hit: make(chan struct{})}
		ex.wrap = r.gate.wrap
	}
	h := scenarios.RunScenario(t, scenarios.Scenario{
		Name: "probe-resume-" + strings.TrimPrefix(t.Name(), "TestProbeResume/"),
		// Outside the memory home: its crash recovery quarantines untracked
		// files there, opencode's data home included.
		Setup: func(h *scenarios.Harness) error {
			ex.runHome = h.RunHome + ".opencode"
			if err := os.RemoveAll(ex.runHome); err != nil {
				return err
			}
			// A launch reconciles before its first turn (internal/chat/chat.go);
			// the harness does not, and recovery quarantines what it left untracked.
			if _, err := fileadapter.NewFileAdapter(h.Paths).Reconcile(context.Background()); err != nil {
				return err
			}
			return ex.Setup(h)
		},
		TurnExecutor: r.exec,
		Steps:        steps,
	})

	var transcript json.RawMessage
	if err := ex.drv.Do(context.Background(), http.MethodGet,
		"/api/experimental/session/"+ex.sessionID+"/export", nil, &transcript); err != nil {
		t.Errorf("export transcript: %v", err)
	} else if err := os.WriteFile(filepath.Join(ex.runHome, "transcript.json"), transcript, 0o644); err != nil {
		t.Errorf("write transcript: %v", err)
	}
	if err := ex.drv.Stop(); err != nil {
		t.Errorf("final stop: %v", err)
	}
	t.Logf("resume %s: final claim %s; transcript %s", point, r.claim(), filepath.Join(ex.runHome, "transcript.json"))
	if touched, err := ex.drv.UserDBTouched(); err != nil || touched {
		t.Errorf("user opencode DB touched=%v err=%v", touched, err)
	}

	m := h.Metrics
	after := map[string]int64{}
	for _, k := range []string{scenarios.MetricOCUncomposedRequests, scenarios.MetricOCMissedDeltas, scenarios.MetricOCHookFallbacks} {
		after[k] = m.CounterValue(k) - r.before[k]
	}
	t.Logf("resume %s: after restart: %d (uncomposed) requests before the next prompt; turn counters %v",
		point, r.resumeRequests, after)
	if n := int64(r.resumeRequests) + after[scenarios.MetricOCUncomposedRequests]; n != 0 {
		t.Errorf("full control lost: %d uncomposed model requests after the restart", n)
	}
	if after[scenarios.MetricOCMissedDeltas] != 0 {
		t.Errorf("%s = %d after the restart, want 0", scenarios.MetricOCMissedDeltas, after[scenarios.MetricOCMissedDeltas])
	}
}

// resumeRun wraps Executor.Exec: it breaks the turn at breakStep and
// restarts both processes before the next.
type resumeRun struct {
	ex             *Executor
	point          string
	gate           *breakGate
	before         map[string]int64 // turn counters at the restart
	resumeRequests int
}

func (r *resumeRun) exec(ctx context.Context, h *scenarios.Harness, step scenarios.Step) (string, turn.TurnInfo, error) {
	i := r.ex.steps
	if i == breakStep {
		return r.breakTurn(ctx, h, step)
	}
	if i == breakStep+1 {
		if err := r.restart(h); err != nil {
			return "", turn.TurnInfo{}, err
		}
	}
	body, info, err := r.ex.Exec(ctx, h, step)
	h.T.Logf("resume %s: step %d: turn id %s, state.TurnNumber %d, len(state.History) %d, uncomposed %d, missed %d (cumulative), err %v",
		r.point, i+1, info.TurnID, h.State.TurnNumber, len(h.State.History), h.Metrics.CounterValue(scenarios.MetricOCUncomposedRequests),
		h.Metrics.CounterValue(scenarios.MetricOCMissedDeltas), err)
	return body, info, err
}

// breakTurn runs the turn to its break point, kills opencode there (or
// stops it after the turn), then reloads the memory service from disk.
func (r *resumeRun) breakTurn(ctx context.Context, h *scenarios.Harness, step scenarios.Step) (string, turn.TurnInfo, error) {
	ex := r.ex
	oracle := scenarios.SnapshotTurnOracle(h.T, h.Paths)
	if r.gate == nil {
		body, info, err := ex.Exec(ctx, h, step)
		if err != nil {
			return "", info, err
		}
		if err := ex.drv.Stop(); err != nil {
			return "", info, err
		}
		h.T.Logf("resume stop: turn %s closed; claim after stop %s", info.TurnID, r.claim())
		return body, info, r.reload(h, oracle)
	}
	if r.point == "tool" {
		if err := writeWorkspaceFile(ex.drv.Workspace, "internal/probe/resume.go", "package probe\n"); err != nil {
			return "", turn.TurnInfo{}, err
		}
		ex.mock.Enqueue(model.Response{ToolCalls: []model.ToolCall{
			{ID: "brk_0", Function: "read", Args: json.RawMessage(`{"path":"internal/probe/resume.go"}`)},
			{ID: "brk_1", Function: "shell", Args: json.RawMessage(`{"command":"sleep 20"}`)},
		}})
	}
	r.gate.armed.Store(true)
	wait, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-r.gate.hit:
		case <-wait.Done():
		}
		cancel()
	}()
	_, _, err := ex.Exec(wait, h, step)
	cancel()
	select {
	case <-r.gate.hit:
	default:
		return "", turn.TurnInfo{}, fmt.Errorf("break point %s not reached; turn ended with %v", r.gate.name, err)
	}
	if err := ex.drv.kill(); err != nil {
		return "", turn.TurnInfo{}, err
	}
	reqs := ex.mock.TakeRequests()
	h.T.Logf("resume %s: killed at %s after %d model requests; claim after kill %s",
		r.point, r.gate.name, len(reqs), r.claim())
	return "", turn.TurnInfo{}, r.reload(h, oracle)
}

// reload is the memory service's relaunch: a fresh adapter over the same
// home, Reconcile, then LoadSession; nothing in memory survives. The
// harness's own RestartSession on the next step reloads once more with its
// ack policy.
func (r *resumeRun) reload(h *scenarios.Harness, oracle scenarios.TurnOracle) error {
	ops := fileadapter.NewFileAdapter(h.Paths)
	rep, err := ops.Reconcile(context.Background())
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	h.T.Logf("resume %s: reconcile: cells %v reset %v reverted %v pending %v; spine %d before the turn",
		r.point, rep.CellsHit, rep.ResetPerformed, rep.RevertedPaths, rep.Pending != nil, oracle.SpineLen)
	scenarios.AssertPostRecovery(h.T, h.Paths, ops, &oracle)
	h.Ops = ops
	h.State, err = turn.LoadSession(context.Background(), ops, h.Project, h.Provider, h.Mock)
	return err
}

// restart starts a fresh memory service, then opencode on the same data
// home, and books what opencode does before the next prompt.
func (r *resumeRun) restart(h *scenarios.Harness) error {
	ex := r.ex
	r.before = map[string]int64{}
	for _, k := range []string{scenarios.MetricOCUncomposedRequests, scenarios.MetricOCMissedDeltas, scenarios.MetricOCHookFallbacks} {
		r.before[k] = h.Metrics.CounterValue(k)
	}
	ex.wrap, ex.fallbacks = nil, 0 // a new plugin counts from 0
	if err := ex.startService(h); err != nil {
		return err
	}
	if err := ex.startOpencode(h); err != nil {
		return err
	}
	resumed, err := r.awaitInactive()
	if err != nil {
		return err
	}
	reqs := ex.mock.TakeRequests()
	r.resumeRequests = len(reqs) // the service opened no turn, so composed none of them
	var names []string
	for _, o := range ex.svc.TakeStats().Order {
		names = append(names, o.Name)
	}
	h.T.Logf("resume %s: restarted; opencode ran the session on its own: %v; model requests %d %s; RPCs seen %v; claim %s",
		r.point, resumed, len(reqs), describeRequests(reqs), names, r.claim())
	return nil
}

// awaitInactive gives opencode 3 s to start draining the session on its own
// and, if it does, waits for the drain to end.
func (r *resumeRun) awaitInactive() (bool, error) {
	seen := false
	start := time.Now()
	for {
		var active struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := r.ex.drv.Do(context.Background(), http.MethodGet, "/api/session/active", nil, &active); err != nil {
			return seen, err
		}
		_, on := active.Data[r.ex.sessionID]
		seen = seen || on
		switch {
		case !on && (seen || time.Since(start) > 3*time.Second):
			return seen, nil
		case time.Since(start) > turnTimeout:
			return seen, fmt.Errorf("session still active %v after restart", turnTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func describeRequests(reqs []model.ServedRequest) string {
	var b strings.Builder
	for _, q := range reqs {
		fmt.Fprintf(&b, "[messages %d system %d hash %.12s]", q.Messages, q.SystemMessages, q.SystemHash)
	}
	return b.String()
}

// claim reads the session's execution claim (time_suspended) and resume
// counter from a copy of the run's own opencode DB and WAL.
func (r *resumeRun) claim() string {
	src := filepath.Join(r.ex.runHome, "home", "data", "opencode", "opencode.db")
	db := filepath.Join(r.ex.runHome, "claim-copy", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		return err.Error()
	}
	for _, suffix := range []string{"", "-wal"} {
		if err := copyFile(src+suffix, db+suffix); err != nil {
			return err.Error()
		}
	}
	out, err := exec.Command("sqlite3", "-readonly", db,
		"SELECT 'suspended='||coalesce(time_suspended,'null')||' resume_attempts='||resume_attempts FROM session_v2 WHERE id="+
			sqlQuote(r.ex.sessionID)+";").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("(sqlite3: %v: %s)", err, out)
	}
	return strings.TrimSpace(string(out))
}

// breakGate stands in for a memory service that dies mid-turn: once armed,
// the first RPC named name, and every RPC after it, is held unanswered until
// its client goes away.
type breakGate struct {
	name  string
	armed atomic.Bool
	once  sync.Once
	hit   chan struct{}
}

func (g *breakGate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-g.hit:
			<-req.Context().Done()
			return
		default:
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &body) // next decodes it again and rejects a bad body
		if g.armed.Load() && body.Name == g.name {
			g.once.Do(func() { close(g.hit) })
			<-req.Context().Done()
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		next.ServeHTTP(w, req)
	})
}
