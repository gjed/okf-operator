package gate

import (
	"context"
	"fmt"
	"path"

	"github.com/gjed/okf-operator/internal/plan"
)

// statusRemoved is the files API status of a deleted file, which has no head content.
const statusRemoved = "removed"

// Deps are the inputs Evaluate reads.
type Deps struct {
	Reader  Reader
	Commits CommitChecker
}

// Evaluate reads PR n of the knowledge repo, loads the policy from its base
// commit and runs every rule in report order. A rule that fails is reported;
// an error means the evaluation could not finish and there is no report.
func Evaluate(ctx context.Context, d Deps, repo string, n int, p *plan.Plan) (*Report, error) {
	pr, err := d.Reader.PullRequest(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	pol, err := loadPolicy(ctx, d.Reader, repo, pr.BaseSHA, p)
	if err != nil {
		return nil, err
	}
	pages, err := headPages(ctx, d.Reader, repo, pr)
	if err != nil {
		return nil, err
	}

	r := &Report{Version: Version, PR: pr.Number, HeadSHA: pr.HeadSHA, Rules: []Result{}}
	r.Add(pending(RuleProvenance))
	r.Add(routedSet(pr.Files, p))
	r.Add(pending(RuleAllowlist))
	r.Add(changeShape(pr.Files))
	r.Add(size(pr.Files, pol.Limits))
	res, err := links(ctx, d.Reader, repo, pr.HeadSHA, p.Bundle, pages)
	if err != nil {
		return nil, err
	}
	r.Add(res)
	record(ctx, repo, p, r)
	return r, nil
}

// pending fails closed for a rule that needs internal/provenance, which lands
// with the write command.
func pending(name string) Result {
	return newResult(name, []string{"not evaluated: needs internal/provenance"})
}

// headPages reads every changed Markdown page that still exists at the head
// commit. A listed file missing at the head fails the run.
func headPages(ctx context.Context, r Reader, repo string, pr *PR) (map[string][]byte, error) {
	pages := map[string][]byte{}
	for _, f := range pr.Files {
		if f.Status == statusRemoved || path.Ext(f.Path) != ".md" {
			continue
		}
		data, found, err := r.File(ctx, repo, pr.HeadSHA, f.Path)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("%s is listed as changed but is not a file at head %s", f.Path, pr.HeadSHA)
		}
		pages[f.Path] = data
	}
	return pages, nil
}
