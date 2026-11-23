package gate

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/gjed/okf-operator/internal/github/githubtest"
	"github.com/gjed/okf-operator/internal/plan"
)

func (f *fixture) evaluate(ctx context.Context, t *testing.T) (*Report, error) {
	t.Helper()
	f.fake.SetPull(kbRepo, prNumber, githubtest.Pull{Head: headSHA, BaseRef: "main"})
	f.fake.SetPullFiles(kbRepo, prNumber, f.files)
	return Evaluate(ctx, Deps{Reader: f.reader, Commits: f.commits}, kbRepo, prNumber, f.plan)
}

func ruleNames(r *Report) []string {
	var names []string
	for _, res := range r.Rules {
		names = append(names, res.Name)
	}
	return names
}

func TestEvaluateReportsEveryRule(t *testing.T) {
	f := newFixture(t)
	f.files = []githubtest.PullFile{modified("product/api.md", 90, 70)}
	r, err := f.evaluate(context.Background(), t)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{RuleProvenance, RuleRoutedSet, RuleAllowlist, RuleChangeShape, RuleSize, RuleLinks}
	if got := ruleNames(r); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	if r.Pass || r.PR != prNumber || r.HeadSHA != headSHA || r.Version != Version {
		t.Errorf("report = %+v", r)
	}
	for _, res := range r.Rules {
		failing := res.Name == RuleSize
		if res.Pass == failing {
			t.Errorf("%s pass = %v, details %q", res.Name, res.Pass, res.Details)
		}
	}
}

func TestEvaluatePasses(t *testing.T) {
	f := newFixture(t)
	r, err := f.evaluate(context.Background(), t)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Pass {
		var out bytes.Buffer
		_ = Encode(&out, r)
		t.Fatalf("a conforming PR failed:\n%s", out.String())
	}
}

