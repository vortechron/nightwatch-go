package nightwatch

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestGrace_LateLogAndSpanJoinTheExecutionCounters(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RootGrace = time.Second })
	now := time.Now()
	h.tr.now = func() time.Time { return now }

	ctx, root := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/x")))
	_, late := h.tracer.Start(ctx, "q", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("db.query.text", "select 1")))
	root.End()
	if len(h.sink.all()) != 0 {
		t.Fatal("a finished root must wait out its grace period")
	}
	late.End()
	h.log(ctx, otellog.SeverityInfo, "after the root")

	now = now.Add(1100 * time.Millisecond)
	h.tr.Sweep()

	req := h.sink.ofType("request")
	if len(req) != 1 || req[0]["queries"] != 1 || req[0]["logs"] != 1 {
		t.Fatalf("counters must include the late children: %v", req)
	}
	if h.tr.Stats().LateChildren != 0 {
		t.Fatalf("children inside the grace period are not late: %+v", h.tr.Stats())
	}
}

func TestGrace_ChildAfterGraceIsSentAloneAndCounted(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RootGrace = time.Second })
	now := time.Now()
	h.tr.now = func() time.Time { return now }

	ctx, root := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET")))
	_, late := h.tracer.Start(ctx, "q", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("db.query.text", "select 1")))
	root.End()
	now = now.Add(2 * time.Second)
	h.tr.Sweep()
	late.End()
	h.log(ctx, otellog.SeverityInfo, "much later")

	if len(h.sink.ofType("query")) != 1 || len(h.sink.ofType("log")) != 1 {
		t.Fatalf("late children are still sent: %v", h.sink.all())
	}
	if h.sink.ofType("request")[0]["queries"] != 0 {
		t.Fatal("the request was already sent without them")
	}
	if got := h.tr.Stats().LateChildren; got != 2 {
		t.Fatalf("late children counted: %d", got)
	}
	if len(h.tr.traces) != 0 {
		t.Fatalf("empty trace buffers must be removed at once, have %d", len(h.tr.traces))
	}
}

func TestSampling_LateLogsAndOrphansRespectTheDecision(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RequestSampleRate = 0; c.ExceptionSampleRate = 0 })
	ctx, root := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET")))
	root.End()
	h.log(ctx, otellog.SeverityError, "late log of a dropped trace")

	octx, orphanRoot := h.tracer.Start(context.Background(), "o", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET")))
	h.query(octx, "select 2")
	h.log(octx, otellog.SeverityError, "orphan log")
	h.tr.Drain()
	orphanRoot.End()

	if len(h.sink.all()) != 0 {
		t.Fatalf("unsampled data leaked: %v", h.sink.all())
	}
}

func TestSink_IsCalledOutsideTheLock(t *testing.T) {
	cfg := testConfig()
	var tr *Translator
	var reentered atomic.Bool
	tr = NewTranslator(cfg, sinkFunc(func([]Record) {
		tr.AddSpans(nil) // takes the lock; deadlocks if the lock is held
		reentered.Store(true)
	}))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tr.SpanExporter()))
	done := make(chan struct{})
	go func() {
		_, span := tp.Tracer("t").Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", "GET")))
		span.End()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock: records were sent while holding the lock")
	}
	if !reentered.Load() {
		t.Fatal("sink not called")
	}
}

type sinkFunc func([]Record)

func (f sinkFunc) Enqueue(r []Record) { f(r) }

func TestCaps_OldestTraceIsSentAsOrphanWhenTooManyAreHeld(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxTraces = 2 })
	var roots []trace.Span
	for i := 0; i < 3; i++ {
		ctx, root := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", "GET")))
		h.query(ctx, "select 1")
		roots = append(roots, root)
	}
	if got := h.tr.Stats().TracesEvicted; got != 1 {
		t.Fatalf("evicted %d", got)
	}
	if len(h.sink.ofType("query")) != 1 || len(h.tr.traces) != 2 {
		t.Fatalf("oldest trace must go out as an orphan: queries=%d held=%d", len(h.sink.ofType("query")), len(h.tr.traces))
	}
	for _, r := range roots {
		r.End()
	}
}

func TestCaps_SpansPerTraceAreBoundedButRootsAlwaysArrive(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxSpansPerTrace = 2 })
	ctx, root := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET")))
	for i := 0; i < 5; i++ {
		h.query(ctx, "select 1")
	}
	h.log(ctx, otellog.SeverityInfo, "a")
	h.log(ctx, otellog.SeverityInfo, "b")
	h.log(ctx, otellog.SeverityInfo, "c")
	root.End()

	if len(h.sink.ofType("request")) != 1 {
		t.Fatal("the root must never be dropped")
	}
	if got := len(h.sink.ofType("query")); got != 2 {
		t.Fatalf("kept %d queries", got)
	}
	if h.tr.Stats().SpansDropped != 3+1 { // 3 spans, 1 log
		t.Fatalf("dropped %d", h.tr.Stats().SpansDropped)
	}
}

func TestCaps_OwnersAreBounded(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.MaxOwners = 4 })
	for i := 0; i < 10; i++ {
		h.request(context.Background(), 200, nil)
	}
	if len(h.tr.owners) > 4 {
		t.Fatalf("owners %d", len(h.tr.owners))
	}
	if h.tr.Stats().OwnersEvicted == 0 {
		t.Fatal("evictions must be counted")
	}
}

