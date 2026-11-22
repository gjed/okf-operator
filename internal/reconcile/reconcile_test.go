package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/cursor"
	"github.com/gjed/okf-operator/internal/github"
	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/routing"
)

const (
	testSource = "acme/api"
	testKB     = "acme/kb"
	cursorX    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	baseB      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	baseC      = "cccccccccccccccccccccccccccccccccccccccc"
	baseD      = "dddddddddddddddddddddddddddddddddddddddd"
	baseE      = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

// fixture is a source repo on the fake whose main branch holds the given
// files, a knowledge repo, and a knowledge checkout.
type fixture struct {
	t    *testing.T
	fake *githubtest.Fake
	dir  string
	head string
	rm   *routing.Map
}

func newFixture(t *testing.T, routingYAML string, files ...string) *fixture {
	t.Helper()
	f := &fixture{t: t, fake: githubtest.New(t), dir: t.TempDir()}
	for _, p := range files {
		f.fake.SetFile(testSource, "main", p, "content of "+p+"\n")
	}
	f.head = f.fake.Ref(testSource, "main")
	f.fake.SetRepo(testSource, "main", f.head)
	f.fake.SetRepo(testKB, "main", "")
	rm, err := routing.Parse([]byte(routingYAML))
	if err != nil {
		t.Fatal(err)
	}
	f.rm = rm
	return f
}

func (f *fixture) page(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) cursor(sha string) {
	f.fake.SetFile(testKB, cursor.Branch, cursor.Path(testSource), sha+"\n")
}

func (f *fixture) compare(base, status string, files ...map[string]any) {
	f.t.Helper()
	if files == nil {
		files = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"status": status, "files": files})
	if err != nil {
		f.t.Fatal(err)
	}
	f.fake.SetCompare(testSource, base, f.head, b)
}

func (f *fixture) build(ctx context.Context) (*plan.Plan, error) {
	f.t.Helper()
	p, err := github.NewProvider(github.Config{APIURL: f.fake.APIURL(), Token: "ghp_test0123456789"},
		github.ReadContents(testSource, testKB), &github.Secrets{}, slog.New(slog.DiscardHandler))
	if err != nil {
		f.t.Fatal(err)
	}
	return Build(ctx, testSource, "", Deps{
		Routing:      f.rm,
		Cursor:       cursor.Reader{Clients: p, KnowledgeRepo: testKB},
		Differ:       plan.GitHubDiffer{Clients: p},
		Tree:         GitHubTree{Clients: p},
		KnowledgeDir: f.dir,
		Logger:       slog.New(slog.DiscardHandler),
	})
}

// generated is a page written by an earlier run at sha from paths.
func generated(repo, sha string, paths ...string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = fmt.Sprintf("%q", p)
	}
	return "---\ntype: concept\nsource_repo: " + repo + "\nsource_sha: \"" + sha + "\"\nsource_paths: [" + strings.Join(quoted, ", ") + "]\n---\n# Page\n"
}

func modified(path string) map[string]any {
	return map[string]any{"filename": path, "status": "modified"}
}

