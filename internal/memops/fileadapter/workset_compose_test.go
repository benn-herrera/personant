// Tests for the substrate-fetch half of ComposeWorkingSet. The pure
// rendering half is covered in internal/workset/workset_test.go; these
// tests confirm the adapter loads each layer's substrate state
// correctly and that warnings route through the event log.

package fileadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"personant/internal/dedup"
	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/workset"
)

// TestComposeWorkingSet_LayerE_DirectivesAndConventions seeds the three
// directive files plus a conventions file and verifies Layer E content.
func TestComposeWorkingSet_LayerE_DirectivesAndConventions(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := store.SaveProjectMeta(a.paths, meta); err != nil {
		t.Fatalf("SaveProjectMeta: %v", err)
	}
	// store.Init seeds defaults.md and user.md with template content;
	// rewrite them with marker bodies so the test asserts on known data.
	defaults := "---\nscope: defaults\n---\nDEFAULTS_BODY\n"
	user := "USER_BODY_NO_FRONTMATTER\n"
	if err := os.WriteFile(filepath.Join(a.paths.DirectivesDir, "defaults.md"), []byte(defaults), 0o644); err != nil {
		t.Fatalf("write defaults.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a.paths.DirectivesDir, "user.md"), []byte(user), 0o644); err != nil {
		t.Fatalf("write user.md: %v", err)
	}
	projectDir := filepath.Join(a.paths.DirectivesDir, "prj_1")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir projectDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "project.md"),
		[]byte("---\nscope: project\n---\nPROJECT_BODY\n"), 0o644); err != nil {
		t.Fatalf("write project.md: %v", err)
	}

	convDir := t.TempDir()
	convPath := filepath.Join(convDir, "CONVENTIONS.md")
	if err := os.WriteFile(convPath, []byte("CONVENTIONS_BODY\n"), 0o644); err != nil {
		t.Fatalf("write conventions: %v", err)
	}
	meta.ConventionsPaths = []string{convPath}
	if err := store.SaveProjectMeta(a.paths, meta); err != nil {
		t.Fatalf("SaveProjectMeta: %v", err)
	}

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{ActiveProject: meta})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	for _, want := range []string{"DEFAULTS_BODY", "USER_BODY_NO_FRONTMATTER", "PROJECT_BODY", "CONVENTIONS_BODY"} {
		if !strings.Contains(out.LayerE, want) {
			t.Errorf("LayerE missing %q\ngot: %s", want, out.LayerE)
		}
	}
	for _, header := range []string{"=== defaults ===", "=== user ===", "=== project: alpha ===", "=== conventions: " + convPath + " ==="} {
		if !strings.Contains(out.LayerE, header) {
			t.Errorf("LayerE missing header %q\ngot: %s", header, out.LayerE)
		}
	}
}

// TestComposeWorkingSet_LayerE_MissingConventionsLogsWarning verifies
// that an unreadable conventions path is emitted to the event log as a
// workset.warning entry.
func TestComposeWorkingSet_LayerE_MissingConventionsLogsWarning(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	meta := memops.ProjectMeta{
		ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now(),
		ConventionsPaths: []string{"/nonexistent/path/CONVENTIONS.md"},
	}
	if err := store.SaveProjectMeta(a.paths, meta); err != nil {
		t.Fatalf("SaveProjectMeta: %v", err)
	}

	if _, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{ActiveProject: meta}); err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	assertEventLogContains(t, a.paths, "workset", "warning", "conventions")
}

