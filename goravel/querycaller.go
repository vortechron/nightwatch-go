package goravel

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// GoravelDatabaseScope is the tracer name of Goravel's database
// instrumentation, whose spans become query records.
const GoravelDatabaseScope = "github.com/goravel/framework/telemetry/instrumentation/database"

// RecordQueryLocations adds nightwatch.QueryCallerProcessor to provider, so
// every query record gets the file and line of the application code that ran
// it. The provider calls it at boot with Goravel's tracer provider. It reports
// false when provider is not an OpenTelemetry SDK provider (telemetry off).
func RecordQueryLocations(provider trace.TracerProvider) bool {
	registrar, ok := provider.(interface{ RegisterSpanProcessor(sdktrace.SpanProcessor) })
	if !ok {
		return false
	}

	registrar.RegisterSpanProcessor(nightwatch.QueryCallerProcessor(GoravelDatabaseScope))

	return true
}
