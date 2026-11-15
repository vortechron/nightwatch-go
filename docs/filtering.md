# Filtering

## Introduction

As in `laravel/nightwatch`, data passes four gates before it leaves the process:

1. **Sampling** decides whether an execution (a request, command, scheduled task or job attempt) is kept, with everything in it.
2. **Filtering** drops single records from a kept execution, by type, by callback or inline.
3. **Redaction** changes records before they are sent, to hide personal data and secrets.
4. **Rate limiting** can refuse a whole batch just before it is sent.

The callbacks below live on the client. Register them once at boot, for example in a service provider of your app. `nwgoravel.ClientFrom(app)` returns the client the Nightwatch provider bound:

```go
func (receiver *AppServiceProvider) Boot(app foundation.Application) {
	client := nwgoravel.ClientFrom(app)
	if client == nil {
		return
	}

	client.RejectQueries(func(query nightwatch.Query) bool {
		return strings.Contains(query.SQL, "health_checks")
	})
}
```

A callback may run on any goroutine and must not block. A callback that panics never takes the application down: the panic goes to the [unrecoverable error handler](exceptions#unrecoverable-errors), and the record is not sent, so a failed redaction cannot leak the data it was meant to hide.

## Sampling

Sampling applies to the entry points: requests, commands and scheduled tasks. When an entry point is sampled in, its whole trace is kept. When it is sampled out, nothing of it is sent. Every rate defaults to `1.0`, which keeps everything.

```ini
NIGHTWATCH_EXCEPTION_SAMPLE_RATE=1.0
NIGHTWATCH_COMMAND_SAMPLE_RATE=1.0
NIGHTWATCH_REQUEST_SAMPLE_RATE=0.1
```

| Variable | Applies to |
|---|---|
| `NIGHTWATCH_REQUEST_SAMPLE_RATE` | Requests |
| `NIGHTWATCH_COMMAND_SAMPLE_RATE` | Commands, and jobs that no request started |
| `NIGHTWATCH_SCHEDULED_TASK_SAMPLE_RATE` | Scheduled tasks |
| `NIGHTWATCH_EXCEPTION_SAMPLE_RATE` | The chance that a sampled-out execution with an unhandled exception is kept anyway |

The decision comes from the trace id, so a request and the jobs it queued always agree. PHP rolls the dice per execution.

### Dynamic sampling

`nightwatch.Sample(ctx, rate)` sets the rate of the execution `ctx` belongs to, like `Nightwatch::sample()`. The last call wins, and it beats the configured rate. `nightwatch.DontSample(ctx)` is `Sample(ctx, 0)`. An unhandled exception still samples the execution again with `NIGHTWATCH_EXCEPTION_SAMPLE_RATE`. A rate below 0 or above 1 means 0, as in Laravel.

```go
func (m *SampleAdmins) Handle(ctx http.Context) {
	if id, err := facades.Auth(ctx).ID(); err == nil && isAdmin(id) {
		nightwatch.Sample(ctx.Context(), 1)
	}

	ctx.Request().Next()
}
```

A route middleware like this one must run after the Nightwatch middleware, which is global. The same call works in a job's `HandleContext`, a command or a scheduled callback.

### Route-based sampling

`nwgoravel.Sample(rate)` is a route middleware, the Go form of `Laravel\Nightwatch\Http\Middleware\Sample`. `nwgoravel.SampleAlways()` and `nwgoravel.SampleNever()` are `Sample(1)` and `Sample(0)`. Put it on a route or a route group:

```go
facades.Route().Middleware(nwgoravel.Sample(0.5)).Get("/users", users.Index)

facades.Route().Middleware(nwgoravel.SampleNever()).Group(func(router route.Router) {
	router.Get("/up", health.Show)
})
```

Package routes (an admin panel's own routes, for example) take the middleware the same way, through the group the package mounts its routes in.

### Unmatched routes

Goravel's fallback route takes no middleware, so sample bot traffic from the fallback handler itself. As the Laravel docs advise, use a low rate rather than dropping all of it:

```go
facades.Route().Fallback(func(ctx http.Context) http.Response {
	nightwatch.Sample(ctx.Context(), 0.1)

	return ctx.Response().String(http.StatusNotFound, "not found")
})
```

### Vendor commands

Laravel does not sample a list of framework and package commands, such as `schedule:list` and `model:prune`. `nightwatch.DefaultVendorCommands()` returns that list. nightwatch-go records them unless you turn Laravel's rule on, so upgrading changes nothing:

```go
client.CaptureDefaultVendorCommands(false)
```

The rule also covers scheduled tasks that run one of these commands.

## Filtering

### Filtering records

Each record type has a reject callback. Return `true` to drop the record. A dropped record is not sent and not counted on its execution, as in Laravel.

| Record | Method | Page |
|---|---|---|
| Queries | `RejectQueries(func(nightwatch.Query) bool)` | [Queries](queries#filtering) |
| Cache events | `RejectCacheEvents`, `RejectCacheKeys(keys...)` | [Cache](cache#filtering) |
| Mail | `RejectMail(func(nightwatch.Mail) bool)` | [Mail](mail#filtering) |
| Notifications | `RejectNotifications(func(nightwatch.Notification) bool)` | [Notifications](notifications#filtering) |
| Outgoing requests | `RejectOutgoingRequests(func(nightwatch.OutgoingRequest) bool)` | [Outgoing Requests](outgoing-requests#filtering) |
| Queued jobs | `RejectQueuedJobs(func(nightwatch.QueuedJob) bool)` | [Jobs](jobs#filtering) |

### Filtering record types

These variables drop every record of one type. The dropped records are not counted on their execution either.

```ini
NIGHTWATCH_IGNORE_QUERIES=true
NIGHTWATCH_IGNORE_OUTGOING_REQUESTS=true
NIGHTWATCH_IGNORE_CACHE_EVENTS=true
NIGHTWATCH_IGNORE_MAIL=true
NIGHTWATCH_IGNORE_NOTIFICATIONS=true
```

`NIGHTWATCH_LOG_LEVEL` sets the lowest log level that is sent. See [Logs](logs).

### Inline filtering

`nightwatch.Ignore(ctx, fn)` runs `fn` with a paused context, like `Nightwatch::ignore()`. The queries, cache events, outgoing requests, mail, notifications and queued jobs started from that context are not recorded, and the attempts of a job queued from it are not sampled. Logs and exceptions are still recorded, as in Laravel.

```go
var users []User

nightwatch.Ignore(ctx, func(ctx context.Context) {
	_ = facades.Orm().WithContext(ctx).Query().Find(&users)
	_ = nwgoravel.Queue(ctx, facades.Queue()).Job(&jobs.SyncUsers{}).Dispatch()
})
```

Go has no global state, so `nightwatch.Pause` and `nightwatch.Resume` return a new context instead of switching a flag. `nightwatch.Paused(ctx)` tells whether a context is paused.

```go
paused := nightwatch.Pause(ctx)
_ = facades.Orm().WithContext(paused).Query().Find(&users)

ctx = nightwatch.Resume(paused)
```

### Ignoring paths

`NIGHTWATCH_IGNORE_PATHS` lists request path globs that are not recorded at all: no request record, none of its queries, logs or exceptions, and nothing in the counters. Add more in code with `nwgoravel.WithIgnorePaths` on the middleware:

```ini
NIGHTWATCH_IGNORE_PATHS=/build/**,/favicon.ico
```

```go
nwgoravel.WithIgnorePaths("/build/**", "/up")
```

The rules:

- A pattern is matched against the whole path, case sensitive. The path is the decoded `URL.Path` the router routes on, which never holds the query string.
- `*` matches any characters except `/`, so it stays inside one segment. `**` matches any characters including `/`, at any depth.
- `/build/*` covers `/build/app.js` but not `/build/assets/app.js`. `/build/**` covers both. Neither covers `/build` itself.
- `/*.png` covers `/logo.png` only. Use `**.png` for every PNG at any depth.
- Nothing is cut or cleaned. `/favicon.ico%3Fanything` decodes to `/favicon.ico?anything`, which is not the favicon, so it is recorded. A path with a `.` or `..` segment, or an empty segment (`//`), is never ignored.

`nightwatch.PathIgnored(patterns, path)` runs the same match.

### Ignoring scheduled tasks

`NIGHTWATCH_IGNORE_SCHEDULED_TASKS` lists task names that are not recorded, with everything that runs inside them. See [Scheduled Tasks](scheduled-tasks).

## Redaction

### Redaction callbacks

A redaction callback gets a pointer to the record and may change some of its fields, the same fields Laravel lets a callback change. Changes to the other fields are ignored. The built-in redaction below runs again on the result, so a callback cannot put a secret back in.

| Record | Method | Fields a callback may change | Page |
|---|---|---|---|
| Requests | `RedactRequests(func(*nightwatch.Request))` | `URL`, `IP`, `Headers`, `Payload` | [Requests](requests#redaction) |
| Queries | `RedactQueries(func(*nightwatch.Query))` | `SQL` | [Queries](queries#redaction) |
| Cache events | `RedactCacheEvents(func(*nightwatch.CacheEvent))` | `Key` | [Cache](cache#redaction) |
| Commands | `RedactCommands(func(*nightwatch.Command))` | `Command` | [Commands](commands#redaction) |
| Mail | `RedactMail(func(*nightwatch.Mail))` | `Subject` | [Mail](mail#redaction) |
| Outgoing requests | `RedactOutgoingRequests(func(*nightwatch.OutgoingRequest))` | `URL` | [Outgoing Requests](outgoing-requests#redaction) |
| Exceptions | `RedactExceptions(func(*nightwatch.Exception))` | `Message` | [Exceptions](exceptions#redaction) |

Like Laravel, the group of a query or a cache event is hashed from the redacted SQL or key.

### Built-in redaction

- **Headers.** Authorization, Cookie, Proxy-Authorization, Set-Cookie, X-CSRF-TOKEN and X-XSRF-TOKEN are always redacted. `NIGHTWATCH_REDACT_HEADERS` adds more names. Values become `[redacted]`, at capture time and again before sending.
- **URL query values.** The values of password, password_confirmation, token, access_token, refresh_token, id_token, api_key, apikey, key, secret, client_secret, signature, code and auth become `[redacted]`, in any case. `NIGHTWATCH_REDACT_QUERY` adds names. The other parameters are kept, the keys are sorted, and a query that cannot be parsed is dropped.
- **Request payloads.** When payload capture is on, `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` (default `_token,password,password_confirmation`) blanks those fields at any depth.
- **Log context.** The attributes `request`, `response`, `user`, `owner`, `trace`, `headers`, `cookies`, `ctx`, `context` and `root` are dropped. A key that looks secret has its value replaced with `[redacted]`. See [Logs](logs).
- **Execution context.** A key that looks secret has its value replaced with `[redacted]`. See [Context](context).

The helpers `nightwatch.RedactURL`, `nightwatch.RedactQuery`, `nightwatch.RedactHeadersJSON` and `nightwatch.RedactPayloadJSON` apply the same rules.

## Rate limiting

`client.OnIngesting` registers a listener that runs before each batch is sent, the Go form of Laravel's `IngestingEvents` event. When a listener returns `false`, the batch is dropped and counted in `client.Stats().Refused`. `EventCount()` counts the records in the batch, without user records. Combined with a counter in a shared cache, this caps ingestion for one app, one environment or several apps. This listener limits the app to 1,000 events a day:

```go
client.OnIngesting(func(event nightwatch.IngestingEvents) bool {
	facades.Cache().Add("nightwatch-events", 0, 24*time.Hour) // starts the day at 0
	total, err := facades.Cache().Increment("nightwatch-events", int64(event.EventCount()))

	return err == nil && total <= 1_000
})
```

> [!NOTE]
> The cache calls the listener makes are not recorded: it runs in the sender, outside any execution.

The listener gets a copy of the records: changing them changes nothing that is sent. A listener that panics drops the batch, and the panic goes to the [unrecoverable error handler](exceptions#unrecoverable-errors).

## What never reaches Omni

Records are built from the attributes the application sets on spans and log lines, and from nothing else.

- `context.Context` values are never read or copied.
- Nothing is printed with `%v` or `%+v`. A panic value that is not an error or a string is reported by its type only, so a panic with a request or a user inside cannot leak it.
- An exception carries the error message, the Go type, the stack and source lines. The message is whatever the application wrote, so do not put secrets in error text, or redact them with `RedactExceptions`.
