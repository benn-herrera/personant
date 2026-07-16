package scenarios

import (
	"os"
	"path/filepath"
	"testing"

	"personant/internal/model"
)

// TestCaptureTagMissWritesRawBody drives stepCaptureTagMiss directly with a
// fake live client installed: a tag-miss turn (topic.tag-missing in the
// step's log lines) writes the raw body to
// test/rundata/<scenario>/tagmiss/<turn>.txt.
func TestCaptureTagMissWritesRawBody(t *testing.T) {
	h := &Harness{
		T:          t,
		RunHome:    t.TempDir(),
		liveClient: model.NewScriptedMock(nil, nil), // fake live client: non-nil gates the live-only path
	}
	const rawBody = "Sure — here's the answer.\n**topic:** thr_1 [trefoil]"
	lines := []string{"2026-07-15T08:00:00Z topic.tag-missing source=model.response bytes=52"}

	stepCaptureTagMiss(h, 6 /* idx → turn 7 */, rawBody, lines)

	path := filepath.Join(h.RunHome, "tagmiss", "7.txt")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected capture file %s: %v", path, err)
	}
	if string(got) != rawBody {
		t.Errorf("captured body = %q, want %q", string(got), rawBody)
	}
}

// TestCaptureTagMissTriggeredByTagInvalid confirms the additive
// topic.tag-invalid marker also triggers capture.
func TestCaptureTagMissTriggeredByTagInvalid(t *testing.T) {
	h := &Harness{
		T:          t,
		RunHome:    t.TempDir(),
		liveClient: model.NewScriptedMock(nil, nil),
	}
	lines := []string{"2026-07-15T08:00:00Z topic.tag-invalid source=model.response reason=markdown-mangled snippet=**topic:**"}

	stepCaptureTagMiss(h, 0, "raw", lines)

	if _, err := os.Stat(filepath.Join(h.RunHome, "tagmiss", "1.txt")); err != nil {
		t.Fatalf("tag-invalid should trigger capture: %v", err)
	}
}

// TestCaptureTagMissMockRungNoCapture pins the live-only gating: with no
// live client installed (a mock/symbolic rung), a tag-miss turn writes
// nothing.
func TestCaptureTagMissMockRungNoCapture(t *testing.T) {
	h := &Harness{
		T:       t,
		RunHome: t.TempDir(),
		// liveClient nil → mock rung
	}
	lines := []string{"2026-07-15T08:00:00Z topic.tag-missing source=model.response bytes=10"}

	stepCaptureTagMiss(h, 0, "raw", lines)

	if _, err := os.Stat(filepath.Join(h.RunHome, "tagmiss")); !os.IsNotExist(err) {
		t.Errorf("mock rung must not create the tagmiss dir (err=%v)", err)
	}
}

// TestCaptureTagMissCleanTurnNoCapture confirms a turn that bound a valid
// tag (no tag-missing/tag-invalid marker) captures nothing even on a live
// rung.
func TestCaptureTagMissCleanTurnNoCapture(t *testing.T) {
	h := &Harness{
		T:          t,
		RunHome:    t.TempDir(),
		liveClient: model.NewScriptedMock(nil, nil),
	}
	lines := []string{"2026-07-15T08:00:00Z thread.engaged thr_1 turn_count=3"}

	stepCaptureTagMiss(h, 0, "raw", lines)

	if _, err := os.Stat(filepath.Join(h.RunHome, "tagmiss")); !os.IsNotExist(err) {
		t.Errorf("clean turn must not create the tagmiss dir (err=%v)", err)
	}
}
