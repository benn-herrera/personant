package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The `[user]` section step. The property that matters throughout: this
// is TEXTUAL insertion, so a hand-edited config.toml keeps every comment
// and every line's position. A TOML round-trip would pass a "the values
// are right" test and silently destroy the file.

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(b)
}

var testGitIdentity = UserIdentity{Name: "Ada Lovelace", Email: "ada@example.com"}

func TestEnsureUserSection(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     string
		wantAdded  []string
		wantName   string
		wantEmail  string
		wantLines  []string // must appear in the result
		wantAbsent []string
	}{
		{
			name:      "absent section is appended",
			before:    "# a comment\n\n[chat]\ndefaultModel = \"reaper/gemma\"\n",
			wantAdded: []string{"name", "email"},
			wantName:  "Ada Lovelace",
			wantEmail: "ada@example.com",
			wantLines: []string{
				"# a comment",
				"[chat]",
				"defaultModel = \"reaper/gemma\"",
				"[user]",
				"name = \"Ada Lovelace\"",
				"email = \"ada@example.com\"",
			},
		},
		{
			name:       "an existing complete section is untouched",
			before:     "[user]\nname = \"Grace Hopper\"\nemail = \"grace@example.com\"\n",
			wantAdded:  nil,
			wantName:   "Grace Hopper",
			wantEmail:  "grace@example.com",
			wantLines:  []string{"name = \"Grace Hopper\""},
			wantAbsent: []string{"Ada Lovelace"},
		},
		{
			// FIELD-level granularity: only what is missing is added, and the
			// value already there wins over the git default.
			name:      "a partial section gains only the missing field",
			before:    "[user]\nname = \"Grace Hopper\"\n",
			wantAdded: []string{"email"},
			wantName:  "Grace Hopper",
			wantEmail: "ada@example.com",
			wantLines: []string{"name = \"Grace Hopper\"", "email = \"ada@example.com\""},
		},
		{
			// A commented-out key is not a key. The shipped template is
			// almost entirely commented examples.
			name:      "a commented-out field does not count as present",
			before:    "[user]\n# name = \"someone\"\n# email = \"someone@example.com\"\n",
			wantAdded: []string{"name", "email"},
			wantName:  "Ada Lovelace",
			wantEmail: "ada@example.com",
			wantLines: []string{"# name = \"someone\"", "name = \"Ada Lovelace\""},
		},
		{
			// A commented-out HEADER is not a header either — the section is
			// absent and must be appended, not written into the comment block.
			name:      "a commented-out header does not count as a section",
			before:    "# [user]\n# name = \"someone\"\n",
			wantAdded: []string{"name", "email"},
			wantName:  "Ada Lovelace",
			wantEmail: "ada@example.com",
			wantLines: []string{"# [user]", "[user]", "name = \"Ada Lovelace\""},
		},
		{
			// The section is not last in the file: the insert goes under its
			// header, and the section that follows stays where it was.
			name:      "insertion lands inside the section, not at the end",
			before:    "[user]\nname = \"Grace Hopper\"\n\n[chat]\ndefaultModel = \"reaper/gemma\"\n",
			wantAdded: []string{"email"},
			wantName:  "Grace Hopper",
			wantEmail: "ada@example.com",
			wantLines: []string{"[user]\nemail = \"ada@example.com\"\nname = \"Grace Hopper\"\n\n[chat]"},
		},
		{
			name:      "an empty file gets just the section",
			before:    "",
			wantAdded: []string{"name", "email"},
			wantName:  "Ada Lovelace",
			wantEmail: "ada@example.com",
			wantLines: []string{"[user]\nname = \"Ada Lovelace\"\nemail = \"ada@example.com\"\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.before)
			res, err := EnsureUserSection(path, testGitIdentity, true)
			if err != nil {
				t.Fatalf("EnsureUserSection: %v", err)
			}
			if strings.Join(res.Added, ",") != strings.Join(tc.wantAdded, ",") {
				t.Errorf("Added = %v, want %v", res.Added, tc.wantAdded)
			}
			if res.Identity.Name != tc.wantName || res.Identity.Email != tc.wantEmail {
				t.Errorf("Identity = %+v, want {%q %q}", res.Identity, tc.wantName, tc.wantEmail)
			}
			got := readConfig(t, path)
			for _, want := range tc.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("result missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("result gained %q it should not have:\n%s", absent, got)
				}
			}
			// Every comment in the input must survive.
			for _, line := range strings.Split(tc.before, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") && !strings.Contains(got, line) {
					t.Errorf("comment %q was lost — this is textual insertion, not a TOML round-trip:\n%s", line, got)
				}
			}
		})
	}
}

