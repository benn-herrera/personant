// Package opencode drives an unmodified, isolated `opencode serve` for the
// end-to-end probe.
package opencode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	startTimeout = 30 * time.Second
	stopTimeout  = 15 * time.Second
	reapTimeout  = 5 * time.Second
)

// Config locates one isolated opencode run. Paths are absolute.
type Config struct {
	RunHome    string // created if absent; holds home, workspace, config and logs
	MockURL    string // OpenAI-compatible base URL, e.g. http://127.0.0.1:N/v1
	PluginDir  string // directory holding the plugin's index.ts
	ServiceURL string // memory service URL, passed as the plugin's only option
}

// Driver is one running opencode server.
type Driver struct {
	URL       string
	PID       int
	RunHome   string
	Workspace string

	auth  string
	stdin io.WriteCloser
	done  chan struct{} // closed once the process is reaped
}

// Start spawns `opencode serve --stdio --port 0` with HOME, TMPDIR, XDG roots
// and config under cfg.RunHome and its cwd in a fresh git workspace, and
// returns once /api/info answers. A t.Cleanup kills the process group if it is
// still running and fails t if anything in it survives.
func Start(t testing.TB, cfg Config) (*Driver, error) {
	home := filepath.Join(cfg.RunHome, "home")
	ws := filepath.Join(cfg.RunHome, "workspace")
	for _, d := range []string{"config", "data", "state", "cache", "tmp"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return nil, err
	}
	pw := make([]byte, 24)
	if _, err := rand.Read(pw); err != nil {
		return nil, err
	}
	password := hex.EncodeToString(pw)
	cfgPath := filepath.Join(cfg.RunHome, "opencode.json")
	env := []string{
		"PATH=" + os.Getenv("PATH"), "LANG=en_US.UTF-8", "TERM=dumb", "SHELL=/bin/bash",
		"HOME=" + home, "TMPDIR=" + filepath.Join(home, "tmp"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_DATA_HOME=" + filepath.Join(home, "data"),
		"XDG_STATE_HOME=" + filepath.Join(home, "state"),
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"OPENCODE_PASSWORD=" + password, "OPENCODE_CONFIG=" + cfgPath,
		"OPENCODE_DISABLE_PROJECT_CONFIG=1", "OPENCODE_DISABLE_CLAUDE_CODE=1",
		"OPENCODE_DISABLE_MODELS_FETCH=1",
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "personant-probe"},
		{"config", "user.email", "probe@personant.invalid"},
	} {
		git := exec.Command("git", args...)
		git.Dir, git.Env = ws, env
		if out, err := git.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
	}
	if err := writeConfig(cfgPath, cfg); err != nil {
		return nil, err
	}

	stderr, err := os.Create(filepath.Join(cfg.RunHome, "opencode.stderr"))
	if err != nil {
		return nil, err
	}
	defer stderr.Close() // the child holds its own copy
	stdoutLog, err := os.Create(filepath.Join(cfg.RunHome, "opencode.stdout"))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("opencode", "serve", "--stdio", "--port", "0")
	cmd.Dir, cmd.Env, cmd.Stderr = ws, env, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stdoutLog.Close()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdoutLog.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdoutLog.Close()
		return nil, fmt.Errorf("start opencode: %w", err)
	}
	d := &Driver{
		PID: cmd.Process.Pid, RunHome: cfg.RunHome, Workspace: ws,
		auth:  "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+password)),
		stdin: stdin, done: make(chan struct{}),
	}
	t.Cleanup(func() {
		if err := d.kill(); err != nil {
			t.Errorf("opencode pid %d: %v", d.PID, err)
		}
	})

	firstLine := make(chan string, 1)
	go func() {
		defer close(d.done)
		defer stdoutLog.Close()
		br := bufio.NewReader(stdout)
		line, _ := br.ReadString('\n')
		firstLine <- line
		if _, err := io.Copy(stdoutLog, br); err != nil {
			fmt.Fprintf(stdoutLog, "\nstdout copy: %v\n", err)
		}
		if err := cmd.Wait(); err != nil {
			fmt.Fprintf(stdoutLog, "\nexit: %v\n", err)
		}
	}()

	select {
	case line := <-firstLine:
		var hello struct{ URL string }
		if err := json.Unmarshal([]byte(line), &hello); err != nil || hello.URL == "" {
			return nil, fmt.Errorf("opencode stdout line 1 %q: not {url}", line)
		}
		d.URL = strings.TrimSuffix(hello.URL, "/")
	case <-time.After(startTimeout):
		return nil, fmt.Errorf("opencode printed no url within %v", startTimeout)
	}

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	for {
		if err := d.Do(ctx, http.MethodGet, "/api/info", nil, nil); err == nil {
			return d, nil
		} else if ctx.Err() != nil {
			return nil, fmt.Errorf("opencode /api/info not ready: %w", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func writeConfig(path string, cfg Config) error {
	c := map[string]any{
		"model":      "mock/mock-model",
		"update":     "disable",
		"share":      "disabled",
		"compaction": map[string]any{"auto": false},
		"permissions": []map[string]string{
			{"action": "*", "resource": "*", "effect": "allow"},
		},
		"providers": map[string]any{"mock": map[string]any{
			"name":     "Personant mock",
			"package":  "@ai-sdk/openai-compatible",
			"settings": map[string]string{"baseURL": cfg.MockURL, "apiKey": "mock-key"},
			"models": map[string]any{"mock-model": map[string]any{
				"name": "Mock", "limit": map[string]int{"context": 200000, "output": 8192},
			}},
		}},
		"plugins": []map[string]any{{
			"package": cfg.PluginDir,
			"options": map[string]string{"url": cfg.ServiceURL},
		}},
	}
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Do sends a JSON request with Basic auth. body and out may be nil; a
// non-2xx status is an error carrying the response body.
func (d *Driver) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.URL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", d.auth)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Stop closes opencode's stdin, which ends the server, and waits for it to be
// reaped; past the deadline it kills the process group. Either way it fails
// if any process of the group is left.
func (d *Driver) Stop() error {
	if err := d.stdin.Close(); err != nil {
		return err
	}
	select {
	case <-d.done:
	case <-time.After(stopTimeout):
		return fmt.Errorf("opencode did not exit %v after stdin close: %w", stopTimeout, d.kill())
	}
	if groupAlive(d.PID) {
		return fmt.Errorf("opencode exited but its process group survives: %w", d.kill())
	}
	return nil
}

// kill SIGKILLs the process group rooted at the recorded PID unless it is
// already reaped and empty, then verifies nothing in it survives.
func (d *Driver) kill() error {
	select {
	case <-d.done:
		if !groupAlive(d.PID) {
			return nil
		}
	default:
	}
	if err := syscall.Kill(-d.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill group %d: %w", d.PID, err)
	}
	select {
	case <-d.done:
	case <-time.After(reapTimeout):
		return fmt.Errorf("pid %d not reaped %v after SIGKILL", d.PID, reapTimeout)
	}
	// Killed descendants other than the leader are reaped by init, not us;
	// give them a moment to leave the group before the verdict.
	for i := 0; i < 50 && groupAlive(d.PID); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if groupAlive(d.PID) {
		return fmt.Errorf("process group %d still alive after SIGKILL", d.PID)
	}
	return nil
}

func groupAlive(pgid int) bool {
	return !errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH)
}

// UserDBTouched reports whether the user's real opencode DB records a session
// in this run's workspace. It copies the DB and its -wal into the run home and
// queries only the copy, read-only; the originals are only read as bytes.
func (d *Driver) UserDBTouched() (bool, error) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	n, err := sessionsUnder(
		filepath.Join(realHome, ".local", "share", "opencode", "opencode.db"),
		filepath.Join(d.RunHome, "user-db-copy"), d.Workspace)
	return n > 0, err
}

// sessionsUnder copies srcDB (and srcDB-wal) into dstDir and counts the
// copy's session_v2 rows whose directory is ws or lies under it. A missing
// srcDB counts 0.
func sessionsUnder(srcDB, dstDir, ws string) (int, error) {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return 0, err
	}
	dst := filepath.Join(dstDir, filepath.Base(srcDB))
	for _, suffix := range []string{"", "-wal"} {
		err := copyFile(srcDB+suffix, dst+suffix)
		if errors.Is(err, os.ErrNotExist) {
			if suffix == "" {
				return 0, nil
			}
			continue
		}
		if err != nil {
			return 0, err
		}
	}
	dirs := []string{ws}
	if real, err := filepath.EvalSymlinks(ws); err == nil && real != ws {
		dirs = append(dirs, real)
	}
	var where []string
	for _, dir := range dirs {
		q := sqlQuote(dir)
		p := sqlQuote(dir + "/")
		where = append(where, fmt.Sprintf("directory = %s OR substr(directory, 1, %d) = %s",
			q, len(dir)+1, p))
	}
	query := "SELECT count(*) FROM session_v2 WHERE " + strings.Join(where, " OR ") + ";"
	out, err := exec.Command("sqlite3", "-readonly", dst, query).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("sqlite3 %s: %w: %s", dst, err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("sqlite3 count %q: %w", out, err)
	}
	return n, nil
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
