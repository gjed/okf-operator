package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
)

const (
	humanPage = "---\ntype: concept\nsource_repo: acme/api\nsource_sha: " + runBase + "\nsource_paths: [src/a.go]\n---\n# API\n\nEdited by a human.\n"
	botPage   = "---\ntype: concept\nsource_repo: acme/api\nsource_sha: " + runBase + "\nsource_paths: [src/a.go]\n---\n# API\n\nEdited by the bot.\n"
)

// botPRFixture is the run fixture with the cursor at the source head, a bot
// PR whose branch changed runPagePath and added product/other.md, and whose
// body (the given one, else one that records runBase..head) lists the page.
func botPRFixture(t *testing.T, body string, states ...string) (*githubtest.Fake, *modelStub, *SourceReconciler, string, int) {
	t.Helper()
	m := newModel(t, pageReply)
	fake, head, _, r, _, _ := newRunFixture(t, m, 0)
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", head+"\n")
	fake.Branch("acme/kb", "main", "okf/acme/api")
	fake.SetFile("acme/kb", "okf/acme/api", runPagePath, botPage)
	fake.SetFile("acme/kb", "okf/acme/api", "product/other.md", "---\ntype: concept\n---\n# Other\n")
	if body == "" {
		body = "Knowledge updates.\n\n<!-- okf:range " + runBase + ".." + head + " -->\n### acme/api\n\nPages:\n\n- changed: `" + runPagePath + "`\n"
	}
	n := fake.AddPullRequest("acme/kb", githubtest.PullRequest{Title: "t", Body: body, Head: "okf/acme/api", Base: "main", State: "open", MergeStates: states})
	return fake, m, r, head, n
}

// approveOldHead stores the review App's approval of head (ID 1), a human's
// approval of the same head (ID 2) and the review App's approval of another
// commit (ID 3).
func approveOldHead(fake *githubtest.Fake, n int, head string) {
	// The fake's GET /app answers the slug okf-writer for both Apps.
	fake.AddReview("acme/kb", n, githubtest.StoredReview{ID: 1, State: "APPROVED", CommitID: head, User: "okf-writer[bot]"})
	fake.AddReview("acme/kb", n, githubtest.StoredReview{ID: 2, State: "APPROVED", CommitID: head, User: "maintainer"})
	fake.AddReview("acme/kb", n, githubtest.StoredReview{ID: 3, State: "APPROVED", CommitID: strings.Repeat("9", 40), User: "okf-writer[bot]"})
}

// states returns each stored review's state by ID.
func reviewStates(fake *githubtest.Fake, n int) map[int64]string {
	out := map[int64]string{}
	for _, rv := range fake.ListedReviews("acme/kb", n) {
		out[rv.ID] = rv.State
	}
	return out
}

func TestRunnerUpdatesABehindBotPR(t *testing.T) {
	fake, m, r, head, n := botPRFixture(t, "", "behind")
	fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	old := fake.Ref("acme/kb", "okf/acme/api")
	approveOldHead(fake, n, old)
	// The review App's approval of the head the repair leaves the branch at,
	// stored when the run lists the reviews, must survive.
	fake.SetHook(func(r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/"+strconv.Itoa(n)+"/reviews") && len(fake.ListedReviews("acme/kb", n)) == 3 {
			fake.AddReview("acme/kb", n, githubtest.StoredReview{ID: 4, State: "APPROVED", CommitID: fake.Ref("acme/kb", "okf/acme/api"), User: "okf-writer[bot]"})
		}
	})
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if got := reviewStates(fake, n); got[1] != "DISMISSED" || got[2] != "APPROVED" || got[3] != "DISMISSED" || got[4] != "APPROVED" {
		t.Errorf("review states after update-branch = %v, want the review App's approvals of the old heads dismissed, a human's and the new head's kept", got)
	}
	st := getSource(t, r, "api").Status
	if calls := fake.UpdateBranches(); !slices.Equal(calls, []githubtest.UpdateBranch{{Number: n, ExpectedHeadSHA: old}}) {
		t.Fatalf("update-branch calls = %v, want one at %s (status %+v)", calls, old, st)
	}
	if got := fake.Ref("acme/kb", "okf/acme/api"); got == old || !slices.Equal(fake.Parents("acme/kb", got), []string{old, fake.Ref("acme/kb", "main")}) {
		t.Fatalf("bot branch %s, parents %v", got, fake.Parents("acme/kb", got))
	}
	if m.calls != 0 {
		t.Errorf("update-branch made %d model calls", m.calls)
	}
	if c := cursorOf(fake); c != head+"\n" {
		t.Errorf("cursor = %q, want it unchanged", c)
	}
	if files := fake.Files("acme/kb", "okf/acme/api"); files[runPagePath] != botPage || files["product/news.md"] == "" {
		t.Errorf("files = %v", files)
	}
}

