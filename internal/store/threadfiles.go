package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"personant/internal/dedup"
)

// Retention-window calibration for §3.9 git-minimization chain aging.
// Names mirror the directive-key convention used by dedup.AnchorCadence /
// dedup.DiffLiteralThreshold — these are package constants for now;
// per-project directive plumbing is later work.
//
// Once a tracked file is committed to its project git repo, its committed
// state is recoverable by commit hash, so Personant's own reverse-delta
// chain of the pre-commit edit history becomes pure duplication of git.
// After a retention window the chain is dropped, leaving only the hash as
// a recovery pointer (see ThreadFiles.AgeOut).
const (
	// FileChainRetentionTurns is the turn-count side of the retention
	// window: a committed file's chain is aged out once this many turns
	// have elapsed since the commit. v0.1 best-guess calibration —
	// calibratable against six-month-sim demand-sizing data.
	FileChainRetentionTurns = 100

	// FileChainRetentionDays is the wall-clock side of the retention
	// window: a committed file's chain is aged out once this many days
	// have elapsed since the commit timestamp. v0.1 best-guess
	// calibration — calibratable against six-month-sim demand-sizing data.
	FileChainRetentionDays = 2
)

// FileEntry is the §3.9.1 persistent record for one tracked file
// within one thread.
type FileEntry struct {
	// Path is the workspace-relative file path. It is the map key in
	// ThreadFiles.Files, denormalized here so a FileEntry is self-contained.
	Path string `json:"path"`
	// Chain is the reverse-delta version history; Chain.Current() is the
	// live literal. It serializes inline.
	Chain dedup.Chain `json:"chain"`
	// LastCommit is the git commit hash recorded at the last fs.commit, or
	// "" when there are uncommitted edits since that commit.
	LastCommit string `json:"last_commit"`
	// CommittedAt is the RFC3339 timestamp of that commit (clock.Timeline()
	// form, supplied by the caller). It is "" iff LastCommit is "".
	CommittedAt string `json:"committed_at"`
	// CommittedTurn is the turn number at which the file was committed. It
	// is 0 iff LastCommit is "". It is the turn-count side of the §3.9
	// git-minimization retention window (see AgeOut).
	CommittedTurn int `json:"committed_turn"`
}

// ThreadFiles is the per-thread tracked-file store, persisted as the JSON
// sidecar <Home>/threads/thr_<id>/files.json.
type ThreadFiles struct {
	ThreadID string                `json:"thread_id"`
	Files    map[string]*FileEntry `json:"files"`
}

// ThreadFilesPath returns the canonical sidecar path for a thread's
// tracked-file store: <ThreadsDir>/thr_<id>/files.json, inside the
// thread's directory (see ThreadDir).
func ThreadFilesPath(paths PersonantPaths, threadID string) string {
	return filepath.Join(ThreadDir(paths, threadID), "files.json")
}

// LoadThreadFiles reads and parses the tracked-file sidecar for threadID.
//
// A missing sidecar is NOT an error: it is the fresh state, and a valid
// empty ThreadFiles is returned. A present-but-corrupt file IS an error.
func LoadThreadFiles(paths PersonantPaths, threadID string) (ThreadFiles, error) {
	path := ThreadFilesPath(paths, threadID)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ThreadFiles{ThreadID: threadID, Files: map[string]*FileEntry{}}, nil
		}
		return ThreadFiles{}, fmt.Errorf("load thread files %s: read: %w", threadID, err)
	}
	var tf ThreadFiles
	if err := json.Unmarshal(data, &tf); err != nil {
		return ThreadFiles{}, fmt.Errorf("load thread files %s: parse %s: %w", threadID, path, err)
	}
	if tf.Files == nil {
		tf.Files = map[string]*FileEntry{}
	}
	return tf, nil
}

// SaveThreadFiles writes the tracked-file sidecar atomically (temp file in
// the ThreadsDir, fsync, rename). The ThreadsDir is created if absent.
// JSON is indented with two spaces so the sidecar diffs cleanly in the
// home-substrate git repo.
func SaveThreadFiles(paths PersonantPaths, tf ThreadFiles) error {
	if tf.ThreadID == "" {
		return errors.New("save thread files: ThreadID is empty")
	}
	dir := ThreadDir(paths, tf.ThreadID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save thread files %s: mkdir %s: %w", tf.ThreadID, dir, err)
	}
	path := ThreadFilesPath(paths, tf.ThreadID)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tf); err != nil {
		return fmt.Errorf("save thread files %s: encode: %w", tf.ThreadID, err)
	}
	if err := WriteFileAtomic(path, buf.Bytes()); err != nil {
		return fmt.Errorf("save thread files %s: %w", tf.ThreadID, err)
	}
	return nil
}

