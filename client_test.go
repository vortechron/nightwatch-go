package nightwatch

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestClient_SpansReachOmniAndShutdownFlushes(t *testing.T) {
	f := newFakeOmni(t)
	cfg := senderConfig(f)
	cfg.FlushInterval = time.Hour
	cfg.TraceMaxWait = 50 * time.Millisecond
	client := New(cfg)
	client.Start()

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(client.SpanExporter()))
	tr := tp.Tracer("t")
	ctx, root := tr.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer))
	root.End()
	_ = ctx
	_ = tp.Shutdown(context.Background())

	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Shutdown(sctx); err != nil {
		t.Fatal(err)
	}
	// a server span without http.request.method is not an execution: nothing to send
	if len(f.calls()) != 0 {
		t.Fatalf("unexpected posts: %d", len(f.calls()))
	}
	if client.Stats().Dropped != 0 || client.Config().Server != "srv" {
		t.Fatalf("stats %+v", client.Stats())
	}
}

func TestClient_FullPath(t *testing.T) {
	f := newFakeOmni(t)
	cfg := senderConfig(f)
	cfg.TraceMaxWait = 20 * time.Millisecond
	client := New(cfg)
	client.Start()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(client.SpanExporter()))
	_, orphan := tp.Tracer("t").Start(context.Background(), "q", trace.WithSpanKind(trace.SpanKindClient))
	orphan.End()
	h := &harness{tp: tp, tracer: tp.Tracer("t"), sink: &captureSink{}}
	h.request(context.Background(), 200, nil)
	f.waitFor(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatal("second shutdown must be harmless")
	}
}
