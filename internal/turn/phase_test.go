package turn

import (
	"bytes"
	"context"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
)

// phaseProbe records the phase stream and the phase that was current when
// the first response byte reached the caller's writer.
type phaseProbe struct {
	body        bytes.Buffer
	seen        []Phase
	atFirstByte Phase
}

func (p *phaseProbe) Write(b []byte) (int, error) {
	if p.atFirstByte == "" && len(b) > 0 && len(p.seen) > 0 {
		p.atFirstByte = p.seen[len(p.seen)-1]
	}
	return p.body.Write(b)
}

func (p *phaseProbe) index(want Phase) int {
	for i, ph := range p.seen {
		if ph == want {
			return i
		}
	}
	return -1
}

// The front end's progress indicator depends on three things being true:
// a phase is announced before the pre-token wait, the model round-trip is
// the phase in effect when the first token lands, and the post-stream
// close window is announced too (with §3.4 recall inside it). Nothing in
// the pipeline branches on phases, so this asserts the UX contract only.
func TestRunAnnouncesPhasesAroundTheStream(t *testing.T) {
	paths, meta := newTestHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	var probe phaseProbe
	state.OnPhase = func(p Phase) { probe.seen = append(probe.seen, p) }

	if _, err := Run(context.Background(), state, "test prompt", &probe); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(probe.seen) == 0 {
		t.Fatal("no phases announced")
	}
	if probe.seen[0] != PhaseComposing {
		t.Errorf("first phase = %q, want %q", probe.seen[0], PhaseComposing)
	}
	if probe.atFirstByte != PhaseWaiting {
		t.Errorf("phase at the first response byte = %q, want %q", probe.atFirstByte, PhaseWaiting)
	}
	closing, recall := probe.index(PhaseClosing), probe.index(PhaseRecall)
	if closing < 0 {
		t.Fatalf("close window unannounced; phases: %v", probe.seen)
	}
	if recall < closing {
		t.Errorf("§3.4 recall must be announced inside the close window; phases: %v", probe.seen)
	}
	if i := probe.index(PhaseReissuing); i >= 0 {
		t.Errorf("clean turn announced a re-issue at %d; phases: %v", i, probe.seen)
	}
}

// The hook is optional. NewState leaves it nil, and a nil hook must not
// be reachable as a panic from any emit site.
func TestRunWithoutPhaseHook(t *testing.T) {
	paths, meta := newTestHome(t)
	mock := model.NewScriptedMock([]model.Response{
		{Content: "*topic: *new-topic* [foo, bar, baz, qux]*\nHello world."},
	}, nil)
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, mock)
	if state.OnPhase != nil {
		t.Fatal("NewState must leave OnPhase nil (progress reporting disabled)")
	}
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	var out bytes.Buffer
	if _, err := Run(context.Background(), state, "test prompt", &out); err != nil {
		t.Fatalf("Run with nil OnPhase: %v", err)
	}
}
