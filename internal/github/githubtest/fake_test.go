package githubtest

import (
	"context"
	"fmt"
	"testing"

	gogithub "github.com/google/go-github/v93/github"
)

func client(t *testing.T, f *Fake) *gogithub.Client {
	t.Helper()
	u := f.APIURL()
	c, err := gogithub.NewClient(gogithub.WithURLs(&u, nil))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPullEndpoints(t *testing.T) {
	ctx := context.Background()
	f := New(t)
	files := make([]PullFile, 0, 5)
	for i := range 5 {
		files = append(files, PullFile{Filename: fmt.Sprintf("p/%d.md", i), Status: "modified", Additions: i, Deletions: 1})
	}
	f.SetPull("acme/kb", 7, Pull{Head: "abc", BaseRef: "main"})
	f.SetPullFiles("acme/kb", 7, files)
	c := client(t, f)

	pr, _, err := c.PullRequests.Get(ctx, "acme", "kb", 7)
	if err != nil {
		t.Fatal(err)
	}
	if pr.GetHead().GetSHA() != "abc" || pr.GetBase().GetRef() != "main" || pr.GetChangedFiles() != 5 || pr.GetNumber() != 7 {
		t.Errorf("pull = %+v", pr)
	}

	var got []*gogithub.CommitFile
	opts := &gogithub.ListOptions{PerPage: 2}
	for {
		page, resp, err := c.PullRequests.ListFiles(ctx, "acme", "kb", 7, opts)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	if len(got) != 5 || got[4].GetFilename() != "p/4.md" || got[4].GetAdditions() != 4 || got[4].GetDeletions() != 1 {
		t.Errorf("files = %d, last %+v", len(got), got[len(got)-1])
	}

	if _, _, err := c.PullRequests.Get(ctx, "acme", "kb", 8); err == nil {
		t.Error("unknown pull should 404")
	}
}

func TestSetCommit(t *testing.T) {
	ctx := context.Background()
	f := New(t)
	f.SetCommit("acme/api", "1234")
	c := client(t, f)
	sha, _, err := c.Repositories.GetCommitSHA1(ctx, "acme", "api", "1234", "")
	if err != nil || sha != "1234" {
		t.Fatalf("sha %q, err %v", sha, err)
	}
	if _, _, err := c.Repositories.GetCommitSHA1(ctx, "acme", "api", "5678", ""); err == nil {
		t.Error("undeclared commit should 404")
	}
}
