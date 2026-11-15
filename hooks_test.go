package nightwatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

func TestHooks_RejectQueriesDropsTheQueryAndItsCount(t *testing.T) {
	h := newHarness(t, nil)
	var seen []Query
	h.tr.RejectQueries(func(q Query) bool {
		seen = append(seen, q)

		return strings.Contains(q.SQL, "health_checks")
	})

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select 1 from health_checks")
		h.query(ctx, "select * from tasks where id = ?")
	})

	queries := h.sink.ofType(RecordQuery)
	if len(queries) != 1 || queries[0]["sql"] != "select * from tasks where id = ?" {
		t.Fatalf("queries = %v", queries)
	}

	if got := h.sink.ofType(RecordRequest)[0]["queries"]; got != 1 {
		t.Fatalf("request counts %v queries, want 1 (a rejected query is not counted, like Laravel)", got)
	}

	if len(seen) != 2 || seen[0].Connection != "sqlite" || seen[0].Duration <= 0 {
		t.Fatalf("callback saw %+v", seen)
	}

	mustValidate(t, h.sink.all())
}

func TestHooks_RedactQueriesChangesOnlyTheSQLAndTheGroupFollowsIt(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RedactQueries(func(q *Query) {
		q.SQL = strings.ReplaceAll(q.SQL, "'ali@example.com'", "'***'")
		q.Connection = "changed" // read-only in Laravel: ignored
	})

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select * from users where email = 'ali@example.com'")
	})

	query := h.sink.ofType(RecordQuery)[0]
	if query["sql"] != "select * from users where email = '***'" || query["connection"] != "sqlite" {
		t.Fatalf("query = %v", query)
	}

	if want := QueryGroup("sqlite", "sqlite", "select * from users where email = '***'"); query["_group"] != want {
		t.Fatalf("group %v, want the hash of the redacted SQL %v", query["_group"], want)
	}
}

func TestHooks_APanickingCallbackDropsTheRecordInsteadOfLeakingIt(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RedactQueries(func(q *Query) {
		if strings.Contains(q.SQL, "secret") {
			panic("redaction bug")
		}
	})
	h.tr.RejectQueries(func(q Query) bool {
		if strings.Contains(q.SQL, "boom") {
			panic(q)
		}

		return false
	})

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select 'secret'")
		h.query(ctx, "select 'boom'")
		h.query(ctx, "select 1")
	})

	queries := h.sink.ofType(RecordQuery)
	if len(queries) != 1 || queries[0]["sql"] != "select 1" {
		t.Fatalf("queries = %v", queries)
	}

	if got := h.sink.ofType(RecordRequest)[0]["queries"]; got != 1 {
		t.Fatalf("request counts %v queries, want 1", got)
	}
}

func TestHooks_UnrecoverableHandlerGetsThePanicButNeverTheRecord(t *testing.T) {
	h := newHarness(t, nil)
	var errs []error
	h.tr.HandleUnrecoverableErrorsUsing(func(err error) { errs = append(errs, err) })
	h.tr.RejectQueries(func(q Query) bool { panic(q) })
	h.tr.RedactQueries(func(*Query) { panic("plain text") })

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select 'card 4111 1111 1111 1111'")
	})

	if len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}

	if text := errs[0].Error(); strings.Contains(text, "4111") || !strings.Contains(text, "nightwatch.Query") {
		t.Fatalf("error %q must name the record type, never its contents", text)
	}
}

func TestHooks_APanickingUnrecoverableHandlerIsIgnored(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.HandleUnrecoverableErrorsUsing(func(error) { panic("handler bug") })
	h.tr.RejectQueries(func(Query) bool { panic("callback bug") })

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select 1")
	})

	if len(h.sink.ofType(RecordRequest)) != 1 || len(h.sink.ofType(RecordQuery)) != 0 {
		t.Fatalf("records = %v", h.sink.all())
	}
}

func (h *harness) cacheEvent(ctx context.Context, key, kind string) {
	h.child(ctx, RecordCacheEvent, attribute.String(AttrCacheStore, "redis"),
		attribute.String(AttrCacheKey, key), attribute.String(AttrCacheType, kind), attribute.Int64(AttrCacheTTL, 60)).End()
}

func TestHooks_CacheEventsAreRejectedByCallbackAndRedacted(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RejectCacheEvents(func(e CacheEvent) bool { return e.Type == CacheMiss })
	h.tr.RedactCacheEvents(func(e *CacheEvent) {
		e.Key = strings.Replace(e.Key, "ali@example.com", "***", 1)
		e.Store = "changed" // read-only in Laravel: ignored
	})

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.cacheEvent(ctx, "user:ali@example.com", CacheHit)
		h.cacheEvent(ctx, "user:ali@example.com", CacheMiss)
	})

	events := h.sink.ofType(RecordCacheEvent)
	if len(events) != 1 || events[0]["key"] != "user:***" || events[0]["store"] != "redis" || events[0]["ttl"] != int64(60) {
		t.Fatalf("cache events = %v", events)
	}

	if events[0]["_group"] != CacheGroup("redis", "user:***") || h.sink.ofType(RecordRequest)[0]["cache_events"] != 1 {
		t.Fatalf("group or counter wrong: %v", events[0])
	}
}

