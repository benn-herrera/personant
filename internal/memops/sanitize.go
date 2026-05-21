package memops

// SanitizeDetail strips newlines and tabs from a free-form detail string
// before it lands in a one-per-line event-log entry (spec §2.8). Log
// lines are one per event; embedded newlines would split them.
//
// Single byte-level pass: \n, \r, and \t become ' '. All other bytes
// pass through unchanged.
func SanitizeDetail(s string) string {
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\t' {
			r = append(r, ' ')
			continue
		}
		r = append(r, c)
	}
	return string(r)
}
