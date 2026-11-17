package routing

import (
	"reflect"
	"strings"
	"testing"
)

const valid = `version: 1
sources:
  - repo: acme/api
    bundle: product
    exclude: ["**/secrets/**"]
    routes:
      - page: product/services/api.md
        paths: ["api/**"]
`

func TestParseValid(t *testing.T) {
	m, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if m.Source("ACME/api") == nil {
		t.Fatal("source lookup should be case-insensitive")
	}
	if m.Source("acme/other") != nil {
		t.Fatal("unknown source should be nil")
	}
}

func TestParseFails(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{"unknown key", strings.Replace(valid, "    bundle: product", "    bundle: product\n    bundel: x", 1), []string{"bundel"}},
		{"version 2", "version: 2\nsources: []\n", []string{"unsupported version 2", "supported version is 1"}},
		{"missing version", "sources: []\n", []string{"unsupported version 0"}},
		{"empty", "", []string{"unsupported version 0"}},
		{"duplicate source", valid + `  - repo: Acme/API
    bundle: product
    routes:
      - page: product/x.md
        paths: ["x"]
`, []string{"duplicate source Acme/API"}},
		{"target outside bundle", strings.Replace(valid, "product/services/api.md", "internal-infra/runbooks/x.md", 1),
			[]string{"acme/api", "internal-infra/runbooks/x.md", "outside bundle"}},
		{"path traversal", strings.Replace(valid, "product/services/api.md", "product/../internal-infra/x.md", 1), []string{`".." segment`}},
		{"absolute target", strings.Replace(valid, "product/services/api.md", "/product/x.md", 1), []string{"absolute path"}},
		{"non-md target", strings.Replace(valid, "product/services/api.md", "product/services/api.txt", 1), []string{"not a .md page"}},
		{"invalid glob", strings.Replace(valid, `"api/**"`, `"api/[**"`, 1), []string{`invalid glob "api/[**"`}},
		{"invalid exclude", strings.Replace(valid, `"**/secrets/**"`, `"{a"`, 1), []string{`invalid exclude glob "{a"`}},
		{"bad repo", strings.Replace(valid, "acme/api", "acme", 1), []string{`repo "acme" is not owner/name`}},
		{"bad bundle", strings.Replace(valid, "bundle: product", "bundle: ../product", 1), []string{"is not a relative directory"}},
		{"no paths", strings.Replace(valid, `paths: ["api/**"]`, "paths: []", 1), []string{"no paths"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("want error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestValidateCollectsEveryError(t *testing.T) {
	_, err := Parse([]byte(`version: 1
sources:
  - repo: acme/api
    bundle: product
    routes:
      - page: other/x.md
        paths: ["a/[**"]
      - page: product/x.txt
        paths: ["b/**"]
  - repo: acme/api
    bundle: product
    routes: []
`))
	if err == nil {
		t.Fatal("want error")
	}
	for _, w := range []string{"outside bundle", "invalid glob", "not a .md page", "duplicate source"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not contain %q", err, w)
		}
	}
}

func TestMatch(t *testing.T) {
	s := Source{
		Repo:    "acme/api",
		Bundle:  "product",
		Exclude: []string{"**/secrets/**", "**/*.pem"},
		Routes: []Route{
			{Page: "product/services/api.md", Paths: []string{"api/**", "deploy/**"}},
			{Page: "product/services/gateway.md", Type: "Service", Paths: []string{"api/openapi.yaml"}},
			{Page: "product/pipelines/deploy.md", Paths: []string{"deploy/**/*.yaml"}},
		},
	}
	got := s.Match([]string{
		"deploy/prod/app/values.yaml",
		"api/openapi.yaml",
		"deploy/secrets/token.yaml",
		"README.md",
		"api/openapi.yaml",
		"certs/server.pem",
		"API/upper.go",
	})
	want := Result{
		Pages: []PageMatch{
			{Page: "product/pipelines/deploy.md", SourcePaths: []string{"deploy/prod/app/values.yaml"}},
			{Page: "product/services/api.md", SourcePaths: []string{"api/openapi.yaml", "deploy/prod/app/values.yaml"}},
			{Page: "product/services/gateway.md", Type: "Service", SourcePaths: []string{"api/openapi.yaml"}},
		},
		Unrouted: []string{"API/upper.go", "README.md"},
		Excluded: []string{"certs/server.pem", "deploy/secrets/token.yaml"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestMatchEmpty(t *testing.T) {
	got := (&Source{}).Match(nil)
	if got.Pages == nil || got.Unrouted == nil || got.Excluded == nil {
		t.Fatalf("lists must be empty, not nil: %+v", got)
	}
}

func TestExampleLoads(t *testing.T) {
	if _, err := Load("../../examples/routing.yaml"); err != nil {
		t.Fatal(err)
	}
}
