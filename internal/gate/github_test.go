package gate

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

func TestPullRequest(t *testing.T) {
	f := newFixture(t)
	f.files = nil
	for i := range 250 {
		f.files = append(f.files, githubtest.PullFile{Filename: fmt.Sprintf("product/%03d.md", i), Status: "modified", Additions: 1})
	}
	pr := f.pr(t)
	if pr.HeadSHA != headSHA || pr.BaseSHA != baseSHA || pr.Number != prNumber {
		t.Errorf("pr = %+v", pr)
	}
	if len(pr.Files) != 250 || pr.Files[249].Path != "product/249.md" || pr.Files[0].Additions != 1 {
		t.Errorf("listed %d files across pages", len(pr.Files))
	}
}

func TestPullRequestIncompleteFileList(t *testing.T) {
	f := newFixture(t)
	f.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "main", ChangedFiles: 3001})
	f.fake.SetPullFiles(kbRepo, prNumber, f.files)
	_, err := f.reader.PullRequest(context.Background(), kbRepo, prNumber)
	if err == nil || !strings.Contains(err.Error(), "changes 3001 files but the API listed 1") {
		t.Fatalf("err = %v", err)
	}
}

// moveHead is a transport that pushes a new head to the fake once the PR's
// file list has been served.
type moveHead struct {
	fake *githubtest.Fake
}

func (m moveHead) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil && strings.HasSuffix(r.URL.Path, "/pulls/7/files") {
		m.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: sourceSHA, BaseRef: "main"})
	}
	return resp, err
}

type staticClients struct{ c *gogithub.Client }

func (s staticClients) ClientFor(context.Context, string) (*gogithub.Client, error) { return s.c, nil }

func TestPullRequestHeadMoved(t *testing.T) {
	f := newFixture(t)
	f.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "main"})
	f.fake.SetPullFiles(kbRepo, prNumber, f.files)
	u := f.fake.APIURL()
	c, err := gogithub.NewClient(gogithub.WithHTTPClient(&http.Client{Transport: moveHead{f.fake}}), gogithub.WithURLs(&u, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = GitHubReader{Clients: staticClients{c}}.PullRequest(context.Background(), kbRepo, prNumber)
	if err == nil || !strings.Contains(err.Error(), "head moved from "+headSHA+" to "+sourceSHA) {
		t.Fatalf("err = %v", err)
	}
}

func TestPullRequestErrors(t *testing.T) {
	f := newFixture(t)
	if _, err := f.reader.PullRequest(context.Background(), kbRepo, 99); err == nil {
		t.Error("unknown PR must fail")
	}
	f.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "gone"})
	if _, err := f.reader.PullRequest(context.Background(), kbRepo, prNumber); err == nil || !strings.Contains(err.Error(), "resolve acme/kb@gone") {
		t.Errorf("unresolvable base: err = %v", err)
	}
}

func TestFile(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	data, found, err := f.reader.File(ctx, kbRepo, headSHA, "product/billing.md")
	if err != nil || !found || string(data) != "# Billing\n" {
		t.Fatalf("data %q found %v err %v", data, found, err)
	}
	if _, found, err := f.reader.File(ctx, kbRepo, headSHA, "product/none.md"); err != nil || found {
		t.Errorf("missing file: found %v err %v", found, err)
	}
	f.fake.FailStatus["/repos/acme/kb/contents/product/boom.md"] = http.StatusInternalServerError
	if _, _, err := f.reader.File(ctx, kbRepo, headSHA, "product/boom.md"); err == nil {
		t.Error("a 500 must fail, not read as missing")
	}
}

func TestCommitExists(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if ok, err := f.commits.CommitExists(ctx, srcRepo, sourceSHA); err != nil || !ok {
		t.Fatalf("known commit: ok %v err %v", ok, err)
	}
	if ok, err := f.commits.CommitExists(ctx, srcRepo, strings.Repeat("9", 40)); err != nil || ok {
		t.Errorf("unknown commit: ok %v err %v", ok, err)
	}
	f.fake.FailStatus["/repos/acme/api/commits/"+sourceSHA] = http.StatusInternalServerError
	if _, err := f.commits.CommitExists(ctx, srcRepo, sourceSHA); err == nil {
		t.Error("a 500 must fail the lookup")
	}
}

func TestIsNoCommit(t *testing.T) {
	resp := func(code int, msg string) error {
		return &gogithub.ErrorResponse{Response: &http.Response{StatusCode: code}, Message: msg}
	}
	if !isNoCommit(resp(http.StatusUnprocessableEntity, "No commit found for SHA: 1234")) {
		t.Error("GitHub's unknown-commit 422 must count as missing")
	}
	if isNoCommit(resp(http.StatusUnprocessableEntity, "Validation Failed")) {
		t.Error("another 422 must not count as missing")
	}
}
