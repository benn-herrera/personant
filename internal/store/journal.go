package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

// TruncateJournal truncates turn-journal.jsonl to empty and fsyncs, the
// CommitTurn post-condition (the turn's content is now durably in the
// commit, so the journal's job is done). An absent file is a no-op — the
// empty post-condition already holds.
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
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("store: TruncateJournal: fsync: %w", err)
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
