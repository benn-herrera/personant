package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindGitRoot walks up from start looking for a .git entry (file or
// directory — git worktrees use a regular file). Returns the directory
// containing it and ok=true on hit; ("", false, nil) on miss when the walk
// reaches the filesystem root with no match. Errors only on i/o failures
// other than "not exist" / "not a directory".
//
// start is filepath.Clean'd before the walk; relative paths are accepted
// but not resolved (caller decides whether to abs-ify first).
func FindGitRoot(start string) (string, bool, error) {
	if start == "" {
		return "", false, nil
	}
	dir := filepath.Clean(start)
	for {
		// Check that dir itself is a directory; if not, walk up.
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			gitPath := filepath.Join(dir, ".git")
			if _, gerr := os.Stat(gitPath); gerr == nil {
				return dir, true, nil
			} else if !errors.Is(gerr, os.ErrNotExist) {
				return "", false, fmt.Errorf("find git root: stat %s: %w", gitPath, gerr)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("find git root: stat %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

// GetGitOriginURL returns the configured remote URL of the repository at
// gitRoot. Tries `remote.origin.url` first and, if no origin is configured,
// falls back to the first remote listed by `git remote` — many repos use a
// remote name other than "origin".
//
// Returns ("", nil) when the repo has no configured remotes. Errors on
// invocation failures (git missing, repo corrupt, etc.). The result is
// stripped of surrounding whitespace; no normalization beyond that — the
// caller is responsible for applying NormalizeRemoteURL when it needs the
// canonical form.
func GetGitOriginURL(gitRoot string) (string, error) {
	if gitRoot == "" {
		return "", errors.New("get git origin: gitRoot is empty")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("get git origin: %w", err)
	}

	if url, ok, err := readGitConfig(gitRoot, "remote.origin.url"); err != nil {
		return "", err
	} else if ok {
		return url, nil
	}

	// Fallback: first remote listed by `git remote`.
	cmd := exec.Command("git", "-C", gitRoot, "remote")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) == 0 {
			return "", fmt.Errorf("get git origin: list remotes: %w", err)
		}
		return "", fmt.Errorf("get git origin: list remotes: %w: %s", err, string(msg))
	}
	first := firstNonEmptyLine(stdout.String())
	if first == "" {
		return "", nil
	}
	if url, ok, err := readGitConfig(gitRoot, "remote."+first+".url"); err != nil {
		return "", err
	} else if ok {
		return url, nil
	}
	return "", nil
}

// readGitConfig invokes `git config --get <key>` at gitRoot. Returns
// (value, true, nil) when the key is present, ("", false, nil) when git
// reports the key is unset (exit 1 with empty stderr), and a wrapped error
// for any other failure mode.
func readGitConfig(gitRoot, key string) (string, bool, error) {
	cmd := exec.Command("git", "-C", gitRoot, "config", "--get", key)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()), true, nil
	}
	// `git config --get` exits 1 when the key is unset, with no stderr in
	// the common case. Treat that as "absent" rather than a hard error so
	// the caller can fall through cleanly.
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && len(bytes.TrimSpace(stderr.Bytes())) == 0 {
		return "", false, nil
	}
	msg := bytes.TrimSpace(stderr.Bytes())
	if len(msg) == 0 {
		return "", false, fmt.Errorf("get git config %s: %w", key, err)
	}
	return "", false, fmt.Errorf("get git config %s: %w: %s", key, err, string(msg))
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

// CommitReachable reports whether commit hash is present and reachable in
// the workspace repo at gitRoot. It is the read-only reachability predicate
// the §3.9 git-minimization aging gate consults before dropping a chain: a
// committed file's chain is dropped only when its hash is confirmed
// reachable, so durable content is never aged away without an
// application-reachable recovery path (SPEC §3.9.1, §6.1.3).
//
// Implemented as `git cat-file -e <hash>^{commit}`: exit 0 ⇒ reachable
// (true). A non-zero git exit ⇒ not reachable (false, no error): git exits
// 1 when the object is absent from the DB and 128 when the revision does not
// resolve to a commit (orphaned, never committed, or syntactically rejected)
// — both are the same "this hash is not an application-reachable recovery
// point" answer, and the safe direction is "not reachable" so the caller
// retains the chain. Only a failure to invoke git at all is an error.
// Read-only; never mutates the workspace tree.
func CommitReachable(gitRoot, hash string) (bool, error) {
	if gitRoot == "" {
		return false, errors.New("commit reachable: gitRoot is empty")
	}
	if hash == "" {
		return false, errors.New("commit reachable: hash is empty")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return false, fmt.Errorf("commit reachable: %w", err)
	}
	cmd := exec.Command("git", "-C", gitRoot, "cat-file", "-e", hash+"^{commit}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// Any clean git exit (1 absent / 128 unresolvable) is the negative
	// answer, not a hard error — retain-the-chain is safe.
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	msg := bytes.TrimSpace(stderr.Bytes())
	if len(msg) == 0 {
		return false, fmt.Errorf("commit reachable %s: %w", hash, err)
	}
	return false, fmt.Errorf("commit reachable %s: %w: %s", hash, err, string(msg))
}

// BlobReachable reports whether the blob at `hash:path` is present and
// reachable in the workspace repo at gitRoot — the §3.9.1 aging gate's
// recovery precondition. It is strictly stronger than CommitReachable: it
// confirms not just that the commit resolves but that the RECORDED PATH
// exists in that commit's tree, which is exactly what the recovery read
// (ShowFileAtCommit → `git show <hash>:<path>`) needs. A blob being
// reachable implies its commit is reachable, so this is the single
// predicate the gate consults before dropping a chain: durable content is
// never aged away unless the same `hash:path` the recovery path reads is
// confirmed present (SPEC §3.9.1, §6.1.3).
//
// Implemented as `git cat-file -e <hash>:<path>`: exit 0 ⇒ reachable
// (true). A non-zero git exit ⇒ not reachable (false, no error): git exits
// 128 both when the revision does not resolve and when the path is absent
// from that commit's tree (path-form mismatch, rename before commit, case
// divergence) — both are the same "this blob is not an application-
// reachable recovery point" answer, and the safe direction is "not
// reachable" so the caller retains the chain. Only a failure to invoke git
// at all is an error. Read-only; never mutates the workspace tree.
func BlobReachable(gitRoot, hash, path string) (bool, error) {
	if gitRoot == "" {
		return false, errors.New("blob reachable: gitRoot is empty")
	}
	if hash == "" {
		return false, errors.New("blob reachable: hash is empty")
	}
	if path == "" {
		return false, errors.New("blob reachable: path is empty")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return false, fmt.Errorf("blob reachable: %w", err)
	}
	cmd := exec.Command("git", "-C", gitRoot, "cat-file", "-e", hash+":"+path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// Any clean git exit (object absent / path not in tree / unresolvable
	// revision) is the negative answer, not a hard error — retain-the-chain
	// is safe.
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	msg := bytes.TrimSpace(stderr.Bytes())
	if len(msg) == 0 {
		return false, fmt.Errorf("blob reachable %s:%s: %w", hash, path, err)
	}
	return false, fmt.Errorf("blob reachable %s:%s: %w: %s", hash, path, err, string(msg))
}

// ShowFileAtCommit returns the bytes of path as of commit hash in the
// workspace repo at gitRoot — the §3.9.1 recovery read for an aged-out
// committed file. Implemented as a read-only `git show <hash>:<path>`.
//
// Returns (content, true, nil) when the blob exists at that commit;
// ("", false, nil) when the path is absent at that commit or the commit is
// unreachable (git exits 128 for both — "not in tree" and "bad object" are
// the same "cannot recover this blob" answer to the caller). The error
// return is reserved for invocation failures (git missing). Read-only;
// never mutates the workspace tree.
func ShowFileAtCommit(gitRoot, hash, path string) (content string, ok bool, err error) {
	if gitRoot == "" {
		return "", false, errors.New("show file at commit: gitRoot is empty")
	}
	if hash == "" {
		return "", false, errors.New("show file at commit: hash is empty")
	}
	if path == "" {
		return "", false, errors.New("show file at commit: path is empty")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", false, fmt.Errorf("show file at commit: %w", err)
	}
	cmd := exec.Command("git", "-C", gitRoot, "show", hash+":"+path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return stdout.String(), true, nil
	}
	// git exits non-zero when the blob is absent at that commit or the
	// commit itself is unreachable. Both are "cannot recover this blob" —
	// report ok=false, not an invocation error, so the recovery path maps
	// it to ErrFileVersionUnreachable rather than a hard failure.
	if _, isExit := runErr.(*exec.ExitError); isExit {
		return "", false, nil
	}
	msg := bytes.TrimSpace(stderr.Bytes())
	if len(msg) == 0 {
		return "", false, fmt.Errorf("show file at commit %s:%s: %w", hash, path, runErr)
	}
	return "", false, fmt.Errorf("show file at commit %s:%s: %w: %s", hash, path, runErr, string(msg))
}