func TestLogTime_FallsBackToObservedThenNow(t *testing.T) {
	h := newHarness(t, nil)
	observed := time.Now().Add(-time.Hour)
	var r1, r2 sdklog.Record
	r1.SetObservedTimestamp(observed)
	r1.SetBody(otellog.StringValue("observed"))
	r2.SetBody(otellog.StringValue("none"))
	h.tr.AddLogs([]sdklog.Record{r1, r2})
	logs := h.sink.ofType("log")
	if len(logs) != 2 {
		t.Fatal("two logs")
	}
	if got := logs[0]["timestamp"].(float64); got != unixSeconds(observed) {
		t.Fatalf("observed time not used: %v", got)
	}
	if got := logs[1]["timestamp"].(float64); got < 1e9 {
		t.Fatalf("zero time emitted: %v", got)
	}
}

func TestExceptions_EachErrorCountsAndDistinctErrorsGroupApart(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		for _, msg := range []string{"first failure", "second failure", "second failure"} {
			span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", "*errors.errorString"), attribute.String("exception.message", msg)))
		}
	})
	excs := h.sink.ofType("exception")
	if len(excs) != 3 {
		t.Fatalf("every reported error counts like in PHP: %d", len(excs))
	}
	if excs[0]["_group"] == excs[1]["_group"] {
		t.Fatal("distinct errors without a stack must not share a group")
	}
	if excs[1]["_group"] != excs[2]["_group"] {
		t.Fatal("the same error should group together")
	}
	if excs[0]["file"] != "GET /api/tasks/:id" || excs[0]["line"] != 0 {
		t.Fatalf("best available location: %v %v", excs[0]["file"], excs[0]["line"])
	}
	if h.sink.ofType("request")[0]["exceptions"] != 3 {
		t.Fatal("counter")
	}
}

func TestExceptions_ReportErrorCapturesARealStack(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)
	h.request(context.Background(), 500, func(ctx context.Context, _ trace.Span) {
		ReportError(ctx, errTest("boom"))
	})
	exc := h.sink.ofType("exception")[0]
	if !strings.HasSuffix(exc["file"].(string), ".go") || exc["line"].(int) == 0 {
		t.Fatalf("file and line must be real: %v:%v", exc["file"], exc["line"])
	}
	if !strings.Contains(exc["trace"].(string), `"file"`) {
		t.Fatal("trace frames")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestExceptions_SourceCodeLinesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/app.go"
	var content strings.Builder
	for i := 1; i <= 30; i++ {
		content.WriteString("line " + itoa(i) + "\n")
	}
	if err := os.WriteFile(file, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	stack := "main.handler(...)\n\t" + file + ":15\n"

	on := newHarness(t, func(c *Config) { c.CaptureExceptionSourceCode = true })
	on.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", "E"), attribute.String("exception.message", "m"), attribute.String("exception.stacktrace", stack)))
	})
	trace1 := on.sink.ofType("exception")[0]["trace"].(string)
	if !strings.Contains(trace1, `"code":{"10":"line 10","11":"line 11"`) || !strings.Contains(trace1, `"20":"line 20"}`) {
		t.Fatalf("source lines: %s", trace1)
	}

	off := newHarness(t, func(c *Config) { c.CaptureExceptionSourceCode = false })
	off.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", "E"), attribute.String("exception.message", "m"), attribute.String("exception.stacktrace", stack)))
	})
	if !strings.Contains(off.sink.ofType("exception")[0]["trace"].(string), `"code":null`) {
		t.Fatal("code must be null when off")
	}

	_ = os.MkdirAll(dir+"/vendor", 0o750)
	_ = os.MkdirAll(dir+"/pkg/mod/x", 0o750)
	_ = os.WriteFile(dir+"/vendor/x.go", []byte("a\nb\n"), 0o600)
	_ = os.WriteFile(dir+"/pkg/mod/x/y.go", []byte("a\nb\n"), 0o600)
	if readSource(dir+"/vendor/x.go", 10) != nil || readSource(dir+"/pkg/mod/x/y.go", 10) != nil || readSource(dir+"/missing.go", 3) != nil || readSource(file, 0) != nil {
		t.Fatal("library, missing and invalid locations give no code")
	}
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+i/10)) + string(rune('0'+i%10)))
}

func TestExporters_HonourContextAndShutdown(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RootGrace = time.Hour })
	spans := h.tr.SpanExporter()
	logs := h.tr.LogExporter()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := spans.ExportSpans(cancelled, nil); err == nil {
		t.Fatal("cancelled context must be an error")
	}
	if err := logs.Export(cancelled, nil); err == nil {
		t.Fatal("cancelled context must be an error")
	}

	h.request(context.Background(), 200, nil) // waits out the (long) grace period
	if len(h.sink.all()) != 0 {
		t.Fatal("held")
	}
	if err := logs.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.sink.ofType("request")) != 1 {
		t.Fatal("log exporter ForceFlush must send finished executions")
	}

	if err := logs.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := spans.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := spans.ExportSpans(context.Background(), nil); err == nil {
		t.Fatal("exporting after shutdown must fail")
	}
	if err := logs.Export(context.Background(), nil); err == nil {
		t.Fatal("exporting after shutdown must fail")
	}
}

func TestMemory_IsPerTranslator(t *testing.T) {
	a := NewTranslator(testConfig(), &captureSink{})
	b := NewTranslator(testConfig(), &captureSink{})
	if a.mem.read(time.Now()) <= 0 {
		t.Fatal("heap size")
	}
	if b.mem.value != 0 {
		t.Fatal("translators must not share the memory cache")
	}
}
