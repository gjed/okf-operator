package gate

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/policy"
)

func modified(path string, add, del int) githubtest.PullFile {
	return githubtest.PullFile{Filename: path, Status: "modified", Additions: add, Deletions: del}
}

func checkResult(t *testing.T, res Result, name string, want []string) {
	t.Helper()
	if res.Name != name {
		t.Errorf("name = %q, want %q", res.Name, name)
	}
	if want == nil {
		want = []string{}
	}
	got := res.Details
	if got == nil {
		got = []string{}
	}
	if res.Pass != (len(want) == 0) || !reflect.DeepEqual(got, want) {
		t.Errorf("pass %v details %q, want %q", res.Pass, got, want)
	}
}

func TestRoutedSet(t *testing.T) {
	tests := []struct {
		name  string
		files []githubtest.PullFile
		want  []string
	}{
		{"planned page", []githubtest.PullFile{modified("product/api.md", 1, 1)}, nil},
		{"unplanned page", []githubtest.PullFile{modified("product/api.md", 1, 1), modified("product/billing.md", 1, 0)},
			[]string{"product/billing.md: not one of the plan's pages"}},
		{"renamed from unplanned", []githubtest.PullFile{{Filename: "product/api.md", Status: "renamed", PreviousFilename: "product/old.md"}},
			[]string{"product/old.md: previous path is not one of the plan's pages"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.files = tt.files
			checkResult(t, routedSet(f.pr(t).Files, f.plan), RuleRoutedSet, tt.want)
		})
	}
}

func TestChangeShape(t *testing.T) {
	tests := []struct {
		name  string
		files []githubtest.PullFile
		want  []string
	}{
		{"modified only", []githubtest.PullFile{modified("product/api.md", 1, 1)}, nil},
		{"added", []githubtest.PullFile{{Filename: "product/new.md", Status: "added", Additions: 3}},
			[]string{"product/new.md: status added, only modified files are allowed"}},
		{"removed", []githubtest.PullFile{{Filename: "product/api.md", Status: "removed", Deletions: 3}},
			[]string{"product/api.md: status removed, only modified files are allowed"}},
		{"renamed", []githubtest.PullFile{{Filename: "product/b.md", Status: "renamed", PreviousFilename: "product/a.md"}},
			[]string{"product/b.md: status renamed from product/a.md, only modified files are allowed"}},
		{"mode change", []githubtest.PullFile{{Filename: "product/api.md", Status: "changed"}},
			[]string{"product/api.md: status changed, only modified files are allowed"}},
		{"kb config", []githubtest.PullFile{modified(".kb/routing.yaml", 1, 1)},
			[]string{".kb/routing.yaml: changes knowledge repo configuration under .kb/"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.files = tt.files
			checkResult(t, changeShape(f.pr(t).Files), RuleChangeShape, tt.want)
		})
	}
}

func TestSize(t *testing.T) {
	limits := policy.Limits{PageLines: 100, PRLines: 150}
	tests := []struct {
		name  string
		files []githubtest.PullFile
		want  []string
	}{
		{"at the limits", []githubtest.PullFile{modified("product/a.md", 60, 40), modified("product/b.md", 30, 20)}, nil},
		{"page over", []githubtest.PullFile{modified("product/a.md", 70, 50)},
			[]string{"product/a.md: 120 changed lines exceed limits.page_lines 100"}},
		{"PR over", []githubtest.PullFile{modified("product/a.md", 50, 30), modified("product/b.md", 50, 30)},
			[]string{"pull request: 160 changed lines exceed limits.pr_lines 150"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.files = tt.files
			checkResult(t, size(f.pr(t).Files, limits), RuleSize, tt.want)
		})
	}
}

func TestLinkTargets(t *testing.T) {
	page := "---\ntitle: \"[not](a-link.md)\"\n---\n# T\n\n" +
		"[a](a.md) ![img](img/x.png \"title\") [b](<b c.md>) [ref]\n" +
		"`[code](code.md)` text\n" +
		"```md\n[fenced](fenced.md)\n```\n" +
		"[ref]: ../ref.md \"Ref\"\n"
	got := linkTargets([]byte(page))
	want := []string{"a.md", "img/x.png", "b c.md", "../ref.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("targets = %q, want %q", got, want)
	}
}

func TestRelativeTarget(t *testing.T) {
	tests := []struct {
		in, out string
		ok      bool
	}{
		{"a.md", "a.md", true},
		{"a.md#frag", "a.md", true},
		{"a.md?x=1", "a.md", true},
		{"#frag", "", false},
		{"https://example.com", "", false},
		{"mailto:a@b.c", "", false},
		{"//cdn.example.com/x", "", false},
		{"/root.md", "", false},
		{"?q", "", false},
	}
	for _, tt := range tests {
		if out, ok := relativeTarget(tt.in); out != tt.out || ok != tt.ok {
			t.Errorf("relativeTarget(%q) = %q, %v", tt.in, out, ok)
		}
	}
}

func TestLinks(t *testing.T) {
	tests := []struct {
		name string
		page string
		want []string
	}{
		{"all resolve", apiPage, nil},
		{"broken link", apiPage + "[gone](../product/missing.md)\n",
			[]string{`product/api.md: link "../product/missing.md" resolves to product/missing.md, which is not a file at the head`}},
		{"escapes bundle", apiPage + "[out](../.kb/policy.yaml)\n",
			[]string{`product/api.md: link "../.kb/policy.yaml" resolves to .kb/policy.yaml, outside bundle product`}},
		{"directory", apiPage + "[dir](.)\n",
			[]string{`product/api.md: link "." resolves to product, outside bundle product`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			res, err := links(context.Background(), f.reader, kbRepo, headSHA, "product", map[string][]byte{"product/api.md": []byte(tt.page)})
			if err != nil {
				t.Fatal(err)
			}
			checkResult(t, res, RuleLinks, tt.want)
		})
	}
}

func TestLinksLookupError(t *testing.T) {
	f := newFixture(t)
	f.fake.FailStatus["/repos/acme/kb/contents/product/billing.md"] = http.StatusInternalServerError
	_, err := links(context.Background(), f.reader, kbRepo, headSHA, "product", map[string][]byte{"product/api.md": []byte(apiPage)})
	if err == nil || !strings.Contains(err.Error(), "product/billing.md") {
		t.Fatalf("err = %v", err)
	}
}
