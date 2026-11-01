package autogit

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"personant/internal/clock"
	"personant/internal/store"
)

// BenchmarkTurnCommit times the per-turn daily commit CommitTurn makes —
// AddPaths of the turn's write set, then Commit — against synthetic daily
// repos of N tracked entries in the home layout. One turn writes k=4 files:
// a new turns/ excerpt and thread.md in one thread, spine.jsonl, and an
// append to the day's log. The tracked set is one main thread holding most
// of the turn excerpts plus benchSmallThreads short threads, the shape a
// long run's daily index takes.
//
// Variants: "addpaths" is the production path; "addpaths-side" is the
// production path with the turn landing in a short thread, so the main
// thread's wide turns/ tree is unchanged. Metrics per op: stage-ms
// (AddPaths), commit-ms (Commit), and index-load-ms / index-save-ms (one
// standalone Storer.Index / SetIndex, untimed by ns/op). ns/op covers
// stage + commit only.
func BenchmarkTurnCommit(b *testing.B) {
	variants := []struct {
		name   string
		thread string
	}{
		{"addpaths", benchMainThread},
		{"addpaths-side", "threads/thr_s000"},
	}
	for _, v := range variants {
		for _, n := range []int{1000, 4000, 16000} {
			b.Run(fmt.Sprintf("%s/N=%d", v.name, n), func(b *testing.B) {
				benchTurnCommit(b, n, v.thread)
			})
		}
	}
}

const (
	benchSmallThreads  = 100
	benchSmallTurns    = 6
	benchSpineBytes    = 128 << 10
	benchLogBytes      = 300 << 10
	benchLogAppend     = 600
	benchThreadMDBytes = 6 << 10
	benchTurnBytes     = 270
	benchMainThread    = "threads/thr_main"
	benchLogRel        = "logs/2026-05-12.log"
)

var benchMiscFiles = []string{
	"README.md", "config.toml", "providers.toml", "version.toml", ".gitignore",
	"directives/defaults.md", "directives/user.md", "projects/prj_1/meta.json",
	"projects/prj_1/digest.json", "archive/index.jsonl",
}

func benchTurnCommit(b *testing.B, n int, thread string) {
	ctx := context.Background()
	h := newTurnBenchHome(b, n)
	var load, save, stage, commit time.Duration
	for b.Loop() {
		b.StopTimer()
		rels := h.writeTurn(b, thread)
		l, s := probeIndexRoundTrip(b, h.paths)
		load += l
		save += s
		b.StartTimer()

		t0 := clock.Profiling()
		err := AddPaths(ctx, h.paths, Daily, rels)
		stage += clock.Since(t0)
		if err != nil {
			b.Fatalf("stage: %v", err)
		}
		t1 := clock.Profiling()
		if err := Commit(ctx, h.paths, Daily, TurnCommitMessage(fmt.Sprintf("t%d", h.nextTurn), ""), 0, 0); err != nil {
			b.Fatalf("commit: %v", err)
		}
		commit += clock.Since(t1)
	}
	perOp := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 / float64(b.N) }
	b.ReportMetric(perOp(stage), "stage-ms/op")
	b.ReportMetric(perOp(commit), "commit-ms/op")
	b.ReportMetric(perOp(load), "index-load-ms/op")
	b.ReportMetric(perOp(save), "index-save-ms/op")
	if fi, err := os.Stat(filepath.Join(h.paths.GitDaily, "index")); err == nil {
		b.ReportMetric(float64(fi.Size())/1024, "index-KiB")
	}
}

type turnBenchHome struct {
	paths    store.PersonantPaths
	spine    []byte
	threadMD []byte
	turnBody []byte
	nextTurn int
}

