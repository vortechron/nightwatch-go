package nightwatch

import (
	"context"

	"go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// SpanExporter returns an OpenTelemetry span exporter that feeds the
// translator. Hand it to a TracerProvider (Goravel: a "custom" exporter).
func (t *Translator) SpanExporter() sdktrace.SpanExporter {
	return spanExporter{t: t}
}

// LogExporter returns an OpenTelemetry log exporter that feeds the translator.
func (t *Translator) LogExporter() log.Exporter {
	return logExporter{t: t}
}

type spanExporter struct{ t *Translator }

func (e spanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.t.AddSpans(spans)

	return nil
}

func (e spanExporter) Shutdown(context.Context) error {
	e.t.Drain()

	return nil
}

type logExporter struct{ t *Translator }

func (e logExporter) Export(_ context.Context, records []log.Record) error {
	e.t.AddLogs(records)

	return nil
}

func (e logExporter) Shutdown(context.Context) error { return nil }

func (e logExporter) ForceFlush(context.Context) error { return nil }

func newTraceID() trace.TraceID {
	var id trace.TraceID
	for i := range id {
		id[i] = byte(rand64())
	}

	return id
}
