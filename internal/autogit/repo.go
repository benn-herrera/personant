package autogit

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"personant/internal/store"
)

// Repo selects which of the two git DBs an autogit verb operates on
// (#94 R3b dual-repo scheme). Both DBs track the ONE shared worktree
// (paths.Home) at different cadences:
//
//   - Primary (<Home>/.git) — career history: one day-grain commit per
//     day barrier plus archival anchors. Barrier-exclusive (INV-5), with
//     the recovery-repair exemption (cell-9 stamp, cell-12 adopt, F1
//     roll-forward).
//   - Daily (<Home>/.git-daily) — today's microscope: per-turn commits,
//     turn resets, mid-session Checkpoints. Disposable by construction —
//     nuked and reborn at each day barrier; losing it is never losing
//     content (the worktree is the truth).
//
// Every verb takes the selector explicitly so the target repo is
// greppable at every call site — the design's defense against a caller
// quietly writing per-turn commits into permanent history.
type Repo int

const (
	Primary Repo = iota
	Daily
)

func (r Repo) String() string {
	switch r {
	case Primary:
		return "primary"
	case Daily:
		return "daily"
	default:
		return fmt.Sprintf("Repo(%d)", int(r))
	}
}

// openRepo opens paths.Home's worktree against the chosen git-dir.
// Primary → <Home>/.git via git.PlainOpen. Daily → <Home>/.git-daily as
// a divorced git-dir via git.Open(filesystem storage, worktree fs) —
// PlainOpen/PlainInit are not usable for the daily handle (they assume
// .git).
func openRepo(paths store.PersonantPaths, which Repo) (*git.Repository, error) {
	switch which {
	case Primary:
		return git.PlainOpen(paths.Home)
	case Daily:
		if paths.GitDaily == "" {
			return nil, errors.New("autogit: PersonantPaths.GitDaily is empty")
		}
		storer := filesystem.NewStorage(osfs.New(paths.GitDaily), cache.NewObjectLRUDefault())
		return git.Open(storer, osfs.New(paths.Home))
	default:
		return nil, fmt.Errorf("autogit: unknown repo selector %d", int(which))
	}
}

// Open exposes the repo handle for the few recovery call sites that walk
// history/trees directly (locateDeletionCommit, trackedLogFiles). It
// exists so no caller outside this package ever spells the git-dir
// resolution itself — the Repo selector stays greppable there too.
func Open(paths store.PersonantPaths, which Repo) (*git.Repository, error) {
	return openRepo(paths, which)
}

// InitDaily creates the daily DB (<Home>/.git-daily) as a fresh, empty
// repository over the existing worktree. It is the first half of
// morning-init (the baseline commit is the caller's next step). The
// git-dir must not already contain a repository — callers recreating a
// half-created/corrupt daily rm -rf it first (NukeDaily).
//
// Deliberately NOT git.Init(storer, worktree): go-git's Init writes a
// `.git` gitdir-link FILE into the worktree when the git-dir is
// divorced — which would collide with primary's real `.git` directory.
// The storage init + HEAD symbolic ref below is exactly git.Init minus
// that link (and minus core.worktree, which git.Open ignores because
// the worktree fs is passed explicitly on every open).
func InitDaily(ctx context.Context, paths store.PersonantPaths) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("autogit.InitDaily: %w", err)
	}
	if paths.GitDaily == "" {
		return errors.New("autogit.InitDaily: PersonantPaths.GitDaily is empty")
	}
	storer := filesystem.NewStorage(osfs.New(paths.GitDaily), cache.NewObjectLRUDefault())
	if err := storer.Init(); err != nil {
		return fmt.Errorf("autogit.InitDaily: init storage: %w", err)
	}
	head := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.Master)
	if err := storer.SetReference(head); err != nil {
		return fmt.Errorf("autogit.InitDaily: set HEAD: %w", err)
	}
	return nil
}

// NukeDaily removes the daily git-dir entirely (barrier B4 / the
// half-created recreate rows). Idempotent: removing an absent dir is a
// no-op. It NEVER touches the worktree or primary — the daily DB is
// disposable scaffolding (§9).
func NukeDaily(paths store.PersonantPaths) error {
	if paths.GitDaily == "" {
		return errors.New("autogit.NukeDaily: PersonantPaths.GitDaily is empty")
	}
	if err := os.RemoveAll(paths.GitDaily); err != nil {
		return fmt.Errorf("autogit.NukeDaily: %w", err)
	}
	return nil
}

// DailyState is the recovery DAILY observable (#94 R3b §2.4).
type DailyState int

const (
	// DailyPresent — .git-daily opens with a valid HEAD.
	DailyPresent DailyState = iota
	// DailyMissing — .git-daily is absent. The NORMAL morning state, not
	// a crash signature (benign-morning row).
	DailyMissing
	// DailyHalfCreated — .git-daily exists but does not open to a valid
	// HEAD (interrupted init, partial nuke, or corruption). Always a
	// recreate-from-worktree candidate, never an error.
	DailyHalfCreated
)

func (s DailyState) String() string {
	switch s {
	case DailyPresent:
		return "present"
	case DailyMissing:
		return "missing"
	case DailyHalfCreated:
		return "half-created"
	default:
		return fmt.Sprintf("DailyState(%d)", int(s))
	}
}

// ProbeDaily evaluates the DAILY observable defensively (F7): an open
// failure on an existing-but-corrupt .git-daily is the half-created
// signal, NOT a propagated error — a corrupt daily is always recreated
// from the worktree and can never wedge an open.
func ProbeDaily(paths store.PersonantPaths) DailyState {
	if paths.GitDaily == "" {
		return DailyMissing
	}
	if _, err := os.Stat(paths.GitDaily); err != nil {
		return DailyMissing
	}
	repo, err := openRepo(paths, Daily)
	if err != nil {
		return DailyHalfCreated
	}
	head, err := repo.Head()
	if err != nil {
		return DailyHalfCreated
	}
	// A garbage HEAD file can "resolve" to a nonexistent object; present
	// means a genuinely loadable HEAD commit, so corruption can never
	// wedge an open downstream (F7).
	if _, err := repo.CommitObject(head.Hash()); err != nil {
		return DailyHalfCreated
	}
	return DailyPresent
}
