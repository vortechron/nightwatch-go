package goravel_test

import (
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	nightwatch "github.com/vortechron/nightwatch-go"
	nwgoravel "github.com/vortechron/nightwatch-go/goravel"
)

func TestRecordQueryLocations_GoravelDatabaseSpansGetTheCallerLine(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	if !nwgoravel.RecordQueryLocations(tp) || nwgoravel.RecordQueryLocations(noop.NewTracerProvider()) {
		t.Fatal("an SDK provider takes the processor, a no-op one cannot")
	}

	_, span := tp.Tracer(nwgoravel.GoravelDatabaseScope).Start(context.Background(), "db", trace.WithSpanKind(trace.SpanKindClient))
	span.End()

	for _, kv := range recorder.Ended()[0].Attributes() {
		if string(kv.Key) == nightwatch.AttrQueryFile && strings.HasSuffix(kv.Value.AsString(), "querycaller_test.go") {
			return
		}
	}

	t.Fatalf("attributes = %v", recorder.Ended()[0].Attributes())
}
