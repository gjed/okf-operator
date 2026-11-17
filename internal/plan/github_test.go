package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
