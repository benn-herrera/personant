package store

import (
	"errors"
	"os"
	"testing"

	"personant/internal/memops"
)

func archiveTestPaths(t *testing.T) PersonantPaths {
	t.Helper()
	paths := PathsForHome(t.TempDir())
	if err := os.MkdirAll(paths.ArchiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive dir: %v", err)
	}
	return paths
}

func entry(id string) memops.ArchiveEntry {
	return memops.ArchiveEntry{
		ThrID:        id,
		CommitHash:   "c-" + id,
		TreeHash:     "t-" + id,
		ArchivedAt:   "2026-05-29T00:00:00Z",
		OriginalPath: "threads/" + id,
		SpineSummary: "summary " + id,
		Anchors:      []string{"a", "b"},
		Project:      "prj_1",
	}
}

func TestLoadArchiveIndex_MissingIsEmpty(t *testing.T) {
	paths := archiveTestPaths(t)
	got, err := LoadArchiveIndex(paths)
	if err != nil {
		t.Fatalf("LoadArchiveIndex on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d entries", len(got))
	}
}

func TestAppendArchiveEntries_RoundTripSortedByThrID(t *testing.T) {
	paths := archiveTestPaths(t)

	// Append out of order across two calls; the file must end up sorted.
	if err := AppendArchiveEntries(paths, []memops.ArchiveEntry{entry("thr_3"), entry("thr_1")}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := AppendArchiveEntries(paths, []memops.ArchiveEntry{entry("thr_2")}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	got, err := LoadArchiveIndex(paths)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{"thr_1", "thr_2", "thr_3"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ThrID != id {
			t.Fatalf("entry %d: got %q, want %q (file not sorted by thr_id)", i, got[i].ThrID, id)
		}
	}
	// Field fidelity check on a representative entry.
	if got[0].CommitHash != "c-thr_1" || got[0].TreeHash != "t-thr_1" || got[0].OriginalPath != "threads/thr_1" {
		t.Fatalf("entry 0 fields not round-tripped: %+v", got[0])
	}
}

func TestAppendArchiveEntries_ReappendReplacesAndStampsRecovered(t *testing.T) {
	paths := archiveTestPaths(t)
	if err := AppendArchiveEntries(paths, []memops.ArchiveEntry{entry("thr_1")}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Re-append same id with RecoveredAt stamped — must replace, not duplicate.
	stamped := entry("thr_1")
	stamped.RecoveredAt = "2026-05-30T00:00:00Z"
	if err := AppendArchiveEntries(paths, []memops.ArchiveEntry{stamped}); err != nil {
		t.Fatalf("re-append: %v", err)
	}
	got, err := LoadArchiveIndex(paths)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after re-append, got %d", len(got))
	}
	if got[0].RecoveredAt != "2026-05-30T00:00:00Z" {
		t.Fatalf("RecoveredAt not stamped: %q", got[0].RecoveredAt)
	}
}

func TestAppendArchiveEntries_EmptyIsNoOp(t *testing.T) {
	paths := archiveTestPaths(t)
	if err := AppendArchiveEntries(paths, nil); err != nil {
		t.Fatalf("empty append: %v", err)
	}
	if _, err := os.Stat(paths.ArchiveIndex); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty append should not create the index file; stat err=%v", err)
	}
}

func TestFindArchiveEntry(t *testing.T) {
	paths := archiveTestPaths(t)
	if err := AppendArchiveEntries(paths, []memops.ArchiveEntry{entry("thr_1"), entry("thr_2")}); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, found, err := FindArchiveEntry(paths, "thr_2")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !found {
		t.Fatal("expected thr_2 found")
	}
	if got.ThrID != "thr_2" {
		t.Fatalf("got %q, want thr_2", got.ThrID)
	}

	_, found, err = FindArchiveEntry(paths, "thr_99")
	if err != nil {
		t.Fatalf("find missing: %v", err)
	}
	if found {
		t.Fatal("expected thr_99 not found")
	}
}
