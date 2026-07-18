package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkerRoundTrip(t *testing.T) {
	paths := PathsForHome(t.TempDir())

	cases := []struct {
		name string
		m    Marker
	}{
		{"turn", Marker{Op: OpTurn, Turn: "t_42"}},
		{"archival", Marker{Op: OpArchival, Orig: "threads/thr_9"}},
		{"sleep", Marker{Op: OpSleep}},
		{"recovery", Marker{Op: OpRecovery}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := WriteMarker(paths, c.m); err != nil {
				t.Fatalf("WriteMarker: %v", err)
			}
			got, present, err := ReadMarker(paths)
			if err != nil {
				t.Fatalf("ReadMarker: %v", err)
			}
			if !present {
				t.Fatal("ReadMarker: present=false after write")
			}
			if got != c.m {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", got, c.m)
			}
		})
	}
}

func TestMarkerAbsentIsNotError(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	got, present, err := ReadMarker(paths)
	if err != nil {
		t.Fatalf("ReadMarker on absent file returned error: %v", err)
	}
	if present {
		t.Fatalf("present=true for absent marker (got %+v)", got)
	}
}

func TestMarkerClear(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := WriteMarker(paths, Marker{Op: OpTurn, Turn: "t_1"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	if err := ClearMarker(paths); err != nil {
		t.Fatalf("ClearMarker: %v", err)
	}
	if _, present, _ := ReadMarker(paths); present {
		t.Fatal("marker still present after ClearMarker")
	}
	// Clearing an absent marker is a no-op, not an error.
	if err := ClearMarker(paths); err != nil {
		t.Fatalf("ClearMarker on absent marker returned error: %v", err)
	}
}

func TestMarkerWriteIsAtomic(t *testing.T) {
	// WriteMarker must route through WriteFileAtomic — leave no tmp- residue
	// and produce a target the reader parses cleanly.
	paths := PathsForHome(t.TempDir())
	if err := WriteMarker(paths, Marker{Op: OpTurn, Turn: "t_7"}); err != nil {
		t.Fatalf("WriteMarker: %v", err)
	}
	tmpPath := tmpSiblingOf(paths.OpMarker)
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("atomic-write temp %s should not survive, stat err = %v", tmpPath, err)
	}
}

func TestMarkerRejectsUnknownOp(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := WriteMarker(paths, Marker{Op: OpKind("bogus")}); err == nil {
		t.Fatal("WriteMarker accepted an unknown op")
	}
	// And it wrote nothing.
	if _, present, _ := ReadMarker(paths); present {
		t.Fatal("a rejected marker was nonetheless persisted")
	}
}

func TestMarkerCorruptIsError(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := os.WriteFile(paths.OpMarker, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt marker: %v", err)
	}
	_, present, err := ReadMarker(paths)
	if err == nil {
		t.Fatal("ReadMarker did not error on corrupt marker")
	}
	if !present {
		t.Fatal("a present-but-corrupt marker must report present=true")
	}
}

// tmpSiblingOf mirrors the atomicWriteTempPrefix convention for tests that
// assert no temp residue survives a write.
func tmpSiblingOf(path string) string {
	return filepath.Join(filepath.Dir(path), atomicWriteTempPrefix+filepath.Base(path))
}
