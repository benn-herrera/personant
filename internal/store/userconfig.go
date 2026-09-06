package store

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The `[user]` section of config.toml — who personant says it is acting
// for when it reaches a third party.
//
// It exists for the §6.1 free-API citizenship rule (CONVENTIONS.md house rule,
// user ruling 2026-08-05): the Wikimedia-family tools REQUIRE contact
// information in their User-Agent and refuse to run without it, and
// Crossref and arXiv strongly prefer it. `personant init` fills the
// section in from the identity a developer already has, so the common
// case needs no hand-editing at all.
//
// # Why the git BINARY, and not go-git (user ruling, 2026-08-05)
//
// go-git's config parser does not resolve `include` / `includeIf`, whose
// FLAGSHIP use case is exactly per-context identity — the developer with
// `[includeIf "gitdir:~/work/"] path = .gitconfig-work` is the person
// most likely to have a considered answer to "who are you". Reading their
// identity with a parser that cannot see it would silently produce the
// wrong address. The installed binary resolves the whole chain correctly,
// costs one process, and is already required by anyone with a global
// identity to read.
//
// CLASSIC `git config --global <key>` syntax, deliberately: the
// `git config get` form landed in 2.46 (2024) and would fail on any older
// install for no gain.

const (
	// gitIdentityTimeout bounds the identity probe. `git config` is a
	// file read; a bound this generous only exists so a pathological
	// environment (a hung credential helper in an include chain, an
	// unresponsive network filesystem) cannot wedge init.
	gitIdentityTimeout = 5 * time.Second

	userSectionHeader = "[user]"
	userKeyName       = "name"
	userKeyEmail      = "email"
)

// UserIdentity is the pair init writes into `[user]`.
type UserIdentity struct {
	Name  string
	Email string
}

// Complete reports whether both halves are populated — the condition the
// Wikimedia-backed tools require.
func (u UserIdentity) Complete() bool { return u.Name != "" && u.Email != "" }

// GitGlobalIdentity reads user.name / user.email from the installed git
// binary's GLOBAL scope.
//
// Every "no value" outcome is an empty string, never an error: an unset
// key exits 1, and git not being on PATH is an ordinary state for a
// personant install (the substrate's own git work is in-process go-git).
// The bool reports whether the binary was found, which is the difference
// between "you have no git identity" and "you have no git" — two
// different sentences in the message the user reads.
func GitGlobalIdentity(ctx context.Context) (UserIdentity, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return UserIdentity{}, false
	}
	return UserIdentity{
		Name:  gitConfigValue(ctx, "user.name"),
		Email: gitConfigValue(ctx, "user.email"),
	}, true
}

func gitConfigValue(ctx context.Context, key string) string {
	ctx, cancel := context.WithTimeout(ctx, gitIdentityTimeout)
	defer cancel()
	// Classic syntax; --global scope only. A repo-scoped identity is
	// deliberately NOT consulted here — init runs against the personant
	// home, whose repo identity is personant's own, not the user's.
	cmd := exec.CommandContext(ctx, "git", "config", "--global", key)
	out, err := cmd.Output()
	if err != nil {
		return "" // exit 1 = unset; anything else is equally "no value"
	}
	return strings.TrimSpace(string(out))
}

// UserSectionResult reports what EnsureUserSection did, so the caller can
// say it in one line.
type UserSectionResult struct {
	// Added lists the fields written this run, in file order. Empty means
	// the section was already complete.
	Added []string
	// Identity is the resulting [user] content — the values now in the
	// file, whether written this run or found already there.
	Identity UserIdentity
	// GitFound reports whether a git binary was available to read
	// defaults from. Meaningless when Added is empty.
	GitFound bool
}

// EnsureUserSection adds whatever `[user]` is missing from config.toml at
// path, filling defaults from the git global identity.
//
// It is TEXTUAL INSERTION, not a TOML round-trip. config.toml is a
// hand-edited file whose comments and ordering are the user's; decoding
// and re-encoding it would silently discard every comment in it — and the
// shipped template is almost entirely comments. Granularity is
// FIELD-level: a section that has `name` but not `email` gains only
// `email`.
//
// An absent file is created holding just the section. A file that already
// has both fields is not rewritten at all.
func EnsureUserSection(path string, git UserIdentity, gitFound bool) (UserSectionResult, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return UserSectionResult{}, fmt.Errorf("read %s: %w", path, err)
	}

	lines := splitLines(existing)
	start, end := findSection(lines, userSectionHeader)
	present := sectionKeys(lines, start, end)

	res := UserSectionResult{GitFound: gitFound}
	res.Identity.Name = present[userKeyName]
	res.Identity.Email = present[userKeyEmail]

	var insert []string
	if _, ok := present[userKeyName]; !ok {
		insert = append(insert, tomlKV(userKeyName, git.Name))
		res.Added = append(res.Added, userKeyName)
		res.Identity.Name = git.Name
	}
	if _, ok := present[userKeyEmail]; !ok {
		insert = append(insert, tomlKV(userKeyEmail, git.Email))
		res.Added = append(res.Added, userKeyEmail)
		res.Identity.Email = git.Email
	}
	if len(insert) == 0 {
		return res, nil
	}

	var out []string
	if start < 0 {
		// No section: append one at the end, after a blank separator so it
		// does not fuse with a trailing comment block.
		out = append(out, trimTrailingBlanks(lines)...)
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, userSectionHeader)
		out = append(out, insert...)
	} else {
		// Section exists: insert the missing keys immediately under the
		// header, where a reader looking for them will look.
		out = append(out, lines[:start+1]...)
		out = append(out, insert...)
		out = append(out, lines[start+1:]...)
	}

	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		return UserSectionResult{}, fmt.Errorf("write %s: %w", path, err)
	}
	return res, nil
}

// splitLines returns the file's lines with the trailing newline's empty
// final element removed, so a join+"\n" round-trips.
func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))), "\n"), "\n")
}

func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// findSection locates a TOML table header and the extent of its body:
// the header's index and the index one past its last line (the next
// header, or the end of the file). Returns (-1, -1) when absent.
//
// This is a LINE SCANNER, not a TOML parser, and it does not need to be
// one: the only question is where the `[user]` header sits and which of
// two keys already appear under it. A commented-out header is not a
// header (the `#` fails the prefix test), which is the behaviour wanted —
// the shipped template's commented examples must not be mistaken for
// live config.
func findSection(lines []string, header string) (start, end int) {
	start, end = -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "[") {
			continue
		}
		if t == header {
			start = i
			continue
		}
		if start >= 0 {
			return start, i
		}
	}
	if start >= 0 {
		return start, len(lines)
	}
	return -1, -1
}

// sectionKeys collects the bare keys assigned inside a section, mapped to
// their raw (unquoted) values. Comment lines are skipped, so a
// commented-out `# name = "…"` correctly reads as absent.
func sectionKeys(lines []string, start, end int) map[string]string {
	out := map[string]string{}
	if start < 0 {
		return out
	}
	for _, l := range lines[start+1 : end] {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return out
}

// tomlKV renders one assignment. The value goes through %q, which is the
// escaping TOML basic strings want for the characters a name or an
// address could plausibly contain.
func tomlKV(key, value string) string {
	return fmt.Sprintf("%s = %q", key, value)
}
