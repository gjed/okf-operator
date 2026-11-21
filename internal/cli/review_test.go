package cli

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/codes"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const reviewSupported = `{"choices":[{"message":{"role":"assistant","content":"{\"verdict\":\"supported\",\"unsupported\":[]}"},"finish_reason":"stop"}]}`

// reviewFixture extends gateFixture with the page at the base and the compare
// of the plan's range, and clears every model variable.
func reviewFixture(t *testing.T) (*githubtest.Fake, string) {
	t.Helper()
	fake, planFile := gateFixture(t)
	fake.SetFile("acme/kb", gateBase, "product/api.md", "---\ntype: Service\n---\n# API\n")
	fake.SetCompare("acme/api", "a", gateSource, []byte(`{"status":"ahead","files":[
		{"filename":"api/x.go","status":"modified","patch":"@@ -1 +1 @@\n-old\n+new\n","changes":2}]}`))
	for _, k := range []string{EnvReviewModelBaseURL, EnvReviewModel, EnvReviewModelAPIKey, EnvModelBaseURL, EnvModel, EnvModelAPIKey} {
		t.Setenv(k, "")
	}
	return fake, planFile
}

func reviewArgs(planFile, modelURL string, extra ...string) []string {
	return append([]string{"review", "--knowledge-repo", "acme/kb", "--pr", "7", "--plan", planFile,
		"--review-model-base-url", modelURL, "--review-model", "r1"}, extra...)
}

func TestReviewHelp(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "review", "-h")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"-knowledge-repo", "-pr", "-plan", "-review-model-base-url", "-review-model", "-max-tokens-per-page", "-app-id"} {
		if !strings.Contains(out, "  "+f+" ") {
			t.Errorf("help does not list %s:\n%s", f, out)
		}
	}
	if _, out, _ := runCLI(t, BuildInfo{}, "help"); !strings.Contains(out, "review") {
		t.Errorf("top-level help does not list review:\n%s", out)
	}
}

func TestReviewUsageErrors(t *testing.T) {
	_, planFile := reviewFixture(t)
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	model := newModelStub(t, func(*http.Request) (int, string) { return http.StatusOK, reviewSupported })
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want []string
	}{
		{"missing pr", []string{"review", "--knowledge-repo", "acme/kb", "--plan", planFile, "--review-model-base-url", model.URL, "--review-model", "r1"},
			nil, []string{"missing required flag --pr", "-review-model"}},
		{"writer settings only", []string{"review", "--knowledge-repo", "acme/kb", "--pr", "7", "--plan", planFile},
			map[string]string{EnvModelBaseURL: model.URL, EnvModel: "w1"},
			[]string{"--review-model-base-url", EnvReviewModelBaseURL, "--review-model", EnvReviewModel}},
		{"zero budget", reviewArgs(planFile, model.URL, "--max-tokens-per-page", "0"), nil, []string{"--max-tokens-per-page 0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			code, out, errOut := runCLI(t, BuildInfo{}, tt.args...)
			if code != 2 || out != "" {
				t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
			}
			for _, w := range tt.want {
				if !strings.Contains(errOut, w) {
					t.Errorf("stderr does not contain %q:\n%s", w, errOut)
				}
			}
		})
	}
	if n := len(model.requests()); n != 0 {
		t.Fatalf("usage errors reached the model %d times", n)
	}
}

func TestReviewEndToEnd(t *testing.T) {
	fake, planFile := reviewFixture(t)
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	t.Setenv(EnvReviewModelAPIKey, "rk-reviewer-key-0123456789")
	t.Setenv(EnvModelAPIKey, "wk-writer-key-0123456789")
	model := newModelStub(t, func(*http.Request) (int, string) { return http.StatusOK, reviewSupported })
	code, out, errOut := runCLI(t, BuildInfo{}, reviewArgs(planFile, model.URL)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	var r struct {
		Version int    `json:"version"`
		PR      int    `json:"pr"`
		HeadSHA string `json:"head_sha"`
		Model   string `json:"model"`
		Pass    bool   `json:"pass"`
		Pages   []struct {
			Path string `json:"path"`
			Pass bool   `json:"pass"`
		} `json:"pages"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stdout is not a result: %v\n%s", err, out)
	}
	if r.Version != 1 || r.PR != 7 || r.HeadSHA != gateHead || r.Model != "r1" || !r.Pass || len(r.Pages) != 1 || r.Pages[0].Path != "product/api.md" {
		t.Fatalf("result = %s", out)
	}
	reqs := model.requests()
	if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "Bearer rk-reviewer-key-0123456789" {
		t.Fatalf("model requests = %+v, want one with the reviewer key", reqs)
	}
	if strings.Contains(string(reqs[0].Body), "wk-writer-key") {
		t.Error("writer key reached the reviewer")
	}
	if w := fake.Writes(); len(w) != 0 {
		t.Errorf("review wrote to GitHub: %v", w)
	}
}

