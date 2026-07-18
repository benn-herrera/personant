package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// PersonantPaths holds the canonical layout of $PERSONANT_HOME.
// See v0.1-SPEC.md §2.1.
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
	ArchiveDir    string // Home/archive/                     (canonical: recoverable thread archival)
	ArchiveIndex  string // Home/archive/index.jsonl          (canonical: archive lookup, sorted by thr_id)
	TmpDir        string // Home/tmp/                         (agent drafting scratch; not git-committed)
	Readme        string // Home/README.md                    (layout doc for human inspection)
	Providers     string // Home/providers.toml               (provider pool; apiKeyFile keeps it scannable)
	Config        string // Home/config.toml                  (chat/embedding choices drawn from the pool)
	Gitignore     string // Home/.gitignore                   (excludes tmp/, key files)
	LastActive    string // Home/last-active                  (operational; one line: prj_<n>; gitignored)
	WorkingSet    string // Home/working-set.json             (operational; Layer B/C membership; rewritten per turn; gitignored)
	RecallCache   string // Home/.recall-cache/               (operational; derived embedding-vector cache; gitignored; never canonical, §5/I5)
	// Crash-stability substrate (#94, SPEC §4.5.8). All operational,
	// gitignored, and survive a recovery reset --hard.
	OpMarker         string // Home/op-in-progress.json          (operational; op-typed in-flight marker; presence authorizes recovery reset)
	TurnJournal      string // Home/turn-journal.jsonl           (operational; per-append-fsync content journal; truncated on CommitTurn)
	DerivedWatermark string // Home/derived-watermark            (operational; built-from-DAILY-commit hash; written only by the regeneration path)
	RecoveryDir      string // Home/recovery/                    (operational; preserved crash artifacts: recovered-turn content + reset log staging; gitignored)
	GitDaily         string // Home/.git-daily                   (operational, gitignored: the disposable per-turn recovery DB; nuked + reborn each day barrier; never career history)
}

// EnvHome is the environment variable that overrides the default home.
const EnvHome = "PERSONANT_HOME"

// DefaultHomeName is the directory name used under $HOME when EnvHome is unset.
const DefaultHomeName = ".personant"

// PathsForHome returns a PersonantPaths rooted at an explicit home directory.
// Used by tests and by `personant init --home <dir>` to bypass environment
// resolution.
func PathsForHome(home string) PersonantPaths {
	return makePaths(home)
}

func makePaths(home string) PersonantPaths {
	return PersonantPaths{
		Home:          home,
		Spine:         filepath.Join(home, spineFileName),
		Symbols:       filepath.Join(home, "symbols.jsonl"),
		ThreadsDir:    filepath.Join(home, threadsDirName),
		ProjectsDir:   filepath.Join(home, projectsDirName),
		DirectivesDir: filepath.Join(home, "directives"),
		LogsDir:       filepath.Join(home, "logs"),
		LogsArchive:   filepath.Join(home, "logs", "archive"),
		ArchiveDir:    filepath.Join(home, "archive"),
		ArchiveIndex:  filepath.Join(home, "archive", "index.jsonl"),
		TmpDir:        filepath.Join(home, "tmp"),
		Readme:        filepath.Join(home, "README.md"),
		Providers:     filepath.Join(home, "providers.toml"),
		Config:        filepath.Join(home, "config.toml"),
		Gitignore:     filepath.Join(home, ".gitignore"),
		LastActive:    filepath.Join(home, "last-active"),
		WorkingSet:    filepath.Join(home, "working-set.json"),
		RecallCache:   filepath.Join(home, ".recall-cache"),

		OpMarker:         filepath.Join(home, "op-in-progress.json"),
		TurnJournal:      filepath.Join(home, "turn-journal.jsonl"),
		DerivedWatermark: filepath.Join(home, "derived-watermark"),
		RecoveryDir:      filepath.Join(home, "recovery"),
		GitDaily:         filepath.Join(home, ".git-daily"),
	}
}

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
// Path validity (existence, scaffold completeness) is not checked here —
// callers that require an initialized home should consult `personant init`
// or `personant verify`.
func ResolvePaths() (PersonantPaths, error) {
	home := os.Getenv(EnvHome)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return PersonantPaths{}, fmt.Errorf("resolve user home: %w", err)
		}
		home = filepath.Join(userHome, DefaultHomeName)
	}
	return makePaths(home), nil
}
