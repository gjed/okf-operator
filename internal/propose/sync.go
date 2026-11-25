package propose

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
)

// Merge states of an open bot PR that call for a repair. Every other state,
// and an unresolved "unknown", leaves the PR as it is.
const (
	MergeBehind = "behind"
	MergeDirty  = "dirty"
)

// Repair kinds, the values of the okf.propose.repair attribute.
const (
	RepairUpdateBranch = "update_branch"
	RepairRegenerate   = "regenerate"
)

// ErrUpdateRefused is the error of an update-branch call GitHub answered
// with 422: the head is not the expected one, or the branch no longer merges
// cleanly. Nothing was written.
var ErrUpdateRefused = errors.New("update-branch refused")

// MergeState reads the mergeable_state of PR number, reading again while
// GitHub reports "unknown" within g.Poll's bounds. The state may still be
// "unknown" when the bounds run out.
func (g GitHubKnowledge) MergeState(ctx context.Context, number int) (string, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return "", err
	}
	pr, err := github.PullMergeState(ctx, c, owner, name, number, g.Poll)
	if err != nil {
		return "", err
	}
	return pr.GetMergeableState(), nil
}

// UpdateBranch merges the base branch into the head branch of PR number with
// GitHub's update-branch API, which only acts when the head is still
// expectedHead. GitHub moves the branch in the background, so it waits within
// g.Poll's bounds for the head to change and returns the new head.
func (g GitHubKnowledge) UpdateBranch(ctx context.Context, number int, expectedHead string) (string, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return "", err
	}
	_, _, err = c.PullRequests.UpdateBranch(ctx, owner, name, number, gogithub.UpdatePullRequestBranchRequest{ExpectedHeadSHA: &expectedHead})
	var accepted *gogithub.AcceptedError
	switch {
	case err == nil, errors.As(err, &accepted):
	case status(err) == http.StatusUnprocessableEntity:
		return "", fmt.Errorf("%w: update PR #%d in %s at %s: %w", ErrUpdateRefused, number, g.Repo, expectedHead, err)
	default:
		return "", fmt.Errorf("update branch of PR #%d in %s: %w", number, g.Repo, err)
	}
	var head string
	moved, err := g.Poll.Until(ctx, func(ctx context.Context) (bool, error) {
		pr, _, err := c.PullRequests.Get(ctx, owner, name, number)
		if err != nil {
			return false, fmt.Errorf("get %s#%d: %w", g.Repo, number, err)
		}
		head = pr.GetHead().GetSHA()
		return head != expectedHead, nil
	})
	if err != nil {
		return "", err
	}
	if !moved {
		return "", fmt.Errorf("PR #%d in %s: the branch did not move off %s after update-branch", number, g.Repo, expectedHead)
	}
	return head, nil
}

// MergeBase returns the best common ancestor of commits a and b.
func (g GitHubKnowledge) MergeBase(ctx context.Context, a, b string) (string, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return "", err
	}
	cmp, _, err := c.Repositories.CompareCommits(ctx, owner, name, a, b, &gogithub.ListOptions{PerPage: 1})
	if err != nil {
		return "", fmt.Errorf("compare %s...%s in %s: %w", a, b, g.Repo, err)
	}
	sha := cmp.GetMergeBaseCommit().GetSHA()
	if sha == "" {
		return "", fmt.Errorf("%s and %s in %s have no merge base", a, b, g.Repo)
	}
	return sha, nil
}

// PullSyncer is what SyncPull needs of the knowledge repo.
type PullSyncer interface {
	MergeState(ctx context.Context, number int) (string, error)
	UpdateBranch(ctx context.Context, number int, expectedHead string) (string, error)
}

// Synced is the state of an open bot PR after SyncPull.
type Synced struct {
	// State is the mergeable_state SyncPull read, "unknown" included.
	State string
	// Head is the PR head to continue on: the new head after an update-branch,
	// else the head SyncPull was given.
	Head string
	// Dirty is true when the branch conflicts with the default branch and has
	// to be repaired with Repair before the run can build on it.
	Dirty bool
}

// SyncPull reads the merge state of pull. A PR that is behind is updated with
// update-branch, with pull.HeadSHA as the expected head, and no model is
// involved. A conflicting PR is reported as Dirty for Repair. Any other state,
// "unknown" included, leaves the PR as it is. A failed update-branch fails
// SyncPull before anything else is written.
func SyncPull(ctx context.Context, k PullSyncer, source string, pull *Pull) (Synced, error) {
	state, err := k.MergeState(ctx, pull.Number)
	if err != nil {
		return Synced{}, err
	}
	out := Synced{State: state, Head: pull.HeadSHA}
	switch state {
	case MergeBehind:
		if out.Head, err = k.UpdateBranch(ctx, pull.Number, pull.HeadSHA); err != nil {
			return Synced{}, err
		}
		repaired(ctx, source, RepairUpdateBranch)
	case MergeDirty:
		out.Dirty = true
	}
	return out, nil
}
