package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
)

// Recorded from gjed/okf-operator with
// gh api repos/gjed/okf-operator/compare/<base>...<head>, trimmed to the
// fields okf-operator reads.
const (
	repo        = "gjed/okf-operator"
	aheadBase   = "9a4ee5112983eec42553536efc6893f7ac050d2a"
	aheadHead   = "f4e931d28b947bc53318e78762096fe607051970"
	renameBase  = "d38d0a4d6925735a7a26dc4a9d4cd9626eb91039"
	renameHead  = "24f366f6966318f3155a546961cfa88d8b0b0355"
	pr11Head    = "6706b73c253efe7f19569d7466f6d2aa679c3b5e"
	fixturesDir = "../../test/fixtures/compare"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixturesDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func differ(t *testing.T) (GitHubDiffer, *githubtest.Fake) {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetCompare(repo, aheadBase, aheadHead, fixture(t, "ahead"))
	fake.SetCompare(repo, renameBase, renameHead, fixture(t, "renamed"))
	fake.SetCompare(repo, aheadHead, aheadHead, fixture(t, "identical"))
	fake.SetCompare(repo, aheadHead, aheadBase, fixture(t, "behind"))
	fake.SetCompare(repo, pr11Head, aheadHead, fixture(t, "diverged"))
	fake.SetRepo(repo, "main", aheadHead)
	p, err := github.NewProvider(github.Config{APIURL: fake.APIURL(), Token: "ghp_test0123456789"},
		github.ReadContents(repo), &github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return GitHubDiffer{Clients: p}, fake
}

func TestDifferHead(t *testing.T) {
	d, _ := differ(t)
	got, err := d.Head(context.Background(), repo)
	if err != nil || got != aheadHead {
		t.Fatalf("head = %q, %v", got, err)
	}
}

func TestDifferAhead(t *testing.T) {
	d, _ := differ(t)
	got, err := d.Changes(context.Background(), repo, aheadBase, aheadHead)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Files []struct{ Filename, Status string } `json:"files"`
	}
	if err := json.Unmarshal(fixture(t, "ahead"), &rec); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(rec.Files) || len(got) == 0 {
		t.Fatalf("got %d files, recorded %d", len(got), len(rec.Files))
	}
	for i, f := range rec.Files {
		if got[i] != (ChangedPath{Path: f.Filename, Status: f.Status}) {
			t.Errorf("file %d = %+v, recorded %+v", i, got[i], f)
		}
	}
}

func TestDifferRenamed(t *testing.T) {
	d, _ := differ(t)
	got, err := d.Changes(context.Background(), repo, renameBase, renameHead)
	if err != nil {
		t.Fatal(err)
	}
	want := ChangedPath{
		Path:         "openspec/changes/archive/2026-11-17-bootstrap-repository/design.md",
		Status:       "renamed",
		PreviousPath: "openspec/changes/bootstrap-repository/design.md",
	}
	for _, c := range got {
		if c == want {
			return
		}
	}
	t.Fatalf("rename not found in %+v", got)
}

func TestDifferIdentical(t *testing.T) {
	d, _ := differ(t)
	got, err := d.Changes(context.Background(), repo, aheadHead, aheadHead)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestDifferDiverged(t *testing.T) {
	d, _ := differ(t)
	for name, r := range map[string][2]string{
		"behind":   {aheadHead, aheadBase},
		"diverged": {pr11Head, aheadHead},
		"404":      {"0000000000000000000000000000000000000000", aheadHead},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.Changes(context.Background(), repo, r[0], r[1])
			if !errors.Is(err, ErrDiverged) {
				t.Fatalf("err = %v, want ErrDiverged", err)
			}
		})
	}
}

// A compare 404 is divergence only when head resolves: a mistyped --to or an
// unreadable source repo is an ordinary failure.
func TestDifferNotFoundIsNotDivergence(t *testing.T) {
	d, _ := differ(t)
	for name, r := range map[string][3]string{
		"missing head":    {repo, aheadBase, "dddddddddddddddddddddddddddddddddddddddd"},
		"unreadable repo": {"gjed/private", aheadBase, aheadHead},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.Changes(context.Background(), r[0], r[1], r[2])
			if err == nil || errors.Is(err, ErrDiverged) || !strings.Contains(err.Error(), "resolve "+r[0]+"@"+r[2]) {
				t.Fatalf("err = %v, want a resolve failure that is not ErrDiverged", err)
			}
		})
	}
}

