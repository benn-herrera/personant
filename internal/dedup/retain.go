package dedup

// DiffRef names one kept diff by its id transition.
type DiffRef struct{ From, To string }

// Retained is the keep set of one thread's tracked files.
type Retained struct {
	Entries []FileEntry // kept entries, in input order
	Blobs   map[string]bool
	Diffs   map[DiffRef]bool
}

// Retain returns what a thread keeps: the entries whose Turn is within
// the last window turns (Turn > turnCount − window), the content ids they
// name, and the diffs of their changes that were kept.
func Retain(entries []FileEntry, turnCount, window int) Retained {
	r := Retained{Blobs: map[string]bool{}, Diffs: map[DiffRef]bool{}}
	for _, e := range entries {
		if e.Turn <= turnCount-window {
			continue
		}
		r.Entries = append(r.Entries, e)
		r.Blobs[e.ID] = true
		for _, c := range e.Changes {
			if c.Diff {
				r.Diffs[DiffRef{c.From, c.To}] = true
			}
		}
	}
	return r
}
