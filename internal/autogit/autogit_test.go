package autogit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"personant/internal/store"
)

// scaffoldHome stands up a fresh PERSONANT_HOME under t.TempDir() and
// returns the resolved paths. The home is left in the clean post-init
// state (initial commit landed; spine empty; derived files fresh).
func scaffoldHome(t *testing.T) store.PersonantPaths {
	t.Helper()
	home := t.TempDir()
	paths := store.PathsForHome(home)
	if err := store.Init(paths, store.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return paths
}

// repoHead returns the current HEAD commit hash for paths.Home. Used to
// assert "commit happened" vs "commit did not happen".
func repoHead(t *testing.T, paths store.PersonantPaths) plumbing.Hash {
	t.Helper()
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	return ref.Hash()
}

func TestCommit_NoFlags(t *testing.T) {
	paths := scaffoldHome(t)
	before := repoHead(t, paths)

	// Stage a new file and commit it with no pre/post flags.
	stray := filepath.Join(paths.Home, "stray.md")
	if err := os.WriteFile(stray, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	if err := Add(context.Background(), paths, Primary, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Commit(context.Background(), paths, Primary, "no-flags", 0, 0); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	after := repoHead(t, paths)
	if before == after {
		t.Errorf("HEAD did not move; before=%s after=%s", before, after)
	}
}

// TestCommit_PreFlagFailureAbortsBeforeCommit: a corrupt spine staged
// for commit triggers CheckSpineIntegrity pre-flag failure before the
// commit lands. HEAD must be unchanged.
func TestCommit_PreFlagFailureAbortsBeforeCommit(t *testing.T) {
	paths := scaffoldHome(t)
	before := repoHead(t, paths)

	// Append a spine record with a 1-element anchor list (invalid; min 4).
	badRecord := `{"id":"thr_1","project":"prj_default","anchors":["only-one"],"summary":"bad","state":"active","created":"2026-05-10T00:00:00Z","last_engaged":"2026-05-10T00:00:00Z","state_changed":"2026-05-10T00:00:00Z","turn_count":1,"recall_fires":0}` + "\n"
	if err := os.WriteFile(paths.Spine, []byte(badRecord), 0o644); err != nil {
		t.Fatalf("write bad spine: %v", err)
	}
	if err := Add(context.Background(), paths, Primary, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}

	err := Commit(context.Background(), paths, Primary, "should-not-land", CheckSpineIntegrity, 0)
	if err == nil {
		t.Fatal("expected pre-flag failure, got nil")
	}
	if errors.Is(err, ErrPostOpVerification) {
		t.Errorf("pre-flag failure incorrectly wrapped as ErrPostOpVerification: %v", err)
	}

	after := repoHead(t, paths)
	if before != after {
		t.Errorf("HEAD moved despite pre-flag failure: before=%s after=%s", before, after)
	}
}

// TestCommit_PostFlagFailureWraps: stage a delta to spine that
// introduces derived-file drift (symbols.jsonl is now stale). The
// commit itself succeeds (HEAD moves); the post-flag wraps
// ErrPostOpVerification.
func TestCommit_PostFlagFailureWraps(t *testing.T) {
	paths := scaffoldHome(t)
	before := repoHead(t, paths)

	// Valid spine record (4 anchors). symbols.jsonl is still the empty
	// post-init blob, so once this lands the derived index is stale.
	rec := `{"id":"thr_1","project":"prj_default","anchors":["a","b","c","d"],"summary":"valid","state":"active","created":"2026-05-10T00:00:00Z","last_engaged":"2026-05-10T00:00:00Z","state_changed":"2026-05-10T00:00:00Z","turn_count":1,"recall_fires":0}` + "\n"
	if err := os.WriteFile(paths.Spine, []byte(rec), 0o644); err != nil {
		t.Fatalf("write spine: %v", err)
	}
	if err := Add(context.Background(), paths, Primary, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}

	err := Commit(context.Background(), paths, Primary, "drift", 0, CheckDerivedFresh)
	if err == nil {
		t.Fatal("expected post-flag failure, got nil")
	}
	if !errors.Is(err, ErrPostOpVerification) {
		t.Errorf("post-flag failure should wrap ErrPostOpVerification; got %v", err)
	}

	after := repoHead(t, paths)
	if before == after {
		t.Errorf("commit did not land despite post-flag-only failure mode: HEAD unchanged")
	}
}

// TestCommit_GitFailureNotWrappedAsPostOp: an empty-staging-area commit
// fails inside go-git itself. The error must not be wrapped with
// ErrPostOpVerification — a git failure is not a post-op verification
// failure.
func TestCommit_GitFailureNotWrappedAsPostOp(t *testing.T) {
	paths := scaffoldHome(t)
	// No staged changes. CheckDerivedFresh as the post-flag would also
	// pass on this clean state, so a wrapped error would indicate a real
	// misclassification.
	err := Commit(context.Background(), paths, Primary, "empty", 0, CheckDerivedFresh)
	if err == nil {
		t.Fatal("expected ErrEmptyCommit, got nil")
	}
	if errors.Is(err, ErrPostOpVerification) {
		t.Errorf("git failure incorrectly wrapped as ErrPostOpVerification: %v", err)
	}
}

// TestCommit_MultipleFlags: PreFlags = CheckDerivedFresh | CheckSpineIntegrity
// over a clean state. Both pass; commit succeeds.
func TestCommit_MultipleFlags(t *testing.T) {
	paths := scaffoldHome(t)
	// Stage a benign new file so commit isn't empty.
	if err := os.WriteFile(filepath.Join(paths.Home, "stray.md"), []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	if err := Add(context.Background(), paths, Primary, "."); err != nil {
		t.Fatalf("Add: %v", err)
	}

	flags := CheckDerivedFresh | CheckSpineIntegrity
	if err := Commit(context.Background(), paths, Primary, "multi-flag", flags, flags); err != nil {
		t.Fatalf("Commit with both flags: %v", err)
	}
}

// TestCheckout_RestoresFile: commit twice (HEAD~1 with content A,
// HEAD with content B); Checkout HEAD~1 restores content A.
func TestCheckout_RestoresFile(t *testing.T) {
	paths := scaffoldHome(t)
	target := filepath.Join(paths.Home, "target.md")

	if err := os.WriteFile(target, []byte("version A\n"), 0o644); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := Add(context.Background(), paths, Primary, "."); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	if err := Commit(context.Background(), paths, Primary, "commit A", 0, 0); err != nil {
		t.Fatalf("Commit A: %v", err)
	}
	commitA := repoHead(t, paths)

	if err := os.WriteFile(target, []byte("version B\n"), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}
	if err := Add(context.Background(), paths, Primary, "."); err != nil {
		t.Fatalf("Add B: %v", err)
	}
	if err := Commit(context.Background(), paths, Primary, "commit B", 0, 0); err != nil {
		t.Fatalf("Commit B: %v", err)
	}

	if err := Checkout(context.Background(), paths, Primary, commitA.String(), "target.md", 0, 0); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if string(got) != "version A\n" {
		t.Errorf("restored content = %q, want %q", got, "version A\n")
	}
}

// TestTag_CreatesTag: tag at HEAD resolves through go-git's Tag().
func TestTag_CreatesTag(t *testing.T) {
	paths := scaffoldHome(t)
	if err := Tag(context.Background(), paths, Primary, "test-tag", ""); err != nil {
		t.Fatalf("Tag: %v", err)
	}
	repo, err := git.PlainOpen(paths.Home)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	ref, err := repo.Tag("test-tag")
	if err != nil {
		t.Fatalf("Tag('test-tag'): %v", err)
	}
	if ref.Hash() != repoHead(t, paths) {
		t.Errorf("tag points at %s, want HEAD %s", ref.Hash(), repoHead(t, paths))
	}
}

// TestApplyFlags_CanonicalOrder: when both flags would fire on the
// same call, the one with the lowest bit (CheckDerivedFresh, bit 0)
// must report first. Asserted by setting up a state where both checks
// would fail and verifying the error message names the lower-bit
// check.
func TestApplyFlags_CanonicalOrder(t *testing.T) {
	paths := scaffoldHome(t)
	// Bad spine: 1 anchor (CheckSpineIntegrity will fire).
	// Also: spine non-empty without rebuilt symbols (CheckDerivedFresh
	// will also fire — the empty symbols.jsonl from init no longer
	// matches what rebuild would produce).
	badRec := `{"id":"thr_1","project":"prj_default","anchors":["only-one"],"summary":"bad","state":"active","created":"2026-05-10T00:00:00Z","last_engaged":"2026-05-10T00:00:00Z","state_changed":"2026-05-10T00:00:00Z","turn_count":1,"recall_fires":0}` + "\n"
	if err := os.WriteFile(paths.Spine, []byte(badRec), 0o644); err != nil {
		t.Fatalf("write spine: %v", err)
	}

	err := applyFlags(context.Background(), paths, CheckDerivedFresh|CheckSpineIntegrity)
	if err == nil {
		t.Fatal("expected applyFlags to fail with both flags failing, got nil")
	}
	// CheckDerivedFresh has bit 0; CheckSpineIntegrity has bit 1.
	// applyFlags walks low-to-high, so the first failure must be from
	// CheckDerivedFresh.
	msg := err.Error()
	if !contains(msg, "CheckDerivedFresh") {
		t.Errorf("expected error to mention CheckDerivedFresh (lowest bit fires first), got: %s", msg)
	}
	if contains(msg, "CheckSpineIntegrity") {
		t.Errorf("higher-bit check should not have run after lower-bit failure: %s", msg)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
