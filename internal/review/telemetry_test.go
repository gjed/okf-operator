package review

import (
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/github/githubtest"
)

// TestTelemetry reviews one passing and one failing page under an in-memory
// span recorder and metric reader. It sets the global MeterProvider, so it
// must not run in parallel with other tests that do.
func TestTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	f := newFixture(t, answer(`{"verdict":"unsupported","unsupported":["SECRET-SENTENCE"]}`))
	f.files = append(f.files, githubtest.PullFile{Filename: "product/other.md", Status: "modified", Additions: 1})

	ctx, span := tp.Tracer("test").Start(t.Context(), "review")
	r, err := f.runCtx(ctx, t)
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	if r.Pass || len(r.Pages) != 2 {
		t.Fatalf("result = %+v", r)
	}

	ended := spans.Ended()
	if len(ended) != 1 || ended[0].Name() != "review" {
		t.Fatalf("spans = %v, want only the dispatcher's review span", ended)
	}
	attrs := map[string]string{}
	for _, kv := range ended[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
		if strings.Contains(kv.Value.String(), "SECRET-SENTENCE") || strings.Contains(kv.Value.String(), "Rate limit") {
			t.Errorf("attribute %s carries page or reply text", kv.Key)
		}
	}
	for k, v := range map[string]string{
		"okf.knowledge.repo":           kbRepo,
		"okf.source.repo":              srcRepo,
		"okf.bundle":                   "product",
		"okf.review.pr":                "7",
		"okf.review.head_sha":          headSHA,
		"okf.review.model":             "reviewer",
		"okf.review.pass":              "false",
		"okf.review.pages.pass":        "0",
		"okf.review.pages.fail":        "2",
		"okf.review.tokens.prompt":     "100",
		"okf.review.tokens.completion": "10",
	} {
		if attrs[k] != v {
			t.Errorf("span attribute %s = %q, want %q", k, attrs[k], v)
		}
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				key := m.Name
				for _, a := range []string{"okf.review.result", "okf.token.kind"} {
					if v, ok := dp.Attributes.Value(attribute.Key(a)); ok {
						key += " " + v.AsString()
					}
				}
				got[key] += dp.Value
			}
		}
	}
	want := map[string]int64{
		"okf.review.pages pass":        0,
		"okf.review.pages fail":        2,
		"okf.review.tokens prompt":     100,
		"okf.review.tokens completion": 10,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}
