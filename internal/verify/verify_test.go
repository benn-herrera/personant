package verify

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/index"
	"personant/internal/memops"
	"personant/internal/store"
)

// hasGit skips the test if git isn't on PATH (store.Init's git step
// requires it).
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

// validRecord returns a SpineRecord that passes every hard-limit
// check; tests that exercise a single failure mode mutate one field
// off this baseline so the rest of the schema does not drift in.
func validRecord(id string) memops.SpineRecord {
	return memops.SpineRecord{
		ID:           id,
		Project:      "prj_default",
		Anchors:      []string{"alpha", "beta", "gamma", "delta"},
		Summary:      "ok",
		State:        memops.ThreadWIP,
		Created:      "2026-05-09T04:00:00-07:00",
		LastEngaged:  "2026-05-09T04:01:00-07:00",
		StateChanged: "2026-05-09T04:00:00-07:00",
		TurnCount:    1,
		RecallFires:  0,
	}
}

// writeSpine writes records via store.WriteSpine and seeds a matching
// thread.md for each record so checkThreads finds the threads it expects
// — verify's thread check warns on a spine record with no thread.md.
// Records whose ID does not match the canonical pattern are skipped for
// the thread seed (those tests deliberately exercise a malformed ID).
func writeSpine(t *testing.T, paths store.PersonantPaths, recs []memops.SpineRecord) {
	t.Helper()
	if err := store.WriteSpine(paths.Spine, recs); err != nil {
		t.Fatalf("WriteSpine: %v", err)
	}
	for _, rec := range recs {
		if !memops.ThreadIDPattern.MatchString(rec.ID) || rec.Project == "" {
			continue
		}
		fm := memops.ThreadFrontmatter{
			ID:           rec.ID,
			Project:      rec.Project,
			Anchors:      rec.Anchors,
			Summary:      rec.Summary,
			State:        rec.State,
			Created:      rec.Created,
			LastEngaged:  rec.LastEngaged,
			StateChanged: rec.StateChanged,
			TurnCount:    rec.TurnCount,
			RecallFires:  rec.RecallFires,
		}
		if err := store.SaveThreadFrontmatter(paths, rec.ID, fm); err != nil {
			t.Fatalf("SaveThreadFrontmatter %s: %v", rec.ID, err)
		}
	}
}

// rebuild runs index.Rebuild quietly so the derived-files comparison
// in Verify starts clean.
func rebuild(t *testing.T, paths store.PersonantPaths) {
	t.Helper()
	if err := index.Rebuild(paths, index.Options{Quiet: true}); err != nil {
		t.Fatalf("index.Rebuild: %v", err)
	}
}

// run invokes Verify quietly and returns the report. Failures from
// Verify itself (I/O errors) are fatal; schema/drift findings are the
// caller's concern to inspect.
func run(t *testing.T, paths store.PersonantPaths) Report {
	t.Helper()
	r, err := Verify(paths, VerifyOptions{Quiet: true})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return r
}

