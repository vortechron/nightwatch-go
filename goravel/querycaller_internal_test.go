package goravel

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	nightwatch "github.com/vortechron/nightwatch-go"
)

// These tests live in package goravel on purpose: their own frames belong to
// this client, so they are never the location, like the adapter's frames in
// a real app. What is left is the Go toolchain and the dependencies.

func queryLocation(t *testing.T, start func(trace.Tracer)) string {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	RecordQueryLocations(tp)

	start(tp.Tracer(GoravelDatabaseScope))

	for _, kv := range recorder.Ended()[0].Attributes() {
		if string(kv.Key) == nightwatch.AttrQueryFile {
			return kv.Value.AsString()
		}
	}

	return ""
}

func startDBSpan(tracer trace.Tracer) {
	_, span := tracer.Start(context.Background(), "db", trace.WithSpanKind(trace.SpanKindClient))
	span.End()
}

func TestQueryLocation_WithoutApplicationCodeADependencyFrameIsUsed(t *testing.T) {
	file := queryLocation(t, func(tracer trace.Tracer) {
		assert.Condition(t, func() bool { startDBSpan(tracer); return true })
	})

	if !strings.Contains(file, "github.com/stretchr/testify") {
		t.Fatalf("want the dependency that called the database (testify here), got %q", file)
	}
}

func TestQueryLocation_TheGoToolchainIsNeverTheLocation(t *testing.T) {
	// Only this client and the testing package (which a downloaded toolchain
	// keeps in the module cache) are on the stack.
	if file := queryLocation(t, startDBSpan); file != "" {
		t.Fatalf("location %q, want none", file)
	}
}
