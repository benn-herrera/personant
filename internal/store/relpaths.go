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
	threadFilesFileName = "files.json"
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
	return threadsDirName + "/" + threadID + "/" + threadTurnsDirName + "/" + turnFileName(turnNumber)
}

// ThreadFilesRel is the repo-relative path of a thread's tracked-file
// sidecar (files.json).
func ThreadFilesRel(threadID string) string {
	return threadsDirName + "/" + threadID + "/" + threadFilesFileName
}

// ProjectMetaRel is the repo-relative path of a project's meta.json.
func ProjectMetaRel(projectID string) string {
	return projectsDirName + "/" + projectID + "/" + ProjectMetaFileName
}
