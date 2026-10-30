package opencode

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"personant/internal/memops"
	"personant/internal/scenarios"
	"personant/internal/turn"
)

var dropSource = flag.String("probe.drop-source", "",
	"delta source whose first delta the memory service skips: fs.read, fs.write, fs.commit or model.response")

// TestProbeSmoke drives one handwritten turn through real opencode at
// checkpoint cadence 1. With no drop source every probe count must be 0
// and a checkpoint must run; with one, the missed-delta detector must fire.
func TestProbeSmoke(t *testing.T) {
	if os.Getenv("PERSONANT_OPENCODE_PROBE") != "1" {
		t.Skip("set PERSONANT_OPENCODE_PROBE=1 (just probe-smoke) to run against the installed opencode")
	}
	switch *dropSource {
	case "", memops.SourceFSRead, memops.SourceFSWrite, memops.SourceFSCommit, memops.SourceModelResponse:
	default:
		t.Fatalf("-probe.drop-source=%q: the service can only skip a tool or model.response delta", *dropSource)
	}

	const path = "internal/probe/smoke.go"
	read := "package probe\n\n// smoke fixture #probe-smoke\n"
	written := read + "\n// edit 1: probe smoke — #probe-smoke\n"
	ex := NewExecutor(1, *dropSource)
	h := scenarios.RunScenario(t, scenarios.Scenario{
		Name:         "probe-smoke",
		Setup:        ex.Setup,
		TurnExecutor: ex.Exec,
		Steps: []scenarios.Step{{
			UserInput: "Edit the probe smoke fixture. #probe-smoke",
			PreEvents: []turn.Delta{
				{Source: memops.SourceFSRead, Content: read, Meta: map[string]string{"path": path}},
				{Source: memops.SourceFSWrite, Content: written, Meta: map[string]string{"path": path}},
				{Source: memops.SourceFSCommit, Meta: map[string]string{"path": path}},
			},
			MockResponse: scenarios.NewMockResponseWithTag(
				[]string{"*new-topic*"}, []string{"probe-smoke", "fixture", "edit"}, "Edited the fixture."),
		}},
	})
	if err := ex.Finish(h); err != nil {
		t.Fatalf("finish: %v", err)
	}

	m := h.Metrics
	counts := map[string]int64{}
	for _, k := range []string{scenarios.MetricOCMissedDeltas, scenarios.MetricOCHookFallbacks,
		scenarios.MetricOCUncomposedRequests, scenarios.MetricCheckpointsPerformed} {
		counts[k] = m.CounterValue(k)
	}
	touched := m.GaugeValue(scenarios.MetricOCUserDBTouched)
	t.Logf("probe smoke (drop-source=%q): %v %s=%v %s=%v %s=%v %s=%v", *dropSource, counts,
		scenarios.MetricOCUserDBTouched, touched,
		scenarios.MetricOCHistoryMessages, m.HistogramSnapshot(scenarios.MetricOCHistoryMessages),
		scenarios.MetricOCTurnMessages, m.HistogramSnapshot(scenarios.MetricOCTurnMessages),
		scenarios.MetricOCHookRTTMs, m.HistogramSnapshot(scenarios.MetricOCHookRTTMs))
	if got, err := os.ReadFile(filepath.Join(ex.drv.Workspace, path)); err != nil || string(got) != written {
		t.Logf("workspace %s after the turn differs from the fs.write content (err %v):\n%s", path, err, got)
	}

	if *dropSource != "" {
		if counts[scenarios.MetricOCMissedDeltas] < 1 {
			t.Errorf("%s = 0 with drop-source %q: the detector did not fire", scenarios.MetricOCMissedDeltas, *dropSource)
		}
		return
	}
	for _, k := range []string{scenarios.MetricOCMissedDeltas, scenarios.MetricOCHookFallbacks, scenarios.MetricOCUncomposedRequests} {
		if counts[k] != 0 {
			t.Errorf("%s = %d, want 0", k, counts[k])
		}
	}
	if touched != 0 {
		t.Errorf("%s = %v, want 0", scenarios.MetricOCUserDBTouched, touched)
	}
	if counts[scenarios.MetricCheckpointsPerformed] < 1 {
		t.Errorf("%s = 0, want >= 1 at cadence 1", scenarios.MetricCheckpointsPerformed)
	}
}
