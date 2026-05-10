package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PersonantPaths holds the canonical layout of $PERSONANT_HOME.
// See v0.1-spec.md §2.1.
//
// The struct is value-based; every field is an absolute path. Paths are
// computed but not created — `personant init` is the scaffolder.
type PersonantPaths struct {
	Home          string // $PERSONANT_HOME (default ~/.personant)
	Spine         string // Home/spine.jsonl                  (canonical)
	Symbols       string // Home/symbols.jsonl                (derived)
	ThreadsDir    string // Home/threads/                     (canonical: thr_<id>.md)
	ProjectsDir   string // Home/projects/                    (per-project meta + digest)
	DirectivesDir string // Home/directives/                  (defaults.md, user.md, <project>/...)
	LogsDir       string // Home/logs/                        (YYYY-MM-DD.log + archive/)
	LogsArchive   string // Home/logs/archive/                (rotated tar.gz)
	TmpDir        string // Home/tmp/                         (agent drafting scratch; not git-committed)
	Readme        string // Home/README.md                    (layout doc for human inspection)
	Providers     string // Home/providers.toml               (canonical, secret-bearing)
	Gitignore     string // Home/.gitignore                   (excludes tmp/, providers.toml)
	LastActive    string // Home/last-active                  (operational; one line: prj_<n>; gitignored)
}

// EnvHome is the environment variable that overrides the default home.
const EnvHome = "PERSONANT_HOME"

// DefaultHomeName is the directory name used under $HOME when EnvHome is unset.
const DefaultHomeName = ".personant"

// PathsForHome returns a PersonantPaths rooted at an explicit home directory.
// Used by tests and by `personant init --home <dir>` to bypass the cached
// global resolution.
func PathsForHome(home string) PersonantPaths {
	return makePaths(home)
}

func makePaths(home string) PersonantPaths {
	return PersonantPaths{
		Home:          home,
		Spine:         filepath.Join(home, "spine.jsonl"),
		Symbols:       filepath.Join(home, "symbols.jsonl"),
		ThreadsDir:    filepath.Join(home, "threads"),
		ProjectsDir:   filepath.Join(home, "projects"),
		DirectivesDir: filepath.Join(home, "directives"),
		LogsDir:       filepath.Join(home, "logs"),
		LogsArchive:   filepath.Join(home, "logs", "archive"),
		TmpDir:        filepath.Join(home, "tmp"),
		Readme:        filepath.Join(home, "README.md"),
		Providers:     filepath.Join(home, "providers.toml"),
		Gitignore:     filepath.Join(home, ".gitignore"),
		LastActive:    filepath.Join(home, "last-active"),
	}
}

var (
	resolvedPaths PersonantPaths
	resolveErr    error
	resolveOnce   sync.Once
)

// IsDir reports whether path exists and is a directory.
func IsDir(path string) bool {
	stat, err := os.Stat(path)
	return err == nil && stat.IsDir()
}

// ResolvePaths returns the canonical PersonantPaths for this process.
//
// Resolution order:
//  1. $PERSONANT_HOME if set and non-empty.
//  2. $HOME/.personant otherwise.
//
// The result is computed once and cached for the lifetime of the process.
// Path validity (existence, scaffold completeness) is not checked here —
// callers that require an initialized home should consult `personant init`
// or `personant verify`.
func ResolvePaths() (PersonantPaths, error) {
	resolveOnce.Do(func() {
		home := os.Getenv(EnvHome)
		if home == "" {
			userHome, err := os.UserHomeDir()
			if err != nil {
				resolveErr = fmt.Errorf("resolve user home: %w", err)
				return
			}
			home = filepath.Join(userHome, DefaultHomeName)
		}
		resolvedPaths = makePaths(home)
	})
	return resolvedPaths, resolveErr
}
