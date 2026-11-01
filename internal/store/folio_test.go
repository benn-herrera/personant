package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"personant/internal/crashpoint"
	"personant/internal/memops"
)

const folioTestThread = "thr_1"

func writeLooseExcerpt(t *testing.T, paths PersonantPaths, name, content string) {
	t.Helper()
	dir := ThreadTurnsDir(paths, folioTestThread)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// excerptTiers returns the thread's loose files and folio lines as name → bytes.
func excerptTiers(t *testing.T, paths PersonantPaths) (loose, folio map[string]string) {
	t.Helper()
	loose, folio = map[string]string{}, map[string]string{}
	entries, err := os.ReadDir(ThreadTurnsDir(paths, folioTestThread))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(ThreadTurnsDir(paths, folioTestThread), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		loose[e.Name()] = string(data)
	}
	lines, err := ReadFolio(ThreadFolioPath(paths, folioTestThread))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, l := range lines {
		folio[l.Name] = *l.Content
	}
	return loose, folio
}

// assertEachReadableOnce checks that every name in want is held by at least
// one tier, every held copy has want's bytes, and no other name is held.
func assertEachReadableOnce(t *testing.T, paths PersonantPaths, want map[string]string) {
	t.Helper()
	loose, folio := excerptTiers(t, paths)
	for name, content := range want {
		l, inLoose := loose[name]
		f, inFolio := folio[name]
		if !inLoose && !inFolio {
			t.Errorf("%s is in neither tier", name)
		}
		if inLoose && l != content || inFolio && f != content {
			t.Errorf("%s: a held copy differs from the original bytes", name)
		}
	}
	for name := range loose {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected loose file %s", name)
		}
	}
	for name := range folio {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected folio line %s", name)
		}
	}
}

type readerView struct {
	Body, BudgetBody string
	Excerpts         []memops.ThreadExcerpt
	Debt, WideDebt   []memops.ThreadExcerpt
}

func readThroughReaders(t *testing.T, paths PersonantPaths) readerView {
	t.Helper()
	var v readerView
	var err error
	if v.Body, err = ReadThreadBody(paths, folioTestThread, 0); err != nil {
		t.Fatalf("ReadThreadBody: %v", err)
	}
	if v.BudgetBody, err = ReadThreadBody(paths, folioTestThread, 4000); err != nil {
		t.Fatalf("ReadThreadBody budget: %v", err)
	}
	if v.Excerpts, err = ReadThreadExcerpts(paths, folioTestThread); err != nil {
		t.Fatalf("ReadThreadExcerpts: %v", err)
	}
	if v.Debt, err = ReadDebtWindowExcerpts(paths, folioTestThread, memops.EmbeddingDebtCap); err != nil {
		t.Fatalf("ReadDebtWindowExcerpts: %v", err)
	}
	if v.WideDebt, err = ReadDebtWindowExcerpts(paths, folioTestThread, 100); err != nil {
		t.Fatalf("ReadDebtWindowExcerpts wide: %v", err)
	}
	return v
}

func assertSameReads(t *testing.T, stage string, got, want readerView) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: readers returned different results than before any move", stage)
	}
}

func excerptNumbers(t *testing.T, paths PersonantPaths) []int {
	t.Helper()
	entries, err := ListExcerptEntries(paths, folioTestThread)
	if err != nil {
		t.Fatalf("ListExcerptEntries: %v", err)
	}
	var nums []int
	for _, e := range entries {
		nums = append(nums, e.Number)
	}
	return nums
}

var folioContentFragments = []string{
	`{"name":"0000001.md","content":"looks like a folio line"}` + "\n",
	"---\nid: thr_9\n---\n",
	"## Turn 3 · 2026-05-08T03:12:00-07:00 · [a, b]\n\n",
	"**user:** crlf\r\nline\r\n",
	"line\xe2\x80\xa8separator (U+2028)",
	"paragraph\xe2\x80\xa9separator (U+2029)",
	"nul\x00byte",
	"<script>&amp;</script>",
	"plain words ",
	"\n\n",
}

func generateExcerpt(rng *rand.Rand) string {
	var b strings.Builder
	for range rng.IntN(5) {
		b.WriteString(folioContentFragments[rng.IntN(len(folioContentFragments))])
	}
	if rng.IntN(2) == 0 {
		b.WriteString("\n")
	}
	return b.String()
}

