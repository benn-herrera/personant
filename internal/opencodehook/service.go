// Package opencodehook is the probe's memory service: loopback HTTP
// handlers the opencode plugin calls, each driving one internal/turn phase
// for the single in-flight turn.
package opencodehook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/turn"
)

// AppliedDelta is one delta the service handed to a phase. Path is set for
// fs.* sources; SHA is model.SystemPromptHash (SHA-256 hex) of the content.
type AppliedDelta struct {
	Source string
	Path   string
	SHA    string
}

// TurnReport is what the service knows about one turn when it closes.
type TurnReport struct {
	Turn            int
	Body            string
	Info            turn.TurnInfo
	Err             error
	Applied         []AppliedDelta
	ComposedHashes  []string // SystemPromptHash of each composed system text
	HistoryMessages []int    // hook-input message count per session.context call
	TurnMessages    int      // the turn's suffix length at its last session.context call
	Response        string
	Usage           model.Usage
	Idles           int
}

// RTT is one plugin-measured RPC round trip, booked to the turn the plugin
// had when the RPC returned.
type RTT struct {
	Turn int
	Hook string
	Ms   float64
}

// OrderEntry is one hook call or event as the plugin observed it: Seq is
// the plugin's own sequence number, Turn the service's turn at receipt and
// Open whether that turn was still in flight.
type OrderEntry struct {
	Seq  int64
	Name string
	Turn int
	Open bool
}

// Stats is what the service accumulated across turns since the last
// TakeStats. Fallbacks is the plugin's cumulative count as last reported.
type Stats struct {
	RTTs        []RTT
	Checkpoints int
	Fallbacks   int64
	Order       []OrderEntry
}

// Service is an http.Handler. Every handler holds one mutex, so phase calls
// are serialized.
type Service struct {
	state      func() *turn.State
	cadence    int
	dropSource string

	closed chan TurnReport

	mu      sync.Mutex
	cur     *turn.Turn
	rep     TurnReport
	texts   []string
	dropped bool
	turnNum int
	stats   Stats
	sample  json.RawMessage
}

// NewService returns a service whose phases run against state() at each
// turn's start. A turn whose number is a multiple of cadence is
// checkpointed. dropSource, when non-empty, names a source whose first delta
// the service skips, to prove the missed-delta detector fires.
func NewService(state func() *turn.State, cadence int, dropSource string) *Service {
	return &Service{state: state, cadence: cadence, dropSource: dropSource, closed: make(chan TurnReport, 1)}
}

// WaitClose blocks until the in-flight turn closes or aborts.
func (s *Service) WaitClose(ctx context.Context) (TurnReport, error) {
	select {
	case r := <-s.closed:
		return r, nil
	case <-ctx.Done():
		return TurnReport{}, fmt.Errorf("opencodehook: waiting for turn close: %w", ctx.Err())
	}
}

// TakeStats drains the cross-turn accumulators.
func (s *Service) TakeStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	s.stats = Stats{Fallbacks: out.Fallbacks}
	return out
}

// Sample returns the first session.context hook input, as the plugin sent it.
func (s *Service) Sample() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sample
}

// rpc is the union of every plugin request body; each handler reads its own
// fields.
type rpc struct {
	Seq       int64  `json:"seq"`
	Name      string `json:"name"`
	RTTs      []RTT  `json:"rtts"`
	Fallbacks int64  `json:"fallbacks"`

	Text string `json:"text"` // prompt

	Messages     int             `json:"messages"` // context
	TurnMessages int             `json:"turnMessages"`
	Sample       json.RawMessage `json:"sample"`

	Tool    string          `json:"tool"` // tool
	Status  string          `json:"status"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`

	Data json.RawMessage `json:"data"` // event
}

type historyMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type reply struct {
	Turn       int              `json:"turn"`
	System     string           `json:"system,omitempty"`
	History    []historyMessage `json:"history,omitempty"`
	Checkpoint bool             `json:"checkpoint,omitempty"`
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req rpc
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := context.Background() // a plugin timeout must not cancel a phase midway
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.RTTs = append(s.stats.RTTs, req.RTTs...)
	s.stats.Fallbacks = max(s.stats.Fallbacks, req.Fallbacks)
	s.stats.Order = append(s.stats.Order, OrderEntry{Seq: req.Seq, Name: req.Name, Turn: s.turnNum, Open: s.cur != nil})

	var out reply
	var err error
	switch r.URL.Path {
	case "/prompt":
		err = s.prompt(ctx, req)
	case "/context":
		out, err = s.compose(ctx, req)
	case "/tool":
		err = s.tool(ctx, req)
	case "/idle":
		s.rep.Idles++
		out.Checkpoint = s.cur != nil && s.rep.Idles == 1 && s.cadence > 0 && s.turnNum%s.cadence == 0
	case "/compaction":
		s.stats.Checkpoints++
	case "/event":
		err = s.event(ctx, req)
	case "/mark":
	default:
		err = fmt.Errorf("unknown path %s", r.URL.Path)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	out.Turn = s.turnNum
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out) // the client sees a short body as a failed RPC
}