// A source with no cursor and none of its pages: create routes are created,
// routes without a type are reported missing, unmatched routes give nothing.
func TestBackfill(t *testing.T) {
	f := newFixture(t, `version: 1
sources:
  - repo: acme/api
    bundle: product
    routes:
      - page: product/guide.md
        type: concept
        paths: ["docs/**"]
      - page: product/api.md
        paths: ["src/**"]
      - page: product/none.md
        type: concept
        paths: ["nothing/**"]
`, "docs/a.md", "docs/b.md", "src/x.go", "README.md")
	p, err := f.build(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 2 || !p.Initial || p.From != "" || p.To != f.head || p.Reconcile.Cursor != plan.CursorMissing {
		t.Fatalf("plan header = %+v", p)
	}
	wantPages := []plan.Page{
		{Path: "product/api.md", State: plan.StateMissing, SourcePaths: []string{"src/x.go"}},
		{Path: "product/guide.md", State: plan.StateCreate, Type: "concept", SourcePaths: []string{"docs/a.md", "docs/b.md"}},
	}
	if !reflect.DeepEqual(p.Pages, wantPages) {
		t.Errorf("pages = %+v, want %+v", p.Pages, wantPages)
	}
	wantEntries := []plan.ReconcilePage{
		{Path: "product/api.md", Reason: plan.ReasonBackfill, Evidence: plan.EvidenceFiles, RoutedPaths: []string{"src/x.go"}},
		{Path: "product/guide.md", Reason: plan.ReasonBackfill, Evidence: plan.EvidenceFiles, RoutedPaths: []string{"docs/a.md", "docs/b.md"}},
	}
	if !reflect.DeepEqual(p.Reconcile.Pages, wantEntries) || len(p.Reconcile.DeletionCandidates) != 0 {
		t.Errorf("reconcile = %+v", p.Reconcile)
	}
}

const mixedRouting = `version: 1
sources:
  - repo: acme/api
    bundle: product
    routes:
      - page: product/stale.md
        paths: ["src/a.go", "src/b.go"]
      - page: product/current.md
        paths: ["lib/**"]
      - page: product/head.md
        paths: ["cmd/**"]
      - page: product/renamed.md
        paths: ["old/**", "new/**"]
      - page: product/gone.md
        paths: ["gone/**"]
      - page: product/hand.md
        paths: ["README.md"]
      - page: product/diverged.md
        paths: ["src/a.go"]
      - page: product/big.md
        paths: ["big/**"]
`

// mixedFixture has one page of each kind and a diverged cursor.
func mixedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, mixedRouting,
		"src/a.go", "src/b.go", "lib/x.go", "cmd/main.go", "new/r.go", "README.md", "big/1.go", "unrelated.go")
	f.cursor(cursorX)
	f.compare(cursorX, "diverged")
	f.compare(baseB, "ahead", modified("src/b.go"), modified("unrelated.go"))
	f.compare(baseC, "ahead", map[string]any{"filename": "new/r.go", "status": "renamed", "previous_filename": "old/r.go"})
	f.compare(baseD, "diverged")
	big := make([]map[string]any, plan.MaxCompareFiles)
	for i := range big {
		big[i] = modified(fmt.Sprintf("f%d.go", i))
	}
	f.compare(baseE, "ahead", big...)

	f.page("product/stale.md", generated(testSource, baseB, "src/a.go"))
	f.page("product/current.md", generated(testSource, baseB, "lib/x.go"))
	f.page("product/head.md", generated(testSource, f.head, "cmd/main.go"))
	f.page("product/renamed.md", generated(testSource, baseC, "old/r.go"))
	f.page("product/gone.md", generated(testSource, baseB, "gone/g.go"))
	f.page("product/hand.md", "# Hand written\n")
	f.page("product/diverged.md", generated(testSource, baseD, "src/a.go"))
	f.page("product/big.md", generated(testSource, baseE, "big/1.go"))
	f.page("product/orphan.md", generated("ACME/api", baseB, "removed.go"))
	f.page("product/other.md", generated("acme/other", baseB, "x.go"))
	f.page("product/bad.md", "---\n- not a mapping\n---\n")
	f.page("product/index.md", generated(testSource, baseB, "removed.go"))
	return f
}

