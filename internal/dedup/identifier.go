package dedup

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
)

// ContentID returns the content identifier of data: its 40-hex git blob
// id, so any git repository holding those bytes resolves it.
func ContentID(data []byte) string {
	return plumbing.ComputeHash(plumbing.BlobObject, data).String()
}

// FileEventKind is the operation an identifier line records.
type FileEventKind string

const (
	FileRead   FileEventKind = "read"
	FileWrite  FileEventKind = "write"
	FileCommit FileEventKind = "commit"
)

// IdentifierLine renders one file event as its excerpt identifier line,
// without a trailing newline. For FileRead and FileWrite, id is a content
// id and renders as "#" plus its first 12 hex; for FileCommit, id is the
// commit hash and renders as "@" plus its first 7. The path is the
// remainder of the line, verbatim.
func IdentifierLine(kind FileEventKind, id, path string) string {
	mark, n := '#', 12
	if kind == FileCommit {
		mark, n = '@', 7
	}
	return fmt.Sprintf("[file %s %c%s] %s", kind, mark, id[:min(n, len(id))], path)
}
