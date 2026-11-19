package propose

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/write"
)

func TestRunOpensPR(t *testing.T) {
	f := newFixture(t)
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	pulls := f.fake.PullRequests(testKB)
	if len(pulls) != 1 {
		t.Fatalf("pulls = %+v, want one", pulls)
	}
	pr := pulls[0]
	if pr.Head != "okf/acme/api" || pr.Base != "main" || pr.State != "open" || pr.Title != "okf: update docs from acme/api" {
		t.Fatalf("pr = %+v", pr)
	}
	head := f.fake.Ref(testKB, "okf/acme/api")
	if res.Action != ActionOpened || res.PR == nil || res.PR.Number != pr.Number || !strings.HasSuffix(res.PR.URL, "/acme/kb/pull/1") ||
		res.Commit != head || !res.CursorAdvanced {
		t.Fatalf("result = %+v", res)
	}
	if p := f.fake.Parents(testKB, head); !slices.Equal(p, []string{f.base}) {
		t.Fatalf("parents = %v, want the base", p)
	}
	// Only the changed page differs from the base tree.
	if got := f.fake.Files(testKB, head); !mapsEqual(got, with(map[string]string{"docs/a.md": pageANew})) {
		t.Fatalf("committed files = %v", got)
	}
	for _, want := range []string{
		"<!-- okf:range " + testFrom + ".." + testTo + " -->",
		"- acme/api#12\n- acme/api#15\n",
		"- changed: `docs/a.md`\n- unchanged: `docs/b.md`\n- dropped: `docs/c.md` (validation_failed)\n",
	} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("body lacks %q:\n%s", want, pr.Body)
		}
	}
	if strings.Contains(pr.Body, "#99") {
		t.Errorf("body links unmerged PR 99:\n%s", pr.Body)
	}
	if msg := f.fake.Message(testKB, head); !strings.HasPrefix(msg, "okf: update docs from acme/api 1111111..2222222\n") || !strings.Contains(msg, "acme/api#12, acme/api#15") {
		t.Errorf("commit message = %q", msg)
	}
	if c := f.cursor(); c != testTo+"\n" {
		t.Fatalf("cursor = %q", c)
	}
}

func TestRunUpdatesOpenBotPR(t *testing.T) {
	f := newFixture(t)
	// An earlier run left a bot PR whose branch changed docs/b.md.
	for p, c := range with(map[string]string{"docs/b.md": "earlier\n"}) {
		f.fake.SetFile(testKB, "okf/acme/api", p, c)
	}
	botHead := f.fake.Ref(testKB, "okf/acme/api")
	n := f.fake.AddPullRequest(testKB, githubtest.PullRequest{Title: "t", Body: "intro\n\n<!-- okf:range a..b -->\n### old\n", Head: "okf/acme/api", Base: "main", State: "open"})

	f.checkout(t, with(map[string]string{"docs/b.md": "earlier\n", "docs/a.md": pageANew}))
	d := f.deps(t)
	d.Base = botHead
	res, err := Run(t.Context(), f.plan, f.rep, d)
	if err != nil {
		t.Fatal(err)
	}
	pulls := f.fake.PullRequests(testKB)
	if len(pulls) != 1 || res.Action != ActionUpdated || res.PR.Number != n {
		t.Fatalf("pulls = %+v, result = %+v", pulls, res)
	}
	head := f.fake.Ref(testKB, "okf/acme/api")
	if p := f.fake.Parents(testKB, head); !slices.Equal(p, []string{botHead}) {
		t.Fatalf("parents = %v, want the bot head", p)
	}
	if got := f.fake.Files(testKB, head); got["docs/b.md"] != "earlier\n" || got["docs/a.md"] != pageANew {
		t.Fatalf("earlier bot change lost: %v", got)
	}
	if !strings.HasPrefix(pulls[0].Body, "intro\n\n<!-- okf:range a..b -->\n### old\n\n<!-- okf:range "+testFrom) {
		t.Fatalf("body = %q", pulls[0].Body)
	}
	if c := f.cursor(); c != testTo+"\n" {
		t.Fatalf("cursor = %q", c)
	}
}

func TestRunResetsStaleBranch(t *testing.T) {
	f := newFixture(t)
	f.fake.SetFile(testKB, "okf/acme/api", "docs/a.md", "old closed PR\n")
	f.fake.AddPullRequest(testKB, githubtest.PullRequest{Head: "okf/acme/api", Base: "main", State: "closed"})
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	head := f.fake.Ref(testKB, "okf/acme/api")
	if res.Action != ActionOpened || !slices.Equal(f.fake.Parents(testKB, head), []string{f.base}) {
		t.Fatalf("result = %+v, parents = %v", res, f.fake.Parents(testKB, head))
	}
}

