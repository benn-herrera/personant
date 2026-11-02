package store

// Repo-relative (slash-separated) path helpers for the per-turn scoped
// staging (#94 R3-addendum item 3): the file adapter records exactly the
// tracked files each turn-scope write touches, and CommitTurn stages
// that set instead of walking the whole tree. Each layout name below is
// a named constant shared with the absolute-path builders (paths.go /
// thread_io.go / threadfiles.go), so a rename is a single edit and the
// rel and abs views cannot drift.

const (
	spineFileName       = "spine.jsonl"
	threadsDirName      = "threads"
	threadMetaFileName  = "thread.md"
	threadTurnsDirName  = "turns"
	threadFolioFileName = "folio.jsonl"
	threadFilesFileName = "files.json"
	trackedIndexName    = "files.jsonl"
	trackedFilesDirName = "files"
	projectsDirName     = "projects"
)

// SpineRel is the repo-relative path of the spine.
func SpineRel() string { return spineFileName }

// ThreadMetaRel is the repo-relative path of a thread's thread.md.
func ThreadMetaRel(threadID string) string {
	return threadsDirName + "/" + threadID + "/" + threadMetaFileName
}

// ThreadTurnRel is the repo-relative path of one turn-excerpt file.
func ThreadTurnRel(threadID string, turnNumber int) string {
	return threadExcerptRel(threadID, turnFileName(turnNumber))
}

// threadExcerptRel is the repo-relative path of the loose excerpt file
// named name.
func threadExcerptRel(threadID, name string) string {
	return threadsDirName + "/" + threadID + "/" + threadTurnsDirName + "/" + name
}

// ThreadFolioRel is the repo-relative path of a thread's excerpt folio.
func ThreadFolioRel(threadID string) string {
	return threadsDirName + "/" + threadID + "/" + threadFolioFileName
}

// ThreadFilesRel is the repo-relative path of a thread's tracked-file
// sidecar (files.json).
func ThreadFilesRel(threadID string) string {
	return threadsDirName + "/" + threadID + "/" + threadFilesFileName
}

// TrackedIndexRel is the repo-relative path of a thread's tracked-file
// index (files.jsonl).
func TrackedIndexRel(threadID string) string {
	return threadsDirName + "/" + threadID + "/" + trackedIndexName
}

// TrackedBlobRel is the repo-relative path of a thread's tracked-file blob
// with content id id.
func TrackedBlobRel(threadID, id string) string {
	return threadsDirName + "/" + threadID + "/" + trackedFilesDirName + "/" + id
}

// TrackedDiffRel is the repo-relative path of a thread's kept diff from
// content id from to content id to.
func TrackedDiffRel(threadID, from, to string) string {
	return threadsDirName + "/" + threadID + "/" + trackedFilesDirName + "/" + trackedDiffName(from, to)
}

// ProjectMetaRel is the repo-relative path of a project's meta.json.
func ProjectMetaRel(projectID string) string {
	return projectsDirName + "/" + projectID + "/" + ProjectMetaFileName
}
