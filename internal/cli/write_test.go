package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

// writeFixture starts the GitHub fake and clears credentials (planFixture),
// then gives the compare of planCursor...planHead a patch for api/handler.go,
// makes product/api.md an update page and writes a plan file that names it.
// It returns the fake, the knowledge checkout and the plan file.
func writeFixture(t *testing.T) (*githubtest.Fake, string, string) {
	t.Helper()
	fake, dir := planFixture(t, "ahead")
	fake.SetCompare("acme/api", planCursor, planHead, []byte(`{"status":"ahead","files":[
		{"filename":"api/handler.go","status":"modified","patch":"@@ -1 +1 @@\n-old\n+new\n","changes":2}]}`))
	page := filepath.Join(dir, "product", "api.md")
	if err := os.WriteFile(page, []byte("---\ntype: api\n---\n# API\n\nOld text.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(t.TempDir(), "plan.json")
	plan := fmt.Sprintf(`{"version":1,"source":"acme/api","bundle":"product","from":%q,"to":%q,"initial":false,
		"changed_paths":[{"path":"api/handler.go","status":"modified","previous_path":""}],
		"pages":[{"path":"product/api.md","state":"update","type":"api","source_paths":["api/handler.go"]}],
		"unrouted":[],"excluded":[]}`, planCursor, planHead)
	if err := os.WriteFile(planFile, []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvModelBaseURL, "")
	t.Setenv(EnvModel, "")
	t.Setenv(EnvModelAPIKey, "")
	return fake, dir, planFile
}

type modelRequest struct {
	Path   string
	Header http.Header
	Body   []byte
}

// modelStub is an OpenAI-compatible endpoint that records every request.
type modelStub struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []modelRequest
}

func newModelStub(t *testing.T, reply func(*http.Request) (int, string)) *modelStub {
	t.Helper()
	m := &modelStub{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.reqs = append(m.reqs, modelRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		m.mu.Unlock()
		status, out := reply(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *modelStub) requests() []modelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]modelRequest(nil), m.reqs...)
}

// noChange answers every request with the NO_CHANGE reply.
func noChange(*http.Request) (int, string) {
	return http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"NO_CHANGE"},"finish_reason":"stop"}]}`
}

// spanSink keeps every span the dispatcher exports. cli.Run installs its own
// tracer provider, so the test selects this exporter through
// OTEL_TRACES_EXPORTER instead of setting a global provider.
type spanSink struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (s *spanSink) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, spans...)
	return nil
}

func (s *spanSink) Shutdown(context.Context) error { return nil }

func (s *spanSink) ended() []sdktrace.ReadOnlySpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan(nil), s.spans...)
}

var (
	testSpans     = &spanSink{}
	registerSpans sync.Once
)

// captureSpans routes the spans of the next cli.Run to the returned sink.
func captureSpans(t *testing.T) *spanSink {
	t.Helper()
	registerSpans.Do(func() {
		autoexport.RegisterSpanExporter("okf-test-spans", func(context.Context) (sdktrace.SpanExporter, error) {
			return testSpans, nil
		})
	})
	testSpans.mu.Lock()
	testSpans.spans = nil
	testSpans.mu.Unlock()
	// With an endpoint set, telemetry.Setup leaves OTEL_TRACES_EXPORTER alone.
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:4318/v1/traces")
	t.Setenv("OTEL_TRACES_EXPORTER", "okf-test-spans")
	return testSpans
}

func TestWriteUsageErrors(t *testing.T) {
	_, dir, planFile := writeFixture(t)
	model := newModelStub(t, noChange)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"missing knowledge dir",
			[]string{"write", "--plan", planFile, "--model-base-url", model.URL, "--model", "m1"},
			[]string{"missing required flag --knowledge-dir"}},
		{"missing plan",
			[]string{"write", "--knowledge-dir", dir, "--model-base-url", model.URL, "--model", "m1"},
			[]string{"missing required flag --plan"}},
		{"no model base URL",
			[]string{"write", "--plan", planFile, "--knowledge-dir", dir, "--model", "m1"},
			[]string{"--model-base-url", EnvModelBaseURL}},
		{"no model name",
			[]string{"write", "--plan", planFile, "--knowledge-dir", dir, "--model-base-url", model.URL},
			[]string{"--model", EnvModel}},
		{"max pages 0",
			[]string{"write", "--plan", planFile, "--knowledge-dir", dir, "--model-base-url", model.URL, "--model", "m1", "--max-pages", "0"},
			[]string{"--max-pages 0 must be at least 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestWriteModelFlagsFallBackToEnv(t *testing.T) {
	_, dir, planFile := writeFixture(t)
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")

	t.Run("env used when flags absent", func(t *testing.T) {
		model := newModelStub(t, noChange)
		t.Setenv(EnvModelBaseURL, model.URL+"/v1")
		t.Setenv(EnvModel, "env-model")
		code, out, errOut := runCLI(t, BuildInfo{}, "write", "--plan", planFile, "--knowledge-dir", dir)
		if code != 0 {
			t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
		}
		reqs := model.requests()
		if len(reqs) != 1 || reqs[0].Path != "/v1/chat/completions" {
			t.Fatalf("model requests = %+v, want one to /v1/chat/completions", reqs)
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(reqs[0].Body, &body); err != nil || body.Model != "env-model" {
			t.Fatalf("request model = %q (%v), want env-model", body.Model, err)
		}
		if !strings.Contains(out, `"model": "env-model"`) {
			t.Fatalf("report does not name env-model:\n%s", out)
		}
	})

	t.Run("flags win over env", func(t *testing.T) {
		flagged := newModelStub(t, noChange)
		t.Setenv(EnvModelBaseURL, "http://127.0.0.1:1/v1")
		t.Setenv(EnvModel, "env-model")
		code, out, errOut := runCLI(t, BuildInfo{}, "write", "--plan", planFile, "--knowledge-dir", dir,
			"--model-base-url", flagged.URL+"/v1", "--model", "flag-model")
		if code != 0 {
			t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
		}
		if n := len(flagged.requests()); n != 1 {
			t.Fatalf("flagged endpoint got %d requests, want 1", n)
		}
		if !strings.Contains(out, `"model": "flag-model"`) {
			t.Fatalf("report does not name flag-model:\n%s", out)
		}
	})
}

func TestWritePlanFromStdin(t *testing.T) {
	_, dir := planFixture(t, "ahead")
	t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
	model := newModelStub(t, noChange)
	plan := fmt.Sprintf(`{"version":1,"source":"acme/api","bundle":"product","from":%q,"to":%q,"initial":false,
		"changed_paths":[],"pages":[],"unrouted":[],"excluded":[]}`, planCursor, planHead)
	var out, errOut strings.Builder
	env := Env{
		Stdin:  strings.NewReader(plan),
		Stdout: &out,
		Stderr: &errOut,
		Logger: slog.New(slog.NewTextHandler(&errOut, nil)),
	}
	args := []string{"--plan", "-", "--knowledge-dir", dir, "--model-base-url", model.URL, "--model", "m1"}
	if err := runWrite(context.Background(), env, args); err != nil {
		t.Fatalf("runWrite: %v\nstderr:\n%s", err, errOut.String())
	}
	var rep struct {
		Version int   `json:"version"`
		Pages   []any `json:"pages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
		t.Fatalf("stdout is not the report: %v\n%s", err, out.String())
	}
	if rep.Version != 3 || rep.Pages == nil || len(rep.Pages) != 0 {
		t.Fatalf("report = %+v, want version 3 and pages []\n%s", rep, out.String())
	}
	if n := len(model.requests()); n != 0 {
		t.Fatalf("empty plan reached the model %d times", n)
	}
}

