package gate

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/policy"
)

func TestAllowlist(t *testing.T) {
	allowed := &policy.Policy{AutoMerge: policy.AutoMerge{Types: []string{"Service"}, Bundles: []string{"product"}}}
	tests := []struct {
		name   string
		policy *policy.Policy
		base   string
		head   string
		page   string
		want   []string
	}{
		{"allowed", allowed, apiPage, apiPage, "product/api.md", nil},
		{"type not allowed", allowed, strings.Replace(apiPage, "type: Service", "type: Runbook", 1), strings.Replace(apiPage, "type: Service", "type: Runbook", 1),
			"product/api.md", []string{`product/api.md: type "Runbook" is not in auto_merge.types`}},
		{"bundle not allowed", &policy.Policy{AutoMerge: policy.AutoMerge{Types: []string{"Service"}}}, apiPage, apiPage,
			"product/api.md", []string{"bundle product is not in auto_merge.bundles"}},
		{"empty allowlist", &policy.Policy{}, apiPage, apiPage, "product/api.md",
			[]string{"bundle product is not in auto_merge.bundles", `product/api.md: type "Service" is not in auto_merge.types`}},
		{"retyped into the allowlist", allowed, strings.Replace(apiPage, "type: Service", "type: Runbook", 1), apiPage,
			"product/api.md", []string{`product/api.md: type changed from "Runbook" to "Service"`}},
		{"no type", allowed, strings.Replace(apiPage, "type: Service\n", "", 1), strings.Replace(apiPage, "type: Service\n", "", 1),
			"product/api.md", []string{"product/api.md: no type"}},
		{"new page", allowed, "", apiPage, "product/new.md",
			[]string{"product/new.md: not a page at base commit " + baseSHA + ", so it has no allowed type"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.base != "" {
				f.fake.SetFile(kbRepo, baseSHA, tt.page, tt.base)
			}
			res, err := allowlist(context.Background(), f.reader, kbRepo, baseSHA, "product", tt.policy, map[string][]byte{tt.page: []byte(tt.head)})
			if err != nil {
				t.Fatal(err)
			}
			checkResult(t, res, RuleAllowlist, tt.want)
		})
	}
}

func TestAllowlistLookupFailsRun(t *testing.T) {
	f := newFixture(t)
	f.fake.FailStatus["/repos/acme/kb/contents/product/api.md"] = http.StatusInternalServerError
	_, err := allowlist(context.Background(), f.reader, kbRepo, baseSHA, "product", &policy.Policy{}, map[string][]byte{"product/api.md": []byte(apiPage)})
	if err == nil {
		t.Fatal("a 500 reading the base page must fail the run")
	}
}
