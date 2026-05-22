package turn

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/model"
	"personant/internal/store"
)

func sym(norm string, count, firstSeen int) memops.HistorySymbol {
	return memops.HistorySymbol{
		Raw:           norm,
		Normalized:    norm,
		FirstSeenTurn: firstSeen,
		Count:         count,
		Source:        memops.SourceModel,
	}
}

// TestProjectAnchorsRanking checks the (class → Count → recency → Normalized)
// ordering and the top-max truncation.
func TestProjectAnchorsRanking(t *testing.T) {
	syms := []memops.HistorySymbol{
		sym("low", 1, 1),
		sym("high", 10, 1),
		sym("mid", 5, 1),
	}
	anchors, _, _ := ProjectAnchors(syms, 8, 5)
	if got := strings.Join(anchors, ","); got != "high,mid,low" {
		t.Errorf("ranking by Count desc: got %q want high,mid,low", got)
	}

	// Truncation to max keeps the top-max only.
	anchors, _, _ = ProjectAnchors(syms, 2, 5)
	if got := strings.Join(anchors, ","); got != "high,mid" {
		t.Errorf("top-max=2: got %q want high,mid", got)
	}
}

// TestProjectAnchorsEverCentralLatch confirms that entering the projection
// latches EverCentral=true and stamps LastActiveTurn, and that a symbol that
// never enters is left untouched.
func TestProjectAnchorsEverCentralLatch(t *testing.T) {
	syms := []memops.HistorySymbol{
		sym("in1", 10, 1),
		sym("in2", 8, 1),
		sym("out", 1, 1),
	}
	_, updated, _ := ProjectAnchors(syms, 2, 7)

	byNorm := map[string]memops.HistorySymbol{}
	for _, s := range updated {
		byNorm[s.Normalized] = s
	}
	for _, n := range []string{"in1", "in2"} {
		if !byNorm[n].EverCentral {
			t.Errorf("%s entered projection but EverCentral not latched", n)
		}
		if byNorm[n].LastActiveTurn != 7 {
			t.Errorf("%s LastActiveTurn = %d, want 7", n, byNorm[n].LastActiveTurn)
		}
		if byNorm[n].Lifecycle != memops.LifecycleActive {
			t.Errorf("%s lifecycle = %q, want active", n, byNorm[n].Lifecycle)
		}
	}
	if byNorm["out"].EverCentral {
		t.Errorf("out never entered projection but EverCentral latched")
	}
	if byNorm["out"].LastActiveTurn != 0 {
		t.Errorf("out LastActiveTurn = %d, want 0 (never active)", byNorm["out"].LastActiveTurn)
	}
}

// TestProjectAnchorsZeroInput: an empty symbol set projects to an empty
// (non-nil) anchor slice with changed=false.
func TestProjectAnchorsZeroInput(t *testing.T) {
	anchors, updated, changed := ProjectAnchors(nil, 8, 1)
	if len(anchors) != 0 {
		t.Errorf("zero input: got %d anchors, want 0", len(anchors))
	}
	if len(updated) != 0 {
		t.Errorf("zero input: got %d symbols, want 0", len(updated))
	}
	if changed {
		t.Errorf("zero input: changed=true, want false (empty prior == empty new)")
	}
}

// TestProjectAnchorsSupersession: a symbol that WAS ever-central and falls
// out of the top-max flips to LifecycleSuperseded (rank-dropout, no TTL).
func TestProjectAnchorsSupersession(t *testing.T) {
	// "old" was already central (EverCentral, active); two stronger discovery
	// symbols now outrank it with max=2.
	syms := []memops.HistorySymbol{
		{Raw: "old", Normalized: "old", FirstSeenTurn: 1, Count: 3, Source: memops.SourceModel, EverCentral: true, Lifecycle: memops.LifecycleActive, LastActiveTurn: 1},
		sym("new1", 10, 5),
		sym("new2", 9, 5),
	}
	anchors, updated, changed := ProjectAnchors(syms, 2, 6)
	if got := strings.Join(anchors, ","); got != "new1,new2" {
		t.Errorf("projection = %q, want new1,new2", got)
	}
	if !changed {
		t.Errorf("changed=false, want true (headline moved off 'old')")
	}
	for _, s := range updated {
		if s.Normalized == "old" {
			if s.Lifecycle != memops.LifecycleSuperseded {
				t.Errorf("old lifecycle = %q, want superseded (rank-dropout)", s.Lifecycle)
			}
			if !s.EverCentral {
				t.Errorf("old EverCentral cleared; must never clear")
			}
		}
	}
}

