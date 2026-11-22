package review

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/gjed/okf-operator/prompts"
)

var (
	systemPrompt   = prompts.ReviewSystem
	reviewTemplate = template.Must(template.New("review").Option("missingkey=error").Parse(prompts.Review))
)

// promptData fills prompts/review.md.
type promptData struct {
	Path   string
	New    bool
	Before string
	After  string
	Hunks  []hunk
}

type hunk struct {
	Path, Patch string
}

// render fills the review template. Every value is untrusted and neutralised
// first, so none can close its block. Paths are quoted, so a quote or a
// newline in a file name cannot leave its header or line.
func render(d promptData) (string, error) {
	name := d.Path
	d.Path, d.Before, d.After = strconv.Quote(prompts.Neutralise(d.Path)), prompts.Neutralise(d.Before), prompts.Neutralise(d.After)
	hunks := make([]hunk, len(d.Hunks))
	for i, h := range d.Hunks {
		hunks[i] = hunk{Path: strconv.Quote(prompts.Neutralise(h.Path)), Patch: prompts.Neutralise(h.Patch)}
	}
	d.Hunks = hunks
	var b strings.Builder
	if err := reviewTemplate.Execute(&b, d); err != nil {
		return "", fmt.Errorf("render review prompt for %s: %w", name, err)
	}
	return b.String(), nil
}
