package prompts

import "strings"

// Neutralise breaks every "<<<" so untrusted text cannot open or close a
// prompt block.
func Neutralise(s string) string {
	return strings.ReplaceAll(s, "<<<", "< < <")
}