// TestComposeWorkingSet_LayerA2_DigestsSortedByLastActive seeds two
// non-active projects with digests and verifies the A2 layer renders
// them sorted by LastActive desc.
func TestComposeWorkingSet_LayerA2_DigestsSortedByLastActive(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	active := memops.ProjectMeta{ID: "prj_1", Name: "active", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := store.SaveProjectMeta(a.paths, active); err != nil {
		t.Fatalf("create active: %v", err)
	}
	older := memops.ProjectMeta{ID: "prj_2", Name: "older", Created: rfc3339Now(), LastActive: "2026-04-01T00:00:00Z"}
	if err := store.SaveProjectMeta(a.paths, older); err != nil {
		t.Fatalf("create older: %v", err)
	}
	newer := memops.ProjectMeta{ID: "prj_3", Name: "newer", Created: rfc3339Now(), LastActive: "2026-05-01T00:00:00Z"}
	if err := store.SaveProjectMeta(a.paths, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}
	writeDigest(t, a.paths, memops.ProjectDigest{
		Project: "prj_2", DisplayName: "older",
		ThreadCount: 2, RecentAnchors: []string{"a", "b", "c", "d", "e", "f"},
		OneLineSummary: "older summary",
	})
	writeDigest(t, a.paths, memops.ProjectDigest{
		Project: "prj_3", DisplayName: "newer",
		ThreadCount: 1, RecentAnchors: []string{"x", "y"},
		OneLineSummary: "newer summary",
	})

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{ActiveProject: active})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.LayerA2, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 A2 lines; got %d: %q", len(lines), out.LayerA2)
	}
	if !strings.HasPrefix(lines[0], "newer (prj_3)") {
		t.Errorf("first A2 line should be prj_3; got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "older (prj_2)") {
		t.Errorf("second A2 line should be prj_2; got %q", lines[1])
	}
	if strings.Contains(out.LayerA2, "prj_1") {
		t.Errorf("active project leaked into A2: %q", out.LayerA2)
	}
}

// testShare is the per-thread Layer B share the file-content compose tests
// run at. Its literal limit, ⌈0.25 × 4097⌉, is 1025 bytes: 0.25 × 4097 is
// fractional, so a limit that rounds down shows here.
const (
	testShare      = 4097
	testLiteralMax = 1025
)

// TestComposeWorkingSet_InPlaceAcrossThreads: two Layer B threads touch
// src/a.go. Its literal renders once, at its most recent position; its N
// most recent changes render as diffs at their turns; its older position
// in the other thread expands nothing; and src/copy.go, holding the same
// content and walked later, renders as a size note.
func TestComposeWorkingSet_InPlaceAcrossThreads(t *testing.T) {
	a := newAdapter(t)
	seedTurns(t, a, "thr_1", "t1", "t2", "t3", "t4", "t5", "t6")
	seedTurns(t, a, "thr_2", "u1", "u2")
	final := revision(5)
	recordFiles(t, a, "thr_2", 2, 1, readEvent("src/a.go", revision(0)), readEvent("src/copy.go", final))
	for turn := 2; turn <= 6; turn++ {
		recordFiles(t, a, "thr_1", turn, turn, writeEvent("src/a.go", revision(turn-1)))
	}

	out := composeLayerB(t, a, "thr_1", "thr_2")

	want := []memops.FileExpansion{
		diffRecord(t, a, "thr_1", 4, "src/a.go", revision(2), revision(3), final),
		diffRecord(t, a, "thr_1", 5, "src/a.go", revision(3), revision(4), final),
		diffRecord(t, a, "thr_1", 6, "src/a.go", revision(4), final, final),
		{ThreadID: "thr_1", Turn: 6, Project: "prj_1", Path: "src/a.go", Kind: "literal",
			ID: dedup.ContentID([]byte(final)), Size: len(final), PathSize: len(final)},
	}
	assertReport(t, out, want)
	if n := strings.Count(out.LayerB, "[end file content #"); n != 1 {
		t.Errorf("LayerB renders %d literals, want 1:\n%s", n, out.LayerB)
	}
	if n := strings.Count(out.LayerB, "[end file diff #"); n != dedup.LiveDiffWindow {
		t.Errorf("LayerB renders %d diffs, want %d:\n%s", n, dedup.LiveDiffWindow, out.LayerB)
	}
	t6 := strings.Index(out.LayerB, "## Turn 6")
	literal := strings.Index(out.LayerB, "[end file content #")
	thr2 := strings.Index(out.LayerB, "(thr_2)")
	if t6 < 0 || literal < t6 || literal > thr2 {
		t.Errorf("literal is not inside thr_1's turn 6 excerpt:\n%s", out.LayerB)
	}
	if strings.Contains(out.LayerB[thr2:], "[end file ") || strings.Contains(out.LayerB[thr2:], "] src/a.go") {
		t.Errorf("thr_2 expands file content:\n%s", out.LayerB[thr2:])
	}
	if note := fmt.Sprintf("not shown: %d bytes] src/copy.go", len(final)); !strings.Contains(out.LayerB[thr2:], note) {
		t.Errorf("thr_2 lacks %q:\n%s", note, out.LayerB[thr2:])
	}
}

