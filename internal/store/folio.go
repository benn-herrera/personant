package store

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"personant/internal/crashpoint"
	"personant/internal/memops"
)

// A thread's turn excerpts live in two tiers: loose files under turns/
// (warm), and one folio.jsonl per thread (cold), one line per excerpt.
// Readers see the union, the loose copy winning for a name in both tiers.
// Only SettleExcerptTiers moves excerpts between tiers.

// HotExcerptTail is how many of a live thread's newest excerpts stay loose:
// the assembly window plus the steady-state embedding-debt window below it,
// so neither window's reader opens the folio.
const HotExcerptTail = ThreadTurnWindow + memops.EmbeddingDebtCap

// Settle crashpoints. Each fires only for a thread whose settle has work in
// that step.
var (
	cpFolioMidLooseWrite  = crashpoint.Register("folio.settle.midLooseWrite")
	cpFolioPostLooseWrite = crashpoint.Register("folio.settle.postLooseWrite.preFolioWrite")
	cpFolioPostFolioWrite = crashpoint.Register("folio.settle.postFolioWrite.preLooseRemove")
	cpFolioMidLooseRemove = crashpoint.Register("folio.settle.midLooseRemove")
)

// ErrExcerptNotUTF8 is returned by SettleExcerptTiers when an excerpt it
// would bind is not valid UTF-8. JSON encoding would silently replace the
// invalid bytes, so the excerpt is never bound and the settle fails.
var ErrExcerptNotUTF8 = errors.New("excerpt is not valid UTF-8")

// ThreadFolioPath returns the path of a thread's excerpt folio:
// <Home>/threads/thr_<n>/folio.jsonl.
func ThreadFolioPath(paths PersonantPaths, threadID string) string {
	return filepath.Join(ThreadDir(paths, threadID), threadFolioFileName)
}

// ExcerptTier names where an excerpt is held.
type ExcerptTier int

// Declared in listing order: for the same name the folio entry sorts first.
const (
	ExcerptFolio ExcerptTier = iota + 1
	ExcerptLoose
)

// ExcerptEntry is one excerpt in one tier.
type ExcerptEntry struct {
	Number int // the turn number parsed from Name
	Name   string
	Tier   ExcerptTier
}

func compareExcerptEntries(a, b ExcerptEntry) int {
	return cmp.Or(cmp.Compare(a.Number, b.Number), strings.Compare(a.Name, b.Name), cmp.Compare(a.Tier, b.Tier))
}

