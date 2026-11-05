# nightwatch-go

An OpenTelemetry based Nightwatch client for Go and Goravel. A Go app that uses it reports into
[Omni](https://omni.vortechron.com) (a Nightwatch compatible receiver) with the same record shapes as
`laravel/nightwatch` v1.30.3, so Omni's dashboards work unchanged.

```
OpenTelemetry spans + logs  ->  translator  ->  background sender  ->  POST /api/omni/ingest
(Goravel already makes them)    (Nightwatch      (batch, gzip, retry)
                                 records)
```

## Install

```sh
go get github.com/vortechron/nightwatch-go@latest
```

Needs Go 1.26. The core package `nightwatch` does not import Goravel. The subpackage
`github.com/vortechron/nightwatch-go/goravel` does.

## Goravel wiring

1. Add the provider last in `bootstrap/providers.go`. It points Goravel telemetry at Nightwatch, starts the
   sender, wraps cache, schedule, console commands and queue jobs, and flushes on shutdown.

```go
import nwgoravel "github.com/vortechron/nightwatch-go/goravel"

&telemetry.ServiceProvider{},
&nwgoravel.ServiceProvider{}, // after the framework providers
```

2. Add the HTTP middleware (Goravel's telemetry middleware first, ours second):

```go
WithMiddleware(func(m configuration.Middleware) {
    m.Append(telemetryhttp.Telemetry(), nwgoravel.LazyMiddleware(facades.App))
})
```

3. Set the environment: `NIGHTWATCH_TOKEN` and `NIGHTWATCH_BASE_URL` (your Omni host).

Where Goravel's API carries no `context.Context`, wrap per request:

```go
nwgoravel.Queue(ctx, facades.Queue()).Job(&jobs.Send{}, args).OnQueue("mail").Dispatch() // keeps the trace
nwgoravel.Mail(ctx, facades.Mail()).To(addrs).Send(m)                                     // mail record
facades.Cache().WithContext(ctx).Get("k")                                                 // cache-event (global wrap)
```

A job that does database or HTTP work implements `HandleContext(ctx, args...)` (`nwgoravel.ContextJob`) so that
work joins the trace. Use `facades.Orm().WithContext(ctx)` and `facades.Log().WithContext(ctx)` in handlers.
For outgoing HTTP use `&http.Client{Transport: nwgoravel.Transport(nil)}`.

Report errors with `nightwatch.ReportError(ctx, err)` (unhandled) or `nightwatch.ReportHandled(ctx, err)`.
Set the user with `nwgoravel.WithUserResolver` on the middleware, or `nightwatch.SetUser(ctx, user)`.

A complete app lives in [`example/goravel`](example/goravel).

## Environment

Same names as the PHP package.

| Variable | Default | Meaning |
|---|---|---|
| `NIGHTWATCH_ENABLED` | `true` (needs a token) | Master switch |
| `NIGHTWATCH_TOKEN` | none | App token. No token means disabled |
| `NIGHTWATCH_BASE_URL` | `https://nightwatch.laravel.com` | Omni host. Ingest URL comes from `POST /api/agent-auth`, fallback `/api/omni/ingest` |
| `NIGHTWATCH_INGEST_URL` | none | Skip discovery and post here |
| `NIGHTWATCH_DEPLOY` | empty | Deploy id |
| `NIGHTWATCH_SERVER` | hostname | Server label |
| `NIGHTWATCH_REQUEST_SAMPLE_RATE` | `1.0` | Chance a request execution is kept (decided from the trace id, so a job follows its request) |
| `NIGHTWATCH_COMMAND_SAMPLE_RATE` | `1.0` | Commands, and jobs not started by a request |
| `NIGHTWATCH_SCHEDULED_TASK_SAMPLE_RATE` | `1.0` | Scheduled tasks |
| `NIGHTWATCH_EXCEPTION_SAMPLE_RATE` | `1.0` | A dropped execution with an unhandled exception is kept with this chance |
| `NIGHTWATCH_IGNORE_QUERIES`, `_OUTGOING_REQUESTS`, `_CACHE_EVENTS`, `_MAIL`, `_NOTIFICATIONS` | `false` | Skip that record type (counters stay true) |
| `NIGHTWATCH_LOG_LEVEL` | `LOG_LEVEL` or `debug` | Minimum log level sent |
| `NIGHTWATCH_REDACT_HEADERS` | the six below | Extra header names to redact. Always redacted: Authorization, Cookie, Proxy-Authorization, Set-Cookie, X-CSRF-TOKEN, X-XSRF-TOKEN. Values become `[redacted]`, at capture time and again before send |
| `NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD` | `false` | Keep the body of 500 responses |
| `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` | `_token,password,password_confirmation` | Fields blanked in a captured body |

Sender behaviour: batch at 500 records or 5 s, gzip, `Idempotency-Key` per batch (reused on retry), retry on
network errors, 429 and 5xx with backoff, drop other 4xx with one log line, pause 15 minutes on 402, at most
5000 records in memory (oldest dropped and counted), flush on shutdown. `Enqueue` never blocks.

## Span to record mapping

Standard OpenTelemetry names are used where they exist. Nightwatch only fields use `nightwatch.*` attributes
(constants in `attrs.go`). A trace is held until its root span ends (10 s at most), then the execution record
and its children are sent together.

| Record | Made from | Execution root |
|---|---|---|
| `request` | server span with `http.request.method` (Goravel telemetry middleware) plus `http.route`, `url.path`, `http.response.status_code`, sizes | yes |
| `query` | client span with `db.query.text` (Goravel database instrumentation) | child |
| `outgoing-request` | client span with `http.request.method` and `url.full` (`nwgoravel.Transport`) | child |
| `exception` | span event `exception` (`ReportError`, `ReportHandled`, Goravel middleware, panics) | child |
| `log` | OpenTelemetry log record (Goravel `otel` log channel), linked by span id | child |
| `queued-job` | span with `nightwatch.record=queued-job` (`StartJobDispatch`, `nwgoravel.Queue`) | child |
| `job-attempt` | span with `nightwatch.record=job-attempt` (`StartJobAttempt`, wrapped jobs) | yes |
| `command` | span with `nightwatch.record=command` (`StartCommand`, wrapped commands) | yes |
| `scheduled-task` | span with `nightwatch.record=scheduled-task` (`StartScheduledTask`, wrapped `Schedule().Call`) | yes |
| `cache-event` | span with `nightwatch.record=cache-event` (`StartCache`, `WithContext(ctx)` caches) | child |
| `mail` | span with `nightwatch.record=mail` (`nwgoravel.Mail`) | child |
| `notification` | span with `nightwatch.record=notification` (`StartNotification`) | child |
| `user` | `enduser.id` plus `nightwatch.user.name` or `.username`, once per id | none |

`trace_id` is the OpenTelemetry trace id written as a UUID. `execution_id` is the trace id for requests, commands
and scheduled tasks, and the attempt id for jobs. The trace travels into queued jobs as a W3C `traceparent` in
a trailing job argument that the wrapped job removes before your `Handle` runs.

## Tools

```sh
go run ./cmd/omni-capture -addr 127.0.0.1:2601 -dir ./capture   # fake Omni; same decoder rules as Omni
go run ./cmd/nwdiff -golden ./golden -go ./capture [-allow allow.txt]   # compare two capture folders
```

`omni-capture` also works for the PHP agent (`NIGHTWATCH_BASE_URL=http://127.0.0.1:2601`). `nwdiff` exits 1
when the Go side lacks a type or field, has a JSON type that differs from Laravel's, has a bad `_group`
length, or has a child whose `execution_id` has no execution record. The allow list has lines like
`type:log` or `field:request.route_name`.

## Develop

```sh
golangci-lint run ./... && go vet ./... && go test -race ./...
cd example/goravel && go test -run TestExample .   # needs Redis (DB 15); starts and stops its own processes
```

## Known gaps

- Stage timings: `action` gets the whole request time; `bootstrap`, `render`, `sending`, `terminating` are 0.
- Query `file` and `line` are empty (Goravel's database spans do not carry the caller).
- `route_action` is empty unless you set `nightwatch.route.action` on the span.
- Exception class is the Go type (`*errors.errorString` for most errors), so `ReportError` with a stack is
  needed for good grouping. Panics are grouped as class `panic`.
- Job attempts are counted per job id inside one worker; Goravel does not pass the attempt number.
- `Schedule().Command(...)` runs as a `command`, not a `scheduled-task`.
- Goravel's mailer always sends SMTP AUTH, which a plain Mailpit refuses (the example's test brings a fake SMTP).
- Chained jobs (`Queue().Chain`) do not carry the trace.
- Goravel's own HTTP client did not emit spans in the example even with `enable_telemetry`; use `nwgoravel.Transport`.