func TestDifferServerError(t *testing.T) {
	d, fake := differ(t)
	fake.FailStatus["/repos/"+repo+"/compare/"+aheadBase+"..."+aheadHead] = 502
	_, err := d.Changes(context.Background(), repo, aheadBase, aheadHead)
	if err == nil || errors.Is(err, ErrDiverged) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
}

func TestDifferFailsClosedAt300Files(t *testing.T) {
	d, fake := differ(t)
	files := make([]map[string]string, MaxCompareFiles)
	for i := range files {
		files[i] = map[string]string{"filename": fmt.Sprintf("f%03d.go", i), "status": "modified"}
	}
	body, _ := json.Marshal(map[string]any{"status": "ahead", "files": files})
	fake.SetCompare(repo, "a", "b", body)
	_, err := d.Changes(context.Background(), repo, "a", "b")
	if !errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), "reconcile required") {
		t.Fatalf("err = %v, want ErrIncomplete", err)
	}
}

func TestDirPages(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "infra", "dir.md"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "infra", "x.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pages := DirPages(dir)
	if ok, err := pages.Exists("infra/x.md"); !ok || err != nil {
		t.Fatalf("existing page: %v %v", ok, err)
	}
	if ok, err := pages.Exists("infra/y.md"); ok || err != nil {
		t.Fatalf("missing page: %v %v", ok, err)
	}
	if _, err := pages.Exists("infra/dir.md"); err == nil {
		t.Fatal("a directory is not a page")
	}
}

const (
	patchBase = "1111111111111111111111111111111111111111"
	patchHead = "3333333333333333333333333333333333333333"
	patchSHA1 = "2222222222222222222222222222222222222222"
	patchSHA2 = "3333333333333333333333333333333333333333"
)

func rangeDiffer(t *testing.T) (GitHubDiffer, *githubtest.Fake) {
	t.Helper()
	d, fake := differ(t)
	fake.SetCompare(repo, patchBase, patchHead, fixture(t, "patches"))
	return d, fake
}

func TestDifferRangePatches(t *testing.T) {
	d, _ := rangeDiffer(t)
	paths := []string{"docs/full.md", "docs/omitted.md", "docs/emptied.md", "docs/empty.md",
		"docs/new.md", "docs/old.md", "docs/renamed-only.md", "docs/renamed-before.md", "docs/not-changed.md"}
	got, err := d.Range(context.Background(), repo, patchBase, patchHead, paths)
	if err != nil {
		t.Fatal(err)
	}
	const renamePatch = "@@ -1 +1 @@\n-Before.\n+After."
	want := map[string]string{
		"docs/full.md":           "@@ -1,3 +1,4 @@\n # Title\n-Old line.\n+New line.\n+Added line.\n Keep.",
		"docs/empty.md":          "",
		"docs/new.md":            renamePatch,
		"docs/old.md":            renamePatch,
		"docs/renamed-only.md":   "",
		"docs/renamed-before.md": "",
	}
	if len(got.Patches) != len(want) {
		t.Fatalf("patches = %q, want keys %v", got.Patches, want)
	}
	for p, w := range want {
		if v, ok := got.Patches[p]; !ok || v != w {
			t.Errorf("patch[%s] = %q, %v; want %q", p, v, ok, w)
		}
	}
	// Omitted patch, changed file with empty patch, and unrequested paths are absent.
	for _, p := range []string{"docs/omitted.md", "docs/emptied.md", "docs/not-changed.md"} {
		if _, ok := got.Patches[p]; ok {
			t.Errorf("%s present, want absent", p)
		}
	}
	if len(got.Commits) != 3 || got.Commits[0] != patchSHA1 || got.Commits[2] != "4444444444444444444444444444444444444444" || got.TotalCommits != 3 {
		t.Errorf("commits = %v total %d", got.Commits, got.TotalCommits)
	}
}

