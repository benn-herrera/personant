package chat

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/recall/measure"
	"personant/internal/store"
	"personant/internal/turn"
	"personant/internal/version"
)

// runChat drives a full REPL session with scripted stdin, failing on any
// bootstrap/unrecoverable error. Returns stdout and stderr.
func runChat(t *testing.T, paths store.PersonantPaths, mock model.Client, project, stdin string) (string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: project,
		Stdin:           strings.NewReader(stdin),
		Stdout:          &out,
		Stderr:          &errb,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String(), errb.String()
}

// readAllLogs concatenates every daily log file under LogsDir.
func readAllLogs(t *testing.T, paths store.PersonantPaths) string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(paths.LogsDir, "*.log"))
	if err != nil {
		t.Fatalf("glob logs: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(e)
		if err != nil {
			t.Fatalf("read log %s: %v", e, err)
		}
		b.Write(data)
	}
	return b.String()
}

// topicTagMock returns a single-slot scripted mock whose sole response
// carries a *new-topic* tag, so any driven turn creates a fresh thread.
func topicTagMock() *model.MockClient {
	return model.NewScriptedMock(
		[]model.Response{{Content: "*topic: *new-topic* [alpha, beta, gamma, delta]*\nWorking on it."}},
		[]model.ModelInfo{{ID: "test-model"}},
	)
}

func findThread(t *testing.T, paths store.PersonantPaths, id string) memops.SpineRecord {
	t.Helper()
	recs, err := store.ReadSpine(paths.Spine)
	if err != nil {
		t.Fatalf("read spine: %v", err)
	}
	for _, r := range recs {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("thread %s not found in spine", id)
	return memops.SpineRecord{}
}

func TestSlashTopicCreatesThread(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/topic knot theory\n/quit\n")

	if !strings.Contains(out, `started topic "knot theory"`) {
		t.Errorf("missing topic confirmation: %q", out)
	}
	rec := findThread(t, paths, "thr_1")
	if rec.Description != "knot theory" || rec.State != memops.ThreadActive {
		t.Errorf("topic thread = %+v, want description 'knot theory' state active", rec)
	}
}

func TestSlashTopicEmptyIsError(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	_, errb := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/topic\n/quit\n")

	if !strings.Contains(errb, "usage: /topic") {
		t.Errorf("expected usage error, got stderr: %q", errb)
	}
}

func TestSlashPauseResume(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, topicTagMock(), "prj_1",
		"work on it\n/pause\n/resume thr_1\n/quit\n")

	if !strings.Contains(out, "paused thr_1") {
		t.Errorf("missing pause confirmation: %q", out)
	}
	if !strings.Contains(out, "resumed thr_1") {
		t.Errorf("missing resume confirmation: %q", out)
	}
	if rec := findThread(t, paths, "thr_1"); rec.State != memops.ThreadActive {
		t.Errorf("thr_1 state = %q, want active after resume", rec.State)
	}
}

func TestSlashPauseNoThreadIsError(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	// No turn ran → no current owner thread → /pause with no ref errors.
	_, errb := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/pause\n/quit\n")

	if !strings.Contains(errb, "no active topic") {
		t.Errorf("expected no-active-topic error, got stderr: %q", errb)
	}
}

func TestSlashBackTo(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, topicTagMock(), "prj_1",
		"work on it\n/pause\n/back-to thr_1\n/quit\n")

	if !strings.Contains(out, "re-engaged thr_1") {
		t.Errorf("missing back-to confirmation: %q", out)
	}
	if rec := findThread(t, paths, "thr_1"); rec.State != memops.ThreadActive {
		t.Errorf("thr_1 state = %q, want active after back-to", rec.State)
	}
}

func TestSlashBackToEmptyIsError(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	_, errb := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/back-to\n/quit\n")

	if !strings.Contains(errb, "usage: /back-to") {
		t.Errorf("expected usage error, got stderr: %q", errb)
	}
}

func TestSlashProjectRename(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/project rename gamma\n/quit\n")

	if !strings.Contains(out, "renamed project prj_1") {
		t.Errorf("missing rename confirmation: %q", out)
	}
	meta, err := newOps(paths).LoadProject(context.Background(), "prj_1")
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	if meta.Name != "gamma" {
		t.Errorf("project name = %q, want gamma", meta.Name)
	}
}

func TestSlashProjectSwitch(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_2", Name: "beta", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/project switch beta\n/quit\n")

	if !strings.Contains(out, "switched to project beta (prj_2)") {
		t.Errorf("missing switch confirmation: %q", out)
	}
	last, err := store.ReadLastActive(paths)
	if err != nil {
		t.Fatalf("read last-active: %v", err)
	}
	if last != "prj_2" {
		t.Errorf("last-active = %q, want prj_2", last)
	}
}

