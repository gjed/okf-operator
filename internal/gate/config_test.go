package gate

import (
	"context"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
)

func TestLoadPolicy(t *testing.T) {
	f := newFixture(t)
	pol, err := loadPolicy(context.Background(), f.reader, kbRepo, baseSHA, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	if !pol.AllowsBundle("product") || pol.Limits.PageLines != 100 {
		t.Errorf("policy = %+v", pol)
	}
}

const otherBase = "4444444444444444444444444444444444444444"

func TestLoadPolicyFails(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fixture)
		want  string
	}{
		{"missing policy", func(f *fixture) {
			f.fake.SetRepo(kbRepo, "main", otherBase)
			f.fake.SetFile(kbRepo, otherBase, ".kb/routing.yaml", baseRouting)
		}, ".kb/policy.yaml not found in acme/kb at base commit " + otherBase},
		{"invalid policy", func(f *fixture) { f.fake.SetFile(kbRepo, baseSHA, ".kb/policy.yaml", "version: 2\n") }, ".kb/policy.yaml: unsupported version 2"},
		{"invalid routing", func(f *fixture) { f.fake.SetFile(kbRepo, baseSHA, ".kb/routing.yaml", "version: 1\nnope: 1\n") }, ".kb/routing.yaml"},
		{"source not onboarded", func(f *fixture) { f.plan.Source = "acme/other" }, "plan source acme/other is not onboarded"},
		{"bundle mismatch", func(f *fixture) { f.plan.Bundle = "infra" }, `plan bundle "infra" differs from bundle "product"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.setup(f)
			_, err := loadPolicy(context.Background(), f.reader, kbRepo, f.pr(t).BaseSHA, f.plan)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestHeadCannotRewritePolicy is a PR whose head edits .kb/policy.yaml to
// allow itself: the base policy is read and the change-shape rule fails.
func TestHeadCannotRewritePolicy(t *testing.T) {
	f := newFixture(t)
	f.fake.SetFile(kbRepo, baseSHA, ".kb/policy.yaml", strings.Replace(basePolicy, "types: [Service]", "types: []", 1))
	f.fake.SetFile(kbRepo, headSHA, ".kb/policy.yaml", strings.Replace(basePolicy, "types: [Service]", "types: [Service, Runbook]", 1))
	f.files = append(f.files, githubtest.PullFile{Filename: ".kb/policy.yaml", Status: "modified", Additions: 1, Deletions: 1})
	f.plan.Pages = append(f.plan.Pages, plan.Page{Path: ".kb/policy.yaml", State: plan.StateUpdate})
	pr := f.pr(t)

	pol, err := loadPolicy(context.Background(), f.reader, kbRepo, pr.BaseSHA, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	if pol.AllowsType("Service") || pol.AllowsType("Runbook") {
		t.Fatalf("gate read the head policy: %v", pol.AutoMerge.Types)
	}
	if res := changeShape(pr.Files); res.Pass || !strings.Contains(strings.Join(res.Details, "\n"), ".kb/policy.yaml: changes knowledge repo configuration") {
		t.Errorf("change_shape = %+v", res)
	}
}
