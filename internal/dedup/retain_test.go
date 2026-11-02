package dedup

import (
	"maps"
	"slices"
	"testing"
)

func TestRetainWindowBoundary(t *testing.T) {
	entries := []FileEntry{
		{Path: "a", ID: "ida", Turn: 10, Changes: []FileChange{
			{From: "a0", To: "a1", Diff: true, Size: 5},
			{From: "a1", To: "ida", Diff: false},
		}},
		{Path: "b", ID: "idb", Turn: 11, Changes: []FileChange{{From: "b0", To: "idb", Diff: true, Size: 3}}},
		{Path: "c", ID: "idc", Turn: 30},
	}
	// turnCount − window = 10: turn 10 drops, turn 11 is kept.
	r := Retain(entries, 30, 20)

	var paths []string
	for _, e := range r.Entries {
		paths = append(paths, e.Path)
	}
	if want := []string{"b", "c"}; !slices.Equal(paths, want) {
		t.Errorf("kept paths = %v, want %v", paths, want)
	}
	wantBlobs := map[string]bool{"idb": true, "idc": true}
	if !maps.Equal(r.Blobs, wantBlobs) {
		t.Errorf("blobs = %v, want %v", r.Blobs, wantBlobs)
	}
	wantDiffs := map[DiffRef]bool{{"b0", "idb"}: true}
	if !maps.Equal(r.Diffs, wantDiffs) {
		t.Errorf("diffs = %v, want %v", r.Diffs, wantDiffs)
	}
}
