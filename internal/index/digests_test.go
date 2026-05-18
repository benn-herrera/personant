package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"personant/internal/memops"
)

func mkSpineEngaged(id, project, lastEngaged string, anchors ...string) memops.SpineRecord {
	return memops.SpineRecord{
		ID: id, Project: project, Anchors: anchors,
		Summary: "s", State: memops.ThreadActive,
		Created: "2026-05-01T00:00:00Z", LastEngaged: lastEngaged, StateChanged: lastEngaged,
		TurnCount: 1, RecallFires: 0,
	}
}

// captureWarnf returns a warner func and a pointer to the captured lines.
func captureWarnf() (func(format string, args ...any), *[]string) {
	var lines []string
	return func(format string, args ...any) {
		lines = append(lines, format)
	}, &lines
}

func TestBuildDigestsEmptySpine(t *testing.T) {
	got, err := BuildDigests(nil, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("BuildDigests: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d digests, want 0", len(got))
	}
}

func TestBuildDigestsThreeThreadsOneProject(t *testing.T) {
	dir := t.TempDir()
	spine := []memops.SpineRecord{
		mkSpineEngaged("thr_1", "prj_1", "2026-05-01T00:00:00Z", "alpha", "beta"),
		mkSpineEngaged("thr_2", "prj_1", "2026-05-02T00:00:00Z", "alpha", "gamma"),
		mkSpineEngaged("thr_3", "prj_1", "2026-05-03T00:00:00Z", "alpha", "delta"),
	}
	warner, _ := captureWarnf()
	digests, err := BuildDigests(spine, dir, warner)
	if err != nil {
		t.Fatalf("BuildDigests: %v", err)
	}
	d, ok := digests["prj_1"]
	if !ok {
		t.Fatalf("missing digest for prj_1: %#v", digests)
	}
	if d.Project != "prj_1" {
		t.Errorf("project %q, want prj_1", d.Project)
	}
	if d.ThreadCount != 3 {
		t.Errorf("thread_count %d, want 3", d.ThreadCount)
	}
	if len(d.RecentAnchors) == 0 {
		t.Errorf("recent_anchors empty")
	}
	// alpha appears in all three threads, must lead.
	if d.RecentAnchors[0] != "alpha" {
		t.Errorf("recent_anchors[0] = %q, want alpha", d.RecentAnchors[0])
	}
	if d.OneLineSummary == "" {
		t.Errorf("one_line_summary empty")
	}
	if d.ByteSize <= 0 {
		t.Errorf("byte_size = %d, want > 0", d.ByteSize)
	}
}

func TestBuildDigestsOneLineSummaryTruncatedTo80(t *testing.T) {
	// Ten anchors of length 20 each → joined ~218 chars; must truncate to ≤ 80.
	spine := []memops.SpineRecord{
		mkSpineEngaged("thr_1", "prj_1", "2026-05-01T00:00:00Z",
			"anchor-aaaaaaaaaaaaa", "anchor-bbbbbbbbbbbbb",
			"anchor-ccccccccccccc", "anchor-ddddddddddddd",
			"anchor-eeeeeeeeeeeee", "anchor-fffffffffffff",
			"anchor-ggggggggggggg", "anchor-hhhhhhhhhhhhh",
		),
	}
	digests, err := BuildDigests(spine, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("BuildDigests: %v", err)
	}
	d := digests["prj_1"]
	if got := len(d.OneLineSummary); got > 80 {
		t.Errorf("one_line_summary length %d > 80: %q", got, d.OneLineSummary)
	}
}

func TestBuildDigestsMultipleProjects(t *testing.T) {
	spine := []memops.SpineRecord{
		mkSpineEngaged("thr_1", "prj_1", "2026-05-01T00:00:00Z", "alpha"),
		mkSpineEngaged("thr_2", "prj_2", "2026-05-02T00:00:00Z", "beta"),
		mkSpineEngaged("thr_3", "prj_2", "2026-05-03T00:00:00Z", "gamma"),
	}
	digests, err := BuildDigests(spine, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("BuildDigests: %v", err)
	}
	if len(digests) != 2 {
		t.Fatalf("got %d digests, want 2: %#v", len(digests), digests)
	}
	if digests["prj_1"].ThreadCount != 1 || digests["prj_2"].ThreadCount != 2 {
		t.Errorf("thread counts wrong: prj_1=%d, prj_2=%d", digests["prj_1"].ThreadCount, digests["prj_2"].ThreadCount)
	}
}

func TestBuildDigestsPrjDefault(t *testing.T) {
	dir := t.TempDir()
	spine := []memops.SpineRecord{
		mkSpineEngaged("thr_1", "prj_default", "2026-05-01T00:00:00Z", "alpha"),
	}
	digests, err := BuildDigests(spine, dir, nil)
	if err != nil {
		t.Fatalf("BuildDigests: %v", err)
	}
	d, ok := digests["prj_default"]
	if !ok {
		t.Fatal("missing digest for prj_default")
	}
	if d.DisplayName != "default" {
		t.Errorf("prj_default display_name = %q, want default", d.DisplayName)
	}
}

func TestBuildDigestsMissingMetaWarnsAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	spine := []memops.SpineRecord{
		mkSpineEngaged("thr_1", "prj_42", "2026-05-01T00:00:00Z", "alpha"),
	}
	warner, lines := captureWarnf()
	digests, err := BuildDigests(spine, dir, warner)
	if err != nil {
		t.Fatalf("BuildDigests: %v", err)
	}
	d := digests["prj_42"]
	if d.DisplayName != "prj_42" {
		t.Errorf("display_name = %q, want prj_42 (fallback)", d.DisplayName)
	}
	if len(*lines) == 0 {
		t.Error("expected at least one warning line")
	}
}

func TestBuildDigestsReadsMetaName(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "prj_3")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := memops.ProjectMeta{ID: "prj_3", Name: "ave-kb"}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(projectDir, "meta.json"), metaBytes, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	spine := []memops.SpineRecord{
		mkSpineEngaged("thr_1", "prj_3", "2026-05-01T00:00:00Z", "alpha"),
	}
	digests, err := BuildDigests(spine, dir, nil)
	if err != nil {
		t.Fatalf("BuildDigests: %v", err)
	}
	if digests["prj_3"].DisplayName != "ave-kb" {
		t.Errorf("display_name = %q, want ave-kb", digests["prj_3"].DisplayName)
	}
}

func TestWriteDigestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	digest := memops.ProjectDigest{
		Project: "prj_1", DisplayName: "demo", ThreadCount: 2,
		RecentAnchors: []string{"alpha", "beta"}, OneLineSummary: "alpha, beta", ByteSize: 0,
	}
	if err := WriteDigest(dir, "prj_1", digest); err != nil {
		t.Fatalf("WriteDigest: %v", err)
	}
	path := filepath.Join(dir, "prj_1", "digest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got memops.ProjectDigest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, digest) {
		t.Errorf("round-trip mismatch:\n got: %#v\nwant: %#v", got, digest)
	}
	// Confirm the file ends with a newline (line-grain diffs).
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("digest file missing trailing newline")
	}
	// Confirm pretty-printing (indent) — first content character should be `{`,
	// followed by newline.
	if !strings.HasPrefix(string(data), "{\n") {
		t.Errorf("digest file not pretty-printed: %q", string(data[:20]))
	}
}
