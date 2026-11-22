package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const (
	reconcileCursor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	reconcileBase   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	reconcilePatch  = "@@ -1 +1 @@\n-package old\n+package a\n"
	reconcileNew    = "---\ntype: concept\n---\n# API\n\nPackage a.\n"
)

// reconcileFixture is source acme/api with src/a.go at main, a diverged
// cursor, and knowledge repo acme/kb whose default branch, mirrored in the
// returned checkout, has product/api.md behind main and product/gone.md
// whose source is gone. It returns the fake, the checkout and the source head.
func reconcileFixture(t *testing.T) (*githubtest.Fake, string, string) {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetFile("acme/api", "main", "src/a.go", "package a\n")
	head := fake.Ref("acme/api", "main")
	fake.SetRepo("acme/api", "main", head)
	fake.SetCompare("acme/api", reconcileCursor, head, []byte(`{"status":"diverged","files":[]}`))
	fake.SetCompare("acme/api", reconcileBase, head, []byte(`{"status":"ahead","files":[
		{"filename":"src/a.go","status":"modified","changes":2,"patch":"@@ -1 +1 @@\n-package old\n+package a\n"}]}`))

	fake.SetRepo("acme/kb", "main", "")
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", reconcileCursor+"\n")
	dir := t.TempDir()
	for name, content := range map[string]string{
		".kb/routing.yaml": "version: 1\nsources:\n  - repo: acme/api\n    bundle: product\n    routes:\n" +
			"      - page: product/api.md\n        paths: [\"src/**\"]\n      - page: product/gone.md\n        paths: [\"gone/**\"]\n",
		"product/api.md": "---\ntype: concept\nsource_repo: acme/api\nsource_sha: " + reconcileBase +
			"\nsource_paths: [src/a.go]\n---\n# API\n\nPackage old.\n",
		"product/gone.md": "---\ntype: concept\nsource_repo: acme/api\nsource_sha: " + reconcileBase +
			"\nsource_paths: [gone/g.go]\n---\n# Gone\n",
	} {
		fake.SetFile("acme/kb", "main", name, content)
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"GITHUB_TOKEN", "OKF_GITHUB_APP_ID", "OKF_GITHUB_APP_PRIVATE_KEY_FILE", EnvModelAPIKey} {
		t.Setenv(k, "")
	}
	t.Setenv("GITHUB_API_URL", fake.APIURL())
	return fake, dir, head
}

func reconcileArgs(dir string, extra ...string) []string {
	return append([]string{"reconcile", "--source", "acme/api", "--knowledge-repo", "acme/kb", "--knowledge-dir", dir}, extra...)
}

func TestReconcileHelpAndUsage(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "reconcile", "-h")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"-source", "-knowledge-repo", "-knowledge-dir", "-routing", "-to", "-app-id", "-app-private-key-file"} {
		if !strings.Contains(out, "  "+f+" ") {
			t.Errorf("help does not list %s:\n%s", f, out)
		}
	}
	if _, out, _ := runCLI(t, BuildInfo{}, "help"); !strings.Contains(out, "reconcile") {
		t.Errorf("top-level help does not list reconcile:\n%s", out)
	}
	_, dir, _ := reconcileFixture(t)
	t.Setenv("GITHUB_TOKEN", "t")
	for name, args := range map[string][]string{
		"missing source": {"reconcile", "--knowledge-repo", "acme/kb", "--knowledge-dir", dir},
		"short --to":     reconcileArgs(dir, "--to", "abc"),
	} {
		if code, out, errOut := runCLI(t, BuildInfo{}, args...); code != 2 || out != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr:\n%s", name, code, out, errOut)
		}
	}
}

func TestReconcileRuntimeErrorsExit1(t *testing.T) {
	t.Run("truncated tree", func(t *testing.T) {
		fake, dir, _ := reconcileFixture(t)
		t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
		fake.TruncateTrees = true
		code, out, errOut := runCLI(t, BuildInfo{}, reconcileArgs(dir)...)
		if code != 1 || out != "" || !strings.Contains(errOut, "truncated") {
			t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
		}
	})
	t.Run("source not onboarded", func(t *testing.T) {
		_, dir, _ := reconcileFixture(t)
		t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
		code, out, errOut := runCLI(t, BuildInfo{}, reconcileArgs(dir, "--source", "acme/web")...)
		if code != 1 || out != "" || !strings.Contains(errOut, "not in the routing map") {
			t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
		}
	})
}