// TestProjectAnchorsChangedFlag: re-projecting an already-projected set with
// no rank change yields changed=false (idempotent-write guard input).
func TestProjectAnchorsChangedFlag(t *testing.T) {
	syms := []memops.HistorySymbol{
		sym("a", 5, 1),
		sym("b", 3, 1),
	}
	// First projection: prior is empty, so changed=true.
	_, updated, changed := ProjectAnchors(syms, 8, 1)
	if !changed {
		t.Fatalf("first projection: changed=false, want true (empty→{a,b})")
	}
	// Second projection over the now-latched symbols, same ranks: no set
	// change → changed=false.
	_, _, changed = ProjectAnchors(updated, 8, 2)
	if changed {
		t.Errorf("re-projection with unchanged set: changed=true, want false")
	}
}

// TestProjectAnchorsB11Class: a low-Count high-specificity identifier
// outranks a high-Count generic word (class beats Count in the comparator),
// so it makes the headline.
func TestProjectAnchorsB11Class(t *testing.T) {
	syms := []memops.HistorySymbol{
		sym("system", 100, 1), // generic, high Count, class 0
		{Raw: "internal/turn/history.go", Normalized: "internal/turn/history.go", FirstSeenTurn: 2, Count: 1, Source: memops.SourceModel}, // B11, class 1
	}
	anchors, _, _ := ProjectAnchors(syms, 1, 3)
	if len(anchors) != 1 || anchors[0] != "internal/turn/history.go" {
		t.Errorf("B11 class should win the single slot: got %v", anchors)
	}
}

// TestEvictionUnionProtection: the eviction protection predicate is the union
// EverCentral || IsHighSpecificity. An EverCentral generic word (not B11)
// must survive against many higher-Count never-central words.
func TestEvictionUnionProtection(t *testing.T) {
	var syms []memops.HistorySymbol
	for i := range historyCapPerThread {
		s := sym("word"+strconv.Itoa(i), 100, i+1)
		syms = append(syms, s)
	}
	// One low-Count, generic-Raw, but EverCentral symbol — protected only by
	// the EverCentral half of the union (it is NOT high-specificity).
	syms = append(syms, memops.HistorySymbol{
		Raw: "abandoned-premise", Normalized: "abandoned-premise",
		FirstSeenTurn: 500, Count: 1, Source: memops.SourceModel,
		EverCentral: true, Lifecycle: memops.LifecycleSuperseded,
	})
	out := capHistorySymbols(syms)
	if len(out) != historyCapPerThread {
		t.Fatalf("cap: got %d want %d", len(out), historyCapPerThread)
	}
	found := false
	for _, s := range out {
		if s.Normalized == "abandoned-premise" {
			found = true
		}
	}
	if !found {
		t.Errorf("EverCentral symbol evicted; union predicate must protect it")
	}
}

// TestEvictionSupersededFirstDegradation: when the protected set ALONE
// overflows the cap, the lowest-Count SUPERSEDED protected entries are
// evicted before any active ever-central one.
func TestEvictionSupersededFirstDegradation(t *testing.T) {
	const n = 50
	var syms []memops.HistorySymbol
	// 45 ACTIVE ever-central symbols, Count 1..45 (low Count among them).
	for i := range 45 {
		syms = append(syms, memops.HistorySymbol{
			Raw: "active" + strconv.Itoa(i), Normalized: "active" + strconv.Itoa(i),
			FirstSeenTurn: i + 1, Count: i + 1, Source: memops.SourceModel,
			EverCentral: true, Lifecycle: memops.LifecycleActive,
		})
	}
	// 5 SUPERSEDED ever-central symbols with HIGH Count — high weight, but
	// superseded, so they must be evicted FIRST under degradation despite
	// outranking the active ones on Count alone.
	for i := range 5 {
		syms = append(syms, memops.HistorySymbol{
			Raw: "superseded" + strconv.Itoa(i), Normalized: "superseded" + strconv.Itoa(i),
			FirstSeenTurn: 100 + i, Count: 1000, Source: memops.SourceModel,
			EverCentral: true, Lifecycle: memops.LifecycleSuperseded,
		})
	}
	if len(syms) != n {
		t.Fatalf("setup: got %d symbols want %d", len(syms), n)
	}
	out := capHistorySymbols(syms)
	if len(out) != historyCapPerThread {
		t.Fatalf("cap must hold under protected overflow: got %d want %d", len(out), historyCapPerThread)
	}
	for _, s := range out {
		if s.Lifecycle == memops.LifecycleSuperseded {
			t.Errorf("superseded %q survived but should be evicted first under degradation", s.Normalized)
		}
	}
}

