package propose

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
)

const botBranch = "okf/acme/api"

// knowledge is the fixture's knowledge repo with polling that does not slow
// the tests down.
func (f *fixture) knowledge(t *testing.T) GitHubKnowledge {
	t.Helper()
	k := f.deps(t).Knowledge.(GitHubKnowledge)
	k.Poll = github.Poll{Reads: 5, Delay: time.Millisecond}
	return k
}

// botPR branches the bot branch off main, commits files on it and opens a PR
// from it whose merge state reads answer states in turn.
func (f *fixture) botPR(t *testing.T, body string, files map[string]string, states ...string) *Pull {
	t.Helper()
	f.fake.Branch(testKB, "main", botBranch)
	for p, c := range files {
		f.fake.SetFile(testKB, botBranch, p, c)
	}
	n := f.fake.AddPullRequest(testKB, githubtest.PullRequest{
		Title: "t", Body: body, Head: botBranch, Base: "main", State: "open", MergeStates: states,
	})
	return &Pull{Number: n, Body: body, HeadSHA: f.fake.Ref(testKB, botBranch)}
}

func (f *fixture) pullReads(n int) int {
	reads := 0
	for _, r := range f.fake.Requests() {
		if r == "GET /repos/"+testKB+"/pulls/"+strconv.Itoa(n) {
			reads++
		}
	}
	return reads
}

func TestSyncPullStates(t *testing.T) {
	tests := []struct {
		name       string
		states     []string
		wantState  string
		wantDirty  bool
		wantUpdate bool
		wantReads  int
	}{
		{"clean", []string{"clean"}, "clean", false, false, 1},
		{"blocked", []string{"blocked"}, "blocked", false, false, 1},
		{"no state", nil, "", false, false, 1},
		{"behind", []string{"behind"}, "behind", false, true, 2},
		{"dirty", []string{"dirty"}, "dirty", true, false, 1},
		{"unknown then resolved", []string{"unknown", "unknown", "behind"}, "behind", false, true, 4},
		{"unknown after every read", []string{"unknown"}, "unknown", false, false, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			pull := f.botPR(t, "body", map[string]string{"docs/b.md": "bot\n"}, tt.states...)
			f.fake.SetFile(testKB, "main", "docs/extra.md", "default\n")
			defHead := f.fake.Ref(testKB, "main")

			got, err := SyncPull(t.Context(), f.knowledge(t), testSource, pull)
			if err != nil {
				t.Fatal(err)
			}
			head := f.fake.Ref(testKB, botBranch)
			if got.State != tt.wantState || got.Dirty != tt.wantDirty {
				t.Fatalf("synced = %+v", got)
			}
			if r := f.pullReads(pull.Number); r != tt.wantReads {
				t.Errorf("pull reads = %d, want %d", r, tt.wantReads)
			}
			calls := f.fake.UpdateBranches()
			if !tt.wantUpdate {
				if len(calls) != 0 || head != pull.HeadSHA || got.Head != pull.HeadSHA {
					t.Fatalf("update calls %v, head %s, synced head %s", calls, head, got.Head)
				}
				return
			}
			if want := []githubtest.UpdateBranch{{Number: pull.Number, ExpectedHeadSHA: pull.HeadSHA}}; !slices.Equal(calls, want) {
				t.Fatalf("update calls = %v, want %v", calls, want)
			}
			if got.Head != head || head == pull.HeadSHA {
				t.Fatalf("synced head %s, branch head %s, old head %s", got.Head, head, pull.HeadSHA)
			}
			if p := f.fake.Parents(testKB, head); !slices.Equal(p, []string{pull.HeadSHA, defHead}) {
				t.Fatalf("parents = %v", p)
			}
			if files := f.fake.Files(testKB, head); files["docs/b.md"] != "bot\n" || files["docs/extra.md"] != "default\n" {
				t.Fatalf("files = %v", files)
			}
		})
	}
}

// A head that moved after the run read it makes GitHub refuse with a 422, and
// the run writes nothing else.
func TestSyncPullHeadMoved(t *testing.T) {
	f := newFixture(t)
	pull := f.botPR(t, "body", map[string]string{"docs/b.md": "bot\n"}, "behind")
	f.fake.SetFile(testKB, "main", "docs/extra.md", "default\n")
	f.fake.SetHook(func(r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/update-branch") {
			f.fake.SetFile(testKB, botBranch, "docs/c.md", "pushed meanwhile\n")
		}
	})
	_, err := SyncPull(t.Context(), f.knowledge(t), testSource, pull)
	if !errors.Is(err, ErrUpdateRefused) {
		t.Fatalf("err = %v, want ErrUpdateRefused", err)
	}
	if w := f.fake.Writes(); !slices.Equal(w, []string{"PUT /repos/" + testKB + "/pulls/" + strconv.Itoa(pull.Number) + "/update-branch"}) {
		t.Fatalf("writes = %v", w)
	}
	if f.fake.Files(testKB, botBranch)["docs/c.md"] != "pushed meanwhile\n" {
		t.Fatal("the other push was lost")
	}
}

func TestSyncPullBranchNeverMoves(t *testing.T) {
	f := newFixture(t)
	pull := f.botPR(t, "body", map[string]string{"docs/b.md": "bot\n"}, "behind")
	f.fake.SetFile(testKB, "main", "docs/extra.md", "default\n")
	f.fake.HoldUpdateBranch = true
	_, err := SyncPull(t.Context(), f.knowledge(t), testSource, pull)
	if err == nil || !strings.Contains(err.Error(), "the branch did not move") {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncPullCountsRepairs(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(sdkmetric.NewMeterProvider()) })

	f := newFixture(t)
	pull := f.botPR(t, "body", map[string]string{"docs/b.md": "bot\n"}, "behind")
	f.fake.SetFile(testKB, "main", "docs/extra.md", "default\n")
	if _, err := SyncPull(t.Context(), f.knowledge(t), testSource, pull); err != nil {
		t.Fatal(err)
	}
	if got := repairCounts(t, reader); got["okf.propose.repair=update_branch"] != 1 || len(got) != 1 {
		t.Fatalf("repairs = %v", got)
	}
}

// repairCounts reads okf.propose.prs.repaired by its repair attribute.
func repairCounts(t *testing.T, reader sdkmetric.Reader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "okf.propose.prs.repaired" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				if v, _ := dp.Attributes.Value("okf.source.repo"); v.AsString() != testSource {
					t.Fatalf("repaired lacks the source label: %v", dp.Attributes)
				}
				kind, _ := dp.Attributes.Value("okf.propose.repair")
				got["okf.propose.repair="+kind.AsString()] += dp.Value
			}
		}
	}
	return got
}
