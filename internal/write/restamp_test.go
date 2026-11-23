package write

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/plan"
	"github.com/gjed/okf-operator/internal/provenance"
)

// commented is a page an earlier run stamped, with a comment, a custom key and
// a key order the re-stamp must keep.
const commented = "---\n" +
	"type: concept\n" +
	"# kept by the maintainers\n" +
	"owner: team-a\n" +
	"title: Alpha\n" +
	"source_repo: acme/api\n" +
	"source_sha: \"" + testCursor + "\"\n" +
	"source_paths:\n  - src/a.go\n" +
	"updated_at: 2026-11-01T08:00:00Z\n" +
	"generated_by: okf-operator/0.9.0/old\n" +
	"routed_paths:\n  - src/a.go\n" +
	"---\n# Alpha\n\nAlpha  does the thing.\n\n```\ncode  block\n```\n"

// restampedPage is commented after a re-stamp at testHead with the routed set
// src/a.go and src/b.go: provenance values change, nothing else does.
const restampedPage = "---\n" +
	"type: concept\n" +
	"# kept by the maintainers\n" +
	"owner: team-a\n" +
	"title: Alpha\n" +
	"source_repo: acme/api\n" +
	"source_sha: \"" + testHead + "\"\n" +
	"source_paths:\n  - src/a.go\n" +
	"updated_at: 2026-11-18T09:30:00Z\n" +
	"generated_by: okf-operator/1.2.3/m1\n" +
	"routed_paths:\n  - src/a.go\n  - src/b.go\n" +
	"---\n# Alpha\n\nAlpha  does the thing.\n\n```\ncode  block\n```\n"

func widenedEntry(path string, routed ...string) plan.ReconcilePage {
	return plan.ReconcilePage{Path: path, Reason: plan.ReasonWidened, Evidence: plan.EvidenceFiles, RoutedPaths: routed}
}

// For a reconcile plan, NO_CHANGE re-stamps only the provenance values: the
// body stays byte for byte; the other keys, their order and the comments are kept.
func TestRunReconcileNoChangeRestamps(t *testing.T) {
	cases := map[string]struct {
		entry func(path string) plan.ReconcilePage
		setup func(e *env)
	}{
		"stale page": {
			entry: func(path string) plan.ReconcilePage {
				en := hunksEntry(path, testCursor)
				en.RoutedPaths = []string{"src/a.go", "src/b.go"}
				return en
			},
		},
		"widened page": {
			entry: func(path string) plan.ReconcilePage { return widenedEntry(path, "src/a.go", "src/b.go") },
			setup: func(e *env) {
				e.gh.SetFile(testSource, testHead, "src/a.go", "package a\n")
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if tc.setup != nil {
				tc.setup(e)
			}
			writeFile(t, e.kb, alphaPath, commented)
			snap := tree(t, e.kb)
			e.llm.push(respond("NO_CHANGE"))
			rep, err := e.run(reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), tc.entry(alphaPath)))
			if err != nil {
				t.Fatal(err)
			}
			if pg := rep.Pages[0]; pg.Result != ResultRestamped || pg.Reason != "" || pg.Attempts != 1 {
				t.Fatalf("page = %+v, want restamped", pg)
			}
			if got := readFile(t, filepath.Join(e.kb, alphaPath)); got != restampedPage {
				t.Errorf("page =\n%s\nwant\n%s", got, restampedPage)
			}
			requireSameTree(t, snap, tree(t, e.kb), alphaPath)
			if n := len(e.llm.requests()); n != 1 {
				t.Errorf("model requests = %d, want 1", n)
			}
		})
	}
}

// A reply equal to the current page apart from provenance counts as NO_CHANGE.
func TestRunReconcileSameContentRestamps(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.kb, alphaPath, commented)
	e.llm.push(respond(strings.ReplaceAll(commented, testCursor, "deadbeef")))
	entry := hunksEntry(alphaPath, testCursor)
	entry.RoutedPaths = []string{"src/a.go", "src/b.go"}
	rep, err := e.run(reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), entry))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultRestamped {
		t.Fatalf("page = %+v, want restamped", rep.Pages[0])
	}
	if got := readFile(t, filepath.Join(e.kb, alphaPath)); got != restampedPage {
		t.Errorf("page =\n%s", got)
	}
}

// A page without provenance (a backfill) gets it, and its body is kept.
func TestRunReconcileBackfillRestamps(t *testing.T) {
	e := newEnv(t)
	e.gh.SetFile(testSource, testHead, "src/a.go", "package a\n")
	e.llm.push(respond("NO_CHANGE"))
	rep, err := e.run(reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), filesEntry(alphaPath)))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultRestamped {
		t.Fatalf("page = %+v, want restamped", rep.Pages[0])
	}
	f, body, err := provenance.Parse([]byte(readFile(t, filepath.Join(e.kb, alphaPath))))
	if err != nil {
		t.Fatal(err)
	}
	if _, want, _ := provenance.Parse([]byte(alphaPage)); string(body) != string(want) {
		t.Errorf("body = %q, want %q", body, want)
	}
	if f.SourceSHA != testHead || f.SourceRepo != testSource || !reflect.DeepEqual(f.SourcePaths, []string{"src/a.go"}) || !f.UpdatedAt.Equal(testNow) {
		t.Errorf("provenance = %+v", f)
	}
}