func TestMixed(t *testing.T) {
	f := mixedFixture(t)
	p, err := f.build(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.Initial || p.From != cursorX || p.Reconcile.Cursor != plan.CursorDiverged {
		t.Fatalf("plan header = %+v", p)
	}
	wantPages := []plan.Page{
		{Path: "product/big.md", State: plan.StateUpdate, SourcePaths: []string{"big/1.go"}},
		{Path: "product/diverged.md", State: plan.StateUpdate, SourcePaths: []string{"src/a.go"}},
		{Path: "product/hand.md", State: plan.StateUpdate, SourcePaths: []string{"README.md"}},
		{Path: "product/renamed.md", State: plan.StateUpdate, SourcePaths: []string{"new/r.go", "old/r.go"}},
		{Path: "product/stale.md", State: plan.StateUpdate, SourcePaths: []string{"src/b.go"}},
	}
	if !reflect.DeepEqual(p.Pages, wantPages) {
		t.Errorf("pages = %+v\nwant    %+v", p.Pages, wantPages)
	}
	wantEntries := []plan.ReconcilePage{
		{Path: "product/big.md", Reason: plan.ReasonStale, Evidence: plan.EvidenceFiles, RoutedPaths: []string{"big/1.go"}},
		{Path: "product/diverged.md", Reason: plan.ReasonBackfill, Evidence: plan.EvidenceFiles, RoutedPaths: []string{"src/a.go"}},
		{Path: "product/hand.md", Reason: plan.ReasonBackfill, Evidence: plan.EvidenceFiles, RoutedPaths: []string{"README.md"}},
		{Path: "product/renamed.md", Reason: plan.ReasonRename, Evidence: plan.EvidenceHunks, Base: baseC, RoutedPaths: []string{"new/r.go"}},
		// No recorded routed set: src/a.go is not planned as widened.
		{Path: "product/stale.md", Reason: plan.ReasonStale, Evidence: plan.EvidenceHunks, Base: baseB, RoutedPaths: []string{"src/a.go", "src/b.go"}},
	}
	if !reflect.DeepEqual(p.Reconcile.Pages, wantEntries) {
		t.Errorf("entries = %+v\nwant      %+v", p.Reconcile.Pages, wantEntries)
	}
	wantCandidates := []plan.DeletionCandidate{
		{Path: "product/gone.md", SourceSHA: baseB, SourcePaths: []string{"gone/g.go"}},
		{Path: "product/orphan.md", SourceSHA: baseB, SourcePaths: []string{"removed.go"}},
	}
	if !reflect.DeepEqual(p.Reconcile.DeletionCandidates, wantCandidates) {
		t.Errorf("candidates = %+v", p.Reconcile.DeletionCandidates)
	}
	if n := strings.Count(strings.Join(f.fake.Requests(), "\n"), "/compare/"+baseB); n != 1 {
		t.Errorf("base %s compared %d times, want once", baseB, n)
	}
	for _, w := range f.fake.Writes() {
		t.Errorf("reconcile wrote to GitHub: %s", w)
	}
}

func TestDeterministicOutput(t *testing.T) {
	f := mixedFixture(t)
	var out [2]bytes.Buffer
	for i := range out {
		p, err := f.build(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := plan.Encode(&out[i], p); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(out[0].Bytes(), out[1].Bytes()) {
		t.Errorf("outputs differ:\n%s\n%s", out[0].String(), out[1].String())
	}
	if !strings.HasPrefix(out[0].String(), "{\n  \"version\": 2,") || !strings.Contains(out[0].String(), "\n  \"reconcile\": {\n    \"cursor\": \"diverged\",") {
		t.Errorf("unexpected encoding:\n%s", out[0].String())
	}
}

func TestFailures(t *testing.T) {
	const oneRoute = "version: 1\nsources:\n  - repo: acme/api\n    bundle: product\n    routes:\n      - page: product/a.md\n        paths: [\"src/**\"]\n"
	t.Run("truncated tree", func(t *testing.T) {
		f := newFixture(t, oneRoute, "src/a.go")
		f.fake.TruncateTrees = true
		if _, err := f.build(t.Context()); !errors.Is(err, ErrTruncated) {
			t.Fatalf("err = %v, want ErrTruncated", err)
		}
	})
	t.Run("not onboarded", func(t *testing.T) {
		f := newFixture(t, strings.Replace(oneRoute, "acme/api", "acme/web", 1), "src/a.go")
		if _, err := f.build(t.Context()); !errors.Is(err, ErrNotOnboarded) {
			t.Fatalf("err = %v, want ErrNotOnboarded", err)
		}
	})
	t.Run("malformed route target", func(t *testing.T) {
		f := newFixture(t, oneRoute, "src/a.go")
		f.page("product/a.md", "---\n- not a mapping\n---\n")
		if _, err := f.build(t.Context()); err == nil || !strings.Contains(err.Error(), "product/a.md") {
			t.Fatalf("err = %v, want an error naming the page", err)
		}
	})
	t.Run("cursor ahead of to", func(t *testing.T) {
		f := newFixture(t, oneRoute, "src/a.go")
		f.cursor(cursorX)
		f.compare(cursorX, "behind")
		if _, err := f.build(t.Context()); !errors.Is(err, ErrRollback) {
			t.Fatalf("err = %v, want ErrRollback", err)
		}
	})
	t.Run("page ahead of to", func(t *testing.T) {
		f := newFixture(t, oneRoute, "src/a.go")
		f.page("product/a.md", generated(testSource, baseB, "src/a.go"))
		f.compare(baseB, "behind")
		if _, err := f.build(t.Context()); !errors.Is(err, ErrRollback) || !strings.Contains(err.Error(), "product/a.md") {
			t.Fatalf("err = %v, want ErrRollback naming the page", err)
		}
	})
	t.Run("compare error", func(t *testing.T) {
		f := newFixture(t, oneRoute, "src/a.go")
		f.page("product/a.md", generated(testSource, baseB, "src/a.go"))
		f.fake.FailStatus["/repos/acme/api/compare/"+baseB+"..."+f.head] = 500
		if _, err := f.build(t.Context()); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	f := mixedFixture(t)
	ctx, span := tp.Tracer("test").Start(context.Background(), "reconcile")
	_, err := f.build(ctx)
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	ended := spans.Ended()
	if len(ended) != 1 || ended[0].Name() != "reconcile" {
		t.Fatalf("want exactly the reconcile span, got %d", len(ended))
	}
	got := map[attribute.Key]any{}
	for _, kv := range ended[0].Attributes() {
		got[kv.Key] = kv.Value.AsInterface()
	}
	want := map[attribute.Key]any{
		"okf.source.repo":                   testSource,
		"okf.bundle":                        "product",
		"okf.reconcile.from":                cursorX,
		"okf.reconcile.to":                  f.head,
		"okf.reconcile.cursor":              "diverged",
		"okf.reconcile.re_anchor":           true,
		"okf.reconcile.deletion_candidates": int64(2),
		"okf.reconcile.pages.backfill":      int64(2),
		"okf.reconcile.pages.stale":         int64(2),
		"okf.reconcile.pages.rename":        int64(1),
		"okf.reconcile.pages.widened":       int64(0),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("span attribute %s = %v, want %v", k, got[k], v)
		}
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	counters := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s: unexpected data %T", m.Name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				repo, _ := dp.Attributes.Value("okf.source.repo")
				bundle, _ := dp.Attributes.Value("okf.bundle")
				if repo.AsString() != testSource || bundle.AsString() != "product" {
					t.Errorf("%s attributes = %v", m.Name, dp.Attributes.ToSlice())
				}
				key := m.Name
				if r, ok := dp.Attributes.Value("okf.reconcile.reason"); ok {
					key += "{" + r.AsString() + "}"
				}
				counters[key] = dp.Value
			}
		}
	}
	wantCounters := map[string]int64{
		"okf.reconcile.pages{backfill}":     2,
		"okf.reconcile.pages{stale}":        2,
		"okf.reconcile.pages{rename}":       1,
		"okf.reconcile.pages{widened}":      0,
		"okf.reconcile.deletion_candidates": 2,
		"okf.reconcile.re_anchors":          1,
	}
	if !reflect.DeepEqual(counters, wantCounters) {
		t.Errorf("counters = %v, want %v", counters, wantCounters)
	}
}