// newTurnBenchHome writes n tracked files, stages them, commits a baseline
// and packs it, leaving the state a daily repo has right after a gc.
func newTurnBenchHome(b *testing.B, n int) *turnBenchHome {
	b.Helper()
	paths := store.PathsForHome(b.TempDir())
	if err := InitDaily(context.Background(), paths); err != nil {
		b.Fatalf("InitDaily: %v", err)
	}
	r := rand.New(rand.NewPCG(1, uint64(n)))
	h := &turnBenchHome{
		paths:    paths,
		spine:    benchText(r, benchSpineBytes),
		threadMD: benchText(r, benchThreadMDBytes),
		turnBody: benchText(r, benchTurnBytes),
	}
	var rels []string
	add := func(rel string, data []byte) {
		h.write(b, rel, data)
		rels = append(rels, rel)
	}
	add("spine.jsonl", h.spine)
	add(benchLogRel, benchText(r, benchLogBytes))
	for _, rel := range benchMiscFiles {
		add(rel, benchText(r, 512))
	}
	for i := range benchSmallThreads {
		dir := fmt.Sprintf("threads/thr_s%03d", i)
		add(dir+"/thread.md", benchText(r, benchThreadMDBytes))
		add(dir+"/files.json", benchText(r, 256))
		for j := range benchSmallTurns {
			add(fmt.Sprintf("%s/turns/%07d.md", dir, j+1), benchText(r, benchTurnBytes))
		}
	}
	add(benchMainThread+"/thread.md", h.threadMD)
	add(benchMainThread+"/files.json", benchText(r, 256))
	mainTurns := n - len(rels)
	if mainTurns < 1 {
		b.Fatalf("N=%d is below the fixed layout's %d entries", n, len(rels)+1)
	}
	for h.nextTurn = 1; h.nextTurn <= mainTurns; h.nextTurn++ {
		add(fmt.Sprintf("%s/turns/%07d.md", benchMainThread, h.nextTurn), benchText(r, benchTurnBytes))
	}
	h.nextTurn--

	if err := AddPaths(context.Background(), paths, Daily, rels); err != nil {
		b.Fatalf("stage baseline: %v", err)
	}
	if err := Commit(context.Background(), paths, Daily, "baseline", 0, 0); err != nil {
		b.Fatalf("commit baseline: %v", err)
	}
	if err := GC(context.Background(), paths, Daily); err != nil {
		b.Fatalf("gc baseline: %v", err)
	}
	return h
}

// writeTurn makes one turn's writes into thread and returns its write set.
func (h *turnBenchHome) writeTurn(b *testing.B, thread string) []string {
	b.Helper()
	h.nextTurn++
	turnRel := fmt.Sprintf("%s/turns/%07d.md", thread, h.nextTurn)
	h.write(b, turnRel, fmt.Appendf(nil, "## Turn %d\n%s", h.nextTurn, h.turnBody))
	h.write(b, thread+"/thread.md", fmt.Appendf(nil, "turn_count: %d\n%s", h.nextTurn, h.threadMD))
	h.write(b, "spine.jsonl", fmt.Appendf(nil, "{\"turn\":%d}\n%s", h.nextTurn, h.spine))
	f, err := os.OpenFile(filepath.Join(h.paths.Home, benchLogRel), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		b.Fatalf("open log: %v", err)
	}
	_, werr := f.Write(h.spine[:benchLogAppend])
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		b.Fatalf("append log: %v", werr)
	}
	return []string{turnRel, thread + "/thread.md", "spine.jsonl", benchLogRel}
}

func (h *turnBenchHome) write(b *testing.B, rel string, data []byte) {
	b.Helper()
	abs := filepath.Join(h.paths.Home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		b.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		b.Fatalf("write %s: %v", rel, err)
	}
}

// probeIndexRoundTrip times one index load and one save of the daily repo.
func probeIndexRoundTrip(b *testing.B, paths store.PersonantPaths) (load, save time.Duration) {
	b.Helper()
	repo, err := openRepo(paths, Daily)
	if err != nil {
		b.Fatalf("open daily: %v", err)
	}
	t0 := clock.Profiling()
	idx, err := repo.Storer.Index()
	load = clock.Since(t0)
	if err != nil {
		b.Fatalf("index load: %v", err)
	}
	t1 := clock.Profiling()
	if err := repo.Storer.SetIndex(idx); err != nil {
		b.Fatalf("index save: %v", err)
	}
	return load, clock.Since(t1)
}

// benchText returns n bytes of word-like text, so zlib sees prose-like
// redundancy rather than a trivially compressible repeat.
func benchText(r *rand.Rand, n int) []byte {
	const letters = "etaoinshrdlucmfwypvbgkqjxz"
	out := make([]byte, 0, n)
	for len(out) < n {
		wl := 2 + r.IntN(8)
		for range wl {
			out = append(out, letters[min(r.IntN(len(letters)), r.IntN(len(letters)))])
		}
		out = append(out, ' ')
		if r.IntN(12) == 0 {
			out = append(out, '\n')
		}
	}
	return out[:n]
}