func TestRunnerRegeneratesAConflictingBotPR(t *testing.T) {
	fake, m, r, head, n := botPRFixture(t, "", "dirty")
	fake.SetFile("acme/kb", "main", runPagePath, humanPage)
	fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	botHead, defHead := fake.Ref("acme/kb", "okf/acme/api"), fake.Ref("acme/kb", "main")
	approveOldHead(fake, n, botHead)

	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if got := reviewStates(fake, n); got[1] != "DISMISSED" || got[2] != "APPROVED" || got[3] != "DISMISSED" {
		t.Errorf("review states after regeneration = %v, want the review App's approvals of the old heads dismissed and a human's kept", got)
	}
	// Dismissing is all the repair does about approvals: nothing is reviewed or approved again.
	for _, req := range fake.Requests() {
		if strings.HasPrefix(req, "POST ") && strings.HasSuffix(req, "/reviews") {
			t.Errorf("the repair posted a review: %s", req)
		}
	}
	// The same PR, one new commit on both parents.
	got := fake.Ref("acme/kb", "okf/acme/api")
	if prs := fake.PullRequests("acme/kb"); len(prs) != 1 || prs[0].Number != n || prs[0].State != "open" {
		t.Fatalf("pulls = %+v", prs)
	}
	if !slices.Equal(fake.Parents("acme/kb", got), []string{defHead, botHead}) {
		t.Fatalf("parents = %v, want default %s and bot %s", fake.Parents("acme/kb", got), defHead, botHead)
	}
	files := fake.Files("acme/kb", got)
	if !strings.Contains(files[runPagePath], "Package a.") {
		t.Errorf("conflicting page = %q, want the regenerated page", files[runPagePath])
	}
	if files["product/other.md"] == "" || files["product/news.md"] == "" {
		t.Errorf("bot-only and default-only pages lost: %v", files)
	}
	// One model call, for the conflicting page only, on top of the human's text.
	if m.calls != 1 || !strings.Contains(m.bodies[0], "Edited by a human.") || strings.Contains(m.bodies[0], "Edited by the bot.") {
		t.Fatalf("model calls = %d, first prompt:\n%v", m.calls, m.bodies)
	}
	if !strings.Contains(m.bodies[0], "package old") || !strings.Contains(m.bodies[0], "package a") {
		t.Errorf("the prompt lacks the PR's whole range: %v", m.bodies[0])
	}
	// Provenance records the regeneration at the range end.
	if !strings.Contains(files[runPagePath], "source_sha: "+head) {
		t.Errorf("page provenance = %q", files[runPagePath])
	}
	if body := fake.PullRequests("acme/kb")[0].Body; !strings.Contains(body, "### Conflict repair") || !strings.Contains(body, "- changed: `"+runPagePath+"`") {
		t.Errorf("body = %q", body)
	}
	// The cursor never moves backwards: it still holds the range end.
	if c := cursorOf(fake); c != head+"\n" {
		t.Errorf("cursor = %q, want %q", c, head+"\n")
	}
}