func TestHooks_RejectCacheKeysTakesPHPStyleRegexesOrExactKeys(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RejectCacheKeys("/^session:/i", "rate:exact", "/(unclosed/", "#^lock-\\d+$#")

	keys := []string{"SESSION:abc", "rate:exact", "rate:exact:no", "/(unclosed/", "(unclosed", "lock-12", "lock-x", "user:1"}
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		for _, key := range keys {
			h.cacheEvent(ctx, key, CacheHit)
		}
	})

	var kept []string
	for _, event := range h.sink.ofType(RecordCacheEvent) {
		kept = append(kept, event["key"].(string))
	}

	// "/(unclosed/" is not a valid pattern, so like PHP it only matches itself.
	if strings.Join(kept, " ") != "rate:exact:no (unclosed lock-x user:1" {
		t.Fatalf("kept %v", kept)
	}
}

func TestHooks_DefaultVendorCacheKeysAreKeptUnlessCaptureIsTurnedOff(t *testing.T) {
	vendor := []string{"illuminate:queue:restart", "illuminate:cache:flexible:created:k", "framework/schedule-x", "telescope:x", "laravel:pulse:x", "app:x"}

	run := func(capture *bool) []string {
		h := newHarness(t, nil)
		if capture != nil {
			h.tr.CaptureDefaultVendorCacheKeys(*capture)
		}
		h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
			for _, key := range vendor {
				h.cacheEvent(ctx, key, CacheHit)
			}
		})
		var kept []string
		for _, event := range h.sink.ofType(RecordCacheEvent) {
			kept = append(kept, event["key"].(string))
		}

		return kept
	}

	if kept := run(nil); len(kept) != len(vendor) {
		t.Fatalf("by default every key is recorded (no change for existing apps), kept %v", kept)
	}

	off := false
	if kept := run(&off); strings.Join(kept, " ") != "illuminate:cache:flexible:created:k app:x" {
		t.Fatalf("Laravel's vendor list should be rejected, kept %v", kept)
	}

	if got := DefaultVendorCacheKeys(); len(got) != 8 || got[1] != "/^illuminate:(?!cache:flexible:created:)/" {
		t.Fatalf("DefaultVendorCacheKeys() = %v", got)
	}
}

func TestHooks_MailIsRejectedByCallbackAndItsSubjectRedacted(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RejectMail(func(m Mail) bool { return m.Class == "app.InternalAlert" })
	h.tr.RedactMail(func(m *Mail) {
		m.Subject = strings.ReplaceAll(m.Subject, "Ali", "***")
		m.To = 99 // read-only in Laravel: ignored
	})

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.child(ctx, RecordMail, attribute.String(AttrMailClass, "app.InternalAlert"), attribute.String(AttrMailSubject, "x")).End()
		h.child(ctx, RecordMail, attribute.String(AttrMailClass, "app.Welcome"), attribute.String(AttrMailSubject, "Hello Ali"),
			attribute.Int(AttrMailTo, 1), attribute.String(AttrMailMailer, "smtp")).End()
	})

	mail := h.sink.ofType(RecordMail)
	if len(mail) != 1 || mail[0]["subject"] != "Hello ***" || mail[0]["to"] != int64(1) || mail[0]["mailer"] != "smtp" {
		t.Fatalf("mail = %v", mail)
	}

	if h.sink.ofType(RecordRequest)[0]["mail"] != 1 {
		t.Fatal("a rejected mail must not be counted")
	}
}

func TestHooks_NotificationsAndQueuedJobsAreRejectedByCallback(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RejectNotifications(func(n Notification) bool { return n.Channel == "database" })
	h.tr.RejectQueuedJobs(func(j QueuedJob) bool { return j.Name == "jobs.LowPriority" })

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.child(ctx, RecordNotification, attribute.String(AttrNotificationChan, "database"), attribute.String(AttrNotificationClass, "N")).End()
		h.child(ctx, RecordNotification, attribute.String(AttrNotificationChan, "mail"), attribute.String(AttrNotificationClass, "N")).End()
		h.child(ctx, RecordQueuedJob, attribute.String(AttrJobName, "jobs.LowPriority"), attribute.String(AttrJobQueue, "low")).End()
		h.child(ctx, RecordQueuedJob, attribute.String(AttrJobName, "jobs.SendEmail"), attribute.String(AttrJobQueue, "mail")).End()
	})

	notes, jobs := h.sink.ofType(RecordNotification), h.sink.ofType(RecordQueuedJob)
	if len(notes) != 1 || notes[0]["channel"] != "mail" || len(jobs) != 1 || jobs[0]["name"] != "jobs.SendEmail" || jobs[0]["queue"] != "mail" {
		t.Fatalf("notifications %v, jobs %v", notes, jobs)
	}

	req := h.sink.ofType(RecordRequest)[0]
	if req["notifications"] != 1 || req["jobs_queued"] != 1 {
		t.Fatalf("counters %v %v", req["notifications"], req["jobs_queued"])
	}
}

