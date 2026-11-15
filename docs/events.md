# Events and Executions

## Event types

nightwatch-go sends the same record types as `laravel/nightwatch`. Each record is made from an OpenTelemetry span, a span event or a log record. Standard OpenTelemetry attribute names are used where they exist. Nightwatch-only fields use `nightwatch.*` attributes, defined as `nightwatch.Attr*` constants.

| Record | Made from | Execution root |
|---|---|---|
| `request` | a server span with `http.request.method` (Goravel's telemetry middleware) plus `http.route`, `url.path`, `http.response.status_code` and sizes | yes |
| `query` | a client span with `db.query.text` (Goravel's database instrumentation) | child |
| `outgoing-request` | a client span with `http.request.method` and `url.full` (`nwgoravel.Transport`) | child |
| `exception` | a span event `exception` (`ReportError`, `ReportHandled`, Goravel's middleware, panics) | child |
| `log` | an OpenTelemetry log record (Goravel's `otel` log channel), linked by span id | child |
| `queued-job` | a span with `nightwatch.record=queued-job` (`StartJobDispatch`, `nwgoravel.Queue`) | child |
| `job-attempt` | a span with `nightwatch.record=job-attempt` (`StartJobAttempt`, wrapped jobs) | yes |
| `command` | a span with `nightwatch.record=command` (`StartCommand`, every artisan run) | yes |
| `scheduled-task` | a span with `nightwatch.record=scheduled-task` (`StartScheduledTask`, the wrapped scheduler) | yes |
| `cache-event` | a span with `nightwatch.record=cache-event` (`StartCache`, `WithContext(ctx)` caches) | child |
| `mail` | a span with `nightwatch.record=mail` (`nwgoravel.Mail`) | child |
| `notification` | a span with `nightwatch.record=notification` (`StartNotification`) | child |
| `user` | `enduser.id` plus `nightwatch.user.name` or `nightwatch.user.username`, once per id | none |

## Execution contexts

An execution is the unit Nightwatch groups events by: a request, a job attempt, a command or a scheduled task. Every other record is a child of one execution.

`trace_id` is the OpenTelemetry trace id written as a UUID. `execution_id` is the trace id for requests, commands and scheduled tasks, and the attempt id for jobs. A queued job carries the trace of the code that queued it, so the worker's job attempt joins the same trace.

## Passing the context

Go has no request-global state. A child record joins its execution only through the `context.Context` you pass. In a Goravel handler, that is `ctx.Context()`:

```go
reqCtx := ctx.Context()

cache := facades.Cache().WithContext(reqCtx)
if !cache.Has("task:" + id) {
	_ = cache.Put("task:"+id, "seen", time.Minute)
}

var rows []map[string]any
if err := facades.Orm().WithContext(reqCtx).Query().Raw("select * from tasks where id = ?", id).Scan(&rows); err != nil {
	return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": err.Error()})
}

facades.Log().WithContext(reqCtx).Info("task loaded")
```

`nightwatch.Active(ctx)` reports whether a context belongs to an execution. The wrappers (`Queue`, `Mail`, `Cache`, `Transport`) trace only when it does. With a disabled client nothing is ever marked, so the wrappers pass every call straight through: `Queue` and `Mail` return the very same object you gave them.

## Timing

A trace is held until its root span ends, for at most `TraceMaxWait` (10 seconds). A finished execution waits `RootGrace` (1 second) more for late logs and spans, then the execution record and its children are sent together. A child that arrives later is sent alone and counted as a late child.

## The sender

Records are sent in batches of 500, or every 5 seconds, gzipped, with an `Idempotency-Key` per batch that is reused on retry.

- Network errors, 429 and 5xx answers are retried with backoff: at least `Retry-After`, at most 5 minutes between tries. After `MaxRetries` (5) retries or `MaxRetryTime` (2 minutes), the batch is dropped and counted, so one stuck batch cannot block the next.
- Other 4xx answers drop the batch with one log line. A 402 answer pauses sending for 15 minutes.
- At most `MaxBuffered` (5000) records wait in memory. Past that, the oldest are dropped and counted. `Enqueue` never blocks.
- On shutdown a failing batch gets one final attempt and is then dropped.

`client.Stats()` reports the pending and dropped records and the translator counters.