// TestFolioRoundTrip: binding then unbinding gives back the same names and
// bytes, and in every intermediate layout the readers return what they
// returned before anything moved.
func TestFolioRoundTrip(t *testing.T) {
	cases := []struct {
		seed  uint64
		count int
	}{
		{1, 3}, {2, 40}, {3, 100}, {4, HotExcerptTail + 2},
	}
	for _, tc := range cases {
		t.Run(strconv.FormatUint(tc.seed, 10), func(t *testing.T) {
			paths := newThreadHome(t)
			rng := rand.New(rand.NewPCG(tc.seed, tc.seed))
			want := map[string]string{
				"0000005.md": "padded name sharing turn 5\n",
				"5.md":       "unpadded name sharing turn 5",
				"2.md":       "",
				"0000003.md": strings.Repeat("multi-megabyte ", 200_000),
			}
			for n := 6; n < tc.count+6; n++ {
				name := turnFileName(n)
				if rng.IntN(10) == 0 {
					name = strconv.Itoa(n) + ".md"
				}
				want[name] = generateExcerpt(rng)
			}
			for name, content := range want {
				writeLooseExcerpt(t, paths, name, content)
			}
			before := readThroughReaders(t, paths)
			all := excerptNumbers(t, paths)

			out, err := bindExcerpts(paths, folioTestThread, all)
			if err != nil {
				t.Fatalf("bind: %v", err)
			}
			if wantSkip := []FolioSkip{{"0000003.md", FolioSkipTooLarge}}; !reflect.DeepEqual(out.Skipped, wantSkip) {
				t.Errorf("bind skipped %v, want %v", out.Skipped, wantSkip)
			}
			if out.Bound != len(want)-1 {
				t.Errorf("bound %d, want %d", out.Bound, len(want)-1)
			}
			loose, folio := excerptTiers(t, paths)
			if len(loose) != 1 || len(folio) != len(want)-1 {
				t.Errorf("after bind: %d loose, %d in folio; want 1 and %d", len(loose), len(folio), len(want)-1)
			}
			assertEachReadableOnce(t, paths, want)
			assertSameReads(t, "after bind", readThroughReaders(t, paths), before)

			if _, err := unbindExcerpts(paths, folioTestThread, all[len(all)/2:]); err != nil {
				t.Fatalf("partial unbind: %v", err)
			}
			assertEachReadableOnce(t, paths, want)
			assertSameReads(t, "after partial unbind", readThroughReaders(t, paths), before)

			if _, err := unbindExcerpts(paths, folioTestThread, all); err != nil {
				t.Fatalf("unbind: %v", err)
			}
			loose, folio = excerptTiers(t, paths)
			if !reflect.DeepEqual(loose, want) {
				t.Errorf("after unbind the loose files differ from the originals")
			}
			if len(folio) != 0 {
				t.Errorf("after unbind the folio holds %d lines", len(folio))
			}
			if _, err := os.Stat(ThreadFolioPath(paths, folioTestThread)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("empty folio file left behind: %v", err)
			}
			assertSameReads(t, "after unbind", readThroughReaders(t, paths), before)
		})
	}
}

// TestFolioReadFolio: lines come back in file order; each malformed kind
// fails with its 1-based line number.
func TestFolioReadFolio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folio.jsonl")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(`{"name":"0000002.md","content":"b"}` + "\n" +
		`{"name":"0000001.md","content":"","extra":1}` + "\n")
	got, err := ReadFolio(path)
	if err != nil {
		t.Fatalf("ReadFolio: %v", err)
	}
	if len(got) != 2 || got[0].Name != "0000002.md" || *got[0].Content != "b" ||
		got[1].Name != "0000001.md" || *got[1].Content != "" {
		t.Errorf("ReadFolio = %+v, want the two lines in file order", got)
	}

	good := `{"name":"0000001.md","content":"a"}` + "\n\n"
	for _, tc := range []struct{ name, line string }{
		{"not JSON", `{"name":`},
		{"name missing", `{"content":"x"}`},
		{"name not an excerpt name", `{"name":"notes.md","content":"x"}`},
		{"name zero", `{"name":"0.md","content":"x"}`},
		{"name signed", `{"name":"+3.md","content":"x"}`},
		{"content missing", `{"name":"0000003.md"}`},
		{"content null", `{"name":"0000003.md","content":null}`},
		{"content not a string", `{"name":"0000003.md","content":5}`},
		{"null line", `null`},
		{"trailing content", `{"name":"0000003.md","content":"x"} {}`},
		{"over the line cap", `{"name":"0000003.md","content":"` + strings.Repeat("x", jsonlMaxLineBytes) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(good + tc.line + "\n")
			_, err := ReadFolio(path)
			if err == nil || !strings.Contains(err.Error(), "folio.jsonl:3:") {
				t.Errorf("ReadFolio error = %v, want one naming line 3", err)
			}
		})
	}
}