func TestRunnerRefusedRepairFailsTheRun(t *testing.T) {
	// The body no longer says what to regenerate.
	fake, m, r, head, _ := botPRFixture(t, "Knowledge updates.\n<!-- okf:omitted --> 2 earlier ranges omitted, see the commit history.\n", "dirty")
	fake.SetFile("acme/kb", "main", runPagePath, humanPage)
	botHead := fake.Ref("acme/kb", "okf/acme/api")

	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if st := getSource(t, r, "api").Status; !strings.HasPrefix(st.LastError, "repair: ") || !strings.Contains(st.LastError, "omitted earlier ranges") {
		t.Fatalf("status = %+v", st)
	}
	if m.calls != 0 || fake.Ref("acme/kb", "okf/acme/api") != botHead || cursorOf(fake) != head+"\n" {
		t.Fatalf("a refused repair wrote: model calls %d, cursor %q", m.calls, cursorOf(fake))
	}
}

// A state GitHub never computes leaves the PR as it is, like a clean one.
func TestRunnerUnknownMergeStateCarriesOn(t *testing.T) {
	fake, _, r, _, n := botPRFixture(t, "", "unknown")
	botHead := fake.Ref("acme/kb", "okf/acme/api")
	approveOldHead(fake, n, botHead)
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	// Only approvals of commits that are not the head go, repaired or not.
	if got := reviewStates(fake, n); got[1] != "APPROVED" || got[2] != "APPROVED" || got[3] != "DISMISSED" {
		t.Errorf("review states of a PR that was not repaired = %v, want only the approval of another commit dismissed", got)
	}
	if calls := fake.UpdateBranches(); len(calls) != 0 || fake.Ref("acme/kb", "okf/acme/api") != botHead {
		t.Fatalf("update calls %v, branch moved to %s", calls, fake.Ref("acme/kb", "okf/acme/api"))
	}
}

// failingCheckout fails the checkout of the knowledge repo while fail is set.
type failingCheckout struct {
	next Checkout
	fail *bool
}

func (c failingCheckout) Checkout(ctx context.Context, clients github.Clients, repo, sha, dir string) error {
	if *c.fail {
		return errors.New("checkout unavailable")
	}
	return c.next.Checkout(ctx, clients, repo, sha, dir)
}

// The head moves in the checkout stage, but the dismissal comes before the
// checkout of the new head, so a checkout that fails afterwards leaves no
// approval of the old head behind.
func TestRunnerDismissesBeforeTheCheckoutOfTheMovedHead(t *testing.T) {
	fake, _, r, _, n := botPRFixture(t, "", "behind")
	fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	old := fake.Ref("acme/kb", "okf/acme/api")
	approveOldHead(fake, n, old)
	fail := true
	runner := r.Pipeline.(*Runner)
	runner.Checkout = failingCheckout{runner.Checkout, &fail}

	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if st := getSource(t, r, "api").Status; !strings.HasPrefix(st.LastError, "checkout: ") {
		t.Fatalf("status = %+v, want a checkout failure", st)
	}
	if fake.Ref("acme/kb", "okf/acme/api") == old {
		t.Fatal("the branch did not move")
	}
	if got := reviewStates(fake, n); got[1] != "DISMISSED" || got[2] != "APPROVED" {
		t.Errorf("review states = %v, want the approval of the old head dismissed before the checkout failed", got)
	}
}