// TestEnsureUserSectionIdempotent — a second run writes nothing at all,
// which is what makes `personant init` safe to re-run.
func TestEnsureUserSectionIdempotent(t *testing.T) {
	path := writeConfig(t, "# keep me\n[chat]\n")
	if _, err := EnsureUserSection(path, testGitIdentity, true); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := readConfig(t, path)

	res, err := EnsureUserSection(path, UserIdentity{Name: "Someone Else", Email: "else@example.com"}, true)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(res.Added) != 0 {
		t.Errorf("the second run added %v; the section was already complete", res.Added)
	}
	if got := readConfig(t, path); got != first {
		t.Errorf("the second run rewrote the file:\nbefore:\n%s\nafter:\n%s", first, got)
	}
}

// TestEnsureUserSectionEmptyIdentity — no git identity is not an error.
// The section is still created, empty, so the user has somewhere obvious
// to type; the CALLER is what tells them the Wikimedia tools are off.
func TestEnsureUserSectionEmptyIdentity(t *testing.T) {
	path := writeConfig(t, "[chat]\n")
	res, err := EnsureUserSection(path, UserIdentity{}, false)
	if err != nil {
		t.Fatalf("EnsureUserSection: %v", err)
	}
	if res.Identity.Complete() {
		t.Errorf("an empty git identity produced a complete one: %+v", res.Identity)
	}
	got := readConfig(t, path)
	for _, want := range []string{"[user]", `name = ""`, `email = ""`} {
		if !strings.Contains(got, want) {
			t.Errorf("result missing %q:\n%s", want, got)
		}
	}

	// And the section it wrote is recognized on a re-run, so a user who
	// fills in only the name gets only the email added next time.
	res2, err := EnsureUserSection(path, testGitIdentity, true)
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if len(res2.Added) != 0 {
		t.Errorf("the empty fields were re-added as duplicates: %v", res2.Added)
	}
}

// TestInitPopulatesUserSection — the wiring: a fresh home's config.toml
// comes out of Init with a [user] section, and the step reports itself on
// BOTH the log and Out (an unpopulated identity disables the Wikimedia
// tools, and the user cannot be expected to have logging turned up).
//
// It deliberately asserts nothing about the VALUES: they come from
// whatever git identity this machine has, which is not the test's
// business. EnsureUserSection above owns the value semantics.
func TestInitPopulatesUserSection(t *testing.T) {
	hasGit(t)
	paths := PathsForHome(t.TempDir())

	var out strings.Builder
	var logged []string
	err := Init(paths, InitOptions{
		Out:    &out,
		Logger: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	cfg := readConfig(t, paths.Config)
	for _, want := range []string{"[user]", "name =", "email ="} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.toml has no %q after init:\n%s", want, cfg)
		}
	}
	// The shipped template's comments must still be there.
	if !strings.Contains(cfg, "# personant configuration") {
		t.Errorf("init clobbered the config.toml template comments:\n%s", cfg)
	}

	if !strings.Contains(out.String(), "[user]") {
		t.Errorf("the [user] step did not reach Out:\n%s", out.String())
	}
	if !strings.Contains(strings.Join(logged, "\n"), "[user]") {
		t.Errorf("the [user] step did not reach the log:\n%s", strings.Join(logged, "\n"))
	}

	// Quiet silences both channels, which is what every embedded caller
	// (the chat bootstrap's idempotent re-init) relies on.
	var quiet strings.Builder
	if err := Init(paths, InitOptions{Quiet: true, Out: &quiet}); err != nil {
		t.Fatalf("quiet Init: %v", err)
	}
	if quiet.Len() != 0 {
		t.Errorf("Quiet=true still wrote to Out: %q", quiet.String())
	}
}

// TestEnsureUserSectionEscapesValues — a name with a quote or a backslash
// must not produce a config.toml that no longer parses.
func TestEnsureUserSectionEscapesValues(t *testing.T) {
	path := writeConfig(t, "")
	awkward := UserIdentity{Name: `Ada "The Countess" Lovelace\`, Email: "ada@example.com"}
	if _, err := EnsureUserSection(path, awkward, true); err != nil {
		t.Fatalf("EnsureUserSection: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("the written config no longer parses: %v", err)
	}
	if cfg.User.Name != awkward.Name {
		t.Errorf("round-tripped name = %q, want %q", cfg.User.Name, awkward.Name)
	}
	if cfg.User.Email != awkward.Email {
		t.Errorf("round-tripped email = %q, want %q", cfg.User.Email, awkward.Email)
	}
}
