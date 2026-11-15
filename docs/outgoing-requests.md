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

> [!NOTE]
> Goravel's own HTTP client did not emit spans in the example app, even with `enable_telemetry` set for the client in `config/http.go`. Use `nwgoravel.Transport`.

## Redaction

Secret query values in the URL become `[redacted]`, at capture time and again in the translator. See [Redaction](filtering#redaction).

## Filtering

`NIGHTWATCH_IGNORE_OUTGOING_REQUESTS=true` drops every outgoing request record.
