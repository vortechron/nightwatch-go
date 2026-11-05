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

1. Add the provider last in `bootstrap/providers.go`. It binds the client in Goravel's container (key
   `nwgoravel.BindingClient`, read it with `nwgoravel.ClientFrom(app)`; there is no package level client), points
   telemetry at Nightwatch, starts the sender, wraps cache, schedule, console commands and queue jobs, and flushes on
   shutdown. Use `&nwgoravel.NightwatchServiceProvider{Config: &cfg}` to pass a hand built `nightwatch.Config`.

```go
import nwgoravel "github.com/vortechron/nightwatch-go/goravel"

&telemetry.ServiceProvider{},
&nwgoravel.NightwatchServiceProvider{}, // after the framework providers
```

2. Add the HTTP middleware (Goravel's telemetry middleware first, ours second). The filter makes Goravel start no
   span at all for ignored paths (static assets, favicons; see `NIGHTWATCH_IGNORE_PATHS`):

```go
WithMiddleware(func(m configuration.Middleware) {
    m.Append(
        telemetryhttp.Telemetry(telemetryhttp.WithFilter(nwgoravel.IgnoreFilter(facades.App))),
        nwgoravel.LazyMiddleware(facades.App),
    )
})
```

   Without the filter the middleware still drops an ignored request (it tags the span and the translator sends
   nothing for it), but Goravel has already started a span.

3. Set `NIGHTWATCH_TOKEN` and `NIGHTWATCH_BASE_URL` (your Omni host) in `.env` or in the process environment. The
   provider reads every `NIGHTWATCH_*` setting (and `LOG_LEVEL`) through Goravel's config, which knows `.env` (Goravel
   does not export `.env` to the process), and falls back to the process environment. Values in `Config` win over both.

The provider refuses to start (panics at boot with `ErrExporterConflict`) when `telemetry.traces.exporter` or
`telemetry.logs.exporter` already names another exporter, because Goravel allows one exporter per signal. Leave
`OTEL_TRACES_EXPORTER` and `OTEL_LOGS_EXPORTER` empty. It also tightens the span and log batch processors to export
at least every 500 ms, so children that end just after their request still count.

Where Goravel's API carries no `context.Context`, wrap per request. Go has no request-global state, so the
context has to be passed; Goravel's `Queue().Job(...).Dispatch()` and `Mail().Send()` cannot be wrapped globally
(no ctx to link to), and a provider level wrap would only create records with no execution:

```go
nwgoravel.Queue(ctx, facades.Queue()).Job(&jobs.Send{}, args).OnQueue("mail").Dispatch() // keeps trace and user; also Chain(...)
nwgoravel.Mail(ctx, facades.Mail()).To(addrs).Send(m)                                     // mail record
facades.Cache().WithContext(ctx).Get("k")                                                 // cache-event (global wrap)
```

These wrappers (`Queue`, `Mail`, `Cache`, `Transport`) trace only when `ctx` belongs to a Nightwatch execution: a
request that went through the middleware, a command, a job attempt or a scheduled task (`nightwatch.Active(ctx)`).
With a disabled client nothing is ever marked, so they are pure pass-through: `Queue` and `Mail` return the very same
object, no envelope argument is added and no span is made. The same holds for code that runs outside any execution
(a bare goroutine with `context.Background()`), which has no execution to attach to anyway. When the client is
disabled the provider also leaves the cache, schedule and artisan alone, and only installs `TolerateEnvelopes` on the
job registry: a worker that does not trace still removes the envelope that a tracing dispatcher added, so a job
handles the same arguments with and without it.

