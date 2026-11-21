package cli

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const passingReport = `{"version":1,"pr":7,"head_sha":"2222222222222222222222222222222222222222","pass":true,"rules":[
{"name":"links","pass":true,"details":[]},{"name":"review","pass":true,"details":[]}]}`

// approveFixture starts a GitHub fake with PR 7 of acme/kb at gateHead,
// writes report to a file and clears every credential variable.
func approveFixture(t *testing.T, report string) (*githubtest.Fake, string) {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetPull("acme/kb", 7, githubtest.Pull{Head: gateHead, BaseRef: "main"})
	file := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(file, []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"GITHUB_TOKEN", "OKF_GITHUB_APP_ID", "OKF_GITHUB_APP_PRIVATE_KEY_FILE", EnvReviewAppID, EnvReviewAppKeyFile, EnvReviewToken} {
		t.Setenv(k, "")
	}
	t.Setenv("GITHUB_API_URL", fake.APIURL())
	return fake, file
}

func approveArgs(file string, extra ...string) []string {
	return append([]string{"approve", "--knowledge-repo", "acme/kb", "--pr", "7", "--report", file}, extra...)
}

type outcome struct {
	Version  int    `json:"version"`
	PR       int    `json:"pr"`
	HeadSHA  string `json:"head_sha"`
	Approved bool   `json:"approved"`
	Reason   string `json:"reason"`
}

func decodeOutcome(t *testing.T, out string) outcome {
	t.Helper()
	var o outcome
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("stdout is not an outcome: %v\n%s", err, out)
	}
	return o
}

func TestApproveHelp(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "approve", "-h")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"-knowledge-repo", "-pr", "-report", "-review-app-id", "-review-app-private-key-file"} {
		if !strings.Contains(out, "  "+f+" ") {
			t.Errorf("help does not list %s:\n%s", f, out)
		}
	}
	if strings.Contains(out, "  -app-id ") {
		t.Errorf("approve offers the writer's --app-id:\n%s", out)
	}
}

func TestApprovePostsOnHead(t *testing.T) {
	fake, file := approveFixture(t, passingReport)
	t.Setenv(EnvReviewToken, "ghp_reviewToken0123456789")
	t.Setenv("GITHUB_TOKEN", "ghp_writerToken0123456789")
	code, out, errOut := runCLI(t, BuildInfo{}, approveArgs(file)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	if o := decodeOutcome(t, out); o != (outcome{Version: 1, PR: 7, HeadSHA: gateHead, Approved: true}) {
		t.Fatalf("outcome = %+v", o)
	}
	reviews := fake.Reviews("acme/kb", 7)
	if len(reviews) != 1 || reviews[0].Event != "APPROVE" || reviews[0].CommitID != gateHead {
		t.Fatalf("reviews = %+v", reviews)
	}
	if reviews[0].Auth != "Bearer ghp_reviewToken0123456789" {
		t.Errorf("approval sent with %q, want the review token", reviews[0].Auth)
	}
}

func TestApproveDeclines(t *testing.T) {
	tests := []struct {
		name, report, reason string
		head                 string
	}{
		{"gate failed", strings.Replace(passingReport, `"pass":true,"rules"`, `"pass":false,"rules"`, 1), "gate_failed", gateHead},
		{"edited pass", strings.Replace(passingReport, `"links","pass":true,"details":[]`, `"links","pass":false,"details":["x"]`, 1), "gate_failed", gateHead},
		{"review missing", strings.Replace(passingReport, `"review"`, `"size"`, 1), "review_missing", gateHead},
		{"head moved", passingReport, "head_moved", gateBase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, file := approveFixture(t, tt.report)
			fake.SetPull("acme/kb", 7, githubtest.Pull{Head: tt.head, BaseRef: "main"})
			t.Setenv(EnvReviewToken, "ghp_reviewToken0123456789")
			code, out, errOut := runCLI(t, BuildInfo{}, approveArgs(file)...)
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", code, errOut)
			}
			if o := decodeOutcome(t, out); o.Approved || o.Reason != tt.reason {
				t.Fatalf("outcome = %+v, want reason %s", o, tt.reason)
			}
			if w := fake.Writes(); len(w) != 0 {
				t.Errorf("approve wrote to GitHub: %v", w)
			}
		})
	}
}