func TestEvaluateDeterministic(t *testing.T) {
	f := newFixture(t)
	f.files = append(f.files, githubtest.PullFile{Filename: "product/new.md", Status: "added", Additions: 2})
	f.fake.SetFile(kbRepo, headSHA, "product/new.md", "[x](gone.md) [y](../../out.md)\n")
	var out [2]bytes.Buffer
	for i := range out {
		r, err := f.evaluate(context.Background(), t)
		if err != nil {
			t.Fatal(err)
		}
		if err := Encode(&out[i], r); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(out[0].Bytes(), out[1].Bytes()) {
		t.Fatalf("reports differ:\n%s\n%s", out[0].String(), out[1].String())
	}
}

func TestEvaluateUsesBasePolicy(t *testing.T) {
	f := newFixture(t)
	f.fake.SetFile(kbRepo, baseSHA, ".kb/policy.yaml", strings.Replace(basePolicy, "page_lines: 100", "page_lines: 1", 1))
	f.fake.SetFile(kbRepo, headSHA, ".kb/policy.yaml", strings.Replace(basePolicy, "page_lines: 100", "page_lines: 1000", 1))
	f.files = append(f.files, modified(".kb/policy.yaml", 1, 1))
	f.plan.Pages = append(f.plan.Pages, plan.Page{Path: ".kb/policy.yaml", State: plan.StateUpdate})
	r, err := f.evaluate(context.Background(), t)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pass {
		t.Fatal("a PR rewriting its own policy passed")
	}
	byName := map[string]Result{}
	for _, res := range r.Rules {
		byName[res.Name] = res
	}
	if byName[RuleChangeShape].Pass || byName[RuleSize].Pass {
		t.Errorf("change_shape %+v, size %+v", byName[RuleChangeShape], byName[RuleSize])
	}
	if !strings.Contains(strings.Join(byName[RuleSize].Details, "\n"), "exceed limits.page_lines 1") {
		t.Errorf("size used the head policy: %q", byName[RuleSize].Details)
	}
}

func TestEvaluateErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fixture)
		want  string
	}{
		{"missing head page", func(f *fixture) { f.files = []githubtest.PullFile{modified("product/ghost.md", 1, 1)} },
			"product/ghost.md is listed as changed but is not a file at head"},
		{"bundle mismatch", func(f *fixture) { f.plan.Bundle = "infra" }, "differs from bundle"},
		{"API outage", func(f *fixture) {
			f.fake.FailStatus["/repos/acme/kb/pulls/7/files"] = http.StatusBadGateway
		}, "list files of acme/kb#7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.setup(f)
			if r, err := f.evaluate(context.Background(), t); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("report %v, err = %v, want %q", r, err, tt.want)
			}
		})
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

	// The dispatcher opens the span; Evaluate only annotates it.
	ctx, span := tp.Tracer("test").Start(context.Background(), "gate")
	f := newFixture(t)
	r, err := f.evaluate(ctx, t)
	span.End()
	if err != nil {
		t.Fatal(err)
	}

	ended := spans.Ended()
	if len(ended) != 1 || ended[0].Name() != "gate" {
		t.Fatalf("want exactly the one gate span, got %d", len(ended))
	}
	got := map[attribute.Key]any{}
	for _, kv := range ended[0].Attributes() {
		got[kv.Key] = kv.Value.AsInterface()
	}
	want := map[attribute.Key]any{
		"okf.knowledge.repo": kbRepo,
		"okf.gate.pr":        int64(prNumber),
		"okf.gate.head_sha":  headSHA,
		"okf.source.repo":    srcRepo,
		"okf.bundle":         "product",
		"okf.gate.pass":      r.Pass,
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
	points := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "okf.gate.rule_results" {
				t.Fatalf("unexpected metric %s", m.Name)
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				rule, _ := dp.Attributes.Value("okf.gate.rule")
				outcome, _ := dp.Attributes.Value("okf.gate.outcome")
				points[rule.AsString()+"="+outcome.AsString()] = dp.Value
			}
		}
	}
	if len(points) != len(r.Rules) {
		t.Fatalf("data points = %v", points)
	}
	for _, res := range r.Rules {
		outcome := "fail"
		if res.Pass {
			outcome = "pass"
		}
		if points[res.Name+"="+outcome] != 1 {
			t.Errorf("no single %s=%s data point: %v", res.Name, outcome, points)
		}
	}
}

// A frontmatter-only change, as a re-stamp makes it, is a modified page like
// any other: every deterministic rule passes when its source_sha is the plan's to.
func TestEvaluateProvenanceOnlyPagePasses(t *testing.T) {
	f := newFixture(t)
	older := strings.Replace(apiPage, "source_sha: "+sourceSHA, "source_sha: "+strings.Repeat("4", 40), 1)
	older = strings.Replace(older, "updated_at: 2026-11-18T10:00:00Z", "updated_at: 2026-11-01T10:00:00Z", 1)
	f.fake.SetFile(kbRepo, baseSHA, "product/api.md", older)
	f.files = []githubtest.PullFile{modified("product/api.md", 2, 2)}
	r, err := f.evaluate(context.Background(), t)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Pass {
		var out bytes.Buffer
		_ = Encode(&out, r)
		t.Fatalf("a provenance-only page failed:\n%s", out.String())
	}
}

// The same page stamped at a commit other than the plan's to fails the provenance rule.
func TestEvaluateProvenanceOnlyPageAtOtherCommitFails(t *testing.T) {
	f := newFixture(t)
	other := strings.Repeat("4", 40)
	f.fake.SetCommit(srcRepo, other)
	f.fake.SetFile(kbRepo, headSHA, "product/api.md", strings.Replace(apiPage, "source_sha: "+sourceSHA, "source_sha: "+other, 1))
	f.files = []githubtest.PullFile{modified("product/api.md", 2, 2)}
	r, err := f.evaluate(context.Background(), t)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pass {
		t.Fatal("a page stamped at a commit other than the plan's to passed")
	}
}