// TestFolioPlacement: a warm thread keeps its newest HotExcerptTail loose;
// a retired thread binds everything; re-warming unbinds the newest again.
func TestFolioPlacement(t *testing.T) {
	for _, tc := range []struct {
		n       int
		retired bool
		want    int
	}{
		{0, false, 0}, {HotExcerptTail, false, 0}, {HotExcerptTail + 1, false, 1},
		{0, true, 0}, {3, true, 3},
	} {
		if got := folioPlacementCount(tc.n, tc.retired); got != tc.want {
			t.Errorf("folioPlacementCount(%d, %v) = %d, want %d", tc.n, tc.retired, got, tc.want)
		}
	}

	paths := newThreadHome(t)
	total := HotExcerptTail + 5
	want := map[string]string{}
	for n := 1; n <= total; n++ {
		want[turnFileName(n)] = turnExcerpt(n)
		writeLooseExcerpt(t, paths, turnFileName(n), turnExcerpt(n))
	}
	assertWarmLayout := func(stage string) {
		t.Helper()
		loose, folio := excerptTiers(t, paths)
		for n := 1; n <= total; n++ {
			_, inLoose := loose[turnFileName(n)]
			_, inFolio := folio[turnFileName(n)]
			if wantLoose := n > 5; inLoose != wantLoose || inFolio == wantLoose {
				t.Fatalf("%s: turn %d loose=%v folio=%v, want loose=%v", stage, n, inLoose, inFolio, wantLoose)
			}
		}
	}
	settle := func(retired bool, wantOut SettleOutcome) {
		t.Helper()
		out, err := SettleExcerptTiers(paths, folioTestThread, retired)
		if err != nil {
			t.Fatalf("settle retired=%v: %v", retired, err)
		}
		if !reflect.DeepEqual(out, wantOut) {
			t.Fatalf("settle retired=%v = %+v, want %+v", retired, out, wantOut)
		}
		assertEachReadableOnce(t, paths, want)
	}

	settle(false, SettleOutcome{Bound: 5})
	assertWarmLayout("warm")
	settle(false, SettleOutcome{})

	settle(true, SettleOutcome{Bound: HotExcerptTail})
	if _, err := os.Stat(ThreadTurnsDir(paths, folioTestThread)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("retired thread kept its turns/ directory: %v", err)
	}
	settle(true, SettleOutcome{})

	settle(false, SettleOutcome{Unbound: HotExcerptTail})
	assertWarmLayout("re-warmed")
}

