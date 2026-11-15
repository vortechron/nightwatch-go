# Laravel to Go

## Setup

| laravel/nightwatch | nightwatch-go |
|---|---|
| `composer require laravel/nightwatch` | `go get github.com/vortechron/nightwatch-go@latest` |
| `php artisan nightwatch:agent` | nothing: the client sends to Omni itself |
| automatic package discovery | `&nwgoravel.NightwatchServiceProvider{}` last in `bootstrap/providers.go` |
| automatic request monitoring | `telemetryhttp.Telemetry(...)` plus `nwgoravel.LazyMiddleware(facades.App)` |
| the `nightwatch` log channel | Goravel's `otel` log channel |
| `NIGHTWATCH_*` variables | the same names, see [Environment Variables](environment-variables) |
| the `Nightwatch` facade | the client: `nwgoravel.ClientFrom(app)` |

## Recording

| laravel/nightwatch | nightwatch-go |
|---|---|
| queries, recorded automatically | `facades.Orm().WithContext(ctx)` |
| the query file and line | filled automatically (`nightwatch.QueryCallerProcessor`) |
| the route name and action | filled automatically from Goravel's route info |
| cache events, recorded automatically | `facades.Cache().WithContext(ctx)` |
| logs, recorded automatically | `facades.Log().WithContext(ctx)` |
| `Job::dispatch(...)` | `nwgoravel.Queue(ctx, facades.Queue()).Job(...).Dispatch()` |
| a job's `handle()` | `HandleContext(ctx, args...)` (`nwgoravel.ContextJob`) |
| `Mail::to(...)->send(...)` | `nwgoravel.Mail(ctx, facades.Mail()).To(...).Send()` |
| `Http::get(...)` | `facades.Http().WithContext(ctx).Get(...)` (read the body), or `&http.Client{Transport: nwgoravel.Transport(nil)}` |
| a notification | `nightwatch.StartNotification(ctx, ...)` |
| a scheduled closure | `nwgoravel.TaskContext(facades.Schedule(), name)` inside the callback |
| `Nightwatch::report($e)` | `nightwatch.ReportError(ctx, err)` or `nightwatch.ReportHandled(ctx, err)` |
| `Nightwatch::user(fn ($user) => [...])` | `nwgoravel.WithUserResolver(...)` on the middleware |
| setting the user by hand | `nightwatch.SetUser(ctx, nightwatch.User{...})` |
| `Context::add('key', $value)` | `nightwatch.AddContext(ctx, "key", value)` |
| `Nightwatch::handleUnrecoverableExceptionsUsing(fn)` | `client.HandleUnrecoverableErrorsUsing(fn)` |

## Sampling

| laravel/nightwatch | nightwatch-go |
|---|---|
| `NIGHTWATCH_*_SAMPLE_RATE` | the same variables |
| `Nightwatch::sample($rate)` | `nightwatch.Sample(ctx, rate)` |
| `Nightwatch::dontSample()` | `nightwatch.DontSample(ctx)` |
| `->middleware(Sample::rate(0.5))` | `.Middleware(nwgoravel.Sample(0.5))` |
| `Sample::always()`, `Sample::never()` | `nwgoravel.SampleAlways()`, `nwgoravel.SampleNever()` |
| `Route::fallback(...)->middleware(Sample::rate(0.5))` | `nightwatch.Sample(ctx.Context(), 0.5)` in the `Fallback` handler |
| `Schedule::command(...)->tap(Sample::rate(0.5))` | `client.SampleScheduledTask(name, 0.5)` |
| `Queue::before(fn () => Nightwatch::sample(0.5))` | `nightwatch.Sample(ctx, 0.5)` in each job's `HandleContext` |
| `Nightwatch::captureDefaultVendorCommands()` | `client.CaptureDefaultVendorCommands(true)` (the default here) |
| `Nightwatch::defaultVendorCommands()` | `nightwatch.DefaultVendorCommands()` |

## Filtering

| laravel/nightwatch | nightwatch-go |
|---|---|
| `NIGHTWATCH_IGNORE_*` | the same variables |
| `Nightwatch::rejectQueries(fn)` | `client.RejectQueries(func(nightwatch.Query) bool)` |
| `Nightwatch::rejectCacheEvents(fn)` | `client.RejectCacheEvents(func(nightwatch.CacheEvent) bool)` |
| `Nightwatch::rejectCacheKeys([...])` | `client.RejectCacheKeys(keys...)` |
| `Nightwatch::captureDefaultVendorCacheKeys()` | `client.CaptureDefaultVendorCacheKeys(true)` (the default here) |
| `Nightwatch::defaultVendorCacheKeys()` | `nightwatch.DefaultVendorCacheKeys()` |
| `Nightwatch::rejectMail(fn)` | `client.RejectMail(func(nightwatch.Mail) bool)` |
| `Nightwatch::rejectNotifications(fn)` | `client.RejectNotifications(func(nightwatch.Notification) bool)` |
| `Nightwatch::rejectOutgoingRequests(fn)` | `client.RejectOutgoingRequests(func(nightwatch.OutgoingRequest) bool)` |
| `Nightwatch::rejectQueuedJobs(fn)` | `client.RejectQueuedJobs(func(nightwatch.QueuedJob) bool)` |
| `Nightwatch::ignore(fn)` | `nightwatch.Ignore(ctx, func(ctx context.Context) {...})` |
| `Nightwatch::pause()`, `resume()`, `paused()` | `ctx = nightwatch.Pause(ctx)`, `ctx = nightwatch.Resume(ctx)`, `nightwatch.Paused(ctx)` |
| ignoring a route | `NIGHTWATCH_IGNORE_PATHS` or `nwgoravel.WithIgnorePaths(...)` |
| ignoring a scheduled task | `NIGHTWATCH_IGNORE_SCHEDULED_TASKS` |
| the `IngestingEvents` event | `client.OnIngesting(func(nightwatch.IngestingEvents) bool)` |

## Redaction

| laravel/nightwatch | nightwatch-go |
|---|---|
| `Nightwatch::redactRequests(fn)` | `client.RedactRequests(func(*nightwatch.Request))` |
| `Nightwatch::redactQueries(fn)` | `client.RedactQueries(func(*nightwatch.Query))` |
| `Nightwatch::redactCommands(fn)` | `client.RedactCommands(func(*nightwatch.Command))` |
| `Nightwatch::redactCacheEvents(fn)` | `client.RedactCacheEvents(func(*nightwatch.CacheEvent))` |
| `Nightwatch::redactMail(fn)` | `client.RedactMail(func(*nightwatch.Mail))` |
| `Nightwatch::redactOutgoingRequests(fn)` | `client.RedactOutgoingRequests(func(*nightwatch.OutgoingRequest))` |
| `Nightwatch::redactExceptions(fn)` | `client.RedactExceptions(func(*nightwatch.Exception))` |
| `NIGHTWATCH_REDACT_HEADERS`, `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` | the same variables, plus `NIGHTWATCH_REDACT_QUERY` |