// TestComposeWorkingSet_SharedIDUnwalkedPositionRendersAtOtherPath: x.go
// and y.go hold the same content; x.go's position is more recent but lies
// in an excerpt the walk does not reach, so y.go renders the literal.
func TestComposeWorkingSet_SharedIDUnwalkedPositionRendersAtOtherPath(t *testing.T) {
	a := newAdapter(t)
	shared := lines(100, 's')
	seedTurns(t, a, "thr_1", "UNWALKED")
	recordFiles(t, a, "thr_1", 1, 9, readEvent("x.go", shared))
	appendSizedTurn(t, a, "thr_1", 2, 0)
	seedTurns(t, a, "thr_2", "y")
	recordFiles(t, a, "thr_2", 1, 1, readEvent("y.go", shared))

	out := composeLayerB(t, a, "thr_1", "thr_2")

	assertReport(t, out, []memops.FileExpansion{{
		ThreadID: "thr_2", Turn: 1, Project: "prj_1", Path: "y.go", Kind: "literal",
		ID: dedup.ContentID([]byte(shared)), Size: len(shared), PathSize: len(shared),
	}})
	if strings.Contains(out.LayerB, "UNWALKED") {
		t.Errorf("the walk reached thr_1's turn 1:\n%s", out.LayerB)
	}
}

// TestComposeWorkingSet_ShareThresholdBoundary: a file at the literal
// limit renders no literal and no diff; a 1024-byte file, under
// 0.25 × 4097 bytes, renders its literal.
func TestComposeWorkingSet_ShareThresholdBoundary(t *testing.T) {
	a := newAdapter(t)
	seedTurns(t, a, "thr_1", "t1", "t2", "t3")
	atMax := lines(testLiteralMax, 'a')
	changed := "b" + atMax[1:]
	under := lines(1024, 'c')
	recordFiles(t, a, "thr_1", 2, 2, readEvent("at.go", atMax))
	recordFiles(t, a, "thr_1", 3, 3, writeEvent("at.go", changed), readEvent("under.go", under))
	entries, err := store.ReadTrackedIndex(a.paths, "thr_1")
	if err != nil || len(entries) != 2 || len(entries[0].Changes) != 1 || !entries[0].Changes[0].Diff {
		t.Fatalf("precondition: at.go must hold a kept diff; index %+v, err %v", entries, err)
	}

	out := composeLayerB(t, a, "thr_1")

	assertReport(t, out, []memops.FileExpansion{{
		ThreadID: "thr_1", Turn: 3, Project: "prj_1", Path: "under.go", Kind: "literal",
		ID: dedup.ContentID([]byte(under)), Size: len(under), PathSize: len(under),
	}})
	if strings.Contains(out.LayerB, "] at.go") {
		t.Errorf("at.go renders content or a note:\n%s", out.LayerB)
	}
	if !strings.Contains(out.LayerB, under) {
		t.Errorf("under.go's literal is missing:\n%s", out.LayerB)
	}
}

// TestComposeWorkingSet_LoadsOnlyRenderedExpansions: early.go is planned
// at an excerpt the walk does not reach and late.go at one where it does
// not fit. Both blobs are corrupt, so reading either would log
// dedup.error; neither is read.
func TestComposeWorkingSet_LoadsOnlyRenderedExpansions(t *testing.T) {
	a := newAdapter(t)
	seedWalkFixture(t, a)
	corruptBlob(t, a, "thr_1", lines(100, 'e'))
	corruptBlob(t, a, "thr_1", lines(900, 'l'))

	out := composeLayerB(t, a, "thr_1")

	if log := readEventLog(t, a); strings.Contains(log, "dedup.error") {
		t.Errorf("compose read a blob it did not render:\n%s", log)
	}
	if !strings.Contains(out.LayerB, "not shown: 900 bytes] late.go") {
		t.Errorf("late.go has no size note:\n%s", out.LayerB)
	}
	if strings.Contains(out.LayerB, "early.go") || strings.Contains(out.LayerB, "## Turn 2") {
		t.Errorf("the walk reached turn 2:\n%s", out.LayerB)
	}
}