// errorsContain reports whether any Finding's Message contains substr.
func errorsContain(r Report, substr string) bool {
	for _, f := range r.Errors {
		if strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}

func warningsContain(r Report, substr string) bool {
	for _, f := range r.Warnings {
		if strings.Contains(f.Message, substr) {
			return true
		}
	}
	return false
}

func TestVerifyEmptyHome(t *testing.T) {
	paths := initFreshHome(t)
	rebuild(t, paths)
	r := run(t, paths)
	if r.HasErrors() {
		t.Fatalf("expected clean verify; got errors=%v drift=%v", r.Errors, r.Drift)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", r.Warnings)
	}
}

func TestVerifyValidSpine(t *testing.T) {
	paths := initFreshHome(t)
	writeSpine(t, paths, []memops.SpineRecord{validRecord("thr_1")})
	rebuild(t, paths)
	r := run(t, paths)
	if r.HasErrors() {
		t.Fatalf("expected clean verify; got errors=%v drift=%v", r.Errors, r.Drift)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", r.Warnings)
	}
}

func TestVerifyInvalidThreadID(t *testing.T) {
	paths := initFreshHome(t)
	rec := validRecord("thr_1")
	// store.WriteSpine sorts by ID, so a single record with a malformed
	// id is fine to write directly.
	rec.ID = "thr_bogus"
	writeSpine(t, paths, []memops.SpineRecord{rec})
	rebuild(t, paths)
	r := run(t, paths)
	if !errorsContain(r, "does not match") {
		t.Errorf("expected id-pattern error; got errors=%v", r.Errors)
	}
}

func TestVerifyDuplicateThreadID(t *testing.T) {
	paths := initFreshHome(t)
	// store.WriteSpine rejects dup keys; write the file by hand.
	dup := `{"id":"thr_1","project":"prj_default","anchors":["alpha","beta","gamma","delta"],"summary":"a","state":"wip","created":"2026-05-09T04:00:00-07:00","last_engaged":"2026-05-09T04:01:00-07:00","state_changed":"2026-05-09T04:00:00-07:00","turn_count":1,"recall_fires":0}
{"id":"thr_1","project":"prj_default","anchors":["alpha","beta","gamma","delta"],"summary":"b","state":"wip","created":"2026-05-09T04:00:00-07:00","last_engaged":"2026-05-09T04:01:00-07:00","state_changed":"2026-05-09T04:00:00-07:00","turn_count":1,"recall_fires":0}
`
	if err := os.WriteFile(paths.Spine, []byte(dup), 0o644); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	r := run(t, paths)
	if !errorsContain(r, "duplicate id") {
		t.Errorf("expected duplicate-id error; got errors=%v", r.Errors)
	}
}

func TestVerifyAnchorsCardinality(t *testing.T) {
	cases := []struct {
		name    string
		anchors []string
		wantErr bool
	}{
		{"three-too-few", []string{"a1", "a2", "a3"}, true},
		{"four-min-ok", []string{"a1", "a2", "a3", "a4"}, false},
		{"eight-max-ok", []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8"}, false},
		{"nine-too-many", []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths := initFreshHome(t)
			rec := validRecord("thr_1")
			rec.Anchors = tc.anchors
			writeSpine(t, paths, []memops.SpineRecord{rec})
			rebuild(t, paths)
			r := run(t, paths)
			gotErr := errorsContain(r, "anchors length")
			if gotErr != tc.wantErr {
				t.Errorf("anchors %v: gotErr=%v wantErr=%v (errors=%v)", tc.anchors, gotErr, tc.wantErr, r.Errors)
			}
		})
	}
}

func TestVerifyDuplicateAnchors(t *testing.T) {
	paths := initFreshHome(t)
	rec := validRecord("thr_1")
	rec.Anchors = []string{"alpha", "alpha", "beta", "gamma"}
	writeSpine(t, paths, []memops.SpineRecord{rec})
	rebuild(t, paths)
	r := run(t, paths)
	if !errorsContain(r, "duplicate anchor") {
		t.Errorf("expected duplicate-anchor error; got errors=%v", r.Errors)
	}
}

func TestVerifyAnchorLength(t *testing.T) {
	cases := []struct {
		name    string
		anchor  string
		wantErr bool
	}{
		{"too-short", "ab", true},
		{"min-ok", "abc", false},
		{"too-long", strings.Repeat("x", 51), true},
		{"max-ok", strings.Repeat("x", 50), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths := initFreshHome(t)
			rec := validRecord("thr_1")
			rec.Anchors = []string{tc.anchor, "alpha", "beta", "gamma"}
			writeSpine(t, paths, []memops.SpineRecord{rec})
			rebuild(t, paths)
			r := run(t, paths)
			gotErr := errorsContain(r, "outside [3,50]")
			if gotErr != tc.wantErr {
				t.Errorf("anchor %q (len=%d): gotErr=%v wantErr=%v (errors=%v)", tc.anchor, len(tc.anchor), gotErr, tc.wantErr, r.Errors)
			}
		})
	}
}

func TestVerifySummaryTooLong(t *testing.T) {
	paths := initFreshHome(t)
	rec := validRecord("thr_1")
	// 201 chars > default 200.
	rec.Summary = strings.Repeat("x", 201)
	writeSpine(t, paths, []memops.SpineRecord{rec})
	rebuild(t, paths)
	r := run(t, paths)
	if !errorsContain(r, "spine.entry-max-chars") {
		t.Errorf("expected summary-too-long error; got errors=%v", r.Errors)
	}
}

func TestVerifyInvalidState(t *testing.T) {
	paths := initFreshHome(t)
	rec := validRecord("thr_1")
	rec.State = "fictional"
	writeSpine(t, paths, []memops.SpineRecord{rec})
	rebuild(t, paths)
	r := run(t, paths)
	if !errorsContain(r, "valid ThreadState") {
		t.Errorf("expected state-enum error; got errors=%v", r.Errors)
	}
}

func TestVerifyTimestampOrder(t *testing.T) {
	paths := initFreshHome(t)
	rec := validRecord("thr_1")
	// created > last_engaged.
	rec.Created = "2026-05-09T05:00:00-07:00"
	rec.LastEngaged = "2026-05-09T04:00:00-07:00"
	writeSpine(t, paths, []memops.SpineRecord{rec})
	rebuild(t, paths)
	r := run(t, paths)
	if !errorsContain(r, "after last_engaged") {
		t.Errorf("expected timestamp-order error; got errors=%v", r.Errors)
	}
}

