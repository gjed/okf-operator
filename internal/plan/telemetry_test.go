package plan

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	// The dispatcher opens the span; Build only annotates it.
	ctx, span := tp.Tracer("test").Start(context.Background(), "plan")
	d := &fakeDiffer{changes: []ChangedPath{
		{Path: "deploy/values.yaml", Status: "modified"},
		{Path: "deploy/api.yaml", Status: "modified"},
		{Path: "README.md", Status: "modified"},
		{Path: "deploy/secrets/x", Status: "added"},
	}}
	r := buildCtx(ctx, t, d)
	span.End()
	if r.err != nil {
		t.Fatal(r.err)
	}

	ended := spans.Ended()
	if len(ended) != 1 || ended[0].Name() != "plan" {
		t.Fatalf("want exactly the one plan span, got %d", len(ended))
	}
	got := map[attribute.Key]attribute.Value{}
	for _, kv := range ended[0].Attributes() {
		got[kv.Key] = kv.Value
	}
	want := map[attribute.Key]any{
		"okf.source.repo":         "acme/platform",
		"okf.bundle":              "infra",
		"okf.plan.from":           cursorSHA,
		"okf.plan.to":             headSHA,
		"okf.plan.initial":        false,
		"okf.plan.changed_paths":  int64(4),
		"okf.plan.routed_pages":   int64(2),
		"okf.plan.unrouted_paths": int64(1),
		"okf.plan.excluded_paths": int64(1),
	}
	for k, v := range want {
		if got[k].AsInterface() != v {
			t.Errorf("span attribute %s = %v, want %v", k, got[k].AsInterface(), v)
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
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("%s: unexpected data %T", m.Name, m.Data)
			}
			dp := sum.DataPoints[0]
			repo, _ := dp.Attributes.Value("okf.source.repo")
			bundle, _ := dp.Attributes.Value("okf.bundle")
			if repo.AsString() != "acme/platform" || bundle.AsString() != "infra" || dp.Attributes.Len() != 2 {
				t.Errorf("%s attributes = %v", m.Name, dp.Attributes.ToSlice())
			}
			counters[m.Name] = dp.Value
		}
	}
	wantCounters := map[string]int64{
		"okf.plan.changed_paths":  4,
		"okf.plan.routed_pages":   2,
		"okf.plan.unrouted_paths": 1,
		"okf.plan.excluded_paths": 1,
	}
	if len(counters) != len(wantCounters) {
		t.Fatalf("counters = %v", counters)
	}
	for k, v := range wantCounters {
		if counters[k] != v {
			t.Errorf("counter %s = %d, want %d", k, counters[k], v)
		}
	}
}