// TestComposeWorkingSet_ReadFailureRendersSizeNote: content whose blob
// does not hash to its id logs dedup.error, renders as its size note and
// adds no report record. Its charge drops back to the note's, which leaves
// room for the older excerpt.
func TestComposeWorkingSet_ReadFailureRendersSizeNote(t *testing.T) {
	a := newAdapter(t)
	content := lines(100, 'x')
	note := workset.Expansion{Kind: workset.ExpansionLiteral, Path: "x.go",
		ID: dedup.ContentID([]byte(content)), Size: len(content), Omitted: true}
	full := note
	full.Omitted = false
	older := strings.Repeat("o", 90)
	olderLen := workset.ExcerptTextLen("## Turn 1\n\n" + older)
	extra := workset.ExpansionLen(full) - workset.ExpansionLen(note)
	if olderLen > extra {
		t.Fatalf("precondition: the older excerpt (%d bytes) must fit in the content's %d extra bytes", olderLen, extra)
	}
	seedTurns(t, a, "thr_1", older)
	recordFiles(t, a, "thr_1", 2, 2, readEvent("x.go", content))
	appendSizedTurn(t, a, "thr_1", 2, workset.ExpansionLen(note)+extra)
	corruptBlob(t, a, "thr_1", content)

	out := composeLayerB(t, a, "thr_1")

	if log := readEventLog(t, a); !strings.Contains(log, "dedup.error") {
		t.Errorf("no dedup.error logged:\n%s", log)
	}
	if !strings.Contains(out.LayerB, "not shown: 100 bytes] x.go") || strings.Contains(out.LayerB, "[end file") {
		t.Errorf("x.go does not render as its size note:\n%s", out.LayerB)
	}
	if !strings.Contains(out.LayerB, older) {
		t.Errorf("the failed read kept its content's charge; the older excerpt is missing:\n%s", out.LayerB)
	}
	assertReport(t, out, nil)
}

// TestComposeWorkingSet_OverflowRule covers the walk's three overflow
// cases at a 4096-byte share.
func TestComposeWorkingSet_OverflowRule(t *testing.T) {
	t.Run("an expansion that does not fit becomes a size note and the walk continues", func(t *testing.T) {
		a := newAdapter(t)
		seedTurns(t, a, "thr_1", "t1", "TURN TWO", strings.Repeat("x", 3000))
		recordFiles(t, a, "thr_1", 3, 3, readEvent("f.go", lines(1000, 'f')))
		out := composeLayerB(t, a, "thr_1")
		if !strings.Contains(out.LayerB, "not shown: 1000 bytes] f.go") {
			t.Errorf("f.go has no size note:\n%s", out.LayerB)
		}
		if !strings.Contains(out.LayerB, "TURN TWO") {
			t.Errorf("the walk stopped at the size note:\n%s", out.LayerB)
		}
		assertReport(t, out, nil)
	})
	t.Run("a bare excerpt that does not fit ends the walk", func(t *testing.T) {
		a := newAdapter(t)
		seedTurns(t, a, "thr_1", "TURN ONE", strings.Repeat("y", 2000), strings.Repeat("x", 3000))
		out := composeLayerB(t, a, "thr_1")
		if strings.Contains(out.LayerB, "## Turn 2") || strings.Contains(out.LayerB, "TURN ONE") {
			t.Errorf("the walk went past turn 3:\n%s", out.LayerB)
		}
	})
	t.Run("the newest excerpt is taken alone over the share", func(t *testing.T) {
		a := newAdapter(t)
		seedTurns(t, a, "thr_1", "TURN ONE", strings.Repeat("z", 5000))
		recordFiles(t, a, "thr_1", 2, 2, readEvent("tiny.go", "tiny\n"))
		out := composeLayerB(t, a, "thr_1")
		if !strings.Contains(out.LayerB, "## Turn 2") || strings.Contains(out.LayerB, "TURN ONE") {
			t.Errorf("want the newest excerpt alone:\n%s", out.LayerB)
		}
		if !strings.Contains(out.LayerB, "truncated") {
			t.Errorf("the backstop did not cut the oversized excerpt:\n%s", out.LayerB)
		}
		if strings.Contains(out.LayerB, "tiny.go") {
			t.Errorf("a size note renders past the share:\n%s", out.LayerB)
		}
		assertReport(t, out, nil)
	})
	t.Run("the newest excerpt's notes that do not fit are dropped and its text is not cut", func(t *testing.T) {
		a := newAdapter(t)
		seedTurns(t, a, "thr_1", "t1")
		recordFiles(t, a, "thr_1", 2, 2, readEvent("f.go", lines(100, 'f')))
		body := appendSizedTurn(t, a, "thr_1", 2, 10)
		out := composeLayerB(t, a, "thr_1")
		if strings.Contains(out.LayerB, "truncated") || !strings.HasSuffix(out.LayerB, body) {
			t.Errorf("the newest excerpt's text is cut:\n%s", out.LayerB)
		}
		if strings.Contains(out.LayerB, "f.go") {
			t.Errorf("f.go's note renders past the share:\n%s", out.LayerB)
		}
		assertReport(t, out, nil)
	})
	t.Run("an older excerpt whose text fits but whose notes do not is not admitted", func(t *testing.T) {
		a := newAdapter(t)
		seedTurns(t, a, "thr_1", "OLDER")
		recordFiles(t, a, "thr_1", 1, 1, readEvent("f.go", lines(100, 'f')))
		body := appendSizedTurn(t, a, "thr_1", 2, workset.ExcerptTextLen("## Turn 1\n\nOLDER")+5)
		out := composeLayerB(t, a, "thr_1")
		if strings.Contains(out.LayerB, "OLDER") || strings.Contains(out.LayerB, "f.go") {
			t.Errorf("the older excerpt was admitted:\n%s", out.LayerB)
		}
		if strings.Contains(out.LayerB, "truncated") || !strings.HasSuffix(out.LayerB, body) {
			t.Errorf("the newest excerpt's text is cut:\n%s", out.LayerB)
		}
	})
}

