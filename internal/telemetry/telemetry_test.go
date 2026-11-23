package telemetry

import (
	"bytes"
	"context"
	"os"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

func serviceName(t *testing.T, res *resource.Resource) string {
	t.Helper()
	v, _ := res.Set().Value(attribute.Key("service.name"))
	return v.AsString()
}

func TestNewLoggerRejectsInvalidValues(t *testing.T) {
	if _, err := NewLogger(&bytes.Buffer{}, "text", "verbose"); err == nil {
		t.Fatal("level verbose: want error")
	}
	if _, err := NewLogger(&bytes.Buffer{}, "xml", "info"); err == nil {
		t.Fatal("format xml: want error")
	}
}

func TestNewResourceNamesServiceUnlessOverridden(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	res, err := newResource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := serviceName(t, res); got != "okf-operator" {
		t.Fatalf("service.name = %q, want okf-operator", got)
	}

	t.Setenv("OTEL_SERVICE_NAME", "custom")
	res, err = newResource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := serviceName(t, res); got != "custom" {
		t.Fatalf("service.name = %q, want custom", got)
	}
}

func TestSetupExporterEnv(t *testing.T) {
	const endpoint = "http://127.0.0.1:1"
	// The Prometheus exporter exists for metrics only, so traces never name it.
	tests := []struct {
		name                    string
		traces, metrics         string
		endpoint                string
		wantTraces, wantMetrics string
	}{
		{"unset, no endpoint", "", "", "", "none", "none"},
		{"unset, with endpoint", "", "", endpoint, "", ""},
		{"prometheus, no endpoint", "", "prometheus", "", "none", "prometheus"},
		{"prometheus, with endpoint", "", "prometheus", endpoint, "", "prometheus"},
		{"console, no endpoint", "console", "console", "", "console", "console"},
		{"console, with endpoint", "console", "console", endpoint, "console", "console"},
		{"otlp, no endpoint", "otlp", "otlp", "", "otlp", "otlp"},
		{"otlp, with endpoint", "otlp", "otlp", endpoint, "otlp", "otlp"},
		{"none, no endpoint", "none", "none", "", "none", "none"},
		{"none, with endpoint", "none", "none", endpoint, "none", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
			t.Setenv("OTEL_TRACES_EXPORTER", tt.traces)
			t.Setenv("OTEL_METRICS_EXPORTER", tt.metrics)
			shutdown, err := Setup(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = shutdown(context.Background()) }()
			if got := os.Getenv("OTEL_TRACES_EXPORTER"); got != tt.wantTraces {
				t.Fatalf("OTEL_TRACES_EXPORTER = %q, want %q", got, tt.wantTraces)
			}
			if got := os.Getenv("OTEL_METRICS_EXPORTER"); got != tt.wantMetrics {
				t.Fatalf("OTEL_METRICS_EXPORTER = %q, want %q", got, tt.wantMetrics)
			}
		})
	}
}