// TestFolioSettleCrashConverges: a crash at each settle crashpoint leaves
// every excerpt readable with its bytes, and the next settle finishes the
// moves.
func TestFolioSettleCrashConverges(t *testing.T) {
	for _, tc := range []struct {
		point  string
		unbind bool
	}{
		{cpFolioMidLooseWrite, true},
		{cpFolioPostLooseWrite, true},
		{cpFolioPostFolioWrite, true},
		{cpFolioPostFolioWrite, false},
		{cpFolioMidLooseRemove, false},
	} {
		name := tc.point + "/bind"
		if tc.unbind {
			name = tc.point + "/unbind"
		}
		t.Run(name, func(t *testing.T) {
			paths := newThreadHome(t)
			want := map[string]string{}
			for n := 1; n <= 4; n++ {
				want[turnFileName(n)] = turnExcerpt(n)
				writeLooseExcerpt(t, paths, turnFileName(n), turnExcerpt(n))
			}
			if tc.unbind {
				if _, err := SettleExcerptTiers(paths, folioTestThread, true); err != nil {
					t.Fatalf("bind fixture: %v", err)
				}
			}
			before := readThroughReaders(t, paths)
			retired := !tc.unbind

			disarm := crashpoint.Arm(tc.point)
			recoverCrash(t, tc.point, func() {
				_, _ = SettleExcerptTiers(paths, folioTestThread, retired)
			})
			disarm()
			assertEachReadableOnce(t, paths, want)
			assertSameReads(t, "at the crash", readThroughReaders(t, paths), before)

			if _, err := SettleExcerptTiers(paths, folioTestThread, retired); err != nil {
				t.Fatalf("re-settle: %v", err)
			}
			assertEachReadableOnce(t, paths, want)
			loose, folio := excerptTiers(t, paths)
			if tc.unbind && (len(loose) != len(want) || len(folio) != 0) ||
				!tc.unbind && (len(loose) != 0 || len(folio) != len(want)) {
				t.Errorf("after re-settle: %d loose, %d in folio", len(loose), len(folio))
			}
			if out, err := SettleExcerptTiers(paths, folioTestThread, retired); err != nil || out.Bound+out.Unbound != 0 {
				t.Errorf("third settle = %+v, %v; want no moves", out, err)
			}
		})
	}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(p)
		out[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestFolioSettleFailsOnInvalidUTF8: an excerpt to be bound that is not
// valid UTF-8 fails the settle, names the file, and nothing is written.
func TestFolioSettleFailsOnInvalidUTF8(t *testing.T) {
	paths := newThreadHome(t)
	writeLooseExcerpt(t, paths, "0000001.md", turnExcerpt(1))
	writeLooseExcerpt(t, paths, "0000002.md", "valid then \xff invalid\n")
	writeLooseExcerpt(t, paths, "0000003.md", turnExcerpt(3))
	dir := ThreadDir(paths, folioTestThread)
	before := snapshotTree(t, dir)

	_, err := SettleExcerptTiers(paths, folioTestThread, true)
	if !errors.Is(err, ErrExcerptNotUTF8) {
		t.Fatalf("settle error = %v, want ErrExcerptNotUTF8", err)
	}
	if !strings.Contains(err.Error(), "threads/thr_1/turns/0000002.md") {
		t.Errorf("settle error %q does not name the file", err)
	}
	if after := snapshotTree(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("thread directory changed: before %v, after %v", before, after)
	}
}

// TestFolioSettleSkips: the too-large boundary sits exactly at the JSONL line
// cap; a name in both tiers with different bytes and a malformed folio move
// nothing.
func TestFolioSettleSkips(t *testing.T) {
	t.Run("too-large at the line cap", func(t *testing.T) {
		paths := newThreadHome(t)
		empty := ""
		base, err := json.Marshal(FolioLine{Name: "0000001.md", Content: &empty})
		if err != nil {
			t.Fatal(err)
		}
		fits := strings.Repeat("a", jsonlMaxLineBytes-1-len(base))
		writeLooseExcerpt(t, paths, "0000001.md", fits)
		writeLooseExcerpt(t, paths, "0000002.md", fits+"a")

		out, err := SettleExcerptTiers(paths, folioTestThread, true)
		if err != nil {
			t.Fatal(err)
		}
		want := SettleOutcome{Bound: 1, Skipped: []FolioSkip{{"0000002.md", FolioSkipTooLarge}}}
		if !reflect.DeepEqual(out, want) {
			t.Errorf("settle = %+v, want %+v", out, want)
		}
		assertEachReadableOnce(t, paths, map[string]string{"0000001.md": fits, "0000002.md": fits + "a"})
	})

	t.Run("conflict", func(t *testing.T) {
		paths := newThreadHome(t)
		writeLooseExcerpt(t, paths, "0000001.md", "bound bytes")
		writeLooseExcerpt(t, paths, "0000002.md", turnExcerpt(2))
		if _, err := SettleExcerptTiers(paths, folioTestThread, true); err != nil {
			t.Fatal(err)
		}
		writeLooseExcerpt(t, paths, "0000001.md", "hand-edited bytes")

		out, err := SettleExcerptTiers(paths, folioTestThread, false)
		if err != nil {
			t.Fatal(err)
		}
		want := SettleOutcome{Unbound: 1, Skipped: []FolioSkip{{"0000001.md", FolioSkipConflict}}}
		if !reflect.DeepEqual(out, want) {
			t.Errorf("settle = %+v, want %+v", out, want)
		}
		loose, folio := excerptTiers(t, paths)
		if loose["0000001.md"] != "hand-edited bytes" || folio["0000001.md"] != "bound bytes" {
			t.Errorf("conflicting copies changed: loose %q, folio %q", loose["0000001.md"], folio["0000001.md"])
		}
		got, err := ReadThreadExcerpts(paths, folioTestThread)
		if err != nil || len(got) != 2 || got[0].Text != "hand-edited bytes" {
			t.Errorf("ReadThreadExcerpts = %+v, %v; want the loose copy of turn 1 first", got, err)
		}
	})

	t.Run("unreadable folio", func(t *testing.T) {
		paths := newThreadHome(t)
		writeLooseExcerpt(t, paths, "0000002.md", turnExcerpt(2))
		if err := os.WriteFile(ThreadFolioPath(paths, folioTestThread), []byte("not json\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := ThreadDir(paths, folioTestThread)
		before := snapshotTree(t, dir)

		out, err := SettleExcerptTiers(paths, folioTestThread, true)
		if err != nil {
			t.Fatal(err)
		}
		want := SettleOutcome{Skipped: []FolioSkip{{threadFolioFileName, FolioSkipUnreadable}}}
		if !reflect.DeepEqual(out, want) {
			t.Errorf("settle = %+v, want %+v", out, want)
		}
		if after := snapshotTree(t, dir); !reflect.DeepEqual(after, before) {
			t.Errorf("thread directory changed")
		}
	})
}