// reconcile → write → propose on a diverged cursor: the stale page is
// refreshed from its own base, the deletion candidate is reported and kept,
// and the cursor moves to the source head only once the PR is open.
func TestReconcileWriteProposeReanchors(t *testing.T) {
	fake, dir, head := reconcileFixture(t)
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	files := t.TempDir()
	planFile, reportFile := filepath.Join(files, "plan.json"), filepath.Join(files, "report.json")

	code, out, errOut := runCLI(t, BuildInfo{}, reconcileArgs(dir)...)
	if code != 0 {
		t.Fatalf("reconcile exit %d:\n%s", code, errOut)
	}
	var p struct {
		Version   int    `json:"version"`
		From      string `json:"from"`
		To        string `json:"to"`
		Reconcile struct {
			Cursor             string `json:"cursor"`
			DeletionCandidates []struct {
				Path string `json:"path"`
			} `json:"deletion_candidates"`
			Pages []struct {
				Path, Reason, Evidence, Base string
			} `json:"pages"`
		} `json:"reconcile"`
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatal(err)
	}
	if p.Version != 2 || p.From != reconcileCursor || p.To != head || p.Reconcile.Cursor != "diverged" ||
		len(p.Reconcile.Pages) != 1 || p.Reconcile.Pages[0].Path != "product/api.md" || p.Reconcile.Pages[0].Base != reconcileBase ||
		len(p.Reconcile.DeletionCandidates) != 1 || p.Reconcile.DeletionCandidates[0].Path != "product/gone.md" {
		t.Fatalf("plan:\n%s", out)
	}
	if err := os.WriteFile(planFile, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}

	model := newModelStub(t, func(*http.Request) (int, string) {
		reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"role": "assistant", "content": reconcileNew}, "finish_reason": "stop",
		}}})
		return http.StatusOK, string(reply)
	})
	code, out, errOut = runCLI(t, BuildInfo{}, "write", "--plan", planFile, "--knowledge-dir", dir,
		"--model-base-url", model.URL+"/v1", "--model", "m1")
	if code != 0 || !strings.Contains(out, `"result": "changed"`) {
		t.Fatalf("write exit %d, stdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if err := os.WriteFile(reportFile, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	if reqs := model.requests(); len(reqs) != 1 || !strings.Contains(string(reqs[0].Body), "Diff of src/a.go") {
		t.Fatalf("model requests = %d, want one with the base patch", len(reqs))
	}

	base := fake.Ref("acme/kb", "main")
	propose := []string{"propose", "--plan", planFile, "--report", reportFile,
		"--knowledge-repo", "acme/kb", "--knowledge-dir", dir, "--knowledge-base", base}
	cursorFile := func() string { return fake.Files("acme/kb", "kb-state")["cursors/acme/api"] }

	fake.FailStatus["POST /repos/acme/kb/pulls"] = http.StatusBadGateway
	if code, _, errOut := runCLI(t, BuildInfo{}, propose...); code != 1 {
		t.Fatalf("propose with a failing PR call exit %d:\n%s", code, errOut)
	}
	if c := cursorFile(); c != reconcileCursor+"\n" {
		t.Fatalf("cursor after a failed run = %q, want the diverged cursor", c)
	}

	delete(fake.FailStatus, "POST /repos/acme/kb/pulls")
	if code, out, errOut := runCLI(t, BuildInfo{}, propose...); code != 0 || !strings.Contains(out, `"cursor_advanced": true`) {
		t.Fatalf("propose exit %d, stdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if c := cursorFile(); c != head+"\n" {
		t.Fatalf("cursor = %q, want the source head", c)
	}
	pulls := fake.PullRequests("acme/kb")
	if len(pulls) != 1 || !strings.Contains(pulls[0].Body, "`product/gone.md`") || !strings.Contains(pulls[0].Body, "re-anchored") {
		t.Fatalf("pulls = %+v", pulls)
	}
	committed := fake.Files("acme/kb", "okf/acme/api")
	if _, ok := committed["product/gone.md"]; !ok || !strings.Contains(committed["product/api.md"], "Package a.") ||
		!strings.Contains(committed["product/api.md"], head) {
		t.Fatalf("committed files = %v", committed)
	}
	if _, err := os.Stat(filepath.Join(dir, "product", "gone.md")); err != nil {
		t.Errorf("deletion candidate removed from the checkout: %v", err)
	}
}

// gate and review read plans through the decoder write uses, which now also
// reads version 2; a reconcile plan must still fail closed in both.
func TestGateAndReviewRefuseReconcilePlan(t *testing.T) {
	for _, cmd := range []string{"gate", "review"} {
		t.Run(cmd, func(t *testing.T) {
			fake, planFile := reviewFixture(t)
			t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
			v2 := strings.Replace(gatePlan, `"version":1`, `"version":2`, 1)
			v2 = strings.Replace(v2, `"excluded":[]}`, `"excluded":[],"reconcile":{"cursor":"diverged",
"pages":[{"path":"product/api.md","reason":"stale","evidence":"files","base":""}],"deletion_candidates":[]}}`, 1)
			if err := os.WriteFile(planFile, []byte(v2), 0o600); err != nil {
				t.Fatal(err)
			}
			model := newModelStub(t, func(*http.Request) (int, string) { return http.StatusOK, reviewSupported })
			args := gateArgs(planFile)
			if cmd == "review" {
				args = reviewArgs(planFile, model.URL)
			}
			before := len(fake.Requests())
			code, out, errOut := runCLI(t, BuildInfo{}, args...)
			if code != 1 || out != "" || !strings.Contains(errOut, "plan version 2 is not supported") {
				t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
			}
			if n := len(fake.Requests()) - before; n != 0 {
				t.Errorf("%s made %d GitHub requests, want 0", cmd, n)
			}
		})
	}
}
