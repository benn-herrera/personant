package fileadapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/memops"
	"personant/internal/store"
	"personant/internal/version"
)

func TestHomeFormat_ScaffoldedByInit(t *testing.T) {
	a := newAdapter(t)

	found, format, err := a.HomeFormat(context.Background())
	if err != nil {
		t.Fatalf("HomeFormat: %v", err)
	}
	if !found || format != version.CurrentHomeFormat {
		t.Errorf("HomeFormat = (%v, %d), want (true, %d)", found, format, version.CurrentHomeFormat)
	}
}

func TestHomeFormat_UnstampedHomeIsNotAnError(t *testing.T) {
	a := newAdapter(t)
	if err := os.Remove(a.paths.HomeVersion); err != nil {
		t.Fatalf("remove stamp: %v", err)
	}

	found, format, err := a.HomeFormat(context.Background())
	if err != nil {
		t.Fatalf("HomeFormat: %v", err)
	}
	if found || format != 0 {
		t.Errorf("HomeFormat = (%v, %d), want (false, 0) for an unstamped home", found, format)
	}
}

func TestStampHomeFormat_RoundTrips(t *testing.T) {
	a := newAdapter(t)
	ctx := context.Background()

	if err := a.StampHomeFormat(ctx, 5); err != nil {
		t.Fatalf("StampHomeFormat: %v", err)
	}
	found, format, err := a.HomeFormat(ctx)
	if err != nil {
		t.Fatalf("HomeFormat: %v", err)
	}
	if !found || format != 5 {
		t.Errorf("HomeFormat = (%v, %d), want (true, 5)", found, format)
	}

	if err := a.StampHomeFormat(ctx, 0); err == nil {
		t.Error("StampHomeFormat(0) = nil, want an error")
	}
}

func TestHomeFormat_CorruptStampErrors(t *testing.T) {
	a := newAdapter(t)
	if err := os.WriteFile(a.paths.HomeVersion, []byte("format = \"one\"\n"), 0o644); err != nil {
		t.Fatalf("write corrupt stamp: %v", err)
	}
	if _, _, err := a.HomeFormat(context.Background()); err == nil {
		t.Error("HomeFormat on a corrupt stamp = nil error, want an error")
	}
}

// TestHomeFormat_Migrate2To3RemovesLegacySidecar: a format-2 home holding
// a legacy files.json passes the gate as a 2→3 migration, is restamped to
// 3, and loses the sidecar at its first day barrier.
func TestHomeFormat_Migrate2To3RemovesLegacySidecar(t *testing.T) {
	a := newBarrierHome(t, 1)
	ctx := context.Background()
	if err := a.StampHomeFormat(ctx, 2); err != nil {
		t.Fatalf("stamp format 2: %v", err)
	}
	legacy := filepath.Join(store.ThreadDir(a.paths, "thr_1"), "files.json")
	if err := os.WriteFile(legacy, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write files.json: %v", err)
	}

	found, format, err := a.HomeFormat(ctx)
	if err != nil {
		t.Fatalf("HomeFormat: %v", err)
	}
	d, err := memops.GateHomeFormat(found, format, version.CurrentHomeFormat, false)
	if err != nil {
		t.Fatalf("GateHomeFormat: %v", err)
	}
	if d.StampFormat != 3 || d.MigratedFrom != 2 {
		t.Fatalf("gate decision = %+v, want StampFormat 3, MigratedFrom 2", d)
	}
	if err := a.StampHomeFormat(ctx, d.StampFormat); err != nil {
		t.Fatalf("StampHomeFormat: %v", err)
	}
	if found, format, err := a.HomeFormat(ctx); err != nil || !found || format != 3 {
		t.Fatalf("HomeFormat after stamp = (%v, %d, %v), want (true, 3)", found, format, err)
	}
	if _, err := a.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	pinBarrierClock(t, bDay1)
	res, err := a.MaybeDayBarrier(ctx)
	if err != nil || len(res.DaysSealed) != 1 {
		t.Fatalf("first barrier = %+v, %v; want one day sealed", res, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("files.json after the first barrier: stat err %v, want absent", err)
	}
}

func TestHomeFormat_HonorsCanceledContext(t *testing.T) {
	a := newAdapter(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := a.HomeFormat(ctx); err == nil {
		t.Error("HomeFormat with a canceled context = nil error, want ctx.Err()")
	}
	if err := a.StampHomeFormat(ctx, version.CurrentHomeFormat); err == nil {
		t.Error("StampHomeFormat with a canceled context = nil error, want ctx.Err()")
	}
	// The refused write must not have touched the substrate.
	found, format, err := store.ReadHomeFormat(a.paths)
	if err != nil {
		t.Fatalf("ReadHomeFormat: %v", err)
	}
	if !found || format != version.CurrentHomeFormat {
		t.Errorf("stamp = (%v, %d) after a canceled write, want the init value", found, format)
	}
}
