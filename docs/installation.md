# Installation

## Requirements

nightwatch-go needs Go 1.26 or later. The module builds with toolchain go1.27.4, and `GOTOOLCHAIN=auto` downloads it when your Go is older. The Goravel adapter targets Goravel v1.18.

The root package `nightwatch` does not import Goravel. The subpackage `github.com/vortechron/nightwatch-go/goravel` does.

## Installing the package

```shell
go get github.com/vortechron/nightwatch-go@latest
```

## Goravel

### Register the service provider

Add the provider **last** in `bootstrap/providers.go`, after the framework providers it wraps:

```go
&telemetry.ServiceProvider{},
// Last: it points telemetry at Nightwatch and wraps the services above.
&nwgoravel.NightwatchServiceProvider{},
```

The provider binds the client in Goravel's container (key `nwgoravel.BindingClient`), points Goravel telemetry at Nightwatch, starts the sender, wraps the cache, the scheduler, console commands and queue jobs, and flushes on shutdown. There is no package-level client. Read it with `nwgoravel.ClientFrom(app)`, which returns nil when the provider is not registered.

To pass a config you built yourself, set the `Config` field. That config replaces every other source, so it must hold the token:

```go
cfg := nightwatch.ConfigFromEnv(os.Getenv, hostname)
cfg.RequestSampleRate = 0.1

return &nwgoravel.NightwatchServiceProvider{Config: &cfg}
```

`nightwatch.ConfigFromEnv` reads the process environment only, not Goravel's `.env`.

### Add the HTTP middleware

Append Goravel's telemetry middleware first and the Nightwatch middleware second. This comes from `bootstrap/app.go` of the example app:

```go
WithMiddleware(func(m configuration.Middleware) {
	// Goravel's telemetry middleware opens the request span; the
	// Nightwatch middleware adds path, headers, sizes and the user.
	m.Append(telemetryhttp.Telemetry(telemetryhttp.WithFilter(nwgoravel.IgnoreFilter(facades.App))), nwgoravel.LazyMiddleware(facades.App, nwgoravel.WithUserResolver(resolveUser)))
}).
```

`LazyMiddleware` takes a function that returns the app, because `WithMiddleware` runs before the app exists. `IgnoreFilter` lets every request open its span, so an [ignored path](filtering#ignoring-paths) is dropped as a whole execution.

### Send logs

Logs reach Nightwatch through Goravel's `otel` log channel. Add it to your log stack in `config/logging.go`:

```go
"stack": map[string]any{
	"driver":   "stack",
	"channels": []string{"single", "otel"},
},
```

```go
"otel": map[string]any{
	"driver":          "otel",
	"instrument_name": config.GetString("APP_NAME", "goravel/log"),
},
```

### Configure the environment

Set your app token and your Omni host in `.env` or in the process environment:

```ini
NIGHTWATCH_TOKEN=your-app-token
NIGHTWATCH_BASE_URL=https://omni.example.com
```

The provider reads every `NIGHTWATCH_*` setting, and `LOG_LEVEL`, through Goravel's config, which knows `.env`. It then falls back to the process environment. Fields in a `Config` you pass win over both. Without a token the client is disabled. [Environment Variables](environment-variables) lists every setting.

> [!WARNING]
> Goravel allows one telemetry exporter per signal. The provider panics at boot with `nwgoravel.ErrExporterConflict` when `telemetry.traces.exporter` or `telemetry.logs.exporter` already names another exporter. Leave `OTEL_TRACES_EXPORTER` and `OTEL_LOGS_EXPORTER` empty.

The provider also makes the span and log batch processors export at least every 500 ms, so children that end just after their request still count.

### Run the provider on every worker

Every process that runs queued jobs must register the provider too. The trace travels as a trailing job argument, which the wrapped job removes before your `Handle` runs. See [Jobs](jobs).

## A complete example

The repository has a full Goravel app in `example/goravel`, with an end-to-end test that sends every record type to a fake Omni.