// TestComposeWorkingSet_ThreeFullThreadsFitLayerB: at a LayerB of three
// shares plus two bytes, three threads each filled to their share, with
// the separator charged, render without the Layer B cut.
func TestComposeWorkingSet_ThreeFullThreadsFitLayerB(t *testing.T) {
	a := newAdapter(t)
	ids := []string{"thr_1", "thr_2", "thr_3"}
	var bodies []string
	for _, id := range ids {
		seedTurns(t, a, id, "OLD")
		// Charged without the separator, the older excerpt would fit.
		left := workset.ExcerptTextLen("## Turn 1\n\nOLD") + 2 - len(workset.ThreadSeparator)
		bodies = append(bodies, appendSizedTurn(t, a, id, 2, left))
	}

	out := composeWithLayerB(t, a, 3*testShare+2, ids...)

	if strings.Contains(out.LayerB, "truncated") || len(out.LayerB) > 3*testShare+2 {
		t.Errorf("Layer B (%d bytes) is cut or over budget:\n%s", len(out.LayerB), out.LayerB)
	}
	for i, body := range bodies {
		if !strings.Contains(out.LayerB, body) {
			t.Errorf("%s's newest excerpt is missing", ids[i])
		}
	}
}

// TestComposeWorkingSet_ShrunkFileDiffGate: a diff kept when the file was
// large is at or above DiffLiteralThreshold of its shrunk current size,
// so it does not render.
func TestComposeWorkingSet_ShrunkFileDiffGate(t *testing.T) {
	a := newAdapter(t)
	seedTurns(t, a, "thr_1", "t1", "t2", "t3", "t4")
	v1 := revision(0)
	v2 := strings.Replace(strings.Replace(v1, "line 05: stable", "line 05: edited", 1), "line 12: stable", "line 12: edited", 1)
	v3 := v2[:strings.Index(v2, "line 06")]
	recordFiles(t, a, "thr_1", 2, 2, writeEvent("s.go", v1))
	recordFiles(t, a, "thr_1", 3, 3, writeEvent("s.go", v2))
	recordFiles(t, a, "thr_1", 4, 4, writeEvent("s.go", v3))
	entries, err := store.ReadTrackedIndex(a.paths, "thr_1")
	if err != nil || len(entries) != 1 || len(entries[0].Changes) == 0 {
		t.Fatalf("precondition: index %+v, err %v", entries, err)
	}
	kept := entries[0].Changes[0]
	if kept.Turn != 3 || !kept.Diff || float64(kept.Size) < dedup.DiffLiteralThreshold*float64(len(v3)) {
		t.Fatalf("precondition: turn 3's change must be a kept diff at or above the gate for %d bytes: %+v", len(v3), kept)
	}

	out := composeLayerB(t, a, "thr_1")

	for _, x := range out.FileExpansions.Expansions {
		if x.Kind == "diff" {
			t.Errorf("a diff renders past the gate: %+v", x)
		}
	}
	if strings.Contains(out.LayerB, "[file diff #") {
		t.Errorf("LayerB renders a diff:\n%s", out.LayerB)
	}
	if !strings.Contains(out.LayerB, "[file content #") {
		t.Errorf("the shrunk file's literal is missing:\n%s", out.LayerB)
	}
}