func TestSlashProjectSwitchUnknownIsError(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	_, errb := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/project switch nonesuch\n/quit\n")

	if !strings.Contains(errb, "no known project matching") {
		t.Errorf("expected unknown-project error, got stderr: %q", errb)
	}
}

// TestSlashDoneClosesThread drives /done end-to-end: create a thread via a
// turn, then close it via the manual §3.5 flow, acking [r]esolved.
func TestSlashDoneClosesThread(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	// Single-slot mock: the turn's ConsultStream AND the curator's Consult
	// both re-serve this response (the summary draft is its content).
	mock := topicTagMock()
	runChat(t, paths, mock, "prj_1", "work on it\n/done\nr\n/quit\n")

	if rec := findThread(t, paths, "thr_1"); rec.State != memops.ThreadResolved {
		t.Errorf("thr_1 state = %q, want resolved after /done r", rec.State)
	}
	logs := readAllLogs(t, paths)
	if !strings.Contains(logs, "retire.ack thr=thr_1 resolution=resolved edited=no") {
		t.Errorf("missing retire.ack: %s", logs)
	}
	if !strings.Contains(logs, "retire.complete thr=thr_1 resolution=resolved") {
		t.Errorf("missing retire.complete: %s", logs)
	}
}

// TestClosureOfferRendering: the §3.5 offer names the topic the way the
// roster and the recall offer do — display name, id, gist — and degrades
// to the bare id rather than "thr_1 (thr_1)" when the runtime resolved
// neither field (user ruling 2026-08-06; the superseded form showed only
// the id, which is the illegibility the recall redesign already fixed).
func TestClosureOfferRendering(t *testing.T) {
	tests := []struct {
		name  string
		offer turn.ClosureOffer
		want  string
	}{
		{
			name: "display and gist",
			offer: turn.ClosureOffer{
				ThreadID: "thr_3",
				Display:  "why is the emulsion separating",
				Gist:     "emulsion, thickener, shear",
				Summary:  "thickener pinned at 0.4",
			},
			want: "idle topic: why is the emulsion separating (thr_3) — emulsion, thickener, shear",
		},
		{
			name:  "no gist",
			offer: turn.ClosureOffer{ThreadID: "thr_3", Display: "surfactant sourcing"},
			want:  "idle topic: surfactant sourcing (thr_3)",
		},
		{
			name:  "nothing resolved",
			offer: turn.ClosureOffer{ThreadID: "thr_3"},
			want:  "idle topic: thr_3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := closureOfferLines(tt.offer)
			if len(lines) != 2 {
				t.Fatalf("closureOfferLines = %q, want 2 lines", lines)
			}
			if lines[0] != tt.want {
				t.Errorf("head line = %q, want %q", lines[0], tt.want)
			}
			if want := "  summary: " + tt.offer.Summary; lines[1] != want {
				t.Errorf("summary line = %q, want %q", lines[1], want)
			}
		})
	}

	// The auto-closure one-liner carries the id for the same reason: the
	// documented revision path is /back-to <thr_id>.
	got := autoClosedLine(turn.ClosureNotice{
		ThreadID: "thr_7", Display: "supplier shortlist", Summary: "two-source split",
	})
	if want := "closed: supplier shortlist (thr_7) — two-source split"; got != want {
		t.Errorf("autoClosedLine = %q, want %q", got, want)
	}
}

