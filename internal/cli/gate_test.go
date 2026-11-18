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

const (
	gateBase = "1111111111111111111111111111111111111111"
	gateHead = "2222222222222222222222222222222222222222"
	gatePlan = `{"version":1,"source":"acme/api","bundle":"product","from":"a","to":"b","initial":false,"changed_paths":[],
"pages":[{"path":"product/api.md","state":"update","type":"","source_paths":["api/x.go"]}],"unrouted":[],"excluded":[]}`
)

// gateFixture starts a GitHub fake with PR 7 in acme/kb modifying
// product/api.md, writes the plan to a file and clears credential variables.
func gateFixture(t *testing.T) (*githubtest.Fake, string) {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetRepo("acme/kb", "main", gateBase)
	fake.SetFile("acme/kb", gateBase, ".kb/policy.yaml", "version: 1\nauto_merge:\n  types: [Service]\n  bundles: [product]\nlimits:\n  page_lines: 100\n  pr_lines: 100\n")
	fake.SetFile("acme/kb", gateBase, ".kb/routing.yaml", "version: 1\nsources:\n  - repo: acme/api\n    bundle: product\n    routes:\n      - page: product/api.md\n        paths: [\"api/**\"]\n")
	fake.SetFile("acme/kb", gateHead, "product/api.md", "# API\n")
	fake.SetPull("acme/kb", 7, githubtest.Pull{Head: gateHead, BaseRef: "main"})
	fake.SetPullFiles("acme/kb", 7, []githubtest.PullFile{{Filename: "product/api.md", Status: "modified", Additions: 1, Deletions: 1}})

	planFile := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planFile, []byte(gatePlan), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"GITHUB_TOKEN", "OKF_GITHUB_APP_ID", "OKF_GITHUB_APP_PRIVATE_KEY_FILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("GITHUB_API_URL", fake.APIURL())
	return fake, planFile
}

func gateArgs(planFile string, extra ...string) []string {
	return append([]string{"gate", "--knowledge-repo", "acme/kb", "--pr", "7", "--plan", planFile}, extra...)
}

func TestGateHelp(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "gate", "-h")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"-knowledge-repo", "-pr", "-plan", "-app-id", "-app-private-key-file"} {
		if !strings.Contains(out, "  "+f+" ") {
			t.Errorf("help does not list %s:\n%s", f, out)
		}
	}
	if _, out, _ := runCLI(t, BuildInfo{}, "help"); !strings.Contains(out, "gate") {
		t.Errorf("top-level help does not list gate:\n%s", out)
	}
}

func TestGateUsageErrors(t *testing.T) {
	_, planFile := gateFixture(t)
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want []string
	}{
		{"missing pr", []string{"gate", "--knowledge-repo", "acme/kb", "--plan", planFile}, map[string]string{"GITHUB_TOKEN": "t"},
			[]string{"missing required flag --pr", "-plan"}},
		{"zero pr", gateArgs(planFile, "--pr", "0"), map[string]string{"GITHUB_TOKEN": "t"}, []string{"missing required flag --pr"}},
		{"missing plan", []string{"gate", "--knowledge-repo", "acme/kb", "--pr", "7"}, map[string]string{"GITHUB_TOKEN": "t"},
			[]string{"missing required flag --plan"}},
		{"bad repo", gateArgs(planFile, "--knowledge-repo", "acme"), map[string]string{"GITHUB_TOKEN": "t"}, []string{`--knowledge-repo "acme" is not owner/name`}},
		{"no credentials", gateArgs(planFile), nil, []string{"--app-id and --app-private-key-file", "GITHUB_TOKEN"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			code, out, errOut := runCLI(t, BuildInfo{}, tt.args...)
			if code != 2 || out != "" {
				t.Fatalf("exit %d, stdout %q", code, out)
			}
			for _, w := range tt.want {
				if !strings.Contains(errOut, w) {
					t.Errorf("stderr does not contain %q:\n%s", w, errOut)
				}
			}
		})
	}
}

func TestGateFailedGateExits0(t *testing.T) {
	_, planFile := gateFixture(t)
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	code, out, errOut := runCLI(t, BuildInfo{}, gateArgs(planFile, "--pr", "7")...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	var r struct {
		Pass  bool `json:"pass"`
		Rules []struct {
			Name string `json:"name"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stdout is not a report: %v\n%s", err, out)
	}
	if r.Pass || len(r.Rules) != 6 {
		t.Errorf("report = %s", out)
	}
}

func TestGatePlanFromStdin(t *testing.T) {
	_, _ = gateFixture(t)
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	var out, errOut strings.Builder
	env := Env{
		Stdin:  strings.NewReader(gatePlan),
		Stdout: &out,
		Stderr: &errOut,
		Logger: slog.New(slog.NewTextHandler(&errOut, nil)),
	}
	if err := runGate(context.Background(), env, gateArgs("-")[1:]); err != nil || !strings.Contains(out.String(), `"head_sha": "`+gateHead+`"`) {
		t.Fatalf("err %v, stdout %q, stderr:\n%s", err, out.String(), errOut.String())
	}
}

func TestGateRuntimeErrorsExit1(t *testing.T) {
	tests := []struct {
		name  string
		setup func(fake *githubtest.Fake, planFile string)
		want  string
	}{
		{"plan version 2", func(_ *githubtest.Fake, planFile string) {
			_ = os.WriteFile(planFile, []byte(strings.Replace(gatePlan, `"version":1`, `"version":2`, 1)), 0o600)
		}, "plan version 2 is not supported"},
		{"missing policy", func(fake *githubtest.Fake, _ string) {
			fake.SetRepo("acme/kb", "main", gateHead)
		}, ".kb/policy.yaml not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, planFile := gateFixture(t)
			t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
			tt.setup(fake, planFile)
			code, out, errOut := runCLI(t, BuildInfo{}, gateArgs(planFile)...)
			if code != 1 || out != "" || !strings.Contains(errOut, tt.want) {
				t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
			}
		})
	}
}

// TestGateAppPermissions checks that every installation token gate requests
// is one of the two narrowed scopes: the knowledge repo with pull requests,
// or the plan's source repo with contents only.
func TestGateAppPermissions(t *testing.T) {
	fake, planFile := gateFixture(t)
	keyFile, _ := githubtest.WriteKey(t)
	code, _, errOut := runCLI(t, BuildInfo{}, gateArgs(planFile, "--app-id", "7", "--app-private-key-file", keyFile)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	kb := map[string]any{
		"repositories": []any{"kb"},
		"permissions":  map[string]any{"contents": "read", "metadata": "read", "pull_requests": "read"},
	}
	src := map[string]any{
		"repositories": []any{"api"},
		"permissions":  map[string]any{"contents": "read", "metadata": "read"},
	}
	reqs := fake.TokenRequests()
	if len(reqs) == 0 || !reflect.DeepEqual(reqs[0].Body, kb) {
		t.Fatalf("first token request = %v, want %v", reqs, kb)
	}
	for i, r := range reqs[1:] {
		if !reflect.DeepEqual(r.Body, src) {
			t.Errorf("token request %d body = %v, want %v", i+1, r.Body, src)
		}
	}
}
