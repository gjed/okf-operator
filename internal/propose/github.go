package propose

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/plan"
)

// GitHubKnowledge implements Knowledge for Repo (owner/name) with the git
// data and pulls REST APIs.
type GitHubKnowledge struct {
	Clients github.Clients
	Repo    string
	// Poll bounds the reads that wait for GitHub to compute a PR's merge
	// state or to move a branch; the zero value is github.Poll's default.
	Poll github.Poll
}

func (g GitHubKnowledge) client(ctx context.Context) (*gogithub.Client, string, string, error) {
	owner, name, _ := strings.Cut(g.Repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	return c, owner, name, err
}

// DefaultBranch implements Knowledge.
func (g GitHubKnowledge) DefaultBranch(ctx context.Context) (string, string, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return "", "", err
	}
	repo, _, err := c.Repositories.Get(ctx, owner, name)
	if err != nil {
		return "", "", fmt.Errorf("read knowledge repo %s: %w", g.Repo, err)
	}
	branch := repo.GetDefaultBranch()
	ref, _, err := c.Git.GetRef(ctx, owner, name, "heads/"+branch)
	if err != nil {
		return "", "", fmt.Errorf("read %s@%s: %w", g.Repo, branch, err)
	}
	return branch, ref.GetObject().GetSHA(), nil
}

// OpenPull implements Knowledge.
func (g GitHubKnowledge) OpenPull(ctx context.Context, head, base string) (*Pull, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return nil, err
	}
	prs, _, err := c.PullRequests.List(ctx, owner, name, &gogithub.PullRequestListOptions{State: "open", Head: head, Base: base})
	if err != nil {
		return nil, fmt.Errorf("list open PRs from %s in %s: %w", head, g.Repo, err)
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return pull(prs[0]), nil
}

func pull(pr *gogithub.PullRequest) *Pull {
	return &Pull{Number: pr.GetNumber(), URL: pr.GetHTMLURL(), Body: pr.GetBody(), HeadSHA: pr.GetHead().GetSHA()}
}

// Tree implements Knowledge.
func (g GitHubKnowledge) Tree(ctx context.Context, commit string) (map[string]Entry, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return nil, err
	}
	cm, _, err := c.Git.GetCommit(ctx, owner, name, commit)
	if err != nil {
		return nil, fmt.Errorf("read knowledge commit %s: %w", commit, err)
	}
	tree, _, err := c.Git.GetTree(ctx, owner, name, cm.GetTree().GetSHA(), true)
	if err != nil {
		return nil, fmt.Errorf("read tree of knowledge commit %s: %w", commit, err)
	}
	if tree.GetTruncated() {
		return nil, fmt.Errorf("tree of knowledge commit %s is truncated: the repo is too large to verify", commit)
	}
	out := make(map[string]Entry, len(tree.Entries))
	for _, e := range tree.Entries {
		if e.GetType() == "tree" {
			continue
		}
		out[e.GetPath()] = Entry{Mode: e.GetMode(), SHA: e.GetSHA()}
	}
	return out, nil
}

// Commit implements Knowledge.
func (g GitHubKnowledge) Commit(ctx context.Context, base string, files map[string]File, message string) (string, error) {
	return g.CommitMerge(ctx, []string{base}, nil, files, message)
}

// CommitMerge creates a commit with the given parents whose tree is the tree
// of parents[0] with reuse and files applied, and returns its SHA. A reuse
// entry points a path at an existing blob (or submodule commit), a nil entry
// deletes the path; files hold new content and win over reuse.
func (g GitHubKnowledge) CommitMerge(ctx context.Context, parents []string, reuse map[string]*Entry, files map[string]File, message string) (string, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return "", err
	}
	base := parents[0]
	parent, _, err := c.Git.GetCommit(ctx, owner, name, base)
	if err != nil {
		return "", fmt.Errorf("read knowledge commit %s: %w", base, err)
	}
	var entries []*gogithub.TreeEntry
	for _, p := range slices.Sorted(maps.Keys(reuse)) {
		if _, ok := files[p]; ok {
			continue
		}
		e := reuse[p]
		if e == nil {
			entries = append(entries, &gogithub.TreeEntry{Path: new(p)})
			continue
		}
		typ := "blob"
		if e.Mode == modeSubmodule {
			typ = "commit"
		}
		entries = append(entries, &gogithub.TreeEntry{Path: new(p), Mode: new(e.Mode), Type: new(typ), SHA: new(e.SHA)})
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		f := files[p]
		blob, _, err := c.Git.CreateBlob(ctx, owner, name, gogithub.Blob{
			Content:  base64.StdEncoding.EncodeToString(f.Content),
			Encoding: new("base64"),
		})
		if err != nil {
			return "", fmt.Errorf("create blob for %s: %w", p, err)
		}
		mode := f.Mode
		if mode == "" {
			mode = modeFile
		}
		entries = append(entries, &gogithub.TreeEntry{Path: new(p), Mode: new(mode), Type: new("blob"), SHA: blob.SHA})
	}
	tree, _, err := c.Git.CreateTree(ctx, owner, name, parent.GetTree().GetSHA(), entries)
	if err != nil {
		return "", fmt.Errorf("create knowledge tree: %w", err)
	}
	commitParents := make([]*gogithub.Commit, 0, len(parents))
	for _, p := range parents {
		commitParents = append(commitParents, &gogithub.Commit{SHA: new(p)})
	}
	cm, _, err := c.Git.CreateCommit(ctx, owner, name, gogithub.Commit{
		Message: &message,
		Tree:    tree,
		Parents: commitParents,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("create knowledge commit: %w", err)
	}
	return cm.GetSHA(), nil
}

