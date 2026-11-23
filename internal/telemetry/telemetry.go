// Package telemetry sets up structured logging and OpenTelemetry for okf-operator.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var levels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// NewLogger returns a logger that writes to w. format is "text" or "json";
// level is "debug", "info", "warn" or "error".
func NewLogger(w io.Writer, format, level string) (*slog.Logger, error) {
	lvl, ok := levels[level]
	if !ok {
		return nil, fmt.Errorf("invalid --log-level %q: allowed values are debug, info, warn, error", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("invalid --log-format %q: allowed values are text, json", format)
}

// Setup installs the global tracer and meter providers and returns a function
// that flushes and shuts them down.
//
// autoexport falls back to OTLP when no exporter is named, so a signal with no
// OTEL_<SIGNAL>_EXPORTER and no OTLP endpoint is forced to "none": nothing is
// exported and no collector is contacted. An exporter named explicitly is left
// as is, including "otlp" with no endpoint, which uses the SDK default endpoint.
func Setup(ctx context.Context) (func(context.Context) error, error) {
	for _, signal := range []string{"TRACES", "METRICS"} {
		if os.Getenv("OTEL_"+signal+"_EXPORTER") == "" && !hasEndpoint(signal) {
			if err := os.Setenv("OTEL_"+signal+"_EXPORTER", "none"); err != nil {
				return nil, err
			}
		}
	}
	spanExporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}
	metricReader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("metric reader: %w", err)
	}
	res, err := newResource(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(spanExporter), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader), sdkmetric.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

// hasEndpoint reports whether a generic or signal-specific OTLP endpoint is set.
func hasEndpoint(signal string) bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}

// newResource names the service okf-operator unless OTEL_SERVICE_NAME overrides it.
func newResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "okf-operator")),
		resource.WithFromEnv(),
	)
}
