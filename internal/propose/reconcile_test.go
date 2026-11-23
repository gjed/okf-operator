package propose

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/write"
)

// asReconcile turns the fixture plan into a version 2 plan with the cursor
// state given, every page reason stale on whole files, and one deletion
// candidate.
func (f *fixture) asReconcile(cursorState string) {
	f.plan.Version = plan.ReconcileVersion
	f.plan.Reconcile = &plan.Reconcile{
		Cursor: cursorState,
		DeletionCandidates: []plan.DeletionCandidate{
			{Path: "docs/old.md", SourceSHA: testFrom, SourcePaths: []string{"src/gone.go"}},
		},
	}
	for _, pg := range f.plan.Pages {
		f.plan.Reconcile.Pages = append(f.plan.Reconcile.Pages,
			plan.ReconcilePage{Path: pg.Path, Reason: plan.ReasonStale, Evidence: plan.EvidenceFiles})
	}
}

// A backfill has no cursor: the PR is opened first, then the cursor is
// created. No source PR is listed and the body names the candidate.
func TestRunReconcileInitialOpensPRThenCreatesCursor(t *testing.T) {
	f := build(t, false)
	f.plan.Initial, f.plan.From, f.rep.From = true, "", ""
	f.asReconcile(plan.CursorMissing)
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionOpened || !res.CursorAdvanced {
		t.Fatalf("result = %+v", res)
	}
	if c := f.cursor(); c != testTo+"\n" {
		t.Fatalf("cursor = %q", c)
	}
	writes := f.fake.Writes()
	pr := slices.Index(writes, "POST /repos/acme/kb/pulls")
	if pr < 0 || pr == len(writes)-1 || writes[len(writes)-1] != "POST /repos/acme/kb/git/refs" {
		t.Fatalf("writes = %v, want the PR before the kb-state ref", writes)
	}
	for _, r := range f.fake.Requests() {
		if strings.HasPrefix(r, "GET /repos/acme/api/") {
			t.Errorf("reconcile propose read the source repo: %s", r)
		}
	}
	pulls := f.fake.PullRequests(testKB)
	body := pulls[0].Body
	for _, want := range []string{
		"<!-- okf:range .." + testTo + " -->\n### reconcile acme/api at `2222222`",
		"Cursor: none, anchored at `2222222`.",
		"- changed: `docs/a.md` (stale)\n- unchanged: `docs/b.md` (stale)\n- dropped: `docs/c.md` (stale, validation_failed)\n",
		"- `docs/old.md` from `1111111`, source paths `src/gone.go`\n",
		"They were not deleted",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	head := f.fake.Ref(testKB, "okf/acme/api")
	if msg := f.fake.Message(testKB, head); msg != "okf: reconcile docs from acme/api at 2222222\n" {
		t.Errorf("commit message = %q", msg)
	}
	if _, ok := f.fake.Files(testKB, head)["docs/old.md"]; ok {
		t.Error("the deletion candidate is in the commit")
	}
}

// A diverged cursor moves to to only after the PR call succeeds.
func TestRunReconcileReanchorsDivergedCursor(t *testing.T) {
	f := newFixture(t)
	f.asReconcile(plan.CursorDiverged)
	f.fake.FailStatus["POST /repos/acme/kb/pulls"] = http.StatusUnprocessableEntity
	if _, err := Run(t.Context(), f.plan, f.rep, f.deps(t)); err == nil {
		t.Fatal("want an error")
	}
	if c := f.cursor(); c != testFrom+"\n" {
		t.Fatalf("cursor after a failed PR call = %q, want the diverged cursor", c)
	}

	delete(f.fake.FailStatus, "POST /repos/acme/kb/pulls")
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionOpened || !res.CursorAdvanced || f.cursor() != testTo+"\n" {
		t.Fatalf("result = %+v, cursor = %q", res, f.cursor())
	}
	if body := f.fake.PullRequests(testKB)[0].Body; !strings.Contains(body, "Cursor: `1111111` diverged from the source history, re-anchored at `2222222`.") {
		t.Errorf("body lacks the re-anchor:\n%s", body)
	}
}

// Pages can lag behind a cursor already at to: the PR is opened and the
// cursor is not written.
func TestRunReconcileCursorAtHead(t *testing.T) {
	f := newFixture(t)
	f.plan.From, f.rep.From = testTo, testTo
	f.fake.SetFile(testKB, "kb-state", "cursors/acme/api", testTo+"\n")
	f.asReconcile(plan.CursorHead)
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionOpened || res.CursorAdvanced {
		t.Fatalf("result = %+v", res)
	}
	for _, w := range f.fake.Writes() {
		if strings.Contains(w, "/contents/cursors/") {
			t.Errorf("cursor written: %s", w)
		}
	}
}

// With nothing changed, a backfill only creates the cursor.
func TestRunReconcileNothingChanged(t *testing.T) {
	f := build(t, false)
	f.checkout(t, baseFiles)
	f.plan.Initial, f.plan.From, f.rep.From = true, "", ""
	f.rep.Pages[0].Result = write.ResultUnchanged
	f.asReconcile(plan.CursorMissing)
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionNone || !res.CursorAdvanced || len(f.fake.PullRequests(testKB)) != 0 || f.cursor() != testTo+"\n" {
		t.Fatalf("result = %+v, cursor = %q", res, f.cursor())
	}
}

const pageBRestamped = "---\ntype: concept\nsource_sha: " + testTo + "\n---\n# B\n"

// A re-stamped page is committed like a changed one and listed apart, as
// provenance refreshed with its content unchanged.
func TestRunReconcileCommitsRestampedPage(t *testing.T) {
	f := newFixture(t)
	f.asReconcile(plan.CursorAncestor)
	f.rep.Pages[1].Result = write.ResultRestamped
	writeFile(t, f.dir, "docs/b.md", pageBRestamped)
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionOpened || !res.CursorAdvanced || f.cursor() != testTo+"\n" {
		t.Fatalf("result = %+v, cursor = %q", res, f.cursor())
	}
	head := f.fake.Ref(testKB, "okf/acme/api")
	files := f.fake.Files(testKB, head)
	if files["docs/a.md"] != pageANew || files["docs/b.md"] != pageBRestamped || files["docs/c.md"] != pageC {
		t.Errorf("committed files = %v", files)
	}
	body := f.fake.PullRequests(testKB)[0].Body
	for _, want := range []string{
		"Pages:\n\n- changed: `docs/a.md` (stale)\n- dropped: `docs/c.md` (stale, validation_failed)\n",
		"\nProvenance refreshed, content unchanged:\n\n- `docs/b.md` (stale)\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "- restamped:") {
		t.Errorf("body lists the re-stamped page among the pages:\n%s", body)
	}
}

// A run whose only change is a re-stamped page still opens a PR, and moves
// the cursor after it.
func TestRunReconcileOnlyRestampedPage(t *testing.T) {
	f := newFixture(t)
	f.checkout(t, with(map[string]string{"docs/b.md": pageBRestamped}))
	f.asReconcile(plan.CursorAncestor)
	f.rep.Pages = []write.PageResult{
		{Path: "docs/a.md", Result: write.ResultUnchanged, Attempts: 1},
		{Path: "docs/b.md", Result: write.ResultRestamped, Attempts: 1},
		{Path: "docs/c.md", Result: write.ResultUnchanged, Attempts: 1},
	}
	res, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != ActionOpened || res.Commit == "" || !res.CursorAdvanced || f.cursor() != testTo+"\n" {
		t.Fatalf("result = %+v, cursor = %q", res, f.cursor())
	}
	body := f.fake.PullRequests(testKB)[0].Body
	if !strings.Contains(body, "- unchanged: `docs/a.md` (stale)\n- unchanged: `docs/c.md` (stale)\n") ||
		!strings.Contains(body, "Provenance refreshed, content unchanged:\n\n- `docs/b.md` (stale)\n") {
		t.Errorf("body:\n%s", body)
	}
}

// A page reported re-stamped must differ from the base, like a changed one.
func TestRunReconcileRestampedPageIdenticalToBase(t *testing.T) {
	f := newFixture(t)
	f.asReconcile(plan.CursorAncestor)
	f.rep.Pages[1].Result = write.ResultRestamped
	_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err == nil || !strings.Contains(err.Error(), "docs/b.md: reported changed but identical to the base commit") {
		t.Fatalf("err = %v", err)
	}
	if len(f.fake.Writes()) != 0 {
		t.Errorf("writes = %v", f.fake.Writes())
	}
}

// A re-stamped result on a version 1 plan fails before any GitHub write.
func TestRunRestampedOnVersion1Plan(t *testing.T) {
	f := newFixture(t)
	f.rep.Pages[1].Result = write.ResultRestamped
	_, err := Run(t.Context(), f.plan, f.rep, f.deps(t))
	if err == nil || !strings.Contains(err.Error(), "restamped but the plan is not a reconcile plan") {
		t.Fatalf("err = %v", err)
	}
	if len(f.fake.Writes()) != 0 {
		t.Errorf("writes = %v", f.fake.Writes())
	}
}
