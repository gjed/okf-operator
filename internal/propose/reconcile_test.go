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
