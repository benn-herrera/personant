package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// OpKind is the operation class of an in-flight crash-recovery scope.
// The in-flight signal — whichever artifact carries it — is exactly what
// authorizes recovery's reset --hard (SPEC §4.5.8): a signal-less-dirty
// tree is a legitimate hand-edit and is never reset.
//
// Two carriers (#94 R3-addendum):
//   - op=turn: a NON-EMPTY turn-journal.jsonl (first record carries the
//     turn id — see JournalOwner). The journal append's existing fsync
//     makes the signal durable for free; there is no per-turn marker
//     file. OpTurn survives here as recovery's effective-op vocabulary
//     (cell dispatch, the re-entry marker's orig field).
//   - batch ops (archival/sleep/recovery): the marker FILE below —
//     per-batch, not per-turn cost.
type OpKind string

const (
	OpTurn       OpKind = "turn"       // a turn is mid-flight (journal→commit window)
	OpArchival   OpKind = "archival"   // deep-cold archival between turns (§3.8)
	OpSleep      OpKind = "sleep"      // sleep-cycle consolidation (RebuildTrees, gc)
	OpRecovery   OpKind = "recovery"   // recovery itself is running (re-entrancy guard)
	OpBarrier    OpKind = "barrier"    // the day barrier B0-B6 (#94 R3b); Day carries the target day
	OpRebaseline OpKind = "rebaseline" // mid-day daily nuke+recreate (§6.2 knob); never touches primary
)

// validOpKinds is the closed set of marker op values. A marker with any
// other op is a contract violation at the marker boundary.
var validOpKinds = map[OpKind]struct{}{
	OpTurn:       {},
	OpArchival:   {},
	OpSleep:      {},
	OpRecovery:   {},
	OpBarrier:    {},
	OpRebaseline: {},
}

// Marker is the on-disk op-in-progress record (op-in-progress.json) for
// BATCH operations only (archival/sleep/recovery). It is present iff
// such an operation is mid-flight; the per-turn scope never touches this
// file (its signal is the journal — see OpKind). The file is gitignored
// and survives a reset --hard so recovery can read it after reverting
// the worktree.
//
// Contract: Op is one of the batch OpKind constants (WriteMarker refuses
// op=turn). Turn carries a turn id only on the op=recovery re-entry
// marker whose orig is a turn; Orig carries the original op for
// op=recovery. Day is the target day index of an op=barrier scope (and
// the day stamped on an op=archival scope's primary commits, F3/F6) —
// load-bearing: recovery reads it to complete an interrupted barrier for
// the RIGHT day. All are omitted when empty/zero.
type Marker struct {
	Op   OpKind `json:"op"`
	Turn string `json:"turn,omitempty"`
	Orig string `json:"orig,omitempty"`
	Day  int    `json:"day,omitempty"`
}

// WriteMarker atomically writes m to op-in-progress.json. It is a marker
// boundary: m.Op must be a known BATCH OpKind, else the write is refused
// (a contract check — an unknown op is a caller bug, never persisted,
// and op=turn never uses the marker file: the non-empty turn journal is
// the in-flight-turn signal, so a per-turn marker write here would be a
// regression to the folded-away protocol).
func WriteMarker(paths PersonantPaths, m Marker) error {
	if paths.OpMarker == "" {
		return errors.New("store: WriteMarker: PersonantPaths.OpMarker is empty")
	}
	if _, ok := validOpKinds[m.Op]; !ok {
		return fmt.Errorf("store: WriteMarker: unknown op %q", m.Op)
	}
	if m.Op == OpTurn {
		return errors.New("store: WriteMarker: op=turn never uses the marker file (the turn journal is the in-flight-turn signal; the marker file is batch ops only)")
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
//
// Read tolerance: an op=turn marker file (written by pre-addendum code,
// never by WriteMarker now) still parses — recovery honors it as the
// legacy turn signal and clears it, so an old home reconciles cleanly.
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
