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
	results := map[string]int{}
	for _, p := range rep.Pages {
		results[p.Result]++
	}
	recordSpend(ctx, rep, failures)
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.Int("okf.write.pages.changed", results[ResultChanged]),
		attribute.Int("okf.write.pages.created", results[ResultCreated]),
		attribute.Int("okf.write.pages.restamped", results[ResultRestamped]),
		attribute.Int("okf.write.pages.unchanged", results[ResultUnchanged]),
		attribute.Int("okf.write.pages.dropped", results[ResultDropped]),
	)
	for _, r := range []string{ResultChanged, ResultCreated, ResultRestamped, ResultUnchanged, ResultDropped} {
		add(ctx, rep, "okf.write.pages", "Planned pages by write result", "{page}", results[r], attribute.String("okf.write.result", r))
	}
}

// recordAborted records what a failed run already spent: the tokens and
// validation failures of the pages processed so far. Nothing was written, so
// no page results are counted.
func recordAborted(ctx context.Context, rep *Report, jobs []*job, failures int) {
	for _, j := range jobs {
		rep.Tokens.add(j.result.Tokens)
	}
	recordSpend(ctx, rep, failures)
}

// recordSpend sets the run attributes and adds tokens and validation failures.
func recordSpend(ctx context.Context, rep *Report, failures int) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(labels(rep)...)
	span.SetAttributes(
		attribute.String("okf.write.model", rep.Model),
		attribute.String("okf.write.from", rep.From),
		attribute.String("okf.write.to", rep.To),
		attribute.Int("okf.write.validation_failures", failures),
		attribute.Int("okf.write.tokens.prompt", rep.Tokens.Prompt),
		attribute.Int("okf.write.tokens.completion", rep.Tokens.Completion),
	)
	add(ctx, rep, "okf.write.tokens", "Model tokens used by write", "{token}", rep.Tokens.Prompt, attribute.String("okf.token.kind", "prompt"))
	add(ctx, rep, "okf.write.tokens", "Model tokens used by write", "{token}", rep.Tokens.Completion, attribute.String("okf.token.kind", "completion"))
	add(ctx, rep, "okf.write.validation_failures", "Model pages rejected by validation", "{failure}", failures)
}

func labels(rep *Report) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("okf.source.repo", rep.Source),
		attribute.String("okf.bundle", rep.Bundle),
	}
}

func add(ctx context.Context, rep *Report, name, desc, unit string, n int, extra ...attribute.KeyValue) {
	c, err := otel.Meter(meterName).Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		otel.Handle(err)
		return
	}
	c.Add(ctx, int64(n), metric.WithAttributes(append(extra, labels(rep)...)...))
}
