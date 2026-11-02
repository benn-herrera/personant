package dedup

import (
	"cmp"
	"slices"
	"time"
)

// FileChange is one id transition of a tracked path.
type FileChange struct {
	Turn     int
	At       time.Time
	From, To string
	Diff     bool // whether the diff was kept at write time
	Size     int  // the kept diff's length in bytes; 0 when Diff is false
}

// FileEntry is one tracked path of a thread: its current content and its
// most recent changes, oldest first.
type FileEntry struct {
	Path    string
	ID      string
	Size    int // length in bytes of the current content
	Turn    int // excerpt number of the path's latest read or write
	At      time.Time
	Changes []FileChange
}

// PlanThread is one Layer B thread's input to [Plan].
type PlanThread struct {
	ID      string
	Project string
	Entries []FileEntry
}

// ExpansionKind is what a planned expansion renders.
type ExpansionKind string

const (
	ExpansionLiteral ExpansionKind = "literal"
	ExpansionDiff    ExpansionKind = "diff"
)

// Expansion is one planned rendering of file content at an excerpt.
type Expansion struct {
	ThreadID string
	Turn     int
	Project  string
	Path     string
	Kind     ExpansionKind
	ID       string // literal only
	From, To string // diff only
	Size     int    // the expansion's own byte size
	PathSize int    // the path's current size
}

// ExcerptPosition names one excerpt: a thread and its turn number.
type ExcerptPosition struct {
	ThreadID string
	Turn     int
}

type projectPath struct{ project, path string }

// placedEntry is an entry with its thread's index in Layer B order.
type placedEntry struct {
	rank   int
	thread *PlanThread
	entry  *FileEntry
}

// placedChange is a change with where it sits, for recency ordering.
type placedChange struct {
	rank   int
	thread *PlanThread
	index  int // position within its entry's Changes
	change *FileChange
}

// Plan decides which excerpt positions expand file content, across the
// Layer B threads given in Layer B order. literalMax is
// ⌈LiteralShareThreshold × the per-thread share⌉, in bytes.
//
// Per (project, path), the most recent entry is the one with the latest
// At; on equal At, the earlier thread wins. A path whose most recent size
// is at or above literalMax gets no expansion. Otherwise it gets a literal
// candidate at that entry's turn, even when another path's current content
// has the same id: which of those renders is the composition walk's
// decision. Each path under the gate also gets diffs for those of its
// LiveDiffWindow most recent changes, across all threads, that were kept
// and are below DiffLiteralThreshold × the path's current size; a change
// dropped from the window is not replaced by an older one. Recency among
// changes is At, then the earlier thread, then the later turn and later
// index.
//
// Within a position, expansions are ordered by path, and per path its
// diffs, oldest first, precede its literal.
func Plan(threads []PlanThread, literalMax int) map[ExcerptPosition][]Expansion {
	var keys []projectPath
	latest := map[projectPath]placedEntry{}
	all := map[projectPath][]placedEntry{}
	for ti := range threads {
		t := &threads[ti]
		for ei := range t.Entries {
			p := placedEntry{ti, t, &t.Entries[ei]}
			k := projectPath{t.Project, p.entry.Path}
			cur, seen := latest[k]
			if !seen {
				keys = append(keys, k)
			}
			if !seen || p.entry.At.After(cur.entry.At) {
				latest[k] = p
			}
			all[k] = append(all[k], p)
		}
	}

	plan := map[ExcerptPosition][]Expansion{}
	add := func(e Expansion) {
		pos := ExcerptPosition{e.ThreadID, e.Turn}
		plan[pos] = append(plan[pos], e)
	}
	for _, k := range keys {
		cur := latest[k]
		if cur.entry.Size >= literalMax {
			continue
		}
		for _, c := range recentDiffs(all[k], cur.entry.Size) {
			add(Expansion{
				ThreadID: c.thread.ID, Turn: c.change.Turn, Project: k.project, Path: k.path,
				Kind: ExpansionDiff, From: c.change.From, To: c.change.To,
				Size: c.change.Size, PathSize: cur.entry.Size,
			})
		}
		add(Expansion{
			ThreadID: cur.thread.ID, Turn: cur.entry.Turn, Project: k.project, Path: k.path,
			Kind: ExpansionLiteral, ID: cur.entry.ID,
			Size: cur.entry.Size, PathSize: cur.entry.Size,
		})
	}
	for _, exps := range plan {
		slices.SortStableFunc(exps, func(a, b Expansion) int {
			return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)))
		})
	}
	return plan
}

func kindOrder(k ExpansionKind) int {
	if k == ExpansionDiff {
		return 0
	}
	return 1
}

// recentDiffs returns, oldest first, those of one (project, path)'s
// LiveDiffWindow most recent changes that were kept and are below
// DiffLiteralThreshold × currentSize.
func recentDiffs(entries []placedEntry, currentSize int) []placedChange {
	var changes []placedChange
	for _, p := range entries {
		for i := range p.entry.Changes {
			changes = append(changes, placedChange{p.rank, p.thread, i, &p.entry.Changes[i]})
		}
	}
	slices.SortFunc(changes, func(a, b placedChange) int {
		return cmp.Or(
			a.change.At.Compare(b.change.At),
			cmp.Compare(b.rank, a.rank),
			cmp.Compare(a.change.Turn, b.change.Turn),
			cmp.Compare(a.index, b.index),
		)
	})
	return slices.DeleteFunc(changes[max(0, len(changes)-LiveDiffWindow):], func(c placedChange) bool {
		return !c.change.Diff || float64(c.change.Size) >= DiffLiteralThreshold*float64(currentSize)
	})
}
