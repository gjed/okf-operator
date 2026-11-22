package cli

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

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
