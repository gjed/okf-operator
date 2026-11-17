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

func TestSetupForcesExportersOffWithoutEndpoint(t *testing.T) {
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER",
	} {
		t.Setenv(key, "")
	}
	shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	for _, key := range []string{"OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER"} {
		if got := os.Getenv(key); got != "none" {
			t.Fatalf("%s = %q, want none", key, got)
		}
	}
}
