package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const (
	proposeOld = "---\ntype: api\n---\n# API\n"
	proposeNew = "---\ntype: api\n---\n# API\n\nNew text.\n"
)

// proposeFixture is source acme/api with a range planCursor..planHead holding
// merged PR 12, a knowledge repo kbRepo whose default branch has product/api.md,
// a cursor at planCursor, and a checkout of the default branch in which write
// changed product/api.md. It returns the fake, the checkout, the plan and
// report files, and the knowledge base commit.
func proposeFixture(t *testing.T, kbRepo string) (fake *githubtest.Fake, dir, planFile, reportFile, base string) {
	t.Helper()
	fake = githubtest.New(t)
	fake.SetRepo("acme/api", "main", planHead)
	fake.SetCompare("acme/api", planCursor, planHead, []byte(`{"status":"ahead","total_commits":1,"commits":[{"sha":"`+planHead+`"}],"files":[]}`))
	fake.SetCommitPulls("acme/api", planHead, []byte(`[{"number":12,"merged_at":"2026-11-18T10:00:00Z"}]`))
	fake.SetRepo(kbRepo, "main", "")
	fake.SetFile(kbRepo, "main", "product/api.md", proposeOld)
	fake.SetFile(kbRepo, "kb-state", "cursors/acme/api", planCursor+"\n")
	base = fake.Ref(kbRepo, "main")

	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "product"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "product", "api.md"), []byte(proposeNew), 0o600); err != nil {
		t.Fatal(err)
	}
	files := t.TempDir()
	planFile = filepath.Join(files, "plan.json")
	reportFile = filepath.Join(files, "report.json")
	writeJSON := func(path, doc string) {
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(planFile, fmt.Sprintf(`{"version":1,"source":"acme/api","bundle":"product","from":%q,"to":%q,"initial":false,
		"changed_paths":[{"path":"api/handler.go","status":"modified","previous_path":""}],
		"pages":[{"path":"product/api.md","state":"update","type":"api","source_paths":["api/handler.go"]}],
		"unrouted":[],"excluded":[]}`, planCursor, planHead))
	writeJSON(reportFile, proposeReport(planHead))

	for _, k := range []string{"GITHUB_TOKEN", "OKF_GITHUB_APP_ID", "OKF_GITHUB_APP_PRIVATE_KEY_FILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("GITHUB_API_URL", fake.APIURL())
	return fake, dir, planFile, reportFile, base
}

func proposeReport(to string) string {
	return fmt.Sprintf(`{"version":1,"source":"acme/api","bundle":"product","from":%q,"to":%q,"model":"m1","generated_by":"okf-operator dev",
		"pages":[{"path":"product/api.md","result":"changed","reason":"","attempts":1,"tokens":{"prompt":10,"completion":5}}],
		"tokens":{"prompt":10,"completion":5}}`, planCursor, to)
}

func proposeArgs(kbRepo, dir, planFile, reportFile, base string, extra ...string) []string {
	return append([]string{"propose", "--plan", planFile, "--report", reportFile,
		"--knowledge-repo", kbRepo, "--knowledge-dir", dir, "--knowledge-base", base}, extra...)
}

func TestProposeHelp(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "propose", "-h")
	if code != 0 || !strings.Contains(out, "-knowledge-base") || !strings.Contains(out, "-report") {
		t.Fatalf("exit %d, stdout:\n%s", code, out)
	}
}

func TestProposeUsageErrors(t *testing.T) {
	_, dir, planFile, reportFile, base := proposeFixture(t, "acme/kb")
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing knowledge base", []string{"propose", "--plan", planFile, "--report", reportFile, "--knowledge-repo", "acme/kb", "--knowledge-dir", dir}, "missing required flag --knowledge-base"},
		{"short knowledge base", proposeArgs("acme/kb", dir, planFile, reportFile, base[:7]), "--knowledge-base"},
		{"plan from stdin", proposeArgs("acme/kb", dir, "-", reportFile, base), "--plan must be a file"},
		{"bad knowledge repo", proposeArgs("acme", dir, planFile, reportFile, base), "is not owner/name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := runCLI(t, BuildInfo{}, tt.args...)
			if code != 2 || !strings.Contains(errOut, tt.want) {
				t.Fatalf("exit %d, want 2 and %q in stderr:\n%s", code, tt.want, errOut)
			}
		})
	}
}