// A dismissal that fails after the head moved is retried by the next run,
// which finds the new head as the PR head and still clears the old approval.
func TestRunnerDismissesAnOldHeadOnTheNextRun(t *testing.T) {
	fake, _, r, _, n := botPRFixture(t, "", "behind", "clean")
	fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	old := fake.Ref("acme/kb", "okf/acme/api")
	approveOldHead(fake, n, old)
	dismissal := "PUT /repos/acme/kb/pulls/" + strconv.Itoa(n) + "/reviews/1/dismissals"
	fake.FailStatus[dismissal] = http.StatusInternalServerError

	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if st := getSource(t, r, "api").Status; !strings.HasPrefix(st.LastError, "dismiss: ") {
		t.Fatalf("status = %+v, want a dismiss failure", st)
	}
	moved := fake.Ref("acme/kb", "okf/acme/api")
	if moved == old || reviewStates(fake, n)[1] != "APPROVED" {
		t.Fatalf("head %s (old %s), review states %v: want a moved head and the approval still in place", moved, old, reviewStates(fake, n))
	}

	delete(fake.FailStatus, dismissal)
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if st := getSource(t, r, "api").Status; st.LastError != "" {
		t.Fatalf("second run failed: %+v", st)
	}
	if got := reviewStates(fake, n); got[1] != "DISMISSED" || got[2] != "APPROVED" {
		t.Errorf("review states = %v, want the old approval dismissed by the next run", got)
	}
	if calls := fake.UpdateBranches(); len(calls) != 1 {
		t.Errorf("the next run repaired again: %v", calls)
	}
}

const (
	rrExtra   = "product/extra.md"
	rrKey     = "acme/kb acme/api"
	rrPolicy  = "version: 1\nauto_merge:\n  types: [concept]\n  bundles: [product]\nlimits:\n  page_lines: 100\n  pr_lines: 150\n"
	rrRouting = runRouting + "      - page: product/extra.md\n        paths: [\"src/**\"]\n"
	rrVerdict = `{"verdict":"supported","unsupported":[]}`
)

// provPage is a page with all five provenance fields, source_sha sha.
func provPage(sha, text string) string {
	return "---\ntype: concept\nsource_repo: acme/api\nsource_sha: " + sha + "\nsource_paths: [src/a.go]\nupdated_at: 2026-11-18T10:00:00Z\ngenerated_by: okf-operator/test/m1\n---\n# Page\n\n" + text + "\n"
}

// rereview is a bot PR that passes the deterministic gate once its head is
// settled, with the cursor at the source head so a run proposes nothing.
type rereview struct {
	fake  *githubtest.Fake
	model *modelStub
	r     *SourceReconciler
	head  string // source head: the range end of the PR
	n     int
	old   string // bot branch head before the run
	// onList runs whenever the run lists the PR's reviews.
	onList func()
}

// newRereview builds a bot PR that changed runPagePath and rrExtra, both
// routed from src/**, in state state. The model answers the first writes
// calls with a page and every later call with a passing review verdict. The
// gate then reads the PR in gateState, as the fake serves it once the run has
// reached its head.
func newRereview(t *testing.T, state, gateState string, writes int) *rereview {
	t.Helper()
	var m *modelStub
	m = newModel(t, func(r *http.Request) (int, string) {
		m.mu.Lock()
		call := m.calls
		m.mu.Unlock()
		if call <= writes {
			return pageReply(r)
		}
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"role": "assistant", "content": rrVerdict}, "finish_reason": "stop",
		}}})
		return http.StatusOK, string(b)
	})
	fake, head, _, r, _, _ := newRunFixture(t, m, 0)
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", head+"\n")
	fake.SetCommit("acme/api", head)
	fake.SetFile("acme/kb", "main", ".kb/routing.yaml", rrRouting)
	fake.SetFile("acme/kb", "main", ".kb/policy.yaml", rrPolicy)
	fake.SetFile("acme/kb", "main", rrExtra, provPage(runBase, "Extra, old."))
	fake.Branch("acme/kb", "main", "okf/acme/api")
	fake.SetFile("acme/kb", "okf/acme/api", runPagePath, provPage(head, "Edited by the bot."))
	fake.SetFile("acme/kb", "okf/acme/api", rrExtra, provPage(head, "Extra, new."))
	body := "Knowledge updates.\n\n<!-- okf:range " + runBase + ".." + head + " -->\n### acme/api\n\nPages:\n\n- changed: `" + runPagePath + "`\n- changed: `" + rrExtra + "`\n"
	n := fake.AddPullRequest("acme/kb", githubtest.PullRequest{Title: "t", Body: body, Head: "okf/acme/api", Base: "main", State: "open", MergeStates: []string{state}})
	files := []githubtest.PullFile{
		{Filename: runPagePath, Status: "modified", Additions: 3, Deletions: 2},
		{Filename: rrExtra, Status: "modified", Additions: 1, Deletions: 1},
	}
	f := &rereview{fake: fake, model: m, r: r, head: head, n: n, old: fake.Ref("acme/kb", "okf/acme/api")}
	// The run lists the PR's reviews as soon as its head is settled, which is
	// when the fake can serve the PR at that head.
	fake.SetHook(func(req *http.Request) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/pulls/"+strconv.Itoa(n)+"/reviews") {
			fake.SetRepo("acme/kb", "main", fake.Ref("acme/kb", "main"))
			fake.SetPull("acme/kb", n, githubtest.Pull{Head: fake.Ref("acme/kb", "okf/acme/api"), BaseRef: "main", MergeStates: []string{gateState}})
			fake.SetPullFiles("acme/kb", n, files)
			if f.onList != nil {
				f.onList()
			}
		}
	})
	return f
}