// TestIdempotentWriteGuard drives two identical owner turns against an
// existing thread. The first turn moves the headline (changed=true) and
// stamps AnchorsProjectedAtTurn; the second emits the same symbols, so the
// projected SET is unchanged (changed=false) and the watermark must NOT be
// bumped — the idempotent-write guard.
func TestIdempotentWriteGuard(t *testing.T) {
	paths, meta := newTestHome(t)
	existing := memops.SpineRecord{
		ID: "thr_7", Project: meta.ID,
		Anchors: nil, Summary: "t", State: memops.ThreadActive, TurnCount: 0,
	}
	if err := store.AppendSpineRecord(paths, existing); err != nil {
		t.Fatalf("append: %v", err)
	}
	state := NewState(fileadapter.NewFileAdapter(paths), meta, memops.Provider{}, model.NewScriptedMock(nil, nil))
	pinClock(t, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))

	if state.coalesce == nil {
		state.coalesce = newCoalesceBuffer()
	}
	driveOwnerTurn := func() memops.SpineRecord {
		t.Helper()
		state.coalesce.reset()
		state.fileEdits = state.fileEdits[:0]
		state.turnOwner = ""
		state.TurnNumber++
		if err := onContextDelta(context.Background(), state,
			Delta{Source: "model.response", Content: "*topic: thr_7 [alpha, beta]*\nReply."}); err != nil {
			t.Fatalf("delta: %v", err)
		}
		if err := closeTurnAndUpdateEngagement(context.Background(), state, "in", "Reply."); err != nil {
			t.Fatalf("close: %v", err)
		}
		rec, found, err := store.FindSpineRecord(paths, "thr_7")
		if err != nil || !found {
			t.Fatalf("find: %v found=%v", err, found)
		}
		return rec
	}

	first := driveOwnerTurn()
	if first.AnchorsProjectedAtTurn == 0 {
		t.Fatalf("first turn: watermark not stamped (changed should be true), got %d", first.AnchorsProjectedAtTurn)
	}
	if strings.Join(first.Anchors, ",") != "alpha,beta" {
		t.Fatalf("first turn anchors = %v, want alpha,beta", first.Anchors)
	}

	second := driveOwnerTurn()
	if second.AnchorsProjectedAtTurn != first.AnchorsProjectedAtTurn {
		t.Errorf("watermark bumped on unchanged set: first=%d second=%d (idempotent-write guard violated)",
			first.AnchorsProjectedAtTurn, second.AnchorsProjectedAtTurn)
	}
}

// TestProjectionNeverEvictsEntrantThisTurn is the R5 correctness test: a
// symbol that ENTERS the projection this turn (latching EverCentral) is never
// evicted this turn, because eviction runs AFTER projection and consumes the
// freshly-latched flag. The merge → project → evict ordering is exercised by
// composing the helpers in that order.
func TestProjectionNeverEvictsEntrantThisTurn(t *testing.T) {
	// A full cap of high-Count generic words plus one brand-new low-Count
	// symbol that, by class, the projection will pick (it is high-spec).
	var merged []memops.HistorySymbol
	for i := range historyCapPerThread {
		merged = append(merged, sym("noise"+strconv.Itoa(i), 100, i+1))
	}
	// Entrant: a high-specificity identifier, Count 1 — lowest weight, but it
	// will enter the projection by class and must therefore survive eviction.
	entrant := memops.HistorySymbol{
		Raw: "https://example.com/x", Normalized: "https://example.com/x",
		FirstSeenTurn: 999, Count: 1, Source: memops.SourceModel,
	}
	merged = append(merged, entrant)

	// merge → PROJECT → evict (the engage.go owner-turn order).
	anchors, projected, _ := ProjectAnchors(merged, memops.AnchorProjectionMax, 999)
	inHeadline := false
	for _, a := range anchors {
		if a == entrant.Normalized {
			inHeadline = true
		}
	}
	if !inHeadline {
		t.Fatalf("test premise broken: entrant did not enter the projection")
	}
	out := capHistorySymbols(projected)

	// The entrant entered the projection this turn → EverCentral latched →
	// protected by the union predicate → must survive eviction this turn.
	survived := false
	for _, s := range out {
		if s.Normalized == entrant.Normalized {
			survived = true
			if !s.EverCentral {
				t.Errorf("entrant survived but EverCentral not latched")
			}
		}
	}
	if !survived {
		t.Errorf("symbol entering the projection this turn was evicted this turn (R5 violation)")
	}
}
