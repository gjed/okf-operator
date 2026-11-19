package gate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github"
)

// PR is a pull request as `gate` sees it: the head and base commits recorded
// once, and every changed file.
type PR struct {
	Number  int
	HeadSHA string
	// BaseSHA is the base branch tip, resolved once when the PR is read.
	BaseSHA string
	Files   []File
}

// File is one changed file, as the pull request files API reports it.
type File struct {
	Path         string
	Status       string
	PreviousPath string
	Additions    int
	Deletions    int
}

// Reader reads a knowledge repo through the API.
type Reader interface {
	// PullRequest returns PR n of repo with its base branch resolved to a
	// commit and its complete file list.
	PullRequest(ctx context.Context, repo string, n int) (*PR, error)
	// File returns the content of a file at ref. found is false when the
	// path is not a file at ref.
	File(ctx context.Context, repo, ref, path string) (content []byte, found bool, err error)
}

// CommitChecker reports whether a commit exists in a source repo.
type CommitChecker interface {
	CommitExists(ctx context.Context, repo, sha string) (bool, error)
}

// filesPerPage is the largest page the pull request files API serves.
const filesPerPage = 100

// GitHubReader implements Reader with the GitHub REST API.
type GitHubReader struct {
	Clients github.Clients
}

// PullRequest reads the PR, resolves its base branch tip and lists its files.
// A file list shorter than the PR's changed_files count (the API stops at
// 3000 files) fails closed, and so does a head that moves during the listing.
func (g GitHubReader) PullRequest(ctx context.Context, repo string, n int) (*PR, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return nil, err
	}
	pr, _, err := c.PullRequests.Get(ctx, owner, name, n)
	if err != nil {
		return nil, fmt.Errorf("get %s#%d: %w", repo, n, err)
	}
	out := &PR{Number: n, HeadSHA: pr.GetHead().GetSHA(), Files: []File{}}
	base := pr.GetBase().GetRef()
	if out.BaseSHA, _, err = c.Repositories.GetCommitSHA1(ctx, owner, name, base, ""); err != nil {
		return nil, fmt.Errorf("resolve %s@%s: %w", repo, base, err)
	}

	opts := &gogithub.ListOptions{PerPage: filesPerPage}
	for {
		page, resp, err := c.PullRequests.ListFiles(ctx, owner, name, n, opts)
		if err != nil {
			return nil, fmt.Errorf("list files of %s#%d: %w", repo, n, err)
		}
		for _, f := range page {
			out.Files = append(out.Files, File{
				Path:         f.GetFilename(),
				Status:       f.GetStatus(),
				PreviousPath: f.GetPreviousFilename(),
				Additions:    f.GetAdditions(),
				Deletions:    f.GetDeletions(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	if len(out.Files) != pr.GetChangedFiles() {
		return nil, fmt.Errorf("%s#%d changes %d files but the API listed %d: the file list is incomplete",
			repo, n, pr.GetChangedFiles(), len(out.Files))
	}
	// The files API has no ref parameter, so re-read the head: a push during
	// the listing would mix two heads in one report.
	again, _, err := c.PullRequests.Get(ctx, owner, name, n)
	if err != nil {
		return nil, fmt.Errorf("get %s#%d: %w", repo, n, err)
	}
	if again.GetHead().GetSHA() != out.HeadSHA {
		return nil, fmt.Errorf("%s#%d head moved from %s to %s while gate read it", repo, n, out.HeadSHA, again.GetHead().GetSHA())
	}
	return out, nil
}

// File reads path at ref with the contents API. A 404 or a directory is not found.
func (g GitHubReader) File(ctx context.Context, repo, ref, path string) ([]byte, bool, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	fc, _, _, err := c.Repositories.GetContents(ctx, owner, name, path, &gogithub.RepositoryContentGetOptions{Ref: ref})
	switch {
	case github.IsNotFound(err):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("read %s@%s:%s: %w", repo, ref, path, err)
	case fc == nil || fc.GetType() != "file":
		return nil, false, nil
	}
	s, err := fc.GetContent()
	if err != nil {
		return nil, false, fmt.Errorf("decode %s@%s:%s: %w", repo, ref, path, err)
	}
	return []byte(s), true, nil
}

// GitHubCommits implements CommitChecker with the GitHub REST API.
type GitHubCommits struct {
	Clients github.Clients
}

// CommitExists looks sha up in repo. GitHub answers 404 or 422 ("No commit
// found for SHA") for an unknown commit; both mean it does not exist.
func (g GitHubCommits) CommitExists(ctx context.Context, repo, sha string) (bool, error) {
	owner, name, _ := strings.Cut(repo, "/")
	c, err := g.Clients.ClientFor(ctx, owner)
	if err != nil {
		return false, err
	}
	_, _, err = c.Repositories.GetCommitSHA1(ctx, owner, name, sha, "")
	switch {
	case err == nil:
		return true, nil
	case github.IsNotFound(err), isNoCommit(err):
		return false, nil
	default:
		return false, fmt.Errorf("look up %s@%s: %w", repo, sha, err)
	}
}

func isNoCommit(err error) bool {
	var e *gogithub.ErrorResponse
	return errors.As(err, &e) && e.Response != nil &&
		e.Response.StatusCode == http.StatusUnprocessableEntity && strings.HasPrefix(e.Message, "No commit found")
}
