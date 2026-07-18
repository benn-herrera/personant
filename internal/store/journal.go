package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"personant/internal/clock"
)

// JournalKind distinguishes the two content journal record kinds.
type JournalKind string

const (
	JournalPrompt   JournalKind = "prompt"   // the user prompt, journaled before the model call
	JournalResponse JournalKind = "response" // the model response, journaled before canonical writes
)

var validJournalKinds = map[JournalKind]struct{}{
	JournalPrompt:   {},
	JournalResponse: {},
}

// JournalRecord is one line of turn-journal.jsonl. Bytes carries the raw
// journaled content (marshalled as base64 by encoding/json); it is the
// prompt-onward content recovered after a mid-turn crash. At is a
// Timeline (simulated-world) timestamp — every clock read goes through
// internal/clock.
type JournalRecord struct {
	Turn  string      `json:"turn"`
	Kind  JournalKind `json:"kind"`
	At    time.Time   `json:"at"`
	Bytes []byte      `json:"bytes"`
}

// AppendJournal appends one record to turn-journal.jsonl with a per-append
// file fsync, so the appended content bytes are durable before the caller
// proceeds (the ≤1-turn durability bar in SPEC §4.5.8 depends on this).
//
// Durability honesty (dir-fsync gap): the file's data is fsynced on every
// append. The parent directory is fsynced ONCE, when this append creates
// the file, so the directory entry for turn-journal.jsonl is itself
// durable; subsequent appends do not re-fsync the directory (the inode
// already has a durable name, and append changes file data, not the
// directory). This is strictly stronger than the atomic-write path:
// WriteFileAtomic fsyncs the temp's data but does NOT fsync the parent
// directory after its rename, so under power loss the *rename* is not
// guaranteed durable (§4.5.8's known rename-durability gap). The journal
// avoids rename entirely (pure append), so its content-durability
// guarantee does not inherit that gap.
func AppendJournal(paths PersonantPaths, turnID string, kind JournalKind, content []byte) error {
	if paths.TurnJournal == "" {
		return errors.New("store: AppendJournal: PersonantPaths.TurnJournal is empty")
	}
	if turnID == "" {
		return errors.New("store: AppendJournal: turnID is empty")
	}
	if _, ok := validJournalKinds[kind]; !ok {
		return fmt.Errorf("store: AppendJournal: unknown kind %q", kind)
	}

	rec := JournalRecord{Turn: turnID, Kind: kind, At: clock.Timeline(), Bytes: content}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("store: AppendJournal: marshal: %w", err)
	}
	line = append(line, '\n')

	// Detect whether this append creates the file, so the parent-directory
	// fsync happens exactly once (on creation). Single-writer substrate, so
	// the stat→open window carries no concurrent-writer hazard.
	_, statErr := os.Stat(paths.TurnJournal)
	created := errors.Is(statErr, os.ErrNotExist)

	f, err := os.OpenFile(paths.TurnJournal, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("store: AppendJournal: open: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return fmt.Errorf("store: AppendJournal: write: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("store: AppendJournal: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store: AppendJournal: close: %w", err)
	}

	if created {
		if err := fsyncDir(filepath.Dir(paths.TurnJournal)); err != nil {
			return fmt.Errorf("store: AppendJournal: dir fsync: %w", err)
		}
	}
	return nil
}

