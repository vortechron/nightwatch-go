package goravel

import (
	"context"
	"sync"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/goravel/framework/contracts/config"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// Names of the exporters ConfigureTelemetry adds to config/telemetry.go.
const (
	TraceExporterName = "nightwatch_traces"
	LogExporterName   = "nightwatch_logs"
)

var (
	clientMu sync.Mutex
	shared   *nightwatch.Client
)

// Client returns the shared client, built from the NIGHTWATCH_* environment
// the first time it is needed.
func Client() *nightwatch.Client {
	clientMu.Lock()
	defer clientMu.Unlock()

	if shared == nil {
		shared = nightwatch.NewFromEnv()
	}

	return shared
}

// SetClient replaces the shared client. Call it before the app boots, for
// example in tests or to use a hand-built Config.
func SetClient(client *nightwatch.Client) {
	clientMu.Lock()
	defer clientMu.Unlock()

	shared = client
}

// TraceExporter is the factory for the "via" key of a custom trace exporter.
func TraceExporter() func(context.Context) (sdktrace.SpanExporter, error) {
	return func(context.Context) (sdktrace.SpanExporter, error) {
		return Client().SpanExporter(), nil
	}
}

// LogExporter is the factory for the "via" key of a custom log exporter.
func LogExporter() func(context.Context) (sdklog.Exporter, error) {
	return func(context.Context) (sdklog.Exporter, error) {
		return Client().LogExporter(), nil
	}
}

// ConfigureTelemetry points Goravel telemetry at Nightwatch: it adds two
// custom exporters (Goravel needs one per signal) and selects them for traces
// and logs. The provider calls it for you. It replaces any exporter already
// chosen for those two signals.
func ConfigureTelemetry(cfg config.Config) {
	cfg.Add("telemetry.exporters."+TraceExporterName, map[string]any{"driver": "custom", "via": TraceExporter()})
	cfg.Add("telemetry.exporters."+LogExporterName, map[string]any{"driver": "custom", "via": LogExporter()})
	cfg.Add("telemetry.traces.exporter", TraceExporterName)
	cfg.Add("telemetry.logs.exporter", LogExporterName)
}
