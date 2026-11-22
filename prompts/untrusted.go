package prompts

import "strings"

// Neutralise breaks every "<<<" so untrusted text cannot open or close a
// prompt block. One pass leaves "<<<" in a run of five or more "<", so it
// repeats until none is left.
func Neutralise(s string) string {
	for strings.Contains(s, "<<<") {
		s = strings.ReplaceAll(s, "<<<", "< < <")
	}
	return s
}
