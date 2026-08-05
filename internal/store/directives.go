package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Directive file names within DirectivesDir (§2.6). The project-scoped
// file lives one level down, under the project id.
const (
	DirectiveDefaultsFile = "defaults.md"
	DirectiveUserFile     = "user.md"
	DirectiveProjectFile  = "project.md"
)

// directiveFrontmatter is the parameter-carrying half of a §2.6 directive
// file's YAML frontmatter. Only `parameters` is read here — scope,
// project, and the modification bookkeeping are prose-level metadata this
// reader has no use for, and decoding into a struct with just this field
// makes the rest tolerated rather than validated.
//
// A parameter value is decoded as `any` because the namespace is mixed:
// integers, floats, duration strings, and one nested map
// (layer.budget.percentages). ReadParameter returns the scalar forms as
// text and refuses the composite ones, which is all any caller has needed.
type directiveFrontmatter struct {
	Parameters map[string]any `yaml:"parameters"`
}

// ReadParameter resolves one §2.6.1 directive parameter by walking the
// precedence chain highest-first: project (directives/<prj>/project.md) →
// user (directives/user.md) → system defaults (directives/defaults.md).
// The first file that sets key wins; ok is false when none does, and the
// caller applies its own compiled-in default.
//
// A missing file is not an error — every level of the chain is optional,
// and a home that has never accrued a user override legitimately has only
// defaults.md. An unreadable or malformed file IS an error: silently
// falling through a hand-edited file with a YAML typo would apply a
// default the user believes they overrode.
//
// Only scalar values resolve. A composite value (the one nested map in the
// namespace) returns an error rather than a stringified Go map, because no
// caller can do anything useful with the latter.
//
// projectID may be empty (no active project), which skips the project
// level of the chain.
func ReadParameter(paths PersonantPaths, projectID, key string) (value string, ok bool, err error) {
	chain := make([]string, 0, 3)
	if projectID != "" {
		chain = append(chain, filepath.Join(paths.DirectivesDir, projectID, DirectiveProjectFile))
	}
	chain = append(chain,
		filepath.Join(paths.DirectivesDir, DirectiveUserFile),
		filepath.Join(paths.DirectivesDir, DirectiveDefaultsFile),
	)

	for _, path := range chain {
		params, err := readDirectiveParameters(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", false, err
		}
		raw, present := params[key]
		if !present {
			continue
		}
		switch v := raw.(type) {
		case nil:
			// An explicitly null value means "unset at this level".
			continue
		case string:
			return v, true, nil
		case bool, int, int64, float64:
			return fmt.Sprint(v), true, nil
		default:
			return "", false, fmt.Errorf("directives: %s: parameter %q is not a scalar", path, key)
		}
	}
	return "", false, nil
}

// readDirectiveParameters parses the `parameters` map out of one
// directive file's frontmatter. A file with no frontmatter, or with no
// parameters block, yields an empty map (not an error): the body-only
// form is legal markdown for a directive file.
func readDirectiveParameters(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fmBytes, _, err := SplitFrontmatter(data)
	if err != nil {
		// No well-formed frontmatter — prose-only directive file.
		return nil, nil
	}
	var parsed directiveFrontmatter
	if err := yaml.Unmarshal(fmBytes, &parsed); err != nil {
		return nil, fmt.Errorf("directives: %s: parse yaml: %w", path, err)
	}
	return parsed.Parameters, nil
}