// plan is the plan of the PR's whole range.
func (f *rereview) plan() *plan.Plan {
	return &plan.Plan{
		Version: plan.Version, Source: "acme/api", Bundle: "product", From: runBase, To: f.head,
		Pages: []plan.Page{
			{Path: runPagePath, State: plan.StateUpdate, SourcePaths: []string{"src/a.go"}},
			{Path: rrExtra, State: plan.StateUpdate, SourcePaths: []string{"src/a.go"}},
		},
	}
}

// remember makes the controller remember the plan behind head.
func (f *rereview) remember(head string) {
	f.r.Pipeline.(*Runner).gated.put(rrKey, head, f.plan())
}

func (f *rereview) run(t *testing.T) {
	t.Helper()
	if _, err := f.r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if st := getSource(t, f.r, "api").Status; st.LastError != "" {
		t.Fatalf("run failed: %+v", st)
	}
}

// posted is the reviews the review App posted.
func (f *rereview) posted() []githubtest.Review { return f.fake.Reviews("acme/kb", f.n) }

// gated is true when the run read the PR's files, as the gate does.
func (f *rereview) gated() bool {
	for _, req := range f.fake.Requests() {
		if strings.HasSuffix(req, "/pulls/"+strconv.Itoa(f.n)+"/files") {
			return true
		}
	}
	return false
}

// approvedHead fails unless exactly one review was posted, an approval of the
// bot branch head.
func (f *rereview) approvedHead(t *testing.T) {
	t.Helper()
	head := f.fake.Ref("acme/kb", "okf/acme/api")
	if got := f.posted(); len(got) != 1 || got[0].Event != "APPROVE" || got[0].CommitID != head {
		t.Fatalf("reviews = %+v, want one approval of %s", got, head)
	}
}

// After update-branch with no new source commits, the repaired head is
// reviewed with the plan of the whole range and approved again.
func TestRunnerReviewsAndApprovesAHeadAfterUpdateBranch(t *testing.T) {
	f := newRereview(t, "behind", "clean", 0)
	f.fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	f.remember(f.old)
	approveOldHead(f.fake, f.n, f.old)

	f.run(t)
	if got := f.fake.Ref("acme/kb", "okf/acme/api"); got == f.old {
		t.Fatal("the branch did not move")
	}
	if got := reviewStates(f.fake, f.n); got[1] != "DISMISSED" || got[2] != "APPROVED" {
		t.Errorf("review states = %v, want the old approval dismissed and a human's kept", got)
	}
	f.approvedHead(t)
	// One review of each page of the whole range, and no model call besides.
	if f.model.calls != 2 || !strings.Contains(f.model.bodies[0], "product/") || !strings.Contains(f.model.bodies[1], "product/") {
		t.Errorf("model calls = %d, want one review per page", f.model.calls)
	}
	if f.model.bodies[0] == f.model.bodies[1] {
		t.Error("both reviews were of the same page")
	}
}

