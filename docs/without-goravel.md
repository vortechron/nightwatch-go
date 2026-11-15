# Without Goravel

## Introduction

The root package `nightwatch` does not import Goravel. Any Go program can send Nightwatch records by registering the client's exporters with OpenTelemetry and using the instrumentation helpers.

## Wiring the client

`nightwatch.NewFromEnv` reads the `NIGHTWATCH_*` variables from the process environment. `Start` launches the sender. Register `SpanExporter` and `LogExporter` with your OpenTelemetry providers, and set the tracer provider globally, because the helpers use `otel.GetTracerProvider()`:

```go
client := nightwatch.NewFromEnv()
client.Start()

tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(client.SpanExporter()))
otel.SetTracerProvider(tp)
lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(client.LogExporter())))

defer func() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = tp.Shutdown(ctx)
	_ = lp.Shutdown(ctx)
	_ = client.Shutdown(ctx)
}()
```

Shut the OpenTelemetry providers down first, so they hand their last spans to the client, then shut the client down. `Shutdown` is safe to call twice. To change settings, build the config yourself:

```go
hostname, _ := os.Hostname()
cfg := nightwatch.ConfigFromEnv(os.Getenv, hostname)
cfg.RequestSampleRate = 0.1
cfg.IgnorePaths = append(cfg.IgnorePaths, "/build/**", "/favicon.ico")

return nightwatch.New(cfg)
```

A request record needs a server span with the standard `http.request.method` attribute, plus `http.route`, `url.path` and `http.response.status_code`. A query record needs a client span with `db.query.text`.

## Instrumentation helpers

Each helper starts a span and returns a context and an end function. Pass the context to the work inside, then call the end function.

```go
ctx, end := nightwatch.StartCommand(ctx, nightwatch.CommandInfo{Name: "reports:send", Line: "reports:send --daily"})
err := sendReports(ctx)
exitCode := 0
if err != nil {
	exitCode = 1
}
end(exitCode, err)
```

```go
ctx, end := nightwatch.StartScheduledTask(ctx, nightwatch.TaskInfo{Name: "prune", Cron: "0 * * * *", Timezone: "UTC"})
err := prune(ctx)
if err != nil {
	end(nightwatch.TaskFailed, err)
	return
}
end(nightwatch.TaskProcessed, nil)
```

To carry a trace into a queued job, start a dispatch span, encode the envelope and send it with the job. The worker decodes it and starts the attempt:

```go
ctx, end := nightwatch.StartJobDispatch(ctx, nightwatch.JobInfo{Name: "SendInvoice", Connection: "redis", Queue: "default"})
envelope := nightwatch.EncodeJobEnvelope(ctx, "redis", "default", false)
end(nil)
```

```go
env, _ := nightwatch.DecodeJobEnvelope(envelope)
ctx, end := nightwatch.StartJobAttempt(ctx, env, nightwatch.JobInfo{Name: "SendInvoice", Connection: "redis", Queue: "default"}, 1)
end(handle(ctx))
```

An empty envelope is fine: the attempt then starts its own trace.

The child helpers work the same way:

```go
_, end := nightwatch.StartCache(ctx, "redis", "user:7")
end(nightwatch.CacheHit, 0, nil)
```

```go
_, end := nightwatch.StartMail(ctx, nightwatch.MailInfo{Mailer: "smtp", Class: "WelcomeMail", Subject: "Welcome", To: 1})
end(nil)
```

The cache event types are `nightwatch.CacheHit`, `CacheMiss`, `CacheWrite`, `CacheWriteFailure`, `CacheDelete` and `CacheDeleteFailure`. The task statuses are `nightwatch.TaskProcessed`, `TaskFailed` and `TaskSkipped`.

## Your own spans

To make a record from a span you start yourself, set `nightwatch.AttrRecord` to one of the record types and fill the matching `nightwatch.Attr*` attributes. The helpers above are the reference for which attributes each record reads.

## Statistics

`client.Stats()` returns the records still pending, the records dropped and the translator's counters (`TracesEvicted`, `SpansDropped`, `OwnersEvicted`, `LateChildren`).

```go
s := client.Stats()

return s.Pending, s.Dropped
```
