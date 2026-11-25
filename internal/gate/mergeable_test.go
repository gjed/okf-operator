package gate

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
)

// evaluateWithStates evaluates PR 7 of the fixture, whose merge state reads
// answer states in turn, and counts the reads of the PR.
func (f *fixture) evaluateWithStates(t *testing.T, states ...string) (*Report, int) {
	t.Helper()
	f.reader.Poll = github.Poll{Reads: 5, Delay: time.Millisecond}
	f.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "main", MergeStates: states})
	f.fake.SetPullFiles(kbRepo, prNumber, f.files)
	r, err := Evaluate(context.Background(), Deps{Reader: f.reader, Commits: f.commits}, kbRepo, prNumber, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, req := range f.fake.Requests() {
		if req == "GET /repos/"+kbRepo+"/pulls/"+strconv.Itoa(prNumber) {
			reads++
		}
	}
	return r, reads
}

func TestMergeableRule(t *testing.T) {
	dirty := []string{"pull request conflicts with main: its mergeable_state is dirty"}
	tests := []struct {
		name      string
		states    []string
		want      []string
		wantReads int
	}{
		{"clean", []string{"clean"}, nil, 2},
		{"behind", []string{"behind"}, nil, 2},
		{"blocked", []string{"blocked"}, nil, 2},
		{"no state", nil, nil, 2},
		{"dirty", []string{"dirty"}, dirty, 2},
		{"unknown, then dirty", []string{"unknown", "unknown", "dirty"}, dirty, 4},
		{"unknown, then clean", []string{"unknown", "clean"}, nil, 3},
		{"unknown after every read", []string{"unknown"}, nil, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			r, reads := f.evaluateWithStates(t, tt.states...)
			var got Result
			for _, res := range r.Rules {
				if res.Name == RuleMergeable {
					got = res
				}
			}
			checkResult(t, got, RuleMergeable, tt.want)
			if reads != tt.wantReads {
				t.Errorf("PR reads = %d, want %d", reads, tt.wantReads)
			}
			// Only the mergeable rule decides: the conforming PR passes
			// every other rule.
			if r.Pass != (tt.want == nil) {
				t.Errorf("report pass = %v with mergeable details %q", r.Pass, strings.Join(got.Details, "|"))
			}
		})
	}
}

func TestMergeableRuleIsAfterLinks(t *testing.T) {
	f := newFixture(t)
	r, _ := f.evaluateWithStates(t, "dirty")
	names := ruleNames(r)
	if names[len(names)-1] != RuleMergeable || names[len(names)-2] != RuleLinks {
		t.Fatalf("rules = %v", names)
	}
}
