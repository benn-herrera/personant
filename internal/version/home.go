package version

import (
	"fmt"
	"strconv"
)

// Rendering shared by every surface that prints version identity:
// `personant version`, the REPL's /version, and Long() itself. One
// alignment width and one home-format rendering, so the surfaces cannot
// drift apart.

// rowLabelWidth is the aligned label column shared by all rendered rows.
const rowLabelWidth = 12

// Row renders one aligned "label: value" line, newline-terminated. The
// caller passes the bare label; Row appends the colon and pads.
func Row(label, value string) string {
	return fmt.Sprintf("%-*s %s\n", rowLabelWidth, label+":", value)
}

// HomeFormatLabel renders a home's on-disk format stamp for display,
// given exactly what MemoryOps.HomeFormat reports.
//
// All three states are legitimate output rather than failures: the
// version surfaces are the diagnostic you run precisely WHEN the home is
// missing, unreadable, or newer than this binary, so a fault is rendered
// as text and the command still succeeds.
func HomeFormatLabel(found bool, format int, err error) string {
	switch {
	case err != nil:
		return "unreadable: " + err.Error()
	case !found:
		return "absent (uninitialized home, or one predating versioning)"
	default:
		return strconv.Itoa(format)
	}
}
