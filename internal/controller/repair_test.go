package controller

import (
	"context"
	"slices"
	"strings"
	"testing"

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

func TestRunnerUpdatesABehindBotPR(t *testing.T) {
	fake, m, r, head, n := botPRFixture(t, "", "behind")
	fake.SetFile("acme/kb", "main", "product/news.md", "---\ntype: concept\n---\n# News\n")
	old := fake.Ref("acme/kb", "okf/acme/api")
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
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

	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
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
	fake, _, r, _, _ := botPRFixture(t, "", "unknown")
	botHead := fake.Ref("acme/kb", "okf/acme/api")
	if _, err := r.Reconcile(context.Background(), request("api")); err != nil {
		t.Fatal(err)
	}
	if calls := fake.UpdateBranches(); len(calls) != 0 || fake.Ref("acme/kb", "okf/acme/api") != botHead {
		t.Fatalf("update calls %v, branch moved to %s", calls, fake.Ref("acme/kb", "okf/acme/api"))
	}
}
