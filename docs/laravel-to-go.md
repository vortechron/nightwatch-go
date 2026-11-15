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

## Recording

| laravel/nightwatch | nightwatch-go |
|---|---|
| queries, recorded automatically | `facades.Orm().WithContext(ctx)` |
| cache events, recorded automatically | `facades.Cache().WithContext(ctx)` |
| logs, recorded automatically | `facades.Log().WithContext(ctx)` |
| `Job::dispatch(...)` | `nwgoravel.Queue(ctx, facades.Queue()).Job(...).Dispatch()` |
| a job's `handle()` | `HandleContext(ctx, args...)` (`nwgoravel.ContextJob`) |
| `Mail::to(...)->send(...)` | `nwgoravel.Mail(ctx, facades.Mail()).To(...).Send()` |
| `Http::get(...)` | `&http.Client{Transport: nwgoravel.Transport(nil)}` with `http.NewRequestWithContext` |
| a notification | `nightwatch.StartNotification(ctx, ...)` |
| a scheduled closure | `nwgoravel.TaskContext(facades.Schedule(), name)` inside the callback |
| `Nightwatch::report($e)` | `nightwatch.ReportError(ctx, err)` or `nightwatch.ReportHandled(ctx, err)` |
| `Nightwatch::user(fn ($user) => [...])` | `nwgoravel.WithUserResolver(...)` on the middleware |
| setting the user by hand | `nightwatch.SetUser(ctx, nightwatch.User{...})` |

## Filtering

| laravel/nightwatch | nightwatch-go |
|---|---|
| `NIGHTWATCH_*_SAMPLE_RATE` | the same variables |
| `NIGHTWATCH_IGNORE_*` | the same variables |
| `Nightwatch::rejectQueries`, `rejectCacheKeys` and the other reject callbacks | none yet, see [Differences](differences) |
| ignoring a route | `NIGHTWATCH_IGNORE_PATHS` or `nwgoravel.WithIgnorePaths(...)` |
| ignoring a scheduled task | `NIGHTWATCH_IGNORE_SCHEDULED_TASKS` |
| `NIGHTWATCH_REDACT_HEADERS`, `NIGHTWATCH_REDACT_PAYLOAD_FIELDS` | the same variables, plus `NIGHTWATCH_REDACT_QUERY` |
