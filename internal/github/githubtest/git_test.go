package githubtest

import (
	"context"
	"net/http"
	"testing"

	gogithub "github.com/google/go-github/v93/github"
)

func status(err error) int {
	if e, ok := err.(*gogithub.ErrorResponse); ok {
		return e.Response.StatusCode
	}
	return 0
}

func TestGitDataRoundTrip(t *testing.T) {
	f := New(t)
	f.SetFile("acme/kb", "main", "docs/a.md", "a\n")
	c := client(t, f)
	ctx := context.Background()
	base := f.Ref("acme/kb", "main")

	bc, _, err := c.Git.GetCommit(ctx, "acme", "kb", base)
	if err != nil {
		t.Fatal(err)
	}
	tree, _, err := c.Git.GetTree(ctx, "acme", "kb", bc.GetTree().GetSHA(), true)
	if err != nil {
		t.Fatal(err)
	}
	var blob string
	for _, e := range tree.Entries {
		if e.GetPath() == "docs/a.md" {
			blob = e.GetSHA()
		}
	}
	if blob != BlobSHA([]byte("a\n")) {
		t.Fatalf("blob sha %q is not the git blob ID", blob)
	}

	b, _, err := c.Git.CreateBlob(ctx, "acme", "kb", gogithub.Blob{Content: "Ygo=", Encoding: new("base64")})
	if err != nil {
		t.Fatal(err)
	}
	nt, _, err := c.Git.CreateTree(ctx, "acme", "kb", bc.GetTree().GetSHA(), []*gogithub.TreeEntry{
		{Path: new("docs/b.md"), Mode: new("100644"), Type: new("blob"), SHA: b.SHA},
	})
	if err != nil {
		t.Fatal(err)
	}
	nc, _, err := c.Git.CreateCommit(ctx, "acme", "kb", gogithub.Commit{Message: new("m"), Tree: nt, Parents: []*gogithub.Commit{{SHA: new(base)}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Git.CreateRef(ctx, "acme", "kb", gogithub.CreateRef{Ref: "refs/heads/main", SHA: nc.GetSHA()}); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("create existing ref: %v", err)
	}
	if _, _, err := c.Git.UpdateRef(ctx, "acme", "kb", "heads/main", gogithub.UpdateRef{SHA: nc.GetSHA()}); err != nil {
		t.Fatal(err)
	}
	if got := f.Files("acme/kb", "main"); got["docs/a.md"] != "a\n" || got["docs/b.md"] != "b\n" {
		t.Fatalf("files = %v", got)
	}
	// Moving back to the parent is not a fast forward.
	if _, _, err := c.Git.UpdateRef(ctx, "acme", "kb", "heads/main", gogithub.UpdateRef{SHA: base}); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("non fast-forward update: %v", err)
	}
	if _, _, err := c.Git.UpdateRef(ctx, "acme", "kb", "heads/main", gogithub.UpdateRef{SHA: base, Force: new(true)}); err != nil {
		t.Fatal(err)
	}
}

func TestContentsCompareAndSwap(t *testing.T) {
	f := New(t)
	f.SetFile("acme/kb", "kb-state", "cursors/acme/api", "old\n")
	c := client(t, f)
	ctx := context.Background()
	opts := func(sha *string) *gogithub.RepositoryContentFileOptions {
		return &gogithub.RepositoryContentFileOptions{Message: new("m"), Content: []byte("new\n"), SHA: sha, Branch: new("kb-state")}
	}
	if _, _, err := c.Repositories.UpdateFile(ctx, "acme", "kb", "cursors/acme/api", opts(nil)); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("missing sha: %v", err)
	}
	if _, _, err := c.Repositories.UpdateFile(ctx, "acme", "kb", "cursors/acme/api", opts(new("0000"))); status(err) != http.StatusConflict {
		t.Fatalf("stale sha: %v", err)
	}
	if _, _, err := c.Repositories.UpdateFile(ctx, "acme", "kb", "cursors/acme/api", opts(new(BlobSHA([]byte("old\n"))))); err != nil {
		t.Fatal(err)
	}
	if got := f.Files("acme/kb", "kb-state")["cursors/acme/api"]; got != "new\n" {
		t.Fatalf("cursor = %q", got)
	}
}

func TestPulls(t *testing.T) {
	f := New(t)
	f.SetFile("acme/kb", "okf/acme/api", "a.md", "a")
	c := client(t, f)
	ctx := context.Background()
	pr, _, err := c.PullRequests.Create(ctx, "acme", "kb", gogithub.CreatePullRequest{Title: new("t"), Head: "okf/acme/api", Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PullRequests.Create(ctx, "acme", "kb", gogithub.CreatePullRequest{Title: new("t"), Head: "okf/acme/api", Base: "main"}); status(err) != http.StatusUnprocessableEntity {
		t.Fatalf("second PR: %v", err)
	}
	open, _, err := c.PullRequests.List(ctx, "acme", "kb", &gogithub.PullRequestListOptions{State: "open", Head: "acme:okf/acme/api", Base: "main"})
	if err != nil || len(open) != 1 || open[0].GetNumber() != pr.GetNumber() || open[0].GetHead().GetSHA() != f.Ref("acme/kb", "okf/acme/api") {
		t.Fatalf("list = %v, %v", open, err)
	}
	if _, _, err := c.PullRequests.Edit(ctx, "acme", "kb", pr.GetNumber(), &gogithub.PullRequest{Body: new("b")}); err != nil {
		t.Fatal(err)
	}
	if got := f.PullRequests("acme/kb"); len(got) != 1 || got[0].Body != "b" {
		t.Fatalf("pulls = %+v", got)
	}
}
