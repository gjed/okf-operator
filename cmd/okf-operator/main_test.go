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

const (
	e2eGateBase = "3333333333333333333333333333333333333333"
	e2eGateHead = "4444444444444444444444444444444444444444"
	e2eGatePlan = `{"version":1,"source":"acme/api","bundle":"product","from":"` + e2eCursor + `","to":"` + e2eHead + `","initial":false,
"changed_paths":[{"path":"api/openapi.yaml","status":"modified","previous_path":""}],
"pages":[{"path":"product/services/api.md","state":"update","type":"","source_paths":["api/openapi.yaml"]}],
"unrouted":[],"excluded":[]}`
	e2ePolicy = `version: 1
auto_merge:
  types: [Service]
  bundles: [product]
limits:
  page_lines: 50
  pr_lines: 80
`
	e2eSource  = "5555555555555555555555555555555555555555"
	e2eAPIPage = `---
type: Service
source_repo: acme/api
source_sha: ` + e2eSource + `
source_paths: [api/openapi.yaml]
updated_at: 2026-11-18T10:00:00Z
generated_by: okf-operator/dev/m1
---
# API

Served by the [gateway](gateway.md) and billed through [billing](billing.md).
`
)

// TestGateEndToEnd builds the binary and runs `gate` against a GitHub fake,
// with the plan on stdin.
func TestGateEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "okf-operator")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	fake := githubtest.New(t)
	fake.SetRepo("acme/kb", "main", e2eGateBase)
	fake.SetFile("acme/kb", e2eGateBase, ".kb/policy.yaml", e2ePolicy)
	fake.SetFile("acme/kb", e2eGateBase, ".kb/routing.yaml", e2eRouting)
	fake.SetFile("acme/kb", e2eGateHead, "product/services/api.md", e2eAPIPage)
	fake.SetFile("acme/kb", e2eGateHead, "product/services/gateway.md", "# Gateway\n")
	fake.SetFile("acme/kb", e2eGateHead, "product/services/notes.md", "# Notes\n")
	fake.SetCommit("acme/api", e2eSource)
	fake.SetPull("acme/kb", 12, githubtest.Pull{Head: e2eGateHead, BaseRef: "main"})
	fake.SetPullFiles("acme/kb", 12, []githubtest.PullFile{
		{Filename: "product/services/api.md", Status: "modified", Additions: 40, Deletions: 20},
		{Filename: "product/services/notes.md", Status: "added", Additions: 1},
	})

	run := func() []byte {
		cmd := exec.Command(bin, "gate", "--knowledge-repo", "acme/kb", "--pr", "12", "--plan", "-")
		cmd.Env = append(os.Environ(),
			"GITHUB_API_URL="+fake.APIURL(),
			"GITHUB_TOKEN=ghp_e2eToken0123456789",
			"OKF_GITHUB_APP_ID=",
			"OKF_GITHUB_APP_PRIVATE_KEY_FILE=",
			"OTEL_EXPORTER_OTLP_ENDPOINT=",
		)
		cmd.Stdin = bytes.NewReader([]byte(e2eGatePlan))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("gate (want exit 0): %v\nstderr:\n%s", err, stderr.String())
		}
		return stdout.Bytes()
	}
	first, second := run(), run()
	if !bytes.Equal(first, second) {
		t.Fatalf("two runs differ:\n%s\n---\n%s", first, second)
	}

	const golden = "testdata/gate.golden.json"
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
