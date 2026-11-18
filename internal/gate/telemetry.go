package gate

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/okf-operator/internal/plan"
)

const meterName = "github.com/gjed/okf-operator/internal/gate"

// Rule outcomes on the okf.gate.rule_results counter.
const (
	outcomePass = "pass"
	outcomeFail = "fail"
)

// record annotates the dispatcher's `gate` span and adds one
// okf.gate.rule_results count per rule. It opens no span of its own.
func record(ctx context.Context, repo string, p *plan.Plan, r *Report) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("okf.knowledge.repo", repo),
		attribute.Int("okf.gate.pr", r.PR),
		attribute.String("okf.gate.head_sha", r.HeadSHA),
		attribute.String("okf.source.repo", p.Source),
		attribute.String("okf.bundle", p.Bundle),
		attribute.Bool("okf.gate.pass", r.Pass),
	)

	counter, err := otel.Meter(meterName).Int64Counter("okf.gate.rule_results",
		metric.WithDescription("Gate rule results by rule and outcome"), metric.WithUnit("{result}"))
	if err != nil {
		otel.Handle(err)
		return
	}
	for _, res := range r.Rules {
		outcome := outcomeFail
		if res.Pass {
			outcome = outcomePass
		}
		counter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("okf.gate.rule", res.Name),
			attribute.String("okf.gate.outcome", outcome),
		))
	}
}
