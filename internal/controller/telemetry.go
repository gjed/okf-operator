package controller

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "github.com/gjed/okf-operator/internal/controller"

// Metric names, documented in docs/controller.md.
const (
	metricEventsReceived = "okf.controller.events.received"
	metricEventsDropped  = "okf.controller.events.dropped"
	metricRunsQueued     = "okf.controller.runs.queued"
	metricRunDuration    = "okf.controller.run.duration"
	metricRunFailures    = "okf.controller.run.failures"
	metricMergeToPR      = "okf.controller.merge_to_pr"
	metricPRsApproved    = "okf.controller.prs.approved"
	metricStaleness      = "okf.controller.cursor.staleness"
)

// Attribute keys of the controller metrics and span.
const (
	attrEvent   = "okf.controller.event"
	attrReason  = "okf.controller.reason"
	attrTrigger = "okf.controller.trigger"
	attrMode    = "okf.controller.mode"
	attrOutcome = "okf.controller.outcome"
	attrStage   = "okf.controller.stage"
	attrSource  = "okf.source.repo"
	attrKB      = "okf.knowledge.repo"
)

func count(ctx context.Context, name, desc, unit string, attrs ...attribute.KeyValue) {
	c, err := otel.Meter(meterName).Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	if err != nil {
		otel.Handle(err)
		return
	}
	c.Add(ctx, 1, metric.WithAttributes(attrs...))
}

func seconds(ctx context.Context, name, desc string, d time.Duration, attrs ...attribute.KeyValue) {
	h, err := otel.Meter(meterName).Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit("s"))
	if err != nil {
		otel.Handle(err)
		return
	}
	h.Record(ctx, d.Seconds(), metric.WithAttributes(attrs...))
}

func eventReceived(ctx context.Context, event string) {
	count(ctx, metricEventsReceived, "GitHub webhook deliveries received", "{event}", attribute.String(attrEvent, event))
}

func eventDropped(ctx context.Context, reason string) {
	count(ctx, metricEventsDropped, "GitHub webhook deliveries that enqueued nothing", "{event}", attribute.String(attrReason, reason))
}

func runQueued(ctx context.Context, trigger string) {
	count(ctx, metricRunsQueued, "Source runs queued", "{run}", attribute.String(attrTrigger, trigger))
}

// Tracker is the in-memory state the webhook receiver and the reconciler
// share: the Apps' bot logins per knowledge base, the earliest source merge
// not yet proposed per Source, and when each Source's cursor last reached
// the source HEAD.
//
// SIMPLIFIED: kept in memory, so a restart forgets pending merge times and
// bot logins until the next run. Upgrade path: annotations on the Source.
type Tracker struct {
	mu       sync.Mutex
	bots     map[string][]string  // KnowledgeBase namespace/name -> logins
	merges   map[string]time.Time // Source namespace/name -> earliest merge
	caughtUp map[string]caughtUp  // Source namespace/name -> last time at HEAD
	gauge    metric.Registration
}

type caughtUp struct {
	repo string
	at   time.Time
}

// NewTracker returns a Tracker and registers the cursor staleness gauge.
func NewTracker() *Tracker {
	t := &Tracker{bots: map[string][]string{}, merges: map[string]time.Time{}, caughtUp: map[string]caughtUp{}}
	meter := otel.Meter(meterName)
	g, err := meter.Float64ObservableGauge(metricStaleness,
		metric.WithDescription("Time since the Source's cursor last reached the source HEAD"), metric.WithUnit("s"))
	if err != nil {
		otel.Handle(err)
		return t
	}
	t.gauge, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		t.mu.Lock()
		defer t.mu.Unlock()
		now := time.Now()
		for _, c := range t.caughtUp {
			o.ObserveFloat64(g, now.Sub(c.at).Seconds(), metric.WithAttributes(attribute.String(attrSource, c.repo)))
		}
		return nil
	}, g)
	if err != nil {
		otel.Handle(err)
	}
	return t
}

// SetBots records the bot logins of a knowledge base's Apps.
func (t *Tracker) SetBots(kb string, logins []string) {
	if len(logins) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bots[kb] = logins
}

// IsBot reports whether login is a bot user of knowledge base kb.
func (t *Tracker) IsBot(kb, login string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range t.bots[kb] {
		if b == login {
			return true
		}
	}
	return false
}

// Merged records a source merge for a Source, keeping the earliest.
func (t *Tracker) Merged(source string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.merges[source]; !ok || at.Before(old) {
		t.merges[source] = at
	}
}

// proposed clears the pending merge of a Source and returns it.
func (t *Tracker) proposed(source string) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.merges[source]
	delete(t.merges, source)
	return at, ok
}

// atHead records that a Source's cursor reached the source HEAD.
func (t *Tracker) atHead(source, repo string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.caughtUp[source] = caughtUp{repo: repo, at: at}
}