func TestApproveRuntimeErrorsExit1(t *testing.T) {
	for name, report := range map[string]string{
		"other PR":      strings.Replace(passingReport, `"pr":7`, `"pr":8`, 1),
		"version 2":     strings.Replace(passingReport, `"version":1`, `"version":2`, 1),
		"unknown field": strings.Replace(passingReport, `"pass":true,"rules"`, `"pass":true,"merge":true,"rules"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			fake, file := approveFixture(t, report)
			t.Setenv(EnvReviewToken, "ghp_reviewToken0123456789")
			code, out, _ := runCLI(t, BuildInfo{}, approveArgs(file)...)
			if code != 1 || out != "" || len(fake.Reviews("acme/kb", 7)) != 0 {
				t.Fatalf("exit %d, stdout %q", code, out)
			}
		})
	}
}

func TestApproveReportFromStdin(t *testing.T) {
	fake, _ := approveFixture(t, passingReport)
	t.Setenv(EnvReviewToken, "ghp_reviewToken0123456789")
	var out, errOut strings.Builder
	env := Env{Stdin: strings.NewReader(passingReport), Stdout: &out, Stderr: &errOut, Logger: slog.New(slog.NewTextHandler(&errOut, nil))}
	if err := runApprove(context.Background(), env, approveArgs("-")[1:]); err != nil || len(fake.Reviews("acme/kb", 7)) != 1 {
		t.Fatalf("err %v, stdout %q, stderr:\n%s", err, out.String(), errOut.String())
	}
}

// TestApproveIgnoresWriterCredentials checks that approve never falls back to
// the writer's App or GITHUB_TOKEN.
func TestApproveIgnoresWriterCredentials(t *testing.T) {
	fake, file := approveFixture(t, passingReport)
	keyFile, _ := githubtest.WriteKey(t)
	t.Setenv("GITHUB_TOKEN", "ghp_writerToken0123456789")
	t.Setenv("OKF_GITHUB_APP_ID", "7")
	t.Setenv("OKF_GITHUB_APP_PRIVATE_KEY_FILE", keyFile)
	code, out, errOut := runCLI(t, BuildInfo{}, approveArgs(file)...)
	if code != 2 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	for _, w := range []string{"--review-app-id", EnvReviewAppID, EnvReviewToken} {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr does not name %s:\n%s", w, errOut)
		}
	}
	if n := len(fake.Requests()); n != 0 {
		t.Errorf("approve called GitHub %d times", n)
	}
}

// TestWriterCommandsIgnoreReviewCredentials checks that write and propose
// never load the review App.
func TestWriterCommandsIgnoreReviewCredentials(t *testing.T) {
	_, dir, planFile := writeFixture(t)
	keyFile, _ := githubtest.WriteKey(t)
	t.Setenv(EnvReviewAppID, "9")
	t.Setenv(EnvReviewAppKeyFile, keyFile)
	t.Setenv(EnvReviewToken, "ghp_reviewToken0123456789")
	model := newModelStub(t, noChange)
	for _, args := range [][]string{
		{"write", "--plan", planFile, "--knowledge-dir", dir, "--model-base-url", model.URL, "--model", "m1"},
		{"propose", "--plan", planFile, "--knowledge-dir", dir, "--knowledge-repo", "acme/kb", "--knowledge-base", gateBase, "--report", planFile},
	} {
		code, _, errOut := runCLI(t, BuildInfo{}, args...)
		if code != 2 || !strings.Contains(errOut, "no GitHub credentials") {
			t.Errorf("%s: exit %d, stderr:\n%s", args[0], code, errOut)
		}
	}
}

func TestApproveAppPermissions(t *testing.T) {
	fake, file := approveFixture(t, passingReport)
	keyFile, _ := githubtest.WriteKey(t)
	code, _, errOut := runCLI(t, BuildInfo{}, approveArgs(file, "--review-app-id", "9", "--review-app-private-key-file", keyFile)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	reqs := fake.TokenRequests()
	want := map[string]any{"repositories": []any{"kb"}, "permissions": map[string]any{"contents": "read", "metadata": "read", "pull_requests": "write"}}
	if len(reqs) != 1 || !reflect.DeepEqual(reqs[0].Body, want) {
		t.Fatalf("token requests = %+v, want one with %v", reqs, want)
	}
}

// TestApproveSecretsNeverLeak runs approve with the review App at debug level
// against a fake that echoes the Authorization header in a 500 on the review
// endpoint. Neither the review App's key, its JWT, its installation token nor
// GITHUB_TOKEN may reach stdout, stderr or the spans.
func TestApproveSecretsNeverLeak(t *testing.T) {
	fake, file := approveFixture(t, passingReport)
	keyFile, pem := githubtest.WriteKey(t)
	const ghToken = "ghp_githubTokenDistinct0123456789"
	t.Setenv("GITHUB_TOKEN", ghToken)
	fake.InstallationToken = "ghs_reviewInstallationToken0123456789"
	fake.FailStatus["POST /repos/acme/kb/pulls/7/reviews"] = 500
	fake.EchoAuth = true
	spans := captureSpans(t)

	code, out, errOut := runCLI(t, BuildInfo{}, append([]string{"--log-level", "debug", "--log-format", "json"},
		approveArgs(file, "--review-app-id", "9", "--review-app-private-key-file", keyFile)...)...)
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "[REDACTED]") || !strings.Contains(errOut, "minted installation token") {
		t.Fatalf("stderr does not show the redacted echo at debug level:\n%s", errOut)
	}
	var dump strings.Builder
	dump.WriteString(out + errOut)
	for _, s := range spans.ended() {
		dump.WriteString(s.Name() + s.Status().Description)
		for _, kv := range s.Attributes() {
			dump.WriteString(kv.Value.String())
		}
		for _, e := range s.Events() {
			for _, kv := range e.Attributes {
				dump.WriteString(kv.Value.String())
			}
		}
	}
	secrets := []string{ghToken, fake.InstallationToken}
	for _, a := range fake.Auths() {
		if v := strings.TrimPrefix(a, "Bearer "); v != "" {
			secrets = append(secrets, v)
		}
	}
	for line := range strings.SplitSeq(pem, "\n") {
		if len(line) > 20 {
			secrets = append(secrets, line)
		}
	}
	for _, s := range secrets {
		if strings.Contains(dump.String(), s) {
			t.Errorf("secret %.12s... leaked into stdout, stderr or spans", s)
		}
	}
	for _, a := range fake.Auths() {
		if strings.Contains(a, ghToken) {
			t.Error("approve sent GITHUB_TOKEN")
		}
	}
}
