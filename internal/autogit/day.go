package autogit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"personant/internal/clock"
	"personant/internal/store"
)

// DayIndexOf is the single-clock day derivation the barrier and the
// Personant-Day trailer use: whole UTC calendar days since the Unix
// epoch. Monotone with the Timeline clock (which the sim slaves to its
// pinned clock), exact under UTC (no DST), and stable across processes
// — everything the new-day poll needs. The absolute value is arbitrary;
// only ordering and per-calendar-day equality are load-bearing.
func DayIndexOf(t time.Time) int {
	return int(t.UTC().Unix() / (24 * 60 * 60))
}

// CurrentDay is DayIndexOf(clock.Timeline()) — "which day is it" on the
// single clock.
func CurrentDay() int {
	return DayIndexOf(clock.Timeline())
}

// Personant-Day trailer machinery (#94 R3b §4, F3). Every PRIMARY commit
// — day-commit, all three archival commits, the cell-12 adopt, and the
// recovery stamp/roll-forward commits — carries `Personant-Day: <N>`,
// the day being sealed or repaired. Consequence: HEADDAY (the trailer of
// primary HEAD) is always defined on any primary that has taken a
// trailered commit, and there is no tag fallback anywhere.
//
// Detection vs. idempotence guard — deliberately NOT conflated:
//   - New-day DETECTION reads the trailer off HEAD regardless of commit
//     shape (HeadDay works on any trailered commit).
//   - The INV-2 idempotence GUARD is stricter: it fires only on a
//     DayCommitMessage-shape HEAD whose trailer equals the target day
//     (HeadIsDayCommit). A trailered-but-non-day-shape HEAD (an archival
//     stamp, an adopt, a roll-forward repair) advances HEADDAY for
//     detection but does NOT satisfy the guard, so the real day-commit
//     is still minted.

// DayTrailerKey is the commit-message trailer key carrying the day index
// on primary commits.
const DayTrailerKey = "Personant-Day"

// daySubjectPrefix is the day-commit subject shape ("day <N>"). The
// INV-2 guard matches the SHAPE, not just the trailer — every primary
// commit is trailered (F3), so a trailer-only guard would let a
// roll-forward stamp or an adopt falsely satisfy day-commit idempotence.
const daySubjectPrefix = "day "

// DayCommitMessage composes the barrier B2 day-commit message: the
// canonical "day <N>" subject plus the day trailer. HeadIsDayCommit
// recognizes exactly this shape.
func DayCommitMessage(day int) string {
	return daySubjectPrefix + strconv.Itoa(day) + "\n\n" + DayTrailerKey + ": " + strconv.Itoa(day) + "\n"
}

// WithDayTrailer appends the Personant-Day trailer to msg — the helper
// every non-day-shape primary commit path (archival, adopt, recovery
// repair) uses so primary commits are trailered uniformly (F3).
func WithDayTrailer(msg string, day int) string {
	msg = strings.TrimRight(msg, "\n")
	return msg + "\n\n" + DayTrailerKey + ": " + strconv.Itoa(day) + "\n"
}

// ParseDayTrailer extracts the day index from a commit message. ok is
// false when the message carries no (or a malformed) Personant-Day
// trailer. The last matching line wins, per git trailer convention.
func ParseDayTrailer(message string) (day int, ok bool) {
	const prefix = DayTrailerKey + ":"
	for _, line := range strings.Split(message, "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), prefix); found {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				day, ok = n, true
			}
		}
	}
	return day, ok
}

// isDayCommitMessage reports whether message has the DayCommitMessage
// shape for exactly `day`: the "day <N>" subject line AND a matching
// trailer.
func isDayCommitMessage(message string, day int) bool {
	subject, _, _ := strings.Cut(message, "\n")
	if strings.TrimSpace(subject) != daySubjectPrefix+strconv.Itoa(day) {
		return false
	}
	n, ok := ParseDayTrailer(message)
	return ok && n == day
}

// headMessage loads `which`'s HEAD commit message.
func headMessage(paths store.PersonantPaths, which Repo) (string, error) {
	repo, err := openRepo(paths, which)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("HEAD: %w", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", fmt.Errorf("load HEAD commit: %w", err)
	}
	return commit.Message, nil
}

// HeadDay returns the Personant-Day trailer of `which`'s HEAD commit —
// the new-day DETECTION read. ok=false means HEAD carries no trailer
// (⊥): a bare/legacy/pre-R3b primary that has never taken a trailered
// commit. Callers pass Primary; daily commits never carry the trailer.
func HeadDay(ctx context.Context, paths store.PersonantPaths, which Repo) (day int, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, fmt.Errorf("autogit.HeadDay: %w", err)
	}
	msg, err := headMessage(paths, which)
	if err != nil {
		return 0, false, fmt.Errorf("autogit.HeadDay: %w", err)
	}
	day, ok = ParseDayTrailer(msg)
	return day, ok, nil
}

// HeadIsDayCommit is the INV-2 idempotence guard: it reports whether
// `which`'s HEAD is a DayCommitMessage-shape commit for exactly `day`.
// See the package comment above for why this is stricter than HeadDay.
func HeadIsDayCommit(ctx context.Context, paths store.PersonantPaths, which Repo, day int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("autogit.HeadIsDayCommit: %w", err)
	}
	msg, err := headMessage(paths, which)
	if err != nil {
		return false, fmt.Errorf("autogit.HeadIsDayCommit: %w", err)
	}
	return isDayCommitMessage(msg, day), nil
}
