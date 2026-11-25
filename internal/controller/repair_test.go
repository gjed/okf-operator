package controller

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
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
