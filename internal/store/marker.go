package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// OpKind is the operation class recorded in the op-in-progress marker.
// Its presence at open time is exactly what authorizes recovery's
// reset --hard (SPEC §4.5.8): a markerless-dirty tree is a legitimate
// hand-edit and is never reset.
type OpKind string

const (
	OpTurn     OpKind = "turn"     // a turn is mid-flight (journal→commit window)
	OpArchival OpKind = "archival" // deep-cold archival between turns (§3.8)
	OpSleep    OpKind = "sleep"    // sleep-cycle consolidation (RebuildTrees, gc)
	OpRecovery OpKind = "recovery" // recovery itself is running (re-entrancy guard)
)

// validOpKinds is the closed set of marker op values. A marker with any
// other op is a contract violation at the marker boundary.
var validOpKinds = map[OpKind]struct{}{
	OpTurn:     {},
	OpArchival: {},
	OpSleep:    {},
	OpRecovery: {},
}

// Marker is the on-disk op-in-progress record (op-in-progress.json). It
// is present iff a mutating operation is mid-flight. The file is
// gitignored and survives a reset --hard so recovery can read it after
// reverting the worktree.
//
// Contract: Op is one of the OpKind constants. Turn carries the turn-id
// for op=turn; Orig carries the original path/id for op=archival (the
// cell-9 unstamped-repair anchor). Both are omitted when empty.
type Marker struct {
	Op   OpKind `json:"op"`
	Turn string `json:"turn,omitempty"`
	Orig string `json:"orig,omitempty"`
}

// WriteMarker atomically writes m to op-in-progress.json. It is a marker
// boundary: m.Op must be a known OpKind, else the write is refused (a
// contract check — an unknown op is a caller bug, never persisted).
func WriteMarker(paths PersonantPaths, m Marker) error {
	if paths.OpMarker == "" {
		return errors.New("store: WriteMarker: PersonantPaths.OpMarker is empty")
	}
	if _, ok := validOpKinds[m.Op]; !ok {
		return fmt.Errorf("store: WriteMarker: unknown op %q", m.Op)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("store: WriteMarker: marshal: %w", err)
	}
	if err := WriteFileAtomic(paths.OpMarker, data); err != nil {
		return fmt.Errorf("store: WriteMarker: %w", err)
	}
	return nil
}

// ReadMarker reads op-in-progress.json. The absent file is the common
// case (no operation in flight) and is reported as present=false with a
// nil error — not an error. A present-but-malformed marker IS an error:
// it is a corrupt in-flight record, and recovery must not silently treat
// corruption as "no op running".
func ReadMarker(paths PersonantPaths) (m Marker, present bool, err error) {
	if paths.OpMarker == "" {
		return Marker{}, false, errors.New("store: ReadMarker: PersonantPaths.OpMarker is empty")
	}
	data, rerr := os.ReadFile(paths.OpMarker)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return Marker{}, false, nil
		}
		return Marker{}, false, fmt.Errorf("store: ReadMarker: %w", rerr)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return Marker{}, true, fmt.Errorf("store: ReadMarker: corrupt marker: %w", err)
	}
	if _, ok := validOpKinds[m.Op]; !ok {
		return m, true, fmt.Errorf("store: ReadMarker: unknown op %q", m.Op)
	}
	return m, true, nil
}

// ClearMarker removes op-in-progress.json. Clearing an absent marker is a
// no-op (the post-condition — no marker present — already holds), so it
// returns nil.
func ClearMarker(paths PersonantPaths) error {
	if paths.OpMarker == "" {
		return errors.New("store: ClearMarker: PersonantPaths.OpMarker is empty")
	}
	if err := os.Remove(paths.OpMarker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("store: ClearMarker: %w", err)
	}
	return nil
}
