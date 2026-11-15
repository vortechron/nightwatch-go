package nightwatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestPause_NothingButLogsAndExceptionsIsRecordedUntilResume(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	h.request(context.Background(), 200, func(ctx context.Context, span trace.Span) {
		paused := Pause(MarkActive(ctx))
		if !Paused(paused) || Paused(ctx) {
			t.Error("Paused must report the paused context only")
		}
		h.query(paused, "select 'paused'")
		h.cacheEvent(paused, "k", CacheHit)
		h.outgoing(paused, "https://api.example.com/x", "api.example.com")
		h.log(paused, otellog.SeverityWarn, "logs are still kept, as in Laravel")

		resumed := Resume(paused)
		if Paused(resumed) {
			t.Error("Resume must end the pause")
		}
		h.query(resumed, "select 'resumed'")
		addException(span, "exceptions are still kept")
	})

	queries := h.sink.ofType(RecordQuery)
	if len(queries) != 1 || queries[0]["sql"] != "select 'resumed'" {
		t.Fatalf("queries = %v", queries)
	}

	request := h.sink.ofType(RecordRequest)[0]
	if request["queries"] != 1 || request["cache_events"] != 0 || request["outgoing_requests"] != 0 || request["logs"] != 1 || request["exceptions"] != 1 {
		t.Fatalf("counters: %v", request)
	}
}

func TestPause_IgnoreRunsTheCallbackPaused(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		Ignore(ctx, func(ctx context.Context) {
			h.query(ctx, "select 'ignored'")
		})
		h.query(ctx, "select 'kept'")
	})

	if queries := h.sink.ofType(RecordQuery); len(queries) != 1 || queries[0]["sql"] != "select 'kept'" {
		t.Fatalf("queries = %v", queries)
	}
}

func TestPause_ALateChildOfAPausedSpanStaysOut(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RootGrace = time.Second })
	useHarness(t, h)
	now := time.Now()
	h.tr.now = func() time.Time { return now }

	var late trace.Span
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		_, late = h.tracer.Start(Pause(MarkActive(ctx)), "q", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
			attribute.String("db.query.text", "select 'late and paused'")))
	})
	now = now.Add(2 * time.Second)
	h.tr.Sweep()
	late.End()

	for _, record := range h.sink.all() {
		if sql, _ := record["sql"].(string); strings.Contains(sql, "paused") {
			t.Fatalf("late paused query sent: %v", record)
		}
	}
}

func TestPause_OutsideAnExecutionNothingChanges(t *testing.T) {
	ctx := context.Background()
	if Pause(ctx) != ctx || Resume(ctx) != ctx || Paused(ctx) {
		t.Fatal("a plain context has nothing to pause")
	}
}

func dispatch(ctx context.Context) JobEnvelope {
	dctx, end := StartJobDispatch(ctx, JobInfo{Name: "jobs.Sync", Connection: "redis", Queue: "default"})
	env, _ := DecodeJobEnvelope(EncodeJobEnvelope(dctx, "redis", "default", false))
	end(nil)

	return env
}

func TestPause_AJobQueuedWhilePausedOrSampledOutIsNotRecordedInTheWorker(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	var paused, sampledOut, normal JobEnvelope
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		Ignore(ctx, func(ctx context.Context) { paused = dispatch(ctx) })
		normal = dispatch(ctx)
	})
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		DontSample(ctx)
		sampledOut = dispatch(ctx)
	})

	if !strings.HasSuffix(paused.TraceParent, "-00") || !strings.HasSuffix(sampledOut.TraceParent, "-00") || !strings.HasSuffix(normal.TraceParent, "-01") {
		t.Fatalf("traceparents: paused %q, sampled out %q, normal %q", paused.TraceParent, sampledOut.TraceParent, normal.TraceParent)
	}

	for _, env := range []JobEnvelope{paused, sampledOut, normal} {
		_, end := StartJobAttempt(context.Background(), env, JobInfo{Name: "jobs.Sync"}, 1)
		end(nil)
	}

	if queued, attempts := h.sink.ofType(RecordQueuedJob), h.sink.ofType(RecordJobAttempt); len(queued) != 1 || len(attempts) != 1 || attempts[0]["job_id"] != normal.JobID {
		t.Fatalf("queued %v, attempts %v", queued, attempts)
	}
}

