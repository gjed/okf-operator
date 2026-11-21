package review

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/okf-operator/internal/plan"
)

const meterName = "github.com/gjed/okf-operator/internal/review"

// Page results on the okf.review.pages counter.
const (
	resultPass = "pass"
	resultFail = "fail"
)

// record annotates the dispatcher's `review` span and adds the run's counts
// to the okf.review.* counters. It opens no span of its own. Page text,
// sentences, prompts and replies never become attributes.
func record(ctx context.Context, repo string, p *plan.Plan, r *Result) {
	counts := map[string]int{resultPass: 0, resultFail: 0}
	for _, pg := range r.Pages {
		if pg.Pass {
			counts[resultPass]++
		} else {
			counts[resultFail]++
		}
	}
	recordSpend(ctx, repo, p, r)
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.Bool("okf.review.pass", r.Pass),
		attribute.Int("okf.review.pages.pass", counts[resultPass]),
		attribute.Int("okf.review.pages.fail", counts[resultFail]),
	)
	for _, res := range []string{resultPass, resultFail} {
		add(ctx, p, "okf.review.pages", "Changed pages by review result", "{page}", counts[res], attribute.String("okf.review.result", res))
	}
}

// recordSpend sets the run attributes and adds the tokens spent so far. A
// failed run calls it alone, so its tokens are still counted.
func recordSpend(ctx context.Context, repo string, p *plan.Plan, r *Result) {
	trace.SpanFromContext(ctx).SetAttributes(append(labels(p),
		attribute.String("okf.knowledge.repo", repo),
		attribute.Int("okf.review.pr", r.PR),
		attribute.String("okf.review.head_sha", r.HeadSHA),
		attribute.String("okf.review.model", r.Model),
		attribute.Int("okf.review.tokens.prompt", r.Tokens.Prompt),
		attribute.Int("okf.review.tokens.completion", r.Tokens.Completion),
	)...)
	add(ctx, p, "okf.review.tokens", "Model tokens used by review", "{token}", r.Tokens.Prompt, attribute.String("okf.token.kind", "prompt"))
	add(ctx, p, "okf.review.tokens", "Model tokens used by review", "{token}", r.Tokens.Completion, attribute.String("okf.token.kind", "completion"))
}

func labels(p *plan.Plan) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("okf.source.repo", p.Source),
		attribute.String("okf.bundle", p.Bundle),
	}
}

func add(ctx context.Context, p *plan.Plan, name, desc, unit string, n int, extra ...attribute.KeyValue) {
	c, err := otel.Meter(meterName).Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		otel.Handle(err)
		return
	}
	c.Add(ctx, int64(n), metric.WithAttributes(append(extra, labels(p)...)...))
}
