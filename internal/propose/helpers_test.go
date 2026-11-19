package propose

import (
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"testing"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/write"
)

const (
	testSource = "acme/api"
	testKB     = "acme/kb"
	testBundle = "docs"
	testFrom   = "1111111111111111111111111111111111111111"
	testTo     = "2222222222222222222222222222222222222222"
	commit1    = "3333333333333333333333333333333333333333"
	commit2    = "4444444444444444444444444444444444444444"
	testToken  = "ghp_test0123456789"

	pageA    = "---\ntype: concept\n---\n# A\n"
	pageANew = "---\ntype: concept\n---\n# A\n\nNew.\n"
	pageB    = "---\ntype: concept\n---\n# B\n"
	pageC    = "---\ntype: concept\n---\n# C\n"
)

var baseFiles = map[string]string{
	"docs/a.md":        pageA,
	"docs/b.md":        pageB,
	"docs/c.md":        pageC,
	".kb/routing.yaml": "sources: []\n",
}

// fixture is a knowledge repo on the fake with main at the base files, a
// cursor at testFrom, a source range testFrom..testTo with PRs 12 and 15, and
// a checkout of main in which write changed docs/a.md.
type fixture struct {
	fake *githubtest.Fake
	dir  string
	base string
	plan *plan.Plan
	rep  *write.Report
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return build(t, true)
}

// build is newFixture; without a cursor, kb-state does not exist.
func build(t *testing.T, withCursor bool) *fixture {
	t.Helper()
	f := &fixture{fake: githubtest.New(t), dir: t.TempDir()}
	f.fake.SetRepo(testKB, "main", "")
	for p, c := range baseFiles {
		f.fake.SetFile(testKB, "main", p, c)
	}
	f.base = f.fake.Ref(testKB, "main")
	if withCursor {
		f.fake.SetFile(testKB, cursor.Branch, cursor.Path(testSource), testFrom+"\n")
	}

	f.fake.SetRepo(testSource, "main", testTo)
	cmp, _ := json.Marshal(map[string]any{
		"status": "ahead", "total_commits": 2,
		"commits": []map[string]string{{"sha": commit1}, {"sha": commit2}},
		"files":   []any{},
	})
	f.fake.SetCompare(testSource, testFrom, testTo, cmp)
	f.fake.SetCommitPulls(testSource, commit1, []byte(`[{"number":12,"merged_at":"2026-11-18T10:00:00Z"},{"number":99}]`))
	f.fake.SetCommitPulls(testSource, commit2, []byte(`[{"number":15,"merged_at":"2026-11-18T11:00:00Z"}]`))

	f.checkout(t, baseFiles)
	writeFile(t, f.dir, "docs/a.md", pageANew)
	writeFile(t, f.dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeFile(t, f.dir, ".git/objects/xx", "junk")

	f.plan = &plan.Plan{
		Version: plan.Version, Source: testSource, Bundle: testBundle, From: testFrom, To: testTo,
		ChangedPaths: []plan.ChangedPath{}, Unrouted: []string{}, Excluded: []string{},
		Pages: []plan.Page{
			{Path: "docs/a.md", State: plan.StateUpdate, Type: "concept", SourcePaths: []string{"src/a.go"}},
			{Path: "docs/b.md", State: plan.StateUpdate, Type: "concept", SourcePaths: []string{"src/b.go"}},
			{Path: "docs/c.md", State: plan.StateUpdate, Type: "concept", SourcePaths: []string{"src/c.go"}},
		},
	}
	f.rep = &write.Report{
		Version: write.Version, Source: testSource, Bundle: testBundle, From: testFrom, To: testTo,
		Pages: []write.PageResult{
			{Path: "docs/a.md", Result: write.ResultChanged, Attempts: 1},
			{Path: "docs/b.md", Result: write.ResultUnchanged, Attempts: 1},
			{Path: "docs/c.md", Result: write.ResultDropped, Reason: write.ReasonValidationFailed, Attempts: 2},
		},
	}
	return f
}

// checkout replaces the checkout's files (not .git) with files.
func (f *fixture) checkout(t *testing.T, files map[string]string) {
	t.Helper()
	entries, _ := os.ReadDir(f.dir)
	for _, e := range entries {
		if e.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(f.dir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	for p, c := range files {
		writeFile(t, f.dir, p, c)
	}
}

func (f *fixture) deps(t *testing.T) Deps {
	t.Helper()
	p, err := github.NewProvider(github.Config{APIURL: f.fake.APIURL(), Token: testToken},
		github.Access{Repos: []string{testKB, testSource}, Permissions: gogithub.InstallationPermissions{Contents: new("write")}},
		&github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return Deps{
		Knowledge:     GitHubKnowledge{Clients: p, Repo: testKB},
		SourcePRs:     GitHubSourcePRs{Differ: plan.GitHubDiffer{Clients: p}},
		Cursor:        cursor.Writer{Clients: p, KnowledgeRepo: testKB},
		KnowledgeRepo: testKB,
		KnowledgeDir:  f.dir,
		Base:          f.base,
		Logger:        slog.New(slog.DiscardHandler),
	}
}

func (f *fixture) cursor() string {
	return f.fake.Files(testKB, cursor.Branch)[cursor.Path(testSource)]
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// with returns baseFiles with overrides applied.
func with(overrides map[string]string) map[string]string {
	out := maps.Clone(baseFiles)
	maps.Copy(out, overrides)
	return out
}