func TestWriteAppModeTokenScopedToSourceRepo(t *testing.T) {
	fake, dir, planFile := writeFixture(t)
	keyFile, _ := githubtest.WriteKey(t)
	model := newModelStub(t, noChange)
	code, out, errOut := runCLI(t, BuildInfo{}, "write", "--plan", planFile, "--knowledge-dir", dir,
		"--model-base-url", model.URL, "--model", "m1", "--app-id", "7", "--app-private-key-file", keyFile)
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
	}
	if !strings.Contains(out, `"result": "unchanged"`) {
		t.Errorf("report does not show the page as unchanged:\n%s", out)
	}

	// write names only the source repo, so the one token request is for acme.
	trs := fake.TokenRequests()
	if len(trs) != 1 {
		t.Fatalf("token requests = %d, want exactly 1", len(trs))
	}
	tr := trs[0]
	if len(tr.Body) != 2 {
		t.Errorf("token request body keys = %v, want repositories and permissions only", tr.Body)
	}
	if got := tr.Body["repositories"]; !reflect.DeepEqual(got, []any{"api"}) {
		t.Errorf("repositories = %v, want [api]", got)
	}
	want := map[string]any{"contents": "read", "metadata": "read", "pull_requests": "read"}
	if got := tr.Body["permissions"]; !reflect.DeepEqual(got, want) {
		t.Errorf("permissions = %v, want %v", got, want)
	}
}