// JournalOwner reads the in-flight-turn signal (#94 R3-addendum): a
// NON-EMPTY turn-journal.jsonl IS the op=turn marker. The first record
// of a turn's journal carries the turn id, and the first append's fsync
// is what makes the signal durable — marker-set durability rides the
// append the pipeline already pays for, so opening a turn's crash-
// recovery scope costs no extra sync. The marker FILE (op-in-progress
// .json) is batch ops only (archival/sleep/recovery).
//
// Returns:
//   - ("", false, nil)  — absent or empty journal: no turn in flight.
//   - (id, true, nil)   — first line parses: turn id owns the scope.
//   - ("", true, nil)   — first line torn (no newline) or corrupt: a
//     turn IS in flight but its id is unrecoverable. Callers must treat
//     an unknown owner as a conflicting scope (never adopt it); recovery
//     classifies the same state via ScanJournal's torn tolerance.
//
// Only the first line is read — the journal can carry a whole model
// response, and the conflict checks that call this run on every turn.
func JournalOwner(paths PersonantPaths) (turnID string, inFlight bool, err error) {
	if paths.TurnJournal == "" {
		return "", false, errors.New("store: JournalOwner: PersonantPaths.TurnJournal is empty")
	}
	f, oerr := os.Open(paths.TurnJournal)
	if oerr != nil {
		if errors.Is(oerr, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("store: JournalOwner: %w", oerr)
	}
	defer f.Close()
	line, rerr := bufio.NewReader(f).ReadBytes('\n')
	if rerr != nil && rerr != io.EOF {
		return "", false, fmt.Errorf("store: JournalOwner: read first line: %w", rerr)
	}
	if len(line) == 0 {
		return "", false, nil // empty file: clean steady state
	}
	if line[len(line)-1] != '\n' {
		return "", true, nil // torn first append: in flight, id unknown
	}
	var rec JournalRecord
	if uerr := json.Unmarshal(line, &rec); uerr != nil {
		return "", true, nil // corrupt first record: in flight, id unknown
	}
	return rec.Turn, true, nil
}

// ScanJournal reads turn-journal.jsonl and returns its well-formed
// records. It is a tolerant reader: any line that fails to unmarshal, and
// a final line lacking a trailing newline (a torn append interrupted
// mid-write), are skipped rather than fatal — torn counts how many lines
// were dropped, which the recovery caller reports. An absent file yields
// (nil, 0, nil): no journal is the clean-shutdown steady state.
func ScanJournal(paths PersonantPaths) (records []JournalRecord, torn int, err error) {
	if paths.TurnJournal == "" {
		return nil, 0, errors.New("store: ScanJournal: PersonantPaths.TurnJournal is empty")
	}
	data, rerr := os.ReadFile(paths.TurnJournal)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("store: ScanJournal: %w", rerr)
	}
	if len(data) == 0 {
		return nil, 0, nil
	}

	// A trailing byte that is not '\n' means the final line was never
	// newline-terminated: it is a torn append. Drop that trailing fragment
	// and count it. Everything up to the last '\n' is complete lines.
	if data[len(data)-1] != '\n' {
		if nl := bytes.LastIndexByte(data, '\n'); nl >= 0 {
			data = data[:nl+1]
		} else {
			data = nil // single unterminated line: whole buffer is torn
		}
		torn++
	}

	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue // trailing empty element after the final '\n'
		}
		var rec JournalRecord
		if uerr := json.Unmarshal(line, &rec); uerr != nil {
			torn++ // corrupt/partial interior line — tolerated, counted
			continue
		}
		records = append(records, rec)
	}
	return records, torn, nil
}

// TruncateJournal truncates turn-journal.jsonl to empty — the CommitTurn
// post-condition (the turn's content is now durably in the commit, so
// the journal's job is done) and, under the R3-addendum fold, the
// in-flight-turn scope release. An absent file is a no-op — the empty
// post-condition already holds.
//
// NO fsync (R3-addendum item 2) — deliberately. The truncate needs no
// durability guarantee of its own because every resurrection of a
// stale-but-truncated journal after power loss lands in an
// already-handled recovery cell:
//
//   - The truncate belongs to turn T's CommitTurn, which runs AFTER T's
//     commit. If power loss resurrects the pre-truncate bytes, the
//     journal's first record carries T and HEADTURN==T — exactly cell
//     5's redundant-journal case: truncate again, zero loss (proof test:
//     TestCell5_ResurrectedTruncatedJournal in internal/recovery).
//   - The truncate cannot stay unsynced past the NEXT turn's first
//     canonical write: turn T+1's prompt append (AppendJournal) fsyncs
//     the file, which makes the truncate AND the new first record
//     durable together — so by the time any T+1 canonical dirt can
//     exist, the journal durably names T+1, never a stale turn.
//   - The residual corner (an older committed turn's journal resurfacing
//     with a clean tree, e.g. after multiple unsynced truncates on an
//     idle session) classifies as cell 6: preserved to an artifact
//     (redundant but never lost) and truncated.
//
// The converse direction, stated honestly: if the truncate IS durable
// but turn T's earlier (also un-fsynced) commit is the write that power
// loss dropped, the substrate opens with an empty journal, HEAD at T-1,
// and T's canonical writes dirty in the worktree — cell 3 (markerless
// dirty), which absorbs the dirt forward with NO content backstop, since
// the journal that would have held it is already empty. This is the exact
// window the pre-fold per-turn marker-clear also left open, not a
// regression: it is a power-loss-tier residual accepted in the SOLUTION
// honest-coverage table (mad-design/crash-stability/SOLUTION.md) — the
// ≤1-turn durability bar covers fsync-acked content, and a commit lost
// before its own fsync is below that bar by construction.
func TruncateJournal(paths PersonantPaths) error {
	if paths.TurnJournal == "" {
		return errors.New("store: TruncateJournal: PersonantPaths.TurnJournal is empty")
	}
	f, err := os.OpenFile(paths.TurnJournal, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("store: TruncateJournal: open: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store: TruncateJournal: close: %w", err)
	}
	return nil
}

// fsyncDir opens dir and fsyncs it, making a directory-entry change (a
// file creation) durable. A directory opened read-only can still be
// Sync'd on the platforms personant targets.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
