package propose

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const meterName = "github.com/gjed/okf-operator/internal/propose"

// repaired adds a repair of an open bot PR, update-branch or conflict
// regeneration, to the okf.propose.prs.repaired counter. It is not part of
// the per-run counters record adds: those count 0 or 1 per run.
func repaired(ctx context.Context, source, kind string) {
	c, err := otel.Meter(meterName).Int64Counter("okf.propose.prs.repaired",
		metric.WithDescription("Bot PRs repaired by update-branch or by regenerating conflicting pages"), metric.WithUnit("{pr}"))
	if err != nil {
		otel.Handle(err)
		return
	}
	c.Add(ctx, 1, metric.WithAttributes(attribute.String("okf.source.repo", source), attribute.String("okf.propose.repair", kind)))
}

// record annotates the dispatcher's `propose` span and adds the run to the
// okf.propose.* counters. It opens no span of its own. Tokens, page content
// and PR bodies never become attributes.
func record(ctx context.Context, res *Result, committed int) {
	pr := 0
	if res.PR != nil {
		pr = res.PR.Number
	}
	labels := []attribute.KeyValue{
		attribute.String("okf.source.repo", res.Source),
		attribute.String("okf.bundle", res.Bundle),
	}
	trace.SpanFromContext(ctx).SetAttributes(append(labels,
		attribute.String("okf.propose.action", res.Action),
		attribute.Int("okf.propose.pr", pr),
		attribute.Bool("okf.propose.cursor_advanced", res.CursorAdvanced),
		attribute.Int("okf.propose.pages.committed", committed),
		attribute.String("okf.propose.from", res.From),
		attribute.String("okf.propose.to", res.To),
	)...)
	count := func(name, desc, unit string, yes bool) {
		n := int64(0)
		if yes {
			n = 1
		}
		c, err := otel.Meter(meterName).Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if err != nil {
			otel.Handle(err)
			return
		}
		c.Add(ctx, n, metric.WithAttributes(labels...))
	}
	count("okf.propose.prs.opened", "Knowledge PRs opened by propose", "{pr}", res.Action == ActionOpened)
	count("okf.propose.prs.updated", "Bot PRs that propose added a commit to", "{pr}", res.Action == ActionUpdated)
	count("okf.propose.cursor.advances", "Source cursors advanced by propose", "{advance}", res.CursorAdvanced)
}