// TestComposeWorkingSet_CorruptIndexWarnsAndRendersThread: an index that
// does not parse logs workset.warning; the thread still renders, without
// expansions.
func TestComposeWorkingSet_CorruptIndexWarnsAndRendersThread(t *testing.T) {
	a := newAdapter(t)
	seedTurns(t, a, "thr_1", "STILL HERE")
	if err := os.WriteFile(store.TrackedIndexPath(a.paths, "thr_1"), []byte("{not json\n"), 0o644); err != nil {
		t.Fatalf("corrupt index: %v", err)
	}

	out := composeLayerB(t, a, "thr_1")

	assertEventLogContains(t, a.paths, "workset", "warning", "thr_1")
	if !strings.Contains(out.LayerB, "STILL HERE") {
		t.Errorf("thread not rendered:\n%s", out.LayerB)
	}
	assertReport(t, out, nil)
}

// TestComposeWorkingSet_LeavesExcerptsByteIdentical: compose rewrites no
// excerpt and no folio.
func TestComposeWorkingSet_LeavesExcerptsByteIdentical(t *testing.T) {
	a := newAdapter(t)
	seedWalkFixture(t, a)
	// Settling as retired binds the excerpts into folio.jsonl; turn 5,
	// appended after, stays a loose file, so both tiers are under test.
	if settled, err := store.SettleExcerptTiers(a.paths, "thr_1", true); err != nil || settled.Bound == 0 {
		t.Fatalf("SettleExcerptTiers: %+v, %v", settled, err)
	}
	if err := store.AppendThreadTurn(a.paths, "thr_1", 5, "## Turn 5\n\nloose"); err != nil {
		t.Fatalf("AppendThreadTurn: %v", err)
	}
	before := snapshotExcerpts(t, a, "thr_1")
	if len(before) == 0 {
		t.Fatal("precondition: no excerpt files")
	}

	composeLayerB(t, a, "thr_1")

	after := snapshotExcerpts(t, a, "thr_1")
	if !maps.Equal(before, after) {
		t.Errorf("excerpt files changed:\nbefore %v\nafter  %v", slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
	}
}

// TestComposeWorkingSet_ReportListsRenderedOnly: of the three expansions
// planned in the walk fixture, only small.go renders; late.go's size
// note and early.go, cut by the walk, add no record.
func TestComposeWorkingSet_ReportListsRenderedOnly(t *testing.T) {
	a := newAdapter(t)
	seedWalkFixture(t, a)
	small := lines(50, 's')

	out := composeLayerB(t, a, "thr_1")

	assertReport(t, out, []memops.FileExpansion{{
		ThreadID: "thr_1", Turn: 4, Project: "prj_1", Path: "small.go", Kind: "literal",
		ID: dedup.ContentID([]byte(small)), Size: len(small), PathSize: len(small),
	}})
}

// TestComposeWorkingSet_LayerB_MissingThreadLogsWarning verifies that
// an active thread id with no spine record / no thread file emits a
// workset.warning event-log line.
func TestComposeWorkingSet_LayerB_MissingThreadLogsWarning(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	meta := memops.ProjectMeta{ID: "prj_1", Name: "alpha", Created: rfc3339Now(), LastActive: rfc3339Now()}
	if err := store.SaveProjectMeta(a.paths, meta); err != nil {
		t.Fatalf("SaveProjectMeta: %v", err)
	}

	out, err := a.ComposeWorkingSet(ctx, memops.WorksetInput{
		ActiveProject: meta,
		ActiveThreads: []string{"thr_does_not_exist"},
	})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	if out.LayerB != "" {
		t.Errorf("LayerB should be empty when only thread is missing; got %q", out.LayerB)
	}
	assertEventLogContains(t, a.paths, "workset", "warning", "thr_does_not_exist")
}

// TestStripFrontmatter exercises the YAML-frontmatter trimming used by
// directive loading. The function lives in the adapter alongside its
// only caller; the table mirrors the substrate behavior contract.
func TestStripFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "no frontmatter", in: "# heading\nbody\n", want: "# heading\nbody\n"},
		{name: "frontmatter stripped", in: "---\nfoo: bar\n---\nbody\n", want: "body\n"},
		{name: "frontmatter with blank line after close", in: "---\nfoo: bar\n---\n\nbody\n", want: "body\n"},
		{name: "leading delimiter without close", in: "---\nfoo: bar\nbody", want: "---\nfoo: bar\nbody"},
		{name: "embedded triple-dash in body left alone", in: "---\nfoo: bar\n---\nbody\n```\n---\n```\n", want: "body\n```\n---\n```\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripFrontmatter(tc.in)
			if got != tc.want {
				t.Errorf("stripFrontmatter:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// ---- helpers ----

// seedTurns creates thread id in prj_1 with one excerpt per body, turn 1
// first.
func seedTurns(t *testing.T, a *FileAdapter, id string, bodies ...string) {
	t.Helper()
	seedThread(t, a, id, "prj_1", fmt.Sprintf("## Turn 1\n\n%s", bodies[0]))
	for i, body := range bodies[1:] {
		if err := store.AppendThreadTurn(a.paths, id, i+2, fmt.Sprintf("## Turn %d\n\n%s", i+2, body)); err != nil {
			t.Fatalf("AppendThreadTurn: %v", err)
		}
	}
}

// recordFiles records one turn's file events at minute past noon, so a
// larger minute is more recent.
func recordFiles(t *testing.T, a *FileAdapter, id string, turn, minute int, events ...memops.FileEvent) {
	t.Helper()
	at := fmt.Sprintf("2026-05-18T12:%02d:00Z", minute)
	if err := a.RecordFileEvents(context.Background(), id, turn, at, events); err != nil {
		t.Fatalf("RecordFileEvents %s turn %d: %v", id, turn, err)
	}
}

func readEvent(path, content string) memops.FileEvent {
	return memops.FileEvent{Kind: memops.FileEventRead, Path: path, Content: content}
}

func writeEvent(path, content string) memops.FileEvent {
	return memops.FileEvent{Kind: memops.FileEventWrite, Path: path, Content: content}
}

// revision is a 41-line file whose first line names k; revisions differ
// only there, so each change keeps a small diff.
func revision(k int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "revision %d\n", k)
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "line %02d: stable text\n", i)
	}
	return b.String()
}