func (h *harness) outgoing(ctx context.Context, url, host string) {
	_, span := h.tracer.Start(ctx, "GET", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("http.request.method", "GET"), attribute.String("url.full", url),
		attribute.String("server.address", host), attribute.Int64("http.response.status_code", 200)))
	span.End()
}

func TestHooks_OutgoingRequestsAreRejectedAndRedactedAndTheQueryStaysRedacted(t *testing.T) {
	h := newHarness(t, nil)
	var seen []string
	h.tr.RejectOutgoingRequests(func(r OutgoingRequest) bool {
		seen = append(seen, r.URL)

		return strings.Contains(r.URL, "metrics.internal")
	})
	h.tr.RedactOutgoingRequests(func(r *OutgoingRequest) {
		// A callback that puts a secret back into the query is redacted again.
		r.URL = strings.Replace(r.URL, "/users/42", "/users/***", 1) + "&api_key=k123"
	})

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.outgoing(ctx, "http://metrics.internal/push", "metrics.internal")
		h.outgoing(ctx, "https://api.example.com/users/42?token=abc&page=2", "api.example.com")
	})

	out := h.sink.ofType(RecordOutgoingRequest)
	if len(out) != 1 || out[0]["host"] != "api.example.com" {
		t.Fatalf("outgoing = %v", out)
	}

	if url := out[0]["url"].(string); url != "https://api.example.com/users/***?api_key=%5Bredacted%5D&page=2&token=%5Bredacted%5D" {
		t.Fatalf("url = %s", url)
	}

	if strings.Contains(strings.Join(seen, " "), "abc") {
		t.Fatalf("callbacks must see the URL after the built-in query redaction: %v", seen)
	}

	if h.sink.ofType(RecordRequest)[0]["outgoing_requests"] != 1 {
		t.Fatal("a rejected outgoing request must not be counted")
	}
}

func TestHooks_RedactExceptionsChangesTheMessageThePreviewAndTheGroup(t *testing.T) {
	h := newHarness(t, nil)
	var seen Exception
	h.tr.RedactExceptions(func(e *Exception) {
		seen = *e
		e.Message = strings.ReplaceAll(e.Message, "ali@example.com", "***")
		e.Class = "changed" // read-only in Laravel: ignored
	})

	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		addException(span, "no account for ali@example.com")
	})

	exception := h.sink.ofType(RecordException)[0]
	if exception["message"] != "no account for ***" || exception["class"] != "*errors.errorString" {
		t.Fatalf("exception = %v", exception)
	}

	if seen.Message != "no account for ali@example.com" || seen.Handled || seen.File == "" {
		t.Fatalf("callback saw %+v", seen)
	}

	request := h.sink.ofType(RecordRequest)[0]
	if strings.Contains(request["exception_preview"].(string), "ali@") || exception["_group"] != ExceptionGroup("*errors.errorString", "", "GET /api/tasks/:id|no account for ***", 0) {
		t.Fatalf("preview %v, group %v", request["exception_preview"], exception["_group"])
	}
}

func TestHooks_RedactCommandsChangesTheCommandLine(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RedactCommands(func(c *Command) {
		c.Command = strings.ReplaceAll(c.Command, "--password=hunter2", "--password=***")
		c.Name = "changed" // read-only in Laravel: ignored
	})

	ctx, cmd := h.execution(context.Background(), RecordCommand,
		attribute.String(AttrCommandName, "users:create"), attribute.String(AttrCommandLine, "artisan users:create --password=hunter2"))
	h.query(ctx, "select 1")
	cmd.End()

	command := h.sink.ofType(RecordCommand)[0]
	if command["command"] != "artisan users:create --password=***" || command["name"] != "users:create" || command["_group"] != NameGroup("users:create") {
		t.Fatalf("command = %v", command)
	}
}

func TestHooks_APanickingExecutionRedactionDropsTheWholeExecution(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RedactCommands(func(*Command) { panic("bug") })

	ctx, cmd := h.execution(context.Background(), RecordCommand, attribute.String(AttrCommandName, "users:create"))
	h.query(ctx, "select 1")
	cmd.End()

	if records := h.sink.all(); len(records) != 0 {
		t.Fatalf("an execution whose redaction failed must leave no records, not orphans: %v", records)
	}
}