func TestRunWrongBase(t *testing.T) {
	t.Run("checkout behind the bot branch", func(t *testing.T) {
		f := newFixture(t)
		for p, c := range with(map[string]string{"docs/b.md": "earlier\n"}) {
			f.fake.SetFile(testKB, "okf/acme/api", p, c)
		}
		f.fake.AddPullRequest(testKB, githubtest.PullRequest{Head: "okf/acme/api", Base: "main", State: "open"})
		_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
		if err == nil || !strings.Contains(err.Error(), "check out okf/acme/api at "+f.fake.Ref(testKB, "okf/acme/api")) {
			t.Fatalf("err = %v", err)
		}
		assertNoWrites(t, f)
	})
	t.Run("default branch moved", func(t *testing.T) {
		f := newFixture(t)
		f.fake.SetFile(testKB, "main", "docs/new.md", "x")
		_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
		if err == nil || !strings.Contains(err.Error(), "check out main at "+f.fake.Ref(testKB, "main")) {
			t.Fatalf("err = %v", err)
		}
		assertNoWrites(t, f)
	})
}

func TestRunPushFails(t *testing.T) {
	f := newFixture(t)
	f.fake.FailStatus["POST /repos/acme/kb/git/refs"] = http.StatusInternalServerError
	if _, err := Run(t.Context(), f.plan, f.rep, f.deps(t)); err == nil {
		t.Fatal("want an error")
	}
	if len(f.fake.PullRequests(testKB)) != 0 || f.cursor() != testFrom+"\n" {
		t.Fatalf("pulls = %v, cursor = %q: want no PR and the cursor at from", f.fake.PullRequests(testKB), f.cursor())
	}
}

func TestRunPROpenFails(t *testing.T) {
	f := newFixture(t)
	f.fake.FailStatus["POST /repos/acme/kb/pulls"] = http.StatusUnprocessableEntity
	if _, err := Run(t.Context(), f.plan, f.rep, f.deps(t)); err == nil {
		t.Fatal("want an error")
	}
	if c := f.cursor(); c != testFrom+"\n" {
		t.Fatalf("cursor = %q, want from", c)
	}
}

func TestRunInitial(t *testing.T) {
	f := build(t, false)
	f.checkout(t, baseFiles)
	f.plan.Initial, f.plan.From, f.plan.Pages = true, "", nil
	f.rep.From, f.rep.Pages = "", nil

	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionNone || res.PR != nil || res.Commit != "" || !res.CursorAdvanced {
		t.Fatalf("result = %+v", res)
	}
	if c := f.cursor(); c != testTo+"\n" {
		t.Fatalf("cursor = %q", c)
	}
	head := f.fake.Ref(testKB, cursor.Branch)
	if len(f.fake.Parents(testKB, head)) != 0 {
		t.Fatal("kb-state should be created by an orphan commit")
	}
	if len(f.fake.PullRequests(testKB)) != 0 || f.fake.Ref(testKB, "okf/acme/api") != "" || f.fake.Ref(testKB, "main") != f.base {
		t.Fatal("initial plan must not touch the knowledge branches")
	}
}

func TestRunNothingChanged(t *testing.T) {
	f := newFixture(t)
	f.checkout(t, baseFiles)
	f.rep.Pages[0].Result = write.ResultUnchanged
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionNone || res.Commit != "" || !res.CursorAdvanced || len(f.fake.PullRequests(testKB)) != 0 {
		t.Fatalf("result = %+v, pulls = %v", res, f.fake.PullRequests(testKB))
	}
	if c := f.cursor(); c != testTo+"\n" {
		t.Fatalf("cursor = %q", c)
	}
}

func TestRunEmptyRange(t *testing.T) {
	f := newFixture(t)
	f.plan.To, f.plan.Pages = testFrom, nil
	f.rep.To, f.rep.Pages = testFrom, nil
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil || res.Action != ActionNone || res.CursorAdvanced {
		t.Fatalf("result = %+v, err = %v", res, err)
	}
	if r := f.fake.Requests(); len(r) != 0 {
		t.Fatalf("requests = %v, want none", r)
	}
}

func TestRunCursorMovedBeforeStart(t *testing.T) {
	f := newFixture(t)
	f.fake.SetFile(testKB, cursor.Branch, cursor.Path(testSource), testTo+"\n")
	_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if !errors.Is(err, cursor.ErrMoved) {
		t.Fatalf("err = %v, want ErrMoved", err)
	}
	assertNoWrites(t, f)
}

// Another run advances the cursor after propose checked it: the PR is open,
// but the cursor write fails and the other run's value stays.
func TestRunCursorMovedDuringRun(t *testing.T) {
	f := newFixture(t)
	const other = "5555555555555555555555555555555555555555"
	var done atomic.Bool
	f.fake.SetHook(func(r *http.Request) {
		if r.Method == http.MethodPut && done.CompareAndSwap(false, true) {
			f.fake.SetFile(testKB, cursor.Branch, cursor.Path(testSource), other+"\n")
		}
	})
	_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if !errors.Is(err, cursor.ErrMoved) {
		t.Fatalf("err = %v, want ErrMoved", err)
	}
	if c := f.cursor(); c != other+"\n" {
		t.Fatalf("cursor = %q, want the other run's value", c)
	}
}

func TestRunRejectsContractViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, f *fixture)
		want   string
	}{
		{"report page outside the plan", func(_ *testing.T, f *fixture) {
			f.rep.Pages = append(f.rep.Pages, write.PageResult{Path: "docs/z.md", Result: write.ResultChanged})
		}, "docs/z.md is not a plan page"},
		{"unreported modified file", func(t *testing.T, f *fixture) {
			writeFile(t, f.dir, "docs/b.md", "tampered\n")
		}, "docs/b.md: differs from the base commit"},
		{"unreported new file", func(t *testing.T, f *fixture) {
			writeFile(t, f.dir, "docs/extra.md", "x")
		}, "docs/extra.md: not in the base commit"},
		{"report to mismatch", func(_ *testing.T, f *fixture) { f.rep.To = commit1 }, "disagree on to"},
		{"changed page is a symlink in the base", func(_ *testing.T, f *fixture) {
			// write replaces a symlinked page with a regular file; committing it
			// with the base mode would turn the page into a link to its text.
			f.fake.SetSymlink(testKB, "main", "docs/a.md", "b.md")
			f.base = f.fake.Ref(testKB, "main")
		}, "docs/a.md: changed page is a symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.mutate(t, f)
			_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			assertNoWrites(t, f)
		})
	}
}

func TestRunTruncatedTree(t *testing.T) {
	f := newFixture(t)
	f.fake.TruncateTrees = true
	if _, err := Run(t.Context(), f.plan, f.rep, f.deps(t)); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("err = %v", err)
	}
	assertNoWrites(t, f)
}

func assertNoWrites(t *testing.T, f *fixture) {
	t.Helper()
	if w := f.fake.Writes(); len(w) != 0 {
		t.Fatalf("writes = %v, want none", w)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Re-anchoring a source by deleting its cursor must work while its bot PR is
// open: an initial plan touches neither the knowledge branches nor the checkout.
func TestRunInitialWhileBotPROpen(t *testing.T) {
	f := build(t, false)
	for p, c := range with(map[string]string{"docs/b.md": "earlier\n"}) {
		f.fake.SetFile(testKB, "okf/acme/api", p, c)
	}
	f.fake.AddPullRequest(testKB, githubtest.PullRequest{Head: "okf/acme/api", Base: "main", State: "open"})
	botHead := f.fake.Ref(testKB, "okf/acme/api")
	f.plan.Initial, f.plan.From, f.plan.Pages = true, "", nil
	f.rep.From, f.rep.Pages = "", nil
	d := f.deps(t)
	d.Base = commit1 // neither the bot head nor the default head

	res, err := Run(t.Context(), f.plan, f.rep, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionNone || !res.CursorAdvanced || f.cursor() != testTo+"\n" {
		t.Fatalf("result = %+v, cursor = %q", res, f.cursor())
	}
	for _, r := range f.fake.Requests() {
		if strings.Contains(r, "/pulls") || strings.Contains(r, "/git/trees/") || strings.Contains(r, "okf/acme/api") {
			t.Errorf("initial plan made knowledge request %s", r)
		}
	}
	if f.fake.Ref(testKB, "okf/acme/api") != botHead {
		t.Fatal("bot branch moved")
	}
}

func TestRunNeverResetsABranchWithAnOpenPR(t *testing.T) {
	t.Run("open PR into another base", func(t *testing.T) {
		f := newFixture(t)
		f.fake.SetFile(testKB, "okf/acme/api", "docs/a.md", "someone else's\n")
		before := f.fake.Ref(testKB, "okf/acme/api")
		n := f.fake.AddPullRequest(testKB, githubtest.PullRequest{Head: "okf/acme/api", Base: "release", State: "open"})
		_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("backs open PR #%d", n)) {
			t.Fatalf("err = %v", err)
		}
		if f.fake.Ref(testKB, "okf/acme/api") != before || f.cursor() != testFrom+"\n" {
			t.Fatal("branch reset or cursor advanced")
		}
	})
	t.Run("422 other than an existing ref", func(t *testing.T) {
		f := newFixture(t)
		f.fake.SetFile(testKB, "okf/acme/api", "docs/a.md", "stale\n")
		before := f.fake.Ref(testKB, "okf/acme/api")
		f.fake.FailStatus["POST /repos/acme/kb/git/refs"] = http.StatusUnprocessableEntity
		if _, err := Run(t.Context(), f.plan, f.rep, f.deps(t)); err == nil {
			t.Fatal("want an error")
		}
		if f.fake.Ref(testKB, "okf/acme/api") != before {
			t.Fatal("branch force-reset on an unrelated 422")
		}
	})
}