func TestReviewModelErrorExit1(t *testing.T) {
	_, planFile := reviewFixture(t)
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	model := newModelStub(t, func(*http.Request) (int, string) { return http.StatusInternalServerError, `{"error":"down"}` })
	code, out, errOut := runCLI(t, BuildInfo{}, reviewArgs(planFile, model.URL)...)
	if code != 1 || out != "" || !strings.Contains(errOut, "HTTP 500") {
		t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
	}
}

func TestReviewAppPermissions(t *testing.T) {
	fake, planFile := reviewFixture(t)
	keyFile, _ := githubtest.WriteKey(t)
	model := newModelStub(t, func(*http.Request) (int, string) { return http.StatusOK, reviewSupported })
	code, _, errOut := runCLI(t, BuildInfo{}, reviewArgs(planFile, model.URL, "--app-id", "7", "--app-private-key-file", keyFile)...)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	want := []map[string]any{
		{"repositories": []any{"kb"}, "permissions": map[string]any{"contents": "read", "metadata": "read", "pull_requests": "read"}},
		{"repositories": []any{"api"}, "permissions": map[string]any{"contents": "read", "metadata": "read"}},
	}
	reqs := fake.TokenRequests()
	if len(reqs) != 2 {
		t.Fatalf("want 2 token requests, got %d: %v", len(reqs), reqs)
	}
	for i := range want {
		if !reflect.DeepEqual(reqs[i].Body, want[i]) {
			t.Errorf("token request %d body = %v, want %v", i, reqs[i].Body, want[i])
		}
	}
}

// TestReviewSecretsNeverLeak runs review in App mode at debug level against a
// reviewer that answers 401 echoing its key and Authorization header, with
// the writer key and GITHUB_TOKEN set too. No secret may reach stdout, stderr,
// the spans, or a request to the wrong endpoint.
func TestReviewSecretsNeverLeak(t *testing.T) {
	fake, planFile := reviewFixture(t)
	keyFile, pem := githubtest.WriteKey(t)
	const reviewKey = "sk-review-KEY-0a1b2c3d4e5f6789"
	const writerKey = "sk-writer-KEY-9f8e7d6c5b4a3210"
	const ghToken = "ghp_githubTokenDistinct0123456789"
	t.Setenv(EnvReviewModelAPIKey, reviewKey)
	t.Setenv(EnvModelAPIKey, writerKey)
	t.Setenv("GITHUB_TOKEN", ghToken)
	model := newModelStub(t, func(r *http.Request) (int, string) {
		body, _ := json.Marshal(map[string]string{"error": "invalid key " + reviewKey, "authorization": r.Header.Get("Authorization")})
		return http.StatusUnauthorized, string(body)
	})
	spans := captureSpans(t)

	code, out, errOut := runCLI(t, BuildInfo{Version: "dev", Commit: "none"},
		append([]string{"--log-level", "debug", "--log-format", "json"},
			reviewArgs(planFile, model.URL, "--app-id", "7", "--app-private-key-file", keyFile)...)...)
	if code != 1 || out != "" {
		t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "HTTP 401") || !strings.Contains(errOut, "[REDACTED]") || !strings.Contains(errOut, "minted installation token") {
		t.Fatalf("stderr does not show the redacted 401 at debug level:\n%s", errOut)
	}

	var dump strings.Builder
	dump.WriteString(out + errOut)
	failed := 0
	for _, s := range spans.ended() {
		if s.Name() == "review" && s.Status().Code == codes.Error {
			failed++
		}
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
	if failed != 1 {
		t.Fatalf("review spans with error status = %d, want 1", failed)
	}
	secrets := []string{reviewKey, writerKey, ghToken, fake.InstallationToken}
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
		if strings.Contains(a, reviewKey) || strings.Contains(a, ghToken) {
			t.Error("GitHub fake received the reviewer key or GITHUB_TOKEN")
		}
	}
	for _, r := range model.requests() {
		var all strings.Builder
		for k, vs := range r.Header {
			all.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
		}
		all.Write(r.Body)
		for _, s := range secrets {
			if s != reviewKey && strings.Contains(all.String(), s) {
				t.Errorf("reviewer request carries another secret %.12s...", s)
			}
		}
	}
}
