package fileadapter

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
)

const recordAt = "2026-05-18T12:00:00Z"

func TestRecordFileEventsRejectsInvalidInput(t *testing.T) {
	read := func(path string) []memops.FileEvent {
		return []memops.FileEvent{{Kind: memops.FileEventRead, Path: path, Content: "v0"}}
	}
	tests := []struct {
		name   string
		thread string
		turn   int
		at     string
		events []memops.FileEvent
		reason string
	}{
		{"unknown thread", "thr_9", 2, recordAt, read("a.go"), "unknown-thread"},
		{"empty thread", "", 2, recordAt, read("a.go"), "unknown-thread"},
		{"turn zero", "thr_1", 0, recordAt, read("a.go"), "turn=0"},
		{"at not RFC3339", "thr_1", 2, "2026-05-18 12:00", read("a.go"), "bad-at"},
		{"commit kind", "thr_1", 2, recordAt, []memops.FileEvent{{Kind: "commit", Path: "a.go"}}, "bad-kind"},
		{"empty path", "thr_1", 2, recordAt, read(""), "empty-path"},
		{"newline in path", "thr_1", 2, recordAt, read("a.go\nb.go"), "newline-in-path"},
		{"invalid UTF-8 path", "thr_1", 2, recordAt, read("a\xff.go"), "invalid-utf8-path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newAdapter(t)
			seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nbody\n")
			a.resetTurnScope()

			err := a.RecordFileEvents(context.Background(), tt.thread, tt.turn, tt.at, tt.events)
			if err == nil {
				t.Fatal("RecordFileEvents accepted invalid input")
			}
			if !strings.Contains(err.Error(), tt.reason) {
				t.Errorf("error %q lacks reason %q", err, tt.reason)
			}
			if log := readEventLog(t, a); strings.Contains(log, "fs.write-error") {
				t.Errorf("adapter logged fs.write-error:\n%s", log)
			}
			if _, err := os.Stat(store.TrackedIndexPath(a.paths, "thr_1")); !os.IsNotExist(err) {
				t.Errorf("index written for rejected input (stat err %v)", err)
			}
		})
	}
}

func TestRecordFileEventsWritesReachTurnScope(t *testing.T) {
	a := newAdapter(t)
	seedThread(t, a, "thr_1", "prj_1", "## Turn 1\n\nbody\n")
	a.resetTurnScope()

	events := []memops.FileEvent{
		{Kind: memops.FileEventRead, Path: "src/a.go", Content: "v0"},
		{Kind: memops.FileEventWrite, Path: "src/a.go", Content: "v1"},
	}
	if err := a.RecordFileEvents(context.Background(), "thr_1", 2, recordAt, events); err != nil {
		t.Fatalf("RecordFileEvents: %v", err)
	}
	// Ids are `git hash-object` of "v0" and "v1"; a two-byte change keeps
	// no diff.
	want := []string{
		store.TrackedBlobRel("thr_1", "28c218c44b49222f91536daf5b4d9871638edc8e"),
		store.TrackedBlobRel("thr_1", "a5e11e51f3c8a6b8e79dfcb73b75041d8cf4302a"),
		store.TrackedIndexRel("thr_1"),
	}
	slices.Sort(want)
	if got := a.snapshotTurnScope(); !slices.Equal(got, want) {
		t.Errorf("turn scope = %v, want %v", got, want)
	}
}