// lines is n bytes of 64-byte lines of c.
func lines(n int, c byte) string {
	line := strings.Repeat(string(c), 63) + "\n"
	return strings.Repeat(line, n/len(line)+1)[:n]
}

// seedWalkFixture builds thr_1, whose walk at testShare takes turn 4 and
// stops at turn 3. Turn 4 plans late.go (900 bytes, which does not fit
// after the excerpt) and small.go (50 bytes, which does); turn 2 plans
// early.go, which the walk never reaches.
func seedWalkFixture(t *testing.T, a *FileAdapter) {
	t.Helper()
	seedTurns(t, a, "thr_1", "t1", "early", strings.Repeat("m", 1500), strings.Repeat("n", 3300))
	recordFiles(t, a, "thr_1", 2, 2, readEvent("early.go", lines(100, 'e')))
	recordFiles(t, a, "thr_1", 4, 4, readEvent("late.go", lines(900, 'l')), readEvent("small.go", lines(50, 's')))
}

func corruptBlob(t *testing.T, a *FileAdapter, id, content string) {
	t.Helper()
	path := store.TrackedBlobPath(a.paths, id, dedup.ContentID([]byte(content)))
	if err := os.WriteFile(path, []byte("corrupt"), 0o644); err != nil {
		t.Fatalf("corrupt blob: %v", err)
	}
}

// appendSizedTurn appends turn to thread id with a body, returned, sized so
// that at testShare the turn's excerpt text leaves left bytes after the
// thread header, the thread separator and itself.
func appendSizedTurn(t *testing.T, a *FileAdapter, id string, turn, left int) string {
	t.Helper()
	fm, err := store.LoadThreadFrontmatter(a.paths, id)
	if err != nil {
		t.Fatalf("LoadThreadFrontmatter %s: %v", id, err)
	}
	prefix := fmt.Sprintf("## Turn %d\n\n", turn)
	textLen := testShare - len(workset.RenderThreadHeader(id, fm)) - len(workset.ThreadSeparator) - left
	body := id + strings.Repeat("b", textLen-workset.ExcerptTextLen(prefix+id))
	if err := store.AppendThreadTurn(a.paths, id, turn, prefix+body); err != nil {
		t.Fatalf("AppendThreadTurn: %v", err)
	}
	return body
}