// TestWriteSecretsNeverLeak runs write in App mode at debug level against a
// model that answers 401 with a body echoing the API key and its Authorization
// header. GITHUB_TOKEN is set too. Nothing secret may reach stdout, stderr,
// the returned error, the recorded spans, the GitHub fake or a model request.
func TestWriteSecretsNeverLeak(t *testing.T) {
	fake, dir, planFile := writeFixture(t)
	keyFile, pem := githubtest.WriteKey(t)
	const apiKey = "sk-model-KEY-9f8e7d6c5b4a3210"
	const ghToken = "ghp_githubTokenDistinct0123456789"
	t.Setenv(EnvModelAPIKey, apiKey)
	t.Setenv("GITHUB_TOKEN", ghToken)
	model := newModelStub(t, func(r *http.Request) (int, string) {
		body, _ := json.Marshal(map[string]string{
			"error":         "invalid API key " + apiKey,
			"authorization": r.Header.Get("Authorization"),
		})
		return http.StatusUnauthorized, string(body)
	})
	spans := captureSpans(t)

	code, out, errOut := runCLI(t, BuildInfo{Version: "dev", Commit: "none"},
		"--log-level", "debug", "--log-format", "json", "write", "--plan", planFile, "--knowledge-dir", dir,
		"--model-base-url", model.URL, "--model", "m1", "--app-id", "7", "--app-private-key-file", keyFile)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stdout %q, stderr:\n%s", code, out, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty on failure", out)
	}
	if !strings.Contains(errOut, "okf-operator: ") || !strings.Contains(errOut, "HTTP 401") {
		t.Fatalf("stderr does not show the 401 failure:\n%s", errOut)
	}
	if !strings.Contains(errOut, "minted installation token") {
		t.Fatalf("debug logging did not run:\n%s", errOut)
	}
	// The endpoint echoed the key, so redaction had to replace it: proof the leak path ran.
	if !strings.Contains(errOut, "[REDACTED]") {
		t.Fatalf("no redaction in stderr; the 401 body did not echo the key:\n%s", errOut)
	}

	// The dispatcher's root span must carry the error, so RecordError ran.
	failedSpans := 0
	var dump strings.Builder
	dump.WriteString(out + errOut)
	for _, s := range spans.ended() {
		if s.Name() == "write" && s.Status().Code == codes.Error {
			failedSpans++
		}
		dump.WriteString(s.Name() + s.Status().Description)
		for _, kv := range s.Attributes() {
			dump.WriteString(kv.Value.String())
		}
		for _, e := range s.Events() {
			dump.WriteString(e.Name)
			for _, kv := range e.Attributes {
				dump.WriteString(kv.Value.String())
			}
		}
	}
	if failedSpans != 1 {
		t.Fatalf("write spans with error status = %d, want 1", failedSpans)
	}

	trs := fake.TokenRequests()
	if len(trs) != 1 {
		t.Fatalf("token requests = %d, want 1", len(trs))
	}
	secrets := []string{apiKey, ghToken, fake.InstallationToken}
	for _, r := range trs {
		secrets = append(secrets, strings.TrimPrefix(r.Auth, "Bearer "))
	}
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
		if s == "" {
			t.Fatal("empty secret in the list: the check would pass vacuously")
		}
		if strings.Contains(dump.String(), s) {
			t.Errorf("secret %.12s... leaked into stdout, stderr or spans", s)
		}
	}

	// GitHub never receives the model key, and App mode never sends GITHUB_TOKEN.
	for _, a := range fake.Auths() {
		if strings.Contains(a, apiKey) {
			t.Error("GitHub fake received the model API key")
		}
		if strings.Contains(a, ghToken) {
			t.Error("GitHub fake received GITHUB_TOKEN in App mode")
		}
	}

	// The model is called, with its key only: no GitHub credential in any header or body.
	reqs := model.requests()
	if len(reqs) == 0 {
		t.Fatal("model was never called")
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer "+apiKey {
		t.Errorf("model Authorization = %q, want the model key", got)
	}
	for _, r := range reqs {
		var all strings.Builder
		for k, vs := range r.Header {
			all.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
		}
		all.Write(r.Body)
		for _, s := range secrets {
			if s == apiKey {
				continue
			}
			if strings.Contains(all.String(), s) {
				t.Errorf("model request %s carries a GitHub or PEM secret %.12s...", r.Path, s)
			}
		}
	}
}
