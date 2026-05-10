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
