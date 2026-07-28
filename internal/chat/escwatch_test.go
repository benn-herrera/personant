package chat

import (
	"io"
	"sync"
	"testing"
	"time"
)

// TestEscScanner is the disambiguation table. Every case is a sequence of
// terminal reads; an EMPTY chunk is the VMIN=0/VTIME=1 idle tick (the read
// window expired with nothing typed), which is the only thing that can
// resolve a pending ESC. wantAbortAt is the index of the chunk that must
// complete a bare Esc, or -1 for "never aborts".
func TestEscScanner(t *testing.T) {
	esc := byte(escByte)
	tests := []struct {
		name        string
		chunks      [][]byte
		wantAbortAt int
	}{
		{"bare esc then idle", [][]byte{{esc}, {}}, 1},
		{"arrow up in one read", [][]byte{{esc, '[', 'A'}, {}}, -1},
		{"arrow up split across reads", [][]byte{{esc}, {'[', 'A'}, {}}, -1},
		{"ss3 function key", [][]byte{{esc, 'O', 'P'}, {}}, -1},
		{"parameterised csi", [][]byte{{esc, '[', '1', ';', '5', 'A'}, {}}, -1},
		{"esc then a slow byte is alt-x", [][]byte{{esc}, {'x'}, {}}, -1},
		{"double esc then idle", [][]byte{{esc, esc}, {}}, 1},
		{"ordinary typing is discarded", [][]byte{{'h', 'i'}, {}}, -1},
		{"esc at eof never resolves", [][]byte{{esc}}, -1},
		{"truncated sequence then a real bare esc", [][]byte{{esc, '['}, {}, {esc}, {}}, 3},
		{"esc interrupting a sequence becomes pending", [][]byte{{esc, '[', esc}, {}}, 1},
		{"sequence then bare esc in one read", [][]byte{{esc, '[', 'A', esc}, {}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sc escScanner
			got := -1
			for i, chunk := range tt.chunks {
				if sc.feed(chunk) {
					got = i
					break
				}
			}
			if got != tt.wantAbortAt {
				t.Errorf("abort at chunk %d, want %d", got, tt.wantAbortAt)
			}
		})
	}
}

// scriptedReads replays a fixed sequence of read results and then idles
// the way a real cbreak read does: a short, bounded (0, nil) that lets the
// watcher notice a stop request. The sleep stands in for the VTIME window.
type scriptedReads struct {
	mu     sync.Mutex
	script [][]byte
	err    error // returned once the script is exhausted, instead of idling
}

func (s *scriptedReads) read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.script) > 0 {
		chunk := s.script[0]
		s.script = s.script[1:]
		return copy(p, chunk), nil
	}
	if s.err != nil {
		return 0, s.err
	}
	time.Sleep(time.Millisecond)
	return 0, nil
}

func TestEscWatcher_StopTerminatesGoroutine(t *testing.T) {
	r := &scriptedReads{}
	fired := make(chan struct{}, 1)
	w := startEscWatcher(r.read, func() { fired <- struct{}{} })

	done := make(chan struct{})
	go func() { w.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not join the watcher goroutine")
	}
	if len(fired) != 0 {
		t.Error("watcher fired an abort with no Esc typed")
	}
	w.close() // idempotent, and must not block a second time
}

func TestEscWatcher_ReadErrorRetiresGoroutine(t *testing.T) {
	r := &scriptedReads{err: io.EOF}
	w := startEscWatcher(r.read, func() { t.Error("abort fired on a dead stdin") })
	// The goroutine retires itself; close must still return promptly.
	done := make(chan struct{})
	go func() { w.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close hung after the read returned an error")
	}
}

func TestEscWatcher_FiresOnceThenHandsBackStdin(t *testing.T) {
	var calls int
	r := &scriptedReads{script: [][]byte{{escByte}}}
	fired := make(chan struct{})
	w := startEscWatcher(r.read, func() { calls++; close(fired) })
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("bare Esc did not fire an abort")
	}
	// Firing retires the watcher, so close is a plain join — if the
	// goroutine were still reading, this would time out.
	done := make(chan struct{})
	go func() { w.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher kept reading stdin after firing")
	}
	if calls != 1 {
		t.Errorf("abort callback fired %d times, want 1", calls)
	}
}
