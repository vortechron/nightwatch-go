package nightwatch_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	nightwatch "github.com/vortechron/nightwatch-go"
)

const dbScope = "example.com/db"

func attrsOf(span sdktrace.ReadOnlySpan) map[string]attribute.Value {
	out := map[string]attribute.Value{}
	for _, kv := range span.Attributes() {
		out[string(kv.Key)] = kv.Value
	}

	return out
}

func TestQueryCallerProcessor_RecordsTheApplicationLineThatRanTheQuery(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(nightwatch.QueryCallerProcessor(dbScope)), sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, _, line, _ := runtime.Caller(0)
	_, span := tp.Tracer(dbScope).Start(context.Background(), "db", trace.WithSpanKind(trace.SpanKindClient))
	span.End()

	_, file, _, _ := runtime.Caller(0)
	attrs := attrsOf(recorder.Ended()[0])
	if attrs[nightwatch.AttrQueryFile].AsString() != file || attrs[nightwatch.AttrQueryLine].AsInt64() != int64(line+1) {
		t.Fatalf("file %v line %v, want %s:%d", attrs[nightwatch.AttrQueryFile], attrs[nightwatch.AttrQueryLine], file, line+1)
	}
}

func TestQueryCallerProcessor_LeavesOtherScopesAndKindsAlone(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(nightwatch.QueryCallerProcessor(dbScope)), sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, other := tp.Tracer("example.com/http").Start(context.Background(), "GET", trace.WithSpanKind(trace.SpanKindClient))
	other.End()
	_, internal := tp.Tracer(dbScope).Start(context.Background(), "tx")
	internal.End()

	for _, span := range recorder.Ended() {
		if _, set := attrsOf(span)[nightwatch.AttrQueryFile]; set {
			t.Fatalf("%s got a query location", span.Name())
		}
	}
}

func TestQueryCallerProcessor_TheGoToolchainIsNeverTheLocation(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(nightwatch.QueryCallerProcessor(dbScope)), sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// Started from a goroutine of the standard library only: no application
	// frame and no dependency frame, so no location at all.
	done := make(chan struct{})
	time.AfterFunc(0, func() {
		_, span := tp.Tracer(dbScope).Start(context.Background(), "db", trace.WithSpanKind(trace.SpanKindClient))
		span.End()
		close(done)
	})
	<-done

	if file, set := attrsOf(recorder.Ended()[0])[nightwatch.AttrQueryFile]; set && strings.Contains(file.AsString(), "/src/") {
		t.Fatalf("location %v is in the Go installation", file)
	}
}
