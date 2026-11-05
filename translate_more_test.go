package nightwatch

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

func (h *harness) execution(ctx context.Context, kind string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	attrs = append(attrs, attribute.String(AttrRecord, kind))

	return h.tracer.Start(ctx, kind, trace.WithAttributes(attrs...))
}

func (h *harness) child(ctx context.Context, kind string, attrs ...attribute.KeyValue) trace.Span {
	attrs = append(attrs, attribute.String(AttrRecord, kind))
	_, span := h.tracer.Start(ctx, kind, trace.WithAttributes(attrs...))

	return span
}

func TestTranslator_JobAttemptKeepsRequestTraceAndLinks(t *testing.T) {
	h := newHarness(t, nil)
	var carrier context.Context

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		queued := h.child(ctx, RecordQueuedJob,
			attribute.String(AttrJobID, "job-1"), attribute.String(AttrJobName, "jobs.SendEmail"),
			attribute.String(AttrJobConnection, "redis"), attribute.String(AttrJobQueue, "default"))
		carrier = trace.ContextWithSpan(ctx, queued)
		queued.End()
	})

	wctx, attempt := h.execution(carrier, RecordJobAttempt,
		attribute.String(AttrJobID, "job-1"), attribute.String(AttrJobName, "jobs.SendEmail"),
		attribute.String(AttrJobConnection, "redis"), attribute.String(AttrJobQueue, "default"),
		attribute.Int64(AttrJobAttempt, 2))
	h.query(wctx, "select 1")
	h.log(wctx, otellog.SeverityWarn, "from job")
	attempt.End()

	records := h.sink.all()
	mustValidate(t, records)

	req := h.sink.ofType("request")[0]
	queued := h.sink.ofType("queued-job")[0]
	job := h.sink.ofType("job-attempt")[0]
	if req["jobs_queued"] != 1 || queued["job_id"] != "job-1" || queued["execution_id"] != req["trace_id"] {
		t.Fatalf("queued linking: %v", queued)
	}
	if job["trace_id"] != req["trace_id"] || job["job_id"] != "job-1" || job["attempt"] != 2 || job["status"] != "processed" {
		t.Fatalf("job attempt: %v", job)
	}
	attemptID := job["attempt_id"].(string)
	for _, kind := range []string{"query", "log"} {
		var found Record
		for _, r := range h.sink.ofType(kind) {
			if r["execution_source"] == "job" {
				found = r
			}
		}
		if found == nil || found["execution_id"] != attemptID || found["execution_preview"] != "jobs.SendEmail" {
			t.Fatalf("%s in job not linked: %v", kind, found)
		}
	}
	if job["queries"] != 1 || job["logs"] != 1 {
		t.Fatalf("job counters %v %v", job["queries"], job["logs"])
	}
}

func TestTranslator_FailedJobStatus(t *testing.T) {
	h := newHarness(t, nil)
	_, span := h.execution(context.Background(), RecordJobAttempt, attribute.String(AttrJobName, "j"))
	span.SetStatus(codes.Error, "boom")
	span.End()
	if got := h.sink.ofType("job-attempt")[0]["status"]; got != "failed" {
		t.Fatalf("status %v", got)
	}
}

func TestTranslator_CommandAndScheduledTask(t *testing.T) {
	h := newHarness(t, nil)

	ctx, cmd := h.execution(context.Background(), RecordCommand,
		attribute.String(AttrCommandName, "emails:send"), attribute.String(AttrCommandLine, "artisan emails:send --all"))
	h.query(ctx, "select 2")
	cmd.SetStatus(codes.Error, "x")
	cmd.End()

	_, task := h.execution(context.Background(), RecordScheduledTask,
		attribute.String(AttrTaskName, "purge"), attribute.String(AttrTaskCron, "* * * * *"),
		attribute.String(AttrTaskTimezone, "UTC"), attribute.Bool(AttrTaskOneServer, true))
	task.End()

	mustValidate(t, h.sink.all())
	c := h.sink.ofType("command")[0]
	if c["name"] != "emails:send" || c["exit_code"] != int64(1) || c["queries"] != 1 || c["_group"] != NameGroup("emails:send") {
		t.Fatalf("command %v", c)
	}
	q := h.sink.ofType("query")[0]
	if q["execution_source"] != "command" || q["execution_id"] != c["trace_id"] {
		t.Fatalf("command child %v", q)
	}
	s := h.sink.ofType("scheduled-task")[0]
	if s["on_one_server"] != true || s["without_overlapping"] != false || s["status"] != "processed" || s["_group"] != ScheduledTaskGroup("purge", "* * * * *", "UTC", 0) {
		t.Fatalf("task %v", s)
	}
}

