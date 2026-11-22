package reconcile

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/okf-operator/internal/plan"
)

const meterName = "github.com/gjed/okf-operator/internal/reconcile"

// reasons lists the page reasons in the order they are recorded.
var reasons = []string{plan.ReasonBackfill, plan.ReasonStale, plan.ReasonRename, plan.ReasonWidened}

// record annotates the dispatcher's `reconcile` span and adds the counts to
// the okf.reconcile.* counters. It opens no span of its own.
func record(ctx context.Context, p *plan.Plan) {
	labels := []attribute.KeyValue{
		attribute.String("okf.source.repo", p.Source),
		attribute.String("okf.bundle", p.Bundle),
	}
	byReason := map[string]int{}
	for _, rp := range p.Reconcile.Pages {
		byReason[rp.Reason]++
	}
	reanchors := 0
	if p.Reconcile.Cursor == plan.CursorDiverged {
		reanchors = 1
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(labels...)
	span.SetAttributes(
		attribute.String("okf.reconcile.from", p.From),
		attribute.String("okf.reconcile.to", p.To),
		attribute.String("okf.reconcile.cursor", p.Reconcile.Cursor),
		attribute.Int("okf.reconcile.deletion_candidates", len(p.Reconcile.DeletionCandidates)),
		attribute.Bool("okf.reconcile.re_anchor", reanchors == 1),
	)
	for _, r := range reasons {
		span.SetAttributes(attribute.Int("okf.reconcile.pages."+r, byReason[r]))
	}

	meter := otel.Meter(meterName)
	set := metric.WithAttributes(labels...)
	add := func(name, desc, unit string, n int, opts ...metric.AddOption) {
		counter, err := meter.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if err != nil {
			otel.Handle(err)
			return
		}
		counter.Add(ctx, int64(n), append([]metric.AddOption{set}, opts...)...)
	}
	for _, r := range reasons {
		add("okf.reconcile.pages", "Knowledge pages planned by reconcile", "{page}", byReason[r],
			metric.WithAttributes(attribute.String("okf.reconcile.reason", r)))
	}
	add("okf.reconcile.deletion_candidates", "Pages reported because no routed source path is left", "{page}", len(p.Reconcile.DeletionCandidates))
	add("okf.reconcile.re_anchors", "Diverged cursors planned to be re-anchored", "{cursor}", reanchors)
}
