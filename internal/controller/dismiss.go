package controller

import (
	"context"
	"fmt"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
)

// dismissStale dismisses every APPROVED review that login posted on commit
// head of PR n, and returns how many it dismissed. clients must act as the
// review App, which owns those reviews.
//
// The alternative, publishing the gate result as a required check run, was
// rejected: it needs checks: write on the writer App and a check that branch
// protection must require, next to the approval the review App already posts.
func dismissStale(ctx context.Context, clients github.Clients, repo string, n int, head, login string) (int, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := clients.ClientFor(ctx, owner)
	if err != nil {
		return 0, err
	}
	var ids []int64
	opts := &gogithub.ListOptions{PerPage: 100}
	for {
		reviews, resp, err := c.PullRequests.ListReviews(ctx, owner, name, n, opts)
		if err != nil {
			return 0, fmt.Errorf("list reviews of %s#%d: %w", repo, n, err)
		}
		for _, rv := range reviews {
			if rv.GetState() == "APPROVED" && rv.GetCommitID() == head && strings.EqualFold(rv.GetUser().GetLogin(), login) {
				ids = append(ids, rv.GetID())
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	msg := "okf-operator: a later gate run failed at " + head + "."
	for i, id := range ids {
		if _, _, err := c.PullRequests.DismissReview(ctx, owner, name, n, id, gogithub.PullRequestDismissReviewRequest{Message: msg}); err != nil {
			return i, fmt.Errorf("dismiss review %d of %s#%d: %w", id, repo, n, err)
		}
	}
	return len(ids), nil
}