Every worker that runs these jobs must run the provider too: the trace travels as a trailing job argument that the
wrapped job removes before your `Handle` runs. The argument is a string `nwtp2|<traceparent>|<job id>|<connection>|<queue>|<user>|<sync>|<crc32>`;
only a last argument that passes the strict check (tag, W3C traceparent, UUID, checksum) is removed. Evidence for
this choice: Goravel's `ConvertArgs` silently drops an argument with an unknown `Type`, so a dedicated type would
never reach `Handle`; and `Handle(args ...any)` is the only place the framework gives a job anything.

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
| `NIGHTWATCH_IGNORE_PATHS` | empty | Comma separated request path globs that are not recorded at all: no request record, no queries, logs or exceptions of it, nothing in the counters. Example `/build/**,/favicon.ico`. See "Ignoring paths" below for the exact rules. Add more in code with `nwgoravel.WithIgnorePaths(...)` |
| `NIGHTWATCH_LOG_LEVEL` | `LOG_LEVEL` or `debug` | Minimum log level sent |
| `NIGHTWATCH_REDACT_HEADERS` | the six below | Extra header names to redact. Always redacted: Authorization, Cookie, Proxy-Authorization, Set-Cookie, X-CSRF-TOKEN, X-XSRF-TOKEN. Values become `[redacted]`, at capture time and again before send |
| `NIGHTWATCH_REDACT_QUERY` | the defaults below | Extra query parameter names to redact. Always redacted (any case): password, password_confirmation, token, access_token, refresh_token, id_token, api_key, apikey, key, secret, client_secret, signature, code, auth |
| `NIGHTWATCH_CAPTURE_REQUEST_PAYLOAD` | `false` | Keep the body of 500 responses (JSON, up to 64 KB, redacted before it leaves the middleware) |
| `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` | `_token,password,password_confirmation` | Fields blanked in a captured body, at any depth |
| `NIGHTWATCH_CAPTURE_EXCEPTION_SOURCE_CODE` | `true` | Add the 5 lines around each frame for the first 10 application frames whose file can be read (never the standard library or `/pkg/mod/`); `null` otherwise |

Sender behaviour: batch at 500 records or 5 s, gzip, `Idempotency-Key` per batch (reused on retry), retry on
network errors, 429 and 5xx with backoff (at least `Retry-After`, 5 minutes at most), at most `MaxRetries` (5)
retries or `MaxRetryTime` (2 min) per batch and then the batch is dropped and counted so one stuck batch cannot block
the next, drop other 4xx with one log line, pause 15 minutes on 402, at most `MaxBuffered` (5000) records in a ring
buffer (oldest dropped and counted; 0 means the default, never unbounded). `Enqueue` never blocks and is O(records).
On `Shutdown` a failing batch gets one final attempt and is then dropped; when the context ends first the rest is
dropped and counted; `Enqueue` after `Shutdown` is dropped and counted. `Client.Stats()` reports pending, dropped and
the translator counters.

Translator bounds (config fields, defaults in brackets): `RootGrace` (1 s) a finished execution waits for late logs
and spans before it is sent; `TraceMaxWait` (10 s) for a root that never ends; `MaxTraces` (2000), `MaxSpansPerTrace`
(2000), `MaxOwners` (50000). Over a cap the oldest trace is sent as an orphan or the extra span is dropped, always
counted in `Client.Stats().Translator`. A child that still arrives after the grace period is sent alone and counted as
a late child (the counters on its execution then miss it). Records are built and sent outside the translator's lock.

## Ignoring paths

`NIGHTWATCH_IGNORE_PATHS=/build/**,/favicon.ico` keeps static assets out of Omni. Rules:

- A pattern is matched against the whole path, case sensitive. The path is the one the router routes on: the
  request's decoded `URL.Path`, which never contains the query string.
- `*` matches any characters except `/` (one path segment). `**` matches any characters including `/` (any depth).
  Everything else matches itself.
  - `/build/*` covers `/build/app.js` but not `/build/assets/app.js`; `/build/**` covers both.
  - Neither covers `/build` itself. `/favicon.ico` covers only `/favicon.ico` (not `/favicon.ico/`).
  - `/*.png` covers `/logo.png` only; use `**.png` for every png at any depth.
- Nothing is cut or cleaned. A `?` or `#` in the path is a normal character: `/favicon.ico%3Fanything` decodes to
  `/favicon.ico?anything` and is not the favicon, so it is recorded. A path with a `.` or `..` segment or an empty
  segment (`//`) is never ignored, whatever the patterns say.
- The middleware and `IgnoreFilter` use the same function, so they always agree.

## What never reaches Omni

Records are built from the attributes the application sets on spans and log lines, and from nothing else:

- `context.Context` values are never read or copied. Go cannot list them, and no code here asks for one by key.
- Nothing is printed with `%v` or `%+v`. A panic value that is not an error or a string is reported by its type
  only (`panic: struct { ... }`), so a panic with a request or a user inside cannot leak it.
