package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
)

// dummyRecallerFactory is a non-nil Scenario.Recaller stand-in for the mode
// derivation test; it is never invoked.
func dummyRecallerFactory(memops.MemoryOps) measure.Recaller { return nil }

// readManifest loads and decodes a run home's manifest.json.
func readManifest(t *testing.T, runHome string) RunManifest {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(runHome, RunManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var man RunManifest
	if err := json.Unmarshal(body, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return man
}

// TestRunManifestStartAndCompletion drives writeRunManifest exactly as
// RunScenario does — once at start (End empty: the crashed-run signature)
// and once at completion — and checks the provenance fields land.
func TestRunManifestStartAndCompletion(t *testing.T) {
	h := &Harness{T: t, RunHome: t.TempDir()}
	sc := Scenario{
		Name:      "manifest-probe",
		RungLabel: "sim-live-1d",
		RungSeed:  0x5e1f,
		RungSpan:  24 * time.Hour,
	}

	writeRunManifest(h, sc, "2026-07-16T17:00:00Z", "")
	man := readManifest(t, h.RunHome)
	if man.End != "" {
		t.Errorf("start-time manifest End = %q, want empty (crashed-run signature)", man.End)
	}
	if man.Scenario != "manifest-probe" || man.Rung != "sim-live-1d" ||
		man.Seed != 0x5e1f || man.Duration != "24h0m0s" || man.Start != "2026-07-16T17:00:00Z" {
		t.Errorf("manifest provenance fields wrong: %+v", man)
	}
	if man.GitHead == "" {
		t.Errorf("manifest GitHead must never be empty (\"unknown\" is the no-git fallback)")
	}

	writeRunManifest(h, sc, "2026-07-16T17:00:00Z", "2026-07-16T17:05:00Z")
	if man = readManifest(t, h.RunHome); man.End != "2026-07-16T17:05:00Z" {
		t.Errorf("completion manifest End = %q, want the completion stamp", man.End)
	}
}

// TestRunManifestModeDerivation pins the mode string to what the harness
// actually installed, so a mock dir cannot pass for a live run's.
func TestRunManifestModeDerivation(t *testing.T) {
	fakeClient := model.NewScriptedMock(nil, nil)
	cases := []struct {
		name string
		h    *Harness
		want string
	}{
		{"mock", &Harness{}, "mock"},
		{"live-inference", &Harness{liveClient: fakeClient}, "live-inference"},
		{"live-embedding", &Harness{recallerFactory: dummyRecallerFactory}, "live-embedding"},
		{"both", &Harness{liveClient: fakeClient, recallerFactory: dummyRecallerFactory}, "live-inference+live-embedding"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runMode(tc.h); got != tc.want {
				t.Errorf("runMode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunManifestOmitsZeroProvenance: a handwritten scenario (no rung
// fields) yields a manifest without rung/seed/duration keys — present
// fields always mean something.
func TestRunManifestOmitsZeroProvenance(t *testing.T) {
	h := &Harness{T: t, RunHome: t.TempDir()}
	writeRunManifest(h, Scenario{Name: "plain"}, "2026-07-16T17:00:00Z", "")

	body, err := os.ReadFile(filepath.Join(h.RunHome, RunManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, key := range []string{"rung", "seed", "duration", "end"} {
		if _, present := raw[key]; present {
			t.Errorf("zero-provenance manifest must omit %q, got %v", key, raw[key])
		}
	}
}