func TestHooks_RedactRequestsChangesURLIPHeadersAndPayload(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.CaptureRequestPayload = true })
	var seen Request
	h.tr.RedactRequests(func(r *Request) {
		seen = *r
		r.URL = strings.Replace(r.URL, "/api/tasks/7", "/api/tasks/***", 1)
		r.IP = "0.0.0.0"
		delete(r.Headers, "x-tenant")
		r.Payload["card"] = "***"
		r.RouteName = "changed" // read-only in Laravel: ignored
	})

	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		span.SetAttributes(
			attribute.String(AttrHeaders, `{"x-tenant":["acme"],"accept":["text/html"]}`),
			attribute.String(AttrPayload, `{"card":"4111","note":"hi"}`),
			attribute.String(AttrRouteName, "tasks.show"),
		)
	})

	request := h.sink.ofType(RecordRequest)[0]
	if request["url"] != "http://127.0.0.1:2601/api/tasks/***" || request["ip"] != "0.0.0.0" || request["route_name"] != "tasks.show" {
		t.Fatalf("request = %v", request)
	}

	if request["headers"] != `{"accept":["text/html"]}` || request["payload"] != `{"card":"***","note":"hi"}` {
		t.Fatalf("headers %v payload %v", request["headers"], request["payload"])
	}

	if seen.Method != "GET" || seen.RoutePath != "/api/tasks/:id" || seen.StatusCode != 500 || seen.IP != "127.0.0.1" {
		t.Fatalf("callback saw %+v", seen)
	}
}

func TestHooks_ARequestCallbackCannotPutSecretsBackIn(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.CaptureRequestPayload = true })
	h.tr.RedactRequests(func(r *Request) {
		r.URL += "?token=abc"
		r.Headers = map[string][]string{"authorization": {"Bearer abc"}, "Cookie": {"s=abc"}}
		r.Payload = map[string]any{"password": "abc", "nested": map[string]any{"password": "abc"}}
	})

	h.request(context.Background(), 500, nil)

	request := h.sink.ofType(RecordRequest)[0]
	for _, field := range []string{"url", "headers", "payload"} {
		if strings.Contains(request[field].(string), "abc") {
			t.Fatalf("%s leaks a secret: %v", field, request[field])
		}
	}
}

func TestHooks_RequestPayloadStaysEmptyWhenNotCaptured(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RedactRequests(func(r *Request) {
		if r.Payload != nil {
			t.Errorf("payload %v, want nil when capture is off", r.Payload)
		}
	})

	h.request(context.Background(), 500, func(_ context.Context, span trace.Span) {
		span.SetAttributes(attribute.String(AttrPayload, `{"a":1}`))
	})

	if got := h.sink.ofType(RecordRequest)[0]["payload"]; got != "" {
		t.Fatalf("payload = %v", got)
	}
}

func TestHooks_ChildrenShowTheRedactedPathInTheirExecutionPreview(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.RootGrace = time.Second })
	now := time.Now()
	h.tr.now = func() time.Time { return now }
	calls := 0
	h.tr.RedactRequests(func(r *Request) {
		calls++
		r.URL = strings.Replace(r.URL, "/api/tasks/7", "/api/tasks/***", 1)
	})

	var late trace.Span
	var lateCtx context.Context
	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select 1")
		lateCtx, late = h.tracer.Start(ctx, "q", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("db.query.text", "select 2")))
	})
	now = now.Add(2 * time.Second)
	h.tr.Sweep()
	late.End()
	h.log(lateCtx, otellog.SeverityInfo, "much later")

	children := append(h.sink.ofType(RecordQuery), h.sink.ofType(RecordLog)...)
	if len(children) != 3 {
		t.Fatalf("children = %v", children)
	}

	for _, child := range children {
		if child["execution_preview"] != "GET /api/tasks/***" {
			t.Fatalf("a child leaks the path the callback redacted: %v", child)
		}
	}

	if calls != 1 {
		t.Fatalf("the callback ran %d times, want once per request", calls)
	}
}

func TestHooks_APanickingRequestRedactionDropsTheRequestAndItsChildren(t *testing.T) {
	h := newHarness(t, nil)
	h.tr.RedactRequests(func(*Request) { panic("bug") })

	h.request(context.Background(), 200, func(ctx context.Context, _ trace.Span) {
		h.query(ctx, "select 1")
		h.log(ctx, otellog.SeverityInfo, "hi")
	})

	if records := h.sink.all(); len(records) != 0 {
		t.Fatalf("records = %v", records)
	}
}
