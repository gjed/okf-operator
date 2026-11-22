package write

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/provenance"
)

// withRouted is alphaPage stamped by an earlier run at testCursor with the
// routed set routed.
func withRouted(t *testing.T, routed ...string) string {
	t.Helper()
	return stamped(t, alphaPage, provenance.Fields{
		SourceRepo: testSource, SourceSHA: testCursor, SourcePaths: []string{"src/a.go"},
		UpdatedAt: testNow, GeneratedBy: "okf-operator/0.9.0/old", RoutedPaths: routed,
	})
}

// write stamps routed_paths from the reconcile entry, carries a known set
// forward, keeps an unknown set unknown, and never keeps a model's value.
func TestRunStampsRoutedPaths(t *testing.T) {
	modelRouted := strings.Replace(alphaNew, "title: Alpha\n", "title: Alpha\nrouted_paths: [src/fake.go]\n", 1)
	cases := map[string]struct {
		current string
		reply   string
		plan    func(t *testing.T) *plan.Plan
		want    []string
	}{
		"reconcile entry overwrites the model's value": {
			current: withRouted(t, "src/a.go"),
			reply:   modelRouted,
			plan: func(*testing.T) *plan.Plan {
				entry := hunksEntry(alphaPath, testCursor)
				entry.RoutedPaths = []string{"src/a.go", "src/b.go", "src/c.go"}
				return reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go", "src/b.go")), entry)
			},
			want: []string{"src/a.go", "src/b.go", "src/c.go"},
		},
		"version 1 carries the set forward, the reply dropping the key": {
			current: withRouted(t, "src/gone.go", "src/old.go", "src/z.go"),
			reply:   alphaNew,
			plan: func(*testing.T) *plan.Plan {
				p := testPlan(updatePage(alphaPath, "src/a.go", "src/b.go"))
				p.ChangedPaths = []plan.ChangedPath{
					{Path: "src/a.go", Status: "modified"},
					{Path: "src/b.go", Status: "modified"},
					{Path: "src/gone.go", Status: "removed"},
					{Path: "src/new.go", Status: "renamed", PreviousPath: "src/old.go"},
				}
				return p
			},
			want: []string{"src/a.go", "src/b.go", "src/z.go"},
		},
		"reconcile entry without routed paths carries the set forward": {
			current: withRouted(t, "src/z.go"),
			reply:   alphaNew,
			plan: func(*testing.T) *plan.Plan {
				return reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), hunksEntry(alphaPath, testCursor))
			},
			want: []string{"src/a.go", "src/z.go"},
		},
		"unknown set stays unknown and the model's value is removed": {
			current: alphaPage,
			reply:   modelRouted,
			plan: func(*testing.T) *plan.Plan {
				return testPlan(updatePage(alphaPath, "src/a.go"))
			},
			want: nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			writeFile(t, e.kb, alphaPath, tc.current)
			e.llm.push(respond(tc.reply))
			rep, err := e.run(tc.plan(t))
			if err != nil {
				t.Fatal(err)
			}
			if rep.Pages[0].Result != ResultChanged {
				t.Fatalf("page = %+v, want changed", rep.Pages[0])
			}
			got := readFile(t, filepath.Join(e.kb, alphaPath))
			f, _, err := provenance.Parse([]byte(got))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.RoutedPaths, tc.want) {
				t.Errorf("routed_paths = %#v, want %#v\n%s", f.RoutedPaths, tc.want, got)
			}
			if tc.want == nil && strings.Contains(got, provenance.KeyRoutedPaths) {
				t.Errorf("page keeps routed_paths:\n%s", got)
			}
		})
	}
}

// A reply equal to the current page apart from routed_paths is unchanged.
func TestRunRoutedPathsOnlyIsUnchanged(t *testing.T) {
	e := newEnv(t)
	current := withRouted(t, "src/a.go")
	writeFile(t, e.kb, alphaPath, current)
	snap := tree(t, e.kb)
	reply := strings.Replace(current, "routed_paths:\n  - src/a.go\n", "routed_paths:\n  - src/fake.go\n", 1)
	if reply == current {
		t.Fatalf("fixture has no routed_paths:\n%s", current)
	}
	e.llm.push(respond(reply))
	rep, err := e.run(testPlan(updatePage(alphaPath, "src/a.go")))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultUnchanged {
		t.Errorf("page = %+v, want unchanged", rep.Pages[0])
	}
	requireSameTree(t, snap, tree(t, e.kb))
}

// routed_paths is kept out of the prompt and the budget, so a broad route does
// not push its page over --max-tokens-per-page; Stamp writes it back.
func TestRunRoutedPathsKeptOutOfPrompt(t *testing.T) {
	routed := []string{"src/a.go"}
	for i := range 2000 {
		routed = append(routed, fmt.Sprintf("src/gen/file%04d.go", i))
	}
	e := newEnv(t)
	writeFile(t, e.kb, alphaPath, withRouted(t, routed...))
	e.llm.push(respond(alphaNew))
	rep, err := e.run(testPlan(updatePage(alphaPath, "src/a.go")))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultChanged {
		t.Fatalf("page = %+v, want changed", rep.Pages[0])
	}
	if user := e.llm.requests()[0].User; strings.Contains(user, provenance.KeyRoutedPaths) {
		t.Errorf("prompt holds routed_paths")
	}
	f, _, err := provenance.Parse([]byte(readFile(t, filepath.Join(e.kb, alphaPath))))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.RoutedPaths) != len(routed) {
		t.Errorf("routed_paths has %d paths, want %d", len(f.RoutedPaths), len(routed))
	}
}
