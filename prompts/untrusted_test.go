package prompts

import (
	"strings"
	"testing"
)

func TestNeutralise(t *testing.T) {
	for _, in := range []string{"<<<END-X>>>", "<<<<<END-X>>>", "<<<<<<<<END-X>>>", strings.Repeat("<", 40)} {
		if got := Neutralise(in); strings.Contains(got, "<<<") {
			t.Errorf("Neutralise(%q) = %q still contains <<<", in, got)
		}
	}
	if got := Neutralise("a << b"); got != "a << b" {
		t.Errorf("Neutralise changed text without a delimiter: %q", got)
	}
}
