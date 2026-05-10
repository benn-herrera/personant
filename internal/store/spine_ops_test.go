package store

import (
	"errors"
	"testing"
)

// spineFixturePaths returns a PersonantPaths rooted at t.TempDir() with an
// empty (but existing) spine.jsonl. The minimal subset needed by the
// spine-ops functions; the broader scaffold from Init is not required.
func spineFixturePaths(t *testing.T) PersonantPaths {
	t.Helper()
	paths := PathsForHome(t.TempDir())
	if err := touchEmpty(paths.Spine); err != nil {
		t.Fatalf("touch spine: %v", err)
	}
	return paths
}

func touchEmpty(path string) error {
	if _, err := touchIfMissing(path); err != nil {
		return err
	}
	return nil
}

func TestAppendSpineRecordEmpty(t *testing.T) {
	paths := spineFixturePaths(t)
	rec := SpineRecord{ID: "thr_1", Project: "prj_1", Summary: "first"}
	if err := AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("AppendSpineRecord: %v", err)
	}
	got, err := ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("ReadSpine: %v", err)
	}
	if len(got) != 1 || got[0].ID != "thr_1" {
		t.Fatalf("unexpected spine: %+v", got)
	}
}

func TestAppendSpineRecordDuplicate(t *testing.T) {
	paths := spineFixturePaths(t)
	rec := SpineRecord{ID: "thr_1", Project: "prj_1", Summary: "first"}
	if err := AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("first append: %v", err)
	}
	err := AppendSpineRecord(paths, rec)
	if !errors.Is(err, ErrDuplicateThreadID) {
		t.Fatalf("expected ErrDuplicateThreadID, got %v", err)
	}
	// Spine must still hold exactly one record.
	got, err := ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("ReadSpine: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("spine should have one record, got %d", len(got))
	}
}

func TestUpdateSpineRecord(t *testing.T) {
	paths := spineFixturePaths(t)
	rec := SpineRecord{ID: "thr_1", Project: "prj_1", Summary: "first", State: ThreadActive}
	if err := AppendSpineRecord(paths, rec); err != nil {
		t.Fatalf("AppendSpineRecord: %v", err)
	}
	rec.State = ThreadResolved
	rec.Summary = "rewritten"
	if err := UpdateSpineRecord(paths, rec); err != nil {
		t.Fatalf("UpdateSpineRecord: %v", err)
	}
	got, ok, err := FindSpineRecord(paths, "thr_1")
	if err != nil {
		t.Fatalf("FindSpineRecord: %v", err)
	}
	if !ok {
		t.Fatal("expected record to exist after update")
	}
	if got.State != ThreadResolved || got.Summary != "rewritten" {
		t.Errorf("update did not stick: %+v", got)
	}
}

func TestUpdateSpineRecordMissing(t *testing.T) {
	paths := spineFixturePaths(t)
	rec := SpineRecord{ID: "thr_99", Project: "prj_1", Summary: "ghost"}
	err := UpdateSpineRecord(paths, rec)
	if !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("expected ErrThreadNotFound, got %v", err)
	}
}

func TestFindSpineRecordPresentAndAbsent(t *testing.T) {
	paths := spineFixturePaths(t)
	for _, rec := range []SpineRecord{
		{ID: "thr_1", Project: "prj_1"},
		{ID: "thr_2", Project: "prj_2"},
	} {
		if err := AppendSpineRecord(paths, rec); err != nil {
			t.Fatalf("AppendSpineRecord %s: %v", rec.ID, err)
		}
	}

	got, ok, err := FindSpineRecord(paths, "thr_2")
	if err != nil {
		t.Fatalf("Find present: %v", err)
	}
	if !ok || got.ID != "thr_2" {
		t.Errorf("expected thr_2, got ok=%v rec=%+v", ok, got)
	}

	_, ok, err = FindSpineRecord(paths, "thr_99")
	if err != nil {
		t.Fatalf("Find absent: %v", err)
	}
	if ok {
		t.Error("expected ok=false for absent id")
	}
}

func TestSpineRecordsByProject(t *testing.T) {
	paths := spineFixturePaths(t)
	mix := []SpineRecord{
		{ID: "thr_1", Project: "prj_1"},
		{ID: "thr_2", Project: "prj_2"},
		{ID: "thr_3", Project: "prj_1"},
		{ID: "thr_4", Project: "prj_default"},
	}
	for _, rec := range mix {
		if err := AppendSpineRecord(paths, rec); err != nil {
			t.Fatalf("AppendSpineRecord %s: %v", rec.ID, err)
		}
	}

	got, err := SpineRecordsByProject(paths, "prj_1")
	if err != nil {
		t.Fatalf("SpineRecordsByProject: %v", err)
	}
	wantIDs := []string{"thr_1", "thr_3"}
	if len(got) != len(wantIDs) {
		t.Fatalf("count: got %d, want %d (records: %+v)", len(got), len(wantIDs), got)
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("position %d: got %s, want %s", i, got[i].ID, id)
		}
	}

	// Empty result for unknown project.
	got, err = SpineRecordsByProject(paths, "prj_unknown")
	if err != nil {
		t.Fatalf("by-project unknown: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestNextThreadID(t *testing.T) {
	cases := []struct {
		name    string
		records []SpineRecord
		want    string
	}{
		{name: "empty", records: nil, want: "thr_1"},
		{name: "single", records: []SpineRecord{{ID: "thr_1"}}, want: "thr_2"},
		{
			name: "sequential",
			records: []SpineRecord{
				{ID: "thr_1"}, {ID: "thr_2"}, {ID: "thr_3"},
			},
			want: "thr_4",
		},
		{
			name: "with gaps",
			records: []SpineRecord{
				{ID: "thr_1"}, {ID: "thr_5"}, {ID: "thr_3"},
			},
			want: "thr_6",
		},
		{
			name: "ignores malformed",
			records: []SpineRecord{
				{ID: "thr_1"}, {ID: "garbage"}, {ID: "thr_42"},
			},
			want: "thr_43",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextThreadID(tc.records); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
