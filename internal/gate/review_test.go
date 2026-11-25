package gate

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
)

func (f *fixture) evaluateReview(t *testing.T, rv *Review) (*Report, error) {
	t.Helper()
	f.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "main"})
	f.fake.SetPullFiles(kbRepo, prNumber, f.files)
	return Evaluate(context.Background(), Deps{Reader: f.reader, Commits: f.commits, Review: f.bind(t, rv)}, kbRepo, prNumber, f.plan)
}

// bind returns a copy of rv with every empty binding set to what the fixture
// evaluates: acme/kb, the base commit and the plan's digest.
func (f *fixture) bind(t *testing.T, rv *Review) *Review {
	t.Helper()
	b := *rv
	digest, err := plan.Digest(f.plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range []struct {
		field *string
		value string
	}{{&b.KnowledgeRepo, kbRepo}, {&b.BaseSHA, baseSHA}, {&b.PlanDigest, digest}} {
		if *kv.field == "" {
			*kv.field = kv.value
		}
	}
	return &b
}

func passingReview() *Review { return &Review{PR: prNumber, HeadSHA: headSHA, Pass: true} }

func TestReviewRule(t *testing.T) {
	failing := &Review{PR: prNumber, HeadSHA: headSHA, Pass: false, Details: []string{"product/api.md: unsupported: Rate limit is 10 rps."}}
	tests := []struct {
		name     string
		files    []githubtest.PullFile
		review   *Review
		wantPass bool
		review7  bool
	}{
		{"failing deterministic rule, passing review", []githubtest.PullFile{modified("product/api.md", 90, 70)}, passingReview(), false, true},
		{"passing deterministic rules, failing review", nil, failing, false, false},
		{"failing review without details", nil, &Review{PR: prNumber, HeadSHA: headSHA}, false, false},
		{"everything passes", nil, passingReview(), true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.files != nil {
				f.files = tt.files
			}
			r, err := f.evaluateReview(t, tt.review)
			if err != nil {
				t.Fatal(err)
			}
			names := ruleNames(r)
			if len(names) != 8 || names[7] != RuleReview {
				t.Fatalf("rules = %v, want review appended after mergeable", names)
			}
			if r.Pass != tt.wantPass || r.Rules[7].Pass != tt.review7 {
				var out bytes.Buffer
				_ = Encode(&out, r)
				t.Fatalf("pass = %v, review pass = %v, want %v and %v:\n%s", r.Pass, r.Rules[7].Pass, tt.wantPass, tt.review7, out.String())
			}
			if tt.review == failing && strings.Join(r.Rules[7].Details, "|") != failing.Details[0] {
				t.Errorf("review details = %q", r.Rules[7].Details)
			}
		})
	}
}

func TestReviewForAnotherPRFails(t *testing.T) {
	for name, rv := range map[string]*Review{
		"other head": {PR: prNumber, HeadSHA: baseSHA, Pass: true},
		"other PR":   {PR: prNumber + 1, HeadSHA: headSHA, Pass: true},
		"other repo": {KnowledgeRepo: "acme/other", PR: prNumber, HeadSHA: headSHA, Pass: true},
		"other base": {PR: prNumber, HeadSHA: headSHA, BaseSHA: headSHA, Pass: true},
		"other plan": {PR: prNumber, HeadSHA: headSHA, PlanDigest: "sha256:0000", Pass: true},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := newFixture(t).evaluateReview(t, rv)
			if err == nil || r != nil {
				t.Fatalf("report %+v, err %v: want an error and no report", r, err)
			}
		})
	}
}

func TestReviewDeterministic(t *testing.T) {
	f := newFixture(t)
	rv := &Review{PR: prNumber, HeadSHA: headSHA, Details: []string{"b: unsupported: x", "a: reply_invalid"}}
	var out [2]bytes.Buffer
	for i := range out {
		r, err := f.evaluateReview(t, rv)
		if err != nil {
			t.Fatal(err)
		}
		if err := Encode(&out[i], r); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(out[0].Bytes(), out[1].Bytes()) {
		t.Fatalf("reports differ:\n%s\n%s", out[0].String(), out[1].String())
	}
	if rv.Details[0] != "b: unsupported: x" {
		t.Error("Evaluate reordered the caller's review details")
	}
}

func TestDecodeReport(t *testing.T) {
	r, err := newFixture(t).evaluateReview(t, passingReview())
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	if err := Encode(&a, r); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.NewReader(a.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := Encode(&b, got); err != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("round trip differs (%v)", err)
	}
	for name, doc := range map[string]string{
		"version 1":     strings.Replace(a.String(), `"version": 2`, `"version": 1`, 1),
		"unknown field": strings.Replace(a.String(), `"pass"`, `"merge": true, "pass"`, 1),
		"two documents": a.String() + a.String(),
		"trailing ]":    a.String() + "]",
		"trailing }":    a.String() + "}",
	} {
		if _, err := Decode(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: decoded without error", name)
		}
	}
}