func TestProposeOpensPRWithReportFromStdin(t *testing.T) {
	fake, dir, planFile, _, base := proposeFixture(t, "acme/kb")
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	var out, errOut strings.Builder
	env := Env{
		Stdin:  strings.NewReader(proposeReport(planHead)),
		Stdout: &out,
		Stderr: &errOut,
		Logger: slog.New(slog.NewTextHandler(&errOut, nil)),
	}
	args := proposeArgs("acme/kb", dir, planFile, "-", base)[1:]
	if err := runPropose(context.Background(), env, args); err != nil {
		t.Fatalf("runPropose: %v\nstderr:\n%s", err, errOut.String())
	}
	var res struct {
		Version        int    `json:"version"`
		Action         string `json:"action"`
		Commit         string `json:"commit"`
		CursorAdvanced bool   `json:"cursor_advanced"`
		PR             *struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
		} `json:"pr"`
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatalf("stdout is not the result: %v\n%s", err, out.String())
	}
	if res.Version != 1 || res.Action != "opened" || res.PR == nil || res.PR.Number != 1 || !res.CursorAdvanced ||
		res.Commit != fake.Ref("acme/kb", "okf/acme/api") {
		t.Fatalf("result = %+v\n%s", res, out.String())
	}
	if got := fake.Files("acme/kb", "kb-state")["cursors/acme/api"]; got != planHead+"\n" {
		t.Fatalf("cursor = %q", got)
	}
	if body := fake.PullRequests("acme/kb")[0].Body; !strings.Contains(body, "- acme/api#12\n") {
		t.Fatalf("body does not link source PR 12:\n%s", body)
	}
}

func TestProposeMismatchMakesNoRequest(t *testing.T) {
	fake, dir, planFile, reportFile, base := proposeFixture(t, "acme/kb")
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	if err := os.WriteFile(reportFile, []byte(proposeReport(planCursor)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, BuildInfo{}, proposeArgs("acme/kb", dir, planFile, reportFile, base)...)
	if code != 1 || !strings.Contains(errOut, "disagree on to") {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	if r := fake.Requests(); len(r) != 0 {
		t.Fatalf("requests = %v, want none", r)
	}
}

func TestProposeAppModeTokens(t *testing.T) {
	for _, kbRepo := range []string{"acme/kb", "kbowner/kb"} {
		t.Run(kbRepo, func(t *testing.T) {
			fake, dir, planFile, reportFile, base := proposeFixture(t, kbRepo)
			keyFile, _ := githubtest.WriteKey(t)
			code, out, errOut := runCLI(t, BuildInfo{}, proposeArgs(kbRepo, dir, planFile, reportFile, base,
				"--app-id", "7", "--app-private-key-file", keyFile)...)
			if code != 0 {
				t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
			}
			type req struct {
				repos any
				perms any
			}
			var got []req
			for _, tr := range fake.TokenRequests() {
				got = append(got, req{tr.Body["repositories"], tr.Body["permissions"]})
			}
			want := []req{
				{[]any{"kb"}, map[string]any{"contents": "write", "metadata": "read", "pull_requests": "write"}},
				{[]any{"api"}, map[string]any{"contents": "read", "metadata": "read", "pull_requests": "read"}},
			}
			if len(got) != 2 {
				t.Fatalf("token requests = %+v, want 2", got)
			}
			for _, w := range want {
				if !slices.ContainsFunc(got, func(g req) bool { return reflect.DeepEqual(g, w) }) {
					t.Errorf("no token request %+v in %+v", w, got)
				}
			}
		})
	}
}

// TestProposeSecretsNeverLeak runs propose in App mode at debug level. The
// PR create call fails with a body that echoes the installation token. No
// token, JWT or key line may reach stdout, stderr or the spans.
func TestProposeSecretsNeverLeak(t *testing.T) {
	fake, dir, planFile, reportFile, base := proposeFixture(t, "acme/kb")
	keyFile, pem := githubtest.WriteKey(t)
	fake.FailStatus["POST /repos/acme/kb/pulls"] = http.StatusUnprocessableEntity
	fake.EchoAuth = true
	spans := captureSpans(t)

	code, out, errOut := runCLI(t, BuildInfo{Version: "dev", Commit: "none"},
		append([]string{"--log-level", "debug", "--log-format", "json"},
			proposeArgs("acme/kb", dir, planFile, reportFile, base, "--app-id", "7", "--app-private-key-file", keyFile)...)...)
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q, want 1 and empty; stderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "minted installation token") || !strings.Contains(errOut, "[REDACTED]") {
		t.Fatalf("debug logging or the echo path did not run:\n%s", errOut)
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
	if !slices.ContainsFunc(spans.ended(), func(s sdktrace.ReadOnlySpan) bool { return s.Name() == "propose" }) {
		t.Fatal("no propose span recorded")
	}
	secrets := []string{fake.InstallationToken}
	for _, tr := range fake.TokenRequests() {
		secrets = append(secrets, strings.TrimPrefix(tr.Auth, "Bearer "))
	}
	for line := range strings.SplitSeq(pem, "\n") {
		if len(line) > 20 {
			secrets = append(secrets, line)
		}
	}
	for _, s := range secrets {
		if s == "" {
			t.Fatal("empty secret in the list: the check would pass vacuously")
		}
		if strings.Contains(dump.String(), s) {
			t.Errorf("secret %.12s... leaked into stdout, stderr or spans", s)
		}
	}
	if got := fake.Files("acme/kb", "kb-state")["cursors/acme/api"]; got != planCursor+"\n" {
		t.Fatalf("cursor = %q, want it unchanged after the failed PR call", got)
	}
}