// TestClosureResolverEditPath unit-tests the [e]dit branch of the
// interactive closure resolver: choosing edit, revising the draft, then
// acking resolved yields EditedSummary set to the revision.
func TestClosureResolverEditPath(t *testing.T) {
	var out bytes.Buffer
	tm := openTestTerm(t, strings.NewReader("e\nrevised gist\nr\n"), &out, &out)
	resolve := interactiveClosureResolver(tm)

	res, err := resolve(context.Background(), turn.ClosureOffer{ThreadID: "thr_1", Summary: "curator draft"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != turn.ClosureResolved {
		t.Errorf("outcome = %v, want resolved", res.Outcome)
	}
	if res.EditedSummary != "revised gist" {
		t.Errorf("EditedSummary = %q, want 'revised gist'", res.EditedSummary)
	}
}

// TestClosureResolverEditUnchanged: resubmitting the draft unchanged at the
// edit prompt must NOT count as an edit (the ack-edit-rate canary must not
// read a rubber-stamp as an edit).
func TestClosureResolverEditUnchanged(t *testing.T) {
	var out bytes.Buffer
	tm := openTestTerm(t, strings.NewReader("e\ncurator draft\nw\n"), &out, &out)
	resolve := interactiveClosureResolver(tm)

	res, err := resolve(context.Background(), turn.ClosureOffer{ThreadID: "thr_1", Summary: "curator draft"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != turn.ClosureWIP {
		t.Errorf("outcome = %v, want wip", res.Outcome)
	}
	if res.EditedSummary != "" {
		t.Errorf("EditedSummary = %q, want empty (unchanged draft is not an edit)", res.EditedSummary)
	}
}

func TestClosureResolverSkipDefers(t *testing.T) {
	var out bytes.Buffer
	tm := openTestTerm(t, strings.NewReader("s\n"), &out, &out)
	resolve := interactiveClosureResolver(tm)

	res, err := resolve(context.Background(), turn.ClosureOffer{ThreadID: "thr_1", Summary: "d"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Outcome != turn.ClosureDefer {
		t.Errorf("outcome = %v, want defer", res.Outcome)
	}
}

// TestSlashVersion: /version prints the binary's identity plus the home's
// on-disk format stamp, and is advertised in /help.
func TestSlashVersion(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/version\n/quit\n")

	if !strings.Contains(out, version.Long()) {
		t.Errorf("/version missing the version long form:\n%s", out)
	}
	// chat.Run's idempotent Init scaffolds the stamp, so the session always
	// reports a concrete revision here.
	want := version.Row("home stamp", strconv.Itoa(version.CurrentHomeFormat))
	if !strings.Contains(out, want) {
		t.Errorf("/version missing %q:\n%s", want, out)
	}
	if !strings.Contains(helpText(), "/version") {
		t.Error("/help does not advertise /version")
	}
}

// TestSessionStartEventIsNotBootstrap: the session/project event is
// session.started (§2.8). system.bootstrap is the version/identity line,
// emitted once at the process boundary by eventlog.LogBootstrap — chat
// must not emit a second, differently-shaped line under that name.
func TestSessionStartEventIsNotBootstrap(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	runChat(t, paths, model.NewScriptedMock(nil, []model.ModelInfo{{ID: "test-model"}}),
		"prj_1", "/quit\n")

	logs := readAllLogs(t, paths)
	if !strings.Contains(logs, "session.started active=prj_1 provider=local") {
		t.Errorf("event log missing the session.started line:\n%s", logs)
	}
	if strings.Contains(logs, "system.bootstrap") {
		t.Errorf("chat emitted system.bootstrap; that name belongs to the identity line:\n%s", logs)
	}
}

// The §4.3.1 persistent-history round trip moved to internal/term with the
// editor it exercises (W2): the file has one owner, and the test belongs
// beside it rather than in the package that used to open it too.

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// TestSlashClosuresEmptyQueue: /closures on a session with nothing queued
// says so rather than staying silent — an empty queue is an answer.
func TestSlashClosuresEmptyQueue(t *testing.T) {
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	out, _ := runChat(t, paths, model.NewScriptedMock(nil, nil), "prj_1", "/closures\n/quit\n")
	if !strings.Contains(out, "no closures pending review") {
		t.Errorf("missing empty-queue reply: %q", out)
	}
}

// TestClosureAckModeDirective: the §2.6.1 closure.ack-mode directive
// selects the session policy, and every failure mode keeps the auto
// default rather than refusing the session.
func TestClosureAckModeDirective(t *testing.T) {
	tests := []struct {
		name      string
		userMD    string
		want      turn.ClosureAckMode
		wantWarn  string
		writeFile bool
	}{
		{name: "unset falls back to auto", want: turn.AckModeAuto},
		{
			name: "always is honored", writeFile: true,
			userMD: "---\nparameters:\n  closure.ack-mode: always\n---\n",
			want:   turn.AckModeAlways,
		},
		{
			name: "unrecognized value warns and keeps auto", writeFile: true,
			userMD:   "---\nparameters:\n  closure.ack-mode: sometimes\n---\n",
			want:     turn.AckModeAuto,
			wantWarn: "is not auto|always",
		},
		{
			name: "malformed directive warns and keeps auto", writeFile: true,
			userMD:   "---\nparameters:\n  closure.ack-mode: [unclosed\n---\n",
			want:     turn.AckModeAuto,
			wantWarn: "warn: read closure.ack-mode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := scaffoldHome(t)
			if tt.writeFile {
				if err := os.MkdirAll(paths.DirectivesDir, 0o755); err != nil {
					t.Fatalf("mkdir directives: %v", err)
				}
				path := filepath.Join(paths.DirectivesDir, store.DirectiveUserFile)
				if err := os.WriteFile(path, []byte(tt.userMD), 0o644); err != nil {
					t.Fatalf("write user.md: %v", err)
				}
			}
			var diag bytes.Buffer
			got := closureAckMode(context.Background(), newOps(paths), "prj_1", &diag)
			if got != tt.want {
				t.Errorf("closureAckMode = %q, want %q", got, tt.want)
			}
			if tt.wantWarn != "" && !strings.Contains(diag.String(), tt.wantWarn) {
				t.Errorf("diag = %q, want a warning containing %q", diag.String(), tt.wantWarn)
			}
			if tt.wantWarn == "" && diag.Len() > 0 {
				t.Errorf("unexpected diag output: %q", diag.String())
			}
		})
	}
}

// TestRecallAckModeDirective: the §2.6.1 recall.ack-mode directive selects
// the session policy, and every failure mode keeps the banded default
// rather than refusing the session (the closure.ack-mode contract).
func TestRecallAckModeDirective(t *testing.T) {
	tests := []struct {
		name      string
		userMD    string
		want      turn.RecallAckMode
		wantWarn  string
		writeFile bool
	}{
		{name: "unset falls back to banded", want: turn.RecallAckBanded},
		{
			name: "always is honored", writeFile: true,
			userMD: "---\nparameters:\n  recall.ack-mode: always\n---\n",
			want:   turn.RecallAckAlways,
		},
		{
			name: "unrecognized value warns and keeps banded", writeFile: true,
			userMD:   "---\nparameters:\n  recall.ack-mode: auto\n---\n",
			want:     turn.RecallAckBanded,
			wantWarn: "is not banded|always",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := scaffoldHome(t)
			if tt.writeFile {
				if err := os.MkdirAll(paths.DirectivesDir, 0o755); err != nil {
					t.Fatalf("mkdir directives: %v", err)
				}
				path := filepath.Join(paths.DirectivesDir, store.DirectiveUserFile)
				if err := os.WriteFile(path, []byte(tt.userMD), 0o644); err != nil {
					t.Fatalf("write user.md: %v", err)
				}
			}
			var diag bytes.Buffer
			got := recallAckMode(context.Background(), newOps(paths), "prj_1", &diag)
			if got != tt.want {
				t.Errorf("recallAckMode = %q, want %q", got, tt.want)
			}
			if tt.wantWarn != "" && !strings.Contains(diag.String(), tt.wantWarn) {
				t.Errorf("diag = %q, want a warning containing %q", diag.String(), tt.wantWarn)
			}
			if tt.wantWarn == "" && diag.Len() > 0 {
				t.Errorf("unexpected diag output: %q", diag.String())
			}
		})
	}
}

// TestRecallOfferLines: the redesigned §3.4 offer line (user ruling
// 2026-08-05) must name the thread and say WHY it matched — never a bare
// thr_N and a bare score. One case per evidence kind.
func TestRecallOfferLines(t *testing.T) {
	tests := []struct {
		name string
		cand turn.RecallCandidate
		want []string
	}{
		{
			name: "symbolic names the matched symbols",
			cand: turn.RecallCandidate{
				Result: measure.Result{
					ThreadID: "thr_3", Score: 0.56,
					Symbolic: &measure.SymbolicHit{Score: 0.56, MatchedSymbols: []string{"emulsion", "viscosity"}},
				},
				Display: "why is the emulsion separating", Gist: "thickener ratio pinned at 0.4",
			},
			want: []string{"why is the emulsion separating", "matched emulsion, viscosity", "shared topics 0.56"},
		},
		{
			name: "intra-thread names the turns",
			cand: turn.RecallCandidate{
				Result: measure.Result{
					ThreadID: "thr_3", Score: 0.81,
					IntraThread: &measure.IntraThreadHit{Score: 0.81, Turns: []int{12, 40, 103, 210}},
				},
				Display: "the long-running rheology thread", Gist: "shear-thinning model selection",
			},
			// The tier label follows the topic ruling (2026-08-05); the Display
			// above deliberately still says "thread" — it is user-authored
			// content, which the vocabulary gate does not and must not rewrite.
			want: []string{"earlier here, turn 12, 40, 103, …", "earlier in this topic 0.81"},
		},
		{
			name: "embedding-only says the similarity is the evidence",
			cand: turn.RecallCandidate{
				Result: measure.Result{
					ThreadID: "thr_9", Score: 0.62,
					Embedding: &measure.EmbeddingHit{Score: 0.62},
				},
				Display: "surfactant sourcing", Gist: "supplier shortlist",
			},
			want: []string{"similar wording", "related 0.62"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(recallOfferLines(1, tt.cand), "\n")
			if strings.Contains(got, "thr_") {
				t.Errorf("offer line leaks a raw thread id: %q", got)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("offer line missing %q:\n%s", want, got)
				}
			}
		})
	}
}
