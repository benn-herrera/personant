package index

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/store"
)

// hasGit skips the test if git isn't on PATH (Init's git step requires it).
func hasGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not in PATH: %v", err)
	}
}

// initFreshHome scaffolds a personant home with store.Init and returns
// the resulting paths.
func initFreshHome(t *testing.T) store.PersonantPaths {
	t.Helper()
	hasGit(t)
	home := t.TempDir()
	paths := store.PathsForHome(home)
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return paths
}

// writeSpine writes the given spine records via store.WriteSpine.
func writeSpine(t *testing.T, paths store.PersonantPaths, recs []store.SpineRecord) {
	t.Helper()
	if err := store.WriteSpine(paths.Spine, recs); err != nil {
		t.Fatalf("WriteSpine: %v", err)
	}
}

func TestRebuildEndToEnd(t *testing.T) {
	paths := initFreshHome(t)
	writeSpine(t, paths, []store.SpineRecord{
		{
			ID: "thr_1", Project: "prj_default",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "first thread", State: store.ThreadWIP,
			Created: "2026-05-09T04:00:00-07:00", LastEngaged: "2026-05-09T04:00:00-07:00", StateChanged: "2026-05-09T04:00:00-07:00",
			TurnCount: 3, RecallFires: 0,
		},
		{
			ID: "thr_2", Project: "prj_default",
			Anchors: []string{"alpha", "epsilon", "zeta", "eta"},
			Summary: "second thread sharing alpha", State: store.ThreadResolved,
			Created: "2026-05-09T04:01:00-07:00", LastEngaged: "2026-05-09T04:01:00-07:00", StateChanged: "2026-05-09T04:01:00-07:00",
			TurnCount: 2, RecallFires: 2,
		},
	})

	if err := Rebuild(paths, Options{Quiet: true}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	syms, err := store.ReadSymbols(paths.Symbols)
	if err != nil {
		t.Fatalf("ReadSymbols: %v", err)
	}
	if len(syms) != 7 {
		t.Errorf("got %d symbols, want 7: %#v", len(syms), syms)
	}
	bySym := make(map[string]store.SymbolRecord, len(syms))
	for _, s := range syms {
		bySym[s.Symbol] = s
	}
	alpha, ok := bySym["alpha"]
	if !ok {
		t.Fatal("missing symbol alpha")
	}
	// thr_2 has higher recall_fires (2 vs 0) so it must lead alpha.threads.
	if len(alpha.Threads) != 2 || alpha.Threads[0] != "thr_2" || alpha.Threads[1] != "thr_1" {
		t.Errorf("alpha.threads = %v, want [thr_2 thr_1]", alpha.Threads)
	}

	digestPath := filepath.Join(paths.ProjectsDir, "prj_default", "digest.json")
	if _, err := os.Stat(digestPath); err != nil {
		t.Fatalf("digest missing: %v", err)
	}
}

func TestCheckCleanAfterRebuild(t *testing.T) {
	paths := initFreshHome(t)
	writeSpine(t, paths, []store.SpineRecord{
		{
			ID: "thr_1", Project: "prj_default",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "x", State: store.ThreadActive,
			Created: "2026-05-09T04:00:00-07:00", LastEngaged: "2026-05-09T04:00:00-07:00", StateChanged: "2026-05-09T04:00:00-07:00",
		},
	})
	if err := Rebuild(paths, Options{Quiet: true}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	res, err := Check(paths, Options{Quiet: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.OK() {
		t.Errorf("Check after Rebuild reported drift: %+v", res.Drifts)
	}
}

func TestCheckCleanOnEmptyHome(t *testing.T) {
	// Init only — no rebuild, no spine writes. Spine and symbols are
	// both empty files. Check should be clean.
	paths := initFreshHome(t)
	res, err := Check(paths, Options{Quiet: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.OK() {
		t.Errorf("Check on fresh init reported drift: %+v", res.Drifts)
	}
}

func TestCheckDetectsCorruptSymbols(t *testing.T) {
	paths := initFreshHome(t)
	writeSpine(t, paths, []store.SpineRecord{
		{
			ID: "thr_1", Project: "prj_default",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "x", State: store.ThreadActive,
			Created: "2026-05-09T04:00:00-07:00", LastEngaged: "2026-05-09T04:00:00-07:00", StateChanged: "2026-05-09T04:00:00-07:00",
		},
	})
	if err := Rebuild(paths, Options{Quiet: true}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	// Append a bogus line to symbols.jsonl.
	f, err := os.OpenFile(paths.Symbols, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open symbols: %v", err)
	}
	if _, err := f.WriteString(`{"symbol":"BOGUS"}` + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	res, err := Check(paths, Options{Quiet: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.OK() {
		t.Fatal("expected drift, got clean")
	}
	found := false
	for _, d := range res.Drifts {
		if strings.HasSuffix(d.Path, "symbols.jsonl") && d.Status == "stale" {
			found = true
		}
	}
	if !found {
		t.Errorf("did not see symbols.jsonl stale drift; got: %+v", res.Drifts)
	}
}

func TestCheckDetectsMissingDigest(t *testing.T) {
	paths := initFreshHome(t)
	writeSpine(t, paths, []store.SpineRecord{
		{
			ID: "thr_1", Project: "prj_default",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "x", State: store.ThreadActive,
			Created: "2026-05-09T04:00:00-07:00", LastEngaged: "2026-05-09T04:00:00-07:00", StateChanged: "2026-05-09T04:00:00-07:00",
		},
	})
	if err := Rebuild(paths, Options{Quiet: true}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	digestPath := filepath.Join(paths.ProjectsDir, "prj_default", "digest.json")
	if err := os.Remove(digestPath); err != nil {
		t.Fatalf("remove digest: %v", err)
	}
	res, err := Check(paths, Options{Quiet: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.OK() {
		t.Fatal("expected drift, got clean")
	}
	found := false
	for _, d := range res.Drifts {
		if d.Path == digestPath && d.Status == "missing" {
			found = true
		}
	}
	if !found {
		t.Errorf("did not see missing digest drift; got: %+v", res.Drifts)
	}
}

func TestRebuildIsIdempotent(t *testing.T) {
	paths := initFreshHome(t)
	writeSpine(t, paths, []store.SpineRecord{
		{
			ID: "thr_1", Project: "prj_default",
			Anchors: []string{"alpha", "beta", "gamma", "delta"},
			Summary: "x", State: store.ThreadActive,
			Created: "2026-05-09T04:00:00-07:00", LastEngaged: "2026-05-09T04:00:00-07:00", StateChanged: "2026-05-09T04:00:00-07:00",
		},
	})
	if err := Rebuild(paths, Options{Quiet: true}); err != nil {
		t.Fatalf("first Rebuild: %v", err)
	}
	symBytes1, err := os.ReadFile(paths.Symbols)
	if err != nil {
		t.Fatalf("read symbols: %v", err)
	}
	digestPath := filepath.Join(paths.ProjectsDir, "prj_default", "digest.json")
	digBytes1, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatalf("read digest: %v", err)
	}

	if err := Rebuild(paths, Options{Quiet: true}); err != nil {
		t.Fatalf("second Rebuild: %v", err)
	}
	symBytes2, _ := os.ReadFile(paths.Symbols)
	digBytes2, _ := os.ReadFile(digestPath)
	if string(symBytes1) != string(symBytes2) {
		t.Errorf("symbols.jsonl changed between rebuilds")
	}
	if string(digBytes1) != string(digBytes2) {
		t.Errorf("digest.json changed between rebuilds")
	}
}