// MoveBranch implements Knowledge.
func (g GitHubKnowledge) MoveBranch(ctx context.Context, branch, sha string, fastForward bool) error {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return err
	}
	if fastForward {
		if _, _, err := c.Git.UpdateRef(ctx, owner, name, "heads/"+branch, gogithub.UpdateRef{SHA: sha, Force: new(false)}); err != nil {
			return fmt.Errorf("fast-forward %s in %s: %w", branch, g.Repo, err)
		}
		return nil
	}
	_, _, err = c.Git.CreateRef(ctx, owner, name, gogithub.CreateRef{Ref: "refs/heads/" + branch, SHA: sha})
	if refExists(err) {
		// A branch left by a merged or closed bot PR: reset it, unless an open
		// PR into any base still uses it.
		// SIMPLIFIED: a run that created the branch but has not opened its PR yet
		// is still overwritten; runs are serialised per knowledge repo, and the
		// cursor compare-and-swap fails the losing run.
		prs, _, lerr := c.PullRequests.List(ctx, owner, name, &gogithub.PullRequestListOptions{State: "open", Head: owner + ":" + branch})
		if lerr != nil {
			return fmt.Errorf("list open PRs from %s in %s: %w", branch, g.Repo, lerr)
		}
		if len(prs) > 0 {
			return fmt.Errorf("branch %s in %s backs open PR #%d: re-run plan", branch, g.Repo, prs[0].GetNumber())
		}
		_, _, err = c.Git.UpdateRef(ctx, owner, name, "heads/"+branch, gogithub.UpdateRef{SHA: sha, Force: new(true)})
	}
	if err != nil {
		return fmt.Errorf("push %s in %s: %w", branch, g.Repo, err)
	}
	return nil
}

// OpenPR implements Knowledge.
func (g GitHubKnowledge) OpenPR(ctx context.Context, title, head, base, body string) (*Pull, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return nil, err
	}
	pr, _, err := c.PullRequests.Create(ctx, owner, name, gogithub.CreatePullRequest{Title: &title, Head: head, Base: base, Body: &body})
	if err != nil {
		return nil, fmt.Errorf("open PR from %s in %s: %w", head, g.Repo, err)
	}
	return pull(pr), nil
}

// EditPR implements Knowledge.
func (g GitHubKnowledge) EditPR(ctx context.Context, number int, body string) (*Pull, error) {
	c, owner, name, err := g.client(ctx)
	if err != nil {
		return nil, err
	}
	pr, _, err := c.PullRequests.Edit(ctx, owner, name, number, &gogithub.PullRequest{Body: &body})
	if err != nil {
		return nil, fmt.Errorf("update PR #%d in %s: %w", number, g.Repo, err)
	}
	return pull(pr), nil
}

// refExists reports whether err is GitHub's 422 for creating a ref that exists.
func refExists(err error) bool {
	var e *gogithub.ErrorResponse
	return status(err) == http.StatusUnprocessableEntity && errors.As(err, &e) &&
		strings.Contains(e.Message, "Reference already exists")
}

func status(err error) int {
	var e *gogithub.ErrorResponse
	if errors.As(err, &e) && e.Response != nil {
		return e.Response.StatusCode
	}
	return 0
}

// GitHubSourcePRs implements SourcePRs with the compare and commit-pulls APIs.
type GitHubSourcePRs struct {
	Differ plan.GitHubDiffer
}

// PullRequests implements SourcePRs.
func (g GitHubSourcePRs) PullRequests(ctx context.Context, repo, from, to string) ([]int, int, int, error) {
	r, err := g.Differ.Range(ctx, repo, from, to, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	prs, err := g.Differ.PullRequests(ctx, repo, r.Commits)
	if err != nil {
		return nil, 0, 0, err
	}
	numbers := make([]int, 0, len(prs))
	for _, pr := range prs {
		numbers = append(numbers, pr.Number)
	}
	return numbers, r.TotalCommits, len(r.Commits), nil
}
