package turn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/memops/fileadapter"
	"personant/internal/store"
)

// recordingOps wraps a real MemoryOps and captures every EmitDelta call's
// Content field — the exact bytes the §3.0 chain step-5 hands to the
// substrate side. Embedding the port means the 40 unrelated methods
// delegate to the real adapter (so the on-disk event log is still
// written), while EmitDelta is intercepted to assert the §3.10.6 contract
// at the boundary it actually governs: what content crosses into the
// persistent layer.
type recordingOps struct {
	memops.MemoryOps
	emitted []memops.Delta
}

func (r *recordingOps) EmitDelta(ctx context.Context, delta memops.Delta) error {
	r.emitted = append(r.emitted, delta)
	return r.MemoryOps.EmitDelta(ctx, delta)
}

// TestChainTaskClassMinimization_LogsOnlyMetadata proves (acceptance a)
// that a tool.result and a user.shell-capture delta reach the persistent
// event log as a metadata summary only — never the raw body. The raw
// payload is a unique sentinel; both the content crossing the EmitDelta
// boundary AND the bytes on disk must be free of it.
func TestChainTaskClassMinimization_LogsOnlyMetadata(t *testing.T) {
	const sentinel = "RAW-SECRET-BODY-must-not-persist-9f3a"

	cases := []struct {
		name   string
		source string
		meta   map[string]string
	}{
		{"tool.result", memops.SourceToolResult, map[string]string{"path": "build/out.txt"}},
		{"user.shell-capture", memops.SourceUserShellCapture, map[string]string{"path": "cmd:ls"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths, meta := newChainHome(t)
			rec := &recordingOps{MemoryOps: fileadapter.NewFileAdapter(paths)}
			state := NewState(rec, meta, memops.Provider{}, nil)

			raw := "line1 " + sentinel + " line2 " + strings.Repeat("x", 4096)
			if err := onContextDelta(context.Background(), state, Delta{
				Source:  tc.source,
				Content: raw,
				Meta:    tc.meta,
			}); err != nil {
				t.Fatalf("onContextDelta: %v", err)
			}

			// Boundary contract: the Content handed to EmitDelta is the
			// metadata summary, not the raw body.
			if len(rec.emitted) != 1 {
				t.Fatalf("expected 1 EmitDelta call, got %d", len(rec.emitted))
			}
			got := rec.emitted[0].Content
			if strings.Contains(got, sentinel) {
				t.Fatalf("§3.10.6 violation: raw body leaked into EmitDelta content for %s:\n%q", tc.source, got)
			}
			// The summary must carry the metadata fields (source, path, byte count).
			for _, want := range []string{tc.source, "path=" + tc.meta["path"], "bytes="} {
				if !strings.Contains(got, want) {
					t.Errorf("metadata summary missing %q; got %q", want, got)
				}
			}

			// On-disk contract: no log file under LogsDir contains the
			// raw body either.
			if disk := readChainLogs(t, paths); strings.Contains(disk, sentinel) {
				t.Fatalf("§3.10.6 violation: raw body reached the persistent event log for %s", tc.source)
			}
		})
	}
}

// TestChainDecisionClassNotMinimized guards the other side of the gate:
// a decision-class delta (model.response) is NOT minimized — its content
// is intentionally persistable. This keeps the task-class gate from
// over-reaching and silently summarizing decision content.
func TestChainDecisionClassNotMinimized(t *testing.T) {
	const body = "DECISION-BODY-keep-9f3a"
	paths, meta := newChainHome(t)
	rec := &recordingOps{MemoryOps: fileadapter.NewFileAdapter(paths)}
	state := NewState(rec, meta, memops.Provider{}, nil)

	if err := onContextDelta(context.Background(), state, Delta{
		Source:  memops.SourceModelResponse,
		Content: body,
	}); err != nil {
		t.Fatalf("onContextDelta: %v", err)
	}
	if len(rec.emitted) != 1 {
		t.Fatalf("expected 1 EmitDelta call, got %d", len(rec.emitted))
	}
	if rec.emitted[0].Content != body {
		t.Errorf("decision-class content should pass through unminimized; got %q want %q",
			rec.emitted[0].Content, body)
	}
}

// TestEventTaxonomyHasNoUnroutedEvents proves (acceptance b) that the
// §3.0.1 event taxonomy declares no event the runtime never emits as a
// delta. The three pruned events (m2) — digest.refresh, slash.injected,
// directive.reloaded — must not appear as taxonomy entries. We assert
// against the §3.0.1 table specifically: each pruned name must not occur
// as a backticked row token inside §3.0.1, while the prose explanation of
// the prune may name them.
func TestEventTaxonomyHasNoUnroutedEvents(t *testing.T) {
	section := readSpecSection(t, "#### 3.0.1")

	pruned := []string{"digest.refresh", "slash.injected", "directive.reloaded"}
	for _, ev := range pruned {
		// A taxonomy row declares an event as a leading table cell:
		// "| `digest.refresh` |". The explanatory prose references the
		// names with surrounding text, never as a leading "| `…` |" cell.
		rowToken := "| `" + ev + "`"
		if strings.Contains(section, rowToken) {
			t.Errorf("§3.0.1 still declares unrouted event %q as a taxonomy row — "+
				"runtime never emits it as a delta (m2 prune incomplete)", ev)
		}
	}

	// The events the runtime DOES emit must remain present as rows.
	for _, ev := range []string{"user.prompt", "model.response", "tool.result",
		"thread.fetched", "user.shell-capture"} {
		if !strings.Contains(section, "| `"+ev+"`") {
			t.Errorf("§3.0.1 dropped an actually-emitted event %q from the taxonomy", ev)
		}
	}
}

// readChainLogs concatenates every *.log file under LogsDir.
func readChainLogs(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	entries, err := os.ReadDir(paths.LogsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read logs dir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.LogsDir, e.Name()))
		if err != nil {
			t.Fatalf("read log %s: %v", e.Name(), err)
		}
		b.Write(data)
	}
	return b.String()
}

// readSpecSection returns the body of the SPEC.md subsection beginning
// with the given heading, up to the next "#### " heading. SPEC.md lives
// two directories above the test package (internal/turn).
func readSpecSection(t *testing.T, heading string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "SPEC.md"))
	if err != nil {
		t.Fatalf("read SPEC.md: %v", err)
	}
	text := string(data)
	start := strings.Index(text, heading)
	if start < 0 {
		t.Fatalf("SPEC.md: heading %q not found", heading)
	}
	rest := text[start+len(heading):]
	if end := strings.Index(rest, "\n#### "); end >= 0 {
		return text[start : start+len(heading)+end]
	}
	return text[start:]
}
