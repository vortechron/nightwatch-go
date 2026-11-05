package goravel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/goravel/framework/contracts/config"
	"github.com/goravel/framework/contracts/foundation"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// BindingClient is the service container key of the Nightwatch client. The
// provider binds it; there is no package level client.
const BindingClient = "nightwatch.client"

// Names of the exporters ConfigureTelemetry adds to config/telemetry.go.
const (
	TraceExporterName = "nightwatch_traces"
	LogExporterName   = "nightwatch_logs"
)

// maxBatchDelay is the longest wait Goravel's span and log batch processors
// may keep data before exporting it. The translator waits a grace period for
// late children; a slower processor would make most of them late.
const maxBatchDelay = 500 * time.Millisecond

// ErrExporterConflict is returned when the app already chose another telemetry
// exporter. Goravel allows one exporter per signal, so Nightwatch cannot share.
var ErrExporterConflict = errors.New("nightwatch: another telemetry exporter is already configured")

// ClientFrom returns the client bound in the container, or nil.
func ClientFrom(app foundation.Application) *nightwatch.Client {
	instance, err := app.Make(BindingClient)
	if err != nil {
		return nil
	}

	client, _ := instance.(*nightwatch.Client)

	return client
}

// TraceExporter is the factory for the "via" key of a custom trace exporter.
func TraceExporter(client *nightwatch.Client) func(context.Context) (sdktrace.SpanExporter, error) {
	return func(context.Context) (sdktrace.SpanExporter, error) {
		return client.SpanExporter(), nil
	}
}

// LogExporter is the factory for the "via" key of a custom log exporter.
func LogExporter(client *nightwatch.Client) func(context.Context) (sdklog.Exporter, error) {
	return func(context.Context) (sdklog.Exporter, error) {
		return client.LogExporter(), nil
	}
}

// ConfigureTelemetry points Goravel telemetry at Nightwatch: it adds two
// custom exporters (Goravel needs one per signal), selects them for traces and
// logs, and makes the batch processors export at least every 500 ms.
//
// It refuses, with ErrExporterConflict, when telemetry.traces.exporter or
// telemetry.logs.exporter already names another exporter. Clear those two
// settings (for example leave OTEL_TRACES_EXPORTER and OTEL_LOGS_EXPORTER
// empty) to use Nightwatch; running OTLP next to it is not supported.
func ConfigureTelemetry(cfg config.Config, client *nightwatch.Client) error {
	for _, signal := range []struct{ key, ours string }{
		{"telemetry.traces.exporter", TraceExporterName},
		{"telemetry.logs.exporter", LogExporterName},
	} {
		if current := cfg.GetString(signal.key); current != "" && current != signal.ours {
			return fmt.Errorf("%w: %s is %q, set it empty to use Nightwatch", ErrExporterConflict, signal.key, current)
		}
	}

	cfg.Add("telemetry.exporters."+TraceExporterName, map[string]any{"driver": "custom", "via": TraceExporter(client)})
	cfg.Add("telemetry.exporters."+LogExporterName, map[string]any{"driver": "custom", "via": LogExporter(client)})
	cfg.Add("telemetry.traces.exporter", TraceExporterName)
	cfg.Add("telemetry.logs.exporter", LogExporterName)

	for _, key := range []string{"telemetry.traces.processor.interval", "telemetry.logs.processor.interval"} {
		if delay := cfg.GetDuration(key); delay == 0 || delay > maxBatchDelay {
			cfg.Add(key, maxBatchDelay.String())
		}
	}

	return nil
}

// flusher returns a function that pushes everything out: OpenTelemetry's
// batch processors first, then the client. Short lived processes (commands)
// call it before they exit.
func flusher(client *nightwatch.Client) func(context.Context) {
	type forceFlusher interface{ ForceFlush(context.Context) error }

	return func(ctx context.Context) {
		if provider, ok := otel.GetTracerProvider().(forceFlusher); ok {
			_ = provider.ForceFlush(ctx)
		}

		if provider, ok := global.GetLoggerProvider().(forceFlusher); ok {
			_ = provider.ForceFlush(ctx)
		}

		client.Flush(ctx)
	}
}