func TestDifferRangeErrors(t *testing.T) {
	d, fake := rangeDiffer(t)
	files := make([]map[string]string, MaxCompareFiles)
	for i := range files {
		files[i] = map[string]string{"filename": fmt.Sprintf("f%03d.go", i), "status": "modified"}
	}
	body, _ := json.Marshal(map[string]any{"status": "ahead", "files": files})
	fake.SetCompare(repo, "a", "b", body)
	for name, tc := range map[string]struct {
		base, head string
		want       error
	}{
		"behind":    {aheadHead, aheadBase, ErrDiverged},
		"404":       {"0000000000000000000000000000000000000000", aheadHead, ErrDiverged},
		"300 files": {"a", "b", ErrIncomplete},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.Range(context.Background(), repo, tc.base, tc.head, []string{"f000.go"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// The compare API lists at most 250 commits; Range keeps those and reports the total.
func TestRangeCommitCapWarns(t *testing.T) {
	d, fake := rangeDiffer(t)
	commits := make([]map[string]string, MaxCompareCommits)
	for i := range commits {
		commits[i] = map[string]string{"sha": fmt.Sprintf("%040x", i+1)}
	}
	body, _ := json.Marshal(map[string]any{"status": "ahead", "total_commits": MaxCompareCommits + 1, "commits": commits, "files": []any{}})
	fake.SetCompare(repo, "a", "b", body)
	got, err := d.Range(context.Background(), repo, "a", "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commits) != MaxCompareCommits || got.TotalCommits != MaxCompareCommits+1 {
		t.Fatalf("commits = %d total = %d", len(got.Commits), got.TotalCommits)
	}
	var buf bytes.Buffer
	got.WarnCommitCap(slog.New(slog.NewTextHandler(&buf, nil)), repo)
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "total_commits=251") {
		t.Errorf("log = %q, want a warning", buf.String())
	}
	buf.Reset()
	Range{Commits: []string{patchSHA1}, TotalCommits: 1}.WarnCommitCap(slog.New(slog.NewTextHandler(&buf, nil)), repo)
	if buf.Len() != 0 {
		t.Errorf("log = %q, want none below the cap", buf.String())
	}
}

func pullJSON(number int, merged bool, title, body string) map[string]any {
	var mergedAt any
	if merged {
		mergedAt = "2026-11-10T10:00:00Z"
	}
	return map[string]any{"number": number, "title": title, "body": body, "state": "closed", "merged_at": mergedAt}
}

func pullsBody(t *testing.T, prs ...map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(prs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDifferPullRequestsDedupeAndFilter(t *testing.T) {
	d, fake := rangeDiffer(t)
	fake.SetCommitPulls(repo, patchSHA1, pullsBody(t,
		pullJSON(12, true, "Second", "b12"),
		pullJSON(7, true, "Seven", "b7"),
		pullJSON(9, false, "Still open", "open body"),
	))
	fake.SetCommitPulls(repo, patchSHA2, pullsBody(t,
		pullJSON(7, true, "Seven", "b7"),
		pullJSON(3, true, "Three", ""),
	))
	// patchSHA3 has no pull requests: the fake answers an empty list.
	got, err := d.PullRequests(context.Background(), repo, []string{patchSHA1, patchSHA2, "4444444444444444444444444444444444444444"})
	if err != nil {
		t.Fatal(err)
	}
	want := []PullRequest{
		{Number: 3, Title: "Three"},
		{Number: 7, Title: "Seven", Body: "b7"},
		{Number: 12, Title: "Second", Body: "b12"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pr %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDifferPullRequestsBodyCap(t *testing.T) {
	cases := map[string]struct {
		body      string
		want      string
		truncated bool
	}{
		"exactly 4096 bytes": {strings.Repeat("a", 4096), strings.Repeat("a", 4096), false},
		"over the cap":       {strings.Repeat("a", 5000), strings.Repeat("a", 4096), true},
		// é is two bytes at 4095-4096: cutting at 4096 would split it.
		"multibyte at boundary": {strings.Repeat("a", 4095) + "é" + "tail", strings.Repeat("a", 4095), true},
		"multibyte inside cap":  {strings.Repeat("é", 3000), strings.Repeat("é", 2048), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d, fake := rangeDiffer(t)
			fake.SetCommitPulls(repo, patchSHA1, pullsBody(t, pullJSON(1, true, "T", tc.body)))
			got, err := d.PullRequests(context.Background(), repo, []string{patchSHA1})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Body != tc.want || got[0].BodyTruncated != tc.truncated {
				t.Fatalf("got body len %d truncated %v, want len %d truncated %v", len(got[0].Body), got[0].BodyTruncated, len(tc.want), tc.truncated)
			}
			if !utf8.ValidString(got[0].Body) {
				t.Fatal("body is not valid UTF-8")
			}
		})
	}
}