// composeLayerB composes with ids as Layer B, each at testShare.
func composeLayerB(t *testing.T, a *FileAdapter, ids ...string) memops.WorksetLayers {
	t.Helper()
	return composeWithLayerB(t, a, testShare*len(ids), ids...)
}

// composeWithLayerB composes with ids as Layer B at a LayerB of layerB.
func composeWithLayerB(t *testing.T, a *FileAdapter, layerB int, ids ...string) memops.WorksetLayers {
	t.Helper()
	budget := memops.DefaultBudget()
	budget.LayerB = layerB
	budget.BTopK = len(ids)
	out, err := a.ComposeWorkingSet(context.Background(), memops.WorksetInput{
		ActiveProject: memops.ProjectMeta{ID: "prj_1"},
		ActiveThreads: ids,
		Budget:        budget,
	})
	if err != nil {
		t.Fatalf("ComposeWorkingSet: %v", err)
	}
	return out
}

// diffRecord is the report record of the kept diff from content from to
// content to, of a path whose current content is current. Its size is
// the stored diff file's.
func diffRecord(t *testing.T, a *FileAdapter, thread string, turn int, path, from, to, current string) memops.FileExpansion {
	t.Helper()
	fromID, toID := dedup.ContentID([]byte(from)), dedup.ContentID([]byte(to))
	info, err := os.Stat(store.TrackedDiffPath(a.paths, thread, fromID, toID))
	if err != nil {
		t.Fatalf("stat stored diff: %v", err)
	}
	return memops.FileExpansion{
		ThreadID: thread, Turn: turn, Project: "prj_1", Path: path, Kind: "diff",
		From: fromID, To: toID, Size: int(info.Size()), PathSize: len(current),
	}
}

// assertReport fails unless out's report holds exactly want, in any
// order, at testLiteralMax.
func assertReport(t *testing.T, out memops.WorksetLayers, want []memops.FileExpansion) {
	t.Helper()
	if out.FileExpansions.LiteralMax != testLiteralMax {
		t.Errorf("LiteralMax = %d, want %d", out.FileExpansions.LiteralMax, testLiteralMax)
	}
	key := func(xs []memops.FileExpansion) []string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = fmt.Sprintf("%+v", x)
		}
		slices.Sort(out)
		return out
	}
	if got, want := key(out.FileExpansions.Expansions), key(want); !slices.Equal(got, want) {
		t.Errorf("report:\n got %v\nwant %v", got, want)
	}
}

// snapshotExcerpts returns the bytes of every file under a thread's
// turns/ directory and its folio, keyed by path.
func snapshotExcerpts(t *testing.T, a *FileAdapter, id string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(store.ThreadTurnsDir(a.paths, id), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		snap[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatalf("walk turns: %v", err)
	}
	folio, err := os.ReadFile(store.ThreadFolioPath(a.paths, id))
	if err != nil {
		t.Fatalf("read folio: %v", err)
	}
	snap["folio.jsonl"] = string(folio)
	return snap
}

func writeDigest(t *testing.T, paths store.PersonantPaths, d memops.ProjectDigest) {
	t.Helper()
	dir := filepath.Join(paths.ProjectsDir, d.Project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir digest dir: %v", err)
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("marshal digest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "digest.json"), data, 0o644); err != nil {
		t.Fatalf("write digest: %v", err)
	}
}

// assertEventLogContains scans every *.log file under the LogsDir and
// fails if no line matches all three substring needles. Used to verify
// adapter-side warnings hit the canonical destination.
func assertEventLogContains(t *testing.T, paths store.PersonantPaths, needles ...string) {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("event log dir missing — no warning emitted")
		}
		t.Fatalf("read logs dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			matched := true
			for _, n := range needles {
				if !strings.Contains(line, n) {
					matched = false
					break
				}
			}
			if matched {
				return
			}
		}
	}
	t.Errorf("expected event-log line containing %v; not found", needles)
}
