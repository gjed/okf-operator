package controller

import (
	"context"
	"fmt"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
)

// Reasons a commit loses the review App's approvals, the sentence of the
// dismissal message that names the commit.
const (
	dismissGateFailed = "a later gate run failed at"
	dismissSuperseded = "the bot branch moved off"
)

// dismissStale dismisses every APPROVED review that login posted on commit
// head of PR n, and returns how many it dismissed. why is one of the dismiss*
// reasons. clients must act as the review App, which owns those reviews.
//
// The alternative, publishing the gate result as a required check run, was
// rejected: it needs checks: write on the writer App and a check that branch
// protection must require, next to the approval the review App already posts.
func dismissStale(ctx context.Context, clients github.Clients, repo string, n int, head, login, why string) (int, error) {
	return dismissApprovals(ctx, clients, repo, n, login, why, func(commit string) bool { return commit == head })
}

// dismissExcept dismisses every APPROVED review that login posted on a
// commit of PR n other than keep, the PR's current head. The review App only
// ever approves the head it evaluated, so an approval of any other commit
// describes content that is no longer the PR's.
func dismissExcept(ctx context.Context, clients github.Clients, repo string, n int, keep, login, why string) (int, error) {
	return dismissApprovals(ctx, clients, repo, n, login, why, func(commit string) bool { return commit != keep })
}

type approval struct {
	id     int64
	commit string
}

func dismissApprovals(ctx context.Context, clients github.Clients, repo string, n int, login, why string, match func(commit string) bool) (int, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := clients.ClientFor(ctx, owner)
	if err != nil {
		return 0, err
	}
	var found []approval
	opts := &gogithub.ListOptions{PerPage: 100}
	for {
		reviews, resp, err := c.PullRequests.ListReviews(ctx, owner, name, n, opts)
		if err != nil {
			return 0, fmt.Errorf("list reviews of %s#%d: %w", repo, n, err)
		}
		for _, rv := range reviews {
			if rv.GetState() == "APPROVED" && match(rv.GetCommitID()) && strings.EqualFold(rv.GetUser().GetLogin(), login) {
				found = append(found, approval{rv.GetID(), rv.GetCommitID()})
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	for i, a := range found {
		msg := "okf-operator: " + why + " " + a.commit + "."
		if _, _, err := c.PullRequests.DismissReview(ctx, owner, name, n, a.id, gogithub.PullRequestDismissReviewRequest{Message: msg}); err != nil {
			return i, fmt.Errorf("dismiss review %d of %s#%d: %w", a.id, repo, n, err)
		}
	}
	return len(found), nil
}
