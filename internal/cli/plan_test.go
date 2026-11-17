package cli

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const (
	planCursor = "1111111111111111111111111111111111111111"
	planHead   = "2222222222222222222222222222222222222222"
)

// planFixture starts a GitHub fake for source acme/api and knowledge repo
// acme/kb, writes a knowledge checkout and clears credential variables.
func planFixture(t *testing.T, compareStatus string) (*githubtest.Fake, string) {
	t.Helper()
	fake := githubtest.New(t)
	fake.SetRepo("acme/api", "main", planHead)
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", planCursor+"\n")
	fake.SetCompare("acme/api", planCursor, planHead, []byte(`{"status":"`+compareStatus+`","files":[
		{"filename":"api/handler.go","status":"modified"},
		{"filename":"README.md","status":"modified"}]}`))

	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".kb/routing.yaml", "version: 1\nsources:\n  - repo: acme/api\n    bundle: product\n    routes:\n      - page: product/api.md\n        paths: [\"api/**\"]\n")
	write("product/api.md", "# API\n")

	for _, k := range []string{"GITHUB_TOKEN", "OKF_GITHUB_APP_ID", "OKF_GITHUB_APP_PRIVATE_KEY_FILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("GITHUB_API_URL", fake.APIURL())
	return fake, dir
}

func planArgs(dir string, extra ...string) []string {
	return append([]string{"plan", "--source", "acme/api", "--knowledge-repo", "acme/kb", "--knowledge-dir", dir}, extra...)
}

func TestPlanHelp(t *testing.T) {
	code, out, _ := runCLI(t, BuildInfo{}, "plan", "-h")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, f := range []string{"-source", "-knowledge-repo", "-knowledge-dir", "-routing", "-to", "-app-id", "-app-private-key-file"} {
		if !strings.Contains(out, "  "+f+" ") {
			t.Errorf("help does not list %s:\n%s", f, out)
		}
	}
	if _, out, _ := runCLI(t, BuildInfo{}, "help"); !strings.Contains(out, "plan") {
		t.Errorf("top-level help does not list plan:\n%s", out)
	}
}

func TestPlanUsageErrors(t *testing.T) {
	_, dir := planFixture(t, "ahead")
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want []string
	}{
		{"missing source", []string{"plan", "--knowledge-repo", "acme/kb", "--knowledge-dir", dir}, map[string]string{"GITHUB_TOKEN": "t"},
			[]string{"missing required flag --source", "-knowledge-dir"}},
		{"bad source", planArgs(dir, "--source", "acme"), map[string]string{"GITHUB_TOKEN": "t"}, []string{`--source "acme" is not owner/name`}},
		{"no credentials", planArgs(dir), nil, []string{"--app-id and --app-private-key-file", "GITHUB_TOKEN"}},
		{"only app id", planArgs(dir, "--app-id", "1"), nil, []string{"private key file is not"}},
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

func TestPlanRuntimeErrorsExit1(t *testing.T) {
	t.Run("diverged cursor", func(t *testing.T) {
		_, dir := planFixture(t, "diverged")
		t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
		code, out, errOut := runCLI(t, BuildInfo{}, planArgs(dir)...)
		if code != 1 || out != "" || !strings.Contains(errOut, "cursor diverged") || !strings.Contains(errOut, "reconcile required") {
			t.Fatalf("exit %d, stdout %q, stderr:\n%s", code, out, errOut)
		}
	})
	t.Run("routing version 2", func(t *testing.T) {
		_, dir := planFixture(t, "ahead")
		t.Setenv("GITHUB_TOKEN", "ghp_test0123456789")
		if err := os.WriteFile(filepath.Join(dir, ".kb", "routing.yaml"), []byte("version: 2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, errOut := runCLI(t, BuildInfo{}, planArgs(dir)...)
		if code != 1 || !strings.Contains(errOut, "supported version is 1") {
			t.Fatalf("exit %d, stderr:\n%s", code, errOut)
		}
	})
}

// TestPlanSecretsNeverLogged runs plan in App mode at debug level, once
// successfully and once failing, and checks that neither the private key nor
// any token reaches stdout, stderr, the returned error or the span.
func TestPlanSecretsNeverLogged(t *testing.T) {
	for _, status := range []string{"ahead", "diverged"} {
		t.Run(status, func(t *testing.T) {
			fake, dir := planFixture(t, status)
			keyFile, pem := githubtest.WriteKey(t)

			spans := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
			ctx, span := tp.Tracer("test").Start(context.Background(), "plan")
			var out, errOut bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&errOut, &slog.HandlerOptions{Level: slog.LevelDebug}))
			env := Env{Stdout: &out, Stderr: &errOut, Logger: logger}
			err := runPlan(ctx, env, planArgs(dir, "--app-id", "7", "--app-private-key-file", keyFile)[1:])
			if err != nil {
				span.RecordError(err)
			}
			span.End()
			if (status == "ahead") != (err == nil) {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(errOut.String(), "minted installation token") {
				t.Fatalf("debug logging did not run:\n%s", errOut.String())
			}

			var dump strings.Builder
			dump.WriteString(out.String() + errOut.String())
			if err != nil {
				dump.WriteString(err.Error())
			}
			for _, s := range spans.Ended() {
				for _, kv := range s.Attributes() {
					dump.WriteString(kv.Value.String())
				}
				for _, e := range s.Events() {
					for _, kv := range e.Attributes {
						dump.WriteString(kv.Value.String())
					}
				}
				dump.WriteString(s.Status().Description)
			}
			secrets := []string{fake.InstallationToken}
			for _, r := range fake.TokenRequests() {
				secrets = append(secrets, strings.TrimPrefix(r.Auth, "Bearer "))
			}
			for line := range strings.SplitSeq(pem, "\n") {
				if len(line) > 20 {
					secrets = append(secrets, line)
				}
			}
			if len(secrets) < 3 {
				t.Fatalf("too few secrets collected: %d", len(secrets))
			}
			for _, s := range secrets {
				if strings.Contains(dump.String(), s) {
					t.Errorf("secret %.12s... leaked into output", s)
				}
			}
		})
	}
}
