package memops

import "errors"

// This file holds the domain types and sentinel errors for §3.8
// recoverable git-based archival (task #99). They are part of the
// MemoryOps port's contract but are kept in their own file because they
// form a self-contained concern.
//
// Substrate-agnostic naming policy (ARCHITECTURE.md "Port abstraction
// policy"): the only substrate-leaking values permitted at this layer
// are the opaque hash strings — CommitHash and TreeHash carry git object
// hashes but the port treats them as opaque recovery tokens, not as a
// git concept the application reasons about. Field names otherwise
// describe abstract archival concepts (ThrID, OriginalPath, SpineSummary)
// rather than on-disk layout.

// ArchiveEntry is one line of the archive index — the lookup record for a
// thread that has been moved off the active spine into deep-cold,
// recoverable storage (spec §3.8 / §2.1). The deletion commit identified
// by CommitHash is the canonical store; this entry is the lookup. Losing
// the index never loses the data: the commit remains reachable by git
// history regardless.
//
// Entries are append-only forensic breadcrumbs sorted by ThrID for
// line-grain diffs. A recovered thread RETAINS its entry (RecoveredAt
// stamped) — the entry becomes a historical marker, not a "currently
// archived" claim.
type ArchiveEntry struct {
	// ThrID is the stable thread id (thr_<n>). IDs are permanent: recovery
	// reuses the same id, never mints a new one. Sort key for the index.
	ThrID string `json:"thr_id"`

	// CommitHash is the opaque hash of the deletion commit — the commit
	// that staged the thread directory's removal from the worktree. The
	// thread's bytes live in this commit's PARENT tree (the deletion
	// commit's own tree no longer contains the directory); recovery reads
	// the parent.
	CommitHash string `json:"commit_hash"`

	// TreeHash is the opaque hash of the thread directory's subtree as it
	// existed at the deletion commit's parent — the integrity token.
	// Recovery re-hashes the restored directory and aborts on mismatch
	// (invariant 2: never write a partially-recovered thread onto the
	// spine).
	TreeHash string `json:"tree_hash"`

	// ArchivedAt is the RFC3339 timestamp the thread was archived
	// (clock.Timeline, stamped by the adapter — the port carries no clock).
	ArchivedAt string `json:"archived_at"`

	// OriginalPath is the repo-relative directory path the thread occupied
	// (e.g. "threads/thr_7"). Recovery restores the subtree here.
	OriginalPath string `json:"original_path"`

	// SpineSummary is a snapshot of the thread's spine summary at archival
	// time — a forensic + future-archive-recall hint. NOT a live recall
	// surface in v0.1.
	SpineSummary string `json:"spine_summary"`

	// Anchors is a snapshot of the thread's anchors at archival time —
	// same forensic/hint role as SpineSummary. A snapshot, not the
	// evolving history_symbols.
	Anchors []string `json:"anchors"`

	// Project is the thread's owning project id at archival time.
	Project string `json:"project"`

	// RecoveredAt is the RFC3339 timestamp the thread was recovered, or
	// empty until recovered. Its presence flips the entry from a
	// "currently archived" record into a historical breadcrumb.
	RecoveredAt string `json:"recovered_at,omitempty"`
}

// ArchiveOutcome is the per-thread result of a batch archival, so a
// partial failure is visible to the caller (design §3.1). Exactly one of
// the success/skip/error states is meaningful per entry.
type ArchiveOutcome struct {
	ThrID string `json:"thr_id"`
	// Archived is true when the thread was archived in this batch.
	Archived bool `json:"archived"`
	// Skipped is true when the thread was not archived for a non-fatal
	// reason (e.g. no spine record to archive); Reason carries the detail.
	Skipped bool `json:"skipped,omitempty"`
	// Reason is a human-readable note for a skip or a per-thread soft
	// failure. Empty on a clean archive.
	Reason string `json:"reason,omitempty"`
}

// ArchiveResult is the aggregate outcome of one batch archival drain.
// Order-independent: outcomes are keyed by ThrID, not positional.
type ArchiveResult struct {
	// Outcomes is one entry per requested thread id.
	Outcomes []ArchiveOutcome `json:"outcomes"`
	// CommitHash is the deletion commit produced by the batch, or empty if
	// nothing was archived (no commit made).
	CommitHash string `json:"commit_hash,omitempty"`
}

var (
	// ErrArchiveEntryNotFound is returned when a recovery or lookup
	// targets a thread id that has no entry in the archive index.
	ErrArchiveEntryNotFound = errors.New("store: archive entry not found")
	// ErrArchiveIntegrity is returned when a recovered thread directory's
	// tree hash does not match the integrity token stored at archival
	// time — the recovery is aborted and no partial thread reaches the
	// spine (invariant 2).
	ErrArchiveIntegrity = errors.New("store: archive integrity check failed")
)
