package gate

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/gjed/okf-operator/internal/plan"
)

// statusRemoved is the files API status of a deleted file, which has no head content.
const statusRemoved = "removed"

// Deps are the inputs Evaluate reads.
type Deps struct {
	Reader  Reader
	Commits CommitChecker
	// Review is the optional LLM review of the same PR head. It is appended
	// after every deterministic rule and can only block.
	Review *Review
}

// Review is an LLM review verdict as gate takes it, with the inputs it was
// made for.
type Review struct {
	KnowledgeRepo string
	PR            int
	HeadSHA       string
	BaseSHA       string
	PlanDigest    string
	Pass          bool
	Details       []string
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

	digest, err := plan.Digest(p)
	if err != nil {
		return nil, err
	}
	r := &Report{Version: Version, KnowledgeRepo: repo, PR: pr.Number, HeadSHA: pr.HeadSHA, BaseSHA: pr.BaseSHA, PlanDigest: digest, Rules: []Result{}}
	res, err := provenanceRule(ctx, d.Commits, p.Source, p.To, pages)
	if err != nil {
		return nil, err
	}
	r.Add(res)
	r.Add(routedSet(pr.Files, p))
	if res, err = allowlist(ctx, d.Reader, repo, pr.BaseSHA, p.Bundle, pol, pages); err != nil {
		return nil, err
	}
	r.Add(res)
	r.Add(changeShape(pr.Files))
	r.Add(size(pr.Files, pol.Limits))
	res, err = links(ctx, d.Reader, repo, pr.HeadSHA, p.Bundle, pages)
	if err != nil {
		return nil, err
	}
	r.Add(res)
	r.Add(mergeable(pr))
	if rv := d.Review; rv != nil {
		if err := matchReview(rv, r); err != nil {
			return nil, err
		}
		r.Add(Result{Name: RuleReview, Pass: rv.Pass, Details: slices.Clone(rv.Details)})
	}
	record(ctx, repo, p, r)
	return r, nil
}

// matchReview fails unless the review was made for exactly what r evaluates:
// the same repo, PR, head, base and plan.
func matchReview(rv *Review, r *Report) error {
	var diffs []string
	for _, f := range []struct{ name, got, want string }{
		{"knowledge_repo", rv.KnowledgeRepo, r.KnowledgeRepo},
		{"pr", strconv.Itoa(rv.PR), strconv.Itoa(r.PR)},
		{"head_sha", rv.HeadSHA, r.HeadSHA},
		{"base_sha", rv.BaseSHA, r.BaseSHA},
		{"plan_digest", rv.PlanDigest, r.PlanDigest},
	} {
		if f.got != f.want {
			diffs = append(diffs, fmt.Sprintf("%s %q, want %q", f.name, f.got, f.want))
		}
	}
	if len(diffs) > 0 {
		return fmt.Errorf("review result is for other inputs than this gate run: %s", strings.Join(diffs, "; "))
	}
	return nil
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
