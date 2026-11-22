package reconcile

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/plan"
)

const widenedRouting = "version: 1\nsources:\n  - repo: acme/api\n    bundle: product\n    routes:\n      - page: product/w.md\n        paths: [\"src/**\"]\n"

// recorded is generated with a routed_paths list.
func recorded(sha string, routed []string, paths ...string) string {
	return strings.Replace(generated(testSource, sha, paths...), "---\n# Page",
		"routed_paths: [\""+strings.Join(routed, "\", \"")+"\"]\n---\n# Page", 1)
}

func TestWidenedRoutes(t *testing.T) {
	routedAt := []string{"src/a.go", "src/b.go"}
	cases := map[string]struct {
		page    func(f *fixture) string
		compare func(f *fixture)
		want    *plan.Page
		entry   plan.ReconcilePage
	}{
		"route widened to an unchanged file": {
			page:    func(*fixture) string { return recorded(baseB, []string{"src/a.go"}, "src/a.go") },
			compare: func(f *fixture) { f.compare(baseB, "ahead", modified("src/a.go"), modified("unrelated.go")) },
			want:    &plan.Page{Path: "product/w.md", State: plan.StateUpdate, SourcePaths: []string{"src/a.go", "src/b.go"}},
			entry:   plan.ReconcilePage{Path: "product/w.md", Reason: plan.ReasonWidened, Evidence: plan.EvidenceFiles, RoutedPaths: routedAt},
		},
		"route widened, nothing changed since": {
			page:    func(*fixture) string { return recorded(baseB, []string{"src/a.go"}, "src/a.go") },
			compare: func(f *fixture) { f.compare(baseB, "ahead", modified("unrelated.go")) },
			want:    &plan.Page{Path: "product/w.md", State: plan.StateUpdate, SourcePaths: []string{"src/b.go"}},
			entry:   plan.ReconcilePage{Path: "product/w.md", Reason: plan.ReasonWidened, Evidence: plan.EvidenceFiles, RoutedPaths: routedAt},
		},
		"route widened at the end commit": {
			page:  func(f *fixture) string { return recorded(f.head, []string{"src/a.go"}, "src/a.go") },
			want:  &plan.Page{Path: "product/w.md", State: plan.StateUpdate, SourcePaths: []string{"src/b.go"}},
			entry: plan.ReconcilePage{Path: "product/w.md", Reason: plan.ReasonWidened, Evidence: plan.EvidenceFiles, RoutedPaths: routedAt},
		},
		"file added under the route stays stale": {
			page: func(*fixture) string { return recorded(baseB, []string{"src/a.go"}, "src/a.go") },
			compare: func(f *fixture) {
				f.compare(baseB, "ahead", map[string]any{"filename": "src/b.go", "status": "added"})
			},
			want:  &plan.Page{Path: "product/w.md", State: plan.StateUpdate, SourcePaths: []string{"src/b.go"}},
			entry: plan.ReconcilePage{Path: "product/w.md", Reason: plan.ReasonStale, Evidence: plan.EvidenceHunks, Base: baseB, RoutedPaths: routedAt},
		},
		"recorded set covers the route": {
			page: func(f *fixture) string { return recorded(f.head, routedAt, "src/a.go") },
		},
		"no recorded set at the end commit, as before": {
			page: func(f *fixture) string { return generated(testSource, f.head, "src/a.go") },
		},
		"no recorded set behind the end commit, as before": {
			page:    func(*fixture) string { return generated(testSource, baseB, "src/a.go") },
			compare: func(f *fixture) { f.compare(baseB, "ahead", modified("unrelated.go")) },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, widenedRouting, "src/a.go", "src/b.go", "unrelated.go")
			f.cursor(f.head)
			f.page("product/w.md", tc.page(f))
			if tc.compare != nil {
				tc.compare(f)
			}
			p, err := f.build(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if len(p.Pages) != 0 || len(p.Reconcile.Pages) != 0 {
					t.Fatalf("planned %+v %+v, want nothing", p.Pages, p.Reconcile.Pages)
				}
				return
			}
			if !reflect.DeepEqual(p.Pages, []plan.Page{*tc.want}) {
				t.Errorf("pages = %+v, want %+v", p.Pages, *tc.want)
			}
			if !reflect.DeepEqual(p.Reconcile.Pages, []plan.ReconcilePage{tc.entry}) {
				t.Errorf("entries = %+v, want %+v", p.Reconcile.Pages, tc.entry)
			}
		})
	}
}

func TestWidenedTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	f := newFixture(t, widenedRouting, "src/a.go", "src/b.go")
	f.cursor(f.head)
	f.page("product/w.md", recorded(f.head, []string{"src/a.go"}, "src/a.go"))
	ctx, span := tp.Tracer("test").Start(context.Background(), "reconcile")
	_, err := f.build(ctx)
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	var widenedAttr any
	for _, kv := range spans.Ended()[0].Attributes() {
		if kv.Key == "okf.reconcile.pages.widened" {
			widenedAttr = kv.Value.AsInterface()
		}
	}
	if widenedAttr != int64(1) {
		t.Errorf("okf.reconcile.pages.widened = %v, want 1", widenedAttr)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	pages := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "okf.reconcile.pages" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				r, _ := dp.Attributes.Value(attribute.Key("okf.reconcile.reason"))
				pages[r.AsString()] = dp.Value
			}
		}
	}
	want := map[string]int64{"backfill": 0, "stale": 0, "rename": 0, "widened": 1}
	if !reflect.DeepEqual(pages, want) {
		t.Errorf("okf.reconcile.pages = %v, want %v", pages, want)
	}
}
