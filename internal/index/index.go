package index

// Top-level entry points for `personant index rebuild` and
// `personant index check`. The two share the same build pipeline; check
// is rebuild-with-no-writes plus a per-file comparison against what is
// currently on disk.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"personant/internal/memops"
	"personant/internal/store"
)

// Options controls Rebuild and Check behavior.
//
// Logger receives one progress line per significant step (one per
// project digest written, one for symbols, one summary). Nil Logger is
// silent. Quiet, when true, suppresses logger calls regardless.
//
// Warner is invoked for recoverable issues (missing meta.json on a
// referenced project, etc.). Defaults to Logger if nil.
type Options struct {
	Quiet  bool
	Logger func(format string, args ...any)
	Warner func(format string, args ...any)
}

// Rebuild regenerates all derived files (symbols.jsonl + per-project
// digest.json) atomically from spine.jsonl. Returns the first error
// encountered. Successful return means every derived file is in sync
// with the canonical spine at the moment of return.
func Rebuild(paths store.PersonantPaths, opts Options) error {
	logf, warnf := loggers(opts)

	plan, err := buildPlan(paths, warnf)
	if err != nil {
		return err
	}

	if err := store.WriteSymbols(paths.Symbols, plan.symbols); err != nil {
		return fmt.Errorf("rebuild: write symbols: %w", err)
	}
	logf("index: wrote %s (%d symbols)", paths.Symbols, len(plan.symbols))

	// Write digests in a deterministic order (lexical project id) for
	// readable progress output.
	ids := make([]string, 0, len(plan.digests))
	for id := range plan.digests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := WriteDigest(paths.ProjectsDir, id, plan.digests[id]); err != nil {
			return fmt.Errorf("rebuild: write digest %s: %w", id, err)
		}
		logf("index: wrote projects/%s/digest.json", id)
	}

	logf("index: rebuild ok (%d symbols, %d digests)", len(plan.symbols), len(plan.digests))
	return nil
}

// Check computes what Rebuild would write and compares to what is on
// disk. It never writes anything. The returned memops.CheckResult is
// non-nil even on success; callers should consult result.OK() and
// result.Drifts.
//
// Drift detection covers:
//   - symbols.jsonl: stale (content differs) or missing.
//   - projects/prj_<n>/digest.json: stale, missing.
//   - extra digest.json files for projects not present in spine: NOT
//     reported as drift, per the conservative removal policy. (The
//     directory exists; its digest is left in place.) [OPEN: revisit
//     once we have empirical evidence on stale-digest frequency.]
func Check(paths store.PersonantPaths, opts Options) (memops.CheckResult, error) {
	_, warnf := loggers(opts)

	plan, err := buildPlan(paths, warnf)
	if err != nil {
		return memops.CheckResult{}, err
	}

	var drifts []memops.Drift

	// Symbols.
	wantSymbols, err := encodeSymbolsJSONL(plan.symbols)
	if err != nil {
		return memops.CheckResult{}, fmt.Errorf("check: encode symbols: %w", err)
	}
	gotSymbols, err := os.ReadFile(paths.Symbols)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			drifts = append(drifts, memops.Drift{Path: paths.Symbols, Status: "missing"})
		} else {
			return memops.CheckResult{}, fmt.Errorf("check: read %s: %w", paths.Symbols, err)
		}
	} else if !bytes.Equal(gotSymbols, wantSymbols) {
		drifts = append(drifts, memops.Drift{
			Path:   paths.Symbols,
			Status: "stale",
			Detail: fmt.Sprintf("got %d bytes, want %d bytes", len(gotSymbols), len(wantSymbols)),
		})
	}

	// Digests.
	ids := make([]string, 0, len(plan.digests))
	for id := range plan.digests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		path := filepath.Join(paths.ProjectsDir, id, "digest.json")
		wantBytes, err := encodeDigestJSON(plan.digests[id])
		if err != nil {
			return memops.CheckResult{}, fmt.Errorf("check: encode digest %s: %w", id, err)
		}
		gotBytes, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				drifts = append(drifts, memops.Drift{Path: path, Status: "missing"})
				continue
			}
			return memops.CheckResult{}, fmt.Errorf("check: read %s: %w", path, err)
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			drifts = append(drifts, memops.Drift{
				Path:   path,
				Status: "stale",
				Detail: fmt.Sprintf("got %d bytes, want %d bytes", len(gotBytes), len(wantBytes)),
			})
		}
	}

	return memops.CheckResult{Drifts: drifts}, nil
}

// plan holds the in-memory result of running the build pipeline.
type plan struct {
	symbols []store.SymbolRecord
	digests map[string]memops.ProjectDigest
}

func buildPlan(paths store.PersonantPaths, warnf func(format string, args ...any)) (plan, error) {
	spine, err := store.ReadSpine(paths.Spine)
	if err != nil {
		return plan{}, fmt.Errorf("read spine: %w", err)
	}
	threads, err := store.LoadAllThreadFrontmatter(paths, nil, warnf)
	if err != nil {
		return plan{}, fmt.Errorf("load thread frontmatter: %w", err)
	}
	syms := BuildSymbols(spine, threads)
	digs, err := BuildDigests(spine, paths.ProjectsDir, warnf)
	if err != nil {
		return plan{}, fmt.Errorf("build digests: %w", err)
	}
	return plan{symbols: syms, digests: digs}, nil
}

func loggers(opts Options) (logf, warnf func(format string, args ...any)) {
	logf = func(string, ...any) {}
	if !opts.Quiet && opts.Logger != nil {
		logf = opts.Logger
	}
	warnf = opts.Warner
	if warnf == nil {
		warnf = logf
	}
	if opts.Quiet {
		warnf = func(string, ...any) {}
	}
	return
}
