package nightwatch

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/vortechron/nightwatch-go/internal/omnirules"
)

type captureSink struct {
	mu      sync.Mutex
	records []Record
}

func (c *captureSink) Enqueue(records []Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, records...)
}

func (c *captureSink) all() []Record {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]Record(nil), c.records...)
}

func (c *captureSink) ofType(t string) []Record {
	var out []Record
	for _, record := range c.all() {
		if record.Type() == t {
			out = append(out, record)
		}
	}

	return out
}

type harness struct {
	tr     *Translator
	sink   *captureSink
	tracer trace.Tracer
	logger otellog.Logger
	tp     *sdktrace.TracerProvider
}

func testConfig() Config {
	cfg := ConfigFromEnv(func(k string) string {
		return map[string]string{"NIGHTWATCH_TOKEN": "t", "NIGHTWATCH_DEPLOY": "dep", "NIGHTWATCH_SERVER": "srv"}[k]
	}, "host")
	cfg.TraceMaxWait = time.Hour

	return cfg
}

func newHarness(t *testing.T, mutate func(*Config)) *harness {
	t.Helper()
	cfg := testConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	sink := &captureSink{}
	tr := NewTranslator(cfg, sink)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tr.SpanExporter()))
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(tr.LogExporter())))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()); _ = lp.Shutdown(context.Background()) })

	return &harness{tr: tr, sink: sink, tracer: tp.Tracer("test"), logger: lp.Logger("test"), tp: tp}
}

func (h *harness) request(ctx context.Context, status int64, fn func(ctx context.Context, span trace.Span)) {
	ctx, span := h.tracer.Start(ctx, "GET /api/tasks/:id", trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
		attribute.String("http.request.method", "GET"),
		attribute.String("http.route", "/api/tasks/:id"),
		attribute.String("url.scheme", "http"),
		attribute.String("server.address", "127.0.0.1:2601"),
		attribute.String("url.path", "/api/tasks/7"),
		attribute.String("client.address", "127.0.0.1"),
	))
	if fn != nil {
		fn(ctx, span)
	}
	span.SetAttributes(attribute.Int64("http.response.status_code", status))
	span.End()
}

func (h *harness) query(ctx context.Context, sql string) {
	_, span := h.tracer.Start(ctx, "SELECT tasks", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("db.system.name", "sqlite"),
		attribute.String("db.query.text", sql),
		attribute.String("db.client.connection.pool.name", "sqlite"),
	))
	time.Sleep(time.Millisecond)
	span.End()
}

func (h *harness) log(ctx context.Context, level otellog.Severity, text string) {
	var record otellog.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(level)
	record.SetBody(otellog.StringValue(text))
	h.logger.Emit(ctx, record)
}

func mustValidate(t *testing.T, records []Record) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"records": records})
	if err != nil {
		t.Fatal(err)
	}
	if _, perr := omnirules.Validate(body, ""); perr != nil {
		t.Fatalf("Omni would reject: %v %+v", perr, perr.Context)
	}
}

func TestTranslator_RequestWithChildren(t *testing.T) {
	h := newHarness(t, nil)

	h.request(context.Background(), 500, func(ctx context.Context, span trace.Span) {
		h.query(ctx, "select * from tasks where id = ?")
		h.log(ctx, otellog.SeverityInfo, "task loaded")
		span.AddEvent("exception", trace.WithAttributes(
			attribute.String("exception.type", "*errors.errorString"),
			attribute.String("exception.message", "task 7 not found"),
		))
	})

	records := h.sink.all()
	mustValidate(t, records)

	requests := h.sink.ofType("request")
	if len(requests) != 1 {
		t.Fatalf("want 1 request, got %d (%v)", len(requests), records)
	}
	req := requests[0]
	traceID := req["trace_id"].(string)
	if req["route_path"] != "/api/tasks/:id" || req["method"] != "GET" || req["status_code"] != int64(500) {
		t.Fatalf("request fields: %v", req)
	}
	if req["url"] != "http://127.0.0.1:2601/api/tasks/7" {
		t.Fatalf("url %v", req["url"])
	}
	if req["queries"] != 1 || req["logs"] != 1 || req["exceptions"] != 1 {
		t.Fatalf("counters: %v %v %v", req["queries"], req["logs"], req["exceptions"])
	}
	if req["exception_preview"] != "task 7 not found" {
		t.Fatalf("preview %v", req["exception_preview"])
	}
	if req["deploy"] != "dep" || req["server"] != "srv" {
		t.Fatalf("deploy/server %v %v", req["deploy"], req["server"])
	}
	if req["_group"] != RequestGroup([]string{"GET"}, "", "/api/tasks/:id") {
		t.Fatalf("group %v", req["_group"])
	}

	for _, kind := range []string{"query", "log", "exception"} {
		children := h.sink.ofType(kind)
		if len(children) != 1 {
			t.Fatalf("%s: want 1, got %d", kind, len(children))
		}
		child := children[0]
		if child["trace_id"] != traceID || child["execution_id"] != traceID || child["execution_source"] != "request" {
			t.Fatalf("%s linking: %v", kind, child)
		}
		if child["execution_preview"] != "GET /api/tasks/7" {
			t.Fatalf("%s preview: %v", kind, child["execution_preview"])
		}
	}

	query := h.sink.ofType("query")[0]
	if query["sql"] != "select * from tasks where id = ?" || query["connection"] != "sqlite" {
		t.Fatalf("query: %v", query)
	}
	if query["_group"] != QueryGroup("sqlite", "sqlite", "select * from tasks where id = ?") {
		t.Fatalf("query group")
	}
}
