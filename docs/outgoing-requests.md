# Outgoing Requests

## Introduction

`nwgoravel.Transport` wraps an HTTP transport so every outgoing request becomes an `outgoing-request` record of the execution in the request's context. Use it on a plain `*http.Client` and build the request with `http.NewRequestWithContext`:

```go
req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
if err != nil {
	return nil, err
}

return (&http.Client{Transport: nwgoravel.Transport(nil)}).Do(req)
```

`Transport(nil)` wraps `http.DefaultTransport`. Pass your own transport to wrap that instead.

## Goravel's HTTP client

Goravel's own client (`facades.Http()`) is recorded too when its client has `enable_telemetry` in `config/http.go`, and the call gets the execution's context:

```go
resp, err := facades.Http().WithContext(ctx.Context()).Get(url)
if err != nil {
	return ctx.Response().Json(http.StatusBadGateway, http.Json{"error": err.Error()})
}

body, err := resp.Body()
```

> [!WARNING]
> Goravel's client uses OpenTelemetry's `otelhttp`, which ends the span only when the response body is read or closed. Goravel's response does neither on its own, so a call whose body you never read (only `resp.Status()`, for example) is never recorded. Read the body, or use `nwgoravel.Transport`, which ends the span when the response arrives.

## Filtering

`RejectOutgoingRequests` drops the outgoing requests for which the callback returns `true`, like `Nightwatch::rejectOutgoingRequests()`:

```go
client.RejectOutgoingRequests(func(request nightwatch.OutgoingRequest) bool {
	return strings.Contains(request.URL, "metrics.internal")
})
```

`NIGHTWATCH_IGNORE_OUTGOING_REQUESTS=true` drops every outgoing request record.

## Redaction

Secret query values in the URL become `[redacted]`, at capture time and again in the translator. See [Redaction](filtering#redaction). The callbacks see the URL after that redaction.

`RedactOutgoingRequests` lets you change the URL before it is sent, like `Nightwatch::redactOutgoingRequests()`. The query of the result is redacted again, and the host stays the real one:

```go
client.RedactOutgoingRequests(func(request *nightwatch.OutgoingRequest) {
	request.URL = strings.ReplaceAll(request.URL, "secret", "***")
})
```
