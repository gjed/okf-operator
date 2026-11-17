package plan

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const meterName = "github.com/gjed/okf-operator/internal/plan"

// record annotates the dispatcher's `plan` span and adds the counts to the
// okf.plan.* counters. It opens no span of its own.
func record(ctx context.Context, p *Plan) {
	labels := []attribute.KeyValue{
		attribute.String("okf.source.repo", p.Source),
		attribute.String("okf.bundle", p.Bundle),
	}
	counts := []struct {
		name, desc, unit string
		n                int
	}{
		{"okf.plan.changed_paths", "Changed source paths in planned ranges", "{path}", len(p.ChangedPaths)},
		{"okf.plan.routed_pages", "Knowledge pages planned", "{page}", len(p.Pages)},
		{"okf.plan.unrouted_paths", "Changed source paths that route to no page", "{path}", len(p.Unrouted)},
		{"okf.plan.excluded_paths", "Changed source paths dropped by exclude globs", "{path}", len(p.Excluded)},
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(labels...)
	span.SetAttributes(
		attribute.String("okf.plan.from", p.From),
		attribute.String("okf.plan.to", p.To),
		attribute.Bool("okf.plan.initial", p.Initial),
	)

	meter := otel.Meter(meterName)
	set := metric.WithAttributes(labels...)
	for _, c := range counts {
		span.SetAttributes(attribute.Int(c.name, c.n))
		counter, err := meter.Int64Counter(c.name, metric.WithDescription(c.desc), metric.WithUnit(c.unit))
		if err != nil {
			otel.Handle(err)
			continue
		}
		counter.Add(ctx, int64(c.n), set)
	}
}
