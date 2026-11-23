package write

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/plan"
)

// point is one data point of a counter: its attributes as strings and its value.
type point struct {
	attrs map[string]string
	value int64
}

// counters collects every int64 sum the reader holds, keyed by metric name.
func counters(t *testing.T, r *sdkmetric.ManualReader) map[string][]point {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string][]point{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s: unexpected data %T", m.Name, m.Data)
			}
			for _, dp := range sum.DataPoints {
				p := point{attrs: map[string]string{}, value: dp.Value}
				for _, kv := range dp.Attributes.ToSlice() {
					p.attrs[string(kv.Key)] = kv.Value.AsString()
				}
				out[m.Name] = append(out[m.Name], p)
			}
		}
	}
	return out
}

// valueOf returns the value of the one point of name whose attributes include
// every pair in match, and fails when there is not exactly one.
func valueOf(t *testing.T, got map[string][]point, name string, match map[string]string) int64 {
	t.Helper()
	var found []int64
	for _, p := range got[name] {
		ok := true
		for k, v := range match {
			if p.attrs[k] != v {
				ok = false
			}
		}
		if ok {
			found = append(found, p.value)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s %v: %d matching points, want 1 (all: %+v)", name, match, len(found), got[name])
	}
	return found[0]
}

// TestTelemetry runs one mixed write under an in-memory span recorder and
// metric reader, and checks the write span and the three counters. It sets the
// global MeterProvider, so it must not run in parallel with other tests that do.
// A run that aborts on a model error still records the tokens and validation
// failures it spent, but no page results.
func TestTelemetryOnAbort(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	e := newEnv(t)
	e.llm.push(withUsage(respond(alphaNew), 100, 40), withUsage(respond("bad"), 50, 5), failWith(500))
	if _, err := Run(t.Context(), testPlan(updatePage(alphaPath, "src/a.go"), updatePage(betaPath, "src/a.go")), e.deps()); err == nil {
		t.Fatal("Run succeeded after HTTP 500")
	}
	c := counters(t, reader)
	if _, ok := c["okf.write.pages"]; ok {
		t.Error("okf.write.pages recorded for an aborted run")
	}
	repo := map[string]string{"okf.source.repo": testSource, "okf.bundle": testBundle}
	for _, tc := range []struct {
		name, kind string
		want       int64
	}{{"okf.write.tokens", "prompt", 150}, {"okf.write.tokens", "completion", 45}, {"okf.write.validation_failures", "", 1}} {
		match := map[string]string{"okf.source.repo": repo["okf.source.repo"], "okf.bundle": repo["okf.bundle"]}
		if tc.kind != "" {
			match["okf.token.kind"] = tc.kind
		}
		if got := valueOf(t, c, tc.name, match); got != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.name, tc.kind, got, tc.want)
		}
	}
}

func TestTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	e := newEnv(t)
	// a changes, b is unchanged, c is invalid twice and dropped, d is created.
	e.llm.push(
		withUsage(respond(alphaNew), 100, 40),
		withUsage(respond("NO_CHANGE"), 90, 3),
		withUsage(respond("bad"), 120, 5),
		withUsage(respond("worse"), 130, 6),
		withUsage(respond(newPage), 80, 30),
	)
	p := testPlan(
		updatePage(alphaPath, "src/a.go"),
		updatePage(betaPath, "src/a.go"),
		updatePage(gammaPath, "src/a.go"),
		plan.Page{Path: "product/new.md", State: plan.StateCreate, Type: "concept", SourcePaths: []string{"src/a.go"}},
	)

	// The dispatcher opens the span; Run only annotates it.
	ctx, span := tp.Tracer("test").Start(t.Context(), "write")
	rep, err := Run(ctx, p, e.deps())
	span.End()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tokens != (Tokens{Prompt: 520, Completion: 84}) {
		t.Fatalf("run tokens = %+v", rep.Tokens)
	}

	ended := spans.Ended()
	if len(ended) != 1 || ended[0].Name() != "write" {
		t.Fatalf("want exactly the one write span, got %d", len(ended))
	}
	got := map[attribute.Key]attribute.Value{}
	for _, kv := range ended[0].Attributes() {
		got[kv.Key] = kv.Value
	}
	want := map[attribute.Key]any{
		"okf.source.repo":               testSource,
		"okf.bundle":                    testBundle,
		"okf.write.model":               testModelName,
		"okf.write.from":                testCursor,
		"okf.write.to":                  testHead,
		"okf.write.pages.changed":       int64(1),
		"okf.write.pages.created":       int64(1),
		"okf.write.pages.restamped":     int64(0),
		"okf.write.pages.unchanged":     int64(1),
		"okf.write.pages.dropped":       int64(1),
		"okf.write.validation_failures": int64(2),
		"okf.write.tokens.prompt":       int64(520),
		"okf.write.tokens.completion":   int64(84),
	}
	for k, v := range want {
		if got[k].AsInterface() != v {
			t.Errorf("span attribute %s = %v, want %v", k, got[k].AsInterface(), v)
		}
	}

	c := counters(t, reader)
	if len(c) != 3 {
		t.Fatalf("counters = %v, want exactly okf.write.tokens, okf.write.pages and okf.write.validation_failures", c)
	}
	repo := map[string]string{"okf.source.repo": testSource, "okf.bundle": testBundle}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range repo {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	for kind, wantN := range map[string]int64{"prompt": 520, "completion": 84} {
		if v := valueOf(t, c, "okf.write.tokens", with(map[string]string{"okf.token.kind": kind})); v != wantN {
			t.Errorf("okf.write.tokens{%s} = %d, want %d", kind, v, wantN)
		}
	}
	for result, wantN := range map[string]int64{"changed": 1, "created": 1, "restamped": 0, "unchanged": 1, "dropped": 1} {
		if v := valueOf(t, c, "okf.write.pages", with(map[string]string{"okf.write.result": result})); v != wantN {
			t.Errorf("okf.write.pages{%s} = %d, want %d", result, v, wantN)
		}
	}
	if v := valueOf(t, c, "okf.write.validation_failures", repo); v != 2 {
		t.Errorf("okf.write.validation_failures = %d, want 2", v)
	}
	for name, pts := range c {
		for _, pt := range pts {
			if pt.attrs["okf.source.repo"] != testSource || pt.attrs["okf.bundle"] != testBundle {
				t.Errorf("%s point lacks the repo or bundle label: %v", name, pt.attrs)
			}
		}
	}
}