// parseExcerptName returns the turn number of an excerpt name, <digits>.md
// with a positive value, and whether name is one.
func parseExcerptName(name string) (int, bool) {
	stem, ok := strings.CutSuffix(name, ".md")
	if !ok || stem == "" {
		return 0, false
	}
	for i := 0; i < len(stem); i++ {
		if stem[i] < '0' || stem[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(stem)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// FolioLine is one folio line: an excerpt's file name and its exact bytes.
// Decoding rejects a line whose name is not an excerpt name or whose content
// is absent or null.
type FolioLine struct {
	Name    string  `json:"name"`
	Content *string `json:"content"`
}

// UnmarshalJSON decodes a line and enforces the folio line rules.
func (l *FolioLine) UnmarshalJSON(data []byte) error {
	type plain FolioLine
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if _, ok := parseExcerptName(p.Name); !ok {
		return fmt.Errorf("folio line: name %q is not an excerpt name", p.Name)
	}
	if p.Content == nil {
		return fmt.Errorf("folio line %s: content missing", p.Name)
	}
	*l = FolioLine(p)
	return nil
}

// folioLineKey sorts folio lines by (turn number, name). The pad covers
// every positive int, so the lexical order is the numeric one.
func folioLineKey(l FolioLine) string {
	n, _ := parseExcerptName(l.Name)
	return fmt.Sprintf("%019d %s", n, l.Name)
}

// folioLineFits reports whether l, encoded, stays within ReadJSONL's line cap.
func folioLineFits(l FolioLine) (bool, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return false, err
	}
	return len(b)+1 <= jsonlMaxLineBytes, nil
}

// ReadFolio reads a folio file's lines in file order, without re-sorting or
// checking order and uniqueness. The first malformed line fails the read
// with an error naming its 1-based line number, so a nil error means every
// Name is an excerpt name and every Content is non-nil. A missing file is
// an error wrapping os.ErrNotExist.
func ReadFolio(path string) ([]FolioLine, error) {
	return ReadJSONL[FolioLine](path)
}

// readThreadFolio reads a thread's folio; a thread without one returns
// (nil, nil). A repeated name is an error.
func readThreadFolio(paths PersonantPaths, threadID string) ([]FolioLine, error) {
	path := ThreadFolioPath(paths, threadID)
	lines, err := ReadFolio(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(lines))
	for _, l := range lines {
		if seen[l.Name] {
			return nil, fmt.Errorf("%s: repeated excerpt name %s", path, l.Name)
		}
		seen[l.Name] = true
	}
	return lines, nil
}

// listLooseExcerpts returns the loose excerpts in turnsDir, sorted. A
// missing directory returns (nil, nil); files that are not excerpt names
// are ignored.
func listLooseExcerpts(turnsDir string) ([]ExcerptEntry, error) {
	entries, err := os.ReadDir(turnsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]ExcerptEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n, ok := parseExcerptName(e.Name()); ok {
			out = append(out, ExcerptEntry{Number: n, Name: e.Name(), Tier: ExcerptLoose})
		}
	}
	slices.SortFunc(out, compareExcerptEntries)
	return out, nil
}

// ListExcerptEntries lists a thread's excerpts in both tiers, one entry per
// (name, tier), so a name held in both tiers appears twice. Entries sort by
// (number, name), the folio entry first for the same name. A malformed
// folio is an error.
func ListExcerptEntries(paths PersonantPaths, threadID string) ([]ExcerptEntry, error) {
	out, err := listLooseExcerpts(ThreadTurnsDir(paths, threadID))
	if err != nil {
		return nil, fmt.Errorf("list excerpts %s: %w", threadID, err)
	}
	folio, err := readThreadFolio(paths, threadID)
	if err != nil {
		return nil, fmt.Errorf("list excerpts %s: %w", threadID, err)
	}
	for _, l := range folio {
		n, _ := parseExcerptName(l.Name)
		out = append(out, ExcerptEntry{Number: n, Name: l.Name, Tier: ExcerptFolio})
	}
	slices.SortFunc(out, compareExcerptEntries)
	return out, nil
}

// excerptRef is one excerpt as readers see it: a loose file, or folio bytes
// already in memory.
type excerptRef struct {
	ExcerptEntry
	content *string // nil for a loose excerpt
}

// readableExcerpts merges the tiers into what readers see, sorted: one ref
// per name, the loose copy for a name in both tiers.
func readableExcerpts(loose []ExcerptEntry, folio []FolioLine) []excerptRef {
	refs := make([]excerptRef, 0, len(loose)+len(folio))
	for _, e := range loose {
		refs = append(refs, excerptRef{ExcerptEntry: e})
	}
	if len(folio) == 0 {
		return refs
	}
	isLoose := make(map[string]bool, len(loose))
	for _, e := range loose {
		isLoose[e.Name] = true
	}
	for _, l := range folio {
		if isLoose[l.Name] {
			continue
		}
		n, _ := parseExcerptName(l.Name)
		refs = append(refs, excerptRef{ExcerptEntry{Number: n, Name: l.Name, Tier: ExcerptFolio}, l.Content})
	}
	slices.SortFunc(refs, func(a, b excerptRef) int { return compareExcerptEntries(a.ExcerptEntry, b.ExcerptEntry) })
	return refs
}

// threadExcerptRefs returns a thread's excerpts as readers see them, enough
// of them that the newest need are right. Settle leaves a thread's newest
// HotExcerptTail loose and turns append loose, so when need fits within
// both that tail and the loose count the folio is not read. A shorter loose
// list proves nothing: an excerpt too large to bind, or one in conflict,
// stays loose below the folio's entries.
func threadExcerptRefs(paths PersonantPaths, threadID string, need int) ([]excerptRef, error) {
	loose, err := listLooseExcerpts(ThreadTurnsDir(paths, threadID))
	if err != nil {
		return nil, err
	}
	if need <= min(len(loose), HotExcerptTail) {
		return readableExcerpts(loose, nil), nil
	}
	folio, err := readThreadFolio(paths, threadID)
	if err != nil {
		return nil, err
	}
	return readableExcerpts(loose, folio), nil
}

// readExcerptText returns an excerpt's text with trailing newlines trimmed.
func readExcerptText(turnsDir string, r excerptRef) (string, error) {
	if r.content != nil {
		return strings.TrimRight(*r.content, "\n"), nil
	}
	data, err := os.ReadFile(filepath.Join(turnsDir, r.Name))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// FolioSkipReason says why settle left an excerpt where it was.
type FolioSkipReason string

const (
	// FolioSkipTooLarge: the excerpt's folio line would exceed the JSONL
	// line cap, so it stays loose.
	FolioSkipTooLarge FolioSkipReason = "too-large"
	// FolioSkipConflict: the name is in both tiers with different bytes;
	// neither copy moves.
	FolioSkipConflict FolioSkipReason = "conflict"
	// FolioSkipUnreadable: the folio is malformed; nothing in the thread
	// moves.
	FolioSkipUnreadable FolioSkipReason = "unreadable"
)

// FolioSkip is one excerpt settle left in place. For FolioSkipUnreadable,
// Name is the folio's file name.
type FolioSkip struct {
	Name   string
	Reason FolioSkipReason
}

// SettleOutcome reports one thread's settle: how many excerpts moved into
// the folio and back to loose files, and what was skipped.
type SettleOutcome struct {
	Bound   int
	Unbound int
	Skipped []FolioSkip
}

// SettleExcerptTiers moves a thread's excerpts between tiers until they
// match the placement rule: a retired thread holds every excerpt in its
// folio; any other thread keeps its newest HotExcerptTail loose and the rest
// in the folio. Too-large excerpts stay loose, conflicting names do not
// move, and a malformed folio leaves the thread untouched; each is reported
// in Skipped. Every excerpt stays readable from at least one tier at every
// step, and rerunning after a crash finishes the moves.
//
// An excerpt to be bound that is not valid UTF-8 fails the call with an
// error wrapping ErrExcerptNotUTF8 and naming its home-relative path; the
// thread is checked whole before anything is written.
func SettleExcerptTiers(paths PersonantPaths, threadID string, retired bool) (SettleOutcome, error) {
	return moveExcerpts(paths, threadID, !retired, func(refs []excerptRef) []ExcerptTier {
		tiers := make([]ExcerptTier, len(refs))
		bound := folioPlacementCount(len(refs), retired)
		for i := range tiers {
			tiers[i] = ExcerptLoose
			if i < bound {
				tiers[i] = ExcerptFolio
			}
		}
		return tiers
	})
}

// folioPlacementCount is the placement rule: how many of a thread's n
// excerpts, oldest first by (number, name), belong in the folio.
func folioPlacementCount(n int, retired bool) int {
	if retired {
		return n
	}
	return max(0, n-HotExcerptTail)
}

// bindExcerpts moves the excerpts with the given turn numbers into the
// folio, leaving the rest where readers find them.
func bindExcerpts(paths PersonantPaths, threadID string, numbers []int) (SettleOutcome, error) {
	return moveExcerpts(paths, threadID, false, tiersFor(numbers, ExcerptFolio))
}

// unbindExcerpts moves the excerpts with the given turn numbers back to
// loose files, leaving the rest where readers find them.
func unbindExcerpts(paths PersonantPaths, threadID string, numbers []int) (SettleOutcome, error) {
	return moveExcerpts(paths, threadID, true, tiersFor(numbers, ExcerptLoose))
}

// tiersFor plans tier for the excerpts numbered in numbers and leaves every
// other excerpt in the tier readers find it in.
func tiersFor(numbers []int, tier ExcerptTier) func([]excerptRef) []ExcerptTier {
	return func(refs []excerptRef) []ExcerptTier {
		tiers := make([]ExcerptTier, len(refs))
		for i, r := range refs {
			tiers[i] = r.Tier
			if slices.Contains(numbers, r.Number) {
				tiers[i] = tier
			}
		}
		return tiers
	}
}

// moveExcerpts moves each of a thread's excerpts to the tier plan assigns
// it; plan receives the readable excerpts in order and returns one tier
// each. mayUnbind false lets a thread with no loose excerpts return without
// reading its folio.
//
// Order is what keeps every excerpt readable: unbound excerpts are written
// loose and fsynced before the folio drops them, and the folio holding the
// bound excerpts is written and fsynced before their loose files go.
func moveExcerpts(paths PersonantPaths, threadID string, mayUnbind bool, plan func([]excerptRef) []ExcerptTier) (SettleOutcome, error) {
	var out SettleOutcome
	fail := func(err error) (SettleOutcome, error) {
		return SettleOutcome{}, fmt.Errorf("settle excerpts %s: %w", threadID, err)
	}
	threadDir := ThreadDir(paths, threadID)
	turnsDir := ThreadTurnsDir(paths, threadID)
	loose, err := listLooseExcerpts(turnsDir)
	if err != nil {
		return fail(err)
	}
	if len(loose) == 0 && !mayUnbind {
		return out, nil
	}
	folio, err := readThreadFolio(paths, threadID)
	if err != nil {
		out.Skipped = append(out.Skipped, FolioSkip{Name: threadFolioFileName, Reason: FolioSkipUnreadable})
		return out, nil
	}
	folioCopy := make(map[string]*string, len(folio))
	for _, l := range folio {
		folioCopy[l.Name] = l.Content
	}

	refs := readableExcerpts(loose, folio)
	targets := plan(refs)
	var toLoose, bound []FolioLine // written loose; added to the folio
	var removeLoose, notUTF8 []string
	leaving := map[string]bool{} // dropped from the folio
	for i, r := range refs {
		want := targets[i]
		switch {
		case r.Tier == ExcerptLoose && folioCopy[r.Name] != nil:
			data, err := os.ReadFile(filepath.Join(turnsDir, r.Name))
			if err != nil {
				return fail(err)
			}
			if string(data) != *folioCopy[r.Name] {
				out.Skipped = append(out.Skipped, FolioSkip{Name: r.Name, Reason: FolioSkipConflict})
				continue
			}
			if want == ExcerptFolio {
				removeLoose = append(removeLoose, r.Name)
				out.Bound++
			} else {
				leaving[r.Name] = true
				out.Unbound++
			}
		case r.Tier == ExcerptLoose && want == ExcerptFolio:
			data, err := os.ReadFile(filepath.Join(turnsDir, r.Name))
			if err != nil {
				return fail(err)
			}
			content := string(data)
			line := FolioLine{Name: r.Name, Content: &content}
			fits, err := folioLineFits(line)
			if err != nil {
				return fail(err)
			}
			if !fits {
				out.Skipped = append(out.Skipped, FolioSkip{Name: r.Name, Reason: FolioSkipTooLarge})
				continue
			}
			if !utf8.Valid(data) {
				notUTF8 = append(notUTF8, threadExcerptRel(threadID, r.Name))
				continue
			}
			bound = append(bound, line)
			removeLoose = append(removeLoose, r.Name)
			out.Bound++
		case r.Tier == ExcerptFolio && want == ExcerptLoose:
			toLoose = append(toLoose, FolioLine{Name: r.Name, Content: r.content})
			leaving[r.Name] = true
			out.Unbound++
		}
	}
	if len(notUTF8) > 0 {
		return fail(fmt.Errorf("%s: %w", strings.Join(notUTF8, ", "), ErrExcerptNotUTF8))
	}

	if len(toLoose) > 0 {
		if err := os.MkdirAll(turnsDir, 0o755); err != nil {
			return fail(err)
		}
		for i, l := range toLoose {
			if i == 1 {
				crashpoint.At(cpFolioMidLooseWrite)
			}
			if err := WriteFileAtomic(filepath.Join(turnsDir, l.Name), []byte(*l.Content)); err != nil {
				return fail(err)
			}
		}
		if err := fsyncDir(turnsDir); err != nil {
			return fail(err)
		}
		if err := fsyncDir(threadDir); err != nil {
			return fail(err)
		}
		crashpoint.At(cpFolioPostLooseWrite)
	}

	if len(bound) > 0 || len(leaving) > 0 {
		next := make([]FolioLine, 0, len(folio)+len(bound))
		for _, l := range folio {
			if !leaving[l.Name] {
				next = append(next, l)
			}
		}
		next = append(next, bound...)
		if err := writeThreadFolio(ThreadFolioPath(paths, threadID), next); err != nil {
			return fail(err)
		}
		if err := fsyncDir(threadDir); err != nil {
			return fail(err)
		}
		crashpoint.At(cpFolioPostFolioWrite)
	}

	if len(removeLoose) > 0 {
		for i, name := range removeLoose {
			if i == 1 {
				crashpoint.At(cpFolioMidLooseRemove)
			}
			if err := os.Remove(filepath.Join(turnsDir, name)); err != nil {
				return fail(err)
			}
		}
		rest, err := os.ReadDir(turnsDir)
		if err != nil {
			return fail(err)
		}
		if len(rest) == 0 {
			if err := os.Remove(turnsDir); err != nil {
				return fail(err)
			}
		}
	}
	return out, nil
}

// writeThreadFolio replaces the folio at path with lines, or removes it when
// lines is empty, then reads it back to confirm it holds exactly lines.
func writeThreadFolio(path string, lines []FolioLine) error {
	if len(lines) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	slices.SortFunc(lines, func(a, b FolioLine) int { return strings.Compare(folioLineKey(a), folioLineKey(b)) })
	if err := WriteJSONL(path, lines, folioLineKey); err != nil {
		return err
	}
	got, err := ReadFolio(path)
	if err != nil {
		return fmt.Errorf("read back: %w", err)
	}
	same := slices.EqualFunc(got, lines, func(a, b FolioLine) bool {
		return a.Name == b.Name && *a.Content == *b.Content
	})
	if !same {
		return fmt.Errorf("read back %s: folio does not hold the lines written", path)
	}
	return nil
}
