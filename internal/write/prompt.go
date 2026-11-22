package write

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/prompts"
)

var (
	systemPrompt       = prompts.System
	pageTemplate       = template.Must(template.New("page").Option("missingkey=error").Parse(prompts.Page))
	createSystemPrompt = prompts.CreateSystem
	createTemplate     = template.Must(template.New("create").Option("missingkey=error").Parse(prompts.Create))
)

// promptData fills prompts/page.md, or prompts/create.md when Create is set.
type promptData struct {
	Create          bool
	Profile         string
	Type            string
	Template        string
	References      []reference
	Path            string
	Page            string
	PullRequests    []plan.PullRequest
	PROmitted       int
	Hunks           []hunk
	Files           []sourceFile
	ValidationError string
}

type reference struct {
	Path, Content string
}

type hunk struct {
	Path, Patch string
}

// sourceFile is the whole content of one source file at the plan's to.
type sourceFile struct {
	Path, Content string
}

// render fills the page template. Source data is neutralised first, so it can
// never contain the markers that close the untrusted block.
func render(d promptData) (string, error) {
	prs := make([]plan.PullRequest, len(d.PullRequests))
	for i, pr := range d.PullRequests {
		pr.Title, pr.Body = neutralise(pr.Title), neutralise(pr.Body)
		prs[i] = pr
	}
	hunks := make([]hunk, len(d.Hunks))
	for i, h := range d.Hunks {
		hunks[i] = hunk{Path: neutralise(h.Path), Patch: neutralise(h.Patch)}
	}
	files := make([]sourceFile, len(d.Files))
	for i, f := range d.Files {
		files[i] = sourceFile{Path: neutralise(f.Path), Content: neutralise(f.Content)}
	}
	d.PullRequests, d.Hunks, d.Files = prs, hunks, files
	d.ValidationError = neutralise(d.ValidationError)

	tmpl := pageTemplate
	if d.Create {
		tmpl = createTemplate
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, d); err != nil {
		return "", fmt.Errorf("render prompt for %s: %w", d.Path, err)
	}
	return b.String(), nil
}

// neutralise breaks every "<<<" so untrusted text cannot open or close the
// untrusted block.
func neutralise(s string) string { return prompts.Neutralise(s) }