func TestTranslator_CacheMailNotificationOutgoing(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.child(ctx, RecordCacheEvent, attribute.String(AttrCacheStore, "redis"), attribute.String(AttrCacheKey, "k"), attribute.String(AttrCacheType, "hit")).End()
		h.child(ctx, RecordCacheEvent, attribute.String(AttrCacheType, "bogus")).End()
		m := h.child(ctx, RecordMail, attribute.String(AttrMailClass, "WelcomeMail"), attribute.Int64(AttrMailTo, 1))
		m.SetStatus(codes.Error, "smtp")
		m.End()
		h.child(ctx, RecordNotification, attribute.String(AttrNotificationChan, "mail"), attribute.String(AttrNotificationClass, "N")).End()
		_, out := h.tracer.Start(ctx, "GET", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
			attribute.String("http.request.method", "GET"), attribute.String("url.full", "https://api.example.com/x"),
			attribute.String("server.address", "api.example.com"), attribute.Int64("http.response.status_code", 201)))
		out.End()
	})
	records := h.sink.all()
	mustValidate(t, records)
	req := h.sink.ofType("request")[0]
	if req["cache_events"] != 1 || req["mail"] != 1 || req["notifications"] != 1 || req["outgoing_requests"] != 1 {
		t.Fatalf("counters %v", req)
	}
	if h.sink.ofType("mail")[0]["failed"] != true || h.sink.ofType("cache-event")[0]["type"] != "hit" {
		t.Fatal("mail/cache fields")
	}
	out := h.sink.ofType("outgoing-request")[0]
	if out["host"] != "api.example.com" || out["status_code"] != int64(201) || out["_group"] != NameGroup("api.example.com") {
		t.Fatalf("outgoing %v", out)
	}
}

func TestTranslator_IgnoreFlagsKeepCountersHonest(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.IgnoreQueries = true; c.LogLevel = "error" })
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select 1")
		h.log(ctx, otellog.SeverityInfo, "quiet")
		h.log(ctx, otellog.SeverityError, "loud")
	})
	if len(h.sink.ofType("query")) != 0 || len(h.sink.ofType("log")) != 1 {
		t.Fatalf("records: %v", h.sink.all())
	}
	req := h.sink.ofType("request")[0]
	if req["queries"] != 0 || req["logs"] != 1 {
		t.Fatalf("counters %v %v", req["queries"], req["logs"])
	}
}

func TestTranslator_SamplingAndExceptionReroll(t *testing.T) {
	exc := func(span trace.Span) {
		span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", "E"), attribute.String("exception.message", "bad")))
	}
	h := newHarness(t, func(c *Config) { c.RequestSampleRate = 0; c.ExceptionSampleRate = 0 })
	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) { exc(span) })
	if len(h.sink.all()) != 0 {
		t.Fatalf("unsampled trace leaked: %v", h.sink.all())
	}

	h = newHarness(t, func(c *Config) { c.RequestSampleRate = 0; c.ExceptionSampleRate = 1 })
	h.request(context.Background(), 200, nil)
	if len(h.sink.all()) != 0 {
		t.Fatal("clean unsampled request must not be sent")
	}
	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) { exc(span) })
	if len(h.sink.ofType("request")) != 1 || len(h.sink.ofType("exception")) != 1 {
		t.Fatalf("exception re-roll should keep the execution: %v", h.sink.all())
	}
}