// A re-stamp that would not change the file is reported unchanged and writes nothing.
func TestRunReconcileRestampIdenticalIsUnchanged(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.kb, alphaPath, restampedPage)
	snap := tree(t, e.kb)
	e.llm.push(respond("NO_CHANGE"))
	entry := hunksEntry(alphaPath, testCursor)
	entry.RoutedPaths = []string{"src/a.go", "src/b.go"}
	rep, err := e.run(reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), entry), func(d *Deps) {
		d.Now = func() time.Time { return time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC) }
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultUnchanged {
		t.Errorf("page = %+v, want unchanged", rep.Pages[0])
	}
	requireSameTree(t, snap, tree(t, e.kb))
}

// A version 1 plan never re-stamps: NO_CHANGE leaves a page with older
// provenance untouched, so the file and its provenance stay as they were.
func TestRunVersion1NoChangeDoesNotRestamp(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.kb, alphaPath, commented)
	snap := tree(t, e.kb)
	e.llm.push(respond("NO_CHANGE"))
	rep, err := e.run(testPlan(updatePage(alphaPath, "src/a.go")))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultUnchanged {
		t.Errorf("page = %+v, want unchanged", rep.Pages[0])
	}
	requireSameTree(t, snap, tree(t, e.kb))
}

// A reconcile page whose reply is a real update stays changed, not restamped.
func TestRunReconcileUpdateStaysChanged(t *testing.T) {
	e := newEnv(t)
	e.llm.push(respond(alphaNew))
	entry := hunksEntry(alphaPath, testCursor)
	rep, err := e.run(reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), entry))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultChanged {
		t.Errorf("page = %+v, want changed", rep.Pages[0])
	}
}

// A model failure on a later page leaves the earlier page's re-stamp unwritten.
func TestRunReconcileRestampIsAllOrNothing(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.kb, alphaPath, commented)
	snap := tree(t, e.kb)
	e.llm.push(respond("NO_CHANGE"), failWith(500))
	entry := func(p string) plan.ReconcilePage {
		en := hunksEntry(p, testCursor)
		en.RoutedPaths = []string{"src/a.go", "src/b.go"}
		return en
	}
	_, err := e.run(reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go"), updatePage(betaPath, "src/a.go")), entry(alphaPath), entry(betaPath)))
	if err == nil {
		t.Fatal("want an error from the failing model call")
	}
	requireSameTree(t, snap, tree(t, e.kb))
}

type recordedSpans struct {
	tp  *sdktrace.TracerProvider
	rec *tracetest.SpanRecorder
}

// installTelemetry routes spans and counters to in-memory recorders.
func installTelemetry(t *testing.T) (recordedSpans, *sdkmetric.ManualReader) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	return recordedSpans{tp: tp, rec: rec}, reader
}

// The write report has version 3, and the counter and span attribute record
// a restamped page.
func TestRestampTelemetry(t *testing.T) {
	spans, reader := installTelemetry(t)
	e := newEnv(t)
	writeFile(t, e.kb, alphaPath, commented)
	e.llm.push(respond("NO_CHANGE"))
	entry := hunksEntry(alphaPath, testCursor)
	entry.RoutedPaths = []string{"src/a.go", "src/b.go"}
	ctx, span := spans.tp.Tracer("test").Start(t.Context(), "write")
	rep, err := Run(ctx, reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), entry), e.deps())
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Version != 3 {
		t.Errorf("report version = %d, want 3", rep.Version)
	}
	var attr any
	for _, kv := range spans.rec.Ended()[0].Attributes() {
		if string(kv.Key) == "okf.write.pages.restamped" {
			attr = kv.Value.AsInterface()
		}
	}
	if attr != int64(1) {
		t.Errorf("okf.write.pages.restamped = %v, want 1", attr)
	}
	c := counters(t, reader)
	for result, want := range map[string]int64{"restamped": 1, "changed": 0, "unchanged": 0} {
		if got := valueOf(t, c, "okf.write.pages", map[string]string{"okf.write.result": result}); got != want {
			t.Errorf("okf.write.pages{%s} = %d, want %d", result, got, want)
		}
	}
	if len(c) != 3 {
		t.Errorf("counters = %v, want only the three existing ones", c)
	}
}

// A reconcile page that cannot take provenance is not re-stamped, and NO_CHANGE
// is rejected for it, so the model is asked again instead of the page staying
// planned on every reconcile. A version 1 plan keeps accepting NO_CHANGE.
func TestRunReconcileNoChangeRejectedWithoutFrontmatter(t *testing.T) {
	const bare = "# Alpha\n\nNo frontmatter.\n"
	e := newEnv(t)
	writeFile(t, e.kb, alphaPath, bare)
	snap := tree(t, e.kb)
	e.llm.push(respond("NO_CHANGE"), respond("NO_CHANGE"))
	rep, err := e.run(reconcilePlan(testPlan(updatePage(alphaPath, "src/a.go")), hunksEntry(alphaPath, testCursor)))
	if err != nil {
		t.Fatal(err)
	}
	if pg := rep.Pages[0]; pg.Result != ResultDropped || pg.Reason != ReasonValidationFailed || pg.Attempts != 2 {
		t.Fatalf("page = %+v, want dropped validation_failed after 2 attempts", pg)
	}
	if reqs := e.llm.requests(); len(reqs) != 2 || !strings.Contains(reqs[1].User, "cannot take provenance") {
		t.Errorf("the retry does not name the error: %+v", reqs)
	}
	requireSameTree(t, snap, tree(t, e.kb))

	e = newEnv(t)
	writeFile(t, e.kb, alphaPath, bare)
	e.llm.push(respond("NO_CHANGE"))
	rep, err = e.run(testPlan(updatePage(alphaPath, "src/a.go")))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pages[0].Result != ResultUnchanged {
		t.Errorf("version 1 page = %+v, want unchanged", rep.Pages[0])
	}
}