func TestVerifyProjectReference(t *testing.T) {
	t.Run("missing-prj-warns", func(t *testing.T) {
		paths := initFreshHome(t)
		rec := validRecord("thr_1")
		rec.Project = "prj_5"
		writeSpine(t, paths, []memops.SpineRecord{rec})
		// index.Rebuild auto-creates projects/prj_5/digest.json (it
		// groups by project), but does not create a meta.json. That's
		// the scenario this test targets.
		rebuild(t, paths)
		r := run(t, paths)
		if errorsContain(r, "missing meta.json") {
			t.Errorf("missing meta.json should be a warning, not an error; errors=%v", r.Errors)
		}
		if !warningsContain(r, "missing meta.json for prj_5") {
			t.Errorf("expected meta-missing warning; got warnings=%v", r.Warnings)
		}
	})

	t.Run("prj-default-no-warn", func(t *testing.T) {
		paths := initFreshHome(t)
		rec := validRecord("thr_1") // already prj_default
		writeSpine(t, paths, []memops.SpineRecord{rec})
		rebuild(t, paths)
		r := run(t, paths)
		if warningsContain(r, "prj_default") {
			t.Errorf("prj_default should not warn; got warnings=%v", r.Warnings)
		}
	})
}

// writeMeta writes a ProjectMeta to projects/<dir>/meta.json. Caller
// supplies the directory name; meta.ID is what the file says about
// itself, which may diverge from dir for the mismatch test.
func writeMeta(t *testing.T, paths store.PersonantPaths, dir string, meta memops.ProjectMeta) {
	t.Helper()
	d := filepath.Join(paths.ProjectsDir, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", d, err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d, "meta.json"), data, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

func validMeta(id, name string) memops.ProjectMeta {
	return memops.ProjectMeta{
		ID:               id,
		Name:             name,
		CurrentRootPath:  "/tmp/" + name,
		Created:          "2026-05-09T04:00:00-07:00",
		LastActive:       "2026-05-09T04:01:00-07:00",
		ThreadCount:      0,
		ConventionsPaths: []string{},
		SymbolPatterns:   []memops.ProjectPattern{},
		IgnoreSymbols:    []string{},
	}
}

func TestVerifyProjectMetaIDMismatch(t *testing.T) {
	paths := initFreshHome(t)
	// Direction lies: directory says prj_3, meta.json says prj_4.
	writeMeta(t, paths, "prj_3", validMeta("prj_4", "wrong"))
	// Spine references prj_3 to keep the cross-ref happy and avoid an
	// unrelated warning. WriteSpine auto-builds digest.json on rebuild,
	// so add a record.
	rec := validRecord("thr_1")
	rec.Project = "prj_3"
	writeSpine(t, paths, []memops.SpineRecord{rec})
	rebuild(t, paths)
	r := run(t, paths)
	if !errorsContain(r, "does not match directory name") {
		t.Errorf("expected id/dir mismatch error; got errors=%v", r.Errors)
	}
}

func TestVerifyProjectMetaName(t *testing.T) {
	paths := initFreshHome(t)
	writeMeta(t, paths, "prj_3", validMeta("prj_3", "Bad Name With Spaces"))
	rec := validRecord("thr_1")
	rec.Project = "prj_3"
	writeSpine(t, paths, []memops.SpineRecord{rec})
	rebuild(t, paths)
	r := run(t, paths)
	if !errorsContain(r, "name") {
		t.Errorf("expected name-pattern error; got errors=%v", r.Errors)
	}
}

func TestVerifyDriftDetection(t *testing.T) {
	paths := initFreshHome(t)
	writeSpine(t, paths, []memops.SpineRecord{validRecord("thr_1")})
	rebuild(t, paths)
	// Append a bogus line to symbols.jsonl post-rebuild.
	f, err := os.OpenFile(paths.Symbols, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open symbols: %v", err)
	}
	if _, err := f.WriteString(`{"symbol":"BOGUS"}` + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	r := run(t, paths)
	if len(r.Drift) == 0 {
		t.Fatalf("expected drift to be reported; got %+v", r)
	}
	if !r.HasErrors() {
		t.Errorf("HasErrors should be true when drift is non-empty; report=%+v", r)
	}
}

func TestVerifyDirectivesMissing(t *testing.T) {
	paths := initFreshHome(t)
	// Delete the seed defaults.md.
	defaultsPath := filepath.Join(paths.DirectivesDir, "defaults.md")
	if err := os.Remove(defaultsPath); err != nil {
		t.Fatalf("remove defaults.md: %v", err)
	}
	writeSpine(t, paths, []memops.SpineRecord{validRecord("thr_1")})
	rebuild(t, paths)
	r := run(t, paths)
	// Verify must still complete without errors and emit a warning
	// about the missing directives file.
	if r.HasErrors() {
		t.Errorf("verify should still pass when defaults.md is missing; errors=%v drift=%v", r.Errors, r.Drift)
	}
	if !warningsContain(r, "spine.entry-max-chars") {
		t.Errorf("expected directives warning mentioning fallback; got warnings=%v", r.Warnings)
	}
	// And the fallback should still apply: a record with summary > 200
	// must still error on a separate run.
	rec := validRecord("thr_2")
	rec.Summary = strings.Repeat("y", 201)
	writeSpine(t, paths, []memops.SpineRecord{validRecord("thr_1"), rec})
	rebuild(t, paths)
	r = run(t, paths)
	if !errorsContain(r, "spine.entry-max-chars 200") {
		t.Errorf("expected summary-too-long under fallback default; got errors=%v", r.Errors)
	}
}
