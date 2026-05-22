// Benchmarks decomposing the recall hot path (ProposeRecall) into its
// three per-call costs, to inform task #93: whether an adapter-level
// read-through parsed-frontmatter cache is worth building.
//
// ProposeRecall does, per call:
//  1. store.ReadSpine            — read+parse spine.jsonl (one file).
//  2. store.LoadAllThreadFrontmatter — per live thread: os.ReadFile +
//     SplitFrontmatter + yaml.Unmarshal. The suspected dominant cost
//     and the thing a frontmatter cache would eliminate.
//  3. scoring.ProposeFromIndex   — pure in-memory Jaccard, no I/O.
//
// The substrate is read-only for all four benchmarks, so it is built
// once per N before the timed loop (b.ResetTimer). Each loop iteration
// is a self-contained call that allocates its own results — no state
// accumulates across iterations.
package fileadapter

import (
	"context"
	"fmt"
	"testing"

	"personant/internal/memops"
	"personant/internal/recall/scoring"
	"personant/internal/store"
)

// benchThreadCounts is the live-thread sweep. Production caps the live
// set (archival removes thread dirs), but the SLOPE across these counts
// is what matters for sim/harness throughput.
var benchThreadCounts = []int{10, 40, 100, 200}

// benchQuery is a realistic ~3-symbol recall query. Its symbols are
// drawn from the symbol vocabulary used to seed the threads so the
// scoring step does real intersection work (not an all-miss fast path).
var benchQuery = []string{"sym0", "sym1", "sym2"}

// buildBenchSubstrate scaffolds a home tree with n live threads modeled
// on the 6m sim steady state: ~6 history_symbols per thread (mean),
// up to 8 anchors, and roughly a third of each thread's symbols marked
// superseded with EverCentral=true (retained abandoned premises). It
// writes per-thread frontmatter via SaveThreadFrontmatter and the spine
// in one WriteSpine, then returns an adapter rooted at the tree.
func buildBenchSubstrate(b *testing.B, n int) *FileAdapter {
	b.Helper()
	paths := store.PathsForHome(b.TempDir())
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		b.Fatalf("store.Init: %v", err)
	}

	now := rfc3339Now()
	recs := make([]memops.SpineRecord, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("thr_%d", i)

		// ~6 history symbols, deterministically varied across threads so
		// queries hit some threads and miss others. symCount cycles 4..7
		// (mean ~5.5), close to the 6m steady-state mean of ~6.
		symCount := 4 + (i % 4)
		syms := make([]memops.HistorySymbol, 0, symCount)
		anchors := make([]string, 0, symCount)
		for j := 0; j < symCount; j++ {
			// Window the symbol namespace by thread index so query symbols
			// {sym0,sym1,sym2} match a realistic minority of threads rather
			// than all or none.
			name := fmt.Sprintf("sym%d", (i+j)%12)
			lifecycle := memops.LifecycleActive
			everCentral := false
			// Mark roughly the last third of each thread's symbols as
			// superseded-but-retained (abandoned premises).
			if j >= symCount-symCount/3 && symCount/3 > 0 {
				lifecycle = memops.LifecycleSuperseded
				everCentral = true
			}
			syms = append(syms, memops.HistorySymbol{
				Raw:            name,
				Normalized:     name,
				FirstSeenTurn:  j + 1,
				Count:          1 + j,
				Source:         memops.SourceModel,
				Lifecycle:      lifecycle,
				EverCentral:    everCentral,
				LastActiveTurn: j + 1,
			})
			// Anchors are the active-symbol projection, capped at the max.
			if lifecycle == memops.LifecycleActive && len(anchors) < memops.AnchorProjectionMax {
				anchors = append(anchors, name)
			}
		}

		fm := memops.ThreadMeta{
			ID:             id,
			Project:        "prj_1",
			Anchors:        anchors,
			Summary:        "bench thread",
			State:          memops.ThreadActive,
			Created:        now,
			LastEngaged:    now,
			StateChanged:   now,
			TurnCount:      symCount,
			HistorySymbols: syms,
		}
		if err := store.SaveThreadFrontmatter(paths, id, fm); err != nil {
			b.Fatalf("SaveThreadFrontmatter %s: %v", id, err)
		}

		recs = append(recs, memops.SpineRecord{
			ID:           id,
			Project:      "prj_1",
			Anchors:      anchors,
			Summary:      "bench thread",
			State:        memops.ThreadActive,
			Created:      now,
			LastEngaged:  now,
			StateChanged: now,
			TurnCount:    symCount,
		})
	}
	if err := store.WriteSpine(paths.Spine, recs); err != nil {
		b.Fatalf("WriteSpine: %v", err)
	}
	return NewFileAdapter(paths)
}