func TestSampledBy(t *testing.T) {
	var low, high trace.TraceID
	high[0] = 0xff
	if !sampledBy(low, 0.5) || sampledBy(high, 0.5) || !sampledBy(high, 1) || sampledBy(low, 0) {
		t.Fatal("sampledBy")
	}
}

func TestTranslator_DedupesRepeatedException(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", "*errors.errorString"), attribute.String("exception.message", "panic: boom")))
		span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", "panic"), attribute.String("exception.message", "boom"),
			attribute.String("exception.stacktrace", sampleStack)))
	})
	excs := h.sink.ofType("exception")
	if len(excs) != 1 {
		t.Fatalf("want 1 exception, got %d", len(excs))
	}
	if excs[0]["file"] != "/app/http/controllers/task.go" || excs[0]["line"] != 42 {
		t.Fatalf("stack not used: %v", excs[0])
	}
	mustValidate(t, h.sink.all())
}

func TestTranslator_LateChildStillLinks(t *testing.T) {
	h := newHarness(t, nil)
	ctx, root := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET"), attribute.String("http.route", "/x")))
	_, late := h.tracer.Start(ctx, "q", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("db.query.text", "select 9")))
	root.End()
	late.End()
	q := h.sink.ofType("query")
	if len(q) != 1 || q[0]["execution_source"] != "request" {
		t.Fatalf("late child: %v", h.sink.all())
	}
}

func TestTranslator_SweepEmitsOrphans(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.TraceMaxWait = time.Second })
	now := time.Now()
	h.tr.now = func() time.Time { return now }
	ctx, root := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET")))
	h.query(ctx, "select 3")
	h.log(ctx, otellog.SeverityInfo, "pending")
	if len(h.sink.all()) != 0 {
		t.Fatal("must hold until root ends")
	}
	h.tr.now = func() time.Time { return now.Add(2 * time.Second) }
	h.tr.Sweep()
	root.End()
	if len(h.sink.ofType("query")) != 1 || len(h.sink.ofType("log")) != 1 {
		t.Fatalf("orphans not emitted: %v", h.sink.all())
	}
	q := h.sink.ofType("query")[0]
	if q["execution_id"] != "" {
		t.Fatalf("orphan must have empty execution id: %v", q)
	}
	mustValidate(t, h.sink.all())
}

func TestTranslator_StandaloneLogGetsOwnTrace(t *testing.T) {
	h := newHarness(t, nil)
	h.log(context.Background(), otellog.SeverityInfo, "boot")
	logs := h.sink.ofType("log")
	if len(logs) != 1 {
		t.Fatal("standalone log missing")
	}
	mustValidate(t, logs)
}

func TestTranslator_UserRecord(t *testing.T) {
	h := newHarness(t, nil)
	h.request(context.Background(), 200, func(_ context.Context, span trace.Span) {
		span.SetAttributes(attribute.String("enduser.id", "42"), attribute.String(AttrUserName, "Ada"))
	})
	h.request(context.Background(), 200, func(_ context.Context, span trace.Span) {
		span.SetAttributes(attribute.String("enduser.id", "42"), attribute.String(AttrUserName, "Ada"))
	})
	if len(h.sink.ofType("user")) != 1 || h.sink.ofType("request")[0]["user"] != "42" {
		t.Fatalf("user: %v", h.sink.all())
	}
	mustValidate(t, h.sink.all())
}

func TestTranslator_DrainOnShutdown(t *testing.T) {
	h := newHarness(t, nil)
	ctx, _ := h.tracer.Start(context.Background(), "r", trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", "GET")))
	h.query(ctx, "select 4")
	_ = h.tp.Shutdown(context.Background())
	if len(h.sink.ofType("query")) != 1 {
		t.Fatalf("drain: %v", h.sink.all())
	}
}