// RecordWrite is the read/modify/write hook for a tracked file. It looks
// up the entry for path (creating one with a fresh dedup.Chain if absent),
// then appends content as the new current version.
//
// A genuine content change invalidates the commit pointer: LastCommit and
// CommittedAt are cleared, since the file now has uncommitted edits.
//
// Idempotency: if path is already tracked and content equals the entry's
// current chain literal, RecordWrite is a no-op — no new chain version is
// appended and the commit pointer is left intact. An unchanged re-read
// (fs.read carrying content that has not moved) or an idempotent re-write
// must neither grow the version chain nor silently un-commit a committed
// file.
func (tf *ThreadFiles) RecordWrite(path, content string) {
	if tf.Files == nil {
		tf.Files = map[string]*FileEntry{}
	}
	e, ok := tf.Files[path]
	if !ok {
		e = &FileEntry{Path: path}
		tf.Files[path] = e
		e.Chain.Append(content)
		e.clearCommit()
		return
	}
	if e.Chain.Current() == content {
		// Unchanged content: no chain growth, commit pointer untouched.
		return
	}
	e.Chain.Append(content)
	e.clearCommit()
}

// clearCommit clears all three commit fields together. The commit pointer
// is a unit: LastCommit, CommittedAt, and CommittedTurn are set together
// by RecordCommit and cleared together whenever an uncommitted edit lands.
func (e *FileEntry) clearCommit() {
	e.LastCommit = ""
	e.CommittedAt = ""
	e.CommittedTurn = 0
}

// RecordCommit sets the git-commit pointer on a tracked file. It returns
// an error if path is not tracked (a file the store has never seen cannot
// be committed) or if hash or committedAt is empty.
//
// committedTurn is the turn number at which the commit happened; it feeds
// the turn-count side of the §3.9 git-minimization retention window.
func (tf *ThreadFiles) RecordCommit(path, hash, committedAt string, committedTurn int) error {
	if hash == "" {
		return fmt.Errorf("record commit %s: hash is empty", path)
	}
	if committedAt == "" {
		return fmt.Errorf("record commit %s: committedAt is empty", path)
	}
	e, ok := tf.Files[path]
	if !ok {
		return fmt.Errorf("record commit %s: file is not tracked", path)
	}
	e.LastCommit = hash
	e.CommittedAt = committedAt
	e.CommittedTurn = committedTurn
	return nil
}

// AgeOut computes the §3.9 git-minimization aging *decision*: it returns
// the sorted list of committed files (LastCommit != "") whose retention
// window has elapsed and whose chains are therefore candidates to be
// dropped. It is a pure read — it does NOT mutate any entry.
//
// The decision is split from the effect so the I/O-dependent reachability
// gate can interpose: a candidate's chain is dropped (via DropChain) only
// after the adapter has confirmed the commit hash is reachable in the
// workspace repo, so durable content is never aged away without an
// application-reachable recovery path (SPEC §3.9.1). Keeping the gate out
// of this method is deliberate: this file stays git-free — the
// workspace-git dependency lives only in the adapter + gitworkspace.go.
//
// An entry is a candidate when EITHER threshold trips first (OR-drop):
//   - currentTurn - CommittedTurn >= FileChainRetentionTurns, or
//   - now is >= FileChainRetentionDays days after CommittedAt.
//
// CommittedAt is parsed as RFC3339; a parse failure is treated as
// not-yet-ageable and the entry is skipped — a corrupt timestamp must not
// silently drop data, only the turn threshold can then age it. Uncommitted
// entries (LastCommit == "") never age, and an already-aged entry (chain
// dropped to empty) is never a candidate again — aging is idempotent.
func (tf *ThreadFiles) AgeOut(currentTurn int, now time.Time) (candidatePaths []string) {
	cutoff := now.AddDate(0, 0, -FileChainRetentionDays)
	for path, e := range tf.Files {
		if e.LastCommit == "" {
			continue
		}
		// Already aged (chain dropped to empty) — nothing left to age, so
		// it is not a candidate. This keeps aging idempotent: a second pass
		// over an aged entry is a no-op, never a re-report (SPEC §3.9.1 Inv:
		// a file already aged stays aged).
		if e.Chain.Len() == 0 {
			continue
		}
		byTurn := currentTurn-e.CommittedTurn >= FileChainRetentionTurns
		byDay := false
		if committedAt, err := time.Parse(time.RFC3339, e.CommittedAt); err == nil {
			byDay = !committedAt.After(cutoff)
		}
		if !byTurn && !byDay {
			continue
		}
		candidatePaths = append(candidatePaths, path)
	}
	sort.Strings(candidatePaths)
	return candidatePaths
}

// DropChain applies the aging *effect* to one path: it replaces the entry's
// Chain with a fresh empty dedup.Chain (the committed literal is NOT
// retained — git + the hash recover it) and keeps Path, LastCommit,
// CommittedAt, CommittedTurn so the hash remains a recovery pointer. It
// returns the bytes freed (len of the dropped chain's live literal) for
// demand-sizing forensics, and ok=false when path is not tracked (no
// mutation in that case).
//
// The caller (the adapter) invokes DropChain only for AgeOut candidates
// whose commit hash it has confirmed reachable in the workspace repo.
func (tf *ThreadFiles) DropChain(path string) (bytesFreed int, ok bool) {
	e, ok := tf.Files[path]
	if !ok {
		return 0, false
	}
	bytesFreed = len(e.Chain.Current())
	e.Chain = dedup.Chain{}
	return bytesFreed, true
}

// Entry returns the tracked-file entry for path, if present.
func (tf *ThreadFiles) Entry(path string) (*FileEntry, bool) {
	e, ok := tf.Files[path]
	return e, ok
}