func (s *Service) prompt(ctx context.Context, req rpc) error {
	if s.cur != nil {
		return fmt.Errorf("prompt while turn %d is open", s.turnNum)
	}
	state := s.state()
	t, err := turn.BeginTurn(ctx, state, nil, req.Text)
	if err != nil {
		turn.AbortTurn(ctx, t)
		s.finish(TurnReport{Err: err})
		return err
	}
	s.cur, s.turnNum = t, state.TurnNumber
	s.rep = TurnReport{Turn: s.turnNum, Applied: []AppliedDelta{{Source: memops.SourceUserPrompt, SHA: model.SystemPromptHash(req.Text)}}}
	s.texts = nil
	return nil
}

func (s *Service) compose(ctx context.Context, req rpc) (reply, error) {
	if s.sample == nil && req.Sample != nil {
		s.sample = req.Sample
	}
	if s.cur == nil {
		return reply{}, errors.New("context with no open turn")
	}
	sp, tail, err := turn.ComposeRequest(ctx, s.cur)
	if err != nil {
		return reply{}, err
	}
	s.rep.ComposedHashes = append(s.rep.ComposedHashes, model.SystemPromptHash(sp))
	s.rep.HistoryMessages = append(s.rep.HistoryMessages, req.Messages)
	s.rep.TurnMessages = req.TurnMessages
	out := reply{System: sp}
	for _, m := range tail {
		if m.Content != "" { // opencode drops empty text parts (PP §5)
			out.History = append(out.History, historyMessage{Role: m.Role, Text: m.Content})
		}
	}
	return out, nil
}

var commitPathRE = regexp.MustCompile(`^git add (\S+) && git commit\b`)

func (s *Service) tool(ctx context.Context, req rpc) error {
	if s.cur == nil {
		return errors.New("tool result with no open turn")
	}
	if req.Status != "completed" {
		return nil
	}
	var in struct {
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(req.Input, &in); err != nil {
		return fmt.Errorf("tool %s input: %w", req.Tool, err)
	}
	d := turn.Delta{Content: toolText(req.Content)}
	switch req.Tool {
	case "read":
		d.Source, d.Meta = memops.SourceFSRead, map[string]string{"path": in.Path}
	case "write":
		d.Source, d.Meta = memops.SourceFSWrite, map[string]string{"path": in.Path}
	case "shell":
		m := commitPathRE.FindStringSubmatch(in.Command)
		if m == nil {
			return nil
		}
		d.Source, d.Meta = memops.SourceFSCommit, map[string]string{"path": m[1]}
	default:
		return nil
	}
	if s.drop(d.Source) {
		return nil
	}
	if err := turn.IngestToolResult(ctx, s.cur, []turn.Delta{d}); err != nil {
		s.abort(ctx, err)
		return err
	}
	s.rep.Applied = append(s.rep.Applied, AppliedDelta{Source: d.Source, Path: d.Meta["path"], SHA: model.SystemPromptHash(d.Content)})
	return nil
}

// toolText flattens a tool result's content: a string as is, or the text
// items joined with "\n" as opencode lowers them to the model (PP §5).
func toolText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &items) // anything else carries no text
	var texts []string
	for _, it := range items {
		if it.Type == "text" {
			texts = append(texts, it.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func (s *Service) event(ctx context.Context, req rpc) error {
	if s.cur == nil {
		return nil // between turns, or after a turn already closed
	}
	switch req.Name {
	case "session.text.ended":
		var d struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(req.Data, &d); err != nil {
			return err
		}
		s.texts = append(s.texts, d.Text)
	case "session.step.ended":
		var d struct {
			Tokens struct {
				Input  int `json:"input"`
				Output int `json:"output"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(req.Data, &d); err != nil {
			return err
		}
		s.rep.Usage = model.Usage{
			PromptTokens:     d.Tokens.Input,
			CompletionTokens: d.Tokens.Output,
			TotalTokens:      d.Tokens.Input + d.Tokens.Output,
		}
	case "session.execution.succeeded":
		return s.close(ctx)
	case "session.execution.failed", "session.execution.interrupted":
		s.abort(ctx, fmt.Errorf("opencode %s: %s", req.Name, req.Data))
	}
	return nil
}

func (s *Service) close(ctx context.Context) error {
	content := strings.Join(s.texts, "")
	s.rep.Response = content
	if !s.drop(memops.SourceModelResponse) {
		if err := turn.IngestModelResponse(ctx, s.cur, content, s.rep.Usage); err != nil {
			s.abort(ctx, err)
			return err
		}
		s.rep.Applied = append(s.rep.Applied, AppliedDelta{Source: memops.SourceModelResponse, SHA: model.SystemPromptHash(content)})
	}
	body, info, err := turn.CloseTurn(ctx, s.cur)
	s.rep.Body, s.rep.Info, s.rep.Err = body, info, err
	s.cur = nil
	s.finish(s.rep)
	return err
}

func (s *Service) abort(ctx context.Context, err error) {
	turn.AbortTurn(ctx, s.cur)
	s.rep.Err = err
	s.cur = nil
	s.finish(s.rep)
}

// finish hands the report to WaitClose; a report nobody is waiting for is
// replaced, never blocks a handler.
func (s *Service) finish(r TurnReport) {
	select {
	case <-s.closed:
	default:
	}
	s.closed <- r
}

// drop reports whether this delta is the first of dropSource, and marks it.
func (s *Service) drop(source string) bool {
	if s.dropped || source != s.dropSource {
		return false
	}
	s.dropped = true
	return true
}