// BenchmarkProposeRecall_Full times the whole ProposeRecall call.
func BenchmarkProposeRecall_Full(b *testing.B) {
	ctx := context.Background()
	opts := memops.RecallOptions{Project: "prj_1"}
	for _, n := range benchThreadCounts {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			a := buildBenchSubstrate(b, n)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := a.ProposeRecall(ctx, benchQuery, opts); err != nil {
					b.Fatalf("ProposeRecall: %v", err)
				}
			}
		})
	}
}

// BenchmarkProposeRecall_LoadAllThreadFrontmatter times only step 2 —
// the per-thread os.ReadFile + SplitFrontmatter + yaml.Unmarshal that a
// parsed-frontmatter cache would eliminate. allocs/op here is the
// allocation budget such a cache would cut.
func BenchmarkProposeRecall_LoadAllThreadFrontmatter(b *testing.B) {
	for _, n := range benchThreadCounts {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			a := buildBenchSubstrate(b, n)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := store.LoadAllThreadFrontmatter(a.paths, nil); err != nil {
					b.Fatalf("LoadAllThreadFrontmatter: %v", err)
				}
			}
		})
	}
}

// BenchmarkProposeRecall_CacheLoadAllWarm times the warm-cache path that
// REPLACES step 2 in production: a.fmCache.LoadAll over an already-populated
// cache. The first iteration (b.ResetTimer is after one priming call) is a
// full parse; every subsequent call is ListThreadIDs (readdir) + map
// lookups, so the steady-state cost is allocs/op far below the ~650/thread
// of the uncached BenchmarkProposeRecall_LoadAllThreadFrontmatter baseline.
func BenchmarkProposeRecall_CacheLoadAllWarm(b *testing.B) {
	for _, n := range benchThreadCounts {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			a := buildBenchSubstrate(b, n)
			// Prime the cache so the timed loop is all hits.
			if _, err := a.fmCache.LoadAll(a.paths, nil); err != nil {
				b.Fatalf("prime LoadAll: %v", err)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := a.fmCache.LoadAll(a.paths, nil); err != nil {
					b.Fatalf("cache.LoadAll: %v", err)
				}
			}
		})
	}
}

// BenchmarkProposeRecall_ReadSpine times only step 1 — the single
// spine.jsonl read+parse.
func BenchmarkProposeRecall_ReadSpine(b *testing.B) {
	for _, n := range benchThreadCounts {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			a := buildBenchSubstrate(b, n)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := store.ReadSpine(a.paths.Spine); err != nil {
					b.Fatalf("ReadSpine: %v", err)
				}
			}
		})
	}
}

// BenchmarkProposeRecall_ProposeFromIndex times only step 3 — the pure
// scoring. Spine + threads are loaded ONCE outside the timed loop, so
// only the in-memory Jaccard is measured.
func BenchmarkProposeRecall_ProposeFromIndex(b *testing.B) {
	scoreOpts := scoring.Options{Project: "prj_1"}
	for _, n := range benchThreadCounts {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			a := buildBenchSubstrate(b, n)
			spine, err := store.ReadSpine(a.paths.Spine)
			if err != nil {
				b.Fatalf("ReadSpine: %v", err)
			}
			threads, err := store.LoadAllThreadFrontmatter(a.paths, nil)
			if err != nil {
				b.Fatalf("LoadAllThreadFrontmatter: %v", err)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = scoring.ProposeFromIndex(spine, threads, benchQuery, scoreOpts)
			}
		})
	}
}
