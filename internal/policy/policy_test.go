package policy

import (
	"strings"
	"testing"
)

const valid = `version: 1
auto_merge:
  types: [Service]
  bundles: [product]
limits:
  page_lines: 200
  pr_lines: 600
`

func TestParseValid(t *testing.T) {
	p, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if !p.AllowsType("Service") || p.AllowsType("Runbook") {
		t.Errorf("types: %v", p.AutoMerge.Types)
	}
	if !p.AllowsBundle("product") || p.AllowsBundle("infra") {
		t.Errorf("bundles: %v", p.AutoMerge.Bundles)
	}
	if p.Limits != (Limits{PageLines: 200, PRLines: 600}) {
		t.Errorf("limits: %+v", p.Limits)
	}
}

func TestParseEmptyAllowlist(t *testing.T) {
	p, err := Parse([]byte("version: 1\nlimits:\n  page_lines: 1\n  pr_lines: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AutoMerge.Types) != 0 || len(p.AutoMerge.Bundles) != 0 || p.AllowsType("") || p.AllowsBundle("") {
		t.Errorf("an omitted allowlist must allow nothing: %+v", p.AutoMerge)
	}
}

func TestParseFails(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{"unknown key", strings.Replace(valid, "  types:", "  typs:", 1), []string{"typs"}},
		{"unknown top-level key", valid + "extra: 1\n", []string{"extra"}},
		{"version 2", "version: 2\n", []string{"unsupported version 2", "supported version is 1"}},
		{"missing version", "limits: {}\n", []string{"unsupported version 0"}},
		{"empty", "", []string{"unsupported version 0"}},
		{"empty type", strings.Replace(valid, "[Service]", `[""]`, 1), []string{"auto_merge.types[0]: empty entry"}},
		{"duplicate type", strings.Replace(valid, "[Service]", "[Service, Service]", 1), []string{`auto_merge.types[1]: duplicate entry "Service"`}},
		{"duplicate bundle", strings.Replace(valid, "[product]", "[product, product]", 1), []string{`auto_merge.bundles[1]: duplicate entry "product"`}},
		{"traversal bundle", strings.Replace(valid, "[product]", "[../other]", 1), []string{`auto_merge.bundles[0]: "../other" is not a relative directory`}},
		{"absolute bundle", strings.Replace(valid, "[product]", "[/product]", 1), []string{"is not a relative directory"}},
		{"unclean bundle", strings.Replace(valid, "[product]", "[product/]", 1), []string{"is not a relative directory"}},
		{"missing pr_lines", strings.Replace(valid, "  pr_lines: 600\n", "", 1), []string{"limits.pr_lines must be a positive integer"}},
		{"zero page_lines", strings.Replace(valid, "page_lines: 200", "page_lines: 0", 1), []string{"limits.page_lines must be a positive integer"}},
		{"negative pr_lines", strings.Replace(valid, "pr_lines: 600", "pr_lines: -1", 1), []string{"limits.pr_lines must be a positive integer, got -1"}},
		{"several problems", strings.Replace(strings.Replace(valid, "[Service]", "[Service, Service]", 1), "page_lines: 200", "page_lines: 0", 1),
			[]string{"duplicate entry", "limits.page_lines"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestExampleLoads(t *testing.T) {
	if _, err := Load("../../examples/policy.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load("testdata/none.yaml"); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("err = %v", err)
	}
}