// After a regeneration with no new source commits, the repaired head is
// reviewed over the whole range, not the regenerated page only, and approved.
func TestRunnerReviewsAndApprovesAHeadAfterARegeneration(t *testing.T) {
	f := newRereview(t, "dirty", "clean", 1)
	f.fake.SetFile("acme/kb", "main", runPagePath, humanPage)
	f.fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	approveOldHead(f.fake, f.n, f.old)

	f.run(t)
	if got := f.fake.Ref("acme/kb", "okf/acme/api"); got == f.old || len(f.fake.Parents("acme/kb", got)) != 2 {
		t.Fatalf("bot branch %s, want the regeneration commit", got)
	}
	if got := reviewStates(f.fake, f.n); got[1] != "DISMISSED" || got[2] != "APPROVED" {
		t.Errorf("review states = %v, want the old approval dismissed and a human's kept", got)
	}
	f.approvedHead(t)
	// One write for the conflicting page, then a review of both pages.
	if f.model.calls != 3 {
		t.Fatalf("model calls = %d, want 1 write and 2 reviews", f.model.calls)
	}
	reviewed := f.model.bodies[1] + f.model.bodies[2]
	if !strings.Contains(reviewed, runPagePath) || !strings.Contains(reviewed, rrExtra) {
		t.Errorf("the review covers %q, want both pages of the range", reviewed)
	}
}

// A run with no repair and no new commits only gates the head again.
func TestRunnerStillOnlyRegatesAnUnrepairedHead(t *testing.T) {
	f := newRereview(t, "clean", "clean", 0)
	f.remember(f.old)
	approveOldHead(f.fake, f.n, f.old)

	f.run(t)
	if !f.gated() {
		t.Fatal("the head was not gated again")
	}
	if len(f.posted()) != 0 || f.model.calls != 0 {
		t.Errorf("reviews %+v, model calls %d: want neither", f.posted(), f.model.calls)
	}
	if got := reviewStates(f.fake, f.n); got[1] != "APPROVED" {
		t.Errorf("review states = %v, want the approval of the head kept", got)
	}
}

// After a restart the controller no longer holds the plan behind the head, so
// a repaired head is neither gated, reviewed nor approved.
func TestRunnerDoesNotReviewARepairedHeadWithoutItsPlan(t *testing.T) {
	f := newRereview(t, "behind", "clean", 0)
	f.fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")

	f.run(t)
	if got := f.fake.Ref("acme/kb", "okf/acme/api"); got == f.old {
		t.Fatal("the branch did not move")
	}
	if len(f.posted()) != 0 || f.model.calls != 0 || f.gated() {
		t.Errorf("reviews %+v, model calls %d, gated %v: want nothing", f.posted(), f.model.calls, f.gated())
	}
}

// A repaired head that fails the gate has the approval of it dismissed and
// costs no reviewer tokens.
func TestRunnerDismissesAndDoesNotReviewARepairedHeadThatFailsTheGate(t *testing.T) {
	f := newRereview(t, "behind", "dirty", 0)
	f.fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	f.remember(f.old)
	approveOldHead(f.fake, f.n, f.old)
	// The review App's approval of the head the repair leaves the branch at.
	f.onList = func() {
		if len(f.fake.ListedReviews("acme/kb", f.n)) == 3 {
			f.fake.AddReview("acme/kb", f.n, githubtest.StoredReview{ID: 4, State: "APPROVED", CommitID: f.fake.Ref("acme/kb", "okf/acme/api"), User: "okf-writer[bot]"})
		}
	}

	f.run(t)
	if got := reviewStates(f.fake, f.n); got[1] != "DISMISSED" || got[4] != "DISMISSED" || got[2] != "APPROVED" {
		t.Errorf("review states = %v, want the review App's approvals dismissed and a human's kept", got)
	}
	if len(f.posted()) != 0 || f.model.calls != 0 {
		t.Errorf("reviews %+v, model calls %d: a failing gate must not review", f.posted(), f.model.calls)
	}
}
