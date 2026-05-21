package store

import (
	"os"
	"reflect"
	"testing"
)

func TestSaveLoadWorkingSetRoundTrip(t *testing.T) {
	paths := PathsForHome(t.TempDir())

	active := []string{"thr_1", "thr_3"}
	dormant := []string{"thr_2"}
	if err := SaveWorkingSet(paths, active, dormant); err != nil {
		t.Fatalf("SaveWorkingSet: %v", err)
	}

	gotActive, gotDormant, err := LoadWorkingSet(paths)
	if err != nil {
		t.Fatalf("LoadWorkingSet: %v", err)
	}
	if !reflect.DeepEqual(gotActive, active) {
		t.Errorf("active: got %v, want %v", gotActive, active)
	}
	if !reflect.DeepEqual(gotDormant, dormant) {
		t.Errorf("dormant: got %v, want %v", gotDormant, dormant)
	}
}

func TestLoadWorkingSetMissing(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	active, dormant, err := LoadWorkingSet(paths)
	if err != nil {
		t.Fatalf("LoadWorkingSet: %v", err)
	}
	if active != nil || dormant != nil {
		t.Errorf("expected (nil, nil) for missing file, got (%v, %v)", active, dormant)
	}
}

func TestLoadWorkingSetMalformed(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := os.WriteFile(paths.WorkingSet, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	if _, _, err := LoadWorkingSet(paths); err == nil {
		t.Fatal("expected error for malformed file, got nil")
	}
}

func TestSaveWorkingSetRejectsEmptyHome(t *testing.T) {
	if err := SaveWorkingSet(PersonantPaths{}, nil, nil); err == nil {
		t.Fatal("expected error for empty Home, got nil")
	}
}
