package main

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	e2eCursor = "1111111111111111111111111111111111111111"
	e2eHead   = "2222222222222222222222222222222222222222"
)

const e2eRouting = `version: 1
sources:
  - repo: acme/api
    bundle: product
    exclude: ["**/secrets/**"]
    routes:
      - page: product/services/api.md
        paths: ["src/api/**", "api/openapi.yaml"]
      - page: product/services/gateway.md
        type: Service
        paths: ["api/openapi.yaml"]
      - page: product/services/billing.md
        paths: ["billing/**"]
`

const e2eCompare = `{"status": "ahead", "files": [
  {"filename": "api/openapi.yaml", "status": "modified"},
  {"filename": "legacy/handler.go", "status": "renamed", "previous_filename": "src/api/handler.go"},
  {"filename": "deploy/secrets/token.yaml", "status": "added"},
  {"filename": "billing/invoice.go", "status": "removed"},
  {"filename": "README.md", "status": "modified"}
]}`

// TestPlanEndToEnd builds the binary and runs `plan` against a GitHub fake
// and a temp knowledge checkout.
func TestPlanEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "okf-operator")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	fake := githubtest.New(t)
	fake.SetRepo("acme/api", "main", e2eHead)
	fake.SetFile("acme/kb", "kb-state", "cursors/acme/api", e2eCursor+"\n")
	fake.SetCompare("acme/api", e2eCursor, e2eHead, []byte(e2eCompare))

	kb := t.TempDir()
	for name, content := range map[string]string{".kb/routing.yaml": e2eRouting, "product/services/api.md": "# API\n"} {
		p := filepath.Join(kb, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run := func() []byte {
		cmd := exec.Command(bin, "plan", "--source", "acme/api", "--knowledge-repo", "acme/kb", "--knowledge-dir", kb)
		cmd.Env = append(os.Environ(),
			"GITHUB_API_URL="+fake.APIURL(),
			"GITHUB_TOKEN=ghp_e2eToken0123456789",
			"OKF_GITHUB_APP_ID=",
			"OKF_GITHUB_APP_PRIVATE_KEY_FILE=",
			"OTEL_EXPORTER_OTLP_ENDPOINT=",
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("plan (want exit 0): %v\nstderr:\n%s", err, stderr.String())
		}
		return stdout.Bytes()
	}
	first, second := run(), run()
	if !bytes.Equal(first, second) {
		t.Fatalf("two runs differ:\n%s\n---\n%s", first, second)
	}

	const golden = "testdata/plan.golden.json"
	if *update {
		if err := os.WriteFile(golden, first, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("stdout:\n%s\nwant:\n%s", first, want)
	}
}
