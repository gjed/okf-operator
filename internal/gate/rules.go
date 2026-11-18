package gate

import (
	"fmt"
	"strings"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/policy"
)

// kbDir holds knowledge repo configuration. A PR may never touch it.
const kbDir = ".kb/"

// statusModified is the only file status the change-shape rule accepts.
const statusModified = "modified"

// routedSet passes when every changed file is one of the plan's pages.
func routedSet(files []File, p *plan.Plan) Result {
	planned := map[string]bool{}
	for _, pg := range p.Pages {
		planned[pg.Path] = true
	}
	var details []string
	for _, f := range files {
		if !planned[f.Path] {
			details = append(details, fmt.Sprintf("%s: not one of the plan's pages", f.Path))
		}
		if f.PreviousPath != "" && !planned[f.PreviousPath] {
			details = append(details, fmt.Sprintf("%s: previous path is not one of the plan's pages", f.PreviousPath))
		}
	}
	return newResult(RuleRoutedSet, details)
}

// changeShape passes when every changed file is modified in place and none is
// under .kb/.
func changeShape(files []File) Result {
	var details []string
	for _, f := range files {
		switch {
		case f.Status != statusModified && f.PreviousPath != "":
			details = append(details, fmt.Sprintf("%s: status %s from %s, only modified files are allowed", f.Path, f.Status, f.PreviousPath))
		case f.Status != statusModified:
			details = append(details, fmt.Sprintf("%s: status %s, only modified files are allowed", f.Path, f.Status))
		}
		for _, p := range []string{f.Path, f.PreviousPath} {
			if strings.HasPrefix(p, kbDir) {
				details = append(details, fmt.Sprintf("%s: changes knowledge repo configuration under %s", p, kbDir))
			}
		}
	}
	return newResult(RuleChangeShape, details)
}

// size passes when no file and not the whole PR exceed the policy's changed
// line limits. A changed line counts as added plus removed, as the files API
// reports it.
func size(files []File, l policy.Limits) Result {
	var details []string
	total := 0
	for _, f := range files {
		n := f.Additions + f.Deletions
		total += n
		if n > l.PageLines {
			details = append(details, fmt.Sprintf("%s: %d changed lines exceed limits.page_lines %d", f.Path, n, l.PageLines))
		}
	}
	if total > l.PRLines {
		details = append(details, fmt.Sprintf("pull request: %d changed lines exceed limits.pr_lines %d", total, l.PRLines))
	}
	return newResult(RuleSize, details)
}
