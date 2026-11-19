package gate

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
)

const (
	kbRepo     = "acme/kb"
	srcRepo    = "acme/api"
	prNumber   = 7
	baseSHA    = "1111111111111111111111111111111111111111"
	headSHA    = "2222222222222222222222222222222222222222"
	sourceSHA  = "3333333333333333333333333333333333333333"
	basePolicy = `version: 1
auto_merge:
  types: [Service]
  bundles: [product]
limits:
  page_lines: 100
  pr_lines: 150
`
	baseRouting = `version: 1
sources:
  - repo: acme/api
    bundle: product
    routes:
      - page: product/api.md
        paths: ["api/**"]
      - page: product/billing.md
        paths: ["billing/**"]
`
	apiPage = `---
type: Service
title: "[not a link](frontmatter.md)"
source_repo: acme/api
source_sha: 3333333333333333333333333333333333333333
source_paths: [api/x.go]
updated_at: 2026-11-18T10:00:00Z
generated_by: okf-operator/dev/m1
---
# API

Bills through [billing](billing.md#charges) and [itself](./api.md).
See [docs](https://example.com/x), [top](#api) and ` + "`[code](missing.md)`" + `.
`
)

// fixture is a knowledge repo acme/kb with PR 7 modifying product/api.md, as
// planned for source acme/api in bundle product.
type fixture struct {
	fake    *githubtest.Fake
	reader  GitHubReader
	commits GitHubCommits
	plan    *plan.Plan
	files   []githubtest.PullFile
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetRepo(kbRepo, "main", baseSHA)
	fake.SetFile(kbRepo, baseSHA, ".kb/policy.yaml", basePolicy)
	fake.SetFile(kbRepo, baseSHA, ".kb/routing.yaml", baseRouting)
	fake.SetFile(kbRepo, baseSHA, "product/api.md", apiPage)
	fake.SetFile(kbRepo, headSHA, ".kb/policy.yaml", basePolicy)
	fake.SetFile(kbRepo, headSHA, ".kb/routing.yaml", baseRouting)
	fake.SetFile(kbRepo, headSHA, "product/api.md", apiPage)
	fake.SetFile(kbRepo, headSHA, "product/billing.md", "# Billing\n")
	fake.SetCommit(srcRepo, sourceSHA)

	provider, err := github.NewProvider(github.Config{APIURL: fake.APIURL(), Token: "test-token"},
		github.ReadContents(kbRepo, srcRepo), &github.Secrets{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		fake:    fake,
		reader:  GitHubReader{Clients: provider},
		commits: GitHubCommits{Clients: provider},
		plan: &plan.Plan{
			Version: plan.Version, Source: srcRepo, Bundle: "product",
			Pages: []plan.Page{{Path: "product/api.md", State: plan.StateUpdate, SourcePaths: []string{"api/x.go"}}},
		},
		files: []githubtest.PullFile{{Filename: "product/api.md", Status: "modified", Additions: 3, Deletions: 2}},
	}
	return f
}

// pr publishes the fixture's files as PR 7 and reads it back through the API.
func (f *fixture) pr(t *testing.T) *PR {
	t.Helper()
	f.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "main"})
	f.fake.SetPullFiles(kbRepo, prNumber, f.files)
	pr, err := f.reader.PullRequest(context.Background(), kbRepo, prNumber)
	if err != nil {
		t.Fatal(err)
	}
	return pr
}
