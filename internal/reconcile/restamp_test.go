package reconcile

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/model"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/provenance"
	"github.com/gjed/okf-operator/internal/write"
)

const restampRouting = "version: 1\nsources:\n  - repo: acme/api\n    bundle: product\n    routes:\n" +
	"      - page: product/s.md\n        paths: [\"src/s.go\"]\n" +
	"      - page: product/w.md\n        paths: [\"src/a.go\", \"src/b.go\"]\n"

// noChangeModel answers NO_CHANGE to every prompt and counts the calls.
func noChangeModel(t *testing.T) (*model.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"role": "assistant", "content": write.NoChange}, "finish_reason": "stop",
		}}})
	}))
	t.Cleanup(srv.Close)
	return &model.Client{BaseURL: srv.URL + "/v1", Model: "m1"}, &calls
}

// A reconcile that finds one stale and one widened page, where the model
// answers NO_CHANGE for both, re-stamps them once: the next reconcile from the
// advanced state plans nothing and costs no model call.
func TestRestampedPagesAreNotPlannedAgain(t *testing.T) {
	f := newFixture(t, restampRouting, "src/s.go", "src/a.go", "src/b.go", "unrelated.go")
	f.cursor(f.head)
	stale := recorded(baseB, []string{"src/s.go"}, "src/s.go")
	widened := recorded(baseB, []string{"src/a.go"}, "src/a.go")
	f.page("product/s.md", stale)
	f.page("product/w.md", widened)
	f.fake.SetCompare(testSource, baseB, f.head, []byte(`{"status":"ahead","total_commits":1,"commits":[{"sha":"`+baseC+`"}],"files":[`+
		`{"filename":"src/s.go","status":"modified","changes":2,"patch":"@@ -1 +1 @@\n-old\n+new\n"},`+
		`{"filename":"unrelated.go","status":"modified","changes":2,"patch":"@@ -1 +1 @@\n-x\n+y\n"}]}`))

	first, err := f.build(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantEntries := []plan.ReconcilePage{
		{Path: "product/s.md", Reason: plan.ReasonStale, Evidence: plan.EvidenceHunks, Base: baseB, RoutedPaths: []string{"src/s.go"}},
		{Path: "product/w.md", Reason: plan.ReasonWidened, Evidence: plan.EvidenceFiles, RoutedPaths: []string{"src/a.go", "src/b.go"}},
	}
	if !reflect.DeepEqual(first.Reconcile.Pages, wantEntries) {
		t.Fatalf("first plan entries = %+v, want %+v", first.Reconcile.Pages, wantEntries)
	}

	llm, calls := noChangeModel(t)
	prov, err := github.NewProvider(github.Config{APIURL: f.fake.APIURL(), Token: "ghp_test0123456789"},
		github.ReadContents(testSource, testKB), &github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	run := func(p *plan.Plan) *write.Report {
		t.Helper()
		rep, err := write.Run(t.Context(), p, write.Deps{
			Source: plan.GitHubDiffer{Clients: prov}, Model: llm, Validator: write.BaseValidator{}, KnowledgeDir: f.dir,
			ModelName: "m1", ToolVersion: "1.0.0", MaxPages: 10, MaxTokensPerPage: 16000,
			Now: func() time.Time { return now }, Logger: slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	rep := run(first)
	if got := calls.Load(); got != 2 {
		t.Fatalf("first run made %d model calls, want 2", got)
	}
	for _, pg := range rep.Pages {
		if pg.Result != write.ResultRestamped {
			t.Fatalf("page %s = %s, want restamped", pg.Path, pg.Result)
		}
	}
	for page, c := range map[string]struct {
		before string
		routed []string
		paths  []string
	}{
		"product/s.md": {stale, []string{"src/s.go"}, []string{"src/s.go"}},
		"product/w.md": {widened, []string{"src/a.go", "src/b.go"}, []string{"src/b.go"}},
	} {
		raw, err := os.ReadFile(filepath.Join(f.dir, page))
		if err != nil {
			t.Fatal(err)
		}
		got, body, err := provenance.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		_, wantBody, _ := provenance.Parse([]byte(c.before))
		if string(body) != string(wantBody) {
			t.Errorf("%s body = %q, want %q", page, body, wantBody)
		}
		if got.SourceSHA != first.To || !reflect.DeepEqual(got.RoutedPaths, c.routed) || !reflect.DeepEqual(got.SourcePaths, c.paths) ||
			!got.UpdatedAt.Equal(now) || got.GeneratedBy != rep.GeneratedBy {
			t.Errorf("%s provenance = %+v", page, got)
		}
		if !strings.Contains(string(raw), "type: concept\n") {
			t.Errorf("%s lost its other frontmatter:\n%s", page, raw)
		}
	}

	second, err := f.build(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Pages) != 0 || len(second.Reconcile.Pages) != 0 {
		t.Fatalf("second plan = %+v %+v, want nothing", second.Pages, second.Reconcile.Pages)
	}
	run(second)
	if got := calls.Load(); got != 2 {
		t.Errorf("second reconcile made %d more model calls, want 0", got-2)
	}
}
