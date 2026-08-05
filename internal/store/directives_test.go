package store

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDirective writes a directive file with the given frontmatter body
// at path, creating parents.
func writeDirective(t *testing.T, path, frontmatter string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	content := "---\n" + frontmatter + "---\n\n# directives\n\nprose.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestReadParameter_PrecedenceChain: project beats user beats defaults,
// and a level that does not set the key falls through rather than
// shadowing the level below it with an empty value.
func TestReadParameter_PrecedenceChain(t *testing.T) {
	const key = "closure.ack-mode"
	const prj = "prj_7"

	tests := []struct {
		name     string
		defaults string
		user     string
		project  string
		want     string
		wantOK   bool
	}{
		{
			name:     "defaults only",
			defaults: "parameters:\n  closure.ack-mode: auto\n",
			want:     "auto", wantOK: true,
		},
		{
			name:     "user overrides defaults",
			defaults: "parameters:\n  closure.ack-mode: auto\n",
			user:     "parameters:\n  closure.ack-mode: always\n",
			want:     "always", wantOK: true,
		},
		{
			name:     "project overrides user",
			defaults: "parameters:\n  closure.ack-mode: auto\n",
			user:     "parameters:\n  closure.ack-mode: always\n",
			project:  "parameters:\n  closure.ack-mode: auto\n",
			want:     "auto", wantOK: true,
		},
		{
			name:     "silent level falls through",
			defaults: "parameters:\n  closure.ack-mode: always\n",
			user:     "parameters: {}\n",
			want:     "always", wantOK: true,
		},
		{
			name:     "unset everywhere",
			defaults: "parameters:\n  engagement.decay-turns: 8\n",
			wantOK:   false,
		},
		{
			name:     "non-string scalar renders as text",
			defaults: "parameters:\n  engagement.decay-turns: 8\n",
			want:     "8", wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			paths := PathsForHome(home)
			if tt.defaults != "" {
				writeDirective(t, filepath.Join(paths.DirectivesDir, DirectiveDefaultsFile), tt.defaults)
			}
			if tt.user != "" {
				writeDirective(t, filepath.Join(paths.DirectivesDir, DirectiveUserFile), tt.user)
			}
			if tt.project != "" {
				writeDirective(t, filepath.Join(paths.DirectivesDir, prj, DirectiveProjectFile), tt.project)
			}

			lookup := key
			if tt.name == "non-string scalar renders as text" {
				lookup = "engagement.decay-turns"
			}
			got, ok, err := ReadParameter(paths, prj, lookup)
			if err != nil {
				t.Fatalf("ReadParameter: %v", err)
			}
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("ReadParameter = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestReadParameter_MissingChainIsNotAnError: a home with no directives
// directory at all resolves to "unset" rather than failing — every level
// of the chain is optional.
func TestReadParameter_MissingChainIsNotAnError(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	got, ok, err := ReadParameter(paths, "prj_1", "closure.ack-mode")
	if err != nil {
		t.Fatalf("ReadParameter: %v", err)
	}
	if ok || got != "" {
		t.Errorf("ReadParameter = (%q, %v), want unset", got, ok)
	}
}

// TestReadParameter_MalformedYAMLIsAnError: a hand-edited file with a
// YAML error must be reported, not silently skipped — the user believes
// they set an override.
func TestReadParameter_MalformedYAMLIsAnError(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	writeDirective(t, filepath.Join(paths.DirectivesDir, DirectiveUserFile),
		"parameters:\n  closure.ack-mode: [unclosed\n")
	if _, _, err := ReadParameter(paths, "", "closure.ack-mode"); err == nil {
		t.Error("ReadParameter accepted malformed YAML frontmatter")
	}
}

// TestReadParameter_CompositeValueRefused: the one nested-map parameter
// in the namespace is refused rather than stringified into something no
// caller can parse.
func TestReadParameter_CompositeValueRefused(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	writeDirective(t, filepath.Join(paths.DirectivesDir, DirectiveDefaultsFile),
		"parameters:\n  layer.budget.percentages:\n    E: 8\n    B: 50\n")
	if _, _, err := ReadParameter(paths, "", "layer.budget.percentages"); err == nil {
		t.Error("ReadParameter accepted a composite value as a scalar")
	}
}

// TestReadParameter_SeededDefaultsCarryAckMode: the install-shipped
// defaults.md is a real input to this reader — a rename or reformat there
// must not silently strand the directive.
func TestReadParameter_SeededDefaultsCarryAckMode(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := os.MkdirAll(paths.DirectivesDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(paths.DirectivesDir, DirectiveDefaultsFile)
	if err := os.WriteFile(path, []byte(seedDefaultsMD), 0o644); err != nil {
		t.Fatalf("write defaults.md: %v", err)
	}
	got, ok, err := ReadParameter(paths, "", "closure.ack-mode")
	if err != nil || !ok {
		t.Fatalf("ReadParameter = (%q, %v, %v), want the seeded value", got, ok, err)
	}
	if got != "auto" {
		t.Errorf("seeded closure.ack-mode = %q, want auto", got)
	}
}
