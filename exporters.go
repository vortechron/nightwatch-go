package nightwatch

import (
	"context"
	crand "crypto/rand"
	"errors"
	"sync/atomic"

	"go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ErrExporterShutdown is returned by an exporter that was shut down.
var ErrExporterShutdown = errors.New("nightwatch: exporter is shut down")

// SpanExporter returns an OpenTelemetry span exporter that feeds the
// translator. Hand it to a TracerProvider (Goravel: a "custom" exporter).
func (t *Translator) SpanExporter() sdktrace.SpanExporter {
	return &spanExporter{t: t}
}

// LogExporter returns an OpenTelemetry log exporter that feeds the translator.
func (t *Translator) LogExporter() log.Exporter {
	return &logExporter{t: t}
}

type spanExporter struct {
	t    *Translator
	down atomic.Bool
}

func (e *spanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := usable(ctx, &e.down); err != nil {
		return err
	}

	e.t.AddSpans(spans)

	return nil
}

func (e *spanExporter) Shutdown(ctx context.Context) error {
	e.down.Store(true)
	e.t.Drain()

	return ctx.Err() //nolint:wrapcheck // plain context error
}

type logExporter struct {
	t    *Translator
	down atomic.Bool
}

func (e *logExporter) Export(ctx context.Context, records []log.Record) error {
	if err := usable(ctx, &e.down); err != nil {
		return err
	}

	e.t.AddLogs(records)

	return nil
}

// Shutdown drains like the span exporter does.
func (e *logExporter) Shutdown(ctx context.Context) error {
	e.down.Store(true)
	e.t.Drain()

	return ctx.Err() //nolint:wrapcheck // plain context error
}

// ForceFlush sends the executions that wait out their grace period, so a
// flush request moves finished work along.
func (e *logExporter) ForceFlush(ctx context.Context) error {
	e.t.FlushPending()

	return ctx.Err() //nolint:wrapcheck // plain context error
}

func usable(ctx context.Context, down *atomic.Bool) error {
	if down.Load() {
		return ErrExporterShutdown
	}

	return ctx.Err() //nolint:wrapcheck // plain context error
}

func newTraceID() trace.TraceID {
	var id trace.TraceID
	_, _ = crand.Read(id[:])

	return id
}
