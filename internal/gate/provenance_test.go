package gate

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestProvenanceRule(t *testing.T) {
	unknownSHA := strings.Repeat("9", 40)
	tests := []struct {
		name string
		page string
		want []string
	}{
		{"all fields, known commit", apiPage, nil},
		{"missing field", strings.Replace(apiPage, "generated_by: okf-operator/dev/m1\n", "", 1),
			[]string{"product/api.md: missing generated_by"}},
		{"several missing", strings.Replace(strings.Replace(apiPage, "source_paths: [api/x.go]\n", "", 1), "updated_at: 2026-11-18T10:00:00Z\n", "", 1),
			[]string{"product/api.md: missing source_paths", "product/api.md: missing updated_at"}},
		{"foreign source repo", strings.Replace(apiPage, "source_repo: acme/api", "source_repo: evil/repo", 1),
			[]string{"product/api.md: source_repo evil/repo is not the plan's source acme/api"}},
		{"unknown commit", strings.Replace(apiPage, sourceSHA, unknownSHA, 1),
			[]string{"product/api.md: source_sha " + unknownSHA + " is not the plan's to " + sourceSHA,
				"product/api.md: source_sha " + unknownSHA + " not found in acme/api"}},
		{"existing commit that is not the plan's to", strings.Replace(apiPage, sourceSHA, headSHA, 1),
			[]string{"product/api.md: source_sha " + headSHA + " is not the plan's to " + sourceSHA}},
		{"short sha", strings.Replace(apiPage, sourceSHA, "3333333", 1),
			[]string{`product/api.md: source_sha "3333333" is not a full 40-character commit SHA`}},
		{"no frontmatter", "# API\n",
			[]string{`product/api.md: provenance: no frontmatter: page does not start with "---\n"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			res, err := provenanceRule(context.Background(), f.commits, srcRepo, sourceSHA, map[string][]byte{"product/api.md": []byte(tt.page)})
			if err != nil {
				t.Fatal(err)
			}
			checkResult(t, res, RuleProvenance, tt.want)
		})
	}
}

func TestProvenanceRuleLookupFailsRun(t *testing.T) {
	f := newFixture(t)
	f.fake.FailStatus["/repos/acme/api/commits/"+sourceSHA] = http.StatusInternalServerError
	_, err := provenanceRule(context.Background(), f.commits, srcRepo, sourceSHA, map[string][]byte{"product/api.md": []byte(apiPage)})
	if err == nil || !strings.Contains(err.Error(), "look up acme/api@"+sourceSHA) {
		t.Fatalf("err = %v", err)
	}
}
