package chat

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
)

// The off-TTY byte-identity gate.
//
// W1 established that a piped session's bytes were unchanged by the term
// migration — by MEASURING it once (two scripted sessions diffed against a
// HEAD build in a throwaway worktree) rather than by a test that re-runs.
// IMPLEMENTATION-PLAN.md §1 records both that fact and its consequence:
// nothing in the substrate imports internal/chat, so the sim and scenario
// goldens never touch this output path and off-TTY byte-identity has had
// no standing automated gate at all.
//
// This is that gate. W2 (the Question read path) and W3 (the pump, the
// decoder, liner's removal) both change the read path, which is exactly
// what threatens the identity, so its job is to SCREAM when the off-TTY
// bytes move — not to describe them. Read a failure as "did I mean to
// change this?"; if yes, regenerate and read the diff.
//
// # Regeneration
//
//	UPDATE_GOLDENS=1 make test-run PKG=./internal/chat RUN=TestPipedSession
//
// A golden regenerated without reading the diff is a gate switched off.
//
// # What makes it stable
//
// The session has no clock, no network and no terminal in it: the model is
// a scripted mock, the home is a fresh t.TempDir() whose absolute path is
// asserted never to reach the output, and the plain backend's status slot
// writes nothing, so the phase-labeled indicator contributes no bytes and
// no timing. Nothing else in the session names a version, a timestamp or a
// duration.
//
// stdout and stderr are captured and compared SEPARATELY. They are two fds
// off a terminal, `personant chat 2>/dev/null` is documented usage, and
// merging them here would hide a byte moving from one to the other —
// which is precisely the stderr-escapes-the-arbiter defect W1 closed.
const updateGoldensEnv = "UPDATE_GOLDENS"

// pipedSessionScript is one full session, chosen to cross every off-TTY
// writer this package has: the banner and the active-project line, a slash
// command on Out, an unknown command on Diag, both §4.4 escape forms, a
// turn that streams a body, a turn that FAILS, and the exit.
const pipedSessionScript = "/help\n" +
	"/thinking on\n" +
	"/bogus\n" +
	"$echo fire-and-forget\n" +
	"#echo captured\n" +
	"hello there\n" +
	"and again\n" +
	"/quit\n"

// TestPipedSession runs that session once and holds it to two standards:
// the committed bytes, and the structural rule that a pipe receives no
// decoration at all.
func TestPipedSession(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	paths := scaffoldHome(t)
	writeMeta(t, paths, memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home})

	// Per-consult, not single-slot: the first turn gets a well-formed
	// tagged response and the second exhausts the queue, so ONE session
	// carries both a normal turn and a failing one. A single-slot mock
	// re-serves the same response forever, and the failure path — the one
	// that writes to Diag mid-session — would never be exercised.
	mock := model.NewScriptedMockPerConsult([]model.Response{
		{Content: "*topic: *new-topic* [alpha, beta, gamma, delta]*\nHi there. This is the body."},
	})
	mock.Models = []model.ModelInfo{{ID: "test-model"}}

	var stdout, stderr bytes.Buffer
	if err := Run(Options{
		Ops:             newOps(paths),
		ExplicitProject: "prj_1",
		Stdin:           strings.NewReader(pipedSessionScript),
		Stdout:          &stdout,
		Stderr:          &stderr,
		Client:          mock,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	streams := map[string]*bytes.Buffer{"stdout": &stdout, "stderr": &stderr}

	// The home is a fresh temp directory per run. If its path ever reached
	// the output the golden would be unstable, and the failure would read
	// as a rendering change rather than as leaked state — so name it.
	for name, buf := range streams {
		if strings.Contains(buf.String(), paths.Home) {
			t.Fatalf("%s leaked the temp home path — the golden cannot be stable:\n%s", name, buf)
		}
	}

	// A pipe has no erasure, so the (always, ephemeral) cell of the §5
	// model is inexpressible and the two tty-only channels are inert. That
	// is a structural claim, and it is asserted against the SAME bytes the
	// golden pins so the two can never disagree.
	for name, buf := range streams {
		if i := bytes.IndexByte(buf.Bytes(), 0x1b); i >= 0 {
			t.Errorf("%s carries an escape byte at offset %d:\n%q", name, i, buf.String())
		}
		if i := bytes.IndexByte(buf.Bytes(), '\r'); i >= 0 {
			t.Errorf("%s carries a carriage return at offset %d:\n%q", name, i, buf.String())
		}
		if strings.Contains(buf.String(), "[thinking]") {
			t.Errorf("%s carries a reasoning bracket — the tty-only channel wrote to a pipe:\n%s", name, buf)
		}
	}

	compareGolden(t, "piped_session.stdout", stdout.Bytes())
	compareGolden(t, "piped_session.stderr", stderr.Bytes())
}

// compareGolden diffs got against testdata/<name>, or rewrites it when
// UPDATE_GOLDENS is set.
func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	regen := fmt.Sprintf("%s=1 make test-run PKG=./internal/chat RUN=%s", updateGoldensEnv, t.Name())

	if os.Getenv(updateGoldensEnv) != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("%s regenerated (%d bytes) — READ THE DIFF before committing it", path, len(got))
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\n\nRegenerate with:\n  %s", path, err, regen)
	}
	if bytes.Equal(got, want) {
		return
	}
	t.Errorf("%s: off-TTY bytes moved.\n%s\nIf the change was intended:\n  %s",
		path, firstDivergence(string(want), string(got)), regen)
}

// firstDivergence names the first line that differs. Not a general diff:
// a golden that moved by more than a line or two is a change the reader
// wants to see whole, and the file is right there under testdata/.
func firstDivergence(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d:\n  want: %q\n   got: %q\n", i+1, wl, gl)
		}
	}
	return "(line-identical; the difference is in trailing bytes)\n"
}
