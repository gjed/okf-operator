package write

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const meterName = "github.com/gjed/okf-operator/internal/write"

// record annotates the dispatcher's `write` span and adds the run's counts to
// the okf.write.* counters. It opens no span of its own. Page text, prompts
// and PR bodies never become attributes.
func record(ctx context.Context, rep *Report, failures int) {
	labels := []attribute.KeyValue{
		attribute.String("okf.source.repo", rep.Source),
		attribute.String("okf.bundle", rep.Bundle),
	}
	results := map[string]int{ResultChanged: 0, ResultUnchanged: 0, ResultDropped: 0}
	for _, p := range rep.Pages {
		results[p.Result]++
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(labels...)
	span.SetAttributes(
		attribute.String("okf.write.model", rep.Model),
		attribute.String("okf.write.from", rep.From),
		attribute.String("okf.write.to", rep.To),
		attribute.Int("okf.write.pages.changed", results[ResultChanged]),
		attribute.Int("okf.write.pages.unchanged", results[ResultUnchanged]),
		attribute.Int("okf.write.pages.dropped", results[ResultDropped]),
		attribute.Int("okf.write.validation_failures", failures),
		attribute.Int("okf.write.tokens.prompt", rep.Tokens.Prompt),
		attribute.Int("okf.write.tokens.completion", rep.Tokens.Completion),
	)

	meter := otel.Meter(meterName)
	add := func(name, desc, unit string, n int, extra ...attribute.KeyValue) {
		c, err := meter.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
		if err != nil {
			otel.Handle(err)
			return
		}
		c.Add(ctx, int64(n), metric.WithAttributes(append(extra, labels...)...))
	}
	add("okf.write.tokens", "Model tokens used by write", "{token}", rep.Tokens.Prompt, attribute.String("okf.token.kind", "prompt"))
	add("okf.write.tokens", "Model tokens used by write", "{token}", rep.Tokens.Completion, attribute.String("okf.token.kind", "completion"))
	for _, r := range []string{ResultChanged, ResultUnchanged, ResultDropped} {
		add("okf.write.pages", "Planned pages by write result", "{page}", results[r], attribute.String("okf.write.result", r))
	}
	add("okf.write.validation_failures", "Model pages rejected by validation", "{failure}", failures)
}