- Log lines: the attributes `request`, `response`, `user`, `owner`, `trace`, `headers`, `cookies`, `ctx`, `context`
  and `root` are dropped (Goravel's debug log attaches the request context under these names). Any key that looks
  secret (contains password, secret, token, authorization, cookie, api_key, credential, session, or is in the redact
  lists) has its value replaced with `[redacted]`, at any depth. Strings are cut at 2 KB, nesting at 4 levels and the
  whole context at 32 KB.
- Exceptions carry the error message, the Go type, the stack and source lines. An error message is whatever the
  application wrote into it; do not put secrets into error text.
- Headers, URL query values and captured payloads are redacted (see the environment table).

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
| `command` | span with `nightwatch.record=command` (`StartCommand`; every `artisan` run, including built-in commands such as `schedule:list`, flushed before the process exits) | yes |
| `scheduled-task` | span with `nightwatch.record=scheduled-task` (`StartScheduledTask`; registered `Schedule().Call` and `Schedule().Command` events, the latter also yields a `command` record) | yes |
| `cache-event` | span with `nightwatch.record=cache-event` (`StartCache`, `WithContext(ctx)` caches). Only what Laravel records: `hit`/`miss` for Get, Has and the read inside Pull and Remember; `write`/`write-failure` for Put and Forever (and the write inside Remember); `delete`/`delete-failure` for Forget (and the one inside Pull, which always forgets). `Add`, `Increment`, `Decrement` and `Lock` record nothing, like Laravel's `add()` on a store that has one, so a set-if-missing on an existing key never shows as a failed write | child |
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

## Differences from the PHP package

- `NIGHTWATCH_REDACT_HEADERS` adds names to the always-redacted six; PHP replaces its list. Redacted values are
  `[redacted]`; PHP writes `[N bytes redacted]` and keeps the auth scheme and cookie names.
- URL query secrets are redacted: values of password, token, access_token, api_key, signature, code and similar
  parameters (see `NIGHTWATCH_REDACT_QUERY`) become `[redacted]` in the request `url` and in outgoing request
  URLs, at capture time and again in the translator. The other parameters are kept and the query is re-encoded
  with sorted keys; a query that cannot be parsed is dropped. PHP sends the full URL unredacted.
- `NIGHTWATCH_INGEST_URL` (Go only) skips `POST /api/agent-auth`; the PHP package has no such setting.
- `execution_stage` is always `action` for children (PHP names the middleware, render and other stages); the
  request's `bootstrap`, `render`, `sending` and `terminating` are 0 and `action` is the whole time.
- Sampling is decided from the trace id, so a request and the jobs it queued agree; PHP rolls dice per execution.
- Exceptions: each reported error counts, like PHP. The one skipped event is Goravel's own echo of an error this
  client already recorded with a stack (same span, same message, echo has no stack), once per original.
- A stack-less exception (for example one Goravel's middleware recorded) uses the span name as file and groups by
  class, span and message, so distinct errors do not collapse. Use `ReportError` for a real stack.
- Exception `class` is the Go type (`*errors.errorString`); `*fmt.wrapError` is unwrapped; panics are class `panic`.
- Payload capture reads only JSON bodies up to 64 KB and keeps them for 500 responses only.
- Job attempts: the attempt number is counted per job id in one worker, because Goravel passes it neither to `Handle`
  nor anywhere a wrapper can see before the run (the worker's `tries` is a local variable; it only reaches
  `ShouldRetry`, after the run). A job queued with the sync driver is not a job attempt (Nightwatch records none).
- Mail `mailer` is `smtp` (the only transport Goravel's mailer has) unless you pass a name to `nwgoravel.Mail`.

## Known gaps

- Query `file` and `line` are empty (Goravel's database spans do not carry the caller).
- `route_action` is empty unless you set `nightwatch.route.action` on the span.
- `Cache().Lock`, `Add`, `Increment` and `Decrement` are not recorded (as in Laravel). Cache calls without `WithContext(ctx)` are not recorded.
- Queue dispatch and mail need `nwgoravel.Queue(ctx, ...)` and `nwgoravel.Mail(ctx, ...)`; see above for why.
- A worker without the provider would hand the trailing envelope string to `Handle`.
- Goravel's mailer always sends SMTP AUTH, which a plain Mailpit refuses (the example's test brings a fake SMTP).
- Goravel's own HTTP client did not emit spans in the example even with `enable_telemetry`; use `nwgoravel.Transport`.
- The scheduler and queue worker run callbacks with no context, so work inside a plain `Schedule().Call` closure is
  not linked to its task; jobs get a context through `ContextJob.HandleContext`.