func TestPause_AWorkerThatRecordsEverySpanStillDropsAnUnsampledJob(t *testing.T) {
	h := newHarness(t, nil)
	alwaysOn := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSyncer(h.tr.SpanExporter()))
	t.Cleanup(func() { _ = alwaysOn.Shutdown(context.Background()) })

	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"
	ctx := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier{"traceparent": parent})
	_, span := alwaysOn.Tracer("w").Start(ctx, "queue.process", trace.WithAttributes(attribute.String(AttrRecord, RecordJobAttempt)))
	span.End()

	if got := h.sink.ofType(RecordJobAttempt); len(got) != 0 {
		t.Fatalf("a job whose dispatcher was not sampled must not be recorded: %v", got)
	}
}

func TestPause_ManyIgnoreCallsDoNotPushOutRealRecords(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		for range 3_000 {
			Ignore(ctx, func(ctx context.Context) {
				_, query := h.tracer.Start(ctx, "SELECT", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
					attribute.String("db.query.text", "select 'ignored'")))
				query.End()
			})
		}
		Ignore(ctx, func(ctx context.Context) {
			h.log(ctx, otellog.SeverityWarn, "a log inside Ignore is still kept")
		})
		h.query(ctx, "select 'kept'")
	})

	if queries := h.sink.ofType(RecordQuery); len(queries) != 1 || queries[0]["sql"] != "select 'kept'" {
		t.Fatalf("like Laravel, ignored work costs nothing; queries = %d", len(queries))
	}

	if logs := h.sink.ofType(RecordLog); len(logs) != 1 {
		t.Fatalf("logs = %v", logs)
	}

	if dropped := h.tr.Stats().SpansDropped; dropped != 0 {
		t.Fatalf("%d spans dropped by the per-trace limit", dropped)
	}
}

func TestPause_NestedWorkLogsExceptionsAndLateChildrenUnderIgnoreBehaveAsBefore(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RootGrace = time.Second })
	useHarness(t, h)
	now := time.Now()
	h.tr.now = func() time.Time { return now }

	var late trace.Span
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		Ignore(ctx, func(ctx context.Context) {
			work, span := h.tracer.Start(ctx, "work")
			h.query(work, "select 'nested and ignored'") // ends before its parent
			_, late = h.tracer.Start(work, "SELECT", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
				attribute.String("db.query.text", "select 'late and ignored'")))
			span.End()
			h.log(work, otellog.SeverityWarn, "logged under ignored work")
			h.failedQuery(ctx, "ignored query failed")
		})
	})
	now = now.Add(2 * time.Second)
	h.tr.Sweep()
	late.End()
	h.tr.Drain()

	for _, record := range h.sink.all() {
		if sql, _ := record["sql"].(string); strings.Contains(sql, "ignored") {
			t.Fatalf("ignored query sent: %v", record)
		}
	}

	request := h.sink.ofType(RecordRequest)[0]
	if request["logs"] != 1 || request["exceptions"] != 1 || len(h.sink.ofType(RecordLog)) != 1 || len(h.sink.ofType(RecordException)) != 1 {
		t.Fatalf("logs and exceptions under Ignore are still recorded: %v", request)
	}

	if h.tr.Stats().LateChildren != 0 {
		t.Fatalf("a late child of ignored work is left out quietly: %+v", h.tr.Stats())
	}
}

func TestPause_OneMarkerPerExecution(t *testing.T) {
	h := newHarness(t, nil)
	useHarness(t, h)

	var first, second trace.SpanContext
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		ctx = MarkActive(ctx)
		first = trace.SpanContextFromContext(Pause(ctx))
		second = trace.SpanContextFromContext(Pause(ctx))
	})

	if !first.IsValid() || !first.Equal(second) {
		t.Fatalf("every Pause of one execution must reuse its marker: %v, %v", first.SpanID(), second.SpanID())
	}
}
